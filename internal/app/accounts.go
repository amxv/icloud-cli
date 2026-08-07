package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/folder"
	"github.com/amxv/icloud-cli/internal/id"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/gofrs/flock"
)

type accountCommands struct {
	keyring         secret.Keyring
	imap            transport.IMAPValidator
	smtp            transport.SMTPValidator
	sender          transport.SMTPSender
	folders         transport.IMAPFolderClient
	folderAdmin     transport.IMAPFolderAdminClient
	mail            transport.IMAPInboxClient
	watch           transport.IMAPWatchClient
	remote          transport.IMAPRemoteClient
	flags           transport.IMAPFlagClient
	locations       transport.IMAPLocationClient
	sent            transport.IMAPSentAppender
	appendMailbox   transport.IMAPMailboxAppender
	blobs           func(config.AppPaths) blob.Stager
	attachmentBlobs func(config.AppPaths) attachmentBlobStore
	now             func() time.Time
	newID           func(string) (string, error)
}

func (commands accountCommands) FolderList(ctx context.Context, home, accountSelector string, limit int, includeMissing bool) (account.Account, []folder.Folder, bool, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, nil, false, 0, err
	}
	defer state.Close()
	value, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, nil, false, state.SchemaVersion(), mapAccountError(err)
	}
	values, more, err := state.Folders().List(ctx, value.ID, limit, includeMissing)
	if err != nil {
		return account.Account{}, nil, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read local folders", false, err)
	}
	return value, values, more, state.SchemaVersion(), nil
}

func (commands accountCommands) FolderShow(ctx context.Context, home, accountSelector, folderSelector string) (account.Account, folder.Folder, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, 0, err
	}
	defer state.Close()
	value, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, state.SchemaVersion(), mapAccountError(err)
	}
	folderValue, err := state.Folders().Resolve(ctx, value.ID, folderSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, state.SchemaVersion(), mapFolderError(err)
	}
	return value, folderValue, state.SchemaVersion(), nil
}

func (commands accountCommands) FolderStatus(ctx context.Context, home, accountSelector string) (account.Account, []folder.Folder, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, nil, 0, err
	}
	defer state.Close()
	defer unlock()
	value, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, nil, state.SchemaVersion(), mapAccountError(err)
	}
	_, credential, err := state.Accounts().Credential(ctx, value.ID)
	if err != nil {
		return account.Account{}, nil, state.SchemaVersion(), mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return account.Account{}, nil, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)

	var (
		result       transport.FolderDiscoveryResult
		usedUsername string
		lastFailure  *transport.Failure
	)
	for _, username := range imapUsernameCandidates(value) {
		result, err = commands.folderClient().DiscoverFolders(ctx, transport.AuthRequest{Username: username, Password: password})
		if err == nil {
			usedUsername = username
			lastFailure = nil
			break
		}
		failure, ok := transport.AsFailure(err)
		if !ok {
			failure = transport.NewFailure(transport.StageConnect, transport.FailureUnavailable, false, err)
		}
		lastFailure = failure
		if failure.Kind != transport.FailureAuthentication {
			break
		}
	}
	if lastFailure != nil {
		return account.Account{}, nil, state.SchemaVersion(), mapFolderTransportError(lastFailure)
	}
	if usedUsername != "" && usedUsername != value.IMAPUsername {
		value, err = state.Accounts().UpdateIMAPUsername(ctx, value.ID, usedUsername, commands.timeNow())
		if err != nil {
			return account.Account{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist the working IMAP username", false, err)
		}
	}
	observations := make([]folder.RemoteObservation, 0, len(result.Folders))
	for _, remote := range result.Folders {
		observations = append(observations, remoteFolderObservation(remote))
	}
	if _, _, err := state.Folders().ReconcilePendingRenames(ctx, value.ID, observations, commands.timeNow()); err != nil {
		return account.Account{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reconcile an interrupted folder rename", false, err)
	}
	values, err := state.Folders().Reconcile(ctx, value.ID, observations, commands.timeNow(), func() (string, error) {
		return commands.generateID("fld")
	})
	if err != nil {
		return account.Account{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist folder discovery", false, err)
	}
	return value, values, state.SchemaVersion(), nil
}

func (commands accountCommands) FolderSetSync(ctx context.Context, home, accountSelector, folderSelector string, enabled bool) (account.Account, folder.Folder, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, 0, err
	}
	defer state.Close()
	defer unlock()
	value, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, state.SchemaVersion(), mapAccountError(err)
	}
	folderValue, err := state.Folders().SetSync(ctx, value.ID, folderSelector, enabled, commands.timeNow())
	if err != nil {
		return account.Account{}, folder.Folder{}, state.SchemaVersion(), mapFolderError(err)
	}
	return value, folderValue, state.SchemaVersion(), nil
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

func (commands accountCommands) Test(ctx context.Context, home, selector string, all bool) ([]account.HealthReport, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return nil, 0, err
	}
	defer state.Close()
	defer unlock()

	values, err := selectedAccounts(ctx, state, selector, all)
	if err != nil {
		return nil, state.SchemaVersion(), mapAccountError(err)
	}
	reports := make([]account.HealthReport, 0, len(values))
	var firstFailure *transport.Failure
	for _, value := range values {
		report, failure, testErr := commands.testAccount(ctx, state, value)
		if testErr != nil {
			return reports, state.SchemaVersion(), testErr
		}
		reports = append(reports, report)
		if firstFailure == nil && failure != nil {
			firstFailure = failure
		}
	}
	if firstFailure != nil {
		return reports, state.SchemaVersion(), mapValidationError(&account.ValidationError{Reports: reports, Failure: firstFailure})
	}
	return reports, state.SchemaVersion(), nil
}

