# ANILIST_INTEGRATION.md — AniList Integration Reference

## Overview

`kansou` integrates with the AniList GraphQL API for three operations:

1. **Search** — find media by name
2. **Fetch** — retrieve media by ID (used for direct URL input)
3. **Publish** — write a final score to the user's AniList account

All three operations are implemented as typed Go functions in `internal/anilist/`.
There is no GraphQL client library — requests are made via raw `net/http` with
a thin JSON wrapper. See `ADR-004` for the reasoning.

---

## Authentication

The AniList user token is read from the `ANILIST_TOKEN` environment variable.

```bash
export ANILIST_TOKEN=your_token_here
```

**How to obtain a token:**
1. Go to https://anilist.co/settings/developer
2. Create a new client (any name, redirect URI not needed for personal use)
3. Use the client ID to generate a token via AniList's implicit OAuth flow:
   `https://anilist.co/api/v2/oauth/authorize?client_id={id}&response_type=token`
4. Copy the token from the redirect URL and export it as `ANILIST_TOKEN`

If `ANILIST_TOKEN` is unset or empty, `kansou` exits immediately with:

```
error: ANILIST_TOKEN environment variable is not set
       set it with: export ANILIST_TOKEN=your_token_here
       see docs/ANILIST_INTEGRATION.md for how to obtain a token
```

The token is never logged, never included in error messages, and never written
to disk by `kansou`.

---

## API Endpoint

All requests go to:

```
POST https://graphql.anilist.co
Content-Type: application/json
Authorization: Bearer {ANILIST_TOKEN}   (publish only — search and fetch are public)
```

Search and fetch do not require authentication. The `Authorization` header is
only sent for the publish mutation.

---

## Operations

### 1. Search by Name

**Triggered by:** `kansou media find <query>` and `kansou score add <query>`

Returns up to 5 results sorted by AniList's `SEARCH_MATCH` relevance ranking,
allowing the user to pick the correct entry when multiple seasons or related
entries exist (e.g. a series split across cours).

**GraphQL query:**

```graphql
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
        extraLarge
      }
      bannerImage
      averageScore
      meanScore
    }
  }
}
```

**Variables:**
```json
{
  "search": "Frieren",
  "type": "ANIME",
  "perPage": 5
}
```

The `type` variable is supplied via the `--type` flag (`anime` or `manga`).
If omitted, AniList searches across all media types.

**On a single result:** returned immediately without prompting.

**On multiple results:** the CLI presents a numbered picker; the REST API
returns the full array and the client selects.

**On no results:**
```
error: no results found for "your search query"
       try a different search term or use --url to provide a direct AniList link
```

---

### 2. Fetch by ID

**Triggered by:** `kansou score add --url https://anilist.co/anime/154587`

The media ID is parsed from the URL path. The URL format is:
```
https://anilist.co/{type}/{id}
https://anilist.co/{type}/{id}/{slug}   # slug is ignored
```

**GraphQL query:**

```graphql
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
}
```

This is identical to the search query with `id` substituted for `search`.
Both queries return the same `Media` struct and share the same response
parsing logic.

---

### 3. Publish Score

**Triggered by:** answering `y` at the publish prompt in `kansou score add` (CLI),
or `POST /score/publish` (API)

**GraphQL mutation (score only):**

```graphql
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
}
```

**GraphQL mutation (score + notes, used when `--notes` flag is set or `notes` field is non-empty):**

```graphql
mutation ($mediaId: Int, $score: Float, $notes: String) {
  SaveMediaListEntry(mediaId: $mediaId, score: $score, notes: $notes) {
    id
    score
    status
    media {
      title {
        romaji
      }
    }
  }
}
```

When notes are requested, an additional query is issued first to fetch the existing
list entry notes. If any exist, the new scoring block is appended after a `---`
separator so prior notes are not overwritten.

**Fetch existing notes query (notes path only):**

```graphql
query ($mediaId: Int) {
  Media(id: $mediaId) {
    mediaListEntry {
      notes
    }
  }
}
```

**Variables (score only):**
```json
{
  "mediaId": 154587,
  "score": 8.79
}
```

