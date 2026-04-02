// @title          kansou API
// @version         1.0
// @description     Personal anime/manga scoring tool with AniList integration.
// @contact.name    kansou
// @license.name    MIT
// @host            localhost:8080
// @BasePath        /

// kansou is a personal anime/manga scoring CLI and REST server.
// It fetches media metadata from AniList, guides an interactive scoring session,
// applies a weighted genre-adjusted formula, and publishes the final score.
//
// Usage:
//
//	kansou score add "Frieren"         # start a scoring session (includes publish prompt)
//	kansou media find "Mushishi"       # look up media without scoring
//	kansou serve                       # start the REST server
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	_ "github.com/kondanta/kansou/docs/swagger" // registers Swagger spec via init()
	"github.com/kondanta/kansou/internal/anilist"
	"github.com/kondanta/kansou/internal/cli"
	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/logger"
	"github.com/kondanta/kansou/internal/scoring"
	"github.com/kondanta/kansou/internal/server"
)

// version is the build version, overridable at link time:
//
//	go build -ldflags "-X main.version=1.2.3"
var version = "dev"

func main() {
	// Setup CLI logger early. If the serve subcommand is invoked it will
	// reinitialise with the JSON handler before starting the server.
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

	// Handle --version before running any subcommand.
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		v, _ := cmd.Flags().GetBool("version")
		if v {
			fmt.Printf("kansou %s\n", version)
			return nil
		}
		return cmd.Help()
	}

	// app is created with nil deps so its commands can be registered before
	// config is loaded. PersistentPreRunE fills in the deps after flag parsing,
	// so --config is honoured. Commands close over the *App pointer and will
	// see the populated fields when their RunE fires.
	app := cli.NewApp(nil, nil, nil)

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

	rootCmd.AddCommand(app.MediaCmd())
	rootCmd.AddCommand(app.ScoreCmd())
	rootCmd.AddCommand(newServeCmd(app))

	if err := rootCmd.Execute(); err != nil {
		slog.Error("command failed", "err", err)
		os.Exit(2)
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

// newServeCmd constructs the `kansou serve` cobra command.
// It closes over app so that PersistentPreRunE has already populated the deps
// by the time RunE fires.
func newServeCmd(app *cli.App) *cobra.Command {
	var port int

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the REST API server",
		Long: `Start the kansou REST API server.
Exposes the scoring engine and AniList integration over HTTP.
Swagger UI is available at /swagger/index.html.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Switch to JSON logging for server mode.
			logger.Setup(true)
			srv := server.New(app.Config, app.AniList, app.Engine)
			if err := srv.ListenAndServe(port); err != nil {
				slog.Error("server error", "err", err)
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&port, "port", 0, "port to listen on (overrides config)")
	return cmd
}
