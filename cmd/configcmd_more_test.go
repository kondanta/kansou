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
)

// newConfigApp returns an App backed by a real SQLite store seeded with
// seedTestConfig (story=0.6, fun=0.4), plus a reload helper.
func newConfigApp(t *testing.T) (*App, context.Context, func()) {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.New(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := seedTestConfig()
	if err := st.SaveScoringConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	app := &App{Store: st, Config: cfg}
	reload := func() {
		c, err := st.LoadScoringConfig(ctx)
		if err != nil {
			t.Fatal(err)
		}
		app.Config = c
	}
	return app, ctx, reload
}

func TestConfigTree_Wiring(t *testing.T) {
	app := &App{}
	root := app.configCmd()

	paths := [][]string{
		{"show"},
		{"import"},
		{"export"},
		{"dimension", "list"},
		{"dimension", "add"},
		{"dimension", "set"},
		{"dimension", "remove"},
		{"genre", "set"},
		{"genre", "remove"},
	}
	for _, p := range paths {
		c, _, err := root.Find(p)
		if err != nil || c.Name() != p[len(p)-1] {
			t.Errorf("config %v not registered: %v", p, err)
		}
		if c != nil && (c.Short == "" || c.RunE == nil) {
			t.Errorf("config %v lacks Short or RunE", p)
		}
	}
	for _, group := range []string{"config", "dimension", "genre"} {
		c := root
		if group != "config" {
			c, _, _ = root.Find([]string{group})
		}
		if c.Short == "" || c.Long == "" {
			t.Errorf("%s needs Short and Long", group)
		}
	}

	set, _, _ := root.Find([]string{"dimension", "set"})
	add, _, _ := root.Find([]string{"dimension", "add"})
	for _, name := range []string{"label", "description", "weight", "bias-resistant"} {
		if set.Flags().Lookup(name) == nil || add.Flags().Lookup(name) == nil {
			t.Errorf("--%s missing on dimension add/set", name)
		}
	}
}

func TestConfigDimensionSet(t *testing.T) {
	// Setting a single weight would unbalance the sum, so weight cases change
	// both labels and a weight that keeps the total at 1.0 only where valid.
	tests := []struct {
		name    string
		key     string
		flags   map[string]string
		wantErr string
		check   func(t *testing.T, c *config.Config)
	}{
		{
			name:  "label and description only keep the weight",
			key:   "story",
			flags: map[string]string{"label": "Plot", "description": "How it unfolds"},
			check: func(t *testing.T, c *config.Config) {
				d := c.Dimensions["story"]
				if d.Label != "Plot" || d.Description != "How it unfolds" || d.Weight != 0.6 {
					t.Errorf("got %+v", d)
				}
			},
		},
		{
			name:  "bias-resistant toggles on",
			key:   "fun",
			flags: map[string]string{"bias-resistant": "true"},
			check: func(t *testing.T, c *config.Config) {
				if !c.Dimensions["fun"].BiasResistant {
					t.Error("bias-resistant not persisted")
				}
				if c.Dimensions["fun"].Label != "Fun" {
					t.Error("unspecified fields must be preserved")
				}
			},
		},
		{
			name:    "weight that breaks the sum is rejected",
			key:     "story",
			flags:   map[string]string{"weight": "0.7"},
			wantErr: "1.0",
		},
		{
			name:    "zero weight rejected",
			key:     "story",
			flags:   map[string]string{"weight": "0"},
			wantErr: "weight must be > 0",
		},
		{
			name:    "negative weight rejected",
			key:     "story",
			flags:   map[string]string{"weight": "-1"},
			wantErr: "weight must be > 0",
		},
		{
			name:    "unknown dimension",
			key:     "nope",
			flags:   map[string]string{"label": "x"},
			wantErr: "does not exist",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, ctx, reload := newConfigApp(t)
			cmd := app.configDimensionSetCmd()
			cmd.SetContext(ctx)
			for k, v := range tt.flags {
				mustSetFlag(t, cmd, k, v)
			}
			var err error
			out := captureStdout(t, func() { err = cmd.RunE(cmd, []string{tt.key}) })
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				reload()
				if app.Config.Dimensions["story"].Weight != 0.6 {
					t.Error("a rejected set must not change the stored config")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "updated") {
				t.Errorf("stdout = %q", out)
			}
			reload()
			tt.check(t, app.Config)
		})
	}
}

