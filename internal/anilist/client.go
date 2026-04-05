package anilist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	apiEndpoint = "https://graphql.anilist.co"
	tokenEnvVar = "ANILIST_TOKEN"
)

// Client is a thin net/http wrapper for the AniList GraphQL API.
// Construct it with NewClient. The token is read from the environment once
// at construction time and is never exposed after that.
type Client struct {
	http  *http.Client
	token string // empty for read-only operations; required for publish
}

// NewClient constructs an AniList client. It does not require the token to
// be set at construction — the token is only needed for PublishScore.
// The token is read from ANILIST_TOKEN at construction time so that a missing
// token is discovered early rather than at publish time.
func NewClient() *Client {
	return &Client{
		http:  &http.Client{Timeout: 15 * time.Second},
		token: os.Getenv(tokenEnvVar),
	}
}

// SearchByNameMulti searches AniList for media matching the given string and
// returns up to searchPageSize results sorted by relevance. This allows the
// caller to present a picker when multiple seasons or related entries exist.
// mediaType may be "ANIME", "MANGA", or "" to search all types.
// Returns an error if the network is unreachable, AniList returns an error,
// or no results are found.
func (c *Client) SearchByNameMulti(search, mediaType string) ([]Media, error) {
	vars := map[string]any{ // interface{} required: GraphQL variables are heterogeneous
		"search":  search,
		"perPage": searchPageSize,
	}
	if mediaType != "" {
		vars["type"] = mediaType
	}

	slog.Debug("anilist: search", "query", search, "type", mediaType)
	resp, err := c.do(searchPageQuery, vars, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Data struct {
			Page struct {
				Media []*gqlMedia `json:"media"`
			} `json:"Page"`
		} `json:"data"`
		Errors []gqlError `json:"errors"`
	}
	if err := decodeResponse(resp, &result); err != nil {
		return nil, err
	}
	if err := checkErrors(result.Errors); err != nil {
		return nil, err
	}
	if len(result.Data.Page.Media) == 0 {
		return nil, fmt.Errorf("no results found for %q — try a different search term or use --url to provide a direct AniList link", search)
	}

	media := make([]Media, len(result.Data.Page.Media))
	for i, m := range result.Data.Page.Media {
		media[i] = *m.toMedia()
	}
	slog.Debug("anilist: search results", "count", len(media), "query", search)
	return media, nil
}

// FetchByID retrieves a media entry by its AniList ID.
// Returns an error if the network is unreachable or AniList returns an error.
func (c *Client) FetchByID(id int) (*Media, error) {
	vars := map[string]any{ // interface{} required: GraphQL variables are heterogeneous
		"id": id,
	}

	slog.Debug("anilist: fetch by id", "id", id)
	resp, err := c.do(fetchByIDQuery, vars, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Data struct {
			Media *gqlMedia `json:"Media"`
		} `json:"data"`
		Errors []gqlError `json:"errors"`
	}
	if err := decodeResponse(resp, &result); err != nil {
		return nil, err
	}
	if err := checkErrors(result.Errors); err != nil {
		return nil, err
	}
	if result.Data.Media == nil {
		return nil, fmt.Errorf("no media found for AniList ID %d", id)
	}
	return result.Data.Media.toMedia(), nil
}

// ParseMediaURL extracts the AniList media ID from a URL of the form
// https://anilist.co/{type}/{id} or https://anilist.co/{type}/{id}/{slug}.
// Returns an error if the URL cannot be parsed or does not contain a valid ID.
func ParseMediaURL(rawURL string) (int, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, fmt.Errorf("parsing URL: %w", err)
	}
	if !strings.Contains(u.Host, "anilist.co") {
		return 0, fmt.Errorf("URL does not point to anilist.co: %q", rawURL)
	}
	// Path: /{type}/{id} or /{type}/{id}/{slug}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return 0, fmt.Errorf("cannot extract media ID from URL %q — expected format: https://anilist.co/{type}/{id}", rawURL)
	}
	id, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("media ID %q in URL is not a valid integer", parts[1])
	}
	return id, nil
}

// PublishScore writes the given score for mediaID to the user's AniList account.
// If notes is non-empty, the existing list entry notes are fetched first and the
// new scoring block is appended before saving, so prior notes are preserved.
// Requires ANILIST_TOKEN to be set; returns an error if it is missing or empty.
func (c *Client) PublishScore(mediaID int, score float64, notes string) (*PublishResult, error) {
	if c.token == "" {
		return nil, fmt.Errorf(
			"ANILIST_TOKEN environment variable is not set\n       set it with: export ANILIST_TOKEN=your_token_here\n       see docs/ANILIST_INTEGRATION.md for how to obtain a token",
		)
	}

	vars := map[string]any{ // interface{} required: GraphQL variables are heterogeneous
		"mediaId": mediaID,
		"score":   score,
	}
	mutation := publishMutation

	if notes != "" {
		existing := c.fetchExistingNotes(mediaID)
		combined := notes
		if existing != "" {
			combined = existing + "\n\n---\n\n" + notes
		}
		vars["notes"] = combined
		mutation = publishWithNotesMutation
	}

	slog.Debug("anilist: publishing score", "media_id", mediaID, "score", score, "with_notes", notes != "")
	resp, err := c.do(mutation, vars, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Data struct {
			SaveMediaListEntry *gqlListEntry `json:"SaveMediaListEntry"`
		} `json:"data"`
		Errors []gqlError `json:"errors"`
	}
	if err := decodeResponse(resp, &result); err != nil {
		return nil, err
	}
	if err := checkErrors(result.Errors); err != nil {
		return nil, err
	}
	if result.Data.SaveMediaListEntry == nil {
		return nil, fmt.Errorf("AniList returned no entry after publish — score may not have been saved")
	}
	e := result.Data.SaveMediaListEntry
	return &PublishResult{
		EntryID:     e.ID,
		Score:       e.Score,
		Status:      e.Status,
		TitleRomaji: e.Media.Title.Romaji,
	}, nil
}

