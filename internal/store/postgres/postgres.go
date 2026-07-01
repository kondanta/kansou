// Package postgres implements the Store interface backed by a PostgreSQL database.
// It uses pgx/v5 via pgxpool for connection pooling, sqlx for struct scanning,
// and golang-migrate for schema migrations.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	connStr := buildConnString(cfg)

	pool, err := pgxpool.New(ctx, connStr)
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

func buildConnString(cfg PostgresConfig) string {
	port := cfg.Port
	if port == "" {
		port = "5432"
	}
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		cfg.Host, port, cfg.User, cfg.Password, cfg.DBName)
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
// Returns config.Load("") defaults when the dimensions table is empty.
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

// SaveScore saves a completed scoring session atomically across all four tables.
// Implemented in Chunk 2 — requires SessionMeta.Format and SessionMeta.UserSelectedGenres.
func (s *PostgresStore) SaveScore(ctx context.Context, result scoring.Result, cfg *config.Config, maxHistory int) error {
	return errors.New("not implemented")
}

// LatestScore returns the most recent non-deleted score for a given AniList media ID.
func (s *PostgresStore) LatestScore(ctx context.Context, anilistID int) (*Score, error) {
	return nil, errors.New("not implemented")
}

// ScoreHistory returns all non-deleted scores for a given AniList media ID.
func (s *PostgresStore) ScoreHistory(ctx context.Context, anilistID int) ([]Score, error) {
	return nil, errors.New("not implemented")
}

// ListLatest returns the latest score for every media entry.
func (s *PostgresStore) ListLatest(ctx context.Context) ([]Score, error) {
	return nil, errors.New("not implemented")
}

// SoftDeleteScore sets deleted_at = now() on the given score ID.
func (s *PostgresStore) SoftDeleteScore(ctx context.Context, scoreID int) error {
	return errors.New("not implemented")
}

// Prune hard-deletes all soft-deleted rows. Returns the number of score rows deleted.
func (s *PostgresStore) Prune(ctx context.Context) (int64, error) {
	return 0, errors.New("not implemented")
}

// LastPruneAt returns the timestamp of the last prune operation.
func (s *PostgresStore) LastPruneAt(ctx context.Context) (*time.Time, error) {
	return nil, errors.New("not implemented")
}

// GenreBreakdown returns the count and percentage of entries per genre.
func (s *PostgresStore) GenreBreakdown(ctx context.Context) ([]store.GenreStat, error) {
	return nil, errors.New("not implemented")
}

// ScoreByGenre returns the average final score per genre.
func (s *PostgresStore) ScoreByGenre(ctx context.Context) ([]store.GenreScore, error) {
	return nil, errors.New("not implemented")
}

// GenreDimensionAffinity returns average dimension scores grouped by genre.
func (s *PostgresStore) GenreDimensionAffinity(ctx context.Context) ([]store.GenreDimensionAffinity, error) {
	return nil, errors.New("not implemented")
}

// DimensionVariance returns the standard deviation of scores per dimension.
func (s *PostgresStore) DimensionVariance(ctx context.Context) ([]store.DimensionVarianceStat, error) {
	return nil, errors.New("not implemented")
}

// ScoringConsistency returns the average standard deviation across all dimensions.
func (s *PostgresStore) ScoringConsistency(ctx context.Context) (*store.ConsistencyStat, error) {
	return nil, errors.New("not implemented")
}

// DimensionCorrelation returns Pearson correlation coefficients between dimension pairs.
func (s *PostgresStore) DimensionCorrelation(ctx context.Context) ([]store.DimensionCorrelationStat, error) {
	return nil, errors.New("not implemented")
}

// SkippedDimensions returns how often each dimension is skipped by media type.
func (s *PostgresStore) SkippedDimensions(ctx context.Context) ([]store.SkippedDimStat, error) {
	return nil, errors.New("not implemented")
}

// WeightOverrides returns how often each dimension has been weight-overridden.
func (s *PostgresStore) WeightOverrides(ctx context.Context) ([]store.WeightOverrideStat, error) {
	return nil, errors.New("not implemented")
}

// MostRescored returns entries ordered by rescore count descending.
func (s *PostgresStore) MostRescored(ctx context.Context) ([]store.RescoredStat, error) {
	return nil, errors.New("not implemented")
}

// Outliers returns entries with dimension scores deviating more than 2 std devs.
func (s *PostgresStore) Outliers(ctx context.Context) ([]store.OutlierStat, error) {
	return nil, errors.New("not implemented")
}

// ConfigImpact returns average score before and after each config change.
func (s *PostgresStore) ConfigImpact(ctx context.Context) ([]store.ConfigImpactStat, error) {
	return nil, errors.New("not implemented")
}

// Close closes the underlying pool and database connection.
func (s *PostgresStore) Close() error {
	err := s.db.Close()
	s.pool.Close()
	return err
}

// Score is a type alias so method signatures match store.Score.
type Score = store.Score
