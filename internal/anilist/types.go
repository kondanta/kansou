// Package anilist implements a thin net/http wrapper for the AniList GraphQL API.
// It exposes three typed operations: search by name, fetch by ID, and publish score.
// There are no retries, no caching, and no GraphQL client library. See ADR-004.
package anilist

import "github.com/kondanta/kansou/internal/scoring"

// Media represents the AniList media data used during a scoring session.
// It is returned by both SearchByName and FetchByID.
type Media struct {
	// ID is the AniList media ID — the canonical identifier throughout a session.
	ID int
	// TitleRomaji is the romanised title.
	TitleRomaji string
	// TitleEnglish is the English title (may be empty).
	TitleEnglish string
	// TitleNative is the native-script title.
	TitleNative string
	// Format is the raw AniList format string (TV, MANGA, OVA, etc.).
	Format string
	// Status is the release status (FINISHED, RELEASING, etc.).
	Status string
	// Episodes is the episode count — anime only, 0 if not applicable.
	Episodes int
	// Chapters is the chapter count — manga only, 0 if not applicable.
	Chapters int
	// Genres is the list of genre strings used for genre bias calculation.
	Genres []string
	// Tags is the list of AniList content tags.
	Tags []Tag
	// CoverImage is the URL of the medium-size cover image.
	CoverImage string
	// AverageScore is the AniList community average score (0–100).
	AverageScore int
	// MeanScore is the AniList mean score (0–100).
	MeanScore int
	// MediaType is derived from Format — Anime or Manga.
	MediaType scoring.MediaType
}

// Tag represents an AniList content tag.
type Tag struct {
	// Name is the tag name.
	Name string
	// Rank is the relevance rank (0–100).
	Rank int
	// IsMediaSpoiler indicates this tag contains spoiler information.
	IsMediaSpoiler bool
}

// PublishResult is returned by PublishScore on success.
type PublishResult struct {
	// EntryID is the AniList list entry ID.
	EntryID int
	// Score is the score that was written.
	Score float64
	// Status is the list status set on the entry (e.g. COMPLETED).
	Status string
	// TitleRomaji is the media title confirmed by AniList.
	TitleRomaji string
}

// mediaTypeFromFormat derives MediaType from the AniList format string.
// Anime formats: TV, TV_SHORT, MOVIE, SPECIAL, OVA, ONA, MUSIC.
// All others are treated as Manga.
func mediaTypeFromFormat(format string) scoring.MediaType {
	switch format {
	case "TV", "TV_SHORT", "MOVIE", "SPECIAL", "OVA", "ONA", "MUSIC":
		return scoring.Anime
	default:
		return scoring.Manga
	}
}
