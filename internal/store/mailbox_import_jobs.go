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

type MailboxImportJobRepository struct {
	db *sql.DB
}

const mailboxImportJobColumns = `
SELECT job_id, account_id, folder_id, source_path, source_content_hash, source_byte_size,
       next_offset, next_index, imported_count, status, error_code, created_at, updated_at
FROM mailbox_import_jobs`

const mailboxImportItemColumns = `
SELECT job_id, item_index, start_offset, next_offset, raw_content_hash, raw_byte_size,
       idempotency_key, status, attempt_count, retry_new_key, import_id, message_id, location_id,
       error_code, created_at, updated_at
FROM mailbox_import_items`

func (repository *MailboxImportJobRepository) Resolve(ctx context.Context, accountID, folderID, sourcePath, sourceHash string, sourceSize int64) (maildomain.MailboxImportJob, error) {
	accountID = strings.TrimSpace(accountID)
	folderID = strings.TrimSpace(folderID)
	sourcePath = strings.TrimSpace(sourcePath)
	sourceHash = strings.ToLower(strings.TrimSpace(sourceHash))
	if accountID == "" || folderID == "" || sourcePath == "" || len(sourceHash) != 64 || sourceSize < 0 {
		return maildomain.MailboxImportJob{}, maildomain.ErrMailboxImportJobNotFound
	}

	active, err := scanMailboxImportJob(repository.db.QueryRowContext(ctx, mailboxImportJobColumns+`
 WHERE account_id = ? AND folder_id = ? AND source_path = ? AND status != 'confirmed'
 ORDER BY created_at DESC, job_id DESC LIMIT 1`, accountID, folderID, sourcePath))
	if err == nil {
		if !strings.EqualFold(active.SourceContentHash, sourceHash) || active.SourceByteSize != sourceSize {
			return maildomain.MailboxImportJob{}, maildomain.ErrMailboxImportJobConflict
		}
		return active, nil
	}
	if !errors.Is(err, maildomain.ErrMailboxImportJobNotFound) {
		return maildomain.MailboxImportJob{}, err
	}

	return scanMailboxImportJob(repository.db.QueryRowContext(ctx, mailboxImportJobColumns+`
 WHERE account_id = ? AND folder_id = ? AND source_content_hash = ? AND source_byte_size = ?
 ORDER BY created_at DESC, job_id DESC LIMIT 1`, accountID, folderID, sourceHash, sourceSize))
}

func (repository *MailboxImportJobRepository) Begin(ctx context.Context, input maildomain.BeginMailboxImportJob) (maildomain.MailboxImportJob, bool, error) {
	input.JobID = strings.TrimSpace(input.JobID)
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.FolderID = strings.TrimSpace(input.FolderID)
	input.SourcePath = strings.TrimSpace(input.SourcePath)
	input.SourceContentHash = strings.ToLower(strings.TrimSpace(input.SourceContentHash))
	if !strings.HasPrefix(input.JobID, "mbi_") || !strings.HasPrefix(input.AccountID, "acct_") ||
		!strings.HasPrefix(input.FolderID, "fld_") || input.SourcePath == "" || len(input.SourceContentHash) != 64 || input.SourceByteSize < 0 {
		return maildomain.MailboxImportJob{}, false, maildomain.ErrInvalidMailboxImport
	}

	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.MailboxImportJob{}, false, fmt.Errorf("begin mailbox import job: %w", err)
	}
	defer tx.Rollback()

	active, activeErr := scanMailboxImportJob(tx.QueryRowContext(ctx, mailboxImportJobColumns+`
 WHERE account_id = ? AND folder_id = ? AND source_path = ? AND status != 'confirmed'
 ORDER BY created_at DESC, job_id DESC LIMIT 1`, input.AccountID, input.FolderID, input.SourcePath))
	if activeErr == nil {
		if !strings.EqualFold(active.SourceContentHash, input.SourceContentHash) || active.SourceByteSize != input.SourceByteSize {
			return maildomain.MailboxImportJob{}, false, maildomain.ErrMailboxImportJobConflict
		}
		return active, true, nil
	}
	if !errors.Is(activeErr, maildomain.ErrMailboxImportJobNotFound) {
		return maildomain.MailboxImportJob{}, false, activeErr
	}

	existing, existingErr := scanMailboxImportJob(tx.QueryRowContext(ctx, mailboxImportJobColumns+`
 WHERE account_id = ? AND folder_id = ? AND source_content_hash = ? AND source_byte_size = ?
 ORDER BY created_at DESC, job_id DESC LIMIT 1`, input.AccountID, input.FolderID, input.SourceContentHash, input.SourceByteSize))
	if existingErr == nil {
		return existing, true, nil
	}
	if !errors.Is(existingErr, maildomain.ErrMailboxImportJobNotFound) {
		return maildomain.MailboxImportJob{}, false, existingErr
	}

	timestamp := input.Now.UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `
INSERT INTO mailbox_import_jobs (
    job_id, account_id, folder_id, source_path, source_content_hash, source_byte_size,
    next_offset, next_index, imported_count, status, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, 0, 1, 0, 'running', ?, ?)`,
		input.JobID, input.AccountID, input.FolderID, input.SourcePath, input.SourceContentHash, input.SourceByteSize, timestamp, timestamp)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return maildomain.MailboxImportJob{}, false, maildomain.ErrMailboxImportJobConflict
		}
		return maildomain.MailboxImportJob{}, false, fmt.Errorf("insert mailbox import job: %w", err)
	}
	value, err := scanMailboxImportJob(tx.QueryRowContext(ctx, mailboxImportJobColumns+` WHERE job_id = ?`, input.JobID))
	if err != nil {
		return maildomain.MailboxImportJob{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.MailboxImportJob{}, false, fmt.Errorf("commit mailbox import job: %w", err)
	}
	return value, false, nil
}

