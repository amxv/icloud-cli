-- +goose Up
CREATE TABLE message_content (
    message_id        TEXT PRIMARY KEY REFERENCES messages(message_id) ON DELETE CASCADE,
    headers_json      TEXT NOT NULL DEFAULT '[]',
    diagnostics_json  TEXT NOT NULL DEFAULT '[]',
    text_paths_json   TEXT NOT NULL DEFAULT '[]',
    html_paths_json   TEXT NOT NULL DEFAULT '[]',
    parsed_complete   INTEGER NOT NULL DEFAULT 1 CHECK (parsed_complete IN (0, 1)),
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

CREATE TABLE message_parts (
    part_id            TEXT PRIMARY KEY CHECK (part_id LIKE 'prt_%'),
    message_id         TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    ordinal            INTEGER NOT NULL CHECK (ordinal >= 0),
    path               TEXT NOT NULL,
    parent_path        TEXT NOT NULL DEFAULT '',
    media_type         TEXT NOT NULL DEFAULT '',
    charset            TEXT NOT NULL DEFAULT '',
    transfer_encoding  TEXT NOT NULL DEFAULT '',
    disposition        TEXT NOT NULL DEFAULT '',
    filename           TEXT NOT NULL DEFAULT '',
    content_id         TEXT NOT NULL DEFAULT '',
    inline             INTEGER NOT NULL DEFAULT 0 CHECK (inline IN (0, 1)),
    attachment         INTEGER NOT NULL DEFAULT 0 CHECK (attachment IN (0, 1)),
    embedded_message   INTEGER NOT NULL DEFAULT 0 CHECK (embedded_message IN (0, 1)),
    decoded_size       INTEGER NOT NULL DEFAULT 0 CHECK (decoded_size >= 0),
    diagnostics_json   TEXT NOT NULL DEFAULT '[]',
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    UNIQUE (message_id, path),
    UNIQUE (message_id, ordinal)
);

CREATE INDEX message_parts_message_order_idx
    ON message_parts (message_id, ordinal);
CREATE INDEX message_parts_attachment_idx
    ON message_parts (message_id, attachment DESC, path);

-- +goose Down
DROP TABLE IF EXISTS message_parts;
DROP TABLE IF EXISTS message_content;
