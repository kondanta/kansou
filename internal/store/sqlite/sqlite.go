// Package sqlite implements the Store interface backed by a SQLite database.
// It uses modernc.org/sqlite (pure-Go, no CGO) and golang-migrate for schema
// migrations. The database file path is configurable; ~ is expanded.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	migrate "github.com/golang-migrate/migrate/v4"
	migsqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jmoiron/sqlx"

	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
	"github.com/kondanta/kansou/internal/store"
)

// SQLiteStore implements store.Store backed by a SQLite file.
type SQLiteStore struct {
	db *sqlx.DB
}

// New opens (or creates) a SQLite database at path, runs migrations, enables
// WAL mode and foreign key enforcement, and returns a ready SQLiteStore.
func New(path string) (*SQLiteStore, error) {
	expanded, err := expandPath(path)
	if err != nil {
		return nil, fmt.Errorf("expanding db path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(expanded), 0o755); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	db, err := sql.Open("sqlite", expanded)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite db: %w", err)
	}
	// Single connection prevents pragma leakage across pool connections.
	db.SetMaxOpenConns(1)

	if err := setPragmas(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := runMigrations(db, expanded); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &SQLiteStore{db: sqlx.NewDb(db, "sqlite")}, nil
}

func expandPath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("getting home dir: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}
	return path, nil
}

func setPragmas(db *sql.DB) error {
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("setting WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return fmt.Errorf("enabling foreign keys: %w", err)
	}
	return nil
}

func runMigrations(db *sql.DB, dbPath string) error {
	driver, err := migsqlite.WithInstance(db, &migsqlite.Config{DatabaseName: dbPath})
	if err != nil {
		return fmt.Errorf("creating migration driver: %w", err)
	}
	src, err := iofs.New(store.MigrationsFS, "migrations/sqlite")
	if err != nil {
		return fmt.Errorf("loading migration source: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("initialising migrations: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("running migrations: %w", err)
	}
	return nil
}

// LoadScoringConfig reads the current scoring config from the database.
// Returns config.Load("") defaults when the dimensions table is empty.
func (s *SQLiteStore) LoadScoringConfig(ctx context.Context) (*config.Config, error) {
	var scalars struct {
		PrimaryGenreWeight float64 `db:"primary_genre_weight"`
		MaxMultiplier      float64 `db:"max_multiplier"`
		MaxHistory         int     `db:"max_history"`
	}
	const scalarQ = `SELECT primary_genre_weight, max_multiplier, max_history
	                 FROM config_scalars WHERE id = 1`
	if err := s.db.GetContext(ctx, &scalars, scalarQ); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Defensive: seed row was removed manually. Fall back to file defaults.
			return config.Load("")
		}
		return nil, fmt.Errorf("loading config scalars: %w", err)
	}

	type dimRow struct {
		Key           string  `db:"key"`
		Label         string  `db:"label"`
		Description   string  `db:"description"`
		Weight        float64 `db:"weight"`
		BiasResistant bool    `db:"bias_resistant"`
		SortOrder     int     `db:"sort_order"`
	}
	var dimRows []dimRow
	const dimQ = `SELECT key, label, description, weight, bias_resistant, sort_order
	              FROM dimensions ORDER BY sort_order`
	if err := s.db.SelectContext(ctx, &dimRows, dimQ); err != nil {
		return nil, fmt.Errorf("loading dimensions: %w", err)
	}
	if len(dimRows) == 0 {
		// First run: no dimensions seeded yet.
		return config.Load("")
	}

	type genreRow struct {
		Genre        string  `db:"genre"`
		DimensionKey string  `db:"dimension_key"`
		Multiplier   float64 `db:"multiplier"`
	}
	var genreRows []genreRow
	const genreQ = `SELECT genre, dimension_key, multiplier FROM genre_multipliers`
	if err := s.db.SelectContext(ctx, &genreRows, genreQ); err != nil {
		return nil, fmt.Errorf("loading genre multipliers: %w", err)
	}

	dims := make(map[string]config.DimensionDef, len(dimRows))
	order := make([]string, len(dimRows))
	for i, r := range dimRows {
		dims[r.Key] = config.DimensionDef{
			Label:         r.Label,
			Description:   r.Description,
			Weight:        r.Weight,
			BiasResistant: r.BiasResistant,
		}
		order[i] = r.Key
	}

	genres := make(map[string]map[string]float64)
	for _, r := range genreRows {
		if genres[r.Genre] == nil {
			genres[r.Genre] = make(map[string]float64)
		}
		genres[r.Genre][r.DimensionKey] = r.Multiplier
	}

	cfg := &config.Config{
		DimensionOrder:     order,
		Dimensions:         dims,
		Genres:             genres,
		MaxMultiplier:      scalars.MaxMultiplier,
		PrimaryGenreWeight: scalars.PrimaryGenreWeight,
		MaxHistory:         scalars.MaxHistory,
	}
	cfg.DimensionsHash = config.HashDimensions(cfg.Dimensions, cfg.DimensionOrder)
	return cfg, nil
}

