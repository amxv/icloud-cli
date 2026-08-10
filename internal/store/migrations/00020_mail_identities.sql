-- +goose Up
CREATE TABLE mail_identities (
    identity_id     TEXT PRIMARY KEY CHECK (identity_id LIKE 'mid_%'),
    account_id      TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    email_address   TEXT NOT NULL,
    display_name    TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('primary', 'icloud_alias', 'custom_domain', 'hide_my_email', 'other')),
    provenance      TEXT NOT NULL CHECK (provenance IN ('migrated_primary', 'account_primary', 'user_configured')),
    verification    TEXT NOT NULL CHECK (verification IN ('unverified', 'verified', 'rejected')),
    enabled         INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    is_default_send INTEGER NOT NULL CHECK (is_default_send IN (0, 1)),
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE UNIQUE INDEX mail_identities_account_address_key
    ON mail_identities (account_id, lower(email_address));
CREATE UNIQUE INDEX mail_identities_one_primary_key
    ON mail_identities (account_id) WHERE kind = 'primary';
CREATE UNIQUE INDEX mail_identities_one_default_key
    ON mail_identities (account_id) WHERE is_default_send = 1;
CREATE INDEX mail_identities_account_page_idx
    ON mail_identities (account_id, is_default_send DESC, lower(email_address), identity_id);

INSERT INTO mail_identities (
    identity_id, account_id, email_address, display_name, kind, provenance,
    verification, enabled, is_default_send, created_at, updated_at
)
SELECT
    'mid_' || substr(account_id, 6), account_id, lower(email_address), friendly_name,
    'primary', 'migrated_primary', 'unverified', 1, 1, created_at, updated_at
FROM accounts;

-- +goose Down
DROP TABLE IF EXISTS mail_identities;
