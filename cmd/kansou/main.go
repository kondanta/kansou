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
//	kansou score add "Frieren"         # start a scoring session
//	kansou score publish               # publish the last score to AniList
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

	// Load config and wire dependencies. Config loading failures are fatal.
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("loading config", "err", err)
		os.Exit(1)
	}
	slog.Debug("config loaded", "dimensions", len(cfg.Dimensions))

	al := anilist.NewClient()
	eng := newEngine(cfg)
	app := cli.NewApp(cfg, al, eng)

	rootCmd.AddCommand(app.MediaCmd())
	rootCmd.AddCommand(app.ScoreCmd())
	rootCmd.AddCommand(newServeCmd(cfg, al, eng))

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
func newServeCmd(cfg *config.Config, al *anilist.Client, eng *scoring.Engine) *cobra.Command {
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
			srv := server.New(cfg, al, eng)
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
