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

type MailSelectionRepository struct {
	db *sql.DB
}

func (repository *MailSelectionRepository) Create(ctx context.Context, selection maildomain.MailSelection) (maildomain.MailSelection, error) {
	selection.ID = strings.TrimSpace(selection.ID)
	selection.AccountID = strings.TrimSpace(selection.AccountID)
	selection.SourceQueryHash = strings.TrimSpace(selection.SourceQueryHash)
	selection.ScopeFolderID = strings.TrimSpace(selection.ScopeFolderID)
	selection.RequestHash = strings.TrimSpace(selection.RequestHash)
	if selection.ID == "" || selection.AccountID == "" || len(selection.SourceQueryHash) != 64 || len(selection.RequestHash) != 64 {
		return maildomain.MailSelection{}, errors.New("create mail selection: invalid identity")
	}
	if len(selection.Items) < 1 || len(selection.Items) > 100 || selection.ItemCount != len(selection.Items) {
		return maildomain.MailSelection{}, errors.New("create mail selection: invalid bounded membership")
	}
	if selection.CreatedAt.IsZero() || selection.ExpiresAt.IsZero() || !selection.ExpiresAt.After(selection.CreatedAt) {
		return maildomain.MailSelection{}, errors.New("create mail selection: invalid expiry")
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.MailSelection{}, fmt.Errorf("create mail selection transaction: %w", err)
	}
	defer tx.Rollback()
	var scope any
	if selection.ScopeFolderID != "" {
		scope = selection.ScopeFolderID
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO mail_selections (
    selection_id, account_id, source_kind, source_description, source_query_hash, scope_folder_id,
    request_hash, item_count, created_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		selection.ID, selection.AccountID, selection.SourceKind, selection.SourceDescription, selection.SourceQueryHash, scope,
		selection.RequestHash, selection.ItemCount, selection.CreatedAt.UTC().Format(time.RFC3339Nano), selection.ExpiresAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		return maildomain.MailSelection{}, fmt.Errorf("create mail selection: %w", err)
	}
	for index, item := range selection.Items {
		var locationID, folderID any
		if item.LocationID != "" || item.FolderID != "" {
			if item.LocationID == "" || item.FolderID == "" {
				return maildomain.MailSelection{}, errors.New("create mail selection: incomplete location evidence")
			}
			locationID, folderID = item.LocationID, item.FolderID
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO mail_selection_items (
    selection_id, ordinal, message_id, location_id, folder_id,
    uid_validity, uid, mod_sequence
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			selection.ID, index, strings.TrimSpace(item.MessageID), locationID, folderID,
			item.UIDValidity, item.UID, item.ModSequence,
		); err != nil {
			return maildomain.MailSelection{}, fmt.Errorf("create mail selection item %d: %w", index, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return maildomain.MailSelection{}, fmt.Errorf("commit mail selection: %w", err)
	}
	return repository.ByID(ctx, selection.ID)
}

func (repository *MailSelectionRepository) ByID(ctx context.Context, selectionID string) (maildomain.MailSelection, error) {
	selectionID = strings.TrimSpace(selectionID)
	var result maildomain.MailSelection
	var sourceKind, createdAt, expiresAt string
	var scopeFolder sql.NullString
	err := repository.db.QueryRowContext(ctx, `
SELECT selection_id, account_id, source_kind, source_description, source_query_hash, scope_folder_id,
       request_hash, item_count, created_at, expires_at
FROM mail_selections WHERE selection_id = ?`, selectionID).Scan(
		&result.ID, &result.AccountID, &sourceKind, &result.SourceDescription, &result.SourceQueryHash, &scopeFolder,
		&result.RequestHash, &result.ItemCount, &createdAt, &expiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return maildomain.MailSelection{}, maildomain.ErrSelectionNotFound
	}
	if err != nil {
		return maildomain.MailSelection{}, fmt.Errorf("read mail selection: %w", err)
	}
	result.SourceKind = maildomain.SelectionSourceKind(sourceKind)
	result.ScopeFolderID = scopeFolder.String
	if result.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return maildomain.MailSelection{}, fmt.Errorf("parse mail selection creation time: %w", err)
	}
	if result.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt); err != nil {
		return maildomain.MailSelection{}, fmt.Errorf("parse mail selection expiry: %w", err)
	}
	rows, err := repository.db.QueryContext(ctx, `
SELECT ordinal, message_id, location_id, folder_id, uid_validity, uid, mod_sequence
FROM mail_selection_items WHERE selection_id = ? ORDER BY ordinal`, selectionID)
	if err != nil {
		return maildomain.MailSelection{}, fmt.Errorf("read mail selection items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item maildomain.MailSelectionItem
		var locationID, folderID sql.NullString
		if err := rows.Scan(&item.Ordinal, &item.MessageID, &locationID, &folderID, &item.UIDValidity, &item.UID, &item.ModSequence); err != nil {
			return maildomain.MailSelection{}, fmt.Errorf("scan mail selection item: %w", err)
		}
		item.LocationID, item.FolderID = locationID.String, folderID.String
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return maildomain.MailSelection{}, fmt.Errorf("read mail selection items: %w", err)
	}
	if len(result.Items) != result.ItemCount {
		return maildomain.MailSelection{}, errors.New("mail selection membership is inconsistent")
	}
	return result, nil
}

func (repository *MailSelectionRepository) Delete(ctx context.Context, accountID, selectionID string) (bool, error) {
	result, err := repository.db.ExecContext(ctx, `DELETE FROM mail_selections WHERE account_id = ? AND selection_id = ?`, strings.TrimSpace(accountID), strings.TrimSpace(selectionID))
	if err != nil {
		return false, fmt.Errorf("delete mail selection: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count deleted mail selection: %w", err)
	}
	return count > 0, nil
}
