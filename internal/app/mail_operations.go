package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

func (commands accountCommands) MailBackfill(ctx context.Context, home, accountSelector, folderSelector string, after, before *time.Time, limit int) (account.Account, folder.Folder, maildomain.BackfillSummary, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), mapAccountError(err)
	}
	folderValue, err := resolveMailFolder(ctx, state, accountValue.ID, folderSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), err
	}
	if after != nil && before != nil && !after.Before(*before) {
		return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorUsage, "backfill --after must precede --before", false)
	}

	frontier, frontierErr := state.Mail().BackfillFrontier(ctx, accountValue.ID, folderValue.ID, after, before)
	if frontierErr == nil && frontier.Complete {
		return accountValue, folderValue, maildomain.BackfillSummary{AccountID: accountValue.ID, FolderID: folderValue.ID, FolderName: folderValue.Name, StartBeforeUID: frontier.BeforeUID, NextBeforeUID: frontier.BeforeUID, Complete: true, Frontier: frontier}, state.SchemaVersion(), nil
	}
	if frontierErr != nil && !errors.Is(frontierErr, maildomain.ErrBackfillFrontierNotFound) {
		return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read historical backfill frontier", false, frontierErr)
	}
	checkpoint, checkpointErr := state.Mail().Checkpoint(ctx, accountValue.ID, folderValue.ID)
	if checkpointErr == nil && checkpoint.State == maildomain.CheckpointRepairRequired {
		return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "folder identity changed at iCloud; repair or rebaseline the forward frontier before historical backfill", false, maildomain.ErrRepairRequired)
	}
	if checkpointErr != nil && !errors.Is(checkpointErr, maildomain.ErrCheckpointNotFound) {
		return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read the forward synchronization checkpoint", false, checkpointErr)
	}

	_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)

	expectedValidity, beforeUID := uint32(0), uint32(0)
	frontierID := ""
	if frontierErr == nil {
		expectedValidity, beforeUID, frontierID = frontier.UIDValidity, frontier.BeforeUID, frontier.ID
	} else if checkpointErr == nil {
		expectedValidity = checkpoint.UIDValidity
		beforeUID = checkpoint.LastScannedUID
		if beforeUID < ^uint32(0) {
			beforeUID++
		}
	}
	if frontierID == "" {
		frontierID, err = commands.generateID("bf")
		if err != nil {
			return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate a backfill frontier ID", false, err)
		}
	}
	attemptedAt := commands.timeNow()
	stager := commands.blobStager(paths)
	page, usedUsername, err := commands.fetchBackfillPage(ctx, accountValue, password, transport.BackfillFetchRequest{Mailbox: folderValue.Name, ExpectedUIDValidity: expectedValidity, BeforeUID: beforeUID, After: after, Before: before, Limit: limit}, stager)
	if err != nil {
		failureCode := "imap_fetch"
		if failure, ok := transport.AsFailure(err); ok && failure.Stage != "" {
			failureCode = string(failure.Stage)
		}
		var validity *transport.UIDValidityError
		if errors.As(err, &validity) {
			failureCode = "uid_validity_changed"
			if checkpointErr == nil {
				if _, repairErr := state.Mail().RequireRepair(ctx, checkpoint.ID, accountValue.ID, folderValue.ID, validity.Observed, attemptedAt); repairErr != nil {
					if frontierErr == nil {
						_ = state.Mail().RecordBackfillFailure(ctx, frontier.ID, accountValue.ID, folderValue.ID, after, before, frontier.UIDValidity, frontier.BeforeUID, failureCode, attemptedAt)
					}
					return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot mark the changed mailbox identity for repair", false, repairErr)
				}
			}
		}
		if frontierErr == nil {
			_ = state.Mail().RecordBackfillFailure(ctx, frontier.ID, accountValue.ID, folderValue.ID, after, before, frontier.UIDValidity, frontier.BeforeUID, failureCode, attemptedAt)
		}
		if validity != nil {
			return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "folder identity changed at iCloud; run mail sync repair before resuming historical backfill", false, err)
		}
		return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), mapMailboxTransportError(err, folderValue.Name)
	}
	defer abortBackfillPage(page)
	if usedUsername != "" && usedUsername != accountValue.IMAPUsername {
		accountValue, err = state.Accounts().UpdateIMAPUsername(ctx, accountValue.ID, usedUsername, commands.timeNow())
		if err != nil {
			return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist the working IMAP username", false, err)
		}
	}
	attachmentSet := newStagedAttachmentSet(commands.attachmentBlobStore(paths))
	defer attachmentSet.Abort()
	records, err := commands.prepareFetchedMailRecords(ctx, state, accountValue, folderValue, page.UIDValidity, page.Messages, attachmentSet)
	if err != nil {
		_ = state.Mail().RecordBackfillFailure(ctx, frontierID, accountValue.ID, folderValue.ID, after, before, page.UIDValidity, page.BeforeUID, "message_prepare", attemptedAt)
		var preparation *fetchedPreparationError
		if errors.As(err, &preparation) {
			return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), preparation.appError()
		}
		return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorUnavailable, "cannot safely prepare a historical mail message", false, err)
	}
	for _, fetched := range page.Messages {
		if err := fetched.Raw.Commit(); err != nil {
			_ = state.Mail().RecordBackfillFailure(ctx, frontierID, accountValue.ID, folderValue.ID, after, before, page.UIDValidity, page.BeforeUID, "blob_commit", attemptedAt)
			return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot commit a historical raw message", true, err)
		}
	}
	if err := attachmentSet.Commit(); err != nil {
		_ = state.Mail().RecordBackfillFailure(ctx, frontierID, accountValue.ID, folderValue.ID, after, before, page.UIDValidity, page.BeforeUID, "attachment_blob_commit", attemptedAt)
		return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot commit a historical attachment", true, err)
	}
	commit, err := state.Mail().CommitBackfillPage(ctx, maildomain.CommitBackfillPage{FrontierID: frontierID, AccountID: accountValue.ID, FolderID: folderValue.ID, UIDValidity: page.UIDValidity, StartBeforeUID: page.BeforeUID, NextBeforeUID: page.NextBeforeUID, After: after, Before: before, Complete: page.Complete, AttemptedAt: attemptedAt, Records: records})
	if err != nil {
		_ = state.Mail().RecordBackfillFailure(ctx, frontierID, accountValue.ID, folderValue.ID, after, before, page.UIDValidity, page.BeforeUID, "transaction", attemptedAt)
		code := cli.ErrorInternal
		if errors.Is(err, maildomain.ErrRepairRequired) {
			code = cli.ErrorConflict
		}
		return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, state.SchemaVersion(), cli.WrapAppError(code, "cannot atomically commit the historical mail page", true, err)
	}
	return accountValue, folderValue, maildomain.BackfillSummary{AccountID: accountValue.ID, FolderID: folderValue.ID, FolderName: folderValue.Name, Fetched: len(records), Created: commit.Created, Updated: commit.Updated, IdentityMerges: commit.IdentityMerges, DedupeConflicts: commit.DedupeConflicts, StartBeforeUID: page.BeforeUID, NextBeforeUID: page.NextBeforeUID, Complete: page.Complete, Frontier: commit.Frontier}, state.SchemaVersion(), nil
}

