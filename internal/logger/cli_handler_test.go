package logger

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// emit logs one record through h and returns what was written.
func emit(
	t *testing.T,
	buf *bytes.Buffer,
	h slog.Handler,
	level slog.Level,
	msg string,
	attrs ...any,
) string {
	t.Helper()
	buf.Reset()
	r := slog.NewRecord(time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC), level, msg, 0)
	r.Add(attrs...)
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	return buf.String()
}

func TestCLIHandler_PlainFormat(t *testing.T) {
	tests := []struct {
		name  string
		level slog.Level
		msg   string
		attrs []any
		want  string
	}{
		{"info no attrs", slog.LevelInfo, "hello", nil, " INFO hello\n"},
		{"warn", slog.LevelWarn, "careful", nil, " WARN careful\n"},
		{"error", slog.LevelError, "boom", nil, "ERROR boom\n"},
		{"debug has timestamp", slog.LevelDebug, "trace", nil, "15:04:05 DEBUG trace\n"},
		{"level between info and warn is INFO", slog.LevelInfo + 1, "x", nil, " INFO x\n"},
		{"above error is ERROR", slog.LevelError + 4, "x", nil, "ERROR x\n"},
		{"attrs", slog.LevelInfo, "m", []any{"a", 1, "b", "two"}, " INFO m a=1 b=two\n"},
		{
			"string with space is quoted",
			slog.LevelInfo,
			"m",
			[]any{"q", "a b"},
			` INFO m q="a b"` + "\n",
		},
		{
			"string with quote is quoted",
			slog.LevelInfo,
			"m",
			[]any{"q", `a"b`},
			` INFO m q="a\"b"` + "\n",
		},
		{
			"error value with space is quoted",
			slog.LevelError,
			"m",
			[]any{"err", errors.New("dial tcp: refused")},
			`ERROR m err="dial tcp: refused"` + "\n",
		},
		{
			"error value without space is bare",
			slog.LevelError,
			"m",
			[]any{"err", errors.New("eof")},
			"ERROR m err=eof\n",
		},
		{"duration", slog.LevelInfo, "m", []any{"d", 2 * time.Second}, " INFO m d=2s\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := &cliHandler{w: &buf, level: slog.LevelDebug}
			if got := emit(t, &buf, h, tt.level, tt.msg, tt.attrs...); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCLIHandler_Colour(t *testing.T) {
	var buf bytes.Buffer
	h := &cliHandler{w: &buf, level: slog.LevelDebug, colour: true}

	got := emit(t, &buf, h, slog.LevelInfo, "hi", "k", "v")
	want := ansiGreen + ansiBold + " INFO" + ansiReset + " " +
		ansiBold + "hi" + ansiReset + " " +
		ansiDim + "k=v" + ansiReset + "\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	got = emit(t, &buf, h, slog.LevelDebug, "d")
	if !strings.HasPrefix(got, ansiDim+"15:04:05"+ansiReset+" ") {
		t.Fatalf("debug timestamp not dimmed: %q", got)
	}
}

func TestCLIHandler_Enabled(t *testing.T) {
	h := &cliHandler{level: slog.LevelWarn}
	tests := []struct {
		level slog.Level
		want  bool
	}{
		{slog.LevelDebug, false},
		{slog.LevelInfo, false},
		{slog.LevelWarn, true},
		{slog.LevelError, true},
	}
	for _, tt := range tests {
		if got := h.Enabled(context.Background(), tt.level); got != tt.want {
			t.Errorf("Enabled(%v) = %v, want %v", tt.level, got, tt.want)
		}
	}
}

func TestCLIHandler_WithAttrsAndGroups(t *testing.T) {
	var buf bytes.Buffer
	base := &cliHandler{w: &buf, level: slog.LevelInfo}

	withA := base.WithAttrs([]slog.Attr{slog.String("a", "1")})
	withAB := withA.WithAttrs([]slog.Attr{slog.String("b", "2")})

	if got := emit(t, &buf, withAB, slog.LevelInfo, "m", "c", "3"); got != " INFO m a=1 b=2 c=3\n" {
		t.Fatalf("attrs not merged in order: %q", got)
	}
	// Derived handlers must not mutate their parents.
	if got := emit(t, &buf, withA, slog.LevelInfo, "m"); got != " INFO m a=1\n" {
		t.Fatalf("parent mutated by child WithAttrs: %q", got)
	}
	if got := emit(t, &buf, base, slog.LevelInfo, "m"); got != " INFO m\n" {
		t.Fatalf("base mutated: %q", got)
	}

	grouped := withA.WithGroup("g").WithGroup("h")
	if got := emit(
		t,
		&buf,
		grouped,
		slog.LevelInfo,
		"m",
		"k",
		"v",
	); got != " INFO m a=1 g.h.k=v\n" {
		t.Fatalf("group prefix wrong: %q", got)
	}
	// Attributes added after a group are qualified by it.
	afterGroup := withA.WithGroup("g").WithAttrs([]slog.Attr{slog.String("b", "2")})
	if got := emit(
		t,
		&buf,
		afterGroup,
		slog.LevelInfo,
		"m",
		"k",
		"v",
	); got != " INFO m a=1 g.b=2 g.k=v\n" {
		t.Fatalf("attr added after WithGroup must be qualified: %q", got)
	}
	if got := emit(t, &buf, withA.WithGroup("g"), slog.LevelInfo, "m"); got != " INFO m a=1\n" {
		t.Fatalf("single group wrong: %q", got)
	}
}

func TestCLIHandler_WithMethodsKeepConfig(t *testing.T) {
	base := &cliHandler{level: slog.LevelWarn, colour: true}
	for name, h := range map[string]*cliHandler{
		"WithAttrs": base.WithAttrs([]slog.Attr{slog.Int("a", 1)}).(*cliHandler),
		"WithGroup": base.WithGroup("g").(*cliHandler),
	} {
		if h.level != slog.LevelWarn || !h.colour {
			t.Errorf("%s dropped level/colour: %+v", name, h)
		}
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestCLIHandler_PropagatesWriteError(t *testing.T) {
	h := &cliHandler{w: failWriter{}, level: slog.LevelInfo}
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "m", 0)
	if err := h.Handle(context.Background(), r); err == nil {
		t.Fatal("expected write error to be returned")
	}
}

// lazyValuer checks that attributes are resolved before formatting.
type lazyValuer struct{}

func (lazyValuer) LogValue() slog.Value { return slog.StringValue("resolved") }

func TestCLIHandler_ResolvesLogValuer(t *testing.T) {
	var buf bytes.Buffer
	h := &cliHandler{w: &buf, level: slog.LevelInfo}
	if got := emit(
		t,
		&buf,
		h,
		slog.LevelInfo,
		"m",
		"k",
		lazyValuer{},
	); got != " INFO m k=resolved\n" {
		t.Fatalf("got %q", got)
	}
}

func TestIsTerminal(t *testing.T) {
	regular, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = regular.Close() }()

	devNull, err := os.Open(os.DevNull) // a character device, like a tty
	if err != nil {
		t.Skipf("no %s: %v", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()

	closed, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close() // Stat on a closed file fails

	tests := []struct {
		name string
		w    any
		want bool
	}{
		{"bytes.Buffer", &bytes.Buffer{}, false},
		{"regular file", regular, false},
		{"character device", devNull, true},
		{"closed file", closed, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, ok := tt.w.(interface{ Write([]byte) (int, error) })
			if !ok {
				t.Fatal("not a writer")
			}
			if got := isTerminal(w); got != tt.want {
				t.Fatalf("isTerminal = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewCLIHandler_ColourDecision(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("no %s: %v", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()

	t.Setenv("NO_COLOR", "")
	if h := newCLIHandler(devNull, slog.LevelInfo); !h.colour {
		t.Error("terminal with NO_COLOR unset should be coloured")
	}
	t.Setenv("NO_COLOR", "1")
	if h := newCLIHandler(devNull, slog.LevelInfo); h.colour {
		t.Error("NO_COLOR must disable colour")
	}
	t.Setenv("NO_COLOR", "")
	if h := newCLIHandler(&bytes.Buffer{}, slog.LevelInfo); h.colour {
		t.Error("non-terminal writer must not be coloured")
	}
}
