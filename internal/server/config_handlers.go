package server

import (
	"net/http"

	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
)

// buildEngine converts config dimensions into a scoring.Engine.
// Duplicated from cmd/root.go — a shared package would either pollute
// internal/config or internal/scoring with the other's types, and a
// one-function adapter package would create naming confusion with the
// scoring engine itself. If scoring.DimensionDef or config.DimensionDef
// fields change, update both copies.
func buildEngine(cfg *config.Config) *scoring.Engine {
	defs := make(map[string]scoring.DimensionDef, len(cfg.Dimensions))
	for key, d := range cfg.Dimensions {
		defs[key] = scoring.DimensionDef{
			Label:         d.Label,
			Description:   d.Description,
			Weight:        d.Weight,
			BiasResistant: d.BiasResistant,
		}
	}
	return scoring.NewEngine(cfg.DimensionOrder, defs, cfg.Genres, cfg.PrimaryGenreWeight)
}

// configDimensionEntry is the JSON representation of a single dimension
// in the GET /config and POST /config payloads.
type configDimensionEntry struct {
	Label         string  `json:"label"`
	Description   string  `json:"description"`
	Weight        float64 `json:"weight"`
	BiasResistant bool    `json:"bias_resistant"`
}

// configPayload is the mutable config surface exchanged by GET /config
// and POST /config. The shape is identical in both directions — GET returns
// it, POST accepts it (without config_hash).
type configPayload struct {
	Dimensions         map[string]configDimensionEntry `json:"dimensions"`
	DimensionOrder     []string                        `json:"dimension_order"`
	Genres             map[string]map[string]float64   `json:"genres"`
	PrimaryGenreWeight float64                         `json:"primary_genre_weight"`
	MaxMultiplier      float64                         `json:"max_multiplier"`
	ConfigHash         string                          `json:"config_hash,omitempty"`
	MaxHistory         int                             `json:"max_history"`
}

// handleGetConfig returns the current mutable config surface.
//
//	@Summary		Get config
//	@Description	Returns the current scoring config (dimensions, genres, weights). Only available when --live-config is set.
//	@Tags			config
//	@Produce		json
//	@Success		200	{object}	configPayload
//	@Router			/api/v1/config [get]
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	snap := s.getSnapshot()
	writeJSON(w, http.StatusOK, toConfigPayload(snap.cfg))
}

// toConfigPayload converts a *config to configPayload for JSON serialization
func toConfigPayload(cfg *config.Config) configPayload {
	dims := make(map[string]configDimensionEntry, len(cfg.Dimensions))
	for key, d := range cfg.Dimensions {
		dims[key] = configDimensionEntry{
			Label:         d.Label,
			Description:   d.Description,
			Weight:        d.Weight,
			BiasResistant: d.BiasResistant,
		}
	}

	return configPayload{
		Dimensions:         dims,
		DimensionOrder:     cfg.DimensionOrder,
		Genres:             cfg.Genres,
		PrimaryGenreWeight: cfg.PrimaryGenreWeight,
		MaxMultiplier:      cfg.MaxMultiplier,
		ConfigHash:         config.Hash(cfg),
		MaxHistory:         cfg.MaxHistory,
	}
}

// handlePostConfig replaces the mutable config surface and reloads the engine.
//
//	@Summary		Update config
//	@Description	Replaces the scoring config (dimensions, genres, weights) and reloads the engine atomically. Persists to the database in DB mode, or to disk otherwise. Only available when --live-config is set.
//	@Tags			config
//	@Accept			json
//	@Produce		json
//	@Param			request	body		configPayload	true	"New config (config_hash is ignored)"
//	@Success		200		{object}	configPayload
//	@Failure		400		{object}	errorResponse
//	@Failure		500		{object}	errorResponse
//	@Router			/api/v1/config [post]
func (s *Server) handlePostConfig(w http.ResponseWriter, r *http.Request) {
	var payload configPayload
	if !decodeBody(w, r, &payload) {
		return
	}

	dims := make(map[string]config.DimensionDef, len(payload.Dimensions))
	for key, d := range payload.Dimensions {
		dims[key] = config.DimensionDef{
			Label:         d.Label,
			Description:   d.Description,
			Weight:        d.Weight,
			BiasResistant: d.BiasResistant,
		}
	}

	snap := s.getSnapshot()
	newCfg, err := config.Rebuild(
		snap.cfg,
		dims,
		payload.Genres,
		payload.PrimaryGenreWeight,
		payload.MaxMultiplier,
		payload.MaxHistory,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// In DB mode, config lives in the database — writing to disk here would
	// silently diverge from what LoadScoringConfig returns on next restart.
	if s.store != nil {
		if err := s.store.SaveScoringConfig(r.Context(), newCfg); err != nil {
			writeError(
				w,
				http.StatusInternalServerError,
				"persisting config to database: "+err.Error(),
			)
			return
		}
	} else {
		if err := config.Write(s.configPath, newCfg); err != nil {
			writeError(
				w,
				http.StatusInternalServerError,
				"config file is not writable: "+err.Error(),
			)
			return
		}
	}

	eng := buildEngine(newCfg)
	s.snapshot.Store(&configSnapshot{cfg: newCfg, engine: eng})

	writeJSON(w, http.StatusOK, toConfigPayload(newCfg))
}
