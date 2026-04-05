# ARCHITECTURE.md — kansou

## Overview

`kansou` is a single Go binary that operates in two modes:

- **CLI mode** — interactive, terminal-driven scoring sessions
- **Server mode** — a REST API (`--serve`) that exposes the same core logic over HTTP for web frontends or external tooling

Both modes share identical business logic. The binary entry point branches into one of the two modes based on how it is invoked. There is no local persistence in v1. All state is in-memory for the duration of a session.

---

## High-Level Architecture

```
┌─────────────────────────────────────────────────────────┐
│                        kansou binary                    │
│                                                         │
│  ┌─────────────────┐         ┌─────────────────────┐   │
│  │   CLI Layer      │         │   Server Layer       │   │
│  │  (cobra)         │         │  (chi router)        │   │
│  │                  │         │                      │   │
│  │  score add       │         │  GET  /dimensions    │   │
│  │  media find      │         │  GET  /genres        │   │
│  │                  │         │  GET  /media/search  │   │
│  │                  │         │  GET  /media/{id}    │   │
│  │                  │         │  POST /score         │   │
│  │                  │         │  POST /score/publish │   │
│  │                  │         │  POST /weights       │   │
│  └────────┬─────────┘         └──────────┬──────────┘   │
│           │                              │               │
│           └──────────────┬───────────────┘               │
│                          │                               │
│              ┌───────────▼────────────┐                  │
│              │      Core Logic        │                  │
│              │                        │                  │
│              │  ┌──────────────────┐  │                  │
│              │  │  Scoring Engine  │  │                  │
│              │  │  - Weight calc   │  │                  │
│              │  │  - Genre adjust  │  │                  │
│              │  │  - Renormalize   │  │                  │
│              │  └──────────────────┘  │                  │
│              │                        │                  │
│              │  ┌──────────────────┐  │                  │
│              │  │  Config Loader   │  │                  │
│              │  │  - TOML parsing  │  │                  │
│              │  │  - Validation    │  │                  │
│              │  │  - Defaults      │  │                  │
│              │  └──────────────────┘  │                  │
│              └───────────┬────────────┘                  │
│                          │                               │
│              ┌───────────▼────────────┐                  │
│              │    AniList Client      │                  │
│              │  - Search by name      │                  │
│              │  - Fetch by ID         │                  │
│              │  - Publish score       │                  │
│              │  (raw net/http wrapper)│                  │
│              └───────────┬────────────┘                  │
│                          │                               │
└──────────────────────────┼──────────────────────────────┘
                           │ HTTPS / GraphQL
                           ▼
                  ┌─────────────────┐
                  │   AniList API   │
                  │  graphql.anilist│
                  │     .co/api/v2  │
                  └─────────────────┘
```

---

## Layer Responsibilities

### Entry Point — `main.go`
Single line: calls `cmd.Execute()`. Contains no logic. Swagger API annotations live here so `swag init -g main.go` picks them up.

### CLI Layer — `cmd/`
`package cmd`. Built on `cobra`. Handles user input, calls core logic, and renders output to stdout. The only layer allowed to write to stdout. Does not contain business logic — it orchestrates calls to the scoring engine and AniList client. `cmd/root.go` owns the `App` struct, `Execute()`, `PersistentPreRunE` (config loading + dep wiring), and `newEngine()`. Each subcommand domain has its own file: `media.go`, `score.go`, `serve.go`.

### Server Layer — `internal/server/`
Built on `chi`. Exposes the same operations as the CLI over HTTP. Handles request parsing, response serialisation, and error enveloping. Swagger annotations live here. Does not contain business logic.

### Scoring Engine — `internal/scoring/`
Pure functions. No I/O, no side effects. Takes a config, a set of genres, a media type, and a map of section scores. Returns a final score and optionally a weighted breakdown. Fully unit tested.

Two public entry points:
- `Engine.Score(Entry) (Result, error)` — full scoring session with per-dimension contributions.
- `Engine.Weights(genres, primaryGenre, skipped, overrides) []WeightRow` — weight-only path,
  no scores required. Used by `POST /weights` for live web UI preview. `Score()` delegates to it,
  ensuring a single renormalization path.

### Config Loader — `internal/config/`
Reads `~/.config/kansou/config.toml`. Validates that base weights sum to 1.0. Applies defaults for any missing fields. Returns a validated `Config` struct. Fails loudly on invalid config rather than silently correcting it.

### Logger — `internal/logger/`
Configures the application-wide `log/slog` default logger. `Setup(isServer bool)` is called once in `main` before any other initialisation. CLI mode uses a custom coloured text handler (plain text if not a TTY or `NO_COLOR` is set). Server mode uses the stdlib JSON handler. Log level is controlled by the `LOG_LEVEL` environment variable (`debug`, `info`, `warn`, `error`; default `info`).

### AniList Client — `internal/anilist/`
A thin wrapper around `net/http`. Contains typed Go functions for each GraphQL operation. Reads `ANILIST_TOKEN` from the environment. Returns typed response structs. Never logs the token. Hard fails on non-200 responses or network errors.

