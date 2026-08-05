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

type AccountRepository struct {
	db *sql.DB
}

func (r *AccountRepository) List(ctx context.Context, limit int) ([]account.Account, bool, error) {
	if limit < 1 {
		return nil, false, fmt.Errorf("list accounts: limit must be positive")
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT a.account_id, a.friendly_name, a.email_address, a.imap_username,
       a.status, a.is_default, COALESCE(c.scheme, ''), a.created_at, a.updated_at
FROM accounts AS a
LEFT JOIN credential_refs AS c ON c.account_id = a.account_id
ORDER BY a.is_default DESC, lower(a.friendly_name), a.account_id
LIMIT ?`, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()

	accounts := make([]account.Account, 0, limit)
	for rows.Next() {
		value, err := scanAccount(rows)
		if err != nil {
			return nil, false, err
		}
		accounts = append(accounts, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list accounts: %w", err)
	}
	more := len(accounts) > limit
	if more {
		accounts = accounts[:limit]
	}
	return accounts, more, nil
}

func (r *AccountRepository) Resolve(ctx context.Context, selector string) (account.Account, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return account.Account{}, account.ErrNotFound
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT a.account_id, a.friendly_name, a.email_address, a.imap_username,
       a.status, a.is_default, COALESCE(c.scheme, ''), a.created_at, a.updated_at
FROM accounts AS a
LEFT JOIN credential_refs AS c ON c.account_id = a.account_id
WHERE a.account_id = ? OR lower(a.friendly_name) = lower(?) OR lower(a.email_address) = lower(?)
ORDER BY a.account_id
LIMIT 2`, selector, selector, selector)
	if err != nil {
		return account.Account{}, fmt.Errorf("resolve account: %w", err)
	}
	defer rows.Close()

	matches := make([]account.Account, 0, 2)
	for rows.Next() {
		value, err := scanAccount(rows)
		if err != nil {
			return account.Account{}, err
		}
		matches = append(matches, value)
	}
	if err := rows.Err(); err != nil {
		return account.Account{}, fmt.Errorf("resolve account: %w", err)
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

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(row rowScanner) (account.Account, error) {
	var (
		value                account.Account
		status               string
		isDefault            int
		createdAt, updatedAt string
	)
	if err := row.Scan(
		&value.ID,
		&value.Name,
		&value.Email,
		&value.IMAPUsername,
		&status,
		&isDefault,
		&value.CredentialScheme,
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
	var err error
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
