package store

import (
	"context"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type SMIMECertificateRepository struct{ db *sql.DB }

type smimeCertificateCursor struct {
	Version   int    `json:"v"`
	AccountID string `json:"account_id"`
	Email     string `json:"email"`
	ID        string `json:"id"`
}

func (repository *SMIMECertificateRepository) Import(ctx context.Context, accountID, certificateID, email string, cert *x509.Certificate, trusted bool, now time.Time) (maildomain.SMIMECertificate, error) {
	accountID, certificateID = strings.TrimSpace(accountID), strings.TrimSpace(certificateID)
	email = strings.ToLower(strings.TrimSpace(email))
	if accountID == "" || !strings.HasPrefix(certificateID, "smc_") || email == "" || !strings.Contains(email, "@") || len(email) > 320 || cert == nil || now.IsZero() {
		return maildomain.SMIMECertificate{}, maildomain.ErrSMIMECertificateInvalid
	}
	metadata := maildomain.SMIMECertificateFromX509(cert)
	if len(metadata.DER) == 0 || len(metadata.DER) > 64<<10 || len(metadata.FingerprintSHA256) != 64 {
		return maildomain.SMIMECertificate{}, maildomain.ErrSMIMECertificateInvalid
	}
	if !cert.IsCA && !certificateMatchesEmail(metadata.Emails, email) {
		return maildomain.SMIMECertificate{}, fmt.Errorf("%w: certificate identity does not match scoped email", maildomain.ErrSMIMECertificateInvalid)
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.SMIMECertificate{}, fmt.Errorf("begin S/MIME certificate import: %w", err)
	}
	defer tx.Rollback()
	var existingID string
	existingErr := tx.QueryRowContext(ctx, `
SELECT certificate_id FROM smime_certificates
WHERE account_id = ? AND email = ? AND fingerprint_sha256 = ?`, accountID, email, metadata.FingerprintSHA256).Scan(&existingID)
	if existingErr != nil && !errors.Is(existingErr, sql.ErrNoRows) {
		return maildomain.SMIMECertificate{}, fmt.Errorf("check existing S/MIME certificate: %w", existingErr)
	}
	if errors.Is(existingErr, sql.ErrNoRows) {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM smime_certificates WHERE account_id = ?`, accountID).Scan(&count); err != nil {
			return maildomain.SMIMECertificate{}, fmt.Errorf("count S/MIME certificates: %w", err)
		}
		if count >= maildomain.MaxSMIMECertificatesPerAccount {
			return maildomain.SMIMECertificate{}, maildomain.ErrSMIMECertificateLimit
		}
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO smime_certificates (
    certificate_id, account_id, email, fingerprint_sha256, der, trusted, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(account_id, email, fingerprint_sha256) DO UPDATE SET
    trusted = excluded.trusted,
    updated_at = excluded.updated_at`,
		certificateID, accountID, email, metadata.FingerprintSHA256, metadata.DER, boolInt(trusted), timestamp, timestamp,
	)
	if err != nil {
		return maildomain.SMIMECertificate{}, fmt.Errorf("import S/MIME certificate: %w", err)
	}
	if err := bumpSMIMEPolicyRevision(ctx, tx, accountID, now); err != nil {
		return maildomain.SMIMECertificate{}, err
	}
	value, err := scanSMIMECertificate(tx.QueryRowContext(ctx, `
SELECT certificate_id, account_id, email, der, trusted, created_at, updated_at
FROM smime_certificates WHERE account_id = ? AND email = ? AND fingerprint_sha256 = ?`, accountID, email, metadata.FingerprintSHA256))
	if err != nil {
		return maildomain.SMIMECertificate{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.SMIMECertificate{}, fmt.Errorf("commit S/MIME certificate import: %w", err)
	}
	return value, nil
}

func (repository *SMIMECertificateRepository) Resolve(ctx context.Context, accountID, certificateID string) (maildomain.SMIMECertificate, error) {
	accountID, certificateID = strings.TrimSpace(accountID), strings.TrimSpace(certificateID)
	if accountID == "" || !strings.HasPrefix(certificateID, "smc_") {
		return maildomain.SMIMECertificate{}, maildomain.ErrSMIMECertificateNotFound
	}
	return scanSMIMECertificate(repository.db.QueryRowContext(ctx, `
SELECT certificate_id, account_id, email, der, trusted, created_at, updated_at
FROM smime_certificates WHERE account_id = ? AND certificate_id = ?`, accountID, certificateID))
}

func (repository *SMIMECertificateRepository) ResolveByFingerprint(ctx context.Context, accountID, email, fingerprint string) (maildomain.SMIMECertificate, error) {
	accountID = strings.TrimSpace(accountID)
	email = strings.ToLower(strings.TrimSpace(email))
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if accountID == "" || email == "" || len(fingerprint) != 64 {
		return maildomain.SMIMECertificate{}, maildomain.ErrSMIMECertificateNotFound
	}
	return scanSMIMECertificate(repository.db.QueryRowContext(ctx, `
SELECT certificate_id, account_id, email, der, trusted, created_at, updated_at
FROM smime_certificates WHERE account_id = ? AND email = ? AND fingerprint_sha256 = ?`, accountID, email, fingerprint))
}

func (repository *SMIMECertificateRepository) Remove(ctx context.Context, accountID, certificateID string, now time.Time) (maildomain.SMIMECertificate, error) {
	accountID, certificateID = strings.TrimSpace(accountID), strings.TrimSpace(certificateID)
	if accountID == "" || !strings.HasPrefix(certificateID, "smc_") || now.IsZero() {
		return maildomain.SMIMECertificate{}, maildomain.ErrSMIMECertificateNotFound
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.SMIMECertificate{}, fmt.Errorf("begin S/MIME certificate removal: %w", err)
	}
	defer tx.Rollback()
	value, err := scanSMIMECertificate(tx.QueryRowContext(ctx, `
SELECT certificate_id, account_id, email, der, trusted, created_at, updated_at
FROM smime_certificates WHERE account_id = ? AND certificate_id = ?`, accountID, certificateID))
	if err != nil {
		return maildomain.SMIMECertificate{}, err
	}
	var identityCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM smime_identities WHERE account_id = ? AND certificate_id = ?`, accountID, certificateID).Scan(&identityCount); err != nil {
		return maildomain.SMIMECertificate{}, fmt.Errorf("check S/MIME certificate identity use: %w", err)
	}
	if identityCount != 0 {
		return maildomain.SMIMECertificate{}, maildomain.ErrSMIMEIdentityConflict
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM smime_certificates WHERE account_id = ? AND certificate_id = ?`, value.AccountID, value.ID)
	if err != nil {
		return maildomain.SMIMECertificate{}, fmt.Errorf("remove S/MIME certificate: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return maildomain.SMIMECertificate{}, maildomain.ErrSMIMECertificateNotFound
	}
	if err := bumpSMIMEPolicyRevision(ctx, tx, accountID, now); err != nil {
		return maildomain.SMIMECertificate{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.SMIMECertificate{}, fmt.Errorf("commit S/MIME certificate removal: %w", err)
	}
	return value, nil
}

func (repository *SMIMECertificateRepository) ListPage(ctx context.Context, accountID, email, cursor string, limit int) (maildomain.SMIMECertificatePage, error) {
	accountID = strings.TrimSpace(accountID)
	email = strings.ToLower(strings.TrimSpace(email))
	if accountID == "" || limit < 1 || limit > 1000 {
		return maildomain.SMIMECertificatePage{}, maildomain.ErrSMIMECertificateInvalid
	}
	decoded := smimeCertificateCursor{}
	if strings.TrimSpace(cursor) != "" {
		if err := decodeOpaqueCursor("smcur_", cursor, &decoded); err != nil || decoded.Version != 1 || decoded.AccountID != accountID || decoded.Email != email || !strings.HasPrefix(decoded.ID, "smc_") {
			return maildomain.SMIMECertificatePage{}, maildomain.ErrSMIMEInvalidCursor
		}
	}
	where := `WHERE account_id = ?`
	args := []any{accountID}
	if email != "" {
		where += ` AND email = ?`
		args = append(args, email)
	}
	if decoded.ID != "" {
		where += ` AND certificate_id > ?`
		args = append(args, decoded.ID)
	}
	args = append(args, limit+1)
	rows, err := repository.db.QueryContext(ctx, `
SELECT certificate_id, account_id, email, der, trusted, created_at, updated_at
FROM smime_certificates `+where+` ORDER BY certificate_id ASC LIMIT ?`, args...)
	if err != nil {
		return maildomain.SMIMECertificatePage{}, fmt.Errorf("list S/MIME certificates: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.SMIMECertificate, 0, limit+1)
	for rows.Next() {
		value, scanErr := scanSMIMECertificate(rows)
		if scanErr != nil {
			return maildomain.SMIMECertificatePage{}, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return maildomain.SMIMECertificatePage{}, fmt.Errorf("list S/MIME certificates: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	next := ""
	if more && len(values) > 0 {
		next, err = encodeOpaqueCursor("smcur_", smimeCertificateCursor{Version: 1, AccountID: accountID, Email: email, ID: values[len(values)-1].ID})
		if err != nil {
			return maildomain.SMIMECertificatePage{}, fmt.Errorf("encode S/MIME certificate cursor: %w", err)
		}
	}
	return maildomain.SMIMECertificatePage{Certificates: values, More: more, NextCursor: next}, nil
}

func (repository *SMIMECertificateRepository) Materials(ctx context.Context, accountID string) ([]maildomain.SMIMECertificateMaterial, error) {
	page, err := repository.ListPage(ctx, accountID, "", "", maildomain.MaxSMIMECertificatesPerAccount)
	if err != nil {
		return nil, err
	}
	if page.More {
		return nil, maildomain.ErrSMIMECertificateLimit
	}
	materials := make([]maildomain.SMIMECertificateMaterial, 0, len(page.Certificates))
	for _, value := range page.Certificates {
		cert, err := x509.ParseCertificate(value.DER)
		if err != nil {
			return nil, fmt.Errorf("parse stored S/MIME certificate %s: %w", value.ID, err)
		}
		materials = append(materials, maildomain.SMIMECertificateMaterial{ID: value.ID, Email: value.Email, Trusted: value.Trusted, Certificate: cert})
	}
	return materials, nil
}

func (repository *SMIMECertificateRepository) PolicyRevision(ctx context.Context, accountID string) (int64, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return 0, maildomain.ErrSMIMECertificateInvalid
	}
	var revision int64
	err := repository.db.QueryRowContext(ctx, `SELECT revision FROM smime_policy_state WHERE account_id = ?`, accountID).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read S/MIME policy revision: %w", err)
	}
	return revision, nil
}

type smimeExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func bumpSMIMEPolicyRevision(ctx context.Context, execer smimeExecer, accountID string, now time.Time) error {
	if _, err := execer.ExecContext(ctx, `
INSERT INTO smime_policy_state (account_id, revision, updated_at)
VALUES (?, 1, ?)
ON CONFLICT(account_id) DO UPDATE SET
    revision = smime_policy_state.revision + 1,
    updated_at = excluded.updated_at`, accountID, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("advance S/MIME policy revision: %w", err)
	}
	return nil
}

func scanSMIMECertificate(scanner interface{ Scan(...any) error }) (maildomain.SMIMECertificate, error) {
	var value maildomain.SMIMECertificate
	var der []byte
	var trusted int
	var createdAt, updatedAt string
	if err := scanner.Scan(&value.ID, &value.AccountID, &value.Email, &der, &trusted, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.SMIMECertificate{}, maildomain.ErrSMIMECertificateNotFound
		}
		return maildomain.SMIMECertificate{}, fmt.Errorf("read S/MIME certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return maildomain.SMIMECertificate{}, fmt.Errorf("parse stored S/MIME certificate: %w", err)
	}
	metadata := maildomain.SMIMECertificateFromX509(cert)
	metadata.ID = value.ID
	metadata.AccountID = value.AccountID
	metadata.Email = value.Email
	metadata.Trusted = trusted == 1
	metadata.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return maildomain.SMIMECertificate{}, fmt.Errorf("parse S/MIME certificate creation time: %w", err)
	}
	metadata.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.SMIMECertificate{}, fmt.Errorf("parse S/MIME certificate update time: %w", err)
	}
	return metadata, nil
}

func certificateMatchesEmail(values []string, email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == email {
			return true
		}
	}
	return false
}
