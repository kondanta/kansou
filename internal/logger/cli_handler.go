package logger

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// ANSI colour codes. Only applied when the output is a TTY and NO_COLOR is unset.
const (
	ansiReset  = "\033[0m"
	ansiDim    = "\033[2m"
	ansiBold   = "\033[1m"
	ansiCyan   = "\033[36m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
)

// cliHandler is a slog.Handler that writes human-readable, optionally
// coloured log lines to an io.Writer.
//
// Format at INFO/WARN/ERROR (no timestamp — reduces noise in interactive use):
//
//	 INFO  message                    key=value key2=value2
//	 WARN  something worth noting
//	ERROR  something went wrong       err="dial tcp: connection refused"
//
// Format at DEBUG (timestamp added for tracing):
//
//	15:04:05 DEBUG  anilist: search   query=Frieren
type cliHandler struct {
	w      io.Writer
	level  slog.Level
	colour bool
	mu     sync.Mutex
	attrs  []slog.Attr
	groups []string
}

// newCLIHandler returns a cliHandler writing to w at the given minimum level.
// Colour is enabled when w is a terminal and NO_COLOR is not set.
func newCLIHandler(w io.Writer, level slog.Level) *cliHandler {
	colour := isTerminal(w) && os.Getenv("NO_COLOR") == ""
	return &cliHandler{w: w, level: level, colour: colour}
}

// Enabled reports whether the handler will emit records at the given level.
func (h *cliHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

// Handle formats and writes a single log record.
func (h *cliHandler) Handle(_ context.Context, r slog.Record) error {
	var buf bytes.Buffer

	// Timestamp — only at DEBUG level.
	if r.Level == slog.LevelDebug {
		ts := r.Time.Format("15:04:05")
		if h.colour {
			fmt.Fprintf(&buf, "%s%s%s ", ansiDim, ts, ansiReset)
		} else {
			fmt.Fprintf(&buf, "%s ", ts)
		}
	}

	// Level badge — fixed 5-char width.
	buf.WriteString(h.levelBadge(r.Level))
	buf.WriteByte(' ')

	// Message.
	if h.colour {
		buf.WriteString(ansiBold)
	}
	buf.WriteString(r.Message)
	if h.colour {
		buf.WriteString(ansiReset)
	}

	// Attributes from WithAttrs: already qualified with the groups that were
	// active when they were added.
	for _, a := range h.attrs {
		buf.WriteByte(' ')
		h.writeAttr(&buf, a, "")
	}

	// Attributes from the record itself take the full current group prefix.
	prefix := h.groupPrefix()
	r.Attrs(func(a slog.Attr) bool {
		buf.WriteByte(' ')
		h.writeAttr(&buf, a, prefix)
		return true
	})

	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf.Bytes())
	return err
}

// WithAttrs returns a new handler with the given attributes pre-attached.
// Per the slog.Handler contract, each attribute is qualified by the groups
// open at this point, not by groups opened later.
func (h *cliHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	prefix := h.groupPrefix()
	merged := make([]slog.Attr, len(h.attrs), len(h.attrs)+len(attrs))
	copy(merged, h.attrs)
	for _, a := range attrs {
		a.Key = prefix + a.Key
		merged = append(merged, a)
	}
	return &cliHandler{
		w:      h.w,
		level:  h.level,
		colour: h.colour,
		attrs:  merged,
		groups: h.groups,
	}
}

// WithGroup returns a new handler with the given group name pushed onto the stack.
func (h *cliHandler) WithGroup(name string) slog.Handler {
	groups := make([]string, len(h.groups)+1)
	copy(groups, h.groups)
	groups[len(h.groups)] = name
	return &cliHandler{
		w:      h.w,
		level:  h.level,
		colour: h.colour,
		attrs:  h.attrs,
		groups: groups,
	}
}

// levelBadge returns a fixed-width level string with colour.
func (h *cliHandler) levelBadge(level slog.Level) string {
	var label, colour string
	switch {
	case level >= slog.LevelError:
		label, colour = "ERROR", ansiRed
	case level >= slog.LevelWarn:
		label, colour = " WARN", ansiYellow
	case level >= slog.LevelInfo:
		label, colour = " INFO", ansiGreen
	default:
		label, colour = "DEBUG", ansiCyan
	}
	if h.colour {
		return colour + ansiBold + label + ansiReset
	}
	return label
}

// groupPrefix returns the open groups joined as "g.h.", or "" if none.
func (h *cliHandler) groupPrefix() string {
	if len(h.groups) == 0 {
		return ""
	}
	return strings.Join(h.groups, ".") + "."
}

// writeAttr formats a single slog.Attr as prefix+key=value.
func (h *cliHandler) writeAttr(buf *bytes.Buffer, a slog.Attr, prefix string) {
	key := prefix + a.Key

	val := a.Value.Resolve()

	if h.colour {
		fmt.Fprintf(buf, "%s%s=%s%s", ansiDim, key, formatValue(val), ansiReset)
	} else {
		fmt.Fprintf(buf, "%s=%s", key, formatValue(val))
	}
}

// formatValue formats a slog.Value for inline display.
// Strings containing spaces are quoted; others are unquoted.
func formatValue(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		s := v.String()
		if strings.ContainsAny(s, " \t\n\"") {
			return fmt.Sprintf("%q", s)
		}
		return s
	case slog.KindAny:
		s := fmt.Sprintf("%v", v.Any())
		if strings.ContainsAny(s, " \t\n\"") {
			return fmt.Sprintf("%q", s)
		}
		return s
	default:
		return v.String()
	}
}

// isTerminal reports whether w is a character device (i.e. a terminal).
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
