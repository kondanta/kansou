package anilist

// searchPageQuery is the GraphQL query used for searching media by name.
// Returns up to searchPageSize results via the Page API, sorted by search
// relevance, so the user can pick the correct entry when multiple seasons
// or related entries exist.
const searchPageQuery = `
query ($search: String, $type: MediaType, $perPage: Int) {
  Page(perPage: $perPage) {
    media(search: $search, type: $type, sort: SEARCH_MATCH) {
      id
      title {
        romaji
        english
        native
      }
      format
      status
      episodes
      chapters
      genres
      tags {
        name
        rank
        isMediaSpoiler
      }
      coverImage {
        medium
      }
      averageScore
      meanScore
    }
  }
}`

// searchPageSize is the maximum number of results returned by SearchByNameMulti.
const searchPageSize = 5

// fetchByIDQuery is the GraphQL query used to fetch media by AniList ID.
// Identical field set to searchQuery; only the variable differs.
const fetchByIDQuery = `
query ($id: Int) {
  Media(id: $id) {
    id
    title {
      romaji
      english
      native
    }
    format
    status
    episodes
    chapters
    genres
    tags {
      name
      rank
      isMediaSpoiler
    }
    coverImage {
      medium
    }
    averageScore
    meanScore
  }
}`

// publishMutation is the GraphQL mutation used to write a score to AniList.
// Sets the list entry score and status to COMPLETED if not already set.
const publishMutation = `
mutation ($mediaId: Int, $score: Float) {
  SaveMediaListEntry(mediaId: $mediaId, score: $score) {
    id
    score
    status
    media {
      title {
        romaji
      }
    }
  }
}`
