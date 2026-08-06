-- +goose Up
CREATE TABLE raw_blobs (
    content_hash TEXT PRIMARY KEY CHECK (length(content_hash) = 64),
    storage_key  TEXT NOT NULL UNIQUE,
    byte_size    INTEGER NOT NULL CHECK (byte_size >= 0),
    created_at   TEXT NOT NULL
);

CREATE TABLE messages (
    message_id        TEXT PRIMARY KEY CHECK (message_id LIKE 'msg_%'),
    account_id        TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    rfc_message_id    TEXT NOT NULL DEFAULT '',
    in_reply_to       TEXT NOT NULL DEFAULT '',
    direction         TEXT NOT NULL CHECK (direction IN ('inbound', 'outbound')),
    from_name         TEXT NOT NULL DEFAULT '',
    from_email        TEXT NOT NULL DEFAULT '',
    reply_to_name     TEXT NOT NULL DEFAULT '',
    reply_to_email    TEXT NOT NULL DEFAULT '',
    subject           TEXT NOT NULL DEFAULT '',
    snippet           TEXT NOT NULL DEFAULT '',
    header_date       TEXT,
    internal_date     TEXT,
    sort_at           TEXT,
    date_source       TEXT NOT NULL CHECK (date_source IN ('header', 'internal_date', 'unknown')),
    attachment_count  INTEGER NOT NULL DEFAULT 0 CHECK (attachment_count >= 0),
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

CREATE UNIQUE INDEX messages_account_rfc_id_unique
    ON messages (account_id, lower(rfc_message_id))
    WHERE rfc_message_id <> '';
CREATE INDEX messages_account_sort_idx
    ON messages (account_id, sort_at DESC, message_id DESC);
CREATE INDEX messages_account_sender_idx
    ON messages (account_id, lower(from_email), sort_at DESC);

CREATE TABLE message_recipients (
    message_id    TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('to', 'cc', 'bcc')),
    ordinal       INTEGER NOT NULL CHECK (ordinal >= 0),
    display_name  TEXT NOT NULL DEFAULT '',
    email_address TEXT NOT NULL,
    PRIMARY KEY (message_id, kind, ordinal)
);
CREATE INDEX message_recipients_email_idx
    ON message_recipients (lower(email_address), message_id);

CREATE TABLE message_references (
    message_id TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    ordinal    INTEGER NOT NULL CHECK (ordinal >= 0),
    reference  TEXT NOT NULL,
    PRIMARY KEY (message_id, ordinal)
);
CREATE INDEX message_references_reference_idx
    ON message_references (lower(reference), message_id);

CREATE TABLE message_locations (
    location_id      TEXT PRIMARY KEY CHECK (location_id LIKE 'loc_%'),
    message_id       TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    account_id       TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    folder_id        TEXT NOT NULL REFERENCES folders(folder_id) ON DELETE CASCADE,
    uid_validity     INTEGER NOT NULL CHECK (uid_validity > 0),
    uid              INTEGER NOT NULL CHECK (uid > 0),
    flags_json       TEXT NOT NULL DEFAULT '[]',
    internal_date    TEXT,
    rfc822_size      INTEGER NOT NULL DEFAULT 0 CHECK (rfc822_size >= 0),
    raw_content_hash TEXT NOT NULL REFERENCES raw_blobs(content_hash),
    raw_storage_key  TEXT NOT NULL,
    raw_byte_size    INTEGER NOT NULL CHECK (raw_byte_size >= 0),
    present          INTEGER NOT NULL DEFAULT 1 CHECK (present IN (0, 1)),
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    UNIQUE (account_id, folder_id, uid_validity, uid)
);
CREATE INDEX message_locations_message_idx
    ON message_locations (message_id, present DESC, folder_id, uid);
CREATE INDEX message_locations_folder_uid_idx
    ON message_locations (account_id, folder_id, uid_validity, uid DESC);

CREATE TABLE sync_checkpoints (
    checkpoint_id      TEXT PRIMARY KEY CHECK (checkpoint_id LIKE 'chk_%'),
    account_id         TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    folder_id          TEXT NOT NULL REFERENCES folders(folder_id) ON DELETE CASCADE,
    uid_validity       INTEGER NOT NULL DEFAULT 0 CHECK (uid_validity >= 0),
    last_committed_uid INTEGER NOT NULL DEFAULT 0 CHECK (last_committed_uid >= 0),
    last_scanned_uid   INTEGER NOT NULL DEFAULT 0 CHECK (last_scanned_uid >= 0),
    bootstrap_complete INTEGER NOT NULL DEFAULT 0 CHECK (bootstrap_complete IN (0, 1)),
    state              TEXT NOT NULL DEFAULT 'idle' CHECK (state IN ('idle', 'failed', 'repair_required')),
    last_error_code    TEXT NOT NULL DEFAULT '',
    more_available     INTEGER NOT NULL DEFAULT 0 CHECK (more_available IN (0, 1)),
    last_attempt_at    TEXT,
    last_success_at    TEXT,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    UNIQUE (account_id, folder_id),
    CHECK (last_scanned_uid >= last_committed_uid)
);
CREATE INDEX sync_checkpoints_account_state_idx
    ON sync_checkpoints (account_id, state, folder_id);

-- +goose Down
DROP TABLE IF EXISTS sync_checkpoints;
DROP TABLE IF EXISTS message_locations;
DROP TABLE IF EXISTS message_references;
DROP TABLE IF EXISTS message_recipients;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS raw_blobs;
