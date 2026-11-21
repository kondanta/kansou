package export

import (
	"context"
	"errors"
	"html/template"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kondanta/kansou/internal/stats"
	"github.com/kondanta/kansou/internal/store"
	"github.com/kondanta/kansou/internal/store/sqlite"
)

var errBoom = errors.New("boom")

// failingStore wraps a real store and fails the one method named by failOn.
type failingStore struct {
	*sqlite.SQLiteStore
	failOn string
}

func (f failingStore) GenreBreakdown(ctx context.Context) ([]store.GenreStat, error) {
	if f.failOn == "GenreBreakdown" {
		return nil, errBoom
	}
	return f.SQLiteStore.GenreBreakdown(ctx)
}

func (f failingStore) DimensionVariance(
	ctx context.Context,
) ([]store.DimensionVarianceStat, error) {
	if f.failOn == "DimensionVariance" {
		return nil, errBoom
	}
	return f.SQLiteStore.DimensionVariance(ctx)
}

func (f failingStore) MostRescored(ctx context.Context) ([]store.RescoredStat, error) {
	if f.failOn == "MostRescored" {
		return nil, errBoom
	}
	return f.SQLiteStore.MostRescored(ctx)
}

func (f failingStore) ListLatest(ctx context.Context) ([]store.Score, error) {
	if f.failOn == "ListLatest" {
		return nil, errBoom
	}
	return f.SQLiteStore.ListLatest(ctx)
}

func newStore(t *testing.T) *sqlite.SQLiteStore {
	t.Helper()
	st, err := sqlite.New(filepath.Join(t.TempDir(), "kansou.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestGenerate_StoreErrorsAreWrapped(t *testing.T) {
	tests := []struct {
		failOn  string
		wantMsg string
	}{
		{"GenreBreakdown", "fetching genre stats"},
		{"DimensionVariance", "fetching dimension stats"},
		{"MostRescored", "fetching history stats"},
		{"ListLatest", "fetching entries"},
	}
	for _, tt := range tests {
		t.Run(tt.failOn, func(t *testing.T) {
			out, err := Generate(context.Background(), failingStore{newStore(t), tt.failOn})
			if err == nil {
				t.Fatal("expected an error")
			}
			if out != nil {
				t.Errorf("expected nil output on error, got %d bytes", len(out))
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q missing context %q", err, tt.wantMsg)
			}
			if !errors.Is(err, errBoom) {
				t.Errorf("error %q does not wrap the cause", err)
			}
		})
	}
}

func TestChartBuilders_ExactJSON(t *testing.T) {
	tests := []struct {
		name string
		got  chartData
		want string
	}{
		{
			"genre count",
			genreCountChart(
				[]store.GenreStat{{Genre: "Action", Count: 3}, {Genre: "Drama", Count: 1}},
			),
			`{"labels":["Action","Drama"],"datasets":[{"label":"Entries","data":[3,1]}]}`,
		},
		{
			"genre score",
			genreScoreChart(
				[]store.GenreScore{
					{Genre: "Action", AvgScore: 8.25},
					{Genre: "Drama", AvgScore: 6.5},
				},
			),
			`{"labels":["Action","Drama"],"datasets":[{"label":"Average Score","data":[8.25,6.5]}]}`,
		},
		{
			"variance",
			varianceChart([]store.DimensionVarianceStat{{Label: "Story", StdDev: 1.5}}),
			`{"labels":["Story"],"datasets":[{"label":"Std Dev","data":[1.5]}]}`,
		},
		{
			// nil input must still encode as [] (not null) or Chart.js throws.
			"empty is arrays not null",
			genreCountChart(nil),
			`{"labels":[],"datasets":[{"label":"Entries","data":[]}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := chartJSON(tt.got)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestChartJSON_MarshalErrorAndHTMLEscaping(t *testing.T) {
	if _, err := chartJSON(make(chan int)); err == nil ||
		!strings.Contains(err.Error(), "marshalling chart data") {
		t.Errorf("expected a wrapped marshal error, got %v", err)
	}

	// A hostile genre name must not be able to close the surrounding <script>.
	got, err := chartJSON(genreCountChart([]store.GenreStat{{Genre: "</script><b>&", Count: 1}}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(string(got), "<>&") {
		t.Errorf("chart JSON contains raw HTML metacharacters: %s", got)
	}
}

func TestBuildPageData_Contents(t *testing.T) {
	genres := &stats.GenreStats{
		Breakdown: []store.GenreStat{{Genre: "Action", Count: 2}},
		ByGenre:   []store.GenreScore{{Genre: "Action", AvgScore: 7}},
	}
	dims := &store.DimensionStatsResponse{
		DimensionVariance: []store.DimensionVarianceStat{{Label: "Story", StdDev: 0.5}},
	}
	history := &stats.HistoryStats{}
	entries := []store.Score{{TitleRomaji: "X"}}

	d, err := buildPageData(genres, dims, history, entries)
	if err != nil {
		t.Fatal(err)
	}
	if d.GenreBreakdownJSON != template.JS(
		`{"labels":["Action"],"datasets":[{"label":"Entries","data":[2]}]}`,
	) {
		t.Errorf("GenreBreakdownJSON = %s", d.GenreBreakdownJSON)
	}
	if d.ScoreByGenreJSON != template.JS(
		`{"labels":["Action"],"datasets":[{"label":"Average Score","data":[7]}]}`,
	) {
		t.Errorf("ScoreByGenreJSON = %s", d.ScoreByGenreJSON)
	}
	if d.DimensionVarianceJSON != template.JS(
		`{"labels":["Story"],"datasets":[{"label":"Std Dev","data":[0.5]}]}`,
	) {
		t.Errorf("DimensionVarianceJSON = %s", d.DimensionVarianceJSON)
	}
	if d.Genres != genres || d.Dimensions != dims || d.History != history || len(d.Entries) != 1 {
		t.Error("stats/entries not passed through to the page data")
	}
	if d.ChartJS == "" {
		t.Error("embedded Chart.js missing")
	}
	if len(d.GeneratedAt) != len("2006-01-02 15:04") {
		t.Errorf("GeneratedAt = %q, want YYYY-MM-DD HH:MM", d.GeneratedAt)
	}
}

// json.Marshal rejects NaN, so a NaN in any chart dataset must surface as an
// error from buildPageData rather than a corrupt page.
func TestBuildPageData_NaNInEachChartFails(t *testing.T) {
	nan := math.NaN()
	ok := func() (*stats.GenreStats, *store.DimensionStatsResponse) {
		return &stats.GenreStats{}, &store.DimensionStatsResponse{}
	}
	tests := []struct {
		name  string
		build func() (*stats.GenreStats, *store.DimensionStatsResponse)
	}{
		{"score by genre", func() (*stats.GenreStats, *store.DimensionStatsResponse) {
			g, d := ok()
			g.ByGenre = []store.GenreScore{{Genre: "A", AvgScore: nan}}
			return g, d
		}},
		{"variance", func() (*stats.GenreStats, *store.DimensionStatsResponse) {
			g, d := ok()
			d.DimensionVariance = []store.DimensionVarianceStat{{Label: "S", StdDev: nan}}
			return g, d
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, d := tt.build()
			if _, err := buildPageData(g, d, &stats.HistoryStats{}, nil); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
