package server

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/kondanta/kansou/internal/logger"
)

// corsMiddleware returns a middleware that sets CORS headers for allowed origins.
// If the request Origin header matches one of the allowed origins, the
// Access-Control-Allow-Origin header is set to that origin.
// Preflight (OPTIONS) requests are responded to directly with 204.
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[strings.ToLower(o)] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allowed[strings.ToLower(origin)] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Vary", "Origin")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// securityHeaders sets conservative security headers on every response.
// X-Content-Type-Options prevents MIME sniffing.
// X-Frame-Options prevents the UI from being embedded in an iframe (clickjacking).
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// requestLogger returns a middleware that logs each request using slog.
// It records the HTTP method, path, status code, response size, latency,
// and the chi request ID. It also attaches a request-scoped logger (with
// request_id pre-set) to the request context via logger.WithContext, so
// handlers and everything they call (anilist, store) can retrieve it with
// logger.FromContext and have their log lines correlate to this request.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		reqID := middleware.GetReqID(r.Context())
		reqLogger := slog.Default().With("request_id", reqID)
		r = r.WithContext(logger.WithContext(r.Context(), reqLogger))

		defer func() {
			reqLogger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"latency", time.Since(start).String(),
			)
		}()

		next.ServeHTTP(ww, r)
	})
}
