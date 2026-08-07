package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"path/filepath"
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
	"github.com/gofrs/flock"
)

var errDraftFolderRoleConflict = errors.New("explicit draft folder conflicts with provider role metadata")

func (commands accountCommands) MailDraftCreate(ctx context.Context, home, accountSelector string, request maildomain.SendRequest) (account.Account, maildomain.Draft, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), mapAccountError(err)
	}
	messageID, err := commands.generateID("msg")
	if err != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate a draft message ID", false, err)
	}
	draftID := maildomain.DraftIDForMessage(messageID)
	return commands.saveDraftInState(ctx, state, paths, accountValue, draftID, messageID, 1, request)
}

func (commands accountCommands) MailDraftUpdate(ctx context.Context, home, accountSelector, draftID string, request maildomain.SendRequest) (account.Account, maildomain.Draft, int64, error) {
	unlockDraft, err := acquireDraftOperationLock(home, draftID)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, 0, err
	}
	defer unlockDraft()
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), mapAccountError(err)
	}
	current, err := state.Drafts().Draft(ctx, accountValue.ID, draftID)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), mapDraftError(err)
	}
	if current.SyncState != maildomain.DraftLocal && current.SyncState != maildomain.DraftSynced {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorConflict, "draft has unresolved provider state; run mail draft sync before editing", true)
	}
	return commands.saveDraftInState(ctx, state, paths, accountValue, current.ID, current.MessageID, current.Revision+1, request)
}

func (commands accountCommands) saveDraftInState(ctx context.Context, state *store.Store, paths config.AppPaths, accountValue account.Account, draftID, messageID string, revision int, request maildomain.SendRequest) (account.Account, maildomain.Draft, int64, error) {
	preparedAttachments, stagedFiles, err := stageOutboundAttachments(ctx, commands.attachmentBlobStore(paths), request.Attachments)
	request.Attachments = preparedAttachments
	if err != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), mapOutboundAttachmentError(err)
	}
	defer stagedFiles.Abort()
	normalized, err := normalizeAccountDraftRequest(accountValue, request)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), mapDraftRequestError(err)
	}
	rfcMessageID, err := maildomain.GenerateOutboundMessageID(accountValue.Email)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate a draft RFC Message-ID", false, err)
	}
	savedAt := commands.timeNow()
	staged, err := stageDraftMessage(ctx, commands.outboundBlobStager(paths), normalized, rfcMessageID, savedAt)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), mapOutboundPreparationError(err)
	}
	defer staged.Abort()
	reader, err := staged.Open()
	if err != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reopen the composed draft", false, err)
	}
	attachmentSet := newStagedAttachmentSet(commands.attachmentBlobStore(paths))
	defer attachmentSet.Abort()
	content, parseErr := maildomain.ParseNormalizedWithAttachmentsContext(ctx, reader, attachmentSet.Sink)
	closeErr := reader.Close()
	if parseErr == nil {
		parseErr = closeErr
	}
	if parseErr != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot verify the composed draft", false, parseErr)
	}
	if err := verifyOutboundAttachmentContent(normalized.Attachments, content.Attachments); err != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "composed draft attachments failed verification", false, err)
	}
	if err := assignContentIDs(commands, messageID, &content); err != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate draft content IDs", false, err)
	}
	if err := staged.Commit(); err != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot commit the composed draft", true, err)
	}
	if err := attachmentSet.Commit(); err != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot commit draft attachment content", true, err)
	}
	value, err := state.Drafts().Save(ctx, maildomain.DraftSave{
		DraftID: draftID, AccountID: accountValue.ID, MessageID: messageID, Revision: revision,
		Request: normalized, RFCMessageID: rfcMessageID,
		RawContentHash: staged.ContentHash, RawStorageKey: staged.StorageKey, RawByteSize: staged.ByteSize,
		Content: content, Now: savedAt,
	})
	if err != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist the local draft", false, err)
	}
	value, err = state.Drafts().Draft(ctx, accountValue.ID, value.ID)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reload the local draft", false, err)
	}
	return accountValue, value, state.SchemaVersion(), nil
}

func normalizeAccountDraftRequest(accountValue account.Account, request maildomain.SendRequest) (maildomain.SendRequest, error) {
	request.Kind = maildomain.OutboundKindSend
	request.ParentMessageID, request.SourceMessageID, request.IdempotencyKey, request.RetryOfAttemptID = "", "", "", ""
	request.From = maildomain.Address{DisplayName: accountValue.Name, Email: accountValue.Email}
	return maildomain.NormalizeDraftRequest(request)
}

