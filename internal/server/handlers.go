package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

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
	BannerImage  string   `json:"banner_image,omitempty"`
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

// dimensionItem is the JSON representation of a single scoring dimension.
// swagger:model dimensionItem
type dimensionItem struct {
	// Key is the internal identifier used in the scores map of POST /score.
	Key string `json:"key"`
	// Label is the human-readable display name.
	Label string `json:"label"`
	// Description is the scoring hint shown to the user.
	Description string `json:"description"`
	// Weight is the base weight for this dimension (all weights sum to 1.0).
	Weight float64 `json:"weight"`
}

// dimensionsResponse is the response body for GET /dimensions.
// swagger:model dimensionsResponse
type dimensionsResponse struct {
	// ConfigHash is the SHA256 digest of the current dimensions config.
	// Clients can use this to detect when the dimension list has changed.
	ConfigHash string `json:"config_hash"`
	// Dimensions is the ordered list of scoring dimensions.
	Dimensions []dimensionItem `json:"dimensions"`
}

// handleDimensions returns the configured scoring dimensions in order.
//
//	@Summary		List scoring dimensions
//	@Description	Returns the ordered list of scoring dimensions defined in server config. Use the returned keys in the scores map when calling POST /score.
//	@Tags			score
//	@Produce		json
//	@Success		200	{object}	dimensionsResponse
//	@Router			/dimensions [get]
func (s *Server) handleDimensions(w http.ResponseWriter, r *http.Request) {
	items := make([]dimensionItem, 0, len(s.cfg.DimensionOrder))
	for _, key := range s.cfg.DimensionOrder {
		d := s.cfg.Dimensions[key]
		items = append(items, dimensionItem{
			Key:         key,
			Label:       d.Label,
			Description: d.Description,
			Weight:      d.Weight,
		})
	}
	writeJSON(w, http.StatusOK, dimensionsResponse{
		ConfigHash: s.cfg.DimensionsHash,
		Dimensions: items,
	})
}

// genreMultiplierItem is the JSON representation of a single genre's configured
// per-dimension multipliers.
// swagger:model genreMultiplierItem
type genreMultiplierItem struct {
	// Genre is the AniList genre name (lowercased).
	Genre string `json:"genre"`
	// Multipliers maps dimension keys to their configured multiplier values.
	Multipliers map[string]float64 `json:"multipliers"`
}

// genresResponse is the response body for GET /genres.
// swagger:model genresResponse
type genresResponse struct {
	// PrimaryGenreWeight is the configured blend ratio for primary genre support.
	PrimaryGenreWeight float64 `json:"primary_genre_weight"`
	// Genres is the list of configured genre multiplier blocks, sorted alphabetically.
	Genres []genreMultiplierItem `json:"genres"`
}

// handleGenres returns the configured genre multiplier table.
//
//	@Summary		List genre multipliers
//	@Description	Returns all configured genre multiplier blocks and the primary genre blend ratio.
//	@Tags			score
//	@Produce		json
//	@Success		200	{object}	genresResponse
//	@Router			/genres [get]
func (s *Server) handleGenres(w http.ResponseWriter, r *http.Request) {
	items := make([]genreMultiplierItem, 0, len(s.cfg.Genres))
	for genre, multipliers := range s.cfg.Genres {
		items = append(items, genreMultiplierItem{
			Genre:       genre,
			Multipliers: multipliers,
		})
	}
	// Sort for deterministic output.
	sort.Slice(items, func(i, j int) bool { return items[i].Genre < items[j].Genre })
	writeJSON(w, http.StatusOK, genresResponse{
		PrimaryGenreWeight: s.cfg.PrimaryGenreWeight,
		Genres:             items,
	})
}

