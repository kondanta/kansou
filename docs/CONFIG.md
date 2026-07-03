# CONFIG.md — kansou Configuration Reference

## Location

Default: `~/.config/kansou/config.toml`
Override: `--config /path/to/config.toml`

If no config file is found at startup, `kansou` runs with built-in defaults
and prints a notice to stderr:

```
notice: no config file found at ~/.config/kansou/config.toml, using built-in defaults
```

This is not an error. `kansou` is fully functional without a config file.

**This section describes DBless mode.** When `KANSOU_DB_TYPE` is set (see
below), scoring config lives in the database instead — `config.toml` becomes
a seed/export format only. See "Database Mode" below (ADR-029).

---

## Environment Variables

All environment variables are optional. `ANILIST_TOKEN` (required only for
publishing scores) is documented in `docs/ANILIST_INTEGRATION.md`, never here
or in any config file.

| Variable | Default | Description |
|---|---|---|
| `KANSOU_DB_TYPE` | unset (DBless) | `sqlite` or `postgres`. Enables persistence, history, stats, and export. Unset = kansou behaves exactly as it always has. |
| `KANSOU_DB_PATH` | `~/.local/share/kansou/kansou.db` | SQLite database file path. Only read when `KANSOU_DB_TYPE=sqlite`. |
| `POSTGRES_HOST` | — | Postgres host. Required when `KANSOU_DB_TYPE=postgres`. |
| `POSTGRES_PORT` | `5432` | Postgres port. |
| `POSTGRES_USER` | — | Postgres username. Required when `KANSOU_DB_TYPE=postgres`. |
| `POSTGRES_PASSWORD` | — | Postgres password. Never logged, never included in error messages, never appears in DSN strings passed to the driver. |
| `POSTGRES_DB` | — | Postgres database name. Required when `KANSOU_DB_TYPE=postgres`. |
| `KANSOU_PORT` | `8080` | REST server port. Replaces `[server].port`, now deprecated (ADR-030). `--port` flag still takes precedence. |
| `KANSOU_CORS_ORIGINS` | `http://localhost:3000,http://localhost:5173,http://localhost:8080` | Comma-separated CORS allowed origins. Replaces `[server].cors_allowed_origins`. |
| `TRUST_PROXY` | `false` | Set to `true` when kansou sits behind exactly one reverse proxy/gateway hop (e.g. an Envoy Kubernetes Gateway) that sets `X-Forwarded-For`. Controls how the client IP used for per-IP rate limiting on `/media/search`, `/media/{id}`, `/score`, and `/score/publish` is resolved. Leave unset for direct-exposed deployments (e.g. a bare `docker run`) — trusting `X-Forwarded-For` there would let clients spoof their rate-limit bucket. |

If `KANSOU_DB_TYPE` is set to anything other than `sqlite` or `postgres`,
`kansou` prints an error and exits with code 1 — there is no default database
type to silently fall back to.

---

## Database Mode

Set `KANSOU_DB_TYPE=sqlite` or `KANSOU_DB_TYPE=postgres` to enable persistent
scoring history, `kansou stats`, `kansou history`, and `kansou export`. See
`docs/ADR.md` ADR-027–034 for the full design rationale.

When a database is configured:

- **Scoring config moves to the database.** `LoadScoringConfig`/
  `SaveScoringConfig` replace `config.toml` as the source of truth. On first
  run against an empty database, kansou seeds it from `config.toml` (or
  built-in defaults if no file exists) automatically.
- **`config.toml` becomes a seed/export format.** Use `kansou config export`
  to write the current DB config out to a file, and `kansou config import` to
  load a file's contents into the database. Both DBless-mode config commands
  (`show`, `import`, `export`) work regardless of database mode; `dimension`
  and `genre` subcommands require a database.
- **`kansou db prune`** hard-deletes soft-deleted score rows (see
  "`max_history`" below and ADR-031). This is irreversible and prompts for
  confirmation.
