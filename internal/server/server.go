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
	"path"
	"strings"
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

// clientIPKey resolves the rate-limit bucket key from the client IP
// established by the ClientIPFromXFFTrustedProxies middleware (see
// buildRouter), rather than r.RemoteAddr directly. Behind the Envoy gateway,
// r.RemoteAddr is Envoy's own address, so keying off it would bucket every
// client behind the gateway together.
func clientIPKey(r *http.Request) (string, error) {
	return httprate.CanonicalizeIP(middleware.GetClientIP(r.Context())), nil
}

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
	// trustProxy reports whether the client IP used for rate limiting should
	// be resolved from X-Forwarded-For (behind a reverse proxy/gateway) or
	// from the raw TCP peer address (direct-exposed). Read once at startup
	// from TRUST_PROXY.
	trustProxy bool
	router     *chi.Mux
}

// New constructs a Server wired with the provided dependencies.
// corsOrigins is the list of CORS allowed origins; store and dbType are the
// zero value ("", nil) in DBless mode. trustProxy selects how the client IP
// used for rate limiting is resolved — see the Server.trustProxy field doc.
func New(
	cfg *config.Config, al *anilist.Client, eng *scoring.Engine, liveConfig bool,
	configPath string, st store.Store, dbType string, corsOrigins []string, trustProxy bool,
) *Server {
	s := &Server{
		al:          al,
		liveConfig:  liveConfig,
		configPath:  configPath,
		corsOrigins: corsOrigins,
		store:       st,
		dbType:      dbType,
		trustProxy:  trustProxy,
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

	if s.trustProxy {
		// One trusted hop: a fronting reverse proxy/gateway (e.g. Envoy)
		// appends itself to X-Forwarded-For. TRUST_PROXY=true opts in.
		r.Use(middleware.ClientIPFromXFFTrustedProxies(1))
	} else {
		// Direct-exposed: the TCP peer is the real client (e.g. bare `docker run`).
		r.Use(middleware.ClientIPFromRemoteAddr)
	}

	// Health check stays at root — outside /api/v1 — for load balancer/orchestrator
	// probes that expect a fixed, unprefixed path.
	r.Get("/health", s.handleHealth)

	r.Route("/api/v1", func(r chi.Router) {
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
		r.Post("/history/{score_id}/promote", s.handleHistoryPromote)
		r.With(httprate.LimitBy(rateLimitSearch, time.Minute, clientIPKey)).Get("/media/search", s.handleMediaSearch)
		r.With(httprate.LimitBy(rateLimitFetch, time.Minute, clientIPKey)).Get("/media/{id}", s.handleMediaFetch)
		r.With(httprate.LimitBy(rateLimitScore, time.Minute, clientIPKey)).Post("/score", s.handleScore)
		r.With(httprate.LimitBy(rateLimitPublish, time.Minute, clientIPKey)).Post("/score/publish", s.handleScorePublish)
		r.Post("/weights", s.handleWeights)

		if s.dbType != "" || s.liveConfig {
			r.Get("/config", s.handleGetConfig)
			r.Post("/config", s.handlePostConfig)
		}
	})

	// Swagger UI — stays at root, not versioned under /api/v1.
	r.Get("/swagger/*", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
	))

	// UI — served at root. Prefers the built Vue app; falls back to the
	// legacy single-file UI when dist hasn't been built yet.
	r.Handle("/*", spaHandler(kansouweb.DistDirFS))

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
		reqPath := r.URL.Path
		if len(reqPath) > 0 && reqPath[0] == '/' {
			reqPath = reqPath[1:]
		}
		if reqPath == "" {
			reqPath = "index.html"
		}

		_, err := distFS.Open(reqPath)
		if err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}

		// API calls should not end up in spaHandler, at all
		// and we should return 404 accordingly if it ever
		// happens.
		// Such as: /undefined/api/v1/<actual endpoint>
		ext := path.Ext(r.URL.Path)
		isApiRequest := strings.Contains(r.URL.Path, "/api")
		isMissingAsset := ext != "" && ext != ".html"

		if isApiRequest || isMissingAsset {
			http.NotFound(w, r)
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
