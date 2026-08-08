package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type OutboundRepository struct {
	db *sql.DB
}

const outboundAttemptColumns = `
SELECT attempt_id, account_id, proposed_message_id, request_hash,
       idempotency_key, status, rfc_message_id,
       raw_content_hash, raw_storage_key, raw_byte_size,
       to_count, cc_count, bcc_count, body_part_count,
       attachment_count, attachment_byte_size,
       CASE WHEN source_message_id <> '' THEN 'forward' ELSE outbound_kind END,
       parent_message_id, source_message_id,
       message_id, thread_id,
       retry_of_attempt_id, reconciliation, reconciled_location_id,
       reconciliation_error_code, error_code,
       accepted_at, created_at, updated_at
FROM outbound_attempts`

func (repository *OutboundRepository) ResolveIdempotency(ctx context.Context, accountID, key, requestHash string) (maildomain.OutboundAttempt, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return maildomain.OutboundAttempt{}, false, nil
	}
	value, err := scanOutboundAttempt(repository.db.QueryRowContext(ctx, outboundAttemptColumns+`
WHERE account_id = ? AND idempotency_key = ?`, strings.TrimSpace(accountID), key))
	if errors.Is(err, maildomain.ErrOutboundAttemptNotFound) {
		return maildomain.OutboundAttempt{}, false, nil
	}
	if err != nil {
		return maildomain.OutboundAttempt{}, false, err
	}
	if value.RequestHash != strings.TrimSpace(requestHash) {
		return maildomain.OutboundAttempt{}, true, maildomain.ErrIdempotencyConflict
	}
	return value, true, nil
}

