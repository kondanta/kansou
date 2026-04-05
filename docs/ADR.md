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

---

## ADR-017 — Configurable genre multiplier ceiling (max_multiplier)

**Status:** Accepted

**Date:** 2026

**Context:**
Genre bias multipliers in `[genres.*]` config blocks were unconstrained. A typo
such as `20` instead of `2.0` would silently produce a massively distorted score
with no feedback. There was also no way to raise the ceiling for users who
legitimately want stronger genre bias without resorting to per-session `--weight`
overrides every time.

**Decision:**
Introduce a `max_multiplier` top-level config field (default `2.0`). On startup,
the config loader validates that every value in every `[genres.*]` block is
`> 0.0` and `≤ max_multiplier`. Any violation causes an immediate exit with a
descriptive error message. The ceiling is configurable so users can raise it
in their `config.toml` when they want more aggressive genre bias.

**Reasoning:**
`2.0` is a generous but sane default — doubling a dimension's weight is already
a strong signal. Typos (e.g. `20`) are caught immediately at startup rather than
silently corrupting scores. Making the ceiling configurable avoids hard-coding a
value that informed users may legitimately need to exceed.

Zero and negative multipliers are also rejected unconditionally regardless of
`max_multiplier`, because a zero multiplier would silently drop a dimension from
the scoring formula (equivalent to setting its weight to zero), and a negative
multiplier has no meaningful interpretation in the scoring engine.

**Alternatives considered:**
- Hard-code the ceiling at 2.0 — rejected. Too restrictive for users with strong
  genre bias preferences; provides no escape valve without a code change.
- No ceiling at all — rejected. The whole point is to catch typos and accidental
  extreme values at load time rather than at score review time.
- Clamp silently to `max_multiplier` instead of rejecting — rejected. `kansou`
  never silently corrects invalid config. If a value is out of range, it must
  be an error the user is asked to fix.

**Consequences:**
- `rawConfig` and `Config` both gain a `MaxMultiplier float64` field
  (`max_multiplier` in TOML).
- `config.DefaultMaxMultiplier = 2.0` is the package-level constant for the
  default; tests and built-in defaults reference it.
- `validateMultipliers` (outer, iterates genres) and `validateGenreMultipliers`
  (inner, iterates per-genre dimensions) enforce the bounds. Two separate
  functions rather than a nested double-loop keep each function focused on one
  responsibility.
- `docs/CONFIG.md`, `config.example.toml`, and `docs/REQUIREMENTS.md`
  updated to document the new field and its validation rule.

---

## ADR-018 — Remove `score publish` CLI command; fold publish into `score add`

**Status:** Accepted

**Date:** 2026

**Context:**
`score publish` was designed as a separate CLI command that reads the calculated
score from in-memory `App.Session` state and writes it to AniList. This worked
on paper but was fundamentally broken in practice: each CLI invocation is a
separate process. `App.Session` is always nil at the start of a new process,
so `score publish` could never succeed when called as a separate invocation
after `score add`. There is no cross-session persistence in v1 (see ADR-002).

**Decision:**
Remove `score publish` as a standalone CLI command. `score add` now ends with
a `Publish to AniList? [y/N]` prompt. Publishing happens inline within the same
process if the user answers `y`. `SessionState` is removed from `App` entirely.
`POST /score/publish` on the REST API is unaffected — the server is already
stateless and receives all data in the request body.

**Reasoning:**
The two-command CLI flow requires either cross-invocation persistence (explicitly
out of scope in v1) or both commands running in the same process (not how a
shell user invokes a CLI). An inline prompt is the natural UX: the user just
finished scoring, the context is fresh, and a single session handles the complete
flow. The REST API already covers programmatic publishing — a `score publish
--media-id X --score Y` command would just be a worse version of a direct API call.

**Alternatives considered:**
- Keep `score publish` with required `--media-id` and `--score` flags (stateless)
  — rejected. Redundant with `POST /score/publish`. Adds surface area with no
  CLI-native benefit; a curl call is more ergonomic for scripting.
- Write a temp file between `score add` and `score publish` — rejected. Introduces
  persistence, which is explicitly deferred to a future version per ADR-002.
- Keep `score publish` unchanged and document the limitation — rejected. A command
  that always fails is worse than no command.

**Consequences:**
- `scorePublishCmd`, `runScorePublish`, and `SessionState` removed from
  `internal/cli/`.
- `App.Session` field removed. `App` now holds only `Config`, `AniList`, and
  `Engine`.