**Variables (with notes):**
```json
{
  "mediaId": 154587,
  "score": 8.79,
  "notes": "Frieren: Beyond Journey's End\nScore: 9.73 / 10  [kansou]\n..."
}
```

AniList accepts scores on a 1–10 scale with up to one decimal place by default,
matching `kansou`'s output format. The mutation also sets the list status to
`COMPLETED` if the entry is not already on the user's list, via the `status`
field on `SaveMediaListEntry`.

**Successful publish output:**
```
✓ Score published to AniList
  Frieren: Beyond Journey's End — 8.79
```

**On failure (CLI):**
```
error: failed to publish score to AniList: {reason}
       your calculated score was 8.79
```

---

## Data Model

The `Media` struct returned by both search and fetch operations:

```go
// Media represents the AniList media data used during a scoring session.
type Media struct {
    ID           int      // AniList media ID — canonical identifier
    TitleRomaji  string   // Romanised title
    TitleEnglish string   // English title (may be empty)
    TitleNative  string   // Native script title
    Format       string   // TV, MANGA, OVA, etc.
    Status       string   // FINISHED, RELEASING, etc.
    Episodes     int      // anime only — 0 if not applicable
    Chapters     int      // manga only — 0 if not applicable
    Genres       []string // used for genre bias calculation
    Tags         []Tag    // AniList content tags
    CoverImage   string   // URL of cover image (extra-large size)
    BannerImage  string   // URL of banner image (may be empty)
    // Note: Genres is normalised to lowercase on ingestion. All genre strings
    // throughout the system are lowercase from this point forward.
    AverageScore int      // AniList community average (0–100)
    MeanScore    int      // AniList mean score (0–100)
    MediaType    scoring.MediaType // derived from Format
}

// Tag represents an AniList content tag.
type Tag struct {
    Name           string
    Rank           int  // relevance rank 0–100
    IsMediaSpoiler bool
}
```

**On `MediaType` derivation:**
The `MediaType` field is not returned by AniList directly — it is derived
from the `Format` field by the client after parsing the response:

```go
func mediaTypeFromFormat(format string) scoring.MediaType {
    switch format {
    case "TV", "TV_SHORT", "MOVIE", "SPECIAL", "OVA", "ONA", "MUSIC":
        return scoring.Anime
    default:
        return scoring.Manga
    }
}
```

---

## Error Handling

All AniList errors follow the same pattern: surface a clean human-readable
message and stop. There are no retries and no fallback in v1.

| Condition | Behaviour |
|-----------|-----------|
| `ANILIST_TOKEN` unset | Exit immediately before any network request |
| Network unreachable | Exit with "AniList is currently unreachable" |
| HTTP non-200 response | Exit with status code and AniList error message |
| GraphQL `errors` in response body | Exit with the first error message from AniList |
| No results for search query | Exit with suggestion to use `--url` |
| Invalid URL format for `--url` | Exit before making any network request |
| Publish failure | Exit with the calculated score printed so it is not lost |

**GraphQL error response shape** (AniList standard):
```json
{
  "errors": [
    {
      "message": "Not Found.",
      "status": 404
    }
  ]
}
```

The client checks for the presence of `errors` in every response before
attempting to parse `data`.

---

## REST Endpoints — Genre and Score

### GET /genres

Returns all configured genre multiplier blocks and the primary genre blend ratio.
Used by web clients to populate the primary genre picker.

**Response shape:**
```json
{
  "primary_genre_weight": 0.6,
  "genres": [
    { "genre": "action",  "multipliers": { "production": 1.4, "pacing": 1.3, "story": 0.8 } },
    { "genre": "mystery", "multipliers": { "story": 1.5, "pacing": 1.3, "world_building": 1.2 } }
  ]
}
```

- `primary_genre_weight` — the configured blend ratio (mirrors `primary_genre_weight` in config.toml).
- `genres` — alphabetically sorted list of configured genre blocks.
- `multipliers` — only dimensions explicitly configured for that genre; dimensions absent here
  are excluded from the contributing-only average (not treated as 1.0).

### POST /score/publish — notes field

The `POST /score/publish` request body accepts an optional `notes` string:

