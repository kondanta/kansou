package cmd

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kondanta/kansou/internal/stats"
)

// statsCmd returns the `kansou stats` cobra command.
func (a *App) statsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats [genres|dimensions|history]",
		Short: "Show scoring history statistics",
		Long: `Show statistics computed from your scoring history.

With no argument, prints a one-line summary per category. Pass "genres",
"dimensions", or "history" to see the full breakdown for that category.

Requires a database (KANSOU_DB_TYPE must be set).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.Store == nil {
				return fmt.Errorf("stats require a database — set KANSOU_DB_TYPE to enable")
			}
			st := stats.New(a.Store)
			ctx := cmd.Context()
			if len(args) == 0 {
				return runStatsSummary(ctx, st)
			}
			switch args[0] {
			case "genres":
				return runStatsGenres(ctx, st)
			case "dimensions":
				return runStatsDimensions(ctx, st)
			case "history":
				return runStatsHistory(ctx, st)
			default:
				return fmt.Errorf("unknown stats category %q — valid options: genres, dimensions, history", args[0])
			}
		},
	}
}

// runStatsSummary prints one headline line per stats category.
func runStatsSummary(ctx context.Context, st *stats.Stats) error {
	sum, err := st.Summary(ctx)
	if err != nil {
		return fmt.Errorf("computing summary: %w", err)
	}

	if sum.TopGenre != nil {
		fmt.Printf("Top genre:             %s (%d entries, %.0f%%)\n",
			sum.TopGenre.Genre, sum.TopGenre.Count, sum.TopGenre.Percentage)
	} else {
		fmt.Println("Top genre:             no data yet")
	}
	if sum.TopScoringGenre != nil {
		fmt.Printf("Highest scoring genre: %s (avg %.2f)\n", sum.TopScoringGenre.Genre, sum.TopScoringGenre.AvgScore)
	}
	if sum.OverallConsistency != nil {
		fmt.Printf("Scoring consistency:   avg std dev %.2f across %d dimensions\n",
			sum.OverallConsistency.AvgStdDev, sum.OverallConsistency.Count)
	}
	if sum.MostConsistentDim != nil {
		fmt.Printf("Most consistent:       %s (std dev %.2f)\n", sum.MostConsistentDim.Label, sum.MostConsistentDim.StdDev)
	}
	if sum.LeastConsistentDim != nil {
		fmt.Printf("Least consistent:      %s (std dev %.2f)\n", sum.LeastConsistentDim.Label, sum.LeastConsistentDim.StdDev)
	}
	if sum.MostRescored != nil {
		fmt.Printf("Most rescored:         %s (%d times)\n", sum.MostRescored.TitleRomaji, sum.MostRescored.ScoreCount)
	}
	fmt.Printf("Outliers detected:     %d\n", sum.OutlierCount)
	if sum.LastPruneAt != nil {
		fmt.Printf("Last pruned:           %s\n", sum.LastPruneAt.Format("2006-01-02 15:04"))
	} else {
		fmt.Println("Last pruned:           never")
	}
	fmt.Println()
	fmt.Println("Run `kansou stats genres|dimensions|history` for the full breakdown.")
	return nil
}

// runStatsGenres prints the genre breakdown, average score by genre, and
// genre-dimension affinity.
func runStatsGenres(ctx context.Context, st *stats.Stats) error {
	g, err := st.Genres(ctx)
	if err != nil {
		return fmt.Errorf("computing genre stats: %w", err)
	}
	if len(g.Breakdown) == 0 {
		fmt.Println("No scored entries yet.")
		return nil
	}

	maxCount := 0
	for _, r := range g.Breakdown {
		if r.Count > maxCount {
			maxCount = r.Count
		}
	}
	fmt.Println("Genre breakdown:")
	for _, r := range g.Breakdown {
		bar := asciiBar(float64(r.Count), float64(maxCount), 20)
		fmt.Printf("  %-15s %s %3d  (%.0f%%)\n", r.Genre, bar, r.Count, r.Percentage)
	}

	fmt.Println("\nAverage score by genre:")
	for _, r := range g.ByGenre {
		fmt.Printf("  %-15s %s %.2f  (n=%d)\n", r.Genre, asciiBar(r.AvgScore, 10, 20), r.AvgScore, r.Count)
	}

	if len(g.Affinity) == 0 {
		return nil
	}
	fmt.Println("\nGenre × dimension affinity (strongest dimension per genre):")
	for _, aff := range g.Affinity {
		if len(aff.Dimensions) == 0 {
			continue
		}
		top := aff.Dimensions[0]
		fmt.Printf("  %-15s strongest: %s (%.2f)\n", aff.Genre, top.Label, top.AvgScore)
	}
	return nil
}

// runStatsDimensions prints variance, consistency, correlation, skip rate,
// and weight override frequency per dimension.
func runStatsDimensions(ctx context.Context, st *stats.Stats) error {
	d, err := st.Dimensions(ctx)
	if err != nil {
		return fmt.Errorf("computing dimension stats: %w", err)
	}
	if len(d.DimensionVariance) == 0 {
		fmt.Println("No scored entries yet.")
		return nil
	}

	fmt.Println("Dimension variance (lower std dev = more consistent):")
	for _, v := range d.DimensionVariance {
		fmt.Printf("  %-15s %s %.2f  (avg %.2f, n=%d)\n", v.Label, asciiBar(v.StdDev, 3, 20), v.StdDev, v.AvgScore, v.Count)
	}
	if d.ScoringConsistency != nil {
		fmt.Printf("\nOverall consistency: avg std dev %.2f across %d dimensions\n",
			d.ScoringConsistency.AvgStdDev, d.ScoringConsistency.Count)
	}

	fmt.Println("\nDimension correlation:")
	if d.CorrelationInsufficient {
		fmt.Println("  Insufficient data — at least 25 shared scored entries required per dimension pair")
	} else {
		for _, c := range d.DimensionCorrelation {
			fmt.Printf("  %-15s ↔ %-15s %+.2f\n", c.DimensionA, c.DimensionB, c.Correlation)
		}
	}

	if len(d.SkippedDimensions) > 0 {
		fmt.Println("\nSkip rate by media type:")
		for _, sk := range d.SkippedDimensions {
			rate := 0.0
			if sk.TotalCount > 0 {
				rate = float64(sk.SkipCount) / float64(sk.TotalCount) * 100
			}
			fmt.Printf("  %-15s %-6s %d/%d  (%.0f%%)\n", sk.Label, sk.MediaType, sk.SkipCount, sk.TotalCount, rate)
		}
	}

	if len(d.WeightOverrides) > 0 {
		fmt.Println("\nWeight override frequency:")
		for _, w := range d.WeightOverrides {
			fmt.Printf("  %-15s %d times\n", w.Label, w.OverrideCount)
		}
	}
	return nil
}

// maxHistoryRows caps CLI table output to keep `kansou stats history` readable.
const maxHistoryRows = 10

// runStatsHistory prints most-rescored entries, outliers, and config impact.
func runStatsHistory(ctx context.Context, st *stats.Stats) error {
	h, err := st.History(ctx)
	if err != nil {
		return fmt.Errorf("computing history stats: %w", err)
	}
	if len(h.MostRescored) == 0 && len(h.Outliers) == 0 && len(h.ConfigImpact) == 0 {
		fmt.Println("No scored entries yet.")
		return nil
	}

	if len(h.MostRescored) > 0 {
		fmt.Println("Most rescored:")
		for i, r := range h.MostRescored {
			if i >= maxHistoryRows {
				break
			}
			fmt.Printf("  %-30s %d times  (latest %.2f)\n", r.TitleRomaji, r.ScoreCount, r.LatestScore)
		}
	}

	if len(h.Outliers) > 0 {
		fmt.Println("\nOutliers (>2 std devs from your personal average):")
		for i, o := range h.Outliers {
			if i >= maxHistoryRows {
				break
			}
			fmt.Printf("  %-30s %-15s %.1f  (avg %.2f, %+.1f std dev)\n",
				o.TitleRomaji, o.Label, o.Score, o.PersonalAvg, o.Deviation)
		}
	}

	if len(h.ConfigImpact) > 0 {
		fmt.Println("\nConfig impact (chronological):")
		for _, c := range h.ConfigImpact {
			fmt.Printf("  %s  n=%-4d avg %.2f  (%s → %s)\n",
				shortHash(c.ConfigHash), c.EntryCount, c.AvgScore,
				c.FirstScoredAt.Format("2006-01-02"), c.LastScoredAt.Format("2006-01-02"))
		}
	}
	return nil
}

// shortHashLen is how many characters of a config hash to show in CLI output.
const shortHashLen = 8

// shortHash truncates a config hash for compact display.
func shortHash(h string) string {
	if len(h) > shortHashLen {
		return h[:shortHashLen]
	}
	return h
}

// asciiBar renders a simple bar chart segment: ─ for the filled portion,
// spaces for the remainder, bordered by │. width is the bar's character width.
func asciiBar(value, max float64, width int) string {
	if max <= 0 {
		max = 1
	}
	filled := int(math.Round(value / max * float64(width)))
	filled = int(math.Max(0, math.Min(float64(width), float64(filled))))
	return "│" + strings.Repeat("─", filled) + strings.Repeat(" ", width-filled) + "│"
}
