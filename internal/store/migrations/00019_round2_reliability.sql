-- +goose Up
DROP INDEX IF EXISTS messages_account_rfc_id_unique;
CREATE INDEX messages_account_rfc_id_idx
    ON messages (account_id, lower(rfc_message_id), message_id)
    WHERE rfc_message_id <> '';

CREATE INDEX IF NOT EXISTS folders_account_enabled_page_idx
    ON folders (account_id, sync_enabled, present, stale, selectable, folder_id);
CREATE INDEX IF NOT EXISTS folders_account_selectable_page_idx
    ON folders (account_id, present, stale, selectable, folder_id);

CREATE TABLE thread_projection_state (
    account_id         TEXT PRIMARY KEY REFERENCES accounts(account_id) ON DELETE CASCADE,
    state              TEXT NOT NULL CHECK (state IN ('ready', 'pending', 'degraded')),
    reason             TEXT NOT NULL DEFAULT '',
    backfill_cursor    TEXT NOT NULL DEFAULT '',
    pending_since      TEXT,
    updated_at         TEXT NOT NULL
);

CREATE TABLE thread_projection_jobs (
    account_id       TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    seed_message_id  TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    reason           TEXT NOT NULL,
    repair_phase     TEXT NOT NULL DEFAULT 'discover' CHECK (repair_phase IN ('discover', 'scan', 'edges')),
    repair_cursor    TEXT NOT NULL DEFAULT '',
    target_threads_json TEXT NOT NULL DEFAULT '[]',
    canonical_thread_id TEXT NOT NULL DEFAULT '',
    attempts         INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error       TEXT NOT NULL DEFAULT '',
    aggregate_message_count    INTEGER NOT NULL DEFAULT 0 CHECK (aggregate_message_count >= 0),
    aggregate_unread_count     INTEGER NOT NULL DEFAULT 0 CHECK (aggregate_unread_count >= 0),
    aggregate_attachment_count INTEGER NOT NULL DEFAULT 0 CHECK (aggregate_attachment_count >= 0),
    aggregate_first_message_at TEXT NOT NULL DEFAULT '',
    aggregate_last_message_at  TEXT NOT NULL DEFAULT '',
    aggregate_latest_sort_at   TEXT NOT NULL DEFAULT '',
    aggregate_latest_message_id TEXT NOT NULL DEFAULT '',
    aggregate_latest_snippet   TEXT NOT NULL DEFAULT '',
    aggregate_latest_subject   TEXT NOT NULL DEFAULT '',
    aggregate_latest_rfc_message_id TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    PRIMARY KEY (account_id, seed_message_id)
);

CREATE INDEX thread_projection_jobs_account_idx
    ON thread_projection_jobs (account_id, seed_message_id);

CREATE TABLE thread_projection_repair_participants (
    account_id      TEXT NOT NULL,
    seed_message_id TEXT NOT NULL,
    value           TEXT NOT NULL,
    PRIMARY KEY (account_id, seed_message_id, value),
    FOREIGN KEY (account_id, seed_message_id)
        REFERENCES thread_projection_jobs(account_id, seed_message_id)
        ON DELETE CASCADE
);

CREATE TABLE thread_projection_repair_folders (
    account_id      TEXT NOT NULL,
    seed_message_id TEXT NOT NULL,
    folder_id       TEXT NOT NULL,
    PRIMARY KEY (account_id, seed_message_id, folder_id),
    FOREIGN KEY (account_id, seed_message_id)
        REFERENCES thread_projection_jobs(account_id, seed_message_id)
        ON DELETE CASCADE
);

INSERT INTO thread_projection_state (
    account_id, state, reason, backfill_cursor, pending_since, updated_at
)
SELECT account_id, 'pending', 'schema_backfill', '',
       strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
       strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
FROM accounts;

-- The v9-era global marker cannot prove that Round 2's bounded projection did
-- not silently skip an oversized affected component. v19 deliberately
-- re-audits every existing account through the persisted keyset frontier.
INSERT INTO app_metadata (key, value, updated_at)
VALUES ('thread_graph_backfill_v1', 'pending_v19', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;

-- +goose Down
DROP TABLE IF EXISTS thread_projection_repair_folders;
DROP TABLE IF EXISTS thread_projection_repair_participants;
DROP TABLE IF EXISTS thread_projection_jobs;
DROP TABLE IF EXISTS thread_projection_state;
DROP INDEX IF EXISTS folders_account_selectable_page_idx;
DROP INDEX IF EXISTS folders_account_enabled_page_idx;
DROP INDEX IF EXISTS messages_account_rfc_id_idx;
CREATE UNIQUE INDEX messages_account_rfc_id_unique
    ON messages (account_id, lower(rfc_message_id))
    WHERE rfc_message_id <> '';
