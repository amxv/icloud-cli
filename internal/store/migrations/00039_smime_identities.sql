-- +goose Up
CREATE TABLE smime_identities (
    smime_identity_id       TEXT PRIMARY KEY CHECK (smime_identity_id LIKE 'smi_%'),
    account_id              TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    mail_identity_id        TEXT NOT NULL REFERENCES mail_identities(identity_id) ON DELETE RESTRICT,
    certificate_id          TEXT NOT NULL REFERENCES smime_certificates(certificate_id) ON DELETE RESTRICT,
    private_key_scheme      TEXT NOT NULL CHECK (private_key_scheme IN ('keyring', 'env', 'file')),
    private_key_reference   TEXT NOT NULL,
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL,
    UNIQUE (account_id, mail_identity_id),
    UNIQUE (account_id, certificate_id)
);

CREATE INDEX smime_identities_account_id_idx
    ON smime_identities (account_id, smime_identity_id);

-- +goose Down
DROP TABLE IF EXISTS smime_identities;
