package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kondanta/kansou/internal/anilist"
	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
	"github.com/kondanta/kansou/internal/store"
	"github.com/kondanta/kansou/internal/store/sqlite"
)

func scoreDims() map[string]config.DimensionDef {
	return map[string]config.DimensionDef{
		"story": {Label: "Story", Description: "Plot", Weight: 0.6},
		"fun":   {Label: "Fun", Description: "Enjoyment", Weight: 0.4, BiasResistant: true},
	}
}

func scoreConfig() *config.Config {
	return &config.Config{
		DimensionOrder:     []string{"story", "fun"},
		Dimensions:         scoreDims(),
		Genres:             map[string]map[string]float64{},
		PrimaryGenreWeight: 0.6,
		MaxMultiplier:      2.0,
		MaxHistory:         -1,
		DimensionsHash:     "hash123",
	}
}

func TestParseWeightFlag(t *testing.T) {
	dims := scoreDims()
	tests := []struct {
		name    string
		flag    string
		want    map[string]float64
		wantErr string
	}{
		{"empty means no overrides", "", nil, ""},
		{"single", "story=0.3", map[string]float64{"story": 0.3}, ""},
		{
			"multiple with spaces",
			" story = 0.3 , fun=0.2 ",
			map[string]float64{"story": 0.3, "fun": 0.2},
			"",
		},
		{"just under 1", "story=0.5,fun=0.499", map[string]float64{"story": 0.5, "fun": 0.499}, ""},
		{"missing equals", "story", nil, "invalid --weight format"},
		{"unknown dimension", "nope=0.2", nil, `unknown dimension "nope"`},
		{"not a number", "story=abc", nil, `invalid weight "abc"`},
		{"zero", "story=0", nil, "must be > 0.0 and ≤ 1.0"},
		{"negative", "story=-0.1", nil, "must be > 0.0 and ≤ 1.0"},
		{"above one", "story=1.5", nil, "must be > 0.0 and ≤ 1.0"},
		{"sum exactly one", "story=0.5,fun=0.5", nil, "must be < 1.0"},
		{"single value one", "story=1", nil, "must be < 1.0"},
		{"sum above one", "story=0.7,fun=0.7", nil, "must be < 1.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWeightFlag(tt.flag, dims)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got (%v, %v), want error containing %q", got, err, tt.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got (%v, %v), want %v", got, err, tt.want)
			}
		})
	}
}

func TestPromptDimensionScore(t *testing.T) {
	def := scoreDims()["story"]
	prev := map[string]float64{"story": 6.5}
	tests := []struct {
		name        string
		input       string
		overrides   map[string]float64
		prevScores  map[string]float64
		prevSkipped map[string]bool
		wantScore   float64
		wantSkipped bool
		wantErr     string
	}{
		{name: "plain score", input: "7.5\n", wantScore: 7.5},
		{name: "lower bound", input: "1\n", wantScore: 1},
		{name: "upper bound", input: "10\n", wantScore: 10},
		{name: "retries until valid", input: "abc\n11\n0\n0.99\n8\n", wantScore: 8},
		{name: "s skips", input: "s\n", wantSkipped: true},
		{name: "skip skips", input: "skip\n", wantSkipped: true},
		{
			name:      "skip an overridden dimension",
			input:     "s\n",
			overrides: map[string]float64{"story": 0.2},
			wantErr:   "mutually exclusive",
		},
		{name: "enter keeps previous score", input: "\n", prevScores: prev, wantScore: 6.5},
		{
			name:        "enter keeps previous skip",
			input:       "\n",
			prevSkipped: map[string]bool{"story": true},
			wantSkipped: true,
		},
		{
			name:  "enter keeps skip of overridden dimension",
			input: "\n",
			prevSkipped: map[string]bool{
				"story": true,
			},
			overrides: map[string]float64{"story": 0.2},
			wantErr:   "mutually exclusive",
		},
		{name: "enter with no previous reprompts", input: "\n\n9\n", wantScore: 9},
		{name: "eof cancels", input: "", wantErr: "cancel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var score float64
			var skipped bool
			var err error
			captureStdout(t, func() {
				score, skipped, err = promptDimensionScore(
					bufio.NewReader(strings.NewReader(tt.input)), "story", def,
					tt.overrides, tt.prevScores, tt.prevSkipped)
			})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got err %v, want containing %q", err, tt.wantErr)
				}
				if tt.wantErr == "cancel" && !errors.Is(err, errUserCancelled) {
					t.Errorf("EOF must return errUserCancelled, got %v", err)
				}
				return
			}
			if err != nil || score != tt.wantScore || skipped != tt.wantSkipped {
				t.Errorf(
					"got (%v, %v, %v), want (%v, %v)",
					score,
					skipped,
					err,
					tt.wantScore,
					tt.wantSkipped,
				)
			}
		})
	}
}