func stageDraftMessage(ctx context.Context, stager blob.Stager, request maildomain.SendRequest, messageID string, savedAt time.Time) (*blob.Staged, error) {
	if stager == nil {
		return nil, errors.New("draft blob stager is unavailable")
	}
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := maildomain.ComposeDraftMessage(writer, request, messageID, savedAt)
		_ = writer.CloseWithError(err)
		done <- err
	}()
	staged, stageErr := stager.Stage(ctx, reader, -1)
	_ = reader.Close()
	composeErr := <-done
	if stageErr != nil {
		if staged != nil {
			_ = staged.Abort()
		}
		return nil, stageErr
	}
	if composeErr != nil {
		_ = staged.Abort()
		return nil, composeErr
	}
	return staged, nil
}

func (commands accountCommands) MailDraftList(ctx context.Context, home, accountSelector string, limit int) (account.Account, []maildomain.Draft, bool, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, nil, false, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, nil, false, state.SchemaVersion(), mapAccountError(err)
	}
	values, more, err := state.Drafts().List(ctx, accountValue.ID, limit)
	if err != nil {
		return account.Account{}, nil, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot list local drafts", false, err)
	}
	return accountValue, values, more, state.SchemaVersion(), nil
}

func (commands accountCommands) MailDraftShow(ctx context.Context, home, accountSelector, draftID string) (account.Account, maildomain.Draft, maildomain.ContentSource, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, nil, 0, err
	}
	paths, pathErr := config.ResolveAppPaths(home)
	if pathErr != nil {
		state.Close()
		return account.Account{}, maildomain.Draft{}, nil, 0, cli.WrapAppError(cli.ErrorConfiguration, "cannot resolve local draft content paths", false, pathErr)
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, nil, state.SchemaVersion(), mapAccountError(err)
	}
	value, err := state.Drafts().Draft(ctx, accountValue.ID, draftID)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, nil, state.SchemaVersion(), mapDraftError(err)
	}
	raw, ok := rawMessageSource(value.Message)
	if !ok || value.Message.Content == nil {
		return accountValue, value, nil, state.SchemaVersion(), nil
	}
	source := &messageContentSource{store: blob.New(paths.BlobRoot), raw: raw, content: *value.Message.Content}
	return accountValue, value, source, state.SchemaVersion(), nil
}

func (commands accountCommands) MailDraftSync(ctx context.Context, home, accountSelector, folderSelector, draftID string, limit int) (account.Account, maildomain.DraftSyncSummary, []maildomain.Draft, int64, error) {
	if limit < 1 || limit > 100 {
		return account.Account{}, maildomain.DraftSyncSummary{}, nil, 0, cli.NewAppError(cli.ErrorUsage, "draft sync limit must be between 1 and 100", false)
	}
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.DraftSyncSummary{}, nil, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.DraftSyncSummary{}, nil, state.SchemaVersion(), mapAccountError(err)
	}
	draftsFolder, err := resolveDraftsFolder(ctx, state, accountValue.ID, folderSelector)
	if err != nil {
		if errors.Is(err, folder.ErrNotFound) {
			return account.Account{}, maildomain.DraftSyncSummary{}, nil, state.SchemaVersion(), cli.NewAppError(cli.ErrorConfiguration, "iCloud Drafts role is unavailable; refresh folder status or explicitly select the provider Drafts mailbox with --folder", false)
		}
		if errors.Is(err, errDraftFolderRoleConflict) {
			return account.Account{}, maildomain.DraftSyncSummary{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "--folder may select an unclassified provider mailbox only when iCloud advertises no Drafts role", false, err)
		}
		return account.Account{}, maildomain.DraftSyncSummary{}, nil, state.SchemaVersion(), mapFolderError(err)
	}
	if !draftsFolder.Present || draftsFolder.Stale || !draftsFolder.Selectable {
		return account.Account{}, maildomain.DraftSyncSummary{}, nil, state.SchemaVersion(), cli.NewAppError(cli.ErrorConflict, "iCloud Drafts folder is not currently selectable; refresh folder status", false)
	}
	_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
	if err != nil {
		return account.Account{}, maildomain.DraftSyncSummary{}, nil, state.SchemaVersion(), mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return account.Account{}, maildomain.DraftSyncSummary{}, nil, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)
	return commands.syncDraftsInState(ctx, state, paths, accountValue, password, draftsFolder, draftID, limit)
}

