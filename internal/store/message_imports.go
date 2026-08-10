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

type MessageImportRepository struct {
	db *sql.DB
}

const messageImportColumns = `
SELECT import_id, account_id, folder_id, raw_content_hash, raw_storage_key, raw_byte_size,
       rfc_message_id, internal_date, flags_json, baseline_uid_validity, baseline_uid_next,
       candidate_uid_upper, provider_uid_validity, provider_uid,
       COALESCE(message_id, ''), COALESCE(location_id, ''), request_hash, idempotency_key,
       status, provider_method, reconciliation_required, error_code, created_at, updated_at
FROM message_imports`

func (repository *MessageImportRepository) Begin(ctx context.Context, input maildomain.BeginMessageImport) (maildomain.MessageImport, bool, error) {
	input.ImportID = strings.TrimSpace(input.ImportID)
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.FolderID = strings.TrimSpace(input.FolderID)
	input.RawContentHash = strings.ToLower(strings.TrimSpace(input.RawContentHash))
	input.RawStorageKey = strings.TrimSpace(input.RawStorageKey)
	input.RFCMessageID = strings.TrimSpace(input.RFCMessageID)
	input.RequestHash = strings.ToLower(strings.TrimSpace(input.RequestHash))
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	flags, err := maildomain.NormalizeImportFlags(input.Flags)
	if err != nil || !strings.HasPrefix(input.ImportID, "imp_") || !strings.HasPrefix(input.AccountID, "acct_") ||
		!strings.HasPrefix(input.FolderID, "fld_") || len(input.RawContentHash) != 64 || input.RawStorageKey == "" ||
		input.RawByteSize < 0 || input.InternalDate.IsZero() || input.BaselineUIDValidity == 0 || input.BaselineUIDNext == 0 ||
		len(input.RequestHash) != 64 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 256 {
		return maildomain.MessageImport{}, false, maildomain.ErrInvalidMessageImport
	}
	flagsJSON, err := json.Marshal(flags)
	if err != nil {
		return maildomain.MessageImport{}, false, fmt.Errorf("encode import flags: %w", err)
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.MessageImport{}, false, fmt.Errorf("begin message import: %w", err)
	}
	defer tx.Rollback()
	existing, scanErr := scanMessageImport(tx.QueryRowContext(ctx, messageImportColumns+` WHERE account_id = ? AND idempotency_key = ?`, input.AccountID, input.IdempotencyKey))
	switch {
	case scanErr == nil:
		if existing.RequestHash != input.RequestHash {
			return maildomain.MessageImport{}, false, maildomain.ErrMessageImportConflict
		}
		return existing, true, nil
	case !errors.Is(scanErr, maildomain.ErrMessageImportNotFound):
		return maildomain.MessageImport{}, false, scanErr
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO raw_blobs (content_hash, storage_key, byte_size, created_at)
VALUES (?, ?, ?, ?) ON CONFLICT(content_hash) DO NOTHING`, input.RawContentHash, input.RawStorageKey, input.RawByteSize, input.Now.UTC().Format(time.RFC3339Nano)); err != nil {
		return maildomain.MessageImport{}, false, fmt.Errorf("persist import raw blob metadata: %w", err)
	}
	var storedKey string
	var storedSize int64
	if err := tx.QueryRowContext(ctx, `SELECT storage_key, byte_size FROM raw_blobs WHERE content_hash = ?`, input.RawContentHash).Scan(&storedKey, &storedSize); err != nil {
		return maildomain.MessageImport{}, false, fmt.Errorf("verify import raw blob metadata: %w", err)
	}
	if storedKey != input.RawStorageKey || storedSize != input.RawByteSize {
		return maildomain.MessageImport{}, false, maildomain.ErrMessageImportConflict
	}
	timestamp := input.Now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO message_imports (
    import_id, account_id, folder_id, raw_content_hash, raw_storage_key, raw_byte_size,
    rfc_message_id, internal_date, flags_json, baseline_uid_validity, baseline_uid_next,
    request_hash, idempotency_key, status, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
		input.ImportID, input.AccountID, input.FolderID, input.RawContentHash, input.RawStorageKey, input.RawByteSize,
		input.RFCMessageID, input.InternalDate.UTC().Format(time.RFC3339Nano), string(flagsJSON), input.BaselineUIDValidity, input.BaselineUIDNext,
		input.RequestHash, input.IdempotencyKey, timestamp, timestamp,
	); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return maildomain.MessageImport{}, false, maildomain.ErrMessageImportConflict
		}
		return maildomain.MessageImport{}, false, fmt.Errorf("insert message import: %w", err)
	}
	value, err := scanMessageImport(tx.QueryRowContext(ctx, messageImportColumns+` WHERE import_id = ?`, input.ImportID))
	if err != nil {
		return maildomain.MessageImport{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.MessageImport{}, false, fmt.Errorf("commit message import intent: %w", err)
	}
	return value, false, nil
}

func (repository *MessageImportRepository) ByIdempotency(ctx context.Context, accountID, idempotencyKey string) (maildomain.MessageImport, error) {
	if strings.TrimSpace(accountID) == "" || strings.TrimSpace(idempotencyKey) == "" {
		return maildomain.MessageImport{}, maildomain.ErrMessageImportNotFound
	}
	return scanMessageImport(repository.db.QueryRowContext(ctx, messageImportColumns+` WHERE account_id = ? AND idempotency_key = ?`, strings.TrimSpace(accountID), strings.TrimSpace(idempotencyKey)))
}

func (repository *MessageImportRepository) Import(ctx context.Context, accountID, importID string) (maildomain.MessageImport, error) {
	return scanMessageImport(repository.db.QueryRowContext(ctx, messageImportColumns+` WHERE account_id = ? AND import_id = ?`, strings.TrimSpace(accountID), strings.TrimSpace(importID)))
}

func (repository *MessageImportRepository) MarkAppendUnresolved(ctx context.Context, importID, errorCode string, now time.Time) (maildomain.MessageImport, error) {
	return repository.markStatus(ctx, importID, []maildomain.MessageImportStatus{maildomain.MessageImportPending}, maildomain.MessageImportAppendUnresolved, true, errorCode, now)
}

func (repository *MessageImportRepository) MarkFailed(ctx context.Context, importID, errorCode string, now time.Time) (maildomain.MessageImport, error) {
	return repository.markStatus(ctx, importID, []maildomain.MessageImportStatus{maildomain.MessageImportPending, maildomain.MessageImportAppendUnresolved}, maildomain.MessageImportFailed, false, errorCode, now)
}

func (repository *MessageImportRepository) markStatus(ctx context.Context, importID string, from []maildomain.MessageImportStatus, to maildomain.MessageImportStatus, reconciliation bool, errorCode string, now time.Time) (maildomain.MessageImport, error) {
	placeholders := make([]string, len(from))
	args := []any{to, boolInt(reconciliation), strings.TrimSpace(errorCode), now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(importID)}
	for index, status := range from {
		placeholders[index] = "?"
		args = append(args, status)
	}
	if _, err := repository.db.ExecContext(ctx, `UPDATE message_imports SET status = ?, reconciliation_required = ?, error_code = ?, updated_at = ? WHERE import_id = ? AND status IN (`+strings.Join(placeholders, ",")+`)`, args...); err != nil {
		return maildomain.MessageImport{}, fmt.Errorf("mark message import %s: %w", to, err)
	}
	return scanMessageImport(repository.db.QueryRowContext(ctx, messageImportColumns+` WHERE import_id = ?`, strings.TrimSpace(importID)))
}

func (repository *MessageImportRepository) SetCandidateUIDUpper(ctx context.Context, importID string, upper uint32, now time.Time) (maildomain.MessageImport, error) {
	if upper == 0 {
		return maildomain.MessageImport{}, maildomain.ErrInvalidMessageImport
	}
	if _, err := repository.db.ExecContext(ctx, `
UPDATE message_imports
SET candidate_uid_upper = CASE WHEN candidate_uid_upper = 0 THEN ? ELSE candidate_uid_upper END, updated_at = ?
WHERE import_id = ? AND status = 'append_unresolved'`, upper, now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(importID)); err != nil {
		return maildomain.MessageImport{}, fmt.Errorf("bound import reconciliation candidates: %w", err)
	}
	return scanMessageImport(repository.db.QueryRowContext(ctx, messageImportColumns+` WHERE import_id = ?`, strings.TrimSpace(importID)))
}

func (repository *MessageImportRepository) ConfirmAppend(ctx context.Context, importID string, uidValidity, uid uint32, providerMethod string, now time.Time) (maildomain.MessageImport, error) {
	if uidValidity == 0 || uid == 0 {
		return maildomain.MessageImport{}, maildomain.ErrInvalidMessageImport
	}
	value, err := scanMessageImport(repository.db.QueryRowContext(ctx, messageImportColumns+` WHERE import_id = ?`, strings.TrimSpace(importID)))
	if err != nil {
		return maildomain.MessageImport{}, err
	}
	if value.Status == maildomain.MessageImportConfirmed || value.Status == maildomain.MessageImportAppended {
		if value.ProviderUIDValidity != uidValidity || value.ProviderUID != uid {
			return maildomain.MessageImport{}, maildomain.ErrMessageImportConflict
		}
		return value, nil
	}
	if value.Status != maildomain.MessageImportAppendUnresolved && value.Status != maildomain.MessageImportPending {
		return maildomain.MessageImport{}, maildomain.ErrMessageImportConflict
	}
	if value.BaselineUIDValidity != uidValidity || uid < value.BaselineUIDNext || (value.CandidateUIDUpper != 0 && uid > value.CandidateUIDUpper) {
		return maildomain.MessageImport{}, maildomain.ErrMessageImportConflict
	}
	if _, err := repository.db.ExecContext(ctx, `
UPDATE message_imports
SET status = 'appended', provider_uid_validity = ?, provider_uid = ?, provider_method = ?,
    reconciliation_required = 0, error_code = '', updated_at = ?
WHERE import_id = ?`, uidValidity, uid, strings.TrimSpace(providerMethod), now.UTC().Format(time.RFC3339Nano), value.ID); err != nil {
		return maildomain.MessageImport{}, fmt.Errorf("confirm import append: %w", err)
	}
	return scanMessageImport(repository.db.QueryRowContext(ctx, messageImportColumns+` WHERE import_id = ?`, value.ID))
}

func (repository *MessageImportRepository) ConfirmIngest(ctx context.Context, importID, messageID, locationID, providerRawHash string, providerRawSize int64, now time.Time) (maildomain.MessageImport, error) {
	providerRawHash = strings.ToLower(strings.TrimSpace(providerRawHash))
	if !strings.HasPrefix(strings.TrimSpace(messageID), "msg_") || !strings.HasPrefix(strings.TrimSpace(locationID), "loc_") || len(providerRawHash) != 64 || providerRawSize < 0 {
		return maildomain.MessageImport{}, maildomain.ErrInvalidMessageImport
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.MessageImport{}, fmt.Errorf("begin import finalization: %w", err)
	}
	defer tx.Rollback()
	value, err := scanMessageImport(tx.QueryRowContext(ctx, messageImportColumns+` WHERE import_id = ?`, strings.TrimSpace(importID)))
	if err != nil {
		return maildomain.MessageImport{}, err
	}
	if value.Status == maildomain.MessageImportConfirmed {
		if value.MessageID != messageID || value.LocationID != locationID {
			return maildomain.MessageImport{}, maildomain.ErrMessageImportConflict
		}
		return value, nil
	}
	if value.Status != maildomain.MessageImportAppended {
		return maildomain.MessageImport{}, maildomain.ErrMessageImportConflict
	}
	var rawHash string
	var rawSize int64
	if err := tx.QueryRowContext(ctx, `SELECT raw_content_hash, raw_byte_size FROM message_locations WHERE location_id = ? AND message_id = ? AND account_id = ? AND folder_id = ? AND uid_validity = ? AND uid = ? AND present = 1`,
		locationID, messageID, value.AccountID, value.FolderID, value.ProviderUIDValidity, value.ProviderUID).Scan(&rawHash, &rawSize); err != nil {
		return maildomain.MessageImport{}, maildomain.ErrMessageImportConflict
	}
	if !strings.EqualFold(rawHash, providerRawHash) || rawSize != providerRawSize {
		return maildomain.MessageImport{}, maildomain.ErrMessageImportConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE message_imports SET status = 'confirmed', message_id = ?, location_id = ?, reconciliation_required = 0, error_code = '', updated_at = ? WHERE import_id = ?`,
		messageID, locationID, now.UTC().Format(time.RFC3339Nano), value.ID); err != nil {
		return maildomain.MessageImport{}, fmt.Errorf("finalize message import: %w", err)
	}
	result, err := scanMessageImport(tx.QueryRowContext(ctx, messageImportColumns+` WHERE import_id = ?`, value.ID))
	if err != nil {
		return maildomain.MessageImport{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.MessageImport{}, fmt.Errorf("commit message import finalization: %w", err)
	}
	return result, nil
}

func scanMessageImport(row rowScanner) (maildomain.MessageImport, error) {
	var value maildomain.MessageImport
	var flagsJSON, status, internalDate, createdAt, updatedAt string
	var reconciliation int
	if err := row.Scan(
		&value.ID, &value.AccountID, &value.FolderID, &value.RawContentHash, &value.RawStorageKey, &value.RawByteSize,
		&value.RFCMessageID, &internalDate, &flagsJSON, &value.BaselineUIDValidity, &value.BaselineUIDNext,
		&value.CandidateUIDUpper, &value.ProviderUIDValidity, &value.ProviderUID, &value.MessageID, &value.LocationID,
		&value.RequestHash, &value.IdempotencyKey, &status, &value.ProviderMethod, &reconciliation, &value.ErrorCode, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.MessageImport{}, maildomain.ErrMessageImportNotFound
		}
		return maildomain.MessageImport{}, fmt.Errorf("read message import: %w", err)
	}
	if err := json.Unmarshal([]byte(flagsJSON), &value.Flags); err != nil {
		return maildomain.MessageImport{}, fmt.Errorf("parse import flags: %w", err)
	}
	var err error
	value.InternalDate, err = time.Parse(time.RFC3339Nano, internalDate)
	if err != nil {
		return maildomain.MessageImport{}, fmt.Errorf("parse import internal date: %w", err)
	}
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return maildomain.MessageImport{}, fmt.Errorf("parse import creation time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.MessageImport{}, fmt.Errorf("parse import update time: %w", err)
	}
	value.Status = maildomain.MessageImportStatus(status)
	value.ReconciliationRequired = reconciliation == 1
	return value, nil
}