func (commands accountCommands) MailRebaseline(ctx context.Context, home, accountSelector, folderSelector string, currentTail bool) (account.Account, folder.Folder, maildomain.RebaselineSummary, int64, error) {
	if !currentTail {
		return account.Account{}, folder.Folder{}, maildomain.RebaselineSummary{}, 0, cli.NewAppError(cli.ErrorSafety, "rebaseline requires --current-tail", false)
	}
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.RebaselineSummary{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.RebaselineSummary{}, state.SchemaVersion(), mapAccountError(err)
	}
	folderValue, err := resolveMailFolder(ctx, state, accountValue.ID, folderSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.RebaselineSummary{}, state.SchemaVersion(), err
	}
	beforeCheckpoint, checkpointErr := state.Mail().Checkpoint(ctx, accountValue.ID, folderValue.ID)
	if errors.Is(checkpointErr, maildomain.ErrCheckpointNotFound) {
		beforeCheckpoint = zeroCheckpoint(accountValue.ID, folderValue.ID)
	} else if checkpointErr != nil {
		return account.Account{}, folder.Folder{}, maildomain.RebaselineSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read the synchronization checkpoint before rebaseline", false, checkpointErr)
	}
	_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.RebaselineSummary{}, state.SchemaVersion(), mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.RebaselineSummary{}, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)
	status, usedUsername, err := commands.fetchMailboxStatus(ctx, accountValue, password, folderValue.Name)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.RebaselineSummary{}, state.SchemaVersion(), mapMailboxTransportError(err, folderValue.Name)
	}
	if usedUsername != "" && usedUsername != accountValue.IMAPUsername {
		accountValue, err = state.Accounts().UpdateIMAPUsername(ctx, accountValue.ID, usedUsername, commands.timeNow())
		if err != nil {
			return account.Account{}, folder.Folder{}, maildomain.RebaselineSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist the working IMAP username", false, err)
		}
	}
	checkpointID := beforeCheckpoint.ID
	if checkpointID == "" {
		checkpointID, err = commands.generateID("chk")
		if err != nil {
			return account.Account{}, folder.Folder{}, maildomain.RebaselineSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate a synchronization checkpoint ID", false, err)
		}
	}
	cached, err := state.Mail().CachedMessageCount(ctx, accountValue.ID, folderValue.ID)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.RebaselineSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot count cached messages before rebaseline", false, err)
	}
	tail := providerTail(status.UIDNext)
	afterCheckpoint, err := state.Mail().RebaselineCheckpoint(ctx, checkpointID, accountValue.ID, folderValue.ID, status.UIDValidity, tail, commands.timeNow())
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.RebaselineSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot rebaseline the synchronization checkpoint", false, err)
	}
	return accountValue, folderValue, maildomain.RebaselineSummary{AccountID: accountValue.ID, FolderID: folderValue.ID, FolderName: folderValue.Name, UIDValidity: status.UIDValidity, PreviousCommitted: beforeCheckpoint.LastCommittedUID, PreviousScanned: beforeCheckpoint.LastScannedUID, PreviousState: beforeCheckpoint.State, RebaselineTail: tail, CachedMessagesKept: cached, Before: beforeCheckpoint, After: afterCheckpoint}, state.SchemaVersion(), nil
}

