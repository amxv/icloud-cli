-- +goose Up
CREATE TABLE mail_purges (
    purge_id       TEXT PRIMARY KEY CHECK (purge_id LIKE 'pur_%'),
    account_id     TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    folder_id      TEXT NOT NULL REFERENCES folders(folder_id) ON DELETE RESTRICT,
    scope_kind     TEXT NOT NULL CHECK (scope_kind IN ('trash', 'junk', 'folder')),
    request_hash   TEXT NOT NULL CHECK (length(request_hash) = 64),
    item_count     INTEGER NOT NULL CHECK (item_count BETWEEN 1 AND 100),
    more           INTEGER NOT NULL CHECK (more IN (0, 1)),
    blocked_unresolved INTEGER NOT NULL DEFAULT 0 CHECK (blocked_unresolved >= 0),
    created_at     TEXT NOT NULL,
    expires_at     TEXT NOT NULL
);

CREATE TABLE mail_purge_items (
    purge_id       TEXT NOT NULL REFERENCES mail_purges(purge_id) ON DELETE CASCADE,
    ordinal        INTEGER NOT NULL CHECK (ordinal >= 0 AND ordinal < 100),
    message_id     TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    location_id    TEXT NOT NULL REFERENCES message_locations(location_id) ON DELETE RESTRICT,
    uid_validity   INTEGER NOT NULL CHECK (uid_validity > 0),
    uid             INTEGER NOT NULL CHECK (uid > 0),
    mod_sequence   INTEGER NOT NULL DEFAULT 0 CHECK (mod_sequence >= 0),
    PRIMARY KEY (purge_id, ordinal),
    UNIQUE (purge_id, location_id)
);

CREATE INDEX mail_purges_account_expiry_idx
    ON mail_purges (account_id, expires_at, purge_id);

-- +goose Down
DROP TABLE IF EXISTS mail_purge_items;
DROP TABLE IF EXISTS mail_purges;
