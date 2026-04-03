package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/kondanta/kansou/internal/logger"
	"github.com/kondanta/kansou/internal/server"
)

// serveCmd returns the `kansou serve` cobra command.
func (a *App) serveCmd() *cobra.Command {
	var port int

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the REST API server",
		Long: `Start the kansou REST API server.
Exposes the scoring engine and AniList integration over HTTP.
Swagger UI is available at /swagger/index.html.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger.Setup(true)
			srv := server.New(a.Config, a.AniList, a.Engine)
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
