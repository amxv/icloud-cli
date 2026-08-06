-- +goose Up
ALTER TABLE outbound_attempts
    ADD COLUMN outbound_kind TEXT NOT NULL DEFAULT 'send'
        CHECK (outbound_kind IN ('send', 'reply', 'reply_all'));
ALTER TABLE outbound_attempts
    ADD COLUMN parent_message_id TEXT NOT NULL DEFAULT '';

CREATE INDEX outbound_attempts_parent_idx
    ON outbound_attempts (account_id, parent_message_id, created_at, attempt_id);

-- +goose Down
DROP INDEX IF EXISTS outbound_attempts_parent_idx;
ALTER TABLE outbound_attempts DROP COLUMN parent_message_id;
ALTER TABLE outbound_attempts DROP COLUMN outbound_kind;
