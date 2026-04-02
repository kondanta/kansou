# ADR.md — Architecture Decision Records

This document records every significant decision made during the design of `kansou`,
the reasoning behind it, the alternatives that were considered, and the consequences
of the choice. It is append-only. Decisions are never deleted — if a decision is
reversed, a new entry is added that supersedes the old one.

Anyone modifying the scoring engine, config schema, or CLI/API surface must read
the relevant ADRs before making changes. The coding agent must not reverse any
decision recorded here without a corresponding new ADR entry.

---

## ADR-001 — Single binary, two modes (CLI + REST server)

**Status:** Accepted

**Date:** 2025

**Context:**
`kansou` needs to operate as both a terminal CLI tool and a REST API server
to support a future web frontend. The question was whether to build two separate
binaries sharing a common library, or a single binary with two invocation modes.

**Decision:**
Single binary. CLI mode is the default interactive experience. Server mode is
invoked explicitly with `kansou serve`.

**Reasoning:**
Both modes require identical business logic — the scoring engine, the AniList
client, the config loader. A separate binary would either duplicate that logic
or introduce a third shared package both binaries import. At this scale that
indirection adds complexity with no benefit. A single binary also means zero
drift between CLI and API behaviour — they call the same functions.

**Alternatives considered:**
- Two binaries (`kansou` and `kansoud`) — rejected due to duplication and
  maintenance overhead.
- CLI as a thin client that talks to the REST server — rejected as unnecessarily
  complex for a personal tool and introduces a network dependency for local use.

**Consequences:**
- The `cmd/kansou/main.go` entry point branches into CLI or server mode.
- All business logic lives in `internal/` and is shared by both modes.
- Adding new functionality requires implementing it once in core and exposing
  it in both the CLI and server layers.

---

## ADR-002 — No local persistence in v1

**Status:** Accepted

**Date:** 2025

**Context:**
The tool calculates scores and publishes them to AniList. The question was
whether to persist scores locally (SQLite, flat file) for history, undo, or
offline use.

**Decision:**
No persistence in v1. All state is in-memory for the duration of a session.

**Reasoning:**
AniList is the source of truth for the user's scores. Duplicating that data
locally creates a synchronisation problem without clear benefit for v1.
Keeping state in-memory forces a clean, simple session model and eliminates
an entire class of bugs (stale cache, schema migrations, file corruption).

**Alternatives considered:**
- SQLite for local score history — deferred, not rejected. A natural v2 addition
  once the core flow is stable.
- Flat JSON file per session — rejected as a poor middle ground: the complexity
  of persistence without the queryability.

**Consequences:**
- `score publish` must be called explicitly within the same session as `score add`.
  There is no "resume" or "undo" across sessions.
- If AniList is unreachable at publish time, the score is lost and the user must
  re-score. This is an acceptable trade-off for v1.
- Adding persistence in v2 requires no changes to the scoring engine or AniList
  client — only a new storage layer in `internal/` and updated CLI/server handlers.

---

## ADR-003 — AniList token via environment variable only

**Status:** Accepted

**Date:** 2025

**Context:**
AniList write operations require a user token. Options were: environment variable,
config file field, OAuth2 PKCE flow, or a combination.

**Decision:**
Token is read exclusively from the `ANILIST_TOKEN` environment variable.
It is never read from config, never logged, never included in error output.

**Reasoning:**
OAuth2 PKCE is the correct long-term solution but is significant implementation
overhead for v1. Storing the token in config risks accidental exposure (committing
config to a public repo). Environment variables are the Unix-conventional approach
for secrets, are excluded from config by definition, and require zero additional
implementation.

**Alternatives considered:**
- Config file field — rejected due to accidental exposure risk.
- OAuth2 PKCE — deferred to a future version, not rejected.
- Encrypted keychain storage — over-engineered for v1.

**Consequences:**
- Users must set `ANILIST_TOKEN` in their shell environment before using `kansou`.
- The AniList client reads the env var once at startup and fails immediately with
  a clear error if it is unset or empty.
- Migrating to OAuth2 in a future version requires changes only to `internal/anilist/`
  and does not affect the scoring engine or config schema.

