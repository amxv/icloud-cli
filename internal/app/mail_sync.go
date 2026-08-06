package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

func (commands accountCommands) MailSync(ctx context.Context, home, accountSelector, folderSelector string, limit int) (account.Account, []folder.Folder, []maildomain.SyncSummary, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, nil, nil, 0, err
	}
	defer state.Close()
	defer unlock()

	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, nil, nil, state.SchemaVersion(), mapAccountError(err)
	}
	folders, err := resolveSyncFolders(ctx, state, accountValue.ID, folderSelector)
	if err != nil {
		return account.Account{}, nil, nil, state.SchemaVersion(), err
	}
	_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
	if err != nil {
		return account.Account{}, nil, nil, state.SchemaVersion(), mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return account.Account{}, nil, nil, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)

	summaries := make([]maildomain.SyncSummary, 0, len(folders))
	completedFolders := make([]folder.Folder, 0, len(folders))
	for _, folderValue := range folders {
		summary, usedUsername, syncErr := commands.syncMailbox(
			ctx, state, paths, accountValue, password, folderValue, limit,
		)
		if syncErr != nil {
			return accountValue, completedFolders, summaries, state.SchemaVersion(), syncErr
		}
		if usedUsername != "" && usedUsername != accountValue.IMAPUsername {
			accountValue, err = state.Accounts().UpdateIMAPUsername(ctx, accountValue.ID, usedUsername, commands.timeNow())
			if err != nil {
				return account.Account{}, completedFolders, summaries, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist the working IMAP username", false, err)
			}
		}
		completedFolders = append(completedFolders, folderValue)
		summaries = append(summaries, summary)
	}
	return accountValue, completedFolders, summaries, state.SchemaVersion(), nil
}

func resolveSyncFolders(ctx context.Context, state *store.Store, accountID, selector string) ([]folder.Folder, error) {
	selector = strings.TrimSpace(selector)
	if selector != "" {
		value, err := state.Folders().Resolve(ctx, accountID, selector)
		if err != nil {
			return nil, mapFolderError(err)
		}
		if err := validateSyncFolder(value); err != nil {
			return nil, err
		}
		return []folder.Folder{value}, nil
	}
	values, _, err := state.Folders().List(ctx, accountID, 1000, false)
	if err != nil {
		return nil, cli.WrapAppError(cli.ErrorInternal, "cannot read enabled mail folders", false, err)
	}
	enabled := make([]folder.Folder, 0, len(values))
	for _, value := range values {
		if value.SyncEnabled && value.Present && !value.Stale && value.Selectable {
			enabled = append(enabled, value)
		}
	}
	if len(enabled) == 0 {
		return nil, cli.WrapAppError(cli.ErrorConfiguration, "no selectable mail folders are enabled; refresh folder status or enable a folder", false, nil)
	}
	return enabled, nil
}

func validateSyncFolder(value folder.Folder) error {
	if !value.Present || value.Stale {
		return cli.WrapAppError(cli.ErrorConflict, "selected folder is missing from the latest provider discovery; refresh folder status", false, nil)
	}
	if !value.Selectable {
		return cli.WrapAppError(cli.ErrorConflict, "selected folder is not selectable at iCloud", false, nil)
	}
	if !value.SyncEnabled {
		return cli.WrapAppError(cli.ErrorConflict, "selected folder synchronization is disabled; enable it before syncing", false, nil)
	}
	return nil
}