// SaveScoringConfig persists the full scoring config in a single transaction.
// The existing dimensions and genre_multipliers rows are replaced atomically.
func (s *SQLiteStore) SaveScoringConfig(ctx context.Context, cfg *config.Config) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const scalQ = `UPDATE config_scalars
	               SET primary_genre_weight = ?, max_multiplier = ?, max_history = ?
	               WHERE id = 1`
	if _, err := tx.ExecContext(ctx, scalQ,
		cfg.PrimaryGenreWeight, cfg.MaxMultiplier, cfg.MaxHistory); err != nil {
		return fmt.Errorf("updating config scalars: %w", err)
	}

	// DELETE cascades to genre_multipliers via FK.
	if _, err := tx.ExecContext(ctx, `DELETE FROM dimensions`); err != nil {
		return fmt.Errorf("deleting dimensions: %w", err)
	}

	const dimQ = `INSERT INTO dimensions (key, label, description, weight, bias_resistant, sort_order)
	              VALUES (?, ?, ?, ?, ?, ?)`
	for i, key := range cfg.DimensionOrder {
		d := cfg.Dimensions[key]
		if _, err := tx.ExecContext(ctx, dimQ,
			key, d.Label, d.Description, d.Weight, d.BiasResistant, i); err != nil {
			return fmt.Errorf("inserting dimension %q: %w", key, err)
		}
	}

	const genreQ = `INSERT INTO genre_multipliers (genre, dimension_key, multiplier)
	                VALUES (?, ?, ?)`
	for genre, mults := range cfg.Genres {
		for dimKey, mult := range mults {
			if _, err := tx.ExecContext(ctx, genreQ, genre, dimKey, mult); err != nil {
				return fmt.Errorf("inserting genre multiplier %q/%q: %w", genre, dimKey, err)
			}
		}
	}

	return tx.Commit()
}

