package stats

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
	"github.com/kondanta/kansou/internal/store"
)

// fakeStore is a minimal store.Store implementation for unit-testing Stats's
// aggregation logic without a real database. Only the fields a given test
// populates are non-zero; every method simply returns its configured field.
type fakeStore struct {
	genreBreakdown []store.GenreStat
	scoreByGenre   []store.GenreScore
	affinity       []store.GenreDimensionAffinity
	variance       []store.DimensionVarianceStat
	consistency    *store.ConsistencyStat
	correlation    []store.DimensionCorrelationStat
	skipped        []store.SkippedDimStat
	overrides      []store.WeightOverrideStat
	mostRescored   []store.RescoredStat
	outliers       []store.OutlierStat
	configImpact   []store.ConfigImpactStat
	lastPruneAt    *time.Time
}

func (f *fakeStore) LoadScoringConfig(context.Context) (*config.Config, error) { return nil, nil }
func (f *fakeStore) SaveScoringConfig(context.Context, *config.Config) error   { return nil }
func (f *fakeStore) SaveScore(context.Context, scoring.Result, *config.Config, int) error {
	return nil
}
func (f *fakeStore) LatestScore(context.Context, int) (*store.Score, error)   { return nil, nil }
func (f *fakeStore) ScoreHistory(context.Context, int) ([]store.Score, error) { return nil, nil }
func (f *fakeStore) ListLatest(context.Context) ([]store.Score, error)        { return nil, nil }
func (f *fakeStore) SoftDeleteScore(context.Context, int) error               { return nil }
func (f *fakeStore) Prune(context.Context) (int64, error)                     { return 0, nil }
func (f *fakeStore) LastPruneAt(context.Context) (*time.Time, error)          { return f.lastPruneAt, nil }
func (f *fakeStore) GenreBreakdown(context.Context) ([]store.GenreStat, error) {
	return f.genreBreakdown, nil
}
func (f *fakeStore) ScoreByGenre(context.Context) ([]store.GenreScore, error) {
	return f.scoreByGenre, nil
}
func (f *fakeStore) GenreDimensionAffinity(context.Context) ([]store.GenreDimensionAffinity, error) {
	return f.affinity, nil
}
func (f *fakeStore) DimensionVariance(context.Context) ([]store.DimensionVarianceStat, error) {
	return f.variance, nil
}
func (f *fakeStore) ScoringConsistency(context.Context) (*store.ConsistencyStat, error) {
	return f.consistency, nil
}
func (f *fakeStore) DimensionCorrelation(context.Context) ([]store.DimensionCorrelationStat, error) {
	return f.correlation, nil
}
func (f *fakeStore) SkippedDimensions(context.Context) ([]store.SkippedDimStat, error) {
	return f.skipped, nil
}
func (f *fakeStore) WeightOverrides(context.Context) ([]store.WeightOverrideStat, error) {
	return f.overrides, nil
}
func (f *fakeStore) MostRescored(context.Context) ([]store.RescoredStat, error) {
	return f.mostRescored, nil
}
func (f *fakeStore) Outliers(context.Context) ([]store.OutlierStat, error) { return f.outliers, nil }
func (f *fakeStore) ConfigImpact(context.Context) ([]store.ConfigImpactStat, error) {
	return f.configImpact, nil
}
func (f *fakeStore) Close() error { return nil }

func TestTopGenreScore(t *testing.T) {
	tests := []struct {
		name   string
		scores []store.GenreScore
		want   *string
	}{
		{name: "empty", scores: nil, want: nil},
		{name: "single", scores: []store.GenreScore{{Genre: "Action"}}, want: ptr("Action")},
		{name: "picks first (already sorted by caller)",
			scores: []store.GenreScore{{Genre: "Drama"}, {Genre: "Action"}}, want: ptr("Drama")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := topGenreScore(tt.scores)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("got %+v, want nil", got)
				}
				return
			}
			if got == nil || got.Genre != *tt.want {
				t.Fatalf("got %+v, want genre %q", got, *tt.want)
			}
		})
	}
}

