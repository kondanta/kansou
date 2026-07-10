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
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	migrate "github.com/golang-migrate/migrate/v4"
	migsqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jmoiron/sqlx"
	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/logger"
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
	slog.Debug("sqlite: opening store", "path", path)
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
		slog.Error("sqlite: migrations failed", "path", expanded, "err", err)
		return nil, err
	}

	slog.Info("sqlite: store ready", "path", expanded)
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
// Returns store.ErrNotSeeded when the dimensions table is empty.
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
			// Defensive: seed row was removed manually — treat like unseeded.
			return nil, store.ErrNotSeeded
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
		return nil, store.ErrNotSeeded
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
	log := logger.FromContext(ctx)
	log.Debug("sqlite: saving scoring config", "dimensions", len(cfg.Dimensions))
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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing scoring config: %w", err)
	}
	log.Info("sqlite: scoring config saved")
	return nil
}

// SaveScore saves a completed scoring session atomically across all four tables:
// media, scores, dimension_scores, and score_matched_genres.
func (s *SQLiteStore) SaveScore(
	ctx context.Context,
	result scoring.Result,
	cfg *config.Config,
	maxHistory int,
) error {
	log := logger.FromContext(ctx)
	log.Debug(
		"sqlite: saving score",
		"media_id",
		result.Meta.MediaID,
		"final_score",
		result.FinalScore,
	)
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	mediaRowID, err := upsertMedia(ctx, tx, result.Meta)
	if err != nil {
		return err
	}

	if err := replaceMediaGenres(ctx, tx, mediaRowID, result.Meta.AllGenres); err != nil {
		return err
	}

	scoreID, err := insertScoreRow(ctx, tx, mediaRowID, result, cfg)
	if err != nil {
		return err
	}

	if err := insertDimensionScoreRows(ctx, tx, scoreID, result.Breakdown); err != nil {
		return err
	}

	if err := insertMatchedGenres(ctx, tx, scoreID, result.Meta); err != nil {
		return err
	}

	if err := pruneScoreHistory(ctx, tx, mediaRowID, maxHistory); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing score: %w", err)
	}
	log.Info("sqlite: score saved", "media_id", result.Meta.MediaID)
	return nil
}

// upsertMedia inserts or updates the media row and returns its row id.
// Titles, format, and cover image may change across rescores.
func upsertMedia(ctx context.Context, tx *sqlx.Tx, meta scoring.SessionMeta) (int, error) {
	const mediaQ = `INSERT INTO media (anilist_id, title_romaji, title_english, media_type, format, cover_image, updated_at)
	                VALUES (?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
	                ON CONFLICT(anilist_id) DO UPDATE SET
	                    title_romaji  = excluded.title_romaji,
	                    title_english = excluded.title_english,
	                    media_type    = excluded.media_type,
	                    format        = excluded.format,
	                    cover_image   = excluded.cover_image,
	                    updated_at    = excluded.updated_at`
	if _, err := tx.ExecContext(ctx, mediaQ,
		meta.MediaID,
		meta.TitleRomaji,
		meta.TitleEnglish,
		string(meta.MediaType),
		meta.Format,
		meta.CoverImage,
	); err != nil {
		return 0, fmt.Errorf("upserting media: %w", err)
	}

	var mediaRowID int
	if err := tx.GetContext(ctx, &mediaRowID,
		`SELECT id FROM media WHERE anilist_id = ?`, meta.MediaID,
	); err != nil {
		return 0, fmt.Errorf("fetching media row id: %w", err)
	}
	return mediaRowID, nil
}

// replaceMediaGenres clears and re-inserts a media row's genres, since the
// genre list can change between AniList syncs.
func replaceMediaGenres(
	ctx context.Context,
	tx *sqlx.Tx,
	mediaRowID int,
	genres []string,
) error {
	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM media_genres WHERE media_id = ?`,
		mediaRowID,
	); err != nil {
		return fmt.Errorf("clearing media genres: %w", err)
	}
	const mgQ = `INSERT OR IGNORE INTO media_genres (media_id, genre) VALUES (?, ?)`
	for _, g := range genres {
		if _, err := tx.ExecContext(ctx, mgQ, mediaRowID, g); err != nil {
			return fmt.Errorf("inserting media genre %q: %w", g, err)
		}
	}
	return nil
}

// insertScore marks any previous latest score for the media as no longer
// latest, inserts the new score row, and returns its id.
func insertScoreRow(
	ctx context.Context, tx *sqlx.Tx, mediaRowID int, result scoring.Result, cfg *config.Config,
) (int64, error) {
	snapshotBytes, err := json.Marshal(store.BuildConfigSnapshot(cfg))
	if err != nil {
		return 0, fmt.Errorf("marshalling config snapshot: %w", err)
	}

	var userSelectedGenresStr *string
	if len(result.Meta.UserSelectedGenres) > 0 {
		usgBytes, marshalErr := json.Marshal(result.Meta.UserSelectedGenres)
		if marshalErr != nil {
			return 0, fmt.Errorf("marshalling user_selected_genres: %w", marshalErr)
		}
		usg := string(usgBytes)
		userSelectedGenresStr = &usg
	}

	var primaryGenreStr *string
	var primaryGenreWeightPtr *float64
	if result.Meta.PrimaryGenre != "" {
		pg := result.Meta.PrimaryGenre
		primaryGenreStr = &pg
		pgw := result.Meta.PrimaryGenreWeight
		primaryGenreWeightPtr = &pgw
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE scores SET is_latest = 0 WHERE media_id = ? AND is_latest = 1`,
		mediaRowID,
	); err != nil {
		return 0, fmt.Errorf("unsetting previous is_latest: %w", err)
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
		config.Hash(cfg),
		string(snapshotBytes),
		userSelectedGenresStr,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting score: %w", err)
	}
	scoreID, err := scoreRes.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting score id: %w", err)
	}
	return scoreID, nil
}

