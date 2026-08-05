package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/id"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/gofrs/flock"
)

type accountCommands struct {
	keyring secret.Keyring
	now     func() time.Time
	newID   func(string) (string, error)
}

func (commands accountCommands) List(ctx context.Context, home string, limit int) ([]account.Account, bool, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return nil, false, 0, err
	}
	defer state.Close()

	accounts, more, err := state.Accounts().List(ctx, limit)
	if err != nil {
		return nil, false, 0, cli.WrapAppError(cli.ErrorInternal, "cannot read local accounts", false, err)
	}
	return accounts, more, state.SchemaVersion(), nil
}

func (commands accountCommands) Show(ctx context.Context, home, selector string) (account.Account, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, 0, err
	}
	defer state.Close()

	value, err := state.Accounts().ResolveSelected(ctx, selector)
	if err != nil {
		return account.Account{}, state.SchemaVersion(), mapAccountError(err)
	}
	return value, state.SchemaVersion(), nil
}

func (commands accountCommands) Add(ctx context.Context, home string, input account.AddInput) (account.Account, int64, error) {
	name, email, imapUsername, err := normalizeAccountInput(input)
	if err != nil {
		return account.Account{}, 0, mapAccountError(err)
	}
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, 0, err
	}
	defer state.Close()
	defer unlock()

	accountID, err := commands.generateID("acct")
	if err != nil {
		return account.Account{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot generate an account ID", false, err)
	}
	credentialID, err := commands.generateID("cred")
	if err != nil {
		return account.Account{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot generate a credential ID", false, err)
	}
	scheme, reference, err := normalizeCredentialSetup(paths, accountID, input.Credential)
	if err != nil {
		return account.Account{}, state.SchemaVersion(), mapCredentialError(err)
	}
	manager := secret.NewManager(commands.keyring)
	storedKeyring := false
	if scheme == account.CredentialKeyring {
		if err := manager.StoreKeyring(ctx, reference, input.Credential.Secret); err != nil {
			return account.Account{}, state.SchemaVersion(), mapCredentialError(err)
		}
		storedKeyring = true
	}

	value, err := state.Accounts().Create(ctx, account.CreateRecord{
		AccountID:        accountID,
		CredentialID:     credentialID,
		Name:             name,
		Email:            email,
		IMAPUsername:     imapUsername,
		MakeDefault:      input.MakeDefault,
		CredentialScheme: scheme,
		CredentialRef:    reference,
		Now:              commands.timeNow(),
	})
	if err != nil {
		if storedKeyring {
			if cleanupErr := manager.DeleteKeyring(ctx, reference); cleanupErr != nil {
				return account.Account{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "account creation failed and native keyring cleanup did not complete", false, cleanupErr)
			}
		}
		return account.Account{}, state.SchemaVersion(), mapAccountError(err)
	}
	return value, state.SchemaVersion(), nil
}

func (commands accountCommands) Rename(ctx context.Context, home, selector, newName string) (account.Account, int64, error) {
	name, err := account.NormalizeName(newName)
	if err != nil {
		return account.Account{}, 0, mapAccountError(err)
	}
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, 0, err
	}
	defer state.Close()
	defer unlock()
	value, err := state.Accounts().Rename(ctx, selector, name, commands.timeNow())
	if err != nil {
		return account.Account{}, state.SchemaVersion(), mapAccountError(err)
	}
	return value, state.SchemaVersion(), nil
}

func (commands accountCommands) SetDefault(ctx context.Context, home, selector string) (account.Account, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, 0, err
	}
	defer state.Close()
	defer unlock()
	value, err := state.Accounts().SetDefault(ctx, selector, commands.timeNow())
	if err != nil {
		return account.Account{}, state.SchemaVersion(), mapAccountError(err)
	}
	return value, state.SchemaVersion(), nil
}