func (commands accountCommands) Doctor(ctx context.Context, home, selector string, all, live bool) ([]account.DoctorReport, int64, error) {
	var (
		liveReports []account.HealthReport
		liveErr     error
	)
	if live {
		liveReports, _, liveErr = commands.Test(ctx, home, selector, all)
	}
	state, err := openState(ctx, home)
	if err != nil {
		return nil, 0, err
	}
	defer state.Close()
	values, err := selectedAccounts(ctx, state, selector, all)
	if err != nil {
		return nil, state.SchemaVersion(), mapAccountError(err)
	}
	liveByAccount := make(map[string]account.HealthReport, len(liveReports))
	for _, report := range liveReports {
		liveByAccount[report.AccountID] = report
	}
	reports := make([]account.DoctorReport, 0, len(values))
	for _, value := range values {
		report := account.DoctorReport{
			Account:              value,
			CredentialConfigured: value.CredentialScheme != "",
		}
		if liveReport, ok := liveByAccount[value.ID]; ok {
			copy := liveReport
			report.Live = &copy
		}
		reports = append(reports, report)
	}
	return reports, state.SchemaVersion(), liveErr
}

func selectedAccounts(ctx context.Context, state *store.Store, selector string, all bool) ([]account.Account, error) {
	if !all {
		value, err := state.Accounts().ResolveSelected(ctx, selector)
		if err != nil {
			return nil, err
		}
		return []account.Account{value}, nil
	}
	values, more, err := state.Accounts().List(ctx, 100)
	if err != nil {
		return nil, err
	}
	if more {
		return nil, account.ErrTooManyAccounts
	}
	return values, nil
}

func (commands accountCommands) testAccount(ctx context.Context, state *store.Store, value account.Account) (account.HealthReport, *transport.Failure, error) {
	checkedAt := commands.timeNow()
	report := account.HealthReport{
		AccountID:    value.ID,
		Status:       account.StatusDegraded,
		IMAPUsername: value.IMAPUsername,
		CheckedAt:    checkedAt,
	}
	_, credential, err := state.Accounts().Credential(ctx, value.ID)
	if err != nil {
		failure := transport.NewFailure(transport.StageCredential, transport.FailureConfiguration, false, err)
		report.IMAP = failedCheck(failure)
		report.SMTP = failedCheck(failure)
		if persistErr := commands.persistHealth(ctx, state, value, report, failure); persistErr != nil {
			return report, failure, persistErr
		}
		return report, failure, nil
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		kind := transport.FailureConfiguration
		if errors.Is(err, secret.ErrMissing) {
			kind = transport.FailureAuthentication
		}
		failure := transport.NewFailure(transport.StageCredential, kind, false, err)
		report.IMAP = failedCheck(failure)
		report.SMTP = failedCheck(failure)
		if persistErr := commands.persistHealth(ctx, state, value, report, failure); persistErr != nil {
			return report, failure, persistErr
		}
		return report, failure, nil
	}
	defer zeroBytes(password)

	var imapFailure *transport.Failure
	for _, username := range imapUsernameCandidates(value) {
		result, validateErr := commands.imapValidator().ValidateIMAP(ctx, transport.AuthRequest{Username: username, Password: password})
		if validateErr == nil {
			report.IMAPUsername = username
			report.IMAP = account.ProtocolCheck{
				OK:           true,
				Capabilities: transport.SanitizeCapabilities(result.Capabilities),
				Mailboxes:    result.Mailboxes,
				Inbox:        result.Inbox,
			}
			imapFailure = nil
			break
		}
		failure, ok := transport.AsFailure(validateErr)
		if !ok {
			failure = transport.NewFailure(transport.StageConnect, transport.FailureUnavailable, false, validateErr)
		}
		imapFailure = failure
		if failure.Kind != transport.FailureAuthentication {
			break
		}
	}
	if !report.IMAP.OK {
		report.IMAP = failedCheck(imapFailure)
	}

	smtpResult, smtpErr := commands.smtpValidator().ValidateSMTP(ctx, transport.AuthRequest{Username: value.Email, Password: password})
	var smtpFailure *transport.Failure
	if smtpErr == nil {
		report.SMTP = account.ProtocolCheck{OK: true, Capabilities: transport.SanitizeCapabilities(smtpResult.Capabilities)}
	} else {
		var ok bool
		smtpFailure, ok = transport.AsFailure(smtpErr)
		if !ok {
			smtpFailure = transport.NewFailure(transport.StageConnect, transport.FailureUnavailable, false, smtpErr)
		}
		report.SMTP = failedCheck(smtpFailure)
	}

	firstFailure := imapFailure
	if firstFailure == nil {
		firstFailure = smtpFailure
	}
	if report.IMAP.OK && report.SMTP.OK {
		report.Status = account.StatusConnected
	}
	if err := commands.persistHealth(ctx, state, value, report, firstFailure); err != nil {
		return report, firstFailure, err
	}
	return report, firstFailure, nil
}

