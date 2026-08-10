package app

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"

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

type preparedAttachmentRewrite struct {
	account     account.Account
	folder      folder.Folder
	message     maildomain.Message
	source      maildomain.Location
	plan        maildomain.MessageRewritePlan
	content     maildomain.NormalizedContent
	raw         *blob.Staged
	attachments *stagedAttachmentSet
}

func (value *preparedAttachmentRewrite) abort() {
	if value == nil {
		return
	}
	if value.raw != nil {
		_ = value.raw.Abort()
	}
	if value.attachments != nil {
		value.attachments.Abort()
	}
}

func (commands accountCommands) MailAttachmentRemovePreview(ctx context.Context, home, accountSelector string, request maildomain.AttachmentRemoveRequest) (account.Account, folder.Folder, maildomain.MessageRewritePlan, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MessageRewritePlan{}, 0, err
	}
	defer state.Close()
	defer unlock()
	request, err = normalizeAttachmentRemoveRequest(request, false)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MessageRewritePlan{}, state.SchemaVersion(), err
	}
	prepared, err := commands.prepareAttachmentRewrite(ctx, state, paths, accountSelector, request, false, nil)
	if prepared != nil {
		defer prepared.abort()
	}
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MessageRewritePlan{}, state.SchemaVersion(), err
	}
	return prepared.account, prepared.folder, prepared.plan, state.SchemaVersion(), nil
}