func TestRunScoringLoop(t *testing.T) {
	cfg := scoreConfig()
	var scores map[string]float64
	var skipped map[string]bool
	var err error
	out := captureStdout(t, func() {
		scores, skipped, err = runScoringLoop(
			bufio.NewReader(strings.NewReader("8.5\ns\n")),
			cfg.DimensionOrder, cfg.Dimensions, nil, nil, nil)
	})
	if err != nil || !reflect.DeepEqual(scores, map[string]float64{"story": 8.5}) ||
		!reflect.DeepEqual(skipped, map[string]bool{"fun": true}) {
		t.Fatalf("got (%v, %v, %v)", scores, skipped, err)
	}
	// Prompts follow config order and show the previous value as a hint.
	if strings.Index(out, "Story") > strings.Index(out, "Fun") || !strings.Contains(out, "Plot") {
		t.Errorf("prompt order/description wrong:\n%s", out)
	}

	out = captureStdout(t, func() {
		_, _, _ = runScoringLoop(bufio.NewReader(strings.NewReader("\n\n")),
			cfg.DimensionOrder, cfg.Dimensions, nil,
			map[string]float64{"story": 6.5}, map[string]bool{"fun": true})
	})
	if !strings.Contains(out, "[prev: 6.5]") || !strings.Contains(out, "[prev: skipped]") {
		t.Errorf("missing previous-value hints:\n%s", out)
	}
}

func TestExtractPrevScores(t *testing.T) {
	v := 7.5
	scores, skipped := extractPrevScores(&store.Score{Breakdown: []store.DimensionScoreRow{
		{DimensionKey: "story", Score: &v},
		{DimensionKey: "fun", Skipped: true},
		{DimensionKey: "art"}, // neither scored nor skipped: ignored
	}})
	if !reflect.DeepEqual(scores, map[string]float64{"story": 7.5}) ||
		!reflect.DeepEqual(skipped, map[string]bool{"fun": true}) {
		t.Errorf("got %v %v", scores, skipped)
	}
}

func TestResolvePrimaryGenre(t *testing.T) {
	genres := []string{"Mystery", "Action"}
	tests := []struct {
		name    string
		flag    string
		genres  []string
		input   string
		want    string
		wantErr string
	}{
		{"flag returns AniList spelling", "mystery", genres, "", "Mystery", ""},
		{
			"flag not in genres",
			"romance",
			genres,
			"",
			"",
			"not in the media's genre list: Mystery, Action",
		},
		{"no genres, no prompt", "", nil, "", "", ""},
		{"enter skips", "", genres, "\n", "", ""},
		{"typed name, case-insensitive", "", genres, "ACTION\n", "Action", ""},
		{"invalid then valid", "", genres, "romance\nmystery\n", "Mystery", ""},
		{"eof cancels", "", genres, "", "", "cancel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			var err error
			captureStdout(t, func() {
				got, err = resolvePrimaryGenre(
					tt.flag,
					tt.genres,
					bufio.NewReader(strings.NewReader(tt.input)),
				)
			})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got err %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("got (%q, %v), want %q", got, err, tt.want)
			}
		})
	}
}

