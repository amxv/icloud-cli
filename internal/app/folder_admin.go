package app

import (
	"context"
	"errors"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

func (commands accountCommands) FolderAdminPreview(ctx context.Context, home, accountSelector string, request folder.AdminRequest) (account.Account, folder.AdminPlan, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, folder.AdminPlan{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.AdminPlan{}, state.SchemaVersion(), mapAccountError(err)
	}
	plan, err := buildFolderAdminPlan(ctx, state, accountValue.ID, request)
	if err != nil {
		return account.Account{}, folder.AdminPlan{}, state.SchemaVersion(), err
	}
	return accountValue, plan, state.SchemaVersion(), nil
}

func (commands accountCommands) FolderAdmin(ctx context.Context, home, accountSelector string, request folder.AdminRequest) (account.Account, folder.AdminOutcome, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.AdminOutcome{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), mapAccountError(err)
	}
	plan, err := buildFolderAdminPlan(ctx, state, accountValue.ID, request)
	if err != nil {
		return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), err
	}
	if plan.Action == folder.AdminDelete && plan.Messages > 0 && !plan.AllowNonEmpty {
		return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorSafety, "folder is not empty; review the message count and rerun with --allow-non-empty --yes only if provider deletion is intended", false)
	}

	_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
	if err != nil {
		return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)
	var renameRecovery folder.RenameRecovery
	if plan.Action == folder.AdminRename {
		renameRecovery, err = state.Folders().BeginRenameRecovery(
			ctx, accountValue.ID, plan.FolderID, plan.CurrentName, plan.RequestedName, commands.timeNow(),
			func() (string, error) { return commands.generateID("frr") },
		)
		if err != nil {
			return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot record durable folder rename recovery before contacting iCloud", false, err)
		}
	}

	providerRequest := transport.FolderAdminRequest{
		Action:        transportFolderAdminAction(plan.Action),
		Mailbox:       plan.CurrentName,
		NewMailbox:    plan.RequestedName,
		AllowNonEmpty: plan.AllowNonEmpty,
	}
	var (
		providerResult transport.FolderAdminResult
		usedUsername   string
		lastErr        error
	)
	for _, username := range imapUsernameCandidates(accountValue) {
		providerResult, lastErr = commands.folderAdminClient().AdministerFolder(ctx, transport.AuthRequest{Username: username, Password: password}, providerRequest)
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
		if renameRecovery.ID != "" {
			var targetErr *transport.FolderAdminTargetError
			if errors.As(lastErr, &targetErr) && strings.TrimSpace(targetErr.TargetMailbox) != "" && targetErr.TargetMailbox != renameRecovery.NewName {
				if updateErr := state.Folders().UpdateRenameRecoveryTarget(ctx, renameRecovery.ID, targetErr.TargetMailbox, commands.timeNow()); updateErr != nil {
					return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "folder rename outcome is uncertain and its recovery target could not be persisted; refresh folder status before another provider write", false, updateErr)
				}
			}
			if failure, ok := transport.AsFailure(lastErr); ok && failure.Kind != transport.FailureUnavailable {
				if updateErr := state.Folders().MarkRenameRecoveryNotApplied(ctx, renameRecovery.ID, commands.timeNow()); updateErr != nil && !errors.Is(updateErr, folder.ErrNotFound) {
					return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "folder rename failed and its recovery record could not be closed", false, updateErr)
				}
			}
		}
		var nonEmpty *transport.FolderNonEmptyError
		if errors.As(lastErr, &nonEmpty) {
			return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorSafety, "folder is not empty at iCloud; refresh folder status and use --allow-non-empty --yes only if provider deletion is intended", false)
		}
		return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), mapFolderAdminTransportError(lastErr, plan.Action)
	}
	if usedUsername != "" && usedUsername != accountValue.IMAPUsername {
		accountValue, err = state.Accounts().UpdateIMAPUsername(ctx, accountValue.ID, usedUsername, commands.timeNow())
		if err != nil {
			return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "folder change succeeded but the working IMAP username could not be persisted", false, err)
		}
	}

	outcome := folder.AdminOutcome{Action: plan.Action, PreviousName: providerResult.PreviousName}
	switch plan.Action {
	case folder.AdminCreate:
		value, err := state.Folders().CreateObserved(ctx, accountValue.ID, remoteFolderObservation(providerResult.Folder), commands.timeNow(), func() (string, error) {
			return commands.generateID("fld")
		})
		if err != nil {
			return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "folder was created at iCloud but local folder identity could not be recorded; refresh folder status before another provider write", false, err)
		}
		outcome.Folder = value
	case folder.AdminRename:
		value, err := state.Folders().ResolveRenameRecovery(ctx, renameRecovery.ID, remoteFolderObservation(providerResult.Folder), commands.timeNow())
		if err != nil {
			return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "folder was renamed at iCloud but local folder identity could not be reconciled; refresh folder status before another provider write", false, err)
		}
		outcome.Folder = value
	case folder.AdminDelete:
		value, err := state.Folders().MarkDeleted(ctx, accountValue.ID, plan.FolderID, commands.timeNow())
		if err != nil {
			return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "folder was deleted at iCloud but local locations could not be reconciled; refresh folder status before another provider write", false, err)
		}
		outcome.Folder = value
		outcome.Deleted = true
	case folder.AdminSubscribe, folder.AdminUnsubscribe:
		subscription := folder.NormalizeSubscription(providerResult.Folder.Subscription)
		if subscription == folder.SubscriptionUnknown {
			if plan.Action == folder.AdminSubscribe {
				subscription = folder.SubscriptionSubscribed
			} else {
				subscription = folder.SubscriptionUnsubscribed
			}
		}
		value, err := state.Folders().SetSubscription(ctx, accountValue.ID, plan.FolderID, subscription, commands.timeNow())
		if err != nil {
			return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "folder subscription changed at iCloud but local state could not be updated; refresh folder status before another provider write", false, err)
		}
		outcome.Folder = value
		outcome.Subscription = subscription
	default:
		return account.Account{}, folder.AdminOutcome{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorUsage, "unsupported folder administration action", false)
	}
	return accountValue, outcome, state.SchemaVersion(), nil
}