- If the database is unreachable or migrations fail, `kansou` fails loudly
  before reaching any prompt — there is no silent fallback to DBless mode.

DBless mode (`KANSOU_DB_TYPE` unset) is unaffected by anything in this
section — every command that requires a database returns a clear error
(`"... requires a database — set KANSOU_DB_TYPE to enable"`) rather than
silently doing nothing.

### Deploying with Helm

The chart in `charts/kansou/` sets the environment variables above from
`values.yaml` — you don't set them directly in a Kubernetes deployment.

| Chart value | Maps to |
|---|---|
| `db.type` | `KANSOU_DB_TYPE` (`""`, `sqlite`, or `postgres`) |
| `db.sqlite.path` | `KANSOU_DB_PATH`. Must live under `/data` — the chart mounts a PVC (`db.sqlite.persistence`) there when `db.type: sqlite`. |
| `db.postgres.host` / `port` / `database` / `user` | `POSTGRES_HOST` / `POSTGRES_PORT` / `POSTGRES_DB` / `POSTGRES_USER` |
| `db.postgres.password` | `POSTGRES_PASSWORD`, stored in the chart's Secret, never a plain env value |
| `corsAllowedOrigins` | `KANSOU_CORS_ORIGINS` (joined with commas) |
| `service.port` | `KANSOU_PORT` |
| `trustProxy` | `TRUST_PROXY`. Defaults to `true` — the chart assumes a fronting gateway/ingress (e.g. Envoy Gateway) adds one `X-Forwarded-For` hop. |

Leaving `db.type` empty deploys kansou stateless, same as an unset
`KANSOU_DB_TYPE` locally. The chart refuses to render if `db.type` is set to
anything other than `""`, `sqlite`, or `postgres`, or if `db.postgres.password`
is missing while `db.type: postgres`.

---

## `max_history`

Controls how many previous scores are kept per media entry (stored in
`config_scalars` in DB mode, or as a top-level `max_history` key in
`config.toml` in DBless mode):

| Value | Behavior |
|---|---|
| `0` (default) | Keep only the latest score. Previous scores are soft-deleted on every new score. |
| `N` (positive integer) | Keep the `N` most recent scores per media. Older ones are soft-deleted. |
| `-1` | Keep all scores forever. No soft deletion ever happens. |

Soft-deleted rows still occupy space until `kansou db prune` hard-deletes
them. `max_history` only controls *retention count*, not *when* pruning
happens — pruning is always a separate, explicit, irreversible action.

---

## Validation Rules

The config loader validates the following on every startup. Any violation
causes an immediate exit with a descriptive error message.

- All dimension weights must sum to 1.0 (±0.001 tolerance for float rounding)
- All dimension keys referenced in `[genres.*]` blocks must exist in `[dimensions]`
- All genre multiplier values must be > 0.0 and ≤ `max_multiplier` (default 2.0)
- `primary_genre_weight`, if set, must be in the range 0.0–1.0
- Server port must be an integer in the range 1024–65535
- Every dimension must have a non-empty `label`
- Every dimension `weight` must be > 0.0 and ≤ 1.0

`kansou` never silently corrects invalid config. If your weights sum to 0.99
due to a rounding error, the validator will tell you exactly which values are
involved and what the current sum is.

---

## Full Annotated Example