func TestGenreHelpers(t *testing.T) {
	if got := unmatchedGenres(
		[]string{"Mystery", "Romance", "Action"},
		[]string{"mystery", "action"},
	); !reflect.DeepEqual(
		got,
		[]string{"Romance"},
	) {
		t.Errorf("unmatchedGenres = %v", got)
	}
	if got := unmatchedGenres([]string{"A"}, []string{"a"}); got != nil {
		t.Errorf("all matched: got %v, want nil", got)
	}
	if got := annotateMatchedGenres(
		[]string{"action", "mystery"},
		"Mystery",
	); got != "action, mystery [primary]" {
		t.Errorf("annotate = %q", got)
	}
	if got := annotateMatchedGenres([]string{"action", "mystery"}, ""); got != "action, mystery" {
		t.Errorf("annotate without primary = %q", got)
	}
	tests := []struct {
		name string
		meta scoring.SessionMeta
		want bool
	}{
		{
			"primary plus secondary",
			scoring.SessionMeta{
				PrimaryGenre: "Mystery",
				GenresActive: []string{"action", "mystery"},
			},
			true,
		},
		{
			"primary is sole active genre",
			scoring.SessionMeta{PrimaryGenre: "Mystery", GenresActive: []string{"mystery"}},
			false,
		},
		{"no active genres", scoring.SessionMeta{PrimaryGenre: "Mystery"}, false},
	}
	for _, tt := range tests {
		if got := blendWasApplied(tt.meta); got != tt.want {
			t.Errorf("%s: blendWasApplied = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestBuildScoringEntry(t *testing.T) {
	media := &anilist.Media{
		ID: 7, TitleRomaji: "R", TitleEnglish: "E", MediaType: scoring.Manga, Format: "MANGA",
		Genres: []string{"Mystery"}, CoverImage: "c.png",
	}
	session := scoringSession{
		Scores: map[string]float64{
			"story": 9,
		},
		Skipped:      map[string]bool{"fun": true},
		PrimaryGenre: "Mystery",
	}
	e := buildScoringEntry(media, session, map[string]float64{"story": 0.3}, scoreConfig())
	if e.Scores["story"] != 9 || !e.SkippedDimensions["fun"] || e.WeightOverrides["story"] != 0.3 ||
		e.PrimaryGenre != "Mystery" || !reflect.DeepEqual(e.Genres, []string{"Mystery"}) {
		t.Errorf("entry = %+v", e)
	}
	m := e.Meta
	if m.MediaID != 7 || m.TitleRomaji != "R" || m.TitleEnglish != "E" ||
		m.MediaType != scoring.Manga ||
		m.Format != "MANGA" ||
		m.AniListURL != "https://anilist.co/manga/7" ||
		m.ConfigHash != "hash123" ||
		m.PrimaryGenre != "Mystery" ||
		m.PrimaryGenreWeight != 0.6 ||
		m.CoverImage != "c.png" {
		t.Errorf("meta = %+v", m)
	}
}

func TestFormatNote(t *testing.T) {
	result := scoring.Result{
		FinalScore: 8.4,
		Breakdown: []scoring.BreakdownRow{
			{
				Label:             "Story",
				Score:             9,
				BaseWeight:        0.6,
				AppliedMultiplier: 1.15,
				FinalWeight:       0.6,
				Contribution:      5.4,
			},
			{
				Label:             "Fun",
				Score:             7.5,
				BaseWeight:        0.4,
				AppliedMultiplier: 1,
				FinalWeight:       0.4,
				Contribution:      3,
				BiasResistant:     true,
			},
			{Label: "Art", Skipped: true},
		},
		Meta: scoring.SessionMeta{
			TitleRomaji: "Sousou no Frieren", PrimaryGenre: "Mystery", PrimaryGenreWeight: 0.6,
			AllGenres: []string{"Mystery", "Action"}, MatchedGenres: []string{"action", "mystery"},
			GenresActive: []string{"action", "mystery"}, ConfigHash: "hash123",
		},
	}
	want := strings.Join([]string{
		"Sousou no Frieren",
		"Score: 8.40 / 10  [kansou]",
		"",
		"Dimension        Score   BaseW   ×Mult  FinalW  Contrib",
		"───────────────────────────────────────────────────────",
		"Story              9.0   60.0%   ×1.15   60.0%  5.40",
		"Fun                7.5   40.0%   ×1.00   40.0%  3.00  *",
		"Art                  —       —       —       —  —",
		"",
		"Primary: Mystery (blend 60/40)",
		"Genres:  Mystery, Action",
		"Matched: action, mystery [primary]",
		"Config:  hash123",
	}, "\n")
	if got := formatNote(result); got != want {
		t.Errorf("formatNote:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	// A sole active primary genre shows no blend ratio.
	result.Meta.GenresActive = []string{"mystery"}
	if got := formatNote(result); !strings.Contains(got, "Primary: Mystery (sole active genre)") ||
		strings.Contains(got, "blend") {
		t.Errorf("sole-genre note wrong:\n%s", got)
	}
}

// --- score add, end to end against a fake AniList ---

var cmdBaseTransport = http.DefaultTransport.(*http.Transport).Clone()

type cmdSwap struct{ target *url.URL }

func (s cmdSwap) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = s.target.Scheme, s.target.Host
	return cmdBaseTransport.RoundTrip(r)
}

type cmdCall struct {
	Query string
	Vars  map[string]any
}

// fakeAniListCLI answers AniList requests via handler. anilist.NewClient leaves
// its transport nil (http.DefaultTransport), so that is swapped for the test.
func fakeAniListCLI(
	t *testing.T,
	token string,
	handler func(cmdCall) string,
) (*anilist.Client, *[]cmdCall) {
	t.Helper()
	var calls []cmdCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Query string         `json:"query"`
			Vars  map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		c := cmdCall{b.Query, b.Vars}
		calls = append(calls, c)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(handler(c)))
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })
	http.DefaultTransport = cmdSwap{u}
	t.Setenv("ANILIST_TOKEN", token)
	return anilist.NewClient(), &calls
}

const cmdMedia = `{"id":154587,"title":{"romaji":"Sousou no Frieren","english":"Frieren","native":"n"},
"format":"TV","status":"FINISHED","episodes":28,"chapters":0,"genres":["Mystery","Action"],
"tags":[],"coverImage":{"extraLarge":"c.png"},"bannerImage":"","averageScore":91,"meanScore":89}`

func cmdAniList(c cmdCall) string {
	if strings.Contains(c.Query, "SaveMediaListEntry") {
		return `{"data":{"SaveMediaListEntry":{"id":1,"score":8.4,"status":"COMPLETED",` +
			`"media":{"title":{"romaji":"Sousou no Frieren"}}}}}`
	}
	if strings.Contains(c.Query, "mediaListEntry") {
		return `{"data":{"Media":{"mediaListEntry":null}}}`
	}
	return `{"data":{"Media":` + cmdMedia + `,"Page":{"media":[` + cmdMedia + `]}}}`
}

func scoreApp(t *testing.T, al *anilist.Client, withStore bool) (*App, *sqlite.SQLiteStore) {
	t.Helper()
	cfg := scoreConfig()
	a := &App{Config: cfg, AniList: al, Engine: newEngine(cfg)}
	var st *sqlite.SQLiteStore
	if withStore {
		var err error
		st, err = sqlite.New(filepath.Join(t.TempDir(), "kansou.db"))
		if err != nil {
			t.Fatalf("sqlite.New: %v", err)
		}
		t.Cleanup(func() { _ = st.Close() })
		a.Store = st
	}
	return a, st
}

const frierenURL = "https://anilist.co/anime/154587"

func runAdd(
	t *testing.T,
	a *App,
	input string,
	breakdown bool,
	weight, primary string,
	notes bool,
) (string, error) {
	t.Helper()
	var err error
	var out string
	withStdin(t, input, func() {
		out = captureStdout(t, func() {
			err = a.runScoreAdd(
				context.Background(),
				nil,
				frierenURL,
				"",
				breakdown,
				weight,
				primary,
				notes,
			)
		})
	})
	return out, err
}

// 9 story × 0.6 + 7.5 fun × 0.4 = 8.4; neither genre is configured, so no
// multipliers apply. Answering "n" to the publish prompt must not touch AniList.
func TestScoreAdd_ScoresSavesAndSkipsPublish(t *testing.T) {
	al, calls := fakeAniListCLI(t, "tok", cmdAniList)
	a, st := scoreApp(t, al, true)

	out, err := runAdd(t, a, "\n9\n7.5\nn\n", true, "", "", false)
	if err != nil {
		t.Fatalf("runScoreAdd: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Found: Sousou no Frieren (ANIME · TV · 28 episodes)", "Genres: mystery, action",
		"Final Score   8.40 / 10", "Publish to AniList?",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	saved, _ := st.LatestScore(context.Background(), 154587)
	if saved == nil || saved.FinalScore != 8.4 || len(saved.Breakdown) != 2 {
		t.Fatalf("saved score = %+v, want 8.4 with 2 rows", saved)
	}
	for _, c := range *calls {
		if strings.Contains(c.Query, "SaveMediaListEntry") {
			t.Error("published although the user answered n")
		}
	}
}

func TestScoreAdd_PublishesWithNotes(t *testing.T) {
	al, calls := fakeAniListCLI(t, "tok", cmdAniList)
	a, _ := scoreApp(t, al, false)

	out, err := runAdd(t, a, "\n9\n7.5\ny\n", false, "", "", true)
	if err != nil {
		t.Fatalf("runScoreAdd: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Score published to AniList") ||
		!strings.Contains(out, "breakdown appended") {
		t.Errorf("missing publish confirmation:\n%s", out)
	}
	last := (*calls)[len(*calls)-1]
	if last.Vars["score"] != 8.4 || last.Vars["mediaId"] != float64(154587) ||
		!strings.Contains(last.Vars["notes"].(string), "[kansou]") {
		t.Errorf("publish vars = %v", last.Vars)
	}
}

func TestScoreAdd_PublishFailureKeepsScoreInMessage(t *testing.T) {
	al, _ := fakeAniListCLI(t, "", cmdAniList) // no token: PublishScore refuses
	a, _ := scoreApp(t, al, false)
	_, err := runAdd(t, a, "\n9\n7.5\ny\n", false, "", "", false)
	if err == nil || !strings.Contains(err.Error(), "your calculated score was 8.40") {
		t.Errorf("got %v, want an error that still reports the 8.40 score", err)
	}
}

func TestScoreAdd_WeightOverrideAndPrimaryGenre(t *testing.T) {
	al, _ := fakeAniListCLI(t, "tok", cmdAniList)
	a, st := scoreApp(t, al, true)
	// fun fixed at 0.25 → story gets the remaining 0.75: 9×0.75 + 7.5×0.25 = 8.625 → 8.63 (binary rounding)
	out, err := runAdd(t, a, "9\n7.5\nn\n", true, "fun=0.25", "mystery", false)
	if err != nil {
		t.Fatalf("runScoreAdd: %v\n%s", err, out)
	}
	saved, _ := st.LatestScore(context.Background(), 154587)
	// The client lowercases AniList genres on ingestion, so the stored primary is lowercase.
	if saved == nil || saved.PrimaryGenre != "mystery" {
		t.Fatalf("saved = %+v, want primary genre mystery", saved)
	}
	if got := saved.FinalScore; got != 8.63 {
		t.Errorf("final score = %v, want 8.63 (8.625 rounds half up)", got)
	}
	for _, row := range saved.Breakdown {
		if row.DimensionKey == "fun" && (row.FinalWeight != 0.25 || !row.WeightOverride) {
			t.Errorf("fun row = %+v, want fixed 0.25", row)
		}
	}
}

func TestScoreAdd_PrefillsFromPreviousScore(t *testing.T) {
	al, _ := fakeAniListCLI(t, "tok", cmdAniList)
	a, st := scoreApp(t, al, true)
	if _, err := runAdd(t, a, "\n9\n7.5\nn\n", false, "", "", false); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// Second run: Enter keeps story 9, fun is changed to 10 → 9×0.6 + 10×0.4 = 9.4.
	out, err := runAdd(t, a, "\n\n10\nn\n", false, "", "", false)
	if err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Previous score: 8.40") || !strings.Contains(out, "[prev: 9.0]") ||
		!strings.Contains(out, "Final Score   9.40 / 10") {
		t.Errorf("prefill output wrong:\n%s", out)
	}
	hist, _ := st.ScoreHistory(context.Background(), 154587)
	if len(hist) != 2 || hist[0].FinalScore != 9.4 || !hist[0].IsLatest {
		t.Errorf("history = %+v, want 9.4 active then 8.4", hist)
	}
}

func TestScoreAdd_Failures(t *testing.T) {
	al, calls := fakeAniListCLI(t, "tok", cmdAniList)
	a, st := scoreApp(t, al, true)

	t.Run("invalid type flag fails before any request", func(t *testing.T) {
		var err error
		withStdin(t, "", func() {
			captureStdout(t, func() {
				err = a.runScoreAdd(
					context.Background(),
					[]string{"x"},
					"",
					"novel",
					false,
					"",
					"",
					false,
				)
			})
		})
		if err == nil || !strings.Contains(err.Error(), "invalid --type") || len(*calls) != 0 {
			t.Errorf("got %v with %d calls", err, len(*calls))
		}
	})
	t.Run("invalid weight flag fails before any request", func(t *testing.T) {
		_, err := runAdd(t, a, "", false, "story=2", "", false)
		if err == nil || !strings.Contains(err.Error(), "invalid weight") || len(*calls) != 0 {
			t.Errorf("got %v with %d calls", err, len(*calls))
		}
	})
	t.Run("overrides covering every dimension fail before any request", func(t *testing.T) {
		_, err := runAdd(t, a, "", false, "story=0.2,fun=0.3", "", false)
		if err == nil || !strings.Contains(err.Error(), "no other scored dimension") ||
			len(*calls) != 0 {
			t.Errorf("got %v with %d calls", err, len(*calls))
		}
	})
	t.Run(
		"skipping the only un-overridden dimension is rejected, nothing saved",
		func(t *testing.T) {
			// fun is pinned at 0.25; skipping story leaves fun as the sole scored
			// dimension, so the remaining 0.75 would have nowhere to go (ADR-042).
			before := len(*calls)
			// The leading blank line declines the primary-genre prompt.
			_, err := runAdd(t, a, "\ns\n7.5\nn\n", false, "fun=0.25", "", false)
			if err == nil || !strings.Contains(err.Error(), "no other scored dimension") {
				t.Fatalf("got %v", err)
			}
			if latest, _ := st.LatestScore(context.Background(), 154587); latest != nil {
				t.Errorf("a rejected session saved %+v", latest)
			}
			for _, c := range (*calls)[before:] {
				if strings.Contains(c.Query, "SaveMediaListEntry") {
					t.Error("a rejected session published to AniList")
				}
			}
		},
	)
	t.Run("no query and no url", func(t *testing.T) {
		var err error
		withStdin(t, "", func() {
			captureStdout(
				t,
				func() { err = a.runScoreAdd(context.Background(), nil, "", "", false, "", "", false) },
			)
		})
		if err == nil || !strings.Contains(err.Error(), "provide a search query or --url") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("all dimensions skipped", func(t *testing.T) {
		_, err := runAdd(t, a, "\ns\ns\n", false, "", "", false)
		if err == nil || !strings.Contains(err.Error(), "all dimensions were skipped") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("cancelled mid-session saves nothing and is not an error", func(t *testing.T) {
		if _, err := runAdd(t, a, "\n9\n", false, "", "", false); err != nil {
			t.Errorf("cancel returned %v, want nil", err)
		}
	})
	t.Run("primary genre not on the media", func(t *testing.T) {
		_, err := runAdd(t, a, "9\n7\n", false, "", "romance", false)
		if err == nil || !strings.Contains(err.Error(), "not in the media's genre list") {
			t.Errorf("got %v", err)
		}
	})
	if saved, _ := st.LatestScore(context.Background(), 154587); saved != nil {
		t.Errorf("a failed or cancelled session saved a score: %+v", saved)
	}
}
