package config

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// writeTOML writes content to a temp file and returns its path.
func writeTOML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "kansou-*.toml")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	_ = f.Close()
	return f.Name()
}

func TestLoad_Defaults_WhenNoFile(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Dimensions) != 7 {
		t.Errorf("expected 7 default dimensions, got %d", len(cfg.Dimensions))
	}
	if cfg.Server.Port != DefaultPort {
		t.Errorf("expected default port %d, got %d", DefaultPort, cfg.Server.Port)
	}
}

func TestLoad_DefaultWeightsSumToOne(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sum := 0.0
	for _, d := range cfg.Dimensions {
		sum += d.Weight
	}
	if math.Abs(sum-1.0) > weightSumTolerance {
		t.Errorf("default weights sum to %v, expected 1.0", sum)
	}
}

func TestLoad_ValidConfig(t *testing.T) {
	content := `
[dimensions.story]
label = "Story"
description = "Narrative"
weight = 0.60
bias_resistant = false

[dimensions.enjoyment]
label = "Enjoyment"
description = "Fun"
weight = 0.40
bias_resistant = true
`
	path := writeTOML(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Dimensions) != 2 {
		t.Errorf("expected 2 dimensions, got %d", len(cfg.Dimensions))
	}
	if !cfg.Dimensions["enjoyment"].BiasResistant {
		t.Error("expected enjoyment to be bias_resistant")
	}
}

func TestLoad_WeightsMustSumToOne(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr bool
	}{
		{
			name: "valid: exact 1.0",
			content: `
[dimensions.a]
label = "A"
weight = 0.5
[dimensions.b]
label = "B"
weight = 0.5
`,
			wantErr: false,
		},
		{
			name: "valid: within tolerance",
			content: `
[dimensions.a]
label = "A"
weight = 0.5005
[dimensions.b]
label = "B"
weight = 0.5
`,
			wantErr: false,
		},
		{
			name: "invalid: sum too high",
			content: `
[dimensions.a]
label = "A"
weight = 0.6
[dimensions.b]
label = "B"
weight = 0.6
`,
			wantErr: true,
		},
		{
			name: "invalid: sum too low",
			content: `
[dimensions.a]
label = "A"
weight = 0.3
[dimensions.b]
label = "B"
weight = 0.3
`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTOML(t, tc.content)
			_, err := Load(path)
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoad_GenreKeysMustExistInDimensions(t *testing.T) {
	content := `
[dimensions.story]
label = "Story"
weight = 1.0

[genres.action]
story = 1.2
unknown_dim = 0.8
`
	path := writeTOML(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for genre referencing unknown dimension")
	}
}

func TestLoad_PortValidation(t *testing.T) {
	cases := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{"valid: 8080", 8080, false},
		{"valid: 1024", 1024, false},
		{"valid: 65535", 65535, false},
		{"invalid: 80 (privileged)", 80, true},
		{"invalid: 0", 0, false}, // 0 defaults to 8080, which is valid
		{"invalid: 70000", 70000, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := "[dimensions.story]\nlabel = \"Story\"\nweight = 1.0\n"
			if tc.port != 0 {
				content += "[server]\nport = " + itoa(tc.port) + "\n"
			}
			path := writeTOML(t, content)
			_, err := Load(path)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for port %d", tc.port)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for port %d: %v", tc.port, err)
			}
		})
	}
}

