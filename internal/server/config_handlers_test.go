package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kondanta/kansou/internal/anilist"
	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
)

// minimalConfig returns a valid *config.Config with two dimensions summing to 1.0.
func minimalConfig() *config.Config {
	dims := map[string]config.DimensionDef{
		"story": {Label: "Story", Description: "Narrative quality", Weight: 0.60},
		"fun":   {Label: "Fun", Description: "Enjoyment", Weight: 0.40},
	}
	order := []string{"fun", "story"}
	return &config.Config{
		DimensionOrder:     order,
		Dimensions:         dims,
		Genres:             map[string]map[string]float64{},
		PrimaryGenreWeight: config.DefaultPrimaryGenreWeight,
		MaxMultiplier:      config.DefaultMaxMultiplier,
		Server:             config.ServerConfig{Port: config.DefaultPort},
	}
}

// minimalEngine builds a scoring.Engine from minimalConfig.
func minimalEngine(cfg *config.Config) *scoring.Engine {
	defs := make(map[string]scoring.DimensionDef, len(cfg.Dimensions))
	for k, d := range cfg.Dimensions {
		defs[k] = scoring.DimensionDef{
			Label:         d.Label,
			Description:   d.Description,
			Weight:        d.Weight,
			BiasResistant: d.BiasResistant,
		}
	}
	return scoring.NewEngine(cfg.DimensionOrder, defs, cfg.Genres, cfg.PrimaryGenreWeight)
}

// newTestServer builds a Server suitable for handler tests.
// configPath should point to a writable TOML file when liveConfig is true.
func newTestServer(cfg *config.Config, liveConfig bool, configPath string) *Server {
	al := anilist.NewClient()
	eng := minimalEngine(cfg)
	return New(cfg, al, eng, liveConfig, configPath, nil, "", nil)
}

// writeConfigFile writes a minimal valid TOML to a temp file and returns its path.
func writeConfigFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[dimensions.story]
label = "Story"
description = "Narrative quality"
weight = 0.60

[dimensions.fun]
label = "Fun"
description = "Enjoyment"
weight = 0.40
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}
	return path
}

func TestHandleGetConfig_ReturnsPayload(t *testing.T) {
	cfg := minimalConfig()
	srv := newTestServer(cfg, true, "")

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload configPayload
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if len(payload.Dimensions) != 2 {
		t.Errorf("expected 2 dimensions, got %d", len(payload.Dimensions))
	}
	if payload.ConfigHash == "" {
		t.Error("config_hash must not be empty")
	}
	if payload.PrimaryGenreWeight != config.DefaultPrimaryGenreWeight {
		t.Errorf("primary_genre_weight: got %v, want %v", payload.PrimaryGenreWeight, config.DefaultPrimaryGenreWeight)
	}
	if payload.MaxMultiplier != config.DefaultMaxMultiplier {
		t.Errorf("max_multiplier: got %v, want %v", payload.MaxMultiplier, config.DefaultMaxMultiplier)
	}
}

func TestHandleGetConfig_HashMatchesConfig(t *testing.T) {
	cfg := minimalConfig()
	srv := newTestServer(cfg, true, "")

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	var payload configPayload
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	want := config.Hash(srv.getSnapshot().cfg)
	if payload.ConfigHash != want {
		t.Errorf("config_hash mismatch: got %s, want %s", payload.ConfigHash, want)
	}
}

func TestHandleGetConfig_RouteAbsentWithoutFlag(t *testing.T) {
	srv := newTestServer(minimalConfig(), false, "")

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	// The SPA wildcard catches /config when the route is not registered and
	// returns 200 HTML — but the config handler would return application/json.
	// Absence of JSON content-type confirms the config handler did not run.
	ct := rec.Header().Get("Content-Type")
	if ct == "application/json" {
		t.Error("GET /config returned JSON — config handler should not be registered without --live-config")
	}
}