func (commands accountCommands) Remove(ctx context.Context, home, selector string) (account.Account, bool, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, false, 0, err
	}
	defer state.Close()
	defer unlock()

	value, credential, err := state.Accounts().RemovalCandidate(ctx, selector)
	if err != nil {
		return account.Account{}, false, state.SchemaVersion(), mapAccountError(err)
	}
	manager := secret.NewManager(commands.keyring)
	backup, hadKeyring, err := removeKeyringCredential(ctx, manager, credential)
	if err != nil {
		return account.Account{}, false, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(backup)

	removed, removedCredential, err := state.Accounts().Remove(ctx, value.ID)
	if err != nil {
		if restoreErr := restoreKeyringCredential(ctx, manager, credential, backup, hadKeyring); restoreErr != nil {
			return account.Account{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "account removal failed and native keyring restoration did not complete", false, restoreErr)
		}
		return account.Account{}, false, state.SchemaVersion(), mapAccountError(err)
	}
	return removed, removedCredential.Scheme == account.CredentialKeyring && hadKeyring, state.SchemaVersion(), nil
}

func (commands accountCommands) CredentialShow(ctx context.Context, home, selector string) (account.Credential, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Credential{}, 0, err
	}
	defer state.Close()
	_, credential, err := state.Accounts().Credential(ctx, selector)
	if err != nil {
		return account.Credential{}, state.SchemaVersion(), mapAccountError(err)
	}
	return credential, state.SchemaVersion(), nil
}

func (commands accountCommands) CredentialReplace(ctx context.Context, home, selector string, setup account.CredentialSetup) (account.Credential, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Credential{}, 0, err
	}
	defer state.Close()
	defer unlock()

	value, err := state.Accounts().Resolve(ctx, selector)
	if err != nil {
		return account.Credential{}, state.SchemaVersion(), mapAccountError(err)
	}
	oldCredential, oldErr := func() (account.Credential, error) {
		_, credential, credentialErr := state.Accounts().Credential(ctx, value.ID)
		return credential, credentialErr
	}()
	if oldErr != nil && !errors.Is(oldErr, account.ErrCredentialMissing) {
		return account.Credential{}, state.SchemaVersion(), mapAccountError(oldErr)
	}

	scheme, reference, err := normalizeCredentialSetup(paths, value.ID, setup)
	if err != nil {
		return account.Credential{}, state.SchemaVersion(), mapCredentialError(err)
	}
	manager := secret.NewManager(commands.keyring)
	backup, hadOldKeyring, err := backupKeyringCredential(ctx, manager, oldCredential)
	if err != nil {
		return account.Credential{}, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(backup)

	oldDeleted := false
	if oldCredential.Scheme == account.CredentialKeyring && oldCredential.Reference != reference {
		if err := manager.DeleteKeyring(ctx, oldCredential.Reference); err != nil {
			return account.Credential{}, state.SchemaVersion(), mapCredentialError(err)
		}
		oldDeleted = true
	}
	newKeyringStored := false
	if scheme == account.CredentialKeyring {
		if err := manager.StoreKeyring(ctx, reference, setup.Secret); err != nil {
			if oldDeleted {
				if restoreErr := restoreKeyringCredential(ctx, manager, oldCredential, backup, hadOldKeyring); restoreErr != nil {
					return account.Credential{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "credential replacement failed and native keyring restoration did not complete", false, restoreErr)
				}
			}
			return account.Credential{}, state.SchemaVersion(), mapCredentialError(err)
		}
		newKeyringStored = true
	}
	credentialID := oldCredential.ID
	if credentialID == "" {
		credentialID, err = commands.generateID("cred")
		if err != nil {
			if rollbackErr := rollbackCredentialKeyrings(ctx, manager, oldCredential, backup, hadOldKeyring, reference, newKeyringStored); rollbackErr != nil {
				return account.Credential{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "credential ID generation failed and native keyring rollback did not complete", false, rollbackErr)
			}
			return account.Credential{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot generate a credential ID", false, err)
		}
	}
	_, credential, err := state.Accounts().UpsertCredential(ctx, value.ID, credentialID, scheme, reference, commands.timeNow())
	if err != nil {
		if rollbackErr := rollbackCredentialKeyrings(ctx, manager, oldCredential, backup, hadOldKeyring, reference, newKeyringStored); rollbackErr != nil {
			return account.Credential{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "credential update failed and native keyring rollback did not complete", false, rollbackErr)
		}
		return account.Credential{}, state.SchemaVersion(), mapAccountError(err)
	}
	return credential, state.SchemaVersion(), nil
}

func (commands accountCommands) CredentialDelete(ctx context.Context, home, selector string) (account.Credential, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Credential{}, 0, err
	}
	defer state.Close()
	defer unlock()
	_, credential, err := state.Accounts().Credential(ctx, selector)
	if err != nil {
		return account.Credential{}, state.SchemaVersion(), mapAccountError(err)
	}
	manager := secret.NewManager(commands.keyring)
	backup, hadKeyring, err := removeKeyringCredential(ctx, manager, credential)
	if err != nil {
		return account.Credential{}, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(backup)
	_, deleted, err := state.Accounts().DeleteCredential(ctx, credential.AccountID, commands.timeNow())
	if err != nil {
		if restoreErr := restoreKeyringCredential(ctx, manager, credential, backup, hadKeyring); restoreErr != nil {
			return account.Credential{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "credential deletion failed and native keyring restoration did not complete", false, restoreErr)
		}
		return account.Credential{}, state.SchemaVersion(), mapAccountError(err)
	}
	return deleted, state.SchemaVersion(), nil
}

func (commands accountCommands) openWriteState(ctx context.Context, home string) (*store.Store, config.AppPaths, func(), error) {
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		return nil, config.AppPaths{}, nil, cli.WrapAppError(cli.ErrorConfiguration, "cannot resolve the iCloud application data directory", false, err)
	}
	if err := config.EnsureAppPaths(paths); err != nil {
		return nil, config.AppPaths{}, nil, cli.WrapAppError(cli.ErrorConfiguration, "cannot access the iCloud application data directory", false, err)
	}
	lock := flock.New(paths.AccountLock, flock.SetPermissions(0o600))
	locked, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil || !locked {
		return nil, config.AppPaths{}, nil, cli.WrapAppError(cli.ErrorConflict, "local account state is busy in another icloud process", true, err)
	}
	unlock := func() { _ = lock.Unlock() }
	state, err := store.Open(ctx, paths)
	if err != nil {
		unlock()
		return nil, config.AppPaths{}, nil, mapOpenStateError(err)
	}
	return state, paths, unlock, nil
}