// scoreRequest is the request body for POST /score.
// swagger:model scoreRequest
type scoreRequest struct {
	// MediaID is the AniList media ID of the entry being scored.
	MediaID int `json:"media_id"`
	// Scores maps dimension keys to user scores (1.0–10.0).
	// Any configured dimension absent from this map is treated as skipped (N/A).
	Scores map[string]float64 `json:"scores"`
	// WeightOverrides maps dimension keys to per-session weight overrides.
	// Optional — omit to use the weights defined in server config.
	WeightOverrides map[string]float64 `json:"weight_overrides,omitempty"`
	// SelectedGenres, if non-empty, restricts multiplier calculation to this
	// subset of the media's AniList genres. Genres absent from this list but
	// present in config are recorded as deselected in the breakdown. When
	// omitted, all matched config genres participate (CLI-compatible behaviour).
	SelectedGenres []string `json:"selected_genres,omitempty"`
	// PrimaryGenre designates one of the media's genres as constitutive for
	// blended multiplier calculation. Must match one of the active genres
	// (case-insensitive). Optional — omit to use contributing-only averaging with no primary.
	PrimaryGenre string `json:"primary_genre,omitempty" example:"Mystery"`
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
	Key                    string  `json:"key"`
	Label                  string  `json:"label"`
	Score                  float64 `json:"score"`
	BaseWeight             float64 `json:"base_weight"`
	AppliedMultiplier      float64 `json:"applied_multiplier"`
	EffectiveWeight        float64 `json:"effective_weight"`
	FinalWeight            float64 `json:"final_weight"`
	Contribution           float64 `json:"contribution"`
	BiasResistant          bool    `json:"bias_resistant"`
	WeightOverride         bool    `json:"weight_override"`
	Skipped                bool    `json:"skipped"`
	GenreDeselected        bool    `json:"genre_deselected,omitempty"`
	PrimaryGenre           string  `json:"primary_genre,omitempty"`
	PrimaryGenreMultiplier float64 `json:"primary_genre_multiplier,omitempty"`
}

// sessionMetaResponse is the JSON representation of SessionMeta.
// swagger:model sessionMetaResponse
type sessionMetaResponse struct {
	MediaID            int      `json:"media_id"`
	TitleRomaji        string   `json:"title_romaji"`
	TitleEnglish       string   `json:"title_english,omitempty"`
	MediaType          string   `json:"media_type"`
	AniListURL         string   `json:"anilist_url"`
	AllGenres          []string `json:"all_genres"`
	MatchedGenres      []string `json:"matched_genres"`
	GenresActive       []string `json:"genres_active,omitempty"`
	ConfigHash         string   `json:"config_hash"`
	PrimaryGenre         string  `json:"primary_genre,omitempty"`
	PrimaryGenreWeight   float64 `json:"primary_genre_weight"`
	EffectiveWeightSum   float64 `json:"effective_weight_sum"`
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

	// Any configured dimension absent from req.Scores is implicitly skipped.
	skipped := make(map[string]bool)
	for _, key := range s.cfg.DimensionOrder {
		if _, ok := req.Scores[key]; !ok {
			skipped[key] = true
		}
	}
	if req.WeightOverrides == nil {
		req.WeightOverrides = map[string]float64{}
	}

	// Validate primary_genre: when selected_genres are provided, the primary must
	// be in that set; otherwise it must be in the full AniList genre list.
	if req.PrimaryGenre != "" {
		validationSet := media.Genres
		if len(req.SelectedGenres) > 0 {
			validationSet = req.SelectedGenres
		}
		found := false
		primaryLower := strings.ToLower(req.PrimaryGenre)
		for _, g := range validationSet {
			if strings.ToLower(g) == primaryLower {
				found = true
				break
			}
		}
		if !found {
			writeError(w, http.StatusBadRequest, "primary_genre "+req.PrimaryGenre+" is not in the active genre set")
			return
		}
	}

	entry := scoring.Entry{
		Scores:             req.Scores,
		SkippedDimensions:  skipped,
		WeightOverrides:    req.WeightOverrides,
		Genres:             media.Genres,
		UserSelectedGenres: req.SelectedGenres,
		PrimaryGenre:       req.PrimaryGenre,
		Meta: scoring.SessionMeta{
			MediaID:            media.ID,
			TitleRomaji:        media.TitleRomaji,
			TitleEnglish:       media.TitleEnglish,
			MediaType:          media.MediaType,
			AniListURL:         aniListURL(media),
			AllGenres:          media.Genres,
			ConfigHash:         s.cfg.DimensionsHash,
			PrimaryGenre:       req.PrimaryGenre,
			PrimaryGenreWeight: s.cfg.PrimaryGenreWeight,
		},
	}

	result, err := s.engine.Score(entry)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, toScoreResponse(result))
}

