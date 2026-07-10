package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/kondanta/kansou/internal/anilist"
	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
	"github.com/kondanta/kansou/internal/store"
	"github.com/spf13/cobra"
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
			return a.runScoreAdd(
				cmd.Context(),
				args,
				urlFlag,
				typeFlag,
				breakdownFlag,
				weightFlag,
				primaryGenreFlag,
				notesFlag,
			)
		},
	}

	cmd.Flags().StringVar(&urlFlag, "url", "", "Fetch by direct AniList URL instead of searching")
	cmd.Flags().StringVar(&typeFlag, "type", "", "Media type filter: anime or manga")
	cmd.Flags().
		BoolVar(&breakdownFlag, "breakdown", false, "Show weighted contribution table after scoring")
	cmd.Flags().StringVar(
		&weightFlag, "weight", "",
		"Override dimension weights for this session (e.g. pacing=0.05,world_building=0.20)",
	)
	cmd.Flags().StringVar(
		&primaryGenreFlag, "primary-genre", "",
		"Designate one genre as primary for blended multiplier calculation (e.g. Mystery)",
	)
	cmd.Flags().
		BoolVar(&notesFlag, "notes", false, "Append scoring breakdown to AniList list entry notes when publishing")
	return cmd
}

// runScoreAdd fetches the media entry, runs the interactive prompt loop,
// calculates the score, and offers to publish it to AniList.
func (a *App) runScoreAdd(
	ctx context.Context,
	args []string,
	urlFlag, typeFlag string,
	breakdown bool,
	weightFlag, primaryGenreFlag string,
	notesFlag bool,
) error {
	mediaType, err := resolveMediaType(typeFlag)
	if err != nil {
		return err
	}

	overrides, err := parseWeightFlag(weightFlag, a.Config.Dimensions)
	if err != nil {
		return err
	}

	media, err := fetchMedia(ctx, args, urlFlag, mediaType, a.AniList)
	if errors.Is(err, errUserCancelled) {
		return nil
	}
	if err != nil {
		return err
	}

	printMediaHeader(media)

	prevScores, prevSkipped, err := a.loadPreviousScores(ctx, media.ID)
	if err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)

	session, err := a.collectScores(
		reader, media.Genres, primaryGenreFlag, overrides, prevScores, prevSkipped,
	)
	if errors.Is(err, errUserCancelled) {
		return nil
	}
	if err != nil {
		return err
	}

	entry := buildScoringEntry(media, session, overrides, a.Config)

	result, err := a.Engine.Score(entry)
	if err != nil {
		return err
	}

	a.saveScoreToStore(ctx, result)

	fmt.Println("──────────────────────────────")
	fmt.Printf("  Final Score   %.2f / 10\n", result.FinalScore)
	fmt.Println("──────────────────────────────")
	fmt.Println()

	if breakdown {
		printBreakdown(result)
	}

	return a.promptAndPublish(ctx, reader, media.ID, result, notesFlag)
}

// scoringSession holds the outcome of an interactive scoring prompt: the
// collected per-dimension scores, the skipped dimensions, and the resolved
// primary genre (if any).
type scoringSession struct {
	Scores       map[string]float64
	Skipped      map[string]bool
	PrimaryGenre string
}

// collectScores resolves the primary genre, runs the interactive per-dimension
// scoring prompt, and validates that at least one dimension was scored.
// Returns errUserCancelled if the user aborts the primary genre picker or the
// scoring loop.
func (a *App) collectScores(
	reader *bufio.Reader,
	genres []string,
	primaryGenreFlag string,
	overrides, prevScores map[string]float64,
	prevSkipped map[string]bool,
) (scoringSession, error) {
	primaryGenre, err := resolvePrimaryGenre(primaryGenreFlag, genres, reader)
	if err != nil {
		return scoringSession{}, err
	}
	if primaryGenre != "" && primaryGenreFlag == "" {
		blendPct := int(a.Config.PrimaryGenreWeight * 100)
		fmt.Printf("Primary genre set: %s (blend %d/%d)\n\n", primaryGenre, blendPct, 100-blendPct)
	}

	fmt.Println("Score each dimension from 1 to 10. Decimals accepted (e.g. 7.5).")
	fmt.Println("Enter 's' or 'skip' to mark a dimension as not applicable.")
	if len(prevScores) > 0 || len(prevSkipped) > 0 {
		fmt.Println("Press Enter to keep the previous value for a dimension.")
	}
	fmt.Println()

	scores, skipped, err := runScoringLoop(
		reader, a.Config.DimensionOrder, a.Config.Dimensions, overrides, prevScores, prevSkipped,
	)
	if err != nil {
		return scoringSession{}, err
	}

	if len(skipped) == len(a.Config.DimensionOrder) {
		return scoringSession{}, fmt.Errorf(
			"all dimensions were skipped — at least one dimension must be scored",
		)
	}

	return scoringSession{Scores: scores, Skipped: skipped, PrimaryGenre: primaryGenre}, nil
}

