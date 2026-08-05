-- +goose Up
CREATE TABLE folders (
    folder_id          TEXT PRIMARY KEY CHECK (folder_id LIKE 'fld_%'),
    account_id         TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    wire_name          TEXT NOT NULL,
    parent_name        TEXT NOT NULL DEFAULT '',
    parent_wire_name   TEXT NOT NULL DEFAULT '',
    delimiter          TEXT NOT NULL DEFAULT '',
    attributes_json    TEXT NOT NULL DEFAULT '[]',
    kind               TEXT NOT NULL DEFAULT 'other' CHECK (kind IN ('inbox', 'sent', 'drafts', 'archive', 'trash', 'junk', 'all', 'flagged', 'important', 'other')),
    kind_source        TEXT NOT NULL DEFAULT '',
    selectable         INTEGER NOT NULL DEFAULT 1 CHECK (selectable IN (0, 1)),
    subscription       TEXT NOT NULL DEFAULT 'unknown' CHECK (subscription IN ('unknown', 'subscribed', 'unsubscribed')),
    present            INTEGER NOT NULL DEFAULT 1 CHECK (present IN (0, 1)),
    stale              INTEGER NOT NULL DEFAULT 0 CHECK (stale IN (0, 1)),
    sync_enabled       INTEGER NOT NULL DEFAULT 0 CHECK (sync_enabled IN (0, 1)),
    message_count      INTEGER NOT NULL DEFAULT 0 CHECK (message_count >= 0),
    unseen_count       INTEGER NOT NULL DEFAULT 0 CHECK (unseen_count >= 0),
    uid_next           INTEGER NOT NULL DEFAULT 0 CHECK (uid_next >= 0),
    uid_validity       INTEGER NOT NULL DEFAULT 0 CHECK (uid_validity >= 0),
    discovered_at      TEXT NOT NULL,
    status_at          TEXT,
    missing_since      TEXT,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    UNIQUE (account_id, wire_name)
);

CREATE INDEX folders_account_present_idx ON folders (account_id, present, lower(name), folder_id);
CREATE INDEX folders_account_kind_idx ON folders (account_id, kind, folder_id);
CREATE INDEX folders_account_sync_idx ON folders (account_id, sync_enabled, present, folder_id);

-- +goose Down
DROP TABLE IF EXISTS folders;
