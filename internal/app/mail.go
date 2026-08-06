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
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

func (commands accountCommands) MailSync(ctx context.Context, home, accountSelector string, limit int) (account.Account, folder.Folder, maildomain.SyncSummary, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, 0, err
	}
	defer state.Close()
	defer unlock()

	accountValue, inbox, credential, checkpoint, err := loadInboxSyncState(ctx, state, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, state.SchemaVersion(), err
	}
	if checkpoint.State == maildomain.CheckpointRepairRequired {
		return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "Inbox identity changed at iCloud; run mail sync repair before continuing", false, maildomain.ErrRepairRequired)
	}
	checkpointID := checkpoint.ID
	if checkpointID == "" {
		checkpointID, err = commands.generateID("chk")
		if err != nil {
			return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate a synchronization checkpoint ID", false, err)
		}
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)
	attemptedAt := commands.timeNow()
	stager := commands.blobStager(paths)

	var (
		page         transport.InboxPage
		usedUsername string
		lastErr      error
	)
	for _, username := range imapUsernameCandidates(accountValue) {
		page, lastErr = commands.inboxClient().FetchInboxPage(ctx, transport.AuthRequest{Username: username, Password: password}, transport.InboxFetchRequest{
			Mailbox:             inbox.WireName,
			ExpectedUIDValidity: checkpoint.UIDValidity,
			LastCommittedUID:    checkpoint.LastCommittedUID,
			LastScannedUID:      checkpoint.LastScannedUID,
			Bootstrap:           !checkpoint.BootstrapComplete,
			Limit:               limit,
		}, stager)
		if lastErr == nil {
			usedUsername = username
			break
		}
		var validity *transport.UIDValidityError
		if errors.As(lastErr, &validity) {
			_, _ = state.Mail().RequireRepair(ctx, checkpointID, accountValue.ID, inbox.ID, validity.Observed, attemptedAt)
			return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "Inbox identity changed at iCloud; synchronization stopped before changing local messages", false, lastErr)
		}
		failure, ok := transport.AsFailure(lastErr)
		if !ok || failure.Kind != transport.FailureAuthentication {
			break
		}
	}
	if lastErr != nil {
		failureCode := "imap_fetch"
		if failure, ok := transport.AsFailure(lastErr); ok && failure.Stage != "" {
			failureCode = string(failure.Stage)
		}
		_, _ = state.Mail().RecordFailure(ctx, checkpointID, accountValue.ID, inbox.ID, checkpoint.UIDValidity, failureCode, attemptedAt)
		return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, state.SchemaVersion(), mapMailTransportError(lastErr)
	}
	defer abortInboxPage(page)

	if usedUsername != "" && usedUsername != accountValue.IMAPUsername {
		accountValue, err = state.Accounts().UpdateIMAPUsername(ctx, accountValue.ID, usedUsername, attemptedAt)
		if err != nil {
			return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist the working IMAP username", false, err)
		}
	}

	records := make([]maildomain.IngestRecord, 0, len(page.Messages))
	for _, fetched := range page.Messages {
		if fetched.Raw == nil {
			_, _ = state.Mail().RecordFailure(ctx, checkpointID, accountValue.ID, inbox.ID, checkpoint.UIDValidity, "raw_message_missing", attemptedAt)
			return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "iCloud returned a message without durable raw content", false, nil)
		}
		reader, openErr := fetched.Raw.Open()
		if openErr != nil {
			_, _ = state.Mail().RecordFailure(ctx, checkpointID, accountValue.ID, inbox.ID, checkpoint.UIDValidity, "blob_open", attemptedAt)
			return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read a staged Inbox message", false, openErr)
		}
		parsed, parseErr := maildomain.ParsePreview(reader)
		closeErr := reader.Close()
		if parseErr == nil {
			parseErr = closeErr
		}
		if parseErr != nil {
			_, _ = state.Mail().RecordFailure(ctx, checkpointID, accountValue.ID, inbox.ID, checkpoint.UIDValidity, "message_parse", attemptedAt)
			return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorUnavailable, "cannot safely normalize an Inbox message", false, parseErr)
		}
		messageID, idErr := commands.generateID("msg")
		if idErr != nil {
			return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate a message ID", false, idErr)
		}
		locationID, idErr := commands.generateID("loc")
		if idErr != nil {
			return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate a message-location ID", false, idErr)
		}
		records = append(records, maildomain.BuildIngestRecord(
			messageID, locationID, accountValue.ID, inbox.ID, page.UIDValidity, fetched.UID,
			fetched.Flags, fetched.InternalDate, fetched.RFC822Size,
			fetched.Raw.ContentHash, fetched.Raw.StorageKey, fetched.Raw.ByteSize, parsed,
		))
	}
	for _, fetched := range page.Messages {
		if err := fetched.Raw.Commit(); err != nil {
			_, _ = state.Mail().RecordFailure(ctx, checkpointID, accountValue.ID, inbox.ID, checkpoint.UIDValidity, "blob_commit", attemptedAt)
			return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot commit a staged Inbox message", true, err)
		}
	}
	commit, err := state.Mail().CommitInboxPage(ctx, maildomain.CommitPage{
		CheckpointID:     checkpointID,
		AccountID:        accountValue.ID,
		FolderID:         inbox.ID,
		UIDValidity:      page.UIDValidity,
		Bootstrap:        page.Bootstrap,
		LastCommittedUID: page.LastCommittedUID,
		LastScannedUID:   page.LastScannedUID,
		MoreAvailable:    page.MoreAvailable,
		AttemptedAt:      attemptedAt,
		Records:          records,
	})
	if err != nil {
		_, _ = state.Mail().RecordFailure(ctx, checkpointID, accountValue.ID, inbox.ID, checkpoint.UIDValidity, "transaction", attemptedAt)
		return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot atomically commit the Inbox page", true, err)
	}
	return accountValue, inbox, maildomain.SyncSummary{
		AccountID:     accountValue.ID,
		FolderID:      inbox.ID,
		FolderName:    inbox.Name,
		Fetched:       len(page.Messages),
		Created:       commit.Created,
		Updated:       commit.Updated,
		LastUID:       commit.Checkpoint.LastCommittedUID,
		MoreAvailable: commit.Checkpoint.MoreAvailable,
		Bootstrap:     page.Bootstrap,
		CompletedAt:   attemptedAt,
		Checkpoint:    commit.Checkpoint,
	}, state.SchemaVersion(), nil
}

