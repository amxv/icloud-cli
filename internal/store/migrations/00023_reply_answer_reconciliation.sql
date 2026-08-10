-- +goose Up
ALTER TABLE outbound_attempts
    ADD COLUMN parent_location_id TEXT NOT NULL DEFAULT '';
ALTER TABLE outbound_attempts
    ADD COLUMN answer_reconciliation TEXT NOT NULL DEFAULT 'none'
        CHECK (answer_reconciliation IN ('none', 'pending', 'confirmed', 'unsupported'));
ALTER TABLE outbound_attempts
    ADD COLUMN answer_error_code TEXT NOT NULL DEFAULT '';

CREATE INDEX outbound_attempts_answer_reconciliation_idx
    ON outbound_attempts (account_id, answer_reconciliation, updated_at, attempt_id);

-- +goose Down
DROP INDEX IF EXISTS outbound_attempts_answer_reconciliation_idx;
ALTER TABLE outbound_attempts DROP COLUMN answer_error_code;
ALTER TABLE outbound_attempts DROP COLUMN answer_reconciliation;
ALTER TABLE outbound_attempts DROP COLUMN parent_location_id;
