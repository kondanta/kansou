package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kondanta/kansou/internal/anilist"
)

// mediaFindCmd returns the `media find` cobra command.
func (a *App) mediaFindCmd() *cobra.Command {
	var urlFlag string

	cmd := &cobra.Command{
		Use:   "find [query]",
		Short: "Search for media on AniList",
		Long: `Search AniList for an anime or manga entry and display its details.
Does not start a scoring session. Useful for verifying the correct entry before scoring.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runMediaFind(args, urlFlag)
		},
	}

	cmd.Flags().StringVar(&urlFlag, "url", "", "Fetch by direct AniList URL instead of searching")
	return cmd
}

// runMediaFind fetches media by search query or URL and prints a summary table.
func (a *App) runMediaFind(args []string, urlFlag string) error {
	var media *anilist.Media
	var err error

	switch {
	case urlFlag != "":
		id, parseErr := anilist.ParseMediaURL(urlFlag)
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", parseErr)
			os.Exit(1)
		}
		media, err = a.AniList.FetchByID(id)
	case len(args) > 0:
		media, err = a.AniList.SearchByName(args[0], "")
	default:
		fmt.Fprintf(os.Stderr, "error: provide a search query or --url\n")
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	printMediaCard(media)
	return nil
}

// printMediaCard renders a media entry as a bordered table to stdout.
func printMediaCard(m *anilist.Media) {
	width := 51

	title := m.TitleRomaji
	if m.TitleEnglish != "" && m.TitleEnglish != m.TitleRomaji {
		title = m.TitleEnglish
	}

	line := strings.Repeat("─", width-2)
	fmt.Printf("┌%s┐\n", line)
	fmt.Printf("│  %-*s│\n", width-4, truncate(title, width-4))
	if m.TitleNative != "" {
		fmt.Printf("│  %-*s│\n", width-4, truncate(m.TitleNative, width-4))
	}
	fmt.Printf("├%s┤\n", line)

	mediaTypeLabel := "Anime"
	if m.MediaType == "MANGA" {
		mediaTypeLabel = "Manga"
	}
	fmt.Printf("│  %-12s│  %-*s│\n", "Type", width-18, truncate(mediaTypeLabel+" ("+m.Format+")", width-18))
	fmt.Printf("│  %-12s│  %-*s│\n", "Status", width-18, truncate(m.Status, width-18))
	if m.Episodes > 0 {
		fmt.Printf("│  %-12s│  %-*s│\n", "Episodes", width-18, fmt.Sprintf("%d", m.Episodes))
	}
	if m.Chapters > 0 {
		fmt.Printf("│  %-12s│  %-*s│\n", "Chapters", width-18, fmt.Sprintf("%d", m.Chapters))
	}

	anilistURL := fmt.Sprintf("https://anilist.co/%s/%d", strings.ToLower(string(m.MediaType)), m.ID)
	fmt.Printf("│  %-12s│  %-*s│\n", "AniList", width-18, truncate(anilistURL, width-18))

	fmt.Printf("├%s┤\n", line)
	fmt.Printf("│  %-12s│  %-*s│\n", "Genres", width-18, truncate(strings.Join(m.Genres, ", "), width-18))

	fmt.Printf("├%s┤\n", line)
	community := fmt.Sprintf("AniList avg: %d  /  mean: %d", m.AverageScore, m.MeanScore)
	fmt.Printf("│  %-12s│  %-*s│\n", "Community", width-18, truncate(community, width-18))

	fmt.Printf("└%s┘\n", line)
}

// truncate shortens s to at most max runes, appending "…" if truncated.
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}