// insertDimensionScores writes one dimension_scores row per breakdown entry.
func insertDimensionScoreRows(
	ctx context.Context,
	tx *sqlx.Tx,
	scoreID int64,
	breakdown []scoring.BreakdownRow,
) error {
	const dimQ = `INSERT INTO dimension_scores
	                  (score_id, dimension_key, label, score, base_weight, final_weight,
	                   applied_multiplier, contribution, skipped, bias_resistant, weight_override, genre_deselected,
	                   primary_genre_multiplier, secondary_genres_multiplier)
	              VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for _, row := range breakdown {
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
			row.PrimaryGenreMultiplier, row.SecondaryGenresMultiplier,
		); err != nil {
			return fmt.Errorf("inserting dimension score %q: %w", row.Key, err)
		}
	}
	return nil
}

// insertMatchedGenres writes one score_matched_genres row per active genre,
// flagging whichever one (if any) was designated primary.
func insertMatchedGenres(
	ctx context.Context,
	tx *sqlx.Tx,
	scoreID int64,
	meta scoring.SessionMeta,
) error {
	const matchQ = `INSERT INTO score_matched_genres (score_id, genre, is_primary) VALUES (?, ?, ?)`
	primaryLower := strings.ToLower(meta.PrimaryGenre)
	for _, genre := range meta.GenresActive {
		isPrimary := primaryLower != "" && genre == primaryLower
		if _, err := tx.ExecContext(ctx, matchQ, scoreID, genre, boolToInt(isPrimary)); err != nil {
			return fmt.Errorf("inserting matched genre %q: %w", genre, err)
		}
	}
	return nil
}

// pruneScoreHistory soft-deletes scores beyond the retention window.
// 0 keeps only the latest, N keeps the N most recent, -1 keeps all.
func pruneScoreHistory(ctx context.Context, tx *sqlx.Tx, mediaRowID, maxHistory int) error {
	if maxHistory < 0 {
		return nil
	}
	keepCount := maxHistory
	if keepCount == 0 {
		keepCount = 1
	}
	const pruneQ = `UPDATE scores
	                SET deleted_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), deleted_reason = ?
	                WHERE media_id = ? AND deleted_at IS NULL
	                  AND id NOT IN (
	                      SELECT id FROM scores
	                      WHERE media_id = ? AND deleted_at IS NULL
	                      ORDER BY scored_at DESC LIMIT ?
	                  )`
	if _, err := tx.ExecContext(
		ctx,
		pruneQ,
		store.DeletedReasonMaxHistory,
		mediaRowID,
		mediaRowID,
		keepCount,
	); err != nil {
		return fmt.Errorf("applying max_history: %w", err)
	}
	return nil
}

// boolToInt converts a bool to 1 or 0 for SQLite INTEGER columns.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// parseRFC3339 parses a SQLite TEXT timestamp column value, wrapping the
// error with the column name for easier diagnosis.
func parseRFC3339(field, val string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing %s %q: %w", field, val, err)
	}
	return t, nil
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
	CoverImage         *string  `db:"cover_image"`
	ScoredAt           string   `db:"scored_at"`
	UserSelectedGenres *string  `db:"user_selected_genres"` // JSON text; nil when SQL NULL
}

// dimRow is the scanning target for dimension_scores queries.
type dimRow struct {
	DimensionKey              string   `db:"dimension_key"`
	Label                     string   `db:"label"`
	Score                     *float64 `db:"score"`
	BaseWeight                float64  `db:"base_weight"`
	FinalWeight               float64  `db:"final_weight"`
	AppliedMultiplier         float64  `db:"applied_multiplier"`
	Contribution              *float64 `db:"contribution"`
	Skipped                   bool     `db:"skipped"`
	BiasResistant             bool     `db:"bias_resistant"`
	WeightOverride            bool     `db:"weight_override"`
	GenreDeselected           bool     `db:"genre_deselected"`
	PrimaryGenreMultiplier    float64  `db:"primary_genre_multiplier"`
	SecondaryGenresMultiplier float64  `db:"secondary_genres_multiplier"`
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
	                  m.cover_image, s.final_score, s.primary_genre, s.primary_genre_weight, s.config_hash,
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
func (s *SQLiteStore) assembleScore(
	ctx context.Context,
	anilistID int,
	row *scoreRow,
) (*Score, error) {
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
	if row.CoverImage != nil {
		sc.CoverImage = *row.CoverImage
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
func (s *SQLiteStore) fetchDimensionScores(
	ctx context.Context,
	scoreID int,
) ([]store.DimensionScoreRow, error) {
	const q = `SELECT dimension_key, label, score, base_weight, final_weight, applied_multiplier,
	                  contribution, skipped, bias_resistant, weight_override, genre_deselected,
	                  primary_genre_multiplier, secondary_genres_multiplier
	           FROM dimension_scores WHERE score_id = ?`
	var rows []dimRow
	if err := s.db.SelectContext(ctx, &rows, q, scoreID); err != nil {
		return nil, fmt.Errorf("fetching dimension scores: %w", err)
	}
	result := make([]store.DimensionScoreRow, len(rows))
	for i, r := range rows {
		result[i] = store.DimensionScoreRow{
			DimensionKey:              r.DimensionKey,
			Label:                     r.Label,
			Score:                     r.Score,
			BaseWeight:                r.BaseWeight,
			FinalWeight:               r.FinalWeight,
			AppliedMultiplier:         r.AppliedMultiplier,
			Contribution:              r.Contribution,
			Skipped:                   r.Skipped,
			BiasResistant:             r.BiasResistant,
			WeightOverride:            r.WeightOverride,
			GenreDeselected:           r.GenreDeselected,
			PrimaryGenreMultiplier:    r.PrimaryGenreMultiplier,
			SecondaryGenresMultiplier: r.SecondaryGenresMultiplier,
		}
	}
	return result, nil
}

// fetchMatchedGenres returns the active genre rows for a score.
func (s *SQLiteStore) fetchMatchedGenres(
	ctx context.Context,
	scoreID int,
) ([]store.MatchedGenreRow, error) {
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

// ScoreHistory returns all non-deleted scores for a given AniList media ID,
// ordered by scored_at DESC.
func (s *SQLiteStore) ScoreHistory(ctx context.Context, anilistID int) ([]Score, error) {
	const q = `SELECT s.id, s.media_id, m.title_romaji, m.title_english, m.media_type, m.format,
	                  m.cover_image, s.final_score, s.primary_genre, s.primary_genre_weight, s.config_hash,
	                  s.is_latest, s.scored_at, s.user_selected_genres
	           FROM scores s
	           JOIN media m ON m.id = s.media_id
	           WHERE m.anilist_id = ? AND s.deleted_at IS NULL
	           ORDER BY s.scored_at DESC`
	var rows []scoreRow
	if err := s.db.SelectContext(ctx, &rows, q, anilistID); err != nil {
		return nil, fmt.Errorf("fetching score history: %w", err)
	}
	result := make([]Score, len(rows))
	for i, row := range rows {
		sc, err := s.assembleScore(ctx, anilistID, &row)
		if err != nil {
			return nil, err
		}
		result[i] = *sc
	}
	return result, nil
}

// listLatestRow is the scanning target for ListLatest, which — unlike
// LatestScore/ScoreHistory — spans every media entry at once and so must
// select anilist_id itself rather than receiving it as a parameter.
type listLatestRow struct {
	ID                 int      `db:"id"`
	AnilistID          int      `db:"anilist_id"`
	TitleRomaji        string   `db:"title_romaji"`
	TitleEnglish       string   `db:"title_english"`
	MediaType          string   `db:"media_type"`
	Format             string   `db:"format"`
	FinalScore         float64  `db:"final_score"`
	PrimaryGenre       *string  `db:"primary_genre"`
	PrimaryGenreWeight *float64 `db:"primary_genre_weight"`
	ConfigHash         string   `db:"config_hash"`
	IsLatest           bool     `db:"is_latest"`
	EntryCount         int      `db:"entry_count"`
	CoverImage         *string  `db:"cover_image"`
	ScoredAt           string   `db:"scored_at"`
}

// ListLatest returns the latest score for every media entry, ordered by
// scored_at DESC. Excludes soft-deleted scores. Does NOT populate Breakdown
// or ActiveGenres — use LatestScore or ScoreHistory when the full breakdown
// is needed; loading it for every entry here would require an expensive JOIN.
func (s *SQLiteStore) ListLatest(ctx context.Context) ([]Score, error) {
	const q = `SELECT s.id, m.anilist_id, m.title_romaji, m.title_english, m.media_type, m.format,
	                  m.cover_image, s.final_score, s.primary_genre, s.primary_genre_weight, s.config_hash,
	                  s.is_latest, s.scored_at,
	                  (SELECT COUNT(*) FROM scores s2
	                   WHERE s2.media_id = s.media_id AND s2.deleted_at IS NULL) AS entry_count
	           FROM scores s
	           JOIN media m ON m.id = s.media_id
	           WHERE s.is_latest = 1 AND s.deleted_at IS NULL
	           ORDER BY s.scored_at DESC`
	var rows []listLatestRow
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("fetching latest scores: %w", err)
	}
	result := make([]Score, len(rows))
	for i, r := range rows {
		scoredAt, err := parseRFC3339("scored_at", r.ScoredAt)
		if err != nil {
			return nil, err
		}
		sc := Score{
			ID:           r.ID,
			AnilistID:    r.AnilistID,
			TitleRomaji:  r.TitleRomaji,
			TitleEnglish: r.TitleEnglish,
			MediaType:    r.MediaType,
			Format:       r.Format,
			FinalScore:   r.FinalScore,
			ConfigHash:   r.ConfigHash,
			IsLatest:     r.IsLatest,
			EntryCount:   r.EntryCount,
			ScoredAt:     scoredAt,
		}
		if r.PrimaryGenre != nil {
			sc.PrimaryGenre = *r.PrimaryGenre
		}
		if r.PrimaryGenreWeight != nil {
			sc.PrimaryGenreWeight = *r.PrimaryGenreWeight
		}
		if r.CoverImage != nil {
			sc.CoverImage = *r.CoverImage
		}
		result[i] = sc
	}
	return result, nil
}

// SearchMediaByTitle returns media whose title_romaji matches query
// case-insensitively, ordered by title_romaji.
func (s *SQLiteStore) SearchMediaByTitle(
	ctx context.Context,
	query string,
) ([]store.MediaSearchResult, error) {
	type row struct {
		AnilistID    int    `db:"anilist_id"`
		TitleRomaji  string `db:"title_romaji"`
		TitleEnglish string `db:"title_english"`
		MediaType    string `db:"media_type"`
		Format       string `db:"format"`
	}
	const q = `SELECT anilist_id, title_romaji, title_english, media_type, format
	           FROM media
	           WHERE title_romaji LIKE '%' || ? || '%' COLLATE NOCASE
	           ORDER BY title_romaji`
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, q, query); err != nil {
		return nil, fmt.Errorf("searching media by title: %w", err)
	}
	result := make([]store.MediaSearchResult, len(rows))
	for i, r := range rows {
		result[i] = store.MediaSearchResult{
			AnilistID: r.AnilistID, TitleRomaji: r.TitleRomaji, TitleEnglish: r.TitleEnglish,
			MediaType: r.MediaType, Format: r.Format,
		}
	}
	return result, nil
}

// SoftDeleteScore sets deleted_at, deleted_reason = manual, and is_latest =
// false on the given score ID. Deliberate removal from active tracking — it
// does NOT promote any other score for the same media to is_latest. See
// store.DeletedReasonManual for why this never conflicts with max_history
// gardening in SaveScore.
func (s *SQLiteStore) SoftDeleteScore(ctx context.Context, scoreID int) error {
	log := logger.FromContext(ctx)
	res, err := s.db.ExecContext(ctx,
		`UPDATE scores
		 SET deleted_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), deleted_reason = ?, is_latest = 0
		 WHERE id = ? AND deleted_at IS NULL`,
		store.DeletedReasonManual, scoreID,
	)
	if err != nil {
		log.Error("sqlite: soft-delete failed", "score_id", scoreID, "err", err)
		return fmt.Errorf("soft-deleting score %d: %w", scoreID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("score %d: %w", scoreID, store.ErrScoreNotFound)
	}
	log.Info("sqlite: score soft-deleted", "score_id", scoreID)
	return nil
}

// HardDeleteScore permanently removes a score row from the database. This is irreversible and
// should only be used with extreme caution. It does not affect any other scores for the same media.
func (s *SQLiteStore) HardDeleteScore(ctx context.Context, scoreID int) error {
	log := logger.FromContext(ctx)
	log.Debug("sqlite: hard-deleting score", "score_id", scoreID)
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var mediaID int
	err = tx.GetContext(ctx, &mediaID, `SELECT media_id FROM scores WHERE id = ?`, scoreID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("score %d: %w", scoreID, store.ErrScoreNotFound)
		}
		return fmt.Errorf("looking up media_id for score %d: %w", scoreID, err)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM scores WHERE id = ?`, scoreID)
	if err != nil {
		return fmt.Errorf("hard-deleting score %d: %w", scoreID, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("score %d: %w", scoreID, store.ErrScoreNotFound)
	}

	const mediaQ = `DELETE FROM media WHERE id = ? AND NOT EXISTS (SELECT 1 FROM scores WHERE media_id = ?)`

	if _, err := tx.ExecContext(ctx, mediaQ, mediaID, mediaID); err != nil {
		return fmt.Errorf("deleting orphaned media %d: %w", mediaID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing hard delete transaction: %w", err)
	}

	log.Info("sqlite: score hard-deleted", "score_id", scoreID, "media_id", mediaID)
	return nil
}

func (s *SQLiteStore) PromoteScore(ctx context.Context, scoreID int) error {
	log := logger.FromContext(ctx)
	log.Debug("sqlite: promoting score", "score_id", scoreID)

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var mediaID int
	err = tx.GetContext(ctx, &mediaID, `SELECT media_id FROM scores WHERE id = ?`, scoreID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("score %d: %w", scoreID, store.ErrScoreNotFound)
		}
		return fmt.Errorf("looking up media_id for score %d: %w", scoreID, err)
	}

	const demoteQ = `UPDATE scores SET is_latest = 0,
	                  deleted_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), deleted_reason = ?
                          WHERE media_id = ? AND is_latest = 1 AND id != ?`
	if _, err := tx.ExecContext(
		ctx,
		demoteQ,
		store.DeletedReasonPromote,
		mediaID,
		scoreID,
	); err != nil {
		return fmt.Errorf("demoting previous latest score for media %d: %w", mediaID, err)
	}

	const promoteQ = `UPDATE scores SET is_latest = 1, deleted_at = NULL, deleted_reason = NULL WHERE id = ?`

	res, err := tx.ExecContext(ctx, promoteQ, scoreID)
	if err != nil {
		return fmt.Errorf("promoting score %d: %w", scoreID, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("score %d: %w", scoreID, store.ErrScoreNotFound)
	}

	return tx.Commit()
}

// Prune hard-deletes all soft-deleted score rows and any media entries with no
// remaining scores. The prune timestamp is recorded in db_metadata before
// deletion so it survives even if zero rows are deleted.
// Returns the number of score rows hard-deleted.
func (s *SQLiteStore) Prune(ctx context.Context) (int64, error) {
	log := logger.FromContext(ctx)
	log.Debug("sqlite: pruning soft-deleted scores")
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

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing prune transaction: %w", err)
	}
	log.Info("sqlite: prune complete", "scores_deleted", n)
	return n, nil
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

// GenreBreakdown returns the count and percentage of entries per genre,
// computed over each media's full AniList genre list (not just genres that
// actively participated in multiplier calculation — see GenreDimensionAffinity
// for that). Percentage is relative to the total number of latest, non-deleted
// scores; since a media entry can have multiple genres, percentages do not sum
// to 100.
func (s *SQLiteStore) GenreBreakdown(ctx context.Context) ([]store.GenreStat, error) {
	var total int
	const totalQ = `SELECT COUNT(*) FROM scores WHERE is_latest = 1 AND deleted_at IS NULL`
	if err := s.db.GetContext(ctx, &total, totalQ); err != nil {
		return nil, fmt.Errorf("counting latest scores: %w", err)
	}
	if total == 0 {
		return nil, nil
	}

	type row struct {
		Genre string `db:"genre"`
		Count int    `db:"cnt"`
	}
	const q = `SELECT mg.genre AS genre, COUNT(DISTINCT s.id) AS cnt
	           FROM media_genres mg
	           JOIN media m ON m.id = mg.media_id
	           JOIN scores s ON s.media_id = m.id
	           WHERE s.is_latest = 1 AND s.deleted_at IS NULL
	           GROUP BY mg.genre
	           ORDER BY cnt DESC`
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("fetching genre breakdown: %w", err)
	}

	result := make([]store.GenreStat, len(rows))
	for i, r := range rows {
		result[i] = store.GenreStat{
			Genre: r.Genre, Count: r.Count,
			Percentage: float64(r.Count) / float64(total) * 100,
		}
	}
	return result, nil
}

