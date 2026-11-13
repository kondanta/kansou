package server

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kondanta/kansou/internal/anilist"
	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/store"
	"github.com/kondanta/kansou/internal/store/sqlite"
)

// apiConfig is a 3-dimension config with two genre blocks. "fun" is bias
// resistant. Weights sum to 1.0.
func apiConfig() *config.Config {
	return &config.Config{
		DimensionOrder: []string{"story", "fun", "art"},
		Dimensions: map[string]config.DimensionDef{
			"story": {Label: "Story", Description: "Plot", Weight: 0.5},
			"fun":   {Label: "Fun", Description: "Enjoyment", Weight: 0.3, BiasResistant: true},
			"art":   {Label: "Art", Description: "Visuals", Weight: 0.2},
		},
		Genres: map[string]map[string]float64{
			"mystery": {"story": 1.5},
			"action":  {"art": 1.4, "story": 0.8},
		},
		PrimaryGenreWeight: 0.6,
		MaxMultiplier:      2.0,
		MaxHistory:         -1,
		DimensionsHash:     "dimhash",
		Server:             config.ServerConfig{Port: config.DefaultPort},
	}
}

// aniListCall is one request the fake AniList received.
type aniListCall struct {
	Query string
	Vars  map[string]any
}

// baseTransport is a private copy of the real default transport, captured
// before any test swaps http.DefaultTransport.
var baseTransport = http.DefaultTransport.(*http.Transport).Clone()

// swapTransport redirects every outgoing request to a test server.
type swapTransport struct{ target *url.URL }

func (s swapTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = s.target.Scheme, s.target.Host
	return baseTransport.RoundTrip(r)
}

// fakeAniList returns an anilist.Client (token set unless token == "") whose
// requests are answered by handler. anilist.NewClient leaves the HTTP
// transport nil, i.e. http.DefaultTransport, so we swap that for the test's
// duration; these tests therefore must not run in parallel.
func fakeAniList(
	t *testing.T, token string, handler func(aniListCall) (int, string),
) (*anilist.Client, *[]aniListCall) {
	t.Helper()
	var calls []aniListCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		c := aniListCall{Query: body.Query, Vars: body.Variables}
		calls = append(calls, c)
		code, resp := handler(c)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)

	u, _ := url.Parse(srv.URL)
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })
	http.DefaultTransport = swapTransport{target: u}

	t.Setenv("ANILIST_TOKEN", token)
	return anilist.NewClient(), &calls
}

const frierenMedia = `{"id":154587,"title":{"romaji":"Sousou no Frieren","english":"Frieren","native":"n"},
"format":"TV","status":"FINISHED","episodes":28,"chapters":0,"genres":["Mystery","Action","Romance"],
"tags":[],"coverImage":{"extraLarge":"https://img/c.png"},"bannerImage":"","averageScore":91,"meanScore":89}`

// mediaHandler answers every media lookup with Frieren.
func mediaHandler(aniListCall) (int, string) {
	return 200, `{"data":{"Media":` + frierenMedia + `,"Page":{"media":[` + frierenMedia + `]}}}`
}

func apiServer(t *testing.T, al *anilist.Client, st store.Store) *Server {
	t.Helper()
	cfg := apiConfig()
	dbType := ""
	if st != nil {
		dbType = "sqlite"
	}
	return New(cfg, al, minimalEngine(cfg), false, "", st, dbType, nil, false)
}