---

## ADR-004 — Raw net/http for AniList GraphQL client

**Status:** Accepted

**Date:** 2025

**Context:**
The AniList API uses GraphQL. The question was whether to use a GraphQL client
library (`machinebox/graphql` or similar) or build a thin wrapper around
the standard `net/http` package.

**Decision:**
Raw `net/http` with a small typed wrapper in `internal/anilist/`.

**Reasoning:**
The AniList integration has a fixed, small surface area: two queries (search by
name, fetch by ID) and one mutation (publish score). A GraphQL library would be
an abstraction over three lines of JSON marshalling and an HTTP POST. Owning that
code directly means zero external maintenance risk, the implementation is readable
by any Go developer, and extending it to a new query is a single typed function.
The library buys nothing that justifies the dependency.

**Alternatives considered:**
- `machinebox/graphql` — rejected. Lightly maintained, no meaningful benefit
  over raw HTTP for a fixed-query client.
- `Khan/genqlient` — too complex for the scope. Designed for large, evolving
  GraphQL schemas.

**Consequences:**
- `internal/anilist/` owns a small HTTP client and typed request/response structs.
- GraphQL query strings live as Go string constants in `internal/anilist/queries.go`.
- All AniList operations are typed functions with named return values — no raw
  `map[string]interface{}` anywhere in the call path.

---

## ADR-005 — Genre bias via multipliers, not additive offsets

**Status:** Accepted

**Date:** 2025

**Context:**
The scoring system needed a mechanism to shift dimension weights based on the
genre of the media being scored. Two approaches were considered: additive offsets
(`story = -10%`) and multiplicative factors (`story = 0.8`).

**Decision:**
Multiplicative factors with renormalization. Genre multipliers are averaged
across all matched genres per dimension, then weights are renormalized to sum
to 1.0.

**Reasoning:**
Additive offsets fail under real-world conditions. AniList commonly returns
4–6 genres per entry. With additive offsets, conflicting genre definitions
(Action pushes Story down, Drama pushes it up) produce results that are
artifacts of which genres happen to be tagged rather than meaningful bias.
Accumulated offsets can also push a weight negative before renormalization
catches it. Multiplicative averaging converges naturally toward 1.0 as more
genres are matched — the more genres, the less any single genre dominates.
This is the correct behaviour for mixed-genre entries.

**Alternatives considered:**
- Additive offsets — rejected for multi-genre instability.
- Multiplicative stacking (multiplying multipliers together) — rejected due
  to extreme distortion with 3+ genres.
- Hybrid (additive interface, multiplicative engine) — considered but rejected
  as unnecessary complexity for a personal tool with a TOML config.

**Consequences:**
- Genre config values are multipliers (1.0 = no change, >1.0 = boost, <1.0 = reduce).
- The TOML config uses values like `story = 0.8`, not `story = -10`.
- For dimensions not defined in a genre block, the multiplier defaults to 1.0.
- The renormalization step is mandatory and must always follow multiplier application.

---

## ADR-006 — Dynamic dimensions via config

**Status:** Accepted

**Date:** 2025

**Context:**
The initial design had seven hardcoded scoring dimensions. The question was
whether the dimension list should be fixed in code or driven entirely by config.

**Decision:**
Dimensions are fully dynamic. The scoring engine has no hardcoded dimension
list. All dimensions — their keys, labels, descriptions, weights, and
bias-resistance — are defined in `[dimensions]` in `config.toml`.

**Reasoning:**
A hardcoded list means adding or removing a dimension requires a code change,
recompilation, and a release. Config-driven dimensions mean the tool is useful
to anyone with different scoring philosophies without forking the codebase.
The scoring engine already operates on maps — making dimensions dynamic costs
nothing architecturally and requires no special-casing.

**Alternatives considered:**
- Fixed seven dimensions in code, configurable weights only — rejected as
  unnecessarily rigid given the engine already supports arbitrary maps.
- Fixed dimensions with an "extra dimensions" escape hatch — rejected as
  an awkward middle ground that adds complexity without full flexibility.

