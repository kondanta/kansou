// Package export renders a self-contained HTML snapshot of kansou's scoring
// history and stats. The output is a single file with inline CSS, inline
// Chart.js, and inline data — it needs no network access or external
// dependencies to view.
package export

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"time"

	"github.com/kondanta/kansou/internal/stats"
	"github.com/kondanta/kansou/internal/store"
)

//go:embed static/chart.4.4.4.min.js
var chartJSLib string

//go:embed template.html
var templateSource string

var pageTemplate = template.Must(template.New("export").Funcs(template.FuncMap{
	"formatDate":     func(t time.Time) string { return t.Format("2006-01-02") },
	"formatDateTime": func(t time.Time) string { return t.Format("2006-01-02 15:04") },
}).Parse(templateSource))

// pageData is the template data model for the export HTML page.
type pageData struct {
	GeneratedAt string
	ChartJS     template.JS

	GenreBreakdownJSON    template.JS
	ScoreByGenreJSON      template.JS
	DimensionVarianceJSON template.JS

	Genres     *stats.GenreStats
	Dimensions *store.DimensionStatsResponse
	History    *stats.HistoryStats
	Entries    []store.Score
}

// Generate fetches all stats and entries from s and renders a self-contained
// HTML export.
func Generate(ctx context.Context, s store.Store) ([]byte, error) {
	st := stats.New(s)

	genres, err := st.Genres(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching genre stats: %w", err)
	}
	dimensions, err := st.Dimensions(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching dimension stats: %w", err)
	}
	history, err := st.History(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching history stats: %w", err)
	}
	entries, err := s.ListLatest(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching entries: %w", err)
	}

	data, err := buildPageData(genres, dimensions, history, entries)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := pageTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("rendering export: %w", err)
	}
	return buf.Bytes(), nil
}

// buildPageData assembles the template data model, including the three
// inline chart datasets.
func buildPageData(
	genres *stats.GenreStats, dimensions *store.DimensionStatsResponse,
	history *stats.HistoryStats, entries []store.Score,
) (pageData, error) {
	genreBreakdownJSON, err := chartJSON(genreCountChart(genres.Breakdown))
	if err != nil {
		return pageData{}, err
	}
	scoreByGenreJSON, err := chartJSON(genreScoreChart(genres.ByGenre))
	if err != nil {
		return pageData{}, err
	}
	varianceJSON, err := chartJSON(varianceChart(dimensions.DimensionVariance))
	if err != nil {
		return pageData{}, err
	}

	return pageData{
		GeneratedAt: time.Now().Format("2006-01-02 15:04"),
		ChartJS: template.JS(
			chartJSLib,
		), //nolint:gosec // embedded local asset, not user input
		GenreBreakdownJSON:    genreBreakdownJSON,
		ScoreByGenreJSON:      scoreByGenreJSON,
		DimensionVarianceJSON: varianceJSON,
		Genres:                genres,
		Dimensions:            dimensions,
		History:               history,
		Entries:               entries,
	}, nil
}

// chartData is the Chart.js bar-chart data shape: { labels, datasets }.
type chartData struct {
	Labels   []string  `json:"labels"`
	Datasets []dataset `json:"datasets"`
}

// dataset is one Chart.js dataset within chartData.
type dataset struct {
	Label string    `json:"label"`
	Data  []float64 `json:"data"`
}

// genreCountChart builds chart data for entry count per genre.
func genreCountChart(breakdown []store.GenreStat) chartData {
	labels := make([]string, len(breakdown))
	values := make([]float64, len(breakdown))
	for i, g := range breakdown {
		labels[i] = g.Genre
		values[i] = float64(g.Count)
	}
	return chartData{Labels: labels, Datasets: []dataset{{Label: "Entries", Data: values}}}
}

// genreScoreChart builds chart data for average score per genre.
func genreScoreChart(byGenre []store.GenreScore) chartData {
	labels := make([]string, len(byGenre))
	values := make([]float64, len(byGenre))
	for i, g := range byGenre {
		labels[i] = g.Genre
		values[i] = g.AvgScore
	}
	return chartData{Labels: labels, Datasets: []dataset{{Label: "Average Score", Data: values}}}
}

// varianceChart builds chart data for standard deviation per dimension.
func varianceChart(variance []store.DimensionVarianceStat) chartData {
	labels := make([]string, len(variance))
	values := make([]float64, len(variance))
	for i, v := range variance {
		labels[i] = v.Label
		values[i] = v.StdDev
	}
	return chartData{Labels: labels, Datasets: []dataset{{Label: "Std Dev", Data: values}}}
}

// chartJSON marshals v to JSON safe for direct embedding inside a <script>
// tag. encoding/json escapes <, >, and & by default specifically so its
// output can be embedded in HTML/JS contexts without further sanitisation —
// that default is what makes wrapping the result in template.JS safe here.
func chartJSON(v any) (template.JS, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshalling chart data: %w", err)
	}
	return template.JS(
		b,
	), nil //nolint:gosec // json.Marshal HTML-escapes by default, see comment above
}