func send(t *testing.T, s *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	switch b := body.(type) {
	case nil:
		r = httptest.NewRequest(method, path, nil)
	case string:
		r = httptest.NewRequest(method, path, strings.NewReader(b))
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = httptest.NewRequest(method, path, bytes.NewReader(raw))
	}
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, r)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body.String(), err)
	}
	return v
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestDimensionsAndGenres(t *testing.T) {
	s := apiServer(t, nil, nil)

	dims := decode[dimensionsResponse](t, send(t, s, "GET", "/api/v1/dimensions", nil))
	if dims.ConfigHash != "dimhash" || len(dims.Dimensions) != 3 {
		t.Fatalf("dimensions = %+v", dims)
	}
	for i, want := range []dimensionItem{
		{"story", "Story", "Plot", 0.5}, {"fun", "Fun", "Enjoyment", 0.3}, {"art", "Art", "Visuals", 0.2},
	} {
		if dims.Dimensions[i] != want {
			t.Errorf("dimension[%d] = %+v, want %+v (config order)", i, dims.Dimensions[i], want)
		}
	}

	genres := decode[genresResponse](t, send(t, s, "GET", "/api/v1/genres", nil))
	if genres.PrimaryGenreWeight != 0.6 || len(genres.Genres) != 2 ||
		genres.Genres[0].Genre != "action" || genres.Genres[1].Genre != "mystery" {
		t.Fatalf("genres = %+v, want action then mystery (sorted)", genres)
	}
	if genres.Genres[0].Multipliers["art"] != 1.4 || genres.Genres[1].Multipliers["story"] != 1.5 {
		t.Errorf("multipliers = %+v", genres.Genres)
	}
}

func TestMediaSearch(t *testing.T) {
	al, calls := fakeAniList(t, "", mediaHandler)
	s := apiServer(t, al, nil)

	rec := send(t, s, "GET", "/api/v1/media/search?q=frieren&type=ANIME", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	got := decode[[]mediaResponse](t, rec)
	if len(got) != 1 || got[0].ID != 154587 || got[0].MediaType != "ANIME" ||
		got[0].AniListURL != "https://anilist.co/anime/154587" || got[0].CoverImage != "https://img/c.png" {
		t.Fatalf("media = %+v", got)
	}
	// Genres are lowercased by the client and flagged against the user's config.
	wantGenres := map[string]bool{"mystery": true, "action": true, "romance": false}
	for _, g := range got[0].Genres {
		if wantGenres[g.Name] != g.IsConfigured {
			t.Errorf(
				"genre %s configured = %v, want %v",
				g.Name,
				g.IsConfigured,
				wantGenres[g.Name],
			)
		}
	}
	if v := (*calls)[0].Vars; v["search"] != "frieren" || v["type"] != "ANIME" {
		t.Errorf("AniList vars = %v", v)
	}
}

func TestMediaSearch_Errors(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		handler  func(aniListCall) (int, string)
		wantCode int
		wantMsg  string
		notMsg   string
	}{
		{"missing q", "/api/v1/media/search", mediaHandler, 400, "'q' is required", ""},
		{
			"no results", "/api/v1/media/search?q=zzz",
			func(aniListCall) (int, string) { return 200, `{"data":{"Page":{"media":[]}}}` },
			502, "no results found", "",
		},
		{
			"upstream outage is generic", "/api/v1/media/search?q=x",
			func(aniListCall) (int, string) { return 500, "secret internal detail" },
			502, "AniList is currently unavailable", "secret internal detail",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			al, _ := fakeAniList(t, "", tt.handler)
			rec := send(t, apiServer(t, al, nil), "GET", tt.path, nil)
			msg := decode[errorResponse](t, rec).Error
			if rec.Code != tt.wantCode || !strings.Contains(msg, tt.wantMsg) {
				t.Errorf("got %d %q, want %d containing %q", rec.Code, msg, tt.wantCode, tt.wantMsg)
			}
			if tt.notMsg != "" && strings.Contains(rec.Body.String(), tt.notMsg) {
				t.Errorf("response leaks upstream detail: %s", rec.Body)
			}
		})
	}
}