func resolveDraftsFolder(ctx context.Context, state *store.Store, accountID, selector string) (folder.Folder, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return state.Folders().ResolveKind(ctx, accountID, folder.KindDrafts)
	}
	selected, err := state.Folders().Resolve(ctx, accountID, selector)
	if err != nil {
		return folder.Folder{}, err
	}
	if selected.Kind == folder.KindDrafts {
		return selected, nil
	}
	if selected.Kind != folder.KindOther {
		return folder.Folder{}, fmt.Errorf("%w: selected folder has provider role %s, not drafts", errDraftFolderRoleConflict, selected.Kind)
	}
	providerDrafts, roleErr := state.Folders().ResolveKind(ctx, accountID, folder.KindDrafts)
	if roleErr == nil {
		if providerDrafts.ID == selected.ID {
			return selected, nil
		}
		return folder.Folder{}, fmt.Errorf("%w: provider already advertises a distinct Drafts folder", errDraftFolderRoleConflict)
	}
	if !errors.Is(roleErr, folder.ErrNotFound) {
		return folder.Folder{}, roleErr
	}
	// The selector is an explicit operator declaration, not role inference.
	// Keep the durable folder kind unchanged so ordinary system-folder safety
	// continues to rely only on provider special-use metadata.
	return selected, nil
}

func (commands accountCommands) syncDraftsInState(ctx context.Context, state *store.Store, paths config.AppPaths, accountValue account.Account, password []byte, draftsFolder folder.Folder, draftID string, limit int) (account.Account, maildomain.DraftSyncSummary, []maildomain.Draft, int64, error) {
	summary := maildomain.DraftSyncSummary{AccountID: accountValue.ID, FolderID: draftsFolder.ID, FolderName: draftsFolder.Name}
	folderSync, usedUsername, err := commands.syncMailbox(ctx, state, paths, accountValue, password, draftsFolder, limit)
	if err != nil {
		return accountValue, summary, nil, state.SchemaVersion(), err
	}
	if usedUsername != "" && usedUsername != accountValue.IMAPUsername {
		accountValue, err = state.Accounts().UpdateIMAPUsername(ctx, accountValue.ID, usedUsername, commands.timeNow())
		if err != nil {
			return account.Account{}, summary, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist the working IMAP username", false, err)
		}
	}
	summary.FolderSync = folderSync
	summary.MoreAvailable = folderSync.MoreAvailable
	values, err := state.Drafts().AdoptFolder(ctx, accountValue.ID, draftsFolder.ID, commands.timeNow())
	if err != nil {
		return accountValue, summary, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reconcile synchronized provider drafts", true, err)
	}
	summary.Observed = len(values)
	appendUnknown := false
	for _, value := range values {
		if draftID != "" && value.ID != draftID {
			continue
		}
		if value.SyncState != maildomain.DraftLocal {
			continue
		}
		updated, unknown, appendErr := commands.appendDraft(ctx, state, paths, accountValue, password, draftsFolder, value)
		if appendErr != nil {
			return accountValue, summary, values, state.SchemaVersion(), appendErr
		}
		summary.Appended++
		appendUnknown = appendUnknown || unknown
		_ = updated
	}
	if draftID != "" {
		if _, err := state.Drafts().Draft(ctx, accountValue.ID, draftID); err != nil {
			return accountValue, summary, values, state.SchemaVersion(), mapDraftError(err)
		}
	}
	if appendUnknown {
		folderSync, usedUsername, err = commands.syncMailbox(ctx, state, paths, accountValue, password, draftsFolder, limit)
		if err != nil {
			return accountValue, summary, values, state.SchemaVersion(), err
		}
		if usedUsername != "" && usedUsername != accountValue.IMAPUsername {
			accountValue, err = state.Accounts().UpdateIMAPUsername(ctx, accountValue.ID, usedUsername, commands.timeNow())
			if err != nil {
				return account.Account{}, summary, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist the working IMAP username", false, err)
			}
		}
		values, err = state.Drafts().AdoptFolder(ctx, accountValue.ID, draftsFolder.ID, commands.timeNow())
		if err != nil {
			return accountValue, summary, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reconcile appended draft evidence", true, err)
		}
		summary.Reconciled++
		summary.MoreAvailable = summary.MoreAvailable || folderSync.MoreAvailable
	}
	values, _, err = state.Drafts().List(ctx, accountValue.ID, 1000)
	if err != nil {
		return accountValue, summary, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reload draft reconciliation state", false, err)
	}
	for _, value := range values {
		if draftID != "" && value.ID != draftID {
			continue
		}
		if value.OutboundAttemptID != "" && (value.SyncState == maildomain.DraftCleanupPending || value.SyncState == maildomain.DraftSendUnknown) {
			attempt, attemptErr := state.Outbound().Attempt(ctx, value.OutboundAttemptID)
			if attemptErr != nil || attempt.Status != maildomain.OutboundSent {
				summary.Pending++
				continue
			}
			if cleanupErr := commands.deleteAllDraftLocations(ctx, state, accountValue, password, value); cleanupErr != nil {
				summary.Pending++
				continue
			}
			if deleteErr := state.Drafts().Delete(ctx, accountValue.ID, value.ID, commands.timeNow()); deleteErr != nil {
				summary.Pending++
				continue
			}
			summary.Cleaned++
			continue
		}
		if value.SyncState == maildomain.DraftCleanupPending {
			if _, cleanupErr := commands.cleanupPreviousDraftLocation(ctx, state, accountValue, password, draftsFolder, value); cleanupErr != nil {
				// Keep the durable recovery pointers. A later draft sync can retry cleanup.
				summary.Pending++
				continue
			}
			summary.Cleaned++
		} else if value.SyncState == maildomain.DraftReconcile || value.SyncState == maildomain.DraftSendUnknown {
			summary.Pending++
		}
	}
	values, _, err = state.Drafts().List(ctx, accountValue.ID, 1000)
	if err != nil {
		return accountValue, summary, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reload synchronized drafts", false, err)
	}
	if draftID != "" {
		filtered := values[:0]
		for _, value := range values {
			if value.ID == draftID {
				filtered = append(filtered, value)
			}
		}
		values = filtered
	}
	summary.CompletedAt = commands.timeNow()
	return accountValue, summary, values, state.SchemaVersion(), nil
}

