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
       message_id, thread_id, reconciliation, error_code,
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
    reconciliation, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?, 'none', ?, ?)`,
		input.AttemptID, input.AccountID, input.ProposedMessageID, input.RequestHash,
		strings.TrimSpace(input.IdempotencyKey), input.RFCMessageID,
		input.RawContentHash, input.RawStorageKey, input.RawByteSize,
		input.ToCount, input.CCCount, input.BCCCount, input.BodyPartCount,
		timestamp, timestamp,
	); err != nil {
		return maildomain.OutboundAttempt{}, false, fmt.Errorf("persist pending outbound attempt: %w", err)
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
	if err := rebuildAffectedThreadGraph(ctx, tx, attempt.AccountID, []string{attempt.ProposedMessageID}, input.AcceptedAt); err != nil {
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
		&value.MessageID, &value.ThreadID, &reconciliation, &value.ErrorCode,
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
