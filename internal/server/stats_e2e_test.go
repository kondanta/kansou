package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
	"github.com/kondanta/kansou/internal/store/sqlite"
)

// seedTwoScores saves two distinct scoring sessions, exercising the exact
// SaveScore path handleScore uses in production. Two entries with a
// deliberate genre-count skew (Action appears in both, Drama in only one)
// keep every aggregate stat below unambiguous — no query result depends on
// SQL's unspecified tie-breaking order for equal counts.
func seedTwoScores(t *testing.T, ctx context.Context, st *sqlite.SQLiteStore) {
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

// newDBBackedTestServer builds a Server wired to a real, on-disk SQLite
// database (not a fake) — used by the stats E2E tests below to exercise the
// full HTTP → handler → stats package → SQL path.
func newDBBackedTestServer(t *testing.T) (*Server, *sqlite.SQLiteStore) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "kansou.db")
	st, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := minimalConfig()
	s := New(cfg, nil, minimalEngine(cfg), false, "", st, "sqlite", nil, false)
	return s, st
}

// doGet issues a GET request against the server's real router and decodes
// the JSON response body into v.
func doGet(t *testing.T, s *Server, path string, v any) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	res := rec.Result()
	if v != nil {
		if err := json.NewDecoder(res.Body).Decode(v); err != nil {
			t.Fatalf("decoding response for %s: %v", path, err)
		}
	}
	return res
}

// TestStatsEndpoints_EndToEnd wires a real chi router, a real on-disk
// SQLite database, and the real internal/stats package together and drives
// every /stats* endpoint over actual HTTP, verifying the seeded data comes
// back out correctly through the full stack.
func TestStatsEndpoints_EndToEnd(t *testing.T) {
	s, st := newDBBackedTestServer(t)
	seedTwoScores(t, context.Background(), st)

	t.Run("summary", func(t *testing.T) {
		var body map[string]any
		res := doGet(t, s, "/api/stats", &body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", res.StatusCode)
		}
		// Action appears in both seeded entries, Drama in only one — the
		// top genre is unambiguous regardless of tie-breaking order.
		topGenre, _ := body["TopGenre"].(map[string]any)
		if topGenre == nil || topGenre["Genre"] != "Action" || topGenre["Count"] != float64(2) {
			t.Errorf("expected TopGenre={Action, count 2}, got %+v", topGenre)
		}
	})

	t.Run("genres", func(t *testing.T) {
		var body genreStatsResponse
		res := doGet(t, s, "/api/stats/genres", &body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", res.StatusCode)
		}
		byGenre := map[string]int{}
		for _, g := range body.GenreBreakdown {
			byGenre[g.Genre] = g.Count
		}
		if byGenre["Action"] != 2 || byGenre["Drama"] != 1 {
			t.Fatalf("genre counts: got %+v, want Action=2 Drama=1", byGenre)
		}
		avgByGenre := map[string]float64{}
		for _, g := range body.ScoreByGenre {
			avgByGenre[g.Genre] = g.AvgScore
		}
		// Action = avg(8.2, 7.0); Drama = 8.2 alone (only Show A has it).
		if got := avgByGenre["Action"]; got < 7.59 || got > 7.61 {
			t.Errorf("Action avg score: got %.4f, want ~7.6", got)
		}
		if got := avgByGenre["Drama"]; got != 8.2 {
			t.Errorf("Drama avg score: got %.4f, want 8.2", got)
		}
	})

	t.Run("dimensions", func(t *testing.T) {
		var body struct {
			DimensionVariance []struct {
				DimensionKey string
				AvgScore     float64
			}
		}
		res := doGet(t, s, "/api/stats/dimensions", &body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", res.StatusCode)
		}
		got := map[string]float64{}
		for _, v := range body.DimensionVariance {
			got[v.DimensionKey] = v.AvgScore
		}
		// story: avg(9.0, 6.0)=7.5; characters: avg(7.0, 9.0)=8.0.
		if got["story"] != 7.5 || got["characters"] != 8.0 {
			t.Errorf("got %+v, want story=7.5 characters=8.0", got)
		}
	})

	t.Run("history", func(t *testing.T) {
		var body historyStatsResponse
		res := doGet(t, s, "/api/stats/history", &body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", res.StatusCode)
		}
		titles := map[string]bool{}
		for _, r := range body.MostRescored {
			titles[r.TitleRomaji] = true
		}
		if len(body.MostRescored) != 2 || !titles["Test Show A"] || !titles["Test Show B"] {
			t.Errorf("got %+v, want both seeded entries", body.MostRescored)
		}
	})

	t.Run("db-info reports sqlite", func(t *testing.T) {
		var body map[string]any
		doGet(t, s, "/api/db-info", &body)
		if body["db"] != "sqlite" {
			t.Errorf("db-info: got %+v, want db=sqlite", body)
		}
	})
}

// TestStatsEndpoints_DBless confirms every /stats* endpoint returns the
// documented 503 envelope, and /db-info reports DBless status, when no
// store is configured — the DB-optional mode real users run in by default.
func TestStatsEndpoints_DBless(t *testing.T) {
	cfg := minimalConfig()
	s := New(cfg, nil, minimalEngine(cfg), true, "", nil, "", nil, false)

	for _, path := range []string{"/api/stats", "/api/stats/genres", "/api/stats/dimensions", "/api/stats/history"} {
		t.Run(path, func(t *testing.T) {
			var body errorResponse
			res := doGet(t, s, path, &body)
			if res.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status: got %d, want 503", res.StatusCode)
			}
			if body.Error == "" {
				t.Error("expected a non-empty error message")
			}
		})
	}

	var info map[string]any
	doGet(t, s, "/api/db-info", &info)
	if db, ok := info["db"]; !ok || db != nil {
		t.Errorf("db-info: got db=%+v, want nil in DBless mode", db)
	}
	if info["live_config"] != true {
		t.Errorf("db-info: got live_config=%+v, want true (server built with liveConfig=true)", info["live_config"])
	}
}