// ScoreByGenre returns the average final score per genre, computed over each
// media's full AniList genre list.
func (s *SQLiteStore) ScoreByGenre(ctx context.Context) ([]store.GenreScore, error) {
	type row struct {
		Genre    string  `db:"genre"`
		AvgScore float64 `db:"avg_score"`
		Count    int     `db:"cnt"`
	}
	const q = `SELECT mg.genre AS genre, AVG(s.final_score) AS avg_score, COUNT(*) AS cnt
	           FROM media_genres mg
	           JOIN media m ON m.id = mg.media_id
	           JOIN scores s ON s.media_id = m.id
	           WHERE s.is_latest = 1 AND s.deleted_at IS NULL
	           GROUP BY mg.genre
	           ORDER BY avg_score DESC`
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("fetching score by genre: %w", err)
	}
	result := make([]store.GenreScore, len(rows))
	for i, r := range rows {
		result[i] = store.GenreScore{Genre: r.Genre, AvgScore: r.AvgScore, Count: r.Count}
	}
	return result, nil
}

// GenreDimensionAffinity returns average dimension scores grouped by genre,
// restricted to genres that actively participated in multiplier calculation
// (score_matched_genres) rather than the media's full genre list.
func (s *SQLiteStore) GenreDimensionAffinity(
	ctx context.Context,
) ([]store.GenreDimensionAffinity, error) {
	type row struct {
		Genre        string  `db:"genre"`
		DimensionKey string  `db:"dimension_key"`
		Label        string  `db:"label"`
		AvgScore     float64 `db:"avg_score"`
	}
	const q = `SELECT smg.genre, ds.dimension_key, ds.label, AVG(ds.score) AS avg_score
	           FROM score_matched_genres smg
	           JOIN dimension_scores ds ON ds.score_id = smg.score_id
	           JOIN scores s ON s.id = smg.score_id
	           WHERE s.is_latest = 1 AND s.deleted_at IS NULL AND ds.skipped = 0
	           GROUP BY smg.genre, ds.dimension_key, ds.label
	           ORDER BY smg.genre, avg_score DESC`
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("fetching genre dimension affinity: %w", err)
	}

	result := make([]store.GenreDimensionAffinity, 0)
	index := make(map[string]int)
	for _, r := range rows {
		i, ok := index[r.Genre]
		if !ok {
			i = len(result)
			index[r.Genre] = i
			result = append(result, store.GenreDimensionAffinity{Genre: r.Genre})
		}
		result[i].Dimensions = append(result[i].Dimensions, store.DimensionAvg{
			DimensionKey: r.DimensionKey, Label: r.Label, AvgScore: r.AvgScore,
		})
	}
	return result, nil
}