// printMediaHeader prints the resolved media's title, format, and genres.
func printMediaHeader(media *anilist.Media) {
	episodes := ""
	if media.Episodes > 0 {
		episodes = fmt.Sprintf(" · %d episodes", media.Episodes)
	} else if media.Chapters > 0 {
		episodes = fmt.Sprintf(" · %d chapters", media.Chapters)
	}
	fmt.Printf(
		"\nFound: %s (%s · %s%s)\n",
		media.TitleRomaji,
		string(media.MediaType),
		media.Format,
		episodes,
	)
	if len(media.Genres) > 0 {
		fmt.Printf("Genres: %s\n\n", strings.Join(media.Genres, ", "))
	}
}

// loadPreviousScores fetches the media's latest saved score (if a Store is
// configured) and returns its per-dimension scores and skipped dimensions
// for pre-filling the scoring prompt.
func (a *App) loadPreviousScores(
	ctx context.Context, mediaID int,
) (map[string]float64, map[string]bool, error) {
	if a.Store == nil {
		return nil, nil, nil
	}
	prev, err := a.Store.LatestScore(ctx, mediaID)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching previous score: %w", err)
	}
	if prev == nil {
		return nil, nil, nil
	}
	prevScores, prevSkipped := extractPrevScores(prev)
	fmt.Printf("Previous score: %.2f — pre-filling dimensions\n\n", prev.FinalScore)
	return prevScores, prevSkipped, nil
}

// buildScoringEntry assembles a scoring.Entry from the resolved media,
// collected scoring session, and session-level weight overrides.
func buildScoringEntry(
	media *anilist.Media,
	session scoringSession,
	overrides map[string]float64,
	cfg *config.Config,
) scoring.Entry {
	return scoring.Entry{
		Scores:            session.Scores,
		SkippedDimensions: session.Skipped,
		WeightOverrides:   overrides,
		Genres:            media.Genres,
		PrimaryGenre:      session.PrimaryGenre,
		Meta: scoring.SessionMeta{
			MediaID:      media.ID,
			TitleRomaji:  media.TitleRomaji,
			TitleEnglish: media.TitleEnglish,
			MediaType:    media.MediaType,
			Format:       media.Format,
			AniListURL: fmt.Sprintf(
				"https://anilist.co/%s/%d",
				strings.ToLower(string(media.MediaType)),
				media.ID,
			),
			AllGenres:          media.Genres,
			ConfigHash:         cfg.DimensionsHash,
			PrimaryGenre:       session.PrimaryGenre,
			PrimaryGenreWeight: cfg.PrimaryGenreWeight,
			CoverImage:         media.CoverImage,
		},
	}
}

// saveScoreToStore persists the result to the configured Store, if any,
// warning to stderr on failure rather than aborting the session.
func (a *App) saveScoreToStore(ctx context.Context, result scoring.Result) {
	if a.Store == nil {
		return
	}
	if err := a.Store.SaveScore(ctx, result, a.Config, a.Config.MaxHistory); err != nil {
		fmt.Fprintf(os.Stderr, "warning: saving score to database: %v\n", err)
	}
}

// promptAndPublish prompts the user to publish the result to AniList and, if
// confirmed, submits the score (with optional breakdown notes).
func (a *App) promptAndPublish(
	ctx context.Context, reader *bufio.Reader, mediaID int, result scoring.Result, notesFlag bool,
) error {
	fmt.Print("Publish to AniList? [y/N]: ")
	line, err := reader.ReadString('\n')
	if err != nil || strings.ToLower(strings.TrimSpace(line)) != "y" {
		return nil
	}

	notes := ""
	if notesFlag {
		notes = formatNote(result)
	}

	pub, err := a.AniList.PublishScore(ctx, mediaID, result.FinalScore, notes)
	if err != nil {
		return fmt.Errorf(
			"publishing score to AniList (your calculated score was %.2f): %w",
			result.FinalScore, err,
		)
	}
	fmt.Printf("✓ Score published to AniList\n")
	fmt.Printf("  %s — %.2f\n", pub.TitleRomaji, pub.Score)
	if notes != "" {
		fmt.Println("  ✓ Scoring breakdown appended to list entry notes")
	}
	return nil
}