func (repository *MailboxImportJobRepository) Job(ctx context.Context, jobID string) (maildomain.MailboxImportJob, error) {
	return scanMailboxImportJob(repository.db.QueryRowContext(ctx, mailboxImportJobColumns+` WHERE job_id = ?`, strings.TrimSpace(jobID)))
}

func (repository *MailboxImportJobRepository) Item(ctx context.Context, jobID string, index int) (maildomain.MailboxImportItem, error) {
	return scanMailboxImportItem(repository.db.QueryRowContext(ctx, mailboxImportItemColumns+` WHERE job_id = ? AND item_index = ?`, strings.TrimSpace(jobID), index))
}

func (repository *MailboxImportJobRepository) PrepareItem(ctx context.Context, input maildomain.PrepareMailboxImportItem) (maildomain.MailboxImportItem, bool, error) {
	input.JobID = strings.TrimSpace(input.JobID)
	input.RawContentHash = strings.ToLower(strings.TrimSpace(input.RawContentHash))
	if !strings.HasPrefix(input.JobID, "mbi_") || input.Index < 1 || input.StartOffset < 0 || input.NextOffset <= input.StartOffset ||
		len(input.RawContentHash) != 64 || input.RawByteSize < 0 {
		return maildomain.MailboxImportItem{}, false, maildomain.ErrInvalidMailboxImport
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.MailboxImportItem{}, false, fmt.Errorf("begin mailbox import item: %w", err)
	}
	defer tx.Rollback()

	if _, err := scanMailboxImportJob(tx.QueryRowContext(ctx, mailboxImportJobColumns+` WHERE job_id = ?`, input.JobID)); err != nil {
		return maildomain.MailboxImportItem{}, false, err
	}
	existing, scanErr := scanMailboxImportItem(tx.QueryRowContext(ctx, mailboxImportItemColumns+` WHERE job_id = ? AND item_index = ?`, input.JobID, input.Index))
	if scanErr == nil {
		if existing.StartOffset != input.StartOffset || existing.NextOffset != input.NextOffset || !strings.EqualFold(existing.RawContentHash, input.RawContentHash) || existing.RawByteSize != input.RawByteSize {
			return maildomain.MailboxImportItem{}, false, maildomain.ErrMailboxImportJobConflict
		}
		if existing.Status == maildomain.MailboxImportItemConfirmed {
			return existing, true, nil
		}
		attempt := existing.AttemptCount
		key := existing.IdempotencyKey
		if existing.Status == maildomain.MailboxImportItemFailed && existing.RetryNewKey {
			attempt++
			key = maildomain.MailboxImportItemKey(input.JobID, input.Index, attempt, input.RawContentHash)
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE mailbox_import_items
SET status = 'prepared', attempt_count = ?, idempotency_key = ?, retry_new_key = 0, error_code = '', updated_at = ?
WHERE job_id = ? AND item_index = ?`, attempt, key, input.Now.UTC().Format(time.RFC3339Nano), input.JobID, input.Index); err != nil {
			return maildomain.MailboxImportItem{}, false, fmt.Errorf("prepare existing mailbox import item: %w", err)
		}
		value, err := scanMailboxImportItem(tx.QueryRowContext(ctx, mailboxImportItemColumns+` WHERE job_id = ? AND item_index = ?`, input.JobID, input.Index))
		if err != nil {
			return maildomain.MailboxImportItem{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return maildomain.MailboxImportItem{}, false, fmt.Errorf("commit mailbox import item retry: %w", err)
		}
		return value, true, nil
	}
	if !errors.Is(scanErr, maildomain.ErrMailboxImportJobNotFound) {
		return maildomain.MailboxImportItem{}, false, scanErr
	}

	key := maildomain.MailboxImportItemKey(input.JobID, input.Index, 1, input.RawContentHash)
	timestamp := input.Now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO mailbox_import_items (
    job_id, item_index, start_offset, next_offset, raw_content_hash, raw_byte_size,
    idempotency_key, status, attempt_count, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 'prepared', 1, ?, ?)`,
		input.JobID, input.Index, input.StartOffset, input.NextOffset, input.RawContentHash, input.RawByteSize,
		key, timestamp, timestamp); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return maildomain.MailboxImportItem{}, false, maildomain.ErrMailboxImportJobConflict
		}
		return maildomain.MailboxImportItem{}, false, fmt.Errorf("insert mailbox import item: %w", err)
	}
	value, err := scanMailboxImportItem(tx.QueryRowContext(ctx, mailboxImportItemColumns+` WHERE job_id = ? AND item_index = ?`, input.JobID, input.Index))
	if err != nil {
		return maildomain.MailboxImportItem{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.MailboxImportItem{}, false, fmt.Errorf("commit mailbox import item: %w", err)
	}
	return value, false, nil
}

