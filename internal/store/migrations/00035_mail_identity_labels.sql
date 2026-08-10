-- +goose Up
ALTER TABLE mail_identities ADD COLUMN label TEXT NOT NULL DEFAULT '';

-- Earlier builds accepted hide_my_email as a generic alternate identity before
-- its provider compatibility boundary existed. An unverified/rejected HME must
-- not remain the implicit Send From identity after this migration.
UPDATE mail_identities
SET is_default_send = 0
WHERE kind = 'hide_my_email'
  AND verification <> 'verified'
  AND is_default_send = 1;

UPDATE mail_identities
SET is_default_send = 1
WHERE kind = 'primary'
  AND account_id IN (
      SELECT accounts.account_id
      FROM accounts
      WHERE NOT EXISTS (
          SELECT 1
          FROM mail_identities AS candidate
          WHERE candidate.account_id = accounts.account_id
            AND candidate.enabled = 1
            AND candidate.is_default_send = 1
      )
  );

-- +goose Down
-- SQLite cannot drop this column safely on every supported version without a
-- table rebuild. Migrations in this project are forward-only in production.