- `score add` prompts for publish confirmation after displaying the final score.
  The `bufio.Reader` used for dimension scoring is reused for the prompt.
- `ARCHITECTURE.md`, `docs/CLI.md`, `docs/REQUIREMENTS.md`, and `CLAUDE.md`
  updated to reflect the new flow.

---

## ADR-019 — Simplified POST /score body; implicit dimension skipping; GET /dimensions

**Status:** Accepted

**Date:** 2026

**Context:**
The original `POST /score` request body exposed `skipped_dimensions` (a list of
keys to mark N/A) and `weight_overrides` (per-session weight adjustments) alongside
`media_id` and `scores`. This created two problems:

1. `skipped_dimensions` was redundant. If a dimension is absent from `scores`,
   the intent is unambiguous — the client chose not to score it. Requiring a
   separate list to say "these keys I already omitted" adds surface area with
   no benefit and creates a contradiction trap (what if a key appears in both
   `scores` and `skipped_dimensions`?).

2. The frontend had no way to know which dimension keys to use in `scores` without
   hardcoding them, which breaks silently when the server config changes dimensions.

**Decision:**
- Remove `skipped_dimensions` from `scoreRequest`. Any configured dimension
  absent from `scores` is implicitly skipped by the server.
- Keep `weight_overrides` as optional. It has genuine utility for frontends
  exposing per-session weight sliders and is not redundant with anything else.
- Add `GET /dimensions` endpoint that returns the configured dimensions in order
  (key, label, description, weight) plus a `config_hash`. Frontends call this
  on load to render the scoring form dynamically and detect config changes.

**Reasoning:**
Removing `skipped_dimensions` eliminates an ambiguity with no loss of
expressiveness — absence from a map is a natural, idiomatic way to signal
non-participation. The `GET /dimensions` endpoint solves the frontend sync
problem at the right layer: the server owns the dimension config, so the
server should be the source of truth for what keys exist. `config_hash` gives
clients a cheap way to detect staleness without polling the full list.
`weight_overrides` stays because it expresses something `scores` cannot — a
desire to shift the weight distribution for a specific session — and mirrors
the existing `--weight` CLI flag.

**Alternatives considered:**
- Keep `skipped_dimensions` as optional — rejected. Optional redundant fields
  become required in practice once clients start using them, and the contradiction
  trap remains. Implicit skipping is strictly cleaner.
- Expose genre multipliers in `GET /dimensions` — rejected. Genre bias is
  server-side scoring logic; the frontend has no use for it and exposing it
  leaks implementation detail.
- Include `bias_resistant` in `GET /dimensions` — deferred. Informational only;
  can be added when a frontend needs it.

**Consequences:**
- `scoreRequest.SkippedDimensions` removed. `scoreRequest.WeightOverrides`
  retained as optional.
- `handleScore` builds the skipped map by comparing `s.cfg.DimensionOrder`
  against `req.Scores` instead of reading a client-supplied list.
- `GET /dimensions` added to the router. `handleDimensions`, `dimensionItem`,
  and `dimensionsResponse` added to `internal/server/handlers.go`.
- `docs/REQUIREMENTS.md` and `docs/ANILIST_INTEGRATION.md` updated.

---

## ADR-020 — Tag-rank-weighted multipliers as a complement to genre bias

**Status:** Proposed — filed as a pre-decision candidate before v1.0.0 tag.
No code changes. To be revisited before or after the first stable release.

**Date:** 2026

**Context:**
The current genre bias system (ADR-005) treats genre matching as binary: a
genre either matches or it doesn't, and its configured multiplier applies at
full strength. AniList also returns **media tags** on every entry — and unlike
genres, tags carry a **rank** (0–100) representing the community's confidence
that the tag applies to the entry. A tag ranked at 99 (e.g. "Tragedy" on a
tragedy) is near-certain; one ranked at 10 is a loose, contested association.

The current system ignores tags entirely. If tag data were used as a signal for
dimension weighting, a tag's rank is a natural scaling factor for how strongly
its associated multiplier should apply. A show that is deeply a tragedy should
lean harder on Story/Emotional Impact than one that is only peripherally
tragic.

**Proposed decision:**
Introduce a `[tags.*]` config section alongside the existing `[genres.*]`
section. Each tag entry follows the same per-dimension multiplier structure as
genres. When scoring, each matched tag's multiplier is **rank-scaled** before
being included in the average:

```
effective_multiplier(tag, dim) = 1.0 + (configured_multiplier - 1.0) × (rank / 100)
```

