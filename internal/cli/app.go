// Package cli implements the kansou command-line interface using cobra.
// All output to the user goes through this package. Business logic lives in
// internal/scoring/ and internal/anilist/ — this package only orchestrates
// and renders.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/kondanta/kansou/internal/anilist"
	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
)

// App owns all shared CLI dependencies.
// It is constructed once in main.go. Config, AniList, and Engine are set by
// PersistentPreRunE after flag parsing and are available to all subcommands.
type App struct {
	// Config is the loaded and validated application config.
	Config *config.Config
	// AniList is the AniList GraphQL client.
	AniList *anilist.Client
	// Engine is the scoring engine wired with the current config.
	Engine *scoring.Engine
}

// NewApp constructs an App with all dependencies wired.
func NewApp(cfg *config.Config, al *anilist.Client, eng *scoring.Engine) *App {
	return &App{
		Config:  cfg,
		AniList: al,
		Engine:  eng,
	}
}

// MediaCmd returns the `media` cobra command and its subcommands.
func (a *App) MediaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "media",
		Short: "Media discovery commands",
		Long:  "Commands for searching and fetching media information from AniList.",
	}
	cmd.AddCommand(a.mediaFindCmd())
	return cmd
}

// ScoreCmd returns the `score` cobra command and its subcommands.
func (a *App) ScoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "score",
		Short: "Scoring commands",
		Long:  "Commands for scoring anime and manga and publishing scores to AniList.",
	}
	cmd.AddCommand(a.scoreAddCmd())
	return cmd
}