func (repository *OutboundRepository) Begin(ctx context.Context, input maildomain.BeginOutboundAttempt) (maildomain.OutboundAttempt, bool, error) {
	if strings.TrimSpace(input.AttemptID) == "" || strings.TrimSpace(input.AccountID) == "" ||
		strings.TrimSpace(input.ProposedMessageID) == "" || len(strings.TrimSpace(input.RequestHash)) != 64 ||
		strings.TrimSpace(input.RFCMessageID) == "" || len(strings.TrimSpace(input.RawContentHash)) != 64 ||
		strings.TrimSpace(input.RawStorageKey) == "" || input.RawByteSize < 0 {
		return maildomain.OutboundAttempt{}, false, errors.New("begin outbound attempt: required identity or raw metadata is missing")
	}
	kind := input.Kind
	if kind == "" {
		kind = maildomain.OutboundKindSend
	}
	parentMessageID := strings.TrimSpace(input.ParentMessageID)
	sourceMessageID := strings.TrimSpace(input.SourceMessageID)
	storedKind := kind
	switch kind {
	case maildomain.OutboundKindSend:
		if parentMessageID != "" || sourceMessageID != "" {
			return maildomain.OutboundAttempt{}, false, errors.New("begin outbound attempt: send cannot name a related message")
		}
	case maildomain.OutboundKindReply, maildomain.OutboundKindReplyAll:
		if !strings.HasPrefix(parentMessageID, "msg_") || sourceMessageID != "" {
			return maildomain.OutboundAttempt{}, false, errors.New("begin outbound attempt: reply parent message is required")
		}
	case maildomain.OutboundKindForward:
		if parentMessageID != "" || !strings.HasPrefix(sourceMessageID, "msg_") {
			return maildomain.OutboundAttempt{}, false, errors.New("begin outbound attempt: forward source message is required")
		}
		// Schema 13 constrained outbound_kind before forward existed. Keep the
		// stored base kind compatible and derive forward from source_message_id.
		storedKind = maildomain.OutboundKindSend
	default:
		return maildomain.OutboundAttempt{}, false, errors.New("begin outbound attempt: unsupported outbound kind")
	}
	var attachmentBytes int64
	for index, attachment := range input.Attachments {
		if attachment.Ordinal != index || strings.TrimSpace(attachment.Filename) == "" || strings.TrimSpace(attachment.MediaType) == "" ||
			len(strings.TrimSpace(attachment.ContentHash)) != 64 || strings.TrimSpace(attachment.StorageKey) == "" || attachment.ByteSize < 0 {
			return maildomain.OutboundAttempt{}, false, errors.New("begin outbound attempt: attachment metadata is incomplete")
		}
		attachmentBytes += attachment.ByteSize
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.OutboundAttempt{}, false, fmt.Errorf("begin outbound attempt transaction: %w", err)
	}
	defer tx.Rollback()
	if key := strings.TrimSpace(input.IdempotencyKey); key != "" {
		value, existingErr := scanOutboundAttempt(tx.QueryRowContext(ctx, outboundAttemptColumns+`
WHERE account_id = ? AND idempotency_key = ?`, input.AccountID, key))
		if existingErr == nil {
			if value.RequestHash != strings.TrimSpace(input.RequestHash) {
				return maildomain.OutboundAttempt{}, true, maildomain.ErrIdempotencyConflict
			}
			return value, true, nil
		}
		if !errors.Is(existingErr, maildomain.ErrOutboundAttemptNotFound) {
			return maildomain.OutboundAttempt{}, false, existingErr
		}
	}
	timestamp := input.Now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO raw_blobs (content_hash, storage_key, byte_size, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(content_hash) DO NOTHING`, input.RawContentHash, input.RawStorageKey, input.RawByteSize, timestamp); err != nil {
		return maildomain.OutboundAttempt{}, false, fmt.Errorf("persist outbound raw blob metadata: %w", err)
	}
	var storedKey string
	var storedSize int64
	if err := tx.QueryRowContext(ctx, `SELECT storage_key, byte_size FROM raw_blobs WHERE content_hash = ?`, input.RawContentHash).Scan(&storedKey, &storedSize); err != nil {
		return maildomain.OutboundAttempt{}, false, fmt.Errorf("verify outbound raw blob metadata: %w", err)
	}
	if storedKey != input.RawStorageKey || storedSize != input.RawByteSize {
		return maildomain.OutboundAttempt{}, false, errors.New("outbound raw blob content-address collision")
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO outbound_attempts (
    attempt_id, account_id, proposed_message_id, request_hash, idempotency_key,
    status, rfc_message_id, raw_content_hash, raw_storage_key, raw_byte_size,
    to_count, cc_count, bcc_count, body_part_count,
    attachment_count, attachment_byte_size, outbound_kind, parent_message_id, source_message_id,
    retry_of_attempt_id,
    reconciliation, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'none', ?, ?)`,
		input.AttemptID, input.AccountID, input.ProposedMessageID, input.RequestHash,
		strings.TrimSpace(input.IdempotencyKey), input.RFCMessageID,
		input.RawContentHash, input.RawStorageKey, input.RawByteSize,
		input.ToCount, input.CCCount, input.BCCCount, input.BodyPartCount,
		len(input.Attachments), attachmentBytes, storedKind, parentMessageID, sourceMessageID,
		strings.TrimSpace(input.RetryOfAttemptID),
		timestamp, timestamp,
	); err != nil {
		return maildomain.OutboundAttempt{}, false, fmt.Errorf("persist pending outbound attempt: %w", err)
	}
	for _, attachment := range input.Attachments {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO outbound_attempt_attachments (
    attempt_id, ordinal, filename, media_type, content_hash, storage_key, byte_size
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			input.AttemptID, attachment.Ordinal, attachment.Filename, attachment.MediaType,
			strings.ToLower(strings.TrimSpace(attachment.ContentHash)), attachment.StorageKey, attachment.ByteSize,
		); err != nil {
			return maildomain.OutboundAttempt{}, false, fmt.Errorf("persist outbound attempt attachment: %w", err)
		}
	}
	if key := strings.TrimSpace(input.IdempotencyKey); key != "" {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO idempotency_records (account_id, idempotency_key, request_hash, attempt_id, created_at)
VALUES (?, ?, ?, ?, ?)`, input.AccountID, key, input.RequestHash, input.AttemptID, timestamp); err != nil {
			return maildomain.OutboundAttempt{}, false, fmt.Errorf("persist outbound idempotency record: %w", err)
		}
	}
	value, err := scanOutboundAttempt(tx.QueryRowContext(ctx, outboundAttemptColumns+` WHERE attempt_id = ?`, input.AttemptID))
	if err != nil {
		return maildomain.OutboundAttempt{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.OutboundAttempt{}, false, fmt.Errorf("commit pending outbound attempt: %w", err)
	}
	return value, false, nil
}

func (repository *OutboundRepository) MarkOutcome(ctx context.Context, attemptID string, status maildomain.OutboundStatus, errorCode string, now time.Time) (maildomain.OutboundAttempt, error) {
	if status != maildomain.OutboundFailed && status != maildomain.OutboundUnknown {
		return maildomain.OutboundAttempt{}, errors.New("mark outbound outcome: status must be failed or unknown")
	}
	reconciliation := maildomain.ReconciliationNone
	if status == maildomain.OutboundUnknown {
		reconciliation = maildomain.ReconciliationUnknown
	}
	result, err := repository.db.ExecContext(ctx, `
UPDATE outbound_attempts
SET status = ?, reconciliation = ?, error_code = ?, updated_at = ?
WHERE attempt_id = ? AND status = 'pending'`,
		status, reconciliation, strings.TrimSpace(errorCode), now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(attemptID),
	)
	if err != nil {
		return maildomain.OutboundAttempt{}, fmt.Errorf("update outbound outcome: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		value, readErr := repository.Attempt(ctx, attemptID)
		if readErr != nil {
			return maildomain.OutboundAttempt{}, readErr
		}
		return value, nil
	}
	return repository.Attempt(ctx, attemptID)
}

// MarkAcceptedIncomplete preserves the provider truth when SMTP acceptance was
// proven but the atomic local message/thread completion could not be committed.
// The attempt is terminally sent so an idempotent retry cannot submit the same
// message again; later Sent reconciliation or repair can restore the missing
// local message linkage from the durable RFC Message-ID and raw source.
func (repository *OutboundRepository) MarkAcceptedIncomplete(ctx context.Context, attemptID, errorCode string, acceptedAt time.Time) (maildomain.OutboundAttempt, error) {
	result, err := repository.db.ExecContext(ctx, `
UPDATE outbound_attempts
SET status = 'sent', reconciliation = 'pending', error_code = ?,
    accepted_at = ?, updated_at = ?
WHERE attempt_id = ? AND status = 'pending'`,
		strings.TrimSpace(errorCode), acceptedAt.UTC().Format(time.RFC3339Nano),
		acceptedAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(attemptID),
	)
	if err != nil {
		return maildomain.OutboundAttempt{}, fmt.Errorf("record accepted outbound attempt with incomplete local state: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return repository.Attempt(ctx, attemptID)
	}
	return repository.Attempt(ctx, attemptID)
}

func (repository *OutboundRepository) MarkReconciliationIssue(ctx context.Context, attemptID, errorCode string, now time.Time) (maildomain.OutboundAttempt, error) {
	attemptID = strings.TrimSpace(attemptID)
	errorCode = strings.TrimSpace(errorCode)
	if attemptID == "" || errorCode == "" {
		return maildomain.OutboundAttempt{}, errors.New("mark outbound reconciliation issue: attempt and error code are required")
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	result, err := repository.db.ExecContext(ctx, `
UPDATE outbound_attempts
SET reconciliation = 'unknown', reconciliation_error_code = ?, updated_at = ?
WHERE attempt_id = ? AND status = 'sent'`, errorCode, timestamp, attemptID)
	if err != nil {
		return maildomain.OutboundAttempt{}, fmt.Errorf("mark outbound reconciliation issue: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return maildomain.OutboundAttempt{}, fmt.Errorf("mark outbound reconciliation issue result: %w", err)
	}
	if changed != 1 {
		return maildomain.OutboundAttempt{}, errors.New("mark outbound reconciliation issue: accepted attempt not found")
	}
	return repository.Attempt(ctx, attemptID)
}

func (repository *OutboundRepository) CompleteSent(ctx context.Context, input maildomain.CompleteOutboundSend) (maildomain.OutboundAttempt, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.OutboundAttempt{}, fmt.Errorf("begin accepted outbound transaction: %w", err)
	}
	defer tx.Rollback()
	attempt, err := scanOutboundAttempt(tx.QueryRowContext(ctx, outboundAttemptColumns+` WHERE attempt_id = ?`, strings.TrimSpace(input.AttemptID)))
	if err != nil {
		return maildomain.OutboundAttempt{}, err
	}
	if attempt.Status == maildomain.OutboundSent {
		return attempt, nil
	}
	if attempt.Status != maildomain.OutboundPending {
		return maildomain.OutboundAttempt{}, errors.New("accepted outbound attempt is not pending")
	}
	if err := verifyOutboundAttemptAttachments(ctx, tx, attempt.ID, input.Content.Attachments); err != nil {
		return maildomain.OutboundAttempt{}, err
	}
	if attempt.Kind != input.Request.Operation() ||
		attempt.ParentMessageID != strings.TrimSpace(input.Request.ParentMessageID) ||
		attempt.SourceMessageID != strings.TrimSpace(input.Request.SourceMessageID) {
		return maildomain.OutboundAttempt{}, errors.New("accepted outbound operation does not match the pending attempt")
	}
	timestamp := input.AcceptedAt.UTC().Format(time.RFC3339Nano)
	parsed := input.Content.Preview
	parsed.From = input.Request.From
	parsed.To = append([]maildomain.Address(nil), input.Request.To...)
	parsed.CC = append([]maildomain.Address(nil), input.Request.CC...)
	parsed.BCC = append([]maildomain.Address(nil), input.Request.BCC...)
	if input.Request.ReplyTo != nil {
		parsed.ReplyTo = *input.Request.ReplyTo
	}
	record := maildomain.IngestRecord{
		MessageID: attempt.ProposedMessageID, AccountID: attempt.AccountID,
		Parsed: parsed, Content: &input.Content, Direction: maildomain.DirectionOutbound,
		InternalDate: input.AcceptedAt,
	}
	if err := upsertLogicalMessage(ctx, tx, record, timestamp); err != nil {
		return maildomain.OutboundAttempt{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO local_message_sources (
    message_id, raw_content_hash, raw_storage_key, raw_byte_size, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(message_id) DO UPDATE SET
    raw_content_hash = excluded.raw_content_hash,
    raw_storage_key = excluded.raw_storage_key,
    raw_byte_size = excluded.raw_byte_size,
    updated_at = excluded.updated_at`,
		attempt.ProposedMessageID, attempt.RawContentHash, attempt.RawStorageKey,
		attempt.RawByteSize, timestamp, timestamp,
	); err != nil {
		return maildomain.OutboundAttempt{}, fmt.Errorf("persist local outbound raw source: %w", err)
	}
	if err := upsertMessageContent(ctx, tx, attempt.ProposedMessageID, "", input.Content, timestamp); err != nil {
		return maildomain.OutboundAttempt{}, err
	}
	if err := persistIdentityEvidence(
		ctx, tx, attempt.AccountID, attempt.ProposedMessageID,
		maildomain.IdentityGeneratedMessageID, maildomain.NormalizeMessageID(attempt.RFCMessageID),
		maildomain.IdentityStrong, "", timestamp,
	); err != nil {
		return maildomain.OutboundAttempt{}, err
	}
	if err := persistIdentityEvidence(
		ctx, tx, attempt.AccountID, attempt.ProposedMessageID,
		maildomain.IdentityRFCMessageID, maildomain.NormalizeMessageID(attempt.RFCMessageID),
		maildomain.IdentityStrong, "", timestamp,
	); err != nil {
		return maildomain.OutboundAttempt{}, err
	}
	if err := refreshMessageSearchDocument(ctx, tx, attempt.ProposedMessageID, &input.Content.SearchText, &input.Content.SearchTextTruncated, "complete", "", timestamp); err != nil {
		return maildomain.OutboundAttempt{}, err
	}
	if err := rebuildCanonicalThreadProjection(ctx, tx, attempt.AccountID, []string{attempt.ProposedMessageID}, input.AcceptedAt); err != nil {
		return maildomain.OutboundAttempt{}, err
	}
	var threadID string
	if err := tx.QueryRowContext(ctx, `SELECT thread_id FROM thread_messages WHERE message_id = ?`, attempt.ProposedMessageID).Scan(&threadID); err != nil {
		return maildomain.OutboundAttempt{}, fmt.Errorf("resolve outbound thread: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE outbound_attempts
SET status = 'sent', message_id = ?, thread_id = ?, reconciliation = 'pending',
    error_code = '', accepted_at = ?, updated_at = ?
WHERE attempt_id = ? AND status = 'pending'`,
		attempt.ProposedMessageID, threadID, timestamp, timestamp, attempt.ID,
	); err != nil {
		return maildomain.OutboundAttempt{}, fmt.Errorf("complete accepted outbound attempt: %w", err)
	}
	value, err := scanOutboundAttempt(tx.QueryRowContext(ctx, outboundAttemptColumns+` WHERE attempt_id = ?`, attempt.ID))
	if err != nil {
		return maildomain.OutboundAttempt{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.OutboundAttempt{}, fmt.Errorf("commit accepted outbound message: %w", err)
	}
	return value, nil
}

func (repository *OutboundRepository) List(ctx context.Context, accountID string, limit int) ([]maildomain.OutboundAttempt, bool, error) {
	if strings.TrimSpace(accountID) == "" || limit < 1 {
		return nil, false, errors.New("list outbound attempts: account and positive limit are required")
	}
	rows, err := repository.db.QueryContext(ctx, outboundAttemptColumns+`
WHERE account_id = ?
ORDER BY created_at DESC, attempt_id DESC
LIMIT ?`, strings.TrimSpace(accountID), limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list outbound attempts: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.OutboundAttempt, 0, limit+1)
	for rows.Next() {
		value, scanErr := scanOutboundAttempt(rows)
		if scanErr != nil {
			return nil, false, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list outbound attempts: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	return values, more, nil
}

func (repository *OutboundRepository) Reconcile(ctx context.Context, accountID string, limit int, now time.Time) (maildomain.OutboundReconcileSummary, error) {
	if strings.TrimSpace(accountID) == "" || limit < 1 || limit > 1000 {
		return maildomain.OutboundReconcileSummary{}, errors.New("reconcile outbound attempts: account and bounded limit are required")
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.OutboundReconcileSummary{}, fmt.Errorf("begin outbound reconciliation: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, outboundAttemptColumns+`
WHERE account_id = ?
  AND status IN ('sent', 'unknown')
  AND reconciliation <> 'confirmed'
ORDER BY updated_at, attempt_id
LIMIT ?`, strings.TrimSpace(accountID), limit)
	if err != nil {
		return maildomain.OutboundReconcileSummary{}, fmt.Errorf("select outbound reconciliation candidates: %w", err)
	}
	attempts := make([]maildomain.OutboundAttempt, 0, limit)
	for rows.Next() {
		attempt, scanErr := scanOutboundAttempt(rows)
		if scanErr != nil {
			rows.Close()
			return maildomain.OutboundReconcileSummary{}, scanErr
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return maildomain.OutboundReconcileSummary{}, fmt.Errorf("read outbound reconciliation candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return maildomain.OutboundReconcileSummary{}, fmt.Errorf("close outbound reconciliation candidates: %w", err)
	}

	summary := maildomain.OutboundReconcileSummary{Scanned: len(attempts)}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	for _, attempt := range attempts {
		candidates, candidateErr := sentEvidenceCandidates(ctx, tx, attempt.AccountID, attempt.RFCMessageID, 3)
		if candidateErr != nil {
			return maildomain.OutboundReconcileSummary{}, candidateErr
		}
		if len(candidates) == 0 {
			summary.Unmatched++
			continue
		}
		if len(candidates) != 1 || (attempt.MessageID != "" && attempt.MessageID != candidates[0].MessageID) {
			summary.Ambiguous++
			if _, err := tx.ExecContext(ctx, `
UPDATE outbound_attempts
SET reconciliation = 'unknown', reconciliation_error_code = ?, updated_at = ?
WHERE attempt_id = ?`, "ambiguous_sent_evidence", timestamp, attempt.ID); err != nil {
				return maildomain.OutboundReconcileSummary{}, fmt.Errorf("record ambiguous outbound reconciliation: %w", err)
			}
			continue
		}
		candidate := candidates[0]
		acceptedAt := candidate.ObservedAt
		if acceptedAt == "" && attempt.AcceptedAt != nil {
			acceptedAt = attempt.AcceptedAt.UTC().Format(time.RFC3339Nano)
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE outbound_attempts
SET status = 'sent', message_id = ?, thread_id = ?, reconciliation = 'confirmed',
    reconciled_location_id = ?, reconciliation_error_code = '', error_code = '',
    accepted_at = CASE WHEN accepted_at IS NULL OR accepted_at = '' THEN NULLIF(?, '') ELSE accepted_at END,
    updated_at = ?
WHERE attempt_id = ?`,
			candidate.MessageID, candidate.ThreadID, candidate.LocationID, acceptedAt, timestamp, attempt.ID,
		); err != nil {
			return maildomain.OutboundReconcileSummary{}, fmt.Errorf("confirm outbound reconciliation: %w", err)
		}
		if attempt.Status == maildomain.OutboundUnknown {
			summary.ResolvedUnknown++
		} else {
			summary.Confirmed++
		}
	}
	if err := tx.Commit(); err != nil {
		return maildomain.OutboundReconcileSummary{}, fmt.Errorf("commit outbound reconciliation: %w", err)
	}
	return summary, nil
}

