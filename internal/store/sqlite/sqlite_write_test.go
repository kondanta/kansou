package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
	"github.com/kondanta/kansou/internal/store"
)

// testConfig returns a small scoring config with fractional values so any
// column swapped or dropped by a write path shows up as a wrong number.
func testConfig() *config.Config {
	return &config.Config{
		DimensionOrder: []string{"story", "visuals"},
		Dimensions: map[string]config.DimensionDef{
			"story":   {Label: "Story", Description: "Plot quality", Weight: 0.65},
			"visuals": {Label: "Visuals", Description: "Art", Weight: 0.35, BiasResistant: true},
		},
		Genres: map[string]map[string]float64{
			"action": {"story": 1.25, "visuals": 0.75},
			"drama":  {"story": 1.5},
		},
		MaxMultiplier:      1.8,
		PrimaryGenreWeight: 0.55,
		MaxHistory:         7,
	}
}

// testResult returns a scoring result exercising every persisted field: a
// scored dimension with a primary genre blend and a skipped one.
func testResult(anilistID int, final float64) scoring.Result {
	return scoring.Result{
		FinalScore: final,
		Breakdown: []scoring.BreakdownRow{
			{
				Key: "story", Label: "Story", Score: 8.75, BaseWeight: 0.65,
				AppliedMultiplier: 1.125, EffectiveWeight: 0.73125, FinalWeight: 0.8,
				Contribution: 7.0, PrimaryGenreMultiplier: 1.25,
				SecondaryGenresMultiplier: 0.875, GenreDeselected: true,
			},
			{
				Key: "visuals", Label: "Visuals", BaseWeight: 0.35,
				AppliedMultiplier: 1.0, BiasResistant: true, Skipped: true,
				WeightOverride: true,
			},
		},
		Meta: scoring.SessionMeta{
			MediaID: anilistID, TitleRomaji: "Sousou no Frieren", TitleEnglish: "Frieren",
			MediaType: scoring.Anime, Format: "TV", CoverImage: "https://img/frieren.png",
			AllGenres:          []string{"Fantasy", "Drama", "Action"},
			GenresActive:       []string{"action", "drama"},
			PrimaryGenre:       "Action",
			PrimaryGenreWeight: 0.55,
			UserSelectedGenres: []string{"Action", "Drama"},
		},
	}
}

// backdate moves every score of the media to a fixed old timestamp so a later
// SaveScore (stamped "now") is unambiguously the newest. scored_at has only
// one-second resolution, so back-to-back saves would otherwise tie.
func backdate(t *testing.T, s *SQLiteStore, n int) {
	t.Helper()
	ts := time.Date(2024, 1, n, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if _, err := s.db.Exec(
		`UPDATE scores SET scored_at = ? WHERE id = (SELECT MAX(id) FROM scores)`, ts,
	); err != nil {
		t.Fatalf("backdating score: %v", err)
	}
}

func TestSaveScore_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	cfg := testConfig()

	if err := s.SaveScore(ctx, testResult(42, 8.37), cfg, -1); err != nil {
		t.Fatalf("SaveScore: %v", err)
	}
	got, err := s.LatestScore(ctx, 42)
	if err != nil || got == nil {
		t.Fatalf("LatestScore: %v, %v", got, err)
	}

	assertScoreHeader(t, got, cfg)

	wantStory := 8.75
	wantContribution := 7.0
	wantBreakdown := map[string]store.DimensionScoreRow{
		"story": {
			DimensionKey: "story", Label: "Story", Score: &wantStory, BaseWeight: 0.65,
			FinalWeight: 0.8, AppliedMultiplier: 1.125, Contribution: &wantContribution,
			GenreDeselected: true, PrimaryGenreMultiplier: 1.25, SecondaryGenresMultiplier: 0.875,
		},
		"visuals": {
			DimensionKey: "visuals", Label: "Visuals", BaseWeight: 0.35, AppliedMultiplier: 1.0,
			Skipped: true, BiasResistant: true, WeightOverride: true,
		},
	}
	if len(got.Breakdown) != len(wantBreakdown) {
		t.Fatalf("breakdown rows: got %d, want %d", len(got.Breakdown), len(wantBreakdown))
	}
	for _, row := range got.Breakdown {
		if want := wantBreakdown[row.DimensionKey]; !reflect.DeepEqual(row, want) {
			t.Errorf("breakdown %q:\n got  %+v\n want %+v", row.DimensionKey, row, want)
		}
	}
}

