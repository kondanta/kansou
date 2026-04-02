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

// SessionState holds the result of a completed score add session.
// It is nil until score add runs successfully.
// It contains both the scoring result and the AniList media identity,
// kept together because score publish needs both.
type SessionState struct {
	// MediaID is the AniList media ID from the scored entry.
	MediaID int
	// Title is the romanised title, printed on publish confirmation.
	Title string
	// Result is the full scoring result including the breakdown.
	Result scoring.Result
}

// App owns all shared CLI dependencies and session state.
// It is constructed once in main.go and never modified after construction,
// except for Session which is set by score add and read by score publish.
type App struct {
	// Config is the loaded and validated application config.
	Config *config.Config
	// AniList is the AniList GraphQL client.
	AniList *anilist.Client
	// Engine is the scoring engine wired with the current config.
	Engine *scoring.Engine
	// Session is nil until score add completes successfully.
	Session *SessionState
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
	cmd.AddCommand(a.scorePublishCmd())
	return cmd
}
