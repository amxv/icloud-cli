-- +goose Up
ALTER TABLE accounts ADD COLUMN imap_capabilities_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE accounts ADD COLUMN smtp_capabilities_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE accounts ADD COLUMN last_tested_at TEXT;

-- +goose Down
ALTER TABLE accounts DROP COLUMN last_tested_at;
ALTER TABLE accounts DROP COLUMN smtp_capabilities_json;
ALTER TABLE accounts DROP COLUMN imap_capabilities_json;
