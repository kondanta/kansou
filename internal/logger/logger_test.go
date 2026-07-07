package logger

import (
	"bytes"
	"context"
	"log/slog"
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
