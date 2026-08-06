-- +goose Up
ALTER TABLE message_content ADD COLUMN source_location_id TEXT NOT NULL DEFAULT '';

UPDATE message_content
SET source_location_id = COALESCE((
    SELECT location_id
    FROM message_locations
    WHERE message_locations.message_id = message_content.message_id
    ORDER BY present DESC, folder_id, uid
    LIMIT 1
), '');

CREATE TABLE message_identity_evidence (
    evidence_id        TEXT PRIMARY KEY CHECK (evidence_id LIKE 'evd_%'),
    account_id         TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    message_id         TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    kind               TEXT NOT NULL CHECK (kind IN (
        'rfc_message_id', 'generated_message_id', 'provider_location', 'content_fingerprint'
    )),
    value              TEXT NOT NULL,
    strength           TEXT NOT NULL CHECK (strength IN ('strong', 'guarded')),
    source_location_id TEXT NOT NULL DEFAULT '',
    first_seen_at      TEXT NOT NULL,
    last_seen_at       TEXT NOT NULL,
    UNIQUE (account_id, message_id, kind, value, source_location_id)
);

CREATE INDEX message_identity_evidence_lookup_idx
    ON message_identity_evidence (account_id, kind, value, message_id);
CREATE UNIQUE INDEX message_identity_generated_unique
    ON message_identity_evidence (account_id, value)
    WHERE kind = 'generated_message_id';

CREATE TABLE message_dedupe_conflicts (
    conflict_id         TEXT PRIMARY KEY CHECK (conflict_id LIKE 'dcf_%'),
    account_id          TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    evidence_kind       TEXT NOT NULL CHECK (evidence_kind IN (
        'rfc_message_id', 'generated_message_id', 'provider_location', 'content_fingerprint'
    )),
    evidence_value      TEXT NOT NULL,
    existing_message_id TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    observed_message_id TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    existing_location_id TEXT NOT NULL DEFAULT '',
    observed_location_id TEXT NOT NULL DEFAULT '',
    reason              TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    created_at          TEXT NOT NULL,
    UNIQUE (
        account_id, evidence_kind, evidence_value,
        existing_message_id, observed_message_id,
        existing_location_id, observed_location_id, reason
    )
);

CREATE INDEX message_dedupe_conflicts_message_idx
    ON message_dedupe_conflicts (
        account_id, existing_message_id, observed_message_id, status, created_at
    );

-- +goose Down
DROP TABLE IF EXISTS message_dedupe_conflicts;
DROP TABLE IF EXISTS message_identity_evidence;
ALTER TABLE message_content DROP COLUMN source_location_id;