func openState(ctx context.Context, home string) (*store.Store, error) {
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		return nil, cli.WrapAppError(cli.ErrorConfiguration, "cannot resolve the iCloud application data directory", false, err)
	}
	state, err := store.Open(ctx, paths)
	if err == nil {
		return state, nil
	}
	return nil, mapOpenStateError(err)
}

func mapOpenStateError(err error) error {
	if errors.Is(err, store.ErrMigrationLock) {
		return cli.WrapAppError(cli.ErrorConflict, "local state is busy while another icloud process migrates it", true, err)
	}
	if errors.Is(err, os.ErrPermission) {
		return cli.WrapAppError(cli.ErrorConfiguration, "cannot access the iCloud application data directory", false, err)
	}
	return cli.WrapAppError(cli.ErrorInternal, "cannot open local iCloud state", false, err)
}

func normalizeAccountInput(input account.AddInput) (string, string, string, error) {
	name, err := account.NormalizeName(input.Name)
	if err != nil {
		return "", "", "", err
	}
	email, err := account.NormalizeEmail(input.Email)
	if err != nil {
		return "", "", "", err
	}
	imapUsername, err := account.NormalizeIMAPUsername(input.IMAPUsername, email)
	if err != nil {
		return "", "", "", err
	}
	return name, email, imapUsername, nil
}

func normalizeCredentialSetup(paths config.AppPaths, accountID string, setup account.CredentialSetup) (account.CredentialScheme, string, error) {
	switch setup.Scheme {
	case account.CredentialKeyring:
		reference, err := secret.KeyringReference(paths.Root, accountID)
		if err != nil {
			return "", "", err
		}
		if len(setup.Secret) == 0 {
			return "", "", secret.ErrMissing
		}
		return setup.Scheme, reference, nil
	case account.CredentialEnv:
		reference, err := secret.CanonicalReference(setup.Scheme, setup.Reference)
		if err != nil {
			return "", "", err
		}
		return setup.Scheme, reference, nil
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
		canonical, err := secret.CanonicalReference(setup.Scheme, absolute)
		if err != nil {
			return "", "", err
		}
		return setup.Scheme, canonical, nil
	default:
		return "", "", secret.ErrInvalidReference
	}
}

