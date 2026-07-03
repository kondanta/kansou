package sqlite

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/kondanta/kansou/internal/store"
)

const floatTolerance = 1e-9

// newTestStore opens a fresh, isolated in-memory SQLite database per the
// convention in HISTORY_IMPL.md's testing requirements.
func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := New("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("opening test store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// insertMedia inserts a media row with its genres and returns the row id.
func insertMedia(t *testing.T, s *SQLiteStore, anilistID int, title, mediaType string, genres []string) int {
	t.Helper()
	res, err := s.db.Exec(
		`INSERT INTO media (anilist_id, title_romaji, title_english, media_type, format)
		 VALUES (?, ?, '', ?, 'TV')`,
		anilistID, title, mediaType,
	)
	if err != nil {
		t.Fatalf("inserting media: %v", err)
	}
	id, _ := res.LastInsertId()
	for _, g := range genres {
		if _, err := s.db.Exec(`INSERT INTO media_genres (media_id, genre) VALUES (?, ?)`, id, g); err != nil {
			t.Fatalf("inserting media genre: %v", err)
		}
	}
	return int(id)
}

// insertScore inserts a scores row and returns its id.
func insertScore(t *testing.T, s *SQLiteStore, mediaID int, finalScore float64, configHash, scoredAt string, isLatest bool) int {
	t.Helper()
	res, err := s.db.Exec(
		`INSERT INTO scores (media_id, final_score, config_hash, config_snapshot, is_latest, scored_at)
		 VALUES (?, ?, ?, '{}', ?, ?)`,
		mediaID, finalScore, configHash, boolToInt(isLatest), scoredAt,
	)
	if err != nil {
		t.Fatalf("inserting score: %v", err)
	}
	id, _ := res.LastInsertId()
	return int(id)
}

// dimFixture describes one dimension_scores row to insert. Score == nil means skipped.
type dimFixture struct {
	Key            string
	Label          string
	Score          *float64
	WeightOverride bool
}

// score is a convenience constructor for a non-nil dimension score pointer.
func score(v float64) *float64 { return new(v) }

// insertDimensionScores inserts dimension_scores rows for a score id.
func insertDimensionScores(t *testing.T, s *SQLiteStore, scoreID int, dims []dimFixture) {
	t.Helper()
	for _, d := range dims {
		var scoreVal any
		if d.Score != nil {
			scoreVal = *d.Score
		}
		_, err := s.db.Exec(
			`INSERT INTO dimension_scores
			     (score_id, dimension_key, label, score, base_weight, final_weight, applied_multiplier, skipped, weight_override)
			 VALUES (?, ?, ?, ?, 0.5, 0.5, 1.0, ?, ?)`,
			scoreID, d.Key, d.Label, scoreVal, boolToInt(d.Score == nil), boolToInt(d.WeightOverride),
		)
		if err != nil {
			t.Fatalf("inserting dimension score: %v", err)
		}
	}
}

// insertMatchedGenre inserts a score_matched_genres row.
func insertMatchedGenre(t *testing.T, s *SQLiteStore, scoreID int, genre string) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO score_matched_genres (score_id, genre, is_primary) VALUES (?, ?, 0)`, scoreID, genre,
	); err != nil {
		t.Fatalf("inserting matched genre: %v", err)
	}
}

func TestGenreBreakdown(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action", "Drama"})
	b := insertMedia(t, s, 2, "Show B", "ANIME", []string{"Drama", "Romance"})
	c := insertMedia(t, s, 3, "Show C", "MANGA", []string{"Action"})
	insertScore(t, s, a, 8.0, "h1", "2024-01-01T00:00:00Z", true)
	insertScore(t, s, b, 6.0, "h1", "2024-01-02T00:00:00Z", true)
	insertScore(t, s, c, 10.0, "h1", "2024-01-03T00:00:00Z", true)

	got, err := s.GenreBreakdown(ctx)
	if err != nil {
		t.Fatalf("GenreBreakdown: %v", err)
	}

	want := map[string]store.GenreStat{
		"Action":  {Genre: "Action", Count: 2, Percentage: 200.0 / 3},
		"Drama":   {Genre: "Drama", Count: 2, Percentage: 200.0 / 3},
		"Romance": {Genre: "Romance", Count: 1, Percentage: 100.0 / 3},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d genres, want %d: %+v", len(got), len(want), got)
	}
	for _, g := range got {
		w, ok := want[g.Genre]
		if !ok {
			t.Fatalf("unexpected genre %q in result", g.Genre)
		}
		if g.Count != w.Count || math.Abs(g.Percentage-w.Percentage) > floatTolerance {
			t.Errorf("genre %q: got %+v, want %+v", g.Genre, g, w)
		}
	}
}

func TestScoreByGenre(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action", "Drama"})
	b := insertMedia(t, s, 2, "Show B", "ANIME", []string{"Drama", "Romance"})
	c := insertMedia(t, s, 3, "Show C", "MANGA", []string{"Action"})
	insertScore(t, s, a, 8.0, "h1", "2024-01-01T00:00:00Z", true)
	insertScore(t, s, b, 6.0, "h1", "2024-01-02T00:00:00Z", true)
	insertScore(t, s, c, 10.0, "h1", "2024-01-03T00:00:00Z", true)

	got, err := s.ScoreByGenre(ctx)
	if err != nil {
		t.Fatalf("ScoreByGenre: %v", err)
	}

	want := map[string]store.GenreScore{
		"Action":  {Genre: "Action", AvgScore: 9.0, Count: 2},
		"Drama":   {Genre: "Drama", AvgScore: 7.0, Count: 2},
		"Romance": {Genre: "Romance", AvgScore: 6.0, Count: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d genres, want %d: %+v", len(got), len(want), got)
	}
	for _, g := range got {
		w := want[g.Genre]
		if g.Count != w.Count || math.Abs(g.AvgScore-w.AvgScore) > floatTolerance {
			t.Errorf("genre %q: got %+v, want %+v", g.Genre, g, w)
		}
	}
}

func TestGenreDimensionAffinity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action", "Drama"})
	b := insertMedia(t, s, 2, "Show B", "ANIME", []string{"Drama"})
	c := insertMedia(t, s, 3, "Show C", "MANGA", []string{"Action"})

	scoreA := insertScore(t, s, a, 8.0, "h1", "2024-01-01T00:00:00Z", true)
	scoreB := insertScore(t, s, b, 7.0, "h1", "2024-01-02T00:00:00Z", true)
	scoreC := insertScore(t, s, c, 8.5, "h1", "2024-01-03T00:00:00Z", true)

	insertDimensionScores(t, s, scoreA, []dimFixture{{Key: "story", Label: "Story", Score: score(9.0)}})
	insertDimensionScores(t, s, scoreB, []dimFixture{{Key: "story", Label: "Story", Score: score(7.0)}})
	insertDimensionScores(t, s, scoreC, []dimFixture{{Key: "story", Label: "Story", Score: score(8.0)}})

	insertMatchedGenre(t, s, scoreA, "Action")
	insertMatchedGenre(t, s, scoreA, "Drama")
	insertMatchedGenre(t, s, scoreB, "Drama")
	insertMatchedGenre(t, s, scoreC, "Action")

	got, err := s.GenreDimensionAffinity(ctx)
	if err != nil {
		t.Fatalf("GenreDimensionAffinity: %v", err)
	}

	wantAvg := map[string]float64{"Action": 8.5, "Drama": 8.0} // Action: (9+8)/2, Drama: (9+7)/2
	if len(got) != len(wantAvg) {
		t.Fatalf("got %d genres, want %d: %+v", len(got), len(wantAvg), got)
	}
	for _, g := range got {
		if len(g.Dimensions) != 1 {
			t.Fatalf("genre %q: got %d dimensions, want 1", g.Genre, len(g.Dimensions))
		}
		want, ok := wantAvg[g.Genre]
		if !ok {
			t.Fatalf("unexpected genre %q", g.Genre)
		}
		if math.Abs(g.Dimensions[0].AvgScore-want) > floatTolerance {
			t.Errorf("genre %q avg: got %.4f, want %.4f", g.Genre, g.Dimensions[0].AvgScore, want)
		}
	}
}

// seedDimensionVarianceFixture inserts 4 media entries with "story" scores
// 8, 7, 7, 8 — chosen so the population variance is a clean, hand-checkable
// number: avg=7.5, avg(x^2)=56.5, variance=56.5-56.25=0.25, std_dev=0.5.
func seedDimensionVarianceFixture(t *testing.T, s *SQLiteStore) {
	t.Helper()
	scores := []float64{8, 7, 7, 8}
	for i, v := range scores {
		m := insertMedia(t, s, i+1, fmt.Sprintf("Show %d", i+1), "ANIME", []string{"Action"})
		sc := insertScore(t, s, m, v, "h1", fmt.Sprintf("2024-01-0%dT00:00:00Z", i+1), true)
		insertDimensionScores(t, s, sc, []dimFixture{{Key: "story", Label: "Story", Score: score(v)}})
	}
}

func TestDimensionVariance(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedDimensionVarianceFixture(t, s)

	got, err := s.DimensionVariance(ctx)
	if err != nil {
		t.Fatalf("DimensionVariance: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d dimensions, want 1: %+v", len(got), got)
	}
	v := got[0]
	if v.DimensionKey != "story" || v.Count != 4 {
		t.Fatalf("unexpected row: %+v", v)
	}
	if math.Abs(v.AvgScore-7.5) > floatTolerance {
		t.Errorf("avg score: got %.6f, want 7.5", v.AvgScore)
	}
	if math.Abs(v.StdDev-0.5) > floatTolerance {
		t.Errorf("std dev: got %.6f, want 0.5", v.StdDev)
	}
}

func TestScoringConsistency(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedDimensionVarianceFixture(t, s)

	got, err := s.ScoringConsistency(ctx)
	if err != nil {
		t.Fatalf("ScoringConsistency: %v", err)
	}
	if got == nil {
		t.Fatal("got nil, want a ConsistencyStat")
	}
	if got.Count != 1 {
		t.Errorf("count: got %d, want 1", got.Count)
	}
	if math.Abs(got.AvgStdDev-0.5) > floatTolerance {
		t.Errorf("avg std dev: got %.6f, want 0.5", got.AvgStdDev)
	}
}

func TestScoringConsistency_NoData(t *testing.T) {
	s := newTestStore(t)
	got, err := s.ScoringConsistency(context.Background())
	if err != nil {
		t.Fatalf("ScoringConsistency: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil for empty database", got)
	}
}

func TestDimensionCorrelation_BelowThresholdExcluded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Only 3 shared entries for the pair — well under the 25 minimum.
	for i, pair := range [][2]float64{{1, 2}, {2, 4}, {3, 6}} {
		m := insertMedia(t, s, i+1, fmt.Sprintf("Show %d", i+1), "ANIME", []string{"Action"})
		sc := insertScore(t, s, m, 7.0, "h1", fmt.Sprintf("2024-01-0%dT00:00:00Z", i+1), true)
		insertDimensionScores(t, s, sc, []dimFixture{
			{Key: "story", Label: "Story", Score: score(pair[0])},
			{Key: "characters", Label: "Characters", Score: score(pair[1])},
		})
	}

	got, err := s.DimensionCorrelation(ctx)
	if err != nil {
		t.Fatalf("DimensionCorrelation: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want empty slice below the 25-entry threshold", got)
	}
}

func TestDimensionCorrelation_ComputesPearson(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 26 perfectly linearly related samples (characters = 2*story) — enough
	// to clear the 25-shared-entries threshold with an exact Pearson r of 1.0.
	const n = 26
	for i := 1; i <= n; i++ {
		story := float64(i)
		characters := 2 * story
		m := insertMedia(t, s, i, fmt.Sprintf("Show %d", i), "ANIME", []string{"Action"})
		sc := insertScore(t, s, m, story, "h1", fmt.Sprintf("2024-%02d-01T00:00:00Z", (i%12)+1), true)
		insertDimensionScores(t, s, sc, []dimFixture{
			{Key: "story", Label: "Story", Score: score(story)},
			{Key: "characters", Label: "Characters", Score: score(characters)},
		})
	}

	got, err := s.DimensionCorrelation(ctx)
	if err != nil {
		t.Fatalf("DimensionCorrelation: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d pairs, want 1: %+v", len(got), got)
	}
	if got[0].DimensionA != "characters" || got[0].DimensionB != "story" {
		t.Errorf("unexpected pair ordering: %+v", got[0])
	}
	if math.Abs(got[0].Correlation-1.0) > floatTolerance {
		t.Errorf("correlation: got %.9f, want 1.0", got[0].Correlation)
	}
}

func TestSkippedDimensions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Anime A", "ANIME", []string{"Action"})
	b := insertMedia(t, s, 2, "Anime B", "ANIME", []string{"Action"})
	c := insertMedia(t, s, 3, "Manga C", "MANGA", []string{"Action"})

	scA := insertScore(t, s, a, 8.0, "h1", "2024-01-01T00:00:00Z", true)
	scB := insertScore(t, s, b, 7.0, "h1", "2024-01-02T00:00:00Z", true)
	scC := insertScore(t, s, c, 9.0, "h1", "2024-01-03T00:00:00Z", true)

	insertDimensionScores(t, s, scA, []dimFixture{{Key: "story", Label: "Story", Score: score(8.0)}})
	insertDimensionScores(t, s, scB, []dimFixture{{Key: "story", Label: "Story", Score: nil}}) // skipped
	insertDimensionScores(t, s, scC, []dimFixture{{Key: "story", Label: "Story", Score: score(9.0)}})

	got, err := s.SkippedDimensions(ctx)
	if err != nil {
		t.Fatalf("SkippedDimensions: %v", err)
	}

	byType := make(map[string]store.SkippedDimStat)
	for _, r := range got {
		byType[r.MediaType] = r
	}
	if anime := byType["ANIME"]; anime.SkipCount != 1 || anime.TotalCount != 2 {
		t.Errorf("ANIME: got skip=%d total=%d, want skip=1 total=2", anime.SkipCount, anime.TotalCount)
	}
	if manga := byType["MANGA"]; manga.SkipCount != 0 || manga.TotalCount != 1 {
		t.Errorf("MANGA: got skip=%d total=%d, want skip=0 total=1", manga.SkipCount, manga.TotalCount)
	}
}

func TestWeightOverrides(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action"})
	b := insertMedia(t, s, 2, "Show B", "ANIME", []string{"Action"})
	scA := insertScore(t, s, a, 8.0, "h1", "2024-01-01T00:00:00Z", true)
	scB := insertScore(t, s, b, 7.0, "h1", "2024-01-02T00:00:00Z", true)

	insertDimensionScores(t, s, scA, []dimFixture{{Key: "story", Label: "Story", Score: score(8.0), WeightOverride: true}})
	insertDimensionScores(t, s, scB, []dimFixture{{Key: "story", Label: "Story", Score: score(7.0)}})

	got, err := s.WeightOverrides(ctx)
	if err != nil {
		t.Fatalf("WeightOverrides: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(got), got)
	}
	if got[0].DimensionKey != "story" || got[0].OverrideCount != 1 {
		t.Errorf("got %+v, want story with count 1", got[0])
	}
}

func TestMostRescored(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Rescored Show", "ANIME", []string{"Action"})
	b := insertMedia(t, s, 2, "Once Show", "ANIME", []string{"Action"})

	insertScore(t, s, a, 7.0, "h1", "2024-01-01T00:00:00Z", false)
	insertScore(t, s, a, 8.5, "h2", "2024-02-01T00:00:00Z", true)
	insertScore(t, s, b, 6.0, "h1", "2024-01-15T00:00:00Z", true)

	got, err := s.MostRescored(ctx)
	if err != nil {
		t.Fatalf("MostRescored: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	if got[0].AnilistID != 1 || got[0].ScoreCount != 2 {
		t.Fatalf("top row: got %+v, want anilist_id=1 score_count=2", got[0])
	}
	if math.Abs(got[0].LatestScore-8.5) > floatTolerance {
		t.Errorf("latest score: got %.4f, want 8.5", got[0].LatestScore)
	}
	if got[1].AnilistID != 2 || got[1].ScoreCount != 1 {
		t.Fatalf("second row: got %+v, want anilist_id=2 score_count=1", got[1])
	}
}

func TestOutliers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 9 entries at story=7.0 (tight cluster) plus one at story=3.0 (outlier).
	// avg = 6.6, population variance = 1.44, std_dev = 1.2, threshold = 2.4.
	// |3.0 - 6.6| = 3.6 > 2.4 → outlier. |7.0 - 6.6| = 0.4 < 2.4 → not outlier.
	for i := 1; i <= 9; i++ {
		m := insertMedia(t, s, i, fmt.Sprintf("Show %d", i), "ANIME", []string{"Action"})
		sc := insertScore(t, s, m, 7.0, "h1", fmt.Sprintf("2024-01-%02dT00:00:00Z", i), true)
		insertDimensionScores(t, s, sc, []dimFixture{{Key: "story", Label: "Story", Score: score(7.0)}})
	}
	outlierMedia := insertMedia(t, s, 10, "Outlier Show", "ANIME", []string{"Action"})
	outlierScoreID := insertScore(t, s, outlierMedia, 3.0, "h1", "2024-01-10T00:00:00Z", true)
	insertDimensionScores(t, s, outlierScoreID, []dimFixture{{Key: "story", Label: "Story", Score: score(3.0)}})

	got, err := s.Outliers(ctx)
	if err != nil {
		t.Fatalf("Outliers: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d outliers, want 1: %+v", len(got), got)
	}
	o := got[0]
	if o.AnilistID != 10 || o.DimensionKey != "story" {
		t.Fatalf("unexpected outlier: %+v", o)
	}
	if math.Abs(o.PersonalAvg-6.6) > floatTolerance {
		t.Errorf("personal avg: got %.6f, want 6.6", o.PersonalAvg)
	}
	if o.Deviation >= 0 {
		t.Errorf("deviation: got %.4f, want negative (score below average)", o.Deviation)
	}
}

func TestConfigImpact(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action"})
	b := insertMedia(t, s, 2, "Show B", "ANIME", []string{"Action"})
	c := insertMedia(t, s, 3, "Show C", "ANIME", []string{"Action"})

	insertScore(t, s, a, 6.0, "h1", "2024-01-01T00:00:00Z", false)
	insertScore(t, s, b, 8.0, "h1", "2024-01-02T00:00:00Z", true)
	insertScore(t, s, c, 9.0, "h2", "2024-02-01T00:00:00Z", true)

	got, err := s.ConfigImpact(ctx)
	if err != nil {
		t.Fatalf("ConfigImpact: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d epochs, want 2: %+v", len(got), got)
	}
	if got[0].ConfigHash != "h1" || got[0].EntryCount != 2 {
		t.Fatalf("first epoch: got %+v, want h1 with 2 entries", got[0])
	}
	if math.Abs(got[0].AvgScore-7.0) > floatTolerance {
		t.Errorf("first epoch avg: got %.4f, want 7.0", got[0].AvgScore)
	}
	if got[1].ConfigHash != "h2" || got[1].EntryCount != 1 {
		t.Fatalf("second epoch: got %+v, want h2 with 1 entry", got[1])
	}
}

func TestScoreHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action"})
	older := insertScore(t, s, a, 6.0, "h1", "2024-01-01T00:00:00Z", false)
	newer := insertScore(t, s, a, 8.0, "h1", "2024-02-01T00:00:00Z", true)
	insertDimensionScores(t, s, older, []dimFixture{{Key: "story", Label: "Story", Score: score(6.0)}})
	insertDimensionScores(t, s, newer, []dimFixture{{Key: "story", Label: "Story", Score: score(8.0)}})

	got, err := s.ScoreHistory(ctx, 1)
	if err != nil {
		t.Fatalf("ScoreHistory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d scores, want 2: %+v", len(got), got)
	}
	// Ordered scored_at DESC — newest first.
	if got[0].FinalScore != 8.0 || got[1].FinalScore != 6.0 {
		t.Errorf("order: got %.1f, %.1f — want 8.0, 6.0 (newest first)", got[0].FinalScore, got[1].FinalScore)
	}
	if len(got[0].Breakdown) != 1 || got[0].Breakdown[0].DimensionKey != "story" {
		t.Errorf("expected full breakdown to be populated: %+v", got[0].Breakdown)
	}
}

func TestListLatest(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action"})
	b := insertMedia(t, s, 2, "Show B", "ANIME", []string{"Drama"})
	insertScore(t, s, a, 6.0, "h1", "2024-01-01T00:00:00Z", false) // older, not latest
	latestA := insertScore(t, s, a, 8.0, "h1", "2024-02-01T00:00:00Z", true)
	insertDimensionScores(t, s, latestA, []dimFixture{{Key: "story", Label: "Story", Score: score(8.0)}})
	insertScore(t, s, b, 7.0, "h1", "2024-01-15T00:00:00Z", true)

	got, err := s.ListLatest(ctx)
	if err != nil {
		t.Fatalf("ListLatest: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 (one per media): %+v", len(got), got)
	}
	// Ordered scored_at DESC.
	if got[0].AnilistID != 1 || got[1].AnilistID != 2 {
		t.Errorf("order: got anilist_ids %d, %d — want 1, 2 (newest first)", got[0].AnilistID, got[1].AnilistID)
	}
	if got[0].Breakdown != nil || got[0].ActiveGenres != nil {
		t.Errorf("ListLatest must not populate Breakdown/ActiveGenres, got %+v", got[0])
	}
}

func TestSoftDeleteScore(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action"})
	older := insertScore(t, s, a, 6.0, "h1", "2024-01-01T00:00:00Z", false)
	latest := insertScore(t, s, a, 8.0, "h1", "2024-02-01T00:00:00Z", true)
	_ = older

	if err := s.SoftDeleteScore(ctx, latest); err != nil {
		t.Fatalf("SoftDeleteScore: %v", err)
	}

	// Deliberate delete does NOT promote the older score to is_latest — the
	// media should disappear from every is_latest-filtered view.
	latestScore, err := s.LatestScore(ctx, 1)
	if err != nil {
		t.Fatalf("LatestScore: %v", err)
	}
	if latestScore != nil {
		t.Errorf("LatestScore: got %+v, want nil (no promotion after deliberate delete)", latestScore)
	}

	list, err := s.ListLatest(ctx)
	if err != nil {
		t.Fatalf("ListLatest: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListLatest: got %+v, want empty", list)
	}

	// The older, never-deleted score must still be reachable via ScoreHistory.
	history, err := s.ScoreHistory(ctx, 1)
	if err != nil {
		t.Fatalf("ScoreHistory: %v", err)
	}
	if len(history) != 1 || history[0].FinalScore != 6.0 {
		t.Fatalf("ScoreHistory: got %+v, want just the older score (6.0)", history)
	}

	var reason string
	if err := s.db.Get(&reason, `SELECT deleted_reason FROM scores WHERE id = ?`, latest); err != nil {
		t.Fatalf("reading deleted_reason: %v", err)
	}
	if reason != store.DeletedReasonManual {
		t.Errorf("deleted_reason: got %q, want %q", reason, store.DeletedReasonManual)
	}
}

func TestSoftDeleteScore_NotFoundOrAlreadyDeleted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SoftDeleteScore(ctx, 999); err == nil {
		t.Error("expected an error for a nonexistent score ID")
	}

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action"})
	sc := insertScore(t, s, a, 8.0, "h1", "2024-01-01T00:00:00Z", true)
	if err := s.SoftDeleteScore(ctx, sc); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := s.SoftDeleteScore(ctx, sc); err == nil {
		t.Error("expected an error deleting an already-deleted score")
	}
}

func TestSearchMediaByTitle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	insertMedia(t, s, 1, "Frieren: Beyond Journey's End", "ANIME", []string{"Drama"})
	insertMedia(t, s, 2, "Attack on Titan", "ANIME", []string{"Action"})
	insertMedia(t, s, 3, "Berserk", "MANGA", []string{"Action"})

	tests := []struct {
		name  string
		query string
		want  []int // expected AnilistIDs, in order
	}{
		{name: "case-insensitive substring match", query: "frieren", want: []int{1}},
		{name: "matches multiple", query: "o", want: []int{2, 1}}, // "Attack on..." < "Frieren: Beyond..." alphabetically
		{name: "no match", query: "nonexistent", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.SearchMediaByTitle(ctx, tt.query)
			if err != nil {
				t.Fatalf("SearchMediaByTitle: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d results, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, r := range got {
				if r.AnilistID != tt.want[i] {
					t.Errorf("result[%d]: got anilist_id %d, want %d", i, r.AnilistID, tt.want[i])
				}
			}
		})
	}
}