---

## Data Flow

### CLI Session — Score a Show

```
User types: kansou score add "Frieren: Beyond Journey's End"
        │
        ▼
CLI: calls AniList client → search by name
        │
        ▼
AniList: returns Media{ID, Title, Genres, Tags, Format, Episodes}
        │
        ▼
CLI: displays media info, prompts for optional primary genre, then prompts for section scores (1–10)
        │
        ▼
Scoring Engine: applies base weights + genre multipliers (contributing-only avg or primary blend) + renormalization
              → returns FinalScore + optional Breakdown
        │
        ▼
CLI: prints score (and breakdown if --breakdown flag is set)
        │
        ▼
CLI: prompts "Publish to AniList? [y/N]"
        │
        ├── y → AniList Client: mutation → writes score to user's AniList account
        │
        └── N / Enter → session ends, score is not published
```

### Self-Insert URL Flow

```
User types: kansou score add --url https://anilist.co/anime/154587
        │
        ▼
CLI: parses media ID from URL → skips search
        │
        ▼
AniList: fetch by ID → same flow as above from this point
```

### Server Mode — Same Flow Over HTTP

```
GET  /dimensions              # list configured scoring dimensions + config_hash
  → returns ordered dimension list for frontend to build score form

GET  /genres                  # list configured genre multiplier blocks
  → returns genres + primary_genre_weight for frontend to build primary genre picker

GET  /media/search?q={query}  # search AniList by name
  → returns Media object

GET  /media/{id}              # fetch media by AniList ID
  → returns Media object

POST /score               { "media_id": 154587, "scores": { ... }, "selected_genres": [...], "primary_genre": "Mystery" }
  → returns FinalScore + Breakdown (selected_genres and primary_genre are optional)
  → breakdown rows include genre_deselected when a deselected genre had an opinion on that dimension

POST /score/publish       { "media_id": 154587, "score": 8.4 }
  → writes to AniList, returns confirmation

POST /weights             { "media_id": 154587, "selected_genres": [...], "primary_genre": "...", "skipped_dimensions": {...}, "weight_overrides": {...} }
  → returns per-dimension final weights without scoring; used for live UI preview
```

**Embedded UI:** `package web` (`web/embed.go`) embeds `web/tribbie/dist` via
`//go:embed all:tribbie/dist` and exports `DistDirFS fs.FS`. `internal/server`
imports `kansouweb "github.com/kondanta/kansou/web"` and passes `kansouweb.DistDirFS`
to `spaHandler`. When `dist/` has not been built yet, `spaHandler` falls back to
the legacy `internal/server/web/index.html`. Build the Vue app with `just build-ui`
(Docker, no local Node required) before `go build` or `just build-all`.

**Web UI initialisation sequence** (Vue SPA):

```
Browser loads /
  → GET /dimensions  ┐  (parallel)
  → GET /genres      ┘
  → user selects media
  → GET /media/search?q=... or GET /media/{id}
  → user fills score form with genre checkboxes (all start checked)
  → genre checkbox change / primary genre change / skip change → POST /weights (debounced 150ms)
    → updates live weight preview in dimension rows
  → POST /score (with selected_genres if any genre was deselected)
  → POST /score/publish  (optional)
```

---

## Binary Invocation

```
kansou serve              # starts REST server on default port (8080)
kansou serve --port 3000

kansou score add "..."    # CLI: score by search (publish prompt included)
kansou score add --url "https://anilist.co/anime/154587"
kansou media find "..."   # CLI: search and display media info only
```

---

## Frontend Compatibility

The REST server is designed to be consumed by a web frontend without modification. The API contract follows these conventions to remain compatible with tooling in the style of `autobrr/netronome` and `autobrr/qui`:

- All responses are JSON
- All errors use a consistent envelope: `{ "error": "message" }`
- CORS headers are configurable in `config.toml`
- The `/health` endpoint returns `200 OK` with no body for liveness checks
- Swagger UI is served at `/swagger/index.html` in server mode

No frontend-specific logic lives in the server layer. The server is a thin HTTP skin over core logic, identical to what the CLI calls.

---

## Configuration

Config is loaded from `~/.config/kansou/config.toml` on startup.
The path is overridable via the `--config` flag.
The server port is overridable via the `--port` flag.
Flag values always take precedence over config file values.

See `docs/CONFIG.md` for the full schema.

---

## Authentication

AniList write operations require a user token. The token is read exclusively from the `ANILIST_TOKEN` environment variable. It is never stored in config, never logged, and never included in error output.

---

## Versioning and Extensibility

The architecture is intentionally minimal for v1. The following are explicitly deferred and must not be introduced without a corresponding ADR update:

- Local persistence (SQLite, flat file, or any other store)
- OAuth2 / PKCE authentication flow
- Background workers or job queues
- Multi-user support
- Caching of AniList responses

The internal package boundary ensures that adding any of the above in a future version requires changes only within the relevant `internal/` package, not across the codebase.
