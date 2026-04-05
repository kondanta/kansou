// Package scoring implements the weighted, genre-adjusted scoring engine for kansou.
// It is a pure-function package with no I/O or side effects. All inputs are passed
// explicitly; all results are returned as values.
package scoring

// MediaType distinguishes anime from manga. The engine itself does not branch on
// this value — it is carried through for provenance purposes and used by the
// caller (CLI/server) to adapt dimension prompt labels.
type MediaType string

const (
	// Anime covers TV, TV_SHORT, MOVIE, SPECIAL, OVA, ONA, MUSIC formats.
	Anime MediaType = "ANIME"
	// Manga covers all non-anime formats (MANGA, ONE_SHOT, NOVEL, etc.).
	Manga MediaType = "MANGA"
)

// DimensionKey is the snake_case config key identifying a scoring dimension
// (e.g. "story", "world_building"). It is a string — the engine never
// references dimensions by name, it iterates over whatever config provides.
type DimensionKey = string

// DimensionDef defines a single scoring dimension as loaded from config.
type DimensionDef struct {
	// Label is the human-readable display name shown in CLI prompts.
	Label string
	// Description is the hint shown during a scoring session.
	Description string
	// Weight is the base weight for this dimension. All weights in a config
	// must sum to 1.0 (±0.001 tolerance). Validated by the config loader.
	Weight float64
	// BiasResistant, when true, causes the engine to always apply a multiplier
	// of exactly 1.0 to this dimension regardless of genre config.
	// See ADR-007.
	BiasResistant bool
}

// Entry is the full input to a scoring session. It is constructed by the
// CLI or server layer and passed to Engine.Score().
type Entry struct {
	// Scores maps each DimensionKey to a user-provided score (1.0–10.0).
	// Dimensions present in config but absent here are treated as skipped
	// if also present in SkippedDimensions, otherwise the engine returns an error.
	Scores map[DimensionKey]float64

	// SkippedDimensions holds the keys of dimensions the user marked as N/A.
	// Skipped dimensions are excluded from the weight pool before renormalization.
	// See ADR-013.
	SkippedDimensions map[DimensionKey]bool

	// WeightOverrides holds per-session weight overrides supplied via --weight.
	// Overridden dimensions are fixed at the given value; remaining dimensions
	// are rescaled proportionally. Applied after genre renormalization.
	// See ADR-008.
	WeightOverrides map[DimensionKey]float64

	// Genres is the list of genre strings returned by AniList for this entry.
	// Used to look up multipliers in the config genre map.
	Genres []string

	// UserSelectedGenres, if non-nil and non-empty, is the definitive active genre
	// set for this session. Only these genres participate in multiplier calculation.
	// When nil or empty, all matched config genres participate — preserving the
	// existing CLI behaviour. Set by the web UI via POST /score's selected_genres field.
	UserSelectedGenres []string

	// PrimaryGenre, if non-empty, designates one genre as the constitutive genre
	// for blended multiplier calculation. See ADR-022.
	PrimaryGenre string

	// Meta carries session-level provenance data (media identity, config hash).
	// Constructed by the caller before invoking Engine.Score().
	Meta SessionMeta
}

// SessionMeta carries session-level provenance — who was scored, with what config.
// Included verbatim in Result regardless of whether --breakdown is requested.
// See ADR-012.
type SessionMeta struct {
	// MediaID is the AniList media ID — the canonical identifier for the entry.
	MediaID int
	// TitleRomaji is the romanised title of the media entry.
	TitleRomaji string
	// TitleEnglish is the English title (may be empty).
	TitleEnglish string
	// MediaType is Anime or Manga, derived from the AniList format field.
	MediaType MediaType
	// AniListURL is the canonical AniList URL for this entry.
	AniListURL string
	// AllGenres is the full genre list returned by AniList.
	AllGenres []string
	// MatchedGenres is the subset of AllGenres that matched a config genre block.
	MatchedGenres []string
	// GenresActive is the genre set that actually participated in multiplier
	// calculation for this session. Equal to MatchedGenres when no
	// UserSelectedGenres were specified; equal to the intersection of
	// UserSelectedGenres with the config genre map otherwise.
	GenresActive []string
	// ConfigHash is a SHA256 hex digest of the serialised dimensions config
	// at time of scoring. Allows detection of config drift for stored scores.
	ConfigHash string
	// PrimaryGenre is the genre designated as primary for this session.
	// Empty when no primary genre was specified. See ADR-022.
	PrimaryGenre string
	// PrimaryGenreWeight is the configured blend ratio that was active during
	// this session (copied from config at score time for provenance).
	PrimaryGenreWeight float64
}

