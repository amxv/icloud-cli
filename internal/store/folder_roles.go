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

type FolderRoleRepository struct {
	db *sql.DB
}

const folderRoleBindingColumns = `
SELECT account_id, role, folder_id, created_at, updated_at
FROM folder_role_bindings`

func (repository *FolderRoleRepository) List(ctx context.Context, accountID string) ([]folder.FolderRoleBinding, error) {
	rows, err := repository.db.QueryContext(ctx, folderRoleBindingColumns+`
WHERE account_id = ?
ORDER BY CASE role
    WHEN 'sent' THEN 0 WHEN 'drafts' THEN 1 WHEN 'archive' THEN 2
    WHEN 'trash' THEN 3 WHEN 'junk' THEN 4 ELSE 5 END`, strings.TrimSpace(accountID))
	if err != nil {
		return nil, fmt.Errorf("list folder role bindings: %w", err)
	}
	defer rows.Close()
	values := make([]folder.FolderRoleBinding, 0, 5)
	for rows.Next() {
		value, err := scanFolderRoleBinding(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate folder role bindings: %w", err)
	}
	return values, nil
}

func (repository *FolderRoleRepository) Binding(ctx context.Context, accountID string, role folder.Role) (folder.FolderRoleBinding, error) {
	if !role.Bindable() {
		return folder.FolderRoleBinding{}, folder.ErrInvalidRole
	}
	return scanFolderRoleBinding(repository.db.QueryRowContext(ctx, folderRoleBindingColumns+`
WHERE account_id = ? AND role = ?`, strings.TrimSpace(accountID), role))
}

func (repository *FolderRoleRepository) RoleForFolder(ctx context.Context, accountID, folderID string) (folder.Role, bool, error) {
	var role string
	err := repository.db.QueryRowContext(ctx, `
SELECT role FROM folder_role_bindings
WHERE account_id = ? AND folder_id = ?`, strings.TrimSpace(accountID), strings.TrimSpace(folderID)).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("resolve bound role for folder: %w", err)
	}
	return folder.Role(role), true, nil
}