func TestMediaFetch(t *testing.T) {
	al, calls := fakeAniList(t, "", mediaHandler)
	s := apiServer(t, al, nil)

	rec := send(t, s, "GET", "/api/v1/media/154587", nil)
	got := decode[mediaResponse](t, rec)
	if rec.Code != 200 || got.ID != 154587 || got.TitleEnglish != "Frieren" ||
		len(got.Genres) != 3 {
		t.Fatalf("code %d media %+v", rec.Code, got)
	}
	if (*calls)[0].Vars["id"] != float64(154587) {
		t.Errorf("AniList vars = %v", (*calls)[0].Vars)
	}

	if rec := send(t, s, "GET", "/api/v1/media/abc", nil); rec.Code != 400 ||
		!strings.Contains(rec.Body.String(), "must be an integer") {
		t.Errorf("non-numeric id: %d %s", rec.Code, rec.Body)
	}

	notFound, _ := fakeAniList(t, "", func(aniListCall) (int, string) {
		return 200, `{"data":{"Media":null}}`
	})
	if rec := send(
		t,
		apiServer(t, notFound, nil),
		"GET",
		"/api/v1/media/9",
		nil,
	); rec.Code != 502 ||
		!strings.Contains(rec.Body.String(), "no media found for AniList ID 9") {
		t.Errorf("unknown id: %d %s", rec.Code, rec.Body)
	}
}

// Hand-computed golden request. Genres mystery+action (romance is not in
// config), scores story 9, fun 7.5, art 8:
//
//	story  multiplier (1.5+0.8)/2 = 1.15 → effective 0.5×1.15 = 0.575
//	fun    bias resistant → 1.0           → effective 0.3
//	art    only action has an opinion → 1.4 → effective 0.2×1.4 = 0.28
//	sum = 1.155; final weights 0.575/1.155, 0.3/1.155, 0.28/1.155
//	score = 9×0.497835… + 7.5×0.259740… + 8×0.242424… = 8.3679… → 8.37
func TestScore_Golden(t *testing.T) {
	al, calls := fakeAniList(t, "", mediaHandler)
	s := apiServer(t, al, nil)
	rec := send(t, s, "POST", "/api/v1/score", scoreRequest{
		MediaID: 154587, Scores: map[string]float64{"story": 9, "fun": 7.5, "art": 8},
	})
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body)
	}
	got := decode[scoreResponse](t, rec)
	if got.FinalScore != 8.37 {
		t.Errorf("final score = %v, want 8.37", got.FinalScore)
	}
	wantMult := map[string]float64{"story": 1.15, "fun": 1.0, "art": 1.4}
	wantFinal := map[string]float64{"story": 0.575 / 1.155, "fun": 0.3 / 1.155, "art": 0.28 / 1.155}
	if len(got.Breakdown) != 3 || got.Breakdown[0].Key != "story" || got.Breakdown[2].Key != "art" {
		t.Fatalf("breakdown order = %+v", got.Breakdown)
	}
	for _, row := range got.Breakdown {
		if !near(row.AppliedMultiplier, wantMult[row.Key]) ||
			!near(row.FinalWeight, wantFinal[row.Key]) {
			t.Errorf(
				"%s: multiplier %v weight %v, want %v %v",
				row.Key,
				row.AppliedMultiplier,
				row.FinalWeight,
				wantMult[row.Key],
				wantFinal[row.Key],
			)
		}
	}
	m := got.Meta
	if !near(m.EffectiveWeightSum, 1.155) || m.MediaID != 154587 ||
		m.TitleRomaji != "Sousou no Frieren" ||
		m.MediaType != "ANIME" ||
		m.ConfigHash != "dimhash" ||
		m.AniListURL != "https://anilist.co/anime/154587" ||
		strings.Join(m.MatchedGenres, ",") != "action,mystery" ||
		len(m.AllGenres) != 3 {
		t.Errorf("meta = %+v", m)
	}
	if (*calls)[0].Vars["id"] != float64(154587) {
		t.Errorf("media fetched with %v", (*calls)[0].Vars)
	}
}

func TestScore_OmittedDimensionIsSkipped(t *testing.T) {
	al, _ := fakeAniList(t, "", mediaHandler)
	rec := send(t, apiServer(t, al, nil), "POST", "/api/v1/score", scoreRequest{
		MediaID: 1, Scores: map[string]float64{"story": 9, "fun": 7},
	})
	got := decode[scoreResponse](t, rec)
	for _, row := range got.Breakdown {
		if row.Key == "art" && (!row.Skipped || row.FinalWeight != 0 || row.Contribution != 0) {
			t.Errorf("art not skipped: %+v", row)
		}
	}
	var sum float64
	for _, row := range got.Breakdown {
		sum += row.FinalWeight
	}
	if !near(sum, 1) {
		t.Errorf("final weights sum to %v, want 1 after skip", sum)
	}
}