func buildFolderAdminPlan(ctx context.Context, state *store.Store, accountID string, request folder.AdminRequest) (folder.AdminPlan, error) {
	action := request.Action
	if action != folder.AdminCreate && action != folder.AdminRename && action != folder.AdminDelete && action != folder.AdminSubscribe && action != folder.AdminUnsubscribe {
		return folder.AdminPlan{}, cli.NewAppError(cli.ErrorUsage, "unsupported folder administration action", false)
	}
	plan := folder.AdminPlan{Action: action, AllowNonEmpty: request.AllowNonEmpty}
	if action == folder.AdminCreate {
		name := strings.TrimSpace(request.Name)
		if name != request.Name {
			return folder.AdminPlan{}, mapFolderError(folder.ErrInvalidName)
		}
		delimiter, err := accountFolderDelimiter(ctx, state, accountID)
		if err != nil {
			return folder.AdminPlan{}, err
		}
		if err := folder.ValidateAdminName(name, delimiter); err != nil {
			return folder.AdminPlan{}, mapFolderError(err)
		}
		if existing, err := state.Folders().Resolve(ctx, accountID, name); err == nil && existing.Present {
			return folder.AdminPlan{}, cli.NewAppError(cli.ErrorConflict, "folder already exists", false)
		} else if err != nil && !errors.Is(err, folder.ErrNotFound) && !errors.Is(err, folder.ErrAmbiguous) {
			return folder.AdminPlan{}, mapFolderError(err)
		} else if errors.Is(err, folder.ErrAmbiguous) {
			return folder.AdminPlan{}, cli.NewAppError(cli.ErrorConflict, "folder name conflicts with more than one current selector; choose a different name", false)
		}
		plan.RequestedName = name
		return plan, nil
	}

	current, err := state.Folders().Resolve(ctx, accountID, strings.TrimSpace(request.Folder))
	if err != nil {
		return folder.AdminPlan{}, mapFolderError(err)
	}
	if !current.Present || current.Stale {
		return folder.AdminPlan{}, cli.NewAppError(cli.ErrorConflict, "selected folder is missing from the latest provider discovery; refresh folder status before changing it", false)
	}
	plan.FolderID = current.ID
	plan.CurrentName = current.Name
	plan.Kind = current.Kind
	plan.Subscription = current.Subscription
	plan.Messages = current.Messages
	plan.SyncEnabled = current.SyncEnabled
	if action == folder.AdminRename || action == folder.AdminDelete {
		if folder.ProtectedFromDestructiveMutation(current) {
			return folder.AdminPlan{}, mapFolderError(folder.ErrProtected)
		}
		hasChildren, err := folderHasCurrentChildren(ctx, state, accountID, current.Name)
		if err != nil {
			return folder.AdminPlan{}, err
		}
		if hasChildren {
			return folder.AdminPlan{}, cli.NewAppError(cli.ErrorSafety, "folders with current child mailboxes are not renamed or deleted automatically; change the child mailboxes first", false)
		}
		plan.Destructive = true
	}
	if action == folder.AdminDelete {
		if !current.Selectable {
			return folder.AdminPlan{}, cli.NewAppError(cli.ErrorSafety, "non-selectable container folders are not deleted automatically; remove or rename child mailboxes explicitly", false)
		}
		plan.ProviderRechecks = true
		return plan, nil
	}
	if action == folder.AdminRename {
		name := strings.TrimSpace(request.Name)
		if name != request.Name {
			return folder.AdminPlan{}, mapFolderError(folder.ErrInvalidName)
		}
		if err := folder.ValidateAdminName(name, current.Delimiter); err != nil {
			return folder.AdminPlan{}, mapFolderError(err)
		}
		if strings.EqualFold(name, current.Name) {
			return folder.AdminPlan{}, cli.NewAppError(cli.ErrorConflict, "new folder name is unchanged", false)
		}
		if existing, err := state.Folders().Resolve(ctx, accountID, name); err == nil && existing.Present && existing.ID != current.ID {
			return folder.AdminPlan{}, cli.NewAppError(cli.ErrorConflict, "destination folder already exists", false)
		} else if err != nil && !errors.Is(err, folder.ErrNotFound) && !errors.Is(err, folder.ErrAmbiguous) {
			return folder.AdminPlan{}, mapFolderError(err)
		} else if errors.Is(err, folder.ErrAmbiguous) {
			return folder.AdminPlan{}, cli.NewAppError(cli.ErrorConflict, "new folder name conflicts with more than one current selector; choose a different name", false)
		}
		plan.RequestedName = name
	}
	return plan, nil
}