**Consequences:**
- `DimensionKey` is `string`, not an enum. The engine never references a
  dimension by name — it iterates over whatever config provides.
- The config validator checks that all dimension keys referenced in genre
  blocks exist in the `[dimensions]` config. Unknown keys are a hard error.
- Built-in defaults define the seven reference dimensions and are used when
  no config file is present.
- The CLI prompt loop is a single generic loop over configured dimensions —
  no hardcoded prompt strings anywhere.

---

## ADR-007 — Bias-resistant dimensions

**Status:** Accepted

**Date:** 2025

**Context:**
Genre multipliers shift dimension weights based on the media's genre. However,
some dimensions — specifically Enjoyment and Value — reflect purely personal,
subjective experience. Applying genre bias to these dimensions would mean the
system mechanically adjusts how much your gut feeling counts based on genre,
which contradicts the intent of those dimensions.

**Decision:**
`DimensionDef` includes a `bias_resistant` boolean field. When `true`, the
engine always applies a multiplier of exactly 1.0 to that dimension, regardless
of what any genre config defines. Enjoyment and Value are `bias_resistant: true`
by default.

**Reasoning:**
A show that bores you should score low on Enjoyment whether it is tagged Action
or Slice of Life. The genre context should not mechanically shift how much that
boredom penalises the final score. Encoding this distinction in the data model
makes the philosophical decision explicit and enforced rather than implicit and
accidental.

**Alternatives considered:**
- Document the convention but not enforce it — rejected. An unenforced convention
  will be violated accidentally by genre config edits.
- No bias-resistant concept, rely on user discipline — rejected for the same reason.

**Consequences:**
- Users can override the default by setting `bias_resistant = false` in their
  config. This is a conscious opt-in, not an accidental change.
- The engine checks `BiasResistant` before calling `combinedMultiplier`.
  Bias-resistant dimensions never enter the multiplier calculation path.
- Genre config blocks may define multipliers for bias-resistant dimensions —
  the engine silently ignores them. This avoids validation errors for users
  who define broad genre blocks.

---

## ADR-008 — Per-session weight overrides via --weight flag

**Status:** Accepted

**Date:** 2025

**Context:**
Genre multipliers are a prior — a belief about how dimensions should be weighted
before watching. Some shows defy their genre in ways a config cannot anticipate.
The question was whether to support per-entry weight adjustments without
modifying the global config.

**Decision:**
`score add` accepts an optional `--weight` flag that overrides the genre-adjusted
weight for specific dimensions for that session only.

```
kansou score add "Mushishi" --weight pacing=0.05,world_building=0.20
```

Overridden dimensions are fixed at the provided value. Remaining dimensions are
rescaled proportionally so all weights still sum to 1.0. Overrides are never
persisted.

**Reasoning:**
The config represents a general scoring philosophy. A per-session override
represents a specific judgment about a specific show. These are different things
and should be expressed differently. A flag is ephemeral by nature — it applies
once and leaves no trace, which is exactly the right scope for a per-entry
judgment. It also costs nothing to implement since the engine already accepts
a weight map.

**Alternatives considered:**
- Modify config before scoring, restore after — rejected as error-prone and
  mutates global state for a per-entry concern.
- A separate "session config" file — over-engineered for the use case.
- No override mechanism — rejected. Real-world genre data from AniList is
  imperfect and users will encounter shows that genuinely need a nudge.

**Consequences:**
- The CLI layer is responsible for validating that override weights are in
  range (0.0–1.0) and that their sum does not exceed 1.0. The engine trusts
  that `WeightOverrides` is valid when it receives it.
- Overriding a bias-resistant dimension via `--weight` is permitted — the user
  is making an explicit per-session decision that supersedes the default.
- The breakdown output must clearly indicate when a dimension's weight was
  manually overridden, so the user can see the effect of their override.

---

## ADR-010 — CLI session state via App struct

**Status:** Accepted

**Date:** 2025

**Context:**
`score add` and `score publish` are separate cobra commands that run in the
same process. `score publish` needs the result and media ID produced by
`score add`. The question was how to share this state between commands without
package-level globals.

