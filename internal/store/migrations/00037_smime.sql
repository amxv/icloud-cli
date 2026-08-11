-- +goose Up
ALTER TABLE message_content ADD COLUMN smime_json TEXT NOT NULL DEFAULT '';

CREATE TABLE smime_certificates (
    certificate_id      TEXT PRIMARY KEY CHECK (certificate_id LIKE 'smc_%'),
    account_id          TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    email               TEXT NOT NULL,
    fingerprint_sha256  TEXT NOT NULL CHECK (length(fingerprint_sha256) = 64),
    der                 BLOB NOT NULL CHECK (length(der) > 0 AND length(der) <= 65536),
    trusted             INTEGER NOT NULL DEFAULT 0 CHECK (trusted IN (0, 1)),
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    UNIQUE (account_id, email, fingerprint_sha256)
);

CREATE INDEX smime_certificates_account_email_idx
    ON smime_certificates (account_id, email, certificate_id);

-- +goose Down
DROP TABLE IF EXISTS smime_certificates;
