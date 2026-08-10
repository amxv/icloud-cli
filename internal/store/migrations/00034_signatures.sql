-- +goose Up
CREATE TABLE mail_signatures (
    signature_id TEXT PRIMARY KEY CHECK (signature_id LIKE 'sig_%'),
    account_id   TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    identity_id  TEXT REFERENCES mail_identities(identity_id) ON DELETE CASCADE,
    text_body    TEXT NOT NULL,
    html_body    TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE INDEX mail_signatures_account_page_idx
    ON mail_signatures (account_id, signature_id);
CREATE INDEX mail_signatures_identity_idx
    ON mail_signatures (account_id, identity_id, signature_id);

-- A NULL identity is the account-wide default scope. A row with a NULL
-- signature_id means explicit none, which is different from no row (fallback
-- from an identity-specific scope to the account-wide scope).
CREATE TABLE mail_signature_defaults (
    account_id   TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    identity_id  TEXT REFERENCES mail_identities(identity_id) ON DELETE CASCADE,
    operation    TEXT NOT NULL CHECK (operation IN ('send', 'reply', 'reply_all', 'forward')),
    signature_id TEXT REFERENCES mail_signatures(signature_id) ON DELETE CASCADE,
    updated_at   TEXT NOT NULL
);

CREATE UNIQUE INDEX mail_signature_defaults_scope_key
    ON mail_signature_defaults (account_id, IFNULL(identity_id, ''), operation);
CREATE INDEX mail_signature_defaults_signature_idx
    ON mail_signature_defaults (account_id, signature_id);

-- +goose Down
DROP TABLE IF EXISTS mail_signature_defaults;
DROP TABLE IF EXISTS mail_signatures;
