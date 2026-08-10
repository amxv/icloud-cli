-- +goose Up
CREATE TABLE message_rewrites (
    rewrite_id              TEXT PRIMARY KEY CHECK (rewrite_id LIKE 'rew_%'),
    account_id              TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    message_id              TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    source_location_id      TEXT NOT NULL REFERENCES message_locations(location_id) ON DELETE RESTRICT,
    source_folder_id        TEXT NOT NULL REFERENCES folders(folder_id) ON DELETE RESTRICT,
    source_uid_validity     INTEGER NOT NULL CHECK (source_uid_validity > 0),
    source_uid              INTEGER NOT NULL CHECK (source_uid > 0),
    source_mod_sequence     INTEGER NOT NULL DEFAULT 0 CHECK (source_mod_sequence >= 0),
    old_raw_content_hash    TEXT NOT NULL CHECK (length(old_raw_content_hash) = 64),
    old_raw_storage_key     TEXT NOT NULL,
    old_raw_byte_size       INTEGER NOT NULL CHECK (old_raw_byte_size >= 0),
    new_raw_content_hash    TEXT NOT NULL CHECK (length(new_raw_content_hash) = 64),
    new_raw_storage_key     TEXT NOT NULL,
    new_raw_byte_size       INTEGER NOT NULL CHECK (new_raw_byte_size >= 0),
    before_attachment_count INTEGER NOT NULL CHECK (before_attachment_count >= 0),
    after_attachment_count  INTEGER NOT NULL CHECK (after_attachment_count >= 0),
    removed_attachments_json TEXT NOT NULL,
    proposed_location_id    TEXT NOT NULL CHECK (proposed_location_id LIKE 'loc_%'),
    new_location_id         TEXT REFERENCES message_locations(location_id) ON DELETE RESTRICT,
    new_uid_validity        INTEGER NOT NULL DEFAULT 0 CHECK (new_uid_validity >= 0),
    new_uid                 INTEGER NOT NULL DEFAULT 0 CHECK (new_uid >= 0),
    new_mod_sequence        INTEGER NOT NULL DEFAULT 0 CHECK (new_mod_sequence >= 0),
    request_hash            TEXT NOT NULL CHECK (length(request_hash) = 64),
    idempotency_key         TEXT NOT NULL DEFAULT '',
    status                  TEXT NOT NULL CHECK (status IN (
        'pending', 'append_unresolved', 'appended', 'delete_unresolved',
        'commit_pending', 'confirmed', 'failed'
    )),
    provider_method         TEXT NOT NULL DEFAULT '',
    source_present          INTEGER NOT NULL DEFAULT 1 CHECK (source_present IN (0, 1)),
    reconciliation_required INTEGER NOT NULL DEFAULT 0 CHECK (reconciliation_required IN (0, 1)),
    error_code              TEXT NOT NULL DEFAULT '',
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL
);

CREATE UNIQUE INDEX message_rewrites_account_idempotency_idx
    ON message_rewrites (account_id, idempotency_key)
    WHERE idempotency_key <> '';

CREATE INDEX message_rewrites_account_status_idx
    ON message_rewrites (account_id, status, updated_at DESC, rewrite_id DESC);

CREATE INDEX message_rewrites_message_status_idx
    ON message_rewrites (message_id, status, updated_at DESC, rewrite_id DESC);

-- +goose Down
DROP TABLE IF EXISTS message_rewrites;
