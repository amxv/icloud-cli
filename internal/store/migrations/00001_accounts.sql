-- +goose Up
CREATE TABLE app_metadata (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT INTO app_metadata (key, value, updated_at)
VALUES ('application', 'icloud-cli', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));

CREATE TABLE accounts (
    account_id      TEXT PRIMARY KEY CHECK (account_id LIKE 'acct_%'),
    friendly_name   TEXT NOT NULL,
    email_address   TEXT NOT NULL,
    imap_username   TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('pending_validation', 'connected', 'degraded', 'disabled')),
    is_default      INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
    last_error_code TEXT,
    last_error_at   TEXT,
    last_healthy_at TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE UNIQUE INDEX accounts_friendly_name_key ON accounts (lower(friendly_name));
CREATE UNIQUE INDEX accounts_email_address_key ON accounts (lower(email_address));
CREATE UNIQUE INDEX accounts_one_default_key ON accounts (is_default) WHERE is_default = 1;

CREATE TABLE credential_refs (
    credential_ref_id TEXT PRIMARY KEY CHECK (credential_ref_id LIKE 'cred_%'),
    account_id        TEXT NOT NULL UNIQUE REFERENCES accounts(account_id) ON DELETE CASCADE,
    scheme            TEXT NOT NULL CHECK (scheme IN ('keyring', 'env', 'file')),
    reference         TEXT NOT NULL,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

CREATE INDEX credential_refs_account_idx ON credential_refs (account_id);

-- +goose Down
DROP TABLE IF EXISTS credential_refs;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS app_metadata;