func (commands accountCommands) appendDraft(ctx context.Context, state *store.Store, paths config.AppPaths, accountValue account.Account, password []byte, draftsFolder folder.Folder, value maildomain.Draft) (maildomain.Draft, bool, error) {
	raw, ok := rawMessageSource(value.Message)
	if !ok {
		return value, false, cli.NewAppError(cli.ErrorUnavailable, "draft raw content is unavailable; repair local blobs before syncing", false)
	}
	var result transport.MailboxAppendResult
	var appendErr error
	for _, username := range imapUsernameCandidates(accountValue) {
		reader, openErr := blob.New(paths.BlobRoot).OpenVerifiedContext(ctx, raw.StorageKey, raw.ContentHash, raw.ByteSize)
		if openErr != nil {
			return value, false, mapBlobReadError(openErr)
		}
		result, appendErr = commands.mailboxAppender().AppendMailbox(ctx, transport.AuthRequest{Username: username, Password: password}, transport.MailboxAppendRequest{
			Mailbox: draftsFolder.Name, Message: reader, Size: raw.ByteSize, InternalDate: commands.timeNow(), Flags: []string{"\\Draft", "\\Seen"},
		})
		closeErr := reader.Close()
		if appendErr == nil {
			appendErr = closeErr
		}
		if appendErr == nil {
			break
		}
		failure, ok := transport.AsFailure(appendErr)
		if !ok || failure.Kind != transport.FailureAuthentication {
			break
		}
	}
	if appendErr != nil {
		return value, false, mapMailboxTransportError(appendErr, draftsFolder.Name)
	}
	if result.UIDValidity == 0 || result.UID == 0 {
		updated, err := state.Drafts().MarkAppendUnknown(ctx, accountValue.ID, value.ID, "appenduid_missing", commands.timeNow())
		if err != nil {
			return value, true, cli.WrapAppError(cli.ErrorInternal, "draft was appended but reconciliation state could not be saved; do not delete provider drafts", false, err)
		}
		return updated, true, nil
	}
	locationID, err := commands.generateID("loc")
	if err != nil {
		return value, false, cli.WrapAppError(cli.ErrorInternal, "draft was appended but its provider location ID could not be allocated; run mail draft sync", true, err)
	}
	updated, err := state.Drafts().ConfirmAppend(ctx, accountValue.ID, value.ID, draftsFolder.ID, locationID, result.UIDValidity, result.UID, commands.timeNow())
	if err != nil {
		return value, false, cli.WrapAppError(cli.ErrorInternal, "draft was appended but its provider location could not be committed; run mail draft sync", true, err)
	}
	return updated, false, nil
}

