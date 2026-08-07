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

const backfillFrontierColumns = `
SELECT frontier_id, account_id, folder_id, after_time, before_time, uid_validity,
       before_uid, complete, last_attempt_at, last_success_at, last_error_code,
       created_at, updated_at
FROM mail_backfill_frontiers`

func (repository *MailRepository) BackfillFrontier(ctx context.Context, accountID, folderID string, after, before *time.Time) (maildomain.BackfillFrontier, error) {
	return scanBackfillFrontier(repository.db.QueryRowContext(ctx, backfillFrontierColumns+`
WHERE account_id = ? AND folder_id = ? AND after_time = ? AND before_time = ?`,
		strings.TrimSpace(accountID), strings.TrimSpace(folderID), operationTimeKey(after), operationTimeKey(before)))
}

func (repository *MailRepository) BackfillFrontiers(ctx context.Context, accountID string, limit int) ([]maildomain.BackfillFrontier, bool, error) {
	if limit < 1 {
		return nil, false, errors.New("list backfill frontiers: limit must be positive")
	}
	rows, err := repository.db.QueryContext(ctx, backfillFrontierColumns+`
WHERE account_id = ? ORDER BY complete, updated_at DESC, frontier_id LIMIT ?`, strings.TrimSpace(accountID), limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list backfill frontiers: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.BackfillFrontier, 0, limit+1)
	for rows.Next() {
		value, scanErr := scanBackfillFrontier(rows)
		if scanErr != nil {
			return nil, false, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list backfill frontiers: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	return values, more, nil
}

func (repository *MailRepository) CommitBackfillPage(ctx context.Context, page maildomain.CommitBackfillPage) (maildomain.BackfillCommitResult, error) {
	if strings.TrimSpace(page.FrontierID) == "" || strings.TrimSpace(page.AccountID) == "" || strings.TrimSpace(page.FolderID) == "" || page.UIDValidity == 0 || page.StartBeforeUID == 0 {
		return maildomain.BackfillCommitResult{}, errors.New("commit backfill page: frontier, account, folder, UID validity, and start cursor are required")
	}
	if page.NextBeforeUID > page.StartBeforeUID {
		return maildomain.BackfillCommitResult{}, errors.New("commit backfill page: reverse frontier cannot move forward")
	}
	for _, record := range page.Records {
		if record.UID >= page.StartBeforeUID || (!page.Complete && page.NextBeforeUID != 0 && record.UID < page.NextBeforeUID) {
			return maildomain.BackfillCommitResult{}, errors.New("commit backfill page: record lies outside the acknowledged reverse page")
		}
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.BackfillCommitResult{}, fmt.Errorf("begin backfill page commit: %w", err)
	}
	defer tx.Rollback()
	previous, previousErr := backfillFrontierInTx(ctx, tx, page.AccountID, page.FolderID, page.After, page.Before)
	if previousErr != nil && !errors.Is(previousErr, maildomain.ErrBackfillFrontierNotFound) {
		return maildomain.BackfillCommitResult{}, previousErr
	}
	if previousErr == nil {
		if previous.Complete {
			return maildomain.BackfillCommitResult{}, errors.New("commit backfill page: historical frontier is already complete")
		}
		if previous.UIDValidity != page.UIDValidity {
			return maildomain.BackfillCommitResult{}, maildomain.ErrRepairRequired
		}
		if previous.BeforeUID != page.StartBeforeUID {
			return maildomain.BackfillCommitResult{}, errors.New("commit backfill page: reverse frontier changed concurrently")
		}
	}
	timestamp := page.AttemptedAt.UTC().Format(time.RFC3339Nano)
	ingested, err := ingestRecordsInTx(ctx, tx, page.AccountID, page.FolderID, page.UIDValidity, 0, page.Records, timestamp)
	if err != nil {
		return maildomain.BackfillCommitResult{}, err
	}
	if err := rebuildCanonicalThreadProjection(ctx, tx, page.AccountID, mapKeysSorted(ingested.Affected), page.AttemptedAt); err != nil {
		return maildomain.BackfillCommitResult{}, err
	}
	frontierID, createdAt := strings.TrimSpace(page.FrontierID), timestamp
	if previousErr == nil {
		frontierID, createdAt = previous.ID, previous.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO mail_backfill_frontiers (
    frontier_id, account_id, folder_id, after_time, before_time, uid_validity,
    before_uid, complete, last_attempt_at, last_success_at, last_error_code,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?)
ON CONFLICT(account_id, folder_id, after_time, before_time) DO UPDATE SET
    uid_validity = excluded.uid_validity,
    before_uid = excluded.before_uid,
    complete = excluded.complete,
    last_attempt_at = excluded.last_attempt_at,
    last_success_at = excluded.last_success_at,
    last_error_code = '',
    updated_at = excluded.updated_at`,
		frontierID, page.AccountID, page.FolderID, operationTimeKey(page.After), operationTimeKey(page.Before), page.UIDValidity,
		page.NextBeforeUID, boolInt(page.Complete), timestamp, timestamp, createdAt, timestamp); err != nil {
		return maildomain.BackfillCommitResult{}, fmt.Errorf("persist backfill frontier: %w", err)
	}
	frontier, err := backfillFrontierInTx(ctx, tx, page.AccountID, page.FolderID, page.After, page.Before)
	if err != nil {
		return maildomain.BackfillCommitResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.BackfillCommitResult{}, fmt.Errorf("commit backfill page: %w", err)
	}
	return maildomain.BackfillCommitResult{Created: ingested.Created, Updated: ingested.Updated, IdentityMerges: ingested.IdentityMerges, DedupeConflicts: ingested.DedupeConflicts, Frontier: frontier}, nil
}

func (repository *MailRepository) RecordBackfillFailure(ctx context.Context, frontierID, accountID, folderID string, after, before *time.Time, uidValidity, beforeUID uint32, code string, attemptedAt time.Time) error {
	if strings.TrimSpace(frontierID) == "" || uidValidity == 0 || beforeUID == 0 {
		return errors.New("record backfill failure: frontier, UID validity, and cursor are required")
	}
	timestamp := attemptedAt.UTC().Format(time.RFC3339Nano)
	_, err := repository.db.ExecContext(ctx, `
INSERT INTO mail_backfill_frontiers (
    frontier_id, account_id, folder_id, after_time, before_time, uid_validity,
    before_uid, complete, last_attempt_at, last_error_code, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)
ON CONFLICT(account_id, folder_id, after_time, before_time) DO UPDATE SET
    last_attempt_at = excluded.last_attempt_at,
    last_error_code = excluded.last_error_code,
    updated_at = excluded.updated_at`,
		frontierID, strings.TrimSpace(accountID), strings.TrimSpace(folderID), operationTimeKey(after), operationTimeKey(before), uidValidity,
		beforeUID, timestamp, strings.TrimSpace(code), timestamp, timestamp)
	if err != nil {
		return fmt.Errorf("record backfill failure: %w", err)
	}
	return nil
}

func (repository *MailRepository) RebaselineCheckpoint(ctx context.Context, checkpointID, accountID, folderID string, uidValidity, tail uint32, now time.Time) (maildomain.Checkpoint, error) {
	if strings.TrimSpace(checkpointID) == "" || strings.TrimSpace(accountID) == "" || strings.TrimSpace(folderID) == "" || uidValidity == 0 {
		return maildomain.Checkpoint{}, errors.New("rebaseline checkpoint: identity and UID validity are required")
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.Checkpoint{}, fmt.Errorf("begin checkpoint rebaseline: %w", err)
	}
	defer tx.Rollback()
	previous, previousErr := checkpointInTx(ctx, tx, accountID, folderID)
	if previousErr != nil && !errors.Is(previousErr, maildomain.ErrCheckpointNotFound) {
		return maildomain.Checkpoint{}, previousErr
	}
	timestamp, createdAt := now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano)
	if previousErr == nil {
		checkpointID, createdAt = previous.ID, previous.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO sync_checkpoints (
 checkpoint_id, account_id, folder_id, uid_validity, last_committed_uid, last_scanned_uid,
 bootstrap_complete, state, last_error_code, more_available, last_attempt_at, last_success_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, 1, 'idle', '', 0, ?, ?, ?, ?)
ON CONFLICT(account_id, folder_id) DO UPDATE SET
 uid_validity=excluded.uid_validity, last_committed_uid=excluded.last_committed_uid,
 last_scanned_uid=excluded.last_scanned_uid, bootstrap_complete=1, state='idle',
 last_error_code='', more_available=0, last_attempt_at=excluded.last_attempt_at,
 last_success_at=excluded.last_success_at, updated_at=excluded.updated_at`,
		checkpointID, accountID, folderID, uidValidity, tail, tail, timestamp, timestamp, createdAt, timestamp); err != nil {
		return maildomain.Checkpoint{}, fmt.Errorf("persist checkpoint rebaseline: %w", err)
	}
	value, err := checkpointInTx(ctx, tx, accountID, folderID)
	if err != nil {
		return maildomain.Checkpoint{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.Checkpoint{}, fmt.Errorf("commit checkpoint rebaseline: %w", err)
	}
	return value, nil
}

func (repository *MailRepository) CachedMessageCount(ctx context.Context, accountID, folderID string) (int, error) {
	var count int
	if err := repository.db.QueryRowContext(ctx, `SELECT count(DISTINCT message_id) FROM message_locations WHERE account_id=? AND folder_id=?`, strings.TrimSpace(accountID), strings.TrimSpace(folderID)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count cached folder messages: %w", err)
	}
	return count, nil
}

func (repository *MailRepository) ResetBackfillFrontier(ctx context.Context, frontierID string, uidValidity, beforeUID uint32, now time.Time) error {
	if uidValidity == 0 || beforeUID == 0 {
		return errors.New("reset backfill frontier: UID validity and current provider cursor are required")
	}
	result, err := repository.db.ExecContext(ctx, `UPDATE mail_backfill_frontiers
SET uid_validity=?, before_uid=?, complete=0, last_error_code='', updated_at=? WHERE frontier_id=?`,
		uidValidity, beforeUID, now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(frontierID))
	if err != nil {
		return fmt.Errorf("reset backfill frontier: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return maildomain.ErrBackfillFrontierNotFound
	}
	return nil
}

func backfillFrontierInTx(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, accountID, folderID string, after, before *time.Time) (maildomain.BackfillFrontier, error) {
	return scanBackfillFrontier(queryer.QueryRowContext(ctx, backfillFrontierColumns+`
WHERE account_id = ? AND folder_id = ? AND after_time = ? AND before_time = ?`, accountID, folderID, operationTimeKey(after), operationTimeKey(before)))
}

func scanBackfillFrontier(row rowScanner) (maildomain.BackfillFrontier, error) {
	var value maildomain.BackfillFrontier
	var after, before, lastAttempt, lastSuccess sql.NullString
	var complete int
	var createdAt, updatedAt string
	if err := row.Scan(&value.ID, &value.AccountID, &value.FolderID, &after, &before, &value.UIDValidity, &value.BeforeUID, &complete, &lastAttempt, &lastSuccess, &value.LastErrorCode, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.BackfillFrontier{}, maildomain.ErrBackfillFrontierNotFound
		}
		return maildomain.BackfillFrontier{}, fmt.Errorf("read backfill frontier: %w", err)
	}
	value.Complete = complete != 0
	var err error
	if value.After, err = parseOptionalOperationTime(after.String); err != nil {
		return maildomain.BackfillFrontier{}, err
	}
	if value.Before, err = parseOptionalOperationTime(before.String); err != nil {
		return maildomain.BackfillFrontier{}, err
	}
	if value.LastAttemptAt, err = parseOptionalOperationTime(lastAttempt.String); err != nil {
		return maildomain.BackfillFrontier{}, err
	}
	if value.LastSuccessAt, err = parseOptionalOperationTime(lastSuccess.String); err != nil {
		return maildomain.BackfillFrontier{}, err
	}
	if value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return maildomain.BackfillFrontier{}, fmt.Errorf("parse backfill frontier creation time: %w", err)
	}
	if value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return maildomain.BackfillFrontier{}, fmt.Errorf("parse backfill frontier update time: %w", err)
	}
	return value, nil
}

func operationTimeKey(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseOptionalOperationTime(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, fmt.Errorf("parse operation time: %w", err)
	}
	parsed = parsed.UTC()
	return &parsed, nil
}
