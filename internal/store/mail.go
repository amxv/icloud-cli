package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type MailRepository struct {
	db *sql.DB
}

const checkpointColumns = `
SELECT checkpoint_id, account_id, folder_id, uid_validity, last_committed_uid,
       last_scanned_uid, bootstrap_complete, state, last_error_code,
       more_available, last_attempt_at, last_success_at, created_at, updated_at
FROM sync_checkpoints`

const messageColumnList = `m.message_id, m.account_id, m.rfc_message_id, m.in_reply_to,
       m.direction, m.from_name, m.from_email, m.reply_to_name,
       m.reply_to_email, m.subject, m.snippet, m.header_date,
       m.internal_date, m.sort_at, m.date_source, m.attachment_count,
       COALESCE((SELECT source_location_id FROM message_content WHERE message_content.message_id = m.message_id), ''),
       EXISTS (
           SELECT 1 FROM outbound_attempts AS reply_attempt
           WHERE reply_attempt.account_id = m.account_id
             AND reply_attempt.parent_message_id = m.message_id
             AND reply_attempt.status = 'sent'
             AND reply_attempt.outbound_kind IN ('reply', 'reply_all')
       ),
       EXISTS (
           SELECT 1 FROM outbound_attempts AS pending_reply
           WHERE pending_reply.account_id = m.account_id
             AND pending_reply.parent_message_id = m.message_id
             AND pending_reply.status = 'sent'
             AND pending_reply.outbound_kind IN ('reply', 'reply_all')
             AND pending_reply.answer_reconciliation = 'pending'
       ),
       EXISTS (
           SELECT 1 FROM outbound_attempts AS unsupported_reply
           WHERE unsupported_reply.account_id = m.account_id
             AND unsupported_reply.parent_message_id = m.message_id
             AND unsupported_reply.status = 'sent'
             AND unsupported_reply.outbound_kind IN ('reply', 'reply_all')
             AND unsupported_reply.answer_reconciliation = 'unsupported'
       ),
       EXISTS (
           SELECT 1 FROM outbound_attempts AS forward_attempt
           WHERE forward_attempt.account_id = m.account_id
             AND forward_attempt.source_message_id = m.message_id
             AND forward_attempt.status = 'sent'
       ),
       m.created_at, m.updated_at`

const messageColumns = `
SELECT ` + messageColumnList + `
FROM messages AS m`

const attachmentColumns = `
SELECT a.attachment_id, a.message_id, a.part_id, a.ordinal, a.part_path,
       a.filename, a.media_type, a.media_params_json, a.disposition,
       a.content_id, a.inline, a.content_hash, a.storage_key, a.byte_size,
       a.availability, a.error_code
FROM attachments AS a`

const locationColumns = `
SELECT location_id, message_id, account_id, folder_id, uid_validity, uid,
       (SELECT kind FROM folders WHERE folders.folder_id = message_locations.folder_id),
       seen, flagged, answered, draft, deleted, unknown_flags_json,
       mod_sequence, flags_checked_at, internal_date, rfc822_size, raw_content_hash,
       raw_storage_key, raw_byte_size, present, created_at, updated_at
FROM message_locations`

func (repository *MailRepository) Checkpoint(ctx context.Context, accountID, folderID string) (maildomain.Checkpoint, error) {
	row := repository.db.QueryRowContext(ctx, checkpointColumns+` WHERE account_id = ? AND folder_id = ?`, accountID, folderID)
	return scanCheckpoint(row)
}

func (repository *MailRepository) Checkpoints(ctx context.Context, accountID string, limit int) ([]maildomain.Checkpoint, bool, error) {
	values, more, _, err := repository.CheckpointsPage(ctx, accountID, limit, "")
	return values, more, err
}

type storedCheckpointCursor struct {
	Version        int    `json:"version"`
	AccountID      string `json:"account_id"`
	RepairRequired int    `json:"repair_required"`
	Failed         int    `json:"failed"`
	UpdatedAt      string `json:"updated_at"`
	FolderID       string `json:"folder_id"`
}