// assertScoreHeader checks every non-breakdown column of the round-trip score.
func assertScoreHeader(t *testing.T, got *store.Score, cfg *config.Config) {
	t.Helper()
	if got.TitleRomaji != "Sousou no Frieren" || got.TitleEnglish != "Frieren" ||
		got.MediaType != "ANIME" || got.Format != "TV" ||
		got.CoverImage != "https://img/frieren.png" {
		t.Errorf("media columns wrong: %+v", got)
	}
	if want := []string{"Action", "Drama", "Fantasy"}; !reflect.DeepEqual(got.Genres, want) {
		t.Errorf("genres: got %v, want %v (sorted)", got.Genres, want)
	}
	if got.FinalScore != 8.37 || got.PrimaryGenre != "Action" || got.PrimaryGenreWeight != 0.55 {
		t.Errorf("score columns wrong: %+v", got)
	}
	if got.ConfigHash != config.Hash(cfg) {
		t.Errorf("config hash: got %q, want %q", got.ConfigHash, config.Hash(cfg))
	}
	if !got.IsLatest {
		t.Error("new score must be is_latest")
	}
	if want := []string{"Action", "Drama"}; !reflect.DeepEqual(got.UserSelectedGenres, want) {
		t.Errorf("user selected genres: got %v, want %v", got.UserSelectedGenres, want)
	}
	// PrimaryGenre is "Action" but GenresActive is lowercase: the primary flag
	// must still land on "action" and only there.
	primary := map[string]bool{}
	for _, g := range got.ActiveGenres {
		primary[g.Genre] = g.IsPrimary
	}
	if want := map[string]bool{"action": true, "drama": false}; !reflect.DeepEqual(primary, want) {
		t.Errorf("active genres: got %v, want %v", primary, want)
	}
}

func TestSaveScore_SnapshotStoredAsJSON(t *testing.T) {
	s := newTestStore(t)
	cfg := testConfig()
	if err := s.SaveScore(context.Background(), testResult(42, 8.37), cfg, -1); err != nil {
		t.Fatalf("SaveScore: %v", err)
	}
	var raw string
	if err := s.db.Get(&raw, `SELECT config_snapshot FROM scores`); err != nil {
		t.Fatalf("reading snapshot: %v", err)
	}
	var got store.ConfigSnapshot
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("snapshot is not valid JSON: %v", err)
	}
	if want := store.BuildConfigSnapshot(cfg); !reflect.DeepEqual(got, want) {
		t.Errorf("snapshot:\n got  %+v\n want %+v", got, want)
	}
}

func TestSaveScore_NoPrimaryNoUserGenres(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	res := testResult(42, 7.5)
	res.Meta.PrimaryGenre, res.Meta.PrimaryGenreWeight = "", 0
	res.Meta.UserSelectedGenres = nil

	if err := s.SaveScore(ctx, res, testConfig(), -1); err != nil {
		t.Fatalf("SaveScore: %v", err)
	}
	var nulls struct {
		PG  *string  `db:"primary_genre"`
		PGW *float64 `db:"primary_genre_weight"`
		USG *string  `db:"user_selected_genres"`
	}
	if err := s.db.Get(
		&nulls,
		`SELECT primary_genre, primary_genre_weight, user_selected_genres FROM scores`,
	); err != nil {
		t.Fatalf("reading columns: %v", err)
	}
	if nulls.PG != nil || nulls.PGW != nil || nulls.USG != nil {
		t.Errorf("want NULL columns, got %+v", nulls)
	}
	got, _ := s.LatestScore(ctx, 42)
	for _, g := range got.ActiveGenres {
		if g.IsPrimary {
			t.Errorf("no primary genre was set but %q is flagged primary", g.Genre)
		}
	}
}

