-- +goose Up
ALTER TABLE outbound_attempts
    ADD COLUMN sender_identity_id TEXT NOT NULL DEFAULT '';

ALTER TABLE mail_drafts
    ADD COLUMN sender_identity_id TEXT NOT NULL DEFAULT '';

UPDATE outbound_attempts
SET sender_identity_id = COALESCE((
    SELECT identity_id
    FROM mail_identities
    WHERE mail_identities.account_id = outbound_attempts.account_id
      AND mail_identities.kind = 'primary'
    LIMIT 1
), '');

UPDATE mail_drafts
SET sender_identity_id = COALESCE((
    SELECT i.identity_id
    FROM messages AS m
    JOIN mail_identities AS i
      ON i.account_id = m.account_id
     AND lower(i.email_address) = lower(m.from_email)
    WHERE m.message_id = mail_drafts.message_id
      AND m.account_id = mail_drafts.account_id
    ORDER BY CASE WHEN i.kind = 'primary' THEN 0 ELSE 1 END, i.identity_id
    LIMIT 1
), '');

CREATE INDEX outbound_attempts_sender_identity_idx
    ON outbound_attempts (account_id, sender_identity_id, created_at DESC, attempt_id DESC);
CREATE INDEX mail_drafts_sender_identity_idx
    ON mail_drafts (account_id, sender_identity_id, updated_at DESC, draft_id DESC);

-- +goose Down
DROP INDEX IF EXISTS mail_drafts_sender_identity_idx;
DROP INDEX IF EXISTS outbound_attempts_sender_identity_idx;
ALTER TABLE mail_drafts DROP COLUMN sender_identity_id;
ALTER TABLE outbound_attempts DROP COLUMN sender_identity_id;
