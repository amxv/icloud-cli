package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
)

type AccountRepository struct {
	db *sql.DB
}

const accountColumns = `
SELECT a.account_id, a.friendly_name, a.email_address, a.imap_username,
       a.status, a.is_default, COALESCE(c.scheme, ''),
       a.imap_capabilities_json, a.smtp_capabilities_json,
       COALESCE(a.last_error_code, ''), a.last_error_at, a.last_healthy_at, a.last_tested_at,
       a.created_at, a.updated_at
FROM accounts AS a
LEFT JOIN credential_refs AS c ON c.account_id = a.account_id`

func (r *AccountRepository) List(ctx context.Context, limit int) ([]account.Account, bool, error) {
	accounts, more, _, err := r.ListPage(ctx, limit, "")
	return accounts, more, err
}

type storedAccountCursor struct {
	Version   int    `json:"version"`
	Default   int    `json:"default"`
	Name      string `json:"name"`
	AccountID string `json:"account_id"`
}

func (r *AccountRepository) ListPage(ctx context.Context, limit int, cursor string) ([]account.Account, bool, string, error) {
	if limit < 1 {
		return nil, false, "", fmt.Errorf("list accounts: limit must be positive")
	}
	where := ""
	arguments := make([]any, 0, 5)
	var cursorValue storedAccountCursor
	if strings.TrimSpace(cursor) != "" {
		if err := decodeOpaqueCursor("acur_", cursor, &cursorValue); err != nil || cursorValue.Version != 1 || (cursorValue.Default != 0 && cursorValue.Default != 1) || strings.TrimSpace(cursorValue.AccountID) == "" {
			return nil, false, "", account.ErrInvalidCursor
		}
		where = `
WHERE a.is_default < ?
   OR (a.is_default = ? AND lower(a.friendly_name) > ?)
   OR (a.is_default = ? AND lower(a.friendly_name) = ? AND a.account_id > ?)`
		arguments = append(arguments, cursorValue.Default, cursorValue.Default, cursorValue.Name, cursorValue.Default, cursorValue.Name, cursorValue.AccountID)
	}
	arguments = append(arguments, limit+1)
	rows, err := r.db.QueryContext(ctx, accountColumns+where+`
ORDER BY a.is_default DESC, lower(a.friendly_name), a.account_id
LIMIT ?`, arguments...)
	if err != nil {
		return nil, false, "", fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()

	accounts := make([]account.Account, 0, limit)
	for rows.Next() {
		value, err := scanAccount(rows)
		if err != nil {
			return nil, false, "", err
		}
		accounts = append(accounts, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, "", fmt.Errorf("list accounts: %w", err)
	}
	more := len(accounts) > limit
	if more {
		accounts = accounts[:limit]
	}
	nextCursor := ""
	if more && len(accounts) > 0 {
		last := accounts[len(accounts)-1]
		var sortName string
		if err := r.db.QueryRowContext(ctx, `SELECT lower(friendly_name) FROM accounts WHERE account_id = ?`, last.ID).Scan(&sortName); err != nil {
			return nil, false, "", fmt.Errorf("read account cursor order: %w", err)
		}
		nextCursor, err = encodeOpaqueCursor("acur_", storedAccountCursor{
			Version: 1, Default: boolInt(last.Default), Name: sortName, AccountID: last.ID,
		})
		if err != nil {
			return nil, false, "", fmt.Errorf("encode account cursor: %w", err)
		}
	}
	return accounts, more, nextCursor, nil
}

func (r *AccountRepository) Resolve(ctx context.Context, selector string) (account.Account, error) {
	return resolveAccount(ctx, r.db, selector)
}

func (r *AccountRepository) ResolveSelected(ctx context.Context, selector string) (account.Account, error) {
	selector = strings.TrimSpace(selector)
	if selector != "" {
		return resolveAccount(ctx, r.db, selector)
	}

	rows, err := r.db.QueryContext(ctx, accountColumns+`
WHERE a.is_default = 1
ORDER BY a.account_id
LIMIT 2`)
	if err != nil {
		return account.Account{}, fmt.Errorf("resolve default account: %w", err)
	}
	defaults, err := scanAccounts(rows, 2)
	if err != nil {
		return account.Account{}, err
	}
	if len(defaults) == 1 {
		return defaults[0], nil
	}
	if len(defaults) > 1 {
		return account.Account{}, account.ErrAmbiguous
	}

	rows, err = r.db.QueryContext(ctx, accountColumns+`
ORDER BY a.account_id
LIMIT 2`)
	if err != nil {
		return account.Account{}, fmt.Errorf("resolve sole account: %w", err)
	}
	values, err := scanAccounts(rows, 2)
	if err != nil {
		return account.Account{}, err
	}
	switch len(values) {
	case 0:
		return account.Account{}, account.ErrNotFound
	case 1:
		return values[0], nil
	default:
		return account.Account{}, account.ErrAmbiguous
	}
}

func (r *AccountRepository) Create(ctx context.Context, record account.CreateRecord) (account.Account, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return account.Account{}, fmt.Errorf("begin account creation: %w", err)
	}
	defer tx.Rollback()

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&count); err != nil {
		return account.Account{}, fmt.Errorf("count accounts: %w", err)
	}
	makeDefault := record.MakeDefault || count == 0
	if makeDefault {
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET is_default = 0 WHERE is_default = 1`); err != nil {
			return account.Account{}, fmt.Errorf("clear default account: %w", err)
		}
	}
	now := record.Now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO accounts (
    account_id, friendly_name, email_address, imap_username, status,
    is_default, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		record.AccountID,
		record.Name,
		record.Email,
		record.IMAPUsername,
		account.StatusPendingValidation,
		boolInt(makeDefault),
		now,
		now,
	); err != nil {
		return account.Account{}, classifyAccountWrite("create account", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO credential_refs (
    credential_ref_id, account_id, scheme, reference, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?)`,
		record.CredentialID,
		record.AccountID,
		record.CredentialScheme,
		record.CredentialRef,
		now,
		now,
	); err != nil {
		return account.Account{}, classifyAccountWrite("create account credential", err)
	}
	value, err := accountByID(ctx, tx, record.AccountID)
	if err != nil {
		return account.Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return account.Account{}, fmt.Errorf("commit account creation: %w", err)
	}
	return value, nil
}