func TestConfigDimensionSet_WeightRebalance(t *testing.T) {
	// A weight change that keeps the sum valid: needs headroom, so re-seed.
	app, ctx, reload := newConfigApp(t)
	cfg := seedTestConfig()
	d := cfg.Dimensions["story"]
	d.Weight = 0.5
	cfg.Dimensions["story"] = d
	f := cfg.Dimensions["fun"]
	f.Weight = 0.5
	cfg.Dimensions["fun"] = f
	if err := app.Store.SaveScoringConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	reload()

	// Both at 0.5; moving story to 0.6 alone breaks the sum.
	cmd := app.configDimensionSetCmd()
	cmd.SetContext(ctx)
	mustSetFlag(t, cmd, "weight", "0.6")
	if err := cmd.RunE(cmd, []string{"story"}); err == nil {
		t.Fatal("expected sum validation error")
	}
}

func TestConfigDimensionRemove_WarnsAboutScoredHistory(t *testing.T) {
	app, ctx, reload := newConfigApp(t)
	st := app.Store.(*sqlite.SQLiteStore)

	// seedOneScore uses its own two dimensions: story + characters.
	seed := &config.Config{
		DimensionOrder: []string{"story", "characters"},
		Dimensions: map[string]config.DimensionDef{
			"story":      {Label: "Story", Weight: 0.6},
			"characters": {Label: "Characters", Weight: 0.4},
		},
		Genres:             map[string]map[string]float64{},
		PrimaryGenreWeight: config.DefaultPrimaryGenreWeight,
		MaxMultiplier:      config.DefaultMaxMultiplier,
	}
	if err := st.SaveScoringConfig(ctx, seed); err != nil {
		t.Fatal(err)
	}
	reload()
	seedOneScore(t, ctx, st)

	rm := app.configDimensionRemoveCmd()
	rm.SetContext(ctx)
	var err error
	out := captureStdout(t, func() { err = rm.RunE(rm, []string{"characters"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `warning: dimension "characters" has 1 scored entries`) {
		t.Errorf("expected a history warning, got %q", out)
	}
	reload()
	if got := app.Config.Dimensions["story"].Weight; math.Abs(got-1.0) > 1e-9 {
		t.Errorf("story should absorb the whole weight, got %v", got)
	}

	// Unknown dimension.
	rm2 := app.configDimensionRemoveCmd()
	rm2.SetContext(ctx)
	if err := rm2.RunE(
		rm2,
		[]string{"ghost"},
	); err == nil ||
		!strings.Contains(err.Error(), "does not exist") {
		t.Errorf("err = %v", err)
	}
}

func TestConfigDimensionAdd_Validation(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		flags   map[string]string
		wantErr string
	}{
		{
			"duplicate key",
			"story",
			map[string]string{"label": "S", "weight": "0.1"},
			"already exists",
		},
		{"missing weight", "x", map[string]string{"label": "X"}, "weight must be > 0"},
		{"missing label", "x", map[string]string{"weight": "0.1"}, "--label is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, ctx, _ := newConfigApp(t)
			cmd := app.configDimensionAddCmd()
			cmd.SetContext(ctx)
			for k, v := range tt.flags {
				mustSetFlag(t, cmd, k, v)
			}
			err := cmd.RunE(cmd, []string{tt.key})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestConfigGenre_ErrorsAndNormalisation(t *testing.T) {
	app, ctx, reload := newConfigApp(t)

	set := func(args ...string) error {
		c := app.configGenreSetCmd()
		c.SetContext(ctx)
		return c.RunE(c, args)
	}
	remove := func(args ...string) error {
		c := app.configGenreRemoveCmd()
		c.SetContext(ctx)
		return c.RunE(c, args)
	}

	if err := set(
		"Action",
		"story",
		"abc",
	); err == nil ||
		!strings.Contains(err.Error(), "invalid multiplier") {
		t.Errorf("non-numeric multiplier: %v", err)
	}
	if err := set("Action", "story", "0"); err == nil {
		t.Error("zero multiplier must be rejected")
	}
	if err := set("Sci-Fi", "story", "1.2"); err != nil {
		t.Fatal(err)
	}
	reload() // each CLI invocation starts from the stored config
	if err := set("Sci-Fi", "fun", "0.8"); err != nil {
		t.Fatal(err)
	}
	reload()
	if got := app.Config.Genres["sci-fi"]; got["story"] != 1.2 || got["fun"] != 0.8 {
		t.Fatalf("genre key must be lowercased and merged: %+v", app.Config.Genres)
	}

	if err := remove(
		"ghost",
	); err == nil ||
		!strings.Contains(err.Error(), "no configured multipliers") {
		t.Errorf("unknown genre: %v", err)
	}
	if err := remove(
		"sci-fi",
		"ghost",
	); err == nil ||
		!strings.Contains(err.Error(), "no multiplier configured") {
		t.Errorf("unknown dimension on genre: %v", err)
	}
	if err := remove("SCI-FI", "story"); err != nil {
		t.Fatal(err)
	}
	reload()
	if _, ok := app.Config.Genres["sci-fi"]["story"]; ok ||
		app.Config.Genres["sci-fi"]["fun"] != 0.8 {
		t.Errorf("only story should be removed: %+v", app.Config.Genres)
	}
	if err := remove("sci-fi"); err != nil {
		t.Fatal(err)
	}
	reload()
	if len(app.Config.Genres) != 0 {
		t.Errorf("genre should be fully removed: %+v", app.Config.Genres)
	}
}

func TestPrintConfig_FullOutput(t *testing.T) {
	cfg := seedTestConfig()
	fun := cfg.Dimensions["fun"]
	fun.BiasResistant = true
	cfg.Dimensions["fun"] = fun
	cfg.Genres = map[string]map[string]float64{
		"romance": {"story": 1.1, "fun": 0.9},
		"action":  {"fun": 1.3},
	}
	cfg.PrimaryGenreWeight = 0.7
	cfg.MaxMultiplier = 1.5
	cfg.MaxHistory = 4

	out := captureStdout(t, func() { printConfig(cfg) })

	// DimensionOrder is fun, story; genres and their dimensions are sorted.
	want := []string{
		"fun             Fun                       weight=0.4000 [bias-resistant]",
		"story           Story                     weight=0.6000",
		"action          fun=1.30",
		"romance         fun=0.90, story=1.10",
		"primary_genre_weight: 0.70",
		"max_multiplier:       1.50",
		"max_history:          4",
	}
	last := -1
	for _, w := range want {
		i := strings.Index(out, w)
		if i < 0 {
			t.Fatalf("missing %q in:\n%s", w, out)
		}
		if i < last {
			t.Fatalf("%q out of order in:\n%s", w, out)
		}
		last = i
	}

	// With no genres the section is omitted entirely.
	cfg.Genres = nil
	if out := captureStdout(
		t,
		func() { printConfig(cfg) },
	); strings.Contains(
		out,
		"Genre multipliers",
	) {
		t.Errorf("empty genres should hide the section:\n%s", out)
	}
}

func TestRunConfigImport_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	bad := filepath.Join(dir, "bad.toml")
	writeFile(t, bad, "not = = toml")
	app := &App{ConfigPath: filepath.Join(dir, "active.toml")}
	if err := runConfigImport(
		ctx,
		app,
		bad,
	); err == nil ||
		!strings.Contains(err.Error(), "loading") {
		t.Errorf("invalid TOML: %v", err)
	}

	// An unwritable active-config location surfaces a write error.
	good := writeConfig(t, 0)
	app.ConfigPath = filepath.Join(dir, "no-such-dir", "active.toml")
	if err := runConfigImport(
		ctx,
		app,
		good,
	); err == nil ||
		!strings.Contains(err.Error(), "writing config file") {
		t.Errorf("unwritable target: %v", err)
	}

	// DB mode saves to the store instead of the file. Seed the database with a
	// story weight that differs from the import file's 0.6, so a skipped write
	// is detected.
	dbApp, dbCtx, reload := newConfigApp(t)
	seeded := seedTestConfig()
	for k, w := range map[string]float64{"story": 0.3, "fun": 0.7} {
		d := seeded.Dimensions[k]
		d.Weight = w
		seeded.Dimensions[k] = d
	}
	if err := dbApp.Store.SaveScoringConfig(dbCtx, seeded); err != nil {
		t.Fatal(err)
	}
	reload()
	if dbApp.Config.Dimensions["story"].Weight != 0.3 {
		t.Fatal("precondition: database should start with story=0.3")
	}
	if err := runConfigImport(ctx, dbApp, good); err != nil {
		t.Fatal(err)
	}
	reload()
	if got := dbApp.Config.Dimensions["story"].Weight; got != 0.6 {
		t.Errorf("imported config not stored in the database: story weight = %v, want 0.6", got)
	}
}

func TestConfigExport_UnwritableTarget(t *testing.T) {
	app := &App{Config: seedTestConfig()}
	c := app.configExportCmd()
	mustSetFlag(t, c, "file", filepath.Join(t.TempDir(), "missing", "out.toml"))
	if err := c.RunE(c, nil); err == nil || !strings.Contains(err.Error(), "writing") {
		t.Errorf("err = %v", err)
	}
}

// writeFile writes content to path or fails the test.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
