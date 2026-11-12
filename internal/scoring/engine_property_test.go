package scoring

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

const propTol = 1e-9

// randomEntry builds a valid Entry from r: random genres (random case, some
// unknown to the config), random skips (never all), up to two weight overrides
// on non-skipped dimensions (never on every active one), and an optional
// primary genre.
func randomEntry(r *rand.Rand, eng *Engine) Entry {
	pool := []string{"action", "drama", "mystery", "slice_of_life", "supernatural", "romance"}
	var genres []string
	for _, g := range pool {
		if r.IntN(2) == 0 {
			if r.IntN(2) == 0 {
				g = fmt.Sprintf("%c%s", g[0]-32, g[1:]) // Capitalised
			}
			genres = append(genres, g)
		}
	}

	entry := Entry{
		Scores:            map[DimensionKey]float64{},
		SkippedDimensions: map[DimensionKey]bool{},
		WeightOverrides:   map[DimensionKey]float64{},
		Genres:            genres,
	}
	var active []DimensionKey
	for _, k := range eng.dimensions {
		entry.Scores[k] = float64(r.IntN(37)+4) / 4 // 1.0 .. 10.0 in 0.25 steps
		if r.IntN(4) == 0 {
			entry.SkippedDimensions[k] = true
			continue
		}
		active = append(active, k)
	}
	if len(active) == 0 {
		delete(entry.SkippedDimensions, eng.dimensions[0])
		active = append(active, eng.dimensions[0])
	}
	// Overrides: leave at least one active dimension free to absorb the rest.
	for _, k := range active[:len(active)-1] {
		if len(entry.WeightOverrides) < 2 && r.IntN(3) == 0 {
			entry.WeightOverrides[k] = float64(r.IntN(8)+1) / 20 // 0.05 .. 0.40
		}
	}
	if len(genres) > 0 && r.IntN(2) == 0 {
		entry.PrimaryGenre = genres[r.IntN(len(genres))]
	}
	return entry
}

// TestScore_Properties checks invariants that must hold for any valid input.
// The seed is fixed so a failure reproduces; the failing iteration is reported.
func TestScore_Properties(t *testing.T) {
	eng := testEngine()
	r := rand.New(rand.NewPCG(1, 2))
	for i := range 2000 {
		entry := randomEntry(r, eng)
		res, err := eng.Score(entry)
		if err != nil {
			t.Fatalf("iter %d: %v (entry %+v)", i, err, entry)
		}
		checkInvariants(t, i, entry, res)
		checkScaling(t, i, eng, entry, res)
	}
}

func checkInvariants(t *testing.T, i int, entry Entry, res Result) {
	t.Helper()
	var weightSum, contribSum float64
	lo, hi := 11.0, -1.0
	for _, row := range res.Breakdown {
		weightSum += row.FinalWeight
		contribSum += row.Contribution
		if row.Skipped {
			if row.FinalWeight != 0 || row.Contribution != 0 {
				t.Errorf("iter %d: skipped %q has weight %v / contribution %v",
					i, row.Key, row.FinalWeight, row.Contribution)
			}
			continue
		}
		lo, hi = min(lo, row.Score), max(hi, row.Score)
		if want, ok := entry.WeightOverrides[row.Key]; ok {
			if !approxEqual(row.FinalWeight, want, propTol) || !row.WeightOverride {
				t.Errorf("iter %d: override %q: weight %v (flag %v), want %v",
					i, row.Key, row.FinalWeight, row.WeightOverride, want)
			}
		}
	}
	if !approxEqual(weightSum, 1.0, propTol) {
		t.Errorf("iter %d: final weights sum to %v, want 1", i, weightSum)
	}
	// FinalScore is rounded to 2dp, so allow half a cent.
	if !approxEqual(contribSum, res.FinalScore, 0.005+propTol) {
		t.Errorf("iter %d: contributions sum to %v, final score %v", i, contribSum, res.FinalScore)
	}
	if res.FinalScore < lo-0.005-propTol || res.FinalScore > hi+0.005+propTol {
		t.Errorf("iter %d: final score %v outside active score range [%v, %v]",
			i, res.FinalScore, lo, hi)
	}
}

// checkScaling: weights do not depend on scores, so halving every score halves
// the result. A uniform entry must score exactly its common value.
func checkScaling(t *testing.T, i int, eng *Engine, entry Entry, res Result) {
	t.Helper()
	half, uniform := entry, entry
	half.Scores, uniform.Scores = map[DimensionKey]float64{}, map[DimensionKey]float64{}
	for k, v := range entry.Scores {
		half.Scores[k] = v / 2
		uniform.Scores[k] = 7.25
	}
	h, err := eng.Score(half)
	if err != nil {
		t.Fatalf("iter %d: scaled: %v", i, err)
	}
	if !approxEqual(h.FinalScore*2, res.FinalScore, 0.01+propTol) {
		t.Errorf("iter %d: halved scores gave %v, full gave %v", i, h.FinalScore, res.FinalScore)
	}
	u, err := eng.Score(uniform)
	if err != nil {
		t.Fatalf("iter %d: uniform: %v", i, err)
	}
	if u.FinalScore != 7.25 {
		t.Errorf("iter %d: uniform 7.25 scored %v (weights do not sum to 1?)", i, u.FinalScore)
	}
}

// TestRound2_PinnedBehaviour documents that round2 rounds the binary float,
// not the decimal string: 1.005 is stored as 1.00499999999999989…, so it
// rounds DOWN. Changing this would change published scores (needs an ADR).
func TestRound2_PinnedBehaviour(t *testing.T) {
	tests := []struct {
		in, want float64
	}{
		{7.9125, 7.91},  // 791.25 → down
		{8.125, 8.13},   // exactly representable half → away from zero
		{1.005, 1.0},    // binary repr just below the half → down (decimal-correct would be 1.01)
		{1.015, 1.01},   // same effect (decimal-correct would be 1.02)
		{0.285, 0.28},   // same effect (decimal-correct would be 0.29)
		{0.004999, 0.0}, // below half a cent
		{0.005, 0.01},   // 0.005*100 == 0.5 → up
		{10, 10},
		{0, 0},
	}
	for _, tt := range tests {
		if got := round2(tt.in); got != tt.want {
			t.Errorf("round2(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
