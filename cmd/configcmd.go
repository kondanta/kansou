package cmd

import (
	"context"
	"fmt"
	"maps"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kondanta/kansou/internal/config"
)

// requireStoreForConfig returns an error if no database is configured.
// Deliberately NOT a PersistentPreRunE on the dimension/genre parent
// commands: cobra only runs the nearest PersistentPreRunE in the chain
// (kansou does not set cobra.EnableTraverseRunHooks), and root's
// PersistentPreRunE is what actually initialises a.Store/a.Config —
// overriding it here would silently skip that initialisation entirely.
func requireStoreForConfig(a *App) error {
	if a.Store == nil {
		return fmt.Errorf("config dimension/genre commands require a database — set KANSOU_DB_TYPE to enable")
	}
	return nil
}

// configCmd returns the `config` cobra command and its subcommands.
func (a *App) configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View and edit scoring configuration",
		Long:  "Commands for viewing and editing scoring config (dimensions, genres, weights).",
	}
	cmd.AddCommand(a.configShowCmd())
	cmd.AddCommand(a.configImportCmd())
	cmd.AddCommand(a.configExportCmd())
	cmd.AddCommand(a.configDimensionCmd())
	cmd.AddCommand(a.configGenreCmd())
	return cmd
}

// configShowCmd returns the `config show` cobra command.
func (a *App) configShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the current scoring config",
		Long:  "Print the current scoring config — from the database if one is configured, from the config file otherwise.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			printConfig(a.Config)
			return nil
		},
	}
}

// configImportCmd returns the `config import` cobra command.
func (a *App) configImportCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import scoring config from a TOML file",
		Long: "Read and validate a TOML file, then make it the active scoring config " +
			"(database in DB mode, the resolved config file otherwise).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigImport(cmd.Context(), a, file)
		},
	}
	cmd.Flags().StringVar(&file, "file", "config.toml", "Path to the TOML file to import")
	return cmd
}

// runConfigImport reads and validates file, then persists it as the active config.
func runConfigImport(ctx context.Context, a *App, file string) error {
	if _, err := os.Stat(file); err != nil {
		return fmt.Errorf("reading %s: %w", file, err)
	}
	cfg, err := config.Load(file)
	if err != nil {
		return fmt.Errorf("loading %s: %w", file, err)
	}
	if a.Store != nil {
		if err := a.Store.SaveScoringConfig(ctx, cfg); err != nil {
			return fmt.Errorf("saving config to database: %w", err)
		}
	} else if err := config.Write(a.ConfigPath, cfg); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}
	fmt.Printf("✓ Config imported from %s\n", file)
	return nil
}

// configExportCmd returns the `config export` cobra command.
func (a *App) configExportCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export the current scoring config to a TOML file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := config.Write(file, a.Config); err != nil {
				return fmt.Errorf("writing %s: %w", file, err)
			}
			fmt.Printf("✓ Config exported to %s\n", file)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "config.toml", "Path to write the TOML file to")
	return cmd
}

// printConfig renders a scoring config to stdout.
func printConfig(cfg *config.Config) {
	fmt.Println("Dimensions:")
	for _, key := range cfg.DimensionOrder {
		d := cfg.Dimensions[key]
		bias := ""
		if d.BiasResistant {
			bias = " [bias-resistant]"
		}
		fmt.Printf("  %-15s %-25s weight=%.4f%s\n", key, d.Label, d.Weight, bias)
	}

	if len(cfg.Genres) > 0 {
		fmt.Println("\nGenre multipliers:")
		for _, g := range sortedKeys(cfg.Genres) {
			mults := cfg.Genres[g]
			parts := make([]string, 0, len(mults))
			for _, d := range sortedFloatKeys(mults) {
				parts = append(parts, fmt.Sprintf("%s=%.2f", d, mults[d]))
			}
			fmt.Printf("  %-15s %s\n", g, strings.Join(parts, ", "))
		}
	}

	fmt.Printf("\nprimary_genre_weight: %.2f\n", cfg.PrimaryGenreWeight)
	fmt.Printf("max_multiplier:       %.2f\n", cfg.MaxMultiplier)
	fmt.Printf("max_history:          %d\n", cfg.MaxHistory)
}

