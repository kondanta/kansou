// Package cmd implements the kansou command tree.
// All cobra command definitions live here. Business logic lives in internal/.
package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/kondanta/kansou/internal/anilist"
	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/logger"
	"github.com/kondanta/kansou/internal/scoring"
	"github.com/kondanta/kansou/internal/store"
	"github.com/kondanta/kansou/internal/store/postgres"
	"github.com/kondanta/kansou/internal/store/sqlite"
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

		// --- Store initialisation ---
		dbType := os.Getenv("KANSOU_DB_TYPE")
		switch dbType {
		case "sqlite":
			path := os.Getenv("KANSOU_DB_PATH")
			if path == "" {
				path = "~/.local/share/kansou/kansou.db"
			}
			s, err := sqlite.New(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: opening sqlite database: %v\n", err)
				os.Exit(1)
			}
			app.Store = s

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
				fmt.Fprintf(os.Stderr, "error: opening postgres database: %v\n", err)
				os.Exit(1)
			}
			app.Store = s

		case "":
			app.Store = nil // DBless mode

		default:
			fmt.Fprintf(os.Stderr, "error: unknown KANSOU_DB_TYPE %q — must be \"sqlite\" or \"postgres\"\n", dbType)
			os.Exit(1)
		}

		// --- Config loading ---
		if app.Store != nil {
			cfg, err := app.Store.LoadScoringConfig(ctx)
			if err != nil {
				return fmt.Errorf("loading scoring config from database: %w", err)
			}

			// Seed the DB on first run (empty dimensions table returned defaults).
			if len(cfg.Dimensions) == 0 {
				fileCfg, err := config.Load(configPath)
				if err != nil {
					return fmt.Errorf("loading config file for DB seed: %w", err)
				}
				if err := app.Store.SaveScoringConfig(ctx, fileCfg); err != nil {
					return fmt.Errorf("seeding database config: %w", err)
				}
				cfg = fileCfg
				slog.Info("seeded database from config file", "dimensions", len(cfg.Dimensions))
			}

			slog.Debug("config loaded from database", "dimensions", len(cfg.Dimensions))
			app.Config = cfg
		} else {
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			slog.Debug("config loaded", "dimensions", len(cfg.Dimensions))
			app.Config = cfg
		}

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
