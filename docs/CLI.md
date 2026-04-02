# CLI.md — kansou Command Reference

## Global Flags

These flags are available on every command.

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `~/.config/kansou/config.toml` | Path to config file |
| `--help` | — | Print help for any command |
| `--version` | — | Print kansou version and exit |

---

## Command Tree

```
kansou
├── serve                        # Start the REST server
├── media
│   └── find <query>             # Search for media on AniList
└── score
    └── add <query>              # Start a scoring session (includes publish prompt)
```

---

## kansou serve

Start the REST API server.

```
kansou serve [flags]
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `8080` | Port to listen on (overrides config) |

**Behaviour:**
- Loads config from the default path or `--config`
- Validates config on startup — exits with error if invalid
- Listens for HTTP requests on the configured port
- Serves Swagger UI at `/swagger/index.html`
- Handles `SIGINT` and `SIGTERM` with graceful shutdown
- Prints the listening address to stdout on start:
  ```
  kansou listening on http://localhost:8080
  swagger available at http://localhost:8080/swagger/index.html
  ```

**Example:**
```bash
kansou serve
kansou serve --port 3000
kansou serve --config /etc/kansou/config.toml --port 9000
```

---

## kansou media find

Search AniList for a media entry and display its details. Does not start a
scoring session. Useful for verifying you have the right entry before scoring.

```
kansou media find <query> [flags]
```

**Arguments:**

| Argument | Description |
|----------|-------------|
| `query` | Search string — title or partial title |

**Flags:**

| Flag | Description |
|------|-------------|
| `--url <url>` | Fetch by direct AniList URL instead of searching |
| `--type <type>` | Filter results by media type: `anime` or `manga` |

**Output — single result:**
```
┌─────────────────────────────────────────────────┐
│  Frieren: Beyond Journey's End                  │
│  フリーレン：葬送のフリーレン                         │
├─────────────────────────────────────────────────┤
│  Type      │  Anime (TV)                        │
│  Status    │  Finished                          │
│  Episodes  │  28                                │
│  AniList   │  https://anilist.co/anime/154587   │
├─────────────────────────────────────────────────┤
│  Genres    │  Adventure, Drama, Fantasy         │
├─────────────────────────────────────────────────┤
│  Community │  AniList avg: 88  /  mean: 89      │
└─────────────────────────────────────────────────┘
```

**Output — multiple results (picker):**
```
  1. Frieren: Beyond Journey's End         (TV · 28 eps · FINISHED)
  2. Frieren: Beyond Journey's End Part 2  (TV · 16 eps · FINISHED)
  3. Frieren: Beyond Journey's End — Abschied (SPECIAL · 1 ep · FINISHED)

Pick a result [1–3]:
> 1
```

After picking, the selected entry's full card is displayed.
When `--url` is provided the picker is skipped entirely.

**Examples:**
```bash
kansou media find "Frieren"
kansou media find "Frieren" --type anime
kansou media find "Frieren" --type manga
kansou media find --url https://anilist.co/anime/154587
```

**Error cases:**
- No results found → suggests using `--url`
- AniList unreachable → exits with error
- Invalid `--type` value → exits before network request
- Invalid URL format → exits before network request

---

## kansou score add

Start an interactive scoring session for a media entry. Prompts for a score
(1–10) for each configured dimension in order. Calculates and displays the
final weighted score, then asks whether to publish it to AniList.

```
kansou score add <query> [flags]
kansou score add --url <url> [flags]
```

**Arguments:**

| Argument | Description |
|----------|-------------|
| `query` | Search string — title or partial title |

**Flags:**

| Flag | Description |
|------|-------------|
| `--url <url>` | Fetch by direct AniList URL instead of searching |
| `--type <type>` | Filter results by media type: `anime` or `manga` |
| `--breakdown` | Show weighted contribution table after scoring |
| `--weight <overrides>` | Override dimension weights for this session only |

**--weight flag syntax:**

Comma-separated `key=value` pairs. Values are decimal weights (not percentages).
All values must be > 0.0. The sum of all overridden values must be < 1.0
(the remaining budget is distributed proportionally across non-overridden dimensions).

```bash
--weight pacing=0.05,world_building=0.20
--weight story=0.30
```

Validation errors for `--weight`:
- Unknown dimension key → exits before fetching media
- Value ≤ 0.0 or > 1.0 → exits before fetching media
- Sum of override values ≥ 1.0 → exits before fetching media
- Key also appears in a skip during the session → session is aborted with error:
  ```
  error: dimension "pacing" was both weight-overridden and skipped — these are mutually exclusive
  ```

**Interactive session flow:**

```
$ kansou score add "Mushishi"

  1. Mushishi                    (TV · 26 eps · FINISHED)
  2. Mushishi Zoku Shou          (TV · 10 eps · FINISHED)
  3. Mushishi Zoku Shou 2nd Season (TV · 10 eps · FINISHED)