func TestVariancExtremes(t *testing.T) {
	tests := []struct {
		name           string
		variance       []store.DimensionVarianceStat
		wantMost       string
		wantLeast      string
		wantNilForBoth bool
	}{
		{name: "empty", variance: nil, wantNilForBoth: true},
		{
			name: "unsorted input still finds true extremes",
			variance: []store.DimensionVarianceStat{
				{DimensionKey: "a", StdDev: 2.0},
				{DimensionKey: "b", StdDev: 0.5},
				{DimensionKey: "c", StdDev: 5.0},
			},
			wantMost:  "b",
			wantLeast: "c",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			most, least := variancExtremes(tt.variance)
			if tt.wantNilForBoth {
				if most != nil || least != nil {
					t.Fatalf("got most=%+v least=%+v, want both nil", most, least)
				}
				return
			}
			if most == nil || most.DimensionKey != tt.wantMost {
				t.Errorf("most consistent: got %+v, want %q", most, tt.wantMost)
			}
			if least == nil || least.DimensionKey != tt.wantLeast {
				t.Errorf("least consistent: got %+v, want %q", least, tt.wantLeast)
			}
		})
	}
}

func TestSummary_MostRescoredGating(t *testing.T) {
	tests := []struct {
		name     string
		rescored []store.RescoredStat
		wantNil  bool
	}{
		{name: "no history", rescored: nil, wantNil: true},
		{name: "scored once is not a rescore", rescored: []store.RescoredStat{{AnilistID: 1, ScoreCount: 1}}, wantNil: true},
		{name: "scored twice surfaces as most rescored", rescored: []store.RescoredStat{{AnilistID: 1, ScoreCount: 2}}, wantNil: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := New(&fakeStore{mostRescored: tt.rescored})
			sum, err := st.Summary(context.Background())
			if err != nil {
				t.Fatalf("Summary: %v", err)
			}
			if tt.wantNil != (sum.MostRescored == nil) {
				t.Errorf("got MostRescored=%+v, wantNil=%v", sum.MostRescored, tt.wantNil)
			}
		})
	}
}

func TestSummary_PassesThroughSimpleFields(t *testing.T) {
	pruned := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fs := &fakeStore{
		genreBreakdown: []store.GenreStat{{Genre: "Action", Count: 4}},
		consistency:    &store.ConsistencyStat{AvgStdDev: 1.5, Count: 3},
		outliers:       []store.OutlierStat{{AnilistID: 1}, {AnilistID: 2}},
		lastPruneAt:    &pruned,
	}
	sum, err := New(fs).Summary(context.Background())
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if sum.TopGenre == nil || sum.TopGenre.Genre != "Action" {
		t.Errorf("TopGenre: got %+v, want Action", sum.TopGenre)
	}
	if sum.OverallConsistency == nil || sum.OverallConsistency.AvgStdDev != 1.5 {
		t.Errorf("OverallConsistency: got %+v, want AvgStdDev=1.5", sum.OverallConsistency)
	}
	if sum.OutlierCount != 2 {
		t.Errorf("OutlierCount: got %d, want 2", sum.OutlierCount)
	}
	if sum.LastPruneAt == nil || !sum.LastPruneAt.Equal(pruned) {
		t.Errorf("LastPruneAt: got %v, want %v", sum.LastPruneAt, pruned)
	}
}

func TestDimensions_CorrelationInsufficientFlag(t *testing.T) {
	tests := []struct {
		name        string
		correlation []store.DimensionCorrelationStat
		want        bool
	}{
		{name: "empty means insufficient", correlation: nil, want: true},
		{name: "any result means sufficient", correlation: []store.DimensionCorrelationStat{{DimensionA: "a", DimensionB: "b"}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := New(&fakeStore{correlation: tt.correlation}).Dimensions(context.Background())
			if err != nil {
				t.Fatalf("Dimensions: %v", err)
			}
			if resp.CorrelationInsufficient != tt.want {
				t.Errorf("CorrelationInsufficient: got %v, want %v", resp.CorrelationInsufficient, tt.want)
			}
		})
	}
}