func (commands accountCommands) cleanupPreviousDraftLocation(ctx context.Context, state *store.Store, accountValue account.Account, password []byte, draftsFolder folder.Folder, value maildomain.Draft) (maildomain.Draft, error) {
	previousID := strings.TrimSpace(value.PreviousLocationID)
	if previousID == "" {
		return value, nil
	}
	location, err := state.Mail().Location(ctx, accountValue.ID, previousID)
	if errors.Is(err, maildomain.ErrLocationNotFound) {
		return state.Drafts().ClearPrevious(ctx, accountValue.ID, value.ID, previousID, commands.timeNow())
	}
	if err != nil {
		return value, cli.WrapAppError(cli.ErrorInternal, "cannot read the previous provider draft location", false, err)
	}
	if !location.Present {
		return state.Drafts().ClearPrevious(ctx, accountValue.ID, value.ID, previousID, commands.timeNow())
	}
	if location.FolderID != draftsFolder.ID {
		return value, cli.NewAppError(cli.ErrorConflict, "previous draft recovery location is no longer in iCloud Drafts; sync before cleanup", true)
	}
	var result transport.LocationMutationResult
	var providerErr error
	for _, username := range imapUsernameCandidates(accountValue) {
		result, providerErr = commands.locationClient().MutateLocation(ctx, transport.AuthRequest{Username: username, Password: password}, transport.LocationMutationRequest{
			Operation: transport.LocationMutationDelete, SourceMailbox: draftsFolder.Name,
			ExpectedSourceUIDValidity: location.UIDValidity, SourceUID: location.UID, ExpectedSourceModSequence: location.ModSequence,
		})
		if providerErr == nil {
			break
		}
		failure, ok := transport.AsFailure(providerErr)
		if !ok || failure.Kind != transport.FailureAuthentication {
			break
		}
	}
	if providerErr != nil || result.ReconciliationRequired || !result.ProviderMutationObserved {
		if providerErr == nil {
			providerErr = errors.New("provider draft delete requires reconciliation")
		}
		return value, mapLocationMutationTransportError(providerErr)
	}
	if _, err := state.Mail().UpdateLocationState(ctx, accountValue.ID, location.ID, location.ModSequence, maildomain.ParseLocationFlags(result.SourceFlags), result.SourceModSequence, false, commands.timeNow()); err != nil {
		return value, cli.WrapAppError(cli.ErrorInternal, "previous provider draft was removed but local cleanup is incomplete; run mail draft sync", true, err)
	}
	updated, err := state.Drafts().ClearPrevious(ctx, accountValue.ID, value.ID, previousID, commands.timeNow())
	if err != nil {
		return value, cli.WrapAppError(cli.ErrorInternal, "previous provider draft was removed but recovery metadata is incomplete; run mail draft sync", true, err)
	}
	return updated, nil
}

func (commands accountCommands) mailboxAppender() transport.IMAPMailboxAppender {
	if commands.appendMailbox != nil {
		return commands.appendMailbox
	}
	if candidate, ok := commands.imap.(transport.IMAPMailboxAppender); ok {
		return candidate
	}
	return unavailableMailboxAppender{}
}

type unavailableMailboxAppender struct{}

func (unavailableMailboxAppender) AppendMailbox(context.Context, transport.AuthRequest, transport.MailboxAppendRequest) (transport.MailboxAppendResult, error) {
	return transport.MailboxAppendResult{}, transport.NewFailure(transport.StageIMAPAppend, transport.FailureConfiguration, false, errors.New("mailbox append transport unavailable"))
}

