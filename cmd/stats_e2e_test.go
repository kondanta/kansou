package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
	"github.com/kondanta/kansou/internal/store/sqlite"
)

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it. cmd/stats.go prints directly to stdout (per
// CLAUDE.md's "never print from business logic" rule, printing belongs to
// the CLI layer), so this is the only way to assert on its output end-to-end.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("closing pipe: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("reading pipe: %v", err)
	}
	return buf.String()
}

// seedOneScore saves a single, minimal but realistic scoring session into
// st, exercising the exact SaveScore path that `kansou score add` uses.
func seedOneScore(t *testing.T, ctx context.Context, st *sqlite.SQLiteStore) {
	t.Helper()
	cfg := &config.Config{
		DimensionOrder: []string{"story", "characters"},
		Dimensions: map[string]config.DimensionDef{
			"story":      {Label: "Story", Weight: 0.6},
			"characters": {Label: "Characters", Weight: 0.4},
		},
		Genres: map[string]map[string]float64{},
	}
	result := scoring.Result{
		FinalScore: 8.2,
		Breakdown: []scoring.BreakdownRow{
			{
				Key: "story", Label: "Story", Score: 9.0, BaseWeight: 0.6,
				FinalWeight: 0.6, AppliedMultiplier: 1.0, Contribution: 5.4,
			},
			{
				Key: "characters", Label: "Characters", Score: 7.0, BaseWeight: 0.4,
				FinalWeight: 0.4, AppliedMultiplier: 1.0, Contribution: 2.8,
			},
		},
		Meta: scoring.SessionMeta{
			MediaID:      1,
			TitleRomaji:  "Test Show",
			MediaType:    scoring.Anime,
			AllGenres:    []string{"Action", "Drama"},
			GenresActive: []string{"action", "drama"},
		},
	}
	if err := st.SaveScore(ctx, result, cfg, 0); err != nil {
		t.Fatalf("seeding score: %v", err)
	}
}

// TestStatsCmd_EndToEnd wires the real cobra command, a real SQLite database
// on disk, and the real internal/stats package together — no fakes — and
// exercises the full path `kansou stats [category]` takes in production.
func TestStatsCmd_EndToEnd(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "kansou.db")
	st, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	seedOneScore(t, ctx, st)

	app := &App{Store: st}
	cmd := app.statsCmd()
	cmd.SetContext(ctx)

	summary := captureStdout(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatalf("stats: %v", err)
		}
	})
	if !strings.Contains(summary, "Top genre:") {
		t.Errorf("summary output missing expected header, got: %q", summary)
	}

	genres := captureStdout(t, func() {
		if err := cmd.RunE(cmd, []string{"genres"}); err != nil {
			t.Fatalf("stats genres: %v", err)
		}
	})
	if !strings.Contains(genres, "Genre breakdown:") || !strings.Contains(genres, "Action") {
		t.Errorf("genres output missing expected content, got: %q", genres)
	}

	dims := captureStdout(t, func() {
		if err := cmd.RunE(cmd, []string{"dimensions"}); err != nil {
			t.Fatalf("stats dimensions: %v", err)
		}
	})
	if !strings.Contains(dims, "Dimension variance") {
		t.Errorf("dimensions output missing expected header, got: %q", dims)
	}

	history := captureStdout(t, func() {
		if err := cmd.RunE(cmd, []string{"history"}); err != nil {
			t.Fatalf("stats history: %v", err)
		}
	})
	if !strings.Contains(history, "Test Show") {
		t.Errorf("history output missing seeded entry, got: %q", history)
	}

	if err := cmd.RunE(cmd, []string{"bogus"}); err == nil {
		t.Error("expected an error for an unknown stats category")
	}
}

// TestStatsCmd_DBless confirms the DBless error path fires before any store
// method is touched.
func TestStatsCmd_DBless(t *testing.T) {
	app := &App{Store: nil}
	cmd := app.statsCmd()
	cmd.SetContext(context.Background())

	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("expected an error in DBless mode")
	}
	if !strings.Contains(err.Error(), "KANSOU_DB_TYPE") {
		t.Errorf("error message should mention KANSOU_DB_TYPE, got: %v", err)
	}
}