func (commands accountCommands) MailRepair(ctx context.Context, home, accountSelector string, options maildomain.RepairOptions) (account.Account, maildomain.RepairSummary, int64, error) {
	if options.Limit < 1 || options.Limit > 100 {
		return account.Account{}, maildomain.RepairSummary{}, 0, cli.NewAppError(cli.ErrorUsage, "repair limit must be between 1 and 100", false)
	}
	if options.Mode != maildomain.RepairCheck && options.Mode != maildomain.RepairApply {
		return account.Account{}, maildomain.RepairSummary{}, 0, cli.NewAppError(cli.ErrorUsage, "repair mode must be check or apply", false)
	}
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.RepairSummary{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), mapAccountError(err)
	}

	audit, err := state.AuditMailRepair(ctx, accountValue.ID, options.Limit)
	if err != nil {
		return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot audit local mail state", false, err)
	}
	changed := 0
	threadProjectionAdvanced := 0
	threadProjectionRepaired := 0
	if options.Mode == maildomain.RepairApply {
		advanced, _, projectionErr := state.AdvanceThreadGraphBackfill(ctx, options.Limit)
		if projectionErr != nil {
			return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot resume the bounded thread backfill frontier", false, projectionErr)
		}
		threadProjectionAdvanced += advanced
		projectionRepair, projectionErr := state.RepairThreadProjection(ctx, accountValue.ID, options.Limit, commands.timeNow())
		if projectionErr != nil {
			return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot resume deferred thread projection", false, projectionErr)
		}
		threadProjectionAdvanced += projectionRepair.Advanced
		threadProjectionRepaired += projectionRepair.Completed
		changed += projectionRepair.Completed
		if repaired, err := state.Search().RefreshMetadata(ctx, accountValue.ID, audit.StaleSearchMessageIDs, commands.timeNow()); err != nil {
			return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot refresh stale local search metadata", false, err)
		} else {
			changed += repaired
		}
		searchSummary, err := commands.repairSearchBodies(ctx, state, paths, accountValue, options.Limit)
		if err != nil {
			return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), err
		}
		changed += searchSummary.Repaired
		outbound, err := state.Outbound().Reconcile(ctx, accountValue.ID, options.Limit, commands.timeNow())
		if err != nil {
			return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reconcile local outbound evidence", false, err)
		}
		changed += outbound.Confirmed + outbound.ResolvedUnknown
		var password []byte
		providerStatuses := make(map[string]transport.MailboxStatus)
		needsProviderTruth := len(audit.RepairCheckpoints) > 0 || len(audit.BackfillResets) > 0
		if needsProviderTruth {
			_, credential, credentialErr := state.Accounts().Credential(ctx, accountValue.ID)
			if credentialErr != nil {
				return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), mapAccountError(credentialErr)
			}
			password, credentialErr = secret.NewManager(commands.keyring).Resolve(ctx, credential)
			if credentialErr != nil {
				return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), mapCredentialError(credentialErr)
			}
			defer zeroBytes(password)
		}
		for _, checkpoint := range audit.RepairCheckpoints {
			folderValue, resolveErr := state.Folders().Resolve(ctx, accountValue.ID, checkpoint.FolderID)
			if resolveErr != nil || !folderValue.Present || folderValue.Stale || !folderValue.Selectable {
				continue
			}
			status, usedUsername, statusErr := commands.fetchMailboxStatus(ctx, accountValue, password, folderValue.Name)
			if statusErr != nil {
				return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), mapMailboxTransportError(statusErr, folderValue.Name)
			}
			providerStatuses[folderValue.ID] = status
			if _, rebaselineErr := state.Mail().RebaselineCheckpoint(ctx, checkpoint.ID, accountValue.ID, folderValue.ID, status.UIDValidity, providerTail(status.UIDNext), commands.timeNow()); rebaselineErr != nil {
				return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot repair the changed mailbox frontier", false, rebaselineErr)
			}
			changed++
			if usedUsername != "" && usedUsername != accountValue.IMAPUsername {
				accountValue, statusErr = state.Accounts().UpdateIMAPUsername(ctx, accountValue.ID, usedUsername, commands.timeNow())
				if statusErr != nil {
					return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist the working IMAP username", false, statusErr)
				}
			}
		}
		postRebaseline, err := state.AuditMailRepair(ctx, accountValue.ID, options.Limit)
		if err != nil {
			return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot verify repaired frontiers", false, err)
		}
		for _, frontier := range postRebaseline.BackfillResets {
			status, ok := providerStatuses[frontier.FolderID]
			if !ok {
				folderValue, resolveErr := state.Folders().Resolve(ctx, accountValue.ID, frontier.FolderID)
				if resolveErr != nil || !folderValue.Present || folderValue.Stale || !folderValue.Selectable {
					continue
				}
				var usedUsername string
				status, usedUsername, err = commands.fetchMailboxStatus(ctx, accountValue, password, folderValue.Name)
				if err != nil {
					return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), mapMailboxTransportError(err, folderValue.Name)
				}
				providerStatuses[frontier.FolderID] = status
				if usedUsername != "" && usedUsername != accountValue.IMAPUsername {
					accountValue, err = state.Accounts().UpdateIMAPUsername(ctx, accountValue.ID, usedUsername, commands.timeNow())
					if err != nil {
						return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist the working IMAP username", false, err)
					}
				}
			}
			beforeUID := status.UIDNext
			if beforeUID == 0 {
				beforeUID = 1
			}
			if err := state.Mail().ResetBackfillFrontier(ctx, frontier.ID, status.UIDValidity, beforeUID, commands.timeNow()); err != nil {
				return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reset a stale historical frontier", false, err)
			}
			changed++
		}
	}

	orphans, orphanMore, err := orphanStagingFiles(paths.BlobRoot, options.Limit)
	if err != nil {
		return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot inspect temporary blob files", false, err)
	}
	if options.Mode == maildomain.RepairApply && options.CleanupOrphans {
		for _, path := range orphans {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot remove an orphan temporary blob", false, err)
			}
			changed++
		}
		orphans, orphanMore, err = orphanStagingFiles(paths.BlobRoot, options.Limit)
		if err != nil {
			return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), err
		}
	}
	finalAudit := audit
	if options.Mode == maildomain.RepairApply {
		finalAudit, err = state.AuditMailRepair(ctx, accountValue.ID, options.Limit)
		if err != nil {
			return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot verify local mail repair", false, err)
		}
	}
	finalAudit.Summary.Mode = options.Mode
	finalAudit.Summary.Changed = changed
	finalAudit.Summary.ThreadProjectionAdvanced = threadProjectionAdvanced
	finalAudit.Summary.ThreadProjectionRepaired = threadProjectionRepaired
	finalAudit.Summary.OrphanTempFiles = len(orphans)
	if orphanMore {
		finalAudit.Summary.More = true
	}
	if len(orphans) > 0 {
		finalAudit.Summary.CleanupSkipped = len(orphans)
		finalAudit.Summary.Findings = append(finalAudit.Summary.Findings, maildomain.RepairFinding{Kind: "orphan_temp_blob", Detail: fmt.Sprintf("%d temporary blob files are not referenced by an active writer", len(orphans)), Action: "rerun --apply --cleanup-orphans", Applicable: options.Mode == maildomain.RepairApply})
	}
	if err := appendCanonicalBlobFindings(ctx, state, paths, accountValue.ID, options.Limit, &finalAudit.Summary); err != nil {
		return account.Account{}, maildomain.RepairSummary{}, state.SchemaVersion(), err
	}
	finalAudit.Summary.NextAction = repairNextAction(finalAudit.Summary, options)
	return accountValue, finalAudit.Summary, state.SchemaVersion(), nil
}