// sortedKeys returns the keys of a genre multiplier map, sorted.
func sortedKeys(m map[string]map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedFloatKeys returns the keys of a single genre's dimension multiplier map, sorted.
func sortedFloatKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// cloneDimensions returns a shallow copy of a dimension map, safe to mutate
// without affecting the original config.
func cloneDimensions(dims map[string]config.DimensionDef) map[string]config.DimensionDef {
	out := maps.Clone(dims)
	return out
}

// cloneGenres returns a deep copy of a genre multiplier map, safe to mutate
// without affecting the original config.
func cloneGenres(genres map[string]map[string]float64) map[string]map[string]float64 {
	out := make(map[string]map[string]float64, len(genres))
	for genre, mults := range genres {
		out[genre] = maps.Clone(mults)
	}

	return out
}

// rebalanceWeightsAfterRemoval redistributes removedWeight proportionally
// across dims, based on each dimension's current relative share of the
// remaining total, so the set continues to sum to 1.0. Called after deleting
// the removed dimension from dims. remainingTotal is never 0 in practice —
// runConfigDimensionRemove's floor guarantees at least one dimension survives,
// and every dimension's weight is validated > 0 on save — but the guard
// avoids a division by zero if that invariant is ever violated elsewhere.
func rebalanceWeightsAfterRemoval(
	dims map[string]config.DimensionDef, removedWeight float64,
) map[string]config.DimensionDef {
	remainingTotal := 0.0
	for _, d := range dims {
		remainingTotal += d.Weight
	}
	if remainingTotal == 0 {
		return dims
	}
	out := make(map[string]config.DimensionDef, len(dims))
	for key, d := range dims {
		share := d.Weight / remainingTotal
		d.Weight += removedWeight * share
		out[key] = d
	}
	return out
}

// removeGenreReferencesToDimension strips any per-genre multiplier entries
// for key, so a subsequent config.Rebuild doesn't fail its genre-key
// validation after the dimension itself is removed.
func removeGenreReferencesToDimension(genres map[string]map[string]float64, key string) map[string]map[string]float64 {
	out := cloneGenres(genres)
	for genre, mults := range out {
		delete(mults, key)
		if len(mults) == 0 {
			delete(out, genre)
		}
	}
	return out
}

// configDimensionCmd returns the `config dimension` cobra command and its subcommands.
func (a *App) configDimensionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dimension",
		Short: "Manage scoring dimensions",
		Long:  "Add, update, remove, or list scoring dimensions. Requires a database.",
	}
	cmd.AddCommand(a.configDimensionListCmd())
	cmd.AddCommand(a.configDimensionAddCmd())
	cmd.AddCommand(a.configDimensionSetCmd())
	cmd.AddCommand(a.configDimensionRemoveCmd())
	return cmd
}

// configDimensionListCmd returns the `config dimension list` cobra command.
func (a *App) configDimensionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all scoring dimensions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireStoreForConfig(a); err != nil {
				return err
			}
			for _, key := range a.Config.DimensionOrder {
				d := a.Config.Dimensions[key]
				bias := ""
				if d.BiasResistant {
					bias = " [bias-resistant]"
				}
				fmt.Printf("  %-15s %-25s weight=%.4f%s\n", key, d.Label, d.Weight, bias)
			}
			return nil
		},
	}
}