func (repository *MailRepository) CheckpointsPage(ctx context.Context, accountID string, limit int, cursor string) ([]maildomain.Checkpoint, bool, string, error) {
	if limit < 1 {
		return nil, false, "", errors.New("list checkpoints: limit must be positive")
	}
	accountID = strings.TrimSpace(accountID)
	const repairOrder = `(state = 'repair_required')`
	const failedOrder = `(state = 'failed')`
	where := ` WHERE account_id = ?`
	arguments := []any{accountID}
	if strings.TrimSpace(cursor) != "" {
		var decoded storedCheckpointCursor
		if err := decodeOpaqueCursor("ccur_", cursor, &decoded); err != nil || decoded.Version != 1 || decoded.AccountID != accountID || strings.TrimSpace(decoded.FolderID) == "" || (decoded.RepairRequired != 0 && decoded.RepairRequired != 1) || (decoded.Failed != 0 && decoded.Failed != 1) {
			return nil, false, "", maildomain.ErrInvalidListCursor
		}
		if parsed, err := time.Parse(time.RFC3339Nano, decoded.UpdatedAt); err != nil || parsed.IsZero() {
			return nil, false, "", maildomain.ErrInvalidListCursor
		}
		where += ` AND (
    ` + repairOrder + ` < ? OR
    (` + repairOrder + ` = ? AND ` + failedOrder + ` < ?) OR
    (` + repairOrder + ` = ? AND ` + failedOrder + ` = ? AND updated_at < ?) OR
    (` + repairOrder + ` = ? AND ` + failedOrder + ` = ? AND updated_at = ? AND folder_id > ?)
)`
		arguments = append(arguments,
			decoded.RepairRequired,
			decoded.RepairRequired, decoded.Failed,
			decoded.RepairRequired, decoded.Failed, decoded.UpdatedAt,
			decoded.RepairRequired, decoded.Failed, decoded.UpdatedAt, decoded.FolderID,
		)
	}
	arguments = append(arguments, limit+1)
	rows, err := repository.db.QueryContext(ctx, checkpointColumns+where+`
ORDER BY `+repairOrder+` DESC, `+failedOrder+` DESC, updated_at DESC, folder_id
LIMIT ?`, arguments...)
	if err != nil {
		return nil, false, "", fmt.Errorf("list checkpoints: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.Checkpoint, 0, limit+1)
	for rows.Next() {
		value, scanErr := scanCheckpoint(rows)
		if scanErr != nil {
			return nil, false, "", scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, "", fmt.Errorf("list checkpoints: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	nextCursor := ""
	if more && len(values) > 0 {
		last := values[len(values)-1]
		repairRequired, failed := 0, 0
		if last.State == maildomain.CheckpointRepairRequired {
			repairRequired = 1
		}
		if last.State == maildomain.CheckpointFailed {
			failed = 1
		}
		nextCursor, err = encodeOpaqueCursor("ccur_", storedCheckpointCursor{
			Version: 1, AccountID: accountID, RepairRequired: repairRequired, Failed: failed,
			UpdatedAt: last.UpdatedAt.UTC().Format(time.RFC3339Nano), FolderID: last.FolderID,
		})
		if err != nil {
			return nil, false, "", fmt.Errorf("encode checkpoint cursor: %w", err)
		}
	}
	return values, more, nextCursor, nil
}

func (repository *MailRepository) CommitInboxPage(ctx context.Context, page maildomain.CommitPage) (maildomain.CommitResult, error) {
	if strings.TrimSpace(page.CheckpointID) == "" || strings.TrimSpace(page.AccountID) == "" || strings.TrimSpace(page.FolderID) == "" || page.UIDValidity == 0 {
		return maildomain.CommitResult{}, errors.New("commit inbox page: checkpoint, account, folder, and UID validity are required")
	}
	if page.LastScannedUID < page.LastCommittedUID {
		return maildomain.CommitResult{}, errors.New("commit inbox page: scanned UID cannot precede committed UID")
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.CommitResult{}, fmt.Errorf("begin inbox page commit: %w", err)
	}
	defer tx.Rollback()

	previous, previousErr := checkpointInTx(ctx, tx, page.AccountID, page.FolderID)
	if previousErr != nil && !errors.Is(previousErr, maildomain.ErrCheckpointNotFound) {
		return maildomain.CommitResult{}, previousErr
	}
	if previousErr == nil {
		if previous.State == maildomain.CheckpointRepairRequired {
			return maildomain.CommitResult{}, maildomain.ErrRepairRequired
		}
		if previous.UIDValidity != 0 && previous.UIDValidity != page.UIDValidity {
			return maildomain.CommitResult{}, maildomain.ErrRepairRequired
		}
		if page.LastCommittedUID < previous.LastCommittedUID || page.LastScannedUID < previous.LastScannedUID {
			return maildomain.CommitResult{}, errors.New("commit inbox page: checkpoint cannot move backwards")
		}
	}

	timestamp := page.AttemptedAt.UTC().Format(time.RFC3339Nano)
	ingested, err := ingestRecordsInTx(ctx, tx, page.AccountID, page.FolderID, page.UIDValidity, page.LastCommittedUID, page.Records, timestamp)
	if err != nil {
		return maildomain.CommitResult{}, err
	}
	stateChanges := 0
	affectedMessages := ingested.Affected
	changedMessages := make(map[string]struct{}, len(ingested.Affected)+len(page.Reconciled))
	for messageID := range ingested.Affected {
		changedMessages[messageID] = struct{}{}
	}
	seenReconciled := make(map[uint32]struct{}, len(page.Reconciled))
	for _, record := range page.Reconciled {
		if record.UID == 0 {
			return maildomain.CommitResult{}, errors.New("commit inbox page: reconciled UID is required")
		}
		if _, exists := seenReconciled[record.UID]; exists {
			return maildomain.CommitResult{}, errors.New("commit inbox page: duplicate reconciled UID")
		}
		seenReconciled[record.UID] = struct{}{}
		changed, reconcileErr := reconcileLocationInTx(ctx, tx, page.AccountID, page.FolderID, page.UIDValidity, record, timestamp)
		if reconcileErr != nil {
			return maildomain.CommitResult{}, reconcileErr
		}
		messageID, reconcileErr := messageIDForProviderLocation(ctx, tx, page.AccountID, page.FolderID, page.UIDValidity, record.UID)
		if reconcileErr != nil {
			return maildomain.CommitResult{}, reconcileErr
		}
		affectedMessages[messageID] = struct{}{}
		if changed {
			stateChanges++
			changedMessages[messageID] = struct{}{}
		}
	}
	if err := rebuildCanonicalThreadProjection(ctx, tx, page.AccountID, mapKeysSorted(affectedMessages), page.AttemptedAt); err != nil {
		return maildomain.CommitResult{}, err
	}

	checkpointID := page.CheckpointID
	createdAt := timestamp
	bootstrapComplete := page.Bootstrap
	if previousErr == nil {
		checkpointID = previous.ID
		createdAt = previous.CreatedAt.UTC().Format(time.RFC3339Nano)
		bootstrapComplete = bootstrapComplete || previous.BootstrapComplete
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO sync_checkpoints (
    checkpoint_id, account_id, folder_id, uid_validity, last_committed_uid,
    last_scanned_uid, bootstrap_complete, state, last_error_code,
    more_available, last_attempt_at, last_success_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 'idle', '', ?, ?, ?, ?, ?)
ON CONFLICT(account_id, folder_id) DO UPDATE SET
    uid_validity = excluded.uid_validity,
    last_committed_uid = excluded.last_committed_uid,
    last_scanned_uid = excluded.last_scanned_uid,
    bootstrap_complete = excluded.bootstrap_complete,
    state = 'idle',
    last_error_code = '',
    more_available = excluded.more_available,
    last_attempt_at = excluded.last_attempt_at,
    last_success_at = excluded.last_success_at,
    updated_at = excluded.updated_at`,
		checkpointID, page.AccountID, page.FolderID, page.UIDValidity,
		page.LastCommittedUID, page.LastScannedUID, boolInt(bootstrapComplete),
		boolInt(page.MoreAvailable), timestamp, timestamp, createdAt, timestamp,
	); err != nil {
		return maildomain.CommitResult{}, fmt.Errorf("persist sync checkpoint: %w", err)
	}
	checkpoint, err := checkpointInTx(ctx, tx, page.AccountID, page.FolderID)
	if err != nil {
		return maildomain.CommitResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.CommitResult{}, fmt.Errorf("commit inbox page: %w", err)
	}
	return maildomain.CommitResult{
		Created: ingested.Created, Updated: ingested.Updated, Reconciled: len(page.Reconciled),
		StateChanges: stateChanges, IdentityMerges: ingested.IdentityMerges,
		DedupeConflicts: ingested.DedupeConflicts, Checkpoint: checkpoint,
		ChangedMessageIDs: mapKeysSorted(changedMessages),
	}, nil
}

func (repository *MailRepository) CommitHydration(ctx context.Context, page maildomain.HydratePage) (maildomain.HydrateResult, error) {
	if strings.TrimSpace(page.AccountID) == "" || strings.TrimSpace(page.FolderID) == "" || page.UIDValidity == 0 || len(page.Records) == 0 || len(page.Records) > 100 {
		return maildomain.HydrateResult{}, errors.New("commit hydration: account, folder, UID validity, and 1-100 records are required")
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.HydrateResult{}, fmt.Errorf("begin hydration commit: %w", err)
	}
	defer tx.Rollback()
	timestamp := page.AttemptedAt.UTC().Format(time.RFC3339Nano)
	ingested, err := ingestRecordsInTx(ctx, tx, page.AccountID, page.FolderID, page.UIDValidity, 0, page.Records, timestamp)
	if err != nil {
		return maildomain.HydrateResult{}, err
	}
	if err := rebuildCanonicalThreadProjection(ctx, tx, page.AccountID, mapKeysSorted(ingested.Affected), page.AttemptedAt); err != nil {
		return maildomain.HydrateResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.HydrateResult{}, fmt.Errorf("commit hydration: %w", err)
	}
	return maildomain.HydrateResult{
		Created: ingested.Created, Updated: ingested.Updated,
		IdentityMerges: ingested.IdentityMerges, DedupeConflicts: ingested.DedupeConflicts,
	}, nil
}

func (repository *MailRepository) RecordFailure(ctx context.Context, checkpointID, accountID, folderID string, uidValidity uint32, code string, attemptedAt time.Time) (maildomain.Checkpoint, error) {
	return repository.recordCheckpointState(ctx, checkpointID, accountID, folderID, uidValidity, maildomain.CheckpointFailed, code, attemptedAt)
}

func (repository *MailRepository) RequireRepair(ctx context.Context, checkpointID, accountID, folderID string, observedUIDValidity uint32, attemptedAt time.Time) (maildomain.Checkpoint, error) {
	return repository.recordCheckpointState(ctx, checkpointID, accountID, folderID, observedUIDValidity, maildomain.CheckpointRepairRequired, "uid_validity_changed", attemptedAt)
}

func (repository *MailRepository) recordCheckpointState(ctx context.Context, checkpointID, accountID, folderID string, uidValidity uint32, state maildomain.CheckpointState, code string, attemptedAt time.Time) (maildomain.Checkpoint, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.Checkpoint{}, fmt.Errorf("begin checkpoint failure update: %w", err)
	}
	defer tx.Rollback()
	previous, previousErr := checkpointInTx(ctx, tx, accountID, folderID)
	if previousErr != nil && !errors.Is(previousErr, maildomain.ErrCheckpointNotFound) {
		return maildomain.Checkpoint{}, previousErr
	}
	timestamp := attemptedAt.UTC().Format(time.RFC3339Nano)
	checkpointID = strings.TrimSpace(checkpointID)
	if checkpointID == "" {
		return maildomain.Checkpoint{}, errors.New("persist checkpoint state: checkpoint ID is required")
	}
	createdAt := timestamp
	lastCommitted, lastScanned := uint32(0), uint32(0)
	bootstrapComplete, moreAvailable := false, false
	storedUIDValidity := uidValidity
	lastSuccess := any(nil)
	if previousErr == nil {
		checkpointID = previous.ID
		createdAt = previous.CreatedAt.UTC().Format(time.RFC3339Nano)
		lastCommitted = previous.LastCommittedUID
		lastScanned = previous.LastScannedUID
		bootstrapComplete = previous.BootstrapComplete
		moreAvailable = previous.MoreAvailable
		if state != maildomain.CheckpointRepairRequired && previous.UIDValidity != 0 {
			storedUIDValidity = previous.UIDValidity
		}
		if previous.LastSuccessAt != nil {
			lastSuccess = previous.LastSuccessAt.UTC().Format(time.RFC3339Nano)
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO sync_checkpoints (
    checkpoint_id, account_id, folder_id, uid_validity, last_committed_uid,
    last_scanned_uid, bootstrap_complete, state, last_error_code,
    more_available, last_attempt_at, last_success_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(account_id, folder_id) DO UPDATE SET
    uid_validity = excluded.uid_validity,
    state = excluded.state,
    last_error_code = excluded.last_error_code,
    last_attempt_at = excluded.last_attempt_at,
    updated_at = excluded.updated_at`,
		checkpointID, accountID, folderID, storedUIDValidity, lastCommitted,
		lastScanned, boolInt(bootstrapComplete), state, strings.TrimSpace(code),
		boolInt(moreAvailable), timestamp, lastSuccess, createdAt, timestamp,
	); err != nil {
		return maildomain.Checkpoint{}, fmt.Errorf("persist checkpoint failure: %w", err)
	}
	checkpoint, err := checkpointInTx(ctx, tx, accountID, folderID)
	if err != nil {
		return maildomain.Checkpoint{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.Checkpoint{}, fmt.Errorf("commit checkpoint failure: %w", err)
	}
	return checkpoint, nil
}

func (repository *MailRepository) MessagesInFolder(ctx context.Context, accountID, folderID string, limit int) ([]maildomain.Message, bool, error) {
	if limit < 1 {
		return nil, false, errors.New("list folder messages: limit must be positive")
	}
	rows, err := repository.db.QueryContext(ctx, messageColumns+`
JOIN message_locations AS l ON l.message_id = m.message_id
WHERE m.account_id = ? AND l.account_id = ? AND l.folder_id = ? AND l.present = 1
ORDER BY m.sort_at IS NULL, m.sort_at DESC, l.uid DESC, m.message_id DESC
LIMIT ?`, accountID, accountID, folderID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list folder messages: %w", err)
	}
	values := make([]maildomain.Message, 0, limit+1)
	for rows.Next() {
		value, scanErr := scanMessage(rows)
		if scanErr != nil {
			rows.Close()
			return nil, false, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, false, fmt.Errorf("list folder messages: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, false, fmt.Errorf("close folder message rows: %w", err)
	}
	for index := range values {
		if err := repository.loadMessageDetails(ctx, &values[index]); err != nil {
			return nil, false, err
		}
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	messagePointers := make([]*maildomain.Message, 0, len(values))
	for index := range values {
		messagePointers = append(messagePointers, &values[index])
	}
	if err := applyMessageSenderPreferenceState(ctx, repository.db, accountID, messagePointers); err != nil {
		return nil, false, err
	}
	if err := applyMessageThreadMuteState(ctx, repository.db, accountID, messagePointers); err != nil {
		return nil, false, err
	}
	return values, more, nil
}

func (repository *MailRepository) MailboxExportPage(ctx context.Context, accountID, folderID string, limit int, cursor *maildomain.MailboxExportCursor) (maildomain.MailboxExportPage, error) {
	accountID = strings.TrimSpace(accountID)
	folderID = strings.TrimSpace(folderID)
	if accountID == "" || folderID == "" || limit < 1 || limit > 1000 {
		return maildomain.MailboxExportPage{}, maildomain.ErrInvalidMailboxExport
	}
	where := `
WHERE l.account_id = ? AND l.folder_id = ? AND l.present = 1`
	arguments := []any{accountID, folderID}
	if cursor != nil {
		if cursor.UIDValidity == 0 || cursor.UID == 0 || strings.TrimSpace(cursor.LocationID) == "" {
			return maildomain.MailboxExportPage{}, maildomain.ErrInvalidMailboxExport
		}
		where += ` AND (l.uid_validity > ? OR (l.uid_validity = ? AND l.uid < ?))`
		arguments = append(arguments, cursor.UIDValidity, cursor.UIDValidity, cursor.UID)
	}
	arguments = append(arguments, limit+1)
	rows, err := repository.db.QueryContext(ctx, `
SELECT m.message_id, l.location_id, l.uid_validity, l.uid,
       m.from_email, COALESCE(l.internal_date, m.header_date, m.created_at),
       l.raw_content_hash, l.raw_storage_key, l.raw_byte_size
FROM message_locations AS l
JOIN messages AS m ON m.message_id = l.message_id AND m.account_id = l.account_id
`+where+`
ORDER BY l.uid_validity ASC, l.uid DESC
LIMIT ?`, arguments...)
	if err != nil {
		return maildomain.MailboxExportPage{}, fmt.Errorf("list mailbox export records: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.MailboxExportRecord, 0, limit+1)
	for rows.Next() {
		var value maildomain.MailboxExportRecord
		var envelopeDate string
		if err := rows.Scan(
			&value.MessageID, &value.LocationID, &value.UIDValidity, &value.UID,
			&value.EnvelopeSender, &envelopeDate, &value.RawContentHash, &value.RawStorageKey, &value.RawByteSize,
		); err != nil {
			return maildomain.MailboxExportPage{}, fmt.Errorf("read mailbox export record: %w", err)
		}
		value.EnvelopeDate, err = time.Parse(time.RFC3339Nano, envelopeDate)
		if err != nil {
			return maildomain.MailboxExportPage{}, fmt.Errorf("parse mailbox export envelope date: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return maildomain.MailboxExportPage{}, fmt.Errorf("list mailbox export records: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	var next *maildomain.MailboxExportCursor
	if more && len(values) > 0 {
		last := values[len(values)-1]
		next = &maildomain.MailboxExportCursor{UIDValidity: last.UIDValidity, UID: last.UID, LocationID: last.LocationID}
	}
	return maildomain.MailboxExportPage{Records: values, More: more, NextCursor: next}, nil
}

func (repository *MailRepository) Inbox(ctx context.Context, accountID, folderID string, limit int) ([]maildomain.Message, bool, error) {
	return repository.MessagesInFolder(ctx, accountID, folderID, limit)
}

func (repository *MailRepository) Message(ctx context.Context, accountID, messageID string) (maildomain.Message, error) {
	value, err := scanMessage(repository.db.QueryRowContext(ctx, messageColumns+`
WHERE m.account_id = ? AND m.message_id = ?`, strings.TrimSpace(accountID), strings.TrimSpace(messageID)))
	if err != nil {
		return maildomain.Message{}, err
	}
	if err := repository.loadMessageDetails(ctx, &value); err != nil {
		return maildomain.Message{}, err
	}
	if err := repository.loadMessageContent(ctx, &value); err != nil {
		return maildomain.Message{}, err
	}
	if err := repository.loadLocalRawSource(ctx, &value); err != nil {
		return maildomain.Message{}, err
	}
	if err := applyMessageSenderPreferenceState(ctx, repository.db, accountID, []*maildomain.Message{&value}); err != nil {
		return maildomain.Message{}, err
	}
	if err := applyMessageThreadMuteState(ctx, repository.db, accountID, []*maildomain.Message{&value}); err != nil {
		return maildomain.Message{}, err
	}
	return value, nil
}

func (repository *MailRepository) loadLocalRawSource(ctx context.Context, value *maildomain.Message) error {
	if value == nil || strings.TrimSpace(value.ID) == "" {
		return nil
	}
	var source maildomain.LocalRawSource
	err := repository.db.QueryRowContext(ctx, `
SELECT raw_content_hash, raw_storage_key, raw_byte_size
FROM local_message_sources WHERE message_id = ?`, value.ID).Scan(
		&source.ContentHash, &source.StorageKey, &source.ByteSize,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read local raw message source: %w", err)
	}
	value.LocalRaw = &source
	return nil
}

func (repository *MailRepository) MessageByProviderLocation(ctx context.Context, accountID, folderID string, uidValidity, uid uint32) (maildomain.Message, error) {
	var messageID string
	if err := repository.db.QueryRowContext(ctx, `
SELECT message_id FROM message_locations
WHERE account_id = ? AND folder_id = ? AND uid_validity = ? AND uid = ? AND present = 1`,
		strings.TrimSpace(accountID), strings.TrimSpace(folderID), uidValidity, uid,
	).Scan(&messageID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.Message{}, maildomain.ErrMessageNotFound
		}
		return maildomain.Message{}, fmt.Errorf("resolve provider message location: %w", err)
	}
	return repository.Message(ctx, accountID, messageID)
}

func (repository *MailRepository) ReconcileUIDs(ctx context.Context, accountID, folderID string, uidValidity uint32, limit int) ([]uint32, error) {
	if uidValidity == 0 || limit < 1 {
		return nil, errors.New("list reconciliation UIDs: UID validity and positive limit are required")
	}
	rows, err := repository.db.QueryContext(ctx, `
SELECT uid
FROM message_locations
WHERE account_id = ? AND folder_id = ? AND uid_validity = ? AND present = 1
ORDER BY flags_checked_at IS NOT NULL, flags_checked_at, uid DESC
LIMIT ?`, strings.TrimSpace(accountID), strings.TrimSpace(folderID), uidValidity, limit)
	if err != nil {
		return nil, fmt.Errorf("list reconciliation UIDs: %w", err)
	}
	defer rows.Close()
	values := make([]uint32, 0, limit)
	for rows.Next() {
		var uid uint32
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("read reconciliation UID: %w", err)
		}
		values = append(values, uid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list reconciliation UIDs: %w", err)
	}
	return values, nil
}

func (repository *MailRepository) Location(ctx context.Context, accountID, locationID string) (maildomain.Location, error) {
	value, err := scanLocation(repository.db.QueryRowContext(ctx, locationColumns+`
WHERE account_id = ? AND location_id = ?`, strings.TrimSpace(accountID), strings.TrimSpace(locationID)))
	if err != nil {
		return maildomain.Location{}, err
	}
	return value, nil
}

func (repository *MailRepository) LocationsByRawHash(ctx context.Context, accountID, messageID, folderID, rawHash string, limit int) ([]maildomain.Location, error) {
	if limit < 1 {
		return nil, errors.New("list locations by raw hash: limit must be positive")
	}
	rows, err := repository.db.QueryContext(ctx, locationColumns+`
WHERE account_id = ? AND message_id = ? AND folder_id = ? AND lower(raw_content_hash) = lower(?) AND present = 1
ORDER BY uid_validity DESC, uid DESC, location_id DESC
LIMIT ?`, strings.TrimSpace(accountID), strings.TrimSpace(messageID), strings.TrimSpace(folderID), strings.TrimSpace(rawHash), limit)
	if err != nil {
		return nil, fmt.Errorf("list locations by raw hash: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.Location, 0, limit)
	for rows.Next() {
		value, scanErr := scanLocation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list locations by raw hash: %w", err)
	}
	return values, nil
}

func (repository *MailRepository) LocationsByRawHashInFolder(ctx context.Context, accountID, folderID, rawHash string, limit int) ([]maildomain.Location, error) {
	if limit < 1 {
		return nil, errors.New("list folder locations by raw hash: limit must be positive")
	}
	rows, err := repository.db.QueryContext(ctx, locationColumns+`
WHERE account_id = ? AND folder_id = ? AND lower(raw_content_hash) = lower(?) AND present = 1
ORDER BY uid_validity DESC, uid DESC, location_id DESC
LIMIT ?`, strings.TrimSpace(accountID), strings.TrimSpace(folderID), strings.TrimSpace(rawHash), limit)
	if err != nil {
		return nil, fmt.Errorf("list folder locations by raw hash: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.Location, 0, limit)
	for rows.Next() {
		value, scanErr := scanLocation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list folder locations by raw hash: %w", err)
	}
	return values, nil
}

func (repository *MailRepository) UpdateLocationState(
	ctx context.Context,
	accountID, locationID string,
	expectedModSequence uint64,
	flags maildomain.LocationFlags,
	modSequence uint64,
	present bool,
	now time.Time,
) (maildomain.Location, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.Location{}, fmt.Errorf("begin location state update: %w", err)
	}
	defer tx.Rollback()
	current, err := scanLocation(tx.QueryRowContext(ctx, locationColumns+`
WHERE account_id = ? AND location_id = ?`, strings.TrimSpace(accountID), strings.TrimSpace(locationID)))
	if err != nil {
		return maildomain.Location{}, err
	}
	if current.ModSequence != expectedModSequence {
		return maildomain.Location{}, maildomain.ErrLocationConflict
	}
	unknownJSON, err := json.Marshal(maildomain.ParseLocationFlags(flags.Unknown).Unknown)
	if err != nil {
		return maildomain.Location{}, fmt.Errorf("encode unknown message flags: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE message_locations
SET seen = ?, flagged = ?, answered = ?, draft = ?, deleted = ?,
    unknown_flags_json = ?, mod_sequence = ?, flags_checked_at = ?,
    present = ?, updated_at = ?
WHERE account_id = ? AND location_id = ?`,
		boolInt(flags.Seen), boolInt(flags.Flagged), boolInt(flags.Answered),
		boolInt(flags.Draft), boolInt(flags.Deleted), string(unknownJSON),
		formatModSequence(modSequence), now.UTC().Format(time.RFC3339Nano),
		boolInt(present), now.UTC().Format(time.RFC3339Nano),
		strings.TrimSpace(accountID), strings.TrimSpace(locationID),
	); err != nil {
		return maildomain.Location{}, fmt.Errorf("update location state: %w", err)
	}
	value, err := scanLocation(tx.QueryRowContext(ctx, locationColumns+` WHERE location_id = ?`, locationID))
	if err != nil {
		return maildomain.Location{}, err
	}
	if err := rebuildCanonicalThreadProjection(ctx, tx, accountID, []string{current.MessageID}, now); err != nil {
		return maildomain.Location{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.Location{}, fmt.Errorf("commit location state update: %w", err)
	}
	return value, nil
}

func (repository *MailRepository) Attachments(ctx context.Context, accountID, messageID string, limit int) ([]maildomain.Attachment, bool, error) {
	values, more, _, err := repository.AttachmentsPage(ctx, accountID, messageID, limit, "")
	return values, more, err
}

type storedAttachmentCursor struct {
	Version      int    `json:"version"`
	AccountID    string `json:"account_id"`
	TargetKind   string `json:"target_kind"`
	TargetID     string `json:"target_id"`
	SortAt       string `json:"sort_at,omitempty"`
	MessageID    string `json:"message_id,omitempty"`
	Ordinal      int    `json:"ordinal"`
	AttachmentID string `json:"attachment_id"`
}

func (repository *MailRepository) AttachmentsPage(ctx context.Context, accountID, messageID string, limit int, cursor string) ([]maildomain.Attachment, bool, string, error) {
	if limit < 1 {
		return nil, false, "", errors.New("list attachments: limit must be positive")
	}
	accountID = strings.TrimSpace(accountID)
	messageID = strings.TrimSpace(messageID)
	where := ` WHERE m.account_id = ? AND a.message_id = ?`
	arguments := []any{accountID, messageID}
	if strings.TrimSpace(cursor) != "" {
		var decoded storedAttachmentCursor
		if err := decodeOpaqueCursor("atcur_", cursor, &decoded); err != nil || decoded.Version != 1 || decoded.AccountID != accountID || decoded.TargetKind != "message" || decoded.TargetID != messageID || strings.TrimSpace(decoded.AttachmentID) == "" || decoded.Ordinal < 0 {
			return nil, false, "", maildomain.ErrInvalidListCursor
		}
		where += ` AND (a.ordinal > ? OR (a.ordinal = ? AND a.attachment_id > ?))`
		arguments = append(arguments, decoded.Ordinal, decoded.Ordinal, decoded.AttachmentID)
	}
	arguments = append(arguments, limit+1)
	rows, err := repository.db.QueryContext(ctx, attachmentColumns+`
JOIN messages AS m ON m.message_id = a.message_id
`+where+`
ORDER BY a.ordinal, a.attachment_id
LIMIT ?`, arguments...)
	if err != nil {
		return nil, false, "", fmt.Errorf("list attachments: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.Attachment, 0, limit+1)
	for rows.Next() {
		value, scanErr := scanAttachment(rows)
		if scanErr != nil {
			return nil, false, "", scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, "", fmt.Errorf("list attachments: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	nextCursor := ""
	if more && len(values) > 0 {
		last := values[len(values)-1]
		nextCursor, err = encodeOpaqueCursor("atcur_", storedAttachmentCursor{
			Version: 1, AccountID: accountID, TargetKind: "message", TargetID: messageID,
			Ordinal: last.Ordinal, AttachmentID: last.ID,
		})
		if err != nil {
			return nil, false, "", fmt.Errorf("encode attachment cursor: %w", err)
		}
	}
	return values, more, nextCursor, nil
}

func (repository *MailRepository) AttachmentsForThread(ctx context.Context, accountID, threadID string, limit int) ([]maildomain.Attachment, bool, error) {
	values, more, _, err := repository.AttachmentsForThreadPage(ctx, accountID, threadID, limit, "")
	return values, more, err
}

func (repository *MailRepository) AttachmentsForThreadPage(ctx context.Context, accountID, threadID string, limit int, cursor string) ([]maildomain.Attachment, bool, string, error) {
	if limit < 1 {
		return nil, false, "", errors.New("list thread attachments: limit must be positive")
	}
	accountID = strings.TrimSpace(accountID)
	threadID = strings.TrimSpace(threadID)
	const sortExpression = `COALESCE(m.sort_at, m.created_at)`
	where := ` WHERE m.account_id = ? AND tm.thread_id = ?`
	arguments := []any{accountID, threadID}
	if strings.TrimSpace(cursor) != "" {
		var decoded storedAttachmentCursor
		if err := decodeOpaqueCursor("atcur_", cursor, &decoded); err != nil || decoded.Version != 1 || decoded.AccountID != accountID || decoded.TargetKind != "thread" || decoded.TargetID != threadID || strings.TrimSpace(decoded.SortAt) == "" || strings.TrimSpace(decoded.MessageID) == "" || strings.TrimSpace(decoded.AttachmentID) == "" || decoded.Ordinal < 0 {
			return nil, false, "", maildomain.ErrInvalidListCursor
		}
		if parsed, err := time.Parse(time.RFC3339Nano, decoded.SortAt); err != nil || parsed.IsZero() {
			return nil, false, "", maildomain.ErrInvalidListCursor
		}
		where += ` AND (
    ` + sortExpression + ` > ? OR
    (` + sortExpression + ` = ? AND m.message_id > ?) OR
    (` + sortExpression + ` = ? AND m.message_id = ? AND a.ordinal > ?) OR
    (` + sortExpression + ` = ? AND m.message_id = ? AND a.ordinal = ? AND a.attachment_id > ?)
)`
		arguments = append(arguments,
			decoded.SortAt,
			decoded.SortAt, decoded.MessageID,
			decoded.SortAt, decoded.MessageID, decoded.Ordinal,
			decoded.SortAt, decoded.MessageID, decoded.Ordinal, decoded.AttachmentID,
		)
	}
	arguments = append(arguments, limit+1)
	rows, err := repository.db.QueryContext(ctx, attachmentColumns+`
JOIN messages AS m ON m.message_id = a.message_id
JOIN thread_messages AS tm ON tm.message_id = a.message_id
`+where+`
ORDER BY `+sortExpression+`, m.message_id, a.ordinal, a.attachment_id
LIMIT ?`, arguments...)
	if err != nil {
		return nil, false, "", fmt.Errorf("list thread attachments: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.Attachment, 0, limit+1)
	for rows.Next() {
		value, scanErr := scanAttachment(rows)
		if scanErr != nil {
			return nil, false, "", scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, "", fmt.Errorf("list thread attachments: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	nextCursor := ""
	if more && len(values) > 0 {
		last := values[len(values)-1]
		var sortAt string
		if err := repository.db.QueryRowContext(ctx, `SELECT COALESCE(sort_at, created_at) FROM messages WHERE account_id = ? AND message_id = ?`, accountID, last.MessageID).Scan(&sortAt); err != nil {
			return nil, false, "", fmt.Errorf("read attachment cursor order: %w", err)
		}
		nextCursor, err = encodeOpaqueCursor("atcur_", storedAttachmentCursor{
			Version: 1, AccountID: accountID, TargetKind: "thread", TargetID: threadID,
			SortAt: sortAt, MessageID: last.MessageID, Ordinal: last.Ordinal, AttachmentID: last.ID,
		})
		if err != nil {
			return nil, false, "", fmt.Errorf("encode thread attachment cursor: %w", err)
		}
	}
	return values, more, nextCursor, nil
}

func (repository *MailRepository) Attachment(ctx context.Context, accountID, attachmentID string) (maildomain.Attachment, error) {
	value, err := scanAttachment(repository.db.QueryRowContext(ctx, attachmentColumns+`
JOIN messages AS m ON m.message_id = a.message_id
WHERE m.account_id = ? AND a.attachment_id = ?`, strings.TrimSpace(accountID), strings.TrimSpace(attachmentID)))
	if err != nil {
		return maildomain.Attachment{}, err
	}
	return value, nil
}

func (repository *MailRepository) SaveMessageContent(ctx context.Context, accountID, messageID, sourceLocationID string, content maildomain.NormalizedContent, now time.Time) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin message content save: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `
SELECT count(*) FROM messages WHERE account_id = ? AND message_id = ?`,
		strings.TrimSpace(accountID), strings.TrimSpace(messageID),
	).Scan(&exists); err != nil {
		return fmt.Errorf("resolve message for content save: %w", err)
	}
	if exists == 0 {
		return maildomain.ErrMessageNotFound
	}
	sourceLocationID = strings.TrimSpace(sourceLocationID)
	if sourceLocationID != "" {
		if err := tx.QueryRowContext(ctx, `
SELECT count(*) FROM message_locations
WHERE account_id = ? AND message_id = ? AND location_id = ?`,
			strings.TrimSpace(accountID), strings.TrimSpace(messageID), sourceLocationID,
		).Scan(&exists); err != nil {
			return fmt.Errorf("resolve source location for content save: %w", err)
		}
		if exists == 0 {
			return maildomain.ErrLocationNotFound
		}
	}
	if err := upsertMessageContent(ctx, tx, messageID, sourceLocationID, content, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	if err := refreshMessageSearchDocument(ctx, tx, messageID, &content.SearchText, &content.SearchTextTruncated, "complete", "", timestamp); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit message content save: %w", err)
	}
	return nil
}

type ingestTransactionResult struct {
	Created         int
	Updated         int
	IdentityMerges  int
	DedupeConflicts int
	Affected        map[string]struct{}
}

func ingestRecordsInTx(
	ctx context.Context,
	tx *sql.Tx,
	accountID, folderID string,
	uidValidity, maxUID uint32,
	records []maildomain.IngestRecord,
	timestamp string,
) (ingestTransactionResult, error) {
	result := ingestTransactionResult{Affected: make(map[string]struct{}, len(records))}
	seenUIDs := make(map[uint32]struct{}, len(records))
	for _, record := range records {
		if err := validateIngestRecordScope(accountID, folderID, uidValidity, maxUID, record); err != nil {
			return ingestTransactionResult{}, err
		}
		if _, duplicate := seenUIDs[record.UID]; duplicate {
			return ingestTransactionResult{}, errors.New("commit mail records: duplicate provider UID")
		}
		seenUIDs[record.UID] = struct{}{}
		created, merged, conflictCount, err := upsertMailboxRecord(ctx, tx, record, timestamp)
		if err != nil {
			return ingestTransactionResult{}, err
		}
		messageID, err := messageIDForProviderLocation(ctx, tx, accountID, folderID, uidValidity, record.UID)
		if err != nil {
			return ingestTransactionResult{}, err
		}
		result.Affected[messageID] = struct{}{}
		if created {
			result.Created++
		} else {
			result.Updated++
		}
		if merged {
			result.IdentityMerges++
		}
		result.DedupeConflicts += conflictCount
	}
	return result, nil
}

func validateIngestRecordScope(accountID, folderID string, uidValidity, maxUID uint32, record maildomain.IngestRecord) error {
	if record.AccountID != accountID || record.FolderID != folderID || record.UIDValidity != uidValidity {
		return errors.New("commit inbox page: record scope does not match page")
	}
	if strings.TrimSpace(record.MessageID) == "" || strings.TrimSpace(record.LocationID) == "" || record.UID == 0 {
		return errors.New("commit inbox page: record identity is incomplete")
	}
	if maxUID != 0 && record.UID > maxUID {
		return errors.New("commit inbox page: record UID exceeds checkpoint frontier")
	}
	if len(record.RawContentHash) != 64 || strings.TrimSpace(record.RawStorageKey) == "" || record.RawByteSize < 0 {
		return errors.New("commit inbox page: raw blob metadata is invalid")
	}
	if record.Content != nil {
		paths := make(map[string]struct{}, len(record.Content.Parts))
		partIDs := make(map[string]string, len(record.Content.Parts))
		ordinals := make(map[int]struct{}, len(record.Content.Parts))
		for _, part := range record.Content.Parts {
			if strings.TrimSpace(part.Path) == "" {
				return errors.New("commit inbox page: MIME part path is required")
			}
			if _, exists := paths[part.Path]; exists {
				return errors.New("commit inbox page: duplicate MIME part path")
			}
			paths[part.Path] = struct{}{}
			if part.Ordinal < 0 {
				return errors.New("commit inbox page: MIME part ordinal cannot be negative")
			}
			if _, exists := ordinals[part.Ordinal]; exists {
				return errors.New("commit inbox page: duplicate MIME part ordinal")
			}
			ordinals[part.Ordinal] = struct{}{}
			if strings.TrimSpace(part.ID) == "" {
				return errors.New("commit inbox page: MIME part ID is required")
			}
			partIDs[part.Path] = part.ID
		}
		attachmentPaths := make(map[string]struct{}, len(record.Content.Attachments))
		attachmentOrdinals := make(map[int]struct{}, len(record.Content.Attachments))
		for _, attachment := range record.Content.Attachments {
			if strings.TrimSpace(attachment.ID) == "" || strings.TrimSpace(attachment.PartID) == "" || strings.TrimSpace(attachment.PartPath) == "" {
				return errors.New("commit inbox page: attachment identity is incomplete")
			}
			if _, exists := paths[attachment.PartPath]; !exists {
				return errors.New("commit inbox page: attachment MIME part is missing")
			}
			if attachment.PartID != partIDs[attachment.PartPath] {
				return errors.New("commit inbox page: attachment MIME part ID does not match its path")
			}
			if attachment.MessageID != record.MessageID {
				return errors.New("commit inbox page: attachment message ID does not match its record")
			}
			if _, exists := attachmentPaths[attachment.PartPath]; exists {
				return errors.New("commit inbox page: duplicate attachment part path")
			}
			attachmentPaths[attachment.PartPath] = struct{}{}
			if attachment.Ordinal < 0 {
				return errors.New("commit inbox page: attachment ordinal cannot be negative")
			}
			if _, exists := attachmentOrdinals[attachment.Ordinal]; exists {
				return errors.New("commit inbox page: duplicate attachment ordinal")
			}
			attachmentOrdinals[attachment.Ordinal] = struct{}{}
		}
	}
	return nil
}

func upsertMailboxRecord(ctx context.Context, tx *sql.Tx, record maildomain.IngestRecord, timestamp string) (bool, bool, int, error) {
	if _, err := tx.ExecContext(ctx, `
INSERT INTO raw_blobs (content_hash, storage_key, byte_size, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(content_hash) DO NOTHING`,
		record.RawContentHash, record.RawStorageKey, record.RawByteSize, timestamp,
	); err != nil {
		return false, false, 0, fmt.Errorf("persist raw blob metadata: %w", err)
	}
	var storedKey string
	var storedSize int64
	if err := tx.QueryRowContext(ctx, `SELECT storage_key, byte_size FROM raw_blobs WHERE content_hash = ?`, record.RawContentHash).Scan(&storedKey, &storedSize); err != nil {
		return false, false, 0, fmt.Errorf("verify raw blob metadata: %w", err)
	}
	if storedKey != record.RawStorageKey || storedSize != record.RawByteSize {
		return false, false, 0, errors.New("persist raw blob metadata: content-address collision")
	}

	resolution, err := resolveMessageIdentity(ctx, tx, record)
	if err != nil {
		return false, false, 0, err
	}
	record.MessageID = resolution.MessageID
	preserveDraft, err := pendingDraftOwnsCanonicalContent(ctx, tx, record.MessageID, record.RawContentHash)
	if err != nil {
		return false, false, 0, err
	}
	if preserveDraft {
		resolution.PreserveLogical = true
	}
	if !resolution.PreserveLogical {
		if err := upsertLogicalMessage(ctx, tx, record, timestamp); err != nil {
			return false, false, 0, err
		}
	} else if record.Direction == maildomain.DirectionOutbound {
		if _, err := tx.ExecContext(ctx, `
UPDATE messages SET direction = 'outbound', updated_at = ? WHERE message_id = ?`, timestamp, record.MessageID); err != nil {
			return false, false, 0, fmt.Errorf("promote message direction: %w", err)
		}
	}

	structuredFlags := maildomain.ParseLocationFlags(record.Flags)
	unknownFlagsJSON, err := json.Marshal(structuredFlags.Unknown)
	if err != nil {
		return false, false, 0, fmt.Errorf("encode unknown message flags: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO message_locations (
    location_id, message_id, account_id, folder_id, uid_validity, uid,
    seen, flagged, answered, draft, deleted, unknown_flags_json, mod_sequence,
    flags_checked_at, internal_date, rfc822_size, raw_content_hash, raw_storage_key,
    raw_byte_size, present, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
ON CONFLICT(account_id, folder_id, uid_validity, uid) DO UPDATE SET
    message_id = excluded.message_id,
    seen = excluded.seen,
    flagged = excluded.flagged,
    answered = excluded.answered,
    draft = excluded.draft,
    deleted = excluded.deleted,
    unknown_flags_json = excluded.unknown_flags_json,
    mod_sequence = excluded.mod_sequence,
    flags_checked_at = excluded.flags_checked_at,
    internal_date = excluded.internal_date,
    rfc822_size = excluded.rfc822_size,
    raw_content_hash = excluded.raw_content_hash,
    raw_storage_key = excluded.raw_storage_key,
    raw_byte_size = excluded.raw_byte_size,
    present = 1,
    updated_at = excluded.updated_at`,
		record.LocationID, record.MessageID, record.AccountID, record.FolderID,
		record.UIDValidity, record.UID,
		boolInt(structuredFlags.Seen), boolInt(structuredFlags.Flagged), boolInt(structuredFlags.Answered),
		boolInt(structuredFlags.Draft), boolInt(structuredFlags.Deleted), string(unknownFlagsJSON),
		formatModSequence(record.ModSequence), timestamp, nullableTimeValue(record.InternalDate),
		record.RFC822Size, record.RawContentHash, record.RawStorageKey,
		record.RawByteSize, timestamp, timestamp,
	); err != nil {
		return false, false, 0, fmt.Errorf("persist message location: %w", err)
	}
	var actualLocationID string
	if err := tx.QueryRowContext(ctx, `
SELECT location_id FROM message_locations
WHERE account_id = ? AND folder_id = ? AND uid_validity = ? AND uid = ?`,
		record.AccountID, record.FolderID, record.UIDValidity, record.UID,
	).Scan(&actualLocationID); err != nil {
		return false, false, 0, fmt.Errorf("read persisted message location identity: %w", err)
	}
	if err := persistRecordIdentity(ctx, tx, record, actualLocationID, resolution, timestamp); err != nil {
		return false, false, 0, err
	}
	contentReplaced := false
	if record.Content != nil && len(resolution.Conflicts) == 0 {
		shouldReplaceContent := !resolution.PreserveLogical
		if !shouldReplaceContent {
			var contentCount int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM message_content WHERE message_id = ?`, record.MessageID).Scan(&contentCount); err != nil {
				return false, false, 0, fmt.Errorf("inspect existing message content: %w", err)
			}
			shouldReplaceContent = contentCount == 0
		}
		if shouldReplaceContent {
			if err := upsertMessageContent(ctx, tx, record.MessageID, actualLocationID, *record.Content, timestamp); err != nil {
				return false, false, 0, err
			}
			contentReplaced = true
		}
	}
	var searchBody *string
	var searchTruncated *bool
	if contentReplaced {
		searchBody = &record.Content.SearchText
		searchTruncated = &record.Content.SearchTextTruncated
	}
	if err := refreshMessageSearchDocument(ctx, tx, record.MessageID, searchBody, searchTruncated, "", "", timestamp); err != nil {
		return false, false, 0, err
	}
	return resolution.Created, resolution.Merged, len(resolution.Conflicts), nil
}

func pendingDraftOwnsCanonicalContent(ctx context.Context, tx *sql.Tx, messageID, observedRawHash string) (bool, error) {
	var state, localRawHash string
	err := tx.QueryRowContext(ctx, `
SELECT d.sync_state, s.raw_content_hash
FROM mail_drafts AS d
JOIN local_message_sources AS s ON s.message_id = d.message_id
WHERE d.message_id = ?`, strings.TrimSpace(messageID)).Scan(&state, &localRawHash)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect pending draft canonical content: %w", err)
	}
	switch maildomain.DraftSyncState(state) {
	case maildomain.DraftLocal, maildomain.DraftReconcile, maildomain.DraftCleanupPending, maildomain.DraftSendUnknown:
		return strings.TrimSpace(localRawHash) != "" && !strings.EqualFold(strings.TrimSpace(localRawHash), strings.TrimSpace(observedRawHash)), nil
	default:
		return false, nil
	}
}

func upsertLogicalMessage(ctx context.Context, tx *sql.Tx, record maildomain.IngestRecord, timestamp string) error {
	internalDate, sortAt, dateSource := maildomain.MessageTimes(record.Parsed, record.InternalDate)
	direction := record.Direction
	if direction != maildomain.DirectionOutbound {
		direction = maildomain.DirectionInbound
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO messages (
    message_id, account_id, rfc_message_id, in_reply_to, direction,
    from_name, from_email, reply_to_name, reply_to_email, subject, snippet,
    header_date, internal_date, sort_at, date_source, attachment_count,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(message_id) DO UPDATE SET
    rfc_message_id = CASE WHEN excluded.rfc_message_id <> '' THEN excluded.rfc_message_id ELSE messages.rfc_message_id END,
    in_reply_to = CASE WHEN excluded.in_reply_to <> '' THEN excluded.in_reply_to ELSE messages.in_reply_to END,
    direction = CASE WHEN messages.direction = 'outbound' OR excluded.direction = 'outbound' THEN 'outbound' ELSE 'inbound' END,
    from_name = CASE WHEN excluded.from_name <> '' THEN excluded.from_name ELSE messages.from_name END,
    from_email = CASE WHEN excluded.from_email <> '' THEN excluded.from_email ELSE messages.from_email END,
    reply_to_name = CASE WHEN excluded.reply_to_name <> '' THEN excluded.reply_to_name ELSE messages.reply_to_name END,
    reply_to_email = CASE WHEN excluded.reply_to_email <> '' THEN excluded.reply_to_email ELSE messages.reply_to_email END,
    subject = CASE WHEN excluded.subject <> '' THEN excluded.subject ELSE messages.subject END,
    snippet = CASE WHEN excluded.snippet <> '' THEN excluded.snippet ELSE messages.snippet END,
    header_date = COALESCE(excluded.header_date, messages.header_date),
    internal_date = COALESCE(excluded.internal_date, messages.internal_date),
    sort_at = COALESCE(excluded.sort_at, messages.sort_at),
    date_source = CASE WHEN excluded.date_source <> 'unknown' THEN excluded.date_source ELSE messages.date_source END,
    attachment_count = MAX(messages.attachment_count, excluded.attachment_count),
    updated_at = excluded.updated_at`,
		record.MessageID, record.AccountID, record.Parsed.RFCMessageID,
		record.Parsed.InReplyTo, direction, record.Parsed.From.DisplayName, record.Parsed.From.Email,
		record.Parsed.ReplyTo.DisplayName, record.Parsed.ReplyTo.Email,
		record.Parsed.Subject, record.Parsed.Snippet, nullableTime(record.Parsed.HeaderDate),
		nullableTime(internalDate), nullableTime(sortAt), dateSource,
		record.Parsed.AttachmentCount, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("persist message: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_recipients WHERE message_id = ?`, record.MessageID); err != nil {
		return fmt.Errorf("replace message recipients: %w", err)
	}
	for _, group := range []struct {
		kind      maildomain.RecipientKind
		addresses []maildomain.Address
	}{
		{maildomain.RecipientTo, record.Parsed.To},
		{maildomain.RecipientCC, record.Parsed.CC},
		{maildomain.RecipientBCC, record.Parsed.BCC},
	} {
		for index, address := range group.addresses {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO message_recipients (message_id, kind, ordinal, display_name, email_address)
VALUES (?, ?, ?, ?, ?)`, record.MessageID, group.kind, index, address.DisplayName, address.Email); err != nil {
				return fmt.Errorf("persist message recipient: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_references WHERE message_id = ?`, record.MessageID); err != nil {
		return fmt.Errorf("replace message references: %w", err)
	}
	for index, reference := range record.Parsed.References {
		if _, err := tx.ExecContext(ctx, `INSERT INTO message_references (message_id, ordinal, reference) VALUES (?, ?, ?)`, record.MessageID, index, reference); err != nil {
			return fmt.Errorf("persist message reference: %w", err)
		}
	}
	return nil
}

func (repository *MailRepository) RegisterGeneratedMessageID(ctx context.Context, accountID, messageID, generatedMessageID string, now time.Time) error {
	accountID = strings.TrimSpace(accountID)
	messageID = strings.TrimSpace(messageID)
	generatedMessageID = maildomain.NormalizeMessageID(generatedMessageID)
	if accountID == "" || messageID == "" || generatedMessageID == "" {
		return errors.New("register generated Message-ID: account, message, and Message-ID are required")
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin generated Message-ID registration: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `
SELECT count(*) FROM messages WHERE account_id = ? AND message_id = ?`, accountID, messageID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("resolve generated Message-ID message: %w", err)
	}
	if exists == 0 {
		return maildomain.ErrMessageNotFound
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	if err := persistIdentityEvidence(
		ctx, tx, accountID, messageID, maildomain.IdentityGeneratedMessageID,
		generatedMessageID, maildomain.IdentityStrong, "", timestamp,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit generated Message-ID registration: %w", err)
	}
	return nil
}

func reconcileLocationInTx(
	ctx context.Context,
	tx *sql.Tx,
	accountID, folderID string,
	uidValidity uint32,
	record maildomain.ReconcileRecord,
	timestamp string,
) (bool, error) {
	current, err := scanLocation(tx.QueryRowContext(ctx, locationColumns+`
WHERE account_id = ? AND folder_id = ? AND uid_validity = ? AND uid = ?`,
		accountID, folderID, uidValidity, record.UID,
	))
	if err != nil {
		if errors.Is(err, maildomain.ErrLocationNotFound) {
			return false, errors.New("commit inbox page: reconciled location is missing locally")
		}
		return false, err
	}
	flags := record.Flags
	unknownJSON, err := json.Marshal(maildomain.ParseLocationFlags(flags.Unknown).Unknown)
	if err != nil {
		return false, fmt.Errorf("encode reconciled unknown flags: %w", err)
	}
	changed := current.Present != record.Present || current.ModSequence != record.ModSequence || !locationFlagsEqual(current.Flags, flags)
	if _, err := tx.ExecContext(ctx, `
UPDATE message_locations
SET seen = ?, flagged = ?, answered = ?, draft = ?, deleted = ?,
    unknown_flags_json = ?, mod_sequence = ?, flags_checked_at = ?,
    present = ?, updated_at = CASE WHEN ? THEN ? ELSE updated_at END
WHERE account_id = ? AND folder_id = ? AND uid_validity = ? AND uid = ?`,
		boolInt(flags.Seen), boolInt(flags.Flagged), boolInt(flags.Answered),
		boolInt(flags.Draft), boolInt(flags.Deleted), string(unknownJSON),
		formatModSequence(record.ModSequence), timestamp, boolInt(record.Present),
		boolInt(changed), timestamp,
		accountID, folderID, uidValidity, record.UID,
	); err != nil {
		return false, fmt.Errorf("reconcile message location: %w", err)
	}
	return changed, nil
}

func messageIDForProviderLocation(
	ctx context.Context,
	tx *sql.Tx,
	accountID, folderID string,
	uidValidity, uid uint32,
) (string, error) {
	var messageID string
	if err := tx.QueryRowContext(ctx, `
SELECT message_id
FROM message_locations
WHERE account_id = ? AND folder_id = ? AND uid_validity = ? AND uid = ?`,
		strings.TrimSpace(accountID), strings.TrimSpace(folderID), uidValidity, uid,
	).Scan(&messageID); err != nil {
		return "", fmt.Errorf("read message for provider location: %w", err)
	}
	return messageID, nil
}

func mapKeysSorted(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func locationFlagsEqual(left, right maildomain.LocationFlags) bool {
	leftValues, rightValues := left.All(), right.All()
	if len(leftValues) != len(rightValues) {
		return false
	}
	for index := range leftValues {
		if leftValues[index] != rightValues[index] {
			return false
		}
	}
	return true
}

func upsertMessageContent(ctx context.Context, tx *sql.Tx, messageID, sourceLocationID string, content maildomain.NormalizedContent, timestamp string) error {
	headersJSON, err := json.Marshal(content.Headers)
	if err != nil {
		return fmt.Errorf("encode message headers: %w", err)
	}
	diagnosticsJSON, err := json.Marshal(content.Diagnostics)
	if err != nil {
		return fmt.Errorf("encode message diagnostics: %w", err)
	}
	textPathsJSON, err := json.Marshal(content.TextPaths)
	if err != nil {
		return fmt.Errorf("encode message text paths: %w", err)
	}
	htmlPathsJSON, err := json.Marshal(content.HTMLPaths)
	if err != nil {
		return fmt.Errorf("encode message HTML paths: %w", err)
	}
	parsedComplete := len(content.Diagnostics) == 0
	if _, err := tx.ExecContext(ctx, `
INSERT INTO message_content (
    message_id, headers_json, diagnostics_json, text_paths_json,
    html_paths_json, parsed_complete, attachments_indexed, source_location_id,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(message_id) DO UPDATE SET
    headers_json = excluded.headers_json,
    diagnostics_json = excluded.diagnostics_json,
    text_paths_json = excluded.text_paths_json,
    html_paths_json = excluded.html_paths_json,
    parsed_complete = excluded.parsed_complete,
    attachments_indexed = excluded.attachments_indexed,
    source_location_id = excluded.source_location_id,
    updated_at = excluded.updated_at`,
		messageID, string(headersJSON), string(diagnosticsJSON), string(textPathsJSON),
		string(htmlPathsJSON), boolInt(parsedComplete), boolInt(content.AttachmentsIndexed),
		strings.TrimSpace(sourceLocationID), timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("persist message content metadata: %w", err)
	}

	existingParts := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT path, part_id FROM message_parts WHERE message_id = ?`, messageID)
	if err != nil {
		return fmt.Errorf("read existing MIME part IDs: %w", err)
	}
	for rows.Next() {
		var path, partID string
		if err := rows.Scan(&path, &partID); err != nil {
			rows.Close()
			return fmt.Errorf("read existing MIME part ID: %w", err)
		}
		existingParts[path] = partID
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read existing MIME part IDs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close existing MIME part IDs: %w", err)
	}
	existingAttachments := map[string]string{}
	attachmentRows, err := tx.QueryContext(ctx, `SELECT part_path, attachment_id FROM attachments WHERE message_id = ?`, messageID)
	if err != nil {
		return fmt.Errorf("read existing attachment IDs: %w", err)
	}
	for attachmentRows.Next() {
		var path, attachmentID string
		if err := attachmentRows.Scan(&path, &attachmentID); err != nil {
			attachmentRows.Close()
			return fmt.Errorf("read existing attachment ID: %w", err)
		}
		existingAttachments[path] = attachmentID
	}
	if err := attachmentRows.Err(); err != nil {
		attachmentRows.Close()
		return fmt.Errorf("read existing attachment IDs: %w", err)
	}
	if err := attachmentRows.Close(); err != nil {
		return fmt.Errorf("close existing attachment IDs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_parts WHERE message_id = ?`, messageID); err != nil {
		return fmt.Errorf("replace MIME parts: %w", err)
	}
	partIDs := make(map[string]string, len(content.Parts))
	for _, part := range content.Parts {
		partID := strings.TrimSpace(part.ID)
		if stableID := existingParts[part.Path]; stableID != "" {
			partID = stableID
		}
		if partID == "" {
			return errors.New("persist MIME part: part ID is required")
		}
		partDiagnostics, err := json.Marshal(part.Diagnostics)
		if err != nil {
			return fmt.Errorf("encode MIME part diagnostics: %w", err)
		}
		mediaParams, err := json.Marshal(part.MediaParams)
		if err != nil {
			return fmt.Errorf("encode MIME part media parameters: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO message_parts (
    part_id, message_id, ordinal, path, parent_path, media_type, media_params_json, charset,
    transfer_encoding, disposition, filename, content_id, inline,
    attachment, embedded_message, decoded_size, diagnostics_json,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			partID, messageID, part.Ordinal, part.Path, part.ParentPath, part.MediaType, string(mediaParams), part.Charset,
			part.TransferEncoding, part.Disposition, part.Filename, part.ContentID,
			boolInt(part.Inline), boolInt(part.Attachment), boolInt(part.EmbeddedMessage),
			part.DecodedSize, string(partDiagnostics), timestamp, timestamp,
		); err != nil {
			return fmt.Errorf("persist MIME part: %w", err)
		}
		partIDs[part.Path] = partID
	}
	for _, attachment := range content.Attachments {
		partID := partIDs[attachment.PartPath]
		if partID == "" {
			return errors.New("persist attachment: MIME part is missing")
		}
		attachmentID := strings.TrimSpace(attachment.ID)
		if stableID := existingAttachments[attachment.PartPath]; stableID != "" {
			attachmentID = stableID
		}
		if attachmentID == "" {
			return errors.New("persist attachment: attachment ID is required")
		}
		availability := attachment.Availability
		if availability == "" {
			availability = maildomain.AttachmentUnavailable
		}
		mediaParams, err := json.Marshal(attachment.MediaParams)
		if err != nil {
			return fmt.Errorf("encode attachment media parameters: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO attachments (
    attachment_id, message_id, part_id, ordinal, part_path, filename,
    media_type, media_params_json, disposition, content_id, inline,
    content_hash, storage_key, byte_size, availability, error_code,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			attachmentID, messageID, partID, attachment.Ordinal, attachment.PartPath,
			attachment.Filename, attachment.MediaType, string(mediaParams), attachment.Disposition,
			attachment.ContentID, boolInt(attachment.Inline), strings.ToLower(strings.TrimSpace(attachment.ContentHash)),
			strings.TrimSpace(attachment.StorageKey), attachment.ByteSize, availability,
			strings.TrimSpace(attachment.ErrorCode), timestamp, timestamp,
		); err != nil {
			return fmt.Errorf("persist attachment: %w", err)
		}
	}
	return nil
}

type identityResolution struct {
	MessageID       string
	Created         bool
	Merged          bool
	PreserveLogical bool
	MatchKind       maildomain.IdentityKind
	Conflicts       []pendingIdentityConflict
}

type pendingIdentityConflict struct {
	Kind               maildomain.IdentityKind
	Value              string
	ExistingMessageID  string
	ObservedMessageID  string
	ExistingLocationID string
	ObservedLocationID string
	Reason             string
}

type identityCandidate struct {
	MessageID  string
	Locations  int
	LocationID string
}

func resolveMessageIdentity(ctx context.Context, tx *sql.Tx, record maildomain.IngestRecord) (identityResolution, error) {
	proposed := strings.TrimSpace(record.MessageID)
	if proposed == "" {
		return identityResolution{}, errors.New("resolve message identity: proposed message ID is required")
	}

	var locationID, locationMessageID string
	err := tx.QueryRowContext(ctx, `
SELECT location_id, message_id
FROM message_locations
WHERE account_id = ? AND folder_id = ? AND uid_validity = ? AND uid = ?`,
		record.AccountID, record.FolderID, record.UIDValidity, record.UID,
	).Scan(&locationID, &locationMessageID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return identityResolution{}, fmt.Errorf("resolve message by provider location: %w", err)
	}

	rfcValue := maildomain.NormalizeMessageID(record.Parsed.RFCMessageID)
	strongMessageID := ""
	strongLocations := 0
	strongKind := maildomain.IdentityKind("")
	if rfcValue != "" {
		rfcCandidates, candidateErr := rfcIdentityCandidates(ctx, tx, record.AccountID, rfcValue, 21)
		if candidateErr != nil {
			return identityResolution{}, candidateErr
		}
		if len(rfcCandidates) > 1 {
			selectedMessageID := proposed
			created := true
			preserveLogical := false
			if locationMessageID != "" {
				selectedMessageID = locationMessageID
				created = false
				preserveLogical = true
			}
			conflicts := make([]pendingIdentityConflict, 0, len(rfcCandidates))
			for _, candidate := range rfcCandidates {
				if candidate.MessageID == selectedMessageID {
					continue
				}
				conflicts = append(conflicts, pendingIdentityConflict{
					Kind: maildomain.IdentityRFCMessageID, Value: rfcValue,
					ExistingMessageID: candidate.MessageID, ObservedMessageID: selectedMessageID,
					ExistingLocationID: candidate.LocationID, ObservedLocationID: locationID,
					Reason: "duplicate_strong_identity",
				})
			}
			return identityResolution{
				MessageID: selectedMessageID, Created: created, PreserveLogical: preserveLogical,
				MatchKind: maildomain.IdentityProviderLocation, Conflicts: conflicts,
			}, nil
		}
		if len(rfcCandidates) == 1 {
			candidate := rfcCandidates[0]
			corroborated, corroborationErr := rfcIdentityCorroborated(ctx, tx, record.AccountID, candidate.MessageID, rfcValue, record.ContentFingerprint, record.RawContentHash)
			if corroborationErr != nil {
				return identityResolution{}, corroborationErr
			}
			if locationMessageID == "" && !corroborated {
				return identityResolution{
					MessageID: proposed, Created: true,
					MatchKind: maildomain.IdentityProviderLocation,
					Conflicts: []pendingIdentityConflict{{
						Kind: maildomain.IdentityRFCMessageID, Value: rfcValue,
						ExistingMessageID: candidate.MessageID, ObservedMessageID: proposed,
						ExistingLocationID: candidate.LocationID, ObservedLocationID: record.LocationID,
						Reason: "rfc_message_id_uncorroborated_provider_collision",
					}},
				}, nil
			}
			strongMessageID = candidate.MessageID
			strongLocations = candidate.Locations
			strongKind = maildomain.IdentityRFCMessageID
		}
		if strongMessageID == "" {
			err = tx.QueryRowContext(ctx, `
SELECT e.message_id,
       (SELECT count(*) FROM message_locations AS l WHERE l.message_id = e.message_id)
FROM message_identity_evidence AS e
WHERE e.account_id = ? AND e.kind = 'generated_message_id' AND e.value = ?`,
				record.AccountID, rfcValue,
			).Scan(&strongMessageID, &strongLocations)
			if err == nil {
				strongKind = maildomain.IdentityGeneratedMessageID
			} else if !errors.Is(err, sql.ErrNoRows) {
				return identityResolution{}, fmt.Errorf("resolve message by generated Message-ID: %w", err)
			}
		}
	}

	if strongMessageID != "" {
		if locationMessageID == "" || locationMessageID == strongMessageID {
			return identityResolution{
				MessageID: strongMessageID, Created: false,
				Merged:          locationMessageID == "",
				PreserveLogical: locationMessageID == "" && strongLocations > 0,
				MatchKind:       strongKind,
			}, nil
		}
		return identityResolution{
			MessageID: locationMessageID, Created: false, PreserveLogical: true,
			MatchKind: maildomain.IdentityProviderLocation,
			Conflicts: []pendingIdentityConflict{{
				Kind: strongKind, Value: rfcValue,
				ExistingMessageID: strongMessageID, ObservedMessageID: locationMessageID,
				ExistingLocationID: "", ObservedLocationID: locationID,
				Reason: "strong_identity_disagrees_with_provider_location",
			}},
		}, nil
	}

	if locationMessageID != "" {
		return identityResolution{
			MessageID: locationMessageID, Created: false,
			MatchKind: maildomain.IdentityProviderLocation,
		}, nil
	}

	fingerprint := strings.TrimSpace(record.ContentFingerprint)
	if fingerprint != "" {
		candidates, candidateErr := contentIdentityCandidates(ctx, tx, record.AccountID, fingerprint, 21)
		if candidateErr != nil {
			return identityResolution{}, candidateErr
		}
		if len(candidates) == 1 && candidates[0].Locations == 0 {
			return identityResolution{
				MessageID: candidates[0].MessageID, Created: false, Merged: true,
				MatchKind: maildomain.IdentityContentFingerprint,
			}, nil
		}
		if len(candidates) > 0 {
			conflicts := make([]pendingIdentityConflict, 0, len(candidates))
			reason := "content_fingerprint_ambiguous"
			if len(candidates) == 1 && candidates[0].Locations > 0 {
				reason = "content_fingerprint_provider_collision"
			}
			for _, candidate := range candidates {
				conflicts = append(conflicts, pendingIdentityConflict{
					Kind: maildomain.IdentityContentFingerprint, Value: fingerprint,
					ExistingMessageID: candidate.MessageID, ObservedMessageID: proposed,
					ExistingLocationID: candidate.LocationID, ObservedLocationID: record.LocationID,
					Reason: reason,
				})
			}
			return identityResolution{
				MessageID: proposed, Created: true,
				MatchKind: maildomain.IdentityProviderLocation,
				Conflicts: conflicts,
			}, nil
		}
	}
	return identityResolution{MessageID: proposed, Created: true, MatchKind: maildomain.IdentityProviderLocation}, nil
}

// rfcIdentityCorroborated deliberately treats RFC Message-ID as evidence, not
// logical identity. A second provider location may join an existing canonical
// message only when independent evidence says it is the same message: either
// the locally generated outbound Message-ID is known, or the canonical content
// fingerprint agrees. Provider-location identity itself is handled before this
// helper and remains authoritative for re-observations of the same UID.
func rfcIdentityCorroborated(ctx context.Context, tx *sql.Tx, accountID, messageID, rfcMessageID, fingerprint, rawContentHash string) (bool, error) {
	var generated int
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
    SELECT 1
    FROM message_identity_evidence
    WHERE account_id = ? AND message_id = ?
      AND kind = 'generated_message_id' AND value = ?
)`, strings.TrimSpace(accountID), strings.TrimSpace(messageID), strings.TrimSpace(rfcMessageID)).Scan(&generated); err != nil {
		return false, fmt.Errorf("corroborate RFC Message-ID with generated identity: %w", err)
	}
	if generated == 1 {
		return true, nil
	}
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint != "" {
		var matchingContent int
		if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
    SELECT 1
    FROM message_identity_evidence
    WHERE account_id = ? AND message_id = ?
      AND kind = 'content_fingerprint' AND value = ?
)`, strings.TrimSpace(accountID), strings.TrimSpace(messageID), fingerprint).Scan(&matchingContent); err != nil {
			return false, fmt.Errorf("corroborate RFC Message-ID with content fingerprint: %w", err)
		}
		if matchingContent == 1 {
			return true, nil
		}
	}
	rawContentHash = strings.ToLower(strings.TrimSpace(rawContentHash))
	if rawContentHash == "" {
		return false, nil
	}
	var matchingRaw int
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
    SELECT 1
    FROM message_locations
    WHERE account_id = ? AND message_id = ? AND lower(raw_content_hash) = ?
)`, strings.TrimSpace(accountID), strings.TrimSpace(messageID), rawContentHash).Scan(&matchingRaw); err != nil {
		return false, fmt.Errorf("corroborate RFC Message-ID with raw content hash: %w", err)
	}
	return matchingRaw == 1, nil
}

func rfcIdentityCandidates(ctx context.Context, tx *sql.Tx, accountID, rfcMessageID string, limit int) ([]identityCandidate, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT m.message_id,
       (SELECT count(*) FROM message_locations AS l WHERE l.message_id = m.message_id),
       COALESCE((SELECT location_id FROM message_locations AS l WHERE l.message_id = m.message_id ORDER BY present DESC, location_id LIMIT 1), '')
FROM messages AS m
WHERE m.account_id = ? AND lower(m.rfc_message_id) = ?
ORDER BY m.message_id
LIMIT ?`, strings.TrimSpace(accountID), strings.TrimSpace(rfcMessageID), limit)
	if err != nil {
		return nil, fmt.Errorf("resolve message by RFC Message-ID: %w", err)
	}
	defer rows.Close()
	candidates := make([]identityCandidate, 0, limit)
	for rows.Next() {
		var candidate identityCandidate
		if err := rows.Scan(&candidate.MessageID, &candidate.Locations, &candidate.LocationID); err != nil {
			return nil, fmt.Errorf("read RFC Message-ID candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resolve message by RFC Message-ID: %w", err)
	}
	return candidates, nil
}

func contentIdentityCandidates(ctx context.Context, tx *sql.Tx, accountID, fingerprint string, limit int) ([]identityCandidate, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT e.message_id,
       (SELECT count(*) FROM message_locations AS l WHERE l.message_id = e.message_id),
       COALESCE((SELECT location_id FROM message_locations AS l WHERE l.message_id = e.message_id ORDER BY present DESC, location_id LIMIT 1), '')
FROM message_identity_evidence AS e
WHERE e.account_id = ? AND e.kind = 'content_fingerprint' AND e.value = ?
GROUP BY e.message_id
ORDER BY e.message_id
LIMIT ?`, strings.TrimSpace(accountID), strings.TrimSpace(fingerprint), limit)
	if err != nil {
		return nil, fmt.Errorf("resolve message by content fingerprint: %w", err)
	}
	defer rows.Close()
	candidates := make([]identityCandidate, 0, limit)
	for rows.Next() {
		var candidate identityCandidate
		if err := rows.Scan(&candidate.MessageID, &candidate.Locations, &candidate.LocationID); err != nil {
			return nil, fmt.Errorf("read content fingerprint candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resolve message by content fingerprint: %w", err)
	}
	return candidates, nil
}

func persistRecordIdentity(ctx context.Context, tx *sql.Tx, record maildomain.IngestRecord, actualLocationID string, resolution identityResolution, timestamp string) error {
	if len(resolution.Conflicts) == 0 {
		if rfcValue := maildomain.NormalizeMessageID(record.Parsed.RFCMessageID); rfcValue != "" {
			if err := persistIdentityEvidence(ctx, tx, record.AccountID, record.MessageID, maildomain.IdentityRFCMessageID, rfcValue, maildomain.IdentityStrong, actualLocationID, timestamp); err != nil {
				return err
			}
		}
	}
	if providerValue := maildomain.ProviderLocationIdentity(record.FolderID, record.UIDValidity, record.UID); providerValue != "" {
		if err := persistIdentityEvidence(ctx, tx, record.AccountID, record.MessageID, maildomain.IdentityProviderLocation, providerValue, maildomain.IdentityStrong, actualLocationID, timestamp); err != nil {
			return err
		}
	}
	if fingerprint := strings.TrimSpace(record.ContentFingerprint); fingerprint != "" && len(resolution.Conflicts) == 0 {
		if err := persistIdentityEvidence(ctx, tx, record.AccountID, record.MessageID, maildomain.IdentityContentFingerprint, fingerprint, maildomain.IdentityGuarded, actualLocationID, timestamp); err != nil {
			return err
		}
	}
	for _, conflict := range resolution.Conflicts {
		if conflict.ObservedLocationID == record.LocationID {
			conflict.ObservedLocationID = actualLocationID
		}
		if err := persistDedupeConflict(ctx, tx, record.AccountID, conflict, timestamp); err != nil {
			return err
		}
	}
	return nil
}

func persistIdentityEvidence(ctx context.Context, tx *sql.Tx, accountID, messageID string, kind maildomain.IdentityKind, value string, strength maildomain.IdentityStrength, sourceLocationID, timestamp string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	evidenceID := stableIdentityID("evd", accountID, messageID, string(kind), value, sourceLocationID)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO message_identity_evidence (
    evidence_id, account_id, message_id, kind, value, strength,
    source_location_id, first_seen_at, last_seen_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(account_id, message_id, kind, value, source_location_id) DO UPDATE SET
    strength = excluded.strength,
    last_seen_at = excluded.last_seen_at`,
		evidenceID, accountID, messageID, kind, value, strength,
		strings.TrimSpace(sourceLocationID), timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("persist message identity evidence: %w", err)
	}
	return nil
}

func persistDedupeConflict(ctx context.Context, tx *sql.Tx, accountID string, conflict pendingIdentityConflict, timestamp string) error {
	conflictID := stableIdentityID(
		"dcf", accountID, string(conflict.Kind), conflict.Value,
		conflict.ExistingMessageID, conflict.ObservedMessageID,
		conflict.ExistingLocationID, conflict.ObservedLocationID, conflict.Reason,
	)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO message_dedupe_conflicts (
    conflict_id, account_id, evidence_kind, evidence_value,
    existing_message_id, observed_message_id,
    existing_location_id, observed_location_id, reason, status, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', ?)
ON CONFLICT(
    account_id, evidence_kind, evidence_value,
    existing_message_id, observed_message_id,
    existing_location_id, observed_location_id, reason
) DO NOTHING`,
		conflictID, accountID, conflict.Kind, conflict.Value,
		conflict.ExistingMessageID, conflict.ObservedMessageID,
		conflict.ExistingLocationID, conflict.ObservedLocationID,
		conflict.Reason, timestamp,
	); err != nil {
		return fmt.Errorf("persist message dedupe conflict: %w", err)
	}
	return nil
}

func stableIdentityID(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + "_" + hex.EncodeToString(sum[:16])
}

func checkpointInTx(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, accountID, folderID string) (maildomain.Checkpoint, error) {
	return scanCheckpoint(queryer.QueryRowContext(ctx, checkpointColumns+` WHERE account_id = ? AND folder_id = ?`, accountID, folderID))
}

func scanCheckpoint(row rowScanner) (maildomain.Checkpoint, error) {
	var (
		value                            maildomain.Checkpoint
		bootstrapComplete, moreAvailable int
		state                            string
		lastAttemptAt, lastSuccessAt     sql.NullString
		createdAt, updatedAt             string
	)
	if err := row.Scan(
		&value.ID, &value.AccountID, &value.FolderID, &value.UIDValidity,
		&value.LastCommittedUID, &value.LastScannedUID, &bootstrapComplete,
		&state, &value.LastErrorCode, &moreAvailable, &lastAttemptAt,
		&lastSuccessAt, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.Checkpoint{}, maildomain.ErrCheckpointNotFound
		}
		return maildomain.Checkpoint{}, fmt.Errorf("read sync checkpoint: %w", err)
	}
	value.BootstrapComplete = bootstrapComplete == 1
	value.State = maildomain.CheckpointState(state)
	value.MoreAvailable = moreAvailable == 1
	var err error
	value.LastAttemptAt, err = parseNullableTime(lastAttemptAt)
	if err != nil {
		return maildomain.Checkpoint{}, fmt.Errorf("parse checkpoint attempt time: %w", err)
	}
	value.LastSuccessAt, err = parseNullableTime(lastSuccessAt)
	if err != nil {
		return maildomain.Checkpoint{}, fmt.Errorf("parse checkpoint success time: %w", err)
	}
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return maildomain.Checkpoint{}, fmt.Errorf("parse checkpoint creation time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.Checkpoint{}, fmt.Errorf("parse checkpoint update time: %w", err)
	}
	return value, nil
}

func scanMessage(row rowScanner) (maildomain.Message, error) {
	return scanMessageWithExtra(row)
}

func scanMessageWithExtra(row rowScanner, extra ...any) (maildomain.Message, error) {
	var (
		value                                                  maildomain.Message
		direction, dateSource                                  string
		headerDate, internalDate, sortAt                       sql.NullString
		createdAt, updatedAt                                   string
		sentReply, pendingAnswer, unsupportedAnswer, forwarded int
	)
	targets := []any{
		&value.ID, &value.AccountID, &value.RFCMessageID, &value.InReplyTo,
		&direction, &value.From.DisplayName, &value.From.Email,
		&value.ReplyTo.DisplayName, &value.ReplyTo.Email, &value.Subject,
		&value.Snippet, &headerDate, &internalDate, &sortAt, &dateSource,
		&value.AttachmentCount, &value.ContentLocationID, &createdAt, &updatedAt,
	}
	targets = targets[:len(targets)-2]
	targets = append(targets,
		&sentReply, &pendingAnswer, &unsupportedAnswer, &forwarded,
		&createdAt, &updatedAt,
	)
	targets = append(targets, extra...)
	if err := row.Scan(targets...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.Message{}, maildomain.ErrMessageNotFound
		}
		return maildomain.Message{}, fmt.Errorf("read message: %w", err)
	}
	value.Direction = maildomain.Direction(direction)
	value.DateSource = maildomain.DateSource(dateSource)
	value.Replied = sentReply == 1
	value.Forwarded = forwarded == 1
	switch {
	case pendingAnswer == 1:
		value.ReplyState = "provider_pending"
	case unsupportedAnswer == 1:
		value.ReplyState = "provider_unsupported"
	case sentReply == 1:
		value.ReplyState = "replied"
	default:
		value.ReplyState = "none"
	}
	var err error
	value.HeaderDate, err = parseNullableTime(headerDate)
	if err != nil {
		return maildomain.Message{}, fmt.Errorf("parse message header date: %w", err)
	}
	value.InternalDate, err = parseNullableTime(internalDate)
	if err != nil {
		return maildomain.Message{}, fmt.Errorf("parse message internal date: %w", err)
	}
	value.SortAt, err = parseNullableTime(sortAt)
	if err != nil {
		return maildomain.Message{}, fmt.Errorf("parse message sort time: %w", err)
	}
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return maildomain.Message{}, fmt.Errorf("parse message creation time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.Message{}, fmt.Errorf("parse message update time: %w", err)
	}
	return value, nil
}

func scanAttachment(row rowScanner) (maildomain.Attachment, error) {
	var (
		value           maildomain.Attachment
		mediaParamsJSON string
		inline          int
		availability    string
	)
	if err := row.Scan(
		&value.ID, &value.MessageID, &value.PartID, &value.Ordinal, &value.PartPath,
		&value.Filename, &value.MediaType, &mediaParamsJSON, &value.Disposition,
		&value.ContentID, &inline, &value.ContentHash, &value.StorageKey,
		&value.ByteSize, &availability, &value.ErrorCode,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.Attachment{}, maildomain.ErrAttachmentNotFound
		}
		return maildomain.Attachment{}, fmt.Errorf("read attachment: %w", err)
	}
	value.Inline = inline == 1
	value.Availability = maildomain.AttachmentAvailability(availability)
	if err := json.Unmarshal([]byte(mediaParamsJSON), &value.MediaParams); err != nil {
		return maildomain.Attachment{}, fmt.Errorf("parse attachment media parameters: %w", err)
	}
	return value, nil
}

func (repository *MailRepository) loadMessageDetails(ctx context.Context, value *maildomain.Message) error {
	recipientRows, err := repository.db.QueryContext(ctx, `
SELECT kind, display_name, email_address
FROM message_recipients WHERE message_id = ?
ORDER BY CASE kind WHEN 'to' THEN 0 WHEN 'cc' THEN 1 ELSE 2 END, ordinal`, value.ID)
	if err != nil {
		return fmt.Errorf("read message recipients: %w", err)
	}
	for recipientRows.Next() {
		var kind string
		var address maildomain.Address
		if err := recipientRows.Scan(&kind, &address.DisplayName, &address.Email); err != nil {
			recipientRows.Close()
			return fmt.Errorf("read message recipient: %w", err)
		}
		switch maildomain.RecipientKind(kind) {
		case maildomain.RecipientTo:
			value.To = append(value.To, address)
		case maildomain.RecipientCC:
			value.CC = append(value.CC, address)
		case maildomain.RecipientBCC:
			value.BCC = append(value.BCC, address)
		}
	}
	if err := recipientRows.Err(); err != nil {
		recipientRows.Close()
		return fmt.Errorf("read message recipients: %w", err)
	}
	if err := recipientRows.Close(); err != nil {
		return fmt.Errorf("close message recipients: %w", err)
	}

	referenceRows, err := repository.db.QueryContext(ctx, `SELECT reference FROM message_references WHERE message_id = ? ORDER BY ordinal`, value.ID)
	if err != nil {
		return fmt.Errorf("read message references: %w", err)
	}
	for referenceRows.Next() {
		var reference string
		if err := referenceRows.Scan(&reference); err != nil {
			referenceRows.Close()
			return fmt.Errorf("read message reference: %w", err)
		}
		value.References = append(value.References, reference)
	}
	if err := referenceRows.Err(); err != nil {
		referenceRows.Close()
		return fmt.Errorf("read message references: %w", err)
	}
	if err := referenceRows.Close(); err != nil {
		return fmt.Errorf("close message references: %w", err)
	}

	locationRows, err := repository.db.QueryContext(ctx, locationColumns+`
WHERE message_id = ?
ORDER BY present DESC, folder_id, uid`, value.ID)
	if err != nil {
		return fmt.Errorf("read message locations: %w", err)
	}
	for locationRows.Next() {
		location, scanErr := scanLocation(locationRows)
		if scanErr != nil {
			locationRows.Close()
			return scanErr
		}
		value.Locations = append(value.Locations, location)
		if location.Present && location.Flags.Eligible() {
			value.Unread = value.Unread || locationCountsUnread(location)
			value.Flagged = value.Flagged || location.Flags.Flagged
			value.Answered = value.Answered || location.Flags.Answered
		}
	}
	if err := locationRows.Err(); err != nil {
		locationRows.Close()
		return fmt.Errorf("read message locations: %w", err)
	}
	if err := locationRows.Close(); err != nil {
		return fmt.Errorf("close message locations: %w", err)
	}

	if value.Answered {
		value.Replied = true
		value.ReplyState = "answered"
	}

	identityRows, err := repository.db.QueryContext(ctx, `
SELECT evidence_id, account_id, message_id, kind, value, strength,
       source_location_id, first_seen_at, last_seen_at
FROM message_identity_evidence
WHERE message_id = ?
ORDER BY CASE kind
    WHEN 'rfc_message_id' THEN 0
    WHEN 'generated_message_id' THEN 1
    WHEN 'provider_location' THEN 2
    ELSE 3 END,
    value, source_location_id
LIMIT 100`, value.ID)
	if err != nil {
		return fmt.Errorf("read message identity evidence: %w", err)
	}
	for identityRows.Next() {
		var evidence maildomain.IdentityEvidence
		var kind, strength, firstSeenAt, lastSeenAt string
		if err := identityRows.Scan(
			&evidence.ID, &evidence.AccountID, &evidence.MessageID, &kind,
			&evidence.Value, &strength, &evidence.SourceLocationID,
			&firstSeenAt, &lastSeenAt,
		); err != nil {
			identityRows.Close()
			return fmt.Errorf("read message identity evidence: %w", err)
		}
		evidence.Kind = maildomain.IdentityKind(kind)
		evidence.Strength = maildomain.IdentityStrength(strength)
		evidence.FirstSeenAt, err = time.Parse(time.RFC3339Nano, firstSeenAt)
		if err != nil {
			identityRows.Close()
			return fmt.Errorf("parse identity first-seen time: %w", err)
		}
		evidence.LastSeenAt, err = time.Parse(time.RFC3339Nano, lastSeenAt)
		if err != nil {
			identityRows.Close()
			return fmt.Errorf("parse identity last-seen time: %w", err)
		}
		value.Identity = append(value.Identity, evidence)
	}
	if err := identityRows.Err(); err != nil {
		identityRows.Close()
		return fmt.Errorf("read message identity evidence: %w", err)
	}
	if err := identityRows.Close(); err != nil {
		return fmt.Errorf("close message identity evidence: %w", err)
	}

	conflictRows, err := repository.db.QueryContext(ctx, `
SELECT conflict_id, account_id, evidence_kind, evidence_value,
       existing_message_id, observed_message_id,
       existing_location_id, observed_location_id, reason, status, created_at
FROM message_dedupe_conflicts
WHERE existing_message_id = ? OR observed_message_id = ?
ORDER BY created_at, conflict_id
LIMIT 100`, value.ID, value.ID)
	if err != nil {
		return fmt.Errorf("read message dedupe conflicts: %w", err)
	}
	for conflictRows.Next() {
		var conflict maildomain.DedupeConflict
		var kind, createdAt string
		if err := conflictRows.Scan(
			&conflict.ID, &conflict.AccountID, &kind, &conflict.EvidenceValue,
			&conflict.ExistingMessageID, &conflict.ObservedMessageID,
			&conflict.ExistingLocationID, &conflict.ObservedLocationID,
			&conflict.Reason, &conflict.Status, &createdAt,
		); err != nil {
			conflictRows.Close()
			return fmt.Errorf("read message dedupe conflict: %w", err)
		}
		conflict.EvidenceKind = maildomain.IdentityKind(kind)
		conflict.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			conflictRows.Close()
			return fmt.Errorf("parse dedupe conflict time: %w", err)
		}
		value.DedupeConflicts = append(value.DedupeConflicts, conflict)
	}
	if err := conflictRows.Err(); err != nil {
		conflictRows.Close()
		return fmt.Errorf("read message dedupe conflicts: %w", err)
	}
	if err := conflictRows.Close(); err != nil {
		return fmt.Errorf("close message dedupe conflicts: %w", err)
	}
	return nil
}

func (repository *MailRepository) loadMessageContent(ctx context.Context, value *maildomain.Message) error {
	var (
		headersJSON, diagnosticsJSON       string
		textPathsJSON, htmlPathsJSON       string
		sourceLocationID                   string
		parsedComplete, attachmentsIndexed int
	)
	err := repository.db.QueryRowContext(ctx, `
SELECT headers_json, diagnostics_json, text_paths_json, html_paths_json,
       parsed_complete, attachments_indexed, source_location_id
FROM message_content WHERE message_id = ?`, value.ID).Scan(
		&headersJSON, &diagnosticsJSON, &textPathsJSON, &htmlPathsJSON,
		&parsedComplete, &attachmentsIndexed, &sourceLocationID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read message content metadata: %w", err)
	}
	content := maildomain.NormalizedContent{}
	if err := json.Unmarshal([]byte(headersJSON), &content.Headers); err != nil {
		return fmt.Errorf("parse message headers: %w", err)
	}
	if err := json.Unmarshal([]byte(diagnosticsJSON), &content.Diagnostics); err != nil {
		return fmt.Errorf("parse message diagnostics: %w", err)
	}
	if err := json.Unmarshal([]byte(textPathsJSON), &content.TextPaths); err != nil {
		return fmt.Errorf("parse message text paths: %w", err)
	}
	if err := json.Unmarshal([]byte(htmlPathsJSON), &content.HTMLPaths); err != nil {
		return fmt.Errorf("parse message HTML paths: %w", err)
	}
	partRows, err := repository.db.QueryContext(ctx, `
SELECT part_id, ordinal, path, parent_path, media_type, media_params_json, charset, transfer_encoding,
       disposition, filename, content_id, inline, attachment,
       embedded_message, decoded_size, diagnostics_json
FROM message_parts WHERE message_id = ? ORDER BY ordinal`, value.ID)
	if err != nil {
		return fmt.Errorf("read MIME parts: %w", err)
	}
	for partRows.Next() {
		var (
			part                                 maildomain.MIMEPart
			inline, attachment, embedded         int
			mediaParamsJSON, partDiagnosticsJSON string
		)
		if err := partRows.Scan(
			&part.ID, &part.Ordinal, &part.Path, &part.ParentPath, &part.MediaType, &mediaParamsJSON, &part.Charset,
			&part.TransferEncoding, &part.Disposition, &part.Filename, &part.ContentID,
			&inline, &attachment, &embedded, &part.DecodedSize, &partDiagnosticsJSON,
		); err != nil {
			partRows.Close()
			return fmt.Errorf("read MIME part: %w", err)
		}
		part.Inline = inline == 1
		part.Attachment = attachment == 1
		part.EmbeddedMessage = embedded == 1
		if err := json.Unmarshal([]byte(mediaParamsJSON), &part.MediaParams); err != nil {
			partRows.Close()
			return fmt.Errorf("parse MIME part media parameters: %w", err)
		}
		if err := json.Unmarshal([]byte(partDiagnosticsJSON), &part.Diagnostics); err != nil {
			partRows.Close()
			return fmt.Errorf("parse MIME part diagnostics: %w", err)
		}
		content.Parts = append(content.Parts, part)
	}
	if err := partRows.Err(); err != nil {
		partRows.Close()
		return fmt.Errorf("read MIME parts: %w", err)
	}
	if err := partRows.Close(); err != nil {
		return fmt.Errorf("close MIME parts: %w", err)
	}
	if parsedComplete == 0 && len(content.Diagnostics) == 0 {
		content.Diagnostics = append(content.Diagnostics, maildomain.ParseDiagnostic{Code: "partial"})
	}
	content.AttachmentsIndexed = attachmentsIndexed == 1
	value.ContentLocationID = sourceLocationID
	attachmentRows, err := repository.db.QueryContext(ctx, `
SELECT attachment_id, message_id, part_id, ordinal, part_path, filename,
       media_type, media_params_json, disposition, content_id, inline,
       content_hash, storage_key, byte_size, availability, error_code
FROM attachments WHERE message_id = ? ORDER BY ordinal`, value.ID)
	if err != nil {
		return fmt.Errorf("read attachments: %w", err)
	}
	for attachmentRows.Next() {
		var (
			attachment      maildomain.Attachment
			mediaParamsJSON string
			inline          int
			availability    string
		)
		if err := attachmentRows.Scan(
			&attachment.ID, &attachment.MessageID, &attachment.PartID, &attachment.Ordinal,
			&attachment.PartPath, &attachment.Filename, &attachment.MediaType, &mediaParamsJSON,
			&attachment.Disposition, &attachment.ContentID, &inline, &attachment.ContentHash,
			&attachment.StorageKey, &attachment.ByteSize, &availability, &attachment.ErrorCode,
		); err != nil {
			attachmentRows.Close()
			return fmt.Errorf("read attachment: %w", err)
		}
		attachment.Inline = inline == 1
		attachment.Availability = maildomain.AttachmentAvailability(availability)
		if err := json.Unmarshal([]byte(mediaParamsJSON), &attachment.MediaParams); err != nil {
			attachmentRows.Close()
			return fmt.Errorf("parse attachment media parameters: %w", err)
		}
		content.Attachments = append(content.Attachments, attachment)
	}
	if err := attachmentRows.Err(); err != nil {
		attachmentRows.Close()
		return fmt.Errorf("read attachments: %w", err)
	}
	if err := attachmentRows.Close(); err != nil {
		return fmt.Errorf("close attachments: %w", err)
	}
	value.Content = &content
	capability := maildomain.ParseUnsubscribeCapability(content.Headers)
	if capability.Available || capability.OneClickDeclared {
		value.Unsubscribe = &capability
	}
	return nil
}

func scanLocation(row rowScanner) (maildomain.Location, error) {
	var (
		value                                   maildomain.Location
		seen, flagged, answered, draft, deleted int
		unknownFlagsJSON, modSequence           string
		flagsCheckedAt, internalDate            sql.NullString
		present                                 int
		createdAt, updatedAt                    string
	)
	if err := row.Scan(
		&value.ID, &value.MessageID, &value.AccountID, &value.FolderID,
		&value.UIDValidity, &value.UID, &value.FolderKind,
		&seen, &flagged, &answered, &draft, &deleted,
		&unknownFlagsJSON, &modSequence, &flagsCheckedAt, &internalDate,
		&value.RFC822Size, &value.RawContentHash, &value.RawStorageKey,
		&value.RawByteSize, &present, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.Location{}, maildomain.ErrLocationNotFound
		}
		return maildomain.Location{}, fmt.Errorf("read message location: %w", err)
	}
	value.Flags.Seen = seen == 1
	value.Flags.Flagged = flagged == 1
	value.Flags.Answered = answered == 1
	value.Flags.Draft = draft == 1
	value.Flags.Deleted = deleted == 1
	if err := json.Unmarshal([]byte(unknownFlagsJSON), &value.Flags.Unknown); err != nil {
		return maildomain.Location{}, fmt.Errorf("parse unknown message flags: %w", err)
	}
	value.Flags.Unknown = maildomain.ParseLocationFlags(value.Flags.Unknown).Unknown
	var err error
	value.ModSequence, err = parseModSequence(modSequence)
	if err != nil {
		return maildomain.Location{}, fmt.Errorf("parse message location mod-sequence: %w", err)
	}
	value.FlagsCheckedAt, err = parseNullableTime(flagsCheckedAt)
	if err != nil {
		return maildomain.Location{}, fmt.Errorf("parse message location flag check time: %w", err)
	}
	value.InternalDate, err = parseNullableTime(internalDate)
	if err != nil {
		return maildomain.Location{}, fmt.Errorf("parse message location date: %w", err)
	}
	value.Present = present == 1
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return maildomain.Location{}, fmt.Errorf("parse message location creation time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.Location{}, fmt.Errorf("parse message location update time: %w", err)
	}
	return value, nil
}

func formatModSequence(value uint64) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprintf("%d", value)
}

func parseModSequence(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	return strconv.ParseUint(value, 10, 64)
}

func nullableTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func nullableTimeValue(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}
