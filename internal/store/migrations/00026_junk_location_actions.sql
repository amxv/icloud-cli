-- +goose Up
ALTER TABLE location_mutations RENAME TO location_mutations_legacy;

CREATE TABLE location_mutations (
    mutation_id              TEXT PRIMARY KEY CHECK (mutation_id LIKE 'mut_%'),
    account_id               TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    message_id               TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    action                   TEXT NOT NULL CHECK (action IN ('move', 'copy', 'archive', 'unarchive', 'trash', 'restore', 'junk', 'not_junk', 'delete')),
    source_location_id       TEXT NOT NULL REFERENCES message_locations(location_id),
    source_folder_id         TEXT NOT NULL REFERENCES folders(folder_id),
    destination_folder_id    TEXT REFERENCES folders(folder_id),
    proposed_location_id     TEXT NOT NULL DEFAULT '',
    destination_location_id  TEXT REFERENCES message_locations(location_id),
    request_hash             TEXT NOT NULL CHECK (length(request_hash) = 64),
    idempotency_key          TEXT NOT NULL DEFAULT '',
    status                   TEXT NOT NULL CHECK (status IN ('pending', 'confirmed', 'unresolved', 'failed')),
    provider_method          TEXT NOT NULL DEFAULT '',
    source_present           INTEGER NOT NULL DEFAULT 1 CHECK (source_present IN (0, 1)),
    destination_uid_validity INTEGER NOT NULL DEFAULT 0 CHECK (destination_uid_validity >= 0),
    destination_uid          INTEGER NOT NULL DEFAULT 0 CHECK (destination_uid >= 0),
    mapping_known            INTEGER NOT NULL DEFAULT 0 CHECK (mapping_known IN (0, 1)),
    reconciliation_required  INTEGER NOT NULL DEFAULT 0 CHECK (reconciliation_required IN (0, 1)),
    error_code               TEXT NOT NULL DEFAULT '',
    created_at               TEXT NOT NULL,
    updated_at               TEXT NOT NULL,
    CHECK ((destination_folder_id IS NULL) = (action = 'delete')),
    CHECK (proposed_location_id = '' OR proposed_location_id LIKE 'loc_%')
);

INSERT INTO location_mutations (
    mutation_id, account_id, message_id, action, source_location_id,
    source_folder_id, destination_folder_id, proposed_location_id,
    destination_location_id, request_hash, idempotency_key, status,
    provider_method, source_present, destination_uid_validity,
    destination_uid, mapping_known, reconciliation_required, error_code,
    created_at, updated_at
)
SELECT
    mutation_id, account_id, message_id, action, source_location_id,
    source_folder_id, destination_folder_id, proposed_location_id,
    destination_location_id, request_hash, idempotency_key, status,
    provider_method, source_present, destination_uid_validity,
    destination_uid, mapping_known, reconciliation_required, error_code,
    created_at, updated_at
FROM location_mutations_legacy;

DROP TABLE location_mutations_legacy;

CREATE UNIQUE INDEX location_mutations_idempotency_idx
    ON location_mutations (account_id, idempotency_key)
    WHERE idempotency_key <> '';
CREATE INDEX location_mutations_account_status_idx
    ON location_mutations (account_id, status, updated_at DESC, mutation_id);
CREATE INDEX location_mutations_message_idx
    ON location_mutations (account_id, message_id, created_at DESC, mutation_id);

-- +goose Down
ALTER TABLE location_mutations RENAME TO location_mutations_junk_actions;

CREATE TABLE location_mutations (
    mutation_id              TEXT PRIMARY KEY CHECK (mutation_id LIKE 'mut_%'),
    account_id               TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    message_id               TEXT NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
    action                   TEXT NOT NULL CHECK (action IN ('move', 'copy', 'archive', 'unarchive', 'trash', 'restore', 'delete')),
    source_location_id       TEXT NOT NULL REFERENCES message_locations(location_id),
    source_folder_id         TEXT NOT NULL REFERENCES folders(folder_id),
    destination_folder_id    TEXT REFERENCES folders(folder_id),
    proposed_location_id     TEXT NOT NULL DEFAULT '',
    destination_location_id  TEXT REFERENCES message_locations(location_id),
    request_hash             TEXT NOT NULL CHECK (length(request_hash) = 64),
    idempotency_key          TEXT NOT NULL DEFAULT '',
    status                   TEXT NOT NULL CHECK (status IN ('pending', 'confirmed', 'unresolved', 'failed')),
    provider_method          TEXT NOT NULL DEFAULT '',
    source_present           INTEGER NOT NULL DEFAULT 1 CHECK (source_present IN (0, 1)),
    destination_uid_validity INTEGER NOT NULL DEFAULT 0 CHECK (destination_uid_validity >= 0),
    destination_uid          INTEGER NOT NULL DEFAULT 0 CHECK (destination_uid >= 0),
    mapping_known            INTEGER NOT NULL DEFAULT 0 CHECK (mapping_known IN (0, 1)),
    reconciliation_required  INTEGER NOT NULL DEFAULT 0 CHECK (reconciliation_required IN (0, 1)),
    error_code               TEXT NOT NULL DEFAULT '',
    created_at               TEXT NOT NULL,
    updated_at               TEXT NOT NULL,
    CHECK ((destination_folder_id IS NULL) = (action = 'delete')),
    CHECK (proposed_location_id = '' OR proposed_location_id LIKE 'loc_%')
);

INSERT INTO location_mutations
SELECT * FROM location_mutations_junk_actions
WHERE action NOT IN ('junk', 'not_junk');

DROP TABLE location_mutations_junk_actions;

CREATE UNIQUE INDEX location_mutations_idempotency_idx
    ON location_mutations (account_id, idempotency_key)
    WHERE idempotency_key <> '';
CREATE INDEX location_mutations_account_status_idx
    ON location_mutations (account_id, status, updated_at DESC, mutation_id);
CREATE INDEX location_mutations_message_idx
    ON location_mutations (account_id, message_id, created_at DESC, mutation_id);