```toml
# ---------------------------------------------------------------
# max_multiplier
#
# Upper bound for all genre bias multiplier values.
# Any value in a [genres.*] block must be > 0.0 and ≤ this.
# Default: 2.0. Raise it if you need more aggressive genre bias.
# Use --weight for one-off per-session adjustments instead.
# ---------------------------------------------------------------

max_multiplier = 2.0

# ---------------------------------------------------------------
# primary_genre_weight
#
# Blend ratio for --primary-genre / primary_genre support (ADR-022).
# When a primary genre is designated, the effective multiplier for each
# non-bias-resistant dimension is:
#
#   final = (primary_mult × primary_genre_weight)
#         + (secondary_avg × (1 − primary_genre_weight))
#
# where:
#   primary_mult  — raw multiplier from the primary genre for this dimension
#                   (1.0 if the primary genre has no configured entry for it)
#   secondary_avg — contributing-only average across all other matched genres
#                   (1.0 if none have an opinion)
#
# Range: 0.0–1.0. 0.0 disables the feature entirely.
# Default: 0.6
# ---------------------------------------------------------------

primary_genre_weight = 0.6

# ---------------------------------------------------------------
# max_history
#
# How many previous scores to keep per media entry (requires a database —
# see "Database Mode" above; ignored in DBless mode beyond being stored).
#   0  = keep only the latest (previous score is soft-deleted on each rescore)
#   N  = keep the N most recent (e.g. max_history = 5)
#  -1  = keep all scores forever
# Default: 0
# ---------------------------------------------------------------

max_history = 0

# ---------------------------------------------------------------
# [dimensions]
#
# Defines the scoring dimensions for every session.
# Each key is the internal identifier used in genre blocks and
# CLI output. Keys must be lowercase, snake_case, no spaces.
#
# Fields per dimension:
#   label          — Display name shown in CLI prompts and output
#   description    — Shown as a hint during the scoring session
#   weight         — Base weight (all weights must sum to 1.0)
#   bias_resistant — If true, genre multipliers never affect this
#                    dimension. Use for subjective dimensions where
#                    genre context should not shift the score weight.
#
# You may add, remove, or rename dimensions freely.
# The application has no hardcoded dimension list.
# ---------------------------------------------------------------

[dimensions.story]
label          = "Story"
description    = "Plot, hook, themes, and how well the narrative concludes"
weight         = 0.25
bias_resistant = false

[dimensions.enjoyment]
label          = "Enjoyment"
description    = "Gut feeling. How much fun did you have? Did you look forward to the next entry?"
weight         = 0.20
bias_resistant = true  # personal experience — genre should not shift this

[dimensions.characters]
label          = "Characters"
description    = "Relatability, growth arcs, and chemistry between the cast"
weight         = 0.15
bias_resistant = false

[dimensions.production]
label          = "Production"
description    = "Anime: animation fluidity, voice acting, OST. Manga: art style, character design, paneling"
weight         = 0.15
bias_resistant = false

[dimensions.pacing]
label          = "Pacing"
description    = "How well the story flows. Is it dragging? Rushed? Does it keep you hooked?"
weight         = 0.10
bias_resistant = false

[dimensions.world_building]
label          = "World Building"
description    = "The setting, rules/systems, and how immersive the lore feels"
weight         = 0.10
bias_resistant = false

[dimensions.value]
label          = "Value"
description    = "Rewatch/reread value. Does it have staying power in your mind?"
weight         = 0.05
bias_resistant = true  # personal experience — genre should not shift this


# ---------------------------------------------------------------
# [genres]
#
# Defines genre bias multipliers. When a media entry is fetched
# from AniList, its genres are matched against these blocks.
#
# Multiplier semantics:
#   1.0  — no change (default for any dimension not listed)
#   >1.0 — boost: this dimension carries more weight for this genre
#   <1.0 — reduce: this dimension carries less weight for this genre
#
# Multi-genre behaviour:
#   When a show has multiple genres, multipliers are AVERAGED per
#   dimension across all matched genres. They are never multiplied
#   together. This prevents extreme distortion on 4–6 genre entries.
#
# Bias-resistant dimensions:
#   Multipliers defined here for bias_resistant dimensions are
#   silently ignored by the engine. You do not need to omit them —
#   broad genre blocks are fine.
#
# Unknown genres:
#   Genres returned by AniList that are not defined here are ignored.
#   They do not affect the score in any way.
#
# You may add genre blocks for any AniList genre string. The key
# must match the AniList genre name exactly (case-insensitive match
# is applied by the engine).
# ---------------------------------------------------------------

[genres.action]
# Execution matters more than narrative for action shows.
production    = 1.4  # animation quality is central
pacing        = 1.3  # momentum is critical
story         = 0.8  # plot is secondary to spectacle
world_building = 0.9

[genres.adventure]
world_building = 1.3  # the world is part of the experience
pacing         = 1.1
story          = 1.1

[genres.comedy]
enjoyment      = 1.3  # ignored (bias_resistant) — listed for clarity
characters     = 1.2  # comedic chemistry drives the genre
pacing         = 1.1
story          = 0.8
world_building = 0.7

[genres.drama]
story          = 1.4  # narrative weight is the point
characters     = 1.3  # emotional investment in cast
production     = 0.8  # visuals are secondary
pacing         = 1.1

[genres.mystery]
story          = 1.5  # the plot IS the experience
pacing         = 1.3  # tension depends on pacing
world_building = 1.2  # the rules of the mystery world matter

[genres.romance]
characters     = 1.4  # the relationship is the core
enjoyment      = 1.2  # ignored (bias_resistant) — listed for clarity
story          = 1.1

[genres.slice_of_life]
characters     = 1.4  # cast chemistry carries the genre
enjoyment      = 1.3  # ignored (bias_resistant) — listed for clarity
world_building = 0.7  # setting matters less
story          = 0.8  # plot structure is not the point
pacing         = 0.9

[genres.thriller]
story          = 1.4
pacing         = 1.4  # tension is everything
characters     = 1.1
production     = 0.9

[genres.fantasy]
world_building = 1.5  # the world is a primary draw
story          = 1.1
production     = 1.1

[genres.sci-fi]
world_building = 1.4  # internal consistency of the setting
story          = 1.2
production     = 1.1

[genres.horror]
production     = 1.3  # atmosphere is critical
pacing         = 1.2  # dread depends on pacing
story          = 1.1
enjoyment      = 0.9  # ignored (bias_resistant) — listed for intent

[genres.psychological]
story          = 1.4
characters     = 1.3  # character interiority is central
pacing         = 1.1
world_building = 1.1
production     = 0.8

[genres.sports]
pacing         = 1.3
characters     = 1.2  # rivalry and team dynamics
production     = 1.2  # animation of movement matters
story          = 0.9
world_building = 0.7

[genres.supernatural]
world_building = 1.3
story          = 1.1
production     = 1.1

[genres.mecha]
production     = 1.4  # mechanical animation is the centrepiece
world_building = 1.2
story          = 1.0
pacing         = 1.1


# ---------------------------------------------------------------
# [server] — DEPRECATED (ADR-030)
#
# Values in this section are ignored at runtime. If present, kansou prints a
# deprecation warning to stderr and uses KANSOU_PORT / KANSOU_CORS_ORIGINS
# environment variables instead (see "Environment Variables" above). This
# section will be removed entirely in the next major version. Delete it from
# your config file — it has no effect.
# ---------------------------------------------------------------

# [server]
# port = 8080
# cors_allowed_origins = [
#   "http://localhost:3000",
#   "http://localhost:5173",
#   "http://localhost:8080",
# ]
```