// SaveScore saves a completed scoring session atomically across all four tables:
// media, scores, dimension_scores, and score_matched_genres.
func (s *SQLiteStore) SaveScore(ctx context.Context, result scoring.Result, cfg *config.Config, maxHistory int) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Upsert media row — titles and format may change across rescores.
	const mediaQ = `INSERT INTO media (anilist_id, title_romaji, title_english, media_type, format, updated_at)
	                VALUES (?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
	                ON CONFLICT(anilist_id) DO UPDATE SET
	                    title_romaji  = excluded.title_romaji,
	                    title_english = excluded.title_english,
	                    media_type    = excluded.media_type,
	                    format        = excluded.format,
	                    updated_at    = excluded.updated_at`
	if _, err := tx.ExecContext(ctx, mediaQ,
		result.Meta.MediaID,
		result.Meta.TitleRomaji,
		result.Meta.TitleEnglish,
		string(result.Meta.MediaType),
		result.Meta.Format,
	); err != nil {
		return fmt.Errorf("upserting media: %w", err)
	}

	var mediaRowID int
	if err := tx.GetContext(ctx, &mediaRowID,
		`SELECT id FROM media WHERE anilist_id = ?`, result.Meta.MediaID,
	); err != nil {
		return fmt.Errorf("fetching media row id: %w", err)
	}

	// Replace media genres — the genre list can change between AniList syncs.
	if _, err := tx.ExecContext(ctx, `DELETE FROM media_genres WHERE media_id = ?`, mediaRowID); err != nil {
		return fmt.Errorf("clearing media genres: %w", err)
	}
	const mgQ = `INSERT OR IGNORE INTO media_genres (media_id, genre) VALUES (?, ?)`
	for _, g := range result.Meta.AllGenres {
		if _, err := tx.ExecContext(ctx, mgQ, mediaRowID, g); err != nil {
			return fmt.Errorf("inserting media genre %q: %w", g, err)
		}
	}

	snapshotBytes, err := json.Marshal(store.BuildConfigSnapshot(cfg))
	if err != nil {
		return fmt.Errorf("marshalling config snapshot: %w", err)
	}

	var userSelectedGenresStr *string
	if len(result.Meta.UserSelectedGenres) > 0 {
		usgBytes, marshalErr := json.Marshal(result.Meta.UserSelectedGenres)
		if marshalErr != nil {
			return fmt.Errorf("marshalling user_selected_genres: %w", marshalErr)
		}
		usg := string(usgBytes)
		userSelectedGenresStr = &usg
	}

	var primaryGenreStr *string
	if result.Meta.PrimaryGenre != "" {
		pg := result.Meta.PrimaryGenre
		primaryGenreStr = &pg
	}

	var primaryGenreWeightPtr *float64
	if result.Meta.PrimaryGenre != "" {
		pgw := result.Meta.PrimaryGenreWeight
		primaryGenreWeightPtr = &pgw
	}

	// Unset is_latest on the previous latest score before inserting the new one.
	if _, err := tx.ExecContext(ctx,
		`UPDATE scores SET is_latest = 0 WHERE media_id = ? AND is_latest = 1`,
		mediaRowID,
	); err != nil {
		return fmt.Errorf("unsetting previous is_latest: %w", err)
	}

	const scoreQ = `INSERT INTO scores
	                    (media_id, final_score, primary_genre, primary_genre_weight,
	                     config_hash, config_snapshot, user_selected_genres, is_latest)
	                VALUES (?, ?, ?, ?, ?, ?, ?, 1)`
	scoreRes, err := tx.ExecContext(ctx, scoreQ,
		mediaRowID,
		result.FinalScore,
		primaryGenreStr,
		primaryGenreWeightPtr,
		result.Meta.ConfigHash,
		string(snapshotBytes),
		userSelectedGenresStr,
	)
	if err != nil {
		return fmt.Errorf("inserting score: %w", err)
	}
	scoreID, err := scoreRes.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting score id: %w", err)
	}

	const dimQ = `INSERT INTO dimension_scores
	                  (score_id, dimension_key, label, score, base_weight, final_weight,
	                   applied_multiplier, contribution, skipped, bias_resistant, weight_override, genre_deselected)
	              VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for _, row := range result.Breakdown {
		var scoreVal, contribVal *float64
		if !row.Skipped {
			sv, cv := row.Score, row.Contribution
			scoreVal, contribVal = &sv, &cv
		}
		if _, err := tx.ExecContext(ctx, dimQ,
			scoreID, row.Key, row.Label,
			scoreVal, row.BaseWeight, row.FinalWeight, row.AppliedMultiplier, contribVal,
			boolToInt(row.Skipped), boolToInt(row.BiasResistant),
			boolToInt(row.WeightOverride), boolToInt(row.GenreDeselected),
		); err != nil {
			return fmt.Errorf("inserting dimension score %q: %w", row.Key, err)
		}
	}

	const matchQ = `INSERT INTO score_matched_genres (score_id, genre, is_primary) VALUES (?, ?, ?)`
	primaryLower := strings.ToLower(result.Meta.PrimaryGenre)
	for _, genre := range result.Meta.GenresActive {
		isPrimary := primaryLower != "" && genre == primaryLower
		if _, err := tx.ExecContext(ctx, matchQ, scoreID, genre, boolToInt(isPrimary)); err != nil {
			return fmt.Errorf("inserting matched genre %q: %w", genre, err)
		}
	}

	// Apply max_history retention: 0 = keep 1 (latest only), N = keep N, -1 = keep all.
	if maxHistory >= 0 {
		keepCount := maxHistory
		if keepCount == 0 {
			keepCount = 1
		}
		const pruneQ = `UPDATE scores
		                SET deleted_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		                WHERE media_id = ? AND deleted_at IS NULL
		                  AND id NOT IN (
		                      SELECT id FROM scores
		                      WHERE media_id = ? AND deleted_at IS NULL
		                      ORDER BY scored_at DESC LIMIT ?
		                  )`
		if _, err := tx.ExecContext(ctx, pruneQ, mediaRowID, mediaRowID, keepCount); err != nil {
			return fmt.Errorf("applying max_history: %w", err)
		}
	}

	return tx.Commit()
}

// boolToInt converts a bool to 1 or 0 for SQLite INTEGER columns.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// scoreRow is the scanning target for the scores+media JOIN query.
// ScoredAt is TEXT (ISO 8601) in SQLite; callers parse it via time.RFC3339.
type scoreRow struct {
	ID                 int      `db:"id"`
	MediaID            int      `db:"media_id"`
	TitleRomaji        string   `db:"title_romaji"`
	TitleEnglish       string   `db:"title_english"`
	MediaType          string   `db:"media_type"`
	Format             string   `db:"format"`
	FinalScore         float64  `db:"final_score"`
	PrimaryGenre       *string  `db:"primary_genre"`
	PrimaryGenreWeight *float64 `db:"primary_genre_weight"`
	ConfigHash         string   `db:"config_hash"`
	IsLatest           bool     `db:"is_latest"`
	ScoredAt           string   `db:"scored_at"`
	UserSelectedGenres *string  `db:"user_selected_genres"` // JSON text; nil when SQL NULL
}

// dimRow is the scanning target for dimension_scores queries.
type dimRow struct {
	DimensionKey      string   `db:"dimension_key"`
	Label             string   `db:"label"`
	Score             *float64 `db:"score"`
	BaseWeight        float64  `db:"base_weight"`
	FinalWeight       float64  `db:"final_weight"`
	AppliedMultiplier float64  `db:"applied_multiplier"`
	Contribution      *float64 `db:"contribution"`
	Skipped           bool     `db:"skipped"`
	BiasResistant     bool     `db:"bias_resistant"`
	WeightOverride    bool     `db:"weight_override"`
	GenreDeselected   bool     `db:"genre_deselected"`
}

// matchRow is the scanning target for score_matched_genres queries.
type matchRow struct {
	Genre     string `db:"genre"`
	IsPrimary bool   `db:"is_primary"`
}

// LatestScore returns the most recent non-deleted score for a given AniList media ID.
// Returns nil, nil when no score exists for the given ID.
func (s *SQLiteStore) LatestScore(ctx context.Context, anilistID int) (*Score, error) {
	const q = `SELECT s.id, s.media_id, m.title_romaji, m.title_english, m.media_type, m.format,
	                  s.final_score, s.primary_genre, s.primary_genre_weight, s.config_hash,
	                  s.is_latest, s.scored_at, s.user_selected_genres
	           FROM scores s
	           JOIN media m ON m.id = s.media_id
	           WHERE m.anilist_id = ? AND s.is_latest = 1 AND s.deleted_at IS NULL
	           LIMIT 1`
	var row scoreRow
	if err := s.db.GetContext(ctx, &row, q, anilistID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("fetching latest score: %w", err)
	}
	return s.assembleScore(ctx, anilistID, &row)
}

// assembleScore loads sub-tables and builds a complete store.Score from a scoreRow.
// Used by LatestScore and ScoreHistory.
func (s *SQLiteStore) assembleScore(ctx context.Context, anilistID int, row *scoreRow) (*Score, error) {
	scoredAt, err := time.Parse(time.RFC3339, row.ScoredAt)
	if err != nil {
		return nil, fmt.Errorf("parsing scored_at %q: %w", row.ScoredAt, err)
	}
	genres, err := s.fetchMediaGenres(ctx, row.MediaID)
	if err != nil {
		return nil, err
	}
	breakdown, err := s.fetchDimensionScores(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	activeGenres, err := s.fetchMatchedGenres(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	var userSelectedGenres []string
	if row.UserSelectedGenres != nil {
		if err := json.Unmarshal([]byte(*row.UserSelectedGenres), &userSelectedGenres); err != nil {
			return nil, fmt.Errorf("decoding user_selected_genres: %w", err)
		}
	}
	sc := &Score{
		ID:                 row.ID,
		AnilistID:          anilistID,
		TitleRomaji:        row.TitleRomaji,
		TitleEnglish:       row.TitleEnglish,
		MediaType:          row.MediaType,
		Format:             row.Format,
		Genres:             genres,
		FinalScore:         row.FinalScore,
		ConfigHash:         row.ConfigHash,
		IsLatest:           row.IsLatest,
		ScoredAt:           scoredAt,
		Breakdown:          breakdown,
		ActiveGenres:       activeGenres,
		UserSelectedGenres: userSelectedGenres,
	}
	if row.PrimaryGenre != nil {
		sc.PrimaryGenre = *row.PrimaryGenre
	}
	if row.PrimaryGenreWeight != nil {
		sc.PrimaryGenreWeight = *row.PrimaryGenreWeight
	}
	return sc, nil
}

// fetchMediaGenres returns the genre list for a media row.
func (s *SQLiteStore) fetchMediaGenres(ctx context.Context, mediaID int) ([]string, error) {
	var genres []string
	const q = `SELECT genre FROM media_genres WHERE media_id = ? ORDER BY genre`
	if err := s.db.SelectContext(ctx, &genres, q, mediaID); err != nil {
		return nil, fmt.Errorf("fetching media genres: %w", err)
	}
	return genres, nil
}

// fetchDimensionScores returns the breakdown rows for a score.
func (s *SQLiteStore) fetchDimensionScores(ctx context.Context, scoreID int) ([]store.DimensionScoreRow, error) {
	const q = `SELECT dimension_key, label, score, base_weight, final_weight, applied_multiplier,
	                  contribution, skipped, bias_resistant, weight_override, genre_deselected
	           FROM dimension_scores WHERE score_id = ?`
	var rows []dimRow
	if err := s.db.SelectContext(ctx, &rows, q, scoreID); err != nil {
		return nil, fmt.Errorf("fetching dimension scores: %w", err)
	}
	result := make([]store.DimensionScoreRow, len(rows))
	for i, r := range rows {
		result[i] = store.DimensionScoreRow{
			DimensionKey: r.DimensionKey, Label: r.Label, Score: r.Score,
			BaseWeight: r.BaseWeight, FinalWeight: r.FinalWeight,
			AppliedMultiplier: r.AppliedMultiplier, Contribution: r.Contribution,
			Skipped: r.Skipped, BiasResistant: r.BiasResistant,
			WeightOverride: r.WeightOverride, GenreDeselected: r.GenreDeselected,
		}
	}
	return result, nil
}

// fetchMatchedGenres returns the active genre rows for a score.
func (s *SQLiteStore) fetchMatchedGenres(ctx context.Context, scoreID int) ([]store.MatchedGenreRow, error) {
	const q = `SELECT genre, is_primary FROM score_matched_genres WHERE score_id = ?`
	var rows []matchRow
	if err := s.db.SelectContext(ctx, &rows, q, scoreID); err != nil {
		return nil, fmt.Errorf("fetching matched genres: %w", err)
	}
	result := make([]store.MatchedGenreRow, len(rows))
	for i, r := range rows {
		result[i] = store.MatchedGenreRow{Genre: r.Genre, IsPrimary: r.IsPrimary}
	}
	return result, nil
}

// ScoreHistory returns all non-deleted scores for a given AniList media ID.
func (s *SQLiteStore) ScoreHistory(ctx context.Context, anilistID int) ([]Score, error) {
	return nil, errors.New("not implemented")
}

// ListLatest returns the latest score for every media entry.
func (s *SQLiteStore) ListLatest(ctx context.Context) ([]Score, error) {
	return nil, errors.New("not implemented")
}

// SoftDeleteScore sets deleted_at = now() on the given score ID.
func (s *SQLiteStore) SoftDeleteScore(ctx context.Context, scoreID int) error {
	return errors.New("not implemented")
}

// Prune hard-deletes all soft-deleted score rows and any media entries with no
// remaining scores. The prune timestamp is recorded in db_metadata before
// deletion so it survives even if zero rows are deleted.
// Returns the number of score rows hard-deleted.
func (s *SQLiteStore) Prune(ctx context.Context) (int64, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const metaQ = `INSERT OR REPLACE INTO db_metadata (key, value)
	               VALUES ('last_prune_at', strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))`
	if _, err := tx.ExecContext(ctx, metaQ); err != nil {
		return 0, fmt.Errorf("recording prune timestamp: %w", err)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM scores WHERE deleted_at IS NOT NULL`)
	if err != nil {
		return 0, fmt.Errorf("deleting soft-deleted scores: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("reading rows affected: %w", err)
	}

	const mediaQ = `DELETE FROM media WHERE id NOT IN (SELECT DISTINCT media_id FROM scores)`
	if _, err := tx.ExecContext(ctx, mediaQ); err != nil {
		return 0, fmt.Errorf("deleting orphaned media: %w", err)
	}

	return n, tx.Commit()
}

