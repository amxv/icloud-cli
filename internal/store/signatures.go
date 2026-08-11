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

type SignatureRepository struct{ db *sql.DB }

type signatureCursor struct {
	Version   int    `json:"v"`
	AccountID string `json:"account_id"`
	ID        string `json:"id"`
}

func (repository *SignatureRepository) Count(ctx context.Context, accountID string) (int, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return 0, maildomain.ErrSignatureInvalid
	}
	var count int
	if err := repository.db.QueryRowContext(ctx, `SELECT count(*) FROM mail_signatures WHERE account_id = ?`, accountID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count mail signatures: %w", err)
	}
	return count, nil
}

func (repository *SignatureRepository) Create(ctx context.Context, accountID, signatureID string, input maildomain.SignatureInput, now time.Time) (maildomain.Signature, error) {
	accountID, signatureID = strings.TrimSpace(accountID), strings.TrimSpace(signatureID)
	input, err := maildomain.NormalizeSignatureInput(input)
	if err != nil || accountID == "" || !strings.HasPrefix(signatureID, "sig_") || now.IsZero() {
		if err != nil {
			return maildomain.Signature{}, err
		}
		return maildomain.Signature{}, maildomain.ErrSignatureInvalid
	}
	if err := repository.validateIdentity(ctx, accountID, input.IdentityID); err != nil {
		return maildomain.Signature{}, err
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	var identity any
	if input.IdentityID != "" {
		identity = input.IdentityID
	}
	if _, err := repository.db.ExecContext(ctx, `
INSERT INTO mail_signatures (signature_id, account_id, identity_id, text_body, html_body, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`, signatureID, accountID, identity, input.TextBody, input.HTMLBody, timestamp, timestamp); err != nil {
		return maildomain.Signature{}, fmt.Errorf("create mail signature: %w", err)
	}
	return repository.Resolve(ctx, accountID, signatureID)
}

func (repository *SignatureRepository) Resolve(ctx context.Context, accountID, signatureID string) (maildomain.Signature, error) {
	accountID, signatureID = strings.TrimSpace(accountID), strings.TrimSpace(signatureID)
	if accountID == "" || !strings.HasPrefix(signatureID, "sig_") {
		return maildomain.Signature{}, maildomain.ErrSignatureNotFound
	}
	return scanSignature(repository.db.QueryRowContext(ctx, `
SELECT signature_id, account_id, identity_id, text_body, html_body, created_at, updated_at
FROM mail_signatures WHERE account_id = ? AND signature_id = ?`, accountID, signatureID))
}

func (repository *SignatureRepository) Update(ctx context.Context, accountID, signatureID string, update maildomain.SignatureUpdate, now time.Time) (maildomain.Signature, error) {
	current, err := repository.Resolve(ctx, accountID, signatureID)
	if err != nil {
		return maildomain.Signature{}, err
	}
	input := maildomain.SignatureInput{IdentityID: current.IdentityID, TextBody: current.TextBody, HTMLBody: current.HTMLBody}
	if update.IdentityID != nil {
		input.IdentityID = strings.TrimSpace(*update.IdentityID)
	}
	if update.TextBody != nil {
		input.TextBody = *update.TextBody
	}
	if update.HTMLBody != nil {
		input.HTMLBody = *update.HTMLBody
	}
	input, err = maildomain.NormalizeSignatureInput(input)
	if err != nil {
		return maildomain.Signature{}, err
	}
	if err := repository.validateIdentity(ctx, current.AccountID, input.IdentityID); err != nil {
		return maildomain.Signature{}, err
	}
	if input.IdentityID != "" {
		var invalidDefaults int
		if err := repository.db.QueryRowContext(ctx, `
SELECT count(*) FROM mail_signature_defaults
WHERE account_id = ? AND signature_id = ? AND (identity_id IS NULL OR identity_id <> ?)`, current.AccountID, current.ID, input.IdentityID).Scan(&invalidDefaults); err != nil {
			return maildomain.Signature{}, fmt.Errorf("check signature defaults before identity update: %w", err)
		}
		if invalidDefaults != 0 {
			return maildomain.Signature{}, fmt.Errorf("%w: clear or move defaults before binding this signature to another identity", maildomain.ErrSignatureIdentity)
		}
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	var identity any
	if input.IdentityID != "" {
		identity = input.IdentityID
	}
	if _, err := repository.db.ExecContext(ctx, `
UPDATE mail_signatures
SET identity_id = ?, text_body = ?, html_body = ?, updated_at = ?
WHERE account_id = ? AND signature_id = ?`, identity, input.TextBody, input.HTMLBody, timestamp, current.AccountID, current.ID); err != nil {
		return maildomain.Signature{}, fmt.Errorf("update mail signature: %w", err)
	}
	return repository.Resolve(ctx, current.AccountID, current.ID)
}

func (repository *SignatureRepository) Remove(ctx context.Context, accountID, signatureID string) (maildomain.Signature, error) {
	value, err := repository.Resolve(ctx, accountID, signatureID)
	if err != nil {
		return maildomain.Signature{}, err
	}
	result, err := repository.db.ExecContext(ctx, `DELETE FROM mail_signatures WHERE account_id = ? AND signature_id = ?`, value.AccountID, value.ID)
	if err != nil {
		return maildomain.Signature{}, fmt.Errorf("remove mail signature: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return maildomain.Signature{}, maildomain.ErrSignatureNotFound
	}
	return value, nil
}

func (repository *SignatureRepository) ListPage(ctx context.Context, accountID, cursor string, limit int) (maildomain.SignaturePage, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || limit < 1 || limit > 1000 {
		return maildomain.SignaturePage{}, maildomain.ErrSignatureInvalid
	}
	decoded := signatureCursor{}
	if strings.TrimSpace(cursor) != "" {
		if err := decodeOpaqueCursor("sigcur_", cursor, &decoded); err != nil || decoded.Version != 1 || decoded.AccountID != accountID || !strings.HasPrefix(decoded.ID, "sig_") {
			return maildomain.SignaturePage{}, maildomain.ErrSignatureInvalidCursor
		}
	}
	where, args := `WHERE account_id = ?`, []any{accountID}
	if decoded.ID != "" {
		where += ` AND signature_id > ?`
		args = append(args, decoded.ID)
	}
	args = append(args, limit+1)
	rows, err := repository.db.QueryContext(ctx, `
SELECT signature_id, account_id, identity_id, text_body, html_body, created_at, updated_at
FROM mail_signatures `+where+` ORDER BY signature_id ASC LIMIT ?`, args...)
	if err != nil {
		return maildomain.SignaturePage{}, fmt.Errorf("list mail signatures: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.Signature, 0, limit+1)
	for rows.Next() {
		value, scanErr := scanSignature(rows)
		if scanErr != nil {
			return maildomain.SignaturePage{}, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return maildomain.SignaturePage{}, fmt.Errorf("list mail signatures: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	next := ""
	if more && len(values) > 0 {
		next, err = encodeOpaqueCursor("sigcur_", signatureCursor{Version: 1, AccountID: accountID, ID: values[len(values)-1].ID})
		if err != nil {
			return maildomain.SignaturePage{}, fmt.Errorf("encode mail signature cursor: %w", err)
		}
	}
	return maildomain.SignaturePage{Signatures: values, More: more, NextCursor: next}, nil
}

func (repository *SignatureRepository) SetDefault(ctx context.Context, accountID, identityID string, operation maildomain.OutboundKind, selector string, now time.Time) (maildomain.SignatureDefault, error) {
	accountID, identityID, selector = strings.TrimSpace(accountID), strings.TrimSpace(identityID), strings.TrimSpace(selector)
	operation, err := maildomain.NormalizeSignatureOperation(operation)
	if err != nil || accountID == "" || now.IsZero() {
		if err != nil {
			return maildomain.SignatureDefault{}, err
		}
		return maildomain.SignatureDefault{}, maildomain.ErrSignatureInvalid
	}
	if err := repository.validateIdentity(ctx, accountID, identityID); err != nil {
		return maildomain.SignatureDefault{}, err
	}
	var signatureID any
	if !strings.EqualFold(selector, "none") {
		value, resolveErr := repository.Resolve(ctx, accountID, selector)
		if resolveErr != nil {
			return maildomain.SignatureDefault{}, resolveErr
		}
		if value.IdentityID != "" && value.IdentityID != identityID {
			return maildomain.SignatureDefault{}, fmt.Errorf("%w: identity-bound signature cannot be the default for this scope", maildomain.ErrSignatureIdentity)
		}
		signatureID = value.ID
	} else {
		signatureID = nil
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.SignatureDefault{}, fmt.Errorf("begin signature default update: %w", err)
	}
	defer tx.Rollback()
	if identityID == "" {
		_, err = tx.ExecContext(ctx, `DELETE FROM mail_signature_defaults WHERE account_id = ? AND identity_id IS NULL AND operation = ?`, accountID, operation)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM mail_signature_defaults WHERE account_id = ? AND identity_id = ? AND operation = ?`, accountID, identityID, operation)
	}
	if err != nil {
		return maildomain.SignatureDefault{}, fmt.Errorf("replace signature default: %w", err)
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	var identity any
	if identityID != "" {
		identity = identityID
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO mail_signature_defaults (account_id, identity_id, operation, signature_id, updated_at)
VALUES (?, ?, ?, ?, ?)`, accountID, identity, operation, signatureID, timestamp); err != nil {
		return maildomain.SignatureDefault{}, fmt.Errorf("persist signature default: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return maildomain.SignatureDefault{}, fmt.Errorf("commit signature default: %w", err)
	}
	return repository.defaultAtScope(ctx, accountID, identityID, operation)
}

// Default returns the effective default for a sender identity. An exact
// identity-scoped row wins, including explicit none; otherwise selection falls
// back to the account-wide row. No configured row is equivalent to none.
func (repository *SignatureRepository) Default(ctx context.Context, accountID, identityID string, operation maildomain.OutboundKind) (maildomain.SignatureDefault, error) {
	accountID, identityID = strings.TrimSpace(accountID), strings.TrimSpace(identityID)
	operation, err := maildomain.NormalizeSignatureOperation(operation)
	if err != nil || accountID == "" {
		if err != nil {
			return maildomain.SignatureDefault{}, err
		}
		return maildomain.SignatureDefault{}, maildomain.ErrSignatureInvalid
	}
	if identityID != "" {
		value, scopeErr := repository.defaultAtScope(ctx, accountID, identityID, operation)
		if scopeErr == nil {
			return value, nil
		}
		if !errors.Is(scopeErr, sql.ErrNoRows) {
			return maildomain.SignatureDefault{}, scopeErr
		}
	}
	value, scopeErr := repository.defaultAtScope(ctx, accountID, "", operation)
	if scopeErr == nil {
		return value, nil
	}
	if errors.Is(scopeErr, sql.ErrNoRows) {
		return maildomain.SignatureDefault{AccountID: accountID, IdentityID: identityID, Operation: operation, None: true}, nil
	}
	return maildomain.SignatureDefault{}, scopeErr
}

func (repository *SignatureRepository) ResolveForCompose(ctx context.Context, accountID, identityID string, operation maildomain.OutboundKind, selector string) (*maildomain.Signature, error) {
	selector = strings.TrimSpace(selector)
	if strings.EqualFold(selector, "none") {
		return nil, nil
	}
	if selector != "" {
		value, err := repository.Resolve(ctx, accountID, selector)
		if err != nil {
			return nil, err
		}
		if value.IdentityID != "" && value.IdentityID != strings.TrimSpace(identityID) {
			return nil, maildomain.ErrSignatureIdentity
		}
		return &value, nil
	}
	configured, err := repository.Default(ctx, accountID, identityID, operation)
	if err != nil {
		return nil, err
	}
	if configured.None || configured.SignatureID == "" {
		return nil, nil
	}
	value, err := repository.Resolve(ctx, accountID, configured.SignatureID)
	if err != nil {
		return nil, err
	}
	if value.IdentityID != "" && value.IdentityID != strings.TrimSpace(identityID) {
		return nil, maildomain.ErrSignatureIdentity
	}
	return &value, nil
}

func (repository *SignatureRepository) defaultAtScope(ctx context.Context, accountID, identityID string, operation maildomain.OutboundKind) (maildomain.SignatureDefault, error) {
	var signatureID sql.NullString
	var storedIdentity sql.NullString
	var updatedAt string
	var err error
	if identityID == "" {
		err = repository.db.QueryRowContext(ctx, `
SELECT identity_id, signature_id, updated_at
FROM mail_signature_defaults
WHERE account_id = ? AND identity_id IS NULL AND operation = ?`, accountID, operation).Scan(&storedIdentity, &signatureID, &updatedAt)
	} else {
		err = repository.db.QueryRowContext(ctx, `
SELECT identity_id, signature_id, updated_at
FROM mail_signature_defaults
WHERE account_id = ? AND identity_id = ? AND operation = ?`, accountID, identityID, operation).Scan(&storedIdentity, &signatureID, &updatedAt)
	}
	if err != nil {
		return maildomain.SignatureDefault{}, err
	}
	when, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.SignatureDefault{}, fmt.Errorf("parse signature default update time: %w", err)
	}
	return maildomain.SignatureDefault{
		AccountID: accountID, IdentityID: storedIdentity.String, Operation: operation,
		SignatureID: signatureID.String, None: !signatureID.Valid, UpdatedAt: when,
	}, nil
}

func (repository *SignatureRepository) validateIdentity(ctx context.Context, accountID, identityID string) error {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return nil
	}
	var found string
	if err := repository.db.QueryRowContext(ctx, `SELECT identity_id FROM mail_identities WHERE account_id = ? AND identity_id = ?`, strings.TrimSpace(accountID), identityID).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.ErrSignatureIdentity
		}
		return fmt.Errorf("validate signature identity: %w", err)
	}
	return nil
}

func scanSignature(scanner interface{ Scan(...any) error }) (maildomain.Signature, error) {
	var value maildomain.Signature
	var identity sql.NullString
	var createdAt, updatedAt string
	if err := scanner.Scan(&value.ID, &value.AccountID, &identity, &value.TextBody, &value.HTMLBody, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.Signature{}, maildomain.ErrSignatureNotFound
		}
		return maildomain.Signature{}, fmt.Errorf("read mail signature: %w", err)
	}
	value.IdentityID = identity.String
	var err error
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return maildomain.Signature{}, fmt.Errorf("parse mail signature creation time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.Signature{}, fmt.Errorf("parse mail signature update time: %w", err)
	}
	return value, nil
}