func (commands accountCommands) MailAttachmentRemove(ctx context.Context, home, accountSelector string, request maildomain.AttachmentRemoveRequest) (account.Account, folder.Folder, maildomain.MessageRewritePlan, maildomain.MessageRewrite, maildomain.Message, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MessageRewritePlan{}, maildomain.MessageRewrite{}, maildomain.Message{}, 0, err
	}
	defer state.Close()
	defer unlock()
	request, err = normalizeAttachmentRemoveRequest(request, true)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MessageRewritePlan{}, maildomain.MessageRewrite{}, maildomain.Message{}, state.SchemaVersion(), err
	}
	selectedAccount, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MessageRewritePlan{}, maildomain.MessageRewrite{}, maildomain.Message{}, state.SchemaVersion(), mapAccountError(err)
	}
	var existing *maildomain.MessageRewrite
	if request.IdempotencyKey != "" {
		value, lookupErr := state.MessageRewrites().ByIdempotency(ctx, selectedAccount.ID, request.IdempotencyKey)
		switch {
		case lookupErr == nil:
			if err := validateExistingAttachmentRewriteRequest(ctx, state, selectedAccount, request, value); err != nil {
				return selectedAccount, folder.Folder{}, maildomain.MessageRewritePlan{}, value, maildomain.Message{}, state.SchemaVersion(), err
			}
			if value.Status == maildomain.MessageRewriteFailed {
				return selectedAccount, folder.Folder{}, messageRewritePlanFromExisting(value, folder.Folder{}, maildomain.Location{}), value, maildomain.Message{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorConflict, "the prior attachment rewrite failed before provider acceptance; use a new idempotency key for a new reviewed rewrite", false)
			}
			if value.Status == maildomain.MessageRewriteConfirmed {
				folderValue, folderErr := state.Folders().Resolve(ctx, selectedAccount.ID, value.SourceFolderID)
				if folderErr != nil {
					return selectedAccount, folder.Folder{}, maildomain.MessageRewritePlan{}, value, maildomain.Message{}, state.SchemaVersion(), mapFolderError(folderErr)
				}
				source, sourceErr := state.Mail().Location(ctx, selectedAccount.ID, value.SourceLocationID)
				if sourceErr != nil {
					return selectedAccount, folderValue, maildomain.MessageRewritePlan{}, value, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reload the confirmed rewrite source evidence", false, sourceErr)
				}
				message, readErr := state.Mail().Message(ctx, selectedAccount.ID, value.MessageID)
				if readErr != nil {
					return selectedAccount, folderValue, maildomain.MessageRewritePlan{}, value, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reload the rewritten message", false, readErr)
				}
				return selectedAccount, folderValue, messageRewritePlanFromExisting(value, folderValue, source), value, message, state.SchemaVersion(), nil
			}
			existing = &value
		case errors.Is(lookupErr, maildomain.ErrMessageRewriteNotFound):
		// A new reviewed rewrite begins below.
		default:
			return selectedAccount, folder.Folder{}, maildomain.MessageRewritePlan{}, maildomain.MessageRewrite{}, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot inspect prior attachment rewrite recovery state", false, lookupErr)
		}
	}
	prepared, err := commands.prepareAttachmentRewrite(ctx, state, paths, accountSelector, request, true, existing)
	if prepared != nil {
		defer prepared.abort()
	}
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MessageRewritePlan{}, maildomain.MessageRewrite{}, maildomain.Message{}, state.SchemaVersion(), err
	}
	if err := prepared.raw.Commit(); err != nil {
		return prepared.account, prepared.folder, prepared.plan, maildomain.MessageRewrite{}, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot commit rewritten raw content", true, err)
	}
	if err := prepared.attachments.Commit(); err != nil {
		return prepared.account, prepared.folder, prepared.plan, maildomain.MessageRewrite{}, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot commit rewritten attachment content", true, err)
	}
	rewriteID, err := commands.generateID("rew")
	if err != nil {
		return prepared.account, prepared.folder, prepared.plan, maildomain.MessageRewrite{}, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate a message rewrite ID", false, err)
	}
	proposedLocationID, err := commands.generateID("loc")
	if err != nil {
		return prepared.account, prepared.folder, prepared.plan, maildomain.MessageRewrite{}, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate a replacement location ID", false, err)
	}
	now := commands.timeNow()
	rewrite, reused, err := state.MessageRewrites().Begin(ctx, maildomain.BeginMessageRewrite{
		RewriteID: rewriteID, AccountID: prepared.account.ID, MessageID: prepared.message.ID,
		SourceLocationID: prepared.source.ID, SourceFolderID: prepared.folder.ID,
		SourceUIDValidity: prepared.source.UIDValidity, SourceUID: prepared.source.UID, SourceModSequence: prepared.source.ModSequence,
		OldRawContentHash: prepared.source.RawContentHash, OldRawStorageKey: prepared.source.RawStorageKey, OldRawByteSize: prepared.source.RawByteSize,
		NewRawContentHash: prepared.raw.ContentHash, NewRawStorageKey: prepared.raw.StorageKey, NewRawByteSize: prepared.raw.ByteSize,
		BeforeAttachmentCount: prepared.plan.BeforeAttachmentCount, AfterAttachmentCount: prepared.plan.AfterAttachmentCount,
		RemovedAttachments: prepared.plan.RemovedAttachments, ProposedLocationID: proposedLocationID,
		RequestHash: prepared.plan.RequestHash, IdempotencyKey: request.IdempotencyKey, Now: now,
	})
	if err != nil {
		return prepared.account, prepared.folder, prepared.plan, maildomain.MessageRewrite{}, maildomain.Message{}, state.SchemaVersion(), mapMessageRewriteStoreError(err)
	}
	if rewrite.Status == maildomain.MessageRewriteConfirmed {
		message, readErr := state.Mail().Message(ctx, prepared.account.ID, prepared.message.ID)
		if readErr != nil {
			return prepared.account, prepared.folder, prepared.plan, rewrite, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reload the rewritten message", false, readErr)
		}
		return prepared.account, prepared.folder, prepared.plan, rewrite, message, state.SchemaVersion(), nil
	}
	_, credential, err := state.Accounts().Credential(ctx, prepared.account.ID)
	if err != nil {
		return prepared.account, prepared.folder, prepared.plan, rewrite, maildomain.Message{}, state.SchemaVersion(), mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return prepared.account, prepared.folder, prepared.plan, rewrite, maildomain.Message{}, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)

	if rewrite.Status == maildomain.MessageRewritePending {
		rewrite, err = state.MessageRewrites().MarkAppendUnresolved(ctx, rewrite.ID, "append_inflight", commands.timeNow())
		if err != nil {
			return prepared.account, prepared.folder, prepared.plan, rewrite, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot fence replacement APPEND before provider contact", false, err)
		}
		appendResult, appendErr := commands.appendRewriteReplacement(ctx, paths, prepared.account, password, prepared.folder, prepared.source, rewrite)
		if appendErr != nil {
			if definitelyBeforeRewriteAppend(appendErr) {
				rewrite, _ = state.MessageRewrites().MarkFailed(ctx, rewrite.ID, messageRewriteErrorCode(appendErr), commands.timeNow())
				return prepared.account, prepared.folder, prepared.plan, rewrite, maildomain.Message{}, state.SchemaVersion(), mapMailboxTransportError(appendErr, prepared.folder.Name)
			}
			if reconciled, reconcileErr := commands.reconcileRewriteAppend(ctx, state, paths, prepared.account, password, prepared.folder, rewrite); reconcileErr == nil && reconciled.ID != "" {
				rewrite = reconciled
			} else {
				return prepared.account, prepared.folder, prepared.plan, rewrite, maildomain.Message{}, state.SchemaVersion(), withMessageRewriteDetails(cli.WrapAppError(cli.ErrorUnavailable, "replacement APPEND may have been accepted; synchronize the source folder and retry the same idempotency key instead of appending again", false, appendErr), rewrite)
			}
		} else if appendResult.UIDValidity != 0 && appendResult.UID != 0 {
			rewrite, err = state.MessageRewrites().ConfirmAppend(ctx, maildomain.ConfirmRewriteAppend{
				RewriteID: rewrite.ID, LocationID: rewrite.ProposedLocationID, UIDValidity: appendResult.UIDValidity, UID: appendResult.UID,
				ProviderMethod: "append_uid", Now: commands.timeNow(),
			})
			if err != nil {
				return prepared.account, prepared.folder, prepared.plan, rewrite, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "replacement was appended but its provider mapping could not be persisted; retry the same idempotency key", true, err)
			}
		} else {
			rewrite, err = commands.reconcileRewriteAppend(ctx, state, paths, prepared.account, password, prepared.folder, rewrite)
			if err != nil {
				return prepared.account, prepared.folder, prepared.plan, rewrite, maildomain.Message{}, state.SchemaVersion(), withMessageRewriteDetails(err, rewrite)
			}
		}
	} else if reused && rewrite.Status == maildomain.MessageRewriteAppendUnresolved {
		rewrite, err = commands.reconcileRewriteAppend(ctx, state, paths, prepared.account, password, prepared.folder, rewrite)
		if err != nil {
			return prepared.account, prepared.folder, prepared.plan, rewrite, maildomain.Message{}, state.SchemaVersion(), withMessageRewriteDetails(err, rewrite)
		}
	}

	freshDeleteFence := false
	if rewrite.Status == maildomain.MessageRewriteAppended {
		rewrite, err = state.MessageRewrites().MarkDeleteUnresolved(ctx, rewrite.ID, "delete_inflight", commands.timeNow())
		if err != nil {
			return prepared.account, prepared.folder, prepared.plan, rewrite, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot fence source DELETE before provider contact", false, err)
		}
		freshDeleteFence = true
	}
	if rewrite.Status == maildomain.MessageRewriteDeleteUnresolved {
		if freshDeleteFence {
			rewrite, err = commands.deleteRewriteSource(ctx, state, prepared.account, password, prepared.folder, prepared.source, rewrite)
		} else {
			absent, probeErr := commands.rewriteSourceAbsent(ctx, prepared.account, password, prepared.folder, rewrite)
			if probeErr != nil {
				return prepared.account, prepared.folder, prepared.plan, rewrite, maildomain.Message{}, state.SchemaVersion(), withMessageRewriteDetails(probeErr, rewrite)
			}
			if absent {
				rewrite, err = state.MessageRewrites().ConfirmDelete(ctx, maildomain.ConfirmRewriteDelete{RewriteID: rewrite.ID, SourceFlags: prepared.source.Flags, SourceModSequence: prepared.source.ModSequence, ProviderMethod: "delete_reconciled_absent", Now: commands.timeNow()})
			} else {
				rewrite, err = commands.deleteRewriteSource(ctx, state, prepared.account, password, prepared.folder, prepared.source, rewrite)
			}
		}
		if err != nil {
			return prepared.account, prepared.folder, prepared.plan, rewrite, maildomain.Message{}, state.SchemaVersion(), withMessageRewriteDetails(err, rewrite)
		}
	}
	if rewrite.Status == maildomain.MessageRewriteCommitPending {
		internalDate := commands.timeNow()
		if prepared.source.InternalDate != nil {
			internalDate = *prepared.source.InternalDate
		} else if prepared.message.InternalDate != nil {
			internalDate = *prepared.message.InternalDate
		}
		rewrite, err = state.MessageRewrites().Finalize(ctx, maildomain.FinalizeMessageRewrite{
			RewriteID: rewrite.ID, Content: prepared.content, Parsed: prepared.content.Preview,
			Direction: prepared.message.Direction, InternalDate: internalDate, Now: commands.timeNow(),
		})
		if err != nil {
			return prepared.account, prepared.folder, prepared.plan, rewrite, maildomain.Message{}, state.SchemaVersion(), withMessageRewriteDetails(cli.WrapAppError(cli.ErrorInternal, "provider replacement is complete but canonical content finalization failed; retry the same idempotency key", true, err), rewrite)
		}
	}
	message, err := state.Mail().Message(ctx, prepared.account.ID, prepared.message.ID)
	if err != nil {
		return prepared.account, prepared.folder, prepared.plan, rewrite, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reload the rewritten message", false, err)
	}
	return prepared.account, prepared.folder, prepared.plan, rewrite, message, state.SchemaVersion(), nil
}

