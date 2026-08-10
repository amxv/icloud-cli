-- +goose Up
CREATE TABLE mail_selections (
    selection_id       TEXT PRIMARY KEY CHECK (selection_id LIKE 'sel_%'),
    account_id         TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    source_kind        TEXT NOT NULL CHECK (source_kind IN ('explicit', 'search')),
    source_description TEXT NOT NULL,
    source_query_hash  TEXT NOT NULL CHECK (length(source_query_hash) = 64),
    scope_folder_id    TEXT REFERENCES folders(folder_id) ON DELETE RESTRICT,
    request_hash       TEXT NOT NULL CHECK (length(request_hash) = 64),
    item_count         INTEGER NOT NULL CHECK (item_count BETWEEN 1 AND 100),
    created_at         TEXT NOT NULL,
    expires_at         TEXT NOT NULL
);

CREATE TABLE mail_selection_items (
    selection_id  TEXT NOT NULL REFERENCES mail_selections(selection_id) ON DELETE CASCADE,
    ordinal       INTEGER NOT NULL CHECK (ordinal >= 0 AND ordinal < 100),
    message_id    TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    location_id   TEXT REFERENCES message_locations(location_id) ON DELETE RESTRICT,
    folder_id     TEXT REFERENCES folders(folder_id) ON DELETE RESTRICT,
    uid_validity  INTEGER NOT NULL DEFAULT 0 CHECK (uid_validity >= 0),
    uid           INTEGER NOT NULL DEFAULT 0 CHECK (uid >= 0),
    mod_sequence  INTEGER NOT NULL DEFAULT 0 CHECK (mod_sequence >= 0),
    PRIMARY KEY (selection_id, ordinal),
    UNIQUE (selection_id, message_id),
    CHECK ((location_id IS NULL) = (folder_id IS NULL)),
    CHECK (location_id IS NULL OR location_id LIKE 'loc_%')
);

CREATE INDEX mail_selections_account_expiry_idx
    ON mail_selections (account_id, expires_at, selection_id);

-- +goose Down
DROP TABLE IF EXISTS mail_selection_items;
DROP TABLE IF EXISTS mail_selections;
