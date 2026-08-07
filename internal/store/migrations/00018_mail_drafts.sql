-- +goose Up
CREATE TABLE mail_drafts (
    draft_id TEXT PRIMARY KEY CHECK (draft_id LIKE 'drf_%'),
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    message_id TEXT NOT NULL UNIQUE REFERENCES messages(message_id) ON DELETE CASCADE,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1),
    sync_state TEXT NOT NULL DEFAULT 'local' CHECK (sync_state IN (
        'local', 'synced', 'reconcile', 'cleanup_pending', 'send_unknown'
    )),
    remote_location_id TEXT NOT NULL DEFAULT '',
    previous_location_id TEXT NOT NULL DEFAULT '',
    outbound_attempt_id TEXT NOT NULL DEFAULT '',
    custom_headers_json TEXT NOT NULL DEFAULT '[]',
    last_error_code TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX mail_drafts_account_updated_idx
    ON mail_drafts (account_id, updated_at DESC, draft_id DESC);
CREATE INDEX mail_drafts_message_idx
    ON mail_drafts (account_id, message_id);

-- +goose Down
DROP INDEX IF EXISTS mail_drafts_message_idx;
DROP INDEX IF EXISTS mail_drafts_account_updated_idx;
DROP TABLE IF EXISTS mail_drafts;