func (commands accountCommands) syncMailbox(
	ctx context.Context,
	state *store.Store,
	paths config.AppPaths,
	accountValue account.Account,
	password []byte,
	folderValue folder.Folder,
	limit int,
) (maildomain.SyncSummary, string, error) {
	checkpoint, err := state.Mail().Checkpoint(ctx, accountValue.ID, folderValue.ID)
	if errors.Is(err, maildomain.ErrCheckpointNotFound) {
		checkpoint = zeroCheckpoint(accountValue.ID, folderValue.ID)
	} else if err != nil {
		return maildomain.SyncSummary{}, "", cli.WrapAppError(cli.ErrorInternal, "cannot read the folder synchronization checkpoint", false, err)
	}
	if checkpoint.State == maildomain.CheckpointRepairRequired {
		return maildomain.SyncSummary{}, "", cli.WrapAppError(cli.ErrorConflict, "folder identity changed at iCloud; run mail sync repair before continuing", false, maildomain.ErrRepairRequired)
	}
	var reconcileUIDs []uint32
	if checkpoint.UIDValidity != 0 {
		reconcileUIDs, err = state.Mail().ReconcileUIDs(ctx, accountValue.ID, folderValue.ID, checkpoint.UIDValidity, 100)
		if err != nil {
			return maildomain.SyncSummary{}, "", cli.WrapAppError(cli.ErrorInternal, "cannot select bounded folder locations for state reconciliation", false, err)
		}
	}
	checkpointID := checkpoint.ID
	if checkpointID == "" {
		checkpointID, err = commands.generateID("chk")
		if err != nil {
			return maildomain.SyncSummary{}, "", cli.WrapAppError(cli.ErrorInternal, "cannot allocate a synchronization checkpoint ID", false, err)
		}
	}

	attemptedAt := commands.timeNow()
	stager := commands.blobStager(paths)
	attachmentSet := newStagedAttachmentSet(commands.attachmentBlobStore(paths))
	defer attachmentSet.Abort()

	var (
		page         transport.InboxPage
		usedUsername string
		lastErr      error
	)
	for _, username := range imapUsernameCandidates(accountValue) {
		page, lastErr = commands.inboxClient().FetchInboxPage(ctx, transport.AuthRequest{Username: username, Password: password}, transport.InboxFetchRequest{
			Mailbox:             folderValue.WireName,
			ExpectedUIDValidity: checkpoint.UIDValidity,
			LastCommittedUID:    checkpoint.LastCommittedUID,
			LastScannedUID:      checkpoint.LastScannedUID,
			Bootstrap:           !checkpoint.BootstrapComplete,
			TailOnlyBootstrap:   !checkpoint.BootstrapComplete && folderValue.Kind == folder.KindSent,
			Limit:               limit,
			ReconcileUIDs:       reconcileUIDs,
		}, stager)
		if lastErr == nil {
			usedUsername = username
			break
		}
		var validity *transport.UIDValidityError
		if errors.As(lastErr, &validity) {
			_, _ = state.Mail().RequireRepair(ctx, checkpointID, accountValue.ID, folderValue.ID, validity.Observed, attemptedAt)
			return maildomain.SyncSummary{}, "", cli.WrapAppError(cli.ErrorConflict, "folder identity changed at iCloud; synchronization stopped before changing local messages", false, lastErr)
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
		_, _ = state.Mail().RecordFailure(ctx, checkpointID, accountValue.ID, folderValue.ID, checkpoint.UIDValidity, failureCode, attemptedAt)
		return maildomain.SyncSummary{}, "", mapMailboxTransportError(lastErr, folderValue.Name)
	}
	defer abortInboxPage(page)

	records, prepareErr := commands.prepareFetchedMailRecords(ctx, accountValue, folderValue, page.UIDValidity, page.Messages, attachmentSet)
	if prepareErr != nil {
		failureCode := "message_prepare"
		var preparation *fetchedPreparationError
		if errors.As(prepareErr, &preparation) {
			failureCode = preparation.code
		}
		_, _ = state.Mail().RecordFailure(ctx, checkpointID, accountValue.ID, folderValue.ID, checkpoint.UIDValidity, failureCode, attemptedAt)
		if preparation != nil {
			return maildomain.SyncSummary{}, "", preparation.appError()
		}
		return maildomain.SyncSummary{}, "", cli.WrapAppError(cli.ErrorUnavailable, "cannot safely prepare a mail message", false, prepareErr)
	}
	reconciled := make([]maildomain.ReconcileRecord, 0, len(page.LocationStates))
	for _, remote := range page.LocationStates {
		reconciled = append(reconciled, maildomain.ReconcileRecord{
			UID: remote.UID, Flags: maildomain.ParseLocationFlags(remote.Flags),
			ModSequence: remote.ModSequence, Present: remote.Present,
		})
	}
	for _, fetched := range page.Messages {
		if err := fetched.Raw.Commit(); err != nil {
			_, _ = state.Mail().RecordFailure(ctx, checkpointID, accountValue.ID, folderValue.ID, checkpoint.UIDValidity, "blob_commit", attemptedAt)
			return maildomain.SyncSummary{}, "", cli.WrapAppError(cli.ErrorInternal, "cannot commit a staged mail message", true, err)
		}
	}
	if err := attachmentSet.Commit(); err != nil {
		_, _ = state.Mail().RecordFailure(ctx, checkpointID, accountValue.ID, folderValue.ID, checkpoint.UIDValidity, "attachment_blob_commit", attemptedAt)
		return maildomain.SyncSummary{}, "", cli.WrapAppError(cli.ErrorInternal, "cannot commit a decoded mail attachment", true, err)
	}
	commit, err := state.Mail().CommitInboxPage(ctx, maildomain.CommitPage{
		CheckpointID: checkpointID, AccountID: accountValue.ID, FolderID: folderValue.ID,
		UIDValidity: page.UIDValidity, Bootstrap: page.Bootstrap,
		LastCommittedUID: page.LastCommittedUID, LastScannedUID: page.LastScannedUID,
		MoreAvailable: page.MoreAvailable, AttemptedAt: attemptedAt,
		Records: records, Reconciled: reconciled,
	})
	if err != nil {
		_, _ = state.Mail().RecordFailure(ctx, checkpointID, accountValue.ID, folderValue.ID, checkpoint.UIDValidity, "transaction", attemptedAt)
		return maildomain.SyncSummary{}, "", cli.WrapAppError(cli.ErrorInternal, "cannot atomically commit the folder page", true, err)
	}
	if commit.Created+commit.Updated != len(records) {
		return maildomain.SyncSummary{}, "", cli.WrapAppError(
			cli.ErrorInternal,
			"mail synchronization committed an inconsistent message count",
			false,
			fmt.Errorf("records=%d created=%d updated=%d", len(records), commit.Created, commit.Updated),
		)
	}
	return maildomain.SyncSummary{
		AccountID: accountValue.ID, FolderID: folderValue.ID, FolderName: folderValue.Name,
		Fetched: len(records), Created: commit.Created, Updated: commit.Updated,
		Reconciled: commit.Reconciled, StateChanges: commit.StateChanges,
		IdentityMerges: commit.IdentityMerges, DedupeConflicts: commit.DedupeConflicts,
		LastUID: commit.Checkpoint.LastCommittedUID, MoreAvailable: commit.Checkpoint.MoreAvailable,
		Bootstrap: page.Bootstrap, CompletedAt: attemptedAt, Checkpoint: commit.Checkpoint,
	}, usedUsername, nil
}

func messageDirection(accountValue account.Account, folderValue folder.Folder, parsed maildomain.ParsedMessage) maildomain.Direction {
	if folderValue.Kind == folder.KindSent || strings.EqualFold(strings.TrimSpace(parsed.From.Email), strings.TrimSpace(accountValue.Email)) {
		return maildomain.DirectionOutbound
	}
	return maildomain.DirectionInbound
}

func mapMailboxTransportError(err error, folderName string) error {
	failure, ok := transport.AsFailure(err)
	if !ok {
		return cli.WrapAppError(cli.ErrorUnavailable, "mail synchronization failed", false, err)
	}
	code := cli.ErrorUnavailable
	switch failure.Kind {
	case transport.FailureAuthentication:
		code = cli.ErrorAuthentication
	case transport.FailureConfiguration:
		code = cli.ErrorConfiguration
	}
	message := "mail synchronization failed"
	if name := strings.TrimSpace(folderName); name != "" {
		message += " for " + name
	}
	if failure.Stage != "" {
		message += " at " + string(failure.Stage)
	}
	appErr := cli.WrapAppError(code, message, failure.Retryable, err)
	appErr.Details = map[string]any{"stage": failure.Stage}
	return appErr
}