// weightsRequest is the request body for POST /weights.
// swagger:model weightsRequest
type weightsRequest struct {
	// MediaID is the AniList media ID. Used to fetch the media's genre list.
	MediaID int `json:"media_id"`
	// SelectedGenres, if non-empty, restricts the active genre set for weight
	// calculation. When omitted, all matched config genres participate.
	SelectedGenres []string `json:"selected_genres,omitempty"`
	// PrimaryGenre designates one genre as constitutive for blended multiplier
	// calculation. Must be present in SelectedGenres when that field is provided,
	// otherwise must be in the media's AniList genre list.
	PrimaryGenre string `json:"primary_genre,omitempty" example:"Mystery"`
	// SkippedDimensions maps dimension keys to true to exclude them from the
	// weight pool before renormalization. Optional.
	SkippedDimensions map[string]bool `json:"skipped_dimensions,omitempty"`
	// WeightOverrides maps dimension keys to per-session weight overrides.
	// Optional — omit to use config weights.
	WeightOverrides map[string]float64 `json:"weight_overrides,omitempty"`
}

// weightDimensionRow is a single row in the POST /weights response.
// swagger:model weightDimensionRow
type weightDimensionRow struct {
	// Key is the dimension's config key.
	Key string `json:"key"`
	// Label is the human-readable display name.
	Label string `json:"label"`
	// BaseWeight is the configured weight before genre adjustment.
	BaseWeight float64 `json:"base_weight"`
	// Multiplier is the blended genre multiplier applied (1.0 when bias-resistant or no genre opinion).
	Multiplier float64 `json:"multiplier"`
	// EffectiveWeight is BaseWeight × Multiplier before renormalization.
	EffectiveWeight float64 `json:"effective_weight"`
	// FinalWeight is the weight after genre adjustment, renormalization, and overrides.
	FinalWeight float64 `json:"final_weight"`
	// Skipped indicates this dimension is excluded from the weight pool.
	Skipped bool `json:"skipped"`
	// BiasResistant indicates this dimension's multiplier is always 1.0.
	BiasResistant bool `json:"bias_resistant"`
	// WeightOverride indicates the final weight was set by a weight override.
	WeightOverride bool `json:"weight_override,omitempty"`
	// PrimaryGenreMultiplier is the raw multiplier the primary genre defines for this
	// dimension. 0 when no primary genre is set or the dimension is bias-resistant.
	PrimaryGenreMultiplier float64 `json:"primary_genre_multiplier,omitempty"`
}

// weightsResponse is the response body for POST /weights.
// swagger:model weightsResponse
type weightsResponse struct {
	// PrimaryGenreWeight is the blend ratio applied when a primary genre is set (0–1).
	// A value of 0.6 means 60 % primary, 40 % secondary average.
	PrimaryGenreWeight float64 `json:"primary_genre_weight"`
	// EffectiveWeightSum is the sum of all per-dimension effective weights before renormalization.
	// Dividing any dimension's effective_weight by this value reproduces its final_weight.
	EffectiveWeightSum float64 `json:"effective_weight_sum"`
	// Dimensions is the ordered list of per-dimension weight rows.
	Dimensions []weightDimensionRow `json:"dimensions"`
}