func (repository *FolderRoleRepository) Set(ctx context.Context, accountID string, role folder.Role, folderID string, now time.Time) (folder.FolderRoleBinding, error) {
	if !role.Bindable() {
		return folder.FolderRoleBinding{}, folder.ErrInvalidRole
	}
	accountID = strings.TrimSpace(accountID)
	folderID = strings.TrimSpace(folderID)
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return folder.FolderRoleBinding{}, fmt.Errorf("begin folder role binding: %w", err)
	}
	defer tx.Rollback()
	if _, err := accountByID(ctx, tx, accountID); err != nil {
		return folder.FolderRoleBinding{}, err
	}
	folderValue, err := folderByID(ctx, tx, folderID)
	if err != nil || folderValue.AccountID != accountID {
		if err == nil || errors.Is(err, folder.ErrNotFound) {
			return folder.FolderRoleBinding{}, folder.ErrNotFound
		}
		return folder.FolderRoleBinding{}, err
	}
	if !folderValue.Present || folderValue.Stale || !folderValue.Selectable {
		return folder.FolderRoleBinding{}, &folder.InvalidRoleBindingError{
			Binding: folder.FolderRoleBinding{AccountID: accountID, Role: role, FolderID: folderID},
			Reason:  "selected folder is not current and selectable",
		}
	}
	var otherRole string
	err = tx.QueryRowContext(ctx, `
SELECT role FROM folder_role_bindings
WHERE account_id = ? AND folder_id = ? AND role <> ?`, accountID, folderID, role).Scan(&otherRole)
	if err == nil {
		return folder.FolderRoleBinding{}, fmt.Errorf("%w: folder %s is bound as %s", folder.ErrRoleAlreadyBound, folderID, otherRole)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return folder.FolderRoleBinding{}, fmt.Errorf("check folder role uniqueness: %w", err)
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO folder_role_bindings (account_id, role, folder_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(account_id, role) DO UPDATE SET
    folder_id = excluded.folder_id,
    updated_at = excluded.updated_at`, accountID, role, folderID, timestamp, timestamp); err != nil {
		return folder.FolderRoleBinding{}, fmt.Errorf("set folder role binding: %w", err)
	}
	value, err := scanFolderRoleBinding(tx.QueryRowContext(ctx, folderRoleBindingColumns+`
WHERE account_id = ? AND role = ?`, accountID, role))
	if err != nil {
		return folder.FolderRoleBinding{}, err
	}
	if err := tx.Commit(); err != nil {
		return folder.FolderRoleBinding{}, fmt.Errorf("commit folder role binding: %w", err)
	}
	return value, nil
}

func (repository *FolderRoleRepository) Clear(ctx context.Context, accountID string, role folder.Role) (folder.FolderRoleBinding, error) {
	if !role.Bindable() {
		return folder.FolderRoleBinding{}, folder.ErrInvalidRole
	}
	accountID = strings.TrimSpace(accountID)
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return folder.FolderRoleBinding{}, fmt.Errorf("begin clear folder role binding: %w", err)
	}
	defer tx.Rollback()
	value, err := scanFolderRoleBinding(tx.QueryRowContext(ctx, folderRoleBindingColumns+`
WHERE account_id = ? AND role = ?`, accountID, role))
	if err != nil {
		return folder.FolderRoleBinding{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM folder_role_bindings WHERE account_id = ? AND role = ?`, accountID, role); err != nil {
		return folder.FolderRoleBinding{}, fmt.Errorf("clear folder role binding: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return folder.FolderRoleBinding{}, fmt.Errorf("commit clear folder role binding: %w", err)
	}
	return value, nil
}

func (repository *FolderRoleRepository) Resolve(ctx context.Context, accountID string, role folder.Role) (folder.FolderRoleResolution, error) {
	accountID = strings.TrimSpace(accountID)
	if role.Kind() == folder.KindOther {
		return folder.FolderRoleResolution{}, folder.ErrInvalidRole
	}
	if role.Bindable() {
		binding, err := repository.Binding(ctx, accountID, role)
		if err == nil {
			folderValue, readErr := folderByID(ctx, repository.db, binding.FolderID)
			if readErr != nil || folderValue.AccountID != accountID {
				return folder.FolderRoleResolution{}, &folder.InvalidRoleBindingError{Binding: binding, Reason: "bound folder is missing"}
			}
			if !folderValue.Present || folderValue.Stale || !folderValue.Selectable {
				return folder.FolderRoleResolution{}, &folder.InvalidRoleBindingError{Binding: binding, Reason: "bound folder is missing, stale, or not selectable"}
			}
			return folder.FolderRoleResolution{Role: role, Folder: folderValue, Source: folder.RoleResolutionBinding, Binding: &binding}, nil
		}
		if !errors.Is(err, folder.ErrRoleBindingMissing) {
			return folder.FolderRoleResolution{}, err
		}
	}

	provider, err := repository.resolveProviderRole(ctx, accountID, role, false)
	if err == nil {
		return folder.FolderRoleResolution{Role: role, Folder: provider, Source: folder.RoleResolutionProvider}, nil
	}
	if !errors.Is(err, folder.ErrNotFound) {
		return folder.FolderRoleResolution{}, err
	}
	if role == folder.RoleSent {
		fallback, fallbackErr := repository.resolveProviderRole(ctx, accountID, role, true)
		if fallbackErr == nil {
			return folder.FolderRoleResolution{Role: role, Folder: fallback, Source: folder.RoleResolutionFallback}, nil
		}
		if !errors.Is(fallbackErr, folder.ErrNotFound) {
			return folder.FolderRoleResolution{}, fallbackErr
		}
	}
	return folder.FolderRoleResolution{}, folder.ErrNotFound
}

func (repository *FolderRoleRepository) resolveProviderRole(ctx context.Context, accountID string, role folder.Role, fallback bool) (folder.Folder, error) {
	whereSource := `kind_source <> '' AND kind_source <> 'fallback'`
	if fallback {
		whereSource = `kind_source = 'fallback'`
	}
	rows, err := repository.db.QueryContext(ctx, folderColumns+`
WHERE account_id = ? AND kind = ? AND `+whereSource+`
  AND present = 1 AND stale = 0 AND selectable = 1
ORDER BY folder_id
LIMIT 2`, strings.TrimSpace(accountID), role.Kind())
	if err != nil {
		return folder.Folder{}, fmt.Errorf("resolve provider folder role: %w", err)
	}
	values, err := scanFolders(rows, 2)
	if err != nil {
		return folder.Folder{}, err
	}
	switch len(values) {
	case 0:
		return folder.Folder{}, folder.ErrNotFound
	case 1:
		return values[0], nil
	default:
		return folder.Folder{}, folder.ErrAmbiguous
	}
}

func scanFolderRoleBinding(scanner interface{ Scan(...any) error }) (folder.FolderRoleBinding, error) {
	var value folder.FolderRoleBinding
	var role, createdAt, updatedAt string
	if err := scanner.Scan(&value.AccountID, &role, &value.FolderID, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return folder.FolderRoleBinding{}, folder.ErrRoleBindingMissing
		}
		return folder.FolderRoleBinding{}, fmt.Errorf("scan folder role binding: %w", err)
	}
	value.Role = folder.Role(role)
	var err error
	if value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return folder.FolderRoleBinding{}, fmt.Errorf("parse folder role created time: %w", err)
	}
	if value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return folder.FolderRoleBinding{}, fmt.Errorf("parse folder role updated time: %w", err)
	}
	return value, nil
}
