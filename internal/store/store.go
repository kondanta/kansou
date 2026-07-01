// Package store defines the persistence interface for kansou scoring history.
// All database access goes through the Store interface — callers never import
// the sqlite or postgres sub-packages directly. The Store is nil in DBless mode.
package store

import (
	"context"
	"embed"
	"time"

	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
)

//go:embed migrations
var MigrationsFS embed.FS

// Store is the persistence interface for kansou. All database access goes
// through this interface — callers never import sqlite/ or postgres/ directly.
type Store interface {
	// --- Scoring config ---

	// LoadScoringConfig returns the current scoring config from the database.
	// Returns defaults if the dimensions table is empty (first-run signal).
	LoadScoringConfig(ctx context.Context) (*config.Config, error)

	// SaveScoringConfig persists the full scoring config to the database.
	SaveScoringConfig(ctx context.Context, cfg *config.Config) error

	// --- Score persistence ---

	// SaveScore saves a completed scoring session atomically across all four
	// tables: media, scores, dimension_scores, score_matched_genres.
	// It upserts media, sets is_latest=true on the new score, sets
	// is_latest=false on the previous latest, and soft-deletes scores beyond
	// max_history.
	// cfg is the scoring config active at the time of scoring. It is used to
	// build the config_snapshot and to compute config_hash via config.Hash(cfg).
	SaveScore(ctx context.Context, result scoring.Result, cfg *config.Config, maxHistory int) error

	// LatestScore returns the most recent non-deleted score for a given
	// AniList media ID. Returns nil, nil if no score exists.
	LatestScore(ctx context.Context, anilistID int) (*Score, error)

	// ScoreHistory returns all non-deleted scores for a given AniList media ID,
	// ordered by scored_at DESC.
	ScoreHistory(ctx context.Context, anilistID int) ([]Score, error)

	// ListLatest returns the latest score for every media entry, ordered by
	// scored_at DESC. Excludes soft-deleted scores. Does NOT populate Breakdown
	// or ActiveGenres — use LatestScore or ScoreHistory when the full breakdown
	// is needed.
	ListLatest(ctx context.Context) ([]Score, error)

	// --- History management ---

	// SoftDeleteScore sets deleted_at = now() on the given score ID.
	SoftDeleteScore(ctx context.Context, scoreID int) error

	// --- Gardening ---

	// Prune hard-deletes all rows where deleted_at IS NOT NULL across all
	// related tables (cascades via FK). Returns the number of score rows deleted.
	Prune(ctx context.Context) (int64, error)

	// LastPruneAt returns the timestamp of the last prune operation, read from
	// db_metadata where key = 'last_prune_at'. Returns nil if Prune has never run.
	LastPruneAt(ctx context.Context) (*time.Time, error)

	// --- Stats ---

	// GenreBreakdown returns the count and percentage of entries per genre.
	GenreBreakdown(ctx context.Context) ([]GenreStat, error)

	// ScoreByGenre returns the average final score per genre.
	ScoreByGenre(ctx context.Context) ([]GenreScore, error)

	// GenreDimensionAffinity returns average dimension scores grouped by genre.
	GenreDimensionAffinity(ctx context.Context) ([]GenreDimensionAffinity, error)

	// DimensionVariance returns the standard deviation of scores per dimension
	// across all latest entries.
	DimensionVariance(ctx context.Context) ([]DimensionVarianceStat, error)

	// ScoringConsistency returns the average standard deviation across all
	// dimensions — a single number representing overall scoring consistency.
	ScoringConsistency(ctx context.Context) (*ConsistencyStat, error)

	// DimensionCorrelation returns Pearson correlation coefficients between
	// pairs of dimensions. Pairs with fewer than 25 shared scored entries are
	// excluded from results.
	DimensionCorrelation(ctx context.Context) ([]DimensionCorrelationStat, error)

	// SkippedDimensions returns how often each dimension is skipped,
	// split by media type (ANIME/MANGA).
	SkippedDimensions(ctx context.Context) ([]SkippedDimStat, error)

	// WeightOverrides returns how often each dimension has been weight-overridden
	// via the --weight flag.
	WeightOverrides(ctx context.Context) ([]WeightOverrideStat, error)

	// MostRescored returns entries ordered by rescore count descending.
	MostRescored(ctx context.Context) ([]RescoredStat, error)

	// Outliers returns entries where at least one dimension score deviates
	// more than 2 standard deviations from the user's personal average for
	// that dimension.
	Outliers(ctx context.Context) ([]OutlierStat, error)

	// ConfigImpact returns average score before and after each config change,
	// identified by config_hash transitions in the scores table.
	ConfigImpact(ctx context.Context) ([]ConfigImpactStat, error)

	// --- Lifecycle ---

	Close() error
}

// Score is the full representation of a scoring event returned by the Store.
type Score struct {
	ID                 int
	AnilistID          int
	TitleRomaji        string
	TitleEnglish       string
	MediaType          string
	Format             string
	Genres             []string
	FinalScore         float64
	PrimaryGenre       string
	PrimaryGenreWeight float64
	ConfigHash         string
	IsLatest           bool
	ScoredAt           time.Time
	DeletedAt          *time.Time
	// UserSelectedGenres is nil if the user did not explicitly select genres.
	UserSelectedGenres []string
	Breakdown          []DimensionScoreRow
	ActiveGenres       []MatchedGenreRow
}

