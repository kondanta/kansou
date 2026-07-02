CREATE TABLE media (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    anilist_id    INTEGER NOT NULL UNIQUE,
    title_romaji  TEXT    NOT NULL,
    title_english TEXT,
    media_type    TEXT    NOT NULL,
    format        TEXT    NOT NULL,
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE TABLE media_genres (
    media_id INTEGER NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    genre    TEXT    NOT NULL,
    PRIMARY KEY (media_id, genre)
);

CREATE TABLE scores (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    media_id              INTEGER NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    final_score           REAL    NOT NULL,
    primary_genre         TEXT,
    primary_genre_weight  REAL,
    config_hash           TEXT    NOT NULL,
    config_snapshot       TEXT    NOT NULL,
    user_selected_genres  TEXT,
    is_latest             INTEGER NOT NULL DEFAULT 0,
    scored_at             TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    deleted_at            TEXT,
    deleted_reason        TEXT CHECK (deleted_reason IN ('manual', 'max_history') OR deleted_reason IS NULL)
);

CREATE TABLE dimension_scores (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    score_id           INTEGER NOT NULL REFERENCES scores(id) ON DELETE CASCADE,
    dimension_key      TEXT    NOT NULL,
    label              TEXT    NOT NULL,
    score              REAL,
    base_weight        REAL    NOT NULL,
    final_weight       REAL    NOT NULL,
    applied_multiplier REAL    NOT NULL,
    contribution       REAL,
    skipped            INTEGER NOT NULL DEFAULT 0,
    bias_resistant     INTEGER NOT NULL DEFAULT 0,
    weight_override    INTEGER NOT NULL DEFAULT 0,
    genre_deselected   INTEGER NOT NULL DEFAULT 0,
    primary_genre_multiplier    REAL NOT NULL DEFAULT 0,
    secondary_genres_multiplier REAL NOT NULL DEFAULT 0
);

CREATE TABLE score_matched_genres (
    score_id           INTEGER NOT NULL REFERENCES scores(id) ON DELETE CASCADE,
    genre              TEXT    NOT NULL,
    is_primary         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (score_id, genre)
);

CREATE TABLE db_metadata (
    key   TEXT NOT NULL PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE dimensions (
    key            TEXT    NOT NULL PRIMARY KEY,
    label          TEXT    NOT NULL,
    description    TEXT    NOT NULL DEFAULT '',
    weight         REAL    NOT NULL,
    bias_resistant INTEGER NOT NULL DEFAULT 0,
    sort_order     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE genre_multipliers (
    genre         TEXT NOT NULL,
    dimension_key TEXT NOT NULL REFERENCES dimensions(key) ON DELETE CASCADE,
    multiplier    REAL NOT NULL,
    PRIMARY KEY (genre, dimension_key)
);

CREATE TABLE config_scalars (
    id                   INTEGER PRIMARY KEY,
    primary_genre_weight REAL    NOT NULL DEFAULT 0.6,
    max_multiplier       REAL    NOT NULL DEFAULT 2.0,
    max_history          INTEGER NOT NULL DEFAULT 0
);

INSERT INTO config_scalars (id, primary_genre_weight, max_multiplier, max_history)
VALUES (1, 0.6, 2.0, 0);

CREATE INDEX idx_scores_media_scored     ON scores(media_id, scored_at DESC);
CREATE INDEX idx_scores_is_latest        ON scores(is_latest);
CREATE INDEX idx_scores_deleted_at       ON scores(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX idx_dim_scores_score_id     ON dimension_scores(score_id);
CREATE INDEX idx_dim_scores_dim_key      ON dimension_scores(dimension_key);
CREATE INDEX idx_media_genres_genre      ON media_genres(genre);
CREATE INDEX idx_matched_genres_score_id ON score_matched_genres(score_id);