**Decision:**
An `App` struct in `internal/cli/` owns all shared dependencies and session
state. Commands are methods on `App`. A `SessionState` struct holds the
`scoring.Result` and AniList media ID together. `main.go` constructs `App`
once and registers commands from it. The server layer does not use `App` —
it is stateless by design.

**Reasoning:**
Package-level variables are untestable and violate the spirit of the no-globals
rule. The `App` struct makes session state instance-scoped, injectable, and
testable — you can construct an `App` with a pre-set `Session` and test
`score publish` in isolation. It also provides a single wiring point for all
CLI dependencies (config, AniList client, scoring engine) without threading
them through function arguments.

`SessionState` is a named type rather than embedding `scoring.Result` directly
because `score publish` needs the AniList media ID alongside the result.
`scoring.Result` is a pure calculation output and must not carry AniList
concerns. `SessionState` is the thin join between the two.

**Alternatives considered:**
- Package-level `var currentSession *scoring.Result` — rejected. Untestable,
  hidden dependency, violates no-globals rule.
- Cobra context (`SetContext`/`Context()`) — rejected. Adds indirection and
  type assertions for something a struct field handles cleanly.

**Consequences:**
- `internal/cli/app.go` defines `App`, `SessionState`, and command constructors.
- `score publish` checks `a.Session == nil` before proceeding and exits with
  a clear error if no session is active.
- The server layer uses `server.NewServeCmd(cfg)` with no reference to `App`.
- Testing `score publish` requires constructing an `App` with a pre-set
  `Session` — no process-level state involved.

---

## ADR-011 — REST search endpoint is GET, not POST

**Status:** Accepted

**Date:** 2025

**Context:**
An inconsistency existed between `ARCHITECTURE.md` (which showed
`POST /media/search`) and `REQUIREMENTS.md` (which showed
`GET /media/search?q={query}`). The correct method needed to be settled.

**Decision:**
`GET /media/search?q={query}`. All read operations use GET with query
parameters. No read operation uses POST.

**Reasoning:**
Search is a read operation with no side effects. GET is the correct HTTP
method by definition. Query parameters are the idiomatic mechanism for
search terms. Using POST for reads breaks REST semantics, prevents HTTP
caching, and is misleading to any consumer of the API.

**Consequences:**
- `ARCHITECTURE.md` updated to show `GET /media/search?q={query}` consistently.
- All Swagger annotations for the search handler must use `@Router ... [get]`.
- The chi route registration uses `r.Get("/media/search", ...)`.

---

## ADR-012 — Full score provenance in every Result

**Status:** Accepted

**Date:** 2025

**Context:**
The initial `Result` struct returned a `FinalScore` and an optional breakdown.
The breakdown was only populated when `--breakdown` was requested. This meant
that for a default session, the score was an opaque float with no record of
what produced it — which genres fired, what weights were used, whether overrides
were applied, or which config produced the result.

**Decision:**
The breakdown is always computed and always included in `Result`, regardless
of whether `--breakdown` is requested. The caller (CLI or server layer) decides
whether to display it. `Result` also carries a `SessionMeta` struct with
media identity, genre match details, and a SHA256 hash of the dimensions config.

**Reasoning:**
Provenance costs nothing at calculation time — all the data is already present
during the session. Discarding it and then needing it later is the failure mode.
Always computing it means the breakdown is available for logging, debugging,
future persistence, and API responses without any change to the engine.
The config hash makes it possible to detect when a stored score was produced
with a different dimension configuration than the current one.

**Alternatives considered:**
- Compute breakdown only on demand — rejected. The data is free to produce
  and discarding it creates an information gap with no benefit.
- Store provenance separately from Result — rejected as unnecessary indirection.

**Consequences:**
- `Result.Breakdown` is always populated. CLI rendering of `--breakdown` is
  a display decision, not a calculation decision.
- `SessionMeta` must be constructed by the CLI/server layer before calling
  `Engine.Score()` and passed in — the engine does not fetch media data itself.
- The config hash allows future tooling to warn when a score was produced with
  a different config than the one currently loaded.
