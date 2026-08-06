-- +goose Up
ALTER TABLE message_content
    ADD COLUMN attachments_indexed INTEGER NOT NULL DEFAULT 0
    CHECK (attachments_indexed IN (0, 1));

ALTER TABLE message_parts
    ADD COLUMN media_params_json TEXT NOT NULL DEFAULT '{}';

CREATE TABLE attachments (
    attachment_id    TEXT PRIMARY KEY CHECK (attachment_id LIKE 'att_%'),
    message_id       TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    part_id          TEXT NOT NULL REFERENCES message_parts(part_id) ON DELETE CASCADE,
    ordinal          INTEGER NOT NULL CHECK (ordinal >= 0),
    part_path        TEXT NOT NULL,
    filename         TEXT NOT NULL DEFAULT '',
    media_type       TEXT NOT NULL DEFAULT '',
    media_params_json TEXT NOT NULL DEFAULT '{}',
    disposition      TEXT NOT NULL DEFAULT '',
    content_id       TEXT NOT NULL DEFAULT '',
    inline           INTEGER NOT NULL DEFAULT 0 CHECK (inline IN (0, 1)),
    content_hash     TEXT NOT NULL DEFAULT '',
    storage_key      TEXT NOT NULL DEFAULT '',
    byte_size        INTEGER NOT NULL DEFAULT 0 CHECK (byte_size >= 0),
    availability     TEXT NOT NULL CHECK (availability IN ('available', 'unavailable')),
    error_code       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    UNIQUE (message_id, part_path),
    UNIQUE (message_id, ordinal),
    CHECK (
        availability = 'unavailable'
        OR (
            length(content_hash) = 64
            AND storage_key <> ''
            AND error_code = ''
        )
    )
);

CREATE INDEX attachments_message_order_idx
    ON attachments (message_id, ordinal);
CREATE INDEX attachments_content_hash_idx
    ON attachments (content_hash)
    WHERE availability = 'available';

-- +goose Down
DROP TABLE IF EXISTS attachments;
ALTER TABLE message_parts DROP COLUMN media_params_json;
ALTER TABLE message_content DROP COLUMN attachments_indexed;
