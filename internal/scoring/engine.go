package scoring

import (
	"fmt"
	"math"
	"sort"
)

// Engine holds the config needed to score an entry. Construct it once per
// config load via NewEngine and reuse across sessions.
type Engine struct {
	// dimensions is the ordered list of dimension keys as they appear in config.
	// Order is preserved for deterministic breakdown output.
	dimensions []DimensionKey
	// defs maps each DimensionKey to its definition.
	defs map[DimensionKey]DimensionDef
	// genres maps lowercase genre name to a map of dimension key → multiplier.
	genres map[string]map[DimensionKey]float64
}

// NewEngine constructs an Engine from the provided dimension definitions and
// genre multiplier map. The dimensions slice defines the iteration order.
// genres maps lowercase genre names to per-dimension multipliers.
func NewEngine(
	dimensions []DimensionKey,
	defs map[DimensionKey]DimensionDef,
	genres map[string]map[DimensionKey]float64,
) *Engine {
	return &Engine{
		dimensions: dimensions,
		defs:       defs,
		genres:     genres,
	}
}

// Score calculates the final weighted score for the given Entry.
// It returns an error if:
//   - all dimensions are skipped
//   - a dimension in Entry.Scores is not present in the engine's config
//   - a --weight override targets a skipped dimension
func (e *Engine) Score(entry Entry) (Result, error) {
	if err := e.validateEntry(entry); err != nil {
		return Result{}, fmt.Errorf("validating entry: %w", err)
	}

	// Step 1: compute effective weights (base × genre multiplier).
	effective, breakdown, err := e.effectiveWeights(entry)
	if err != nil {
		return Result{}, fmt.Errorf("computing effective weights: %w", err)
	}

	// Step 2: renormalize effective weights to sum to 1.0.
	normalised := renormalise(effective)

	// Step 3: apply per-session weight overrides and rescale the remainder.
	finalWeights := applyOverrides(normalised, entry.WeightOverrides, entry.SkippedDimensions)

	// Step 4: compute final score and fill breakdown contributions.
	finalScore := 0.0
	for i := range breakdown {
		row := &breakdown[i]
		if row.Skipped {
			continue
		}
		w := finalWeights[row.Key]
		row.FinalWeight = w
		row.WeightOverride = isOverridden(row.Key, entry.WeightOverrides)
		row.Contribution = round2(entry.Scores[row.Key] * w)
		finalScore += entry.Scores[row.Key] * w
	}

	return Result{
		FinalScore: round2(finalScore),
		Breakdown:  breakdown,
		Meta:       entry.Meta,
	}, nil
}

// validateEntry checks that the entry is consistent before calculation.
func (e *Engine) validateEntry(entry Entry) error {
	// All dimensions skipped?
	active := 0
	for _, key := range e.dimensions {
		if !entry.SkippedDimensions[key] {
			active++
		}
	}
	if active == 0 {
		return fmt.Errorf("all dimensions were skipped — at least one dimension must be scored")
	}

	// --weight override on a skipped dimension?
	for key := range entry.WeightOverrides {
		if entry.SkippedDimensions[key] {
			return fmt.Errorf("dimension %q was both weight-overridden and skipped — these are mutually exclusive", key)
		}
	}

	// All scored dimensions exist in config?
	for key := range entry.Scores {
		if _, ok := e.defs[key]; !ok {
			return fmt.Errorf("unknown dimension %q — not present in config", key)
		}
	}

	return nil
}

// effectiveWeights computes base × averaged genre multiplier for each dimension,
// excluding skipped dimensions. Returns effective weights map and initial breakdown rows.
func (e *Engine) effectiveWeights(entry Entry) (map[DimensionKey]float64, []BreakdownRow, error) {
	effective := make(map[DimensionKey]float64, len(e.dimensions))
	breakdown := make([]BreakdownRow, 0, len(e.dimensions))

	// Collect matched genres (lowercased for case-insensitive lookup).
	matchedGenres := matchedGenreKeys(entry.Genres, e.genres)

	for _, key := range e.dimensions {
		def, ok := e.defs[key]
		if !ok {
			return nil, nil, fmt.Errorf("dimension %q in engine order not found in defs", key)
		}

		row := BreakdownRow{
			Key:           key,
			Label:         def.Label,
			BaseWeight:    def.Weight,
			BiasResistant: def.BiasResistant,
			Score:         entry.Scores[key],
			GenreMultipliers: make(map[string]float64),
		}

		if entry.SkippedDimensions[key] {
			row.Skipped = true
			row.AppliedMultiplier = 0
			breakdown = append(breakdown, row)
			// Skipped dimensions contribute 0 to the effective weight pool.
			continue
		}

		multiplier := 1.0
		if !def.BiasResistant {
			multiplier, row.GenreMultipliers = combinedMultiplier(key, matchedGenres, e.genres)
		}
		row.AppliedMultiplier = multiplier
		effective[key] = def.Weight * multiplier
		breakdown = append(breakdown, row)
	}

	return effective, breakdown, nil
}

