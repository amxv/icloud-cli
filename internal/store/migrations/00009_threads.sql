-- +goose Up
CREATE TABLE threads (
    thread_id            TEXT PRIMARY KEY CHECK (thread_id LIKE 'thr_%'),
    account_id           TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    root_rfc_message_id  TEXT NOT NULL DEFAULT '',
    canonical_subject    TEXT NOT NULL DEFAULT '',
    snippet              TEXT NOT NULL DEFAULT '',
    participants_json    TEXT NOT NULL DEFAULT '[]',
    folder_ids_json      TEXT NOT NULL DEFAULT '[]',
    message_count        INTEGER NOT NULL DEFAULT 0 CHECK (message_count >= 0),
    unread_count         INTEGER NOT NULL DEFAULT 0 CHECK (unread_count >= 0),
    attachment_count     INTEGER NOT NULL DEFAULT 0 CHECK (attachment_count >= 0),
    first_message_at     TEXT,
    last_message_at      TEXT,
    status               TEXT NOT NULL DEFAULT 'active' CHECK (status = 'active'),
    reconstruction       TEXT NOT NULL DEFAULT 'local_rfc_graph' CHECK (reconstruction = 'local_rfc_graph'),
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
);

CREATE INDEX threads_account_latest_idx
    ON threads (account_id, last_message_at DESC, thread_id DESC);

CREATE TABLE thread_messages (
    thread_id  TEXT NOT NULL REFERENCES threads(thread_id) ON DELETE CASCADE,
    message_id TEXT NOT NULL UNIQUE REFERENCES messages(message_id) ON DELETE CASCADE,
    joined_at  TEXT NOT NULL,
    PRIMARY KEY (thread_id, message_id)
);

CREATE INDEX thread_messages_thread_idx
    ON thread_messages (thread_id, message_id);

CREATE TABLE thread_aliases (
    alias_thread_id     TEXT PRIMARY KEY CHECK (alias_thread_id LIKE 'thr_%'),
    account_id          TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    canonical_thread_id TEXT NOT NULL REFERENCES threads(thread_id) ON DELETE CASCADE,
    reason              TEXT NOT NULL DEFAULT 'graph_merge' CHECK (reason IN ('graph_merge', 'manual_repair')),
    created_at          TEXT NOT NULL,
    CHECK (alias_thread_id <> canonical_thread_id)
);

CREATE INDEX thread_aliases_canonical_idx
    ON thread_aliases (account_id, canonical_thread_id, alias_thread_id);

CREATE TABLE thread_edges (
    edge_id              TEXT PRIMARY KEY CHECK (edge_id LIKE 'ted_%'),
    account_id           TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    child_message_id     TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    relation             TEXT NOT NULL CHECK (relation IN ('in_reply_to', 'reference', 'fallback')),
    ordinal              INTEGER NOT NULL DEFAULT 0 CHECK (ordinal >= 0),
    token                TEXT NOT NULL,
    parent_message_id    TEXT REFERENCES messages(message_id) ON DELETE SET NULL,
    status               TEXT NOT NULL CHECK (status IN (
        'resolved', 'unresolved', 'self', 'cycle', 'malformed', 'grouped', 'isolated'
    )),
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    UNIQUE (account_id, child_message_id, relation, ordinal, token)
);

CREATE INDEX thread_edges_token_idx
    ON thread_edges (account_id, token, child_message_id);
CREATE INDEX thread_edges_parent_idx
    ON thread_edges (account_id, parent_message_id, child_message_id);

INSERT INTO threads (
    thread_id, account_id, root_rfc_message_id, canonical_subject, snippet,
    participants_json, folder_ids_json, message_count, unread_count,
    attachment_count, first_message_at, last_message_at, created_at, updated_at
)
SELECT
    'thr_' || substr(message_id, 5), account_id, lower(rfc_message_id), subject, snippet,
    '[]', '[]', 1, 0, attachment_count, sort_at, sort_at, created_at, updated_at
FROM messages;

INSERT INTO thread_messages (thread_id, message_id, joined_at)
SELECT 'thr_' || substr(message_id, 5), message_id, created_at
FROM messages;

-- +goose Down
DROP TABLE IF EXISTS thread_edges;
DROP TABLE IF EXISTS thread_aliases;
DROP TABLE IF EXISTS thread_messages;
DROP TABLE IF EXISTS threads;
