package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type MessageRewriteRepository struct {
	db *sql.DB
}

const messageRewriteColumns = `
SELECT rewrite_id, account_id, message_id, source_location_id, source_folder_id,
       source_uid_validity, source_uid, source_mod_sequence,
       old_raw_content_hash, old_raw_storage_key, old_raw_byte_size,
       new_raw_content_hash, new_raw_storage_key, new_raw_byte_size,
       before_attachment_count, after_attachment_count, removed_attachments_json, proposed_location_id,
       COALESCE(new_location_id, ''), new_uid_validity, new_uid, new_mod_sequence,
       request_hash, idempotency_key, status, provider_method, source_present,
       reconciliation_required, error_code, created_at, updated_at
FROM message_rewrites`

func (repository *MessageRewriteRepository) Begin(ctx context.Context, input maildomain.BeginMessageRewrite) (maildomain.MessageRewrite, bool, error) {
	input.RewriteID = strings.TrimSpace(input.RewriteID)
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.MessageID = strings.TrimSpace(input.MessageID)
	input.SourceLocationID = strings.TrimSpace(input.SourceLocationID)
	input.SourceFolderID = strings.TrimSpace(input.SourceFolderID)
	input.OldRawContentHash = strings.ToLower(strings.TrimSpace(input.OldRawContentHash))
	input.OldRawStorageKey = strings.TrimSpace(input.OldRawStorageKey)
	input.NewRawContentHash = strings.ToLower(strings.TrimSpace(input.NewRawContentHash))
	input.NewRawStorageKey = strings.TrimSpace(input.NewRawStorageKey)
	input.ProposedLocationID = strings.TrimSpace(input.ProposedLocationID)
	input.RequestHash = strings.ToLower(strings.TrimSpace(input.RequestHash))
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if !strings.HasPrefix(input.RewriteID, "rew_") || !strings.HasPrefix(input.AccountID, "acct_") ||
		!strings.HasPrefix(input.MessageID, "msg_") || !strings.HasPrefix(input.SourceLocationID, "loc_") ||
		!strings.HasPrefix(input.SourceFolderID, "fld_") || !strings.HasPrefix(input.ProposedLocationID, "loc_") ||
		input.SourceUIDValidity == 0 || input.SourceUID == 0 || len(input.OldRawContentHash) != 64 ||
		input.OldRawStorageKey == "" || input.OldRawByteSize < 0 || len(input.NewRawContentHash) != 64 ||
		input.NewRawStorageKey == "" || input.NewRawByteSize < 0 || len(input.RequestHash) != 64 ||
		input.BeforeAttachmentCount < 1 || input.AfterAttachmentCount < 0 || input.AfterAttachmentCount >= input.BeforeAttachmentCount ||
		len(input.RemovedAttachments) == 0 || len(input.IdempotencyKey) > 256 {
		return maildomain.MessageRewrite{}, false, maildomain.ErrInvalidMessageRewrite
	}
	removedJSON, err := json.Marshal(input.RemovedAttachments)
	if err != nil {
		return maildomain.MessageRewrite{}, false, fmt.Errorf("encode rewrite attachments: %w", err)
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.MessageRewrite{}, false, fmt.Errorf("begin message rewrite: %w", err)
	}
	defer tx.Rollback()
	if input.IdempotencyKey != "" {
		existing, scanErr := scanMessageRewrite(tx.QueryRowContext(ctx, messageRewriteColumns+` WHERE account_id = ? AND idempotency_key = ?`, input.AccountID, input.IdempotencyKey))
		switch {
		case scanErr == nil:
			if existing.RequestHash != input.RequestHash {
				return maildomain.MessageRewrite{}, false, maildomain.ErrMessageRewriteConflict
			}
			return existing, true, nil
		case !errors.Is(scanErr, maildomain.ErrMessageRewriteNotFound):
			return maildomain.MessageRewrite{}, false, scanErr
		}
	}
	var activeID string
	activeErr := tx.QueryRowContext(ctx, `
SELECT rewrite_id FROM message_rewrites
WHERE message_id = ? AND source_location_id = ? AND status NOT IN ('confirmed', 'failed')
ORDER BY updated_at DESC LIMIT 1`, input.MessageID, input.SourceLocationID).Scan(&activeID)
	if activeErr == nil {
		return maildomain.MessageRewrite{}, false, maildomain.ErrMessageRewriteConflict
	}
	if activeErr != nil && !errors.Is(activeErr, sql.ErrNoRows) {
		return maildomain.MessageRewrite{}, false, fmt.Errorf("inspect active message rewrite: %w", activeErr)
	}
	source, err := scanLocation(tx.QueryRowContext(ctx, locationColumns+` WHERE location_id = ?`, input.SourceLocationID))
	if err != nil {
		return maildomain.MessageRewrite{}, false, err
	}
	if source.AccountID != input.AccountID || source.MessageID != input.MessageID || source.FolderID != input.SourceFolderID ||
		source.UIDValidity != input.SourceUIDValidity || source.UID != input.SourceUID || !source.Present || !source.Flags.Eligible() ||
		(source.ModSequence != 0 && input.SourceModSequence != 0 && source.ModSequence != input.SourceModSequence) ||
		!strings.EqualFold(source.RawContentHash, input.OldRawContentHash) || source.RawStorageKey != input.OldRawStorageKey || source.RawByteSize != input.OldRawByteSize {
		return maildomain.MessageRewrite{}, false, maildomain.ErrMessageRewriteConflict
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO raw_blobs (content_hash, storage_key, byte_size, created_at)
VALUES (?, ?, ?, ?) ON CONFLICT(content_hash) DO NOTHING`, input.NewRawContentHash, input.NewRawStorageKey, input.NewRawByteSize, input.Now.UTC().Format(time.RFC3339Nano)); err != nil {
		return maildomain.MessageRewrite{}, false, fmt.Errorf("persist rewrite raw blob metadata: %w", err)
	}
	var storedKey string
	var storedSize int64
	if err := tx.QueryRowContext(ctx, `SELECT storage_key, byte_size FROM raw_blobs WHERE content_hash = ?`, input.NewRawContentHash).Scan(&storedKey, &storedSize); err != nil {
		return maildomain.MessageRewrite{}, false, fmt.Errorf("verify rewrite raw blob metadata: %w", err)
	}
	if storedKey != input.NewRawStorageKey || storedSize != input.NewRawByteSize {
		return maildomain.MessageRewrite{}, false, maildomain.ErrMessageRewriteConflict
	}
	timestamp := input.Now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO message_rewrites (
    rewrite_id, account_id, message_id, source_location_id, source_folder_id,
    source_uid_validity, source_uid, source_mod_sequence,
    old_raw_content_hash, old_raw_storage_key, old_raw_byte_size,
    new_raw_content_hash, new_raw_storage_key, new_raw_byte_size,
    before_attachment_count, after_attachment_count, removed_attachments_json,
    proposed_location_id, request_hash, idempotency_key,
    status, source_present, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 1, ?, ?)`,
		input.RewriteID, input.AccountID, input.MessageID, input.SourceLocationID, input.SourceFolderID,
		input.SourceUIDValidity, input.SourceUID, formatModSequence(input.SourceModSequence),
		input.OldRawContentHash, input.OldRawStorageKey, input.OldRawByteSize,
		input.NewRawContentHash, input.NewRawStorageKey, input.NewRawByteSize,
		input.BeforeAttachmentCount, input.AfterAttachmentCount, string(removedJSON),
		input.ProposedLocationID, input.RequestHash, input.IdempotencyKey, timestamp, timestamp,
	); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return maildomain.MessageRewrite{}, false, maildomain.ErrMessageRewriteConflict
		}
		return maildomain.MessageRewrite{}, false, fmt.Errorf("insert message rewrite: %w", err)
	}
	value, err := scanMessageRewrite(tx.QueryRowContext(ctx, messageRewriteColumns+` WHERE rewrite_id = ?`, input.RewriteID))
	if err != nil {
		return maildomain.MessageRewrite{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.MessageRewrite{}, false, fmt.Errorf("commit message rewrite intent: %w", err)
	}
	return value, false, nil
}

