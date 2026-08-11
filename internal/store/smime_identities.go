package store

import (
	"context"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type SMIMEIdentityRepository struct{ db *sql.DB }

type smimeIdentityCursor struct {
	Version   int    `json:"v"`
	AccountID string `json:"account_id"`
	ID        string `json:"id"`
}

type SMIMEIdentityMaterial struct {
	Identity    maildomain.SMIMEIdentity
	Certificate *x509.Certificate
}

const smimeIdentityColumns = `
SELECT si.smime_identity_id, si.account_id, si.mail_identity_id, mi.email_address,
       si.certificate_id, sc.der, si.private_key_scheme, si.private_key_reference,
       si.created_at, si.updated_at
FROM smime_identities si
JOIN mail_identities mi ON mi.identity_id = si.mail_identity_id AND mi.account_id = si.account_id
JOIN smime_certificates sc ON sc.certificate_id = si.certificate_id AND sc.account_id = si.account_id`

func (repository *SMIMEIdentityRepository) Configure(ctx context.Context, accountID, smimeIdentityID, mailIdentityID, certificateID string, scheme account.CredentialScheme, keyReference string, now time.Time) (maildomain.SMIMEIdentity, error) {
	accountID = strings.TrimSpace(accountID)
	smimeIdentityID = strings.TrimSpace(smimeIdentityID)
	mailIdentityID = strings.TrimSpace(mailIdentityID)
	certificateID = strings.TrimSpace(certificateID)
	keyReference = strings.TrimSpace(keyReference)
	if accountID == "" || !strings.HasPrefix(smimeIdentityID, "smi_") || !strings.HasPrefix(mailIdentityID, "mid_") || !strings.HasPrefix(certificateID, "smc_") || keyReference == "" || now.IsZero() {
		return maildomain.SMIMEIdentity{}, maildomain.ErrSMIMEIdentityInvalid
	}
	switch scheme {
	case account.CredentialKeyring, account.CredentialEnv, account.CredentialFile:
	default:
		return maildomain.SMIMEIdentity{}, maildomain.ErrSMIMEIdentityInvalid
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return maildomain.SMIMEIdentity{}, fmt.Errorf("begin S/MIME identity configuration: %w", err)
	}
	defer tx.Rollback()
	var email, certEmail string
	if err := tx.QueryRowContext(ctx, `SELECT email_address FROM mail_identities WHERE account_id = ? AND identity_id = ?`, accountID, mailIdentityID).Scan(&email); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.SMIMEIdentity{}, maildomain.ErrSMIMEIdentityNotFound
		}
		return maildomain.SMIMEIdentity{}, fmt.Errorf("read mail identity for S/MIME: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT email FROM smime_certificates WHERE account_id = ? AND certificate_id = ?`, accountID, certificateID).Scan(&certEmail); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.SMIMEIdentity{}, maildomain.ErrSMIMECertificateNotFound
		}
		return maildomain.SMIMEIdentity{}, fmt.Errorf("read S/MIME identity certificate: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(email), strings.TrimSpace(certEmail)) {
		return maildomain.SMIMEIdentity{}, maildomain.ErrSMIMEIdentityInvalid
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM smime_identities WHERE account_id = ?`, accountID).Scan(&count); err != nil {
		return maildomain.SMIMEIdentity{}, fmt.Errorf("count S/MIME identities: %w", err)
	}
	var existingID string
	existingErr := tx.QueryRowContext(ctx, `SELECT smime_identity_id FROM smime_identities WHERE account_id = ? AND mail_identity_id = ?`, accountID, mailIdentityID).Scan(&existingID)
	if existingErr != nil && !errors.Is(existingErr, sql.ErrNoRows) {
		return maildomain.SMIMEIdentity{}, fmt.Errorf("read existing S/MIME identity: %w", existingErr)
	}
	if errors.Is(existingErr, sql.ErrNoRows) && count >= maildomain.MaxSMIMEIdentitiesPerAccount {
		return maildomain.SMIMEIdentity{}, maildomain.ErrSMIMEIdentityLimit
	}
	if existingID != "" {
		smimeIdentityID = existingID
	}
	var otherID string
	otherErr := tx.QueryRowContext(ctx, `SELECT smime_identity_id FROM smime_identities WHERE account_id = ? AND certificate_id = ? AND mail_identity_id <> ?`, accountID, certificateID, mailIdentityID).Scan(&otherID)
	if otherErr == nil {
		return maildomain.SMIMEIdentity{}, maildomain.ErrSMIMEIdentityConflict
	}
	if !errors.Is(otherErr, sql.ErrNoRows) {
		return maildomain.SMIMEIdentity{}, fmt.Errorf("check S/MIME certificate ownership: %w", otherErr)
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `
INSERT INTO smime_identities (
    smime_identity_id, account_id, mail_identity_id, certificate_id,
    private_key_scheme, private_key_reference, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(account_id, mail_identity_id) DO UPDATE SET
    certificate_id = excluded.certificate_id,
    private_key_scheme = excluded.private_key_scheme,
    private_key_reference = excluded.private_key_reference,
    updated_at = excluded.updated_at`,
		smimeIdentityID, accountID, mailIdentityID, certificateID, string(scheme), keyReference, timestamp, timestamp,
	)
	if err != nil {
		return maildomain.SMIMEIdentity{}, classifySMIMEIdentityWrite(err)
	}
	value, err := scanSMIMEIdentity(tx.QueryRowContext(ctx, smimeIdentityColumns+` WHERE si.account_id = ? AND si.mail_identity_id = ?`, accountID, mailIdentityID))
	if err != nil {
		return maildomain.SMIMEIdentity{}, err
	}
	if err := tx.Commit(); err != nil {
		return maildomain.SMIMEIdentity{}, fmt.Errorf("commit S/MIME identity configuration: %w", err)
	}
	return value, nil
}

func (repository *SMIMEIdentityRepository) Resolve(ctx context.Context, accountID, selector string) (maildomain.SMIMEIdentity, error) {
	accountID = strings.TrimSpace(accountID)
	selector = strings.TrimSpace(selector)
	if accountID == "" || selector == "" {
		return maildomain.SMIMEIdentity{}, maildomain.ErrSMIMEIdentityNotFound
	}
	return scanSMIMEIdentity(repository.db.QueryRowContext(ctx, smimeIdentityColumns+`
WHERE si.account_id = ? AND (
    si.smime_identity_id = ? OR si.mail_identity_id = ? OR lower(mi.email_address) = lower(?)
)`, accountID, selector, selector, selector))
}

func (repository *SMIMEIdentityRepository) Remove(ctx context.Context, accountID, selector string) (maildomain.SMIMEIdentity, error) {
	value, err := repository.Resolve(ctx, accountID, selector)
	if err != nil {
		return maildomain.SMIMEIdentity{}, err
	}
	result, err := repository.db.ExecContext(ctx, `DELETE FROM smime_identities WHERE account_id = ? AND smime_identity_id = ?`, accountID, value.ID)
	if err != nil {
		return maildomain.SMIMEIdentity{}, fmt.Errorf("remove S/MIME identity: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return maildomain.SMIMEIdentity{}, maildomain.ErrSMIMEIdentityNotFound
	}
	return value, nil
}

func (repository *SMIMEIdentityRepository) ListPage(ctx context.Context, accountID, cursor string, limit int) (maildomain.SMIMEIdentityPage, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || limit < 1 || limit > maildomain.MaxSMIMEIdentitiesPerAccount {
		return maildomain.SMIMEIdentityPage{}, maildomain.ErrSMIMEIdentityInvalid
	}
	decoded := smimeIdentityCursor{}
	if strings.TrimSpace(cursor) != "" {
		if err := decodeOpaqueCursor("smicur_", cursor, &decoded); err != nil || decoded.Version != 1 || decoded.AccountID != accountID || !strings.HasPrefix(decoded.ID, "smi_") {
			return maildomain.SMIMEIdentityPage{}, maildomain.ErrSMIMEIdentityCursor
		}
	}
	where := ` WHERE si.account_id = ?`
	args := []any{accountID}
	if decoded.ID != "" {
		where += ` AND si.smime_identity_id > ?`
		args = append(args, decoded.ID)
	}
	args = append(args, limit+1)
	rows, err := repository.db.QueryContext(ctx, smimeIdentityColumns+where+` ORDER BY si.smime_identity_id LIMIT ?`, args...)
	if err != nil {
		return maildomain.SMIMEIdentityPage{}, fmt.Errorf("list S/MIME identities: %w", err)
	}
	defer rows.Close()
	values := make([]maildomain.SMIMEIdentity, 0, limit+1)
	for rows.Next() {
		value, err := scanSMIMEIdentity(rows)
		if err != nil {
			return maildomain.SMIMEIdentityPage{}, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return maildomain.SMIMEIdentityPage{}, fmt.Errorf("iterate S/MIME identities: %w", err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	next := ""
	if more && len(values) > 0 {
		next, err = encodeOpaqueCursor("smicur_", smimeIdentityCursor{Version: 1, AccountID: accountID, ID: values[len(values)-1].ID})
		if err != nil {
			return maildomain.SMIMEIdentityPage{}, fmt.Errorf("encode S/MIME identity cursor: %w", err)
		}
	}
	return maildomain.SMIMEIdentityPage{Identities: values, More: more, NextCursor: next}, nil
}

func (repository *SMIMEIdentityRepository) Materials(ctx context.Context, accountID string) ([]SMIMEIdentityMaterial, error) {
	page, err := repository.ListPage(ctx, accountID, "", maildomain.MaxSMIMEIdentitiesPerAccount)
	if err != nil {
		return nil, err
	}
	if page.More {
		return nil, maildomain.ErrSMIMEIdentityLimit
	}
	values := make([]SMIMEIdentityMaterial, 0, len(page.Identities))
	for _, value := range page.Identities {
		var enabled int
		if err := repository.db.QueryRowContext(ctx, `SELECT enabled FROM mail_identities WHERE account_id = ? AND identity_id = ?`, accountID, value.MailIdentityID).Scan(&enabled); err != nil {
			return nil, fmt.Errorf("read mail identity availability for S/MIME decryption: %w", err)
		}
		if enabled != 1 {
			continue
		}
		certValue, err := repository.certificateDER(ctx, accountID, value.CertificateID)
		if err != nil {
			return nil, err
		}
		cert, err := x509.ParseCertificate(certValue)
		if err != nil {
			return nil, fmt.Errorf("parse S/MIME identity certificate: %w", err)
		}
		values = append(values, SMIMEIdentityMaterial{Identity: value, Certificate: cert})
	}
	return values, nil
}

func (repository *SMIMEIdentityRepository) certificateDER(ctx context.Context, accountID, certificateID string) ([]byte, error) {
	var der []byte
	if err := repository.db.QueryRowContext(ctx, `SELECT der FROM smime_certificates WHERE account_id = ? AND certificate_id = ?`, accountID, certificateID).Scan(&der); err != nil {
		return nil, fmt.Errorf("read S/MIME identity certificate: %w", err)
	}
	return der, nil
}

func scanSMIMEIdentity(scanner interface{ Scan(...any) error }) (maildomain.SMIMEIdentity, error) {
	var value maildomain.SMIMEIdentity
	var scheme, createdAt, updatedAt string
	var der []byte
	if err := scanner.Scan(
		&value.ID, &value.AccountID, &value.MailIdentityID, &value.Email,
		&value.CertificateID, &der, &scheme, &value.PrivateKeyReference,
		&createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return maildomain.SMIMEIdentity{}, maildomain.ErrSMIMEIdentityNotFound
		}
		return maildomain.SMIMEIdentity{}, fmt.Errorf("read S/MIME identity: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return maildomain.SMIMEIdentity{}, fmt.Errorf("parse S/MIME identity certificate: %w", err)
	}
	metadata := maildomain.SMIMECertificateFromX509(cert)
	value.FingerprintSHA256 = metadata.FingerprintSHA256
	value.Subject = metadata.Subject
	value.NotBefore = metadata.NotBefore
	value.NotAfter = metadata.NotAfter
	value.KeyUsage = metadata.KeyUsage
	value.ExtendedKeyUsage = metadata.ExtendedKeyUsage
	value.PrivateKeyScheme = account.CredentialScheme(scheme)
	value.PrivateKeyConfigured = strings.TrimSpace(value.PrivateKeyReference) != ""
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return maildomain.SMIMEIdentity{}, fmt.Errorf("parse S/MIME identity created time: %w", err)
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return maildomain.SMIMEIdentity{}, fmt.Errorf("parse S/MIME identity updated time: %w", err)
	}
	return value, nil
}

func classifySMIMEIdentityWrite(err error) error {
	if err == nil {
		return nil
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "unique constraint") || strings.Contains(text, "foreign key constraint") {
		return maildomain.ErrSMIMEIdentityConflict
	}
	return fmt.Errorf("write S/MIME identity: %w", err)
}