// fetchMedia resolves the target media entry from either a direct URL or a
// search query. Returns errUserCancelled if the user aborts a search picker.
func fetchMedia(
	ctx context.Context, args []string, urlFlag, mediaType string, client *anilist.Client,
) (*anilist.Media, error) {
	switch {
	case urlFlag != "":
		id, err := anilist.ParseMediaURL(urlFlag)
		if err != nil {
			return nil, err
		}
		return client.FetchByID(ctx, id)
	case len(args) > 0:
		results, err := client.SearchByNameMulti(ctx, args[0], mediaType)
		if err != nil {
			return nil, err
		}
		return pickMedia(results)
	default:
		return nil, fmt.Errorf("provide a search query or --url")
	}
}

// runScoringLoop drives the interactive per-dimension scoring prompt.
// Returns scores and skipped maps on success, errUserCancelled on EOF,
// or an error if an overridden dimension is also skipped.
func runScoringLoop(
	reader *bufio.Reader,
	order []string,
	dims map[string]config.DimensionDef,
	overrides map[string]float64,
	prevScores map[string]float64,
	prevSkipped map[string]bool,
) (map[string]float64, map[string]bool, error) {
	scores := make(map[string]float64, len(order))
	skipped := make(map[string]bool)

	for _, key := range order {
		def := dims[key]
		printDimensionPrompt(key, def, prevScores, prevSkipped)

		score, isSkipped, err := promptDimensionScore(
			reader,
			key,
			def,
			overrides,
			prevScores,
			prevSkipped,
		)
		if err != nil {
			return nil, nil, err
		}
		if isSkipped {
			skipped[key] = true
		} else {
			scores[key] = score
		}
	}

	return scores, skipped, nil
}

// printDimensionPrompt prints a dimension's label, description, and — if a
// previous session scored or skipped it — a hint of that previous value.
func printDimensionPrompt(
	key string, def config.DimensionDef, prevScores map[string]float64, prevSkipped map[string]bool,
) {
	hint := ""
	if prevSkipped[key] {
		hint = " [prev: skipped]"
	} else if prev, ok := prevScores[key]; ok {
		hint = fmt.Sprintf(" [prev: %.1f]", prev)
	}
	fmt.Printf("  %-15s— %s%s\n", def.Label, def.Description, hint)
}

// promptDimensionScore reads input for a single dimension until it gets a
// valid score, a skip, or Enter (which reuses the previous value/skip state).
// Returns errUserCancelled on EOF, or an error if an overridden dimension is
// also skipped.
func promptDimensionScore(
	reader *bufio.Reader,
	key string,
	def config.DimensionDef,
	overrides map[string]float64,
	prevScores map[string]float64,
	prevSkipped map[string]bool,
) (float64, bool, error) {
	for {
		fmt.Print("  > ")
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			fmt.Fprintln(os.Stderr, "\nsession cancelled — no score was published")
			return 0, false, errUserCancelled
		}
		input := strings.TrimSpace(line)

		if input == "" {
			if prevSkipped[key] {
				if _, overridden := overrides[key]; overridden {
					return 0, false, fmt.Errorf(
						"dimension %q was both weight-overridden and skipped — these are mutually exclusive",
						key,
					)
				}
				fmt.Printf("  ✓ %s marked as not applicable — excluded from score\n\n", def.Label)
				return 0, true, nil
			}
			if prev, ok := prevScores[key]; ok {
				fmt.Println()
				return prev, false, nil
			}
			fmt.Println("  invalid: enter a number between 1 and 10, or 's' to skip")
			continue
		}

		if input == "s" || input == "skip" {
			if _, overridden := overrides[key]; overridden {
				return 0, false, fmt.Errorf(
					"dimension %q was both weight-overridden and skipped — these are mutually exclusive",
					key,
				)
			}
			fmt.Printf("  ✓ %s marked as not applicable — excluded from score\n\n", def.Label)
			return 0, true, nil
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

		fmt.Println()
		return score, false, nil
	}
}

