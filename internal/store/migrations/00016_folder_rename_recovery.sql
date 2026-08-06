-- +goose Up
CREATE TABLE folder_rename_recoveries (
    recovery_id TEXT PRIMARY KEY CHECK (recovery_id LIKE 'frr_%'),
    account_id TEXT NOT NULL REFERENCES accounts(account_id) ON DELETE CASCADE,
    folder_id TEXT NOT NULL REFERENCES folders(folder_id) ON DELETE CASCADE,
    old_name TEXT NOT NULL,
    new_name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'resolved', 'not_applied')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX folder_rename_recoveries_one_pending_idx
ON folder_rename_recoveries(folder_id)
WHERE status = 'pending';

CREATE INDEX folder_rename_recoveries_account_status_idx
ON folder_rename_recoveries(account_id, status, created_at, recovery_id);

-- +goose Down
DROP INDEX IF EXISTS folder_rename_recoveries_account_status_idx;
DROP INDEX IF EXISTS folder_rename_recoveries_one_pending_idx;
DROP TABLE IF EXISTS folder_rename_recoveries;
