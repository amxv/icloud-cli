-- +goose Up
CREATE TABLE mail_direction_reclassifications (
    account_id            TEXT PRIMARY KEY REFERENCES accounts(account_id) ON DELETE CASCADE,
    after_message_id      TEXT NOT NULL DEFAULT '',
    pending               INTEGER NOT NULL CHECK (pending IN (0, 1)),
    requested_at          TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    last_completed_at     TEXT
);

CREATE INDEX messages_identity_direction_page_idx
    ON messages (account_id, direction, message_id);

-- Existing schema-20/21 installations may already contain alternate enabled
-- identities. Queue one bounded repair frontier per affected account without
-- scanning messages during migration/open.
INSERT INTO mail_direction_reclassifications (
    account_id, after_message_id, pending, requested_at, updated_at, last_completed_at
)
SELECT DISTINCT i.account_id, '', 1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), NULL
FROM mail_identities AS i
WHERE i.enabled = 1 AND i.kind <> 'primary';

-- +goose Down
DROP INDEX IF EXISTS messages_identity_direction_page_idx;
DROP TABLE IF EXISTS mail_direction_reclassifications;
