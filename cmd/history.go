package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kondanta/kansou/internal/store"
)

// historyCmd returns the `history` cobra command and its subcommands.
func (a *App) historyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Browse scoring history",
		Long: `List, inspect, or remove entries from your scoring history.

With no subcommand, lists the latest score for every scored entry, newest first.

Requires a database (KANSOU_DB_TYPE must be set).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.Store == nil {
				return fmt.Errorf("history requires a database — set KANSOU_DB_TYPE to enable")
			}
			return runHistoryList(cmd.Context(), a.Store)
		},
	}
	cmd.AddCommand(a.historyShowCmd())
	cmd.AddCommand(a.historyDeleteCmd())
	return cmd
}

// historyShowCmd returns the `history show` cobra command.
func (a *App) historyShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <query>",
		Short: "Show the full scoring breakdown for a history entry",
		Long: `Show the full scoring breakdown for a history entry.

<query> is either a numeric AniList media ID or a title search string. If a
title search matches more than one entry, you'll be prompted to pick one.
Older scores, if any survive max_history retention, are listed below the
breakdown.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.Store == nil {
				return fmt.Errorf("history requires a database — set KANSOU_DB_TYPE to enable")
			}
			return runHistoryShow(cmd.Context(), a.Store, args[0])
		},
	}
}

