package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kondanta/kansou/internal/anilist"
	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
	"github.com/kondanta/kansou/internal/store/sqlite"
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
	return New(cfg, al, eng, liveConfig, configPath, nil, "", nil, false)
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
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
		t.Errorf(
			"primary_genre_weight: got %v, want %v",
			payload.PrimaryGenreWeight,
			config.DefaultPrimaryGenreWeight,
		)
	}
	if payload.MaxMultiplier != config.DefaultMaxMultiplier {
		t.Errorf(
			"max_multiplier: got %v, want %v",
			payload.MaxMultiplier,
			config.DefaultMaxMultiplier,
		)
	}
}

func TestHandleGetConfig_HashMatchesConfig(t *testing.T) {
	cfg := minimalConfig()
	srv := newTestServer(cfg, true, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	// The SPA wildcard catches /api/v1/config when the route is not registered
	// and returns 200 HTML — but the config handler would return application/json.
	// Absence of JSON content-type confirms the config handler did not run.
	ct := rec.Header().Get("Content-Type")
	if ct == "application/json" {
		t.Error(
			"GET /api/v1/config returned JSON — config handler should not be registered without --live-config",
		)
	}
}

// ---------------------------------------------------------------------------
// PATCH /config/general
// ---------------------------------------------------------------------------

func TestHandlePatchConfigGeneral_UpdatesMaxHistory(t *testing.T) {
	path := writeConfigFile(t)
	cfg := minimalConfig()
	srv := newTestServer(cfg, true, path)

	body := generalPayload{MaxHistory: new(int)}
	*body.MaxHistory = 50
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/config/general", bytes.NewReader(b))
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
	if payload.MaxHistory != 50 {
		t.Errorf("max_history in response: got %d, want 50", payload.MaxHistory)
	}
	if srv.getSnapshot().cfg.MaxHistory != 50 {
		t.Error("snapshot not updated after PATCH /config/general")
	}

	// Verify the change actually landed on disk — not just in memory.
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reloading config from disk: %v", err)
	}
	if reloaded.MaxHistory != 50 {
		t.Errorf("disk not updated: max_history on disk is %d, want 50", reloaded.MaxHistory)
	}
}

func TestHandlePatchConfigGeneral_AbsentFieldIsNoop(t *testing.T) {
	path := writeConfigFile(t)
	cfg := minimalConfig()
	cfg.MaxHistory = 25
	srv := newTestServer(cfg, true, path)

	// Empty body — no fields set.
	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/config/general",
		bytes.NewReader([]byte("{}")),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// Explicitly verify the value — not a hash proxy.
	if srv.getSnapshot().cfg.MaxHistory != 25 {
		t.Errorf("max_history changed: got %d, want 25 — absent field must leave value unchanged",
			srv.getSnapshot().cfg.MaxHistory)
	}
}

func TestHandlePatchConfigGeneral_MalformedBody_Returns400(t *testing.T) {
	srv := newTestServer(minimalConfig(), true, "")

	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/config/general",
		bytes.NewReader([]byte("not json")),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandlePatchConfigGeneral_RouteAbsentWithoutFlag(t *testing.T) {
	srv := newTestServer(minimalConfig(), false, "")

	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/config/general",
		bytes.NewReader([]byte("{}")),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Type") == "application/json" {
		t.Error(
			"PATCH /config/general returned JSON — handler should not be registered without --live-config",
		)
	}
}

// ---------------------------------------------------------------------------
// PATCH /config/genres
// ---------------------------------------------------------------------------

