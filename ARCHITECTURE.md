# ARCHITECTURE.md — kansou

## Overview

`kansou` is a single Go binary that operates in two modes:

- **CLI mode** — interactive, terminal-driven scoring sessions
- **Server mode** — a REST API (`--serve`) that exposes the same core logic over HTTP for web frontends or external tooling

Both modes share identical business logic. The binary entry point branches into one of the two modes based on how it is invoked.

Local persistence is **opt-in** (`KANSOU_DB_TYPE` environment variable — see `docs/CONFIG.md`). When unset, kansou runs exactly as it always has: fully stateless, all state in-memory for the duration of a session ("DBless mode"). When set, kansou persists every scoring session to SQLite or Postgres, enabling `kansou history`, `kansou stats`, `kansou export`, and DB-backed scoring config editing (`kansou config dimension/genre`). See `docs/ADR.md` ADR-027–034.

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
│  │  score add       │         │  GET  /health            │   │
│  │  media find      │         │  GET  /api/v1/dimensions    │   │
│  │  history         │         │  GET  /api/v1/genres        │   │
│  │  stats           │         │  GET  /api/v1/media/search  │   │
│  │  export          │         │  GET  /api/v1/media/{id}    │   │
│  │  db prune        │         │  POST /api/v1/score         │   │
│  │  config          │         │  POST /api/v1/score/publish │   │
│  │                  │         │  POST /api/v1/weights       │   │
│  │                  │         │  GET  /api/v1/db-info       │   │
│  │                  │         │  GET  /api/v1/history*      │   │
│  │                  │         │  GET  /api/v1/stats*        │   │
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
│         ┌────────────────┼────────────────┐              │
│         │                │                │              │
│  ┌──────▼──────┐  ┌──────▼───────┐ ┌──────▼───────┐      │
│  │AniList Client│  │ Store (opt.) │ │ Stats/Export │      │
│  │- Search      │  │ SQLite or    │ │ (opt., needs │      │
│  │- Fetch by ID │  │ Postgres,    │ │  a Store)    │      │
│  │- Publish     │  │ behind one   │ │ - genre/dim/ │      │
│  │(raw net/http)│  │ interface    │ │   history    │      │
│  └──────┬───────┘  └──────────────┘ │   stats      │      │
│         │                            │ - HTML export│      │
│         │                            └──────────────┘      │
└─────────┼──────────────────────────────────────────────────┘
          │ HTTPS / GraphQL
          ▼
 ┌─────────────────┐
 │   AniList API   │
 │  graphql.anilist│
 │     .co/api/v1/v2  │
 └─────────────────┘
```

The Store box is present but unused (`nil`) unless `KANSOU_DB_TYPE` is set — see
"Persistence Layer" below. Stats and Export depend on a configured Store; both
return a clear error rather than silently doing nothing when it's absent.

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
  no scores required. Used by `POST /api/v1/weights` for live web UI preview. `Score()` delegates to it,
  ensuring a single renormalization path.

### Config Loader — `internal/config/`
Reads `~/.config/kansou/config.toml`. Validates that base weights sum to 1.0. Applies defaults for any missing fields. Returns a validated `Config` struct. Fails loudly on invalid config rather than silently correcting it.

### Logger — `internal/logger/`
Configures the application-wide `log/slog` default logger. `Setup(isServer bool)` is called once in `main` before any other initialisation. CLI mode uses a custom coloured text handler (plain text if not a TTY or `NO_COLOR` is set). Server mode uses the stdlib JSON handler. Log level is controlled by the `LOG_LEVEL` environment variable (`debug`, `info`, `warn`, `error`; default `info`).

### AniList Client — `internal/anilist/`
A thin wrapper around `net/http`. Contains typed Go functions for each GraphQL operation. Reads `ANILIST_TOKEN` from the environment. Returns typed response structs. Never logs the token. Hard fails on non-200 responses or network errors.

### Persistence Layer — `internal/store/`
Optional (`KANSOU_DB_TYPE` env var). Defines a single `Store` interface (`store.go`) implemented twice — `sqlite/` (pure-Go `modernc.org/sqlite`, no CGO) and `postgres/` (`jackc/pgx/v5`) — so every caller depends only on the interface, never on a specific backend. Schema migrations (`golang-migrate/migrate/v4`) run from an embedded `migrations/{sqlite,postgres}/` filesystem shared by both backends. `Store` is `nil` in DBless mode; every caller checks for that explicitly rather than relying on a no-op implementation. See ADR-027/028.

### Stats — `internal/stats/`
A thin formatting/aggregation layer over `Store` — no SQL of its own, no business logic beyond bundling several `Store` calls into the shapes `kansou stats` and `GET /api/v1/stats*` need (genre category, dimension category, history category, and a cross-category summary). All actual computation (variance, Pearson correlation, etc.) lives in the `Store` implementations' SQL.

### Export — `internal/export/`
Renders a self-contained HTML file (inline CSS, inline Chart.js v4.4.4 pinned via `go:embed`, inline JSON chart data) from the same `internal/stats` data plus `Store.ListLatest`. No server, no network access needed to view the output.

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
GET  /health                       # liveness check — stays at root, unprefixed

GET  /api/v1/dimensions          # list configured scoring dimensions + config_hash
  → returns ordered dimension list for frontend to build score form

GET  /api/v1/genres              # list configured genre multiplier blocks
  → returns genres + primary_genre_weight for frontend to build primary genre picker

GET  /api/v1/media/search?q={query}  # search AniList by name
  → returns Media object

GET  /api/v1/media/{id}          # fetch media by AniList ID
  → returns Media object

POST /api/v1/score            { "media_id": 154587, "scores": { ... }, "selected_genres": [...], "primary_genre": "Mystery" }
  → returns FinalScore + Breakdown (selected_genres and primary_genre are optional)
  → breakdown rows include genre_deselected when a deselected genre had an opinion on that dimension

POST /api/v1/score/publish    { "media_id": 154587, "score": 8.4 }
  → writes to AniList, returns confirmation

POST /api/v1/weights          { "media_id": 154587, "selected_genres": [...], "primary_genre": "...", "skipped_dimensions": {...}, "weight_overrides": {...} }
  → returns per-dimension final weights without scoring; used for live UI preview

GET  /api/v1/db-info                 # always available — { "db": "sqlite"|"postgres"|null, "live_config"?: bool }

# The following all require a database (KANSOU_DB_TYPE set) and return
# HTTP 503 with the standard error envelope otherwise:
GET    /api/v1/history                   # latest score per entry, newest first
GET    /api/v1/history/{anilist_id}      # all non-deleted scores for one entry, full breakdown
DELETE /api/v1/history/{score_id}        # soft-delete one score by its row ID (not the AniList ID)
GET    /api/v1/stats                     # one-line summary per category
GET    /api/v1/stats/genres              # genre breakdown, score by genre, genre×dimension affinity
GET    /api/v1/stats/dimensions          # variance, consistency, correlation, skip rate, weight overrides
GET    /api/v1/stats/history             # most rescored, outliers, config impact
```