func (repository *MessageRewriteRepository) Rewrite(ctx context.Context, accountID, rewriteID string) (maildomain.MessageRewrite, error) {
	return scanMessageRewrite(repository.db.QueryRowContext(ctx, messageRewriteColumns+` WHERE account_id = ? AND rewrite_id = ?`, strings.TrimSpace(accountID), strings.TrimSpace(rewriteID)))
}

func (repository *MessageRewriteRepository) ByIdempotency(ctx context.Context, accountID, idempotencyKey string) (maildomain.MessageRewrite, error) {
	accountID = strings.TrimSpace(accountID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if accountID == "" || idempotencyKey == "" {
		return maildomain.MessageRewrite{}, maildomain.ErrMessageRewriteNotFound
	}
	return scanMessageRewrite(repository.db.QueryRowContext(ctx, messageRewriteColumns+` WHERE account_id = ? AND idempotency_key = ?`, accountID, idempotencyKey))
}

func (repository *MessageRewriteRepository) ConfirmAppend(ctx context.Context, input maildomain.ConfirmRewriteAppend) (maildomain.MessageRewrite, error) {
	if input.UIDValidity == 0 || input.UID == 0 {
		return maildomain.MessageRewrite{}, maildomain.ErrInvalidMessageRewrite
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("begin rewrite append confirmation: %w", err)
	}
	defer tx.Rollback()
	rewrite, err := scanMessageRewrite(tx.QueryRowContext(ctx, messageRewriteColumns+` WHERE rewrite_id = ?`, strings.TrimSpace(input.RewriteID)))
	if err != nil {
		return maildomain.MessageRewrite{}, err
	}
	if rewrite.Status == maildomain.MessageRewriteConfirmed || rewrite.Status == maildomain.MessageRewriteCommitPending || rewrite.Status == maildomain.MessageRewriteDeleteUnresolved || rewrite.Status == maildomain.MessageRewriteAppended {
		return rewrite, nil
	}
	if rewrite.Status != maildomain.MessageRewritePending && rewrite.Status != maildomain.MessageRewriteAppendUnresolved {
		return maildomain.MessageRewrite{}, maildomain.ErrMessageRewriteConflict
	}
	source, err := scanLocation(tx.QueryRowContext(ctx, locationColumns+` WHERE location_id = ?`, rewrite.SourceLocationID))
	if err != nil {
		return maildomain.MessageRewrite{}, err
	}
	var existingID, existingMessageID, existingRawHash string
	err = tx.QueryRowContext(ctx, `
SELECT location_id, message_id, raw_content_hash FROM message_locations
WHERE account_id = ? AND folder_id = ? AND uid_validity = ? AND uid = ?`, rewrite.AccountID, rewrite.SourceFolderID, input.UIDValidity, input.UID).Scan(&existingID, &existingMessageID, &existingRawHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return maildomain.MessageRewrite{}, fmt.Errorf("resolve rewrite replacement location: %w", err)
	}
	if err == nil && !strings.EqualFold(existingRawHash, rewrite.NewRawContentHash) {
		return maildomain.MessageRewrite{}, maildomain.ErrMessageRewriteConflict
	}
	if err == nil && existingMessageID != rewrite.MessageID {
		if err := adoptRewriteReplacementLocation(ctx, tx, rewrite, existingID, existingMessageID, input.Now); err != nil {
			return maildomain.MessageRewrite{}, err
		}
		existingMessageID = rewrite.MessageID
	}
	locationID := existingID
	if locationID == "" {
		locationID = strings.TrimSpace(input.LocationID)
		if locationID == "" {
			locationID = rewrite.ProposedLocationID
		}
		unknownJSON, encodeErr := json.Marshal(maildomain.ParseLocationFlags(source.Flags.Unknown).Unknown)
		if encodeErr != nil {
			return maildomain.MessageRewrite{}, fmt.Errorf("encode rewrite replacement flags: %w", encodeErr)
		}
		timestamp := input.Now.UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO message_locations (
    location_id, message_id, account_id, folder_id, uid_validity, uid,
    seen, flagged, answered, draft, deleted, unknown_flags_json, mod_sequence,
    flags_checked_at, internal_date, rfc822_size, raw_content_hash, raw_storage_key,
    raw_byte_size, present, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			locationID, rewrite.MessageID, rewrite.AccountID, rewrite.SourceFolderID, input.UIDValidity, input.UID,
			boolInt(source.Flags.Seen), boolInt(source.Flags.Flagged), boolInt(source.Flags.Answered), boolInt(source.Flags.Draft),
			string(unknownJSON), formatModSequence(input.ModSequence), timestamp, nullableTimeValueFromPointer(source.InternalDate),
			rewrite.NewRawByteSize, rewrite.NewRawContentHash, rewrite.NewRawStorageKey, rewrite.NewRawByteSize, timestamp, timestamp,
		); err != nil {
			return maildomain.MessageRewrite{}, fmt.Errorf("persist rewrite replacement location: %w", err)
		}
	}
	timestamp := input.Now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
UPDATE message_rewrites
SET status = 'appended', new_location_id = ?, new_uid_validity = ?, new_uid = ?, new_mod_sequence = ?,
    provider_method = ?, reconciliation_required = 0, error_code = '', updated_at = ?
WHERE rewrite_id = ?`, locationID, input.UIDValidity, input.UID, formatModSequence(input.ModSequence), strings.TrimSpace(input.ProviderMethod), timestamp, rewrite.ID); err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("confirm rewrite append: %w", err)
	}
	value, err := scanMessageRewrite(tx.QueryRowContext(ctx, messageRewriteColumns+` WHERE rewrite_id = ?`, rewrite.ID))
	if err != nil {
		return maildomain.MessageRewrite{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("commit rewrite append confirmation: %w", err)
	}
	return value, nil
}

func adoptRewriteReplacementLocation(ctx context.Context, tx *sql.Tx, rewrite maildomain.MessageRewrite, locationID, observedMessageID string, now time.Time) error {
	locationID = strings.TrimSpace(locationID)
	observedMessageID = strings.TrimSpace(observedMessageID)
	if locationID == "" || observedMessageID == "" || observedMessageID == rewrite.MessageID {
		return maildomain.ErrMessageRewriteConflict
	}

	var (
		accountID   string
		folderID    string
		rawHash     string
		uidValidity uint32
		uid         uint32
		present     int
	)
	if err := tx.QueryRowContext(ctx, `
SELECT account_id, folder_id, uid_validity, uid, raw_content_hash, present
FROM message_locations
WHERE location_id = ? AND message_id = ?`, locationID, observedMessageID).Scan(
		&accountID, &folderID, &uidValidity, &uid, &rawHash, &present,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.ErrMessageRewriteConflict
		}
		return fmt.Errorf("inspect rewrite replacement adoption: %w", err)
	}
	if accountID != rewrite.AccountID || folderID != rewrite.SourceFolderID || uidValidity == 0 || uid == 0 || present != 1 || !strings.EqualFold(rawHash, rewrite.NewRawContentHash) {
		return maildomain.ErrMessageRewriteConflict
	}

	timestamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
UPDATE message_locations
SET message_id = ?, updated_at = ?
WHERE location_id = ? AND message_id = ?`, rewrite.MessageID, timestamp, locationID, observedMessageID); err != nil {
		return fmt.Errorf("adopt rewrite replacement location: %w", err)
	}

	// Provider-location evidence follows the stable logical message. The
	// replacement can have been ingested as a temporary logical message during
	// APPEND reconciliation because its content fingerprint intentionally differs
	// from the source revision.
	if _, err := tx.ExecContext(ctx, `
DELETE FROM message_identity_evidence
WHERE message_id = ? AND source_location_id = ?`, observedMessageID, locationID); err != nil {
		return fmt.Errorf("remove temporary rewrite identity evidence: %w", err)
	}
	if err := persistIdentityEvidence(
		ctx, tx, rewrite.AccountID, rewrite.MessageID,
		maildomain.IdentityProviderLocation,
		maildomain.ProviderLocationIdentity(rewrite.SourceFolderID, uidValidity, uid),
		maildomain.IdentityStrong, locationID, timestamp,
	); err != nil {
		return err
	}

	var remainingLocations int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM message_locations WHERE message_id = ?`, observedMessageID).Scan(&remainingLocations); err != nil {
		return fmt.Errorf("count temporary rewrite locations: %w", err)
	}
	if remainingLocations > 0 {
		return nil
	}

	var threadID string
	threadErr := tx.QueryRowContext(ctx, `
SELECT tm.thread_id
FROM thread_messages AS tm
JOIN threads AS t ON t.thread_id = tm.thread_id
WHERE t.account_id = ? AND tm.message_id = ?`, rewrite.AccountID, observedMessageID).Scan(&threadID)
	if threadErr != nil && !errors.Is(threadErr, sql.ErrNoRows) {
		return fmt.Errorf("resolve temporary rewrite thread before delete: %w", threadErr)
	}
	remainingThreadMessages := []string{}
	if threadErr == nil {
		rows, err := tx.QueryContext(ctx, `
SELECT message_id FROM thread_messages
WHERE thread_id = ? AND message_id <> ?
ORDER BY message_id`, threadID, observedMessageID)
		if err != nil {
			return fmt.Errorf("list temporary rewrite thread members: %w", err)
		}
		for rows.Next() {
			var messageID string
			if err := rows.Scan(&messageID); err != nil {
				rows.Close()
				return fmt.Errorf("read temporary rewrite thread member: %w", err)
			}
			remainingThreadMessages = append(remainingThreadMessages, messageID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("list temporary rewrite thread members: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close temporary rewrite thread members: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE account_id = ? AND message_id = ?`, rewrite.AccountID, observedMessageID); err != nil {
		return fmt.Errorf("delete temporary rewrite message: %w", err)
	}
	if len(remainingThreadMessages) > 0 {
		if err := rebuildCanonicalThreadProjection(ctx, tx, rewrite.AccountID, remainingThreadMessages, now); err != nil {
			return fmt.Errorf("rebuild thread after rewrite adoption: %w", err)
		}
	} else if threadID != "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM threads WHERE account_id = ? AND thread_id = ?`, rewrite.AccountID, threadID); err != nil {
			return fmt.Errorf("delete empty temporary rewrite thread: %w", err)
		}
	}
	return nil
}