func normalizeAttachmentRemoveRequest(request maildomain.AttachmentRemoveRequest, requireIdempotency bool) (maildomain.AttachmentRemoveRequest, error) {
	request.MessageID = strings.TrimSpace(request.MessageID)
	request.Folder = strings.TrimSpace(request.Folder)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if !strings.HasPrefix(request.MessageID, "msg_") || len(request.AttachmentIDs) == 0 || len(request.AttachmentIDs) > 100 || len(request.IdempotencyKey) > 256 {
		return maildomain.AttachmentRemoveRequest{}, cli.NewAppError(cli.ErrorUsage, "attachment removal requires one msg_… ID, 1-100 attachment IDs, and an idempotency key no longer than 256 bytes", false)
	}
	if requireIdempotency && request.IdempotencyKey == "" {
		return maildomain.AttachmentRemoveRequest{}, cli.NewAppError(cli.ErrorUsage, "real attachment removal requires --idempotency-key so ambiguous provider outcomes can be resumed safely", false)
	}
	seen := make(map[string]struct{}, len(request.AttachmentIDs))
	normalized := make([]string, 0, len(request.AttachmentIDs))
	for _, attachmentID := range request.AttachmentIDs {
		attachmentID = strings.TrimSpace(attachmentID)
		if !strings.HasPrefix(attachmentID, "att_") {
			return maildomain.AttachmentRemoveRequest{}, cli.NewAppError(cli.ErrorUsage, "--attachment values must be stable att_… IDs", false)
		}
		if _, duplicate := seen[attachmentID]; duplicate {
			return maildomain.AttachmentRemoveRequest{}, cli.NewAppError(cli.ErrorUsage, "attachment removal repeats the same attachment ID", false)
		}
		seen[attachmentID] = struct{}{}
		normalized = append(normalized, attachmentID)
	}
	request.AttachmentIDs = normalized
	return request, nil
}

