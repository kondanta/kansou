# kansou (感想)

A personal anime and manga scoring CLI and REST server.

Fetches media metadata from [AniList](https://anilist.co), walks you through a structured per-dimension scoring session, applies a weighted genre-adjusted formula, and publishes the final score back to your AniList account.

---

## Features

- **Configurable dimensions** — define any scoring dimensions you want in `config.toml`; the engine has no hardcoded list
- **Genre-aware weights** — multipliers shift dimension weights based on the media's AniList genres; averaged across all matched genres
- **Bias-resistant dimensions** — mark dimensions like Enjoyment as immune to genre adjustment
- **Per-session overrides** — `--weight pacing=0.05` to nudge a specific show without touching your config
- **Skippable dimensions** — enter `s` at any prompt to mark a dimension as N/A
- **Full provenance** — every result carries a per-dimension audit trail and a config hash
- **REST server** — same logic over HTTP for a web frontend, with Swagger UI included

---

## How It Works

kansou scores media through a four-step pipeline. The renormalization step is what makes it robust: skipping a dimension or applying a genre multiplier never silently distorts the other weights — the formula always rebalances.

### Step 1 — Effective weight

Each active dimension starts from its configured base weight. If the media's genres match any configured genre rules, the dimension's weight is nudged by a **genre multiplier**.

Let $G_i \subseteq G$ be the subset of matched genres that **explicitly define** a multiplier for dimension $i$. Genres that have no configured entry for dimension $i$ are excluded entirely — they do not contribute a diluting $1.0$ to the average (Option B, see ADR-021):

$$\bar{m}_i = \begin{cases} \dfrac{1}{|G_i|} \displaystyle\sum_{g \in G_i} m_{g,i} & \text{if } |G_i| > 0 \\ 1.0 & \text{if } |G_i| = 0 \end{cases}$$

The effective (pre-normalisation) weight is:

$$w_{\text{eff},i} = w_{\text{base},i} \times \bar{m}_i$$

Dimensions marked `bias_resistant` always use $\bar{m}_i = 1.0$ — genre rules never touch them.

**Optional: primary genre blend.** When `--primary-genre` is specified, one genre is designated as constitutive. Its multiplier is blended with the contributing-only average across the remaining genres at a configurable ratio $\beta$ (`primary_genre_weight`, default $0.6$):

$$\bar{m}_i = \beta \cdot m_{\text{primary},i} + (1-\beta) \cdot \bar{m}^{\text{secondary}}_i$$

where $m_{\text{primary},i}$ is the primary genre's multiplier for dimension $i$ (or $1.0$ if it has no entry), and $\bar{m}^{\text{secondary}}_i$ is the contributing-only average over non-primary matched genres. Setting $\beta = 0$ disables the feature.

### Step 2 — Renormalization

Skipped dimensions are removed from the pool entirely. The remaining effective weights are rescaled to sum exactly to $1.0$:

$$w'_i = \frac{w_{\text{eff},i}}{\displaystyle\sum_{j \in \text{active}} w_{\text{eff},j}}$$

This is the core of the formula. Whether you skip two dimensions or five, whether genres push weights up or down, the active dimensions always share the full $[0, 1]$ budget proportionally. Nothing leaks, nothing inflates.

### Step 3 — Per-session overrides (optional)

`--weight pacing=0.05` pins a dimension to an explicit value. Overridden dimensions are fixed; the remaining budget is distributed proportionally among the rest:

$$w''_k = \frac{w'_k}{\displaystyle\sum_{j \notin \text{pinned}} w'_j} \times \left(1 - \sum_{i \in \text{pinned}} w^*_i\right) \quad \text{for } k \notin \text{pinned}$$

### Step 4 — Final score

$$\text{score} = \sum_i s_i \times w''_i \qquad s_i \in [1, 10]$$

Each dimension's score is multiplied by its final renormalized weight. The result is a single number on the $[1, 10]$ scale.

### Example

Config has six dimensions with equal base weights of $0.20$. The media matches one genre that defines a $1.5\times$ multiplier on *Story* and a $0.8\times$ multiplier on *Pacing* — it has **no entry** for *Characters* or *World Building*. The user skips *Production*.

| Dimension   | Base W | $G_i$ | Multiplier | Effective W | Renormalized W |
|-------------|--------|--------|-----------|-------------|----------------|
| Story       | 0.20   | 1      | ×1.50     | 0.300       | **0.294**      |
| Characters  | 0.20   | 0      | ×1.00 †   | 0.200       | **0.196**      |
| Pacing      | 0.20   | 1      | ×0.80     | 0.160       | **0.157**      |
| Enjoyment   | 0.20   | —  *   | ×1.00     | 0.200       | **0.196**      |
| Production  | —      | —      | skipped   | —           | —              |
| World Build | 0.20   | 0      | ×1.00 †   | 0.200       | **0.196**      |
| **Total**   |        |        |           | **1.020** ✗ | **1.000** ✓    |

\* Enjoyment is `bias_resistant` — genre rules never apply.  
† No matched genre defined a multiplier for this dimension — contributing-only averaging returns $1.0$ (neutral), not an average diluted by a phantom $1.0$ contribution.

Without renormalization the weights would sum to $1.02$ and the score would be silently inflated. With renormalization, the active pool is always $1.0$ and the result is honest.

---

## Installation

```bash
git clone https://github.com/kondanta/kansou
cd kansou
just build
```

Or with version stamping:

```bash
just build-release
```

Requires Go 1.26+.

---

## Configuration

Copy the example config and edit to taste:

```bash
cp config.example.toml ~/.config/kansou/config.toml
```

The tool runs with built-in defaults if no config file is found. See [`docs/CONFIG.md`](docs/CONFIG.md) for the full schema.

---

## AniList Token

Write operations (publish prompt in `score add`, `POST /score/publish`) require an AniList user token:

```bash
export ANILIST_TOKEN=your_token_here
```

To obtain a token:
1. Go to https://anilist.co/settings/developer
2. Create a client (redirect URI not needed for personal use)
3. Authorise via: `https://anilist.co/api/v2/oauth/authorize?client_id={id}&response_type=token`
4. Copy the token from the redirect URL

Read operations (search, fetch) do not require a token.

> **What publish does and does not do:**
> Publishing writes only the final numeric score to your AniList list entry.
> It does **not** change the entry's status (watching, completed, dropped, etc.).
> If the entry does not yet exist in your list, it is created with the score but no status set.
> Your watch/read status is always left as-is.

---

## CLI Usage

```
kansou [command]

Commands:
  media find <query>    Search AniList and display media info
  score add <query>     Start an interactive scoring session (includes publish prompt)
  serve                 Start the REST server

Global flags:
  --config <path>       Config file path (default: ~/.config/kansou/config.toml)
  --version             Print version and exit
```

### Score a show

```bash
kansou score add "Frieren: Beyond Journey's End"
kansou score add "frieren" --breakdown
kansou score add --url https://anilist.co/anime/154587 --breakdown
```

After scoring, you'll be prompted:

```
Publish to AniList? [y/N]:
```

With per-session weight overrides:

```bash
kansou score add "Mushishi" --weight pacing=0.05,world_building=0.20
```

### Look up media without scoring

```bash
kansou media find "Mushishi"
kansou media find --url https://anilist.co/anime/457
```

---

## REST Server

```bash
kansou serve
kansou serve --port 3000
```

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Liveness check |
| `GET` | `/dimensions` | List configured scoring dimensions |
| `GET` | `/genres` | List configured genre multiplier blocks |
| `GET` | `/media/search?q={query}` | Search AniList by name |
| `GET` | `/media/{id}` | Fetch media by AniList ID |
| `POST` | `/score` | Calculate a weighted score |
| `POST` | `/score/publish` | Publish a score to AniList |

Swagger UI: `http://localhost:8080/swagger/index.html`

All errors return `{ "error": "description" }`.

---

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `ANILIST_TOKEN` | For write ops | AniList user token |
| `LOG_LEVEL` | No | `debug`, `info`, `warn`, `error` (default: `info`) |
| `NO_COLOR` | No | Set to disable coloured CLI log output |

---

## Development

```
just build          # build binary
just build-release  # build with git version stamp
just test           # run tests
just test-race      # run tests with race detector
just vet            # go vet
just check          # build + test + vet (full definition-of-done gate)
just swagger        # regenerate Swagger docs after handler changes
just run -- <args>  # run via go run
just serve          # start server via go run
just clean          # remove built binary
```

---

## Docs

| Document | Contents |
|----------|----------|
| [`docs/REQUIREMENTS.md`](docs/REQUIREMENTS.md) | Functional and non-functional requirements |
| [`docs/ADR.md`](docs/ADR.md) | Architecture decision records |
| [`docs/CONFIG.md`](docs/CONFIG.md) | Full config schema reference |
| [`docs/CLI.md`](docs/CLI.md) | CLI command reference |
| [`docs/ANILIST_INTEGRATION.md`](docs/ANILIST_INTEGRATION.md) | AniList GraphQL integration details |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | Package structure and data flow |

---

## License

Licensed under either of:

- [MIT License](LICENSE-MIT)
- [Apache License, Version 2.0](LICENSE-APACHE)

at your option.

Copyright (c) 2026 Taylan Dogan
