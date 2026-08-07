-- +goose Up
CREATE TABLE mail_backfill_frontiers (
    frontier_id TEXT PRIMARY KEY CHECK (frontier_id LIKE 'bf_%'),
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    folder_id TEXT NOT NULL REFERENCES folders(folder_id) ON DELETE CASCADE,
    after_time TEXT NOT NULL DEFAULT '',
    before_time TEXT NOT NULL DEFAULT '',
    uid_validity INTEGER NOT NULL CHECK (uid_validity >= 0),
    before_uid INTEGER NOT NULL CHECK (before_uid >= 0),
    complete INTEGER NOT NULL DEFAULT 0 CHECK (complete IN (0, 1)),
    last_attempt_at TEXT,
    last_success_at TEXT,
    last_error_code TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (account_id, folder_id, after_time, before_time)
);

CREATE INDEX mail_backfill_frontiers_account_folder_idx
    ON mail_backfill_frontiers (account_id, folder_id, updated_at DESC, frontier_id);

-- +goose Down
DROP INDEX IF EXISTS mail_backfill_frontiers_account_folder_idx;
DROP TABLE IF EXISTS mail_backfill_frontiers;