func TestHandlePostConfig_ValidReplacement(t *testing.T) {
	path := writeConfigFile(t)
	cfg := minimalConfig()
	srv := newTestServer(cfg, true, path)

	body := configPayload{
		Dimensions: map[string]configDimensionEntry{
			"story": {Label: "Story", Description: "Narrative quality", Weight: 0.70},
			"fun":   {Label: "Fun", Description: "Enjoyment", Weight: 0.30},
		},
		DimensionOrder:     []string{"story", "fun"},
		Genres:             map[string]map[string]float64{},
		PrimaryGenreWeight: 0.5,
		MaxMultiplier:      1.5,
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/config", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload configPayload
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if payload.Dimensions["story"].Weight != 0.70 {
		t.Errorf("story weight: got %v, want 0.70", payload.Dimensions["story"].Weight)
	}
	if payload.PrimaryGenreWeight != 0.5 {
		t.Errorf("primary_genre_weight: got %v, want 0.5", payload.PrimaryGenreWeight)
	}
	if payload.ConfigHash == "" {
		t.Error("config_hash must not be empty in response")
	}

	// Snapshot must reflect the new config.
	snap := srv.getSnapshot()
	if snap.cfg.Dimensions["story"].Weight != 0.70 {
		t.Error("snapshot not updated after POST /config")
	}
}

func TestHandlePostConfig_WritesToDisk(t *testing.T) {
	path := writeConfigFile(t)
	cfg := minimalConfig()
	srv := newTestServer(cfg, true, path)

	body := configPayload{
		Dimensions: map[string]configDimensionEntry{
			"story": {Label: "Story Updated", Description: "Narrative quality", Weight: 0.55},
			"fun":   {Label: "Fun", Description: "Enjoyment", Weight: 0.45},
		},
		Genres:             map[string]map[string]float64{},
		PrimaryGenreWeight: config.DefaultPrimaryGenreWeight,
		MaxMultiplier:      config.DefaultMaxMultiplier,
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/config", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	srv.router.ServeHTTP(httptest.NewRecorder(), req)

	// Reload from disk and verify the new label survived.
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if reloaded.Dimensions["story"].Label != "Story Updated" {
		t.Errorf("disk not updated: got label %q, want %q",
			reloaded.Dimensions["story"].Label, "Story Updated")
	}
}

func TestHandlePostConfig_InvalidWeights_Returns400(t *testing.T) {
	path := writeConfigFile(t)
	cfg := minimalConfig()
	srv := newTestServer(cfg, true, path)
	hashBefore := config.Hash(srv.getSnapshot().cfg)

	cases := []struct {
		name string
		body configPayload
	}{
		{
			name: "weights_dont_sum_to_one",
			body: configPayload{
				Dimensions: map[string]configDimensionEntry{
					"story": {Label: "Story", Description: "d", Weight: 0.60},
					"fun":   {Label: "Fun", Description: "d", Weight: 0.60},
				},
				Genres:             map[string]map[string]float64{},
				PrimaryGenreWeight: config.DefaultPrimaryGenreWeight,
				MaxMultiplier:      config.DefaultMaxMultiplier,
			},
		},
		{
			name: "genre_references_unknown_dimension",
			body: configPayload{
				Dimensions: map[string]configDimensionEntry{
					"story": {Label: "Story", Description: "d", Weight: 0.60},
					"fun":   {Label: "Fun", Description: "d", Weight: 0.40},
				},
				Genres: map[string]map[string]float64{
					"action": {"nonexistent": 1.2},
				},
				PrimaryGenreWeight: config.DefaultPrimaryGenreWeight,
				MaxMultiplier:      config.DefaultMaxMultiplier,
			},
		},
		{
			name: "multiplier_exceeds_max",
			body: configPayload{
				Dimensions: map[string]configDimensionEntry{
					"story": {Label: "Story", Description: "d", Weight: 0.60},
					"fun":   {Label: "Fun", Description: "d", Weight: 0.40},
				},
				Genres: map[string]map[string]float64{
					"action": {"story": 9.9},
				},
				PrimaryGenreWeight: config.DefaultPrimaryGenreWeight,
				MaxMultiplier:      config.DefaultMaxMultiplier,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(tc.body)
			req := httptest.NewRequest(http.MethodPost, "/config", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d", rec.Code)
			}

			var errResp errorResponse
			if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
				t.Fatalf("decoding error response: %v", err)
			}
			if errResp.Error == "" {
				t.Error("error field must not be empty")
			}

			// Snapshot must be unchanged.
			if config.Hash(srv.getSnapshot().cfg) != hashBefore {
				t.Error("snapshot changed after rejected POST /config")
			}
		})
	}
}

func TestHandlePostConfig_MalformedBody_Returns400(t *testing.T) {
	srv := newTestServer(minimalConfig(), true, "")

	req := httptest.NewRequest(http.MethodPost, "/config", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandlePostConfig_WriteFailure_Returns500_SnapshotUnchanged(t *testing.T) {
	// Point configPath at a read-only directory so Write fails.
	roDir := t.TempDir()
	if err := os.Chmod(roDir, 0o555); err != nil {
		t.Skip("cannot set directory read-only:", err)
	}
	t.Cleanup(func() { os.Chmod(roDir, 0o755) }) //nolint:errcheck

	roPath := filepath.Join(roDir, "config.toml")
	cfg := minimalConfig()
	srv := newTestServer(cfg, true, roPath)
	hashBefore := config.Hash(srv.getSnapshot().cfg)

	body := configPayload{
		Dimensions: map[string]configDimensionEntry{
			"story": {Label: "Story", Description: "d", Weight: 0.70},
			"fun":   {Label: "Fun", Description: "d", Weight: 0.30},
		},
		Genres:             map[string]map[string]float64{},
		PrimaryGenreWeight: config.DefaultPrimaryGenreWeight,
		MaxMultiplier:      config.DefaultMaxMultiplier,
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/config", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}

	if config.Hash(srv.getSnapshot().cfg) != hashBefore {
		t.Error("snapshot changed after write failure — must not swap on disk error")
	}
}

func TestHandlePostConfig_RouteAbsentWithoutFlag(t *testing.T) {
	srv := newTestServer(minimalConfig(), false, "")

	body := configPayload{
		Dimensions:         map[string]configDimensionEntry{},
		Genres:             map[string]map[string]float64{},
		PrimaryGenreWeight: config.DefaultPrimaryGenreWeight,
		MaxMultiplier:      config.DefaultMaxMultiplier,
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/config", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	// The SPA wildcard catches all methods including POST and returns 200 HTML.
	// The config handler returns application/json — absence of that confirms
	// the config handler did not run.
	ct := rec.Header().Get("Content-Type")
	if ct == "application/json" {
		t.Error("POST /config returned JSON — config handler should not be registered without --live-config")
	}
}