// TestGenresAndHistoryBundle uses two elements per list with distinguishing
// values (not just a length of 1) so a field-swap bug — e.g. Genres()
// accidentally assigning ScoreByGenre's result into Breakdown — would fail
// the value checks even though the compiler can't catch it (the store
// methods return different-but-structurally-similar types).
func TestGenresAndHistoryBundle(t *testing.T) {
	fs := &fakeStore{
		genreBreakdown: []store.GenreStat{{Genre: "Action", Count: 3}, {Genre: "Drama", Count: 1}},
		scoreByGenre:   []store.GenreScore{{Genre: "Romance", AvgScore: 6.5}},
		affinity:       []store.GenreDimensionAffinity{{Genre: "Comedy"}},
		mostRescored:   []store.RescoredStat{{AnilistID: 42, TitleRomaji: "Rescored Show"}},
		outliers:       []store.OutlierStat{{AnilistID: 7}, {AnilistID: 9}},
		configImpact:   []store.ConfigImpactStat{{ConfigHash: "h1"}},
	}
	st := New(fs)

	g, err := st.Genres(context.Background())
	if err != nil {
		t.Fatalf("Genres: %v", err)
	}
	if len(g.Breakdown) != 2 || g.Breakdown[0].Genre != "Action" || g.Breakdown[1].Genre != "Drama" {
		t.Errorf("Breakdown: got %+v, want [Action, Drama]", g.Breakdown)
	}
	if len(g.ByGenre) != 1 || g.ByGenre[0].Genre != "Romance" {
		t.Errorf("ByGenre: got %+v, want [Romance]", g.ByGenre)
	}
	if len(g.Affinity) != 1 || g.Affinity[0].Genre != "Comedy" {
		t.Errorf("Affinity: got %+v, want [Comedy]", g.Affinity)
	}

	h, err := st.History(context.Background())
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(h.MostRescored) != 1 || h.MostRescored[0].TitleRomaji != "Rescored Show" {
		t.Errorf("MostRescored: got %+v, want [Rescored Show]", h.MostRescored)
	}
	if len(h.Outliers) != 2 {
		t.Errorf("Outliers: got %+v, want 2 entries", h.Outliers)
	}
	if len(h.ConfigImpact) != 1 || h.ConfigImpact[0].ConfigHash != "h1" {
		t.Errorf("ConfigImpact: got %+v, want [h1]", h.ConfigImpact)
	}
}

// errStore fails every method with a distinct, identifiable error so tests
// can confirm Stats propagates (rather than swallows) store errors. It
// embeds *fakeStore by pointer so the promoted (pointer-receiver) methods
// remain part of errStore's own method set.
type errStore struct{ *fakeStore }

var errBoom = errors.New("boom")

func (errStore) GenreBreakdown(context.Context) ([]store.GenreStat, error) { return nil, errBoom }
func (errStore) ScoreByGenre(context.Context) ([]store.GenreScore, error)  { return nil, errBoom }
func (errStore) DimensionVariance(context.Context) ([]store.DimensionVarianceStat, error) {
	return nil, errBoom
}
func (errStore) MostRescored(context.Context) ([]store.RescoredStat, error) { return nil, errBoom }

func TestErrorsPropagateFromStore(t *testing.T) {
	st := New(errStore{&fakeStore{}})
	ctx := context.Background()

	if _, err := st.Genres(ctx); !errors.Is(err, errBoom) {
		t.Errorf("Genres: got err=%v, want it to wrap errBoom", err)
	}
	if _, err := st.Dimensions(ctx); !errors.Is(err, errBoom) {
		t.Errorf("Dimensions: got err=%v, want it to wrap errBoom", err)
	}
	if _, err := st.History(ctx); !errors.Is(err, errBoom) {
		t.Errorf("History: got err=%v, want it to wrap errBoom", err)
	}
	if _, err := st.Summary(ctx); !errors.Is(err, errBoom) {
		t.Errorf("Summary: got err=%v, want it to wrap errBoom", err)
	}
}

func ptr(s string) *string { return &s }