// configDimensionAddCmd returns the `config dimension add` cobra command.
func (a *App) configDimensionAddCmd() *cobra.Command {
	var label, description string
	var weight float64
	var biasResistant bool

	cmd := &cobra.Command{
		Use:   "add <key>",
		Short: "Add a new scoring dimension",
		Long: `Add a new scoring dimension.

The resulting set of dimension weights (including this new one) must still
sum to 1.0 — reduce another dimension's weight first with 'config dimension
set' if needed, since this command refuses to save an invalid state.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireStoreForConfig(a); err != nil {
				return err
			}
			return runConfigDimensionAdd(cmd.Context(), a, args[0], label, description, weight, biasResistant)
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "Display label (required)")
	cmd.Flags().StringVar(&description, "description", "", "Scoring hint shown during 'score add'")
	cmd.Flags().Float64Var(&weight, "weight", 0, "Base weight, must be > 0 (required)")
	cmd.Flags().BoolVar(&biasResistant, "bias-resistant", false, "Ignore genre multipliers for this dimension")
	return cmd
}

// runConfigDimensionAdd validates and persists a new dimension.
func runConfigDimensionAdd(
	ctx context.Context, a *App, key, label, description string, weight float64, biasResistant bool,
) error {
	if _, exists := a.Config.Dimensions[key]; exists {
		return fmt.Errorf("dimension %q already exists — use 'kansou config dimension set' to modify it", key)
	}
	if weight <= 0 {
		return fmt.Errorf("weight must be > 0")
	}
	if label == "" {
		return fmt.Errorf("--label is required")
	}

	dims := cloneDimensions(a.Config.Dimensions)
	dims[key] = config.DimensionDef{Label: label, Description: description, Weight: weight, BiasResistant: biasResistant}

	newCfg, err := config.Rebuild(a.Config, dims, a.Config.Genres, a.Config.PrimaryGenreWeight, a.Config.MaxMultiplier)
	if err != nil {
		return fmt.Errorf("adding dimension %q: %w", key, err)
	}
	if err := a.Store.SaveScoringConfig(ctx, newCfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Printf("✓ Dimension %q added\n", key)
	return nil
}

// configDimensionSetCmd returns the `config dimension set` cobra command.
func (a *App) configDimensionSetCmd() *cobra.Command {
	var label, description string
	var weight float64
	var biasResistant bool

	cmd := &cobra.Command{
		Use:   "set <key>",
		Short: "Update an existing scoring dimension",
		Long: `Update one or more fields of an existing scoring dimension.
Only flags explicitly passed are changed; omitted flags keep their current value.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireStoreForConfig(a); err != nil {
				return err
			}
			var labelPtr, descriptionPtr *string
			var weightPtr *float64
			var biasResistantPtr *bool
			if cmd.Flags().Changed("label") {
				labelPtr = &label
			}
			if cmd.Flags().Changed("description") {
				descriptionPtr = &description
			}
			if cmd.Flags().Changed("weight") {
				weightPtr = &weight
			}
			if cmd.Flags().Changed("bias-resistant") {
				biasResistantPtr = &biasResistant
			}
			return runConfigDimensionSet(cmd.Context(), a, args[0], labelPtr, descriptionPtr, weightPtr, biasResistantPtr)
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "New display label")
	cmd.Flags().StringVar(&description, "description", "", "New scoring hint")
	cmd.Flags().Float64Var(&weight, "weight", 0, "New base weight, must be > 0")
	cmd.Flags().BoolVar(&biasResistant, "bias-resistant", false, "Whether genre multipliers are ignored")
	return cmd
}

