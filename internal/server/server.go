// Package server implements the kansou REST API server.
// It exposes the scoring engine and AniList client over HTTP via a chi router.
// All handlers return JSON. The server is stateless — no session state is held
// between requests. See ADR-001.
package server

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger"

	"github.com/kondanta/kansou/internal/anilist"
	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
)

//go:embed web/index.html
var indexHTML []byte

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
	r.Use(corsMiddleware(s.cfg.Server.CORSAllowedOrigins))
	r.Use(requestLogger)

	// Test UI — served at root.
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML) //nolint:errcheck
	})

	r.Get("/health", s.handleHealth)
	r.Get("/dimensions", s.handleDimensions)
	r.Get("/media/search", s.handleMediaSearch)
	r.Get("/media/{id}", s.handleMediaFetch)
	r.Post("/score", s.handleScore)
	r.Post("/score/publish", s.handleScorePublish)

	// Swagger UI.
	r.Get("/swagger/*", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
	))

	return r
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
		Addr:    addr,
		Handler: s.router,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("kansou listening", "addr", "http://localhost"+addr)
		slog.Info("swagger available", "addr", "http://localhost"+addr+"/swagger/index.html")
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