func (commands accountCommands) persistHealth(ctx context.Context, state *store.Store, previous account.Account, report account.HealthReport, failure *transport.Failure) error {
	update := account.HealthUpdate{
		AccountID:        report.AccountID,
		Status:           report.Status,
		IMAPUsername:     report.IMAPUsername,
		IMAPCapabilities: report.IMAP.Capabilities,
		SMTPCapabilities: report.SMTP.Capabilities,
		CheckedAt:        report.CheckedAt,
		Healthy:          report.Status == account.StatusConnected,
	}
	if !report.IMAP.OK {
		update.IMAPCapabilities = previous.IMAPCapabilities
		update.IMAPUsername = previous.IMAPUsername
	}
	if !report.SMTP.OK {
		update.SMTPCapabilities = previous.SMTPCapabilities
	}
	if strings.TrimSpace(update.IMAPUsername) == "" {
		update.IMAPUsername = previous.IMAPUsername
	}
	if failure != nil {
		update.LastErrorCode = string(failure.Stage)
	}
	if _, err := state.Accounts().UpdateHealth(ctx, update); err != nil {
		return cli.WrapAppError(cli.ErrorInternal, "cannot persist account health", false, err)
	}
	return nil
}

func failedCheck(failure *transport.Failure) account.ProtocolCheck {
	if failure == nil {
		return account.ProtocolCheck{Stage: transport.StageConnect, Kind: transport.FailureUnavailable}
	}
	return account.ProtocolCheck{Stage: failure.Stage, Kind: failure.Kind, Retryable: failure.Retryable}
}

func imapUsernameCandidates(value account.Account) []string {
	local, _, _ := strings.Cut(value.Email, "@")
	values := []string{value.IMAPUsername, value.Email, local}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, candidate := range values {
		candidate = strings.TrimSpace(candidate)
		key := strings.ToLower(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, candidate)
	}
	return result
}

func (commands accountCommands) imapValidator() transport.IMAPValidator {
	if commands.imap == nil {
		return unavailableIMAPValidator{}
	}
	return commands.imap
}

func (commands accountCommands) smtpValidator() transport.SMTPValidator {
	if commands.smtp == nil {
		return unavailableSMTPValidator{}
	}
	return commands.smtp
}

func (commands accountCommands) folderClient() transport.IMAPFolderClient {
	if commands.folders != nil {
		return commands.folders
	}
	if candidate, ok := commands.imap.(transport.IMAPFolderClient); ok {
		return candidate
	}
	return unavailableFolderClient{}
}

func (commands accountCommands) folderAdminClient() transport.IMAPFolderAdminClient {
	if commands.folderAdmin != nil {
		return commands.folderAdmin
	}
	if candidate, ok := commands.folders.(transport.IMAPFolderAdminClient); ok {
		return candidate
	}
	if candidate, ok := commands.imap.(transport.IMAPFolderAdminClient); ok {
		return candidate
	}
	return unavailableFolderAdminClient{}
}

type unavailableIMAPValidator struct{}

func (unavailableIMAPValidator) ValidateIMAP(context.Context, transport.AuthRequest) (transport.IMAPResult, error) {
	return transport.IMAPResult{}, transport.NewFailure(transport.StageConnect, transport.FailureConfiguration, false, errors.New("IMAP validator unavailable"))
}

