// Package logger configures the application-wide structured logger.
// It wraps log/slog and sets the global default logger via slog.SetDefault,
// so all packages can call slog.Info/Debug/Warn/Error directly without
// threading a logger through every function.
//
// Call Setup once in main before any other initialisation.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

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
