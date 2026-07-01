CREATE TABLE media (
    id            SERIAL PRIMARY KEY,
    anilist_id    INTEGER      NOT NULL UNIQUE,
    title_romaji  TEXT         NOT NULL,
    title_english TEXT,
    media_type    TEXT         NOT NULL CHECK (media_type IN ('ANIME', 'MANGA')),
    format        TEXT         NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE media_genres (
    media_id  INTEGER NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    genre     TEXT    NOT NULL,
    PRIMARY KEY (media_id, genre)
);

CREATE TABLE scores (
    id                    SERIAL PRIMARY KEY,
    media_id              INTEGER          NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    final_score           DOUBLE PRECISION NOT NULL,
    primary_genre         TEXT,
    primary_genre_weight  DOUBLE PRECISION,
    config_hash           TEXT             NOT NULL,
    config_snapshot       JSONB            NOT NULL,
    user_selected_genres  JSONB,
    is_latest             BOOLEAN          NOT NULL DEFAULT FALSE,
    scored_at             TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    deleted_at            TIMESTAMPTZ
);

CREATE TABLE dimension_scores (
    id                 SERIAL PRIMARY KEY,
    score_id           INTEGER          NOT NULL REFERENCES scores(id) ON DELETE CASCADE,
    dimension_key      TEXT             NOT NULL,
    label              TEXT             NOT NULL,
    score              DOUBLE PRECISION,
    base_weight        DOUBLE PRECISION NOT NULL,
    final_weight       DOUBLE PRECISION NOT NULL,
    applied_multiplier DOUBLE PRECISION NOT NULL,
    contribution       DOUBLE PRECISION,
    skipped            BOOLEAN          NOT NULL DEFAULT FALSE,
    bias_resistant     BOOLEAN          NOT NULL DEFAULT FALSE,
    weight_override    BOOLEAN          NOT NULL DEFAULT FALSE,
    genre_deselected   BOOLEAN          NOT NULL DEFAULT FALSE
);

CREATE TABLE score_matched_genres (
    score_id           INTEGER          NOT NULL REFERENCES scores(id) ON DELETE CASCADE,
    genre              TEXT             NOT NULL,
    is_primary         BOOLEAN          NOT NULL DEFAULT FALSE,
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
    weight         DOUBLE PRECISION NOT NULL,
    bias_resistant BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE genre_multipliers (
    genre          TEXT             NOT NULL,
    dimension_key  TEXT             NOT NULL REFERENCES dimensions(key) ON DELETE CASCADE,
    multiplier     DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (genre, dimension_key)
);

CREATE TABLE config_scalars (
    id                   INTEGER          PRIMARY KEY,
    primary_genre_weight DOUBLE PRECISION NOT NULL DEFAULT 0.6,
    max_multiplier       DOUBLE PRECISION NOT NULL DEFAULT 2.0,
    max_history          INTEGER          NOT NULL DEFAULT 0
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
