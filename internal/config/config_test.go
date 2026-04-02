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
	f.Close()
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
