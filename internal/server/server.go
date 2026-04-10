// Package server implements the kansou REST API server.
// It exposes the scoring engine and AniList client over HTTP via a chi router.
// All handlers return JSON. The server is stateless — no session state is held
// between requests. See ADR-001.
package server

import (
	"context"
	_ "embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	httpSwagger "github.com/swaggo/http-swagger"

	"github.com/kondanta/kansou/internal/anilist"
	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
	kansouweb "github.com/kondanta/kansou/web"
)

//go:embed web/index.html
var legacyHTML []byte

// Rate limits for AniList-proxying endpoints (requests per minute, per IP).
// These are safety rails against runaway scripts or frontend bugs — not
// intended to trip during normal use.
const (
	rateLimitSearch  = 30 // /media/search  — user-initiated, typed queries
	rateLimitFetch   = 30 // /media/{id}    — direct ID lookup
	rateLimitScore   = 20 // /score         — deliberate scoring action
	rateLimitPublish = 5  // /score/publish — write to AniList, must be intentional
)

// Server holds the dependencies for the REST server.
type Server struct {
	cfg    *config.Config
	al     *anilist.Client
	engine *scoring.Engine
	router *chi.Mux
}

// New constructs a Server wired with the provided dependencies.
func New(cfg *config.Config, al *anilist.Client, eng *scoring.Engine) *Server {
	s := &Server{
		cfg:    cfg,
		al:     al,
		engine: eng,
	}
	s.router = s.buildRouter()
	return s
}

// buildRouter registers all routes and middleware.
func (s *Server) buildRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)
	r.Use(corsMiddleware(s.cfg.Server.CORSAllowedOrigins))
	r.Use(requestLogger)

	// UI — served at root. Prefers the built Vue app; falls back to the
	// legacy single-file UI when dist hasn't been built yet.
	r.Handle("/*", spaHandler(kansouweb.DistDirFS))

	r.Get("/health", s.handleHealth)
	r.Get("/dimensions", s.handleDimensions)
	r.Get("/genres", s.handleGenres)
	r.With(httprate.LimitByIP(rateLimitSearch, time.Minute)).Get("/media/search", s.handleMediaSearch)
	r.With(httprate.LimitByIP(rateLimitFetch, time.Minute)).Get("/media/{id}", s.handleMediaFetch)
	r.With(httprate.LimitByIP(rateLimitScore, time.Minute)).Post("/score", s.handleScore)
	r.With(httprate.LimitByIP(rateLimitPublish, time.Minute)).Post("/score/publish", s.handleScorePublish)
	r.Post("/weights", s.handleWeights)

	// Swagger UI.
	r.Get("/swagger/*", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
	))

	return r
}

// spaHandler serves the Vue SPA from distFS.
// For any path where the file doesn't exist it serves dist/index.html so that
// Vue Router's client-side routing works. When dist/index.html itself is absent
// (i.e. just build-ui hasn't been run yet) it falls back to legacyHTML.
func spaHandler(distFS fs.FS) http.HandlerFunc {
	fileServer := http.FileServer(http.FS(distFS))
	return func(w http.ResponseWriter, r *http.Request) {
		// Strip leading slash for fs.FS open calls.
		path := r.URL.Path
		if len(path) > 0 && path[0] == '/' {
			path = path[1:]
		}
		if path == "" {
			path = "index.html"
		}

		_, err := distFS.Open(path)
		if err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}

		// Path not found — SPA fallback to index.html.
		idx, err := distFS.Open("index.html")
		if err != nil {
			// dist not built yet — serve legacy UI.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(legacyHTML) //nolint:errcheck
			return
		}
		idx.Close()
		http.ServeFileFS(w, r, distFS, "index.html")
	}
}

// ListenAndServe starts the HTTP server on the configured port.
// It handles SIGINT and SIGTERM with a graceful shutdown, waiting up to
// 10 seconds for in-flight requests to complete.
func (s *Server) ListenAndServe(portOverride int) error {
	port := s.cfg.Server.Port
	if portOverride > 0 {
		port = portOverride
	}

	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("kansou listening", "addr", "http://0.0.0.0"+addr)
		slog.Info("swagger available", "addr", "http://0.0.0.0"+addr+"/swagger/index.html")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case <-stop:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	}
}