// LastPruneAt returns the timestamp of the last prune operation.
// Returns nil, nil if Prune has never run.
func (s *SQLiteStore) LastPruneAt(ctx context.Context) (*time.Time, error) {
	var val string
	err := s.db.GetContext(ctx, &val, `SELECT value FROM db_metadata WHERE key = 'last_prune_at'`)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching last_prune_at: %w", err)
	}
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return nil, fmt.Errorf("parsing last_prune_at %q: %w", val, err)
	}
	return &t, nil
}

// GenreBreakdown returns the count and percentage of entries per genre.
func (s *SQLiteStore) GenreBreakdown(ctx context.Context) ([]store.GenreStat, error) {
	return nil, errors.New("not implemented")
}

// ScoreByGenre returns the average final score per genre.
func (s *SQLiteStore) ScoreByGenre(ctx context.Context) ([]store.GenreScore, error) {
	return nil, errors.New("not implemented")
}

// GenreDimensionAffinity returns average dimension scores grouped by genre.
func (s *SQLiteStore) GenreDimensionAffinity(ctx context.Context) ([]store.GenreDimensionAffinity, error) {
	return nil, errors.New("not implemented")
}

// DimensionVariance returns the standard deviation of scores per dimension.
func (s *SQLiteStore) DimensionVariance(ctx context.Context) ([]store.DimensionVarianceStat, error) {
	return nil, errors.New("not implemented")
}