func (commands accountCommands) MailInbox(ctx context.Context, home, accountSelector string, limit int) (account.Account, folder.Folder, []maildomain.Message, bool, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, nil, false, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, nil, false, state.SchemaVersion(), mapAccountError(err)
	}
	inbox, err := resolveInbox(ctx, state, accountValue.ID)
	if err != nil {
		return account.Account{}, folder.Folder{}, nil, false, state.SchemaVersion(), err
	}
	messages, more, err := state.Mail().Inbox(ctx, accountValue.ID, inbox.ID, limit)
	if err != nil {
		return account.Account{}, folder.Folder{}, nil, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read the local Inbox", false, err)
	}
	return accountValue, inbox, messages, more, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSyncStatus(ctx context.Context, home, accountSelector string) (account.Account, folder.Folder, maildomain.Checkpoint, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.Checkpoint{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.Checkpoint{}, state.SchemaVersion(), mapAccountError(err)
	}
	inbox, err := resolveInbox(ctx, state, accountValue.ID)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.Checkpoint{}, state.SchemaVersion(), err
	}
	checkpoint, err := state.Mail().Checkpoint(ctx, accountValue.ID, inbox.ID)
	if errors.Is(err, maildomain.ErrCheckpointNotFound) {
		checkpoint = zeroCheckpoint(accountValue.ID, inbox.ID)
	} else if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.Checkpoint{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read Inbox synchronization status", false, err)
	}
	return accountValue, inbox, checkpoint, state.SchemaVersion(), nil
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

func loadInboxSyncState(ctx context.Context, state *store.Store, accountSelector string) (account.Account, folder.Folder, account.Credential, maildomain.Checkpoint, error) {
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, account.Credential{}, maildomain.Checkpoint{}, mapAccountError(err)
	}
	inbox, err := resolveInbox(ctx, state, accountValue.ID)
	if err != nil {
		return account.Account{}, folder.Folder{}, account.Credential{}, maildomain.Checkpoint{}, err
	}
	_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
	if err != nil {
		return account.Account{}, folder.Folder{}, account.Credential{}, maildomain.Checkpoint{}, mapAccountError(err)
	}
	checkpoint, err := state.Mail().Checkpoint(ctx, accountValue.ID, inbox.ID)
	if errors.Is(err, maildomain.ErrCheckpointNotFound) {
		checkpoint = zeroCheckpoint(accountValue.ID, inbox.ID)
	} else if err != nil {
		return account.Account{}, folder.Folder{}, account.Credential{}, maildomain.Checkpoint{}, cli.WrapAppError(cli.ErrorInternal, "cannot read the Inbox checkpoint", false, err)
	}
	return accountValue, inbox, credential, checkpoint, nil
}

func resolveInbox(ctx context.Context, state *store.Store, accountID string) (folder.Folder, error) {
	value, err := state.Folders().Resolve(ctx, accountID, string(folder.KindInbox))
	if err != nil {
		if errors.Is(err, folder.ErrNotFound) {
			return folder.Folder{}, cli.WrapAppError(cli.ErrorConfiguration, "Inbox is not discovered; run `icloud mail folders status` first", false, err)
		}
		return folder.Folder{}, mapFolderError(err)
	}
	if !value.Present || value.Stale {
		return folder.Folder{}, cli.WrapAppError(cli.ErrorConflict, "Inbox is missing from the latest folder discovery; refresh folder status", false, nil)
	}
	if !value.Selectable {
		return folder.Folder{}, cli.WrapAppError(cli.ErrorConflict, "Inbox is not selectable at iCloud", false, nil)
	}
	if !value.SyncEnabled {
		return folder.Folder{}, cli.WrapAppError(cli.ErrorConflict, "Inbox synchronization is disabled; enable it before syncing", false, nil)
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
	return transport.InboxPage{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureConfiguration, false, errors.New("Inbox synchronization unavailable"))
}

func abortInboxPage(page transport.InboxPage) {
	for _, message := range page.Messages {
		if message.Raw != nil {
			_ = message.Raw.Abort()
		}
	}
}

func mapMailTransportError(err error) error {
	failure, ok := transport.AsFailure(err)
	if !ok {
		return cli.WrapAppError(cli.ErrorUnavailable, "Inbox synchronization failed", false, err)
	}
	code := cli.ErrorUnavailable
	switch failure.Kind {
	case transport.FailureAuthentication:
		code = cli.ErrorAuthentication
	case transport.FailureConfiguration:
		code = cli.ErrorConfiguration
	}
	message := "Inbox synchronization failed"
	if failure.Stage != "" {
		message += " at " + string(failure.Stage)
	}
	appErr := cli.WrapAppError(code, message, failure.Retryable, err)
	appErr.Details = map[string]any{"stage": failure.Stage}
	return appErr
}
