package cmd

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
	"github.com/kondanta/kansou/internal/store"
	"github.com/kondanta/kansou/internal/store/sqlite"
)

// seedTitled saves one score for a media with the given id and titles.
func seedTitled(
	t *testing.T,
	ctx context.Context,
	st *sqlite.SQLiteStore,
	id int,
	romaji, english string,
) {
	t.Helper()
	cfg := &config.Config{
		DimensionOrder: []string{"story"},
		Dimensions:     map[string]config.DimensionDef{"story": {Label: "Story", Weight: 1}},
		Genres:         map[string]map[string]float64{},
	}
	res := scoring.Result{
		FinalScore: 8,
		Breakdown: []scoring.BreakdownRow{{
			Key: "story", Label: "Story", Score: 8, BaseWeight: 1,
			FinalWeight: 1, AppliedMultiplier: 1, Contribution: 8,
		}},
		Meta: scoring.SessionMeta{
			MediaID: id, TitleRomaji: romaji, TitleEnglish: english,
			MediaType: scoring.Anime, Format: "TV",
		},
	}
	if err := st.SaveScore(ctx, res, cfg, 0); err != nil {
		t.Fatal(err)
	}
}

func TestResolveHistoryQuery(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.New(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	seedTitled(t, ctx, st, 10, "Sousou no Frieren", "Frieren: Beyond Journey's End")
	seedTitled(t, ctx, st, 20, "Kimi no Na wa", "Your Name")
	seedTitled(t, ctx, st, 30, "Kimi ni Todoke", "From Me to You")

	tests := []struct {
		name      string
		query     string
		stdin     string
		wantID    int
		wantTitle string
		wantErr   string
		cancelled bool
	}{
		{"numeric id used directly, no lookup", "999", "", 999, "", "", false},
		{"unique title match skips the picker", "frieren", "", 10, "Sousou no Frieren", "", false},
		{"no match", "zzz", "", 0, "", `no history entries found matching "zzz"`, false},
		{
			"ambiguous picks the chosen entry (results sorted by romaji)",
			"kimi",
			"2\n",
			20,
			"Kimi no Na wa",
			"",
			false,
		},
		{"ambiguous retries on bad input", "kimi", "x\n9\n0\n1\n", 30, "Kimi ni Todoke", "", false},
		{"ambiguous EOF cancels", "kimi", "", 0, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var id int
			var title string
			var err error
			withStdin(t, tt.stdin, func() {
				captureStdout(t, func() { id, title, err = resolveHistoryQuery(ctx, st, tt.query) })
			})
			switch {
			case tt.cancelled:
				if !errors.Is(err, errUserCancelled) {
					t.Fatalf("err = %v, want errUserCancelled", err)
				}
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
			default:
				if err != nil || id != tt.wantID || title != tt.wantTitle {
					t.Fatalf(
						"got (%d, %q, %v), want (%d, %q)",
						id,
						title,
						err,
						tt.wantID,
						tt.wantTitle,
					)
				}
			}
		})
	}
}

func TestResolveHistoryQuery_SearchError(t *testing.T) {
	st, err := sqlite.New(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close() // a closed store makes the search fail
	_, _, err = resolveHistoryQuery(context.Background(), st, "anything")
	if err == nil || !strings.Contains(err.Error(), "searching history") {
		t.Fatalf("err = %v", err)
	}
}

func TestPickMediaSearchResult_Rendering(t *testing.T) {
	results := []store.MediaSearchResult{
		{AnilistID: 1, TitleRomaji: "Romaji Only", MediaType: "ANIME", Format: "TV"},
		{
			AnilistID:    2,
			TitleRomaji:  "Same",
			TitleEnglish: "Same",
			MediaType:    "MANGA",
			Format:       "MANGA",
		},
		{
			AnilistID:    3,
			TitleRomaji:  "Native",
			TitleEnglish: "English Wins",
			MediaType:    "ANIME",
			Format:       "MOVIE",
		},
	}
	var got *store.MediaSearchResult
	var err error
	var out string
	withStdin(t, "3\n", func() {
		out = captureStdout(t, func() { got, err = pickMediaSearchResult(results) })
	})
	if err != nil || got == nil || got.AnilistID != 3 {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, want := range []string{
		"1. Romaji Only", "(ANIME · TV)", "2. Same", "(MANGA · MANGA)",
		"3. English Wins", "(ANIME · MOVIE)", "Pick a result [1–3]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Native") {
		t.Error("English title should replace romaji when they differ")
	}

	// A single result is returned without reading stdin.
	one, err := pickMediaSearchResult(results[:1])
	if err != nil || one.AnilistID != 1 {
		t.Fatalf("single result: %+v, %v", one, err)
	}
}

func TestQueryLabel(t *testing.T) {
	if got := queryLabel("Frieren", 5); got != `"Frieren"` {
		t.Errorf("got %s", got)
	}
	if got := queryLabel("", 5); got != "AniList ID 5" {
		t.Errorf("got %s", got)
	}
}