func (repository *MailboxImportJobRepository) FailItem(ctx context.Context, jobID string, index int, errorCode string, retryNewKey bool, now time.Time) (maildomain.MailboxImportJob, maildomain.MailboxImportItem, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, fmt.Errorf("begin mailbox import failure: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
UPDATE mailbox_import_items SET status = 'failed', retry_new_key = ?, error_code = ?, updated_at = ?
WHERE job_id = ? AND item_index = ? AND status != 'confirmed'`, boolInt(retryNewKey), strings.TrimSpace(errorCode), now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(jobID), index); err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, fmt.Errorf("mark mailbox import item failed: %w", err)
	}
	item, err := scanMailboxImportItem(tx.QueryRowContext(ctx, mailboxImportItemColumns+` WHERE job_id = ? AND item_index = ?`, strings.TrimSpace(jobID), index))
	if err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE mailbox_import_jobs SET status = 'partial', error_code = ?, updated_at = ? WHERE job_id = ? AND status != 'confirmed'`,
		strings.TrimSpace(errorCode), now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(jobID)); err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, fmt.Errorf("mark mailbox import job partial: %w", err)
	}
	job, err := scanMailboxImportJob(tx.QueryRowContext(ctx, mailboxImportJobColumns+` WHERE job_id = ?`, strings.TrimSpace(jobID)))
	if err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, fmt.Errorf("commit mailbox import failure: %w", err)
	}
	return job, item, nil
}

