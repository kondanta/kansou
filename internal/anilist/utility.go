package anilist

import (
	"strings"

	"github.com/kondanta/kansou/internal/config"
)

// GenreConfigStatus reports whether a single AniList genre has a matching
// entry in the user's configured genre multipliers.
type GenreConfigStatus struct {
	Name         string
	IsConfigured bool
}

// ConfiguredGenreSet is a case-insensitive lookup of the user's configured
// genre multipliers, built once per config snapshot and reused across
// multiple AnnotateGenreConfigStatus calls (e.g. once per search result)
// instead of rebuilding it from cfg.Genres on every call.
type ConfiguredGenreSet map[string]map[string]float64

// NewConfiguredGenreSet builds a ConfiguredGenreSet from cfg.
func NewConfiguredGenreSet(cfg *config.Config) ConfiguredGenreSet {
	return ConfiguredGenreSet(config.LowercaseGenreKeys(cfg.Genres))
}

// AnnotateGenreConfigStatus checks whether each genre in anilistGenres has a
// matching entry in cfgGenres.
func AnnotateGenreConfigStatus(anilistGenres []string, cfgGenres ConfiguredGenreSet) []GenreConfigStatus {
	checkedGenres := make([]GenreConfigStatus, 0, len(anilistGenres))

	for _, genre := range anilistGenres {
		genreLower := strings.ToLower(genre)
		_, isConfigured := cfgGenres[genreLower]
		checkedGenres = append(checkedGenres, GenreConfigStatus{
			Name:         genreLower,
			IsConfigured: isConfigured,
		})
	}

	return checkedGenres
}
