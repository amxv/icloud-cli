package app

import (
	"context"
	"errors"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/amxv/icloud-cli/internal/transport"
)

func (commands accountCommands) MailUpdateFlag(
	ctx context.Context,
	home, accountSelector, messageID, folderSelector string,
	mutation maildomain.FlagMutation,
) (account.Account, folder.Folder, maildomain.Message, maildomain.Location, int64, error) {
	providerFlag, add, valid := mutation.ProviderFlag()
	if !valid {
		return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, 0, cli.NewAppError(cli.ErrorUsage, "unsupported message-state mutation", false)
	}
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, state.SchemaVersion(), mapAccountError(err)
	}
	message, err := state.Mail().Message(ctx, accountValue.ID, strings.TrimSpace(messageID))
	if err != nil {
		if errors.Is(err, maildomain.ErrMessageNotFound) {
			return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "message not found", false, err)
		}
		return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read local message locations", false, err)
	}
	folderValue, location, err := resolveMutationLocation(ctx, state.Folders(), accountValue.ID, message, folderSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, state.SchemaVersion(), err
	}
	_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, state.SchemaVersion(), mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)
	var (
		remote       transport.FlagMutationResult
		usedUsername string
		lastErr      error
	)
	for _, username := range imapUsernameCandidates(accountValue) {
		remote, lastErr = commands.flagClient().UpdateFlags(ctx, transport.AuthRequest{Username: username, Password: password}, transport.FlagMutationRequest{
			Mailbox: folderValue.Name, ExpectedUIDValidity: location.UIDValidity,
			UID: location.UID, ExpectedModSequence: location.ModSequence,
			Flag: providerFlag, Add: add,
		})
		if lastErr == nil {
			usedUsername = username
			break
		}
		failure, ok := transport.AsFailure(lastErr)
		if !ok || failure.Kind != transport.FailureAuthentication {
			break
		}
	}
	if lastErr != nil {
		var validity *transport.UIDValidityError
		if errors.As(lastErr, &validity) {
			return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "mailbox identity changed at iCloud; refresh folder status and repair synchronization before retrying", false, lastErr)
		}
		return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, state.SchemaVersion(), mapFlagTransportError(lastErr)
	}
	if usedUsername != "" && usedUsername != accountValue.IMAPUsername {
		accountValue, err = state.Accounts().UpdateIMAPUsername(ctx, accountValue.ID, usedUsername, commands.timeNow())
		if err != nil {
			return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist the working IMAP username", false, err)
		}
	}
	updatedFlags := maildomain.ParseLocationFlags(remote.Flags)
	updatedLocation, err := state.Mail().UpdateLocationState(
		ctx, accountValue.ID, location.ID, location.ModSequence,
		updatedFlags, remote.ModSequence, remote.Present, commands.timeNow(),
	)
	if err != nil {
		if errors.Is(err, maildomain.ErrLocationConflict) {
			return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "local message state changed concurrently; retry from fresh state", true, err)
		}
		return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist reconciled message state", true, err)
	}
	message, err = state.Mail().Message(ctx, accountValue.ID, message.ID)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reload the mutated message", true, err)
	}
	if !remote.Present {
		return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "the selected message location no longer exists at iCloud", false, maildomain.ErrLocationNotFound)
	}
	if !mutation.Achieved(updatedFlags) {
		appErr := cli.WrapAppError(cli.ErrorConflict, "iCloud state changed concurrently; the requested flag state was not applied", true, maildomain.ErrLocationConflict)
		appErr.Details = map[string]any{
			"message_id": message.ID, "location_id": updatedLocation.ID,
			"folder_id": folderValue.ID, "flags": updatedFlags,
			"mod_sequence": updatedLocation.ModSequence,
		}
		return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, state.SchemaVersion(), appErr
	}
	return accountValue, folderValue, message, updatedLocation, state.SchemaVersion(), nil
}

