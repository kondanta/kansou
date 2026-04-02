package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/kondanta/kansou/internal/anilist"
	"github.com/kondanta/kansou/internal/scoring"
)

// errorResponse is the standard JSON error envelope for all error responses.
// swagger:model errorResponse
type errorResponse struct {
	// Error is a human-readable description of what went wrong.
	Error string `json:"error"`
}

// handleHealth is the liveness check endpoint.
//
//	@Summary		Health check
//	@Description	Returns 200 OK if the server is running.
//	@Tags			system
//	@Produce		plain
//	@Success		200
//	@Router			/health [get]
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// mediaResponse is the JSON representation of an AniList media entry.
// swagger:model mediaResponse
type mediaResponse struct {
	ID           int      `json:"id"`
	TitleRomaji  string   `json:"title_romaji"`
	TitleEnglish string   `json:"title_english,omitempty"`
	TitleNative  string   `json:"title_native,omitempty"`
	Format       string   `json:"format"`
	Status       string   `json:"status"`
	Episodes     int      `json:"episodes,omitempty"`
	Chapters     int      `json:"chapters,omitempty"`
	Genres       []string `json:"genres"`
	CoverImage   string   `json:"cover_image,omitempty"`
	AverageScore int      `json:"average_score"`
	MeanScore    int      `json:"mean_score"`
	MediaType    string   `json:"media_type"`
	AniListURL   string   `json:"anilist_url"`
}

// handleMediaSearch searches AniList for media by name.
//
//	@Summary		Search media
//	@Description	Search AniList for anime or manga by name. Returns up to 5 results sorted by relevance.
//	@Tags			media
//	@Produce		json
//	@Param			q		query	string	true	"Search query"
//	@Param			type	query	string	false	"Media type: ANIME or MANGA"
//	@Success		200	{array}		mediaResponse
//	@Failure		400	{object}	errorResponse
//	@Failure		404	{object}	errorResponse
//	@Failure		502	{object}	errorResponse
//	@Router			/media/search [get]
func (s *Server) handleMediaSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}
	mediaType := r.URL.Query().Get("type")

	results, err := s.al.SearchByNameMulti(q, mediaType)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	responses := make([]mediaResponse, len(results))
	for i := range results {
		responses[i] = toMediaResponse(&results[i])
	}
	writeJSON(w, http.StatusOK, responses)
}

// handleMediaFetch fetches a specific media entry by its AniList ID.
//
//	@Summary		Fetch media by ID
//	@Description	Fetch an anime or manga entry by its AniList media ID.
//	@Tags			media
//	@Produce		json
//	@Param			id	path		int	true	"AniList media ID"
//	@Success		200	{object}	mediaResponse
//	@Failure		400	{object}	errorResponse
//	@Failure		404	{object}	errorResponse
//	@Failure		502	{object}	errorResponse
//	@Router			/media/{id} [get]
func (s *Server) handleMediaFetch(w http.ResponseWriter, r *http.Request) {
	rawID := chi.URLParam(r, "id")
	id, err := strconv.Atoi(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "media ID must be an integer")
		return
	}

	media, err := s.al.FetchByID(id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toMediaResponse(media))
}

// scoreRequest is the request body for POST /score.
// swagger:model scoreRequest
type scoreRequest struct {
	// MediaID is the AniList media ID of the entry being scored.
	MediaID int `json:"media_id"`
	// Scores maps dimension keys to user scores (1.0–10.0).
	Scores map[string]float64 `json:"scores"`
	// SkippedDimensions lists dimension keys the user has marked as N/A.
	SkippedDimensions []string `json:"skipped_dimensions,omitempty"`
	// WeightOverrides maps dimension keys to per-session weight overrides.
	WeightOverrides map[string]float64 `json:"weight_overrides,omitempty"`
}

// scoreResponse is the response body for POST /score.
// swagger:model scoreResponse
type scoreResponse struct {
	// FinalScore is the computed weighted score (rounded to 2 decimal places).
	FinalScore float64 `json:"final_score"`
	// Breakdown is the per-dimension audit trail.
	Breakdown []breakdownRowResponse `json:"breakdown"`
	// Meta carries session-level provenance.
	Meta sessionMetaResponse `json:"meta"`
}