func TestSaveScore_Rescore(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	cfg := testConfig()

	if err := s.SaveScore(ctx, testResult(42, 6.25), cfg, -1); err != nil {
		t.Fatalf("first SaveScore: %v", err)
	}
	backdate(t, s, 1)

	second := testResult(42, 9.5)
	second.Meta.TitleRomaji = "Frieren (renamed)"
	second.Meta.CoverImage = "https://img/new.png"
	second.Meta.AllGenres = []string{"Adventure"} // Fantasy/Drama/Action dropped
	if err := s.SaveScore(ctx, second, cfg, -1); err != nil {
		t.Fatalf("second SaveScore: %v", err)
	}

	var mediaRows int
	if err := s.db.Get(&mediaRows, `SELECT COUNT(*) FROM media`); err != nil || mediaRows != 1 {
		t.Fatalf("media rows: got %d (err %v), want 1 (upsert)", mediaRows, err)
	}
	hist, err := s.ScoreHistory(ctx, 42)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history: got %d rows (err %v), want 2", len(hist), err)
	}
	if hist[0].FinalScore != 9.5 || !hist[0].IsLatest {
		t.Errorf("newest: got %+v, want 9.5 and latest", hist[0])
	}
	if hist[1].FinalScore != 6.25 || hist[1].IsLatest {
		t.Errorf("previous: got %+v, want 6.25 and not latest", hist[1])
	}
	if hist[0].TitleRomaji != "Frieren (renamed)" || hist[0].CoverImage != "https://img/new.png" {
		t.Errorf("media not updated on rescore: %+v", hist[0])
	}
	if want := []string{"Adventure"}; !reflect.DeepEqual(hist[0].Genres, want) {
		t.Errorf(
			"genres after rescore: got %v, want %v (replaced, not merged)",
			hist[0].Genres,
			want,
		)
	}
}

func TestSaveScore_MaxHistory(t *testing.T) {
	tests := []struct {
		name       string
		maxHistory int
		wantKept   int
	}{
		{"keep all", -1, 4},
		{"zero keeps only latest", 0, 1},
		{"one keeps one", 1, 1},
		{"two keeps two", 2, 2},
		{"more than saved keeps all", 10, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			finals := []float64{5.25, 6.5, 7.75, 9.0}
			for i, f := range finals {
				if err := s.SaveScore(
					ctx,
					testResult(42, f),
					testConfig(),
					tt.maxHistory,
				); err != nil {
					t.Fatalf("SaveScore %d: %v", i, err)
				}
				backdate(t, s, i+1)
			}
			// The last save was backdated too; re-stamp it as the newest.
			if _, err := s.db.Exec(
				`UPDATE scores SET scored_at = '2025-01-01T00:00:00Z' WHERE final_score = 9.0`,
			); err != nil {
				t.Fatalf("re-stamping newest: %v", err)
			}

			hist, err := s.ScoreHistory(ctx, 42)
			if err != nil {
				t.Fatalf("ScoreHistory: %v", err)
			}
			if len(hist) != tt.wantKept {
				t.Fatalf("kept %d, want %d", len(hist), tt.wantKept)
			}
			// The survivors must be the newest ones.
			for i, h := range hist {
				if want := finals[len(finals)-1-i]; h.FinalScore != want {
					t.Errorf("survivor %d: got %.2f, want %.2f", i, h.FinalScore, want)
				}
			}
			assertPrunedRowsMarked(t, s, len(finals)-tt.wantKept)
		})
	}
}

// assertPrunedRowsMarked checks the soft-deleted rows carry the max_history reason.
func assertPrunedRowsMarked(t *testing.T, s *SQLiteStore, want int) {
	t.Helper()
	var n int
	if err := s.db.Get(&n,
		`SELECT COUNT(*) FROM scores WHERE deleted_at IS NOT NULL AND deleted_reason = ?`,
		store.DeletedReasonMaxHistory); err != nil {
		t.Fatalf("counting pruned rows: %v", err)
	}
	if n != want {
		t.Errorf("rows soft-deleted as max_history: got %d, want %d", n, want)
	}
}

func TestSaveScore_MaxHistoryIsPerMedia(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, id := range []int{1, 2} {
		if err := s.SaveScore(ctx, testResult(id, 7.5), testConfig(), 0); err != nil {
			t.Fatalf("SaveScore %d: %v", id, err)
		}
	}
	for _, id := range []int{1, 2} {
		if got, _ := s.LatestScore(ctx, id); got == nil {
			t.Errorf("media %d lost its score when another media was saved", id)
		}
	}
}

