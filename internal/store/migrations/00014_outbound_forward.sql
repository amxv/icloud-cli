-- +goose Up
ALTER TABLE outbound_attempts
    ADD COLUMN source_message_id TEXT NOT NULL DEFAULT '';

CREATE INDEX outbound_attempts_source_idx
    ON outbound_attempts (account_id, source_message_id, created_at, attempt_id);

CREATE INDEX outbound_attempts_forward_message_idx
    ON outbound_attempts (account_id, proposed_message_id)
    WHERE source_message_id <> '';

-- +goose Down
DROP INDEX IF EXISTS outbound_attempts_source_idx;
DROP INDEX IF EXISTS outbound_attempts_forward_message_idx;
ALTER TABLE outbound_attempts DROP COLUMN source_message_id;
