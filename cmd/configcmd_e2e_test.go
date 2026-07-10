package cmd

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/store/sqlite"
	"github.com/spf13/cobra"
)

// seedTestConfig returns a minimal, valid two-dimension config for config
// command tests: story=0.6, fun=0.4 (sums to 1.0).
func seedTestConfig() *config.Config {
	return &config.Config{
		DimensionOrder: []string{"fun", "story"},
		Dimensions: map[string]config.DimensionDef{
			"story": {Label: "Story", Description: "Narrative quality", Weight: 0.6},
			"fun":   {Label: "Fun", Description: "Enjoyment", Weight: 0.4},
		},
		Genres:             map[string]map[string]float64{},
		PrimaryGenreWeight: config.DefaultPrimaryGenreWeight,
		MaxMultiplier:      config.DefaultMaxMultiplier,
	}
}

// TestConfigCmd_DBMode_EndToEnd wires the real cobra commands, a real
// on-disk SQLite database, and the real Store implementation together and
// exercises the DB-backed config dimension/genre mutation flows. Subtests
// run in sequence and each builds on the previous one's persisted state
// (extracted into named helpers below to keep this function's own
// cyclomatic complexity down — golangci-lint counts every subtest closure
// inline against the same function otherwise).
func TestConfigCmd_DBMode_EndToEnd(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "kansou.db")
	st, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := seedTestConfig()
	if err := st.SaveScoringConfig(ctx, cfg); err != nil {
		t.Fatalf("seeding config: %v", err)
	}

	app := &App{Store: st, Config: cfg}
	reload := func() {
		reloaded, err := st.LoadScoringConfig(ctx)
		if err != nil {
			t.Fatalf("reloading config: %v", err)
		}
		app.Config = reloaded
	}

	t.Run("show", func(t *testing.T) { testConfigShow(t, app) })
	t.Run("dimension add fails until weights sum to 1.0", func(t *testing.T) {
		testConfigDimensionAddFailsUnbalanced(t, ctx, app)
	})
	t.Run("dimension add succeeds when weights sum to 1.0", func(t *testing.T) {
		testConfigDimensionAddSucceeds(t, ctx, st, app, reload)
	})
	t.Run("dimension list shows the new dimension", func(t *testing.T) {
		testConfigDimensionList(t, ctx, app)
	})
	t.Run("genre set then remove", func(t *testing.T) {
		testConfigGenreSetThenRemove(t, ctx, app, reload)
	})
	t.Run("genre set rejects unknown dimension", func(t *testing.T) {
		testConfigGenreSetRejectsUnknownDimension(t, ctx, app)
	})
	t.Run("genre set rejects multiplier above max_multiplier", func(t *testing.T) {
		testConfigGenreSetRejectsExcessiveMultiplier(t, ctx, app)
	})
	t.Run(
		"dimension remove rebalances remaining weights and strips referencing genre multipliers",
		func(t *testing.T) {
			testConfigDimensionRemoveRebalances(t, ctx, app, reload)
		},
	)
	t.Run("dimension remove refuses to remove the last dimension", func(t *testing.T) {
		testConfigDimensionRemoveFloor(t, ctx, app, reload)
	})
}

func testConfigShow(t *testing.T, app *App) {
	showCmd := app.configShowCmd()
	out := captureStdout(t, func() {
		if err := showCmd.RunE(showCmd, nil); err != nil {
			t.Fatalf("config show: %v", err)
		}
	})
	if !strings.Contains(out, "story") || !strings.Contains(out, "0.6000") {
		t.Errorf("show output missing expected content, got: %q", out)
	}
}

func testConfigDimensionAddFailsUnbalanced(t *testing.T, ctx context.Context, app *App) {
	addCmd := app.configDimensionAddCmd()
	addCmd.SetContext(ctx)
	mustSetFlag(t, addCmd, "label", "Pacing")
	mustSetFlag(t, addCmd, "weight", "0.2")
	err := addCmd.RunE(addCmd, []string{"pacing"})
	if err == nil {
		t.Fatal("expected an error — adding a 0.2-weight dimension on top of 0.6+0.4 sums to 1.2")
	}
	if !strings.Contains(err.Error(), "1.0") {
		t.Errorf("error should explain the weight-sum requirement, got: %v", err)
	}
}