Pick a result [1–3]:
> 1

Found: Mushishi (Anime · TV · 26 episodes)
Genres: Mystery, Slice of Life, Supernatural

Score each dimension from 1 to 10. Decimals accepted (e.g. 7.5).
Enter 's' or 'skip' to mark a dimension as not applicable.

  Story         — Plot, hook, themes, and how well the narrative concludes
  > 9

  Enjoyment     — Gut feeling. How much fun did you have?
  > 10

  Characters    — Relatability, growth arcs, and chemistry between the cast
  > 8.5

  Production    — Anime: animation fluidity, voice acting, OST
  > 9

  Pacing        — How well the story flows. Is it dragging? Rushed?
  > 8

  World Building — The setting, rules/systems, and how immersive the lore feels
  > 9.5

  Value         — Rewatch/reread value. Does it have staying power?
  > s
  ✓ Value marked as not applicable — excluded from score

──────────────────────────────
  Final Score   9.22 / 10
──────────────────────────────

Publish to AniList? [y/N]: y
✓ Score published to AniList
  Mushishi — 9.22
```

Entering anything other than `y` (including pressing Enter) skips publishing.

**With --breakdown flag:**

```
──────────────────────────────────────────────────────────────────────────────
  Dimension       Score   Base W   Multiplier   Final W   Contribution
──────────────────────────────────────────────────────────────────────────────
  Story             9.0   25.00%     ×1.23      26.10%       2.35
  Enjoyment        10.0   20.00%     ×1.00  *   21.37%       2.14   [bias-resistant]
  Characters        8.5   15.00%     ×1.00      16.03%       1.36
  Production        9.0   15.00%     ×1.00      16.03%       1.44
  Pacing            8.0   10.00%     ×1.30      11.11%       0.89   [genre adjusted]
  World Building    9.5   10.00%     ×1.23      10.53%       1.00   [genre adjusted]
  Value             —     5.00%       —          —            —     [skipped]
──────────────────────────────────────────────────────────────────────────────
  Final Score                                               9.18 / 10
──────────────────────────────────────────────────────────────────────────────
  * bias-resistant — genre multipliers not applied
  Genres returned by AniList : Mystery, Slice of Life, Supernatural
  Genres matched in config   : Mystery (story×1.5, pacing×1.3, world_building×1.2)
                               Slice of Life (characters×1.4, pacing×0.8)
  Genres unmatched           : Supernatural
```

The breakdown table shows:
- Base weight before genre adjustment
- The averaged multiplier applied (`×1.00` for bias-resistant or unmatched)
- Final weight after genre adjustment and renormalization
- Which dimensions are bias-resistant (`*`)
- Which dimensions were affected by genre multipliers
- Which dimensions were skipped
- Full genre match detail below the table — which genres matched, which
  dimensions they affected, and which genres were returned but not in config

**With --weight overrides, the breakdown marks overridden dimensions:**

```
  Pacing            8.0   10.00%     ×1.30       5.00%       0.40   [overridden]
  World Building    9.5   10.00%     ×1.23      20.00%       1.90   [overridden]
```

**Input validation during session:**
- Values outside 1–10 are rejected with a re-prompt:
  ```
  invalid: score must be between 1 and 10
  > 
  ```
- Non-numeric input (other than `s`/`skip`) is rejected with a re-prompt:
  ```
  invalid: enter a number between 1 and 10, or 's' to skip
  > 
  ```
- `s` or `skip` marks the dimension as not applicable:
  ```
  ✓ Value marked as not applicable — excluded from score
  ```
- Skipping a dimension that was given a `--weight` override aborts the session:
  ```
  error: dimension "pacing" was both weight-overridden and skipped — these are mutually exclusive
  ```
- Skipping all dimensions is rejected:
  ```
  error: all dimensions were skipped — at least one dimension must be scored
  ```
- `Ctrl+C` or EOF during a session cancels without publishing:
  ```
  session cancelled — no score was published
  ```

**Examples:**
```bash
kansou score add "Mushishi"
kansou score add "mushishi" --breakdown
kansou score add --url https://anilist.co/anime/457 --breakdown
kansou score add "Mushishi" --weight pacing=0.05,world_building=0.20
kansou score add "Mushishi" --weight pacing=0.05 --breakdown
```

---


## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | General error (config invalid, AniList error, bad input) |
| `2` | Misuse of CLI (unknown command, missing argument) |

---

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `ANILIST_TOKEN` | Yes (to publish) | AniList user token for write operations |

`ANILIST_TOKEN` is required when answering `y` to the publish prompt in `score add`.
It is not required for `media find` or for scoring without publishing (both are
read-only AniList operations that do not require authentication).

---

## Session Model

`kansou` CLI is stateless between invocations. Each `score add` run is a
complete session: search → score → optional publish. There is no cross-invocation
state and no queuing or history.

This is a v1 constraint. See `ADR-002` for context.
