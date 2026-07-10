package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/kondanta/kansou/internal/store"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const floatTolerance = 1e-9

// sharedStore is a single PostgresStore backed by one testcontainers-managed
// Postgres instance, reused across every test in this file. Spinning up a
// fresh container per test would be needlessly slow; each test instead
// truncates the tables it touches before running (see requireStore).
var sharedStore *PostgresStore

// setupErr is non-nil when the container or store could not be started —
// most commonly because the Docker daemon isn't running. Every test skips
// (not fails) in that case, so `go test ./...` stays green on machines and
// CI runs without Docker available.
var setupErr error

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests owns the container lifecycle so `defer` cleanup actually runs —
// TestMain itself must not defer before calling os.Exit, since os.Exit skips
// any pending deferred calls in the calling function.
func runTests(m *testing.M) int {
	ctx := context.Background()

	container, err := tcpg.Run(ctx, "postgres:16-alpine",
		tcpg.WithDatabase("kansou_test"),
		tcpg.WithUsername("kansou"),
		tcpg.WithPassword("kansou"),
		tcpg.BasicWaitStrategies(),
	)
	if err != nil {
		setupErr = fmt.Errorf("starting postgres container (is Docker running?): %w", err)
		return m.Run()
	}
	defer func() { _ = container.Terminate(ctx) }()

	host, err := container.Host(ctx)
	if err != nil {
		setupErr = fmt.Errorf("getting container host: %w", err)
		return m.Run()
	}
	mapped, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		setupErr = fmt.Errorf("getting mapped port: %w", err)
		return m.Run()
	}

	st, err := New(ctx, PostgresConfig{
		Host: host, Port: mapped.Port(), User: "kansou", Password: "kansou", DBName: "kansou_test",
	})
	if err != nil {
		setupErr = fmt.Errorf("connecting store to test container: %w", err)
		return m.Run()
	}
	defer func() { _ = st.Close() }()
	sharedStore = st

	return m.Run()
}

// requireStore skips the test when the container/store failed to start, and
// otherwise returns the shared store with a clean slate for score-history
// tables (dimensions/genre_multipliers/config_scalars are left alone — no
// stats test touches scoring config).
func requireStore(t *testing.T) *PostgresStore {
	t.Helper()
	if setupErr != nil {
		t.Skipf("postgres integration test skipped: %v", setupErr)
	}
	const q = `TRUNCATE media, scores, dimension_scores, score_matched_genres, db_metadata RESTART IDENTITY CASCADE`
	if _, err := sharedStore.db.Exec(q); err != nil {
		t.Fatalf("truncating tables: %v", err)
	}
	return sharedStore
}

func insertMedia(
	t *testing.T,
	s *PostgresStore,
	anilistID int,
	title, mediaType string,
	genres []string,
) int {
	t.Helper()
	var id int
	err := s.db.QueryRow(
		`INSERT INTO media (anilist_id, title_romaji, title_english, media_type, format)
		 VALUES ($1, $2, '', $3, 'TV') RETURNING id`,
		anilistID, title, mediaType,
	).Scan(&id)
	if err != nil {
		t.Fatalf("inserting media: %v", err)
	}
	for _, g := range genres {
		if _, err := s.db.Exec(
			`INSERT INTO media_genres (media_id, genre) VALUES ($1, $2)`,
			id,
			g,
		); err != nil {
			t.Fatalf("inserting media genre: %v", err)
		}
	}
	return id
}

func insertScore(
	t *testing.T,
	s *PostgresStore,
	mediaID int,
	finalScore float64,
	configHash string,
	scoredAt time.Time,
	isLatest bool,
) int {
	t.Helper()
	var id int
	err := s.db.QueryRow(
		`INSERT INTO scores (media_id, final_score, config_hash, config_snapshot, is_latest, scored_at)
		 VALUES ($1, $2, $3, '{}', $4, $5) RETURNING id`,
		mediaID,
		finalScore,
		configHash,
		isLatest,
		scoredAt,
	).Scan(&id)
	if err != nil {
		t.Fatalf("inserting score: %v", err)
	}
	return id
}

// dimFixture describes one dimension_scores row to insert. Score == nil means skipped.
type dimFixture struct {
	Key            string
	Label          string
	Score          *float64
	WeightOverride bool
}

func score(v float64) *float64 { return new(v) }

