-- +goose Up
ALTER TABLE message_locations RENAME TO message_locations_legacy;

CREATE TABLE message_locations (
    location_id       TEXT PRIMARY KEY CHECK (location_id LIKE 'loc_%'),
    message_id        TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    account_id        TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    folder_id         TEXT NOT NULL REFERENCES folders(folder_id) ON DELETE CASCADE,
    uid_validity      INTEGER NOT NULL CHECK (uid_validity > 0),
    uid               INTEGER NOT NULL CHECK (uid > 0),
    seen              INTEGER NOT NULL DEFAULT 0 CHECK (seen IN (0, 1)),
    flagged           INTEGER NOT NULL DEFAULT 0 CHECK (flagged IN (0, 1)),
    answered          INTEGER NOT NULL DEFAULT 0 CHECK (answered IN (0, 1)),
    draft             INTEGER NOT NULL DEFAULT 0 CHECK (draft IN (0, 1)),
    deleted           INTEGER NOT NULL DEFAULT 0 CHECK (deleted IN (0, 1)),
    unknown_flags_json TEXT NOT NULL DEFAULT '[]',
    mod_sequence      TEXT NOT NULL DEFAULT '',
    flags_checked_at  TEXT,
    internal_date     TEXT,
    rfc822_size       INTEGER NOT NULL DEFAULT 0 CHECK (rfc822_size >= 0),
    raw_content_hash  TEXT NOT NULL REFERENCES raw_blobs(content_hash),
    raw_storage_key   TEXT NOT NULL,
    raw_byte_size     INTEGER NOT NULL CHECK (raw_byte_size >= 0),
    present           INTEGER NOT NULL DEFAULT 1 CHECK (present IN (0, 1)),
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    UNIQUE (account_id, folder_id, uid_validity, uid),
    CHECK (mod_sequence = '' OR mod_sequence NOT GLOB '*[^0-9]*')
);

INSERT INTO message_locations (
    location_id, message_id, account_id, folder_id, uid_validity, uid,
    seen, flagged, answered, draft, deleted, unknown_flags_json, mod_sequence,
    flags_checked_at,
    internal_date, rfc822_size, raw_content_hash, raw_storage_key,
    raw_byte_size, present, created_at, updated_at
)
SELECT
    legacy.location_id, legacy.message_id, legacy.account_id, legacy.folder_id,
    legacy.uid_validity, legacy.uid,
    EXISTS (SELECT 1 FROM json_each(legacy.flags_json) WHERE lower(value) = '\seen'),
    EXISTS (SELECT 1 FROM json_each(legacy.flags_json) WHERE lower(value) = '\flagged'),
    EXISTS (SELECT 1 FROM json_each(legacy.flags_json) WHERE lower(value) = '\answered'),
    EXISTS (SELECT 1 FROM json_each(legacy.flags_json) WHERE lower(value) = '\draft'),
    EXISTS (SELECT 1 FROM json_each(legacy.flags_json) WHERE lower(value) = '\deleted'),
    COALESCE((
        SELECT json_group_array(flag)
        FROM (
            SELECT value AS flag
            FROM json_each(legacy.flags_json)
            WHERE lower(value) NOT IN ('\seen', '\flagged', '\answered', '\draft', '\deleted')
            ORDER BY lower(value), value
        )
    ), '[]'),
    '', NULL, legacy.internal_date, legacy.rfc822_size, legacy.raw_content_hash,
    legacy.raw_storage_key, legacy.raw_byte_size, legacy.present,
    legacy.created_at, legacy.updated_at
FROM message_locations_legacy AS legacy;

DROP TABLE message_locations_legacy;

CREATE INDEX message_locations_message_idx
    ON message_locations (message_id, present DESC, folder_id, uid);
CREATE INDEX message_locations_folder_uid_idx
    ON message_locations (account_id, folder_id, uid_validity, uid DESC);
CREATE INDEX message_locations_folder_state_idx
    ON message_locations (account_id, folder_id, present, flags_checked_at, uid DESC);

-- +goose Down
ALTER TABLE message_locations RENAME TO message_locations_structured;

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

INSERT INTO message_locations (
    location_id, message_id, account_id, folder_id, uid_validity, uid,
    flags_json, internal_date, rfc822_size, raw_content_hash, raw_storage_key,
    raw_byte_size, present, created_at, updated_at
)
SELECT
    structured.location_id, structured.message_id, structured.account_id,
    structured.folder_id, structured.uid_validity, structured.uid,
    COALESCE((
        SELECT json_group_array(flag)
        FROM (
            SELECT '\Seen' AS flag WHERE structured.seen = 1
            UNION ALL SELECT '\Flagged' WHERE structured.flagged = 1
            UNION ALL SELECT '\Answered' WHERE structured.answered = 1
            UNION ALL SELECT '\Draft' WHERE structured.draft = 1
            UNION ALL SELECT '\Deleted' WHERE structured.deleted = 1
            UNION ALL SELECT value FROM json_each(structured.unknown_flags_json)
        )
    ), '[]'),
    structured.internal_date, structured.rfc822_size, structured.raw_content_hash,
    structured.raw_storage_key, structured.raw_byte_size, structured.present,
    structured.created_at, structured.updated_at
FROM message_locations_structured AS structured;

DROP TABLE message_locations_structured;

CREATE INDEX message_locations_message_idx
    ON message_locations (message_id, present DESC, folder_id, uid);
CREATE INDEX message_locations_folder_uid_idx
    ON message_locations (account_id, folder_id, uid_validity, uid DESC);
