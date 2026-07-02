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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	"github.com/kondanta/kansou/internal/anilist"
	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
	"github.com/kondanta/kansou/internal/store"
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

// configSnapshot holds the config and engine as an atomic pair
type configSnapshot struct {
	cfg    *config.Config
	engine *scoring.Engine
}

// Server holds the dependencies for the REST server.
type Server struct {
	snapshot    atomic.Value
	liveConfig  bool
	configPath  string
	corsOrigins []string
	al          *anilist.Client
	store       store.Store
	// dbType is "sqlite", "postgres", or "" (DBless). Read once at startup
	// from KANSOU_DB_TYPE — the server never re-reads the env var per request.
	dbType string
	router *chi.Mux
}

// New constructs a Server wired with the provided dependencies.
// corsOrigins is the list of CORS allowed origins; store and dbType are the
// zero value ("", nil) in DBless mode.
func New(
	cfg *config.Config, al *anilist.Client, eng *scoring.Engine, liveConfig bool,
	configPath string, st store.Store, dbType string, corsOrigins []string,
) *Server {
	s := &Server{
		al:          al,
		liveConfig:  liveConfig,
		configPath:  configPath,
		corsOrigins: corsOrigins,
		store:       st,
		dbType:      dbType,
	}
	s.snapshot.Store(&configSnapshot{cfg: cfg, engine: eng})
	s.router = s.buildRouter()
	return s
}

func (s *Server) getSnapshot() *configSnapshot {
	return s.snapshot.Load().(*configSnapshot)
}

// buildRouter registers all routes and middleware.
func (s *Server) buildRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)
	r.Use(corsMiddleware(s.corsOrigins))
	r.Use(requestLogger)

	// UI — served at root. Prefers the built Vue app; falls back to the
	// legacy single-file UI when dist hasn't been built yet.
	r.Handle("/*", spaHandler(kansouweb.DistDirFS))

	// Health check stays at root — outside /api — for load balancer/orchestrator
	// probes that expect a fixed, unprefixed path.
	r.Get("/health", s.handleHealth)

	r.Route("/api", func(r chi.Router) {
		r.Get("/db-info", s.handleDBInfo)
		r.Get("/dimensions", s.handleDimensions)
		r.Get("/genres", s.handleGenres)
		r.Get("/stats", s.handleStatsSummary)
		r.Get("/stats/genres", s.handleStatsGenres)
		r.Get("/stats/dimensions", s.handleStatsDimensions)
		r.Get("/stats/history", s.handleStatsHistory)
		r.Get("/history", s.handleHistoryList)
		r.Get("/history/{anilist_id}", s.handleHistoryDetail)
		r.Delete("/history/{score_id}", s.handleHistoryDelete)
		r.With(httprate.LimitByIP(rateLimitSearch, time.Minute)).Get("/media/search", s.handleMediaSearch)
		r.With(httprate.LimitByIP(rateLimitFetch, time.Minute)).Get("/media/{id}", s.handleMediaFetch)
		r.With(httprate.LimitByIP(rateLimitScore, time.Minute)).Post("/score", s.handleScore)
		r.With(httprate.LimitByIP(rateLimitPublish, time.Minute)).Post("/score/publish", s.handleScorePublish)
		r.Post("/weights", s.handleWeights)

		if s.dbType != "" || s.liveConfig {
			r.Get("/config", s.handleGetConfig)
			r.Post("/config", s.handlePostConfig)
		}
	})

	// Swagger UI — stays at root, not versioned under /api.
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
		_ = idx.Close()
		http.ServeFileFS(w, r, distFS, "index.html")
	}
}

// ListenAndServe starts the HTTP server on the given port.
// Port resolution (--port flag > KANSOU_PORT env var > 8080) is handled by
// the caller (cmd/serve.go). It handles SIGINT and SIGTERM with a graceful
// shutdown, waiting up to 10 seconds for in-flight requests to complete.
func (s *Server) ListenAndServe(port int) error {
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