func (commands accountCommands) fetchBackfillPage(ctx context.Context, accountValue account.Account, password []byte, request transport.BackfillFetchRequest, stager blob.Stager) (transport.BackfillPage, string, error) {
	var lastErr error
	for _, username := range imapUsernameCandidates(accountValue) {
		page, err := commands.backfillClient().FetchBackfillPage(ctx, transport.AuthRequest{Username: username, Password: password}, request, stager)
		if err == nil {
			return page, username, nil
		}
		lastErr = err
		failure, ok := transport.AsFailure(err)
		if !ok || failure.Kind != transport.FailureAuthentication {
			break
		}
	}
	return transport.BackfillPage{}, "", lastErr
}

func (commands accountCommands) fetchMailboxStatus(ctx context.Context, accountValue account.Account, password []byte, mailbox string) (transport.MailboxStatus, string, error) {
	var lastErr error
	for _, username := range imapUsernameCandidates(accountValue) {
		status, err := commands.mailboxStatusClient().MailboxStatus(ctx, transport.AuthRequest{Username: username, Password: password}, mailbox)
		if err == nil {
			return status, username, nil
		}
		lastErr = err
		failure, ok := transport.AsFailure(err)
		if !ok || failure.Kind != transport.FailureAuthentication {
			break
		}
	}
	return transport.MailboxStatus{}, "", lastErr
}