At `rank = 100` this is the full configured multiplier. At `rank = 0` it
collapses to a neutral `1.0`. At `rank = 50` it applies half the configured
adjustment.

The combined multiplier for a dimension would then average across all matched
genres **and** all matched tags together:

```
m̄ᵢ = (Σ genre multipliers + Σ rank-scaled tag multipliers) / (|matched genres| + |matched tags|)
```

Bias-resistant dimensions continue to receive `1.0` regardless.

**Why this is interesting:**
The current system knows a show is "Action" — it does not know whether the
action is incidental or wall-to-wall. The tag rank provides exactly that
gradient. A `max_multiplier` ceiling (ADR-017) already exists to bound the
output, so the scaled values cannot escape the configured ceiling even when
multiple tags compound.

**Open questions before accepting:**
1. Should tags and genres pool into a single average, or should they be
   computed as separate averages and then averaged together? Pooling dilutes
   genres as more tags are added (AniList returns many tags per entry).
   Separate averaging and then blending may be more stable.
2. AniList tags include a `isMediaSpoiler` and `isGeneralSpoiler` flag.
   Should spoiler tags be excluded from multiplier calculations by default,
   since their presence leaks plot information the user may not want surfaced?
3. Should there be a `min_tag_rank` threshold (e.g. ignore tags below rank 30)
   to filter noise before rank-scaling is applied?
4. The `[tags.*]` config section would substantially expand `config.toml` for
   users who want full coverage. A sensible default (no tag rules) preserves
   backward compatibility — tag bias is opt-in.

**Alternatives to consider:**
- Tags only, no genres — a cleaner single signal, but genres are simpler to
  reason about in config and are coarser-grained by design.
- Tag rank as a binary threshold (e.g. rank ≥ 50 = match, rank < 50 = ignore)
  — simpler to configure but discards the gradient that makes tags interesting.
- Use tag rank to pick which configured multiplier to apply from a tiered table
  (high/medium/low) rather than continuous scaling — a middle ground worth
  considering if continuous math proves hard to tune in practice.

**Consequences if accepted:**
- `internal/anilist/` must surface `tags` (key + rank) on the `Media` struct.
  The GraphQL queries already fetch `tags` — only the Go struct mapping is missing.
- `Entry` would gain a `Tags []TagEntry` field alongside `Genres []string`.
- `combinedMultiplier` would need to accept both genre matches and rank-scaled
  tag matches and pool them in the averaging step.
- `config.go` and `config.example.toml` gain a `[tags.*]` section with the
  same per-dimension multiplier structure as `[genres.*]`.
- `BreakdownRow.GenreMultipliers` (currently genre-only) may need to become
  `AppliedMultipliers map[string]float64` covering both genres and tags, to
  preserve full provenance in the breakdown output.

---

## ADR-021 — Contributing-only averaging: only genres with a configured entry for a dimension participate

**Status:** Accepted

**Date:** 2026

**Context:**
The original averaging formula (ADR-005) treated matched genres that have no
configured multiplier for a given dimension as contributing a neutral `1.0` to
the average. This is the **dilution problem**: if a show matches two genres and
only one defines a multiplier for `characters`, the other genre silently pulls
the average toward `1.0`, weakening the signal from the genre that does have an
opinion.

Example:
- Drama defines `characters = 1.3`, Action does not.
- Old formula: `(1.3 + 1.0) / 2 = 1.15` — Action dilutes Drama's signal.
- contributing-only averaging:    `1.3 / 1 = 1.3`         — only Drama has an opinion; it counts alone.

**Decision:**
Switch to **contributing-only averaging**: include in the average only the genres that
explicitly define a multiplier for the dimension being calculated. Genres in the
matched set that have no entry for the dimension are excluded from the
denominator entirely. If no matched genre has an opinion on a dimension, the
result is `1.0` (neutral).

**Reasoning:**
The previous neutral-contribution approach was a conservative default that
appeared mathematically safe but consistently undershot the intended genre
effect. A genre with no configured opinion should have zero influence, not a
diluting `1.0`. Option B produces averages that actually reflect the signal from
genres that have been configured to care about the dimension.

**Alternatives considered:**
- Option A (keep original): simple but dilutes signal with every additional
  matched genre that has no opinion on the dimension.
- Multiplicative combination: `Π multipliers` — rejected (ADR-005) because it
  compounds to extreme values for multi-genre media.
- Per-dimension max instead of average: `max(multipliers)` — rejected because
  it makes the outcome dependent on the highest single genre regardless of how
  strongly the other matched genres pull in the opposite direction.