```json
{
  "media_id": 154587,
  "score": 9.73,
  "notes": "Frieren: Beyond Journey's End\nScore: 9.73 / 10  [kansou]\n..."
}
```

- If `notes` is non-empty, the server fetches the existing list entry notes and
  appends the new block after a `---` separator before saving.
- If `notes` is omitted or empty, only the score is written (existing notes unchanged).
- The web UI's `buildNotes()` function produces a compatible string from a `POST /score` result.

---

### POST /score — selected_genres and primary_genre fields

The `POST /score` request body accepts optional `selected_genres` and `primary_genre` fields:

```json
{
  "media_id": 154587,
  "scores": { "story": 9, "characters": 8.5 },
  "selected_genres": ["Mystery"],
  "primary_genre": "Mystery"
}
```

- `selected_genres` — when present, restricts multiplier calculation to this subset of the
  media's AniList genres. Genres absent from this list are excluded from the active set.
  When omitted or empty, all matched config genres participate (CLI-compatible behaviour).
- `primary_genre` — designates one genre as constitutive for blended multiplier calculation.
  When `selected_genres` is provided, `primary_genre` must be in that set; otherwise it must
  be in the media's full AniList genre list. Case-insensitive matching. If omitted or empty,
  contributing-only averaging applies with no primary.
- The response `meta` object includes `genres_active` (the active genres that participated in
  calculation), `primary_genre`, and `primary_genre_weight` for provenance.
- Breakdown rows include `genre_deselected: true` when a deselected genre had a configured
  multiplier for that dimension.

---

### POST /weights — live weight preview (ADR-023)

Returns per-dimension final weights without requiring scores. Used by the web UI to show
a live weight preview as the user adjusts genre selection or skips dimensions.

**Request body:**
```json
{
  "media_id": 154587,
  "selected_genres": ["Mystery"],
  "primary_genre": "Mystery",
  "skipped_dimensions": { "value": true },
  "weight_overrides": { "pacing": 0.05 }
}
```

- `media_id` — required. Used to fetch the media's genre list.
- `selected_genres` — optional. Same semantics as in `POST /score`.
- `primary_genre` — optional. Same validation rules as in `POST /score`.
- `skipped_dimensions` — optional. Keys mapped to `true` are excluded from the weight pool.
- `weight_overrides` — optional. Same validation rules as in `POST /score`.

**Response shape:**
```json
{
  "dimensions": [
    {
      "key": "story",
      "label": "Story",
      "base_weight": 0.25,
      "multiplier": 1.5,
      "final_weight": 0.28,
      "skipped": false,
      "bias_resistant": false,
      "weight_override": false,
      "primary_genre_multiplier": 1.5
    }
  ]
}
```

- `multiplier` — the blended genre multiplier (1.0 for bias-resistant dimensions or when no
  matched genre has an opinion on this dimension).
- `final_weight` — weight after genre adjustment, renormalization, and overrides. This is
  identical to the `final_weight` field that `POST /score` would produce.
- `primary_genre_multiplier` — the raw multiplier the primary genre defines for this
  dimension. 0 when no primary genre is set, when the dimension is bias-resistant, or when
  the primary genre has no configured entry for this dimension.

**Validation errors** (same rules as `POST /score`):
- Unknown key in `weight_overrides` → 400
- `weight_overrides` value ≤ 0 or > 1 → 400
- Sum of `weight_overrides` ≥ 1.0 → 400
- `primary_genre` not in the active genre set → 400

---

## Rate Limiting

AniList enforces a rate limit of 90 requests per minute. `kansou` makes at
most 2 requests per scoring session (one fetch, one publish), so rate limiting
is not a practical concern for normal use. No rate limit handling is implemented
in v1.

---

## Spoiler Tags

AniList tags include an `IsMediaSpoiler` field. `kansou` fetches these tags
as part of the media data but does not display them to the user during a
scoring session. They are available in the full media response for future use.

---

## AniList Score Format

AniList supports multiple score formats per user (10-point, 100-point, stars, etc.).
`kansou` always submits scores on the **10-point decimal scale** regardless of
the user's AniList score format setting. AniList normalises the submitted value
to the user's preferred format on their end. No format detection or conversion
is required on `kansou`'s side.