func folderHasCurrentChildren(ctx context.Context, state *store.Store, accountID, name string) (bool, error) {
	cursor := ""
	for {
		values, more, next, err := state.Folders().ListPage(ctx, accountID, syncFolderSchedulePageSize, false, cursor)
		if err != nil {
			return false, cli.WrapAppError(cli.ErrorInternal, "cannot inspect folder hierarchy", false, err)
		}
		for _, value := range values {
			if value.ParentName == name && value.Present && !value.Stale {
				return true, nil
			}
		}
		if !more {
			return false, nil
		}
		if next == "" || next == cursor {
			return false, cli.NewAppError(cli.ErrorInternal, "folder hierarchy pagination did not advance", false)
		}
		cursor = next
	}
}

func accountFolderDelimiter(ctx context.Context, state *store.Store, accountID string) (string, error) {
	cursor := ""
	fallback := ""
	for {
		values, more, next, err := state.Folders().ListPage(ctx, accountID, syncFolderSchedulePageSize, false, cursor)
		if err != nil {
			return "", cli.WrapAppError(cli.ErrorInternal, "cannot read folder hierarchy", false, err)
		}
		for _, value := range values {
			if value.Kind == folder.KindInbox && value.Delimiter != "" {
				return value.Delimiter, nil
			}
			if fallback == "" && value.Delimiter != "" {
				fallback = value.Delimiter
			}
		}
		if !more {
			return fallback, nil
		}
		if next == "" || next == cursor {
			return "", cli.NewAppError(cli.ErrorInternal, "folder hierarchy pagination did not advance", false)
		}
		cursor = next
	}
}

func transportFolderAdminAction(action folder.AdminAction) transport.FolderAdminAction {
	switch action {
	case folder.AdminCreate:
		return transport.FolderAdminCreate
	case folder.AdminRename:
		return transport.FolderAdminRename
	case folder.AdminDelete:
		return transport.FolderAdminDelete
	case folder.AdminSubscribe:
		return transport.FolderAdminSubscribe
	case folder.AdminUnsubscribe:
		return transport.FolderAdminUnsubscribe
	default:
		return ""
	}
}

func mapFolderAdminTransportError(err error, action folder.AdminAction) error {
	failure, ok := transport.AsFailure(err)
	if !ok {
		return cli.WrapAppError(cli.ErrorUnavailable, "folder change could not be confirmed; refresh folder status before retrying", false, err)
	}
	code := cli.ErrorUnavailable
	switch failure.Kind {
	case transport.FailureAuthentication:
		code = cli.ErrorAuthentication
	case transport.FailureConfiguration:
		code = cli.ErrorConflict
	}
	message := string(action) + " folder failed at " + string(failure.Stage)
	if failure.Kind == transport.FailureUnavailable {
		message += "; refresh folder status before retrying because provider state may have changed"
	}
	appErr := cli.WrapAppError(code, message, false, err)
	appErr.Details = map[string]any{"stage": failure.Stage}
	return appErr
}
