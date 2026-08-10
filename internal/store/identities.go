package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
)

type MailIdentityRepository struct {
	db *sql.DB
}

func primaryMailIdentityID(accountID string) string {
	return "mid_" + strings.TrimPrefix(strings.TrimSpace(accountID), "acct_")
}

const mailIdentityColumns = `
SELECT identity_id, account_id, email_address, display_name, kind, provenance,
       verification, enabled, is_default_send, created_at, updated_at
FROM mail_identities`

type storedMailIdentityCursor struct {
	Version    int    `json:"version"`
	AccountID  string `json:"account_id"`
	Default    int    `json:"default"`
	Address    string `json:"address"`
	IdentityID string `json:"identity_id"`
}

func (r *MailIdentityRepository) ListPage(ctx context.Context, accountID string, limit int, cursor string) ([]account.MailIdentity, bool, string, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || limit < 1 {
		return nil, false, "", fmt.Errorf("list mail identities: invalid account or limit")
	}
	where := " WHERE account_id = ?"
	arguments := []any{accountID}
	if strings.TrimSpace(cursor) != "" {
		var value storedMailIdentityCursor
		if err := decodeOpaqueCursor("icur_", cursor, &value); err != nil || value.Version != 1 || value.AccountID != accountID || (value.Default != 0 && value.Default != 1) || strings.TrimSpace(value.IdentityID) == "" {
			return nil, false, "", account.ErrIdentityInvalidCursor
		}
		where += ` AND (
    is_default_send < ?
    OR (is_default_send = ? AND lower(email_address) > ?)
    OR (is_default_send = ? AND lower(email_address) = ? AND identity_id > ?)
)`
		arguments = append(arguments, value.Default, value.Default, value.Address, value.Default, value.Address, value.IdentityID)
	}
	arguments = append(arguments, limit+1)
	rows, err := r.db.QueryContext(ctx, mailIdentityColumns+where+`
ORDER BY is_default_send DESC, lower(email_address), identity_id
LIMIT ?`, arguments...)
	if err != nil {
		return nil, false, "", fmt.Errorf("list mail identities: %w", err)
	}
	values, err := scanMailIdentities(rows, limit+1)
	if err != nil {
		return nil, false, "", err
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	nextCursor := ""
	if more && len(values) > 0 {
		last := values[len(values)-1]
		nextCursor, err = encodeOpaqueCursor("icur_", storedMailIdentityCursor{
			Version: 1, AccountID: accountID, Default: boolInt(last.Default), Address: strings.ToLower(last.Address), IdentityID: last.ID,
		})
		if err != nil {
			return nil, false, "", fmt.Errorf("encode mail identity cursor: %w", err)
		}
	}
	return values, more, nextCursor, nil
}

func (r *MailIdentityRepository) Resolve(ctx context.Context, accountID, selector string) (account.MailIdentity, error) {
	return resolveMailIdentity(ctx, r.db, accountID, selector)
}

func (r *MailIdentityRepository) Default(ctx context.Context, accountID string) (account.MailIdentity, error) {
	row := r.db.QueryRowContext(ctx, mailIdentityColumns+` WHERE account_id = ? AND is_default_send = 1`, accountID)
	return scanMailIdentity(row)
}

func (r *MailIdentityRepository) Enabled(ctx context.Context, accountID string) ([]account.MailIdentity, error) {
	rows, err := r.db.QueryContext(ctx, mailIdentityColumns+`
WHERE account_id = ? AND enabled = 1
ORDER BY is_default_send DESC, lower(email_address), identity_id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list enabled mail identities: %w", err)
	}
	return scanMailIdentities(rows, 8)
}

func (r *MailIdentityRepository) Create(ctx context.Context, record account.MailIdentityCreateRecord) (account.MailIdentity, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return account.MailIdentity{}, fmt.Errorf("begin mail identity creation: %w", err)
	}
	defer tx.Rollback()
	if _, err := accountByID(ctx, tx, record.AccountID); err != nil {
		return account.MailIdentity{}, err
	}
	if record.MakeDefault && !record.Enabled {
		return account.MailIdentity{}, account.ErrIdentityDisabled
	}
	if record.MakeDefault {
		if _, err := tx.ExecContext(ctx, `UPDATE mail_identities SET is_default_send = 0 WHERE account_id = ? AND is_default_send = 1`, record.AccountID); err != nil {
			return account.MailIdentity{}, fmt.Errorf("clear default mail identity: %w", err)
		}
	}
	if err := insertMailIdentity(ctx, tx, record); err != nil {
		return account.MailIdentity{}, err
	}
	value, err := mailIdentityByID(ctx, tx, record.AccountID, record.IdentityID)
	if err != nil {
		return account.MailIdentity{}, err
	}
	if err := ensureMailIdentityInvariant(ctx, tx, record.AccountID); err != nil {
		return account.MailIdentity{}, err
	}
	if err := tx.Commit(); err != nil {
		return account.MailIdentity{}, fmt.Errorf("commit mail identity creation: %w", err)
	}
	return value, nil
}

func (r *MailIdentityRepository) Update(ctx context.Context, accountID, selector string, update account.MailIdentityUpdateInput, now time.Time) (account.MailIdentity, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return account.MailIdentity{}, fmt.Errorf("begin mail identity update: %w", err)
	}
	defer tx.Rollback()
	value, err := resolveMailIdentity(ctx, tx, accountID, selector)
	if err != nil {
		return account.MailIdentity{}, err
	}
	if update.Address == nil && update.DisplayName == nil && update.Kind == nil {
		return account.MailIdentity{}, account.ErrInvalidInput
	}
	address, displayName, kind := value.Address, value.DisplayName, value.Kind
	if update.Address != nil {
		address = *update.Address
	}
	if update.DisplayName != nil {
		displayName = *update.DisplayName
	}
	if update.Kind != nil {
		kind = *update.Kind
	}
	if value.Kind == account.MailIdentityPrimary && (address != value.Address || kind != account.MailIdentityPrimary) {
		return account.MailIdentity{}, account.ErrIdentityProtected
	}
	if value.Kind != account.MailIdentityPrimary && kind == account.MailIdentityPrimary {
		return account.MailIdentity{}, account.ErrIdentityProtected
	}
	_, err = tx.ExecContext(ctx, `
UPDATE mail_identities
SET email_address = ?, display_name = ?, kind = ?, verification = ?, updated_at = ?
WHERE account_id = ? AND identity_id = ?`,
		address, displayName, kind, account.MailIdentityUnverified, now.UTC().Format(time.RFC3339Nano), accountID, value.ID,
	)
	if err != nil {
		return account.MailIdentity{}, classifyMailIdentityWrite("update mail identity", err)
	}
	value, err = mailIdentityByID(ctx, tx, accountID, value.ID)
	if err != nil {
		return account.MailIdentity{}, err
	}
	if err := tx.Commit(); err != nil {
		return account.MailIdentity{}, fmt.Errorf("commit mail identity update: %w", err)
	}
	return value, nil
}

func (r *MailIdentityRepository) SetEnabled(ctx context.Context, accountID, selector string, enabled bool, now time.Time) (account.MailIdentity, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return account.MailIdentity{}, fmt.Errorf("begin mail identity enabled update: %w", err)
	}
	defer tx.Rollback()
	value, err := resolveMailIdentity(ctx, tx, accountID, selector)
	if err != nil {
		return account.MailIdentity{}, err
	}
	if !enabled && value.Kind == account.MailIdentityPrimary {
		return account.MailIdentity{}, account.ErrIdentityProtected
	}
	if value.Enabled == enabled {
		return value, nil
	}
	if !enabled && value.Default {
		replacement, err := replacementMailIdentity(ctx, tx, accountID, value.ID)
		if err != nil {
			return account.MailIdentity{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mail_identities SET is_default_send = 0 WHERE account_id = ? AND identity_id = ?`, accountID, value.ID); err != nil {
			return account.MailIdentity{}, fmt.Errorf("clear disabled default mail identity: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mail_identities SET is_default_send = 1, updated_at = ? WHERE account_id = ? AND identity_id = ?`, now.UTC().Format(time.RFC3339Nano), accountID, replacement.ID); err != nil {
			return account.MailIdentity{}, fmt.Errorf("replace default mail identity: %w", err)
		}
	}
	verification := value.Verification
	if enabled && verification == account.MailIdentityRejected {
		verification = account.MailIdentityUnverified
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mail_identities SET enabled = ?, verification = ?, updated_at = ? WHERE account_id = ? AND identity_id = ?`, boolInt(enabled), verification, now.UTC().Format(time.RFC3339Nano), accountID, value.ID); err != nil {
		return account.MailIdentity{}, fmt.Errorf("update mail identity enabled state: %w", err)
	}
	if err := ensureMailIdentityInvariant(ctx, tx, accountID); err != nil {
		return account.MailIdentity{}, err
	}
	value, err = mailIdentityByID(ctx, tx, accountID, value.ID)
	if err != nil {
		return account.MailIdentity{}, err
	}
	if err := tx.Commit(); err != nil {
		return account.MailIdentity{}, fmt.Errorf("commit mail identity enabled update: %w", err)
	}
	return value, nil
}

func (r *MailIdentityRepository) SetVerification(ctx context.Context, accountID, selector string, verification account.MailIdentityVerification, now time.Time) (account.MailIdentity, error) {
	switch verification {
	case account.MailIdentityUnverified, account.MailIdentityVerified, account.MailIdentityRejected:
	default:
		return account.MailIdentity{}, account.ErrInvalidInput
	}
	value, err := r.Resolve(ctx, accountID, selector)
	if err != nil {
		return account.MailIdentity{}, err
	}
	if value.Verification == verification {
		return value, nil
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE mail_identities SET verification = ?, updated_at = ? WHERE account_id = ? AND identity_id = ?`, verification, now.UTC().Format(time.RFC3339Nano), accountID, value.ID); err != nil {
		return account.MailIdentity{}, fmt.Errorf("update mail identity verification: %w", err)
	}
	return r.Resolve(ctx, accountID, value.ID)
}

func (r *MailIdentityRepository) SetDefault(ctx context.Context, accountID, selector string, now time.Time) (account.MailIdentity, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return account.MailIdentity{}, fmt.Errorf("begin default mail identity update: %w", err)
	}
	defer tx.Rollback()
	value, err := resolveMailIdentity(ctx, tx, accountID, selector)
	if err != nil {
		return account.MailIdentity{}, err
	}
	if !value.Enabled {
		return account.MailIdentity{}, account.ErrIdentityDisabled
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE mail_identities SET is_default_send = 0 WHERE account_id = ? AND is_default_send = 1`, accountID); err != nil {
		return account.MailIdentity{}, fmt.Errorf("clear default mail identity: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mail_identities SET is_default_send = 1, updated_at = ? WHERE account_id = ? AND identity_id = ?`, timestamp, accountID, value.ID); err != nil {
		return account.MailIdentity{}, fmt.Errorf("set default mail identity: %w", err)
	}
	value, err = mailIdentityByID(ctx, tx, accountID, value.ID)
	if err != nil {
		return account.MailIdentity{}, err
	}
	if err := tx.Commit(); err != nil {
		return account.MailIdentity{}, fmt.Errorf("commit default mail identity update: %w", err)
	}
	return value, nil
}