func (repository *MessageRewriteRepository) MarkAppendUnresolved(ctx context.Context, rewriteID, errorCode string, now time.Time) (maildomain.MessageRewrite, error) {
	return repository.markStatus(ctx, rewriteID, []maildomain.MessageRewriteStatus{maildomain.MessageRewritePending}, maildomain.MessageRewriteAppendUnresolved, true, errorCode, now)
}

func (repository *MessageRewriteRepository) MarkDeleteUnresolved(ctx context.Context, rewriteID, errorCode string, now time.Time) (maildomain.MessageRewrite, error) {
	return repository.markStatus(ctx, rewriteID, []maildomain.MessageRewriteStatus{maildomain.MessageRewriteAppended}, maildomain.MessageRewriteDeleteUnresolved, true, errorCode, now)
}

func (repository *MessageRewriteRepository) MarkFailed(ctx context.Context, rewriteID, errorCode string, now time.Time) (maildomain.MessageRewrite, error) {
	return repository.markStatus(ctx, rewriteID, []maildomain.MessageRewriteStatus{maildomain.MessageRewritePending, maildomain.MessageRewriteAppendUnresolved}, maildomain.MessageRewriteFailed, false, errorCode, now)
}

func (repository *MessageRewriteRepository) markStatus(ctx context.Context, rewriteID string, from []maildomain.MessageRewriteStatus, to maildomain.MessageRewriteStatus, reconciliation bool, errorCode string, now time.Time) (maildomain.MessageRewrite, error) {
	placeholders := make([]string, len(from))
	args := make([]any, 0, len(from)+5)
	args = append(args, to, boolInt(reconciliation), strings.TrimSpace(errorCode), now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(rewriteID))
	for index, status := range from {
		placeholders[index] = "?"
		args = append(args, status)
	}
	query := `UPDATE message_rewrites SET status = ?, reconciliation_required = ?, error_code = ?, updated_at = ? WHERE rewrite_id = ? AND status IN (` + strings.Join(placeholders, ",") + `)`
	if _, err := repository.db.ExecContext(ctx, query, args...); err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("mark message rewrite %s: %w", to, err)
	}
	return scanMessageRewrite(repository.db.QueryRowContext(ctx, messageRewriteColumns+` WHERE rewrite_id = ?`, strings.TrimSpace(rewriteID)))
}