// DimensionVariance returns the standard deviation of scores per dimension
// across all latest, non-deleted, non-skipped entries. SQLite has no STDDEV
// aggregate, so the population standard deviation is computed manually from
// AVG(x) and AVG(x^2); the MAX(0.0, ...) guard absorbs floating-point rounding
// that can otherwise push a true-zero variance slightly negative before SQRT.
func (s *SQLiteStore) DimensionVariance(
	ctx context.Context,
) ([]store.DimensionVarianceStat, error) {
	type row struct {
		DimensionKey string  `db:"dimension_key"`
		Label        string  `db:"label"`
		StdDev       float64 `db:"std_dev"`
		AvgScore     float64 `db:"avg_score"`
		Count        int     `db:"cnt"`
	}
	const q = `SELECT ds.dimension_key, ds.label,
	                  SQRT(MAX(0.0, AVG(ds.score * ds.score) - AVG(ds.score) * AVG(ds.score))) AS std_dev,
	                  AVG(ds.score) AS avg_score, COUNT(*) AS cnt
	           FROM dimension_scores ds
	           JOIN scores s ON s.id = ds.score_id
	           WHERE s.is_latest = 1 AND s.deleted_at IS NULL AND ds.skipped = 0
	           GROUP BY ds.dimension_key, ds.label
	           ORDER BY ds.dimension_key`
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("fetching dimension variance: %w", err)
	}
	result := make([]store.DimensionVarianceStat, len(rows))
	for i, r := range rows {
		result[i] = store.DimensionVarianceStat{
			DimensionKey: r.DimensionKey, Label: r.Label, StdDev: r.StdDev,
			AvgScore: r.AvgScore, Count: r.Count,
		}
	}
	return result, nil
}