func (r *MailIdentityRepository) Remove(ctx context.Context, accountID, selector string, now time.Time) (account.MailIdentity, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return account.MailIdentity{}, fmt.Errorf("begin mail identity removal: %w", err)
	}
	defer tx.Rollback()
	value, err := resolveMailIdentity(ctx, tx, accountID, selector)
	if err != nil {
		return account.MailIdentity{}, err
	}
	if value.Kind == account.MailIdentityPrimary {
		return account.MailIdentity{}, account.ErrIdentityProtected
	}
	var replacement account.MailIdentity
	if value.Default {
		replacement, err = replacementMailIdentity(ctx, tx, accountID, value.ID)
		if err != nil {
			return account.MailIdentity{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM mail_identities WHERE account_id = ? AND identity_id = ?`, accountID, value.ID); err != nil {
		return account.MailIdentity{}, fmt.Errorf("remove mail identity: %w", err)
	}
	if replacement.ID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE mail_identities SET is_default_send = 1, updated_at = ? WHERE account_id = ? AND identity_id = ?`, now.UTC().Format(time.RFC3339Nano), accountID, replacement.ID); err != nil {
			return account.MailIdentity{}, fmt.Errorf("replace removed default mail identity: %w", err)
		}
	}
	if err := ensureMailIdentityInvariant(ctx, tx, accountID); err != nil {
		return account.MailIdentity{}, err
	}
	if err := tx.Commit(); err != nil {
		return account.MailIdentity{}, fmt.Errorf("commit mail identity removal: %w", err)
	}
	return value, nil
}

type mailIdentityQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type mailIdentityExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertMailIdentity(ctx context.Context, execer mailIdentityExecer, record account.MailIdentityCreateRecord) error {
	timestamp := record.Now.UTC().Format(time.RFC3339Nano)
	if _, err := execer.ExecContext(ctx, `
INSERT INTO mail_identities (
    identity_id, account_id, email_address, display_name, kind, provenance,
    verification, enabled, is_default_send, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.IdentityID, record.AccountID, record.Address, record.DisplayName, record.Kind,
		record.Provenance, record.Verification, boolInt(record.Enabled), boolInt(record.MakeDefault), timestamp, timestamp,
	); err != nil {
		return classifyMailIdentityWrite("create mail identity", err)
	}
	return nil
}

func resolveMailIdentity(ctx context.Context, queryer mailIdentityQueryer, accountID, selector string) (account.MailIdentity, error) {
	selector = strings.TrimSpace(selector)
	if strings.TrimSpace(accountID) == "" || selector == "" {
		return account.MailIdentity{}, account.ErrIdentityNotFound
	}
	rows, err := queryer.QueryContext(ctx, mailIdentityColumns+`
WHERE account_id = ? AND (identity_id = ? OR lower(email_address) = lower(?))
ORDER BY identity_id
LIMIT 2`, accountID, selector, selector)
	if err != nil {
		return account.MailIdentity{}, fmt.Errorf("resolve mail identity: %w", err)
	}
	values, err := scanMailIdentities(rows, 2)
	if err != nil {
		return account.MailIdentity{}, err
	}
	if len(values) != 1 {
		return account.MailIdentity{}, account.ErrIdentityNotFound
	}
	return values[0], nil
}

func mailIdentityByID(ctx context.Context, queryer mailIdentityQueryer, accountID, identityID string) (account.MailIdentity, error) {
	row := queryer.QueryRowContext(ctx, mailIdentityColumns+` WHERE account_id = ? AND identity_id = ?`, accountID, identityID)
	return scanMailIdentity(row)
}

func replacementMailIdentity(ctx context.Context, queryer mailIdentityQueryer, accountID, excludeID string) (account.MailIdentity, error) {
	row := queryer.QueryRowContext(ctx, mailIdentityColumns+`
WHERE account_id = ? AND identity_id <> ? AND enabled = 1
ORDER BY CASE WHEN kind = 'primary' THEN 0 ELSE 1 END, lower(email_address), identity_id
LIMIT 1`, accountID, excludeID)
	value, err := scanMailIdentity(row)
	if errors.Is(err, account.ErrIdentityNotFound) {
		return account.MailIdentity{}, account.ErrIdentityNoEnabledSender
	}
	return value, err
}

func ensureMailIdentityInvariant(ctx context.Context, queryer mailIdentityQueryer, accountID string) error {
	var enabled, defaults int
	if err := queryer.QueryRowContext(ctx, `
SELECT count(*), COALESCE(sum(CASE WHEN is_default_send = 1 THEN 1 ELSE 0 END), 0)
FROM mail_identities WHERE account_id = ? AND enabled = 1`, accountID).Scan(&enabled, &defaults); err != nil {
		return fmt.Errorf("validate mail identity state: %w", err)
	}
	if enabled < 1 || defaults != 1 {
		return account.ErrIdentityNoEnabledSender
	}
	return nil
}

func scanMailIdentities(rows *sql.Rows, capacity int) ([]account.MailIdentity, error) {
	defer rows.Close()
	values := make([]account.MailIdentity, 0, capacity)
	for rows.Next() {
		value, err := scanMailIdentity(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read mail identities: %w", err)
	}
	return values, nil
}

func scanMailIdentity(row rowScanner) (account.MailIdentity, error) {
	var (
		value                account.MailIdentity
		kind, provenance     string
		verification         string
		enabled, isDefault   int
		createdAt, updatedAt string
	)
	if err := row.Scan(
		&value.ID, &value.AccountID, &value.Address, &value.DisplayName,
		&kind, &provenance, &verification, &enabled, &isDefault, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return account.MailIdentity{}, account.ErrIdentityNotFound
		}
		return account.MailIdentity{}, fmt.Errorf("read mail identity: %w", err)
	}
	value.Kind = account.MailIdentityKind(kind)
	value.Provenance = account.MailIdentityProvenance(provenance)
	value.Verification = account.MailIdentityVerification(verification)
	value.Enabled = enabled == 1
	value.Default = isDefault == 1
	var err error
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return account.MailIdentity{}, fmt.Errorf("parse mail identity created time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return account.MailIdentity{}, fmt.Errorf("parse mail identity updated time: %w", err)
	}
	return value, nil
}

func classifyMailIdentityWrite(action string, err error) error {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "unique constraint failed") {
		return fmt.Errorf("%w: %s", account.ErrIdentityConflict, action)
	}
	return fmt.Errorf("%s: %w", action, err)
}
