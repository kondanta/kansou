package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/logger"
	"github.com/kondanta/kansou/internal/server"
)

// serveCmd returns the `kansou serve` cobra command.
func (a *App) serveCmd() *cobra.Command {
	var (
		port       int
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
			srv := server.New(a.Config, a.AniList, a.Engine, liveConfig, a.ConfigPath)
			if err := srv.ListenAndServe(port); err != nil {
				slog.Error("server error", "err", err)
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&port, "port", 0, "port to listen on (overrides config)")
	cmd.Flags().BoolVar(
		&liveConfig, "live-config", false,
		"enable GET /config and POST /config endpoints for runtime config editing",
	)
	return cmd
}
