package app

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/secret"
)

func (commands accountCommands) MailSMIMEIdentitiesList(ctx context.Context, home, accountSelector, cursor string, limit int) (account.Account, maildomain.SMIMEIdentityPage, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentityPage{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentityPage{}, state.SchemaVersion(), mapAccountError(err)
	}
	page, err := state.SMIMEIdentities().ListPage(ctx, accountValue.ID, cursor, limit)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentityPage{}, state.SchemaVersion(), mapSMIMEIdentityError(err)
	}
	return accountValue, page, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSMIMEIdentityShow(ctx context.Context, home, accountSelector, selector string) (account.Account, maildomain.SMIMEIdentity, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapAccountError(err)
	}
	value, err := state.SMIMEIdentities().Resolve(ctx, accountValue.ID, selector)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapSMIMEIdentityError(err)
	}
	return accountValue, value, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSMIMEIdentityConfigure(ctx context.Context, home, accountSelector, mailIdentitySelector, certificateID string, setup account.CredentialSetup) (account.Account, maildomain.SMIMEIdentity, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, 0, err
	}
	defer state.Close()
	defer unlock()
	defer zeroBytes(setup.Secret)
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapAccountError(err)
	}
	mailIdentity, err := state.Identities().Resolve(ctx, accountValue.ID, mailIdentitySelector)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapMailIdentityError(err)
	}
	certificate, err := state.SMIMECertificates().Resolve(ctx, accountValue.ID, certificateID)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapSMIMECertificateError(err)
	}
	cert, err := x509.ParseCertificate(certificate.DER)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "stored S/MIME certificate is invalid", false, err)
	}
	if cert.IsCA || !strings.EqualFold(certificate.Email, mailIdentity.Address) || !smimeCertificateAllowsEmailProtection(cert) {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorUsage, "S/MIME identity certificate must be an end-entity EmailProtection certificate scoped to the selected mail identity", false)
	}

	existing, existingErr := state.SMIMEIdentities().Resolve(ctx, accountValue.ID, mailIdentity.ID)
	if existingErr != nil && !errors.Is(existingErr, maildomain.ErrSMIMEIdentityNotFound) {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapSMIMEIdentityError(existingErr)
	}
	smimeIdentityID := existing.ID
	if smimeIdentityID == "" {
		smimeIdentityID, err = commands.generateID("smi")
		if err != nil {
			return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate an S/MIME identity ID", false, err)
		}
	}
	scheme, reference, err := normalizeSMIMEPrivateKeySetup(paths.Root, smimeIdentityID, setup)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapSMIMEPrivateKeyError(err)
	}
	manager := secret.NewManager(commands.keyring)
	keyBytes, err := resolveSMIMEPrivateKeySetup(ctx, manager, scheme, reference, setup.Secret)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapSMIMEPrivateKeyError(err)
	}
	defer zeroBytes(keyBytes)
	privateKey, err := maildomain.ParseSMIMEPrivateKey(keyBytes)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapSMIMEPrivateKeyError(err)
	}
	if !maildomain.SMIMEPrivateKeyMatchesCertificate(privateKey, cert) {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapSMIMEPrivateKeyError(maildomain.ErrSMIMEPrivateKeyMismatch)
	}

	oldCredential := account.Credential{}
	if existing.ID != "" {
		oldCredential = account.Credential{AccountID: accountValue.ID, Scheme: existing.PrivateKeyScheme, Reference: existing.PrivateKeyReference, Configured: existing.PrivateKeyConfigured}
	}
	backup, hadOldKeyring, err := backupKeyringCredential(ctx, manager, oldCredential)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapSMIMEPrivateKeyError(err)
	}
	defer zeroBytes(backup)
	oldDeleted := false
	if oldCredential.Scheme == account.CredentialKeyring && oldCredential.Reference != "" && oldCredential.Reference != reference {
		if err := manager.DeleteKeyring(ctx, oldCredential.Reference); err != nil {
			return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapSMIMEPrivateKeyError(err)
		}
		oldDeleted = true
	}
	newStored := false
	if scheme == account.CredentialKeyring {
		if err := manager.StoreKeyring(ctx, reference, setup.Secret); err != nil {
			if oldDeleted {
				_ = restoreKeyringCredential(ctx, manager, oldCredential, backup, hadOldKeyring)
			}
			return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapSMIMEPrivateKeyError(err)
		}
		newStored = true
	}
	value, err := state.SMIMEIdentities().Configure(ctx, accountValue.ID, smimeIdentityID, mailIdentity.ID, certificate.ID, scheme, reference, commands.timeNow())
	if err != nil {
		if rollbackErr := rollbackCredentialKeyrings(ctx, manager, oldCredential, backup, hadOldKeyring, reference, newStored); rollbackErr != nil {
			return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "S/MIME identity configuration failed and keyring rollback did not complete", false, rollbackErr)
		}
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapSMIMEIdentityError(err)
	}
	return accountValue, value, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSMIMEIdentityRemove(ctx context.Context, home, accountSelector, selector string) (account.Account, maildomain.SMIMEIdentity, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapAccountError(err)
	}
	value, err := state.SMIMEIdentities().Resolve(ctx, accountValue.ID, selector)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapSMIMEIdentityError(err)
	}
	manager := secret.NewManager(commands.keyring)
	credential := account.Credential{AccountID: accountValue.ID, Scheme: value.PrivateKeyScheme, Reference: value.PrivateKeyReference, Configured: value.PrivateKeyConfigured}
	backup, hadKeyring, err := removeKeyringCredential(ctx, manager, credential)
	if err != nil {
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapSMIMEPrivateKeyError(err)
	}
	defer zeroBytes(backup)
	removed, err := state.SMIMEIdentities().Remove(ctx, accountValue.ID, value.ID)
	if err != nil {
		if restoreErr := restoreKeyringCredential(ctx, manager, credential, backup, hadKeyring); restoreErr != nil {
			return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "S/MIME identity removal failed and native keyring restoration did not complete", false, restoreErr)
		}
		return account.Account{}, maildomain.SMIMEIdentity{}, state.SchemaVersion(), mapSMIMEIdentityError(err)
	}
	return accountValue, removed, state.SchemaVersion(), nil
}