func TestScore_PrimaryAndSelectedGenres(t *testing.T) {
	al, _ := fakeAniList(t, "", mediaHandler)
	s := apiServer(t, al, nil)
	scores := map[string]float64{"story": 9, "fun": 7.5, "art": 8}

	// Primary genre matches case-insensitively and is echoed in the response.
	rec := send(t, s, "POST", "/api/v1/score", scoreRequest{
		MediaID: 1, Scores: scores, PrimaryGenre: "MYSTERY",
	})
	got := decode[scoreResponse](t, rec)
	if rec.Code != 200 || got.Meta.PrimaryGenre != "MYSTERY" || got.Meta.PrimaryGenreWeight != 0.6 {
		t.Fatalf("primary: %d %+v", rec.Code, got.Meta)
	}

	// Selecting only "mystery" deselects "action": its art/story opinions are
	// flagged as deselected and no longer shape the multipliers.
	rec = send(t, s, "POST", "/api/v1/score", scoreRequest{
		MediaID: 1, Scores: scores, SelectedGenres: []string{"mystery"},
	})
	got = decode[scoreResponse](t, rec)
	if rec.Code != 200 || strings.Join(got.Meta.GenresActive, ",") != "mystery" {
		t.Fatalf("selected: %d %+v", rec.Code, got.Meta)
	}
	for _, row := range got.Breakdown {
		want := map[string]struct {
			mult  float64
			desel bool
		}{"story": {1.5, true}, "fun": {1.0, false}, "art": {1.0, true}}[row.Key]
		if !near(row.AppliedMultiplier, want.mult) || row.GenreDeselected != want.desel {
			t.Errorf("%s: multiplier %v deselected %v, want %v %v",
				row.Key, row.AppliedMultiplier, row.GenreDeselected, want.mult, want.desel)
		}
	}

	// A primary genre outside the active set is rejected.
	rec = send(t, s, "POST", "/api/v1/score", scoreRequest{
		MediaID: 1, Scores: scores, SelectedGenres: []string{"mystery"}, PrimaryGenre: "Action",
	})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "not in the active genre set") {
		t.Errorf("primary outside selection: %d %s", rec.Code, rec.Body)
	}
}

func TestScore_Validation(t *testing.T) {
	al, calls := fakeAniList(t, "", mediaHandler)
	s := apiServer(t, al, nil)
	ok := map[string]float64{"story": 9}

	tests := []struct {
		name    string
		body    any
		code    int
		wantMsg string
	}{
		{"missing media id", scoreRequest{Scores: ok}, 400, "media_id is required"},
		{"empty scores", scoreRequest{MediaID: 1}, 400, "scores map is required"},
		{
			"score too low",
			scoreRequest{MediaID: 1, Scores: map[string]float64{"story": 0.99}},
			400,
			"between 1 and 10",
		},
		{
			"score too high",
			scoreRequest{MediaID: 1, Scores: map[string]float64{"story": 10.01}},
			400,
			"between 1 and 10",
		},
		{"malformed json", `{"media_id":`, 400, "invalid request body"},
		{"oversized body", `{"x":"` + strings.Repeat("a", maxRequestBodyBytes) + `"}`, 413, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := send(t, s, "POST", "/api/v1/score", tt.body)
			if rec.Code != tt.code || !strings.Contains(rec.Body.String(), tt.wantMsg) {
				t.Errorf(
					"got %d %s, want %d containing %q",
					rec.Code,
					rec.Body,
					tt.code,
					tt.wantMsg,
				)
			}
		})
	}
	for _, c := range *calls {
		t.Errorf("validation failures must not reach AniList, got call %v", c.Vars)
	}
}