// fetchExistingNotes retrieves the authenticated user's current list entry notes
// for mediaID. Returns an empty string if the entry has no notes, is not on the
// user's list, or if the request fails — note fetching failure must not block publishing.
func (c *Client) fetchExistingNotes(mediaID int) string {
	vars := map[string]any{ // interface{} required: GraphQL variables are heterogeneous
		"mediaId": mediaID,
	}
	resp, err := c.do(fetchListEntryNotesQuery, vars, true)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var result struct {
		Data struct {
			Media struct {
				MediaListEntry *struct {
					Notes string `json:"notes"`
				} `json:"mediaListEntry"`
			} `json:"Media"`
		} `json:"data"`
		Errors []gqlError `json:"errors"`
	}
	if err := decodeResponse(resp, &result); err != nil {
		return ""
	}
	if result.Data.Media.MediaListEntry == nil {
		return ""
	}
	return result.Data.Media.MediaListEntry.Notes
}

// do executes a GraphQL request. If withAuth is true, the Authorization header
// is included. The caller is responsible for closing resp.Body.
func (c *Client) do(query string, variables map[string]any, withAuth bool) (*http.Response, error) {
	body, err := json.Marshal(map[string]any{ // interface{} required: GraphQL request body is heterogeneous
		"query":     query,
		"variables": variables,
	})
	if err != nil {
		return nil, fmt.Errorf("marshalling GraphQL request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, apiEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building AniList request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if withAuth {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("AniList is currently unreachable: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("AniList returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

// decodeResponse decodes a JSON response body into dst.
// dst must be a pointer to a struct with an Errors []gqlError field at the top level.
// The caller must call checkErrors on the decoded Errors slice separately.
func decodeResponse(resp *http.Response, dst any) error { // interface{} required: generic JSON decode target
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decoding AniList response: %w", err)
	}
	return nil
}

// checkErrors returns the first GraphQL error in errs, or nil if there are none.
func checkErrors(errs []gqlError) error {
	if len(errs) > 0 {
		return fmt.Errorf("AniList error: %s", errs[0].Message)
	}
	return nil
}

// --- Internal GraphQL response shapes ---

// gqlError is the standard AniList error object in a GraphQL response.
type gqlError struct {
	Message string `json:"message"`
	Status  int    `json:"status"`
}

// gqlMedia is the raw GraphQL Media response shape.
type gqlMedia struct {
	ID    int `json:"id"`
	Title struct {
		Romaji  string `json:"romaji"`
		English string `json:"english"`
		Native  string `json:"native"`
	} `json:"title"`
	Format       string   `json:"format"`
	Status       string   `json:"status"`
	Episodes     int      `json:"episodes"`
	Chapters     int      `json:"chapters"`
	Genres       []string `json:"genres"`
	Tags         []struct {
		Name           string `json:"name"`
		Rank           int    `json:"rank"`
		IsMediaSpoiler bool   `json:"isMediaSpoiler"`
	} `json:"tags"`
	CoverImage struct {
		ExtraLarge string `json:"extraLarge"`
	} `json:"coverImage"`
	BannerImage  string `json:"bannerImage"`
	AverageScore int    `json:"averageScore"`
	MeanScore    int `json:"meanScore"`
}

// toMedia converts a gqlMedia response to our domain Media type.
func (g *gqlMedia) toMedia() *Media {
	tags := make([]Tag, len(g.Tags))
	for i, t := range g.Tags {
		tags[i] = Tag{
			Name:           t.Name,
			Rank:           t.Rank,
			IsMediaSpoiler: t.IsMediaSpoiler,
		}
	}
	return &Media{
		ID:           g.ID,
		TitleRomaji:  g.Title.Romaji,
		TitleEnglish: g.Title.English,
		TitleNative:  g.Title.Native,
		Format:       g.Format,
		Status:       g.Status,
		Episodes:     g.Episodes,
		Chapters:     g.Chapters,
		Genres:       g.Genres,
		Tags:         tags,
		CoverImage:   g.CoverImage.ExtraLarge,
		BannerImage:  g.BannerImage,
		AverageScore: g.AverageScore,
		MeanScore:    g.MeanScore,
		MediaType:    mediaTypeFromFormat(g.Format),
	}
}

// gqlListEntry is the raw GraphQL SaveMediaListEntry response shape.
type gqlListEntry struct {
	ID     int     `json:"id"`
	Score  float64 `json:"score"`
	Status string  `json:"status"`
	Media  struct {
		Title struct {
			Romaji string `json:"romaji"`
		} `json:"title"`
	} `json:"media"`
}
