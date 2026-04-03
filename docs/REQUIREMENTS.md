# REQUIREMENTS.md — kansou

## Purpose

`kansou` (感想) is a personal anime and manga scoring tool. It fetches media metadata
from AniList, guides the user through a structured per-dimension scoring session,
computes a weighted final score adjusted for genre, and publishes the result back
to the user's AniList account.

It is opinionated by design. The scoring model reflects a specific viewing philosophy
(see `docs/ADR.md` for the decisions behind it) and is configurable without recompilation.

---

## Scope — v1

The following defines what v1 does and explicitly does not do.

### In Scope
- Fetch anime and manga metadata from AniList by search query or direct URL
- Guide the user through scoring 7 defined dimensions
- Compute a weighted, genre-adjusted final score
- Display the final score and optionally a per-dimension weighted breakdown
- Publish the final score to the user's AniList account
- Operate as both a CLI tool and a REST server from a single binary
- Load all scoring configuration from a TOML file

### Out of Scope (v1)
- Local persistence of any kind (no database, no flat file, no cache)
- OAuth2 authentication flow (token via environment variable only)
- Background jobs, scheduled scoring, or batch processing
- Multi-user support
- Any frontend — the REST server exposes an API only

---

## Users

`kansou` has exactly one user type in v1: **the person who runs it.**
It is a personal tool. There is no concept of accounts, roles, or shared state.

---

## Functional Requirements

### FR-01 — Media Discovery

**FR-01a — Search by name**
The user provides a search string. `kansou` queries the AniList API and returns
up to 5 results sorted by relevance. If only one result is found it is selected
automatically. If multiple results are found, the user picks from a numbered list.
If no match is found, the user is informed and the session ends.

**FR-01b — Fetch by URL**
The user provides a direct AniList URL (e.g. `https://anilist.co/anime/154587`).
`kansou` parses the media ID from the URL and fetches metadata directly,
skipping the search step entirely.

**FR-01c — Media type detection**
The media type (Anime or Manga) is determined from the AniList response.
The scoring session adapts its Production dimension criteria label accordingly
(see FR-03). No other behaviour changes.

---

### FR-02 — Scoring Session

**FR-02a — Dynamic dimensions**
The set of scoring dimensions is defined entirely by the `[dimensions]` block in `config.toml`.
There is no hardcoded list of dimensions in the application. The scoring session iterates
over whatever dimensions are configured, in the order they appear in the config file.

The following are the **built-in defaults**, used when no config file is present.
They serve as the reference implementation of the scoring philosophy, not as a fixed constraint:

| Key | Label | What is evaluated |
|-----|-------|-------------------|
| story | Story | Plot, hook, themes, narrative conclusion |
| characters | Characters | Relatability, arcs, cast chemistry |
| production | Production | Anime: animation, voice acting, OST. Manga: art style, character design, paneling |
| world_building | World Building | Setting, rules/systems, lore immersion |
| pacing | Pacing | Flow, momentum, absence of drag or rush |
| enjoyment | Enjoyment | Gut feeling, anticipation for the next episode/chapter |
| value | Value | Rewatch/reread value, staying power |

A user may add, remove, or rename any dimension by editing their config.
The application treats all dimensions uniformly — no dimension is special-cased in code.

**FR-02b — Score input**
Each dimension is scored on a scale of 1–10. Decimal values are accepted (e.g. 7.5).
Values outside 1–10 are rejected with a clear error message and the user is re-prompted.

**FR-02c — Skippable dimensions**
A user may mark a dimension as not applicable during a scoring session by
entering `s` or `skip` at the score prompt. Skipped dimensions are excluded
from the weight pool entirely. The remaining dimension weights renormalize
to sum to 1.0 before the final score is calculated.

Skipped dimensions are recorded in the result breakdown with a `[skipped]`
annotation. They contribute 0 to the final score and 0 to the weight pool.

A session where all dimensions are skipped is invalid and is rejected before
calculation.

**FR-02d — No silent skipping**
Dimensions may only be skipped by explicit user input during the session.
There is no automatic skipping based on media type or any other condition.

---

### FR-03 — Score Calculation

