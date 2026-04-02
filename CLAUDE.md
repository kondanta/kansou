# CLAUDE.md — Agent Instructions for kansou

This file governs how you (the coding agent) think, decide, and act when working on this codebase.
Read it fully before touching any file. When in doubt, re-read it before acting.

---

## What is kansou?

`kansou` is a personal anime/manga scoring CLI and REST server written in Go.
It fetches media metadata from AniList (via GraphQL), prompts the user for per-dimension scores,
applies a weighted + genre-adjusted scoring formula, and writes the final score back to AniList.

It is a single binary with two modes: CLI and REST server (`--serve`).
There is no local persistence in v1. All state is in-memory for the duration of a session.

---

## Project Structure

You must follow this layout exactly. Do not create packages outside of it without asking first.

```
kansou/
├── cmd/
│   └── kansou/
│       └── main.go          # Entrypoint only. No logic here.
├── internal/
│   ├── anilist/             # AniList GraphQL client
│   ├── config/              # Config loader, validator, defaults
│   ├── scoring/             # Scoring engine: weights, multipliers, formula
│   ├── cli/                 # CLI command definitions (cobra)
│   ├── logger/              # Structured logging setup (log/slog wrappers)
│   └── server/              # REST server (chi router + handlers)
├── docs/
│   ├── REQUIREMENTS.md
│   ├── ADR.md
│   ├── CONFIG.md
│   ├── ANILIST_INTEGRATION.md
│   └── CLI.md
├── config.example.toml      # Fully annotated example config
├── ARCHITECTURE.md
├── CLAUDE.md                # This file
└── go.mod                   # module github.com/kondanta/kansou
```

`internal/` is intentional. Nothing inside it is importable by external packages. Keep it that way.

---

## Language and Runtime

- Go 1.22 or later
- Use the standard library wherever it is sufficient
- Allowed third-party dependencies (do not add others without asking):
  - `github.com/spf13/cobra` — CLI framework
  - `github.com/go-chi/chi/v5` — HTTP router
  - `github.com/BurntSushi/toml` — Config parsing
  - `github.com/swaggo/swag` + `github.com/swaggo/http-swagger` — Swagger generation
  - Raw `net/http` for AniList GraphQL — no GraphQL client library (see ADR-004)

If you think a new dependency is justified, stop and ask. Do not `go get` anything not on this list.

---

## CLI Application Structure

The CLI layer uses an `App` struct defined in `internal/cli/` to own all shared
dependencies and session state. This is the only sanctioned pattern for sharing
state between cobra commands. Do not use package-level variables for this purpose.

```go
// internal/cli/app.go

// SessionState holds the result of a completed score add session.
// It is nil until score add runs successfully.
// It contains both the scoring result and the AniList media ID,
// kept together because score publish needs both.
type SessionState struct {
    MediaID int
    Result  scoring.Result
}

// App owns all shared CLI dependencies and session state.
// It is constructed once in main.go and never modified after construction,
// except for Session which is set by score add and read by score publish.
type App struct {
    Config  *config.Config
    AniList *anilist.Client
    Engine  *scoring.Engine
    Session *SessionState // nil until score add runs
}

// NewApp constructs an App with all dependencies wired.
func NewApp(cfg *config.Config, al *anilist.Client, eng *scoring.Engine) *App

// MediaCmd returns the `media` cobra command and its subcommands.
func (a *App) MediaCmd() *cobra.Command

// ScoreCmd returns the `score` cobra command and its subcommands.
func (a *App) ScoreCmd() *cobra.Command
```

`main.go` owns the root command and wires everything:

```go
// cmd/kansou/main.go
app := cli.NewApp(cfg, anilistClient, scoringEngine)
rootCmd.AddCommand(app.MediaCmd())
rootCmd.AddCommand(app.ScoreCmd())
rootCmd.AddCommand(server.NewServeCmd(cfg))  // server has no session state
```

`server.NewServeCmd` does not use `App` — the server is stateless by design.
Each HTTP request receives all necessary data in the request body.

**Session nil check:** `score publish` must check `a.Session == nil` before
proceeding and exit with a clear error if no session is active:
```
error: no score to publish — run `kansou score add` first
```

---

## General
- All exported types, functions, and methods must have a GoDoc comment. No exceptions.
- No `interface{}` or `any` unless there is no alternative. If you use it, leave a comment explaining why.
- Prefer explicit over clever. If a piece of code needs a comment to be understood, write the comment.
- No global variables except for the Cobra root command in `cmd/kansou/main.go`.

### Error Handling
- Never swallow errors silently. Every error must be either returned or handled explicitly.
- Wrap errors with context using `fmt.Errorf("operation: %w", err)`. The message should describe what was being attempted, not what went wrong — the wrapped error already says what went wrong.
- Fatal errors (config not found, AniList unreachable) should print a human-readable message to stderr and exit with code 1. Do not panic.
- Do not use `log.Fatal` outside of `main.go`.

### Functions
- Functions should do one thing. If you find yourself writing a function that needs more than one level of abstraction, split it.
- Maximum function length is roughly 40 lines. If you are approaching that, stop and refactor.
- No naked returns.

### Naming
- Acronyms in names follow Go convention: `AniList`, not `Anilist`; `ID`, not `Id`; `URL`, not `Url`.
- Config struct fields must exactly match TOML keys (snake_case in TOML, mapped via struct tags).

