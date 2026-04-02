package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kondanta/kansou/internal/anilist"
)

// mediaFindCmd returns the `media find` cobra command.
func (a *App) mediaFindCmd() *cobra.Command {
	var urlFlag string
	var typeFlag string

	cmd := &cobra.Command{
		Use:   "find [query]",
		Short: "Search for media on AniList",
		Long: `Search AniList for an anime or manga entry and display its details.
Does not start a scoring session. Useful for verifying the correct entry before scoring.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runMediaFind(args, urlFlag, typeFlag)
		},
	}

	cmd.Flags().StringVar(&urlFlag, "url", "", "Fetch by direct AniList URL instead of searching")
	cmd.Flags().StringVar(&typeFlag, "type", "", "Media type filter: anime or manga")
	return cmd
}

// runMediaFind fetches media by search query or URL and prints a summary table.
func (a *App) runMediaFind(args []string, urlFlag, typeFlag string) error {
	var media *anilist.Media
	var err error

	mediaType, err := resolveMediaType(typeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	switch {
	case urlFlag != "":
		id, parseErr := anilist.ParseMediaURL(urlFlag)
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", parseErr)
			os.Exit(1)
		}
		media, err = a.AniList.FetchByID(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case len(args) > 0:
		results, searchErr := a.AniList.SearchByNameMulti(args[0], mediaType)
		if searchErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", searchErr)
			os.Exit(1)
		}
		media, err = pickMedia(results)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "error: provide a search query or --url\n")
		os.Exit(1)
	}

	printMediaCard(media)
	return nil
}

// pickMedia presents a numbered list of results and returns the one the user
// selects. If there is only one result it is returned immediately without prompting.
func pickMedia(results []anilist.Media) (*anilist.Media, error) {
	if len(results) == 1 {
		return &results[0], nil
	}

	fmt.Println()
	for i, m := range results {
		title := m.TitleRomaji
		if m.TitleEnglish != "" && m.TitleEnglish != m.TitleRomaji {
			title = m.TitleEnglish
		}
		extra := ""
		if m.Episodes > 0 {
			extra = fmt.Sprintf(" · %d eps", m.Episodes)
		} else if m.Chapters > 0 {
			extra = fmt.Sprintf(" · %d ch", m.Chapters)
		}
		fmt.Printf("  %d. %-45s (%s%s · %s)\n",
			i+1,
			truncate(title, 45),
			m.Format,
			extra,
			m.Status,
		)
	}
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("Pick a result [1–%d]: ", len(results))
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "\ncancelled\n")
			os.Exit(0)
		}
		n, parseErr := strconv.Atoi(strings.TrimSpace(line))
		if parseErr != nil || n < 1 || n > len(results) {
			fmt.Printf("  invalid: enter a number between 1 and %d\n", len(results))
			continue
		}
		fmt.Println()
		return &results[n-1], nil
	}
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

// resolveMediaType normalises a --type flag value to an AniList MediaType string.
// Accepts "anime", "manga" (case-insensitive) or "" (no filter).
// Returns an error for any other value.
func resolveMediaType(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "anime":
		return "ANIME", nil
	case "manga":
		return "MANGA", nil
	case "":
		return "", nil
	default:
		return "", fmt.Errorf("invalid --type %q — must be anime or manga", s)
	}
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
