package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/folder"
)

type FolderRepository struct {
	db *sql.DB
}

const folderColumns = `
SELECT folder_id, account_id, name, wire_name, parent_name, parent_wire_name,
       delimiter, attributes_json, kind, kind_source, selectable, subscription,
       present, stale, sync_enabled, message_count, unseen_count, uid_next,
       uid_validity, discovered_at, status_at, missing_since, created_at, updated_at
FROM folders`

func (repository *FolderRepository) List(ctx context.Context, accountID string, limit int, includeMissing bool) ([]folder.Folder, bool, error) {
	if limit < 1 {
		return nil, false, fmt.Errorf("list folders: limit must be positive")
	}
	where := ` WHERE account_id = ?`
	if !includeMissing {
		where += ` AND present = 1`
	}
	rows, err := repository.db.QueryContext(ctx, folderColumns+where+`
ORDER BY present DESC, sync_enabled DESC,
         CASE kind WHEN 'inbox' THEN 0 WHEN 'sent' THEN 1 ELSE 2 END,
         lower(name), folder_id
LIMIT ?`, accountID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list folders: %w", err)
	}
	values, err := scanFolders(rows, limit+1)
	if err != nil {
		return nil, false, err
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	return values, more, nil
}

func (repository *FolderRepository) ListSelectable(ctx context.Context, accountID string, limit int) ([]folder.Folder, bool, error) {
	if limit < 1 {
		return nil, false, fmt.Errorf("list selectable folders: limit must be positive")
	}
	rows, err := repository.db.QueryContext(ctx, folderColumns+`
WHERE account_id = ? AND present = 1 AND stale = 0 AND selectable = 1
ORDER BY CASE kind WHEN 'inbox' THEN 0 WHEN 'sent' THEN 1 ELSE 2 END,
         lower(name), folder_id
LIMIT ?`, strings.TrimSpace(accountID), limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list selectable folders: %w", err)
	}
	values, err := scanFolders(rows, limit+1)
	if err != nil {
		return nil, false, err
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	return values, more, nil
}

func (repository *FolderRepository) Resolve(ctx context.Context, accountID, selector string) (folder.Folder, error) {
	return resolveFolder(ctx, repository.db, accountID, selector)
}

// ResolveKind selects a folder by its provider-derived semantic role only.
// Unlike Resolve, it never treats a decoded or wire mailbox name as role
// evidence. Convenience workflows such as archive/trash rely on this so an
// ordinary mailbox literally named "Archive" cannot masquerade as a
// SPECIAL-USE Archive folder.
func (repository *FolderRepository) ResolveKind(ctx context.Context, accountID string, kind folder.Kind) (folder.Folder, error) {
	if kind == "" || kind == folder.KindOther {
		return folder.Folder{}, folder.ErrNotFound
	}
	rows, err := repository.db.QueryContext(ctx, folderColumns+`
WHERE account_id = ? AND kind = ?
ORDER BY present DESC, stale ASC, folder_id
LIMIT 2`, strings.TrimSpace(accountID), kind)
	if err != nil {
		return folder.Folder{}, fmt.Errorf("resolve folder kind: %w", err)
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

func (repository *FolderRepository) SetSync(ctx context.Context, accountID, selector string, enabled bool, now time.Time) (folder.Folder, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return folder.Folder{}, fmt.Errorf("begin folder policy update: %w", err)
	}
	defer tx.Rollback()
	value, err := resolveFolder(ctx, tx, accountID, selector)
	if err != nil {
		return folder.Folder{}, err
	}
	if enabled && !value.Selectable {
		return folder.Folder{}, folder.ErrNotSelectable
	}
	if _, err := tx.ExecContext(ctx, `UPDATE folders SET sync_enabled = ?, updated_at = ? WHERE folder_id = ?`,
		boolInt(enabled), now.UTC().Format(time.RFC3339Nano), value.ID); err != nil {
		return folder.Folder{}, fmt.Errorf("update folder sync policy: %w", err)
	}
	value, err = folderByID(ctx, tx, value.ID)
	if err != nil {
		return folder.Folder{}, err
	}
	if err := tx.Commit(); err != nil {
		return folder.Folder{}, fmt.Errorf("commit folder policy update: %w", err)
	}
	return value, nil
}

func (repository *FolderRepository) CreateObserved(ctx context.Context, accountID string, observation folder.RemoteObservation, now time.Time, newID func() (string, error)) (folder.Folder, error) {
	if newID == nil {
		return folder.Folder{}, errors.New("create observed folder: ID generator is required")
	}
	if err := validateObservations([]folder.RemoteObservation{observation}); err != nil {
		return folder.Folder{}, err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return folder.Folder{}, fmt.Errorf("begin observed folder creation: %w", err)
	}
	defer tx.Rollback()
	if _, err := accountByID(ctx, tx, accountID); err != nil {
		return folder.Folder{}, err
	}
	if existing, err := resolveFolderByWire(ctx, tx, accountID, observation.WireName); err == nil {
		return folder.Folder{}, fmt.Errorf("%w: folder already exists as %s", folder.ErrAmbiguous, existing.ID)
	} else if !errors.Is(err, folder.ErrNotFound) {
		return folder.Folder{}, err
	}
	folderID, err := newID()
	if err != nil {
		return folder.Folder{}, fmt.Errorf("generate folder ID: %w", err)
	}
	attributes, err := json.Marshal(observation.Attributes)
	if err != nil {
		return folder.Folder{}, fmt.Errorf("encode folder attributes: %w", err)
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	var statusAt any
	if observation.StatusKnown {
		statusAt = timestamp
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO folders (
    folder_id, account_id, name, wire_name, parent_name, parent_wire_name,
    delimiter, attributes_json, kind, kind_source, selectable, subscription,
    present, stale, sync_enabled, message_count, unseen_count, uid_next,
    uid_validity, discovered_at, status_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		folderID, accountID, observation.Name, observation.WireName,
		observation.ParentName, observation.ParentWireName, observation.Delimiter,
		string(attributes), observation.Kind, observation.KindSource,
		boolInt(observation.Selectable), observation.Subscription,
		boolInt(folder.DefaultSyncEnabled(observation.Kind) && observation.Selectable),
		observation.Messages, observation.Unseen, observation.UIDNext,
		observation.UIDValidity, timestamp, statusAt, timestamp, timestamp,
	); err != nil {
		return folder.Folder{}, classifyFolderWrite("create observed folder", err)
	}
	value, err := folderByID(ctx, tx, folderID)
	if err != nil {
		return folder.Folder{}, err
	}
	if err := tx.Commit(); err != nil {
		return folder.Folder{}, fmt.Errorf("commit observed folder creation: %w", err)
	}
	return value, nil
}

func (repository *FolderRepository) ApplyObservation(ctx context.Context, accountID, folderID string, observation folder.RemoteObservation, now time.Time) (folder.Folder, error) {
	if err := validateObservations([]folder.RemoteObservation{observation}); err != nil {
		return folder.Folder{}, err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return folder.Folder{}, fmt.Errorf("begin folder observation update: %w", err)
	}
	defer tx.Rollback()
	value, err := applyFolderObservationInTx(ctx, tx, accountID, folderID, observation, now)
	if err != nil {
		return folder.Folder{}, err
	}
	if err := tx.Commit(); err != nil {
		return folder.Folder{}, fmt.Errorf("commit folder observation update: %w", err)
	}
	return value, nil
}

func applyFolderObservationInTx(ctx context.Context, tx *sql.Tx, accountID, folderID string, observation folder.RemoteObservation, now time.Time) (folder.Folder, error) {
	current, err := folderByID(ctx, tx, folderID)
	if err != nil {
		return folder.Folder{}, err
	}
	if current.AccountID != strings.TrimSpace(accountID) {
		return folder.Folder{}, folder.ErrNotFound
	}
	attributes, err := json.Marshal(observation.Attributes)
	if err != nil {
		return folder.Folder{}, fmt.Errorf("encode folder attributes: %w", err)
	}
	subscription := observation.Subscription
	if subscription == folder.SubscriptionUnknown && current.Subscription != folder.SubscriptionUnknown {
		subscription = current.Subscription
	}
	messages, unseen, uidNext, uidValidity := current.Messages, current.Unseen, current.UIDNext, current.UIDValidity
	statusAt := current.StatusAt
	if observation.StatusKnown {
		messages, unseen, uidNext, uidValidity = observation.Messages, observation.Unseen, observation.UIDNext, observation.UIDValidity
		copy := now.UTC()
		statusAt = &copy
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	var statusValue any
	if statusAt != nil {
		statusValue = statusAt.UTC().Format(time.RFC3339Nano)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE folders
SET name = ?, wire_name = ?, parent_name = ?, parent_wire_name = ?, delimiter = ?,
    attributes_json = ?, kind = ?, kind_source = ?, selectable = ?, subscription = ?,
    present = 1, stale = 0, message_count = ?, unseen_count = ?, uid_next = ?,
    uid_validity = ?, discovered_at = ?, status_at = ?, missing_since = NULL,
    updated_at = ?
WHERE folder_id = ? AND account_id = ?`,
		observation.Name, observation.WireName, observation.ParentName,
		observation.ParentWireName, observation.Delimiter, string(attributes),
		observation.Kind, observation.KindSource, boolInt(observation.Selectable),
		subscription, messages, unseen, uidNext, uidValidity, timestamp, statusValue,
		timestamp, folderID, accountID,
	); err != nil {
		return folder.Folder{}, classifyFolderWrite("update observed folder", err)
	}
	return folderByID(ctx, tx, folderID)
}

func (repository *FolderRepository) SetSubscription(ctx context.Context, accountID, folderID string, subscription folder.Subscription, now time.Time) (folder.Folder, error) {
	if subscription != folder.SubscriptionSubscribed && subscription != folder.SubscriptionUnsubscribed {
		return folder.Folder{}, fmt.Errorf("set folder subscription: invalid subscription")
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	result, err := repository.db.ExecContext(ctx, `
UPDATE folders SET subscription = ?, updated_at = ? WHERE folder_id = ? AND account_id = ?`,
		subscription, timestamp, strings.TrimSpace(folderID), strings.TrimSpace(accountID))
	if err != nil {
		return folder.Folder{}, fmt.Errorf("set folder subscription: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return folder.Folder{}, folder.ErrNotFound
	}
	return folderByID(ctx, repository.db, strings.TrimSpace(folderID))
}

func (repository *FolderRepository) MarkDeleted(ctx context.Context, accountID, folderID string, now time.Time) (folder.Folder, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return folder.Folder{}, fmt.Errorf("begin folder deletion reconciliation: %w", err)
	}
	defer tx.Rollback()
	current, err := folderByID(ctx, tx, folderID)
	if err != nil {
		return folder.Folder{}, err
	}
	if current.AccountID != strings.TrimSpace(accountID) {
		return folder.Folder{}, folder.ErrNotFound
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT message_id FROM message_locations WHERE account_id = ? AND folder_id = ? AND present = 1 ORDER BY message_id`, accountID, folderID)
	if err != nil {
		return folder.Folder{}, fmt.Errorf("select deleted-folder messages: %w", err)
	}
	messageIDs := make([]string, 0)
	for rows.Next() {
		var messageID string
		if err := rows.Scan(&messageID); err != nil {
			rows.Close()
			return folder.Folder{}, fmt.Errorf("read deleted-folder message: %w", err)
		}
		messageIDs = append(messageIDs, messageID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return folder.Folder{}, fmt.Errorf("read deleted-folder messages: %w", err)
	}
	if err := rows.Close(); err != nil {
		return folder.Folder{}, fmt.Errorf("close deleted-folder message rows: %w", err)
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE message_locations SET present = 0, updated_at = ? WHERE account_id = ? AND folder_id = ? AND present = 1`, timestamp, accountID, folderID); err != nil {
		return folder.Folder{}, fmt.Errorf("mark deleted-folder locations absent: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE folders
SET present = 0, stale = 1, sync_enabled = 0, message_count = 0, unseen_count = 0,
    missing_since = COALESCE(missing_since, ?), updated_at = ?
WHERE folder_id = ? AND account_id = ?`, timestamp, timestamp, folderID, accountID); err != nil {
		return folder.Folder{}, fmt.Errorf("mark folder deleted: %w", err)
	}
	if len(messageIDs) > 0 {
		if err := rebuildAffectedThreadGraph(ctx, tx, accountID, messageIDs, now); err != nil {
			return folder.Folder{}, err
		}
	}
	value, err := folderByID(ctx, tx, folderID)
	if err != nil {
		return folder.Folder{}, err
	}
	if err := tx.Commit(); err != nil {
		return folder.Folder{}, fmt.Errorf("commit folder deletion reconciliation: %w", err)
	}
	return value, nil
}

func (repository *FolderRepository) Reconcile(
	ctx context.Context,
	accountID string,
	observations []folder.RemoteObservation,
	now time.Time,
	newID func() (string, error),
) ([]folder.Folder, error) {
	if newID == nil {
		return nil, errors.New("reconcile folders: ID generator is required")
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin folder reconciliation: %w", err)
	}
	defer tx.Rollback()
	if _, err := accountByID(ctx, tx, accountID); err != nil {
		return nil, err
	}
	existing, err := listAllFolders(ctx, tx, accountID)
	if err != nil {
		return nil, err
	}
	if err := validateObservations(observations); err != nil {
		return nil, err
	}

	existingKindCount := countFolderKinds(existing)
	remoteKindCount := countObservationKinds(observations)
	matched := make(map[string]bool, len(existing))
	timestamp := now.UTC().Format(time.RFC3339Nano)
	for _, observation := range observations {
		match := matchFolder(existing, matched, observation, existingKindCount, remoteKindCount)
		if match != nil && observation.Subscription == folder.SubscriptionUnknown && match.Subscription != folder.SubscriptionUnknown {
			observation.Subscription = match.Subscription
		}
		attributes, err := json.Marshal(observation.Attributes)
		if err != nil {
			return nil, fmt.Errorf("encode folder attributes: %w", err)
		}
		var statusAt any
		if observation.StatusKnown {
			statusAt = timestamp
		}
		if match == nil {
			folderID, err := newID()
			if err != nil {
				return nil, fmt.Errorf("generate folder ID: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO folders (
    folder_id, account_id, name, wire_name, parent_name, parent_wire_name,
    delimiter, attributes_json, kind, kind_source, selectable, subscription,
    present, stale, sync_enabled, message_count, unseen_count, uid_next,
    uid_validity, discovered_at, status_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				folderID, accountID, observation.Name, observation.WireName,
				observation.ParentName, observation.ParentWireName, observation.Delimiter,
				string(attributes), observation.Kind, observation.KindSource,
				boolInt(observation.Selectable), observation.Subscription,
				boolInt(folder.DefaultSyncEnabled(observation.Kind) && observation.Selectable),
				observation.Messages, observation.Unseen, observation.UIDNext,
				observation.UIDValidity, timestamp, statusAt, timestamp, timestamp,
			); err != nil {
				return nil, classifyFolderWrite("create folder", err)
			}
			matched[folderID] = true
			continue
		}
		matched[match.ID] = true
		if _, err := tx.ExecContext(ctx, `
UPDATE folders
SET name = ?, wire_name = ?, parent_name = ?, parent_wire_name = ?, delimiter = ?,
    attributes_json = ?, kind = ?, kind_source = ?, selectable = ?, subscription = ?,
    present = 1, stale = 0, message_count = ?, unseen_count = ?, uid_next = ?,
    uid_validity = ?, discovered_at = ?, status_at = ?, missing_since = NULL,
    updated_at = ?
WHERE folder_id = ?`,
			observation.Name, observation.WireName, observation.ParentName,
			observation.ParentWireName, observation.Delimiter, string(attributes),
			observation.Kind, observation.KindSource, boolInt(observation.Selectable),
			observation.Subscription, observation.Messages, observation.Unseen,
			observation.UIDNext, observation.UIDValidity, timestamp, statusAt,
			timestamp, match.ID,
		); err != nil {
			return nil, classifyFolderWrite("refresh folder", err)
		}
	}

	for _, value := range existing {
		if matched[value.ID] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE folders
SET present = 0, stale = 1, missing_since = COALESCE(missing_since, ?), updated_at = ?
WHERE folder_id = ?`, timestamp, timestamp, value.ID); err != nil {
			return nil, fmt.Errorf("mark missing folder: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit folder reconciliation: %w", err)
	}
	values, _, err := repository.List(ctx, accountID, 1000, true)
	return values, err
}

func resolveFolderByWire(ctx context.Context, queryer folderQueryer, accountID, wireName string) (folder.Folder, error) {
	return scanFolder(queryer.QueryRowContext(ctx, folderColumns+` WHERE account_id = ? AND wire_name = ?`, strings.TrimSpace(accountID), strings.TrimSpace(wireName)))
}

func validateObservations(observations []folder.RemoteObservation) error {
	wires := make(map[string]struct{}, len(observations))
	for _, value := range observations {
		if strings.TrimSpace(value.Name) == "" || strings.TrimSpace(value.WireName) == "" {
			return errors.New("reconcile folders: remote name is required")
		}
		if _, exists := wires[value.WireName]; exists {
			return fmt.Errorf("reconcile folders: duplicate remote wire name")
		}
		wires[value.WireName] = struct{}{}
	}
	return nil
}

func matchFolder(
	existing []folder.Folder,
	matched map[string]bool,
	observation folder.RemoteObservation,
	existingKindCount map[folder.Kind]int,
	remoteKindCount map[folder.Kind]int,
) *folder.Folder {
	find := func(predicate func(folder.Folder) bool) *folder.Folder {
		for index := range existing {
			if matched[existing[index].ID] || !predicate(existing[index]) {
				continue
			}
			return &existing[index]
		}
		return nil
	}
	if observation.OldWireName != "" {
		if value := find(func(candidate folder.Folder) bool { return candidate.WireName == observation.OldWireName }); value != nil {
			return value
		}
	}
	if value := find(func(candidate folder.Folder) bool { return candidate.WireName == observation.WireName }); value != nil {
		return value
	}
	if observation.Kind != folder.KindOther && existingKindCount[observation.Kind] == 1 && remoteKindCount[observation.Kind] == 1 {
		return find(func(candidate folder.Folder) bool { return candidate.Kind == observation.Kind })
	}
	return nil
}

func countFolderKinds(values []folder.Folder) map[folder.Kind]int {
	counts := make(map[folder.Kind]int)
	for _, value := range values {
		if value.Kind != folder.KindOther {
			counts[value.Kind]++
		}
	}
	return counts
}

func countObservationKinds(values []folder.RemoteObservation) map[folder.Kind]int {
	counts := make(map[folder.Kind]int)
	for _, value := range values {
		if value.Kind != folder.KindOther {
			counts[value.Kind]++
		}
	}
	return counts
}

type folderQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func resolveFolder(ctx context.Context, queryer folderQueryer, accountID, selector string) (folder.Folder, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return folder.Folder{}, folder.ErrNotFound
	}
	rows, err := queryer.QueryContext(ctx, folderColumns+`
WHERE account_id = ? AND (
    folder_id = ? OR lower(name) = lower(?) OR wire_name = ? OR lower(kind) = lower(?)
)
ORDER BY present DESC, folder_id
LIMIT 2`, accountID, selector, selector, selector, selector)
	if err != nil {
		return folder.Folder{}, fmt.Errorf("resolve folder: %w", err)
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

func folderByID(ctx context.Context, queryer folderQueryer, folderID string) (folder.Folder, error) {
	return scanFolder(queryer.QueryRowContext(ctx, folderColumns+` WHERE folder_id = ?`, folderID))
}

func listAllFolders(ctx context.Context, queryer folderQueryer, accountID string) ([]folder.Folder, error) {
	rows, err := queryer.QueryContext(ctx, folderColumns+` WHERE account_id = ? ORDER BY folder_id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("read existing folders: %w", err)
	}
	return scanFolders(rows, 100)
}

func scanFolders(rows *sql.Rows, capacity int) ([]folder.Folder, error) {
	defer rows.Close()
	values := make([]folder.Folder, 0, capacity)
	for rows.Next() {
		value, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read folders: %w", err)
	}
	return values, nil
}

func scanFolder(row rowScanner) (folder.Folder, error) {
	var (
		value                                   folder.Folder
		attributesJSON                          string
		kind, subscription                      string
		selectable, present, stale, syncEnabled int
		discoveredAt, createdAt, updatedAt      string
		statusAt, missingSince                  sql.NullString
	)
	if err := row.Scan(
		&value.ID, &value.AccountID, &value.Name, &value.WireName,
		&value.ParentName, &value.ParentWireName, &value.Delimiter,
		&attributesJSON, &kind, &value.KindSource, &selectable, &subscription,
		&present, &stale, &syncEnabled, &value.Messages, &value.Unseen,
		&value.UIDNext, &value.UIDValidity, &discoveredAt, &statusAt,
		&missingSince, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return folder.Folder{}, folder.ErrNotFound
		}
		return folder.Folder{}, fmt.Errorf("read folder: %w", err)
	}
	if err := json.Unmarshal([]byte(attributesJSON), &value.Attributes); err != nil {
		return folder.Folder{}, fmt.Errorf("parse folder attributes: %w", err)
	}
	value.Kind = folder.NormalizeKind(kind)
	value.Subscription = folder.NormalizeSubscription(subscription)
	value.Selectable = selectable == 1
	value.Present = present == 1
	value.Stale = stale == 1
	value.SyncEnabled = syncEnabled == 1
	var err error
	value.DiscoveredAt, err = time.Parse(time.RFC3339Nano, discoveredAt)
	if err != nil {
		return folder.Folder{}, fmt.Errorf("parse folder discovery time: %w", err)
	}
	value.StatusAt, err = parseNullableTime(statusAt)
	if err != nil {
		return folder.Folder{}, fmt.Errorf("parse folder status time: %w", err)
	}
	value.MissingSince, err = parseNullableTime(missingSince)
	if err != nil {
		return folder.Folder{}, fmt.Errorf("parse folder missing time: %w", err)
	}
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return folder.Folder{}, fmt.Errorf("parse folder created time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return folder.Folder{}, fmt.Errorf("parse folder updated time: %w", err)
	}
	return value, nil
}

func classifyFolderWrite(action string, err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
		return fmt.Errorf("%w: %s", folder.ErrAmbiguous, action)
	}
	return fmt.Errorf("%s: %w", action, err)
}
