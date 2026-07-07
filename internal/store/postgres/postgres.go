// Package postgres implements the Store interface backed by a PostgreSQL database.
// It uses pgx/v5 via pgxpool for connection pooling, sqlx for struct scanning,
// and golang-migrate for schema migrations.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	migrate "github.com/golang-migrate/migrate/v4"
	migpgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"

	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
	"github.com/kondanta/kansou/internal/store"
)

// PostgresConfig holds the connection parameters for a Postgres database.
// Each field corresponds to the environment variable described in HISTORY_IMPL.md.
type PostgresConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
}

// PostgresStore implements store.Store backed by PostgreSQL.
type PostgresStore struct {
	pool *pgxpool.Pool
	db   *sqlx.DB
}

// New connects to Postgres, runs migrations, and returns a ready PostgresStore.
func New(ctx context.Context, cfg PostgresConfig) (*PostgresStore, error) {
	poolCfg, err := buildPoolConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building postgres config: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	if err := runMigrations(sqlDB); err != nil {
		pool.Close()
		return nil, err
	}

	return &PostgresStore{
		pool: pool,
		db:   sqlx.NewDb(sqlDB, "pgx5"),
	}, nil
}

// buildPoolConfig constructs a pgxpool.Config from PostgresConfig without
// interpolating the password into a string (prevents it surfacing in errors).
func buildPoolConfig(cfg PostgresConfig) (*pgxpool.Config, error) {
	port := cfg.Port
	if port == "" {
		port = "5432"
	}
	// Use a DSN without the password so pgx error messages cannot expose it.
	// The password is set directly on the parsed config struct.
	dsn := fmt.Sprintf("host=%s port=%s user=%s dbname=%s sslmode=disable",
		cfg.Host, port, cfg.User, cfg.DBName)
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing postgres config: %w", err)
	}
	poolCfg.ConnConfig.Password = cfg.Password
	return poolCfg, nil
}