type unavailableSMTPValidator struct{}

func (unavailableSMTPValidator) ValidateSMTP(context.Context, transport.AuthRequest) (transport.SMTPResult, error) {
	return transport.SMTPResult{}, transport.NewFailure(transport.StageConnect, transport.FailureConfiguration, false, errors.New("SMTP validator unavailable"))
}

type unavailableFolderClient struct{}

func (unavailableFolderClient) DiscoverFolders(context.Context, transport.AuthRequest) (transport.FolderDiscoveryResult, error) {
	return transport.FolderDiscoveryResult{}, transport.NewFailure(transport.StageConnect, transport.FailureConfiguration, false, errors.New("folder discovery unavailable"))
}

type unavailableFolderAdminClient struct{}

func (unavailableFolderAdminClient) AdministerFolder(context.Context, transport.AuthRequest, transport.FolderAdminRequest) (transport.FolderAdminResult, error) {
	return transport.FolderAdminResult{}, transport.NewFailure(transport.StageConnect, transport.FailureConfiguration, false, errors.New("folder administration unavailable"))
}

func remoteFolderObservation(remote transport.RemoteFolder) folder.RemoteObservation {
	observation := folder.RemoteObservation{
		Name:           remote.Name,
		WireName:       remote.WireName,
		OldName:        remote.OldName,
		OldWireName:    remote.OldWireName,
		ParentName:     remote.ParentName,
		ParentWireName: remote.ParentWireName,
		Delimiter:      remote.Delimiter,
		Attributes:     append([]string(nil), remote.Attributes...),
		Kind:           folder.NormalizeKind(remote.Kind),
		KindSource:     remote.KindSource,
		Selectable:     remote.Selectable,
		Subscription:   folder.NormalizeSubscription(remote.Subscription),
	}
	if remote.Status != nil {
		observation.Messages = remote.Status.Messages
		observation.Unseen = remote.Status.Unseen
		observation.UIDNext = remote.Status.UIDNext
		observation.UIDValidity = remote.Status.UIDValidity
		observation.StatusKnown = true
	}
	return observation
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
	case errors.Is(err, account.ErrTooManyAccounts):
		return cli.WrapAppError(cli.ErrorConflict, "select one account when more than 100 profiles exist", false, err)
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

func mapFolderError(err error) error {
	switch {
	case errors.Is(err, folder.ErrNotFound):
		return cli.WrapAppError(cli.ErrorNotFound, "folder not found", false, err)
	case errors.Is(err, folder.ErrAmbiguous):
		return cli.WrapAppError(cli.ErrorConflict, "folder selection is ambiguous; use a stable folder ID or exact wire name", false, err)
	case errors.Is(err, folder.ErrNotSelectable):
		return cli.WrapAppError(cli.ErrorConflict, "folder is not selectable and cannot be enabled for synchronization", false, err)
	case errors.Is(err, folder.ErrInvalidName):
		return cli.WrapAppError(cli.ErrorUsage, "folder name is invalid for the current mailbox hierarchy", false, err)
	case errors.Is(err, folder.ErrProtected):
		return cli.WrapAppError(cli.ErrorSafety, "Inbox and provider-discovered system folders are protected from rename and delete", false, err)
	default:
		return cli.WrapAppError(cli.ErrorInternal, "cannot update local folder state", false, err)
	}
}

func mapFolderTransportError(failure *transport.Failure) error {
	if failure == nil {
		return cli.WrapAppError(cli.ErrorInternal, "folder discovery failed", false, failure)
	}
	code := cli.ErrorUnavailable
	switch failure.Kind {
	case transport.FailureAuthentication:
		code = cli.ErrorAuthentication
	case transport.FailureConfiguration:
		code = cli.ErrorConfiguration
	}
	appErr := cli.WrapAppError(code, "folder discovery failed at "+string(failure.Stage), failure.Retryable, failure)
	appErr.Details = map[string]any{"stage": failure.Stage}
	return appErr
}

func mapValidationError(validation *account.ValidationError) error {
	if validation == nil || validation.Failure == nil {
		return cli.WrapAppError(cli.ErrorInternal, "account validation failed", false, validation)
	}
	code := cli.ErrorUnavailable
	switch validation.Failure.Kind {
	case transport.FailureAuthentication:
		code = cli.ErrorAuthentication
	case transport.FailureConfiguration:
		code = cli.ErrorConfiguration
	}
	appErr := cli.WrapAppError(code, validation.Error(), validation.Failure.Retryable, validation)
	appErr.Details = map[string]any{
		"stage":   validation.Failure.Stage,
		"reports": validation.Reports,
	}
	return appErr
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
