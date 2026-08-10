-- +goose Up
CREATE TABLE sender_preferences (
    account_id        TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    email_address     TEXT NOT NULL CHECK (email_address = lower(trim(email_address)) AND email_address <> ''),
    vip               INTEGER NOT NULL DEFAULT 0 CHECK (vip IN (0, 1)),
    blocked           INTEGER NOT NULL DEFAULT 0 CHECK (blocked IN (0, 1)),
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    PRIMARY KEY (account_id, email_address)
);

CREATE INDEX sender_preferences_account_vip_email_idx
    ON sender_preferences (account_id, vip, email_address);
CREATE INDEX sender_preferences_account_blocked_email_idx
    ON sender_preferences (account_id, blocked, email_address);

-- +goose Down
DROP INDEX IF EXISTS sender_preferences_account_blocked_email_idx;
DROP INDEX IF EXISTS sender_preferences_account_vip_email_idx;
DROP TABLE IF EXISTS sender_preferences;