func normalizeSMIMEPrivateKeySetup(root, smimeIdentityID string, setup account.CredentialSetup) (account.CredentialScheme, string, error) {
	switch setup.Scheme {
	case account.CredentialKeyring:
		if len(setup.Secret) == 0 {
			return "", "", secret.ErrMissing
		}
		reference, err := secret.KeyringReference(root, smimeIdentityID)
		return setup.Scheme, reference, err
	case account.CredentialEnv:
		reference, err := secret.CanonicalReference(setup.Scheme, setup.Reference)
		return setup.Scheme, reference, err
	case account.CredentialFile:
		reference := strings.TrimSpace(setup.Reference)
		reference = strings.TrimPrefix(reference, "file://")
		reference = strings.TrimPrefix(reference, "file:")
		if reference == "" {
			return "", "", secret.ErrInvalidReference
		}
		absolute, err := filepath.Abs(filepath.Clean(reference))
		if err != nil {
			return "", "", secret.ErrInvalidReference
		}
		if err := validateSMIMEPrivateKeyFile(absolute); err != nil {
			return "", "", err
		}
		canonical, err := secret.CanonicalReference(setup.Scheme, absolute)
		return setup.Scheme, canonical, err
	default:
		return "", "", secret.ErrInvalidReference
	}
}

func resolveSMIMEPrivateKeySetup(ctx context.Context, manager secret.Manager, scheme account.CredentialScheme, reference string, inline []byte) ([]byte, error) {
	if scheme == account.CredentialKeyring {
		if len(inline) == 0 {
			return nil, secret.ErrMissing
		}
		return append([]byte(nil), inline...), nil
	}
	if scheme == account.CredentialFile {
		path := strings.TrimPrefix(reference, "file:")
		return readSMIMEPrivateKeyFile(ctx, path)
	}
	return manager.Resolve(ctx, account.Credential{Scheme: scheme, Reference: reference, Configured: true})
}

func validateSMIMEPrivateKeyFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%w: private key file cannot be read", secret.ErrMissing)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() < 1 || info.Size() > secret.MaxSecretBytes {
		return fmt.Errorf("%w: private key file must be a private regular non-symlink file", secret.ErrInvalidReference)
	}
	return nil
}

func readSMIMEPrivateKeyFile(ctx context.Context, path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: private key file cannot be read", secret.ErrMissing)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() || before.Mode().Perm()&0o077 != 0 || before.Size() < 1 || before.Size() > secret.MaxSecretBytes {
		return nil, fmt.Errorf("%w: private key file must be a private regular non-symlink file", secret.ErrInvalidReference)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: private key file cannot be opened", secret.ErrMissing)
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || after.Mode().Perm()&0o077 != 0 || after.Size() != before.Size() || !os.SameFile(before, after) {
		return nil, fmt.Errorf("%w: private key file changed while being opened", secret.ErrInvalidReference)
	}
	value, err := io.ReadAll(io.LimitReader(&appContextReader{ctx: ctx, reader: file}, secret.MaxSecretBytes+1))
	if err != nil {
		zeroBytes(value)
		return nil, err
	}
	if len(value) == 0 || len(value) > secret.MaxSecretBytes {
		zeroBytes(value)
		return nil, secret.ErrInvalidReference
	}
	return value, nil
}

func smimeCertificateAllowsEmailProtection(cert *x509.Certificate) bool {
	if cert == nil || len(cert.ExtKeyUsage) == 0 {
		return true
	}
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageAny || usage == x509.ExtKeyUsageEmailProtection {
			return true
		}
	}
	return false
}

func mapSMIMEIdentityError(err error) error {
	switch {
	case errors.Is(err, maildomain.ErrSMIMEIdentityNotFound):
		return cli.WrapAppError(cli.ErrorNotFound, "S/MIME identity was not found", false, err)
	case errors.Is(err, maildomain.ErrSMIMEIdentityConflict):
		return cli.WrapAppError(cli.ErrorConflict, "S/MIME identity or certificate is already bound to another mail identity", false, err)
	case errors.Is(err, maildomain.ErrSMIMEIdentityLimit):
		return cli.WrapAppError(cli.ErrorConflict, "S/MIME identity inventory reached its bounded account limit", false, err)
	case errors.Is(err, maildomain.ErrSMIMEIdentityInvalid), errors.Is(err, maildomain.ErrSMIMEIdentityCursor):
		return cli.WrapAppError(cli.ErrorUsage, "S/MIME identity request is invalid", false, err)
	default:
		return cli.WrapAppError(cli.ErrorInternal, "cannot resolve local S/MIME identity state", false, err)
	}
}

func mapSMIMEPrivateKeyError(err error) error {
	switch {
	case errors.Is(err, maildomain.ErrSMIMEPrivateKeyEncrypted):
		return cli.WrapAppError(cli.ErrorUsage, "password-protected private key input is not supported; use a protected keyring or private file reference with an unencrypted key", false, err)
	case errors.Is(err, maildomain.ErrSMIMEPrivateKeyMismatch):
		return cli.WrapAppError(cli.ErrorUsage, "S/MIME private key does not match the selected certificate", false, err)
	case errors.Is(err, maildomain.ErrSMIMEPrivateKeyInvalid), errors.Is(err, secret.ErrInvalidReference):
		return cli.WrapAppError(cli.ErrorUsage, "S/MIME private key or external key reference is invalid", false, err)
	case errors.Is(err, secret.ErrMissing):
		return cli.WrapAppError(cli.ErrorConfiguration, "S/MIME private key material is unavailable", false, err)
	case errors.Is(err, secret.ErrUnavailable):
		return cli.WrapAppError(cli.ErrorConfiguration, "native keyring is unavailable; use an environment or private file S/MIME key reference", false, err)
	default:
		return cli.WrapAppError(cli.ErrorInternal, "cannot manage S/MIME private key material", false, err)
	}
}