**FR-03a — Base weights**
Each dimension defined in config carries a weight. The weights of all configured
dimensions must sum to 1.0. The config loader validates this on startup.

Built-in defaults (used when no config file is present):

| Dimension | Default Weight |
|-----------|---------------|
| Story | 25% |
| Enjoyment | 20% |
| Characters | 15% |
| Production | 15% |
| Pacing | 10% |
| World Building | 10% |
| Value | 5% |

Adding a new dimension requires adding its weight to config. The validator
ensures the total remains 1.0 — it will reject a config where a new dimension
is added without rebalancing the other weights.

**FR-03b — Genre adjustment**
Each genre defined in config carries a multiplier per dimension (default 1.0 if unspecified).
When a media entry has multiple genres, the multiplier for each dimension is the
**average** of that dimension's multiplier across all matched genres.
Genres present on the entry but absent from config are ignored.

Dimensions marked `bias_resistant = true` in config always receive a multiplier
of exactly 1.0, regardless of what any genre block defines. By default, Enjoyment
and Value are bias-resistant. This reflects the philosophy that personal, subjective
experience should not be shifted by genre context.

**FR-03c — Renormalization**
After genre multipliers are applied, the effective weights are renormalized
so they sum to 1.0 before the final score is computed.

**FR-03d — Final score formula**

```
effective_weight(s)  = base_weight(s) × average_genre_multiplier(s)
                       (skipped dimensions excluded from pool)
final_weight(s)      = effective_weight(s) / Σ effective_weight(all non-skipped)
final_score          = Σ ( section_score(s) × final_weight(s) )
```

The final score is a decimal rounded to two decimal places for display.

**FR-03e — Score provenance**
Every `Result` carries full provenance metadata regardless of whether
`--breakdown` is requested. The breakdown is always computed — the caller
decides whether to display it.

Per-dimension provenance includes:
- Base weight before genre adjustment
- Which genres fired a multiplier for this dimension and at what value
- The averaged multiplier actually applied
- Whether the dimension is bias-resistant
- Whether a `--weight` override was applied
- Whether the dimension was skipped

Session-level provenance includes:
- Media ID, title, type, and AniList URL
- All genres returned by AniList
- Which genres matched a config block
- A SHA256 hash of the serialised dimensions config at time of scoring

**FR-03f — Per-session weight override**
The `score add` command accepts an optional `--weight` flag that overrides
the genre-adjusted weight for specific dimensions for that session only.

```
kansou score add "Mushishi" --weight pacing=0.05,world_building=0.20
```

Overridden dimensions are fixed at the provided value. The remaining
dimensions are rescaled proportionally so all weights still sum to 1.0.
Overrides are never persisted. They apply to one scoring session only.
Overriding a bias-resistant dimension is permitted — the user is explicitly
overriding their own default.

**FR-03f — Breakdown mode**
When the `--breakdown` flag is provided (CLI) or the breakdown field is requested (API),
the response includes a table showing each section's score, its final weight after
genre adjustment, and its weighted contribution to the total.

---

### FR-04 — Publishing

**FR-04a — Explicit publish step**
Publishing requires explicit user confirmation. In the CLI, `score add` displays
the final score and then prompts `Publish to AniList? [y/N]`. Only a `y` response
triggers the publish. Entering anything else (including pressing Enter) skips
publishing silently. Via the API, the caller must send `POST /score/publish`
explicitly with `media_id` and `score`. A calculated score is never automatically
published.

**FR-04a-i — Implicit dimension skipping (API)**
When calling `POST /score`, any dimension defined in server config that is absent
from the `scores` map is automatically treated as skipped (N/A). The client does
not need to declare skipped dimensions explicitly. `weight_overrides` is an
optional field for per-session weight adjustment; omitting it uses config weights.

**FR-04b — What is published**
Only the final numeric score is written to AniList. The breakdown and
per-dimension scores are not transmitted to AniList in v1.

**FR-04c — Confirmation**
After a successful publish, the user receives confirmation including the media title
and the score that was written.

---

### FR-05 — AniList Integration