func TestLoad_GenreKeysLowercased(t *testing.T) {
	content := `
[dimensions.story]
label = "Story"
weight = 1.0

[genres.Action]
story = 1.2

[genres.DRAMA]
story = 1.3
`
	path := writeTOML(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := cfg.Genres["action"]; !ok {
		t.Error("expected genre key 'action' after lowercasing 'Action'")
	}
	if _, ok := cfg.Genres["drama"]; !ok {
		t.Error("expected genre key 'drama' after lowercasing 'DRAMA'")
	}
	if _, ok := cfg.Genres["Action"]; ok {
		t.Error("original-case key 'Action' should not be present after lowercasing")
	}
}

func TestLoad_DimensionsHashIsStable(t *testing.T) {
	// Same config loaded twice must produce the same hash.
	content := `
[dimensions.story]
label = "Story"
weight = 0.6
[dimensions.enjoyment]
label = "Enjoyment"
weight = 0.4
`
	path := writeTOML(t, content)
	cfg1, err := Load(path)
	if err != nil {
		t.Fatalf("first load error: %v", err)
	}
	cfg2, err := Load(path)
	if err != nil {
		t.Fatalf("second load error: %v", err)
	}
	if cfg1.DimensionsHash != cfg2.DimensionsHash {
		t.Errorf("hash not stable: %q vs %q", cfg1.DimensionsHash, cfg2.DimensionsHash)
	}
}

func TestLoad_DimensionsHashChangesOnWeightChange(t *testing.T) {
	content1 := "[dimensions.story]\nlabel = \"S\"\nweight = 0.6\n[dimensions.e]\nlabel = \"E\"\nweight = 0.4\n"
	content2 := "[dimensions.story]\nlabel = \"S\"\nweight = 0.7\n[dimensions.e]\nlabel = \"E\"\nweight = 0.3\n"

	cfg1, _ := Load(writeTOML(t, content1))
	cfg2, _ := Load(writeTOML(t, content2))

	if cfg1.DimensionsHash == cfg2.DimensionsHash {
		t.Error("hash should differ when weights change")
	}
}

func TestLoad_DefaultConfig_HasDimensionsHash(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DimensionsHash == "" {
		t.Error("expected non-empty DimensionsHash for default config")
	}
}

func TestLoad_MaxMultiplier_Default(t *testing.T) {
	// No max_multiplier in config — should default to 2.0.
	content := "[dimensions.story]\nlabel = \"S\"\nweight = 1.0\n"
	cfg, err := Load(writeTOML(t, content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxMultiplier != DefaultMaxMultiplier {
		t.Errorf("expected default max_multiplier %.1f, got %.1f", DefaultMaxMultiplier, cfg.MaxMultiplier)
	}
}

func TestLoad_MaxMultiplier_Configurable(t *testing.T) {
	content := "max_multiplier = 3.0\n[dimensions.story]\nlabel = \"S\"\nweight = 1.0\n[genres.action]\nstory = 2.5\n"
	cfg, err := Load(writeTOML(t, content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxMultiplier != 3.0 {
		t.Errorf("expected max_multiplier 3.0, got %.1f", cfg.MaxMultiplier)
	}
}

func TestLoad_MultiplierValidation(t *testing.T) {
	base := "[dimensions.story]\nlabel = \"S\"\nweight = 1.0\n"
	cases := []struct {
		name    string
		content string
		wantErr bool
	}{
		{
			name:    "valid: within default ceiling",
			content: base + "[genres.action]\nstory = 1.5\n",
			wantErr: false,
		},
		{
			name:    "valid: at default ceiling",
			content: base + "[genres.action]\nstory = 2.0\n",
			wantErr: false,
		},
		{
			name:    "invalid: exceeds default ceiling",
			content: base + "[genres.action]\nstory = 2.1\n",
			wantErr: true,
		},
		{
			name:    "invalid: zero multiplier",
			content: base + "[genres.action]\nstory = 0.0\n",
			wantErr: true,
		},
		{
			name:    "invalid: negative multiplier",
			content: base + "[genres.action]\nstory = -0.5\n",
			wantErr: true,
		},
		{
			name:    "valid: raised ceiling allows higher value",
			content: "max_multiplier = 3.0\n" + base + "[genres.action]\nstory = 2.5\n",
			wantErr: false,
		},
		{
			name:    "invalid: exceeds raised ceiling",
			content: "max_multiplier = 3.0\n" + base + "[genres.action]\nstory = 3.1\n",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeTOML(t, tc.content))
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoad_DefaultConfig_MaxMultiplier(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxMultiplier != DefaultMaxMultiplier {
		t.Errorf("default config: expected max_multiplier %.1f, got %.1f", DefaultMaxMultiplier, cfg.MaxMultiplier)
	}
}

func TestWrite_RoundTrip(t *testing.T) {
	content := `
[dimensions.story]
label = "Story"
description = "Narrative quality"
weight = 0.60
bias_resistant = false

[dimensions.enjoyment]
label = "Enjoyment"
description = "Fun factor"
weight = 0.40
bias_resistant = true

[genres.action]
story = 1.2

[server]
port = 9090
`
	path := writeTOML(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if err := Write(path, cfg); err != nil {
		t.Fatalf("write: %v", err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if len(reloaded.Dimensions) != len(cfg.Dimensions) {
		t.Errorf("dimensions: got %d, want %d", len(reloaded.Dimensions), len(cfg.Dimensions))
	}
	if reloaded.Dimensions["story"].Label != "Story" {
		t.Errorf("story label: got %q, want %q", reloaded.Dimensions["story"].Label, "Story")
	}
	if !reloaded.Dimensions["enjoyment"].BiasResistant {
		t.Error("enjoyment: expected bias_resistant=true after round-trip")
	}
	if _, ok := reloaded.Genres["action"]; !ok {
		t.Error("genre 'action' missing after round-trip")
	}
	if reloaded.Server.Port != 9090 {
		t.Errorf("server port: got %d, want 9090", reloaded.Server.Port)
	}
}

func TestHash_IsStable(t *testing.T) {
	content := `
[dimensions.story]
label = "Story"
description = "Narrative"
weight = 0.60
[dimensions.enjoyment]
label = "Enjoyment"
description = "Fun"
weight = 0.40
[genres.action]
story = 1.2
`
	cfg, err := Load(writeTOML(t, content))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	h1 := Hash(cfg)
	h2 := Hash(cfg)
	if h1 != h2 {
		t.Error("Hash is not deterministic: same *Config produced different hashes on successive calls")
	}
}

func TestHash_ChangesOnMutation(t *testing.T) {
	base := `
[dimensions.story]
label = "Story"
description = "Narrative"
weight = 0.60
[dimensions.enjoyment]
label = "Enjoyment"
description = "Fun"
weight = 0.40
`
	cases := []struct {
		name    string
		mutated string
	}{
		{
			name: "weight change",
			mutated: `
[dimensions.story]
label = "Story"
description = "Narrative"
weight = 0.70
[dimensions.enjoyment]
label = "Enjoyment"
description = "Fun"
weight = 0.30
`,
		},
		{
			name: "label change",
			mutated: `
[dimensions.story]
label = "Story (changed)"
description = "Narrative"
weight = 0.60
[dimensions.enjoyment]
label = "Enjoyment"
description = "Fun"
weight = 0.40
`,
		},
		{
			name:    "primary_genre_weight change",
			mutated: "primary_genre_weight = 0.7\n" + base,
		},
		{
			name:    "max_multiplier change",
			mutated: "max_multiplier = 3.0\n" + base,
		},
		{
			name:    "genre added",
			mutated: base + "[genres.action]\nstory = 1.2\n",
		},
	}

	baseCfg, err := Load(writeTOML(t, base))
	if err != nil {
		t.Fatalf("load base: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutated, err := Load(writeTOML(t, tc.mutated))
			if err != nil {
				t.Fatalf("load mutated: %v", err)
			}
			if Hash(baseCfg) == Hash(mutated) {
				t.Error("expected hash to differ after mutation")
			}
		})
	}
}

func TestRebuild_Valid(t *testing.T) {
	base := `
[dimensions.story]
label = "Story"
description = "Narrative"
weight = 0.60
[dimensions.enjoyment]
label = "Enjoyment"
description = "Fun"
weight = 0.40
[server]
port = 9090
`
	cfg, err := Load(writeTOML(t, base))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	dims := map[string]DimensionDef{
		"story":     {Label: "Story (updated)", Description: "Narrative", Weight: 0.70},
		"enjoyment": {Label: "Enjoyment", Description: "Fun", Weight: 0.30, BiasResistant: true},
	}
	rebuilt, err := Rebuild(cfg, dims, nil, DefaultPrimaryGenreWeight, DefaultMaxMultiplier)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if rebuilt.Dimensions["story"].Label != "Story (updated)" {
		t.Errorf("label: got %q, want %q", rebuilt.Dimensions["story"].Label, "Story (updated)")
	}
	if rebuilt.Dimensions["story"].Weight != 0.70 {
		t.Errorf("weight: got %.2f, want 0.70", rebuilt.Dimensions["story"].Weight)
	}
	// Server config must be preserved from base.
	if rebuilt.Server.Port != 9090 {
		t.Errorf("server port: got %d, want 9090 (should be preserved from base)", rebuilt.Server.Port)
	}
}

func TestRebuild_InvalidWeights(t *testing.T) {
	cfg, err := Load(writeTOML(t, "[dimensions.story]\nlabel=\"S\"\nweight=1.0\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	dims := map[string]DimensionDef{
		"story":     {Label: "S", Weight: 0.60},
		"enjoyment": {Label: "E", Weight: 0.60}, // sum = 1.20, invalid
	}
	_, err = Rebuild(cfg, dims, nil, DefaultPrimaryGenreWeight, DefaultMaxMultiplier)
	if err == nil {
		t.Error("expected error for weights summing to 1.20, got nil")
	}
}

func TestRebuild_UnknownGenreDimension(t *testing.T) {
	cfg, err := Load(writeTOML(t, "[dimensions.story]\nlabel=\"S\"\nweight=1.0\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	dims := map[string]DimensionDef{
		"story": {Label: "S", Weight: 1.0},
	}
	genres := map[string]map[string]float64{
		"action": {"nonexistent_dim": 1.2},
	}
	_, err = Rebuild(cfg, dims, genres, DefaultPrimaryGenreWeight, DefaultMaxMultiplier)
	if err == nil {
		t.Error("expected error for genre referencing unknown dimension, got nil")
	}
}

func TestProbeWritable_WritableDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := ProbeWritable(path); err != nil {
		t.Errorf("expected writable dir to succeed, got: %v", err)
	}
}

func TestProbeWritable_ReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0444); err != nil {
		t.Skipf("cannot set dir read-only: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0755) }) //nolint:errcheck
	path := filepath.Join(dir, "config.toml")
	if err := ProbeWritable(path); err == nil {
		t.Error("expected error for read-only dir, got nil")
	}
}

// itoa converts an int to its decimal string representation without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
