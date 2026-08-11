-- +goose Up
ALTER TABLE message_content ADD COLUMN smime_policy_revision INTEGER NOT NULL DEFAULT 0 CHECK (smime_policy_revision >= 0);

CREATE TABLE smime_policy_state (
    account_id  TEXT PRIMARY KEY REFERENCES accounts(account_id) ON DELETE CASCADE,
    revision    INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
    updated_at  TEXT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS smime_policy_state;