// An unknown dimension key is only detected by the engine, after the media
// fetch; it must still surface as a 400 naming the key.
func TestScore_UnknownDimensionIs400(t *testing.T) {
	al, _ := fakeAniList(t, "", mediaHandler)
	rec := send(t, apiServer(t, al, nil), "POST", "/api/v1/score", scoreRequest{
		MediaID: 1, Scores: map[string]float64{"story": 9, "nope": 5},
	})
	if msg := decode[errorResponse](
		t,
		rec,
	).Error; rec.Code != 400 ||
		!strings.Contains(msg, "nope") {
		t.Errorf("got %d %q, want 400 naming the unknown dimension", rec.Code, msg)
	}
}

func TestScore_Boundaries(t *testing.T) {
	al, _ := fakeAniList(t, "", mediaHandler)
	s := apiServer(t, al, nil)
	for _, v := range []float64{1, 10} {
		rec := send(t, s, "POST", "/api/v1/score", scoreRequest{
			MediaID: 1, Scores: map[string]float64{"story": v},
		})
		if rec.Code != 200 {
			t.Errorf("score %v: code %d, want 200 (inclusive bounds)", v, rec.Code)
		}
	}
}

func TestScore_UpstreamErrorIs502(t *testing.T) {
	al, _ := fakeAniList(t, "", func(aniListCall) (int, string) { return 503, "down" })
	rec := send(t, apiServer(t, al, nil), "POST", "/api/v1/score", scoreRequest{
		MediaID: 1, Scores: map[string]float64{"story": 9},
	})
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "AniList is currently unavailable") {
		t.Errorf("got %d %s", rec.Code, rec.Body)
	}
}

func newSQLite(t *testing.T) *sqlite.SQLiteStore {
	t.Helper()
	st, err := sqlite.New(filepath.Join(t.TempDir(), "kansou.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestScore_PersistsToStore(t *testing.T) {
	al, _ := fakeAniList(t, "", mediaHandler)
	st := newSQLite(t)
	s := apiServer(t, al, st)

	rec := send(t, s, "POST", "/api/v1/score", scoreRequest{
		MediaID: 154587, Scores: map[string]float64{"story": 9, "fun": 7.5, "art": 8},
	})
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body)
	}
	saved, err := st.LatestScore(context.Background(), 154587)
	if err != nil || saved == nil {
		t.Fatalf("LatestScore: (%v, %v), want the saved score", saved, err)
	}
	if saved.FinalScore != 8.37 || saved.TitleRomaji != "Sousou no Frieren" ||
		len(saved.Breakdown) != 3 {
		t.Errorf("stored score = %+v", saved)
	}
	if saved.ConfigHash != config.Hash(apiConfig()) {
		t.Errorf("config hash = %q, want %q", saved.ConfigHash, config.Hash(apiConfig()))
	}
}

// A failing database must not fail scoring: the score is still returned.
func TestScore_StoreFailureDoesNotFailRequest(t *testing.T) {
	al, _ := fakeAniList(t, "", mediaHandler)
	st := newSQLite(t)
	_ = st.Close()
	rec := send(t, apiServer(t, al, st), "POST", "/api/v1/score", scoreRequest{
		MediaID: 1, Scores: map[string]float64{"story": 9},
	})
	if rec.Code != 200 {
		t.Errorf("code = %d, want 200 even though the save failed", rec.Code)
	}
}

