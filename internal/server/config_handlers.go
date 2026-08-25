package server

import (
	"context"
	"fmt"
	"maps"
	"net/http"

	"github.com/go-chi/chi/v5"
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

// configPayload is the full config surface returned by GET /config and all
// write endpoints. Write endpoints return this after applying their change.
type configPayload struct {
	Dimensions         map[string]configDimensionEntry `json:"dimensions"`
	DimensionOrder     []string                        `json:"dimension_order"`
	Genres             map[string]map[string]float64   `json:"genres"`
	PrimaryGenreWeight float64                         `json:"primary_genre_weight"`
	MaxMultiplier      float64                         `json:"max_multiplier"`
	ConfigHash         string                          `json:"config_hash,omitempty"`
	MaxHistory         int                             `json:"max_history"`
}

// generalPayload is the request body for PATCH /config/general
type generalPayload struct {
	MaxHistory *int `json:"max_history,omitempty"`
}

// genresUpdatePayload is the request body for PATCH /config/genres
// Absent map entries are left untouched. Absent scalar pointers are left untouched.
type genresUpdatePayload struct {
	Genres             map[string]map[string]float64 `json:"genres,omitempty"`
	PrimaryGenreWeight *float64                      `json:"primary_genre_weight,omitempty"`
	MaxMultiplier      *float64                      `json:"max_multiplier,omitempty"`
}

// dimensionUpdatePayload is the request body for PUT /config/dimensions
type dimensionsUpdatePayload struct {
	Dimensions map[string]configDimensionEntry `json:"dimensions"`
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

// handlePatchConfigGeneral updates general operational config (max_history).
//
//	@Summary		Update general config
//	@Description	Updates general operational parameters. Absent fields are left unchanged. Only available when --live-config is set or a database is configured.
//	@Tags			config
//	@Accept			json
//	@Produce		json
//	@Param			request	body		generalPayload	true	"Fields to update"
//	@Success		200		{object}	configPayload
//	@Failure		400		{object}	errorResponse
//	@Failure		500		{object}	errorResponse
//	@Router			/api/v1/config/general [patch]
func (s *Server) handlePatchConfigGeneral(w http.ResponseWriter, r *http.Request) {
	var payload generalPayload
	if !decodeBody(w, r, &payload) {
		return
	}

	snap := s.getSnapshot()
	maxHistory := snap.cfg.MaxHistory
	if payload.MaxHistory != nil {
		maxHistory = *payload.MaxHistory
	}

	newCfg, err := config.Rebuild(
		snap.cfg,
		snap.cfg.Dimensions,
		snap.cfg.Genres,
		snap.cfg.PrimaryGenreWeight,
		snap.cfg.MaxMultiplier,
		maxHistory,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.persistAndSwap(r.Context(), newCfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toConfigPayload(newCfg))
}

// handlePatchConfigGenres merges genre map entries and governing scalars into
// the current config.
//
//	@Summary		Update genres config
//	@Description	Merges genre map entries into the current config. Absent map keys are left untouched. Absent scalar fields (primary_genre_weight, max_multiplier) are left unchanged. Genre keys must reference dimensions that already exist — call PUT /config/dimensions first if adding a new dimension. Only available when --live-config is set or a database is configured.
//	@Tags			config
//	@Accept			json
//	@Produce		json
//	@Param			request	body		genresUpdatePayload	true	"Genre entries and/or governing scalars to update"
//	@Success		200		{object}	configPayload
//	@Failure		400		{object}	errorResponse
//	@Failure		500		{object}	errorResponse
//	@Router			/api/v1/config/genres [patch]
func (s *Server) handlePatchConfigGenres(w http.ResponseWriter, r *http.Request) {
	var payload genresUpdatePayload
	if !decodeBody(w, r, &payload) {
		return
	}

	snap := s.getSnapshot()

	mergedGenres := make(map[string]map[string]float64, len(snap.cfg.Genres))
	maps.Copy(mergedGenres, snap.cfg.Genres)
	maps.Copy(mergedGenres, payload.Genres)

	pgw := snap.cfg.PrimaryGenreWeight
	if payload.PrimaryGenreWeight != nil {
		pgw = *payload.PrimaryGenreWeight
	}

	mm := snap.cfg.MaxMultiplier
	if payload.MaxMultiplier != nil {
		mm = *payload.MaxMultiplier
	}

	newCfg, err := config.Rebuild(
		snap.cfg,
		snap.cfg.Dimensions,
		mergedGenres,
		pgw,
		mm,
		snap.cfg.MaxHistory,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.persistAndSwap(r.Context(), newCfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toConfigPayload(newCfg))
}

// handleDeleteConfigGenre removes a single genre entry from the config.
//
//	@Summary		Delete a genre entry
//	@Description	Removes a single genre entry by key. Returns 404 if the key does not exist. primary_genre_weight and max_multiplier cannot be targeted — they are struct fields, not genre map entries. Only avawhen --live-config is set or a database is configured.
//	@Tags			config
//	@Produce		json
//	@Param			key	path		string	true	"Genre key to remove"
//	@Success		200	{object}	configPayload
//	@Failure		404	{object}	errorResponse
//	@Failure		500	{object}	errorResponse
//	@Router			/api/v1/config/genre/{key} [delete]
func (s *Server) handleDeleteConfigGenre(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")

	snap := s.getSnapshot()
	if _, ok := snap.cfg.Genres[key]; !ok {
		writeError(w, http.StatusNotFound, "genre key not found: "+key)
		return
	}

	mergedGenres := make(map[string]map[string]float64, len(snap.cfg.Genres)-1)
	for k, v := range snap.cfg.Genres {
		if k != key {
			mergedGenres[k] = v
		}
	}

	newCfg, err := config.Rebuild(
		snap.cfg,
		snap.cfg.Dimensions,
		mergedGenres,
		snap.cfg.PrimaryGenreWeight,
		snap.cfg.MaxMultiplier,
		snap.cfg.MaxHistory,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.persistAndSwap(r.Context(), newCfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toConfigPayload(newCfg))
}

// handlePutConfigDimensions fully replaces the dimensions map and reloads the engine.
//
//	@Summary		Replace dimensions config
//	@Description	Full replacement of the dimensions map. All existing dimensions are replaced by the provided map. Weights must sum to 1.0. Note: if any genre entries reference dimensions removed by this call,next PATCH /config/genres will fail validation — remove or update those genre entries first. Only available when --live-config is set or a database is configured.
//	@Tags			config
//	@Accept			json
//	@Produce		json
//	@Param			request	body		dimensionsUpdatePayload	true	"Full dimensions map"
//	@Success		200		{object}	configPayload
//	@Failure		400		{object}	errorResponse
//	@Failure		500		{object}	errorResponse
//	@Router			/api/v1/config/dimensions [put]
func (s *Server) handlePutConfigDimensions(w http.ResponseWriter, r *http.Request) {
	var payload dimensionsUpdatePayload
	if !decodeBody(w, r, &payload) {
		return
	}

	if len(payload.Dimensions) == 0 {
		writeError(w, http.StatusBadRequest, "dimensions map must not be empty")
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
		snap.cfg.Genres,
		snap.cfg.PrimaryGenreWeight,
		snap.cfg.MaxMultiplier,
		snap.cfg.MaxHistory,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.persistAndSwap(r.Context(), newCfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toConfigPayload(newCfg))
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

func (s *Server) persistAndSwap(ctx context.Context, cfg *config.Config) error {
	if s.store != nil {
		if err := s.store.SaveScoringConfig(ctx, cfg); err != nil {
			return fmt.Errorf("persisting config to database: %w", err)
		}
	} else {
		if err := config.Write(s.configPath, cfg); err != nil {
			return fmt.Errorf("config file is not writable: %w", err)
		}
	}

	engine := buildEngine(cfg)
	s.snapshot.Store(&configSnapshot{cfg, engine})

	return nil
}