**Consequences:**
- `combinedMultiplier` in `internal/scoring/engine.go` now iterates matched
  genres and accumulates only those with an entry for the target dimension.
  Denominator is the count of contributing genres, not `len(matchedGenres)`.
- Existing tests for partial genre matching (`TestScore_PartialGenreMatch`)
  continue to pass unchanged — they already exercised the unmatched-genre
  exclusion path (genres not in config at all).
- `TestScore_UnmatchedGenreContributes1` renamed to `TestScore_NoMatchedGenres_Multiplier1`
  to accurately describe what it tests (no matched genres at all → multiplier 1.0).
- New test `TestScore_OptionB_DimensionlessGenreExcluded` covers the dilution
  scenario explicitly (Action+Drama for `characters` → 1.3 not 1.15).

---

## ADR-022 — Primary genre blend for multi-genre media

**Status:** Accepted

**Date:** 2026

**Context:**
Some shows have multiple genres of very unequal importance. A "Mystery" show
tagged with "Slice of Life" may use slice-of-life as incidental texture while
being constitutively a mystery. Treating both genres with equal weight in the
contributing-only average applies the slice-of-life multipliers as strongly as the
mystery multipliers, which misrepresents the show's character.

**Decision:**
Add an optional `--primary-genre` flag to `kansou score add` (and a
`primary_genre` field to `POST /score`). When a primary genre is set, the
effective multiplier for each dimension is calculated as a weighted blend:

```
final_multiplier = (primary_mult × blend) + (secondary_avg × (1 − blend))
```

Where:
- `primary_mult` is the primary genre's raw multiplier for the dimension (`1.0`
  if the primary genre has no opinion on it).
- `secondary_avg` is the contributing-only average across all **non-primary** matched
  genres (`1.0` if there are none or none have an opinion).
- `blend` is `primary_genre_weight` from config (default: `0.6`).

The configurable blend ratio `primary_genre_weight` (range `[0.0, 1.0]`) is
set once in `config.toml`. Setting it to `0.0` is equivalent to disabling
primary genre support entirely (falls back to Option B). Default is `0.6`.

Bias-resistant dimensions are unaffected — they always receive `1.0`.