func (r *AccountRepository) Rename(ctx context.Context, selector, name string, now time.Time) (account.Account, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return account.Account{}, fmt.Errorf("begin account rename: %w", err)
	}
	defer tx.Rollback()
	value, err := resolveAccount(ctx, tx, selector)
	if err != nil {
		return account.Account{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE accounts SET friendly_name = ?, updated_at = ? WHERE account_id = ?`,
		name, now.UTC().Format(time.RFC3339Nano), value.ID,
	); err != nil {
		return account.Account{}, classifyAccountWrite("rename account", err)
	}
	value, err = accountByID(ctx, tx, value.ID)
	if err != nil {
		return account.Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return account.Account{}, fmt.Errorf("commit account rename: %w", err)
	}
	return value, nil
}

func (r *AccountRepository) SetDefault(ctx context.Context, selector string, now time.Time) (account.Account, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return account.Account{}, fmt.Errorf("begin default-account update: %w", err)
	}
	defer tx.Rollback()
	value, err := resolveAccount(ctx, tx, selector)
	if err != nil {
		return account.Account{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET is_default = 0 WHERE is_default = 1`); err != nil {
		return account.Account{}, fmt.Errorf("clear default account: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE accounts SET is_default = 1, updated_at = ? WHERE account_id = ?`,
		now.UTC().Format(time.RFC3339Nano), value.ID,
	); err != nil {
		return account.Account{}, fmt.Errorf("set default account: %w", err)
	}
	value, err = accountByID(ctx, tx, value.ID)
	if err != nil {
		return account.Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return account.Account{}, fmt.Errorf("commit default-account update: %w", err)
	}
	return value, nil
}

func (r *AccountRepository) UpdateIMAPUsername(ctx context.Context, accountID, username string, now time.Time) (account.Account, error) {
	result, err := r.db.ExecContext(ctx, `
UPDATE accounts SET imap_username = ?, updated_at = ? WHERE account_id = ?`,
		username, now.UTC().Format(time.RFC3339Nano), accountID,
	)
	if err != nil {
		return account.Account{}, fmt.Errorf("update IMAP username: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return account.Account{}, fmt.Errorf("read IMAP username update result: %w", err)
	}
	if changed != 1 {
		return account.Account{}, account.ErrNotFound
	}
	return accountByID(ctx, r.db, accountID)
}

func (r *AccountRepository) Remove(ctx context.Context, selector string) (account.Account, account.Credential, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return account.Account{}, account.Credential{}, fmt.Errorf("begin account removal: %w", err)
	}
	defer tx.Rollback()
	value, err := resolveAccount(ctx, tx, selector)
	if err != nil {
		return account.Account{}, account.Credential{}, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&count); err != nil {
		return account.Account{}, account.Credential{}, fmt.Errorf("count accounts: %w", err)
	}
	if value.Default && count > 1 {
		return account.Account{}, account.Credential{}, account.ErrDefaultRemoval
	}
	credential, err := credentialByAccountID(ctx, tx, value.ID)
	if err != nil && !errors.Is(err, account.ErrCredentialMissing) {
		return account.Account{}, account.Credential{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE account_id = ?`, value.ID); err != nil {
		return account.Account{}, account.Credential{}, fmt.Errorf("remove account: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return account.Account{}, account.Credential{}, fmt.Errorf("commit account removal: %w", err)
	}
	return value, credential, nil
}

func (r *AccountRepository) RemovalCandidate(ctx context.Context, selector string) (account.Account, account.Credential, error) {
	value, err := resolveAccount(ctx, r.db, selector)
	if err != nil {
		return account.Account{}, account.Credential{}, err
	}
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&count); err != nil {
		return account.Account{}, account.Credential{}, fmt.Errorf("count accounts: %w", err)
	}
	if value.Default && count > 1 {
		return account.Account{}, account.Credential{}, account.ErrDefaultRemoval
	}
	credential, err := credentialByAccountID(ctx, r.db, value.ID)
	if err != nil && !errors.Is(err, account.ErrCredentialMissing) {
		return account.Account{}, account.Credential{}, err
	}
	return value, credential, nil
}

func (r *AccountRepository) Credential(ctx context.Context, selector string) (account.Account, account.Credential, error) {
	value, err := resolveAccount(ctx, r.db, selector)
	if err != nil {
		return account.Account{}, account.Credential{}, err
	}
	credential, err := credentialByAccountID(ctx, r.db, value.ID)
	if err != nil {
		return account.Account{}, account.Credential{}, err
	}
	return value, credential, nil
}

func (r *AccountRepository) UpsertCredential(
	ctx context.Context,
	selector string,
	credentialID string,
	scheme account.CredentialScheme,
	reference string,
	now time.Time,
) (account.Account, account.Credential, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return account.Account{}, account.Credential{}, fmt.Errorf("begin credential update: %w", err)
	}
	defer tx.Rollback()
	value, err := resolveAccount(ctx, tx, selector)
	if err != nil {
		return account.Account{}, account.Credential{}, err
	}
	existing, err := credentialByAccountID(ctx, tx, value.ID)
	timestamp := now.UTC().Format(time.RFC3339Nano)
	switch {
	case err == nil:
		if _, err := tx.ExecContext(ctx, `
UPDATE credential_refs SET scheme = ?, reference = ?, updated_at = ? WHERE account_id = ?`,
			scheme, reference, timestamp, value.ID,
		); err != nil {
			return account.Account{}, account.Credential{}, classifyAccountWrite("replace account credential", err)
		}
		credentialID = existing.ID
	case errors.Is(err, account.ErrCredentialMissing):
		if _, err := tx.ExecContext(ctx, `
INSERT INTO credential_refs (
    credential_ref_id, account_id, scheme, reference, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?)`,
			credentialID, value.ID, scheme, reference, timestamp, timestamp,
		); err != nil {
			return account.Account{}, account.Credential{}, classifyAccountWrite("create account credential", err)
		}
	default:
		return account.Account{}, account.Credential{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE accounts
SET status = ?, last_error_code = NULL, last_error_at = NULL, last_tested_at = NULL,
    imap_capabilities_json = '[]', smtp_capabilities_json = '[]', updated_at = ?
WHERE account_id = ?`,
		account.StatusPendingValidation, timestamp, value.ID,
	); err != nil {
		return account.Account{}, account.Credential{}, fmt.Errorf("mark account pending validation: %w", err)
	}
	value, err = accountByID(ctx, tx, value.ID)
	if err != nil {
		return account.Account{}, account.Credential{}, err
	}
	credential, err := credentialByAccountID(ctx, tx, value.ID)
	if err != nil {
		return account.Account{}, account.Credential{}, err
	}
	if credential.ID != credentialID {
		return account.Account{}, account.Credential{}, errors.New("credential identity changed unexpectedly")
	}
	if err := tx.Commit(); err != nil {
		return account.Account{}, account.Credential{}, fmt.Errorf("commit credential update: %w", err)
	}
	return value, credential, nil
}

func (r *AccountRepository) DeleteCredential(ctx context.Context, selector string, now time.Time) (account.Account, account.Credential, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return account.Account{}, account.Credential{}, fmt.Errorf("begin credential deletion: %w", err)
	}
	defer tx.Rollback()
	value, err := resolveAccount(ctx, tx, selector)
	if err != nil {
		return account.Account{}, account.Credential{}, err
	}
	credential, err := credentialByAccountID(ctx, tx, value.ID)
	if err != nil {
		return account.Account{}, account.Credential{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM credential_refs WHERE account_id = ?`, value.ID); err != nil {
		return account.Account{}, account.Credential{}, fmt.Errorf("delete account credential: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE accounts
SET status = ?, last_error_code = NULL, last_error_at = NULL, last_tested_at = NULL,
    imap_capabilities_json = '[]', smtp_capabilities_json = '[]', updated_at = ?
WHERE account_id = ?`,
		account.StatusPendingValidation, now.UTC().Format(time.RFC3339Nano), value.ID,
	); err != nil {
		return account.Account{}, account.Credential{}, fmt.Errorf("mark account pending validation: %w", err)
	}
	value, err = accountByID(ctx, tx, value.ID)
	if err != nil {
		return account.Account{}, account.Credential{}, err
	}
	if err := tx.Commit(); err != nil {
		return account.Account{}, account.Credential{}, fmt.Errorf("commit credential deletion: %w", err)
	}
	credential.Configured = false
	credential.UpdatedAt = now.UTC()
	return value, credential, nil
}

func (r *AccountRepository) UpdateHealth(ctx context.Context, update account.HealthUpdate) (account.Account, error) {
	imapCapabilities, err := json.Marshal(update.IMAPCapabilities)
	if err != nil {
		return account.Account{}, fmt.Errorf("encode IMAP capabilities: %w", err)
	}
	smtpCapabilities, err := json.Marshal(update.SMTPCapabilities)
	if err != nil {
		return account.Account{}, fmt.Errorf("encode SMTP capabilities: %w", err)
	}
	timestamp := update.CheckedAt.UTC().Format(time.RFC3339Nano)
	lastErrorCode := nullableString(update.LastErrorCode)
	var lastErrorAt any
	var lastHealthyAt any
	if update.Healthy {
		lastHealthyAt = timestamp
	} else {
		lastErrorAt = timestamp
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE accounts
SET status = ?, imap_username = ?, imap_capabilities_json = ?, smtp_capabilities_json = ?,
    last_error_code = ?, last_error_at = ?,
    last_healthy_at = COALESCE(?, last_healthy_at), last_tested_at = ?, updated_at = ?
WHERE account_id = ?`,
		update.Status,
		update.IMAPUsername,
		string(imapCapabilities),
		string(smtpCapabilities),
		lastErrorCode,
		lastErrorAt,
		lastHealthyAt,
		timestamp,
		timestamp,
		update.AccountID,
	)
	if err != nil {
		return account.Account{}, fmt.Errorf("update account health: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return account.Account{}, fmt.Errorf("read account health update result: %w", err)
	}
	if rows != 1 {
		return account.Account{}, account.ErrNotFound
	}
	return accountByID(ctx, r.db, update.AccountID)
}

type accountQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func resolveAccount(ctx context.Context, queryer accountQueryer, selector string) (account.Account, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return account.Account{}, account.ErrNotFound
	}
	rows, err := queryer.QueryContext(ctx, accountColumns+`
WHERE a.account_id = ? OR lower(a.friendly_name) = lower(?) OR lower(a.email_address) = lower(?)
ORDER BY a.account_id
LIMIT 2`, selector, selector, selector)
	if err != nil {
		return account.Account{}, fmt.Errorf("resolve account: %w", err)
	}
	matches, err := scanAccounts(rows, 2)
	if err != nil {
		return account.Account{}, err
	}
	switch len(matches) {
	case 0:
		return account.Account{}, account.ErrNotFound
	case 1:
		return matches[0], nil
	default:
		return account.Account{}, account.ErrAmbiguous
	}
}

func accountByID(ctx context.Context, queryer accountQueryer, accountID string) (account.Account, error) {
	row := queryer.QueryRowContext(ctx, accountColumns+` WHERE a.account_id = ?`, accountID)
	return scanAccount(row)
}

func credentialByAccountID(ctx context.Context, queryer accountQueryer, accountID string) (account.Credential, error) {
	row := queryer.QueryRowContext(ctx, `
SELECT credential_ref_id, account_id, scheme, reference, created_at, updated_at
FROM credential_refs WHERE account_id = ?`, accountID)
	var (
		value                account.Credential
		scheme               string
		createdAt, updatedAt string
	)
	if err := row.Scan(&value.ID, &value.AccountID, &scheme, &value.Reference, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return account.Credential{}, account.ErrCredentialMissing
		}
		return account.Credential{}, fmt.Errorf("read account credential: %w", err)
	}
	value.Scheme = account.CredentialScheme(scheme)
	value.Configured = true
	var err error
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return account.Credential{}, fmt.Errorf("parse credential created time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return account.Credential{}, fmt.Errorf("parse credential updated time: %w", err)
	}
	return value, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccounts(rows *sql.Rows, capacity int) ([]account.Account, error) {
	defer rows.Close()
	values := make([]account.Account, 0, capacity)
	for rows.Next() {
		value, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read accounts: %w", err)
	}
	return values, nil
}

func scanAccount(row rowScanner) (account.Account, error) {
	var (
		value                                      account.Account
		status                                     string
		isDefault                                  int
		imapCapabilitiesJSON, smtpCapabilitiesJSON string
		lastErrorAt, lastHealthyAt, lastTestedAt   sql.NullString
		createdAt, updatedAt                       string
	)
	if err := row.Scan(
		&value.ID,
		&value.Name,
		&value.Email,
		&value.IMAPUsername,
		&status,
		&isDefault,
		&value.CredentialScheme,
		&imapCapabilitiesJSON,
		&smtpCapabilitiesJSON,
		&value.LastErrorCode,
		&lastErrorAt,
		&lastHealthyAt,
		&lastTestedAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return account.Account{}, account.ErrNotFound
		}
		return account.Account{}, fmt.Errorf("read account: %w", err)
	}
	value.Status = account.Status(status)
	value.Default = isDefault == 1
	if err := json.Unmarshal([]byte(imapCapabilitiesJSON), &value.IMAPCapabilities); err != nil {
		return account.Account{}, fmt.Errorf("parse IMAP capabilities: %w", err)
	}
	if err := json.Unmarshal([]byte(smtpCapabilitiesJSON), &value.SMTPCapabilities); err != nil {
		return account.Account{}, fmt.Errorf("parse SMTP capabilities: %w", err)
	}
	var err error
	value.LastErrorAt, err = parseNullableTime(lastErrorAt)
	if err != nil {
		return account.Account{}, fmt.Errorf("parse account last error time: %w", err)
	}
	value.LastHealthyAt, err = parseNullableTime(lastHealthyAt)
	if err != nil {
		return account.Account{}, fmt.Errorf("parse account last healthy time: %w", err)
	}
	value.LastTestedAt, err = parseNullableTime(lastTestedAt)
	if err != nil {
		return account.Account{}, fmt.Errorf("parse account last tested time: %w", err)
	}
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return account.Account{}, fmt.Errorf("parse account created time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return account.Account{}, fmt.Errorf("parse account updated time: %w", err)
	}
	return value, nil
}

func parseNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func classifyAccountWrite(action string, err error) error {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "unique constraint failed") {
		return fmt.Errorf("%w: %s", account.ErrConflict, action)
	}
	return fmt.Errorf("%s: %w", action, err)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