func (commands accountCommands) generateID(prefix string) (string, error) {
	if commands.newID != nil {
		return commands.newID(prefix)
	}
	return id.New(prefix)
}

func (commands accountCommands) timeNow() time.Time {
	if commands.now != nil {
		return commands.now().UTC()
	}
	return time.Now().UTC()
}

func mapAccountError(err error) error {
	switch {
	case errors.Is(err, account.ErrInvalidInput):
		return cli.WrapAppError(cli.ErrorUsage, strings.TrimPrefix(err.Error(), account.ErrInvalidInput.Error()+": "), false, err)
	case errors.Is(err, account.ErrNotFound):
		return cli.WrapAppError(cli.ErrorNotFound, "account not found", false, err)
	case errors.Is(err, account.ErrAmbiguous):
		return cli.WrapAppError(cli.ErrorConflict, "account selection is ambiguous; use --account or an explicit name, email, or ID", false, err)
	case errors.Is(err, account.ErrConflict):
		return cli.WrapAppError(cli.ErrorConflict, "an account with that name or email already exists", false, err)
	case errors.Is(err, account.ErrDefaultRemoval):
		return cli.WrapAppError(cli.ErrorConflict, "set another default account before removing this one", false, err)
	case errors.Is(err, account.ErrCredentialMissing):
		return cli.WrapAppError(cli.ErrorNotFound, "account credential is not configured", false, err)
	default:
		return cli.WrapAppError(cli.ErrorInternal, "cannot update local account state", false, err)
	}
}

func mapCredentialError(err error) error {
	switch {
	case errors.Is(err, secret.ErrInvalidReference):
		return cli.WrapAppError(cli.ErrorUsage, "credential source is invalid", false, err)
	case errors.Is(err, secret.ErrMissing):
		return cli.WrapAppError(cli.ErrorAuthentication, "credential value is missing", false, err)
	case errors.Is(err, secret.ErrUnavailable):
		return cli.WrapAppError(cli.ErrorConfiguration, "native keyring is unavailable; use an environment or file credential reference", false, err)
	default:
		return cli.WrapAppError(cli.ErrorInternal, "cannot manage the account credential", false, err)
	}
}

func backupKeyringCredential(ctx context.Context, manager secret.Manager, credential account.Credential) ([]byte, bool, error) {
	if credential.Scheme != account.CredentialKeyring || credential.Reference == "" {
		return nil, false, nil
	}
	value, err := manager.Resolve(ctx, credential)
	if errors.Is(err, secret.ErrMissing) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func removeKeyringCredential(ctx context.Context, manager secret.Manager, credential account.Credential) ([]byte, bool, error) {
	backup, found, err := backupKeyringCredential(ctx, manager, credential)
	if err != nil || credential.Scheme != account.CredentialKeyring || credential.Reference == "" {
		return backup, found, err
	}
	if err := manager.DeleteKeyring(ctx, credential.Reference); err != nil {
		zeroBytes(backup)
		return nil, false, err
	}
	return backup, found, nil
}

func restoreKeyringCredential(ctx context.Context, manager secret.Manager, credential account.Credential, backup []byte, found bool) error {
	if credential.Scheme != account.CredentialKeyring || credential.Reference == "" || !found {
		return nil
	}
	return manager.StoreKeyring(ctx, credential.Reference, backup)
}

func rollbackCredentialKeyrings(
	ctx context.Context,
	manager secret.Manager,
	old account.Credential,
	backup []byte,
	hadOld bool,
	newReference string,
	newStored bool,
) error {
	var failures []error
	if newStored && (old.Reference != newReference || !hadOld) {
		if err := manager.DeleteKeyring(ctx, newReference); err != nil {
			failures = append(failures, err)
		}
	}
	if hadOld {
		if err := manager.StoreKeyring(ctx, old.Reference, backup); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