**Embedded UI:** `package web` (`web/embed.go`) embeds `web/dist/` via
`//go:embed all:dist` and exports `DistDirFS fs.FS`. The Vue source lives in the
`web/tribbie/` submodule; `just build-ui` (Docker, no local Node required) builds it
and writes the output to `web/dist/`. `internal/server` imports
`kansouweb "github.com/kondanta/kansou/web"` and passes `kansouweb.DistDirFS`
to `spaHandler`. When `dist/` has not been built yet (only `.gitkeep` present),
`spaHandler` falls back to the legacy `internal/server/web/index.html`.

**Web UI initialisation sequence** (Vue SPA):

```
Browser loads /
  → GET /api/v1/dimensions  ┐  (parallel)
  → GET /api/v1/genres      ┘
  → user selects media
  → GET /api/v1/media/search?q=... or GET /api/v1/media/{id}
  → user fills score form with genre checkboxes (all start checked)
  → genre checkbox change / primary genre change / skip change → POST /api/v1/weights (debounced 150ms)
    → updates live weight preview in dimension rows
  → POST /api/v1/score (with selected_genres if any genre was deselected)
  → POST /api/v1/score/publish  (optional)
```

---

## Binary Invocation

```
kansou serve              # starts REST server on default port (8080)
kansou serve --port 3000

kansou score add "..."    # CLI: score by search (publish prompt included)
kansou score add --url "https://anilist.co/anime/154587"
kansou media find "..."   # CLI: search and display media info only

# The following require KANSOU_DB_TYPE to be set (sqlite or postgres):
kansou history                 # list latest scores
kansou history show "..."      # full breakdown + previous scores
kansou history delete "..."    # soft-delete the latest score for an entry
kansou stats                   # one-line summary per category
kansou stats dimensions        # variance, consistency, correlation, ...
kansou export                  # self-contained HTML export
kansou db prune                # hard-delete soft-deleted score records
kansou config show             # works DBless too
kansou config dimension add pacing --label Pacing --weight 0.1
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

**DBless mode** (default): config is loaded from `~/.config/kansou/config.toml`
on startup. The path is overridable via the `--config` flag.

**Database mode** (`KANSOU_DB_TYPE` set): scoring config (dimensions, genres,
`primary_genre_weight`, `max_multiplier`, `max_history`) is loaded from and
saved to the database instead — `config.toml` becomes a seed/export format
only (`kansou config import`/`export`). See ADR-029.

The server port and CORS origins are configured via `KANSOU_PORT`/
`KANSOU_CORS_ORIGINS` environment variables (the `[server]` section of
`config.toml` is deprecated — see ADR-030), overridable by the `--port` flag.
Flag values always take precedence.

See `docs/CONFIG.md` for the full schema and environment variable reference.

---

## Authentication

AniList write operations require a user token. The token is read exclusively from the `ANILIST_TOKEN` environment variable. It is never stored in config, never logged, and never included in error output.

---

## Versioning and Extensibility

The architecture is intentionally minimal. Local persistence (previously deferred
in v1) is now implemented as an **opt-in** layer — see "Persistence Layer" above
and ADR-027–034 — but only touches `internal/store/`, `internal/stats/`,
`internal/export/`, and additive fields/branches in `cmd/` and `internal/server/`.
DBless mode is unchanged and remains the default.

The following are still explicitly deferred and must not be introduced without a
corresponding ADR update:

- OAuth2 / PKCE authentication flow
- Background workers or job queues
- Multi-user support
- Caching of AniList responses

The internal package boundary ensures that adding any of the above in a future version requires changes only within the relevant `internal/` package, not across the codebase.