// ScoringConsistency returns the average standard deviation across all
// dimensions — a single number representing overall scoring consistency.
// Count is the number of dimensions included in the average; dimensions with
// zero scored entries are excluded rather than counted as zero-variance.
// Returns nil, nil when no dimension has any scored entries yet.
func (s *SQLiteStore) ScoringConsistency(ctx context.Context) (*store.ConsistencyStat, error) {
	const q = `SELECT AVG(std_dev) AS avg_std_dev, COUNT(*) AS cnt
	           FROM (
	               SELECT SQRT(MAX(0.0, AVG(ds.score * ds.score) - AVG(ds.score) * AVG(ds.score))) AS std_dev
	               FROM dimension_scores ds
	               JOIN scores s ON s.id = ds.score_id
	               WHERE s.is_latest = 1 AND s.deleted_at IS NULL AND ds.skipped = 0
	               GROUP BY ds.dimension_key
	           ) sub`
	var res struct {
		AvgStdDev *float64 `db:"avg_std_dev"`
		Count     int      `db:"cnt"`
	}
	if err := s.db.GetContext(ctx, &res, q); err != nil {
		return nil, fmt.Errorf("computing scoring consistency: %w", err)
	}
	if res.Count == 0 || res.AvgStdDev == nil {
		return nil, nil
	}
	return &store.ConsistencyStat{AvgStdDev: *res.AvgStdDev, Count: res.Count}, nil
}

