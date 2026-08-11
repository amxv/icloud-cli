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

type OutboxRepository struct{ db *sql.DB }

const outboxItemColumns = `
SELECT outbox_id, account_id, source_attempt_id, latest_attempt_id, sender_identity_id,
       status, failure_stage, failure_code, request_hash, rfc_message_id,
       raw_content_hash, raw_storage_key, raw_byte_size, envelope_json,
       outbound_kind, parent_message_id, parent_location_id, source_message_id,
       to_count, cc_count, bcc_count, body_part_count, attachment_count, attachment_bytes,
       created_at, updated_at
FROM outbox_items`

func (repository *OutboxRepository) EnqueueSafeFailure(ctx context.Context, input maildomain.EnqueueOutboxItem) (maildomain.OutboxItem, maildomain.OutboundAttempt, error) {
	if !strings.HasPrefix(strings.TrimSpace(input.OutboxID), "out_") || !strings.HasPrefix(strings.TrimSpace(input.AttemptID), "send_") ||
		strings.TrimSpace(input.FailureStage) == "" || strings.TrimSpace(input.FailureCode) == "" || input.Now.IsZero() {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, maildomain.ErrOutboxInvalid
	}
	if err := input.Envelope.Validate(); err != nil {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, err
	}
	envelopeJSON, err := json.Marshal(input.Envelope)
	if err != nil {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, fmt.Errorf("encode outbox replay envelope: %w", err)
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, fmt.Errorf("begin outbox enqueue: %w", err)
	}
	defer tx.Rollback()
	attempt, err := scanOutboundAttempt(tx.QueryRowContext(ctx, outboundAttemptColumns+` WHERE attempt_id = ?`, strings.TrimSpace(input.AttemptID)))
	if err != nil {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, err
	}
	if attempt.Status != maildomain.OutboundPending {
		if existing, existingErr := scanOutboxItem(tx.QueryRowContext(ctx, outboxItemColumns+` WHERE account_id = ? AND source_attempt_id = ?`, attempt.AccountID, attempt.ID)); existingErr == nil {
			return existing, attempt, nil
		}
		return maildomain.OutboxItem{}, attempt, errors.New("enqueue outbox item: outbound attempt is not pending")
	}
	timestamp := input.Now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
UPDATE outbound_attempts
SET status = 'failed', reconciliation = 'none', error_code = ?, updated_at = ?
WHERE attempt_id = ? AND status = 'pending'`, strings.TrimSpace(input.FailureCode), timestamp, attempt.ID); err != nil {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, fmt.Errorf("record safe outbound failure: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO outbox_items (
    outbox_id, account_id, source_attempt_id, latest_attempt_id, sender_identity_id,
    status, failure_stage, failure_code, request_hash, rfc_message_id,
    raw_content_hash, raw_storage_key, raw_byte_size, envelope_json,
    outbound_kind, parent_message_id, parent_location_id, source_message_id,
    to_count, cc_count, bcc_count, body_part_count, attachment_count, attachment_bytes,
    created_at, updated_at
) VALUES (?, ?, ?, '', ?, 'retryable', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.OutboxID, attempt.AccountID, attempt.ID, attempt.SenderIdentityID,
		strings.TrimSpace(input.FailureStage), strings.TrimSpace(input.FailureCode), attempt.RequestHash, attempt.RFCMessageID,
		attempt.RawContentHash, attempt.RawStorageKey, attempt.RawByteSize, string(envelopeJSON),
		attempt.Kind, attempt.ParentMessageID, attempt.ParentLocationID, attempt.SourceMessageID,
		attempt.ToCount, attempt.CCCount, attempt.BCCCount, attempt.BodyPartCount, attempt.AttachmentCount, attempt.AttachmentBytes,
		timestamp, timestamp,
	); err != nil {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, fmt.Errorf("persist outbox item: %w", err)
	}
	item, err := scanOutboxItem(tx.QueryRowContext(ctx, outboxItemColumns+` WHERE outbox_id = ?`, input.OutboxID))
	if err != nil {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, err
	}
	attempt, err = scanOutboundAttempt(tx.QueryRowContext(ctx, outboundAttemptColumns+` WHERE attempt_id = ?`, attempt.ID))
	if err != nil {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, fmt.Errorf("commit outbox enqueue: %w", err)
	}
	return item, attempt, nil
}

func (repository *OutboxRepository) Item(ctx context.Context, accountID, selector string) (maildomain.OutboxItem, error) {
	accountID, selector = strings.TrimSpace(accountID), strings.TrimSpace(selector)
	if accountID == "" || selector == "" {
		return maildomain.OutboxItem{}, maildomain.ErrOutboxInvalid
	}
	return scanOutboxItem(repository.db.QueryRowContext(ctx, outboxItemColumns+` WHERE account_id = ? AND outbox_id = ?`, accountID, selector))
}

type storedOutboxCursor struct {
	Version   int    `json:"version"`
	AccountID string `json:"account_id"`
	CreatedAt string `json:"created_at"`
	OutboxID  string `json:"outbox_id"`
}

func (repository *OutboxRepository) ListPage(ctx context.Context, accountID string, limit int, cursor string) ([]maildomain.OutboxItem, bool, string, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || limit < 1 {
		return nil, false, "", maildomain.ErrOutboxInvalid
	}
	where := ` WHERE account_id = ?`
	args := []any{accountID}
	if strings.TrimSpace(cursor) != "" {
		var decoded storedOutboxCursor
		if err := decodeOpaqueCursor("obcur_", cursor, &decoded); err != nil || decoded.Version != 1 || decoded.AccountID != accountID || decoded.OutboxID == "" {
			return nil, false, "", maildomain.ErrOutboxInvalidCursor
		}
		if _, err := time.Parse(time.RFC3339Nano, decoded.CreatedAt); err != nil {
			return nil, false, "", maildomain.ErrOutboxInvalidCursor
		}
		where += ` AND (created_at < ? OR (created_at = ? AND outbox_id < ?))`
		args = append(args, decoded.CreatedAt, decoded.CreatedAt, decoded.OutboxID)
	}
	args = append(args, limit+1)
	rows, err := repository.db.QueryContext(ctx, outboxItemColumns+where+` ORDER BY created_at DESC, outbox_id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, false, "", fmt.Errorf("list outbox items: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.OutboxItem, 0, limit+1)
	for rows.Next() {
		value, scanErr := scanOutboxItem(rows)
		if scanErr != nil {
			return nil, false, "", scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, "", fmt.Errorf("list outbox items: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	next := ""
	if more && len(values) > 0 {
		last := values[len(values)-1]
		next, err = encodeOpaqueCursor("obcur_", storedOutboxCursor{Version: 1, AccountID: accountID, CreatedAt: last.CreatedAt.UTC().Format(time.RFC3339Nano), OutboxID: last.ID})
		if err != nil {
			return nil, false, "", fmt.Errorf("encode outbox cursor: %w", err)
		}
	}
	return values, more, next, nil
}

func (repository *OutboxRepository) Cancel(ctx context.Context, accountID, outboxID string, now time.Time) (maildomain.OutboxItem, error) {
	result, err := repository.db.ExecContext(ctx, `
UPDATE outbox_items
SET status = 'canceled', updated_at = ?
WHERE account_id = ? AND outbox_id = ? AND status = 'retryable'`, now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(accountID), strings.TrimSpace(outboxID))
	if err != nil {
		return maildomain.OutboxItem{}, fmt.Errorf("cancel outbox item: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		value, readErr := repository.Item(ctx, accountID, outboxID)
		if readErr != nil {
			return maildomain.OutboxItem{}, readErr
		}
		if value.Status != maildomain.OutboxCanceled {
			return maildomain.OutboxItem{}, maildomain.ErrOutboxNotRetryable
		}
	}
	return repository.Item(ctx, accountID, outboxID)
}

func (repository *OutboxRepository) PrepareRetry(ctx context.Context, accountID, outboxID, attemptID string, now time.Time) (maildomain.OutboxItem, error) {
	if !strings.HasPrefix(strings.TrimSpace(attemptID), "send_") {
		return maildomain.OutboxItem{}, maildomain.ErrOutboxInvalid
	}
	result, err := repository.db.ExecContext(ctx, `
UPDATE outbox_items
SET status = 'retrying', latest_attempt_id = ?, updated_at = ?
WHERE account_id = ? AND outbox_id = ? AND status = 'retryable'`, strings.TrimSpace(attemptID), now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(accountID), strings.TrimSpace(outboxID))
	if err != nil {
		return maildomain.OutboxItem{}, fmt.Errorf("prepare outbox retry: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return maildomain.OutboxItem{}, maildomain.ErrOutboxNotRetryable
	}
	return repository.Item(ctx, accountID, outboxID)
}

func (repository *OutboxRepository) ReleasePreparedRetry(ctx context.Context, accountID, outboxID, attemptID string, now time.Time) error {
	_, err := repository.db.ExecContext(ctx, `
UPDATE outbox_items
SET status = 'retryable', latest_attempt_id = '', updated_at = ?
WHERE account_id = ? AND outbox_id = ? AND status = 'retrying' AND latest_attempt_id = ?
  AND NOT EXISTS (SELECT 1 FROM outbound_attempts WHERE attempt_id = ?)`,
		now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(accountID), strings.TrimSpace(outboxID), strings.TrimSpace(attemptID), strings.TrimSpace(attemptID))
	if err != nil {
		return fmt.Errorf("release prepared outbox retry: %w", err)
	}
	return nil
}

func (repository *OutboxRepository) MarkRetryOutcome(ctx context.Context, accountID, outboxID, attemptID string, status maildomain.OutboundStatus, outboxEligible bool, failureStage, failureCode string, now time.Time) (maildomain.OutboxItem, maildomain.OutboundAttempt, error) {
	if status != maildomain.OutboundFailed && status != maildomain.OutboundUnknown {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, maildomain.ErrOutboxInvalid
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, fmt.Errorf("begin outbox retry outcome: %w", err)
	}
	defer tx.Rollback()
	outboxStatus := maildomain.OutboxRetryable
	reconciliation := maildomain.ReconciliationNone
	if status == maildomain.OutboundUnknown {
		outboxStatus = maildomain.OutboxUnknown
		reconciliation = maildomain.ReconciliationUnknown
	} else if !outboxEligible {
		outboxStatus = maildomain.OutboxFailed
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `
UPDATE outbound_attempts
SET status = ?, reconciliation = ?, error_code = ?, updated_at = ?
WHERE attempt_id = ? AND account_id = ? AND status = 'pending'`, status, reconciliation, strings.TrimSpace(failureCode), timestamp, strings.TrimSpace(attemptID), strings.TrimSpace(accountID))
	if err != nil {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, fmt.Errorf("record outbox retry attempt outcome: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, errors.New("record outbox retry attempt outcome: pending attempt not found")
	}
	result, err = tx.ExecContext(ctx, `
UPDATE outbox_items
SET status = ?, failure_stage = ?, failure_code = ?, updated_at = ?
WHERE account_id = ? AND outbox_id = ? AND status = 'retrying' AND latest_attempt_id = ?`,
		outboxStatus, strings.TrimSpace(failureStage), strings.TrimSpace(failureCode), timestamp, strings.TrimSpace(accountID), strings.TrimSpace(outboxID), strings.TrimSpace(attemptID))
	if err != nil {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, fmt.Errorf("record outbox retry item outcome: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, errors.New("record outbox retry item outcome: retrying item not found")
	}
	item, err := scanOutboxItem(tx.QueryRowContext(ctx, outboxItemColumns+` WHERE account_id = ? AND outbox_id = ?`, accountID, outboxID))
	if err != nil {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, err
	}
	attempt, err := scanOutboundAttempt(tx.QueryRowContext(ctx, outboundAttemptColumns+` WHERE attempt_id = ?`, attemptID))
	if err != nil {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, fmt.Errorf("commit outbox retry outcome: %w", err)
	}
	return item, attempt, nil
}

func (repository *OutboxRepository) MarkRetrySent(ctx context.Context, accountID, outboxID, attemptID string, now time.Time) (maildomain.OutboxItem, error) {
	result, err := repository.db.ExecContext(ctx, `
UPDATE outbox_items
SET status = 'sent', failure_stage = '', failure_code = '', updated_at = ?
WHERE account_id = ? AND outbox_id = ? AND status = 'retrying' AND latest_attempt_id = ?
  AND EXISTS (SELECT 1 FROM outbound_attempts WHERE attempt_id = ? AND status = 'sent')`,
		now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(accountID), strings.TrimSpace(outboxID), strings.TrimSpace(attemptID), strings.TrimSpace(attemptID))
	if err != nil {
		return maildomain.OutboxItem{}, fmt.Errorf("complete outbox retry: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return maildomain.OutboxItem{}, errors.New("complete outbox retry: sent linked attempt not found")
	}
	return repository.Item(ctx, accountID, outboxID)
}

// RepairInterrupted fails closed. If a previous process died after allocating
// the linked attempt, a still-pending attempt cannot prove that no bytes reached
// the provider, so both the attempt and Outbox item become unknown/non-retryable.
func (repository *OutboxRepository) RepairInterrupted(ctx context.Context, accountID string, now time.Time) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return maildomain.ErrOutboxInvalid
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin outbox repair: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
SELECT outbox_id, latest_attempt_id
FROM outbox_items
WHERE account_id = ? AND status = 'retrying'`, accountID)
	if err != nil {
		return fmt.Errorf("read interrupted outbox retries: %w", err)
	}
	type interrupted struct{ outboxID, attemptID string }
	items := make([]interrupted, 0, 4)
	for rows.Next() {
		var value interrupted
		if err := rows.Scan(&value.outboxID, &value.attemptID); err != nil {
			rows.Close()
			return fmt.Errorf("read interrupted outbox retry: %w", err)
		}
		items = append(items, value)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close interrupted outbox retries: %w", err)
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	for _, item := range items {
		var status, errorCode string
		err := tx.QueryRowContext(ctx, `SELECT status, error_code FROM outbound_attempts WHERE account_id = ? AND attempt_id = ?`, accountID, item.attemptID).Scan(&status, &errorCode)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.ExecContext(ctx, `UPDATE outbox_items SET status = 'retryable', latest_attempt_id = '', updated_at = ? WHERE outbox_id = ? AND status = 'retrying'`, timestamp, item.outboxID); err != nil {
				return fmt.Errorf("release unstarted outbox retry: %w", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("read interrupted outbound attempt: %w", err)
		}
		switch maildomain.OutboundStatus(status) {
		case maildomain.OutboundPending:
			if _, err := tx.ExecContext(ctx, `UPDATE outbound_attempts SET status = 'unknown', reconciliation = 'unknown', error_code = 'retry_interrupted', updated_at = ? WHERE attempt_id = ? AND status = 'pending'`, timestamp, item.attemptID); err != nil {
				return fmt.Errorf("mark interrupted outbound retry unknown: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE outbox_items SET status = 'unknown', failure_stage = 'retry_interrupted', failure_code = 'retry_interrupted', updated_at = ? WHERE outbox_id = ? AND status = 'retrying'`, timestamp, item.outboxID); err != nil {
				return fmt.Errorf("mark interrupted outbox retry unknown: %w", err)
			}
		case maildomain.OutboundFailed:
			// A process can only observe a terminal failed retry here if the attempt
			// outcome was persisted without the matching outbox transition. The
			// provider stage that made the failure safe is not durably encoded on
			// the attempt, so fail closed rather than silently restoring retryability.
			if _, err := tx.ExecContext(ctx, `UPDATE outbox_items SET status = 'failed', failure_stage = ?, failure_code = ?, updated_at = ? WHERE outbox_id = ? AND status = 'retrying'`, errorCode, errorCode, timestamp, item.outboxID); err != nil {
				return fmt.Errorf("restore failed outbox retry: %w", err)
			}
		case maildomain.OutboundUnknown:
			if _, err := tx.ExecContext(ctx, `UPDATE outbox_items SET status = 'unknown', failure_stage = ?, failure_code = ?, updated_at = ? WHERE outbox_id = ? AND status = 'retrying'`, errorCode, errorCode, timestamp, item.outboxID); err != nil {
				return fmt.Errorf("restore unknown outbox retry: %w", err)
			}
		case maildomain.OutboundSent:
			if _, err := tx.ExecContext(ctx, `UPDATE outbox_items SET status = 'sent', failure_stage = '', failure_code = '', updated_at = ? WHERE outbox_id = ? AND status = 'retrying'`, timestamp, item.outboxID); err != nil {
				return fmt.Errorf("restore sent outbox retry: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit outbox repair: %w", err)
	}
	return nil
}

func scanOutboxItem(row rowScanner) (maildomain.OutboxItem, error) {
	var value maildomain.OutboxItem
	var status, kind, envelopeJSON, createdAt, updatedAt string
	if err := row.Scan(
		&value.ID, &value.AccountID, &value.SourceAttemptID, &value.LatestAttemptID, &value.SenderIdentityID,
		&status, &value.FailureStage, &value.FailureCode, &value.RequestHash, &value.RFCMessageID,
		&value.RawContentHash, &value.RawStorageKey, &value.RawByteSize, &envelopeJSON,
		&kind, &value.ParentMessageID, &value.ParentLocationID, &value.SourceMessageID,
		&value.ToCount, &value.CCCount, &value.BCCCount, &value.BodyPartCount, &value.AttachmentCount, &value.AttachmentBytes,
		&createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.OutboxItem{}, maildomain.ErrOutboxNotFound
		}
		return maildomain.OutboxItem{}, fmt.Errorf("read outbox item: %w", err)
	}
	value.Status = maildomain.OutboxStatus(status)
	value.Kind = maildomain.OutboundKind(kind)
	if value.SourceMessageID != "" {
		value.Kind = maildomain.OutboundKindForward
	}
	if err := json.Unmarshal([]byte(envelopeJSON), &value.Envelope); err != nil {
		return maildomain.OutboxItem{}, fmt.Errorf("decode private outbox envelope: %w", err)
	}
	if err := value.Envelope.Validate(); err != nil {
		return maildomain.OutboxItem{}, fmt.Errorf("validate private outbox envelope: %w", err)
	}
	var err error
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return maildomain.OutboxItem{}, fmt.Errorf("parse outbox creation time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.OutboxItem{}, fmt.Errorf("parse outbox update time: %w", err)
	}
	return value, nil
}
