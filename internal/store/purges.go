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

type MailPurgeRepository struct {
	db *sql.DB
}

func (repository *MailPurgeRepository) Create(ctx context.Context, purge maildomain.MailPurge) (maildomain.MailPurge, error) {
	purge.ID = strings.TrimSpace(purge.ID)
	purge.AccountID = strings.TrimSpace(purge.AccountID)
	purge.FolderID = strings.TrimSpace(purge.FolderID)
	purge.RequestHash = strings.TrimSpace(purge.RequestHash)
	if purge.ID == "" || purge.AccountID == "" || purge.FolderID == "" || len(purge.RequestHash) != 64 {
		return maildomain.MailPurge{}, errors.New("create mail purge: invalid identity")
	}
	if len(purge.Items) < 1 || len(purge.Items) > 100 || purge.ItemCount != len(purge.Items) {
		return maildomain.MailPurge{}, errors.New("create mail purge: invalid bounded membership")
	}
	if purge.CreatedAt.IsZero() || purge.ExpiresAt.IsZero() || !purge.ExpiresAt.After(purge.CreatedAt) {
		return maildomain.MailPurge{}, errors.New("create mail purge: invalid expiry")
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.MailPurge{}, fmt.Errorf("create mail purge transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO mail_purges (
    purge_id, account_id, folder_id, scope_kind, request_hash,
    item_count, more, blocked_unresolved, created_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		purge.ID, purge.AccountID, purge.FolderID, purge.ScopeKind, purge.RequestHash,
		purge.ItemCount, boolInt(purge.More), purge.BlockedUnresolved, purge.CreatedAt.UTC().Format(time.RFC3339Nano), purge.ExpiresAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		return maildomain.MailPurge{}, fmt.Errorf("create mail purge: %w", err)
	}
	for index, item := range purge.Items {
		if strings.TrimSpace(item.MessageID) == "" || strings.TrimSpace(item.LocationID) == "" || item.UIDValidity == 0 || item.UID == 0 {
			return maildomain.MailPurge{}, fmt.Errorf("create mail purge: invalid item %d", index)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO mail_purge_items (
    purge_id, ordinal, message_id, location_id, uid_validity, uid, mod_sequence
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			purge.ID, index, item.MessageID, item.LocationID, item.UIDValidity, item.UID, item.ModSequence,
		); err != nil {
			return maildomain.MailPurge{}, fmt.Errorf("create mail purge item %d: %w", index, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return maildomain.MailPurge{}, fmt.Errorf("commit mail purge: %w", err)
	}
	return repository.ByID(ctx, purge.ID)
}

func (repository *MailPurgeRepository) ByID(ctx context.Context, purgeID string) (maildomain.MailPurge, error) {
	purgeID = strings.TrimSpace(purgeID)
	var result maildomain.MailPurge
	var scopeKind, createdAt, expiresAt string
	var more int
	err := repository.db.QueryRowContext(ctx, `
SELECT purge_id, account_id, folder_id, scope_kind, request_hash,
       item_count, more, blocked_unresolved, created_at, expires_at
FROM mail_purges WHERE purge_id = ?`, purgeID).Scan(
		&result.ID, &result.AccountID, &result.FolderID, &scopeKind, &result.RequestHash,
		&result.ItemCount, &more, &result.BlockedUnresolved, &createdAt, &expiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return maildomain.MailPurge{}, maildomain.ErrPurgeNotFound
	}
	if err != nil {
		return maildomain.MailPurge{}, fmt.Errorf("read mail purge: %w", err)
	}
	result.ScopeKind = maildomain.PurgeScopeKind(scopeKind)
	result.More = more != 0
	if result.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return maildomain.MailPurge{}, fmt.Errorf("parse mail purge creation time: %w", err)
	}
	if result.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt); err != nil {
		return maildomain.MailPurge{}, fmt.Errorf("parse mail purge expiry: %w", err)
	}
	rows, err := repository.db.QueryContext(ctx, `
SELECT ordinal, message_id, location_id, uid_validity, uid, mod_sequence
FROM mail_purge_items WHERE purge_id = ? ORDER BY ordinal`, purgeID)
	if err != nil {
		return maildomain.MailPurge{}, fmt.Errorf("read mail purge items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item maildomain.MailPurgeItem
		if err := rows.Scan(&item.Ordinal, &item.MessageID, &item.LocationID, &item.UIDValidity, &item.UID, &item.ModSequence); err != nil {
			return maildomain.MailPurge{}, fmt.Errorf("scan mail purge item: %w", err)
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return maildomain.MailPurge{}, fmt.Errorf("read mail purge items: %w", err)
	}
	if len(result.Items) != result.ItemCount {
		return maildomain.MailPurge{}, errors.New("mail purge membership is inconsistent")
	}
	return result, nil
}

func (repository *MailRepository) PurgeLocationsPage(ctx context.Context, accountID, folderID string, limit int) ([]maildomain.Location, bool, int, error) {
	if limit < 1 || limit > 100 {
		return nil, false, 0, errors.New("list purge locations: limit must be between 1 and 100")
	}
	rows, err := repository.db.QueryContext(ctx, locationColumns+`
WHERE account_id = ? AND folder_id = ? AND present = 1 AND deleted = 0
  AND NOT EXISTS (
      SELECT 1 FROM location_mutations AS mutation
      WHERE mutation.account_id = message_locations.account_id
        AND mutation.source_location_id = message_locations.location_id
        AND mutation.action = 'delete'
        AND mutation.status IN ('pending', 'unresolved')
  )
ORDER BY uid_validity, uid, location_id
LIMIT ?`, strings.TrimSpace(accountID), strings.TrimSpace(folderID), limit+1)
	if err != nil {
		return nil, false, 0, fmt.Errorf("list purge locations: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.Location, 0, limit+1)
	for rows.Next() {
		value, scanErr := scanLocation(rows)
		if scanErr != nil {
			return nil, false, 0, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, 0, fmt.Errorf("list purge locations: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	var blocked int
	if err := repository.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM message_locations
WHERE account_id = ? AND folder_id = ? AND present = 1 AND deleted = 0
  AND EXISTS (
      SELECT 1 FROM location_mutations AS mutation
      WHERE mutation.account_id = message_locations.account_id
        AND mutation.source_location_id = message_locations.location_id
        AND mutation.action = 'delete'
        AND mutation.status IN ('pending', 'unresolved')
  )`, strings.TrimSpace(accountID), strings.TrimSpace(folderID)).Scan(&blocked); err != nil {
		return nil, false, 0, fmt.Errorf("count unresolved purge locations: %w", err)
	}
	return values, more, blocked, nil
}