func (commands accountCommands) backfillClient() transport.IMAPBackfillClient {
	if candidate, ok := commands.imap.(transport.IMAPBackfillClient); ok {
		return candidate
	}
	if candidate, ok := commands.mail.(transport.IMAPBackfillClient); ok {
		return candidate
	}
	return unavailableBackfillClient{}
}
func (commands accountCommands) mailboxStatusClient() transport.IMAPMailboxStatusClient {
	if candidate, ok := commands.imap.(transport.IMAPMailboxStatusClient); ok {
		return candidate
	}
	if candidate, ok := commands.mail.(transport.IMAPMailboxStatusClient); ok {
		return candidate
	}
	return unavailableMailboxStatusClient{}
}

type unavailableBackfillClient struct{}

func (unavailableBackfillClient) FetchBackfillPage(context.Context, transport.AuthRequest, transport.BackfillFetchRequest, blob.Stager) (transport.BackfillPage, error) {
	return transport.BackfillPage{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureConfiguration, false, errors.New("historical backfill unavailable"))
}

type unavailableMailboxStatusClient struct{}

func (unavailableMailboxStatusClient) MailboxStatus(context.Context, transport.AuthRequest, string) (transport.MailboxStatus, error) {
	return transport.MailboxStatus{}, transport.NewFailure(transport.StageIMAPStatus, transport.FailureConfiguration, false, errors.New("mailbox status unavailable"))
}