// DimensionCorrelation returns Pearson correlation coefficients between
// dimension pairs, computed via a manual self-join formula (SQLite has no
// CORR aggregate). Pairs with fewer than 25 shared scored entries are
// excluded via HAVING — enforced per pair, not as a global sample count,
// since a user may have plenty of total entries but few where both
// dimensions in a given pair were actually scored.
func (s *SQLiteStore) DimensionCorrelation(
	ctx context.Context,
) ([]store.DimensionCorrelationStat, error) {
	type row struct {
		DimA        string  `db:"dim_a"`
		DimB        string  `db:"dim_b"`
		Correlation float64 `db:"correlation"`
	}
	const q = `SELECT a.dimension_key AS dim_a, b.dimension_key AS dim_b,
	                  (COUNT(*) * SUM(a.score * b.score) - SUM(a.score) * SUM(b.score)) /
	                  (SQRT(COUNT(*) * SUM(a.score * a.score) - SUM(a.score) * SUM(a.score)) *
	                   SQRT(COUNT(*) * SUM(b.score * b.score) - SUM(b.score) * SUM(b.score))) AS correlation
	           FROM dimension_scores a
	           JOIN dimension_scores b ON a.score_id = b.score_id
	           JOIN scores s ON s.id = a.score_id
	           WHERE s.deleted_at IS NULL AND s.is_latest = 1
	             AND a.skipped = 0 AND b.skipped = 0
	             AND a.dimension_key < b.dimension_key
	           GROUP BY a.dimension_key, b.dimension_key
	           HAVING COUNT(*) >= 25`
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("computing dimension correlation: %w", err)
	}
	result := make([]store.DimensionCorrelationStat, len(rows))
	for i, r := range rows {
		result[i] = store.DimensionCorrelationStat{
			DimensionA:  r.DimA,
			DimensionB:  r.DimB,
			Correlation: r.Correlation,
		}
	}
	return result, nil
}