type sentEvidenceCandidate struct {
	MessageID  string
	ThreadID   string
	LocationID string
	ObservedAt string
}

func sentEvidenceCandidates(ctx context.Context, tx *sql.Tx, accountID, rfcMessageID string, limit int) ([]sentEvidenceCandidate, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT m.message_id,
       COALESCE((SELECT thread_id FROM thread_messages WHERE message_id = m.message_id LIMIT 1), ''),
       l.location_id,
       COALESCE(l.internal_date, m.sort_at, '')
FROM messages AS m
JOIN message_locations AS l ON l.message_id = m.message_id AND l.present = 1
JOIN folders AS f ON f.folder_id = l.folder_id AND f.kind = 'sent'
WHERE m.account_id = ? AND lower(m.rfc_message_id) = ?
ORDER BY m.message_id, l.location_id
LIMIT ?`, strings.TrimSpace(accountID), maildomain.NormalizeMessageID(rfcMessageID), limit)
	if err != nil {
		return nil, fmt.Errorf("find Sent evidence for outbound attempt: %w", err)
	}
	defer rows.Close()
	values := make([]sentEvidenceCandidate, 0, limit)
	for rows.Next() {
		var value sentEvidenceCandidate
		if err := rows.Scan(&value.MessageID, &value.ThreadID, &value.LocationID, &value.ObservedAt); err != nil {
			return nil, fmt.Errorf("read Sent evidence for outbound attempt: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("find Sent evidence for outbound attempt: %w", err)
	}
	return values, nil
}

func verifyOutboundAttemptAttachments(ctx context.Context, tx *sql.Tx, attemptID string, values []maildomain.Attachment) error {
	rows, err := tx.QueryContext(ctx, `
SELECT ordinal, filename, media_type, content_hash, storage_key, byte_size
FROM outbound_attempt_attachments
WHERE attempt_id = ?
ORDER BY ordinal`, strings.TrimSpace(attemptID))
	if err != nil {
		return fmt.Errorf("read outbound attempt attachments: %w", err)
	}
	defer rows.Close()
	stored := make([]maildomain.OutboundAttachmentRecord, 0, len(values))
	for rows.Next() {
		var value maildomain.OutboundAttachmentRecord
		if err := rows.Scan(&value.Ordinal, &value.Filename, &value.MediaType, &value.ContentHash, &value.StorageKey, &value.ByteSize); err != nil {
			return fmt.Errorf("read outbound attempt attachment: %w", err)
		}
		stored = append(stored, value)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read outbound attempt attachments: %w", err)
	}
	if len(stored) != len(values) {
		return errors.New("accepted outbound attachment count does not match the pending attempt")
	}
	for index := range stored {
		if stored[index].Ordinal != index || stored[index].Filename != values[index].Filename || stored[index].MediaType != values[index].MediaType ||
			stored[index].ContentHash != values[index].ContentHash || stored[index].StorageKey != values[index].StorageKey || stored[index].ByteSize != values[index].ByteSize {
			return errors.New("accepted outbound attachment metadata does not match the pending attempt")
		}
	}
	return nil
}

func (repository *OutboundRepository) Attempt(ctx context.Context, attemptID string) (maildomain.OutboundAttempt, error) {
	return scanOutboundAttempt(repository.db.QueryRowContext(ctx, outboundAttemptColumns+` WHERE attempt_id = ?`, strings.TrimSpace(attemptID)))
}

func scanOutboundAttempt(row rowScanner) (maildomain.OutboundAttempt, error) {
	var value maildomain.OutboundAttempt
	var status, reconciliation, acceptedAt, createdAt, updatedAt sql.NullString
	if err := row.Scan(
		&value.ID, &value.AccountID, &value.ProposedMessageID, &value.RequestHash,
		&value.IdempotencyKey, &status, &value.RFCMessageID,
		&value.RawContentHash, &value.RawStorageKey, &value.RawByteSize,
		&value.ToCount, &value.CCCount, &value.BCCCount, &value.BodyPartCount,
		&value.AttachmentCount, &value.AttachmentBytes, &value.Kind, &value.ParentMessageID, &value.SourceMessageID,
		&value.MessageID, &value.ThreadID,
		&value.RetryOfAttemptID, &reconciliation, &value.ReconciledLocationID,
		&value.ReconciliationErrorCode, &value.ErrorCode,
		&acceptedAt, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.OutboundAttempt{}, maildomain.ErrOutboundAttemptNotFound
		}
		return maildomain.OutboundAttempt{}, fmt.Errorf("read outbound attempt: %w", err)
	}
	value.Status = maildomain.OutboundStatus(status.String)
	value.Reconciliation = maildomain.ReconciliationState(reconciliation.String)
	var err error
	value.AcceptedAt, err = parseNullableTime(acceptedAt)
	if err != nil {
		return maildomain.OutboundAttempt{}, fmt.Errorf("parse outbound acceptance time: %w", err)
	}
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt.String)
	if err != nil {
		return maildomain.OutboundAttempt{}, fmt.Errorf("parse outbound creation time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt.String)
	if err != nil {
		return maildomain.OutboundAttempt{}, fmt.Errorf("parse outbound update time: %w", err)
	}
	return value, nil
}