**FR-05a — Authentication**
The AniList user token is read from the `ANILIST_TOKEN` environment variable.
If the variable is unset or empty, `kansou` exits with a clear error message before
making any network request.

**FR-05b — Failure handling**
If AniList is unreachable or returns a non-200 response, `kansou` surfaces a
human-readable error and stops. There are no retries and no fallback in v1.

**FR-05c — Required AniList data**
The following fields are fetched per media entry:

```graphql
id
title { romaji english }
format          # ANIME | MANGA | ONE_SHOT | NOVEL | ...
genres
tags { name rank }
episodes        # anime only
chapters        # manga only
status
```

---

### FR-06 — Configuration

**FR-06a — Config file location**
Default: `~/.config/kansou/config.toml`
Overridable via `--config` flag.

**FR-06b — Configurable values**
- Dimension definitions (key, label, description, weight) — fully dynamic
- Genre multiplier definitions, keyed to dimension keys
- Server port (default: 8080)
- CORS allowed origins

**FR-06c — Validation**
The config loader validates on startup:
- All configured dimension weights sum to 1.0 (±0.001 tolerance)
- All dimension keys referenced in genre multiplier blocks exist in the dimensions config
- Port is a valid integer in range 1024–65535

Invalid config causes an immediate exit with a descriptive error.
`kansou` never silently corrects or ignores invalid config values.

**FR-06d — Defaults**
If no config file is found, `kansou` runs with built-in defaults and informs
the user that no config file was loaded. The built-in defaults define the
seven reference dimensions with their labels, descriptions, and weights.

---

### FR-07 — REST Server

**FR-07a — Endpoints**

| Method | Path | Description |
|--------|------|-------------|
| GET | /health | Liveness check. Returns 200 with no body. |
| GET | /dimensions | List configured scoring dimensions in order |
| GET | /media/search?q={query} | Search AniList by name |
| GET | /media/{id} | Fetch media by AniList ID |
| POST | /score | Calculate score for a media entry |
| POST | /score/publish | Publish a score to AniList |

**FR-07a-i — Dimension sync contract**
`GET /dimensions` returns the ordered list of scoring dimensions as configured
on the server, including each dimension's key, label, description, and base weight.
The response includes a `config_hash` field (SHA256 of the serialised dimensions
config) that clients can store and compare to detect when the dimension list has
changed. Frontends must use the keys returned by this endpoint as the keys in the
`scores` map when calling `POST /score` — dimension keys are defined by server
config and must not be hardcoded on the client.

**FR-07b — Error envelope**
All errors return JSON in this shape:
```json
{ "error": "human readable description of what went wrong" }
```

**FR-07c — Swagger**
Swagger documentation is auto-generated via `swaggo/swag` and served at
`/swagger/index.html` when the server is running.

**FR-07d — CORS**
Allowed origins are configurable in `config.toml`. Default is localhost only.

---

### FR-08 — CLI Commands

Full command reference is in `docs/CLI.md`. Summary:

| Command | Description |
|---------|-------------|
| `kansou serve` | Start the REST server |
| `kansou media find <query>` | Search for media and display info |
| `kansou score add <query>` | Start a scoring session by search (includes publish prompt) |
| `kansou score add --url <url>` | Start a scoring session by AniList URL (includes publish prompt) |

---

## Non-Functional Requirements

**NFR-01 — No persistence**
`kansou` v1 stores nothing to disk beyond the config file the user maintains themselves.
No database, no cache, no log files, no session state.

**NFR-02 — Single binary**
The entire tool ships as a single compiled Go binary with no runtime dependencies.

**NFR-03 — Startup time**
The binary must be ready to accept input within 500ms on any modern machine,
excluding network latency for the AniList fetch.

**NFR-04 — Graceful shutdown**
The REST server handles `SIGINT` and `SIGTERM` with a graceful shutdown,
allowing in-flight requests to complete before exiting.

**NFR-05 — Token safety**
The AniList token must never appear in log output, error messages, or HTTP responses.

**NFR-06 — Frontend readiness**
The REST API must be consumable by a web frontend without modification.
This means consistent JSON envelopes, documented endpoints, and configurable CORS.
