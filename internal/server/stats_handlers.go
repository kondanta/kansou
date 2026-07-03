package server

import (
	"log/slog"
	"net/http"

	"github.com/kondanta/kansou/internal/stats"
	"github.com/kondanta/kansou/internal/store"
)

// requireStore writes the DBless 503 error and returns false when no
// database is configured. Callers must return immediately when it returns
// false.
func (s *Server) requireStore(w http.ResponseWriter) bool {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{
			Error: "stats require a database — set KANSOU_DB_TYPE to enable",
		})
		return false
	}
	return true
}

// handleStatsSummary returns a headline summary across all stats categories.
//
//	@Summary		Stats summary
//	@Description	Returns one headline metric per stats category. Requires a database.
//	@Tags			stats
//	@Produce		json
//	@Success		200	{object}	stats.Summary
//	@Failure		503	{object}	errorResponse
//	@Router			/api/v1/stats [get]
func (s *Server) handleStatsSummary(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	sum, err := stats.New(s.store).Summary(r.Context())
	if err != nil {
		slog.Error("computing stats summary", "err", err)
		writeError(w, http.StatusInternalServerError, "computing stats summary")
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// genreStatsResponse is the response body for GET /stats/genres.
// swagger:model genreStatsResponse
type genreStatsResponse struct {
	GenreBreakdown         []store.GenreStat              `json:"genre_breakdown"`
	ScoreByGenre           []store.GenreScore             `json:"score_by_genre"`
	GenreDimensionAffinity []store.GenreDimensionAffinity `json:"genre_dimension_affinity"`
}

// handleStatsGenres returns genre breakdown, average score by genre, and
// genre-dimension affinity.
//
//	@Summary		Genre stats
//	@Description	Returns genre breakdown, average score by genre, and genre-dimension affinity. Requires a database.
//	@Tags			stats
//	@Produce		json
//	@Success		200	{object}	genreStatsResponse
//	@Failure		503	{object}	errorResponse
//	@Router			/api/v1/stats/genres [get]
func (s *Server) handleStatsGenres(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	g, err := stats.New(s.store).Genres(r.Context())
	if err != nil {
		slog.Error("computing genre stats", "err", err)
		writeError(w, http.StatusInternalServerError, "computing genre stats")
		return
	}
	writeJSON(w, http.StatusOK, genreStatsResponse{
		GenreBreakdown:         g.Breakdown,
		ScoreByGenre:           g.ByGenre,
		GenreDimensionAffinity: g.Affinity,
	})
}

// handleStatsDimensions returns variance, consistency, correlation, skip
// rate, and weight override frequency per dimension.
//
//	@Summary		Dimension stats
//	@Description	Returns variance, consistency, correlation, skip rate, and weight override frequency per dimension. Requires a database.
//	@Tags			stats
//	@Produce		json
//	@Success		200	{object}	store.DimensionStatsResponse
//	@Failure		503	{object}	errorResponse
//	@Router			/api/v1/stats/dimensions [get]
func (s *Server) handleStatsDimensions(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	d, err := stats.New(s.store).Dimensions(r.Context())
	if err != nil {
		slog.Error("computing dimension stats", "err", err)
		writeError(w, http.StatusInternalServerError, "computing dimension stats")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// historyStatsResponse is the response body for GET /stats/history.
// swagger:model historyStatsResponse
type historyStatsResponse struct {
	MostRescored []store.RescoredStat     `json:"most_rescored"`
	Outliers     []store.OutlierStat      `json:"outliers"`
	ConfigImpact []store.ConfigImpactStat `json:"config_impact"`
}

// handleStatsHistory returns most-rescored entries, outliers, and config
// impact.
//
//	@Summary		History stats
//	@Description	Returns most-rescored entries, outliers, and config impact. Requires a database.
//	@Tags			stats
//	@Produce		json
//	@Success		200	{object}	historyStatsResponse
//	@Failure		503	{object}	errorResponse
//	@Router			/api/v1/stats/history [get]
func (s *Server) handleStatsHistory(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	h, err := stats.New(s.store).History(r.Context())
	if err != nil {
		slog.Error("computing history stats", "err", err)
		writeError(w, http.StatusInternalServerError, "computing history stats")
		return
	}
	writeJSON(w, http.StatusOK, historyStatsResponse{
		MostRescored: h.MostRescored,
		Outliers:     h.Outliers,
		ConfigImpact: h.ConfigImpact,
	})
}

// dbInfoResponse is the response body for GET /db-info.
// swagger:model dbInfoResponse
type dbInfoResponse struct {
	// DB is "sqlite", "postgres", or null in DBless mode.
	DB *string `json:"db"`
	// LiveConfig is only present in DBless mode — it is not meaningful in DB
	// mode, where config is always writable via POST /config.
	LiveConfig *bool `json:"live_config,omitempty"`
}

// handleDBInfo reports which database backend, if any, is active.
//
//	@Summary		Database info
//	@Description	Reports the active database backend, or DBless live-config status. Always available.
//	@Tags			system
//	@Produce		json
//	@Success		200	{object}	dbInfoResponse
//	@Router			/api/v1/db-info [get]
func (s *Server) handleDBInfo(w http.ResponseWriter, r *http.Request) {
	if s.dbType != "" {
		writeJSON(w, http.StatusOK, dbInfoResponse{DB: &s.dbType})
		return
	}
	writeJSON(w, http.StatusOK, dbInfoResponse{LiveConfig: &s.liveConfig})
}
