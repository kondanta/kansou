// Package cmd implements the kansou command tree.
// All cobra command definitions live here. Business logic lives in internal/.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/kondanta/kansou/internal/anilist"
	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/logger"
	"github.com/kondanta/kansou/internal/scoring"
	"github.com/kondanta/kansou/internal/store"
	"github.com/kondanta/kansou/internal/store/postgres"
	"github.com/kondanta/kansou/internal/store/sqlite"
	"github.com/spf13/cobra"
)

// version is the build version, overridable at link time:
//
//	go build -ldflags "-X github.com/kondanta/kansou/cmd.version=1.2.3"
var version = "dev"

// App owns all shared CLI dependencies.
// It is constructed with nil deps so commands can be registered before config
// is loaded. PersistentPreRunE populates the fields after flag parsing, so
// --config is honoured. Commands close over the *App pointer and see the
// populated fields when their RunE fires.
type App struct {
	// Config is the loaded and validated application config.
	Config *config.Config
	// AniList is the AniList GraphQL client.
	AniList *anilist.Client
	// Engine is the scoring engine wired with the current config.
	Engine *scoring.Engine
	// ConfigPath is the resolved path to the loaded config file.
	ConfigPath string
	// Store is the persistence layer. Nil in DBless mode (KANSOU_DB_TYPE unset).
	Store store.Store
}

// Execute builds the command tree and runs it. Called from main.
func Execute() {
	logger.Setup(false)

	var configPath string

	rootCmd := &cobra.Command{
		Use:   "kansou",
		Short: "A weighted anime/manga scoring tool with AniList integration",
		Long: `kansou (感想) is a personal anime and manga scoring tool.
It fetches media metadata from AniList, guides you through a per-dimension
scoring session, and publishes the final weighted score back to AniList.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.PersistentFlags().StringVar(
		&configPath, "config", "",
		"path to config file (default: ~/.config/kansou/config.toml)",
	)
	rootCmd.Flags().Bool("version", false, "print version and exit")

	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		v, _ := cmd.Flags().GetBool("version")
		if v {
			fmt.Printf("kansou %s\n", version)
			return nil
		}
		return cmd.Help()
	}

	app := &App{}

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		st, err := initStore(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		app.Store = st

		cfg, err := loadAppConfig(ctx, app.Store, configPath)
		if err != nil {
			return err
		}
		app.Config = cfg

		resolved, err := config.ResolvePath(configPath)
		if err != nil {
			return fmt.Errorf("resolving config path: %w", err)
		}
		app.ConfigPath = resolved
		app.AniList = anilist.NewClient()
		app.Engine = newEngine(app.Config)
		return nil
	}

	rootCmd.AddCommand(app.mediaCmd())
	rootCmd.AddCommand(app.scoreCmd())
	rootCmd.AddCommand(app.serveCmd())
	rootCmd.AddCommand(app.dbCmd())
	rootCmd.AddCommand(app.statsCmd())
	rootCmd.AddCommand(app.historyCmd())
	rootCmd.AddCommand(app.configCmd())
	rootCmd.AddCommand(app.exportCmd())

	exitCode := 0
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitCode = 1
	}
	if app.Store != nil {
		_ = app.Store.Close()
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

// initStore opens the persistence layer selected by KANSOU_DB_TYPE, or
// returns a nil Store for DBless mode when the variable is unset.
func initStore(ctx context.Context) (store.Store, error) {
	dbType := os.Getenv("KANSOU_DB_TYPE")
	switch dbType {
	case "sqlite":
		path := os.Getenv("KANSOU_DB_PATH")
		if path == "" {
			path = "~/.local/share/kansou/kansou.db"
		}
		s, err := sqlite.New(path)
		if err != nil {
			return nil, fmt.Errorf("opening sqlite database: %w", err)
		}
		return s, nil

	case "postgres":
		pgcfg := postgres.PostgresConfig{
			Host:     os.Getenv("POSTGRES_HOST"),
			Port:     os.Getenv("POSTGRES_PORT"),
			User:     os.Getenv("POSTGRES_USER"),
			Password: os.Getenv("POSTGRES_PASSWORD"),
			DBName:   os.Getenv("POSTGRES_DB"),
		}
		s, err := postgres.New(ctx, pgcfg)
		if err != nil {
			return nil, fmt.Errorf("opening postgres database: %w", err)
		}
		return s, nil

	case "":
		return nil, nil

	default:
		return nil, fmt.Errorf(
			"unknown KANSOU_DB_TYPE %q — must be \"sqlite\" or \"postgres\"", dbType,
		)
	}
}

// loadAppConfig loads the scoring config from st if configured, seeding it
// from the config file on first run, or falls back to the config file
// directly in DBless mode.
func loadAppConfig(ctx context.Context, st store.Store, configPath string) (*config.Config, error) {
	if st == nil {
		cfg, err := config.Load(configPath)
		if err != nil {
			return nil, fmt.Errorf("loading config: %w", err)
		}
		slog.Debug("config loaded", "dimensions", len(cfg.Dimensions))
		return cfg, nil
	}

	cfg, err := st.LoadScoringConfig(ctx)
	if errors.Is(err, store.ErrNotSeeded) {
		fileCfg, loadErr := config.Load(configPath)
		if loadErr != nil {
			return nil, fmt.Errorf("loading config file for DB seed: %w", loadErr)
		}
		if err := st.SaveScoringConfig(ctx, fileCfg); err != nil {
			return nil, fmt.Errorf("seeding database config: %w", err)
		}
		slog.Info("seeded database from config file", "dimensions", len(fileCfg.Dimensions))
		return fileCfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading scoring config from database: %w", err)
	}

	slog.Debug("config loaded from database", "dimensions", len(cfg.Dimensions))
	return cfg, nil
}

// newEngine converts config dimensions into a scoring.Engine.
// Duplicated in internal/server/config_handlers.go — a shared package would
// either pollute internal/config or internal/scoring with the other's types,
// and a one-function adapter package would create naming confusion with the
// scoring engine itself. If scoring.DimensionDef or config.DimensionDef fields
// change, update both copies.
func newEngine(cfg *config.Config) *scoring.Engine {
	dims := make(map[string]scoring.DimensionDef, len(cfg.Dimensions))
	for key, d := range cfg.Dimensions {
		dims[key] = scoring.DimensionDef{
			Label:         d.Label,
			Description:   d.Description,
			Weight:        d.Weight,
			BiasResistant: d.BiasResistant,
		}
	}
	return scoring.NewEngine(cfg.DimensionOrder, dims, cfg.Genres, cfg.PrimaryGenreWeight)
}