// extractPrevScores converts a store.Score into the two maps consumed by
// runScoringLoop for dimension pre-fill.
func extractPrevScores(sc *store.Score) (map[string]float64, map[string]bool) {
	scores := make(map[string]float64, len(sc.Breakdown))
	skipped := make(map[string]bool)
	for _, row := range sc.Breakdown {
		if row.Skipped {
			skipped[row.DimensionKey] = true
		} else if row.Score != nil {
			scores[row.DimensionKey] = *row.Score
		}
	}
	return scores, skipped
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
			return nil, fmt.Errorf(
				"invalid --weight format %q — expected key=value pairs separated by commas",
				pair,
			)
		}
		key := strings.TrimSpace(parts[0])
		valStr := strings.TrimSpace(parts[1])

		if _, ok := dims[key]; !ok {
			return nil, fmt.Errorf("unknown dimension %q in --weight flag", key)
		}
		val, err := strconv.ParseFloat(valStr, 64)
		if err != nil || val <= 0 || val > 1.0 {
			return nil, fmt.Errorf(
				"invalid weight %q for dimension %q — must be > 0.0 and ≤ 1.0",
				valStr,
				key,
			)
		}
		result[key] = val
		sum += val
	}
	if sum >= 1.0 {
		return nil, fmt.Errorf(
			"sum of --weight overrides is %.3f — must be < 1.0 so remaining dimensions can share the rest",
			sum,
		)
	}
	return result, nil
}

// biasResistantMark annotates a bias-resistant dimension's multiplier column
// in breakdown tables (CLI and AniList notes).
const biasResistantMark = "  *"

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
		switch {
		case row.Skipped:
			scoreStr = "—"
			mult = "—"
			finalW = "—"
			contrib = "—"
			annotations = " [skipped]"
		case row.WeightOverride:
			annotations = " [overridden]"
		case row.PrimaryGenre != "" && !row.BiasResistant:
			annotations = " [primary blended]"
		case row.AppliedMultiplier != 1.0:
			annotations = " [genre adjusted]"
		}
		if row.BiasResistant && !row.Skipped {
			mult += biasResistantMark
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
		if blendWasApplied(result.Meta) {
			blendPct := int(result.Meta.PrimaryGenreWeight * 100)
			fmt.Printf("  Primary genre          : %s [primary] (blend %d/%d)\n",
				result.Meta.PrimaryGenre, blendPct, 100-blendPct)
		} else {
			fmt.Printf("  Primary genre          : %s [primary] (sole active genre)\n",
				result.Meta.PrimaryGenre)
		}
	}

	// Genre match detail.
	if len(result.Meta.AllGenres) > 0 {
		fmt.Printf("  Genres returned        : %s\n", strings.Join(result.Meta.AllGenres, ", "))
	}
	if len(result.Meta.MatchedGenres) > 0 {
		fmt.Printf(
			"  Genres matched config  : %s\n",
			annotateMatchedGenres(result.Meta.MatchedGenres, result.Meta.PrimaryGenre),
		)
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
func resolvePrimaryGenre(
	flagVal string,
	mediaGenres []string,
	reader *bufio.Reader,
) (string, error) {
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
			suffix = biasResistantMark
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
		if blendWasApplied(result.Meta) {
			blendPct := int(result.Meta.PrimaryGenreWeight * 100)
			fmt.Fprintf(
				&b,
				"Primary: %s (blend %d/%d)\n",
				result.Meta.PrimaryGenre,
				blendPct,
				100-blendPct,
			)
		} else {
			fmt.Fprintf(&b, "Primary: %s (sole active genre)\n", result.Meta.PrimaryGenre)
		}
	}
	if len(result.Meta.AllGenres) > 0 {
		fmt.Fprintf(&b, "Genres:  %s\n", strings.Join(result.Meta.AllGenres, ", "))
	}
	if len(result.Meta.MatchedGenres) > 0 {
		fmt.Fprintf(
			&b,
			"Matched: %s\n",
			annotateMatchedGenres(result.Meta.MatchedGenres, result.Meta.PrimaryGenre),
		)
	}
	fmt.Fprintf(&b, "Config:  %s", result.Meta.ConfigHash)

	return b.String()
}

// blendWasApplied reports whether the primary genre blend formula was actually
// used — i.e. at least one real secondary genre was active alongside the primary.
// When the primary is the sole active matched genre, the engine applies its
// multiplier directly (ADR-025) and no blend ratio is meaningful to display.
func blendWasApplied(meta scoring.SessionMeta) bool {
	primaryLower := strings.ToLower(meta.PrimaryGenre)
	for _, g := range meta.GenresActive {
		if g != primaryLower {
			return true
		}
	}
	return false
}
