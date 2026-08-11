-- +goose Up
ALTER TABLE mail_drafts
    ADD COLUMN smime_mode TEXT NOT NULL DEFAULT '' CHECK (smime_mode IN ('', 'sign', 'encrypt', 'sign-encrypt'));

-- +goose Down
ALTER TABLE mail_drafts DROP COLUMN smime_mode;
