-- +goose Up
ALTER TABLE outbound_attempts
    ADD COLUMN attachment_count INTEGER NOT NULL DEFAULT 0 CHECK (attachment_count >= 0);
ALTER TABLE outbound_attempts
    ADD COLUMN attachment_byte_size INTEGER NOT NULL DEFAULT 0 CHECK (attachment_byte_size >= 0);
ALTER TABLE outbound_attempts
    ADD COLUMN retry_of_attempt_id TEXT NOT NULL DEFAULT '';
ALTER TABLE outbound_attempts
    ADD COLUMN reconciled_location_id TEXT NOT NULL DEFAULT '';
ALTER TABLE outbound_attempts
    ADD COLUMN reconciliation_error_code TEXT NOT NULL DEFAULT '';

CREATE TABLE outbound_attempt_attachments (
    attempt_id   TEXT NOT NULL REFERENCES outbound_attempts(attempt_id) ON DELETE CASCADE,
    ordinal      INTEGER NOT NULL CHECK (ordinal >= 0),
    filename     TEXT NOT NULL,
    media_type   TEXT NOT NULL,
    content_hash TEXT NOT NULL CHECK (length(content_hash) = 64),
    storage_key  TEXT NOT NULL,
    byte_size    INTEGER NOT NULL CHECK (byte_size >= 0),
    PRIMARY KEY (attempt_id, ordinal)
);

CREATE INDEX outbound_attempts_reconciliation_idx
    ON outbound_attempts (account_id, reconciliation, updated_at, attempt_id);
CREATE INDEX outbound_attempt_attachments_hash_idx
    ON outbound_attempt_attachments (content_hash);

-- +goose Down
DROP INDEX IF EXISTS outbound_attempt_attachments_hash_idx;
DROP INDEX IF EXISTS outbound_attempts_reconciliation_idx;
DROP TABLE IF EXISTS outbound_attempt_attachments;
ALTER TABLE outbound_attempts DROP COLUMN reconciliation_error_code;
ALTER TABLE outbound_attempts DROP COLUMN reconciled_location_id;
ALTER TABLE outbound_attempts DROP COLUMN retry_of_attempt_id;
ALTER TABLE outbound_attempts DROP COLUMN attachment_byte_size;
ALTER TABLE outbound_attempts DROP COLUMN attachment_count;