// BreakdownRow is the full audit trail for a single dimension's contribution
// to the final score. Always populated regardless of --breakdown flag.
// See ADR-012.
type BreakdownRow struct {
	// Key is the dimension's config key.
	Key DimensionKey
	// Label is the display name for this dimension.
	Label string
	// Score is the user-provided score (0 if skipped).
	Score float64
	// BaseWeight is the configured weight before any genre adjustment.
	BaseWeight float64
	// AppliedMultiplier is the averaged multiplier actually used
	// (1.0 for bias-resistant dimensions or when no genres matched).
	AppliedMultiplier float64
	// FinalWeight is the weight after genre adjustment and renormalization.
	// Zero if skipped.
	FinalWeight float64
	// Contribution is Score × FinalWeight. Zero if skipped.
	Contribution float64
	// BiasResistant indicates this dimension's multiplier is always 1.0.
	BiasResistant bool
	// WeightOverride indicates the final weight was set by a --weight flag
	// rather than derived from base weight and genre multipliers.
	WeightOverride bool
	// Skipped indicates the user marked this dimension as not applicable.
	// Skipped dimensions have FinalWeight=0 and Contribution=0.
	Skipped bool
	// GenreDeselected is true when at least one deselected genre (present in
	// Entry.Genres and the config, but excluded by UserSelectedGenres) has a
	// configured multiplier for this specific dimension. False when no
	// UserSelectedGenres were specified or no deselected genre had an opinion
	// on this dimension. See ADR-023.
	GenreDeselected bool
	// PrimaryGenre is the genre designated as primary for this scoring session.
	// Empty when no primary genre was specified. Carried for provenance only.
	// See ADR-022.
	PrimaryGenre string
	// PrimaryGenreMultiplier is the raw multiplier the primary genre defines for
	// this dimension (1.0 if the primary genre has no opinion on it). Zero when
	// no primary genre was specified or the dimension is bias-resistant.
	PrimaryGenreMultiplier float64
}

// Result is the output of Engine.Score(). The Breakdown is always fully
// populated — the caller decides whether to display it. See ADR-012.
type Result struct {
	// FinalScore is the weighted sum, rounded to two decimal places.
	FinalScore float64
	// Breakdown is the per-dimension audit trail. Always populated.
	Breakdown []BreakdownRow
	// Meta is the session-level provenance carried through from Entry.Meta.
	Meta SessionMeta
}

// WeightRow is the per-dimension output of Engine.Weights(). It carries the
// final weight and multiplier for a single dimension without requiring scores.
// Used by POST /weights for live preview in the web UI. See ADR-023.
type WeightRow struct {
	// Key is the dimension's config key.
	Key DimensionKey
	// Label is the human-readable display name for this dimension.
	Label string
	// BaseWeight is the configured weight before any genre adjustment.
	BaseWeight float64
	// Multiplier is the blended genre multiplier applied to this dimension
	// (1.0 for bias-resistant dimensions or when no genres have an opinion).
	Multiplier float64
	// FinalWeight is the weight after genre adjustment, renormalization, and
	// any weight overrides. This is the value used in the final score formula.
	FinalWeight float64
	// Skipped indicates this dimension was excluded from the weight pool.
	Skipped bool
	// BiasResistant indicates this dimension's multiplier is always 1.0.
	BiasResistant bool
	// WeightOverride indicates the final weight was set by a weight override
	// rather than derived from the base weight and genre multipliers.
	WeightOverride bool
	// PrimaryGenreMultiplier is the raw multiplier the primary genre defines for
	// this dimension (0 when no primary genre is set or the dimension is
	// bias-resistant; 0 also when the primary genre has no config entry for it).
	PrimaryGenreMultiplier float64
}
