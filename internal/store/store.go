// Package store defines the persistence interface for kansou scoring history.
// All database access goes through the Store interface — callers never import
// the sqlite or postgres sub-packages directly. The Store is nil in DBless mode.
package store

import (
	"context"
	"embed"
	"errors"
	"time"

	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
)

// ErrScoreNotFound is returned by SoftDeleteScore when the given score ID
// does not exist or is already soft-deleted. Wrapped with context by callers
// (e.g. fmt.Errorf("score %d: %w", id, ErrScoreNotFound)) — check with
// errors.Is, not string comparison.
var ErrScoreNotFound = errors.New("score not found or already deleted")

// ErrNotSeeded is returned by LoadScoringConfig when the dimensions table is
// empty — the database has never been seeded with a scoring config. Callers
// (see cmd/root.go) check with errors.Is and seed from the config file on
// this signal; it is distinct from a config successfully loaded with
// built-in defaults, which is not an error.
var ErrNotSeeded = errors.New("scoring config not yet seeded")

//go:embed migrations
var MigrationsFS embed.FS

// Soft-delete reasons recorded in scores.deleted_reason. The two paths that
// set deleted_at can never race on the same row (gardening only ever prunes
// rows older than the retention window; SoftDeleteScore only ever targets one
// caller-specified row), so this distinction exists for accountability and
// audit, not to resolve a conflict between the two.
const (
	// DeletedReasonManual marks a row removed by a deliberate SoftDeleteScore call.
	DeletedReasonManual = "manual"
	// DeletedReasonMaxHistory marks a row pruned automatically by max_history
	// retention inside SaveScore.
	DeletedReasonMaxHistory = "max_history"
)

// Store is the persistence interface for kansou. All database access goes
// through this interface — callers never import sqlite/ or postgres/ directly.
type Store interface {
	// --- Scoring config ---

	// LoadScoringConfig returns the current scoring config from the database.
	// Returns ErrNotSeeded if the dimensions table is empty — the caller must
	// seed the database from the config file in that case.
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

	// SearchMediaByTitle returns media whose title_romaji matches query
	// case-insensitively (substring match), ordered by title_romaji. Matches
	// any media that has ever been scored, regardless of whether its scores
	// are soft-deleted — used to resolve `history show`/`history delete
	// <query>` when query isn't a numeric AniList ID. Every media row has at
	// least one associated score by construction (SaveScore always inserts
	// both in the same transaction), so no JOIN against scores is needed.
	SearchMediaByTitle(ctx context.Context, query string) ([]MediaSearchResult, error)

	// --- History management ---

	// SoftDeleteScore sets deleted_at = now(), deleted_reason = DeletedReasonManual,
	// and is_latest = false on the given score ID. Deliberate removal from active
	// tracking — it does NOT promote any other score for the same media to
	// is_latest, even if the deleted score was the latest one. Older scores stay
	// in the database (subject to max_history) but the media stops appearing in
	// is_latest-filtered views (ListLatest, LatestScore, all stats methods) until
	// the user scores it again, which naturally sets a fresh is_latest row via
	// the existing SaveScore path.
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

// MediaSearchResult is one match from SearchMediaByTitle.
type MediaSearchResult struct {
	AnilistID    int    `json:"anilist_id"`
	TitleRomaji  string `json:"title_romaji"`
	TitleEnglish string `json:"title_english"`
	MediaType    string `json:"media_type"`
	Format       string `json:"format"`
}

// Score is the full representation of a scoring event returned by the Store.
type Score struct {
	ID                 int      `json:"id"`
	AnilistID          int      `json:"anilist_id"`
	TitleRomaji        string   `json:"title_romaji"`
	TitleEnglish       string   `json:"title_english"`
	MediaType          string   `json:"media_type"`
	Format             string   `json:"format"`
	Genres             []string `json:"genres"`
	FinalScore         float64  `json:"final_score"`
	PrimaryGenre       string   `json:"primary_genre"`
	PrimaryGenreWeight float64  `json:"primary_genre_weight"`
	ConfigHash         string   `json:"config_hash"`
	IsLatest           bool     `json:"is_latest"`
	// EntryCount is the number of non-deleted scores for this media entry.
	// Only populated by ListLatest; zero on rows returned by ScoreHistory or
	// LatestScore, where the caller already has the full list to count.
	EntryCount int        `json:"entry_count"`
	CoverImage string     `json:"cover_image"`
	ScoredAt   time.Time  `json:"scored_at"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
	// UserSelectedGenres is nil if the user did not explicitly select genres.
	UserSelectedGenres []string            `json:"user_selected_genres,omitempty"`
	Breakdown          []DimensionScoreRow `json:"breakdown"`
	ActiveGenres       []MatchedGenreRow   `json:"active_genres"`
}

// DimensionScoreRow is one dimension within a Score.
type DimensionScoreRow struct {
	DimensionKey      string   `json:"dimension_key"`
	Label             string   `json:"label"`
	Score             *float64 `json:"score"` // nil if skipped
	BaseWeight        float64  `json:"base_weight"`
	FinalWeight       float64  `json:"final_weight"`
	AppliedMultiplier float64  `json:"applied_multiplier"`
	Contribution      *float64 `json:"contribution"` // nil if skipped
	Skipped           bool     `json:"skipped"`
	BiasResistant     bool     `json:"bias_resistant"`
	WeightOverride    bool     `json:"weight_override"`
	// GenreDeselected is true when a deselected genre would have contributed
	// to this dimension's multiplier.
	GenreDeselected bool `json:"genre_deselected"`
	// PrimaryGenreMultiplier is the raw multiplier the primary genre defined for
	// this dimension at scoring time. 0 when no primary genre was set or the
	// dimension is bias-resistant.
	PrimaryGenreMultiplier float64 `json:"primary_genre_multiplier"`
	// SecondaryGenresMultiplier is the contributing-only average multiplier
	// across non-primary matched genres at scoring time. 0 when no primary genre
	// was set, there were no secondary genres, or the dimension is bias-resistant.
	SecondaryGenresMultiplier float64 `json:"secondary_genres_multiplier"`
}

// MatchedGenreRow is one genre entry within a Score.
type MatchedGenreRow struct {
	Genre     string `json:"genre"`
	IsPrimary bool   `json:"is_primary"`
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
	AnilistID   int
	TitleRomaji string
	ScoreCount  int
	// LatestScore is nil if every non-deleted score for this media has had
	// is_latest unset (e.g. the latest score was removed via SoftDeleteScore
	// without a replacement being scored yet). ScoreCount is still counted
	// from non-deleted rows regardless.
	LatestScore   *float64
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