func testConfigDimensionAddSucceeds(
	t *testing.T, ctx context.Context, st *sqlite.SQLiteStore, app *App, reload func(),
) {
	// Single-field set/add each validate the *whole* weight sum immediately,
	// so there's no achievable sequence of single-dimension edits that
	// rebalances two existing dimensions to make room for a third — every
	// intermediate state must independently sum to 1.0. Re-seed directly
	// with headroom (story=0.5, fun=0.3, sum=0.8) to test the success path,
	// rather than trying to reach it via the CLI.
	headroom := &config.Config{
		DimensionOrder: []string{"fun", "story"},
		Dimensions: map[string]config.DimensionDef{
			"story": {Label: "Story", Description: "Narrative quality", Weight: 0.5},
			"fun":   {Label: "Fun", Description: "Enjoyment", Weight: 0.3},
		},
		Genres:             map[string]map[string]float64{},
		PrimaryGenreWeight: config.DefaultPrimaryGenreWeight,
		MaxMultiplier:      config.DefaultMaxMultiplier,
	}
	if err := st.SaveScoringConfig(ctx, headroom); err != nil {
		t.Fatalf("re-seeding config with headroom: %v", err)
	}
	reload()

	addCmd := app.configDimensionAddCmd()
	addCmd.SetContext(ctx)
	mustSetFlag(t, addCmd, "label", "Pacing")
	mustSetFlag(t, addCmd, "weight", "0.2")
	if err := addCmd.RunE(addCmd, []string{"pacing"}); err != nil {
		t.Fatalf("config dimension add: %v", err)
	}
	reload()

	if _, ok := app.Config.Dimensions["pacing"]; !ok {
		t.Fatal("pacing dimension was not persisted")
	}
}

func testConfigDimensionList(t *testing.T, ctx context.Context, app *App) {
	listCmd := app.configDimensionListCmd()
	listCmd.SetContext(ctx)
	out := captureStdout(t, func() {
		if err := listCmd.RunE(listCmd, nil); err != nil {
			t.Fatalf("config dimension list: %v", err)
		}
	})
	if !strings.Contains(out, "pacing") {
		t.Errorf("list output missing new dimension, got: %q", out)
	}
}

func testConfigGenreSetThenRemove(t *testing.T, ctx context.Context, app *App, reload func()) {
	setCmd := app.configGenreSetCmd()
	setCmd.SetContext(ctx)
	if err := setCmd.RunE(setCmd, []string{"Action", "pacing", "1.3"}); err != nil {
		t.Fatalf("config genre set: %v", err)
	}
	reload()
	if app.Config.Genres["action"]["pacing"] != 1.3 {
		t.Fatalf("genre multiplier not persisted: got %+v", app.Config.Genres)
	}

	removeCmd := app.configGenreRemoveCmd()
	removeCmd.SetContext(ctx)
	if err := removeCmd.RunE(removeCmd, []string{"Action", "pacing"}); err != nil {
		t.Fatalf("config genre remove: %v", err)
	}
	reload()
	if _, ok := app.Config.Genres["action"]; ok {
		t.Errorf(
			"genre should be fully removed once its only multiplier is gone: %+v",
			app.Config.Genres,
		)
	}
}

func testConfigGenreSetRejectsUnknownDimension(t *testing.T, ctx context.Context, app *App) {
	setCmd := app.configGenreSetCmd()
	setCmd.SetContext(ctx)
	if err := setCmd.RunE(setCmd, []string{"Action", "nonexistent", "1.3"}); err == nil {
		t.Error("expected an error for an unknown dimension")
	}
}

func testConfigGenreSetRejectsExcessiveMultiplier(t *testing.T, ctx context.Context, app *App) {
	setCmd := app.configGenreSetCmd()
	setCmd.SetContext(ctx)
	if err := setCmd.RunE(setCmd, []string{"Action", "pacing", "999"}); err == nil {
		t.Error("expected an error for a multiplier exceeding max_multiplier")
	}
}

func testConfigDimensionRemoveRebalances(
	t *testing.T,
	ctx context.Context,
	app *App,
	reload func(),
) {
	setCmd := app.configGenreSetCmd()
	setCmd.SetContext(ctx)
	if err := setCmd.RunE(setCmd, []string{"Drama", "pacing", "1.1"}); err != nil {
		t.Fatalf("config genre set: %v", err)
	}
	reload()

	// Entering this test: story=0.5, fun=0.3, pacing=0.2 (sum=1.0). Removing
	// pacing should redistribute its 0.2 proportionally over story:fun's 5:3
	// ratio: story += 0.2*(0.5/0.8) = 0.625, fun += 0.2*(0.3/0.8) = 0.375.
	removeCmd := app.configDimensionRemoveCmd()
	removeCmd.SetContext(ctx)
	if err := removeCmd.RunE(removeCmd, []string{"pacing"}); err != nil {
		t.Fatalf("config dimension remove: %v", err)
	}
	reload()

	if _, ok := app.Config.Dimensions["pacing"]; ok {
		t.Error("pacing dimension should have been removed")
	}
	const tolerance = 1e-9
	if got := app.Config.Dimensions["story"].Weight; math.Abs(got-0.625) > tolerance {
		t.Errorf("story weight: got %.6f, want 0.625 (proportional rebalance)", got)
	}
	if got := app.Config.Dimensions["fun"].Weight; math.Abs(got-0.375) > tolerance {
		t.Errorf("fun weight: got %.6f, want 0.375 (proportional rebalance)", got)
	}
	sum := 0.0
	for _, d := range app.Config.Dimensions {
		sum += d.Weight
	}
	if math.Abs(sum-1.0) > tolerance {
		t.Errorf("weights sum to %.6f after removal, want 1.0", sum)
	}
	if _, ok := app.Config.Genres["drama"]; ok {
		t.Errorf(
			"drama genre multiplier referencing removed dimension should be gone too: %+v",
			app.Config.Genres,
		)
	}
}