func validateExistingAttachmentRewriteRequest(ctx context.Context, state *store.Store, accountValue account.Account, request maildomain.AttachmentRemoveRequest, rewrite maildomain.MessageRewrite) error {
	if rewrite.AccountID != accountValue.ID || rewrite.MessageID != request.MessageID {
		return cli.NewAppError(cli.ErrorConflict, "idempotency key belongs to a different attachment rewrite target", false)
	}
	requested := append([]string(nil), request.AttachmentIDs...)
	expected := make([]string, 0, len(rewrite.RemovedAttachments))
	for _, attachment := range rewrite.RemovedAttachments {
		expected = append(expected, attachment.ID)
	}
	slices.Sort(requested)
	slices.Sort(expected)
	if !slices.Equal(requested, expected) {
		return cli.NewAppError(cli.ErrorConflict, "idempotency key belongs to a different reviewed attachment set", false)
	}
	if request.Folder != "" {
		folderValue, err := state.Folders().Resolve(ctx, accountValue.ID, request.Folder)
		if err != nil {
			return mapFolderError(err)
		}
		if folderValue.ID != rewrite.SourceFolderID {
			return cli.NewAppError(cli.ErrorConflict, "idempotency key belongs to a rewrite from a different provider folder", false)
		}
	}
	return nil
}

func messageRewritePlanFromExisting(rewrite maildomain.MessageRewrite, folderValue folder.Folder, source maildomain.Location) maildomain.MessageRewritePlan {
	if source.ID == "" {
		source = maildomain.Location{
			ID: rewrite.SourceLocationID, MessageID: rewrite.MessageID, AccountID: rewrite.AccountID, FolderID: rewrite.SourceFolderID,
			UIDValidity: rewrite.SourceUIDValidity, UID: rewrite.SourceUID, ModSequence: rewrite.SourceModSequence,
			RawContentHash: rewrite.OldRawContentHash, RawStorageKey: rewrite.OldRawStorageKey, RawByteSize: rewrite.OldRawByteSize,
			Present: rewrite.SourcePresent,
		}
	}
	return maildomain.MessageRewritePlan{
		MessageID: rewrite.MessageID, SourceLocation: source, SourceFolderID: rewrite.SourceFolderID, SourceFolderName: folderValue.Name,
		RemovedAttachments:    append([]maildomain.Attachment(nil), rewrite.RemovedAttachments...),
		BeforeAttachmentCount: rewrite.BeforeAttachmentCount, AfterAttachmentCount: rewrite.AfterAttachmentCount,
		BeforeRawBytes: rewrite.OldRawByteSize, AfterRawBytes: rewrite.NewRawByteSize,
		NewRawContentHash: rewrite.NewRawContentHash, RequestHash: rewrite.RequestHash, IdempotencyKey: rewrite.IdempotencyKey,
	}
}

