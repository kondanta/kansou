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

	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "path to config file (default: ~/.config/kansou/config.toml)")
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
		cfg, err := config.Load(configPath)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		slog.Debug("config loaded", "dimensions", len(cfg.Dimensions))
		app.Config = cfg
		app.AniList = anilist.NewClient()
		app.Engine = newEngine(cfg)
		return nil
	}

	rootCmd.AddCommand(app.mediaCmd())
	rootCmd.AddCommand(app.scoreCmd())
	rootCmd.AddCommand(app.serveCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// newEngine constructs a scoring.Engine from the loaded config.
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
	return scoring.NewEngine(cfg.DimensionOrder, dims, cfg.Genres)
}
