package anilist

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kondanta/kansou/internal/config"
	"github.com/kondanta/kansou/internal/scoring"
)

func TestParseMediaURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    int
		wantErr string // substring; empty means success
	}{
		{"anime", "https://anilist.co/anime/154587", 154587, ""},
		{"with slug", "https://anilist.co/anime/154587/Sousou-no-Frieren", 154587, ""},
		{"manga", "https://anilist.co/manga/30013", 30013, ""},
		{"trailing slash", "https://anilist.co/anime/1/", 1, ""},
		{"www host", "https://www.anilist.co/anime/5", 5, ""},
		{"with port", "https://anilist.co:443/anime/5", 5, ""},
		{"other host", "https://example.com/anime/1", 0, "does not point to anilist.co"},
		{
			"suffix lookalike",
			"https://anilist.co.evil.example/anime/5",
			0,
			"does not point to anilist.co",
		},
		{"prefix lookalike", "https://notanilist.co/anime/5", 0, "does not point to anilist.co"},
		{
			"unrelated subdomain",
			"https://evil.anilist.co/anime/5",
			0,
			"does not point to anilist.co",
		},
		{"no host", "/anime/5", 0, "does not point to anilist.co"},
		{"no id segment", "https://anilist.co/anime", 0, "cannot extract media ID"},
		{"empty path", "https://anilist.co/", 0, "cannot extract media ID"},
		{"non-numeric id", "https://anilist.co/anime/abc", 0, "not a valid integer"},
		{"zero id", "https://anilist.co/anime/0", 0, "must be a positive integer"},
		{"negative id", "https://anilist.co/anime/-3", 0, "must be a positive integer"},
		{"unparseable", "://bad", 0, "parsing URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMediaURL(tt.url)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got (%d, %v), want error containing %q", got, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("got (%d, %v), want %d", got, err, tt.want)
			}
		})
	}
}

func TestMediaTypeFromFormat(t *testing.T) {
	tests := []struct {
		format string
		want   scoring.MediaType
	}{
		{"TV", scoring.Anime},
		{"TV_SHORT", scoring.Anime},
		{"MOVIE", scoring.Anime},
		{"SPECIAL", scoring.Anime},
		{"OVA", scoring.Anime},
		{"ONA", scoring.Anime},
		{"MUSIC", scoring.Anime},
		{"MANGA", scoring.Manga},
		{"NOVEL", scoring.Manga},
		{"ONE_SHOT", scoring.Manga},
		{"", scoring.Manga},
		{"SOMETHING_NEW", scoring.Manga},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			if got := mediaTypeFromFormat(tt.format); got != tt.want {
				t.Errorf("mediaTypeFromFormat(%q) = %q, want %q", tt.format, got, tt.want)
			}
		})
	}
}

func TestToMedia_MapsEveryField(t *testing.T) {
	var g gqlMedia
	g.ID = 154587
	g.Title.Romaji, g.Title.English, g.Title.Native = "Sousou no Frieren", "Frieren", "葬送のフリーレン"
	g.Format, g.Status, g.Episodes, g.Chapters = "TV", "FINISHED", 28, 0
	g.Genres = []string{"Adventure", "FANTASY"}
	g.Tags = []struct {
		Name           string `json:"name"`
		Rank           int    `json:"rank"`
		IsMediaSpoiler bool   `json:"isMediaSpoiler"`
	}{{"Elf", 90, false}, {"Time Skip", 40, true}}
	g.CoverImage.ExtraLarge = "https://img/cover.png"
	g.BannerImage = "https://img/banner.png"
	g.AverageScore, g.MeanScore = 91, 89

	want := &Media{
		ID: 154587, TitleRomaji: "Sousou no Frieren", TitleEnglish: "Frieren",
		TitleNative: "葬送のフリーレン", Format: "TV", Status: "FINISHED",
		Episodes: 28, Chapters: 0, Genres: []string{"adventure", "fantasy"},
		Tags:       []Tag{{"Elf", 90, false}, {"Time Skip", 40, true}},
		CoverImage: "https://img/cover.png", BannerImage: "https://img/banner.png",
		AverageScore: 91, MeanScore: 89, MediaType: scoring.Anime,
	}
	if got := g.toMedia(); !reflect.DeepEqual(got, want) {
		t.Errorf("toMedia:\n got  %+v\n want %+v", got, want)
	}
}

func TestToMedia_EmptyListsAreNonNil(t *testing.T) {
	got := (&gqlMedia{Format: "MANGA"}).toMedia()
	if got.Genres == nil || got.Tags == nil {
		t.Errorf("want empty non-nil slices, got genres=%v tags=%v", got.Genres, got.Tags)
	}
	if got.MediaType != scoring.Manga {
		t.Errorf("MediaType = %q, want MANGA", got.MediaType)
	}
}

func TestAnnotateGenreConfigStatus(t *testing.T) {
	cfg := &config.Config{Genres: map[string]map[string]float64{
		"Action":        {"story": 1.2}, // mixed-case config key
		"slice_of_life": {"pacing": 0.9},
	}}
	set := NewConfiguredGenreSet(cfg)

	got := AnnotateGenreConfigStatus([]string{"ACTION", "Romance", "slice_of_life"}, set)
	want := []GenreConfigStatus{
		{"action", true}, {"romance", false}, {"slice_of_life", true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	if empty := AnnotateGenreConfigStatus(nil, set); empty == nil || len(empty) != 0 {
		t.Errorf("nil input: got %#v, want empty non-nil slice", empty)
	}
}