func (commands accountCommands) prepareAttachmentRewrite(ctx context.Context, state *store.Store, paths config.AppPaths, accountSelector string, request maildomain.AttachmentRemoveRequest, assignIDs bool, existing *maildomain.MessageRewrite) (*preparedAttachmentRewrite, error) {
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return nil, mapAccountError(err)
	}
	if existing != nil && existing.AccountID != accountValue.ID {
		return nil, cli.NewAppError(cli.ErrorConflict, "message rewrite recovery state belongs to another account", false)
	}
	message, err := state.Mail().Message(ctx, accountValue.ID, request.MessageID)
	if err != nil {
		return nil, cli.WrapAppError(cli.ErrorNotFound, "message not found", false, err)
	}
	if existing == nil && (message.Content == nil || len(message.Content.Attachments) == 0) {
		return nil, cli.NewAppError(cli.ErrorNotFound, "message has no indexed attachment content to remove", false)
	}
	if rewriteHasProtectedMIME(message.Content) {
		return nil, cli.NewAppError(cli.ErrorConflict, "cryptographically protected MIME cannot be safely rewritten before S/MIME support is configured", false)
	}
	var folderValue folder.Folder
	var source maildomain.Location
	var removed []maildomain.Attachment
	if existing != nil {
		folderValue, err = state.Folders().Resolve(ctx, accountValue.ID, existing.SourceFolderID)
		if err != nil {
			return nil, mapFolderError(err)
		}
		source, err = state.Mail().Location(ctx, accountValue.ID, existing.SourceLocationID)
		if err != nil {
			return nil, cli.WrapAppError(cli.ErrorInternal, "cannot reload the original rewrite source evidence", false, err)
		}
		source.RawContentHash = existing.OldRawContentHash
		source.RawStorageKey = existing.OldRawStorageKey
		source.RawByteSize = existing.OldRawByteSize
		removed = append([]maildomain.Attachment(nil), existing.RemovedAttachments...)
	} else {
		folderValue, source, err = resolveMutationLocation(ctx, state.Folders(), accountValue.ID, message, request.Folder)
		if err != nil {
			return nil, err
		}
		if source.RawContentHash == "" || source.RawStorageKey == "" || source.RawByteSize < 0 {
			return nil, cli.NewAppError(cli.ErrorUnavailable, "selected provider copy has no verified raw source; synchronize or hydrate it before rewriting", false)
		}
		if !rewriteSourceMatchesCanonical(message, source) {
			return nil, cli.NewAppError(cli.ErrorConflict, "selected provider copy is not the canonical attachment-bearing raw revision; select the canonical provider copy before rewriting", false)
		}
		removed = make([]maildomain.Attachment, 0, len(request.AttachmentIDs))
	}
	pathsToRemove := make(map[string]struct{}, len(request.AttachmentIDs))
	for _, attachmentID := range request.AttachmentIDs {
		if existing != nil {
			found := false
			for _, attachment := range removed {
				if attachment.ID == attachmentID {
					pathsToRemove[attachment.PartPath] = struct{}{}
					found = true
					break
				}
			}
			if !found {
				return nil, cli.NewAppError(cli.ErrorConflict, "rewrite recovery attachment set no longer matches the durable review", false)
			}
			continue
		}
		found := false
		for _, attachment := range message.Content.Attachments {
			if attachment.ID == attachmentID {
				removed = append(removed, attachment)
				pathsToRemove[attachment.PartPath] = struct{}{}
				found = true
				break
			}
		}
		if !found {
			return nil, cli.NewAppError(cli.ErrorNotFound, "selected attachment does not belong to the current canonical message content", false)
		}
	}
	for left := range pathsToRemove {
		for right := range pathsToRemove {
			if left != right && strings.HasPrefix(right, left+".") {
				return nil, cli.NewAppError(cli.ErrorUsage, "attachment removal cannot select both an embedded-message attachment and one of its nested attachments", false)
			}
		}
	}
	slices.SortFunc(removed, func(left, right maildomain.Attachment) int { return strings.Compare(left.ID, right.ID) })
	sourceReader, err := commands.inboundBlobStore(paths).OpenVerifiedContext(ctx, source.RawStorageKey, source.RawContentHash, source.RawByteSize)
	if err != nil {
		return nil, mapBlobReadError(err)
	}
	pipeReader, pipeWriter := io.Pipe()
	go func() {
		rewriteErr := maildomain.RewriteRawWithoutParts(ctx, sourceReader, pathsToRemove, pipeWriter)
		closeErr := sourceReader.Close()
		if rewriteErr == nil {
			rewriteErr = closeErr
		}
		_ = pipeWriter.CloseWithError(rewriteErr)
	}()
	stagedRaw, err := commands.blobStager(paths).Stage(ctx, pipeReader, -1)
	_ = pipeReader.Close()
	if err != nil {
		return nil, cli.WrapAppError(cli.ErrorUnavailable, "cannot stage the rewritten raw message", false, err)
	}
	attachmentSet := newStagedAttachmentSet(commands.attachmentBlobStore(paths))
	prepared := &preparedAttachmentRewrite{account: accountValue, folder: folderValue, message: message, source: source, raw: stagedRaw, attachments: attachmentSet}
	reader, err := stagedRaw.Open()
	if err != nil {
		prepared.abort()
		return nil, cli.WrapAppError(cli.ErrorInternal, "cannot read staged rewritten raw content", false, err)
	}
	content, parseErr := maildomain.ParseNormalizedWithAttachmentsContext(ctx, reader, attachmentSet.Sink)
	closeErr := reader.Close()
	if parseErr == nil {
		parseErr = closeErr
	}
	if parseErr != nil {
		prepared.abort()
		return nil, cli.WrapAppError(cli.ErrorUnavailable, "rewritten MIME could not be normalized safely", false, parseErr)
	}
	if !sameRewriteSemanticMessage(message, content.Preview) {
		prepared.abort()
		return nil, cli.NewAppError(cli.ErrorInternal, "attachment removal changed stable message headers; rewrite aborted before provider contact", false)
	}
	if assignIDs {
		if err := assignContentIDs(commands, message.ID, &content); err != nil {
			prepared.abort()
			return nil, cli.WrapAppError(cli.ErrorInternal, "cannot allocate rewritten MIME identities", false, err)
		}
	}
	beforeAttachmentCount := len(removed) + len(content.Attachments)
	if message.Content != nil && existing == nil {
		beforeAttachmentCount = len(message.Content.Attachments)
	}
	if existing != nil {
		beforeAttachmentCount = existing.BeforeAttachmentCount
		if stagedRaw.ContentHash != existing.NewRawContentHash || stagedRaw.StorageKey != existing.NewRawStorageKey || stagedRaw.ByteSize != existing.NewRawByteSize || len(content.Attachments) != existing.AfterAttachmentCount {
			prepared.abort()
			return nil, cli.NewAppError(cli.ErrorConflict, "durable attachment rewrite no longer reconstructs the reviewed replacement bytes; provider recovery stopped", false)
		}
	}
	plan := maildomain.MessageRewritePlan{
		MessageID: message.ID, SourceLocation: source, SourceFolderID: folderValue.ID, SourceFolderName: folderValue.Name,
		RemovedAttachments: removed, BeforeAttachmentCount: beforeAttachmentCount, AfterAttachmentCount: len(content.Attachments),
		BeforeRawBytes: source.RawByteSize, AfterRawBytes: stagedRaw.ByteSize, NewRawContentHash: stagedRaw.ContentHash,
		IdempotencyKey: request.IdempotencyKey,
	}
	plan.RequestHash, err = maildomain.MessageRewriteHash(plan)
	if err != nil {
		prepared.abort()
		return nil, cli.WrapAppError(cli.ErrorInternal, "cannot hash the attachment rewrite review", false, err)
	}
	if existing != nil && plan.RequestHash != existing.RequestHash {
		prepared.abort()
		return nil, cli.NewAppError(cli.ErrorConflict, "durable attachment rewrite request hash no longer matches reconstructed recovery state", false)
	}
	prepared.plan = plan
	prepared.content = content
	return prepared, nil
}