- `BreakdownRow` carries `BaseWeight`, `AppliedMultiplier`, `GenreMultipliers`,
  `BiasResistant`, `WeightOverride`, and `Skipped` fields. This is the full
  audit trail for a single dimension's contribution.

---

## ADR-013 — Skippable dimensions (N/A)

**Status:** Accepted

**Date:** 2025

**Context:**
The v1 requirement was that all configured dimensions must be scored. This
works for a personal tool where the user controls the dimension list. For a
more general use case, some dimensions will genuinely not apply to a specific
entry — a silent film has no voice acting, a one-shot has no pacing arc worth
judging. Forcing a score in these cases introduces noise.

**Decision:**
During a scoring session, the user may enter `s` or `skip` at any dimension
prompt to mark it as not applicable. Skipped dimensions are excluded from the
weight pool before renormalization. The remaining weights fill to 1.0 via the
existing renormalization step. Skipped dimensions are recorded in the breakdown
with `Skipped: true` and contribute 0 to the final score.

**Reasoning:**
The renormalization step already handles arbitrary weight pools — excluding
skipped dimensions requires no formula change, only filtering before the
weight calculation. The feature is therefore almost free to implement. Skipping
is explicit and user-driven only — no automatic skipping based on media type
or format. A session where all dimensions are skipped is rejected as invalid.

**Alternatives considered:**
- Score substitution (use 5.0 or personal average for N/A dimensions) — deferred.
  Requires per-user scoring history to compute a meaningful personal average,
  which touches persistence. Can be added in a future version as Case B skipping.
- Automatic skipping based on media type — rejected. Implicit behaviour that
  changes scores without user awareness is worse than requiring explicit input.
- Allow 0 as a valid score for N/A — rejected. 0 is outside the 1–10 scale
  and would be treated as a real score by the formula, producing distorted results.

**Consequences:**
- `Entry.SkippedDimensions` is a `map[DimensionKey]bool`. The engine checks
  this before including a dimension in the weight pool.
- The CLI prompt loop must accept `s`/`skip` as valid input alongside 1–10.
- Skipped dimensions appear in the breakdown with `[skipped]` annotation,
  showing their base weight and the fact that they were excluded.
- The `--weight` flag may not be used to override a skipped dimension's weight.
  The CLI validator should reject this combination before the session starts.

---

## ADR-014 — Swagger generation via swaggo/swag

**Status:** Accepted

**Date:** 2025

**Context:**
The REST server needs API documentation. The question was whether to write
Swagger/OpenAPI specs by hand or generate them from code annotations.

**Decision:**
Generate Swagger automatically using `swaggo/swag`. Annotations live in
handler functions in `internal/server/`. Swagger UI is served at
`/swagger/index.html` in server mode.

**Reasoning:**
Hand-written specs drift from the implementation. Generated specs are
always in sync as long as annotations are maintained. `swaggo/swag` is the
most mature annotation-based generator for Go and integrates cleanly with
`chi` via `swaggo/http-swagger`.

**Alternatives considered:**
- Hand-written OpenAPI YAML — rejected due to drift risk.
- `getkin/kin-openapi` with manual schema construction — more flexible but
  significantly more verbose for a small API surface.

**Consequences:**
- Every handler in `internal/server/` must have complete `swaggo` annotations.
  Unannotated handlers are a documentation gap and must not be merged.
- `swag init` must be run after any handler signature change to regenerate docs.
  This step belongs in the project Justfile (`just swagger`).
- The generated `docs/` output from swag is committed to the repository.

---

## ADR-015 — Structured logging via log/slog

**Status:** Accepted

**Date:** 2026

**Context:**
The application needed structured logging for both CLI and server modes.
The question was whether to use a third-party library (zerolog, zap) or the
standard library's `log/slog` package introduced in Go 1.21.

**Decision:**
Use `log/slog` exclusively. `Setup(isServer bool)` in `internal/logger/`
sets the global default logger once at startup. All packages call
`slog.Info/Debug/Warn/Error` directly — no logger is threaded through
function parameters. Log level is controlled by the `LOG_LEVEL` environment
variable (`debug`, `info`, `warn`, `error`; default `info`).

