// Package stats aggregates scoring history into statistics for CLI and REST
// consumption. It is a thin formatting layer over store.Store — all actual
// computation happens in SQL, inside the Store implementations.
package stats

import (
	"context"
	"fmt"
	"time"

	"github.com/kondanta/kansou/internal/store"
)

// Stats computes and bundles scoring-history statistics from a Store.
type Stats struct {
	store store.Store
}

// New constructs a Stats wired to the given Store.
func New(s store.Store) *Stats {
	return &Stats{store: s}
}

// GenreStats bundles the genre-category results for `kansou stats genres`
// and GET /stats/genres.
type GenreStats struct {
	Breakdown []store.GenreStat
	ByGenre   []store.GenreScore
	Affinity  []store.GenreDimensionAffinity
}

// Genres computes the full genre-category breakdown.
func (st *Stats) Genres(ctx context.Context) (*GenreStats, error) {
	breakdown, err := st.store.GenreBreakdown(ctx)
	if err != nil {
		return nil, fmt.Errorf("genre breakdown: %w", err)
	}
	byGenre, err := st.store.ScoreByGenre(ctx)
	if err != nil {
		return nil, fmt.Errorf("score by genre: %w", err)
	}
	affinity, err := st.store.GenreDimensionAffinity(ctx)
	if err != nil {
		return nil, fmt.Errorf("genre dimension affinity: %w", err)
	}
	return &GenreStats{Breakdown: breakdown, ByGenre: byGenre, Affinity: affinity}, nil
}

// Dimensions computes the full dimension-category breakdown. The returned
// CorrelationInsufficient flag is true whenever DimensionCorrelation comes
// back empty — either no pair has 25+ shared scored entries yet, or fewer
// than two dimensions have ever been scored.
func (st *Stats) Dimensions(ctx context.Context) (*store.DimensionStatsResponse, error) {
	variance, err := st.store.DimensionVariance(ctx)
	if err != nil {
		return nil, fmt.Errorf("dimension variance: %w", err)
	}
	consistency, err := st.store.ScoringConsistency(ctx)
	if err != nil {
		return nil, fmt.Errorf("scoring consistency: %w", err)
	}
	correlation, err := st.store.DimensionCorrelation(ctx)
	if err != nil {
		return nil, fmt.Errorf("dimension correlation: %w", err)
	}
	skipped, err := st.store.SkippedDimensions(ctx)
	if err != nil {
		return nil, fmt.Errorf("skipped dimensions: %w", err)
	}
	overrides, err := st.store.WeightOverrides(ctx)
	if err != nil {
		return nil, fmt.Errorf("weight overrides: %w", err)
	}
	return &store.DimensionStatsResponse{
		DimensionVariance:       variance,
		ScoringConsistency:      consistency,
		DimensionCorrelation:    correlation,
		CorrelationInsufficient: len(correlation) == 0,
		SkippedDimensions:       skipped,
		WeightOverrides:         overrides,
	}, nil
}

// HistoryStats bundles the history-category results for `kansou stats
// history` and GET /stats/history.
type HistoryStats struct {
	MostRescored []store.RescoredStat
	Outliers     []store.OutlierStat
	ConfigImpact []store.ConfigImpactStat
}

// History computes the full history-category breakdown.
func (st *Stats) History(ctx context.Context) (*HistoryStats, error) {
	mostRescored, err := st.store.MostRescored(ctx)
	if err != nil {
		return nil, fmt.Errorf("most rescored: %w", err)
	}
	outliers, err := st.store.Outliers(ctx)
	if err != nil {
		return nil, fmt.Errorf("outliers: %w", err)
	}
	configImpact, err := st.store.ConfigImpact(ctx)
	if err != nil {
		return nil, fmt.Errorf("config impact: %w", err)
	}
	return &HistoryStats{
		MostRescored: mostRescored,
		Outliers:     outliers,
		ConfigImpact: configImpact,
	}, nil
}

// Summary aggregates one headline metric per stats category, for the bare
// `kansou stats` command and GET /stats. It intentionally draws only on the
// stats methods above (not ListLatest/ScoreHistory), so it has no dependency
// on history-command support landing first.
type Summary struct {
	TopGenre           *store.GenreStat
	TopScoringGenre    *store.GenreScore
	MostConsistentDim  *store.DimensionVarianceStat
	LeastConsistentDim *store.DimensionVarianceStat
	OverallConsistency *store.ConsistencyStat
	// MostRescored is nil unless some entry has been scored more than once.
	MostRescored *store.RescoredStat
	OutlierCount int
	// LastPruneAt is nil if `kansou db prune` has never run.
	LastPruneAt *time.Time
}

// Summary computes the headline summary.
func (st *Stats) Summary(ctx context.Context) (*Summary, error) {
	sum := &Summary{}

	breakdown, err := st.store.GenreBreakdown(ctx)
	if err != nil {
		return nil, fmt.Errorf("genre breakdown: %w", err)
	}
	if len(breakdown) > 0 {
		sum.TopGenre = &breakdown[0]
	}

	byGenre, err := st.store.ScoreByGenre(ctx)
	if err != nil {
		return nil, fmt.Errorf("score by genre: %w", err)
	}
	if top := topGenreScore(byGenre); top != nil {
		sum.TopScoringGenre = top
	}

	variance, err := st.store.DimensionVariance(ctx)
	if err != nil {
		return nil, fmt.Errorf("dimension variance: %w", err)
	}
	sum.MostConsistentDim, sum.LeastConsistentDim = variancExtremes(variance)

	consistency, err := st.store.ScoringConsistency(ctx)
	if err != nil {
		return nil, fmt.Errorf("scoring consistency: %w", err)
	}
	sum.OverallConsistency = consistency

	rescored, err := st.store.MostRescored(ctx)
	if err != nil {
		return nil, fmt.Errorf("most rescored: %w", err)
	}
	if len(rescored) > 0 && rescored[0].ScoreCount > 1 {
		sum.MostRescored = &rescored[0]
	}

	outliers, err := st.store.Outliers(ctx)
	if err != nil {
		return nil, fmt.Errorf("outliers: %w", err)
	}
	sum.OutlierCount = len(outliers)

	lastPrune, err := st.store.LastPruneAt(ctx)
	if err != nil {
		return nil, fmt.Errorf("last prune at: %w", err)
	}
	sum.LastPruneAt = lastPrune

	return sum, nil
}

// topGenreScore returns a pointer to the highest-average-score genre, or nil
// if scores is empty. ScoreByGenre already orders by avg_score DESC.
func topGenreScore(scores []store.GenreScore) *store.GenreScore {
	if len(scores) == 0 {
		return nil
	}
	return &scores[0]
}

// variancExtremes returns pointers to the lowest- and highest-std-dev
// dimensions, or nil, nil if variance is empty.
func variancExtremes(
	variance []store.DimensionVarianceStat,
) (mostConsistent, leastConsistent *store.DimensionVarianceStat) {
	if len(variance) == 0 {
		return nil, nil
	}
	mostConsistent, leastConsistent = &variance[0], &variance[0]
	for i := range variance {
		if variance[i].StdDev < mostConsistent.StdDev {
			mostConsistent = &variance[i]
		}
		if variance[i].StdDev > leastConsistent.StdDev {
			leastConsistent = &variance[i]
		}
	}
	return mostConsistent, leastConsistent
}
