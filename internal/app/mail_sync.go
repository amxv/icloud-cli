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

const syncFolderSchedulePageSize = 100

func (commands accountCommands) MailSync(ctx context.Context, home, accountSelector, folderSelector string, limit int) (account.Account, []folder.Folder, []maildomain.SyncSummary, int64, error) {
	return commands.mailSync(ctx, home, accountSelector, folderSelector, limit, false, false)
}

func (commands accountCommands) MailSyncMode(ctx context.Context, home, accountSelector, folderSelector string, limit int, continueOnError bool) (account.Account, []folder.Folder, []maildomain.SyncSummary, int64, error) {
	return commands.mailSync(ctx, home, accountSelector, folderSelector, limit, false, continueOnError)
}

func (commands accountCommands) MailSyncAll(ctx context.Context, home, accountSelector string, limit int) (account.Account, []folder.Folder, []maildomain.SyncSummary, int64, error) {
	return commands.mailSync(ctx, home, accountSelector, "", limit, true, false)
}

func (commands accountCommands) MailSyncAllMode(ctx context.Context, home, accountSelector string, limit int, continueOnError bool) (account.Account, []folder.Folder, []maildomain.SyncSummary, int64, error) {
	return commands.mailSync(ctx, home, accountSelector, "", limit, true, continueOnError)
}

func (commands accountCommands) mailSync(ctx context.Context, home, accountSelector, folderSelector string, limit int, allFolders, continueOnError bool) (account.Account, []folder.Folder, []maildomain.SyncSummary, int64, error) {
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
	selector := strings.TrimSpace(folderSelector)
	var selectedFolder *folder.Folder
	requestedCount := 0
	if selector != "" {
		value, resolveErr := state.Folders().Resolve(ctx, accountValue.ID, selector)
		if resolveErr != nil {
			return account.Account{}, nil, nil, state.SchemaVersion(), mapFolderError(resolveErr)
		}
		if allFolders {
			return account.Account{}, nil, nil, state.SchemaVersion(), cli.NewAppError(cli.ErrorUsage, "--all-folders cannot be combined with --folder", false)
		}
		if err := validateSyncFolder(value); err != nil {
			return account.Account{}, nil, nil, state.SchemaVersion(), err
		}
		selectedFolder = &value
		requestedCount = 1
	} else {
		requestedCount, err = state.Folders().CountSelectable(ctx, accountValue.ID, !allFolders)
		if err != nil {
			return account.Account{}, nil, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot count schedulable mail folders", false, err)
		}
		if requestedCount == 0 {
			if allFolders {
				return account.Account{}, nil, nil, state.SchemaVersion(), cli.NewAppError(cli.ErrorConfiguration, "no current selectable mail folders are available; refresh folder status", false)
			}
			return account.Account{}, nil, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConfiguration, "no selectable mail folders are enabled; refresh folder status or enable a folder", false, nil)
		}
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

	summaries := make([]maildomain.SyncSummary, 0, min(requestedCount, syncFolderSchedulePageSize))
	completedFolders := make([]folder.Folder, 0, min(requestedCount, syncFolderSchedulePageSize))
	failures := make([]map[string]any, 0)
	attempts := make([]map[string]any, 0, min(requestedCount, syncFolderSchedulePageSize))
	var firstFailure *cli.AppError
	var stopErr error
	processFolder := func(index int, folderValue folder.Folder) bool {
		summary, usedUsername, syncErr := commands.syncMailbox(
			ctx, state, paths, accountValue, password, folderValue, limit,
		)
		if syncErr != nil {
			normalized := cli.NormalizeError(syncErr)
			committed := summary.FolderID != ""
			if committed {
				completedFolders = append(completedFolders, folderValue)
				summaries = append(summaries, summary)
			}
			if firstFailure == nil {
				firstFailure = normalized
			}
			failure := map[string]any{"index": index, "folder_id": folderValue.ID, "folder_name": folderValue.Name, "error": normalized, "committed": committed}
			if committed {
				failure["summary"] = summary
			}
			failures = append(failures, failure)
			attempts = append(attempts, failure)
			if !continueOnError {
				stopErr = syncPartialError(normalized, requestedCount, completedFolders, summaries, failures, attempts)
				return false
			}
			return true
		}
		completedFolders = append(completedFolders, folderValue)
		summaries = append(summaries, summary)
		attempt := map[string]any{"index": index, "folder_id": folderValue.ID, "folder_name": folderValue.Name, "summary": summary, "committed": true}
		if usedUsername != "" && usedUsername != accountValue.IMAPUsername {
			accountValue, err = state.Accounts().UpdateIMAPUsername(ctx, accountValue.ID, usedUsername, commands.timeNow())
			if err != nil {
				normalized := cli.NormalizeError(cli.WrapAppError(cli.ErrorInternal, "folder synchronization committed but the working IMAP username could not be persisted", false, err))
				if firstFailure == nil {
					firstFailure = normalized
				}
				attempt["error"] = normalized
				failure := map[string]any{"index": index, "folder_id": folderValue.ID, "folder_name": folderValue.Name, "error": normalized, "committed": true, "summary": summary}
				failures = append(failures, failure)
				attempts = append(attempts, attempt)
				if !continueOnError {
					stopErr = syncPartialError(normalized, requestedCount, completedFolders, summaries, failures, attempts)
					return false
				}
				return true
			}
		}
		attempts = append(attempts, attempt)
		return true
	}

	if selectedFolder != nil {
		processFolder(0, *selectedFolder)
	} else {
		cursor := ""
		index := 0
		for {
			var values []folder.Folder
			var more bool
			var next string
			if allFolders {
				values, more, next, err = state.Folders().ListSelectablePage(ctx, accountValue.ID, syncFolderSchedulePageSize, cursor)
			} else {
				values, more, next, err = state.Folders().ListSyncEnabledPage(ctx, accountValue.ID, syncFolderSchedulePageSize, cursor)
			}
			if err != nil {
				return accountValue, completedFolders, summaries, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot page schedulable mail folders", false, err)
			}
			for _, folderValue := range values {
				if !processFolder(index, folderValue) {
					break
				}
				index++
			}
			if stopErr != nil || !more {
				break
			}
			if next == "" || next == cursor {
				return accountValue, completedFolders, summaries, state.SchemaVersion(), cli.NewAppError(cli.ErrorInternal, "folder scheduling pagination did not advance", false)
			}
			cursor = next
		}
	}
	if stopErr != nil {
		return accountValue, completedFolders, summaries, state.SchemaVersion(), stopErr
	}
	if firstFailure != nil {
		return accountValue, completedFolders, summaries, state.SchemaVersion(), syncPartialError(firstFailure, requestedCount, completedFolders, summaries, failures, attempts)
	}
	return accountValue, completedFolders, summaries, state.SchemaVersion(), nil
}