// Hand-computed weights preview for genres mystery+action: see TestScore_Golden.
func TestWeights(t *testing.T) {
	al, _ := fakeAniList(t, "", mediaHandler)
	s := apiServer(t, al, nil)

	rec := send(t, s, "POST", "/api/v1/weights", weightsRequest{MediaID: 1})
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body)
	}
	got := decode[weightsResponse](t, rec)
	if got.PrimaryGenreWeight != 0.6 || !near(got.EffectiveWeightSum, 1.155) ||
		len(got.Dimensions) != 3 {
		t.Fatalf("weights = %+v", got)
	}
	want := map[string][2]float64{ // multiplier, final weight
		"story": {1.15, 0.575 / 1.155}, "fun": {1.0, 0.3 / 1.155}, "art": {1.4, 0.28 / 1.155},
	}
	for _, d := range got.Dimensions {
		if !near(d.Multiplier, want[d.Key][0]) || !near(d.FinalWeight, want[d.Key][1]) {
			t.Errorf(
				"%s: multiplier %v weight %v, want %v",
				d.Key,
				d.Multiplier,
				d.FinalWeight,
				want[d.Key],
			)
		}
	}
	if !got.Dimensions[1].BiasResistant {
		t.Error("fun must be reported bias resistant")
	}

	// A skipped dimension drops out; an override stays fixed.
	rec = send(t, s, "POST", "/api/v1/weights", weightsRequest{
		MediaID: 1, SkippedDimensions: map[string]bool{"art": true},
		WeightOverrides: map[string]float64{"fun": 0.25},
	})
	got = decode[weightsResponse](t, rec)
	for _, d := range got.Dimensions {
		switch d.Key {
		case "art":
			if !d.Skipped || d.FinalWeight != 0 {
				t.Errorf("art = %+v, want skipped", d)
			}
		case "fun":
			if !near(d.FinalWeight, 0.25) || !d.WeightOverride {
				t.Errorf("fun = %+v, want fixed 0.25", d)
			}
		case "story":
			if !near(d.FinalWeight, 0.75) {
				t.Errorf("story = %+v, want the remaining 0.75", d)
			}
		}
	}
}

func TestWeights_Validation(t *testing.T) {
	al, _ := fakeAniList(t, "", mediaHandler)
	s := apiServer(t, al, nil)
	tests := []struct {
		name string
		req  weightsRequest
		want string
	}{
		{"missing media id", weightsRequest{}, "media_id is required"},
		{
			"unknown override dimension",
			weightsRequest{MediaID: 1, WeightOverrides: map[string]float64{"nope": 0.2}},
			"unknown dimension",
		},
		{
			"zero override",
			weightsRequest{MediaID: 1, WeightOverrides: map[string]float64{"story": 0}},
			"> 0 and ≤ 1",
		},
		{
			"override above 1",
			weightsRequest{MediaID: 1, WeightOverrides: map[string]float64{"story": 1.01}},
			"> 0 and ≤ 1",
		},
		{
			"overrides sum to 1",
			weightsRequest{
				MediaID:         1,
				WeightOverrides: map[string]float64{"story": 0.5, "fun": 0.5},
			},
			"must be < 1.0",
		},
		{
			"primary not active",
			weightsRequest{MediaID: 1, SelectedGenres: []string{"mystery"}, PrimaryGenre: "action"},
			"not in the active genre set",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := send(t, s, "POST", "/api/v1/weights", tt.req)
			// Decode: raw JSON escapes < and > as \u003c / \u003e.
			if msg := decode[errorResponse](
				t,
				rec,
			).Error; rec.Code != 400 ||
				!strings.Contains(msg, tt.want) {
				t.Errorf("got %d %q, want 400 containing %q", rec.Code, msg, tt.want)
			}
		})
	}
	if rec := send(t, s, "POST", "/api/v1/weights", `{`); rec.Code != 400 {
		t.Errorf("malformed json: %d", rec.Code)
	}
}

func TestWeights_UpstreamErrorIs502(t *testing.T) {
	al, _ := fakeAniList(t, "", func(aniListCall) (int, string) { return 500, "x" })
	rec := send(t, apiServer(t, al, nil), "POST", "/api/v1/weights", weightsRequest{MediaID: 1})
	if rec.Code != 502 {
		t.Errorf("code = %d, want 502", rec.Code)
	}
}

const publishedEntry = `{"data":{"SaveMediaListEntry":{"id":9,"score":8.5,"status":"COMPLETED",
"media":{"title":{"romaji":"Sousou no Frieren"}}}}}`