// breakdownRowResponse is the JSON representation of a single BreakdownRow.
// swagger:model breakdownRowResponse
type breakdownRowResponse struct {
	Key               string             `json:"key"`
	Label             string             `json:"label"`
	Score             float64            `json:"score"`
	BaseWeight        float64            `json:"base_weight"`
	AppliedMultiplier float64            `json:"applied_multiplier"`
	FinalWeight       float64            `json:"final_weight"`
	Contribution      float64            `json:"contribution"`
	GenreMultipliers  map[string]float64 `json:"genre_multipliers,omitempty"`
	BiasResistant     bool               `json:"bias_resistant"`
	WeightOverride    bool               `json:"weight_override"`
	Skipped           bool               `json:"skipped"`
}

// sessionMetaResponse is the JSON representation of SessionMeta.
// swagger:model sessionMetaResponse
type sessionMetaResponse struct {
	MediaID       int      `json:"media_id"`
	TitleRomaji   string   `json:"title_romaji"`
	TitleEnglish  string   `json:"title_english,omitempty"`
	MediaType     string   `json:"media_type"`
	AniListURL    string   `json:"anilist_url"`
	AllGenres     []string `json:"all_genres"`
	MatchedGenres []string `json:"matched_genres"`
	ConfigHash    string   `json:"config_hash"`
}

// handleScore calculates a weighted score for the given media entry.
//
//	@Summary		Calculate score
//	@Description	Calculate a weighted, genre-adjusted score for a media entry.
//	@Tags			score
//	@Accept			json
//	@Produce		json
//	@Param			request	body		scoreRequest	true	"Scoring input"
//	@Success		200		{object}	scoreResponse
//	@Failure		400		{object}	errorResponse
//	@Failure		502		{object}	errorResponse
//	@Router			/score [post]
func (s *Server) handleScore(w http.ResponseWriter, r *http.Request) {
	var req scoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.MediaID == 0 {
		writeError(w, http.StatusBadRequest, "media_id is required")
		return
	}
	if len(req.Scores) == 0 {
		writeError(w, http.StatusBadRequest, "scores map is required and must not be empty")
		return
	}

	// Fetch media to get genres and title for provenance.
	media, err := s.al.FetchByID(req.MediaID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	skipped := make(map[string]bool, len(req.SkippedDimensions))
	for _, k := range req.SkippedDimensions {
		skipped[k] = true
	}
	if req.WeightOverrides == nil {
		req.WeightOverrides = map[string]float64{}
	}

	// Determine matched genres for session meta.
	matchedGenres := matchedGenres(media.Genres, s.cfg.Genres)

	entry := scoring.Entry{
		Scores:            req.Scores,
		SkippedDimensions: skipped,
		WeightOverrides:   req.WeightOverrides,
		Genres:            media.Genres,
		Meta: scoring.SessionMeta{
			MediaID:       media.ID,
			TitleRomaji:   media.TitleRomaji,
			TitleEnglish:  media.TitleEnglish,
			MediaType:     media.MediaType,
			AniListURL:    aniListURL(media),
			AllGenres:     media.Genres,
			MatchedGenres: matchedGenres,
			ConfigHash:    s.cfg.DimensionsHash,
		},
	}

	result, err := s.engine.Score(entry)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toScoreResponse(result))
}

// publishRequest is the request body for POST /score/publish.
// swagger:model publishRequest
type publishRequest struct {
	// MediaID is the AniList media ID to publish the score for.
	MediaID int `json:"media_id"`
	// Score is the final score to publish (e.g. 8.79).
	Score float64 `json:"score"`
}

// publishResponse is the response body for POST /score/publish.
// swagger:model publishResponse
type publishResponse struct {
	// Message is a human-readable confirmation.
	Message string `json:"message"`
	// Title is the media title as confirmed by AniList.
	Title string `json:"title"`
	// Score is the score that was written.
	Score float64 `json:"score"`
}

