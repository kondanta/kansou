package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/logger"
	"github.com/kondanta/kansou/internal/server"
)

// defaultCORSOrigins are the origins allowed when KANSOU_CORS_ORIGINS is not set.
var defaultCORSOrigins = []string{
	"http://localhost:3000",
	"http://localhost:5173",
	"http://localhost:8080",
}

// serveCmd returns the `kansou serve` cobra command.
func (a *App) serveCmd() *cobra.Command {
	var (
		portFlag   int
		liveConfig bool
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the REST API server",
		Long: `Start the kansou REST API server.
Exposes the scoring engine and AniList integration over HTTP.
Swagger UI is available at /swagger/index.html.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger.Setup(true)
			if liveConfig {
				if err := config.ProbeWritable(a.ConfigPath); err != nil {
					fmt.Fprintf(os.Stderr, "--live-config: %v\n", err)
					os.Exit(1)
				}
			}

			// Warn if the deprecated [server] block is present in the config file.
			if a.Config.Server.Port != 0 || len(a.Config.Server.CORSAllowedOrigins) > 0 {
				fmt.Fprintf(os.Stderr, "warning: [server] in config.toml is deprecated and ignored — "+
					"use KANSOU_PORT and KANSOU_CORS_ORIGINS env vars instead\n")
			}

			port := resolvePort(portFlag)
			corsOrigins := resolveCORSOrigins()
			dbType := os.Getenv("KANSOU_DB_TYPE")

			srv := server.New(a.Config, a.AniList, a.Engine, liveConfig, a.ConfigPath, a.Store, dbType, corsOrigins)
			if err := srv.ListenAndServe(port); err != nil {
				slog.Error("server error", "err", err)
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&portFlag, "port", 0, "port to listen on (overrides KANSOU_PORT env var)")
	cmd.Flags().BoolVar(
		&liveConfig, "live-config", false,
		"enable GET /config and POST /config endpoints for runtime config editing",
	)
	return cmd
}

// resolvePort returns the effective port: --port flag > KANSOU_PORT env var > 8080.
func resolvePort(flagValue int) int {
	if flagValue > 0 {
		return flagValue
	}
	if env := os.Getenv("KANSOU_PORT"); env != "" {
		if p, err := strconv.Atoi(env); err == nil && p > 0 {
			return p
		}
	}
	return 8080
}

// resolveCORSOrigins returns allowed CORS origins from KANSOU_CORS_ORIGINS or
// falls back to the built-in defaults.
func resolveCORSOrigins() []string {
	if env := os.Getenv("KANSOU_CORS_ORIGINS"); env != "" {
		var origins []string
		for _, o := range strings.Split(env, ",") {
			if s := strings.TrimSpace(o); s != "" {
				origins = append(origins, s)
			}
		}
		if len(origins) > 0 {
			return origins
		}
	}
	return defaultCORSOrigins
}