func providerTail(uidNext uint32) uint32 {
	if uidNext <= 1 {
		return 0
	}
	return uidNext - 1
}
func abortBackfillPage(page transport.BackfillPage) {
	for _, message := range page.Messages {
		if message.Raw != nil {
			_ = message.Raw.Abort()
		}
	}
}

func orphanStagingFiles(blobRoot string, limit int) ([]string, bool, error) {
	root := filepath.Join(blobRoot, ".staging")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	paths := make([]string, 0, minOperationInt(limit+1, len(entries)))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".tmp") {
			continue
		}
		paths = append(paths, filepath.Join(root, name))
		if len(paths) > limit {
			break
		}
	}
	more := len(paths) > limit
	if more {
		paths = paths[:limit]
	}
	return paths, more, nil
}

func appendCanonicalBlobFindings(ctx context.Context, state *store.Store, paths config.AppPaths, accountID string, limit int, summary *maildomain.RepairSummary) error {
	refs, more, err := state.MailBlobReferences(ctx, accountID, limit)
	if err != nil {
		return cli.WrapAppError(cli.ErrorInternal, "cannot enumerate canonical mail blobs", false, err)
	}
	if more {
		summary.More = true
	}
	blobStore := blob.NewWithLimits(paths.BlobRoot, paths.InboundLimits.RawMessageBytes, paths.InboundLimits.AttachmentBytes)
	seen := map[string]struct{}{}
	for _, ref := range refs {
		key := ref.StorageKey + "\x00" + ref.ContentHash + fmt.Sprintf("\x00%d", ref.ByteSize)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		reader, openErr := blobStore.OpenVerifiedContext(ctx, ref.StorageKey, ref.ContentHash, ref.ByteSize)
		if openErr == nil {
			openErr = reader.Close()
		}
		if openErr != nil {
			summary.CanonicalBlobIssues++
			summary.Findings = append(summary.Findings, maildomain.RepairFinding{Kind: "canonical_blob", ID: ref.ID, Detail: "saved blob is missing or does not match its recorded content address", Action: "targeted provider recovery or restore from backup", Applicable: false})
		}
	}
	return nil
}

func minOperationInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func repairNextAction(summary maildomain.RepairSummary, options maildomain.RepairOptions) string {
	if summary.CanonicalBlobIssues > 0 || summary.DedupeConflicts > 0 || summary.ForeignKeyProblems > 0 ||
		summary.IntegrityProblems > 0 || summary.LocationProblems > 0 || summary.ThreadRedirectProblems > 0 {
		return "review_verbose_or_json"
	}
	if summary.OrphanTempFiles > 0 {
		if options.Mode == maildomain.RepairApply && !options.CleanupOrphans {
			return "rerun_apply_cleanup_orphans"
		}
		return "run_apply_cleanup_orphans"
	}
	deterministic := summary.PendingSearch + summary.FailedSearch + summary.StaleFTSRows + summary.RepairCheckpoints + summary.FrontierProblems + summary.OutboundUnknown + summary.ThreadProjectionPending
	if summary.ThreadProjectionState != "" && summary.ThreadProjectionState != "ready" {
		deterministic++
	}
	if deterministic > 0 {
		if options.Mode == maildomain.RepairCheck {
			return "run_apply"
		}
		return "rerun_check"
	}
	if summary.More {
		if options.Limit < 100 {
			return "increase_limit"
		}
		return "bounded_check_partial"
	}
	return "none"
}