// SkippedDimensions returns how often each dimension is skipped, split by
// media type (ANIME/MANGA), across all latest, non-deleted entries.
func (s *SQLiteStore) SkippedDimensions(ctx context.Context) ([]store.SkippedDimStat, error) {
	type row struct {
		DimensionKey string `db:"dimension_key"`
		Label        string `db:"label"`
		MediaType    string `db:"media_type"`
		SkipCount    int    `db:"skip_count"`
		TotalCount   int    `db:"total_count"`
	}
	const q = `SELECT ds.dimension_key, ds.label, m.media_type,
	                  SUM(ds.skipped) AS skip_count, COUNT(*) AS total_count
	           FROM dimension_scores ds
	           JOIN scores s ON s.id = ds.score_id
	           JOIN media m ON m.id = s.media_id
	           WHERE s.is_latest = 1 AND s.deleted_at IS NULL
	           GROUP BY ds.dimension_key, ds.label, m.media_type
	           ORDER BY ds.dimension_key, m.media_type`
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("fetching skipped dimensions: %w", err)
	}
	result := make([]store.SkippedDimStat, len(rows))
	for i, r := range rows {
		result[i] = store.SkippedDimStat{
			DimensionKey: r.DimensionKey, Label: r.Label, MediaType: r.MediaType,
			SkipCount: r.SkipCount, TotalCount: r.TotalCount,
		}
	}
	return result, nil
}

// WeightOverrides returns how often each dimension has been weight-overridden
// via the --weight flag, across all latest, non-deleted entries.
func (s *SQLiteStore) WeightOverrides(ctx context.Context) ([]store.WeightOverrideStat, error) {
	type row struct {
		DimensionKey  string `db:"dimension_key"`
		Label         string `db:"label"`
		OverrideCount int    `db:"override_count"`
	}
	const q = `SELECT ds.dimension_key, ds.label, COUNT(*) AS override_count
	           FROM dimension_scores ds
	           JOIN scores s ON s.id = ds.score_id
	           WHERE s.is_latest = 1 AND s.deleted_at IS NULL AND ds.weight_override = 1
	           GROUP BY ds.dimension_key, ds.label
	           ORDER BY override_count DESC`
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("fetching weight overrides: %w", err)
	}
	result := make([]store.WeightOverrideStat, len(rows))
	for i, r := range rows {
		result[i] = store.WeightOverrideStat{
			DimensionKey:  r.DimensionKey,
			Label:         r.Label,
			OverrideCount: r.OverrideCount,
		}
	}
	return result, nil
}

// MostRescored returns entries ordered by rescore count descending. Counts
// all non-deleted scores per media, not just is_latest — rescore count is a
// historical fact independent of which score currently holds is_latest.
func (s *SQLiteStore) MostRescored(ctx context.Context) ([]store.RescoredStat, error) {
	type row struct {
		AnilistID     int             `db:"anilist_id"`
		TitleRomaji   string          `db:"title_romaji"`
		ScoreCount    int             `db:"score_count"`
		LatestScore   sql.NullFloat64 `db:"latest_score"`
		FirstScoredAt string          `db:"first_scored_at"`
		LastScoredAt  string          `db:"last_scored_at"`
	}
	const q = `SELECT m.anilist_id, m.title_romaji, COUNT(*) AS score_count,
	                  MAX(CASE WHEN s.is_latest THEN s.final_score END) AS latest_score,
	                  MIN(s.scored_at) AS first_scored_at, MAX(s.scored_at) AS last_scored_at
	           FROM scores s
	           JOIN media m ON m.id = s.media_id
	           WHERE s.deleted_at IS NULL
	           GROUP BY m.id, m.anilist_id, m.title_romaji
	           ORDER BY score_count DESC`
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("fetching most rescored: %w", err)
	}
	result := make([]store.RescoredStat, len(rows))
	for i, r := range rows {
		first, err := parseRFC3339("first_scored_at", r.FirstScoredAt)
		if err != nil {
			return nil, err
		}
		last, err := parseRFC3339("last_scored_at", r.LastScoredAt)
		if err != nil {
			return nil, err
		}
		var latestScore *float64
		if r.LatestScore.Valid {
			v := r.LatestScore.Float64
			latestScore = &v
		}
		result[i] = store.RescoredStat{
			AnilistID: r.AnilistID, TitleRomaji: r.TitleRomaji, ScoreCount: r.ScoreCount,
			LatestScore: latestScore, FirstScoredAt: first, LastScoredAt: last,
		}
	}
	return result, nil
}