// DimensionScoreRow is one dimension within a Score.
type DimensionScoreRow struct {
	DimensionKey      string
	Label             string
	Score             *float64 // nil if skipped
	BaseWeight        float64
	FinalWeight       float64
	AppliedMultiplier float64
	Contribution      *float64 // nil if skipped
	Skipped           bool
	BiasResistant     bool
	WeightOverride    bool
	// GenreDeselected is true when a deselected genre would have contributed
	// to this dimension's multiplier.
	GenreDeselected bool
}

// MatchedGenreRow is one genre entry within a Score.
type MatchedGenreRow struct {
	Genre     string
	IsPrimary bool
}

// ConfigSnapshot is the full scoring config state persisted alongside each score.
// It answers "why did this score change after I edited my config?" and is the
// source of truth for config_snapshot in the scores table.
type ConfigSnapshot struct {
	Dimensions         map[string]snapshotDimension  `json:"dimensions"`
	Genres             map[string]map[string]float64 `json:"genres"`
	PrimaryGenreWeight float64                       `json:"primary_genre_weight"`
	MaxMultiplier      float64                       `json:"max_multiplier"`
}

// snapshotDimension captures per-dimension state at scoring time.
type snapshotDimension struct {
	Label         string  `json:"label"`
	Weight        float64 `json:"weight"`
	BiasResistant bool    `json:"bias_resistant"`
}

// BuildConfigSnapshot constructs a ConfigSnapshot from a Config.
// Called by SaveScore to build the JSON blob stored in scores.config_snapshot.
func BuildConfigSnapshot(cfg *config.Config) ConfigSnapshot {
	dims := make(map[string]snapshotDimension, len(cfg.Dimensions))
	for key, d := range cfg.Dimensions {
		dims[key] = snapshotDimension{
			Label:         d.Label,
			Weight:        d.Weight,
			BiasResistant: d.BiasResistant,
		}
	}
	return ConfigSnapshot{
		Dimensions:         dims,
		Genres:             cfg.Genres,
		PrimaryGenreWeight: cfg.PrimaryGenreWeight,
		MaxMultiplier:      cfg.MaxMultiplier,
	}
}

// --- Stat result types ---

// GenreStat holds the count and percentage of entries for a single genre.
type GenreStat struct {
	Genre      string
	Count      int
	Percentage float64
}

// GenreScore holds the average final score and entry count for a single genre.
type GenreScore struct {
	Genre    string
	AvgScore float64
	Count    int
}

// GenreDimensionAffinity holds average dimension scores for a single genre.
type GenreDimensionAffinity struct {
	Genre      string
	Dimensions []DimensionAvg
}

// DimensionAvg is one dimension's average score within a GenreDimensionAffinity.
type DimensionAvg struct {
	DimensionKey string
	Label        string
	AvgScore     float64
}

// DimensionVarianceStat holds the standard deviation and average for a dimension.
type DimensionVarianceStat struct {
	DimensionKey string
	Label        string
	StdDev       float64
	AvgScore     float64
	Count        int
}

// ConsistencyStat holds the average standard deviation across all dimensions.
type ConsistencyStat struct {
	AvgStdDev float64
	Count     int
}

// DimensionCorrelationStat holds the Pearson correlation between two dimensions.
type DimensionCorrelationStat struct {
	DimensionA  string
	DimensionB  string
	Correlation float64 // Pearson -1.0 to 1.0
}

// DimensionStatsResponse is the response shape for GET /stats/dimensions.
// CorrelationInsufficient is true when all dimension pairs have fewer than 25
// shared scored entries and DimensionCorrelation is therefore an empty slice.
type DimensionStatsResponse struct {
	DimensionVariance    []DimensionVarianceStat
	ScoringConsistency   *ConsistencyStat
	DimensionCorrelation []DimensionCorrelationStat
	// CorrelationInsufficient is true when no pairs met the minimum threshold.
	CorrelationInsufficient bool `json:"correlation_insufficient"`
	SkippedDimensions       []SkippedDimStat
	WeightOverrides         []WeightOverrideStat
}

// SkippedDimStat holds how often a dimension was skipped, split by media type.
type SkippedDimStat struct {
	DimensionKey string
	Label        string
	MediaType    string
	SkipCount    int
	TotalCount   int
}

// WeightOverrideStat holds how often a dimension was weight-overridden.
type WeightOverrideStat struct {
	DimensionKey  string
	Label         string
	OverrideCount int
}

// RescoredStat holds rescore count data for one media entry.
type RescoredStat struct {
	AnilistID     int
	TitleRomaji   string
	ScoreCount    int
	LatestScore   float64
	FirstScoredAt time.Time
	LastScoredAt  time.Time
}

// OutlierStat holds a dimension score that deviates more than 2 std devs from
// the user's personal average for that dimension.
type OutlierStat struct {
	AnilistID    int
	TitleRomaji  string
	ScoreID      int
	ScoredAt     time.Time
	DimensionKey string
	Label        string
	Score        float64
	PersonalAvg  float64
	Deviation    float64 // how many std devs from personal avg
}

// ConfigImpactStat holds average score data for one config hash epoch.
type ConfigImpactStat struct {
	ConfigHash    string
	EntryCount    int
	AvgScore      float64
	FirstScoredAt time.Time
	LastScoredAt  time.Time
}
