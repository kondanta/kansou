package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kondanta/kansou/internal/anilist"
	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
)

// scoreAddCmd returns the `score add` cobra command.
func (a *App) scoreAddCmd() *cobra.Command {
	var urlFlag string
	var typeFlag string
	var breakdownFlag bool
	var weightFlag string

	cmd := &cobra.Command{
		Use:   "add [query]",
		Short: "Start an interactive scoring session",
		Long: `Start an interactive scoring session for a media entry.
Prompts for a score (1–10) for each configured dimension.
Enter 's' or 'skip' to mark a dimension as not applicable.

The calculated score is held in memory. Use 'score publish' to write it to AniList.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runScoreAdd(args, urlFlag, typeFlag, breakdownFlag, weightFlag)
		},
	}

	cmd.Flags().StringVar(&urlFlag, "url", "", "Fetch by direct AniList URL instead of searching")
	cmd.Flags().StringVar(&typeFlag, "type", "", "Media type filter: anime or manga")
	cmd.Flags().BoolVar(&breakdownFlag, "breakdown", false, "Show weighted contribution table after scoring")
	cmd.Flags().StringVar(&weightFlag, "weight", "", "Override dimension weights for this session (e.g. pacing=0.05,world_building=0.20)")
	return cmd
}


// runScoreAdd fetches the media entry, runs the interactive prompt loop,
// calculates the score, and stores the result in a.Session.
func (a *App) runScoreAdd(args []string, urlFlag, typeFlag string, breakdown bool, weightFlag string) error {
	// Parse --type and --weight before any network I/O.
	mediaType, err := resolveMediaType(typeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	overrides, err := parseWeightFlag(weightFlag, a.Config.Dimensions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Fetch media.
	var media *anilist.Media
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

	// Display media header.
	episodes := ""
	if media.Episodes > 0 {
		episodes = fmt.Sprintf(" · %d episodes", media.Episodes)
	} else if media.Chapters > 0 {
		episodes = fmt.Sprintf(" · %d chapters", media.Chapters)
	}
	fmt.Printf("\nFound: %s (%s · %s%s)\n", media.TitleRomaji, string(media.MediaType), media.Format, episodes)
	if len(media.Genres) > 0 {
		fmt.Printf("Genres: %s\n\n", strings.Join(media.Genres, ", "))
	}

	fmt.Println("Score each dimension from 1 to 10. Decimals accepted (e.g. 7.5).")
	fmt.Println("Enter 's' or 'skip' to mark a dimension as not applicable.")
	fmt.Println()

	// Interactive prompt loop.
	reader := bufio.NewReader(os.Stdin)
	scores := make(map[string]float64, len(a.Config.DimensionOrder))
	skipped := make(map[string]bool)

	for _, key := range a.Config.DimensionOrder {
		def := a.Config.Dimensions[key]
		fmt.Printf("  %-15s— %s\n", def.Label, def.Description)

		for {
			fmt.Print("  > ")
			line, err := reader.ReadString('\n')
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nsession cancelled — no score was published\n")
				os.Exit(0)
			}
			input := strings.TrimSpace(line)

			if input == "s" || input == "skip" {
				// Check: can't skip an overridden dimension.
				if _, overridden := overrides[key]; overridden {
					fmt.Fprintf(os.Stderr, "error: dimension %q was both weight-overridden and skipped — these are mutually exclusive\n", key)
					os.Exit(1)
				}
				skipped[key] = true
				fmt.Printf("  ✓ %s marked as not applicable — excluded from score\n\n", def.Label)
				break
			}

			score, parseErr := strconv.ParseFloat(input, 64)
			if parseErr != nil {
				fmt.Println("  invalid: enter a number between 1 and 10, or 's' to skip")
				continue
			}
			if score < 1 || score > 10 {
				fmt.Println("  invalid: score must be between 1 and 10")
				continue
			}

			scores[key] = score
			fmt.Println()
			break
		}
	}

	// Check: all skipped?
	if len(skipped) == len(a.Config.DimensionOrder) {
		fmt.Fprintf(os.Stderr, "error: all dimensions were skipped — at least one dimension must be scored\n")
		os.Exit(1)
	}

	// Determine matched genres for session meta.
	matchedGenreList := matchedGenres(media.Genres, a.Config.Genres)

	entry := scoring.Entry{
		Scores:            scores,
		SkippedDimensions: skipped,
		WeightOverrides:   overrides,
		Genres:            media.Genres,
		Meta: scoring.SessionMeta{
			MediaID:       media.ID,
			TitleRomaji:   media.TitleRomaji,
			TitleEnglish:  media.TitleEnglish,
			MediaType:     media.MediaType,
			AniListURL:    fmt.Sprintf("https://anilist.co/%s/%d", strings.ToLower(string(media.MediaType)), media.ID),
			AllGenres:     media.Genres,
			MatchedGenres: matchedGenreList,
			ConfigHash:    a.Config.DimensionsHash,
		},
	}

	result, err := a.Engine.Score(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Print final score.
	fmt.Println("──────────────────────────────")
	fmt.Printf("  Final Score   %.2f / 10\n", result.FinalScore)
	fmt.Println("──────────────────────────────")
	fmt.Println()

	if breakdown {
		printBreakdown(result)
	}

	// Prompt to publish.
	fmt.Print("Publish to AniList? [y/N]: ")
	line, err := reader.ReadString('\n')
	if err != nil || strings.ToLower(strings.TrimSpace(line)) != "y" {
		return nil
	}

	pub, pubErr := a.AniList.PublishScore(media.ID, result.FinalScore)
	if pubErr != nil {
		fmt.Fprintf(os.Stderr, "error: failed to publish score to AniList: %v\n", pubErr)
		fmt.Fprintf(os.Stderr, "       your calculated score was %.2f\n", result.FinalScore)
		os.Exit(1)
	}
	fmt.Printf("✓ Score published to AniList\n")
	fmt.Printf("  %s — %.2f\n", pub.TitleRomaji, pub.Score)
	return nil
}

// parseWeightFlag parses the --weight flag string into a map of dimension key → weight.
// Validates that keys exist in dims, values are in (0,1], and sum < 1.0.
func parseWeightFlag(flag string, dims map[string]config.DimensionDef) (map[string]float64, error) {
	if flag == "" {
		return map[string]float64{}, nil
	}
	result := map[string]float64{}
	pairs := strings.Split(flag, ",")
	sum := 0.0
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid --weight format %q — expected key=value pairs separated by commas", pair)
		}
		key := strings.TrimSpace(parts[0])
		valStr := strings.TrimSpace(parts[1])

		if _, ok := dims[key]; !ok {
			return nil, fmt.Errorf("unknown dimension %q in --weight flag", key)
		}
		val, err := strconv.ParseFloat(valStr, 64)
		if err != nil || val <= 0 || val > 1.0 {
			return nil, fmt.Errorf("invalid weight %q for dimension %q — must be > 0.0 and ≤ 1.0", valStr, key)
		}
		result[key] = val
		sum += val
	}
	if sum >= 1.0 {
		return nil, fmt.Errorf("sum of --weight overrides is %.3f — must be < 1.0 so remaining dimensions can share the rest", sum)
	}
	return result, nil
}

// printBreakdown renders the full per-dimension breakdown table to stdout.
func printBreakdown(result scoring.Result) {
	sep := strings.Repeat("─", 78)
	fmt.Println(sep)
	fmt.Printf("  %-15s  %-6s  %-8s  %-11s  %-8s  %-12s\n",
		"Dimension", "Score", "Base W", "Multiplier", "Final W", "Contribution")
	fmt.Println(sep)

	biasNote := false
	genreNote := false

	for _, row := range result.Breakdown {
		scoreStr := fmt.Sprintf("%.1f", row.Score)
		baseW := fmt.Sprintf("%.2f%%", row.BaseWeight*100)
		mult := fmt.Sprintf("×%.2f", row.AppliedMultiplier)
		finalW := fmt.Sprintf("%.2f%%", row.FinalWeight*100)
		contrib := fmt.Sprintf("%.2f", row.Contribution)

		annotations := ""
		if row.Skipped {
			scoreStr = "—"
			mult = "—"
			finalW = "—"
			contrib = "—"
			annotations = " [skipped]"
		} else if row.WeightOverride {
			annotations = " [overridden]"
		} else if row.AppliedMultiplier != 1.0 {
			annotations = " [genre adjusted]"
			genreNote = true
		}
		if row.BiasResistant && !row.Skipped {
			mult += "  *"
			biasNote = true
		}

		fmt.Printf("  %-15s  %-6s  %-8s  %-11s  %-8s  %-12s%s\n",
			row.Label, scoreStr, baseW, mult, finalW, contrib, annotations)
	}

	fmt.Println(sep)
	fmt.Printf("  %-15s  %-6s  %-8s  %-11s  %-8s  %.2f / 10\n",
		"Final Score", "", "", "", "", result.FinalScore)
	fmt.Println(sep)

	if biasNote {
		fmt.Println("  * bias-resistant — genre multipliers not applied")
	}

	// Genre match detail.
	if len(result.Meta.AllGenres) > 0 {
		fmt.Printf("  Genres returned by AniList : %s\n", strings.Join(result.Meta.AllGenres, ", "))
	}
	if len(result.Meta.MatchedGenres) > 0 {
		fmt.Printf("  Genres matched in config   : %s\n", strings.Join(result.Meta.MatchedGenres, ", "))
	}
	unmatched := unmatchedGenres(result.Meta.AllGenres, result.Meta.MatchedGenres)
	if len(unmatched) > 0 {
		fmt.Printf("  Genres unmatched           : %s\n", strings.Join(unmatched, ", "))
	}
	_ = genreNote
	fmt.Println()
}

// unmatchedGenres returns genres in all that are not in matched.
func unmatchedGenres(all, matched []string) []string {
	matchedSet := make(map[string]bool, len(matched))
	for _, g := range matched {
		matchedSet[strings.ToLower(g)] = true
	}
	out := make([]string, 0)
	for _, g := range all {
		if !matchedSet[strings.ToLower(g)] {
			out = append(out, g)
		}
	}
	return out
}

// matchedGenres returns the subset of genres that have a config entry (case-insensitive).
func matchedGenres(genres []string, configGenres map[string]map[string]float64) []string {
	matched := make([]string, 0, len(genres))
	for _, g := range genres {
		lower := strings.ToLower(g)
		if _, ok := configGenres[lower]; ok {
			matched = append(matched, g)
		}
	}
	return matched
}