func draftSendRequest(ctx context.Context, paths config.AppPaths, accountValue account.Account, value maildomain.Draft) (maildomain.SendRequest, error) {
	message := value.Message
	request := maildomain.SendRequest{
		From: maildomain.Address{DisplayName: accountValue.Name, Email: accountValue.Email},
		To:   append([]maildomain.Address(nil), message.To...), CC: append([]maildomain.Address(nil), message.CC...), BCC: append([]maildomain.Address(nil), message.BCC...),
		Subject: message.Subject, InReplyTo: message.InReplyTo, References: append([]string(nil), message.References...), Headers: append([]maildomain.CustomHeader(nil), value.CustomHeaders...),
	}
	if message.ReplyTo.Email != "" {
		reply := message.ReplyTo
		request.ReplyTo = &reply
	}
	if message.Content == nil {
		return request, nil
	}
	raw, ok := rawMessageSource(message)
	if !ok {
		return maildomain.SendRequest{}, maildomain.ErrContentUnavailable
	}
	source := &messageContentSource{store: blob.New(paths.BlobRoot), raw: raw, content: *message.Content}
	if len(message.Content.TextPaths) > 0 {
		writer := &forwardBodyWriter{limit: maildomain.MaxSendBodyBytes}
		if err := source.WriteBody(ctx, maildomain.ContentText, writer); err != nil {
			return maildomain.SendRequest{}, err
		}
		request.TextBody = writer.buffer.String()
	}
	if len(message.Content.HTMLPaths) > 0 {
		writer := &forwardBodyWriter{limit: maildomain.MaxSendBodyBytes}
		if err := source.WriteBody(ctx, maildomain.ContentHTML, writer); err != nil {
			return maildomain.SendRequest{}, err
		}
		request.HTMLBody = writer.buffer.String()
	}
	for _, attachment := range message.Content.Attachments {
		if attachment.Availability != maildomain.AttachmentAvailable {
			return maildomain.SendRequest{}, fmt.Errorf("draft attachment %q is unavailable", attachment.Filename)
		}
		copy := attachment
		request.Attachments = append(request.Attachments, maildomain.OutboundAttachment{
			Filename: copy.Filename, MediaType: copy.MediaType, ContentHash: copy.ContentHash, ByteSize: copy.ByteSize,
			Open: func() (io.ReadCloser, error) {
				return blob.New(paths.BlobRoot).OpenVerifiedContext(ctx, copy.StorageKey, copy.ContentHash, copy.ByteSize)
			},
		})
	}
	return maildomain.NormalizeDraftRequest(request)
}

func acquireDraftOperationLock(home, draftID string) (func(), error) {
	draftID = strings.TrimSpace(draftID)
	if draftID == "" {
		return nil, cli.NewAppError(cli.ErrorUsage, "draft ID is required", false)
	}
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		return nil, cli.WrapAppError(cli.ErrorConfiguration, "cannot resolve draft lock paths", false, err)
	}
	if err := config.EnsureAppPaths(paths); err != nil {
		return nil, cli.WrapAppError(cli.ErrorConfiguration, "cannot initialize draft lock paths", false, err)
	}
	digest := sha256.Sum256([]byte(draftID))
	lock := flock.New(filepath.Join(paths.LockRoot, fmt.Sprintf("draft-%x.lock", digest[:12])), flock.SetPermissions(0o600))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, cli.WrapAppError(cli.ErrorConflict, "cannot acquire the draft operation lock", true, err)
	}
	if !locked {
		return nil, cli.NewAppError(cli.ErrorConflict, "another draft update, send, or delete is already running for this draft", true)
	}
	return func() { _ = lock.Unlock() }, nil
}

func mapDraftRequestError(err error) error {
	return cli.WrapAppError(cli.ErrorUsage, "draft request is invalid", false, err)
}
func mapDraftError(err error) error {
	if errors.Is(err, maildomain.ErrDraftNotFound) {
		return cli.NewAppError(cli.ErrorNotFound, "mail draft not found", false)
	}
	return cli.WrapAppError(cli.ErrorInternal, "cannot read mail draft", false, err)
}

func (commands accountCommands) MailDraftSendPreview(ctx context.Context, home, accountSelector, draftID string) (account.Account, maildomain.Draft, maildomain.SendPreview, int64, error) {
	accountValue, value, request, schemaVersion, err := commands.loadDraftSendRequest(ctx, home, accountSelector, draftID)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, maildomain.SendPreview{}, schemaVersion, err
	}
	_, preview, _, err := commands.MailSendPreview(ctx, home, accountValue.ID, request)
	if err != nil {
		return accountValue, value, maildomain.SendPreview{}, schemaVersion, err
	}
	return accountValue, value, preview, schemaVersion, nil
}

