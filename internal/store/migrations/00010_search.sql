-- +goose Up
CREATE TABLE message_search_documents (
    rowid             INTEGER PRIMARY KEY,
    message_id        TEXT NOT NULL UNIQUE REFERENCES messages(message_id) ON DELETE CASCADE,
    account_id        TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    subject           TEXT NOT NULL DEFAULT '',
    sender            TEXT NOT NULL DEFAULT '',
    participants      TEXT NOT NULL DEFAULT '',
    body              TEXT NOT NULL DEFAULT '',
    attachment_names  TEXT NOT NULL DEFAULT '',
    attachment_types  TEXT NOT NULL DEFAULT '',
    rfc_message_id    TEXT NOT NULL DEFAULT '',
    body_state        TEXT NOT NULL DEFAULT 'pending'
        CHECK (body_state IN ('pending', 'complete', 'error')),
    body_truncated    INTEGER NOT NULL DEFAULT 0 CHECK (body_truncated IN (0, 1)),
    body_error_code   TEXT NOT NULL DEFAULT '',
    indexed_at        TEXT NOT NULL
);

CREATE INDEX message_search_documents_account_message_idx
    ON message_search_documents (account_id, message_id);
CREATE INDEX message_search_documents_account_state_idx
    ON message_search_documents (account_id, body_state, message_id);

CREATE VIRTUAL TABLE message_search_fts USING fts5(
    subject,
    sender,
    participants,
    body,
    attachment_names,
    attachment_types,
    rfc_message_id,
    content = 'message_search_documents',
    content_rowid = 'rowid',
    tokenize = 'unicode61 remove_diacritics 2'
);

-- +goose StatementBegin
CREATE TRIGGER message_search_documents_ai AFTER INSERT ON message_search_documents BEGIN
    INSERT INTO message_search_fts (
        rowid, subject, sender, participants, body,
        attachment_names, attachment_types, rfc_message_id
    ) VALUES (
        new.rowid, new.subject, new.sender, new.participants, new.body,
        new.attachment_names, new.attachment_types, new.rfc_message_id
    );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER message_search_documents_ad AFTER DELETE ON message_search_documents BEGIN
    INSERT INTO message_search_fts (
        message_search_fts, rowid, subject, sender, participants, body,
        attachment_names, attachment_types, rfc_message_id
    ) VALUES (
        'delete', old.rowid, old.subject, old.sender, old.participants, old.body,
        old.attachment_names, old.attachment_types, old.rfc_message_id
    );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER message_search_documents_au AFTER UPDATE ON message_search_documents BEGIN
    INSERT INTO message_search_fts (
        message_search_fts, rowid, subject, sender, participants, body,
        attachment_names, attachment_types, rfc_message_id
    ) VALUES (
        'delete', old.rowid, old.subject, old.sender, old.participants, old.body,
        old.attachment_names, old.attachment_types, old.rfc_message_id
    );
    INSERT INTO message_search_fts (
        rowid, subject, sender, participants, body,
        attachment_names, attachment_types, rfc_message_id
    ) VALUES (
        new.rowid, new.subject, new.sender, new.participants, new.body,
        new.attachment_names, new.attachment_types, new.rfc_message_id
    );
END;
-- +goose StatementEnd

INSERT INTO message_search_documents (
    message_id, account_id, subject, sender, participants, body,
    attachment_names, attachment_types, rfc_message_id,
    body_state, body_truncated, body_error_code, indexed_at
)
SELECT
    m.message_id,
    m.account_id,
    m.subject,
    trim(m.from_name || ' ' || m.from_email || ' ' || m.reply_to_name || ' ' || m.reply_to_email),
    COALESCE((
        SELECT group_concat(trim(r.display_name || ' ' || r.email_address), ' ')
        FROM message_recipients AS r
        WHERE r.message_id = m.message_id AND r.kind IN ('to', 'cc')
    ), ''),
    m.snippet,
    COALESCE((
        SELECT group_concat(a.filename, ' ')
        FROM attachments AS a
        WHERE a.message_id = m.message_id
    ), ''),
    COALESCE((
        SELECT group_concat(a.media_type, ' ')
        FROM attachments AS a
        WHERE a.message_id = m.message_id
    ), ''),
    m.rfc_message_id,
    'pending',
    0,
    '',
    m.updated_at
FROM messages AS m;

CREATE INDEX messages_account_subject_idx
    ON messages (account_id, lower(subject), sort_at DESC, message_id DESC);
CREATE INDEX message_recipients_message_email_idx
    ON message_recipients (message_id, lower(email_address), kind);
CREATE INDEX attachments_message_name_type_idx
    ON attachments (message_id, lower(filename), lower(media_type));
CREATE INDEX message_locations_message_search_state_idx
    ON message_locations (message_id, present, deleted, seen, flagged, folder_id);

-- +goose Down
DROP INDEX IF EXISTS message_locations_message_search_state_idx;
DROP INDEX IF EXISTS attachments_message_name_type_idx;
DROP INDEX IF EXISTS message_recipients_message_email_idx;
DROP INDEX IF EXISTS messages_account_subject_idx;
DROP TRIGGER IF EXISTS message_search_documents_au;
DROP TRIGGER IF EXISTS message_search_documents_ad;
DROP TRIGGER IF EXISTS message_search_documents_ai;
DROP TABLE IF EXISTS message_search_fts;
DROP TABLE IF EXISTS message_search_documents;