// historyDeleteCmd returns the `history delete` cobra command.
func (a *App) historyDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <query>",
		Short: "Remove the latest score for a history entry from active tracking",
		Long: `Soft-delete the latest score for a history entry.

<query> is either a numeric AniList media ID or a title search string. This
is a deliberate removal from active tracking, not an undo — no other score
is promoted in its place, so the entry stops appearing in 'kansou history'
and stats until you score it again. Older scores are kept (subject to
max_history) and remain reachable via 'kansou history show'. Run
'kansou db prune' to permanently remove soft-deleted rows.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.Store == nil {
				return fmt.Errorf("history requires a database — set KANSOU_DB_TYPE to enable")
			}
			return runHistoryDelete(cmd.Context(), a.Store, args[0])
		},
	}
}

// runHistoryList prints the latest score for every scored entry, newest first.
func runHistoryList(ctx context.Context, st store.Store) error {
	entries, err := st.ListLatest(ctx)
	if err != nil {
		return fmt.Errorf("listing history: %w", err)
	}
	if len(entries) == 0 {
		fmt.Println("No scoring history yet.")
		return nil
	}
	for _, e := range entries {
		fmt.Printf("  %-30s %-6s %5.2f   %s\n",
			truncate(e.TitleRomaji, 30), e.MediaType, e.FinalScore, e.ScoredAt.Format("2006-01-02"))
	}
	return nil
}

// runHistoryShow resolves query, prints the current breakdown, and lists any
// surviving older scores below it.
func runHistoryShow(ctx context.Context, st store.Store, query string) error {
	anilistID, title, err := resolveHistoryQuery(ctx, st, query)
	if errors.Is(err, errUserCancelled) {
		return nil
	}
	if err != nil {
		return err
	}

	history, err := st.ScoreHistory(ctx, anilistID)
	if err != nil {
		return fmt.Errorf("fetching history: %w", err)
	}
	if len(history) == 0 {
		return fmt.Errorf("no active score history for %s", queryLabel(title, anilistID))
	}

	current := history[0]
	fmt.Printf("\n%s (%s · %s)\n\n", current.TitleRomaji, current.MediaType, current.Format)
	printStoredBreakdown(&current)

	if len(history) > 1 {
		fmt.Println("Previous scores:")
		for _, prev := range history[1:] {
			fmt.Printf("  %.2f   %s\n", prev.FinalScore, prev.ScoredAt.Format("2006-01-02"))
		}
		fmt.Println()
	}
	return nil
}

// runHistoryDelete resolves query, confirms with the user, and soft-deletes
// the entry's latest score.
func runHistoryDelete(ctx context.Context, st store.Store, query string) error {
	anilistID, title, err := resolveHistoryQuery(ctx, st, query)
	if errors.Is(err, errUserCancelled) {
		return nil
	}
	if err != nil {
		return err
	}

	latest, err := st.LatestScore(ctx, anilistID)
	if err != nil {
		return fmt.Errorf("fetching latest score: %w", err)
	}
	if latest == nil {
		return fmt.Errorf("no active score to delete for %s", queryLabel(title, anilistID))
	}
	if title == "" {
		title = latest.TitleRomaji
	}

	fmt.Printf("Delete score for %s? [y/N]: ", title)
	reader := bufio.NewReader(os.Stdin)
	line, readErr := reader.ReadString('\n')
	if readErr != nil || strings.ToLower(strings.TrimSpace(line)) != "y" {
		fmt.Println("Cancelled.")
		return nil
	}

	if err := st.SoftDeleteScore(ctx, latest.ID); err != nil {
		return fmt.Errorf("deleting score: %w", err)
	}
	fmt.Printf("✓ Score for %s marked for deletion. Run 'kansou db prune' to permanently remove.\n", title)
	return nil
}

// queryLabel formats title (if resolved via search) or anilistID (if the
// query was numeric and no title was ever resolved) for error messages.
func queryLabel(title string, anilistID int) string {
	if title != "" {
		return fmt.Sprintf("%q", title)
	}
	return fmt.Sprintf("AniList ID %d", anilistID)
}

// resolveHistoryQuery resolves a history query to an AniList media ID.
// Numeric queries are used directly as the AniList ID (title is returned
// empty — callers fall back to whatever title the fetched data carries).
// Non-numeric queries search local history by title, prompting a picker if
// more than one entry matches. Returns errUserCancelled if the user aborts
// the picker with EOF.
func resolveHistoryQuery(ctx context.Context, st store.Store, query string) (anilistID int, title string, err error) {
	if id, convErr := strconv.Atoi(query); convErr == nil {
		return id, "", nil
	}

	results, err := st.SearchMediaByTitle(ctx, query)
	if err != nil {
		return 0, "", fmt.Errorf("searching history: %w", err)
	}
	if len(results) == 0 {
		return 0, "", fmt.Errorf("no history entries found matching %q", query)
	}
	picked, err := pickMediaSearchResult(results)
	if err != nil {
		return 0, "", err
	}
	return picked.AnilistID, picked.TitleRomaji, nil
}

// pickMediaSearchResult presents a numbered list of local history search
// results and returns the one the user selects. If there is only one result
// it is returned immediately without prompting. Returns errUserCancelled if
// the user aborts with EOF (Ctrl+D).
func pickMediaSearchResult(results []store.MediaSearchResult) (*store.MediaSearchResult, error) {
	if len(results) == 1 {
		return &results[0], nil
	}

	fmt.Println()
	for i, r := range results {
		title := r.TitleRomaji
		if r.TitleEnglish != "" && r.TitleEnglish != r.TitleRomaji {
			title = r.TitleEnglish
		}
		fmt.Printf("  %d. %-45s (%s · %s)\n", i+1, truncate(title, 45), r.MediaType, r.Format)
	}
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("Pick a result [1–%d]: ", len(results))
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintln(os.Stderr, "\ncancelled")
			return nil, errUserCancelled
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

// printStoredBreakdown renders a persisted store.Score's dimension breakdown,
// mirroring printBreakdown's layout for a live scoring.Result. Some
// ephemeral session-only detail isn't available here — only what SaveScore
// actually persisted (see PrimaryGenreMultiplier/SecondaryGenresMultiplier
// on store.DimensionScoreRow).
func printStoredBreakdown(sc *store.Score) {
	sep := strings.Repeat("─", 78)
	fmt.Println(sep)
	fmt.Printf("  %-15s  %-6s  %-8s  %-11s  %-8s  %-12s\n",
		"Dimension", "Score", "Base W", "Multiplier", "Final W", "Contribution")
	fmt.Println(sep)

	for _, row := range sc.Breakdown {
		printStoredBreakdownRow(row)
	}

	fmt.Println(sep)
	fmt.Printf("  %-15s  %-6s  %-8s  %-11s  %-8s  %.2f / 10\n",
		"Final Score", "", "", "", "", sc.FinalScore)
	fmt.Println(sep)

	if primary := findPrimaryGenre(sc.ActiveGenres); primary != "" {
		fmt.Printf("  Primary genre          : %s [primary]\n", primary)
	}
	if len(sc.Genres) > 0 {
		fmt.Printf("  Genres returned        : %s\n", strings.Join(sc.Genres, ", "))
	}
	if len(sc.ActiveGenres) > 0 {
		names := make([]string, len(sc.ActiveGenres))
		for i, g := range sc.ActiveGenres {
			names[i] = g.Genre
		}
		fmt.Printf("  Genres matched config  : %s\n", strings.Join(names, ", "))
	}
	fmt.Println()
}

// printStoredBreakdownRow renders one dimension_scores row within the table
// printStoredBreakdown builds.
func printStoredBreakdownRow(row store.DimensionScoreRow) {
	if row.Skipped {
		fmt.Printf("  %-15s  %-6s  %-8s  %-11s  %-8s  %-12s [skipped]\n",
			row.Label, "—", fmt.Sprintf("%.2f%%", row.BaseWeight*100), "—", "—", "—")
		return
	}

	scoreStr, contrib := "—", "—"
	if row.Score != nil {
		scoreStr = fmt.Sprintf("%.1f", *row.Score)
	}
	if row.Contribution != nil {
		contrib = fmt.Sprintf("%.2f", *row.Contribution)
	}
	mult := fmt.Sprintf("×%.2f", row.AppliedMultiplier)
	if row.BiasResistant {
		mult += biasResistantMark
	}
	baseW := fmt.Sprintf("%.2f%%", row.BaseWeight*100)
	finalW := fmt.Sprintf("%.2f%%", row.FinalWeight*100)

	annotations := ""
	switch {
	case row.WeightOverride:
		annotations = " [overridden]"
	case row.GenreDeselected:
		annotations = " [genre filtered]"
	case row.PrimaryGenreMultiplier != 0 || row.SecondaryGenresMultiplier != 0:
		annotations = " [primary blended]"
	case row.AppliedMultiplier != 1.0:
		annotations = " [genre adjusted]"
	}

	fmt.Printf("  %-15s  %-6s  %-8s  %-11s  %-8s  %-12s%s\n",
		row.Label, scoreStr, baseW, mult, finalW, contrib, annotations)
}

// findPrimaryGenre returns the genre marked IsPrimary, or "" if none.
func findPrimaryGenre(genres []store.MatchedGenreRow) string {
	for _, g := range genres {
		if g.IsPrimary {
			return g.Genre
		}
	}
	return ""
}
