-- +goose Up
-- Thread mute is local client state anchored to a stable logical message, not
-- to a derived thread row. Current thread membership projects the preference,
-- so late graph merges and stronger-evidence splits cannot orphan it.
CREATE TABLE thread_preferences (
    account_id        TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    anchor_message_id TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    muted             INTEGER NOT NULL DEFAULT 1 CHECK (muted IN (0, 1)),
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    PRIMARY KEY (account_id, anchor_message_id)
);

CREATE INDEX thread_preferences_account_muted_anchor_idx
    ON thread_preferences (account_id, muted, anchor_message_id);

-- +goose Down
DROP TABLE IF EXISTS thread_preferences;
