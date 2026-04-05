package scoring

import (
	"math"
	"testing"
)

// testEngine returns an Engine wired with the reference 7-dimension config
// from config.example.toml. Reused across test cases.
func testEngine() *Engine {
	dims := []DimensionKey{
		"story", "enjoyment", "characters", "production", "pacing", "world_building", "value",
	}
	defs := map[DimensionKey]DimensionDef{
		"story":          {Label: "Story", Weight: 0.25, BiasResistant: false},
		"enjoyment":      {Label: "Enjoyment", Weight: 0.20, BiasResistant: true},
		"characters":     {Label: "Characters", Weight: 0.15, BiasResistant: false},
		"production":     {Label: "Production", Weight: 0.15, BiasResistant: false},
		"pacing":         {Label: "Pacing", Weight: 0.10, BiasResistant: false},
		"world_building": {Label: "World Building", Weight: 0.10, BiasResistant: false},
		"value":          {Label: "Value", Weight: 0.05, BiasResistant: true},
	}
	genres := map[string]map[DimensionKey]float64{
		"action": {
			"production":     1.4,
			"pacing":         1.3,
			"story":          0.8,
			"world_building": 0.9,
		},
		"drama": {
			"story":      1.4,
			"characters": 1.3,
			"production": 0.8,
			"pacing":     1.1,
		},
		"mystery": {
			"story":          1.5,
			"pacing":         1.3,
			"world_building": 1.2,
		},
		"slice_of_life": {
			"characters":     1.4,
			"world_building": 0.7,
			"story":          0.8,
			"pacing":         0.9,
		},
		"supernatural": {
			"world_building": 1.3,
			"story":          1.1,
			"production":     1.1,
		},
	}
	return NewEngine(dims, defs, genres, 0.6)
}

// allTen returns an Entry with every dimension scored 10, no skips, no overrides.
func allTen(genres []string) Entry {
	return Entry{
		Scores: map[DimensionKey]float64{
			"story": 10, "enjoyment": 10, "characters": 10,
			"production": 10, "pacing": 10, "world_building": 10, "value": 10,
		},
		SkippedDimensions: map[DimensionKey]bool{},
		WeightOverrides:   map[DimensionKey]float64{},
		Genres:            genres,
	}
}

func approxEqual(a, b, tolerance float64) bool {
	return math.Abs(a-b) <= tolerance
}