// Outliers returns entries where a dimension score deviates more than 2
// population standard deviations from the user's personal average for that
// dimension (computed over all latest, non-deleted, non-skipped scores).
// Deviation is signed: positive means the score is above the personal
// average, negative means below.
func (s *SQLiteStore) Outliers(ctx context.Context) ([]store.OutlierStat, error) {
	type row struct {
		AnilistID    int     `db:"anilist_id"`
		TitleRomaji  string  `db:"title_romaji"`
		ScoreID      int     `db:"score_id"`
		ScoredAt     string  `db:"scored_at"`
		DimensionKey string  `db:"dimension_key"`
		Label        string  `db:"label"`
		Score        float64 `db:"score"`
		PersonalAvg  float64 `db:"personal_avg"`
		Deviation    float64 `db:"deviation"`
	}
	const q = `WITH dim_stats AS (
	               SELECT ds.dimension_key,
	                      AVG(ds.score) AS avg_score,
	                      SQRT(MAX(0.0, AVG(ds.score * ds.score) - AVG(ds.score) * AVG(ds.score))) AS std_dev
	               FROM dimension_scores ds
	               JOIN scores s ON s.id = ds.score_id
	               WHERE s.is_latest = 1 AND s.deleted_at IS NULL AND ds.skipped = 0
	               GROUP BY ds.dimension_key
	           )
	           SELECT m.anilist_id, m.title_romaji, s.id AS score_id, s.scored_at,
	                  ds.dimension_key, ds.label, ds.score,
	                  dst.avg_score AS personal_avg,
	                  (ds.score - dst.avg_score) / dst.std_dev AS deviation
	           FROM dimension_scores ds
	           JOIN scores s ON s.id = ds.score_id
	           JOIN media m ON m.id = s.media_id
	           JOIN dim_stats dst ON dst.dimension_key = ds.dimension_key
	           WHERE s.is_latest = 1 AND s.deleted_at IS NULL AND ds.skipped = 0
	             AND dst.std_dev > 0
	             AND ABS(ds.score - dst.avg_score) > 2 * dst.std_dev
	           ORDER BY ABS(ds.score - dst.avg_score) DESC`
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("fetching outliers: %w", err)
	}
	result := make([]store.OutlierStat, len(rows))
	for i, r := range rows {
		scoredAt, err := parseRFC3339("scored_at", r.ScoredAt)
		if err != nil {
			return nil, err
		}
		result[i] = store.OutlierStat{
			AnilistID: r.AnilistID, TitleRomaji: r.TitleRomaji, ScoreID: r.ScoreID,
			ScoredAt: scoredAt, DimensionKey: r.DimensionKey, Label: r.Label,
			Score: r.Score, PersonalAvg: r.PersonalAvg, Deviation: r.Deviation,
		}
	}
	return result, nil
}

// ConfigImpact returns average score data grouped by config_hash, ordered
// chronologically by first_scored_at. Each row is one "config epoch" — the
// caller diffs adjacent rows to see how a config change affected scores.
// Includes all non-deleted scores, not just is_latest, since a config change
// occurring mid-history is exactly what this stat exists to surface.
func (s *SQLiteStore) ConfigImpact(ctx context.Context) ([]store.ConfigImpactStat, error) {
	type row struct {
		ConfigHash    string  `db:"config_hash"`
		EntryCount    int     `db:"entry_count"`
		AvgScore      float64 `db:"avg_score"`
		FirstScoredAt string  `db:"first_scored_at"`
		LastScoredAt  string  `db:"last_scored_at"`
	}
	const q = `SELECT config_hash, COUNT(*) AS entry_count, AVG(final_score) AS avg_score,
	                  MIN(scored_at) AS first_scored_at, MAX(scored_at) AS last_scored_at
	           FROM scores
	           WHERE deleted_at IS NULL
	           GROUP BY config_hash
	           ORDER BY first_scored_at ASC`
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("fetching config impact: %w", err)
	}
	result := make([]store.ConfigImpactStat, len(rows))
	for i, r := range rows {
		first, err := parseRFC3339("first_scored_at", r.FirstScoredAt)
		if err != nil {
			return nil, err
		}
		last, err := parseRFC3339("last_scored_at", r.LastScoredAt)
		if err != nil {
			return nil, err
		}
		result[i] = store.ConfigImpactStat{
			ConfigHash: r.ConfigHash, EntryCount: r.EntryCount, AvgScore: r.AvgScore,
			FirstScoredAt: first, LastScoredAt: last,
		}
	}
	return result, nil
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// Score is a type alias so method signatures in this file match store.Score.
type Score = store.Score