func (commands accountCommands) MailDraftSend(ctx context.Context, home, accountSelector, draftID string) (account.Account, maildomain.Draft, maildomain.OutboundAttempt, bool, int64, error) {
	unlockDraft, err := acquireDraftOperationLock(home, draftID)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, maildomain.OutboundAttempt{}, false, 0, err
	}
	defer unlockDraft()
	accountValue, value, request, schemaVersion, err := commands.loadDraftSendRequest(ctx, home, accountSelector, draftID)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, maildomain.OutboundAttempt{}, false, schemaVersion, err
	}
	request.IdempotencyKey = fmt.Sprintf("draft:%s:r%d", value.ID, value.Revision)
	accountValue, attempt, existing, schemaVersion, sendErr := commands.MailSend(ctx, home, accountValue.ID, request)
	if sendErr != nil || attempt.Status == maildomain.OutboundUnknown {
		state, _, unlock, openErr := commands.openWriteState(ctx, home)
		if openErr == nil {
			stateValue, stateErr := state.Drafts().Draft(ctx, accountValue.ID, draftID)
			if stateErr == nil {
				stateKind := stateValue.SyncState
				if attempt.Status == maildomain.OutboundUnknown {
					stateKind = maildomain.DraftSendUnknown
				}
				_, _ = state.Drafts().SetLifecycle(ctx, accountValue.ID, draftID, stateKind, stateValue.RemoteLocationID, stateValue.PreviousLocationID, attempt.ID, string(attempt.Status), commands.timeNow())
			}
			state.Close()
			unlock()
		}
		return accountValue, value, attempt, existing, schemaVersion, sendErr
	}
	if attempt.Status != maildomain.OutboundSent {
		return accountValue, value, attempt, existing, schemaVersion, cli.NewAppError(cli.ErrorConflict, "draft send has not reached a proven sent outcome; draft preserved", false)
	}
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return accountValue, value, attempt, existing, schemaVersion, withAttemptDetails(cli.WrapAppError(cli.ErrorInternal, "message is sent but draft cleanup could not start; do not resend", false, err), attempt)
	}
	defer state.Close()
	defer unlock()
	value, err = state.Drafts().Draft(ctx, accountValue.ID, draftID)
	if errors.Is(err, maildomain.ErrDraftNotFound) {
		return accountValue, value, attempt, existing, state.SchemaVersion(), nil
	}
	if err != nil {
		return accountValue, value, attempt, existing, state.SchemaVersion(), withAttemptDetails(cli.WrapAppError(cli.ErrorInternal, "message is sent but local draft state cannot be read; do not resend", false, err), attempt)
	}
	_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
	if err != nil {
		return accountValue, value, attempt, existing, state.SchemaVersion(), commands.markSentDraftCleanupPending(ctx, state, value, attempt, mapAccountError(err))
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return accountValue, value, attempt, existing, state.SchemaVersion(), commands.markSentDraftCleanupPending(ctx, state, value, attempt, mapCredentialError(err))
	}
	defer zeroBytes(password)
	if cleanupErr := commands.deleteAllDraftLocations(ctx, state, accountValue, password, value); cleanupErr != nil {
		return accountValue, value, attempt, existing, state.SchemaVersion(), commands.markSentDraftCleanupPending(ctx, state, value, attempt, cleanupErr)
	}
	if err := state.Drafts().Delete(ctx, accountValue.ID, draftID, commands.timeNow()); err != nil {
		return accountValue, value, attempt, existing, state.SchemaVersion(), commands.markSentDraftCleanupPending(ctx, state, value, attempt, cli.WrapAppError(cli.ErrorInternal, "message is sent and provider drafts are removed but local draft cleanup is incomplete; do not resend", false, err))
	}
	return accountValue, value, attempt, existing, state.SchemaVersion(), nil
}

func (commands accountCommands) markSentDraftCleanupPending(ctx context.Context, state *store.Store, value maildomain.Draft, attempt maildomain.OutboundAttempt, primary error) error {
	_, updateErr := state.Drafts().SetLifecycle(ctx, value.AccountID, value.ID, maildomain.DraftCleanupPending, value.RemoteLocationID, value.PreviousLocationID, attempt.ID, "sent_cleanup_pending", commands.timeNow())
	if updateErr != nil {
		primary = errors.Join(primary, updateErr)
	}
	return withAttemptDetails(cli.WrapAppError(cli.ErrorUnavailable, "message was sent but draft cleanup is pending; do not resend", true, primary), attempt)
}