func TestHandlePatchConfigGenres_MergesEntries(t *testing.T) {
	path := writeConfigFile(t)
	cfg := minimalConfig()
	// Genre keys are lowercased by config.Rebuild (LowercaseGenreKeys).
	// Use lowercase throughout so snapshot lookups match what Rebuild stores.
	cfg.Genres = map[string]map[string]float64{
		"action": {"story": 1.2},
	}
	srv := newTestServer(cfg, true, path)

	body := genresUpdatePayload{
		Genres: map[string]map[string]float64{
			"comedy": {"fun": 1.1},
		},
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/config/genres", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	snap := srv.getSnapshot()

	// Original entry must survive with its original multiplier value intact.
	actionMult, ok := snap.cfg.Genres["action"]["story"]
	if !ok {
		t.Fatal("existing 'action' genre entry was removed — merge must preserve absent keys")
	}
	if actionMult != 1.2 {
		t.Errorf("action/story multiplier changed: got %v, want 1.2", actionMult)
	}

	// New entry must be present with the correct multiplier value.
	comedyMult, ok := snap.cfg.Genres["comedy"]["fun"]
	if !ok {
		t.Fatal("new 'comedy' genre entry not present after PATCH /config/genres")
	}
	if comedyMult != 1.1 {
		t.Errorf("comedy/fun multiplier: got %v, want 1.1", comedyMult)
	}
}

func TestHandlePatchConfigGenres_UpdatesScalars(t *testing.T) {
	path := writeConfigFile(t)
	cfg := minimalConfig()
	srv := newTestServer(cfg, true, path)

	newPGW := 0.4
	newMM := 1.3
	body := genresUpdatePayload{
		PrimaryGenreWeight: &newPGW,
		MaxMultiplier:      &newMM,
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/config/genres", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	snap := srv.getSnapshot()
	if snap.cfg.PrimaryGenreWeight != newPGW {
		t.Errorf("primary_genre_weight: got %v, want %v", snap.cfg.PrimaryGenreWeight, newPGW)
	}
	if snap.cfg.MaxMultiplier != newMM {
		t.Errorf("max_multiplier: got %v, want %v", snap.cfg.MaxMultiplier, newMM)
	}
}

func TestHandlePatchConfigGenres_InvalidPayload_Returns400(t *testing.T) {
	path := writeConfigFile(t)
	cfg := minimalConfig()
	srv := newTestServer(cfg, true, path)
	hashBefore := config.Hash(srv.getSnapshot().cfg)

	cases := []struct {
		name string
		body genresUpdatePayload
	}{
		{
			name: "genre_references_unknown_dimension",
			body: genresUpdatePayload{
				Genres: map[string]map[string]float64{
					"action": {"nonexistent_dim": 1.2},
				},
			},
		},
		{
			name: "multiplier_exceeds_max",
			body: genresUpdatePayload{
				Genres: map[string]map[string]float64{
					"action": {"story": 9.9},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(tc.body)
			req := httptest.NewRequest(
				http.MethodPatch,
				"/api/v1/config/genres",
				bytes.NewReader(b),
			)
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
			if config.Hash(srv.getSnapshot().cfg) != hashBefore {
				t.Error("snapshot changed after rejected PATCH /config/genres")
			}
		})
	}
}

func TestHandlePatchConfigGenres_MalformedBody_Returns400(t *testing.T) {
	srv := newTestServer(minimalConfig(), true, "")

	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/config/genres",
		bytes.NewReader([]byte("not json")),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// DELETE /config/genres/{key}
// ---------------------------------------------------------------------------

func TestHandleDeleteConfigGenre_RemovesExistingKey(t *testing.T) {
	path := writeConfigFile(t)
	cfg := minimalConfig()
	// Genre keys are lowercased by config.Rebuild — use lowercase in both the
	// seed and the URL so the handler's map lookup and the snapshot assertion agree.
	cfg.Genres = map[string]map[string]float64{
		"action": {"story": 1.2},
		"comedy": {"fun": 1.1},
	}
	srv := newTestServer(cfg, true, path)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/config/genre/action", nil)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	snap := srv.getSnapshot()
	if _, ok := snap.cfg.Genres["action"]; ok {
		t.Error("'action' still present after DELETE /config/genre/action")
	}
	if _, ok := snap.cfg.Genres["comedy"]; !ok {
		t.Error("'comedy' was removed — DELETE must only remove the targeted key")
	}
}

func TestHandleDeleteConfigGenre_NonexistentKey_Returns404(t *testing.T) {
	srv := newTestServer(minimalConfig(), true, "")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/config/genre/DoesNotExist", nil)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleDeleteConfigGenre_ScalarNamesReturn404(t *testing.T) {
	// primary_genre_weight and max_multiplier are struct fields, not genre map
	// entries — DELETE must return 404, not affect those fields.
	cfg := minimalConfig()
	srv := newTestServer(cfg, true, "")

	for _, key := range []string{"primary_genre_weight", "max_multiplier"} {
		t.Run(key, func(t *testing.T) {
			req := httptest.NewRequest(
				http.MethodDelete,
				"/api/v1/config/genres/"+key,
				nil,
			)
			rec := httptest.NewRecorder()
			srv.router.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("DELETE /config/genres/%s: expected 404, got %d", key, rec.Code)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// PUT /config/dimensions
// ---------------------------------------------------------------------------

func TestHandlePutConfigDimensions_ValidReplacement(t *testing.T) {
	path := writeConfigFile(t)
	// minimalConfig has "story" and "fun". The replacement removes "fun" and
	// introduces "art" — proving this is a full replacement, not a merge.
	cfg := minimalConfig()
	srv := newTestServer(cfg, true, path)

	body := dimensionsUpdatePayload{
		Dimensions: map[string]configDimensionEntry{
			"story": {Label: "Story", Description: "Narrative quality", Weight: 0.60},
			"art":   {Label: "Art", Description: "Visual quality", Weight: 0.40},
		},
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/dimensions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	snap := srv.getSnapshot()

	// "fun" was in the original but not in the payload — it must be gone.
	if _, ok := snap.cfg.Dimensions["fun"]; ok {
		t.Error("'fun' dimension still present — PUT must fully replace, not merge")
	}
	// New dimension must exist with the correct weight.
	if snap.cfg.Dimensions["art"].Weight != 0.40 {
		t.Errorf("art weight: got %v, want 0.40", snap.cfg.Dimensions["art"].Weight)
	}
	if snap.cfg.Dimensions["story"].Weight != 0.60 {
		t.Errorf("story weight: got %v, want 0.60", snap.cfg.Dimensions["story"].Weight)
	}
}

func TestHandlePutConfigDimensions_InvalidWeights_Returns400(t *testing.T) {
	srv := newTestServer(minimalConfig(), true, "")
	hashBefore := config.Hash(srv.getSnapshot().cfg)

	body := dimensionsUpdatePayload{
		Dimensions: map[string]configDimensionEntry{
			"story": {Label: "Story", Description: "d", Weight: 0.60},
			"fun":   {Label: "Fun", Description: "d", Weight: 0.60},
		},
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/dimensions", bytes.NewReader(b))
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
	if config.Hash(srv.getSnapshot().cfg) != hashBefore {
		t.Error("snapshot changed after rejected PUT /config/dimensions")
	}
}

func TestHandlePutConfigDimensions_EmptyMap_Returns400(t *testing.T) {
	srv := newTestServer(minimalConfig(), true, "")

	body := dimensionsUpdatePayload{Dimensions: map[string]configDimensionEntry{}}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/dimensions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandlePutConfigDimensions_WriteFailure_Returns500_SnapshotUnchanged(t *testing.T) {
	roDir := t.TempDir()
	if err := os.Chmod(roDir, 0o555); err != nil {
		t.Skip("cannot set directory read-only:", err)
	}
	t.Cleanup(func() { os.Chmod(roDir, 0o755) }) //nolint:errcheck

	roPath := filepath.Join(roDir, "config.toml")
	cfg := minimalConfig()
	srv := newTestServer(cfg, true, roPath)
	hashBefore := config.Hash(srv.getSnapshot().cfg)

	body := dimensionsUpdatePayload{
		Dimensions: map[string]configDimensionEntry{
			"story": {Label: "Story", Description: "d", Weight: 0.70},
			"fun":   {Label: "Fun", Description: "d", Weight: 0.30},
		},
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/dimensions", bytes.NewReader(b))
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

// TestHandlePutConfigDimensions_DBMode_PersistsToStore_NotDisk mirrors the
// regression test from the old handlePostConfig suite: write must land in the
// database and leave the config file on disk untouched.
func TestHandlePutConfigDimensions_DBMode_PersistsToStore_NotDisk(t *testing.T) {
	path := writeConfigFile(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture config file: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "kansou.db")
	st, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := minimalConfig()
	if err := st.SaveScoringConfig(context.Background(), cfg); err != nil {
		t.Fatalf("seeding db config: %v", err)
	}
	eng := minimalEngine(cfg)
	srv := New(cfg, anilist.NewClient(), eng, true, path, st, "sqlite", nil, false)

	body := dimensionsUpdatePayload{
		Dimensions: map[string]configDimensionEntry{
			"story": {Label: "Story Updated In DB", Description: "Narrative quality", Weight: 0.55},
			"fun":   {Label: "Fun", Description: "Enjoyment", Weight: 0.45},
		},
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/dimensions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	reloaded, err := st.LoadScoringConfig(context.Background())
	if err != nil {
		t.Fatalf("reloading config from db: %v", err)
	}
	if reloaded.Dimensions["story"].Label != "Story Updated In DB" {
		t.Errorf("db not updated: got label %q, want %q",
			reloaded.Dimensions["story"].Label, "Story Updated In DB")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading config file after request: %v", err)
	}
	if string(before) != string(after) {
		t.Error(
			"config file was modified in DB mode — PUT /config/dimensions should persist to the store, not disk",
		)
	}
}