func TestScorePublish(t *testing.T) {
	al, calls := fakeAniList(
		t,
		"tok",
		func(aniListCall) (int, string) { return 200, publishedEntry },
	)
	s := apiServer(t, al, nil)

	rec := send(t, s, "POST", "/api/v1/score/publish", publishRequest{MediaID: 154587, Score: 8.5})
	got := decode[publishResponse](t, rec)
	if rec.Code != 200 || got.Title != "Sousou no Frieren" || got.Score != 8.5 ||
		got.Message != "score published to AniList" {
		t.Fatalf("got %d %+v", rec.Code, got)
	}
	if v := (*calls)[0].Vars; v["mediaId"] != float64(154587) || v["score"] != 8.5 {
		t.Errorf("publish vars = %v", v)
	}
	if _, has := (*calls)[0].Vars["notes"]; has {
		t.Error("notes must not be sent when empty")
	}

	rec = send(
		t,
		s,
		"POST",
		"/api/v1/score/publish",
		publishRequest{MediaID: 1, Score: 9, Notes: "my notes"},
	)
	if rec.Code != 200 {
		t.Fatalf("with notes: %d %s", rec.Code, rec.Body)
	}
	last := (*calls)[len(*calls)-1]
	if last.Vars["notes"] != "my notes" {
		t.Errorf("notes var = %v, want my notes", last.Vars["notes"])
	}
}

func TestScorePublish_Errors(t *testing.T) {
	ok := func(aniListCall) (int, string) { return 200, publishedEntry }
	tests := []struct {
		name    string
		token   string
		handler func(aniListCall) (int, string)
		req     publishRequest
		code    int
		want    string
		notWant string
	}{
		{"missing media id", "tok", ok, publishRequest{Score: 8}, 400, "media_id is required", ""},
		{
			"score below 1",
			"tok",
			ok,
			publishRequest{MediaID: 1, Score: 0.5},
			400,
			"score must be between 1 and 10",
			"",
		},
		{
			"score above 10",
			"tok",
			ok,
			publishRequest{MediaID: 1, Score: 10.5},
			400,
			"score must be between 1 and 10",
			"",
		},
		{"no token", "", ok, publishRequest{MediaID: 1, Score: 8}, 502, "ANILIST_TOKEN", ""},
		{
			"upstream outage", "tok",
			func(aniListCall) (int, string) { return 500, "boom detail" },
			publishRequest{MediaID: 1, Score: 8},
			502, "AniList is currently unavailable", "boom detail",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			al, _ := fakeAniList(t, tt.token, tt.handler)
			rec := send(t, apiServer(t, al, nil), "POST", "/api/v1/score/publish", tt.req)
			if rec.Code != tt.code || !strings.Contains(rec.Body.String(), tt.want) {
				t.Errorf("got %d %s, want %d containing %q", rec.Code, rec.Body, tt.code, tt.want)
			}
			if tt.notWant != "" && strings.Contains(rec.Body.String(), tt.notWant) {
				t.Errorf("leaks %q: %s", tt.notWant, rec.Body)
			}
		})
	}
}

func TestHistoryPromote(t *testing.T) {
	al, _ := fakeAniList(t, "", mediaHandler)
	st := newSQLite(t)
	s := apiServer(t, al, st)
	ctx := context.Background()

	// Two scores for one media; the second becomes the active one.
	for _, v := range []float64{5, 9} {
		rec := send(t, s, "POST", "/api/v1/score", scoreRequest{
			MediaID: 154587, Scores: map[string]float64{"story": v, "fun": v, "art": v},
		})
		if rec.Code != 200 {
			t.Fatalf("seeding score %v: %d %s", v, rec.Code, rec.Body)
		}
	}
	hist, err := st.ScoreHistory(ctx, 154587)
	if err != nil || len(hist) != 2 || hist[0].FinalScore != 9 {
		t.Fatalf("history = (%+v, %v), want active 9 first", hist, err)
	}
	older := hist[1].ID

	rec := send(t, s, "POST", "/api/v1/history/"+strconv.Itoa(older)+"/promote", nil)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("promote: %d %q, want 204 and empty body", rec.Code, rec.Body)
	}
	latest, _ := st.LatestScore(ctx, 154587)
	if latest == nil || latest.ID != older || latest.FinalScore != 5 {
		t.Errorf("latest after promote = %+v, want the older 5.0 score", latest)
	}
}