func syncPartialError(first *cli.AppError, requested int, completed []folder.Folder, summaries []maildomain.SyncSummary, failures, attempts []map[string]any) error {
	if first == nil {
		first = cli.NewAppError(cli.ErrorInternal, "multi-folder synchronization failed", false)
	}
	copyErr := *first
	if len(completed) > 0 {
		copyErr.Message = "multi-folder synchronization partially completed; ordered attempted-folder results are reported in error details"
	}
	copyErr.Details = map[string]any{
		"requested":           requested,
		"completed_count":     len(completed),
		"completed_folders":   append([]folder.Folder(nil), completed...),
		"completed_summaries": append([]maildomain.SyncSummary(nil), summaries...),
		"failures":            failures,
		"items":               attempts,
		"partial":             len(completed) > 0,
	}
	return &copyErr
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
	semanticKind, err := semanticFolderKind(ctx, state, accountValue.ID, folderValue)
	if err != nil {
		return maildomain.SyncSummary{}, "", err
	}
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
			Mailbox:             folderValue.Name,
			ExpectedUIDValidity: checkpoint.UIDValidity,
			LastCommittedUID:    checkpoint.LastCommittedUID,
			LastScannedUID:      checkpoint.LastScannedUID,
			Bootstrap:           !checkpoint.BootstrapComplete,
			TailOnlyBootstrap:   !checkpoint.BootstrapComplete && semanticKind == folder.KindSent,
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

	records, prepareErr := commands.prepareFetchedMailRecords(ctx, state, accountValue, folderValue, page.UIDValidity, page.Messages, attachmentSet)
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
	summary := maildomain.SyncSummary{
		AccountID: accountValue.ID, FolderID: folderValue.ID, FolderName: folderValue.Name,
		Fetched: len(records), Created: commit.Created, Updated: commit.Updated,
		Reconciled: commit.Reconciled, StateChanges: commit.StateChanges,
		IdentityMerges: commit.IdentityMerges, DedupeConflicts: commit.DedupeConflicts,
		LastUID: commit.Checkpoint.LastCommittedUID, MoreAvailable: commit.Checkpoint.MoreAvailable,
		Bootstrap: page.Bootstrap, CompletedAt: attemptedAt, Checkpoint: commit.Checkpoint,
	}
	if commit.Created+commit.Updated != len(records) {
		return summary, usedUsername, cli.WrapAppError(
			cli.ErrorInternal,
			"mail synchronization committed an inconsistent message count",
			false,
			fmt.Errorf("records=%d created=%d updated=%d", len(records), commit.Created, commit.Updated),
		)
	}
	var outboundReconcile *maildomain.OutboundReconcileSummary
	if semanticKind == folder.KindSent {
		outboundSummary, reconcileErr := state.Outbound().Reconcile(ctx, accountValue.ID, 100, attemptedAt)
		if reconcileErr != nil {
			return summary, usedUsername, cli.WrapAppError(cli.ErrorInternal, "Sent mail committed but outbound reconciliation failed", true, reconcileErr)
		}
		outboundReconcile = &outboundSummary
	}
	summary.OutboundReconcile = outboundReconcile
	return summary, usedUsername, nil
}

func messageDirection(selfAddresses map[string]struct{}, folderValue folder.Folder, parsed maildomain.ParsedMessage, flags maildomain.LocationFlags) maildomain.Direction {
	_, fromSelf := selfAddresses[strings.ToLower(strings.TrimSpace(parsed.From.Email))]
	if folderValue.Kind == folder.KindSent || folderValue.Kind == folder.KindDrafts || flags.Draft || fromSelf {
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
