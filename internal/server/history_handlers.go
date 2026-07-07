package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kondanta/kansou/internal/store"
)

// historyListItem is one row in the GET /history response.
// swagger:model historyListItem
type historyListItem struct {
	ScoreID     int       `json:"score_id"`
	AnilistID   int       `json:"anilist_id"`
	TitleRomaji string    `json:"title_romaji"`
	MediaType   string    `json:"media_type"`
	Format      string    `json:"format"`
	CoverImage  string    `json:"cover_image"`
	FinalScore  float64   `json:"final_score"`
	EntryCount  int       `json:"entry_count"`
	ScoredAt    time.Time `json:"scored_at"`
}

// handleHistoryList returns the latest score for every scored entry.
//
//	@Summary		List history
//	@Description	Returns the latest score for every scored entry, ordered by scored_at descending. Requires a database.
//	@Tags			history
//	@Produce		json
//	@Success		200	{array}		historyListItem
//	@Failure		503	{object}	errorResponse
//	@Router			/api/v1/history [get]
func (s *Server) handleHistoryList(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	entries, err := s.store.ListLatest(r.Context())
	if err != nil {
		slog.Error("listing history", "err", err)
		writeError(w, http.StatusInternalServerError, "listing history")
		return
	}
	items := make([]historyListItem, len(entries))
	for i, e := range entries {
		items[i] = historyListItem{
			ScoreID: e.ID, AnilistID: e.AnilistID, TitleRomaji: e.TitleRomaji,
			MediaType: e.MediaType, Format: e.Format, CoverImage: e.CoverImage,
			FinalScore: e.FinalScore, EntryCount: e.EntryCount, ScoredAt: e.ScoredAt,
		}
	}
	writeJSON(w, http.StatusOK, items)
}

// handleHistoryDetail returns all non-deleted scores for one AniList media ID.
//
//	@Summary		Show history detail
//	@Description	Returns all non-deleted scores for one AniList media ID, newest first, with full breakdown. Requires a database.
//	@Tags			history
//	@Produce		json
//	@Param			anilist_id	path	int	true	"AniList media ID"
//	@Success		200	{array}		store.Score
//	@Failure		400	{object}	errorResponse
//	@Failure		503	{object}	errorResponse
//	@Router			/api/v1/history/{anilist_id} [get]
func (s *Server) handleHistoryDetail(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	anilistID, err := strconv.Atoi(chi.URLParam(r, "anilist_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "anilist_id must be an integer")
		return
	}
	history, err := s.store.ScoreHistory(r.Context(), anilistID)
	if err != nil {
		slog.Error("fetching score history", "err", err)
		writeError(w, http.StatusInternalServerError, "fetching score history")
		return
	}
	writeJSON(w, http.StatusOK, history)
}

// handleHistoryDelete soft-deletes a score by its row ID.
//
//	@Summary		Delete a history entry
//	@Description	Soft-deletes a score by its row ID (not the AniList ID). Deliberate removal from active tracking — does not promote any other score to latest. Requires a database.
//	@Tags			history
//	@Param			score_id	path	int	true	"scores.id primary key"
//	@Success		204
//	@Failure		400	{object}	errorResponse
//	@Failure		404	{object}	errorResponse
//	@Failure		503	{object}	errorResponse
//	@Router			/api/v1/history/{score_id} [delete]
func (s *Server) handleHistoryDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireStore(w) {
		return
	}
	scoreID, err := strconv.Atoi(chi.URLParam(r, "score_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "score_id must be an integer")
		return
	}

	if err := s.store.SoftDeleteScore(r.Context(), scoreID); err != nil {
		if errors.Is(err, store.ErrScoreNotFound) {
			writeError(w, http.StatusNotFound, "score not found or already deleted")
			return
		}
		slog.Error("deleting score", "err", err)
		writeError(w, http.StatusInternalServerError, "deleting score")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