func insertDimensionScores(t *testing.T, s *PostgresStore, scoreID int, dims []dimFixture) {
	t.Helper()
	for _, d := range dims {
		var scoreVal any
		if d.Score != nil {
			scoreVal = *d.Score
		}
		_, err := s.db.Exec(
			`INSERT INTO dimension_scores
			     (score_id, dimension_key, label, score, base_weight, final_weight, applied_multiplier, skipped, weight_override)
			 VALUES ($1, $2, $3, $4, 0.5, 0.5, 1.0, $5, $6)`,
			scoreID, d.Key, d.Label, scoreVal, d.Score == nil, d.WeightOverride,
		)
		if err != nil {
			t.Fatalf("inserting dimension score: %v", err)
		}
	}
}

func insertMatchedGenre(t *testing.T, s *PostgresStore, scoreID int, genre string) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO score_matched_genres (score_id, genre, is_primary) VALUES ($1, $2, FALSE)`,
		scoreID,
		genre,
	); err != nil {
		t.Fatalf("inserting matched genre: %v", err)
	}
}

func day(n int) time.Time {
	return time.Date(2024, 1, n, 0, 0, 0, 0, time.UTC)
}

func TestGenreBreakdown(t *testing.T) {
	s := requireStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action", "Drama"})
	b := insertMedia(t, s, 2, "Show B", "ANIME", []string{"Drama", "Romance"})
	c := insertMedia(t, s, 3, "Show C", "MANGA", []string{"Action"})
	insertScore(t, s, a, 8.0, "h1", day(1), true)
	insertScore(t, s, b, 6.0, "h1", day(2), true)
	insertScore(t, s, c, 10.0, "h1", day(3), true)

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
	s := requireStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action", "Drama"})
	b := insertMedia(t, s, 2, "Show B", "ANIME", []string{"Drama", "Romance"})
	c := insertMedia(t, s, 3, "Show C", "MANGA", []string{"Action"})
	insertScore(t, s, a, 8.0, "h1", day(1), true)
	insertScore(t, s, b, 6.0, "h1", day(2), true)
	insertScore(t, s, c, 10.0, "h1", day(3), true)

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
	s := requireStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action", "Drama"})
	b := insertMedia(t, s, 2, "Show B", "ANIME", []string{"Drama"})
	c := insertMedia(t, s, 3, "Show C", "MANGA", []string{"Action"})

	scoreA := insertScore(t, s, a, 8.0, "h1", day(1), true)
	scoreB := insertScore(t, s, b, 7.0, "h1", day(2), true)
	scoreC := insertScore(t, s, c, 8.5, "h1", day(3), true)

	insertDimensionScores(
		t,
		s,
		scoreA,
		[]dimFixture{{Key: "story", Label: "Story", Score: score(9.0)}},
	)
	insertDimensionScores(
		t,
		s,
		scoreB,
		[]dimFixture{{Key: "story", Label: "Story", Score: score(7.0)}},
	)
	insertDimensionScores(
		t,
		s,
		scoreC,
		[]dimFixture{{Key: "story", Label: "Story", Score: score(8.0)}},
	)

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
// 8, 7, 7, 8 — avg=7.5, avg(x^2)=56.5, variance=56.5-56.25=0.25, std_dev=0.5.
func seedDimensionVarianceFixture(t *testing.T, s *PostgresStore) {
	t.Helper()
	scores := []float64{8, 7, 7, 8}
	for i, v := range scores {
		m := insertMedia(t, s, i+1, fmt.Sprintf("Show %d", i+1), "ANIME", []string{"Action"})
		sc := insertScore(t, s, m, v, "h1", day(i+1), true)
		insertDimensionScores(
			t,
			s,
			sc,
			[]dimFixture{{Key: "story", Label: "Story", Score: score(v)}},
		)
	}
}

func TestDimensionVariance(t *testing.T) {
	s := requireStore(t)
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
	s := requireStore(t)
	ctx := context.Background()
	seedDimensionVarianceFixture(t, s)

	got, err := s.ScoringConsistency(ctx)
	if err != nil {
		t.Fatalf("ScoringConsistency: %v", err)
	}
	if got == nil { //nolint:staticcheck // SA5011 false positive: t.Fatal halts execution on nil
		t.Fatal("got nil, want a ConsistencyStat")
	}
	if got.Count != 1 { //nolint:staticcheck // SA5011 false positive: t.Fatal above halts execution on nil
		t.Errorf("count: got %d, want 1", got.Count)
	}
	if math.Abs(got.AvgStdDev-0.5) > floatTolerance {
		t.Errorf("avg std dev: got %.6f, want 0.5", got.AvgStdDev)
	}
}

func TestScoringConsistency_NoData(t *testing.T) {
	s := requireStore(t)
	got, err := s.ScoringConsistency(context.Background())
	if err != nil {
		t.Fatalf("ScoringConsistency: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil for empty database", got)
	}
}

func TestDimensionCorrelation_BelowThresholdExcluded(t *testing.T) {
	s := requireStore(t)
	ctx := context.Background()

	// Only 3 shared entries for the pair — well under the 25 minimum.
	for i, pair := range [][2]float64{{1, 2}, {2, 4}, {3, 6}} {
		m := insertMedia(t, s, i+1, fmt.Sprintf("Show %d", i+1), "ANIME", []string{"Action"})
		sc := insertScore(t, s, m, 7.0, "h1", day(i+1), true)
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
	s := requireStore(t)
	ctx := context.Background()

	// 26 perfectly linearly related samples (characters = 2*story) — enough
	// to clear the 25-shared-entries threshold with an exact Pearson r of 1.0.
	const n = 26
	for i := 1; i <= n; i++ {
		storyVal := float64(i)
		charactersVal := 2 * storyVal
		m := insertMedia(t, s, i, fmt.Sprintf("Show %d", i), "ANIME", []string{"Action"})
		sc := insertScore(t, s, m, storyVal, "h1", day((i%28)+1), true)
		insertDimensionScores(t, s, sc, []dimFixture{
			{Key: "story", Label: "Story", Score: score(storyVal)},
			{Key: "characters", Label: "Characters", Score: score(charactersVal)},
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
	s := requireStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Anime A", "ANIME", []string{"Action"})
	b := insertMedia(t, s, 2, "Anime B", "ANIME", []string{"Action"})
	c := insertMedia(t, s, 3, "Manga C", "MANGA", []string{"Action"})

	scA := insertScore(t, s, a, 8.0, "h1", day(1), true)
	scB := insertScore(t, s, b, 7.0, "h1", day(2), true)
	scC := insertScore(t, s, c, 9.0, "h1", day(3), true)

	insertDimensionScores(
		t,
		s,
		scA,
		[]dimFixture{{Key: "story", Label: "Story", Score: score(8.0)}},
	)
	insertDimensionScores(
		t,
		s,
		scB,
		[]dimFixture{{Key: "story", Label: "Story", Score: nil}},
	) // skipped
	insertDimensionScores(
		t,
		s,
		scC,
		[]dimFixture{{Key: "story", Label: "Story", Score: score(9.0)}},
	)

	got, err := s.SkippedDimensions(ctx)
	if err != nil {
		t.Fatalf("SkippedDimensions: %v", err)
	}

	byType := make(map[string]store.SkippedDimStat)
	for _, r := range got {
		byType[r.MediaType] = r
	}
	if anime := byType["ANIME"]; anime.SkipCount != 1 || anime.TotalCount != 2 {
		t.Errorf(
			"ANIME: got skip=%d total=%d, want skip=1 total=2",
			anime.SkipCount,
			anime.TotalCount,
		)
	}
	if manga := byType["MANGA"]; manga.SkipCount != 0 || manga.TotalCount != 1 {
		t.Errorf(
			"MANGA: got skip=%d total=%d, want skip=0 total=1",
			manga.SkipCount,
			manga.TotalCount,
		)
	}
}

func TestWeightOverrides(t *testing.T) {
	s := requireStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action"})
	b := insertMedia(t, s, 2, "Show B", "ANIME", []string{"Action"})
	scA := insertScore(t, s, a, 8.0, "h1", day(1), true)
	scB := insertScore(t, s, b, 7.0, "h1", day(2), true)

	insertDimensionScores(
		t,
		s,
		scA,
		[]dimFixture{{Key: "story", Label: "Story", Score: score(8.0), WeightOverride: true}},
	)
	insertDimensionScores(
		t,
		s,
		scB,
		[]dimFixture{{Key: "story", Label: "Story", Score: score(7.0)}},
	)

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
	s := requireStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Rescored Show", "ANIME", []string{"Action"})
	b := insertMedia(t, s, 2, "Once Show", "ANIME", []string{"Action"})

	insertScore(t, s, a, 7.0, "h1", day(1), false)
	insertScore(t, s, a, 8.5, "h2", day(15), true)
	insertScore(t, s, b, 6.0, "h1", day(10), true)

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
	if got[0].LatestScore == nil {
		t.Fatal("latest score: got nil, want 8.5")
	}
	if math.Abs(*got[0].LatestScore-8.5) > floatTolerance {
		t.Errorf("latest score: got %.4f, want 8.5", *got[0].LatestScore)
	}
	if got[1].AnilistID != 2 || got[1].ScoreCount != 1 {
		t.Fatalf("second row: got %+v, want anilist_id=2 score_count=1", got[1])
	}
}

func TestOutliers(t *testing.T) {
	s := requireStore(t)
	ctx := context.Background()

	// 9 entries at story=7.0 (tight cluster) plus one at story=3.0 (outlier).
	// avg = 6.6, population variance = 1.44, std_dev = 1.2, threshold = 2.4.
	// |3.0 - 6.6| = 3.6 > 2.4 → outlier. |7.0 - 6.6| = 0.4 < 2.4 → not outlier.
	for i := 1; i <= 9; i++ {
		m := insertMedia(t, s, i, fmt.Sprintf("Show %d", i), "ANIME", []string{"Action"})
		sc := insertScore(t, s, m, 7.0, "h1", day(i), true)
		insertDimensionScores(
			t,
			s,
			sc,
			[]dimFixture{{Key: "story", Label: "Story", Score: score(7.0)}},
		)
	}
	outlierMedia := insertMedia(t, s, 10, "Outlier Show", "ANIME", []string{"Action"})
	outlierScoreID := insertScore(t, s, outlierMedia, 3.0, "h1", day(10), true)
	insertDimensionScores(
		t,
		s,
		outlierScoreID,
		[]dimFixture{{Key: "story", Label: "Story", Score: score(3.0)}},
	)

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
	s := requireStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action"})
	b := insertMedia(t, s, 2, "Show B", "ANIME", []string{"Action"})
	c := insertMedia(t, s, 3, "Show C", "ANIME", []string{"Action"})

	insertScore(t, s, a, 6.0, "h1", day(1), false)
	insertScore(t, s, b, 8.0, "h1", day(2), true)
	insertScore(t, s, c, 9.0, "h2", day(20), true)

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
	s := requireStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action"})
	older := insertScore(t, s, a, 6.0, "h1", day(1), false)
	newer := insertScore(t, s, a, 8.0, "h1", day(2), true)
	insertDimensionScores(
		t,
		s,
		older,
		[]dimFixture{{Key: "story", Label: "Story", Score: score(6.0)}},
	)
	insertDimensionScores(
		t,
		s,
		newer,
		[]dimFixture{{Key: "story", Label: "Story", Score: score(8.0)}},
	)

	got, err := s.ScoreHistory(ctx, 1)
	if err != nil {
		t.Fatalf("ScoreHistory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d scores, want 2: %+v", len(got), got)
	}
	// Ordered scored_at DESC — newest first.
	if got[0].FinalScore != 8.0 || got[1].FinalScore != 6.0 {
		t.Errorf(
			"order: got %.1f, %.1f — want 8.0, 6.0 (newest first)",
			got[0].FinalScore,
			got[1].FinalScore,
		)
	}
	if len(got[0].Breakdown) != 1 || got[0].Breakdown[0].DimensionKey != "story" {
		t.Errorf("expected full breakdown to be populated: %+v", got[0].Breakdown)
	}
}

func TestListLatest(t *testing.T) {
	s := requireStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action"})
	b := insertMedia(t, s, 2, "Show B", "ANIME", []string{"Drama"})
	insertScore(t, s, a, 6.0, "h1", day(1), false) // older, not latest
	latestA := insertScore(t, s, a, 8.0, "h1", day(20), true)
	insertDimensionScores(
		t,
		s,
		latestA,
		[]dimFixture{{Key: "story", Label: "Story", Score: score(8.0)}},
	)
	insertScore(t, s, b, 7.0, "h1", day(15), true)

	got, err := s.ListLatest(ctx)
	if err != nil {
		t.Fatalf("ListLatest: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 (one per media): %+v", len(got), got)
	}
	// Ordered scored_at DESC.
	if got[0].AnilistID != 1 || got[1].AnilistID != 2 {
		t.Errorf(
			"order: got anilist_ids %d, %d — want 1, 2 (newest first)",
			got[0].AnilistID,
			got[1].AnilistID,
		)
	}
	if got[0].Breakdown != nil || got[0].ActiveGenres != nil {
		t.Errorf("ListLatest must not populate Breakdown/ActiveGenres, got %+v", got[0])
	}
}

func TestSoftDeleteScore(t *testing.T) {
	s := requireStore(t)
	ctx := context.Background()

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action"})
	insertScore(t, s, a, 6.0, "h1", day(1), false)
	latest := insertScore(t, s, a, 8.0, "h1", day(2), true)

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
		t.Errorf(
			"LatestScore: got %+v, want nil (no promotion after deliberate delete)",
			latestScore,
		)
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
	if err := s.db.Get(
		&reason,
		`SELECT deleted_reason FROM scores WHERE id = $1`,
		latest,
	); err != nil {
		t.Fatalf("reading deleted_reason: %v", err)
	}
	if reason != store.DeletedReasonManual {
		t.Errorf("deleted_reason: got %q, want %q", reason, store.DeletedReasonManual)
	}
}

func TestSoftDeleteScore_NotFoundOrAlreadyDeleted(t *testing.T) {
	s := requireStore(t)
	ctx := context.Background()

	if err := s.SoftDeleteScore(ctx, 999999); err == nil {
		t.Error("expected an error for a nonexistent score ID")
	}

	a := insertMedia(t, s, 1, "Show A", "ANIME", []string{"Action"})
	sc := insertScore(t, s, a, 8.0, "h1", day(1), true)
	if err := s.SoftDeleteScore(ctx, sc); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := s.SoftDeleteScore(ctx, sc); err == nil {
		t.Error("expected an error deleting an already-deleted score")
	}
}

func TestHardDeleteScore(t *testing.T) {
	s := requireStore(t)
	ctx := context.Background()

	t.Run("Scenario A: Deletes score and removes orphaned media", func(t *testing.T) {
		// Seed 1 media and 1 score
		a := insertMedia(t, s, 101, "Show A", "ANIME", []string{"Action"})
		scoreID := insertScore(t, s, a, 8.0, "h1", day(1), true)

		// Execute Hard Delete
		if err := s.HardDeleteScore(ctx, scoreID); err != nil {
			t.Fatalf("HardDeleteScore: %v", err)
		}

		// Assertions: Score row should be completely removed from the DB
		var scoreCount int
		if err := s.db.Get(
			&scoreCount,
			`SELECT COUNT(*) FROM scores WHERE id = $1`,
			scoreID,
		); err != nil {
			t.Fatalf("checking score existence: %v", err)
		}
		if scoreCount != 0 {
			t.Error("expected score row to be completely deleted from the database")
		}

		// Assertions: Media should be swept as an orphan
		var mediaCount int
		if err := s.db.Get(&mediaCount, `SELECT COUNT(*) FROM media WHERE id = $1`, a); err != nil {
			t.Fatalf("checking media existence: %v", err)
		}
		if mediaCount != 0 {
			t.Error("expected media to be reaped as an orphan, but it still exists")
		}
	})

	t.Run("Scenario B: Deletes score but keeps shared media", func(t *testing.T) {
		// Seed 1 media and 2 distinct scores pointing to it
		a := insertMedia(t, s, 102, "Show B", "ANIME", []string{"Drama"})
		scoreID1 := insertScore(t, s, a, 10.0, "h1", day(1), false)
		scoreID2 := insertScore(t, s, a, 8.0, "h1", day(2), true)

		// Delete only the first score
		if err := s.HardDeleteScore(ctx, scoreID1); err != nil {
			t.Fatalf("HardDeleteScore: %v", err)
		}

		// Assertions: First score should be gone
		var scoreCount1 int
		if err := s.db.Get(
			&scoreCount1,
			`SELECT COUNT(*) FROM scores WHERE id = $1`,
			scoreID1,
		); err != nil {
			t.Fatalf("checking score 1 existence: %v", err)
		}
		if scoreCount1 != 0 {
			t.Error("expected score 1 to be deleted")
		}

		// Assertions: Second score should remain untouched
		var scoreCount2 int
		if err := s.db.Get(
			&scoreCount2,
			`SELECT COUNT(*) FROM scores WHERE id = $1`,
			scoreID2,
		); err != nil {
			t.Fatalf("checking score 2 existence: %v", err)
		}
		if scoreCount2 != 1 {
			t.Error("expected score 2 to still exist")
		}

		// Assertions: Media should still exist because score 2 relies on it
		var mediaCount int
		if err := s.db.Get(&mediaCount, `SELECT COUNT(*) FROM media WHERE id = $1`, a); err != nil {
			t.Fatalf("checking media existence: %v", err)
		}
		if mediaCount != 1 {
			t.Error("expected media to be preserved because another score still references it")
		}
	})
}

func TestHardDeleteScore_NotFound(t *testing.T) {
	s := requireStore(t)
	ctx := context.Background()

	// Assertions: Should fail immediately for a nonexistent score ID
	if err := s.HardDeleteScore(ctx, 999999); err == nil {
		t.Error("expected an error for a nonexistent score ID")
	}
}

func TestPostgresStore_PromoteScore(t *testing.T) {
	s := requireStore(t)
	ctx := context.Background()

	t.Run("Scenario A: Promotes target score and demotes current latest", func(t *testing.T) {
		a := insertMedia(t, s, 301, "Show A", "ANIME", []string{"Action"})

		// Insert an older score (simulating a past score or soft-deleted one)
		olderScore := insertScore(t, s, a, 7.0, "h1", day(1), false)
		// Insert the current active score
		latestScore := insertScore(t, s, a, 9.0, "h1", day(2), true)

		if err := s.PromoteScore(ctx, olderScore); err != nil {
			t.Fatalf("PromoteScore: %v", err)
		}

		// Assertions: The older score should now be active and undeleted
		var isLatest bool
		var deletedAt sql.NullTime
		err := s.db.QueryRow(`SELECT is_latest, deleted_at FROM scores WHERE id = $1`, olderScore).
			Scan(&isLatest, &deletedAt)
		if err != nil {
			t.Fatalf("checking older score: %v", err)
		}
		if !isLatest {
			t.Error("expected older score to be marked as active (is_latest = true)")
		}
		if deletedAt.Valid {
			t.Error("expected older score to have NULL deleted_at")
		}

		// Assertions: The previously active score should be demoted and soft-deleted
		var demotedLatest bool
		var demotedReason sql.NullString
		err = s.db.QueryRow(`SELECT is_latest, deleted_at, deleted_reason FROM scores WHERE id = $1`, latestScore).
			Scan(&demotedLatest, &deletedAt, &demotedReason)
		if err != nil {
			t.Fatalf("checking demoted score: %v", err)
		}
		if demotedLatest {
			t.Error("expected previous latest score to be demoted (is_latest = false)")
		}
		if !deletedAt.Valid {
			t.Error("expected previous latest score to have a deleted_at timestamp populated")
		}
		if demotedReason.String != store.DeletedReasonPromote {
			t.Errorf(
				"expected deleted_reason %q, got %q",
				store.DeletedReasonPromote,
				demotedReason.String,
			)
		}
	})

	t.Run("Scenario B: Promoting an already-latest score is a safe no-op", func(t *testing.T) {
		b := insertMedia(t, s, 302, "Show B", "ANIME", []string{"Drama"})
		latestScore := insertScore(t, s, b, 8.0, "h1", day(1), true)

		if err := s.PromoteScore(ctx, latestScore); err != nil {
			t.Fatalf("PromoteScore: %v", err)
		}

		// Ensure it didn't accidentally demote/soft-delete itself
		var isLatest bool
		var deletedAt sql.NullTime
		err := s.db.QueryRow(`SELECT is_latest, deleted_at FROM scores WHERE id = $1`, latestScore).
			Scan(&isLatest, &deletedAt)
		if err != nil {
			t.Fatalf("checking safe no-op score: %v", err)
		}
		if !isLatest {
			t.Error("expected score to still be marked as active")
		}
		if deletedAt.Valid {
			t.Error("expected score to still have NULL deleted_at")
		}
	})

	t.Run("Scenario C: Returns ErrScoreNotFound for missing score", func(t *testing.T) {
		err := s.PromoteScore(ctx, 999999)
		if err == nil {
			t.Fatal("expected an error for a nonexistent score ID")
		}
		if !errors.Is(err, store.ErrScoreNotFound) {
			t.Errorf("expected ErrScoreNotFound, got: %v", err)
		}
	})
}

func TestSearchMediaByTitle(t *testing.T) {
	s := requireStore(t)
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
		{
			name:  "matches multiple",
			query: "o",
			want:  []int{2, 1},
		}, // "Attack on..." < "Frieren: Beyond..." alphabetically
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
