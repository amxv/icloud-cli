-- +goose Up
CREATE TABLE folder_role_bindings (
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('sent', 'drafts', 'archive', 'trash', 'junk')),
    folder_id  TEXT NOT NULL REFERENCES folders(folder_id) ON DELETE RESTRICT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (account_id, role),
    UNIQUE (account_id, folder_id)
);

CREATE INDEX folder_role_bindings_folder_idx
    ON folder_role_bindings (account_id, folder_id);

-- +goose Down
DROP TABLE IF EXISTS folder_role_bindings;