type folderResolver interface {
	Resolve(context.Context, string, string) (folder.Folder, error)
}

func resolveMutationLocation(ctx context.Context, folders folderResolver, accountID string, message maildomain.Message, folderSelector string) (folder.Folder, maildomain.Location, error) {
	eligible := make([]maildomain.Location, 0, len(message.Locations))
	for _, location := range message.Locations {
		if location.Present && location.Flags.Eligible() {
			eligible = append(eligible, location)
		}
	}
	if strings.TrimSpace(folderSelector) == "" {
		switch len(eligible) {
		case 0:
			return folder.Folder{}, maildomain.Location{}, cli.WrapAppError(cli.ErrorNotFound, "message has no eligible current location", false, maildomain.ErrLocationNotFound)
		case 1:
			folderValue, err := folders.Resolve(ctx, accountID, eligible[0].FolderID)
			if err != nil {
				return folder.Folder{}, maildomain.Location{}, mapFolderError(err)
			}
			if !folderValue.Present || folderValue.Stale || !folderValue.Selectable {
				return folder.Folder{}, maildomain.Location{}, cli.NewAppError(cli.ErrorConflict, "the message location is not in a current selectable folder", false)
			}
			return folderValue, eligible[0], nil
		default:
			return folder.Folder{}, maildomain.Location{}, cli.WrapAppError(cli.ErrorConflict, "message has multiple eligible locations; select one with --folder", false, maildomain.ErrLocationAmbiguous)
		}
	}
	folderValue, err := folders.Resolve(ctx, accountID, folderSelector)
	if err != nil {
		return folder.Folder{}, maildomain.Location{}, mapFolderError(err)
	}
	if !folderValue.Present || folderValue.Stale || !folderValue.Selectable {
		return folder.Folder{}, maildomain.Location{}, cli.NewAppError(cli.ErrorConflict, "selected folder is not current and selectable", false)
	}
	matches := make([]maildomain.Location, 0, 1)
	for _, location := range eligible {
		if location.FolderID == folderValue.ID {
			matches = append(matches, location)
		}
	}
	switch len(matches) {
	case 0:
		return folder.Folder{}, maildomain.Location{}, cli.WrapAppError(cli.ErrorNotFound, "message has no eligible location in the selected folder", false, maildomain.ErrLocationNotFound)
	case 1:
		return folderValue, matches[0], nil
	default:
		return folder.Folder{}, maildomain.Location{}, cli.WrapAppError(cli.ErrorConflict, "message has multiple locations in the selected folder", false, maildomain.ErrLocationAmbiguous)
	}
}

func (commands accountCommands) flagClient() transport.IMAPFlagClient {
	if commands.flags != nil {
		return commands.flags
	}
	if candidate, ok := commands.imap.(transport.IMAPFlagClient); ok {
		return candidate
	}
	return unavailableFlagClient{}
}

type unavailableFlagClient struct{}

func (unavailableFlagClient) UpdateFlags(context.Context, transport.AuthRequest, transport.FlagMutationRequest) (transport.FlagMutationResult, error) {
	return transport.FlagMutationResult{}, transport.NewFailure(transport.StageIMAPStore, transport.FailureConfiguration, false, errors.New("message-state mutation unavailable"))
}

func mapFlagTransportError(err error) error {
	failure, ok := transport.AsFailure(err)
	if !ok {
		return cli.WrapAppError(cli.ErrorUnavailable, "message-state mutation failed", false, err)
	}
	code := cli.ErrorUnavailable
	switch failure.Kind {
	case transport.FailureAuthentication:
		code = cli.ErrorAuthentication
	case transport.FailureConfiguration:
		code = cli.ErrorConfiguration
	}
	message := "message-state mutation failed"
	if failure.Stage != "" {
		message += " at " + string(failure.Stage)
	}
	appErr := cli.WrapAppError(code, message, failure.Retryable, err)
	appErr.Details = map[string]any{"stage": failure.Stage}
	return appErr
}