// handleScorePublish publishes a score to the user's AniList account.
//
//	@Summary		Publish score
//	@Description	Write a score to the user's AniList account. Requires ANILIST_TOKEN env var.
//	@Tags			score
//	@Accept			json
//	@Produce		json
//	@Param			request	body		publishRequest	true	"Publish input"
//	@Success		200		{object}	publishResponse
//	@Failure		400		{object}	errorResponse
//	@Failure		502		{object}	errorResponse
//	@Router			/score/publish [post]
func (s *Server) handleScorePublish(w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.MediaID == 0 {
		writeError(w, http.StatusBadRequest, "media_id is required")
		return
	}
	if req.Score < 1 || req.Score > 10 {
		writeError(w, http.StatusBadRequest, "score must be between 1 and 10")
		return
	}

	pub, err := s.al.PublishScore(req.MediaID, req.Score)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, publishResponse{
		Message: "score published to AniList",
		Title:   pub.TitleRomaji,
		Score:   pub.Score,
	})
}

// --- helpers ---

// writeJSON serialises v as JSON and writes it to w with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) { // interface{} required: generic JSON response helper
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck — write errors on closed connections are not actionable
}

// writeError writes a JSON error envelope with the given status and message.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// toMediaResponse converts an anilist.Media to a mediaResponse.
func toMediaResponse(m *anilist.Media) mediaResponse {
	return mediaResponse{
		ID:           m.ID,
		TitleRomaji:  m.TitleRomaji,
		TitleEnglish: m.TitleEnglish,
		TitleNative:  m.TitleNative,
		Format:       m.Format,
		Status:       m.Status,
		Episodes:     m.Episodes,
		Chapters:     m.Chapters,
		Genres:       m.Genres,
		CoverImage:   m.CoverImage,
		AverageScore: m.AverageScore,
		MeanScore:    m.MeanScore,
		MediaType:    string(m.MediaType),
		AniListURL:   aniListURL(m),
	}
}

// toScoreResponse converts a scoring.Result to a scoreResponse.
func toScoreResponse(r scoring.Result) scoreResponse {
	rows := make([]breakdownRowResponse, len(r.Breakdown))
	for i, row := range r.Breakdown {
		rows[i] = breakdownRowResponse{
			Key:               row.Key,
			Label:             row.Label,
			Score:             row.Score,
			BaseWeight:        row.BaseWeight,
			AppliedMultiplier: row.AppliedMultiplier,
			FinalWeight:       row.FinalWeight,
			Contribution:      row.Contribution,
			GenreMultipliers:  row.GenreMultipliers,
			BiasResistant:     row.BiasResistant,
			WeightOverride:    row.WeightOverride,
			Skipped:           row.Skipped,
		}
	}
	return scoreResponse{
		FinalScore: r.FinalScore,
		Breakdown:  rows,
		Meta: sessionMetaResponse{
			MediaID:       r.Meta.MediaID,
			TitleRomaji:   r.Meta.TitleRomaji,
			TitleEnglish:  r.Meta.TitleEnglish,
			MediaType:     string(r.Meta.MediaType),
			AniListURL:    r.Meta.AniListURL,
			AllGenres:     r.Meta.AllGenres,
			MatchedGenres: r.Meta.MatchedGenres,
			ConfigHash:    r.Meta.ConfigHash,
		},
	}
}

// aniListURL constructs the canonical AniList URL for a media entry.
func aniListURL(m *anilist.Media) string {
	t := "anime"
	if m.MediaType == "MANGA" {
		t = "manga"
	}
	return "https://anilist.co/" + t + "/" + strconv.Itoa(m.ID)
}

// matchedGenres returns the subset of genres that have a config entry (case-insensitive).
func matchedGenres(genres []string, configGenres map[string]map[string]float64) []string {
	matched := make([]string, 0, len(genres))
	for _, g := range genres {
		lower := toLower(g)
		if _, ok := configGenres[lower]; ok {
			matched = append(matched, g)
		}
	}
	return matched
}

// toLower is a dependency-free ASCII lowercase for genre keys.
func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
