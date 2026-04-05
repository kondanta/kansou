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
| `--primary-genre <genre>` | Designate one genre as primary for blended multiplier calculation |
| `--notes` | Append scoring breakdown to AniList list entry notes when publishing |

**--weight flag syntax:**

Comma-separated `key=value` pairs. Values are decimal weights (not percentages).
All values must be > 0.0. The sum of all overridden values must be < 1.0
(the remaining budget is distributed proportionally across non-overridden dimensions).

```bash
--weight pacing=0.05,world_building=0.20
--weight story=0.30
```

**--primary-genre flag and inline prompt:**

After displaying the media header and genre list, `score add` interactively
prompts for a primary genre before starting the dimension scoring loop:

```
Designate a primary genre? (enter genre name or press Enter to skip):
  > Mystery

Primary genre set: Mystery (blend 60/40)
```

- Input is matched case-insensitively against the media's genre list.
- If the input does not match, the prompt re-displays with the valid genre list:
  ```
  "xyz" is not a genre of this show. Choose from: Mystery, Slice of Life, Supernatural (or press Enter to skip):
    >
  ```
- Empty input (Enter) skips designation — contributing-only averaging applies with no primary.
- Ctrl+D / EOF during the prompt cancels the session with no score published.
- A genre accepted here but absent from config still works — it records in provenance
  but uses a neutral `1.0` primary multiplier since no config entry exists.

Passing `--primary-genre` **bypasses the inline prompt entirely** — the flag value is
validated and applied without interaction. Useful for scripted or non-interactive use.

```bash
--primary-genre Mystery
--primary-genre "Slice of Life"
```

The blend ratio is configured globally via `primary_genre_weight` in config.toml
(default `0.6`). When this flag is set, the effective multiplier for each
non-bias-resistant dimension becomes:

```
final = (primary_mult × blend) + (secondary_avg × (1 − blend))
```

Where `secondary_avg` uses contributing-only averaging across the remaining matched genres.
Setting `primary_genre_weight = 0.0` in config disables the feature globally.

The `--breakdown` table marks primary-blended dimensions with `[primary blended]`.

Validation errors for `--primary-genre`:
- Value not in the media's genre list → exits with error listing available genres

****Validation errors for `--weight`:****
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

**With --breakdown flag (no primary genre):**

```
──────────────────────────────────────────────────────────────────────────────
  Dimension       Score   Base W   Multiplier   Final W   Contribution
──────────────────────────────────────────────────────────────────────────────
  Story             9.0   25.00%     ×1.23      26.10%       2.35
  Enjoyment        10.0   15.00%     ×1.00  *   17.05%       1.71   [bias-resistant]
  Characters        8.5   20.00%     ×1.00      22.73%       1.93
  Production        9.0   15.00%     ×1.00      17.05%       1.53
  Pacing            8.0   10.00%     ×1.30      14.77%       1.18   [genre adjusted]
  World Building    9.5   10.00%     ×1.23      13.98%       1.33   [genre adjusted]
  Value             —     5.00%       —          —            —     [skipped]
──────────────────────────────────────────────────────────────────────────────
  Final Score                                               9.03 / 10
──────────────────────────────────────────────────────────────────────────────
  * bias-resistant — genre multipliers not applied
  Genres returned        : Mystery, Slice of Life, Supernatural
  Genres matched config  : Mystery, Slice of Life
  Genres unmatched       : Supernatural
```

**With --breakdown and a primary genre active:**

```
──────────────────────────────────────────────────────────────────────────────
  ...
  Story             9.0   25.00%     ×1.38      ...          ...   [primary blended]
  ...
──────────────────────────────────────────────────────────────────────────────
  Final Score                                               9.18 / 10
──────────────────────────────────────────────────────────────────────────────
  * bias-resistant — genre multipliers not applied
  Primary genre          : Mystery [primary] (blend 60/40)
  Genres returned        : Mystery, Slice of Life, Supernatural
  Genres matched config  : Mystery [primary], Slice of Life
  Genres unmatched       : Supernatural
```

When a primary genre is active, non-bias-resistant rows are annotated `[primary blended]`.
The footer shows the designated primary genre, its blend ratio, and marks it in the matched
genre list. When no primary genre is set, the "Primary genre" line is omitted entirely.

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
kansou score add "Mushishi" --primary-genre "Supernatural"
kansou score add "Mushishi" --primary-genre "Mystery" --breakdown
kansou score add "Mushishi" --notes
kansou score add "Mushishi" --breakdown --notes
```

**--notes flag:**

When `--notes` is set and the user confirms publish (`y`), `kansou` appends the
scoring breakdown as a note to the AniList list entry. If the entry already has
notes, the new block is appended after a `---` separator so prior content is
preserved.

```
✓ Score published to AniList
  Mushishi — 9.22
  ✓ Scoring breakdown appended to list entry notes
```

The note format is:
```
Mushishi
Score: 9.22 / 10  [kansou]

Dimension        Score   BaseW    ×Mult  FinalW  Contrib
───────────────────────────────────────────────────────
Story             9.0   25.0%   ×1.23   26.1%     2.35
Enjoyment        10.0   15.0%   ×1.00   13.2%     1.32  *
...

Genres:  Mystery, Slice of Life, Supernatural
Matched: Mystery, Slice of Life
Config:  04d78507
```

`--notes` has no effect if the user does not confirm publish.
The `--notes` and `--breakdown` flags are independent — `--breakdown` controls
terminal display, `--notes` controls what is written to AniList.

---


## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Any error (config invalid, AniList error, bad input, unknown command) |

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
