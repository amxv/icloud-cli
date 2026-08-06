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

type LocationMutationRepository struct {
	db *sql.DB
}

const locationMutationColumns = `
SELECT mutation_id, account_id, message_id, action,
       source_location_id, source_folder_id,
       COALESCE(destination_folder_id, ''), proposed_location_id,
       COALESCE(destination_location_id, ''), request_hash, idempotency_key,
       status, provider_method, source_present,
       destination_uid_validity, destination_uid, mapping_known,
       reconciliation_required, error_code, created_at, updated_at
FROM location_mutations`

func (repository *LocationMutationRepository) Begin(
	ctx context.Context,
	input maildomain.BeginLocationMutation,
) (maildomain.LocationMutation, bool, error) {
	input.MutationID = strings.TrimSpace(input.MutationID)
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.MessageID = strings.TrimSpace(input.MessageID)
	input.SourceLocationID = strings.TrimSpace(input.SourceLocationID)
	input.SourceFolderID = strings.TrimSpace(input.SourceFolderID)
	input.DestinationFolderID = strings.TrimSpace(input.DestinationFolderID)
	input.ProposedLocationID = strings.TrimSpace(input.ProposedLocationID)
	input.RequestHash = strings.ToLower(strings.TrimSpace(input.RequestHash))
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if !strings.HasPrefix(input.MutationID, "mut_") || !strings.HasPrefix(input.AccountID, "acct_") ||
		!strings.HasPrefix(input.MessageID, "msg_") || !strings.HasPrefix(input.SourceLocationID, "loc_") ||
		!strings.HasPrefix(input.SourceFolderID, "fld_") || len(input.RequestHash) != 64 ||
		input.Action.TransportOperation() == "" {
		return maildomain.LocationMutation{}, false, fmt.Errorf("begin location mutation: %w", maildomain.ErrInvalidLocationMutation)
	}
	if input.Action == maildomain.LocationActionDelete {
		if input.DestinationFolderID != "" || input.ProposedLocationID != "" {
			return maildomain.LocationMutation{}, false, fmt.Errorf("begin location mutation: delete cannot name a destination")
		}
	} else if !strings.HasPrefix(input.DestinationFolderID, "fld_") || !strings.HasPrefix(input.ProposedLocationID, "loc_") {
		return maildomain.LocationMutation{}, false, fmt.Errorf("begin location mutation: destination folder and proposed location are required")
	}
	if len(input.IdempotencyKey) > 256 {
		return maildomain.LocationMutation{}, false, fmt.Errorf("begin location mutation: idempotency key is too long")
	}

	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.LocationMutation{}, false, fmt.Errorf("begin location mutation transaction: %w", err)
	}
	defer tx.Rollback()
	if input.IdempotencyKey != "" {
		existing, scanErr := scanLocationMutation(tx.QueryRowContext(ctx, locationMutationColumns+`
WHERE account_id = ? AND idempotency_key = ?`, input.AccountID, input.IdempotencyKey))
		switch {
		case scanErr == nil:
			if existing.RequestHash != input.RequestHash {
				return maildomain.LocationMutation{}, false, maildomain.ErrMutationConflict
			}
			return existing, true, nil
		case !errors.Is(scanErr, maildomain.ErrMutationNotFound):
			return maildomain.LocationMutation{}, false, scanErr
		}
	}
	var sourceMessageID, sourceFolderID, sourceAccountID string
	if err := tx.QueryRowContext(ctx, `
SELECT message_id, folder_id, account_id
FROM message_locations WHERE location_id = ?`, input.SourceLocationID).Scan(
		&sourceMessageID, &sourceFolderID, &sourceAccountID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.LocationMutation{}, false, maildomain.ErrLocationNotFound
		}
		return maildomain.LocationMutation{}, false, fmt.Errorf("resolve mutation source location: %w", err)
	}
	if sourceMessageID != input.MessageID || sourceFolderID != input.SourceFolderID || sourceAccountID != input.AccountID {
		return maildomain.LocationMutation{}, false, maildomain.ErrMutationConflict
	}
	timestamp := input.Now.UTC().Format(time.RFC3339Nano)
	var destination any
	if input.DestinationFolderID != "" {
		destination = input.DestinationFolderID
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO location_mutations (
    mutation_id, account_id, message_id, action,
    source_location_id, source_folder_id, destination_folder_id,
    proposed_location_id, request_hash, idempotency_key,
    status, source_present, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 1, ?, ?)`,
		input.MutationID, input.AccountID, input.MessageID, input.Action,
		input.SourceLocationID, input.SourceFolderID, destination,
		input.ProposedLocationID, input.RequestHash, input.IdempotencyKey,
		timestamp, timestamp,
	); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") && input.IdempotencyKey != "" {
			existing, readErr := scanLocationMutation(tx.QueryRowContext(ctx, locationMutationColumns+`
WHERE account_id = ? AND idempotency_key = ?`, input.AccountID, input.IdempotencyKey))
			if readErr == nil && existing.RequestHash == input.RequestHash {
				return existing, true, nil
			}
			return maildomain.LocationMutation{}, false, maildomain.ErrMutationConflict
		}
		return maildomain.LocationMutation{}, false, fmt.Errorf("insert location mutation: %w", err)
	}
	value, err := scanLocationMutation(tx.QueryRowContext(ctx, locationMutationColumns+` WHERE mutation_id = ?`, input.MutationID))
	if err != nil {
		return maildomain.LocationMutation{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.LocationMutation{}, false, fmt.Errorf("commit location mutation intent: %w", err)
	}
	return value, false, nil
}

func (repository *LocationMutationRepository) Mutation(ctx context.Context, accountID, mutationID string) (maildomain.LocationMutation, error) {
	return scanLocationMutation(repository.db.QueryRowContext(ctx, locationMutationColumns+`
WHERE account_id = ? AND mutation_id = ?`, strings.TrimSpace(accountID), strings.TrimSpace(mutationID)))
}

func (repository *LocationMutationRepository) ByIdempotencyKey(ctx context.Context, accountID, key string) (maildomain.LocationMutation, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return maildomain.LocationMutation{}, maildomain.ErrMutationNotFound
	}
	return scanLocationMutation(repository.db.QueryRowContext(ctx, locationMutationColumns+`
WHERE account_id = ? AND idempotency_key = ?`, strings.TrimSpace(accountID), key))
}

func (repository *LocationMutationRepository) List(
	ctx context.Context,
	accountID string,
	limit int,
) ([]maildomain.LocationMutation, bool, error) {
	if limit < 1 {
		return nil, false, errors.New("list location mutations: limit must be positive")
	}
	rows, err := repository.db.QueryContext(ctx, locationMutationColumns+`
WHERE account_id = ?
ORDER BY updated_at DESC, mutation_id DESC
LIMIT ?`, strings.TrimSpace(accountID), limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list location mutations: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.LocationMutation, 0, limit+1)
	for rows.Next() {
		value, scanErr := scanLocationMutation(rows)
		if scanErr != nil {
			return nil, false, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list location mutations: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	return values, more, nil
}

func (repository *LocationMutationRepository) MarkFailed(
	ctx context.Context,
	mutationID, errorCode string,
	now time.Time,
) (maildomain.LocationMutation, error) {
	if _, err := repository.db.ExecContext(ctx, `
UPDATE location_mutations
SET status = 'failed', error_code = ?, updated_at = ?
WHERE mutation_id = ? AND status = 'pending'`,
		strings.TrimSpace(errorCode), now.UTC().Format(time.RFC3339Nano), strings.TrimSpace(mutationID),
	); err != nil {
		return maildomain.LocationMutation{}, fmt.Errorf("mark location mutation failed: %w", err)
	}
	return scanLocationMutation(repository.db.QueryRowContext(ctx, locationMutationColumns+` WHERE mutation_id = ?`, strings.TrimSpace(mutationID)))
}

func (repository *LocationMutationRepository) Apply(
	ctx context.Context,
	input maildomain.ApplyLocationMutation,
) (maildomain.LocationMutation, error) {
	if input.Status != maildomain.LocationMutationConfirmed && input.Status != maildomain.LocationMutationUnresolved {
		return maildomain.LocationMutation{}, fmt.Errorf("apply location mutation: invalid outcome status")
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.LocationMutation{}, fmt.Errorf("begin location mutation apply: %w", err)
	}
	defer tx.Rollback()
	mutation, err := scanLocationMutation(tx.QueryRowContext(ctx, locationMutationColumns+` WHERE mutation_id = ?`, strings.TrimSpace(input.MutationID)))
	if err != nil {
		return maildomain.LocationMutation{}, err
	}
	if mutation.Status != maildomain.LocationMutationPending {
		return mutation, nil
	}
	source, err := scanLocation(tx.QueryRowContext(ctx, locationColumns+` WHERE location_id = ?`, mutation.SourceLocationID))
	if err != nil {
		return maildomain.LocationMutation{}, err
	}
	timestamp := input.Now.UTC().Format(time.RFC3339Nano)
	if err := updateMutationLocationState(ctx, tx, source.ID, input.SourceFlags, input.SourceModSequence, input.SourcePresent, timestamp); err != nil {
		return maildomain.LocationMutation{}, err
	}
	var destinationLocationID string
	if input.DestinationKnown {
		if mutation.DestinationFolderID == "" || input.DestinationUIDValidity == 0 || input.DestinationUID == 0 {
			return maildomain.LocationMutation{}, fmt.Errorf("apply location mutation: incomplete destination mapping")
		}
		destinationLocationID, err = upsertMutationDestination(ctx, tx, mutation, source, input, timestamp)
		if err != nil {
			return maildomain.LocationMutation{}, err
		}
	}
	var destination any
	if destinationLocationID != "" {
		destination = destinationLocationID
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE location_mutations
SET status = ?, provider_method = ?, source_present = ?,
    destination_location_id = ?, destination_uid_validity = ?, destination_uid = ?,
    mapping_known = ?, reconciliation_required = ?, error_code = ?, updated_at = ?
WHERE mutation_id = ?`,
		input.Status, strings.TrimSpace(input.ProviderMethod), boolInt(input.SourcePresent),
		destination, input.DestinationUIDValidity, input.DestinationUID,
		boolInt(input.DestinationKnown), boolInt(input.ReconciliationRequired),
		strings.TrimSpace(input.ErrorCode), timestamp, mutation.ID,
	); err != nil {
		return maildomain.LocationMutation{}, fmt.Errorf("update location mutation outcome: %w", err)
	}
	if err := rebuildAffectedThreadGraph(ctx, tx, mutation.AccountID, []string{mutation.MessageID}, input.Now); err != nil {
		return maildomain.LocationMutation{}, err
	}
	value, err := scanLocationMutation(tx.QueryRowContext(ctx, locationMutationColumns+` WHERE mutation_id = ?`, mutation.ID))
	if err != nil {
		return maildomain.LocationMutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.LocationMutation{}, fmt.Errorf("commit location mutation outcome: %w", err)
	}
	return value, nil
}