func TestSaveScore_RollsBackOnFailure(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	res := testResult(42, 8.0)
	// Duplicate matched genre violates the (score_id, genre) primary key
	// after media, score and dimension rows were already written.
	res.Meta.GenresActive = []string{"action", "action"}

	if err := s.SaveScore(ctx, res, testConfig(), -1); err == nil {
		t.Fatal("expected SaveScore to fail on duplicate matched genre")
	}
	for _, table := range []string{"media", "media_genres", "scores", "dimension_scores", "score_matched_genres"} {
		var n int
		if err := s.db.Get(&n, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, table)); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s has %d rows after a failed SaveScore, want 0 (rollback)", table, n)
		}
	}
}

func TestLoadScoringConfig_NotSeeded(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.LoadScoringConfig(context.Background()); !errors.Is(err, store.ErrNotSeeded) {
		t.Fatalf("got %v, want ErrNotSeeded", err)
	}
}

func TestLoadScoringConfig_ScalarRowMissing(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec(`DELETE FROM config_scalars`); err != nil {
		t.Fatalf("deleting scalars: %v", err)
	}
	if _, err := s.LoadScoringConfig(context.Background()); !errors.Is(err, store.ErrNotSeeded) {
		t.Fatalf("got %v, want ErrNotSeeded", err)
	}
}

func TestScoringConfig_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	want := testConfig()

	if err := s.SaveScoringConfig(ctx, want); err != nil {
		t.Fatalf("SaveScoringConfig: %v", err)
	}
	got, err := s.LoadScoringConfig(ctx)
	if err != nil {
		t.Fatalf("LoadScoringConfig: %v", err)
	}
	assertConfigEqual(t, got, want)
	if wantHash := config.HashDimensions(
		want.Dimensions,
		want.DimensionOrder,
	); got.DimensionsHash != wantHash {
		t.Errorf("DimensionsHash: got %q, want %q", got.DimensionsHash, wantHash)
	}
}

func assertConfigEqual(t *testing.T, got, want *config.Config) {
	t.Helper()
	if !reflect.DeepEqual(got.DimensionOrder, want.DimensionOrder) {
		t.Errorf("order: got %v, want %v", got.DimensionOrder, want.DimensionOrder)
	}
	if !reflect.DeepEqual(got.Dimensions, want.Dimensions) {
		t.Errorf("dimensions:\n got  %+v\n want %+v", got.Dimensions, want.Dimensions)
	}
	if !reflect.DeepEqual(got.Genres, want.Genres) {
		t.Errorf("genres:\n got  %+v\n want %+v", got.Genres, want.Genres)
	}
	if got.MaxMultiplier != want.MaxMultiplier ||
		got.PrimaryGenreWeight != want.PrimaryGenreWeight ||
		got.MaxHistory != want.MaxHistory {
		t.Errorf("scalars: got (%v, %v, %v), want (%v, %v, %v)",
			got.MaxMultiplier, got.PrimaryGenreWeight, got.MaxHistory,
			want.MaxMultiplier, want.PrimaryGenreWeight, want.MaxHistory)
	}
}

func TestSaveScoringConfig_ReplacesPrevious(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.SaveScoringConfig(ctx, testConfig()); err != nil {
		t.Fatalf("first save: %v", err)
	}

	next := &config.Config{
		DimensionOrder: []string{"pacing"},
		Dimensions: map[string]config.DimensionDef{
			"pacing": {Label: "Pacing", Weight: 1.0},
		},
		Genres:             map[string]map[string]float64{"comedy": {"pacing": 1.5}},
		MaxMultiplier:      1.25,
		PrimaryGenreWeight: 0.2,
		MaxHistory:         -1,
	}
	if err := s.SaveScoringConfig(ctx, next); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, err := s.LoadScoringConfig(ctx)
	if err != nil {
		t.Fatalf("LoadScoringConfig: %v", err)
	}
	assertConfigEqual(t, got, next)
}

