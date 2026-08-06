package app

import (
	"context"
	"errors"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

func (commands accountCommands) MailInbox(ctx context.Context, home, accountSelector, folderSelector string, limit int) (account.Account, folder.Folder, []maildomain.Message, bool, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, nil, false, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, nil, false, state.SchemaVersion(), mapAccountError(err)
	}
	folderValue, err := resolveMailFolder(ctx, state, accountValue.ID, folderSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, nil, false, state.SchemaVersion(), err
	}
	messages, more, err := state.Mail().MessagesInFolder(ctx, accountValue.ID, folderValue.ID, limit)
	if err != nil {
		return account.Account{}, folder.Folder{}, nil, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read the local mail folder", false, err)
	}
	return accountValue, folderValue, messages, more, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSyncStatus(ctx context.Context, home, accountSelector, folderSelector string) (account.Account, folder.Folder, maildomain.Checkpoint, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.Checkpoint{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.Checkpoint{}, state.SchemaVersion(), mapAccountError(err)
	}
	folderValue, err := resolveMailFolder(ctx, state, accountValue.ID, folderSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.Checkpoint{}, state.SchemaVersion(), err
	}
	checkpoint, err := state.Mail().Checkpoint(ctx, accountValue.ID, folderValue.ID)
	if errors.Is(err, maildomain.ErrCheckpointNotFound) {
		checkpoint = zeroCheckpoint(accountValue.ID, folderValue.ID)
	} else if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.Checkpoint{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read folder synchronization status", false, err)
	}
	return accountValue, folderValue, checkpoint, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSyncCheckpoints(ctx context.Context, home, accountSelector string, limit int) (account.Account, []maildomain.Checkpoint, bool, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, nil, false, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, nil, false, state.SchemaVersion(), mapAccountError(err)
	}
	values, more, err := state.Mail().Checkpoints(ctx, accountValue.ID, limit)
	if err != nil {
		return account.Account{}, nil, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read synchronization checkpoints", false, err)
	}
	return accountValue, values, more, state.SchemaVersion(), nil
}

func resolveMailFolder(ctx context.Context, state *store.Store, accountID, selector string) (folder.Folder, error) {
	if selector == "" {
		selector = string(folder.KindInbox)
	}
	value, err := state.Folders().Resolve(ctx, accountID, selector)
	if err != nil {
		if errors.Is(err, folder.ErrNotFound) {
			return folder.Folder{}, cli.WrapAppError(cli.ErrorConfiguration, "mail folder is not discovered; run `icloud mail folders status` first", false, err)
		}
		return folder.Folder{}, mapFolderError(err)
	}
	if !value.Present || value.Stale {
		return folder.Folder{}, cli.WrapAppError(cli.ErrorConflict, "mail folder is missing from the latest provider discovery; refresh folder status", false, nil)
	}
	if !value.Selectable {
		return folder.Folder{}, cli.WrapAppError(cli.ErrorConflict, "mail folder is not selectable at iCloud", false, nil)
	}
	return value, nil
}

func zeroCheckpoint(accountID, folderID string) maildomain.Checkpoint {
	return maildomain.Checkpoint{
		AccountID: accountID,
		FolderID:  folderID,
		State:     maildomain.CheckpointIdle,
	}
}

func (commands accountCommands) inboxClient() transport.IMAPInboxClient {
	if commands.mail != nil {
		return commands.mail
	}
	if candidate, ok := commands.imap.(transport.IMAPInboxClient); ok {
		return candidate
	}
	return unavailableInboxClient{}
}

func (commands accountCommands) blobStager(paths config.AppPaths) blob.Stager {
	if commands.blobs != nil {
		return commands.blobs(paths)
	}
	return blob.New(paths.BlobRoot)
}

type unavailableInboxClient struct{}

func (unavailableInboxClient) FetchInboxPage(context.Context, transport.AuthRequest, transport.InboxFetchRequest, blob.Stager) (transport.InboxPage, error) {
	return transport.InboxPage{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureConfiguration, false, errors.New("mail synchronization unavailable"))
}

func abortInboxPage(page transport.InboxPage) {
	for _, message := range page.Messages {
		if message.Raw != nil {
			_ = message.Raw.Abort()
		}
	}
}