func (repository *MessageRewriteRepository) ConfirmDelete(ctx context.Context, input maildomain.ConfirmRewriteDelete) (maildomain.MessageRewrite, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("begin rewrite delete confirmation: %w", err)
	}
	defer tx.Rollback()
	rewrite, err := scanMessageRewrite(tx.QueryRowContext(ctx, messageRewriteColumns+` WHERE rewrite_id = ?`, strings.TrimSpace(input.RewriteID)))
	if err != nil {
		return maildomain.MessageRewrite{}, err
	}
	if rewrite.Status == maildomain.MessageRewriteConfirmed || rewrite.Status == maildomain.MessageRewriteCommitPending {
		return rewrite, nil
	}
	if rewrite.Status != maildomain.MessageRewriteAppended && rewrite.Status != maildomain.MessageRewriteDeleteUnresolved {
		return maildomain.MessageRewrite{}, maildomain.ErrMessageRewriteConflict
	}
	timestamp := input.Now.UTC().Format(time.RFC3339Nano)
	if err := updateMutationLocationState(ctx, tx, rewrite.SourceLocationID, input.SourceFlags, input.SourceModSequence, false, timestamp); err != nil {
		return maildomain.MessageRewrite{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE message_rewrites
SET status = 'commit_pending', provider_method = ?, source_present = 0,
    reconciliation_required = 0, error_code = '', updated_at = ?
WHERE rewrite_id = ?`, strings.TrimSpace(input.ProviderMethod), timestamp, rewrite.ID); err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("confirm rewrite source deletion: %w", err)
	}
	value, err := scanMessageRewrite(tx.QueryRowContext(ctx, messageRewriteColumns+` WHERE rewrite_id = ?`, rewrite.ID))
	if err != nil {
		return maildomain.MessageRewrite{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("commit rewrite source deletion: %w", err)
	}
	return value, nil
}

func (repository *MessageRewriteRepository) Finalize(ctx context.Context, input maildomain.FinalizeMessageRewrite) (maildomain.MessageRewrite, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("begin rewrite finalization: %w", err)
	}
	defer tx.Rollback()
	rewrite, err := scanMessageRewrite(tx.QueryRowContext(ctx, messageRewriteColumns+` WHERE rewrite_id = ?`, strings.TrimSpace(input.RewriteID)))
	if err != nil {
		return maildomain.MessageRewrite{}, err
	}
	if rewrite.Status == maildomain.MessageRewriteConfirmed {
		return rewrite, nil
	}
	if rewrite.Status != maildomain.MessageRewriteCommitPending || rewrite.SourcePresent || rewrite.NewLocationID == "" {
		return maildomain.MessageRewrite{}, maildomain.ErrMessageRewriteConflict
	}
	newLocation, err := scanLocation(tx.QueryRowContext(ctx, locationColumns+` WHERE location_id = ?`, rewrite.NewLocationID))
	if err != nil {
		return maildomain.MessageRewrite{}, err
	}
	if !newLocation.Present || newLocation.MessageID != rewrite.MessageID || !strings.EqualFold(newLocation.RawContentHash, rewrite.NewRawContentHash) {
		return maildomain.MessageRewrite{}, maildomain.ErrMessageRewriteConflict
	}
	timestamp := input.Now.UTC().Format(time.RFC3339Nano)
	record := maildomain.IngestRecord{
		MessageID: rewrite.MessageID, AccountID: rewrite.AccountID, FolderID: rewrite.SourceFolderID,
		UIDValidity: rewrite.NewUIDValidity, UID: rewrite.NewUID, ModSequence: rewrite.NewModSequence,
		Flags: newLocation.Flags.All(), InternalDate: input.InternalDate, RFC822Size: rewrite.NewRawByteSize,
		RawContentHash: rewrite.NewRawContentHash, RawStorageKey: rewrite.NewRawStorageKey, RawByteSize: rewrite.NewRawByteSize,
		Parsed: input.Parsed, Content: &input.Content, Direction: input.Direction,
	}
	if err := upsertLogicalMessage(ctx, tx, record, timestamp); err != nil {
		return maildomain.MessageRewrite{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET attachment_count = ?, updated_at = ? WHERE message_id = ?`, input.Parsed.AttachmentCount, timestamp, rewrite.MessageID); err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("set rewritten attachment count: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO local_message_sources (message_id, raw_content_hash, raw_storage_key, raw_byte_size, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(message_id) DO UPDATE SET raw_content_hash = excluded.raw_content_hash,
    raw_storage_key = excluded.raw_storage_key, raw_byte_size = excluded.raw_byte_size, updated_at = excluded.updated_at`,
		rewrite.MessageID, rewrite.NewRawContentHash, rewrite.NewRawStorageKey, rewrite.NewRawByteSize, timestamp, timestamp); err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("persist rewritten local raw source: %w", err)
	}
	if err := upsertMessageContent(ctx, tx, rewrite.MessageID, rewrite.NewLocationID, input.Content, timestamp); err != nil {
		return maildomain.MessageRewrite{}, err
	}
	if err := refreshMessageSearchDocument(ctx, tx, rewrite.MessageID, &input.Content.SearchText, &input.Content.SearchTextTruncated, "complete", "", timestamp); err != nil {
		return maildomain.MessageRewrite{}, err
	}
	if err := rebuildCanonicalThreadProjection(ctx, tx, rewrite.AccountID, []string{rewrite.MessageID}, input.Now); err != nil {
		return maildomain.MessageRewrite{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE message_rewrites SET status = 'confirmed', reconciliation_required = 0, error_code = '', updated_at = ? WHERE rewrite_id = ?`, timestamp, rewrite.ID); err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("finalize message rewrite: %w", err)
	}
	value, err := scanMessageRewrite(tx.QueryRowContext(ctx, messageRewriteColumns+` WHERE rewrite_id = ?`, rewrite.ID))
	if err != nil {
		return maildomain.MessageRewrite{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("commit message rewrite finalization: %w", err)
	}
	return value, nil
}

