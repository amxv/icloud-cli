package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type DraftRepository struct{ db *sql.DB }

const draftColumns = `
SELECT draft_id, account_id, message_id, revision, sync_state,
       remote_location_id, previous_location_id, outbound_attempt_id,
       custom_headers_json, last_error_code, created_at, updated_at
FROM mail_drafts`

func (repository *DraftRepository) Save(ctx context.Context, input maildomain.DraftSave) (maildomain.Draft, error) {
	if strings.TrimSpace(input.DraftID) == "" || strings.TrimSpace(input.AccountID) == "" ||
		strings.TrimSpace(input.MessageID) == "" || input.Revision < 1 ||
		len(strings.TrimSpace(input.RawContentHash)) != 64 || strings.TrimSpace(input.RawStorageKey) == "" || input.RawByteSize < 0 {
		return maildomain.Draft{}, errors.New("save draft: required identity or raw metadata is missing")
	}
	request, err := maildomain.NormalizeDraftRequest(input.Request)
	if err != nil {
		return maildomain.Draft{}, fmt.Errorf("save draft: %w", err)
	}
	headersJSON, err := json.Marshal(request.Headers)
	if err != nil {
		return maildomain.Draft{}, fmt.Errorf("encode draft custom headers: %w", err)
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.Draft{}, fmt.Errorf("begin draft save: %w", err)
	}
	defer tx.Rollback()
	timestamp := input.Now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO raw_blobs (content_hash, storage_key, byte_size, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(content_hash) DO NOTHING`, input.RawContentHash, input.RawStorageKey, input.RawByteSize, timestamp); err != nil {
		return maildomain.Draft{}, fmt.Errorf("persist draft raw blob metadata: %w", err)
	}
	var storedKey string
	var storedSize int64
	if err := tx.QueryRowContext(ctx, `SELECT storage_key, byte_size FROM raw_blobs WHERE content_hash = ?`, input.RawContentHash).Scan(&storedKey, &storedSize); err != nil {
		return maildomain.Draft{}, fmt.Errorf("verify draft raw blob metadata: %w", err)
	}
	if storedKey != input.RawStorageKey || storedSize != input.RawByteSize {
		return maildomain.Draft{}, errors.New("draft raw blob content-address collision")
	}

	parsed := input.Content.Preview
	parsed.RFCMessageID = maildomain.NormalizeMessageID(input.RFCMessageID)
	parsed.From = request.From
	parsed.To = append([]maildomain.Address(nil), request.To...)
	parsed.CC = append([]maildomain.Address(nil), request.CC...)
	parsed.BCC = append([]maildomain.Address(nil), request.BCC...)
	parsed.Subject = request.Subject
	parsed.InReplyTo = request.InReplyTo
	parsed.References = append([]string(nil), request.References...)
	if request.ReplyTo != nil {
		parsed.ReplyTo = *request.ReplyTo
	} else {
		parsed.ReplyTo = maildomain.Address{}
	}
	if err := replaceDraftLogicalMessage(ctx, tx, input.AccountID, input.MessageID, parsed, input.Now, timestamp); err != nil {
		return maildomain.Draft{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO local_message_sources (
    message_id, raw_content_hash, raw_storage_key, raw_byte_size, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(message_id) DO UPDATE SET
    raw_content_hash = excluded.raw_content_hash,
    raw_storage_key = excluded.raw_storage_key,
    raw_byte_size = excluded.raw_byte_size,
    updated_at = excluded.updated_at`, input.MessageID, input.RawContentHash, input.RawStorageKey, input.RawByteSize, timestamp, timestamp); err != nil {
		return maildomain.Draft{}, fmt.Errorf("persist local draft raw source: %w", err)
	}
	if err := upsertMessageContent(ctx, tx, input.MessageID, "", input.Content, timestamp); err != nil {
		return maildomain.Draft{}, err
	}
	for _, kind := range []maildomain.IdentityKind{maildomain.IdentityGeneratedMessageID, maildomain.IdentityRFCMessageID} {
		if err := persistIdentityEvidence(ctx, tx, input.AccountID, input.MessageID, kind,
			maildomain.NormalizeMessageID(input.RFCMessageID), maildomain.IdentityStrong, "", timestamp); err != nil {
			return maildomain.Draft{}, err
		}
	}
	if err := refreshMessageSearchDocument(ctx, tx, input.MessageID, &input.Content.SearchText, &input.Content.SearchTextTruncated, "complete", "", timestamp); err != nil {
		return maildomain.Draft{}, err
	}
	if err := rebuildAffectedThreadGraph(ctx, tx, input.AccountID, []string{input.MessageID}, input.Now); err != nil {
		return maildomain.Draft{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO mail_drafts (
    draft_id, account_id, message_id, revision, sync_state,
    remote_location_id, previous_location_id, outbound_attempt_id,
    custom_headers_json, last_error_code, created_at, updated_at
) VALUES (?, ?, ?, ?, 'local', '', '', '', ?, '', ?, ?)
ON CONFLICT(draft_id) DO UPDATE SET
    revision = excluded.revision,
    sync_state = 'local',
    previous_location_id = CASE
        WHEN mail_drafts.remote_location_id <> '' THEN mail_drafts.remote_location_id
        ELSE mail_drafts.previous_location_id
    END,
    remote_location_id = '',
    outbound_attempt_id = '',
    custom_headers_json = excluded.custom_headers_json,
    last_error_code = '',
    updated_at = excluded.updated_at`,
		input.DraftID, input.AccountID, input.MessageID, input.Revision, string(headersJSON), timestamp, timestamp); err != nil {
		return maildomain.Draft{}, fmt.Errorf("persist draft lifecycle: %w", err)
	}
	value, err := scanDraft(tx.QueryRowContext(ctx, draftColumns+` WHERE draft_id = ?`, input.DraftID))
	if err != nil {
		return maildomain.Draft{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.Draft{}, fmt.Errorf("commit draft save: %w", err)
	}
	return value, nil
}

func replaceDraftLogicalMessage(ctx context.Context, tx *sql.Tx, accountID, messageID string, parsed maildomain.ParsedMessage, now time.Time, timestamp string) error {
	internalDate, sortAt, dateSource := maildomain.MessageTimes(parsed, now)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO messages (
    message_id, account_id, rfc_message_id, in_reply_to, direction,
    from_name, from_email, reply_to_name, reply_to_email, subject, snippet,
    header_date, internal_date, sort_at, date_source, attachment_count,
    created_at, updated_at
) VALUES (?, ?, ?, ?, 'outbound', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(message_id) DO UPDATE SET
    rfc_message_id = excluded.rfc_message_id,
    in_reply_to = excluded.in_reply_to,
    direction = 'outbound',
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
		messageID, accountID, parsed.RFCMessageID, parsed.InReplyTo,
		parsed.From.DisplayName, parsed.From.Email, parsed.ReplyTo.DisplayName, parsed.ReplyTo.Email,
		parsed.Subject, parsed.Snippet, nullableTime(parsed.HeaderDate), nullableTime(internalDate), nullableTime(sortAt),
		dateSource, parsed.AttachmentCount, timestamp, timestamp); err != nil {
		return fmt.Errorf("persist draft message: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_recipients WHERE message_id = ?`, messageID); err != nil {
		return fmt.Errorf("replace draft recipients: %w", err)
	}
	for _, group := range []struct {
		kind   maildomain.RecipientKind
		values []maildomain.Address
	}{{maildomain.RecipientTo, parsed.To}, {maildomain.RecipientCC, parsed.CC}, {maildomain.RecipientBCC, parsed.BCC}} {
		for index, address := range group.values {
			if _, err := tx.ExecContext(ctx, `INSERT INTO message_recipients (message_id, kind, ordinal, display_name, email_address) VALUES (?, ?, ?, ?, ?)`,
				messageID, group.kind, index, address.DisplayName, address.Email); err != nil {
				return fmt.Errorf("persist draft recipient: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_references WHERE message_id = ?`, messageID); err != nil {
		return fmt.Errorf("replace draft references: %w", err)
	}
	for index, reference := range parsed.References {
		if _, err := tx.ExecContext(ctx, `INSERT INTO message_references (message_id, ordinal, reference) VALUES (?, ?, ?)`, messageID, index, reference); err != nil {
			return fmt.Errorf("persist draft reference: %w", err)
		}
	}
	return nil
}

func (repository *DraftRepository) Draft(ctx context.Context, accountID, draftID string) (maildomain.Draft, error) {
	value, err := scanDraft(repository.db.QueryRowContext(ctx, draftColumns+` WHERE account_id = ? AND draft_id = ?`, strings.TrimSpace(accountID), strings.TrimSpace(draftID)))
	if err != nil {
		return maildomain.Draft{}, err
	}
	message, err := (&MailRepository{db: repository.db}).Message(ctx, accountID, value.MessageID)
	if err != nil {
		return maildomain.Draft{}, err
	}
	value.Message = message
	return value, nil
}

func (repository *DraftRepository) ByMessage(ctx context.Context, accountID, messageID string) (maildomain.Draft, error) {
	value, err := scanDraft(repository.db.QueryRowContext(ctx, draftColumns+` WHERE account_id = ? AND message_id = ?`, strings.TrimSpace(accountID), strings.TrimSpace(messageID)))
	if err != nil {
		return maildomain.Draft{}, err
	}
	message, err := (&MailRepository{db: repository.db}).Message(ctx, accountID, value.MessageID)
	if err != nil {
		return maildomain.Draft{}, err
	}
	value.Message = message
	return value, nil
}

func (repository *DraftRepository) List(ctx context.Context, accountID string, limit int) ([]maildomain.Draft, bool, error) {
	values, more, _, err := repository.ListPage(ctx, accountID, "", limit)
	return values, more, err
}

type storedDraftCursor struct {
	AccountID string `json:"account_id"`
	UpdatedAt string `json:"updated_at"`
	DraftID   string `json:"draft_id"`
}

func (repository *DraftRepository) ListPage(ctx context.Context, accountID, cursor string, limit int) ([]maildomain.Draft, bool, string, error) {
	if strings.TrimSpace(accountID) == "" || limit < 1 {
		return nil, false, "", errors.New("list drafts: account and positive limit are required")
	}
	accountID = strings.TrimSpace(accountID)
	where := ` WHERE account_id = ?`
	arguments := []any{accountID}
	if strings.TrimSpace(cursor) != "" {
		decoded, err := decodeDraftCursor(cursor, accountID)
		if err != nil {
			return nil, false, "", err
		}
		where += ` AND (updated_at < ? OR (updated_at = ? AND draft_id < ?))`
		arguments = append(arguments, decoded.UpdatedAt, decoded.UpdatedAt, decoded.DraftID)
	}
	arguments = append(arguments, limit+1)
	rows, err := repository.db.QueryContext(ctx, draftColumns+where+` ORDER BY updated_at DESC, draft_id DESC LIMIT ?`, arguments...)
	if err != nil {
		return nil, false, "", fmt.Errorf("list drafts: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.Draft, 0, limit+1)
	for rows.Next() {
		value, err := scanDraft(rows)
		if err != nil {
			return nil, false, "", err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, "", fmt.Errorf("list drafts: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	mailRepo := &MailRepository{db: repository.db}
	for index := range values {
		message, err := mailRepo.Message(ctx, accountID, values[index].MessageID)
		if err != nil {
			return nil, false, "", err
		}
		values[index].Message = message
	}
	nextCursor := ""
	if more && len(values) > 0 {
		last := values[len(values)-1]
		var err error
		nextCursor, err = encodeDraftCursor(accountID, last.UpdatedAt, last.ID)
		if err != nil {
			return nil, false, "", err
		}
	}
	return values, more, nextCursor, nil
}

func encodeDraftCursor(accountID string, updatedAt time.Time, draftID string) (string, error) {
	payload, err := json.Marshal(storedDraftCursor{AccountID: strings.TrimSpace(accountID), UpdatedAt: updatedAt.UTC().Format(time.RFC3339Nano), DraftID: strings.TrimSpace(draftID)})
	if err != nil {
		return "", fmt.Errorf("encode draft cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeDraftCursor(value, accountID string) (storedDraftCursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return storedDraftCursor{}, maildomain.ErrInvalidDraftCursor
	}
	var cursor storedDraftCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.AccountID != strings.TrimSpace(accountID) || strings.TrimSpace(cursor.DraftID) == "" {
		return storedDraftCursor{}, maildomain.ErrInvalidDraftCursor
	}
	if _, err := time.Parse(time.RFC3339Nano, cursor.UpdatedAt); err != nil {
		return storedDraftCursor{}, maildomain.ErrInvalidDraftCursor
	}
	return cursor, nil
}

func (repository *DraftRepository) ListActionablePage(ctx context.Context, accountID, afterDraftID string, limit int) ([]maildomain.Draft, bool, error) {
	if strings.TrimSpace(accountID) == "" || limit < 1 {
		return nil, false, errors.New("list actionable drafts: account and positive limit are required")
	}
	rows, err := repository.db.QueryContext(ctx, draftColumns+`
WHERE account_id = ? AND draft_id > ? AND sync_state IN ('local', 'reconcile', 'cleanup_pending', 'send_unknown')
ORDER BY draft_id ASC LIMIT ?`, strings.TrimSpace(accountID), strings.TrimSpace(afterDraftID), limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list actionable drafts: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.Draft, 0, limit+1)
	for rows.Next() {
		value, err := scanDraft(rows)
		if err != nil {
			return nil, false, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list actionable drafts: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	mailRepo := &MailRepository{db: repository.db}
	for index := range values {
		message, err := mailRepo.Message(ctx, accountID, values[index].MessageID)
		if err != nil {
			return nil, false, err
		}
		values[index].Message = message
	}
	return values, more, nil
}

func (repository *DraftRepository) SetLifecycle(ctx context.Context, accountID, draftID string, state maildomain.DraftSyncState, remoteLocationID, previousLocationID, attemptID, errorCode string, now time.Time) (maildomain.Draft, error) {
	result, err := repository.db.ExecContext(ctx, `
UPDATE mail_drafts SET sync_state = ?, remote_location_id = ?, previous_location_id = ?,
    outbound_attempt_id = ?, last_error_code = ?, updated_at = ?
WHERE account_id = ? AND draft_id = ?`, state, strings.TrimSpace(remoteLocationID), strings.TrimSpace(previousLocationID),
		strings.TrimSpace(attemptID), strings.TrimSpace(errorCode), now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(accountID), strings.TrimSpace(draftID))
	if err != nil {
		return maildomain.Draft{}, fmt.Errorf("update draft lifecycle: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return maildomain.Draft{}, maildomain.ErrDraftNotFound
	}
	return repository.Draft(ctx, accountID, draftID)
}

func (repository *DraftRepository) Delete(ctx context.Context, accountID, draftID string, now time.Time) error {
	accountID = strings.TrimSpace(accountID)
	draftID = strings.TrimSpace(draftID)
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin draft delete: %w", err)
	}
	defer tx.Rollback()
	var messageID string
	if err := tx.QueryRowContext(ctx, `SELECT message_id FROM mail_drafts WHERE account_id = ? AND draft_id = ?`, accountID, draftID).Scan(&messageID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.ErrDraftNotFound
		}
		return fmt.Errorf("resolve draft delete: %w", err)
	}
	var threadID string
	threadErr := tx.QueryRowContext(ctx, `
SELECT tm.thread_id
FROM thread_messages AS tm
JOIN threads AS t ON t.thread_id = tm.thread_id
WHERE t.account_id = ? AND tm.message_id = ?`, accountID, messageID).Scan(&threadID)
	if threadErr != nil && !errors.Is(threadErr, sql.ErrNoRows) {
		return fmt.Errorf("resolve draft thread before delete: %w", threadErr)
	}
	remaining := []string{}
	if threadErr == nil {
		rows, err := tx.QueryContext(ctx, `
SELECT tm.message_id FROM thread_messages AS tm
WHERE tm.thread_id = ? AND tm.message_id <> ? ORDER BY tm.message_id`, threadID, messageID)
		if err != nil {
			return fmt.Errorf("list draft thread members before delete: %w", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return fmt.Errorf("read draft thread member before delete: %w", err)
			}
			remaining = append(remaining, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("list draft thread members before delete: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close draft thread members before delete: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE account_id = ? AND message_id = ?`, accountID, messageID); err != nil {
		return fmt.Errorf("delete draft message: %w", err)
	}
	if len(remaining) > 0 {
		if err := rebuildAffectedThreadGraph(ctx, tx, accountID, remaining, now); err != nil {
			return fmt.Errorf("rebuild thread after draft delete: %w", err)
		}
	} else if threadID != "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM threads WHERE account_id = ? AND thread_id = ?`, accountID, threadID); err != nil {
			return fmt.Errorf("delete empty draft thread: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit draft delete: %w", err)
	}
	return nil
}

func scanDraft(row rowScanner) (maildomain.Draft, error) {
	var value maildomain.Draft
	var state string
	var headersJSON, createdAt, updatedAt string
	if err := row.Scan(&value.ID, &value.AccountID, &value.MessageID, &value.Revision, &state,
		&value.RemoteLocationID, &value.PreviousLocationID, &value.OutboundAttemptID,
		&headersJSON, &value.LastErrorCode, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.Draft{}, maildomain.ErrDraftNotFound
		}
		return maildomain.Draft{}, fmt.Errorf("scan draft: %w", err)
	}
	value.SyncState = maildomain.DraftSyncState(state)
	if err := json.Unmarshal([]byte(headersJSON), &value.CustomHeaders); err != nil {
		return maildomain.Draft{}, fmt.Errorf("decode draft headers: %w", err)
	}
	var err error
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return maildomain.Draft{}, err
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.Draft{}, err
	}
	return value, nil
}

func (repository *DraftRepository) MarkAppendUnknown(ctx context.Context, accountID, draftID, errorCode string, now time.Time) (maildomain.Draft, error) {
	value, err := repository.Draft(ctx, accountID, draftID)
	if err != nil {
		return maildomain.Draft{}, err
	}
	return repository.SetLifecycle(ctx, accountID, draftID, maildomain.DraftReconcile, "", value.PreviousLocationID, value.OutboundAttemptID, errorCode, now)
}

func (repository *DraftRepository) ConfirmAppend(ctx context.Context, accountID, draftID, folderID, locationID string, uidValidity, uid uint32, now time.Time) (maildomain.Draft, error) {
	if uidValidity == 0 || uid == 0 || strings.TrimSpace(folderID) == "" || strings.TrimSpace(locationID) == "" {
		return maildomain.Draft{}, errors.New("confirm draft append: provider location identity is required")
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.Draft{}, fmt.Errorf("begin draft append confirmation: %w", err)
	}
	defer tx.Rollback()
	value, err := scanDraft(tx.QueryRowContext(ctx, draftColumns+` WHERE account_id = ? AND draft_id = ?`, strings.TrimSpace(accountID), strings.TrimSpace(draftID)))
	if err != nil {
		return maildomain.Draft{}, err
	}
	var rawHash, rawKey string
	var rawSize int64
	if err := tx.QueryRowContext(ctx, `SELECT raw_content_hash, raw_storage_key, raw_byte_size FROM local_message_sources WHERE message_id = ?`, value.MessageID).Scan(&rawHash, &rawKey, &rawSize); err != nil {
		return maildomain.Draft{}, fmt.Errorf("resolve draft raw source for append confirmation: %w", err)
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO message_locations (
    location_id, message_id, account_id, folder_id, uid_validity, uid,
    seen, flagged, answered, draft, deleted, unknown_flags_json,
    mod_sequence, flags_checked_at, internal_date, rfc822_size,
    raw_content_hash, raw_storage_key, raw_byte_size, present,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, 1, 0, 0, 1, 0, '[]', '', ?, ?, ?, ?, ?, ?, 1, ?, ?)
ON CONFLICT(account_id, folder_id, uid_validity, uid) DO UPDATE SET
    message_id = excluded.message_id,
    seen = 1, draft = 1, deleted = 0, present = 1,
    raw_content_hash = excluded.raw_content_hash,
    raw_storage_key = excluded.raw_storage_key,
    raw_byte_size = excluded.raw_byte_size,
    rfc822_size = excluded.rfc822_size,
    flags_checked_at = excluded.flags_checked_at,
    updated_at = excluded.updated_at`,
		locationID, value.MessageID, value.AccountID, folderID, uidValidity, uid,
		timestamp, timestamp, rawSize, rawHash, rawKey, rawSize, timestamp, timestamp); err != nil {
		return maildomain.Draft{}, fmt.Errorf("persist confirmed draft location: %w", err)
	}
	state := maildomain.DraftSynced
	previous := strings.TrimSpace(value.PreviousLocationID)
	if previous != "" && previous != strings.TrimSpace(locationID) {
		state = maildomain.DraftCleanupPending
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE mail_drafts SET sync_state = ?, remote_location_id = ?, last_error_code = '', updated_at = ?
WHERE draft_id = ?`, state, strings.TrimSpace(locationID), timestamp, value.ID); err != nil {
		return maildomain.Draft{}, fmt.Errorf("update confirmed draft lifecycle: %w", err)
	}
	if err := rebuildAffectedThreadGraph(ctx, tx, accountID, []string{value.MessageID}, now); err != nil {
		return maildomain.Draft{}, err
	}
	updated, err := scanDraft(tx.QueryRowContext(ctx, draftColumns+` WHERE draft_id = ?`, value.ID))
	if err != nil {
		return maildomain.Draft{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.Draft{}, fmt.Errorf("commit draft append confirmation: %w", err)
	}
	return updated, nil
}

const draftReconciliationPageSize = 100

func (repository *DraftRepository) AdoptFolder(ctx context.Context, accountID, folderID string, now time.Time) (int, error) {
	afterMessageID := ""
	observed := 0
	for {
		count, lastMessageID, more, err := repository.adoptFolderPage(ctx, accountID, folderID, afterMessageID, draftReconciliationPageSize, now)
		if err != nil {
			return observed, err
		}
		observed += count
		if !more {
			return observed, nil
		}
		afterMessageID = lastMessageID
	}
}

func (repository *DraftRepository) adoptFolderPage(ctx context.Context, accountID, folderID, afterMessageID string, limit int, now time.Time) (int, string, bool, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, "", false, fmt.Errorf("begin draft folder reconciliation: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT m.message_id, m.rfc_message_id
FROM messages AS m
JOIN message_locations AS l ON l.message_id = m.message_id
WHERE m.account_id = ? AND l.account_id = ? AND l.folder_id = ? AND l.present = 1 AND l.deleted = 0 AND l.draft = 1
  AND m.message_id > ?
ORDER BY m.message_id
LIMIT ?`, strings.TrimSpace(accountID), strings.TrimSpace(accountID), strings.TrimSpace(folderID), strings.TrimSpace(afterMessageID), limit+1)
	if err != nil {
		return 0, "", false, fmt.Errorf("select synchronized provider drafts: %w", err)
	}
	type candidate struct{ messageID, rfcMessageID string }
	candidates := make([]candidate, 0, limit+1)
	for rows.Next() {
		var value candidate
		if err := rows.Scan(&value.messageID, &value.rfcMessageID); err != nil {
			rows.Close()
			return 0, "", false, fmt.Errorf("read synchronized provider draft: %w", err)
		}
		candidates = append(candidates, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, "", false, fmt.Errorf("select synchronized provider drafts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, "", false, fmt.Errorf("close synchronized provider drafts: %w", err)
	}
	more := len(candidates) > limit
	if more {
		candidates = candidates[:limit]
	}
	lastMessageID := afterMessageID
	if len(candidates) > 0 {
		lastMessageID = candidates[len(candidates)-1].messageID
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	for _, candidate := range candidates {
		locationID, err := preferredDraftLocation(ctx, tx, accountID, candidate.messageID, folderID, candidate.rfcMessageID)
		if err != nil {
			return 0, "", false, err
		}
		if locationID == "" {
			continue
		}
		draftID := maildomain.DraftIDForMessage(candidate.messageID)
		if draftID == "" {
			return 0, "", false, errors.New("reconcile provider draft: canonical message ID is invalid")
		}
		current, currentErr := scanDraft(tx.QueryRowContext(ctx, draftColumns+` WHERE account_id = ? AND message_id = ?`, accountID, candidate.messageID))
		if errors.Is(currentErr, maildomain.ErrDraftNotFound) {
			headersJSON, err := providerDraftCustomHeadersJSON(ctx, tx, candidate.messageID)
			if err != nil {
				return 0, "", false, err
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO mail_drafts (
    draft_id, account_id, message_id, revision, sync_state, remote_location_id,
    previous_location_id, outbound_attempt_id, custom_headers_json, last_error_code,
    created_at, updated_at
) VALUES (?, ?, ?, 1, 'synced', ?, '', '', ?, '', ?, ?)`, draftID, accountID, candidate.messageID, locationID, headersJSON, timestamp, timestamp); err != nil {
				return 0, "", false, fmt.Errorf("adopt provider draft: %w", err)
			}
			continue
		}
		if currentErr != nil {
			return 0, "", false, currentErr
		}
		state, remote, previous := current.SyncState, current.RemoteLocationID, current.PreviousLocationID
		switch current.SyncState {
		case maildomain.DraftLocal:
			state, remote = maildomain.DraftSynced, locationID
		case maildomain.DraftReconcile:
			remote = locationID
			if previous != "" && locationID != previous {
				state = maildomain.DraftCleanupPending
			} else {
				state = maildomain.DraftSynced
			}
		case maildomain.DraftSynced:
			remote = locationID
		case maildomain.DraftCleanupPending, maildomain.DraftSendUnknown:
			// Preserve explicit recovery state until its owner resolves it.
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mail_drafts SET sync_state = ?, remote_location_id = ?, previous_location_id = ?, updated_at = ? WHERE draft_id = ?`, state, remote, previous, timestamp, current.ID); err != nil {
			return 0, "", false, fmt.Errorf("reconcile provider draft lifecycle: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, "", false, fmt.Errorf("commit draft folder reconciliation: %w", err)
	}
	return len(candidates), lastMessageID, more, nil
}

func providerDraftCustomHeadersJSON(ctx context.Context, tx *sql.Tx, messageID string) (string, error) {
	var headersJSON string
	if err := tx.QueryRowContext(ctx, `SELECT headers_json FROM message_content WHERE message_id = ?`, messageID).Scan(&headersJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "[]", nil
		}
		return "", fmt.Errorf("read provider draft headers: %w", err)
	}
	var fields []maildomain.HeaderField
	if err := json.Unmarshal([]byte(headersJSON), &fields); err != nil {
		return "", fmt.Errorf("decode provider draft headers: %w", err)
	}
	custom, err := json.Marshal(maildomain.SafeCustomHeaders(fields))
	if err != nil {
		return "", fmt.Errorf("encode provider draft custom headers: %w", err)
	}
	return string(custom), nil
}

func preferredDraftLocation(ctx context.Context, tx *sql.Tx, accountID, messageID, folderID, rfcMessageID string) (string, error) {
	var locationID string
	var currentRawHash string
	rawErr := tx.QueryRowContext(ctx, `SELECT raw_content_hash FROM local_message_sources WHERE message_id = ?`, messageID).Scan(&currentRawHash)
	if rawErr == nil && strings.TrimSpace(currentRawHash) != "" {
		err := tx.QueryRowContext(ctx, `
SELECT location_id FROM message_locations
WHERE account_id = ? AND message_id = ? AND folder_id = ? AND present = 1 AND deleted = 0
  AND raw_content_hash = ?
ORDER BY uid DESC LIMIT 1`, accountID, messageID, folderID, currentRawHash).Scan(&locationID)
		if err == nil {
			return locationID, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("resolve current draft by canonical raw content: %w", err)
		}
		// A local source means this draft revision has an exact canonical byte
		// identity. Never fall back to an older provider version merely because it
		// shares the stable local message ID.
		return "", nil
	}
	if rawErr != nil && !errors.Is(rawErr, sql.ErrNoRows) {
		return "", fmt.Errorf("resolve current draft raw identity: %w", rawErr)
	}
	rfcMessageID = maildomain.NormalizeMessageID(rfcMessageID)
	if rfcMessageID != "" {
		err := tx.QueryRowContext(ctx, `
SELECT l.location_id
FROM message_locations AS l
JOIN message_identity_evidence AS e ON e.source_location_id = l.location_id
WHERE l.account_id = ? AND l.message_id = ? AND l.folder_id = ? AND l.present = 1 AND l.deleted = 0
  AND e.account_id = ? AND e.message_id = ? AND e.kind IN ('rfc_message_id','generated_message_id')
  AND lower(e.value) = lower(?)
ORDER BY l.uid DESC LIMIT 1`, accountID, messageID, folderID, accountID, messageID, rfcMessageID).Scan(&locationID)
		if err == nil {
			return locationID, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("resolve current draft provider location: %w", err)
		}
	}
	err := tx.QueryRowContext(ctx, `
SELECT location_id FROM message_locations
WHERE account_id = ? AND message_id = ? AND folder_id = ? AND present = 1 AND deleted = 0
ORDER BY uid DESC LIMIT 1`, accountID, messageID, folderID).Scan(&locationID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve draft provider location: %w", err)
	}
	return locationID, nil
}

func (repository *DraftRepository) ClearPrevious(ctx context.Context, accountID, draftID, locationID string, now time.Time) (maildomain.Draft, error) {
	result, err := repository.db.ExecContext(ctx, `
UPDATE mail_drafts SET previous_location_id = '', sync_state = 'synced', last_error_code = '', updated_at = ?
WHERE account_id = ? AND draft_id = ? AND previous_location_id = ? AND remote_location_id <> ''`,
		now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(accountID), strings.TrimSpace(draftID), strings.TrimSpace(locationID))
	if err != nil {
		return maildomain.Draft{}, fmt.Errorf("complete draft replacement cleanup: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return maildomain.Draft{}, maildomain.ErrDraftNotFound
	}
	return repository.Draft(ctx, accountID, draftID)
}
