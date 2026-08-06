-- +goose Up
CREATE TABLE outbound_attempts (
    attempt_id          TEXT PRIMARY KEY CHECK (attempt_id LIKE 'send_%'),
    account_id          TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    proposed_message_id TEXT NOT NULL CHECK (proposed_message_id LIKE 'msg_%'),
    request_hash        TEXT NOT NULL CHECK (length(request_hash) = 64),
    idempotency_key     TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'failed', 'unknown')),
    rfc_message_id      TEXT NOT NULL,
    raw_content_hash    TEXT NOT NULL REFERENCES raw_blobs(content_hash),
    raw_storage_key     TEXT NOT NULL,
    raw_byte_size       INTEGER NOT NULL CHECK (raw_byte_size >= 0),
    to_count            INTEGER NOT NULL DEFAULT 0 CHECK (to_count >= 0),
    cc_count            INTEGER NOT NULL DEFAULT 0 CHECK (cc_count >= 0),
    bcc_count           INTEGER NOT NULL DEFAULT 0 CHECK (bcc_count >= 0),
    body_part_count     INTEGER NOT NULL DEFAULT 0 CHECK (body_part_count >= 0),
    message_id          TEXT NOT NULL DEFAULT '',
    thread_id           TEXT NOT NULL DEFAULT '',
    reconciliation     TEXT NOT NULL DEFAULT 'none' CHECK (reconciliation IN ('none', 'pending', 'confirmed', 'unknown')),
    error_code          TEXT NOT NULL DEFAULT '',
    accepted_at         TEXT,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);

CREATE INDEX outbound_attempts_account_created_idx
    ON outbound_attempts (account_id, created_at DESC, attempt_id DESC);
CREATE INDEX outbound_attempts_status_idx
    ON outbound_attempts (account_id, status, updated_at);
CREATE UNIQUE INDEX outbound_attempts_account_idempotency_unique
    ON outbound_attempts (account_id, idempotency_key)
    WHERE idempotency_key <> '';

CREATE TABLE idempotency_records (
    account_id      TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL,
    request_hash    TEXT NOT NULL CHECK (length(request_hash) = 64),
    attempt_id      TEXT NOT NULL REFERENCES outbound_attempts(attempt_id) ON DELETE CASCADE,
    created_at      TEXT NOT NULL,
    PRIMARY KEY (account_id, idempotency_key)
);

CREATE TABLE local_message_sources (
    message_id       TEXT PRIMARY KEY REFERENCES messages(message_id) ON DELETE CASCADE,
    raw_content_hash TEXT NOT NULL REFERENCES raw_blobs(content_hash),
    raw_storage_key  TEXT NOT NULL,
    raw_byte_size    INTEGER NOT NULL CHECK (raw_byte_size >= 0),
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS local_message_sources;
DROP TABLE IF EXISTS idempotency_records;
DROP TABLE IF EXISTS outbound_attempts;