---

## Adding a New Dimension

1. Add a `[dimensions.your_key]` block with `label`, `description`, `weight`,
   and `bias_resistant`.
2. Rebalance the weights of existing dimensions so the total remains 1.0.
3. Optionally add multipliers for your new key to any relevant `[genres.*]` blocks.
4. Restart `kansou`. The validator will confirm your weights sum correctly.

Example — adding a `soundtrack` dimension by splitting the `production` weight:

```toml
[dimensions.production]
label          = "Production"
description    = "Animation fluidity, art style, character design, paneling"
weight         = 0.10  # reduced from 0.15
bias_resistant = false

[dimensions.soundtrack]
label          = "Soundtrack"
description    = "OST quality, voice acting, use of silence and sound design"
weight         = 0.05  # new — takes the 0.05 removed from production
bias_resistant = false
```

Note: manga scoring sessions will display both dimensions. You are responsible
for scoring `soundtrack` appropriately for manga (likely low, unless the manga
has an official OST release). If you want `soundtrack` to be anime-only in
practice, set its weight very low (0.01–0.02) rather than removing it — the
formula handles low-weight dimensions gracefully.

---

## Removing a Dimension

Remove the `[dimensions.your_key]` block and redistribute its weight to
remaining dimensions. Remove any references to the key in `[genres.*]` blocks
(or leave them — the validator only errors on genre blocks referencing keys
that exist in dimensions with a mismatch, unknown keys in genre blocks for
removed dimensions are ignored after the dimension is removed).