// runConfigDimensionSet applies the given field overrides to an existing dimension.
func runConfigDimensionSet(
	ctx context.Context, a *App, key string,
	label, description *string, weight *float64, biasResistant *bool,
) error {
	existing, ok := a.Config.Dimensions[key]
	if !ok {
		return fmt.Errorf("dimension %q does not exist", key)
	}
	if label != nil {
		existing.Label = *label
	}
	if description != nil {
		existing.Description = *description
	}
	if weight != nil {
		if *weight <= 0 {
			return fmt.Errorf("weight must be > 0")
		}
		existing.Weight = *weight
	}
	if biasResistant != nil {
		existing.BiasResistant = *biasResistant
	}

	dims := cloneDimensions(a.Config.Dimensions)
	dims[key] = existing

	newCfg, err := config.Rebuild(a.Config, dims, a.Config.Genres, a.Config.PrimaryGenreWeight, a.Config.MaxMultiplier)
	if err != nil {
		return fmt.Errorf("updating dimension %q: %w", key, err)
	}
	if err := a.Store.SaveScoringConfig(ctx, newCfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Printf("✓ Dimension %q updated\n", key)
	return nil
}

// minDimensionsAfterRemoval is the floor `config dimension remove` enforces:
// refuse a removal that would leave fewer than this many dimensions. One
// dimension at weight 1.0 is mathematically valid, so the floor is 1, not 2 —
// only removing the very last dimension is blocked. Confirmed with the user
// 2026-07-02; no ceiling on dimension count was judged necessary (add already
// self-limits in practice — each addition requires manually working out
// weights that still sum to 1.0).
const minDimensionsAfterRemoval = 1

// configDimensionRemoveCmd returns the `config dimension remove` cobra command.
func (a *App) configDimensionRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <key>",
		Short: "Remove a scoring dimension",
		Long: `Remove a scoring dimension and any genre multipliers that reference it.

The freed weight is redistributed proportionally across the remaining
dimensions (based on their current relative weights) so the total keeps
summing to 1.0 — unlike 'add'/'set', which refuse to save an unbalanced
result, removal inherently changes more than one dimension's weight, so
there is no single-field edit that could satisfy that same rule on its own.

Refuses to remove the last remaining dimension. Warns (but does not block)
if the dimension has scored entries in current history — removing it does
not delete that data, but future stats silently exclude it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireStoreForConfig(a); err != nil {
				return err
			}
			return runConfigDimensionRemove(cmd.Context(), a, args[0])
		},
	}
}

// runConfigDimensionRemove removes a dimension, proportionally redistributing
// its weight across the remaining dimensions, warning first if the dimension
// has current scored history. The history check only sees latest, non-skipped
// entries (DimensionVariance's own scope) — it is a best-effort warning, not
// an exhaustive audit.
func runConfigDimensionRemove(ctx context.Context, a *App, key string) error {
	target, ok := a.Config.Dimensions[key]
	if !ok {
		return fmt.Errorf("dimension %q does not exist", key)
	}
	if len(a.Config.Dimensions) <= minDimensionsAfterRemoval {
		return fmt.Errorf(
			"cannot remove %q — at least %d dimension(s) must remain; add a replacement first",
			key, minDimensionsAfterRemoval,
		)
	}

	variance, err := a.Store.DimensionVariance(ctx)
	if err != nil {
		return fmt.Errorf("checking dimension history: %w", err)
	}
	for _, v := range variance {
		if v.DimensionKey == key && v.Count > 0 {
			fmt.Printf(
				"warning: dimension %q has %d scored entries in current history — "+
					"removing it does not delete that data, but future stats will silently exclude it\n",
				key, v.Count,
			)
			break
		}
	}

	dims := cloneDimensions(a.Config.Dimensions)
	delete(dims, key)
	dims = rebalanceWeightsAfterRemoval(dims, target.Weight)
	genres := removeGenreReferencesToDimension(a.Config.Genres, key)

	newCfg, err := config.Rebuild(a.Config, dims, genres, a.Config.PrimaryGenreWeight, a.Config.MaxMultiplier)
	if err != nil {
		return fmt.Errorf("removing dimension %q: %w", key, err)
	}
	if err := a.Store.SaveScoringConfig(ctx, newCfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Printf("✓ Dimension %q removed — remaining weights rebalanced proportionally\n", key)
	return nil
}

// configGenreCmd returns the `config genre` cobra command and its subcommands.
func (a *App) configGenreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "genre",
		Short: "Manage genre multipliers",
		Long:  "Set or remove per-genre, per-dimension multipliers. Requires a database.",
	}
	cmd.AddCommand(a.configGenreSetCmd())
	cmd.AddCommand(a.configGenreRemoveCmd())
	return cmd
}

// configGenreSetCmd returns the `config genre set` cobra command.
func (a *App) configGenreSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <genre> <dimension> <multiplier>",
		Short: "Set a genre's multiplier for a dimension",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireStoreForConfig(a); err != nil {
				return err
			}
			multiplier, err := strconv.ParseFloat(args[2], 64)
			if err != nil {
				return fmt.Errorf("invalid multiplier %q: %w", args[2], err)
			}
			return runConfigGenreSet(cmd.Context(), a, args[0], args[1], multiplier)
		},
	}
}

// runConfigGenreSet validates and persists a genre/dimension multiplier.
func runConfigGenreSet(ctx context.Context, a *App, genre, dimension string, multiplier float64) error {
	if _, ok := a.Config.Dimensions[dimension]; !ok {
		return fmt.Errorf("dimension %q does not exist", dimension)
	}
	if multiplier <= 0 || multiplier > a.Config.MaxMultiplier {
		return fmt.Errorf("multiplier %.4f must be > 0 and ≤ %.4f", multiplier, a.Config.MaxMultiplier)
	}

	genreLower := strings.ToLower(genre)
	genres := cloneGenres(a.Config.Genres)
	if genres[genreLower] == nil {
		genres[genreLower] = map[string]float64{}
	}
	genres[genreLower][dimension] = multiplier

	newCfg, err := config.Rebuild(
		a.Config, a.Config.Dimensions, genres, a.Config.PrimaryGenreWeight, a.Config.MaxMultiplier,
	)
	if err != nil {
		return fmt.Errorf("setting genre multiplier: %w", err)
	}
	if err := a.Store.SaveScoringConfig(ctx, newCfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Printf("✓ Genre %q dimension %q multiplier set to %.4f\n", genreLower, dimension, multiplier)
	return nil
}

// configGenreRemoveCmd returns the `config genre remove` cobra command.
func (a *App) configGenreRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <genre> [dimension]",
		Short: "Remove a genre's multiplier",
		Long:  "Remove all of a genre's multipliers, or just one dimension's if given.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireStoreForConfig(a); err != nil {
				return err
			}
			dimension := ""
			if len(args) == 2 {
				dimension = args[1]
			}
			return runConfigGenreRemove(cmd.Context(), a, args[0], dimension)
		},
	}
}

// runConfigGenreRemove removes a genre's multiplier for one dimension, or all
// of its multipliers if dimension is empty.
func runConfigGenreRemove(ctx context.Context, a *App, genre, dimension string) error {
	genreLower := strings.ToLower(genre)
	genres := cloneGenres(a.Config.Genres)
	if _, ok := genres[genreLower]; !ok {
		return fmt.Errorf("genre %q has no configured multipliers", genreLower)
	}

	if dimension == "" {
		delete(genres, genreLower)
	} else {
		if _, ok := genres[genreLower][dimension]; !ok {
			return fmt.Errorf("genre %q has no multiplier configured for dimension %q", genreLower, dimension)
		}
		delete(genres[genreLower], dimension)
		if len(genres[genreLower]) == 0 {
			delete(genres, genreLower)
		}
	}

	newCfg, err := config.Rebuild(
		a.Config, a.Config.Dimensions, genres, a.Config.PrimaryGenreWeight, a.Config.MaxMultiplier,
	)
	if err != nil {
		return fmt.Errorf("removing genre multiplier: %w", err)
	}
	if err := a.Store.SaveScoringConfig(ctx, newCfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Printf("✓ Genre %q multiplier removed\n", genreLower)
	return nil
}