// handleWeights computes per-dimension final weights without requiring scores.
// Used by the web UI for live weight preview when the user adjusts genre selection.
//
//	@Summary		Preview dimension weights
//	@Description	Compute per-dimension final weights for a given genre/override configuration without scoring. Used for live weight preview in the web UI.
//	@Tags			score
//	@Accept			json
//	@Produce		json
//	@Param			request	body		weightsRequest	true	"Weight preview input"
//	@Success		200		{object}	weightsResponse
//	@Failure		400		{object}	errorResponse
//	@Failure		502		{object}	errorResponse
//	@Router			/weights [post]
func (s *Server) handleWeights(w http.ResponseWriter, r *http.Request) {
	var req weightsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.MediaID == 0 {
		writeError(w, http.StatusBadRequest, "media_id is required")
		return
	}

	// Validate weight overrides.
	for key, v := range req.WeightOverrides {
		if _, ok := s.cfg.Dimensions[key]; !ok {
			writeError(w, http.StatusBadRequest, "unknown dimension in weight_overrides: "+key)
			return
		}
		if v <= 0 || v > 1 {
			writeError(w, http.StatusBadRequest, "weight_overrides value for "+key+" must be > 0 and ≤ 1")
			return
		}
	}
	overrideSum := 0.0
	for _, v := range req.WeightOverrides {
		overrideSum += v
	}
	if overrideSum >= 1.0 {
		writeError(w, http.StatusBadRequest, "sum of weight_overrides must be < 1.0")
		return
	}

	media, err := s.al.FetchByID(req.MediaID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Determine active genre source.
	genreSource := media.Genres
	if len(req.SelectedGenres) > 0 {
		genreSource = req.SelectedGenres
	}

	// Validate primary_genre against the active genre set.
	if req.PrimaryGenre != "" {
		validationSet := media.Genres
		if len(req.SelectedGenres) > 0 {
			validationSet = req.SelectedGenres
		}
		found := false
		primaryLower := strings.ToLower(req.PrimaryGenre)
		for _, g := range validationSet {
			if strings.ToLower(g) == primaryLower {
				found = true
				break
			}
		}
		if !found {
			writeError(w, http.StatusBadRequest, "primary_genre "+req.PrimaryGenre+" is not in the active genre set")
			return
		}
	}

	rows := s.engine.Weights(genreSource, req.PrimaryGenre, req.SkippedDimensions, req.WeightOverrides)

	dimRows := make([]weightDimensionRow, len(rows))
	for i, wr := range rows {
		dimRows[i] = weightDimensionRow{
			Key:                    wr.Key,
			Label:                  wr.Label,
			BaseWeight:             wr.BaseWeight,
			Multiplier:             wr.Multiplier,
			EffectiveWeight:        wr.EffectiveWeight,
			FinalWeight:            wr.FinalWeight,
			Skipped:                wr.Skipped,
			BiasResistant:          wr.BiasResistant,
			WeightOverride:         wr.WeightOverride,
			PrimaryGenreMultiplier: wr.PrimaryGenreMultiplier,
		}
	}

	effectiveSum := 0.0
	for _, wr := range rows {
		effectiveSum += wr.EffectiveWeight
	}

	writeJSON(w, http.StatusOK, weightsResponse{
		PrimaryGenreWeight: s.cfg.PrimaryGenreWeight,
		EffectiveWeightSum: effectiveSum,
		Dimensions:         dimRows,
	})
}

// publishRequest is the request body for POST /score/publish.
// swagger:model publishRequest
type publishRequest struct {
	// MediaID is the AniList media ID to publish the score for.
	MediaID int `json:"media_id"`
	// Score is the final score to publish (e.g. 8.79).
	Score float64 `json:"score"`
	// Notes is an optional pre-formatted scoring breakdown to append to the
	// AniList list entry notes. If the entry already has notes, the new block
	// is appended after a "---" separator so existing content is preserved.
	// Omit or leave empty to skip writing notes entirely.
	Notes string `json:"notes,omitempty" example:"Frieren: Beyond Journey's End\nScore: 9.73 / 10  [kansou]"`
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

	pub, err := s.al.PublishScore(req.MediaID, req.Score, req.Notes)
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
func writeJSON(w http.ResponseWriter, status int, v any) {
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
		BannerImage:  m.BannerImage,
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
			Key:                    row.Key,
			Label:                  row.Label,
			Score:                  row.Score,
			BaseWeight:             row.BaseWeight,
			AppliedMultiplier:      row.AppliedMultiplier,
			EffectiveWeight:        row.EffectiveWeight,
			FinalWeight:            row.FinalWeight,
			Contribution:           row.Contribution,
			BiasResistant:          row.BiasResistant,
			WeightOverride:         row.WeightOverride,
			Skipped:                row.Skipped,
			GenreDeselected:        row.GenreDeselected,
			PrimaryGenre:           row.PrimaryGenre,
			PrimaryGenreMultiplier: row.PrimaryGenreMultiplier,
		}
	}
	return scoreResponse{
		FinalScore: r.FinalScore,
		Breakdown:  rows,
		Meta: sessionMetaResponse{
			MediaID:            r.Meta.MediaID,
			TitleRomaji:        r.Meta.TitleRomaji,
			TitleEnglish:       r.Meta.TitleEnglish,
			MediaType:          string(r.Meta.MediaType),
			AniListURL:         r.Meta.AniListURL,
			AllGenres:          r.Meta.AllGenres,
			MatchedGenres:      r.Meta.MatchedGenres,
			GenresActive:       r.Meta.GenresActive,
			ConfigHash:         r.Meta.ConfigHash,
			PrimaryGenre:       r.Meta.PrimaryGenre,
			PrimaryGenreWeight: r.Meta.PrimaryGenreWeight,
			EffectiveWeightSum: r.Meta.EffectiveWeightSum,
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
