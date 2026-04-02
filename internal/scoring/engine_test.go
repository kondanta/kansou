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
		"story":         {Label: "Story", Weight: 0.25, BiasResistant: false},
		"enjoyment":     {Label: "Enjoyment", Weight: 0.20, BiasResistant: true},
		"characters":    {Label: "Characters", Weight: 0.15, BiasResistant: false},
		"production":    {Label: "Production", Weight: 0.15, BiasResistant: false},
		"pacing":        {Label: "Pacing", Weight: 0.10, BiasResistant: false},
		"world_building": {Label: "World Building", Weight: 0.10, BiasResistant: false},
		"value":         {Label: "Value", Weight: 0.05, BiasResistant: true},
	}
	genres := map[string]map[DimensionKey]float64{
		"action": {
			"production":    1.4,
			"pacing":        1.3,
			"story":         0.8,
			"world_building": 0.9,
		},
		"drama": {
			"story":      1.4,
			"characters": 1.3,
			"production": 0.8,
			"pacing":     1.1,
		},
		"mystery": {
			"story":         1.5,
			"pacing":        1.3,
			"world_building": 1.2,
		},
		"slice_of_life": {
			"characters":    1.4,
			"world_building": 0.7,
			"story":         0.8,
			"pacing":        0.9,
		},
		"supernatural": {
			"world_building": 1.3,
			"story":         1.1,
			"production":    1.1,
		},
	}
	return NewEngine(dims, defs, genres)
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

func TestScore_UnmatchedGenreContributes1(t *testing.T) {
	// "romance" is not in the test engine's genre config.
	// story multiplier should be 1.0 (unmatched genre defaults to 1.0).
	eng := testEngine()
	result, err := eng.Score(allTen([]string{"Romance"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, row := range result.Breakdown {
		if row.Key == "story" {
			if !approxEqual(row.AppliedMultiplier, 1.0, 0.001) {
				t.Errorf("unmatched genre: expected multiplier 1.0 for story, got %v", row.AppliedMultiplier)
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
			"pacing":        0.05,
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
