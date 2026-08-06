package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/folder"
)

const folderRenameRecoveryColumns = `
SELECT recovery_id, account_id, folder_id, old_name, new_name, status, created_at, updated_at
FROM folder_rename_recoveries`

func (repository *FolderRepository) BeginRenameRecovery(
	ctx context.Context,
	accountID, folderID, oldName, newName string,
	now time.Time,
	newID func() (string, error),
) (folder.RenameRecovery, error) {
	if newID == nil {
		return folder.RenameRecovery{}, errors.New("begin folder rename recovery: ID generator is required")
	}
	recoveryID, err := newID()
	if err != nil {
		return folder.RenameRecovery{}, fmt.Errorf("generate folder rename recovery ID: %w", err)
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := repository.db.ExecContext(ctx, `
INSERT INTO folder_rename_recoveries (
    recovery_id, account_id, folder_id, old_name, new_name, status, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 'pending', ?, ?)`,
		recoveryID, strings.TrimSpace(accountID), strings.TrimSpace(folderID), strings.TrimSpace(oldName), strings.TrimSpace(newName), timestamp, timestamp,
	); err != nil {
		return folder.RenameRecovery{}, fmt.Errorf("begin folder rename recovery: %w", err)
	}
	return repository.RenameRecovery(ctx, recoveryID)
}

func (repository *FolderRepository) RenameRecovery(ctx context.Context, recoveryID string) (folder.RenameRecovery, error) {
	return scanFolderRenameRecovery(repository.db.QueryRowContext(ctx, folderRenameRecoveryColumns+` WHERE recovery_id = ?`, strings.TrimSpace(recoveryID)))
}

func (repository *FolderRepository) LatestRenameRecoveryForFolder(ctx context.Context, accountID, folderID string) (folder.RenameRecovery, error) {
	return scanFolderRenameRecovery(repository.db.QueryRowContext(ctx, folderRenameRecoveryColumns+`
 WHERE account_id = ? AND folder_id = ?
 ORDER BY created_at DESC, recovery_id DESC
 LIMIT 1`, strings.TrimSpace(accountID), strings.TrimSpace(folderID)))
}

func (repository *FolderRepository) UpdateRenameRecoveryTarget(ctx context.Context, recoveryID, newName string, now time.Time) error {
	result, err := repository.db.ExecContext(ctx, `
UPDATE folder_rename_recoveries
SET new_name = ?, updated_at = ?
WHERE recovery_id = ? AND status = 'pending'`,
		strings.TrimSpace(newName), now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(recoveryID),
	)
	if err != nil {
		return fmt.Errorf("update folder rename recovery target: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return folder.ErrNotFound
	}
	return nil
}

func (repository *FolderRepository) MarkRenameRecoveryNotApplied(ctx context.Context, recoveryID string, now time.Time) error {
	result, err := repository.db.ExecContext(ctx, `
UPDATE folder_rename_recoveries
SET status = 'not_applied', updated_at = ?
WHERE recovery_id = ? AND status = 'pending'`,
		now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(recoveryID),
	)
	if err != nil {
		return fmt.Errorf("mark folder rename not applied: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return folder.ErrNotFound
	}
	return nil
}

func (repository *FolderRepository) ResolveRenameRecovery(
	ctx context.Context,
	recoveryID string,
	observation folder.RemoteObservation,
	now time.Time,
) (folder.Folder, error) {
	if err := validateObservations([]folder.RemoteObservation{observation}); err != nil {
		return folder.Folder{}, err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return folder.Folder{}, fmt.Errorf("begin folder rename recovery resolution: %w", err)
	}
	defer tx.Rollback()
	recovery, err := scanFolderRenameRecovery(tx.QueryRowContext(ctx, folderRenameRecoveryColumns+` WHERE recovery_id = ?`, strings.TrimSpace(recoveryID)))
	if err != nil {
		return folder.Folder{}, err
	}
	if recovery.Status != folder.RenameRecoveryPending {
		return folder.Folder{}, fmt.Errorf("resolve folder rename recovery: recovery is %s", recovery.Status)
	}
	value, err := applyFolderObservationInTx(ctx, tx, recovery.AccountID, recovery.FolderID, observation, now)
	if err != nil {
		return folder.Folder{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE folder_rename_recoveries
SET new_name = ?, status = 'resolved', updated_at = ?
WHERE recovery_id = ? AND status = 'pending'`,
		observation.Name, now.UTC().Format(time.RFC3339Nano), recovery.ID,
	); err != nil {
		return folder.Folder{}, fmt.Errorf("complete folder rename recovery: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return folder.Folder{}, fmt.Errorf("commit folder rename recovery: %w", err)
	}
	return value, nil
}

// ReconcilePendingRenames uses a fresh full LIST observation to settle
// ambiguous RENAME outcomes before ordinary folder reconciliation runs. This
// preserves the original stable folder ID even when the provider accepted a
// RENAME but omitted LIST-EXTENDED OLDNAME on the next discovery.
func (repository *FolderRepository) ReconcilePendingRenames(
	ctx context.Context,
	accountID string,
	observations []folder.RemoteObservation,
	now time.Time,
) (resolved, notApplied int, err error) {
	rows, err := repository.db.QueryContext(ctx, folderRenameRecoveryColumns+`
 WHERE account_id = ? AND status = 'pending'
 ORDER BY created_at, recovery_id
 LIMIT 101`, strings.TrimSpace(accountID))
	if err != nil {
		return 0, 0, fmt.Errorf("list pending folder rename recoveries: %w", err)
	}
	recoveries := make([]folder.RenameRecovery, 0)
	for rows.Next() {
		value, scanErr := scanFolderRenameRecovery(rows)
		if scanErr != nil {
			rows.Close()
			return 0, 0, scanErr
		}
		recoveries = append(recoveries, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, fmt.Errorf("read pending folder rename recoveries: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, 0, fmt.Errorf("close pending folder rename recoveries: %w", err)
	}
	if len(recoveries) > 100 {
		return 0, 0, fmt.Errorf("pending folder rename recovery count exceeds safety bound")
	}
	for _, recovery := range recoveries {
		var target *folder.RemoteObservation
		oldPresent := false
		for index := range observations {
			observation := &observations[index]
			if observation.Name == recovery.NewName {
				target = observation
			}
			if observation.Name == recovery.OldName {
				oldPresent = true
			}
		}
		switch {
		case target != nil && !oldPresent:
			if _, err := repository.ResolveRenameRecovery(ctx, recovery.ID, *target, now); err != nil {
				return resolved, notApplied, err
			}
			resolved++
		case target == nil && oldPresent:
			if err := repository.MarkRenameRecoveryNotApplied(ctx, recovery.ID, now); err != nil {
				return resolved, notApplied, err
			}
			notApplied++
		}
	}
	return resolved, notApplied, nil
}

func scanFolderRenameRecovery(scanner interface{ Scan(...any) error }) (folder.RenameRecovery, error) {
	var value folder.RenameRecovery
	var status, createdAt, updatedAt string
	if err := scanner.Scan(&value.ID, &value.AccountID, &value.FolderID, &value.OldName, &value.NewName, &status, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return folder.RenameRecovery{}, folder.ErrNotFound
		}
		return folder.RenameRecovery{}, fmt.Errorf("scan folder rename recovery: %w", err)
	}
	value.Status = folder.RenameRecoveryStatus(status)
	var err error
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return folder.RenameRecovery{}, fmt.Errorf("parse folder rename recovery created_at: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return folder.RenameRecovery{}, fmt.Errorf("parse folder rename recovery updated_at: %w", err)
	}
	return value, nil
}