func scanMessageRewrite(row rowScanner) (maildomain.MessageRewrite, error) {
	var value maildomain.MessageRewrite
	var removedJSON, sourceMod, newMod, status, createdAt, updatedAt string
	var sourcePresent, reconciliation int
	if err := row.Scan(
		&value.ID, &value.AccountID, &value.MessageID, &value.SourceLocationID, &value.SourceFolderID,
		&value.SourceUIDValidity, &value.SourceUID, &sourceMod,
		&value.OldRawContentHash, &value.OldRawStorageKey, &value.OldRawByteSize,
		&value.NewRawContentHash, &value.NewRawStorageKey, &value.NewRawByteSize,
		&value.BeforeAttachmentCount, &value.AfterAttachmentCount, &removedJSON,
		&value.ProposedLocationID, &value.NewLocationID, &value.NewUIDValidity, &value.NewUID, &newMod,
		&value.RequestHash, &value.IdempotencyKey, &status, &value.ProviderMethod, &sourcePresent,
		&reconciliation, &value.ErrorCode, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.MessageRewrite{}, maildomain.ErrMessageRewriteNotFound
		}
		return maildomain.MessageRewrite{}, fmt.Errorf("read message rewrite: %w", err)
	}
	if err := json.Unmarshal([]byte(removedJSON), &value.RemovedAttachments); err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("parse rewrite attachments: %w", err)
	}
	var err error
	value.SourceModSequence, err = parseModSequence(sourceMod)
	if err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("parse rewrite source mod-sequence: %w", err)
	}
	value.NewModSequence, err = parseModSequence(newMod)
	if err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("parse rewrite replacement mod-sequence: %w", err)
	}
	value.Status = maildomain.MessageRewriteStatus(status)
	value.SourcePresent = sourcePresent == 1
	value.ReconciliationRequired = reconciliation == 1
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("parse rewrite creation time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.MessageRewrite{}, fmt.Errorf("parse rewrite update time: %w", err)
	}
	return value, nil
}

func nullableTimeValueFromPointer(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}