func rewriteSourceMatchesCanonical(message maildomain.Message, source maildomain.Location) bool {
	if message.ContentLocationID == source.ID {
		return true
	}
	if message.LocalRaw != nil && strings.EqualFold(message.LocalRaw.ContentHash, source.RawContentHash) {
		return true
	}
	for _, location := range message.Locations {
		if location.ID == message.ContentLocationID && strings.EqualFold(location.RawContentHash, source.RawContentHash) {
			return true
		}
	}
	return false
}

func rewriteHasProtectedMIME(content *maildomain.NormalizedContent) bool {
	if content == nil {
		return false
	}
	for _, part := range content.Parts {
		switch strings.ToLower(strings.TrimSpace(part.MediaType)) {
		case "multipart/signed", "multipart/encrypted",
			"application/pkcs7-mime", "application/x-pkcs7-mime",
			"application/pkcs7-signature", "application/x-pkcs7-signature":
			return true
		}
	}
	return false
}

func sameRewriteSemanticMessage(message maildomain.Message, parsed maildomain.ParsedMessage) bool {
	return maildomain.NormalizeMessageID(message.RFCMessageID) == maildomain.NormalizeMessageID(parsed.RFCMessageID) &&
		maildomain.NormalizeMessageID(message.InReplyTo) == maildomain.NormalizeMessageID(parsed.InReplyTo) &&
		message.From == parsed.From && message.ReplyTo == parsed.ReplyTo && message.Subject == parsed.Subject &&
		slices.Equal(message.To, parsed.To) && slices.Equal(message.CC, parsed.CC) && slices.Equal(message.BCC, parsed.BCC) &&
		slices.Equal(message.References, parsed.References)
}

