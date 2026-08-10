-- +goose Up
CREATE TABLE mailbox_import_jobs (
    job_id                    TEXT PRIMARY KEY CHECK (job_id LIKE 'mbi_%'),
    account_id                TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    folder_id                 TEXT NOT NULL REFERENCES folders(folder_id) ON DELETE RESTRICT,
    source_path               TEXT NOT NULL,
    source_content_hash       TEXT NOT NULL CHECK (length(source_content_hash) = 64),
    source_byte_size          INTEGER NOT NULL CHECK (source_byte_size >= 0),
    next_offset               INTEGER NOT NULL DEFAULT 0 CHECK (next_offset >= 0),
    next_index                INTEGER NOT NULL DEFAULT 1 CHECK (next_index >= 1),
    imported_count            INTEGER NOT NULL DEFAULT 0 CHECK (imported_count >= 0),
    status                    TEXT NOT NULL CHECK (status IN ('running', 'partial', 'confirmed')),
    error_code                TEXT NOT NULL DEFAULT '',
    created_at                TEXT NOT NULL,
    updated_at                TEXT NOT NULL,
    UNIQUE (account_id, folder_id, source_content_hash, source_byte_size)
);

CREATE INDEX mailbox_import_jobs_account_path_idx
    ON mailbox_import_jobs (account_id, folder_id, source_path, created_at DESC, job_id DESC);

CREATE TABLE mailbox_import_items (
    job_id                    TEXT NOT NULL REFERENCES mailbox_import_jobs(job_id) ON DELETE CASCADE,
    item_index                INTEGER NOT NULL CHECK (item_index >= 1),
    start_offset              INTEGER NOT NULL CHECK (start_offset >= 0),
    next_offset               INTEGER NOT NULL CHECK (next_offset > start_offset),
    raw_content_hash          TEXT NOT NULL CHECK (length(raw_content_hash) = 64),
    raw_byte_size             INTEGER NOT NULL CHECK (raw_byte_size >= 0),
    idempotency_key           TEXT NOT NULL,
    status                    TEXT NOT NULL CHECK (status IN ('prepared', 'confirmed', 'failed')),
    attempt_count             INTEGER NOT NULL DEFAULT 1 CHECK (attempt_count >= 1),
    retry_new_key             INTEGER NOT NULL DEFAULT 0 CHECK (retry_new_key IN (0, 1)),
    import_id                 TEXT NOT NULL DEFAULT '',
    message_id                TEXT NOT NULL DEFAULT '',
    location_id               TEXT NOT NULL DEFAULT '',
    error_code                TEXT NOT NULL DEFAULT '',
    created_at                TEXT NOT NULL,
    updated_at                TEXT NOT NULL,
    PRIMARY KEY (job_id, item_index),
    UNIQUE (job_id, start_offset),
    UNIQUE (job_id, idempotency_key)
);

CREATE INDEX mailbox_import_items_job_status_idx
    ON mailbox_import_items (job_id, status, item_index);

-- +goose Down
DROP INDEX IF EXISTS mailbox_import_items_job_status_idx;
DROP TABLE IF EXISTS mailbox_import_items;
DROP INDEX IF EXISTS mailbox_import_jobs_account_path_idx;
DROP TABLE IF EXISTS mailbox_import_jobs;