func runMigrations(db *sql.DB) error {
	driver, err := migpgx.WithInstance(db, &migpgx.Config{})
	if err != nil {
		return fmt.Errorf("creating migration driver: %w", err)
	}
	src, err := iofs.New(store.MigrationsFS, "migrations/postgres")
	if err != nil {
		return fmt.Errorf("loading migration source: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
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
func (s *PostgresStore) LoadScoringConfig(ctx context.Context) (*config.Config, error) {
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
func (s *PostgresStore) SaveScoringConfig(ctx context.Context, cfg *config.Config) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const scalQ = `UPDATE config_scalars
	               SET primary_genre_weight = $1, max_multiplier = $2, max_history = $3
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
	              VALUES ($1, $2, $3, $4, $5, $6)`
	for i, key := range cfg.DimensionOrder {
		d := cfg.Dimensions[key]
		if _, err := tx.ExecContext(ctx, dimQ,
			key, d.Label, d.Description, d.Weight, d.BiasResistant, i); err != nil {
			return fmt.Errorf("inserting dimension %q: %w", key, err)
		}
	}

	const genreQ = `INSERT INTO genre_multipliers (genre, dimension_key, multiplier)
	                VALUES ($1, $2, $3)`
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
func (s *PostgresStore) SaveScore(ctx context.Context, result scoring.Result, cfg *config.Config, maxHistory int) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Upsert media row — titles and format may change across rescores.
	const mediaQ = `INSERT INTO media (anilist_id, title_romaji, title_english, media_type, format, cover_image, updated_at)
	                VALUES ($1, $2, $3, $4, $5, $6, NOW())
	                ON CONFLICT (anilist_id) DO UPDATE SET
	                    title_romaji  = EXCLUDED.title_romaji,
	                    title_english = EXCLUDED.title_english,
	                    media_type    = EXCLUDED.media_type,
	                    format        = EXCLUDED.format,
	                    cover_image   = EXCLUDED.cover_image,
	                    updated_at    = NOW()`
	if _, err := tx.ExecContext(ctx, mediaQ,
		result.Meta.MediaID,
		result.Meta.TitleRomaji,
		result.Meta.TitleEnglish,
		string(result.Meta.MediaType),
		result.Meta.Format,
		result.Meta.CoverImage,
	); err != nil {
		return fmt.Errorf("upserting media: %w", err)
	}

	var mediaRowID int
	if err := tx.GetContext(ctx, &mediaRowID,
		`SELECT id FROM media WHERE anilist_id = $1`, result.Meta.MediaID,
	); err != nil {
		return fmt.Errorf("fetching media row id: %w", err)
	}

	// Replace media genres — the genre list can change between AniList syncs.
	if _, err := tx.ExecContext(ctx, `DELETE FROM media_genres WHERE media_id = $1`, mediaRowID); err != nil {
		return fmt.Errorf("clearing media genres: %w", err)
	}
	const mgQ = `INSERT INTO media_genres (media_id, genre) VALUES ($1, $2) ON CONFLICT DO NOTHING`
	for _, g := range result.Meta.AllGenres {
		if _, err := tx.ExecContext(ctx, mgQ, mediaRowID, g); err != nil {
			return fmt.Errorf("inserting media genre %q: %w", g, err)
		}
	}

	snapshotBytes, err := json.Marshal(store.BuildConfigSnapshot(cfg))
	if err != nil {
		return fmt.Errorf("marshalling config snapshot: %w", err)
	}

	var userSelectedGenresBytes []byte
	if len(result.Meta.UserSelectedGenres) > 0 {
		userSelectedGenresBytes, err = json.Marshal(result.Meta.UserSelectedGenres)
		if err != nil {
			return fmt.Errorf("marshalling user_selected_genres: %w", err)
		}
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
		`UPDATE scores SET is_latest = FALSE WHERE media_id = $1 AND is_latest = TRUE`,
		mediaRowID,
	); err != nil {
		return fmt.Errorf("unsetting previous is_latest: %w", err)
	}

	const scoreQ = `INSERT INTO scores
	                    (media_id, final_score, primary_genre, primary_genre_weight,
	                     config_hash, config_snapshot, user_selected_genres, is_latest)
	                VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE) RETURNING id`
	var scoreID int64
	if err := tx.QueryRowContext(ctx, scoreQ,
		mediaRowID,
		result.FinalScore,
		primaryGenreStr,
		primaryGenreWeightPtr,
		config.Hash(cfg),
		snapshotBytes,
		userSelectedGenresBytes,
	).Scan(&scoreID); err != nil {
		return fmt.Errorf("inserting score: %w", err)
	}

	const dimQ = `INSERT INTO dimension_scores
	                  (score_id, dimension_key, label, score, base_weight, final_weight,
	                   applied_multiplier, contribution, skipped, bias_resistant, weight_override, genre_deselected,
	                   primary_genre_multiplier, secondary_genres_multiplier)
	              VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`
	for _, row := range result.Breakdown {
		var scoreVal, contribVal *float64
		if !row.Skipped {
			sv, cv := row.Score, row.Contribution
			scoreVal, contribVal = &sv, &cv
		}
		if _, err := tx.ExecContext(ctx, dimQ,
			scoreID, row.Key, row.Label,
			scoreVal, row.BaseWeight, row.FinalWeight, row.AppliedMultiplier, contribVal,
			row.Skipped, row.BiasResistant, row.WeightOverride, row.GenreDeselected,
			row.PrimaryGenreMultiplier, row.SecondaryGenresMultiplier,
		); err != nil {
			return fmt.Errorf("inserting dimension score %q: %w", row.Key, err)
		}
	}

	const matchQ = `INSERT INTO score_matched_genres (score_id, genre, is_primary) VALUES ($1, $2, $3)`
	primaryLower := strings.ToLower(result.Meta.PrimaryGenre)
	for _, genre := range result.Meta.GenresActive {
		isPrimary := primaryLower != "" && genre == primaryLower
		if _, err := tx.ExecContext(ctx, matchQ, scoreID, genre, isPrimary); err != nil {
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
		                SET deleted_at = NOW(), deleted_reason = $3
		                WHERE media_id = $1 AND deleted_at IS NULL
		                  AND id NOT IN (
		                      SELECT id FROM scores
		                      WHERE media_id = $1 AND deleted_at IS NULL
		                      ORDER BY scored_at DESC LIMIT $2
		                  )`
		if _, err := tx.ExecContext(ctx, pruneQ, mediaRowID, keepCount, store.DeletedReasonMaxHistory); err != nil {
			return fmt.Errorf("applying max_history: %w", err)
		}
	}

	return tx.Commit()
}

// scoreRow is the scanning target for the scores+media JOIN query.
// ScoredAt is TIMESTAMPTZ in Postgres and scans directly into time.Time.
type scoreRow struct {
	ID                 int       `db:"id"`
	MediaID            int       `db:"media_id"`
	TitleRomaji        string    `db:"title_romaji"`
	TitleEnglish       string    `db:"title_english"`
	MediaType          string    `db:"media_type"`
	Format             string    `db:"format"`
	FinalScore         float64   `db:"final_score"`
	PrimaryGenre       *string   `db:"primary_genre"`
	PrimaryGenreWeight *float64  `db:"primary_genre_weight"`
	ConfigHash         string    `db:"config_hash"`
	IsLatest           bool      `db:"is_latest"`
	CoverImage         *string   `db:"cover_image"`
	ScoredAt           time.Time `db:"scored_at"`
	UserSelectedGenres []byte    `db:"user_selected_genres"` // JSONB; nil when SQL NULL
}

// dimRow is the scanning target for dimension_scores queries.
// Shadows the function-local dimRow inside LoadScoringConfig within that scope.
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
func (s *PostgresStore) LatestScore(ctx context.Context, anilistID int) (*Score, error) {
	const q = `SELECT s.id, s.media_id, m.title_romaji, m.title_english, m.media_type, m.format,
	                  m.cover_image, s.final_score, s.primary_genre, s.primary_genre_weight, s.config_hash,
	                  s.is_latest, s.scored_at, s.user_selected_genres
	           FROM scores s
	           JOIN media m ON m.id = s.media_id
	           WHERE m.anilist_id = $1 AND s.is_latest = TRUE AND s.deleted_at IS NULL
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
func (s *PostgresStore) assembleScore(ctx context.Context, anilistID int, row *scoreRow) (*Score, error) {
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
	if len(row.UserSelectedGenres) > 0 {
		if err := json.Unmarshal(row.UserSelectedGenres, &userSelectedGenres); err != nil {
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
		ScoredAt:           row.ScoredAt,
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
func (s *PostgresStore) fetchMediaGenres(ctx context.Context, mediaID int) ([]string, error) {
	var genres []string
	const q = `SELECT genre FROM media_genres WHERE media_id = $1 ORDER BY genre`
	if err := s.db.SelectContext(ctx, &genres, q, mediaID); err != nil {
		return nil, fmt.Errorf("fetching media genres: %w", err)
	}
	return genres, nil
}

// fetchDimensionScores returns the breakdown rows for a score.
func (s *PostgresStore) fetchDimensionScores(ctx context.Context, scoreID int) ([]store.DimensionScoreRow, error) {
	const q = `SELECT dimension_key, label, score, base_weight, final_weight, applied_multiplier,
	                  contribution, skipped, bias_resistant, weight_override, genre_deselected,
	                  primary_genre_multiplier, secondary_genres_multiplier
	           FROM dimension_scores WHERE score_id = $1`
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
			PrimaryGenreMultiplier: r.PrimaryGenreMultiplier, SecondaryGenresMultiplier: r.SecondaryGenresMultiplier,
		}
	}
	return result, nil
}

// fetchMatchedGenres returns the active genre rows for a score.
func (s *PostgresStore) fetchMatchedGenres(ctx context.Context, scoreID int) ([]store.MatchedGenreRow, error) {
	const q = `SELECT genre, is_primary FROM score_matched_genres WHERE score_id = $1`
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
func (s *PostgresStore) ScoreHistory(ctx context.Context, anilistID int) ([]Score, error) {
	const q = `SELECT s.id, s.media_id, m.title_romaji, m.title_english, m.media_type, m.format,
	                  m.cover_image, s.final_score, s.primary_genre, s.primary_genre_weight, s.config_hash,
	                  s.is_latest, s.scored_at, s.user_selected_genres
	           FROM scores s
	           JOIN media m ON m.id = s.media_id
	           WHERE m.anilist_id = $1 AND s.deleted_at IS NULL
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
	ID                 int       `db:"id"`
	AnilistID          int       `db:"anilist_id"`
	TitleRomaji        string    `db:"title_romaji"`
	TitleEnglish       string    `db:"title_english"`
	MediaType          string    `db:"media_type"`
	Format             string    `db:"format"`
	FinalScore         float64   `db:"final_score"`
	PrimaryGenre       *string   `db:"primary_genre"`
	PrimaryGenreWeight *float64  `db:"primary_genre_weight"`
	ConfigHash         string    `db:"config_hash"`
	IsLatest           bool      `db:"is_latest"`
	EntryCount         int       `db:"entry_count"`
	CoverImage         *string   `db:"cover_image"`
	ScoredAt           time.Time `db:"scored_at"`
}

// ListLatest returns the latest score for every media entry, ordered by
// scored_at DESC. Excludes soft-deleted scores. Does NOT populate Breakdown
// or ActiveGenres — use LatestScore or ScoreHistory when the full breakdown
// is needed; loading it for every entry here would require an expensive JOIN.
func (s *PostgresStore) ListLatest(ctx context.Context) ([]Score, error) {
	const q = `SELECT s.id, m.anilist_id, m.title_romaji, m.title_english, m.media_type, m.format,
	                  m.cover_image, s.final_score, s.primary_genre, s.primary_genre_weight, s.config_hash,
	                  s.is_latest, s.scored_at,
	                  (SELECT COUNT(*) FROM scores s2
	                   WHERE s2.media_id = s.media_id AND s2.deleted_at IS NULL) AS entry_count
	           FROM scores s
	           JOIN media m ON m.id = s.media_id
	           WHERE s.is_latest = TRUE AND s.deleted_at IS NULL
	           ORDER BY s.scored_at DESC`
	var rows []listLatestRow
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("fetching latest scores: %w", err)
	}
	result := make([]Score, len(rows))
	for i, r := range rows {
		sc := Score{
			ID: r.ID, AnilistID: r.AnilistID, TitleRomaji: r.TitleRomaji, TitleEnglish: r.TitleEnglish,
			MediaType: r.MediaType, Format: r.Format, FinalScore: r.FinalScore,
			ConfigHash: r.ConfigHash, IsLatest: r.IsLatest, EntryCount: r.EntryCount, ScoredAt: r.ScoredAt,
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
func (s *PostgresStore) SearchMediaByTitle(ctx context.Context, query string) ([]store.MediaSearchResult, error) {
	type row struct {
		AnilistID    int    `db:"anilist_id"`
		TitleRomaji  string `db:"title_romaji"`
		TitleEnglish string `db:"title_english"`
		MediaType    string `db:"media_type"`
		Format       string `db:"format"`
	}
	const q = `SELECT anilist_id, title_romaji, title_english, media_type, format
	           FROM media
	           WHERE title_romaji ILIKE '%' || $1 || '%'
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
func (s *PostgresStore) SoftDeleteScore(ctx context.Context, scoreID int) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE scores
		 SET deleted_at = NOW(), deleted_reason = $1, is_latest = FALSE
		 WHERE id = $2 AND deleted_at IS NULL`,
		store.DeletedReasonManual, scoreID,
	)
	if err != nil {
		return fmt.Errorf("soft-deleting score %d: %w", scoreID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("score %d: %w", scoreID, store.ErrScoreNotFound)
	}
	return nil
}

// Prune hard-deletes all soft-deleted score rows and any media entries with no
// remaining scores. The prune timestamp is recorded in db_metadata before
// deletion so it survives even if zero rows are deleted.
// Returns the number of score rows hard-deleted.
func (s *PostgresStore) Prune(ctx context.Context) (int64, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const metaQ = `INSERT INTO db_metadata (key, value)
	               VALUES ('last_prune_at', TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'))
	               ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`
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
func (s *PostgresStore) LastPruneAt(ctx context.Context) (*time.Time, error) {
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
func (s *PostgresStore) GenreBreakdown(ctx context.Context) ([]store.GenreStat, error) {
	var total int
	const totalQ = `SELECT COUNT(*) FROM scores WHERE is_latest = TRUE AND deleted_at IS NULL`
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
	           WHERE s.is_latest = TRUE AND s.deleted_at IS NULL
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
func (s *PostgresStore) ScoreByGenre(ctx context.Context) ([]store.GenreScore, error) {
	type row struct {
		Genre    string  `db:"genre"`
		AvgScore float64 `db:"avg_score"`
		Count    int     `db:"cnt"`
	}
	const q = `SELECT mg.genre AS genre, AVG(s.final_score) AS avg_score, COUNT(*) AS cnt
	           FROM media_genres mg
	           JOIN media m ON m.id = mg.media_id
	           JOIN scores s ON s.media_id = m.id
	           WHERE s.is_latest = TRUE AND s.deleted_at IS NULL
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
func (s *PostgresStore) GenreDimensionAffinity(ctx context.Context) ([]store.GenreDimensionAffinity, error) {
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
	           WHERE s.is_latest = TRUE AND s.deleted_at IS NULL AND ds.skipped = FALSE
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
// across all latest, non-deleted, non-skipped entries, using Postgres's
// native population standard deviation aggregate.
func (s *PostgresStore) DimensionVariance(ctx context.Context) ([]store.DimensionVarianceStat, error) {
	type row struct {
		DimensionKey string  `db:"dimension_key"`
		Label        string  `db:"label"`
		StdDev       float64 `db:"std_dev"`
		AvgScore     float64 `db:"avg_score"`
		Count        int     `db:"cnt"`
	}
	const q = `SELECT ds.dimension_key, ds.label, STDDEV_POP(ds.score) AS std_dev,
	                  AVG(ds.score) AS avg_score, COUNT(*) AS cnt
	           FROM dimension_scores ds
	           JOIN scores s ON s.id = ds.score_id
	           WHERE s.is_latest = TRUE AND s.deleted_at IS NULL AND ds.skipped = FALSE
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
func (s *PostgresStore) ScoringConsistency(ctx context.Context) (*store.ConsistencyStat, error) {
	const q = `SELECT AVG(std_dev) AS avg_std_dev, COUNT(*) AS cnt
	           FROM (
	               SELECT STDDEV_POP(ds.score) AS std_dev
	               FROM dimension_scores ds
	               JOIN scores s ON s.id = ds.score_id
	               WHERE s.is_latest = TRUE AND s.deleted_at IS NULL AND ds.skipped = FALSE
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
// dimension pairs, using Postgres's native CORR aggregate. Pairs with fewer
// than 25 shared scored entries are excluded via HAVING — enforced per pair,
// not as a global sample count, since a user may have plenty of total entries
// but few where both dimensions in a given pair were actually scored.
func (s *PostgresStore) DimensionCorrelation(ctx context.Context) ([]store.DimensionCorrelationStat, error) {
	type row struct {
		DimA        string  `db:"dim_a"`
		DimB        string  `db:"dim_b"`
		Correlation float64 `db:"correlation"`
	}
	const q = `SELECT a.dimension_key AS dim_a, b.dimension_key AS dim_b, CORR(a.score, b.score) AS correlation
	           FROM dimension_scores a
	           JOIN dimension_scores b ON a.score_id = b.score_id
	           JOIN scores s ON s.id = a.score_id
	           WHERE s.deleted_at IS NULL AND s.is_latest = TRUE
	             AND a.skipped = FALSE AND b.skipped = FALSE
	             AND a.dimension_key < b.dimension_key
	           GROUP BY a.dimension_key, b.dimension_key
	           HAVING COUNT(*) >= 25`
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("computing dimension correlation: %w", err)
	}
	result := make([]store.DimensionCorrelationStat, len(rows))
	for i, r := range rows {
		result[i] = store.DimensionCorrelationStat{DimensionA: r.DimA, DimensionB: r.DimB, Correlation: r.Correlation}
	}
	return result, nil
}

// SkippedDimensions returns how often each dimension is skipped, split by
// media type (ANIME/MANGA), across all latest, non-deleted entries.
func (s *PostgresStore) SkippedDimensions(ctx context.Context) ([]store.SkippedDimStat, error) {
	type row struct {
		DimensionKey string `db:"dimension_key"`
		Label        string `db:"label"`
		MediaType    string `db:"media_type"`
		SkipCount    int    `db:"skip_count"`
		TotalCount   int    `db:"total_count"`
	}
	const q = `SELECT ds.dimension_key, ds.label, m.media_type,
	                  SUM(CASE WHEN ds.skipped THEN 1 ELSE 0 END) AS skip_count,
	                  COUNT(*) AS total_count
	           FROM dimension_scores ds
	           JOIN scores s ON s.id = ds.score_id
	           JOIN media m ON m.id = s.media_id
	           WHERE s.is_latest = TRUE AND s.deleted_at IS NULL
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
func (s *PostgresStore) WeightOverrides(ctx context.Context) ([]store.WeightOverrideStat, error) {
	type row struct {
		DimensionKey  string `db:"dimension_key"`
		Label         string `db:"label"`
		OverrideCount int    `db:"override_count"`
	}
	const q = `SELECT ds.dimension_key, ds.label, COUNT(*) AS override_count
	           FROM dimension_scores ds
	           JOIN scores s ON s.id = ds.score_id
	           WHERE s.is_latest = TRUE AND s.deleted_at IS NULL AND ds.weight_override = TRUE
	           GROUP BY ds.dimension_key, ds.label
	           ORDER BY override_count DESC`
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("fetching weight overrides: %w", err)
	}
	result := make([]store.WeightOverrideStat, len(rows))
	for i, r := range rows {
		result[i] = store.WeightOverrideStat{DimensionKey: r.DimensionKey, Label: r.Label, OverrideCount: r.OverrideCount}
	}
	return result, nil
}

// MostRescored returns entries ordered by rescore count descending. Counts
// all non-deleted scores per media, not just is_latest — rescore count is a
// historical fact independent of which score currently holds is_latest.
func (s *PostgresStore) MostRescored(ctx context.Context) ([]store.RescoredStat, error) {
	type row struct {
		AnilistID     int             `db:"anilist_id"`
		TitleRomaji   string          `db:"title_romaji"`
		ScoreCount    int             `db:"score_count"`
		LatestScore   sql.NullFloat64 `db:"latest_score"`
		FirstScoredAt time.Time       `db:"first_scored_at"`
		LastScoredAt  time.Time       `db:"last_scored_at"`
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
		var latestScore *float64
		if r.LatestScore.Valid {
			v := r.LatestScore.Float64
			latestScore = &v
		}
		result[i] = store.RescoredStat{
			AnilistID: r.AnilistID, TitleRomaji: r.TitleRomaji, ScoreCount: r.ScoreCount,
			LatestScore: latestScore, FirstScoredAt: r.FirstScoredAt, LastScoredAt: r.LastScoredAt,
		}
	}
	return result, nil
}

// Outliers returns entries where a dimension score deviates more than 2
// population standard deviations from the user's personal average for that
// dimension (computed over all latest, non-deleted, non-skipped scores).
// Deviation is signed: positive means the score is above the personal
// average, negative means below.
func (s *PostgresStore) Outliers(ctx context.Context) ([]store.OutlierStat, error) {
	type row struct {
		AnilistID    int       `db:"anilist_id"`
		TitleRomaji  string    `db:"title_romaji"`
		ScoreID      int       `db:"score_id"`
		ScoredAt     time.Time `db:"scored_at"`
		DimensionKey string    `db:"dimension_key"`
		Label        string    `db:"label"`
		Score        float64   `db:"score"`
		PersonalAvg  float64   `db:"personal_avg"`
		Deviation    float64   `db:"deviation"`
	}
	const q = `WITH dim_stats AS (
	               SELECT ds.dimension_key, AVG(ds.score) AS avg_score, STDDEV_POP(ds.score) AS std_dev
	               FROM dimension_scores ds
	               JOIN scores s ON s.id = ds.score_id
	               WHERE s.is_latest = TRUE AND s.deleted_at IS NULL AND ds.skipped = FALSE
	               GROUP BY ds.dimension_key
	           )
	           SELECT m.anilist_id, m.title_romaji, s.id AS score_id, s.scored_at,
	                  ds.dimension_key, ds.label, ds.score,
	                  dst.avg_score AS personal_avg,
	                  (ds.score - dst.avg_score) / NULLIF(dst.std_dev, 0) AS deviation
	           FROM dimension_scores ds
	           JOIN scores s ON s.id = ds.score_id
	           JOIN media m ON m.id = s.media_id
	           JOIN dim_stats dst ON dst.dimension_key = ds.dimension_key
	           WHERE s.is_latest = TRUE AND s.deleted_at IS NULL AND ds.skipped = FALSE
	             AND dst.std_dev > 0
	             AND ABS(ds.score - dst.avg_score) > 2 * dst.std_dev
	           ORDER BY ABS(ds.score - dst.avg_score) DESC`
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("fetching outliers: %w", err)
	}
	result := make([]store.OutlierStat, len(rows))
	for i, r := range rows {
		result[i] = store.OutlierStat{
			AnilistID: r.AnilistID, TitleRomaji: r.TitleRomaji, ScoreID: r.ScoreID,
			ScoredAt: r.ScoredAt, DimensionKey: r.DimensionKey, Label: r.Label,
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
func (s *PostgresStore) ConfigImpact(ctx context.Context) ([]store.ConfigImpactStat, error) {
	type row struct {
		ConfigHash    string    `db:"config_hash"`
		EntryCount    int       `db:"entry_count"`
		AvgScore      float64   `db:"avg_score"`
		FirstScoredAt time.Time `db:"first_scored_at"`
		LastScoredAt  time.Time `db:"last_scored_at"`
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
		result[i] = store.ConfigImpactStat{
			ConfigHash: r.ConfigHash, EntryCount: r.EntryCount, AvgScore: r.AvgScore,
			FirstScoredAt: r.FirstScoredAt, LastScoredAt: r.LastScoredAt,
		}
	}
	return result, nil
}

// Close closes the underlying pool and database connection.
// closeTimeout bounds how long Close waits for pgxpool to release and
// destroy every connection. pgxpool.Pool.Close blocks until all connections
// are returned to the pool — if a connection is ever leaked or its
// underlying socket hangs mid-teardown, that call blocks forever with no
// way to cancel it. Since Close runs on the CLI's exit path (see
// cmd/root.go), an unbounded hang here would hang the whole program.
const closeTimeout = 5 * time.Second

func (s *PostgresStore) Close() error {
	err := s.db.Close()

	done := make(chan struct{})
	go func() {
		s.pool.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(closeTimeout):
		// The process is exiting either way; a lingering pool teardown
		// goroutine is harmless once the OS reclaims the socket.
	}
	return err
}

// Score is a type alias so method signatures match store.Score.
type Score = store.Score