func TestScore_AllTen_NoGenre(t *testing.T) {
	eng := testEngine()
	result, err := eng.Score(allTen(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FinalScore != 10.0 {
		t.Errorf("expected 10.0, got %v", result.FinalScore)
	}
}

func TestScore_AllTen_GenreNoEffect(t *testing.T) {
	// All-10 scores: genre multipliers shift weights but every dimension is 10,
	// so the final score must still be 10.0 regardless of genre.
	eng := testEngine()
	result, err := eng.Score(allTen([]string{"Action", "Drama"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FinalScore != 10.0 {
		t.Errorf("expected 10.0 for uniform scores, got %v", result.FinalScore)
	}
}

func TestScore_WeightedCalculation(t *testing.T) {
	// With no genres and no overrides, the result is simply Σ(score × weight).
	// Using reference weights: story=0.25, enjoyment=0.20, characters=0.15,
	// production=0.15, pacing=0.10, world_building=0.10, value=0.05.
	// Expected = 9×0.25 + 10×0.20 + 8×0.15 + 9×0.15 + 8×0.10 + 9×0.10 + 7×0.05
	//          = 2.25 + 2.00 + 1.20 + 1.35 + 0.80 + 0.90 + 0.35 = 8.85
	eng := testEngine()
	entry := Entry{
		Scores: map[DimensionKey]float64{
			"story": 9, "enjoyment": 10, "characters": 8,
			"production": 9, "pacing": 8, "world_building": 9, "value": 7,
		},
		SkippedDimensions: map[DimensionKey]bool{},
		WeightOverrides:   map[DimensionKey]float64{},
		Genres:            nil,
	}
	result, err := eng.Score(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approxEqual(result.FinalScore, 8.85, 0.01) {
		t.Errorf("expected ~8.85, got %v", result.FinalScore)
	}
}

func TestScore_BiasResistantIgnoresGenreMultiplier(t *testing.T) {
	// "enjoyment" and "value" are bias-resistant — their multiplier must be 1.0
	// even when genre config defines a different value for them.
	eng := testEngine()
	// Action genre defines no multiplier for enjoyment/value, but we test
	// that the BiasResistant flag is respected in the breakdown.
	result, err := eng.Score(allTen([]string{"Action"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range result.Breakdown {
		if row.BiasResistant && row.AppliedMultiplier != 1.0 {
			t.Errorf("bias-resistant dimension %q got multiplier %v, want 1.0", row.Key, row.AppliedMultiplier)
		}
	}
}

func TestScore_GenreMultiplierAveraged(t *testing.T) {
	// story: action=0.8, drama=1.4 → average = (0.8+1.4)/2 = 1.1
	eng := testEngine()
	result, err := eng.Score(allTen([]string{"Action", "Drama"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range result.Breakdown {
		if row.Key == "story" {
			if !approxEqual(row.AppliedMultiplier, 1.1, 0.001) {
				t.Errorf("story multiplier: expected 1.1, got %v", row.AppliedMultiplier)
			}
		}
	}
}

func TestScore_NoMatchedGenres_Multiplier1(t *testing.T) {
	// "romance" is not in the test engine's genre config at all, so no genres
	// match. All dimension multipliers must be 1.0 (contributing-only averaging: no opinions → neutral).
	eng := testEngine()
	result, err := eng.Score(allTen([]string{"Romance"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range result.Breakdown {
		if !approxEqual(row.AppliedMultiplier, 1.0, 0.001) && !row.Skipped && !row.BiasResistant {
			t.Errorf("no matched genres: expected multiplier 1.0 for %q, got %v", row.Key, row.AppliedMultiplier)
		}
	}
}

func TestScore_ContributingOnly_DimensionlessGenreExcluded(t *testing.T) {
	// Action defines production, pacing, story, world_building — but NOT characters.
	// Drama defines story, characters, production, pacing.
	// Old behavior: characters = (drama.characters + action.neutral_1.0) / 2 = (1.3+1.0)/2 = 1.15
	// contributing-only averaging:     characters = drama.characters / 1 = 1.3 (action excluded for this dimension)
	eng := testEngine()
	result, err := eng.Score(allTen([]string{"Action", "Drama"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range result.Breakdown {
		if row.Key == "characters" {
			if !approxEqual(row.AppliedMultiplier, 1.3, 0.001) {
				t.Errorf("contributing-only averaging: characters multiplier expected 1.3, got %v (action should be excluded as it has no opinion)", row.AppliedMultiplier)
			}
		}
	}
}

func TestScore_PartialGenreMatch(t *testing.T) {
	// "mystery" is in config (story=1.5); "romance" is not in the test engine's
	// genre config and is ignored entirely — it does NOT contribute a 1.0 to the
	// average. Unmatched genres are excluded from the pool per FR-03b.
	// story average = 1.5/1 = 1.5 (only mystery contributes).
	eng := testEngine()
	result, err := eng.Score(allTen([]string{"Mystery", "Romance"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range result.Breakdown {
		if row.Key == "story" {
			if !approxEqual(row.AppliedMultiplier, 1.5, 0.001) {
				t.Errorf("partial genre match: story multiplier expected 1.5, got %v", row.AppliedMultiplier)
			}
		}
	}
}

func TestScore_WeightsRenormaliseAfterGenre(t *testing.T) {
	// After genre adjustment, final weights must sum to 1.0.
	eng := testEngine()
	result, err := eng.Score(allTen([]string{"Action", "Drama", "Mystery"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sum := 0.0
	for _, row := range result.Breakdown {
		sum += row.FinalWeight
	}
	if !approxEqual(sum, 1.0, 0.001) {
		t.Errorf("final weights sum to %v, expected 1.0", sum)
	}
}

func TestScore_SkippedDimensionExcludedFromPool(t *testing.T) {
	// Skip "value". Remaining weights must renormalise to 1.0.
	eng := testEngine()
	entry := Entry{
		Scores: map[DimensionKey]float64{
			"story": 9, "enjoyment": 10, "characters": 8,
			"production": 9, "pacing": 8, "world_building": 9,
		},
		SkippedDimensions: map[DimensionKey]bool{"value": true},
		WeightOverrides:   map[DimensionKey]float64{},
		Genres:            nil,
	}
	result, err := eng.Score(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sum := 0.0
	for _, row := range result.Breakdown {
		if row.Key == "value" {
			if !row.Skipped {
				t.Error("expected value to be marked skipped")
			}
			if row.FinalWeight != 0 || row.Contribution != 0 {
				t.Errorf("skipped dimension should have FinalWeight=0 and Contribution=0")
			}
		}
		sum += row.FinalWeight
	}
	if !approxEqual(sum, 1.0, 0.001) {
		t.Errorf("weights after skip sum to %v, expected 1.0", sum)
	}
}

func TestScore_AllDimensionsSkipped_Error(t *testing.T) {
	eng := testEngine()
	entry := Entry{
		Scores: map[DimensionKey]float64{},
		SkippedDimensions: map[DimensionKey]bool{
			"story": true, "enjoyment": true, "characters": true,
			"production": true, "pacing": true, "world_building": true, "value": true,
		},
		WeightOverrides: map[DimensionKey]float64{},
	}
	_, err := eng.Score(entry)
	if err == nil {
		t.Fatal("expected error when all dimensions are skipped")
	}
}

func TestScore_WeightOverride_OverriddenFixed_RemainderRescaled(t *testing.T) {
	// Override pacing=0.05, world_building=0.20. Total overridden=0.25.
	// Remaining budget=0.75, distributed proportionally across the other 5 dimensions.
	eng := testEngine()
	entry := Entry{
		Scores: map[DimensionKey]float64{
			"story": 9, "enjoyment": 10, "characters": 8,
			"production": 9, "pacing": 8, "world_building": 9, "value": 7,
		},
		SkippedDimensions: map[DimensionKey]bool{},
		WeightOverrides: map[DimensionKey]float64{
			"pacing":         0.05,
			"world_building": 0.20,
		},
		Genres: nil,
	}
	result, err := eng.Score(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, row := range result.Breakdown {
		switch row.Key {
		case "pacing":
			if !approxEqual(row.FinalWeight, 0.05, 0.001) {
				t.Errorf("pacing: expected override weight 0.05, got %v", row.FinalWeight)
			}
			if !row.WeightOverride {
				t.Error("pacing: expected WeightOverride=true")
			}
		case "world_building":
			if !approxEqual(row.FinalWeight, 0.20, 0.001) {
				t.Errorf("world_building: expected override weight 0.20, got %v", row.FinalWeight)
			}
			if !row.WeightOverride {
				t.Error("world_building: expected WeightOverride=true")
			}
		}
	}

	// All weights must still sum to 1.0.
	sum := 0.0
	for _, row := range result.Breakdown {
		sum += row.FinalWeight
	}
	if !approxEqual(sum, 1.0, 0.001) {
		t.Errorf("weights after override sum to %v, expected 1.0", sum)
	}
}

func TestScore_WeightOverrideAndSkip_Error(t *testing.T) {
	eng := testEngine()
	entry := Entry{
		Scores: map[DimensionKey]float64{
			"story": 9, "enjoyment": 10, "characters": 8,
			"production": 9, "world_building": 9, "value": 7,
		},
		SkippedDimensions: map[DimensionKey]bool{"pacing": true},
		WeightOverrides:   map[DimensionKey]float64{"pacing": 0.10},
		Genres:            nil,
	}
	_, err := eng.Score(entry)
	if err == nil {
		t.Fatal("expected error when override and skip are both set for same dimension")
	}
}

func TestScore_BreakdownAlwaysPopulated(t *testing.T) {
	eng := testEngine()
	result, err := eng.Score(allTen(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Breakdown) != 7 {
		t.Errorf("expected 7 breakdown rows, got %d", len(result.Breakdown))
	}
	for _, row := range result.Breakdown {
		if row.Key == "" {
			t.Error("breakdown row has empty key")
		}
		if row.Label == "" {
			t.Error("breakdown row has empty label")
		}
	}
}

func TestScore_BreakdownOrderMatchesConfig(t *testing.T) {
	// Breakdown rows must appear in the same order as the engine's dimension slice.
	eng := testEngine()
	result, err := eng.Score(allTen(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []DimensionKey{
		"story", "enjoyment", "characters", "production", "pacing", "world_building", "value",
	}
	for i, row := range result.Breakdown {
		if row.Key != expected[i] {
			t.Errorf("breakdown[%d]: expected %q, got %q", i, expected[i], row.Key)
		}
	}
}

func TestScore_UnknownDimensionInScores_Error(t *testing.T) {
	eng := testEngine()
	entry := allTen(nil)
	entry.Scores["nonexistent"] = 5
	_, err := eng.Score(entry)
	if err == nil {
		t.Fatal("expected error for unknown dimension key in scores")
	}
}

func TestScore_GenreCaseInsensitive(t *testing.T) {
	// "ACTION", "action", and "Action" should all match the same genre block.
	eng := testEngine()
	r1, err := eng.Score(allTen([]string{"action"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r2, err := eng.Score(allTen([]string{"ACTION"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r3, err := eng.Score(allTen([]string{"Action"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r1.FinalScore != r2.FinalScore || r2.FinalScore != r3.FinalScore {
		t.Errorf("genre case sensitivity: scores differ: %v %v %v", r1.FinalScore, r2.FinalScore, r3.FinalScore)
	}
}

func TestScore_MultipleMatchedGenres_MultiplierAveragedNotMultiplied(t *testing.T) {
	// story: mystery=1.5, slice_of_life=0.8, supernatural=1.1
	// average = (1.5+0.8+1.1)/3 = 1.133...
	eng := testEngine()
	result, err := eng.Score(allTen([]string{"Mystery", "Slice_of_Life", "Supernatural"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range result.Breakdown {
		if row.Key == "story" {
			expected := (1.5 + 0.8 + 1.1) / 3
			if !approxEqual(row.AppliedMultiplier, expected, 0.001) {
				t.Errorf("story multiplier: expected %v, got %v", expected, row.AppliedMultiplier)
			}
		}
	}
}

func TestRenormalise_SumsToOne(t *testing.T) {
	cases := []struct {
		name    string
		weights map[DimensionKey]float64
	}{
		{"uniform", map[DimensionKey]float64{"a": 0.25, "b": 0.25, "c": 0.25, "d": 0.25}},
		{"uneven", map[DimensionKey]float64{"a": 0.3, "b": 0.5, "c": 0.2}},
		{"single", map[DimensionKey]float64{"a": 0.7}},
		{"after multipliers", map[DimensionKey]float64{"a": 0.35, "b": 0.18, "c": 0.12}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := renormalise(tc.weights)
			sum := 0.0
			for _, v := range out {
				sum += v
			}
			if !approxEqual(sum, 1.0, 0.001) {
				t.Errorf("renormalise(%v): sum=%v, expected 1.0", tc.weights, sum)
			}
		})
	}
}

func TestApplyOverrides_SumsToOne(t *testing.T) {
	cases := []struct {
		name      string
		weights   map[DimensionKey]float64
		overrides map[DimensionKey]float64
	}{
		{
			"single override",
			map[DimensionKey]float64{"a": 0.4, "b": 0.3, "c": 0.3},
			map[DimensionKey]float64{"a": 0.10},
		},
		{
			"two overrides",
			map[DimensionKey]float64{"a": 0.4, "b": 0.3, "c": 0.2, "d": 0.1},
			map[DimensionKey]float64{"a": 0.05, "b": 0.20},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := applyOverrides(tc.weights, tc.overrides, map[DimensionKey]bool{})
			sum := 0.0
			for _, v := range out {
				sum += v
			}
			if !approxEqual(sum, 1.0, 0.001) {
				t.Errorf("applyOverrides: sum=%v, expected 1.0", sum)
			}
			for k, v := range tc.overrides {
				if !approxEqual(out[k], v, 0.001) {
					t.Errorf("override key %q: expected %v, got %v", k, v, out[k])
				}
			}
		})
	}
}

func TestCombinedMultiplier_NoGenres(t *testing.T) {
	m, _ := combinedMultiplier("story", nil, nil)
	if m != 1.0 {
		t.Errorf("expected 1.0 for no genres, got %v", m)
	}
}

func TestCombinedMultiplier_SingleGenre(t *testing.T) {
	genres := map[string]map[DimensionKey]float64{
		"action": {"story": 0.8},
	}
	m, contributions := combinedMultiplier("story", []string{"action"}, genres)
	if !approxEqual(m, 0.8, 0.001) {
		t.Errorf("expected 0.8, got %v", m)
	}
	if contributions["action"] != 0.8 {
		t.Errorf("expected contribution action=0.8, got %v", contributions["action"])
	}
}

func TestCombinedMultiplier_ContributingOnly_NoDimensionEntry(t *testing.T) {
	// genre "action" defines story=0.8 but NOT characters.
	// For "characters": action should be excluded by contributing-only averaging → result 1.0.
	genres := map[string]map[DimensionKey]float64{
		"action": {"story": 0.8},
	}
	m, contrib := combinedMultiplier("characters", []string{"action"}, genres)
	if m != 1.0 {
		t.Errorf("contributing-only averaging: expected 1.0 when no genre has opinion on dimension, got %v", m)
	}
	if len(contrib) != 0 {
		t.Errorf("expected empty contributions, got %v", contrib)
	}
}

func TestScore_PrimaryGenre_BlendApplied(t *testing.T) {
	// Mystery (primary, story=1.5) + Action (secondary, story=0.8), blend=0.6.
	// final = (1.5 × 0.6) + (0.8 × 0.4) = 0.9 + 0.32 = 1.22
	eng := testEngine()
	entry := allTen([]string{"Mystery", "Action"})
	entry.PrimaryGenre = "mystery"
	result, err := eng.Score(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range result.Breakdown {
		if row.Key == "story" {
			expected := (1.5 * 0.6) + (0.8 * 0.4)
			if !approxEqual(row.AppliedMultiplier, expected, 0.001) {
				t.Errorf("primary blend: story multiplier expected %.4f, got %v", expected, row.AppliedMultiplier)
			}
			if !approxEqual(row.PrimaryGenreMultiplier, 1.5, 0.001) {
				t.Errorf("primary blend: PrimaryGenreMultiplier expected 1.5, got %v", row.PrimaryGenreMultiplier)
			}
		}
	}
}

func TestScore_PrimaryGenre_NoPrimary_FallsBackToOptionB(t *testing.T) {
	// Same genres without primary genre flag → plain contributing-only average.
	// story: mystery=1.5, action=0.8 → (1.5+0.8)/2 = 1.15
	eng := testEngine()
	result, err := eng.Score(allTen([]string{"Mystery", "Action"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range result.Breakdown {
		if row.Key == "story" {
			expected := (1.5 + 0.8) / 2
			if !approxEqual(row.AppliedMultiplier, expected, 0.001) {
				t.Errorf("fallback to contributing-only averaging: story multiplier expected %.4f, got %v", expected, row.AppliedMultiplier)
			}
		}
	}
}

func TestScore_PrimaryGenre_NoSecondary_BlendWithNeutral(t *testing.T) {
	// Only mystery matched (primary). No secondary genres.
	// story: primary_mult=1.5, secondary_avg=1.0 (neutral), blend=0.6.
	// final = (1.5 × 0.6) + (1.0 × 0.4) = 0.9 + 0.4 = 1.30
	eng := testEngine()
	entry := allTen([]string{"Mystery"})
	entry.PrimaryGenre = "mystery"
	result, err := eng.Score(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range result.Breakdown {
		if row.Key == "story" {
			expected := (1.5 * 0.6) + (1.0 * 0.4)
			if !approxEqual(row.AppliedMultiplier, expected, 0.001) {
				t.Errorf("primary only: story multiplier expected %.4f, got %v", expected, row.AppliedMultiplier)
			}
		}
	}
}

func TestScore_PrimaryGenre_NoDimensionEntry_PrimaryMult1(t *testing.T) {
	// Mystery has no "characters" entry. Primary mult defaults to 1.0.
	// Drama (secondary) has characters=1.3.
	// final_characters = (1.0 × 0.6) + (1.3 × 0.4) = 0.6 + 0.52 = 1.12
	eng := testEngine()
	entry := allTen([]string{"Mystery", "Drama"})
	entry.PrimaryGenre = "mystery"
	result, err := eng.Score(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range result.Breakdown {
		if row.Key == "characters" {
			expected := (1.0 * 0.6) + (1.3 * 0.4)
			if !approxEqual(row.AppliedMultiplier, expected, 0.001) {
				t.Errorf("primary no-opinion: characters multiplier expected %.4f, got %v", expected, row.AppliedMultiplier)
			}
			// PrimaryGenreMultiplier should be 0 because mystery has no characters entry.
			if row.PrimaryGenreMultiplier != 0 {
				t.Errorf("expected PrimaryGenreMultiplier=0 when primary has no dimension entry, got %v", row.PrimaryGenreMultiplier)
			}
		}
	}
}

func TestScore_PrimaryGenre_WeightsStillSumToOne(t *testing.T) {
	// After primary genre blending, final weights must still sum to 1.0.
	eng := testEngine()
	entry := allTen([]string{"Mystery", "Drama", "Action"})
	entry.PrimaryGenre = "mystery"
	result, err := eng.Score(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sum := 0.0
	for _, row := range result.Breakdown {
		sum += row.FinalWeight
	}
	if !approxEqual(sum, 1.0, 0.001) {
		t.Errorf("primary genre blend: final weights sum to %v, expected 1.0", sum)
	}
}

// --- v2 tests: Engine.Weights() and UserSelectedGenres ---

func TestWeights_SumsToOne_NoGenres(t *testing.T) {
	// Weights() with no genres or overrides must produce final weights summing to 1.0.
	eng := testEngine()
	rows := eng.Weights(nil, "", map[DimensionKey]bool{}, map[DimensionKey]float64{})
	sum := 0.0
	for _, wr := range rows {
		sum += wr.FinalWeight
	}
	if !approxEqual(sum, 1.0, 0.001) {
		t.Errorf("Weights() no genres: sum=%v, expected 1.0", sum)
	}
}

func TestWeights_MatchesScoreBreakdown_FinalWeights(t *testing.T) {
	// Weights() and Score() must produce identical FinalWeights for every dimension.
	eng := testEngine()
	genres := []string{"Mystery", "Drama"}
	entry := allTen(genres)

	result, err := eng.Score(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows := eng.Weights(genres, "", map[DimensionKey]bool{}, map[DimensionKey]float64{})
	weightByKey := make(map[DimensionKey]float64, len(rows))
	for _, wr := range rows {
		weightByKey[wr.Key] = wr.FinalWeight
	}
	for _, row := range result.Breakdown {
		if !approxEqual(row.FinalWeight, weightByKey[row.Key], 0.0001) {
			t.Errorf("dimension %q: Score FinalWeight=%v, Weights FinalWeight=%v", row.Key, row.FinalWeight, weightByKey[row.Key])
		}
	}
}

func TestWeights_SkippedDimension_ZeroFinalWeight(t *testing.T) {
	eng := testEngine()
	skipped := map[DimensionKey]bool{"value": true}
	rows := eng.Weights(nil, "", skipped, map[DimensionKey]float64{})
	for _, wr := range rows {
		if wr.Key == "value" {
			if !wr.Skipped {
				t.Error("expected value to be marked Skipped")
			}
			if wr.FinalWeight != 0 {
				t.Errorf("skipped dimension FinalWeight should be 0, got %v", wr.FinalWeight)
			}
		}
	}
	// Remaining weights must still sum to 1.0.
	sum := 0.0
	for _, wr := range rows {
		sum += wr.FinalWeight
	}
	if !approxEqual(sum, 1.0, 0.001) {
		t.Errorf("Weights() with skip: sum=%v, expected 1.0", sum)
	}
}

func TestWeights_Override_FixedAndRemainder(t *testing.T) {
	eng := testEngine()
	overrides := map[DimensionKey]float64{"pacing": 0.05, "world_building": 0.20}
	rows := eng.Weights(nil, "", map[DimensionKey]bool{}, overrides)
	for _, wr := range rows {
		switch wr.Key {
		case "pacing":
			if !approxEqual(wr.FinalWeight, 0.05, 0.001) {
				t.Errorf("pacing override: expected 0.05, got %v", wr.FinalWeight)
			}
			if !wr.WeightOverride {
				t.Error("pacing: expected WeightOverride=true")
			}
		case "world_building":
			if !approxEqual(wr.FinalWeight, 0.20, 0.001) {
				t.Errorf("world_building override: expected 0.20, got %v", wr.FinalWeight)
			}
		}
	}
	sum := 0.0
	for _, wr := range rows {
		sum += wr.FinalWeight
	}
	if !approxEqual(sum, 1.0, 0.001) {
		t.Errorf("Weights() with overrides: sum=%v, expected 1.0", sum)
	}
}

func TestScore_UserSelectedGenres_RestrictsActiveSet(t *testing.T) {
	// Media has Mystery + Action. User selects only Mystery.
	// story multiplier should be mystery=1.5 (action excluded from active set).
	eng := testEngine()
	entry := allTen([]string{"Mystery", "Action"})
	entry.UserSelectedGenres = []string{"Mystery"}

	result, err := eng.Score(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range result.Breakdown {
		if row.Key == "story" {
			// With only mystery active, story mult = 1.5 (not (1.5+0.8)/2=1.15).
			if !approxEqual(row.AppliedMultiplier, 1.5, 0.001) {
				t.Errorf("UserSelectedGenres: story multiplier expected 1.5, got %v", row.AppliedMultiplier)
			}
		}
	}
}

func TestScore_UserSelectedGenres_GenreDeselected_FlagSet(t *testing.T) {
	// Media has Mystery + Action. User selects only Mystery.
	// Action defines production, pacing, story, world_building.
	// Those dimensions should have GenreDeselected=true.
	// Characters (only drama defines it, drama not in media.Genres) should be false.
	eng := testEngine()
	entry := allTen([]string{"Mystery", "Action"})
	entry.UserSelectedGenres = []string{"Mystery"}

	result, err := eng.Score(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Action defines: production, pacing, story, world_building → GenreDeselected expected true.
	deselectedExpected := map[DimensionKey]bool{
		"production":     true,
		"pacing":         true,
		"story":          true,
		"world_building": true,
	}
	for _, row := range result.Breakdown {
		want := deselectedExpected[row.Key]
		if row.GenreDeselected != want {
			t.Errorf("dimension %q: GenreDeselected expected %v, got %v", row.Key, want, row.GenreDeselected)
		}
	}
}

func TestScore_UserSelectedGenres_None_NoDeselectedFlag(t *testing.T) {
	// When UserSelectedGenres is nil, GenreDeselected must always be false.
	eng := testEngine()
	result, err := eng.Score(allTen([]string{"Mystery", "Action"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range result.Breakdown {
		if row.GenreDeselected {
			t.Errorf("dimension %q: GenreDeselected should be false when no UserSelectedGenres, got true", row.Key)
		}
	}
}

func TestScore_UserSelectedGenres_AllSelected_NoDeselectedFlag(t *testing.T) {
	// When UserSelectedGenres contains all the same genres, no genre is deselected.
	eng := testEngine()
	entry := allTen([]string{"Mystery", "Action"})
	entry.UserSelectedGenres = []string{"Mystery", "Action"}

	result, err := eng.Score(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range result.Breakdown {
		if row.GenreDeselected {
			t.Errorf("dimension %q: GenreDeselected should be false when all genres selected, got true", row.Key)
		}
	}
}

func TestScore_UserSelectedGenres_GenresActiveInMeta(t *testing.T) {
	// GenresActive in meta should reflect the active (selected) genre set,
	// not the full AniList genre list.
	eng := testEngine()
	entry := allTen([]string{"Mystery", "Action"})
	entry.UserSelectedGenres = []string{"Mystery"}

	result, err := eng.Score(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// GenresActive should only contain "mystery" (lowercased, only matched config genre).
	if len(result.Meta.GenresActive) != 1 || result.Meta.GenresActive[0] != "mystery" {
		t.Errorf("GenresActive: expected [mystery], got %v", result.Meta.GenresActive)
	}
}

func TestScore_NoUserSelectedGenres_GenresActiveEqualsMatchedGenres(t *testing.T) {
	// When UserSelectedGenres is absent, GenresActive = matched genres from full list.
	eng := testEngine()
	entry := allTen([]string{"Mystery", "Action", "Romance"}) // Romance not in config.

	result, err := eng.Score(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Matched genres = mystery + action (romance not in config). GenresActive should match.
	if len(result.Meta.GenresActive) != 2 {
		t.Errorf("GenresActive: expected 2 entries, got %v", result.Meta.GenresActive)
	}
}

func TestWeights_OrderMatchesEngineConfig(t *testing.T) {
	// Weights() rows must appear in the same order as the engine's dimension slice.
	eng := testEngine()
	rows := eng.Weights(nil, "", map[DimensionKey]bool{}, map[DimensionKey]float64{})
	expected := []DimensionKey{
		"story", "enjoyment", "characters", "production", "pacing", "world_building", "value",
	}
	if len(rows) != len(expected) {
		t.Fatalf("expected %d rows, got %d", len(expected), len(rows))
	}
	for i, wr := range rows {
		if wr.Key != expected[i] {
			t.Errorf("rows[%d]: expected %q, got %q", i, expected[i], wr.Key)
		}
	}
}
