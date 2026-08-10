-- +goose Up
CREATE INDEX outbound_attempts_sent_reply_parent_idx
    ON outbound_attempts (account_id, parent_message_id, answer_reconciliation)
    WHERE status = 'sent' AND outbound_kind IN ('reply', 'reply_all');

CREATE INDEX outbound_attempts_sent_forward_source_idx
    ON outbound_attempts (account_id, source_message_id, status);

-- +goose Down
DROP INDEX IF EXISTS outbound_attempts_sent_forward_source_idx;
DROP INDEX IF EXISTS outbound_attempts_sent_reply_parent_idx;
