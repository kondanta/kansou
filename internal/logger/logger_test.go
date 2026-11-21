package logger

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestFromContext_ReturnsStoredLogger(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, nil)).With("request_id", "abc123")

	ctx := WithContext(context.Background(), l)
	got := FromContext(ctx)

	got.Info("test message")
	if !bytes.Contains(buf.Bytes(), []byte("request_id=abc123")) {
		t.Fatalf("expected logged output to contain request_id=abc123, got: %s", buf.String())
	}
}

func TestFromContext_FallsBackToDefault(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{"nil context", nil},
		{"context without logger", context.Background()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FromContext(tt.ctx)
			if got != slog.Default() {
				t.Fatalf("expected slog.Default(), got a different logger instance")
			}
		})
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{" Warn ", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"info", slog.LevelInfo},
		{"", slog.LevelInfo},
		{"bogus", slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := parseLevel(tt.in); got != tt.want {
				t.Fatalf("parseLevel(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// captureSetup runs Setup with the given env, logs one record at every
// level, and returns what reached stderr. It restores stderr and the
// default logger afterwards.
func captureSetup(t *testing.T, isServer bool, level, appEnv string) string {
	t.Helper()
	t.Setenv("LOG_LEVEL", level)
	t.Setenv("APP_ENV", appEnv)
	t.Setenv("NO_COLOR", "1")

	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	// slog.SetDefault also redirects the standard log package, and restoring
	// the old slog logger does not undo that, so save and restore it too.
	origErr, origLogger := os.Stderr, slog.Default()
	origLogWriter, origLogFlags := log.Writer(), log.Flags()
	os.Stderr = f
	defer func() {
		os.Stderr = origErr
		slog.SetDefault(origLogger)
		log.SetOutput(origLogWriter)
		log.SetFlags(origLogFlags)
	}()

	Setup(isServer)
	slog.Debug("dbg-msg")
	slog.Info("inf-msg", "k", "v")
	slog.Warn("wrn-msg")
	slog.Error("err-msg")

	out, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestSetup_Handlers(t *testing.T) {
	t.Run("server prod emits JSON", func(t *testing.T) {
		out := captureSetup(t, true, "", "")
		if !strings.Contains(out, `"msg":"inf-msg"`) || !strings.Contains(out, `"k":"v"`) {
			t.Fatalf("expected JSON lines, got %q", out)
		}
		if strings.Contains(out, "dbg-msg") {
			t.Fatalf("debug must be filtered at default level: %q", out)
		}
		if strings.Contains(out, `"source"`) {
			t.Fatalf("source only at debug level: %q", out)
		}
	})
	t.Run("server debug adds source", func(t *testing.T) {
		out := captureSetup(t, true, "debug", "production")
		if !strings.Contains(out, "dbg-msg") || !strings.Contains(out, `"source"`) {
			t.Fatalf("expected debug record with source, got %q", out)
		}
	})
	for _, env := range []string{"dev", "development", "local", " DEV "} {
		t.Run("server APP_ENV="+env+" is text not JSON", func(t *testing.T) {
			out := captureSetup(t, true, "", env)
			if !strings.Contains(out, "inf-msg") || strings.Contains(out, `"msg"`) {
				t.Fatalf("expected tint text output, got %q", out)
			}
		})
	}
	t.Run("cli uses the CLI handler", func(t *testing.T) {
		out := captureSetup(t, false, "", "")
		want := " INFO inf-msg k=v\n WARN wrn-msg\nERROR err-msg\n"
		if out != want {
			t.Fatalf("got %q, want %q", out, want)
		}
	})
	t.Run("cli level warn filters info", func(t *testing.T) {
		out := captureSetup(t, false, "warn", "")
		if out != " WARN wrn-msg\nERROR err-msg\n" {
			t.Fatalf("got %q", out)
		}
	})
	t.Run("cli debug shows timestamped debug", func(t *testing.T) {
		out := captureSetup(t, false, "debug", "")
		if !strings.Contains(out, "DEBUG dbg-msg") {
			t.Fatalf("got %q", out)
		}
	})
}
