// Package logger configures the application-wide structured logger.
// It wraps log/slog and sets the global default logger via slog.SetDefault,
// so all packages can call slog.Info/Debug/Warn/Error directly without
// threading a logger through every function.
//
// Call Setup once in main before any other initialisation.
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// ctxKey is an unexported type to avoid collisions with context keys
// defined in other packages.
type ctxKey struct{}

// WithContext returns a copy of ctx carrying l. Retrieve it later with
// FromContext. This lets a request-scoped logger (e.g. one with a
// request_id attached) flow through call chains without threading a
// *slog.Logger parameter through every function signature.
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the logger previously stored in ctx by WithContext.
// If ctx is nil or carries no logger, it returns slog.Default().
func FromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// Setup configures the global slog default logger.
// isServer selects the handler: JSON (stderr) for server mode,
// a coloured text handler for CLI mode.
// The log level is read from the LOG_LEVEL environment variable.
// Valid values: debug, info, warn, error (case-insensitive). Default: info.
func Setup(isServer bool) {
	level := parseLevel(os.Getenv("LOG_LEVEL"))

	var handler slog.Handler
	if isServer {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level:     level,
			AddSource: level == slog.LevelDebug,
		})
	} else {
		handler = newCLIHandler(os.Stderr, level)
	}

	slog.SetDefault(slog.New(handler))
}

// parseLevel converts a LOG_LEVEL string to a slog.Level.
// Unrecognised values default to LevelInfo.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