// ScoringConsistency returns the average standard deviation across all dimensions.
func (s *SQLiteStore) ScoringConsistency(ctx context.Context) (*store.ConsistencyStat, error) {
	return nil, errors.New("not implemented")
}

// DimensionCorrelation returns Pearson correlation coefficients between dimension pairs.
func (s *SQLiteStore) DimensionCorrelation(ctx context.Context) ([]store.DimensionCorrelationStat, error) {
	return nil, errors.New("not implemented")
}

// SkippedDimensions returns how often each dimension is skipped by media type.
func (s *SQLiteStore) SkippedDimensions(ctx context.Context) ([]store.SkippedDimStat, error) {
	return nil, errors.New("not implemented")
}

// WeightOverrides returns how often each dimension has been weight-overridden.
func (s *SQLiteStore) WeightOverrides(ctx context.Context) ([]store.WeightOverrideStat, error) {
	return nil, errors.New("not implemented")
}

// MostRescored returns entries ordered by rescore count descending.
func (s *SQLiteStore) MostRescored(ctx context.Context) ([]store.RescoredStat, error) {
	return nil, errors.New("not implemented")
}

// Outliers returns entries with dimension scores deviating more than 2 std devs.
func (s *SQLiteStore) Outliers(ctx context.Context) ([]store.OutlierStat, error) {
	return nil, errors.New("not implemented")
}

// ConfigImpact returns average score before and after each config change.
func (s *SQLiteStore) ConfigImpact(ctx context.Context) ([]store.ConfigImpactStat, error) {
	return nil, errors.New("not implemented")
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// Score is a type alias so method signatures in this file match store.Score.
type Score = store.Score