**Reasoning:**
The blend approach gives the user an explicit signal ("this is the defining
genre") without hard-coding that genre's multipliers as gospel. At `0.6` the
primary genre's multipliers carry 60% of the weight; secondary genres share the
remaining 40%. The fallback to contributing-only averaging when no primary genre is specified
preserves backward compatibility.

**Alternatives considered:**
- Primary-only (ignore secondary genres entirely): too aggressive; loses signal
  from genres that do legitimately affect the show.
- Raise the primary genre's weight to 2× before averaging: implicit and hard to
  reason about; does not generalise cleanly to more than two matched genres.
- Make `blend` a per-session override: deferred; `config.toml` setting covers
  the use case without adding flag complexity per session.

**Consequences:**
- `Engine` gains a `primaryGenreWeight float64` field. `NewEngine` accepts it
  as a parameter. `cmd/root.go` passes `cfg.PrimaryGenreWeight`.
- `Entry` gains `PrimaryGenre string`. `BreakdownRow` gains `PrimaryGenre string`
  and `PrimaryGenreMultiplier float64` for provenance.
- `config.go` adds `DefaultPrimaryGenreWeight = 0.6`, `PrimaryGenreWeight *float64`
  in `rawConfig`, and `PrimaryGenreWeight float64` in `Config`.
  Pointer in rawConfig distinguishes "not set" (use default) from "explicitly 0.0".
- `config.example.toml` documents `primary_genre_weight = 0.6`.
- `cmd/score.go` adds `--primary-genre` flag. Validation: the value must appear
  in the media's genre list (case-insensitive); unknown value is an error.
- `POST /score` accepts optional `primary_genre` string field.
- `GET /genres` endpoint added to the REST server, returning the configured genre
  multiplier table so web clients can display available genre options.

---

## ADR-023 — User-selectable active genre set (POST /score `selected_genres` and POST /weights)

**Date:** 2026-04-05
**Status:** Accepted

**Context:**
A multi-genre show (e.g. Mystery + Slice of Life + Action) may have genres whose
multipliers work against each other. A user may want to score a show treating it
primarily as one genre type and exclude others whose influence feels wrong for
that particular work. There was no mechanism to restrict which genres participate
in multiplier calculation beyond omitting them from config globally — which would
affect all shows of that genre.

**Decision:**
`POST /score` and `POST /weights` accept an optional `selected_genres []string`
field. When provided, only the listed genres participate in multiplier calculation.
Genres present on the media but absent from `selected_genres` are excluded from
the active set. When omitted or empty, all matched config genres participate —
preserving the existing CLI behaviour (no breaking change).

A new `POST /weights` endpoint allows clients to preview per-dimension final
weights without providing scores. It is used by the web UI for a live weight
preview as the user adjusts genre selection. The endpoint accepts: `media_id`,
`selected_genres`, `primary_genre`, `skipped_dimensions`, and `weight_overrides`.
It returns a `dimensions` array of `weightDimensionRow` objects with the same
final weights that `POST /score` would use.

**Engine changes:**
- `Entry` gains `UserSelectedGenres []string`. When non-empty, `Score()` uses
  this as the genre source instead of `Entry.Genres`.
- `Engine.Weights()` is extracted as a public method with signature:
  `Weights(genres []string, primaryGenre string, skipped map[DimensionKey]bool, overrides map[DimensionKey]float64) []WeightRow`
  This is the single renormalization path. `Score()` delegates to it.
- `WeightRow` is a new type (per-dimension weight without score). `Score()` builds
  `[]BreakdownRow` from `[]WeightRow` and the per-dimension scores.
- `BreakdownRow.GenreDeselected bool` is added. It is `true` when at least one
  deselected genre (present in `Entry.Genres` and in config, but excluded by
  `UserSelectedGenres`) has a configured multiplier for that specific dimension.
  Computed as a post-pass in `Score()` using `matchedGenreKeys()`.
- `BreakdownRow.GenreMultipliers map[string]float64` is removed (dead code — not
  used by any consumer since the switch to `blendedMultiplier`).
- `SessionMeta` gains `GenresActive []string` — the intersection of the active
  genre source with the config genre map. Equals `MatchedGenres` when no
  `UserSelectedGenres` were specified.
- `POST /score` response: `breakdownRowResponse` adds `genre_deselected bool`;
  `sessionMetaResponse` adds `genres_active []string`.
- `primary_genre` validation: when `selected_genres` is present, `primary_genre`
  must be in `selected_genres`; otherwise it must be in the media's full genre list.

**Alternatives considered:**
- Expose genre weights as editable sliders (per-genre multiplier override per session):
  much more complex; the current model already has `weight_overrides` for
  per-dimension control. Genre selection is a simpler and more natural UI model.
- Keep genre exclusion as a CLI-only feature: rules out web UI usage.

**Consequences:**
- `GET /genres` response is used by the web UI to distinguish config-matched
  genres from unmatched ones, so checkboxes for unmatched genres can be
  visually dimmed (they have no effect on weights).
- Web UI: genre checkboxes replace plain informational display. All start checked.
  Unchecking a genre that is set as primary automatically clears the primary
  selection. A debounced (150ms) POST /weights call updates the live weight
  preview in the dimension rows.
- CLI is not changed — no `selected_genres` flag. The feature is web-UI-only
  for now. `UserSelectedGenres` is nil in all CLI-originated entries.

---

## ADR-024 — Remove BreakdownRow.GenreMultipliers (dead field cleanup)

**Date:** 2026-04-05
**Status:** Accepted

**Context:**
`BreakdownRow.GenreMultipliers map[string]float64` was introduced to provide
per-genre contribution provenance in the breakdown. After the introduction of
`blendedMultiplier` (ADR-022), the per-genre contributions map was returned from
`blendedMultiplier` as a third return value but was no longer surfaced in any
consumer — it was not included in the server JSON response (`handlers.go`
serialised it via `json:"genre_multipliers,omitempty"` but `toScoreResponse` set
it from `row.GenreMultipliers` which was always populated with an empty map
after `effectiveWeights` construction), and was not rendered by `printBreakdown`
in the CLI.

**Decision:**
Remove `BreakdownRow.GenreMultipliers` entirely. The third return value from
`blendedMultiplier` (the contributions map) is now discarded with `_` in
`Engine.Weights()`. The field is no longer part of the engine's public API.
`breakdownRowResponse.GenreMultipliers` is also removed from `handlers.go`.

**Alternatives considered:**
- Keep the field but omit it from the JSON response: retains dead internal state
  with no benefit; makes the struct larger and the tests harder to read.

**Consequences:**
- `BreakdownRow` is smaller and cleaner.
- `breakdownRowResponse` loses the `genre_multipliers` JSON field. This is a
  breaking change to the API response shape, but since the field was always
  empty in practice (populated with an empty map, serialised as absent via
  `omitempty`), no client observed any data in this field.