---

## Per-Session Overrides (not in config)

The `--weight` flag on `score add` overrides dimension weights for a single
session without modifying config. See `docs/CLI.md` for usage.

---

## Runtime Config Editing (`--live-config`)

The `--live-config` flag on `kansou serve` enables two additional endpoints:

- `GET /api/config` — returns the current mutable config surface as JSON
- `POST /api/config` — replaces the mutable config surface, reloads the scoring
  engine atomically, and writes the updated config to disk

Both endpoints are absent when the flag is not set.

### Editable fields

| Field | Description |
|-------|-------------|
| `dimensions` (add/remove/edit) | `label`, `description`, `weight`, `bias_resistant` per dimension |
| `genres` (add/remove/edit) | Per-dimension multiplier maps |
| `primary_genre_weight` | Blend ratio for the primary genre feature (ADR-022) |
| `max_multiplier` | Ceiling for all genre multiplier values |

Fields not listed here (`server.port`, `server.cors_allowed_origins`) are not
exposed by these endpoints and cannot be changed at runtime.

### Validation

`POST /api/config` runs the same strict validation as the config loader:
dimension weights must sum to 1.0 (±0.001), genre blocks may only reference
dimension keys present in the submitted dimensions map, and all multiplier
values must be > 0.0 and ≤ `max_multiplier`. No auto-normalization. On any
validation failure the request is rejected with HTTP 400 and the in-memory
config is unchanged.

### Persistence: database vs. disk (ADR-029)

After a successful `POST /api/config`:

- **Database mode** (`KANSOU_DB_TYPE` set): the update is persisted via
  `SaveScoringConfig` — the config file on disk is untouched.
- **DBless mode**: the updated config is written atomically to the config
  file on disk (encode to a temp file, then `os.Rename` into place). Comments
  and custom formatting from the original file are not preserved after the
  first write. `config.example.toml` remains the annotated human-readable
  reference.

This branch exists because `POST /api/config` originally always wrote to disk,
even in database mode — meaning a live config change over HTTP silently
failed to persist to the database and was overwritten by `LoadScoringConfig`
on the next restart. Fixed as part of ADR-029.

### Writability requirement (DBless mode only)

`--live-config` requires the config file path to be on a writable filesystem.
At startup, the server probes writability by creating and deleting a temporary
file in the same directory as the config file. If the probe fails, the server
exits immediately with a clear error before accepting any requests.

Kubernetes deployment notes:
- **ConfigMap mounts** are read-only and incompatible with `--live-config`.
- **PVC (PersistentVolumeClaim)** mounts are writable and work correctly.
- Docker and bare-metal deployments can use any writable path.

Deployments that do not use `--live-config` are unaffected and can continue
using read-only config file mounts.

### `config_hash`

`GET /api/config` returns a `config_hash` field — a SHA-256 digest of the full
mutable config surface. The UI can compare this value between a GET and a
subsequent POST to detect config drift (e.g. if another client wrote a
`POST /api/config` in the meantime).
