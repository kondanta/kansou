// Package sqlite implements the Store interface backed by a SQLite database.
// It uses modernc.org/sqlite (pure-Go, no CGO) and golang-migrate for schema
// migrations. The database file path is configurable; ~ is expanded.
package sqlite

import (
	"context"
	"database/sql"
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

// SaveScore saves a completed scoring session atomically across all four tables.
// Implemented in Chunk 2 — requires SessionMeta.Format and SessionMeta.UserSelectedGenres
// to be added to internal/scoring/types.go first.
func (s *SQLiteStore) SaveScore(ctx context.Context, result scoring.Result, cfg *config.Config, maxHistory int) error {
	return errors.New("not implemented")
}

// LatestScore returns the most recent non-deleted score for a given AniList media ID.
func (s *SQLiteStore) LatestScore(ctx context.Context, anilistID int) (*Score, error) {
	return nil, errors.New("not implemented")
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

// Prune hard-deletes all soft-deleted rows. Returns the number of score rows deleted.
func (s *SQLiteStore) Prune(ctx context.Context) (int64, error) {
	return 0, errors.New("not implemented")
}

// LastPruneAt returns the timestamp of the last prune operation.
func (s *SQLiteStore) LastPruneAt(ctx context.Context) (*time.Time, error) {
	return nil, errors.New("not implemented")
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