func updateMutationLocationState(
	ctx context.Context,
	tx *sql.Tx,
	locationID string,
	flags maildomain.LocationFlags,
	modSequence uint64,
	present bool,
	timestamp string,
) error {
	unknownJSON, err := json.Marshal(maildomain.ParseLocationFlags(flags.Unknown).Unknown)
	if err != nil {
		return fmt.Errorf("encode mutation location flags: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE message_locations
SET seen = ?, flagged = ?, answered = ?, draft = ?, deleted = ?,
    unknown_flags_json = ?, mod_sequence = ?, flags_checked_at = ?,
    present = ?, updated_at = ?
WHERE location_id = ?`,
		boolInt(flags.Seen), boolInt(flags.Flagged), boolInt(flags.Answered),
		boolInt(flags.Draft), boolInt(flags.Deleted), string(unknownJSON),
		formatModSequence(modSequence), timestamp, boolInt(present), timestamp, locationID,
	); err != nil {
		return fmt.Errorf("update mutation source location: %w", err)
	}
	return nil
}

func upsertMutationDestination(
	ctx context.Context,
	tx *sql.Tx,
	mutation maildomain.LocationMutation,
	source maildomain.Location,
	input maildomain.ApplyLocationMutation,
	timestamp string,
) (string, error) {
	var existingID, existingMessageID string
	err := tx.QueryRowContext(ctx, `
SELECT location_id, message_id
FROM message_locations
WHERE account_id = ? AND folder_id = ? AND uid_validity = ? AND uid = ?`,
		mutation.AccountID, mutation.DestinationFolderID,
		input.DestinationUIDValidity, input.DestinationUID,
	).Scan(&existingID, &existingMessageID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("resolve mutation destination location: %w", err)
	}
	if err == nil && existingMessageID != mutation.MessageID {
		return "", maildomain.ErrMutationConflict
	}
	locationID := existingID
	if locationID == "" {
		locationID = mutation.ProposedLocationID
	}
	if !strings.HasPrefix(locationID, "loc_") {
		return "", fmt.Errorf("apply location mutation: destination location ID is unavailable")
	}
	unknownJSON, err := json.Marshal(maildomain.ParseLocationFlags(input.DestinationFlags.Unknown).Unknown)
	if err != nil {
		return "", fmt.Errorf("encode destination flags: %w", err)
	}
	var internalDate any
	if source.InternalDate != nil {
		internalDate = source.InternalDate.UTC().Format(time.RFC3339Nano)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO message_locations (
    location_id, message_id, account_id, folder_id, uid_validity, uid,
    seen, flagged, answered, draft, deleted, unknown_flags_json, mod_sequence,
    flags_checked_at, internal_date, rfc822_size, raw_content_hash,
    raw_storage_key, raw_byte_size, present, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
ON CONFLICT(account_id, folder_id, uid_validity, uid) DO UPDATE SET
    message_id = excluded.message_id,
    seen = excluded.seen,
    flagged = excluded.flagged,
    answered = excluded.answered,
    draft = excluded.draft,
    deleted = excluded.deleted,
    unknown_flags_json = excluded.unknown_flags_json,
    mod_sequence = excluded.mod_sequence,
    flags_checked_at = excluded.flags_checked_at,
    present = 1,
    updated_at = excluded.updated_at`,
		locationID, mutation.MessageID, mutation.AccountID, mutation.DestinationFolderID,
		input.DestinationUIDValidity, input.DestinationUID,
		boolInt(input.DestinationFlags.Seen), boolInt(input.DestinationFlags.Flagged),
		boolInt(input.DestinationFlags.Answered), boolInt(input.DestinationFlags.Draft),
		boolInt(input.DestinationFlags.Deleted), string(unknownJSON),
		formatModSequence(input.DestinationModSequence), timestamp,
		internalDate, source.RFC822Size, source.RawContentHash,
		source.RawStorageKey, source.RawByteSize, timestamp, timestamp,
	); err != nil {
		return "", fmt.Errorf("persist mutation destination location: %w", err)
	}
	if err := persistIdentityEvidence(
		ctx, tx, mutation.AccountID, mutation.MessageID,
		maildomain.IdentityProviderLocation,
		maildomain.ProviderLocationIdentity(mutation.DestinationFolderID, input.DestinationUIDValidity, input.DestinationUID),
		maildomain.IdentityStrong, locationID, timestamp,
	); err != nil {
		return "", err
	}
	return locationID, nil
}

func scanLocationMutation(row rowScanner) (maildomain.LocationMutation, error) {
	var (
		value                                  maildomain.LocationMutation
		sourcePresent, mappingKnown, reconcile int
		createdAt, updatedAt                   string
	)
	if err := row.Scan(
		&value.ID, &value.AccountID, &value.MessageID, &value.Action,
		&value.SourceLocationID, &value.SourceFolderID,
		&value.DestinationFolderID, &value.ProposedLocationID,
		&value.DestinationLocationID, &value.RequestHash, &value.IdempotencyKey,
		&value.Status, &value.ProviderMethod, &sourcePresent,
		&value.DestinationUIDValidity, &value.DestinationUID, &mappingKnown,
		&reconcile, &value.ErrorCode, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.LocationMutation{}, maildomain.ErrMutationNotFound
		}
		return maildomain.LocationMutation{}, fmt.Errorf("read location mutation: %w", err)
	}
	value.SourcePresent = sourcePresent == 1
	value.MappingKnown = mappingKnown == 1
	value.ReconciliationRequired = reconcile == 1
	var err error
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return maildomain.LocationMutation{}, fmt.Errorf("parse mutation creation time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.LocationMutation{}, fmt.Errorf("parse mutation update time: %w", err)
	}
	return value, nil
}
