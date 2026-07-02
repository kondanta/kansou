package export

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
	"github.com/kondanta/kansou/internal/store/sqlite"
)

// seedExportFixture saves two scoring sessions into st via the real SaveScore
// path, giving Generate real genre/dimension/history data to render.
func seedExportFixture(t *testing.T, ctx context.Context, st *sqlite.SQLiteStore) {
	t.Helper()
	cfg := &config.Config{
		DimensionOrder: []string{"story", "characters"},
		Dimensions: map[string]config.DimensionDef{
			"story":      {Label: "Story", Weight: 0.6},
			"characters": {Label: "Characters", Weight: 0.4},
		},
		Genres: map[string]map[string]float64{},
	}
	sessions := []struct {
		mediaID    int
		title      string
		finalScore float64
		story      float64
		characters float64
		genres     []string
	}{
		{mediaID: 1, title: "Test Show A", finalScore: 8.2, story: 9.0, characters: 7.0, genres: []string{"Action", "Drama"}},
		{mediaID: 2, title: "Test Show B", finalScore: 7.0, story: 6.0, characters: 9.0, genres: []string{"Action"}},
	}
	for _, sess := range sessions {
		result := scoring.Result{
			FinalScore: sess.finalScore,
			Breakdown: []scoring.BreakdownRow{
				{
					Key: "story", Label: "Story", Score: sess.story, BaseWeight: 0.6,
					FinalWeight: 0.6, AppliedMultiplier: 1.0, Contribution: sess.story * 0.6,
				},
				{
					Key: "characters", Label: "Characters", Score: sess.characters, BaseWeight: 0.4,
					FinalWeight: 0.4, AppliedMultiplier: 1.0, Contribution: sess.characters * 0.4,
				},
			},
			Meta: scoring.SessionMeta{
				MediaID:     sess.mediaID,
				TitleRomaji: sess.title,
				MediaType:   scoring.Anime,
				AllGenres:   sess.genres,
			},
		}
		if err := st.SaveScore(ctx, result, cfg, 0); err != nil {
			t.Fatalf("seeding score for %s: %v", sess.title, err)
		}
	}
}

func TestGenerate_EndToEnd(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "kansou.db")
	st, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	seedExportFixture(t, ctx, st)

	html, err := Generate(ctx, st)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := string(html)

	if len(html) < 200_000 {
		t.Errorf("output suspiciously small (%d bytes) — the embedded Chart.js library alone is ~200KB", len(html))
	}
	if !strings.Contains(out, "<!DOCTYPE html>") {
		t.Error("output is not a valid HTML document")
	}
	if !strings.Contains(out, "Chart.defaults") {
		t.Error("Chart.js initialisation script missing")
	}
	if !strings.Contains(out, "Test Show A") || !strings.Contains(out, "Test Show B") {
		t.Errorf("output missing seeded entries")
	}
	if !strings.Contains(out, `"labels":["Action","Drama"]`) {
		t.Errorf("genre breakdown chart data missing or malformed, got: %s", excerptAround(out, "genreBreakdownChart"))
	}
	if strings.Contains(out, "<no value>") {
		t.Error("output contains an unresolved template field (<no value>)")
	}
}

func TestGenerate_EmptyDatabase(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "kansou.db")
	st, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	html, err := Generate(ctx, st)
	if err != nil {
		t.Fatalf("Generate on an empty database should not error: %v", err)
	}
	if !strings.Contains(string(html), "<!DOCTYPE html>") {
		t.Error("output is not a valid HTML document")
	}
}

// excerptAround returns a small window of s around the first occurrence of
// marker, for readable test failure output.
func excerptAround(s, marker string) string {
	i := strings.Index(s, marker)
	if i < 0 {
		return "(marker not found)"
	}
	start, end := i-80, i+80
	if start < 0 {
		start = 0
	}
	if end > len(s) {
		end = len(s)
	}
	return s[start:end]
}
