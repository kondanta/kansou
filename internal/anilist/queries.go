package anilist

// searchQuery is the GraphQL query used for searching media by name.
// Returns the best single match from AniList.
const searchQuery = `
query ($search: String, $type: MediaType) {
  Media(search: $search, type: $type) {
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
  SaveMediaListEntry(mediaId: $mediaId, score: $score, status: COMPLETED) {
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
