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
# [server]
#
# Configuration for `kansou serve` mode.
# All fields are optional — the values below are the defaults.
# ---------------------------------------------------------------

[server]
port = 8080

# CORS allowed origins for the REST API.
# Add your frontend origin here when building a web UI.
# Default allows localhost development on common ports.
cors_allowed_origins = [
  "http://localhost:3000",
  "http://localhost:5173",
  "http://localhost:8080",
]
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