func (commands accountCommands) loadDraftSendRequest(ctx context.Context, home, accountSelector, draftID string) (account.Account, maildomain.Draft, maildomain.SendRequest, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, maildomain.SendRequest{}, 0, err
	}
	defer state.Close()
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, maildomain.SendRequest{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConfiguration, "cannot resolve local state paths", false, err)
	}
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, maildomain.SendRequest{}, state.SchemaVersion(), mapAccountError(err)
	}
	value, err := state.Drafts().Draft(ctx, accountValue.ID, draftID)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, maildomain.SendRequest{}, state.SchemaVersion(), mapDraftError(err)
	}
	request, err := draftSendRequest(ctx, paths, accountValue, value)
	if err != nil {
		return accountValue, value, maildomain.SendRequest{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorUnavailable, "draft content cannot be prepared for sending", false, err)
	}
	request.IdempotencyKey = fmt.Sprintf("draft:%s:r%d", value.ID, value.Revision)
	return accountValue, value, request, state.SchemaVersion(), nil
}

func (commands accountCommands) MailDraftDelete(ctx context.Context, home, accountSelector, draftID string) (account.Account, maildomain.Draft, int, int64, error) {
	unlockDraft, err := acquireDraftOperationLock(home, draftID)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, 0, 0, err
	}
	defer unlockDraft()
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, 0, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, 0, state.SchemaVersion(), mapAccountError(err)
	}
	value, err := state.Drafts().Draft(ctx, accountValue.ID, draftID)
	if err != nil {
		return account.Account{}, maildomain.Draft{}, 0, state.SchemaVersion(), mapDraftError(err)
	}
	remoteCount := draftPresentLocationCount(value)
	if remoteCount > 0 {
		_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
		if err != nil {
			return accountValue, value, 0, state.SchemaVersion(), mapAccountError(err)
		}
		password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
		if err != nil {
			return accountValue, value, 0, state.SchemaVersion(), mapCredentialError(err)
		}
		defer zeroBytes(password)
		if err := commands.deleteAllDraftLocations(ctx, state, accountValue, password, value); err != nil {
			return accountValue, value, 0, state.SchemaVersion(), err
		}
	}
	if err := state.Drafts().Delete(ctx, accountValue.ID, draftID, commands.timeNow()); err != nil {
		return accountValue, value, remoteCount, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "provider draft cleanup completed but the local draft could not be removed", true, err)
	}
	return accountValue, value, remoteCount, state.SchemaVersion(), nil
}

func draftPresentLocationCount(value maildomain.Draft) int {
	count := 0
	for _, location := range value.Message.Locations {
		if location.Present && !location.Flags.Deleted && location.Flags.Draft {
			count++
		}
	}
	return count
}

func (commands accountCommands) deleteAllDraftLocations(ctx context.Context, state *store.Store, accountValue account.Account, password []byte, value maildomain.Draft) error {
	locations := make([]maildomain.Location, 0)
	for _, location := range value.Message.Locations {
		if location.Present && !location.Flags.Deleted && location.Flags.Draft {
			locations = append(locations, location)
		}
	}
	if len(locations) > 20 {
		return cli.NewAppError(cli.ErrorConflict, "draft has more than 20 provider versions; run mail draft sync and inspect before deleting", false)
	}
	for _, location := range locations {
		folderValue, err := state.Folders().Resolve(ctx, accountValue.ID, location.FolderID)
		if err != nil {
			return mapFolderError(err)
		}
		var result transport.LocationMutationResult
		var providerErr error
		for _, username := range imapUsernameCandidates(accountValue) {
			result, providerErr = commands.locationClient().MutateLocation(ctx, transport.AuthRequest{Username: username, Password: password}, transport.LocationMutationRequest{
				Operation: transport.LocationMutationDelete, SourceMailbox: folderValue.Name,
				ExpectedSourceUIDValidity: location.UIDValidity, SourceUID: location.UID, ExpectedSourceModSequence: location.ModSequence,
			})
			if providerErr == nil {
				break
			}
			failure, ok := transport.AsFailure(providerErr)
			if !ok || failure.Kind != transport.FailureAuthentication {
				break
			}
		}
		if providerErr != nil || result.ReconciliationRequired || !result.ProviderMutationObserved {
			if providerErr == nil {
				providerErr = errors.New("provider draft delete requires reconciliation")
			}
			return mapLocationMutationTransportError(providerErr)
		}
		if _, err := state.Mail().UpdateLocationState(ctx, accountValue.ID, location.ID, location.ModSequence, maildomain.ParseLocationFlags(result.SourceFlags), result.SourceModSequence, false, commands.timeNow()); err != nil {
			return cli.WrapAppError(cli.ErrorInternal, "provider draft was removed but local location state is incomplete; run mail draft sync", true, err)
		}
	}
	return nil
}
