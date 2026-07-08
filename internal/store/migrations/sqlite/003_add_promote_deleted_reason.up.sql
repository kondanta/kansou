-- 1. Temporarily disable foreign key checks
PRAGMA foreign_keys=OFF;

-- 2. Create the replacement table with the updated CHECK constraint
CREATE TABLE scores_new (
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
    deleted_reason        TEXT CHECK (deleted_reason IN ('manual', 'max_history', 'promote') OR deleted_reason IS NULL)
);

-- 3. Copy all existing data over
INSERT INTO scores_new SELECT * FROM scores;

-- 4. Drop the old table
DROP TABLE scores;

-- 5. Rename the new table to the original name
ALTER TABLE scores_new RENAME TO scores;

-- 6. Recreate the indexes for the scores table
CREATE INDEX idx_scores_media_scored     ON scores(media_id, scored_at DESC);
CREATE INDEX idx_scores_is_latest        ON scores(is_latest);
CREATE INDEX idx_scores_deleted_at       ON scores(deleted_at) WHERE deleted_at IS NOT NULL;

-- 7. Turn foreign key checks back on
PRAGMA foreign_keys=ON;