func TestHistoryPromote_Errors(t *testing.T) {
	st := newSQLite(t)
	s := apiServer(t, nil, st)
	tests := []struct {
		name string
		srv  *Server
		path string
		code int
		want string
	}{
		{"non-numeric id", s, "/api/v1/history/abc/promote", 400, "must be an integer"},
		{"unknown score", s, "/api/v1/history/999/promote", 404, "score not found"},
		{"no database", apiServer(t, nil, nil), "/api/v1/history/1/promote", 503, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := send(t, tt.srv, "POST", tt.path, nil)
			if rec.Code != tt.code || !strings.Contains(rec.Body.String(), tt.want) {
				t.Errorf("got %d %s, want %d containing %q", rec.Code, rec.Body, tt.code, tt.want)
			}
		})
	}
}

// Regression: POST /score used to accept any weight_overrides (sum > 1, unknown
// dimension, negative, > 1) and return a wrong score with 200, which was then
// persisted. It must reject them like POST /weights does, before any AniList call.
func TestScore_RejectsInvalidWeightOverrides(t *testing.T) {
	al, calls := fakeAniList(t, "", mediaHandler)
	s := apiServer(t, al, nil)
	scores := map[string]float64{"story": 9, "fun": 7.5, "art": 8}
	tests := []struct {
		name string
		ov   map[string]float64
		want string
	}{
		{"sum above 1", map[string]float64{"story": 0.9, "fun": 0.9}, "must be < 1.0"},
		{"sum exactly 1", map[string]float64{"story": 0.5, "fun": 0.5}, "must be < 1.0"},
		{
			"unknown dimension",
			map[string]float64{"nope": 0.3},
			"unknown dimension in weight_overrides: nope",
		},
		{"negative", map[string]float64{"story": -0.5}, "> 0 and ≤ 1"},
		{"zero", map[string]float64{"story": 0}, "> 0 and ≤ 1"},
		{"above 1", map[string]float64{"story": 1.5}, "> 0 and ≤ 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := send(t, s, "POST", "/api/v1/score", scoreRequest{
				MediaID: 1, Scores: scores, WeightOverrides: tt.ov,
			})
			if msg := decode[errorResponse](
				t,
				rec,
			).Error; rec.Code != 400 ||
				!strings.Contains(msg, tt.want) {
				t.Errorf("got %d %q, want 400 containing %q", rec.Code, msg, tt.want)
			}
		})
	}
	if len(*calls) != 0 {
		t.Errorf("rejected requests reached AniList %d times, want 0", len(*calls))
	}
}

// A valid override is applied: the overridden weight is fixed and flagged, and
// the remaining dimensions share what is left.
func TestScore_AppliesValidWeightOverride(t *testing.T) {
	al, _ := fakeAniList(t, "", mediaHandler)
	rec := send(t, apiServer(t, al, nil), "POST", "/api/v1/score", scoreRequest{
		MediaID: 1, Scores: map[string]float64{"story": 9, "fun": 7.5, "art": 8},
		WeightOverrides: map[string]float64{"fun": 0.25},
	})
	if rec.Code != 200 {
		t.Fatalf("code %d: %s", rec.Code, rec.Body)
	}
	var sum float64
	for _, row := range decode[scoreResponse](t, rec).Breakdown {
		sum += row.FinalWeight
		if row.Key == "fun" && (!near(row.FinalWeight, 0.25) || !row.WeightOverride) {
			t.Errorf("fun = %+v, want fixed 0.25 and flagged", row)
		}
	}
	if !near(sum, 1) {
		t.Errorf("final weights sum to %v, want 1", sum)
	}
}
