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

type MailRepository struct {
	db *sql.DB
}

const checkpointColumns = `
SELECT checkpoint_id, account_id, folder_id, uid_validity, last_committed_uid,
       last_scanned_uid, bootstrap_complete, state, last_error_code,
       more_available, last_attempt_at, last_success_at, created_at, updated_at
FROM sync_checkpoints`

const messageColumns = `
SELECT m.message_id, m.account_id, m.rfc_message_id, m.in_reply_to,
       m.direction, m.from_name, m.from_email, m.reply_to_name,
       m.reply_to_email, m.subject, m.snippet, m.header_date,
       m.internal_date, m.sort_at, m.date_source, m.attachment_count,
       m.created_at, m.updated_at
FROM messages AS m`

func (repository *MailRepository) Checkpoint(ctx context.Context, accountID, folderID string) (maildomain.Checkpoint, error) {
	row := repository.db.QueryRowContext(ctx, checkpointColumns+` WHERE account_id = ? AND folder_id = ?`, accountID, folderID)
	return scanCheckpoint(row)
}

func (repository *MailRepository) Checkpoints(ctx context.Context, accountID string, limit int) ([]maildomain.Checkpoint, bool, error) {
	if limit < 1 {
		return nil, false, errors.New("list checkpoints: limit must be positive")
	}
	rows, err := repository.db.QueryContext(ctx, checkpointColumns+`
WHERE account_id = ?
ORDER BY state = 'repair_required' DESC, state = 'failed' DESC, updated_at DESC, folder_id
LIMIT ?`, accountID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list checkpoints: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.Checkpoint, 0, limit+1)
	for rows.Next() {
		value, scanErr := scanCheckpoint(rows)
		if scanErr != nil {
			return nil, false, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list checkpoints: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	return values, more, nil
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

	created, updated := 0, 0
	timestamp := page.AttemptedAt.UTC().Format(time.RFC3339Nano)
	for _, record := range page.Records {
		if err := validateIngestRecord(page, record); err != nil {
			return maildomain.CommitResult{}, err
		}
		wasCreated, ingestErr := upsertInboxRecord(ctx, tx, record, timestamp)
		if ingestErr != nil {
			return maildomain.CommitResult{}, ingestErr
		}
		if wasCreated {
			created++
		} else {
			updated++
		}
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
	return maildomain.CommitResult{Created: created, Updated: updated, Checkpoint: checkpoint}, nil
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

func (repository *MailRepository) Inbox(ctx context.Context, accountID, folderID string, limit int) ([]maildomain.Message, bool, error) {
	if limit < 1 {
		return nil, false, errors.New("list inbox: limit must be positive")
	}
	rows, err := repository.db.QueryContext(ctx, messageColumns+`
JOIN message_locations AS l ON l.message_id = m.message_id
WHERE m.account_id = ? AND l.account_id = ? AND l.folder_id = ? AND l.present = 1
ORDER BY m.sort_at IS NULL, m.sort_at DESC, l.uid DESC, m.message_id DESC
LIMIT ?`, accountID, accountID, folderID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list inbox: %w", err)
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
		return nil, false, fmt.Errorf("list inbox: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, false, fmt.Errorf("close inbox rows: %w", err)
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
	return values, more, nil
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
	return value, nil
}

func (repository *MailRepository) SaveMessageContent(ctx context.Context, accountID, messageID string, content maildomain.NormalizedContent, now time.Time) error {
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
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM message_content WHERE message_id = ?`, messageID).Scan(&exists); err != nil {
		return fmt.Errorf("inspect existing message content: %w", err)
	}
	if exists == 0 {
		if err := upsertMessageContent(ctx, tx, messageID, content, now.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit message content save: %w", err)
	}
	return nil
}

func validateIngestRecord(page maildomain.CommitPage, record maildomain.IngestRecord) error {
	if record.AccountID != page.AccountID || record.FolderID != page.FolderID || record.UIDValidity != page.UIDValidity {
		return errors.New("commit inbox page: record scope does not match page")
	}
	if strings.TrimSpace(record.MessageID) == "" || strings.TrimSpace(record.LocationID) == "" || record.UID == 0 {
		return errors.New("commit inbox page: record identity is incomplete")
	}
	if record.UID > page.LastCommittedUID || record.UID > page.LastScannedUID {
		return errors.New("commit inbox page: record UID exceeds checkpoint frontier")
	}
	if len(record.RawContentHash) != 64 || strings.TrimSpace(record.RawStorageKey) == "" || record.RawByteSize < 0 {
		return errors.New("commit inbox page: raw blob metadata is invalid")
	}
	if record.Content != nil {
		paths := make(map[string]struct{}, len(record.Content.Parts))
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
		}
	}
	return nil
}

func upsertInboxRecord(ctx context.Context, tx *sql.Tx, record maildomain.IngestRecord, timestamp string) (bool, error) {
	if _, err := tx.ExecContext(ctx, `
INSERT INTO raw_blobs (content_hash, storage_key, byte_size, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(content_hash) DO NOTHING`,
		record.RawContentHash, record.RawStorageKey, record.RawByteSize, timestamp,
	); err != nil {
		return false, fmt.Errorf("persist raw blob metadata: %w", err)
	}
	var storedKey string
	var storedSize int64
	if err := tx.QueryRowContext(ctx, `SELECT storage_key, byte_size FROM raw_blobs WHERE content_hash = ?`, record.RawContentHash).Scan(&storedKey, &storedSize); err != nil {
		return false, fmt.Errorf("verify raw blob metadata: %w", err)
	}
	if storedKey != record.RawStorageKey || storedSize != record.RawByteSize {
		return false, errors.New("persist raw blob metadata: content-address collision")
	}

	messageID, created, err := resolveMessageIdentity(ctx, tx, record)
	if err != nil {
		return false, err
	}
	record.MessageID = messageID
	internalDate, sortAt, dateSource := maildomain.MessageTimes(record.Parsed, record.InternalDate)
	headerDate := nullableTime(record.Parsed.HeaderDate)
	internalDateValue := nullableTime(internalDate)
	sortAtValue := nullableTime(sortAt)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO messages (
    message_id, account_id, rfc_message_id, in_reply_to, direction,
    from_name, from_email, reply_to_name, reply_to_email, subject, snippet,
    header_date, internal_date, sort_at, date_source, attachment_count,
    created_at, updated_at
) VALUES (?, ?, ?, ?, 'inbound', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(message_id) DO UPDATE SET
    rfc_message_id = excluded.rfc_message_id,
    in_reply_to = excluded.in_reply_to,
    from_name = excluded.from_name,
    from_email = excluded.from_email,
    reply_to_name = excluded.reply_to_name,
    reply_to_email = excluded.reply_to_email,
    subject = excluded.subject,
    snippet = excluded.snippet,
    header_date = excluded.header_date,
    internal_date = excluded.internal_date,
    sort_at = excluded.sort_at,
    date_source = excluded.date_source,
    attachment_count = excluded.attachment_count,
    updated_at = excluded.updated_at`,
		record.MessageID, record.AccountID, record.Parsed.RFCMessageID,
		record.Parsed.InReplyTo, record.Parsed.From.DisplayName, record.Parsed.From.Email,
		record.Parsed.ReplyTo.DisplayName, record.Parsed.ReplyTo.Email,
		record.Parsed.Subject, record.Parsed.Snippet, headerDate, internalDateValue,
		sortAtValue, dateSource, record.Parsed.AttachmentCount, timestamp, timestamp,
	); err != nil {
		return false, fmt.Errorf("persist message: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_recipients WHERE message_id = ?`, record.MessageID); err != nil {
		return false, fmt.Errorf("replace message recipients: %w", err)
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
				return false, fmt.Errorf("persist message recipient: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_references WHERE message_id = ?`, record.MessageID); err != nil {
		return false, fmt.Errorf("replace message references: %w", err)
	}
	for index, reference := range record.Parsed.References {
		if _, err := tx.ExecContext(ctx, `INSERT INTO message_references (message_id, ordinal, reference) VALUES (?, ?, ?)`, record.MessageID, index, reference); err != nil {
			return false, fmt.Errorf("persist message reference: %w", err)
		}
	}
	if record.Content != nil {
		if err := upsertMessageContent(ctx, tx, record.MessageID, *record.Content, timestamp); err != nil {
			return false, err
		}
	}
	flagsJSON, err := json.Marshal(maildomain.NormalizeFlags(record.Flags))
	if err != nil {
		return false, fmt.Errorf("encode message flags: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO message_locations (
    location_id, message_id, account_id, folder_id, uid_validity, uid,
    flags_json, internal_date, rfc822_size, raw_content_hash, raw_storage_key,
    raw_byte_size, present, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
ON CONFLICT(account_id, folder_id, uid_validity, uid) DO UPDATE SET
    message_id = excluded.message_id,
    flags_json = excluded.flags_json,
    internal_date = excluded.internal_date,
    rfc822_size = excluded.rfc822_size,
    raw_content_hash = excluded.raw_content_hash,
    raw_storage_key = excluded.raw_storage_key,
    raw_byte_size = excluded.raw_byte_size,
    present = 1,
    updated_at = excluded.updated_at`,
		record.LocationID, record.MessageID, record.AccountID, record.FolderID,
		record.UIDValidity, record.UID, string(flagsJSON), nullableTimeValue(record.InternalDate),
		record.RFC822Size, record.RawContentHash, record.RawStorageKey,
		record.RawByteSize, timestamp, timestamp,
	); err != nil {
		return false, fmt.Errorf("persist message location: %w", err)
	}
	return created, nil
}

func upsertMessageContent(ctx context.Context, tx *sql.Tx, messageID string, content maildomain.NormalizedContent, timestamp string) error {
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
    html_paths_json, parsed_complete, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(message_id) DO UPDATE SET
    headers_json = excluded.headers_json,
    diagnostics_json = excluded.diagnostics_json,
    text_paths_json = excluded.text_paths_json,
    html_paths_json = excluded.html_paths_json,
    parsed_complete = excluded.parsed_complete,
    updated_at = excluded.updated_at`,
		messageID, string(headersJSON), string(diagnosticsJSON), string(textPathsJSON),
		string(htmlPathsJSON), boolInt(parsedComplete), timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("persist message content metadata: %w", err)
	}

	existing := map[string]string{}
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
		existing[path] = partID
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close existing MIME part IDs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_parts WHERE message_id = ?`, messageID); err != nil {
		return fmt.Errorf("replace MIME parts: %w", err)
	}
	for _, part := range content.Parts {
		partID := strings.TrimSpace(part.ID)
		if stableID := existing[part.Path]; stableID != "" {
			partID = stableID
		}
		partDiagnostics, err := json.Marshal(part.Diagnostics)
		if err != nil {
			return fmt.Errorf("encode MIME part diagnostics: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO message_parts (
    part_id, message_id, ordinal, path, parent_path, media_type, charset,
    transfer_encoding, disposition, filename, content_id, inline,
    attachment, embedded_message, decoded_size, diagnostics_json,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			partID, messageID, part.Ordinal, part.Path, part.ParentPath, part.MediaType, part.Charset,
			part.TransferEncoding, part.Disposition, part.Filename, part.ContentID,
			boolInt(part.Inline), boolInt(part.Attachment), boolInt(part.EmbeddedMessage),
			part.DecodedSize, string(partDiagnostics), timestamp, timestamp,
		); err != nil {
			return fmt.Errorf("persist MIME part: %w", err)
		}
	}
	return nil
}

func resolveMessageIdentity(ctx context.Context, tx *sql.Tx, record maildomain.IngestRecord) (string, bool, error) {
	var messageID string
	err := tx.QueryRowContext(ctx, `
SELECT message_id
FROM message_locations
WHERE account_id = ? AND folder_id = ? AND uid_validity = ? AND uid = ?`,
		record.AccountID, record.FolderID, record.UIDValidity, record.UID,
	).Scan(&messageID)
	if err == nil {
		return messageID, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, fmt.Errorf("resolve message by provider location: %w", err)
	}
	if rfcMessageID := strings.TrimSpace(record.Parsed.RFCMessageID); rfcMessageID != "" {
		err = tx.QueryRowContext(ctx, `
SELECT message_id
FROM messages
WHERE account_id = ? AND lower(rfc_message_id) = lower(?)`, record.AccountID, rfcMessageID).Scan(&messageID)
		if err == nil {
			return messageID, false, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", false, fmt.Errorf("resolve message by RFC Message-ID: %w", err)
		}
	}
	messageID = strings.TrimSpace(record.MessageID)
	if messageID == "" {
		return "", false, errors.New("resolve message identity: proposed message ID is required")
	}
	return messageID, true, nil
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
	var (
		value                            maildomain.Message
		direction, dateSource            string
		headerDate, internalDate, sortAt sql.NullString
		createdAt, updatedAt             string
	)
	if err := row.Scan(
		&value.ID, &value.AccountID, &value.RFCMessageID, &value.InReplyTo,
		&direction, &value.From.DisplayName, &value.From.Email,
		&value.ReplyTo.DisplayName, &value.ReplyTo.Email, &value.Subject,
		&value.Snippet, &headerDate, &internalDate, &sortAt, &dateSource,
		&value.AttachmentCount, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.Message{}, maildomain.ErrMessageNotFound
		}
		return maildomain.Message{}, fmt.Errorf("read message: %w", err)
	}
	value.Direction = maildomain.Direction(direction)
	value.DateSource = maildomain.DateSource(dateSource)
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
	if err := referenceRows.Close(); err != nil {
		return fmt.Errorf("close message references: %w", err)
	}

	locationRows, err := repository.db.QueryContext(ctx, `
SELECT location_id, message_id, account_id, folder_id, uid_validity, uid,
       flags_json, internal_date, rfc822_size, raw_content_hash,
       raw_storage_key, raw_byte_size, present, created_at, updated_at
FROM message_locations WHERE message_id = ?
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
		if location.Present {
			value.Unread = !maildomain.HasFlag(location.Flags, `\Seen`)
			value.Flagged = maildomain.HasFlag(location.Flags, `\Flagged`)
		}
	}
	if err := locationRows.Close(); err != nil {
		return fmt.Errorf("close message locations: %w", err)
	}
	return nil
}

func (repository *MailRepository) loadMessageContent(ctx context.Context, value *maildomain.Message) error {
	var (
		headersJSON, diagnosticsJSON string
		textPathsJSON, htmlPathsJSON string
		parsedComplete               int
	)
	err := repository.db.QueryRowContext(ctx, `
SELECT headers_json, diagnostics_json, text_paths_json, html_paths_json, parsed_complete
FROM message_content WHERE message_id = ?`, value.ID).Scan(
		&headersJSON, &diagnosticsJSON, &textPathsJSON, &htmlPathsJSON, &parsedComplete,
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
SELECT part_id, ordinal, path, parent_path, media_type, charset, transfer_encoding,
       disposition, filename, content_id, inline, attachment,
       embedded_message, decoded_size, diagnostics_json
FROM message_parts WHERE message_id = ? ORDER BY ordinal`, value.ID)
	if err != nil {
		return fmt.Errorf("read MIME parts: %w", err)
	}
	for partRows.Next() {
		var (
			part                         maildomain.MIMEPart
			inline, attachment, embedded int
			partDiagnosticsJSON          string
		)
		if err := partRows.Scan(
			&part.ID, &part.Ordinal, &part.Path, &part.ParentPath, &part.MediaType, &part.Charset,
			&part.TransferEncoding, &part.Disposition, &part.Filename, &part.ContentID,
			&inline, &attachment, &embedded, &part.DecodedSize, &partDiagnosticsJSON,
		); err != nil {
			partRows.Close()
			return fmt.Errorf("read MIME part: %w", err)
		}
		part.Inline = inline == 1
		part.Attachment = attachment == 1
		part.EmbeddedMessage = embedded == 1
		if err := json.Unmarshal([]byte(partDiagnosticsJSON), &part.Diagnostics); err != nil {
			partRows.Close()
			return fmt.Errorf("parse MIME part diagnostics: %w", err)
		}
		content.Parts = append(content.Parts, part)
	}
	if err := partRows.Close(); err != nil {
		return fmt.Errorf("close MIME parts: %w", err)
	}
	if parsedComplete == 0 && len(content.Diagnostics) == 0 {
		content.Diagnostics = append(content.Diagnostics, maildomain.ParseDiagnostic{Code: "partial"})
	}
	value.Content = &content
	return nil
}

func scanLocation(row rowScanner) (maildomain.Location, error) {
	var (
		value                maildomain.Location
		flagsJSON            string
		internalDate         sql.NullString
		present              int
		createdAt, updatedAt string
	)
	if err := row.Scan(
		&value.ID, &value.MessageID, &value.AccountID, &value.FolderID,
		&value.UIDValidity, &value.UID, &flagsJSON, &internalDate,
		&value.RFC822Size, &value.RawContentHash, &value.RawStorageKey,
		&value.RawByteSize, &present, &createdAt, &updatedAt,
	); err != nil {
		return maildomain.Location{}, fmt.Errorf("read message location: %w", err)
	}
	if err := json.Unmarshal([]byte(flagsJSON), &value.Flags); err != nil {
		return maildomain.Location{}, fmt.Errorf("parse message flags: %w", err)
	}
	var err error
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
