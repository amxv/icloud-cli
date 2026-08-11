-- +goose Up
CREATE TABLE outbox_items (
    outbox_id            TEXT PRIMARY KEY CHECK (outbox_id LIKE 'out_%'),
    account_id           TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    source_attempt_id    TEXT NOT NULL REFERENCES outbound_attempts(attempt_id) ON DELETE CASCADE,
    latest_attempt_id    TEXT NOT NULL DEFAULT '',
    sender_identity_id   TEXT NOT NULL,
    status               TEXT NOT NULL CHECK (status IN ('retryable', 'retrying', 'sent', 'failed', 'unknown', 'canceled')),
    failure_stage        TEXT NOT NULL DEFAULT '',
    failure_code         TEXT NOT NULL DEFAULT '',
    request_hash         TEXT NOT NULL CHECK (length(request_hash) = 64),
    rfc_message_id       TEXT NOT NULL,
    raw_content_hash     TEXT NOT NULL REFERENCES raw_blobs(content_hash),
    raw_storage_key      TEXT NOT NULL,
    raw_byte_size        INTEGER NOT NULL CHECK (raw_byte_size >= 0),
    envelope_json        TEXT NOT NULL,
    outbound_kind        TEXT NOT NULL CHECK (outbound_kind IN ('send', 'reply', 'reply_all', 'forward')),
    parent_message_id    TEXT NOT NULL DEFAULT '',
    parent_location_id   TEXT NOT NULL DEFAULT '',
    source_message_id    TEXT NOT NULL DEFAULT '',
    to_count             INTEGER NOT NULL DEFAULT 0 CHECK (to_count >= 0),
    cc_count             INTEGER NOT NULL DEFAULT 0 CHECK (cc_count >= 0),
    bcc_count            INTEGER NOT NULL DEFAULT 0 CHECK (bcc_count >= 0),
    body_part_count      INTEGER NOT NULL DEFAULT 0 CHECK (body_part_count >= 0),
    attachment_count     INTEGER NOT NULL DEFAULT 0 CHECK (attachment_count >= 0),
    attachment_bytes     INTEGER NOT NULL DEFAULT 0 CHECK (attachment_bytes >= 0),
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    UNIQUE (account_id, source_attempt_id)
);

CREATE INDEX outbox_items_account_created_idx
    ON outbox_items (account_id, created_at DESC, outbox_id DESC);
CREATE INDEX outbox_items_account_status_idx
    ON outbox_items (account_id, status, updated_at, outbox_id);

-- +goose Down
DROP INDEX IF EXISTS outbox_items_account_status_idx;
DROP INDEX IF EXISTS outbox_items_account_created_idx;
DROP TABLE IF EXISTS outbox_items;