func testConfigDimensionRemoveFloor(t *testing.T, ctx context.Context, app *App, reload func()) {
	// Entering this test: story=0.625, fun=0.375 (2 dimensions).
	removeCmd := app.configDimensionRemoveCmd()
	removeCmd.SetContext(ctx)
	if err := removeCmd.RunE(removeCmd, []string{"fun"}); err != nil {
		t.Fatalf("removing fun (2 -> 1 dimensions) should succeed: %v", err)
	}
	reload()
	if len(app.Config.Dimensions) != 1 {
		t.Fatalf(
			"expected exactly 1 dimension remaining, got %d: %+v",
			len(app.Config.Dimensions),
			app.Config.Dimensions,
		)
	}

	var lastKey string
	for k := range app.Config.Dimensions {
		lastKey = k
	}
	removeCmd2 := app.configDimensionRemoveCmd()
	removeCmd2.SetContext(ctx)
	err := removeCmd2.RunE(removeCmd2, []string{lastKey})
	if err == nil {
		t.Fatal("expected an error removing the last remaining dimension")
	}
	if !strings.Contains(err.Error(), "at least") {
		t.Errorf("error should explain the floor, got: %v", err)
	}
}

// mustSetFlag sets a cobra flag or fails the test immediately.
func mustSetFlag(t *testing.T, cmd *cobra.Command, name, value string) {
	t.Helper()
	if err := cmd.Flags().Set(name, value); err != nil {
		t.Fatalf("setting --%s: %v", name, err)
	}
}

// TestConfigCmd_DBless_EndToEnd exercises config show/import/export, which
// must work without a database, using the real config package (no fakes).
func TestConfigCmd_DBless_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	activePath := filepath.Join(dir, "active.toml")
	cfg := seedTestConfig()
	if err := config.Write(activePath, cfg); err != nil {
		t.Fatalf("seeding active config file: %v", err)
	}
	loaded, err := config.Load(activePath)
	if err != nil {
		t.Fatalf("loading seeded config: %v", err)
	}

	app := &App{Store: nil, Config: loaded, ConfigPath: activePath}

	t.Run("show", func(t *testing.T) { testConfigShow(t, app) })

	t.Run("export then import round-trips", func(t *testing.T) {
		exportPath := filepath.Join(dir, "exported.toml")
		exportCmd := app.configExportCmd()
		if err := exportCmd.Flags().Set("file", exportPath); err != nil {
			t.Fatal(err)
		}
		if err := exportCmd.RunE(exportCmd, nil); err != nil {
			t.Fatalf("config export: %v", err)
		}
		if _, err := os.Stat(exportPath); err != nil {
			t.Fatalf("exported file missing: %v", err)
		}

		importCmd := app.configImportCmd()
		if err := importCmd.Flags().Set("file", exportPath); err != nil {
			t.Fatal(err)
		}
		if err := importCmd.RunE(importCmd, nil); err != nil {
			t.Fatalf("config import: %v", err)
		}

		reimported, err := config.Load(app.ConfigPath)
		if err != nil {
			t.Fatalf("reloading active config: %v", err)
		}
		if reimported.Dimensions["story"].Weight != 0.6 {
			t.Errorf(
				"round-trip lost data: got weight %v, want 0.6",
				reimported.Dimensions["story"].Weight,
			)
		}
	})

	t.Run("import rejects a missing file", func(t *testing.T) {
		importCmd := app.configImportCmd()
		if err := importCmd.Flags().
			Set("file", filepath.Join(dir, "does-not-exist.toml")); err != nil {
			t.Fatal(err)
		}
		if err := importCmd.RunE(importCmd, nil); err == nil {
			t.Error("expected an error importing a nonexistent file")
		}
	})

	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"dimension list", func() error {
			c := app.configDimensionListCmd()
			return c.RunE(c, nil)
		}},
		{"dimension add", func() error {
			c := app.configDimensionAddCmd()
			return c.RunE(c, []string{"pacing"})
		}},
		{"genre set", func() error {
			c := app.configGenreSetCmd()
			return c.RunE(c, []string{"Action", "story", "1.1"})
		}},
	} {
		t.Run(tc.name+" requires a database", func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("expected an error in DBless mode")
			}
			if !strings.Contains(err.Error(), "KANSOU_DB_TYPE") {
				t.Errorf("error message should mention KANSOU_DB_TYPE, got: %v", err)
			}
		})
	}
}