func (repository *MailboxImportJobRepository) ConfirmItem(ctx context.Context, jobID string, index int, importID, messageID, locationID string, now time.Time) (maildomain.MailboxImportJob, maildomain.MailboxImportItem, error) {
	jobID = strings.TrimSpace(jobID)
	importID = strings.TrimSpace(importID)
	messageID = strings.TrimSpace(messageID)
	locationID = strings.TrimSpace(locationID)
	if !strings.HasPrefix(importID, "imp_") || !strings.HasPrefix(messageID, "msg_") || !strings.HasPrefix(locationID, "loc_") {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, maildomain.ErrInvalidMailboxImport
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, fmt.Errorf("begin mailbox import confirmation: %w", err)
	}
	defer tx.Rollback()
	item, err := scanMailboxImportItem(tx.QueryRowContext(ctx, mailboxImportItemColumns+` WHERE job_id = ? AND item_index = ?`, jobID, index))
	if err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, err
	}
	if item.Status == maildomain.MailboxImportItemConfirmed {
		if item.ImportID != importID || item.MessageID != messageID || item.LocationID != locationID {
			return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, maildomain.ErrMailboxImportJobConflict
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
UPDATE mailbox_import_items
SET status = 'confirmed', import_id = ?, message_id = ?, location_id = ?, error_code = '', updated_at = ?
WHERE job_id = ? AND item_index = ?`, importID, messageID, locationID, now.UTC().Format(time.RFC3339Nano), jobID, index); err != nil {
			return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, fmt.Errorf("confirm mailbox import item: %w", err)
		}
	}
	if err := advanceMailboxImportFrontierTx(ctx, tx, jobID, now); err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, err
	}
	item, err = scanMailboxImportItem(tx.QueryRowContext(ctx, mailboxImportItemColumns+` WHERE job_id = ? AND item_index = ?`, jobID, index))
	if err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, err
	}
	job, err := scanMailboxImportJob(tx.QueryRowContext(ctx, mailboxImportJobColumns+` WHERE job_id = ?`, jobID))
	if err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, fmt.Errorf("commit mailbox import confirmation: %w", err)
	}
	return job, item, nil
}

func advanceMailboxImportFrontierTx(ctx context.Context, tx *sql.Tx, jobID string, now time.Time) error {
	job, err := scanMailboxImportJob(tx.QueryRowContext(ctx, mailboxImportJobColumns+` WHERE job_id = ?`, jobID))
	if err != nil {
		return err
	}
	nextOffset := job.NextOffset
	nextIndex := job.NextIndex
	for {
		item, itemErr := scanMailboxImportItem(tx.QueryRowContext(ctx, mailboxImportItemColumns+` WHERE job_id = ? AND item_index = ?`, jobID, nextIndex))
		if errors.Is(itemErr, maildomain.ErrMailboxImportJobNotFound) {
			break
		}
		if itemErr != nil {
			return itemErr
		}
		if item.Status != maildomain.MailboxImportItemConfirmed {
			break
		}
		if item.StartOffset != nextOffset || item.NextOffset <= item.StartOffset {
			return maildomain.ErrMailboxImportJobConflict
		}
		nextOffset = item.NextOffset
		nextIndex++
	}
	var imported, failed int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mailbox_import_items WHERE job_id = ? AND status = 'confirmed'`, jobID).Scan(&imported); err != nil {
		return fmt.Errorf("count confirmed mailbox import items: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mailbox_import_items WHERE job_id = ? AND status = 'failed'`, jobID).Scan(&failed); err != nil {
		return fmt.Errorf("count failed mailbox import items: %w", err)
	}
	status := maildomain.MailboxImportJobRunning
	errorCode := ""
	if failed > 0 {
		status = maildomain.MailboxImportJobPartial
		_ = tx.QueryRowContext(ctx, `SELECT error_code FROM mailbox_import_items WHERE job_id = ? AND status = 'failed' ORDER BY item_index LIMIT 1`, jobID).Scan(&errorCode)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE mailbox_import_jobs SET next_offset = ?, next_index = ?, imported_count = ?, status = ?, error_code = ?, updated_at = ?
WHERE job_id = ? AND status != 'confirmed'`, nextOffset, nextIndex, imported, status, errorCode, now.UTC().Format(time.RFC3339Nano), jobID); err != nil {
		return fmt.Errorf("advance mailbox import frontier: %w", err)
	}
	return nil
}

func (repository *MailboxImportJobRepository) MarkError(ctx context.Context, jobID, errorCode string, now time.Time) (maildomain.MailboxImportJob, error) {
	if _, err := repository.db.ExecContext(ctx, `
UPDATE mailbox_import_jobs SET status = 'partial', error_code = ?, updated_at = ?
WHERE job_id = ? AND status != 'confirmed'`, strings.TrimSpace(errorCode), now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(jobID)); err != nil {
		return maildomain.MailboxImportJob{}, fmt.Errorf("mark mailbox import job error: %w", err)
	}
	return repository.Job(ctx, jobID)
}

func (repository *MailboxImportJobRepository) MarkComplete(ctx context.Context, jobID string, now time.Time) (maildomain.MailboxImportJob, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.MailboxImportJob{}, fmt.Errorf("begin mailbox import completion: %w", err)
	}
	defer tx.Rollback()
	job, err := scanMailboxImportJob(tx.QueryRowContext(ctx, mailboxImportJobColumns+` WHERE job_id = ?`, strings.TrimSpace(jobID)))
	if err != nil {
		return maildomain.MailboxImportJob{}, err
	}
	if job.Status == maildomain.MailboxImportJobConfirmed {
		return job, nil
	}
	var unresolved int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mailbox_import_items WHERE job_id = ? AND status != 'confirmed'`, job.ID).Scan(&unresolved); err != nil {
		return maildomain.MailboxImportJob{}, fmt.Errorf("count unresolved mailbox import items: %w", err)
	}
	if job.NextOffset != job.SourceByteSize || unresolved != 0 {
		return job, maildomain.ErrMailboxImportJobConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mailbox_import_jobs SET status = 'confirmed', error_code = '', updated_at = ? WHERE job_id = ?`, now.UTC().Format(time.RFC3339Nano), job.ID); err != nil {
		return maildomain.MailboxImportJob{}, fmt.Errorf("complete mailbox import job: %w", err)
	}
	job, err = scanMailboxImportJob(tx.QueryRowContext(ctx, mailboxImportJobColumns+` WHERE job_id = ?`, job.ID))
	if err != nil {
		return maildomain.MailboxImportJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.MailboxImportJob{}, fmt.Errorf("commit mailbox import completion: %w", err)
	}
	return job, nil
}

func scanMailboxImportJob(row rowScanner) (maildomain.MailboxImportJob, error) {
	var value maildomain.MailboxImportJob
	var status, createdAt, updatedAt string
	if err := row.Scan(&value.ID, &value.AccountID, &value.FolderID, &value.SourcePath, &value.SourceContentHash, &value.SourceByteSize,
		&value.NextOffset, &value.NextIndex, &value.ImportedCount, &status, &value.ErrorCode, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.MailboxImportJob{}, maildomain.ErrMailboxImportJobNotFound
		}
		return maildomain.MailboxImportJob{}, fmt.Errorf("read mailbox import job: %w", err)
	}
	var err error
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return maildomain.MailboxImportJob{}, fmt.Errorf("parse mailbox import job creation time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.MailboxImportJob{}, fmt.Errorf("parse mailbox import job update time: %w", err)
	}
	value.Status = maildomain.MailboxImportJobStatus(status)
	return value, nil
}

func scanMailboxImportItem(row rowScanner) (maildomain.MailboxImportItem, error) {
	var value maildomain.MailboxImportItem
	var status, createdAt, updatedAt string
	var retryNewKey int
	if err := row.Scan(&value.JobID, &value.Index, &value.StartOffset, &value.NextOffset, &value.RawContentHash, &value.RawByteSize,
		&value.IdempotencyKey, &status, &value.AttemptCount, &retryNewKey, &value.ImportID, &value.MessageID, &value.LocationID,
		&value.ErrorCode, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.MailboxImportItem{}, maildomain.ErrMailboxImportJobNotFound
		}
		return maildomain.MailboxImportItem{}, fmt.Errorf("read mailbox import item: %w", err)
	}
	var err error
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return maildomain.MailboxImportItem{}, fmt.Errorf("parse mailbox import item creation time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.MailboxImportItem{}, fmt.Errorf("parse mailbox import item update time: %w", err)
	}
	value.Status = maildomain.MailboxImportItemStatus(status)
	value.RetryNewKey = retryNewKey == 1
	return value, nil
}
