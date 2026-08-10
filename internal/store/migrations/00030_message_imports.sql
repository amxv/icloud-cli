-- +goose Up
CREATE TABLE message_imports (
    import_id                TEXT PRIMARY KEY CHECK (import_id LIKE 'imp_%'),
    account_id               TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    folder_id                TEXT NOT NULL REFERENCES folders(folder_id) ON DELETE RESTRICT,
    raw_content_hash         TEXT NOT NULL CHECK (length(raw_content_hash) = 64),
    raw_storage_key          TEXT NOT NULL,
    raw_byte_size            INTEGER NOT NULL CHECK (raw_byte_size >= 0),
    rfc_message_id           TEXT NOT NULL DEFAULT '',
    internal_date            TEXT NOT NULL,
    flags_json               TEXT NOT NULL DEFAULT '[]',
    baseline_uid_validity    INTEGER NOT NULL CHECK (baseline_uid_validity > 0),
    baseline_uid_next        INTEGER NOT NULL CHECK (baseline_uid_next > 0),
    candidate_uid_upper      INTEGER NOT NULL DEFAULT 0 CHECK (candidate_uid_upper >= 0),
    provider_uid_validity    INTEGER NOT NULL DEFAULT 0 CHECK (provider_uid_validity >= 0),
    provider_uid             INTEGER NOT NULL DEFAULT 0 CHECK (provider_uid >= 0),
    message_id               TEXT REFERENCES messages(message_id) ON DELETE RESTRICT,
    location_id              TEXT REFERENCES message_locations(location_id) ON DELETE RESTRICT,
    request_hash             TEXT NOT NULL CHECK (length(request_hash) = 64),
    idempotency_key          TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 256),
    status                   TEXT NOT NULL CHECK (status IN ('pending', 'append_unresolved', 'appended', 'confirmed', 'failed')),
    provider_method          TEXT NOT NULL DEFAULT '',
    reconciliation_required  INTEGER NOT NULL DEFAULT 0 CHECK (reconciliation_required IN (0, 1)),
    error_code               TEXT NOT NULL DEFAULT '',
    created_at               TEXT NOT NULL,
    updated_at               TEXT NOT NULL
);

CREATE UNIQUE INDEX message_imports_account_idempotency_idx
    ON message_imports (account_id, idempotency_key);

CREATE INDEX message_imports_account_status_idx
    ON message_imports (account_id, status, updated_at DESC, import_id DESC);

-- +goose Down
DROP TABLE IF EXISTS message_imports;
