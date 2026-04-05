package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kondanta/kansou/internal/anilist"
	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
)

// scoreCmd returns the `score` cobra command and its subcommands.
func (a *App) scoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "score",
		Short: "Scoring commands",
		Long:  "Commands for scoring anime and manga and publishing scores to AniList.",
	}
	cmd.AddCommand(a.scoreAddCmd())
	return cmd
}

// scoreAddCmd returns the `score add` cobra command.
func (a *App) scoreAddCmd() *cobra.Command {
	var urlFlag string
	var typeFlag string
	var breakdownFlag bool
	var weightFlag string
	var primaryGenreFlag string
	var notesFlag bool

	cmd := &cobra.Command{
		Use:   "add [query]",
		Short: "Start an interactive scoring session",
		Long: `Start an interactive scoring session for a media entry.
Prompts for a score (1–10) for each configured dimension.
Enter 's' or 'skip' to mark a dimension as not applicable.

After scoring, prompts whether to publish the result to AniList.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runScoreAdd(args, urlFlag, typeFlag, breakdownFlag, weightFlag, primaryGenreFlag, notesFlag)
		},
	}

	cmd.Flags().StringVar(&urlFlag, "url", "", "Fetch by direct AniList URL instead of searching")
	cmd.Flags().StringVar(&typeFlag, "type", "", "Media type filter: anime or manga")
	cmd.Flags().BoolVar(&breakdownFlag, "breakdown", false, "Show weighted contribution table after scoring")
	cmd.Flags().StringVar(&weightFlag, "weight", "", "Override dimension weights for this session (e.g. pacing=0.05,world_building=0.20)")
	cmd.Flags().StringVar(&primaryGenreFlag, "primary-genre", "", "Designate one genre as primary for blended multiplier calculation (e.g. Mystery)")
	cmd.Flags().BoolVar(&notesFlag, "notes", false, "Append scoring breakdown to AniList list entry notes when publishing")
	return cmd
}

// runScoreAdd fetches the media entry, runs the interactive prompt loop,
// calculates the score, and offers to publish it to AniList.
func (a *App) runScoreAdd(args []string, urlFlag, typeFlag string, breakdown bool, weightFlag, primaryGenreFlag string, notesFlag bool) error {
	// Parse --type and --weight before any network I/O.
	mediaType, err := resolveMediaType(typeFlag)
	if err != nil {
		return err
	}

	overrides, err := parseWeightFlag(weightFlag, a.Config.Dimensions)
	if err != nil {
		return err
	}

	// Fetch media.
	var media *anilist.Media
	switch {
	case urlFlag != "":
		id, parseErr := anilist.ParseMediaURL(urlFlag)
		if parseErr != nil {
			return parseErr
		}
		media, err = a.AniList.FetchByID(id)
		if err != nil {
			return err
		}
	case len(args) > 0:
		results, searchErr := a.AniList.SearchByNameMulti(args[0], mediaType)
		if searchErr != nil {
			return searchErr
		}
		media, err = pickMedia(results)
		if errors.Is(err, errUserCancelled) {
			return nil
		}
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("provide a search query or --url")
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

	// Reader is shared across the primary genre prompt and the scoring loop.
	reader := bufio.NewReader(os.Stdin)

	// Resolve primary genre — from flag (bypasses prompt) or interactively.
	primaryGenre, err := resolvePrimaryGenre(primaryGenreFlag, media.Genres, reader)
	if errors.Is(err, errUserCancelled) {
		return nil
	}
	if err != nil {
		return err
	}
	// When the user designated a primary genre interactively, confirm it with the blend ratio.
	if primaryGenre != "" && primaryGenreFlag == "" {
		blendPct := int(a.Config.PrimaryGenreWeight * 100)
		fmt.Printf("Primary genre set: %s (blend %d/%d)\n\n", primaryGenre, blendPct, 100-blendPct)
	}

	fmt.Println("Score each dimension from 1 to 10. Decimals accepted (e.g. 7.5).")
	fmt.Println("Enter 's' or 'skip' to mark a dimension as not applicable.")
	fmt.Println()

	// Interactive scoring loop.
	scores := make(map[string]float64, len(a.Config.DimensionOrder))
	skipped := make(map[string]bool)

	for _, key := range a.Config.DimensionOrder {
		def := a.Config.Dimensions[key]
		fmt.Printf("  %-15s— %s\n", def.Label, def.Description)

		for {
			fmt.Print("  > ")
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				fmt.Fprintln(os.Stderr, "\nsession cancelled — no score was published")
				return nil
			}
			input := strings.TrimSpace(line)

			if input == "s" || input == "skip" {
				// Can't skip an overridden dimension.
				if _, overridden := overrides[key]; overridden {
					return fmt.Errorf("dimension %q was both weight-overridden and skipped — these are mutually exclusive", key)
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

	if len(skipped) == len(a.Config.DimensionOrder) {
		return fmt.Errorf("all dimensions were skipped — at least one dimension must be scored")
	}

	entry := scoring.Entry{
		Scores:            scores,
		SkippedDimensions: skipped,
		WeightOverrides:   overrides,
		Genres:            media.Genres,
		PrimaryGenre:      primaryGenre,
		Meta: scoring.SessionMeta{
			MediaID:            media.ID,
			TitleRomaji:        media.TitleRomaji,
			TitleEnglish:       media.TitleEnglish,
			MediaType:          media.MediaType,
			AniListURL:         fmt.Sprintf("https://anilist.co/%s/%d", strings.ToLower(string(media.MediaType)), media.ID),
			AllGenres:          media.Genres,
			ConfigHash:         a.Config.DimensionsHash,
			PrimaryGenre:       primaryGenre,
			PrimaryGenreWeight: a.Config.PrimaryGenreWeight,
		},
	}

	result, err := a.Engine.Score(entry)
	if err != nil {
		return err
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

	notes := ""
	if notesFlag {
		notes = formatNote(result)
	}

	pub, pubErr := a.AniList.PublishScore(media.ID, result.FinalScore, notes)
	if pubErr != nil {
		return fmt.Errorf("publishing score to AniList (your calculated score was %.2f): %w", result.FinalScore, pubErr)
	}
	fmt.Printf("✓ Score published to AniList\n")
	fmt.Printf("  %s — %.2f\n", pub.TitleRomaji, pub.Score)
	if notes != "" {
		fmt.Println("  ✓ Scoring breakdown appended to list entry notes")
	}
	return nil
}

// parseWeightFlag parses the --weight flag string into a map of dimension key → weight.
// Validates that keys exist in dims, values are in (0,1], and sum < 1.0.
func parseWeightFlag(flag string, dims map[string]config.DimensionDef) (map[string]float64, error) {
	if flag == "" {
		return nil, nil
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
		} else if row.PrimaryGenre != "" && !row.BiasResistant {
			annotations = " [primary blended]"
		} else if row.AppliedMultiplier != 1.0 {
			annotations = " [genre adjusted]"
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

	// Primary genre annotation.
	if result.Meta.PrimaryGenre != "" {
		blendPct := int(result.Meta.PrimaryGenreWeight * 100)
		fmt.Printf("  Primary genre          : %s [primary] (blend %d/%d)\n",
			result.Meta.PrimaryGenre, blendPct, 100-blendPct)
	}

	// Genre match detail.
	if len(result.Meta.AllGenres) > 0 {
		fmt.Printf("  Genres returned        : %s\n", strings.Join(result.Meta.AllGenres, ", "))
	}
	if len(result.Meta.MatchedGenres) > 0 {
		fmt.Printf("  Genres matched config  : %s\n", annotateMatchedGenres(result.Meta.MatchedGenres, result.Meta.PrimaryGenre))
	}
	unmatched := unmatchedGenres(result.Meta.AllGenres, result.Meta.MatchedGenres)
	if len(unmatched) > 0 {
		fmt.Printf("  Genres unmatched       : %s\n", strings.Join(unmatched, ", "))
	}
	fmt.Println()
}

// unmatchedGenres returns genres in all that are not in matched.
func unmatchedGenres(all, matched []string) []string {
	matchedSet := make(map[string]bool, len(matched))
	for _, g := range matched {
		matchedSet[strings.ToLower(g)] = true
	}
	var out []string
	for _, g := range all {
		if !matchedSet[strings.ToLower(g)] {
			out = append(out, g)
		}
	}
	return out
}

// resolvePrimaryGenre returns the canonical primary genre string (as returned by AniList).
// If flagVal is non-empty, it is validated against mediaGenres and returned immediately —
// the interactive prompt is bypassed entirely.
// If flagVal is empty and mediaGenres is non-empty, the user is prompted interactively.
// Empty input (Enter) skips designation. Returns errUserCancelled on EOF.
func resolvePrimaryGenre(flagVal string, mediaGenres []string, reader *bufio.Reader) (string, error) {
	if flagVal != "" {
		// --primary-genre flag: validate and return without prompting.
		lower := strings.ToLower(flagVal)
		for _, g := range mediaGenres {
			if strings.ToLower(g) == lower {
				return g, nil
			}
		}
		return "", fmt.Errorf("primary genre %q is not in the media's genre list: %s",
			flagVal, strings.Join(mediaGenres, ", "))
	}

	if len(mediaGenres) == 0 {
		return "", nil
	}

	fmt.Println("Designate a primary genre? (enter genre name or press Enter to skip):")
	for {
		fmt.Print("  > ")
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			fmt.Fprintln(os.Stderr, "\nsession cancelled — no score was published")
			return "", errUserCancelled
		}
		input := strings.TrimSpace(line)
		if input == "" {
			fmt.Println()
			return "", nil
		}
		lower := strings.ToLower(input)
		for _, g := range mediaGenres {
			if strings.ToLower(g) == lower {
				return g, nil
			}
		}
		fmt.Printf("  %q is not a genre of this show. Choose from: %s (or press Enter to skip):\n",
			input, strings.Join(mediaGenres, ", "))
	}
}

// annotateMatchedGenres renders the matched genre list with the primary genre
// annotated as "[primary]". Returns the plain joined string when no primary is set.
func annotateMatchedGenres(matched []string, primaryGenre string) string {
	if primaryGenre == "" {
		return strings.Join(matched, ", ")
	}
	lowerPrimary := strings.ToLower(primaryGenre)
	parts := make([]string, len(matched))
	for i, g := range matched {
		if strings.ToLower(g) == lowerPrimary {
			parts[i] = g + " [primary]"
		} else {
			parts[i] = g
		}
	}
	return strings.Join(parts, ", ")
}

// formatNote builds the plain-text scoring breakdown to be stored as an AniList
// list entry note. The caller is responsible for appending to any existing notes.
func formatNote(result scoring.Result) string {
	const sep = "───────────────────────────────────────────────────────"
	var b strings.Builder

	fmt.Fprintf(&b, "%s\n", result.Meta.TitleRomaji)
	fmt.Fprintf(&b, "Score: %.2f / 10  [kansou]\n\n", result.FinalScore)
	fmt.Fprintf(&b, "%-15s  %5s  %6s  %6s  %6s  Contrib\n",
		"Dimension", "Score", "BaseW", "×Mult", "FinalW")
	fmt.Fprintln(&b, sep)

	for _, row := range result.Breakdown {
		if row.Skipped {
			fmt.Fprintf(&b, "%-15s  %5s  %6s  %6s  %6s  —\n",
				row.Label, "—", "—", "—", "—")
			continue
		}
		suffix := ""
		if row.BiasResistant {
			suffix = "  *"
		}
		fmt.Fprintf(&b, "%-15s  %5.1f  %6s  %6s  %6s  %.2f%s\n",
			row.Label, row.Score,
			fmt.Sprintf("%.1f%%", row.BaseWeight*100),
			fmt.Sprintf("×%.2f", row.AppliedMultiplier),
			fmt.Sprintf("%.1f%%", row.FinalWeight*100),
			row.Contribution, suffix)
	}

	fmt.Fprintln(&b)
	if result.Meta.PrimaryGenre != "" {
		blendPct := int(result.Meta.PrimaryGenreWeight * 100)
		fmt.Fprintf(&b, "Primary: %s (blend %d/%d)\n", result.Meta.PrimaryGenre, blendPct, 100-blendPct)
	}
	if len(result.Meta.AllGenres) > 0 {
		fmt.Fprintf(&b, "Genres:  %s\n", strings.Join(result.Meta.AllGenres, ", "))
	}
	if len(result.Meta.MatchedGenres) > 0 {
		fmt.Fprintf(&b, "Matched: %s\n", annotateMatchedGenres(result.Meta.MatchedGenres, result.Meta.PrimaryGenre))
	}
	fmt.Fprintf(&b, "Config:  %s", result.Meta.ConfigHash)

	return b.String()
}