func TestSaveScoringConfig_PreservesDimensionOrder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	cfg := testConfig()
	cfg.DimensionOrder = []string{"visuals", "story"} // reverse of alphabetical
	if err := s.SaveScoringConfig(ctx, cfg); err != nil {
		t.Fatalf("SaveScoringConfig: %v", err)
	}
	got, err := s.LoadScoringConfig(ctx)
	if err != nil {
		t.Fatalf("LoadScoringConfig: %v", err)
	}
	if !reflect.DeepEqual(got.DimensionOrder, cfg.DimensionOrder) {
		t.Errorf("order: got %v, want %v", got.DimensionOrder, cfg.DimensionOrder)
	}
}

func TestPrune(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Media 1: one live score and one soft-deleted score (media survives).
	// Media 2: only a soft-deleted score (media is orphaned and removed).
	m1 := insertMedia(t, s, 1, "Keeps", "ANIME", []string{"Action"})
	m2 := insertMedia(t, s, 2, "Orphaned", "ANIME", []string{"Drama"})
	insertScore(t, s, m1, 8.0, "h", "2024-02-01T00:00:00Z", true)
	gone1 := insertScore(t, s, m1, 6.0, "h", "2024-01-01T00:00:00Z", false)
	gone2 := insertScore(t, s, m2, 7.0, "h", "2024-01-01T00:00:00Z", false)
	for _, id := range []int{gone1, gone2} {
		if err := s.SoftDeleteScore(ctx, id); err != nil {
			t.Fatalf("SoftDeleteScore: %v", err)
		}
	}

	n, err := s.Prune(ctx)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted count: got %d, want 2", n)
	}

	var scores, media int
	_ = s.db.Get(&scores, `SELECT COUNT(*) FROM scores`)
	_ = s.db.Get(&media, `SELECT COUNT(*) FROM media`)
	if scores != 1 || media != 1 {
		t.Errorf("after prune: %d scores, %d media; want 1 and 1", scores, media)
	}
	if kept, _ := s.LatestScore(ctx, 1); kept == nil || kept.FinalScore != 8.0 {
		t.Errorf("live score lost by Prune: %+v", kept)
	}
	// FK cascade must clear the media's genres with the orphan.
	var genres int
	_ = s.db.Get(&genres, `SELECT COUNT(*) FROM media_genres WHERE media_id = ?`, m2)
	if genres != 0 {
		t.Errorf("orphaned media still has %d genre rows", genres)
	}
}

func TestPrune_NothingToDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m := insertMedia(t, s, 1, "Show", "ANIME", nil)
	insertScore(t, s, m, 8.0, "h", "2024-02-01T00:00:00Z", true)

	n, err := s.Prune(ctx)
	if err != nil || n != 0 {
		t.Fatalf("Prune: got (%d, %v), want (0, nil)", n, err)
	}
	// The timestamp is recorded even when nothing was deleted.
	if at, err := s.LastPruneAt(ctx); err != nil || at == nil {
		t.Errorf("LastPruneAt after empty prune: got (%v, %v), want a timestamp", at, err)
	}
}

func TestLastPruneAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if at, err := s.LastPruneAt(ctx); err != nil || at != nil {
		t.Fatalf("before any prune: got (%v, %v), want (nil, nil)", at, err)
	}

	before := time.Now().UTC().Add(-2 * time.Second)
	if _, err := s.Prune(ctx); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	at, err := s.LastPruneAt(ctx)
	if err != nil || at == nil {
		t.Fatalf("after prune: got (%v, %v)", at, err)
	}
	if at.Before(before) || at.After(time.Now().UTC().Add(2*time.Second)) {
		t.Errorf("last prune %v is not close to now", at)
	}
}

func TestLastPruneAt_Corrupt(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec(
		`INSERT INTO db_metadata (key, value) VALUES ('last_prune_at', 'not-a-time')`,
	); err != nil {
		t.Fatalf("seeding corrupt value: %v", err)
	}
	if _, err := s.LastPruneAt(context.Background()); err == nil {
		t.Error("expected a parse error for a corrupt timestamp")
	}
}

func TestExpandPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	tests := []struct{ name, in, want string }{
		{"tilde prefix", "~/kansou/db.sqlite", filepath.Join(home, "kansou", "db.sqlite")},
		{"absolute untouched", "/var/lib/kansou.db", "/var/lib/kansou.db"},
		{"relative untouched", "data/kansou.db", "data/kansou.db"},
		{"bare tilde untouched", "~", "~"},
		{"tilde mid-path untouched", "a/~/b", "a/~/b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expandPath(tt.in)
			if err != nil || got != tt.want {
				t.Errorf("expandPath(%q) = (%q, %v), want %q", tt.in, got, err, tt.want)
			}
		})
	}
}

// Regression: scored_at has one-second resolution, so two saves in the same
// second used to tie, and the prune subquery kept the OLDER row (soft-deleting
// the new is_latest one). Ties must now resolve to the highest id.
func TestSaveScore_BackToBackKeepsNewest(t *testing.T) {
	for _, maxHistory := range []int{0, 1} {
		t.Run(fmt.Sprintf("max_history=%d", maxHistory), func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			for _, f := range []float64{5.5, 9.5} {
				if err := s.SaveScore(
					ctx,
					testResult(42, f),
					testConfig(),
					maxHistory,
				); err != nil {
					t.Fatalf("SaveScore: %v", err)
				}
			}
			got, err := s.LatestScore(ctx, 42)
			if err != nil || got == nil || got.FinalScore != 9.5 {
				t.Fatalf("LatestScore: got (%+v, %v), want the 9.5 score", got, err)
			}
		})
	}
}

// Deterministic version of the tie: A and B share a timestamp, C is newer.
// Keeping two must keep C and B (higher id), never A.
func TestSaveScore_TieBreaksOnID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, f := range []float64{5.5, 6.5} { // A, B
		if err := s.SaveScore(ctx, testResult(42, f), testConfig(), -1); err != nil {
			t.Fatalf("SaveScore: %v", err)
		}
	}
	if _, err := s.db.Exec(`UPDATE scores SET scored_at = '2024-01-01T00:00:00Z'`); err != nil {
		t.Fatalf("forcing tie: %v", err)
	}
	if err := s.SaveScore(ctx, testResult(42, 9.5), testConfig(), 2); err != nil { // C
		t.Fatalf("SaveScore: %v", err)
	}
	hist, err := s.ScoreHistory(ctx, 42)
	if err != nil || len(hist) != 2 || hist[0].FinalScore != 9.5 || hist[1].FinalScore != 6.5 {
		t.Fatalf("history: got (%+v, %v), want [9.5, 6.5]", hist, err)
	}
}

// Rows tied on scored_at are listed newest (highest id) first.
func TestScoreHistory_TieOrdersByIDDesc(t *testing.T) {
	s := newTestStore(t)
	m := insertMedia(t, s, 1, "Show", "ANIME", nil)
	insertScore(t, s, m, 5.5, "h", "2024-01-01T00:00:00Z", false)
	insertScore(t, s, m, 6.5, "h", "2024-01-01T00:00:00Z", true)
	hist, err := s.ScoreHistory(context.Background(), 1)
	if err != nil || len(hist) != 2 || hist[0].FinalScore != 6.5 {
		t.Fatalf("history: got (%+v, %v), want 6.5 first", hist, err)
	}
}

// After PromoteScore restores an older score, that score is the current one and
// must be history[0] even though a newer (non-active) score exists (ADR-041).
func TestScoreHistory_ActiveScoreFirst(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m := insertMedia(t, s, 1, "Show", "ANIME", nil)
	a := insertScore(t, s, m, 5.5, "h", "2024-01-01T00:00:00Z", false)
	insertScore(t, s, m, 6.5, "h", "2024-02-01T00:00:00Z", false)
	insertScore(t, s, m, 9.5, "h", "2024-03-01T00:00:00Z", true) // C: current, newest

	if err := s.PromoteScore(ctx, a); err != nil { // C is demoted and soft-deleted
		t.Fatalf("PromoteScore: %v", err)
	}
	hist, err := s.ScoreHistory(ctx, 1)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history: got (%+v, %v), want 2 rows", hist, err)
	}
	if !hist[0].IsLatest || hist[0].FinalScore != 5.5 || hist[1].FinalScore != 6.5 {
		t.Errorf("order: got %.1f (latest=%v), %.1f; want promoted 5.5 first then 6.5",
			hist[0].FinalScore, hist[0].IsLatest, hist[1].FinalScore)
	}
}