func (commands accountCommands) appendRewriteReplacement(ctx context.Context, paths config.AppPaths, accountValue account.Account, password []byte, folderValue folder.Folder, source maildomain.Location, rewrite maildomain.MessageRewrite) (transport.MailboxAppendResult, error) {
	var result transport.MailboxAppendResult
	var lastErr error
	internalDate := commands.timeNow()
	if source.InternalDate != nil {
		internalDate = *source.InternalDate
	}
	for _, username := range imapUsernameCandidates(accountValue) {
		reader, err := commands.inboundBlobStore(paths).OpenVerifiedContext(ctx, rewrite.NewRawStorageKey, rewrite.NewRawContentHash, rewrite.NewRawByteSize)
		if err != nil {
			return transport.MailboxAppendResult{}, mapBlobReadError(err)
		}
		result, lastErr = commands.mailboxAppender().AppendMailbox(ctx, transport.AuthRequest{Username: username, Password: password}, transport.MailboxAppendRequest{
			Mailbox: folderValue.Name, Message: reader, Size: rewrite.NewRawByteSize, InternalDate: internalDate, Flags: source.Flags.All(),
		})
		closeErr := reader.Close()
		if lastErr == nil {
			lastErr = closeErr
		}
		if lastErr == nil {
			return result, nil
		}
		failure, ok := transport.AsFailure(lastErr)
		if !ok || failure.Kind != transport.FailureAuthentication {
			break
		}
	}
	return result, lastErr
}

func (commands accountCommands) reconcileRewriteAppend(ctx context.Context, state *store.Store, paths config.AppPaths, accountValue account.Account, password []byte, folderValue folder.Folder, rewrite maildomain.MessageRewrite) (maildomain.MessageRewrite, error) {
	_, _, syncErr := commands.syncMailbox(ctx, state, paths, accountValue, password, folderValue, 100)
	if syncErr != nil {
		return rewrite, cli.WrapAppError(cli.ErrorUnavailable, "replacement APPEND needs reconciliation but the source folder could not be synchronized", true, syncErr)
	}
	locations, err := state.Mail().LocationsByRawHashInFolder(ctx, accountValue.ID, folderValue.ID, rewrite.NewRawContentHash, 3)
	if err != nil {
		return rewrite, cli.WrapAppError(cli.ErrorInternal, "cannot inspect synchronized replacement evidence", false, err)
	}
	filtered := locations[:0]
	for _, location := range locations {
		if location.ID != rewrite.SourceLocationID {
			filtered = append(filtered, location)
		}
	}
	if len(filtered) != 1 {
		return rewrite, cli.NewAppError(cli.ErrorConflict, "replacement APPEND is unresolved; synchronize the folder and retry the same idempotency key after exactly one matching provider copy is visible", false)
	}
	location := filtered[0]
	return state.MessageRewrites().ConfirmAppend(ctx, maildomain.ConfirmRewriteAppend{
		RewriteID: rewrite.ID, LocationID: location.ID, UIDValidity: location.UIDValidity, UID: location.UID,
		ModSequence: location.ModSequence, ProviderMethod: "append_reconciled", Now: commands.timeNow(),
	})
}