CLI mode uses a custom coloured text handler: no timestamp at INFO/WARN/ERROR
(reduces noise in interactive sessions), timestamp only at DEBUG. Colour
degrades automatically when stderr is not a TTY or `NO_COLOR` is set.
Server mode uses the stdlib JSON handler with timestamps always present.

**Reasoning:**
`log/slog` covers every requirement: structured key-value output, multiple
handlers, level filtering, and a zero-allocation fast path. Third-party
libraries would add a dependency for no meaningful gain at this scale.
Using `slog.SetDefault` keeps call sites clean — `slog.Info("msg", "k", v)`
anywhere in the codebase without importing or passing a logger instance.

**Alternatives considered:**
- `zerolog` — fastest allocation profile, but a new dependency with no
  feature benefit over slog for this use case.
- `zap` — mature and fast, but verbose API and another dependency.
- `log` (old stdlib) — no structured output, rejected.
- Thread logger through all function signatures — rejected as noisy and
  unnecessary when slog's global default achieves the same result cleanly.

**Consequences:**
- `internal/logger/` is a new package not in the original v1 structure.
  CLAUDE.md and ARCHITECTURE.md updated accordingly.
- `logger.Setup(false)` is called first in `main()` for CLI mode.
  `logger.Setup(true)` is called inside the `serve` subcommand before
  starting the server, overriding the CLI handler with the JSON one.
- Business logic packages (`scoring`, `config`, `anilist`) may call
  `slog.Debug/Info` directly. They must never call `log.Fatal` — that
  remains restricted to `main.go` per the existing convention.
- The custom CLI handler implements `slog.Handler` fully, including
  `WithAttrs` and `WithGroup`, so it is compatible with all slog usage patterns.

---

## ADR-016 — Multi-result media search via Page query

**Status:** Accepted

**Date:** 2026

**Context:**
The original search implementation used AniList's `Media` query, which returns
a single best match. This caused problems when a series has multiple seasons or
related entries — searching "Frieren" would silently return the manga instead of
the anime, with no way for the user to see or choose among the alternatives.
The only workaround was `--url`, which requires the user to look up the AniList
ID manually.

**Decision:**
Replace the `Media` query with a `Page` query returning up to 5 results sorted
by `SEARCH_MATCH`. `SearchByNameMulti` in `internal/anilist/` returns `[]Media`.
The CLI presents a numbered picker when more than one result is returned; a single
result is selected automatically without prompting. `GET /media/search` now returns
`[]mediaResponse` instead of a single object.

**Reasoning:**
The `Page` query is a drop-in replacement with no additional API surface and the
same field set. Five results covers virtually all real-world disambiguation cases
(sequels, cours, specials) without overwhelming the picker. Returning an array
from `GET /media/search` is correct REST semantics for a search endpoint and is a
breaking change we can absorb now, before any frontend depends on the old shape.
The single-result auto-select path preserves the zero-friction experience for
unambiguous queries.

**Alternatives considered:**
- Keep `Media` query, add `--type` flag only — rejected. `--type` reduces the
  problem but does not solve it; a series can still have multiple anime entries
  (cours, specials) that `--type anime` does not disambiguate.
- Interactive confirmation after single result — rejected. Adds a prompt for the
  common case (unambiguous query) with no benefit.
- Relations API to navigate sequels after picking — deferred. Useful for series
  browsing but out of scope for the scoring flow; `--url` covers the edge case.

**Consequences:**
- `SearchByName` removed; `SearchByNameMulti` is the only search entry point.
- `GET /media/search` response shape changed from `{object}` to `{array}`.
  Swagger annotations and generated docs updated accordingly.
- The CLI picker is shared between `media find` and `score add` via `pickMedia`
  in `internal/cli/media.go`.
- `--url` bypasses search entirely and is unaffected by this change.
- `docs/ANILIST_INTEGRATION.md`, `docs/CLI.md`, and `docs/REQUIREMENTS.md`
  updated to reflect the new behaviour.