// combinedMultiplier averages the per-dimension multiplier across all matched genres.
// Genres that do not define a multiplier for this dimension contribute 1.0.
// Returns the averaged multiplier and the per-genre contributions for provenance.
func combinedMultiplier(
	key DimensionKey,
	matchedGenres []string,
	genres map[string]map[DimensionKey]float64,
) (float64, map[string]float64) {
	if len(matchedGenres) == 0 {
		return 1.0, nil
	}

	contributions := make(map[string]float64)
	sum := 0.0
	for _, genre := range matchedGenres {
		m := 1.0
		if gm, ok := genres[genre]; ok {
			if v, ok := gm[key]; ok {
				m = v
				contributions[genre] = v
			}
		}
		sum += m
	}
	return sum / float64(len(matchedGenres)), contributions
}

// renormalise returns a new weight map scaled so values sum to 1.0.
// Dimensions absent from the map (i.e. skipped) are not included.
func renormalise(weights map[DimensionKey]float64) map[DimensionKey]float64 {
	total := 0.0
	for _, w := range weights {
		total += w
	}
	if total == 0 {
		return weights
	}
	out := make(map[DimensionKey]float64, len(weights))
	for k, w := range weights {
		out[k] = w / total
	}
	return out
}

// applyOverrides fixes overridden dimension weights and rescales the remainder
// proportionally so all weights still sum to 1.0.
func applyOverrides(
	weights map[DimensionKey]float64,
	overrides map[DimensionKey]float64,
	skipped map[DimensionKey]bool,
) map[DimensionKey]float64 {
	if len(overrides) == 0 {
		return weights
	}

	out := make(map[DimensionKey]float64, len(weights))

	// Sum of overridden values and remaining budget for non-overridden dimensions.
	overriddenSum := 0.0
	for key, v := range overrides {
		if !skipped[key] {
			overriddenSum += v
			out[key] = v
		}
	}

	// Remaining budget goes to non-overridden, non-skipped dimensions,
	// distributed proportionally based on their pre-override weights.
	remaining := 1.0 - overriddenSum
	nonOverrideTotal := 0.0
	for key, w := range weights {
		if _, isOverride := overrides[key]; !isOverride {
			nonOverrideTotal += w
		}
	}

	for key, w := range weights {
		if _, isOverride := overrides[key]; isOverride {
			continue // already set above
		}
		if nonOverrideTotal == 0 {
			out[key] = 0
			continue
		}
		out[key] = (w / nonOverrideTotal) * remaining
	}

	return out
}

// matchedGenreKeys returns the lowercased genre keys from entry genres that
// exist in the config genre map.
func matchedGenreKeys(genres []string, configGenres map[string]map[DimensionKey]float64) []string {
	seen := make(map[string]bool, len(genres))
	matched := make([]string, 0, len(genres))
	for _, g := range genres {
		lower := toLower(g)
		if _, ok := configGenres[lower]; ok && !seen[lower] {
			matched = append(matched, lower)
			seen[lower] = true
		}
	}
	// Sort for deterministic output.
	sort.Strings(matched)
	return matched
}

// isOverridden reports whether key is present in the overrides map.
func isOverridden(key DimensionKey, overrides map[DimensionKey]float64) bool {
	_, ok := overrides[key]
	return ok
}

// round2 rounds f to two decimal places.
func round2(f float64) float64 {
	return math.Round(f*100) / 100
}

// toLower is a dependency-free ASCII lowercase for genre keys.
func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