func (commands accountCommands) rewriteSourceAbsent(ctx context.Context, accountValue account.Account, password []byte, folderValue folder.Folder, rewrite maildomain.MessageRewrite) (bool, error) {
	var lastErr error
	for _, username := range imapUsernameCandidates(accountValue) {
		_, lastErr = commands.remoteClient().FetchRemoteSummary(ctx, transport.AuthRequest{Username: username, Password: password}, transport.RemoteFetchRequest{
			Mailbox: folderValue.Name, ExpectedUIDValidity: rewrite.SourceUIDValidity, UID: rewrite.SourceUID,
		})
		if lastErr == nil {
			return false, nil
		}
		if errors.Is(lastErr, transport.ErrRemoteMessageUnavailable) {
			return true, nil
		}
		failure, ok := transport.AsFailure(lastErr)
		if !ok || failure.Kind != transport.FailureAuthentication {
			break
		}
	}
	return false, mapMailboxTransportError(lastErr, folderValue.Name)
}

func (commands accountCommands) deleteRewriteSource(ctx context.Context, state *store.Store, accountValue account.Account, password []byte, folderValue folder.Folder, source maildomain.Location, rewrite maildomain.MessageRewrite) (maildomain.MessageRewrite, error) {
	var result transport.LocationMutationResult
	var providerErr error
	for _, username := range imapUsernameCandidates(accountValue) {
		result, providerErr = commands.locationClient().MutateLocation(ctx, transport.AuthRequest{Username: username, Password: password}, transport.LocationMutationRequest{
			Operation: transport.LocationMutationDelete, SourceMailbox: folderValue.Name,
			ExpectedSourceUIDValidity: rewrite.SourceUIDValidity, SourceUID: rewrite.SourceUID,
			ExpectedSourceModSequence: rewrite.SourceModSequence,
		})
		if providerErr == nil {
			break
		}
		failure, ok := transport.AsFailure(providerErr)
		if !ok || failure.Kind != transport.FailureAuthentication {
			break
		}
	}
	if providerErr != nil || result.ReconciliationRequired || result.SourcePresent {
		return rewrite, mapObservedLocationMutationError(providerErr, result)
	}
	flags := maildomain.ParseLocationFlags(result.SourceFlags)
	if len(result.SourceFlags) == 0 {
		flags = source.Flags
	}
	return state.MessageRewrites().ConfirmDelete(ctx, maildomain.ConfirmRewriteDelete{
		RewriteID: rewrite.ID, SourceFlags: flags, SourceModSequence: result.SourceModSequence,
		ProviderMethod: result.ProviderMethod, Now: commands.timeNow(),
	})
}

func definitelyBeforeRewriteAppend(err error) bool {
	failure, ok := transport.AsFailure(err)
	if !ok {
		return false
	}
	switch failure.Stage {
	case transport.StageConnect, transport.StageTLS, transport.StageIMAPGreeting, transport.StageIMAPCapability, transport.StageIMAPAuth, transport.StageIMAPSelect:
		return true
	default:
		return false
	}
}

func messageRewriteErrorCode(err error) string {
	if failure, ok := transport.AsFailure(err); ok {
		return string(failure.Stage)
	}
	return "provider"
}

func mapMessageRewriteStoreError(err error) error {
	switch {
	case errors.Is(err, maildomain.ErrMessageRewriteConflict):
		return cli.WrapAppError(cli.ErrorConflict, "message rewrite conflicts with existing reviewed/provider state", false, err)
	case errors.Is(err, maildomain.ErrInvalidMessageRewrite):
		return cli.WrapAppError(cli.ErrorUsage, "message rewrite request is invalid", false, err)
	default:
		return cli.WrapAppError(cli.ErrorInternal, "cannot persist the message rewrite", false, err)
	}
}

func withMessageRewriteDetails(err error, rewrite maildomain.MessageRewrite) error {
	appErr := cli.NormalizeError(err)
	appErr.Details = map[string]any{
		"rewrite_id": rewrite.ID, "message_id": rewrite.MessageID, "status": rewrite.Status,
		"source_location_id": rewrite.SourceLocationID, "new_location_id": rewrite.NewLocationID,
		"reconciliation_required": rewrite.ReconciliationRequired,
	}
	return appErr
}