### Testing
- Every function in `internal/scoring/` must have a unit test. The scoring engine is the core of this tool and must be verifiable in isolation.
- Use table-driven tests. No single-case test functions.
- Test files live alongside the code they test (`scoring_test.go` next to `scoring.go`).
- You do not need to test CLI rendering or HTTP handlers in v1, but do not write code that makes them untestable.

---

## The Scoring Engine — Critical Rules

This is the most important logic in the codebase. Treat it with extra care.

- Base weights are loaded from config and must sum to 1.0 (±0.001 tolerance for float rounding). Validate on load.
- Genre multipliers are averaged across all matched genres per dimension (not multiplied, not summed — averaged).
- Dimensions with `BiasResistant: true` always receive a multiplier of exactly 1.0. Never apply genre multipliers to them regardless of config.
- After applying multipliers, weights are renormalized so they sum to 1.0 before the final score is calculated.
- Per-session `WeightOverrides` are applied after renormalization. Overridden dimensions are fixed; remaining dimensions are rescaled proportionally so the total remains 1.0.
- Media-type awareness: the Production dimension uses different internal criteria for Anime vs Manga, but the weight and position in the formula do not change. The engine does not need to know about this distinction — it is a scoring concern for the user, not a calculation concern for the engine.
- The final score is: `Σ (section_score × final_weight)` where `final_weight` is the renormalized genre-adjusted weight.

Never change this formula without a corresponding update to `docs/ADR.md`.

---

## AniList Integration — Critical Rules

- The AniList token is read exclusively from the `ANILIST_TOKEN` environment variable. Never read it from the config file. Never log it. Never include it in error messages.
- If AniList returns a non-200 response or the network is unreachable, surface a clean error message to the user and stop. No retries, no fallback, no cache in v1.
- GraphQL queries live in `internal/anilist/` as typed Go functions. No raw query strings floating in handlers or CLI commands.
- The AniList media ID is the canonical identifier for an entry throughout the entire session.

---

## REST Server Rules

- All routes must be documented with `swaggo/swag` annotations.
- All handlers must return JSON. No plain text responses except for `/health`.
- HTTP errors must use a consistent envelope:
  ```json
  { "error": "human readable message" }
  ```
- The server is started with `kansou serve`. The port defaults to 8080, is configurable in `config.toml`, and is overridable by `--port`. Flag takes precedence over config.

---

## CLI Rules

- Use `cobra` for all commands. No manual `os.Args` parsing.
- Every command must have a `Short` and `Long` description.
- The `--breakdown` flag on `score add` prints a weighted contribution table. Without it, only the final score is printed.
- Never print to stdout from business logic (`internal/`). Only CLI and server layers print output. Business logic returns values and errors.

---

## Documentation Sync Rules

Every code change that affects documented behaviour must include a corresponding
documentation update in the same task. A task is not complete if the code and
docs are out of sync. This is non-negotiable.

The table below maps what changed to which documents must be updated:

| What changed | Documents to update |
|---|---|
| Scoring formula, weights, multiplier logic, bias-resistant behaviour, skip behaviour | `docs/ADR.md` (new or amended entry) + `docs/REQUIREMENTS.md` (affected FR) |
| `DimensionDef`, `Entry`, `Result`, `BreakdownRow`, `SessionMeta` fields | `docs/REQUIREMENTS.md` + `internal/scoring/` GoDoc comments |
| Config schema (new field, renamed key, changed default) | `docs/CONFIG.md` + `config.example.toml` |
| New or changed CLI command, flag, or output format | `docs/CLI.md` |
| New or changed REST endpoint, request/response shape, error message | `docs/ANILIST_INTEGRATION.md` (if AniList-facing) and/or Swagger annotations |
| New dependency added (after approval) | `CLAUDE.md` approved dependency list |
| New package or change to package responsibilities | `ARCHITECTURE.md` + `CLAUDE.md` project structure |
| Any decision reversal or new architectural decision | `docs/ADR.md` (new entry — never edit or delete existing entries) |

**Rules:**
- Never amend an existing ADR entry. If a decision is reversed or superseded,
  add a new entry that references the old one by number.
- `config.example.toml` must always be a faithful, working representation of
  the current config schema. If the schema changes, the example changes.
- If a change affects the scoring formula section of `CLAUDE.md` itself
  (the Critical Rules block), update that block too.
- When in doubt about whether a doc needs updating, update it. Stale
  documentation is worse than slightly over-documented code.

---

## What You Must Never Do Without Asking

- Add a new top-level package outside the defined structure
- Add a dependency not on the approved list
- Change the scoring formula
- Add any form of local persistence (file, DB, cache)
- Implement OAuth2 or any auth mechanism beyond env var token reading
- Change the config file format or location
- Rename the binary or any top-level command

If a task seems to require any of the above, stop, explain what you've found, and ask how to proceed.

---

## Definition of Done (per task)

A task is complete when:
1. The code compiles with `go build ./...`
2. All tests pass with `go test ./...`
3. `go vet ./...` produces no output
4. New exported symbols have GoDoc comments
5. No new dependencies were added without approval
6. All documentation affected by this task has been updated per the
   Documentation Sync Rules above

Do not mark a task complete if any of these are not true.
