package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

const (
	maxRemoteProviderCallsPerInvocation = 2
	remoteFolderSignaturePageSize       = 100
	maxRemoteSelectableFolders          = 1000
)

func (commands accountCommands) MailRemoteSearch(
	ctx context.Context,
	home, accountSelector, folderSelector string,
	allFolders bool,
	options maildomain.SearchOptions,
) (account.Account, maildomain.RemoteSearchPage, int64, error) {
	if err := validateRemoteSearchOptions(options); err != nil {
		return account.Account{}, maildomain.RemoteSearchPage{}, 0, err
	}
	if allFolders && strings.TrimSpace(folderSelector) != "" {
		return account.Account{}, maildomain.RemoteSearchPage{}, 0, cli.NewAppError(cli.ErrorUsage, "--folder and --all-folders cannot be combined", false)
	}
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.RemoteSearchPage{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), mapAccountError(err)
	}
	criteria := remoteCriteria(options)
	queryHash, err := remoteSearchQueryHash(options)
	if err != nil {
		return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot hash the remote search query", false, err)
	}
	_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
	if err != nil {
		return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)

	if allFolders {
		page, searchErr := commands.mailRemoteSearchAllFolders(ctx, state, accountValue, password, criteria, queryHash, options)
		return accountValue, page, state.SchemaVersion(), searchErr
	}
	folderValue, err := resolveRemoteSearchFolder(ctx, state, accountValue.ID, folderSelector)
	if err != nil {
		return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), err
	}
	page, searchErr := commands.mailRemoteSearchOneFolder(ctx, state, accountValue, password, folderValue, criteria, queryHash, options)
	return accountValue, page, state.SchemaVersion(), searchErr
}

func (commands accountCommands) mailRemoteSearchOneFolder(
	ctx context.Context,
	state *store.Store,
	accountValue account.Account,
	password []byte,
	folderValue folder.Folder,
	criteria transport.RemoteSearchCriteria,
	queryHash string,
	options maildomain.SearchOptions,
) (maildomain.RemoteSearchPage, error) {
	startUID := uint32(0)
	resumeUIDValidity := uint32(0)
	var pendingUIDs []uint32
	if encodedCursor := strings.TrimSpace(options.Cursor); encodedCursor != "" {
		cursor, decodeErr := maildomain.DecodeRemoteSearchCursor(encodedCursor)
		if decodeErr != nil {
			return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorUsage, "remote search cursor is invalid; use next_cursor from the same remote query", false, decodeErr)
		}
		if cursor.AllFolders || cursor.Version != 1 {
			return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorConflict, "remote search cursor belongs to an all-folder search", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		if cursor.AccountID != accountValue.ID {
			return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorConflict, "remote search cursor belongs to another account", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		if cursor.FolderID != folderValue.ID {
			return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorConflict, "remote search cursor belongs to another folder", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		if cursor.QueryHash != queryHash {
			return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorConflict, "remote search cursor belongs to a different query or filter set", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		if cursor.Limit != options.Limit {
			return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorConflict, "remote search cursor belongs to a different result limit", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		if folderValue.UIDValidity != 0 && cursor.UIDValidity != folderValue.UIDValidity {
			return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorConflict, "remote search cursor is stale because the folder identity changed", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		startUID = cursor.NextUID
		resumeUIDValidity = cursor.UIDValidity
		pendingUIDs = append([]uint32(nil), cursor.PendingUIDs...)
	}
	if resumeUIDValidity != 0 {
		// A resumed provider traversal is bound to the cursor's mailbox
		// incarnation even when cached folder metadata has no UIDVALIDITY.
		folderValue.UIDValidity = resumeUIDValidity
	}
	page, err := commands.searchRemoteFolder(ctx, accountValue, password, folderValue, criteria, options.Limit, startUID, pendingUIDs)
	if err != nil {
		return maildomain.RemoteSearchPage{}, err
	}
	hits, err := commands.remoteSearchHits(ctx, state, accountValue.ID, folderValue, page.UIDValidity, page.Messages, options)
	if err != nil {
		return maildomain.RemoteSearchPage{}, err
	}
	sortRemoteSearchHits(hits)
	nextCursor := ""
	if page.MoreAvailable || page.Partial {
		if page.NextUID == 0 && len(page.PendingUIDs) == 0 {
			return maildomain.RemoteSearchPage{}, cli.NewAppError(cli.ErrorInternal, "remote search reported older provider space without a continuation frontier", false)
		}
		nextCursor, err = maildomain.EncodeRemoteSearchCursor(maildomain.RemoteSearchCursor{
			Version: 1, AccountID: accountValue.ID, FolderID: folderValue.ID, UIDValidity: page.UIDValidity,
			QueryHash: queryHash, Limit: options.Limit, NextUID: page.NextUID,
			PendingUIDs: append([]uint32(nil), page.PendingUIDs...),
		})
		if err != nil {
			return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorInternal, "cannot encode the remote search continuation", false, err)
		}
	}
	return maildomain.RemoteSearchPage{
		Hits: hits, Count: len(hits), More: page.MoreAvailable, Partial: page.Partial,
		Folders: 1, CandidateUIDs: page.CandidateUIDs, NextCursor: nextCursor,
	}, nil
}

func (commands accountCommands) MailRemoteSummary(
	ctx context.Context,
	home, accountSelector, encodedReference string,
) (account.Account, maildomain.RemoteSearchHit, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.RemoteSearchHit{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.RemoteSearchHit{}, state.SchemaVersion(), mapAccountError(err)
	}
	reference, folderValue, err := resolveRemoteReference(ctx, state, accountValue.ID, encodedReference)
	if err != nil {
		return account.Account{}, maildomain.RemoteSearchHit{}, state.SchemaVersion(), err
	}
	_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
	if err != nil {
		return account.Account{}, maildomain.RemoteSearchHit{}, state.SchemaVersion(), mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return account.Account{}, maildomain.RemoteSearchHit{}, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)
	result, err := commands.fetchRemoteSummary(ctx, accountValue, password, folderValue, reference)
	if err != nil {
		return account.Account{}, maildomain.RemoteSearchHit{}, state.SchemaVersion(), err
	}
	hit := remoteSearchHit(accountValue.ID, folderValue, result.UIDValidity, encodedReference, result.Message, "remote_reference")
	if cached, cachedErr := state.Mail().MessageByProviderLocation(ctx, accountValue.ID, folderValue.ID, result.UIDValidity, reference.UID); cachedErr == nil {
		hit.CachedMessageID = cached.ID
	} else if !errors.Is(cachedErr, maildomain.ErrMessageNotFound) {
		return account.Account{}, maildomain.RemoteSearchHit{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot inspect the local cache for a remote message", false, cachedErr)
	}
	return accountValue, hit, state.SchemaVersion(), nil
}

func (commands accountCommands) MailHydrate(
	ctx context.Context,
	home, accountSelector string,
	encodedReferences []string,
) (account.Account, []maildomain.HydrateOutcome, int64, error) {
	accountValue, items, schemaVersion, err := commands.MailHydrateItems(ctx, home, accountSelector, encodedReferences, false)
	if err != nil {
		return account.Account{}, nil, schemaVersion, err
	}
	outcomes := make([]maildomain.HydrateOutcome, 0, len(items))
	for _, item := range items {
		if item.Error != nil {
			return accountValue, outcomes, schemaVersion, hydratePartialError(item.Error, item.Reference, outcomes)
		}
		if item.Outcome != nil {
			outcomes = append(outcomes, *item.Outcome)
		}
	}
	return accountValue, outcomes, schemaVersion, nil
}

func (commands accountCommands) MailHydrateItems(
	ctx context.Context,
	home, accountSelector string,
	encodedReferences []string,
	continueOnError bool,
) (account.Account, []cli.HydrateItemResult, int64, error) {
	if len(encodedReferences) < 1 || len(encodedReferences) > 100 {
		return account.Account{}, nil, 0, cli.NewAppError(cli.ErrorUsage, "--hydrate requires between 1 and 100 remote references", false)
	}
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, nil, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, nil, state.SchemaVersion(), mapAccountError(err)
	}
	_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
	if err != nil {
		return account.Account{}, nil, state.SchemaVersion(), mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return account.Account{}, nil, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)

	items := make([]cli.HydrateItemResult, 0, len(encodedReferences))
	seen := make(map[string]struct{}, len(encodedReferences))
	for index, encoded := range encodedReferences {
		encoded = strings.TrimSpace(encoded)
		if _, duplicate := seen[encoded]; duplicate {
			continue
		}
		seen[encoded] = struct{}{}
		item := commands.hydrateItem(ctx, state, paths, accountValue, password, index, encoded)
		items = append(items, item)
		if item.Error != nil && !continueOnError {
			break
		}
	}
	return accountValue, items, state.SchemaVersion(), nil
}

func (commands accountCommands) hydrateItem(
	ctx context.Context,
	state *store.Store,
	paths config.AppPaths,
	accountValue account.Account,
	password []byte,
	index int,
	encoded string,
) cli.HydrateItemResult {
	item := cli.HydrateItemResult{Index: index, Reference: encoded, CommittedState: "not_committed"}
	reference, folderValue, resolveErr := resolveRemoteReference(ctx, state, accountValue.ID, encoded)
	if resolveErr != nil {
		item.Error = cli.NormalizeError(resolveErr)
		return item
	}
	if cached, cachedErr := state.Mail().MessageByProviderLocation(ctx, accountValue.ID, folderValue.ID, reference.UIDValidity, reference.UID); cachedErr == nil {
		outcome := maildomain.HydrateOutcome{
			Reference: encoded, MessageID: cached.ID,
			LocationID: providerLocationID(cached, folderValue.ID, reference.UIDValidity, reference.UID),
		}
		item.Outcome = &outcome
		item.CommittedState = "already_committed"
		return item
	} else if !errors.Is(cachedErr, maildomain.ErrMessageNotFound) {
		item.Error = cli.NormalizeError(cli.WrapAppError(cli.ErrorInternal, "cannot inspect the local cache before hydration", false, cachedErr))
		return item
	}

	stager := commands.blobStager(paths)
	attachmentSet := newStagedAttachmentSet(commands.attachmentBlobStore(paths))
	result, fetchErr := commands.fetchRemoteMessage(ctx, accountValue, password, folderValue, reference, stager)
	if fetchErr != nil {
		attachmentSet.Abort()
		item.Error = cli.NormalizeError(fetchErr)
		return item
	}
	fetched := result.Message
	defer fetched.Raw.Abort()
	records, prepareErr := commands.prepareFetchedMailRecords(ctx, state, accountValue, folderValue, result.UIDValidity, []transport.FetchedMessage{fetched}, attachmentSet)
	if prepareErr != nil {
		attachmentSet.Abort()
		var preparation *fetchedPreparationError
		if errors.As(prepareErr, &preparation) {
			item.Error = cli.NormalizeError(preparation.appError())
			return item
		}
		item.Error = cli.NormalizeError(cli.WrapAppError(cli.ErrorUnavailable, "cannot safely prepare the remote message", false, prepareErr))
		return item
	}
	if err := fetched.Raw.Commit(); err != nil {
		attachmentSet.Abort()
		item.Error = cli.NormalizeError(cli.WrapAppError(cli.ErrorInternal, "cannot commit the hydrated raw message", true, err))
		return item
	}
	if err := attachmentSet.Commit(); err != nil {
		item.Error = cli.NormalizeError(cli.WrapAppError(cli.ErrorInternal, "cannot commit a hydrated attachment", true, err))
		return item
	}
	commit, commitErr := state.Mail().CommitHydration(ctx, maildomain.HydratePage{
		AccountID: accountValue.ID, FolderID: folderValue.ID, UIDValidity: result.UIDValidity,
		AttemptedAt: commands.timeNow(), Records: records,
	})
	if commitErr != nil {
		item.Error = cli.NormalizeError(cli.WrapAppError(cli.ErrorInternal, "cannot atomically commit the hydrated message", true, commitErr))
		return item
	}
	item.CommittedState = "committed"
	outcome := maildomain.HydrateOutcome{Reference: encoded, Created: commit.Created == 1, Updated: commit.Updated == 1}
	item.Outcome = &outcome
	message, readErr := state.Mail().MessageByProviderLocation(ctx, accountValue.ID, folderValue.ID, result.UIDValidity, reference.UID)
	if readErr != nil {
		item.Error = cli.NormalizeError(cli.WrapAppError(cli.ErrorInternal, "cannot reload the hydrated message", false, readErr))
		return item
	}
	outcome.MessageID = message.ID
	outcome.LocationID = providerLocationID(message, folderValue.ID, result.UIDValidity, reference.UID)
	item.Outcome = &outcome
	return item
}

func hydratePartialError(err error, reference string, outcomes []maildomain.HydrateOutcome) error {
	appErr := cli.NormalizeError(err)
	appErr.Details = map[string]any{
		"failed_reference": strings.TrimSpace(reference),
		"completed":        append([]maildomain.HydrateOutcome(nil), outcomes...),
		"completed_count":  len(outcomes),
		"partial":          len(outcomes) > 0,
	}
	return appErr
}

func validateRemoteSearchOptions(options maildomain.SearchOptions) error {
	if options.Limit < 1 || options.Limit > 100 {
		return cli.NewAppError(cli.ErrorUsage, "remote search limit must be between 1 and 100", false)
	}
	if options.ResultKind != "" && options.ResultKind != maildomain.SearchMessages {
		return cli.WrapAppError(cli.ErrorUsage, "remote search returns messages only; cache results before local thread reconstruction", false, maildomain.ErrUnsupportedRemoteFilter)
	}
	if strings.TrimSpace(options.AttachmentFilename) != "" || strings.TrimSpace(options.AttachmentType) != "" {
		return cli.WrapAppError(cli.ErrorUsage, "remote search cannot filter attachment metadata; hydrate or synchronize before using attachment filters", false, maildomain.ErrUnsupportedRemoteFilter)
	}
	if options.After != nil && options.Before != nil && !options.After.Before(*options.Before) {
		return cli.NewAppError(cli.ErrorUsage, "--after must precede --before", false)
	}
	return nil
}

type remoteSearchHeadKey struct {
	folderID string
	uid      uint32
}

type allFolderRemoteSearchWork struct {
	providerCalls  int
	searchCalls    int
	summaryFetches int
	candidateUIDs  int
	contacted      map[string]struct{}
	summaries      map[remoteSearchHeadKey]transport.RemoteMessageSummary
}

func newAllFolderRemoteSearchWork() *allFolderRemoteSearchWork {
	return &allFolderRemoteSearchWork{
		contacted: make(map[string]struct{}),
		summaries: make(map[remoteSearchHeadKey]transport.RemoteMessageSummary),
	}
}

func (commands accountCommands) mailRemoteSearchAllFolders(
	ctx context.Context,
	state *store.Store,
	accountValue account.Account,
	password []byte,
	criteria transport.RemoteSearchCriteria,
	queryHash string,
	options maildomain.SearchOptions,
) (maildomain.RemoteSearchPage, error) {
	folderSignature, folderCount, err := remoteSelectableFolderSignature(ctx, state, accountValue.ID)
	if err != nil {
		return maildomain.RemoteSearchPage{}, err
	}
	if folderCount == 0 {
		return maildomain.RemoteSearchPage{}, cli.NewAppError(cli.ErrorNotFound, "no current selectable folders are available for remote search", false)
	}
	if folderCount > maxRemoteSelectableFolders {
		return maildomain.RemoteSearchPage{}, cli.NewAppError(cli.ErrorUsage, fmt.Sprintf("all-folder remote search supports at most %d current selectable folders", maxRemoteSelectableFolders), false)
	}

	cursor := maildomain.RemoteSearchCursor{
		Version: 2, AccountID: accountValue.ID, QueryHash: queryHash, Limit: options.Limit,
		AllFolders: true, FolderSignature: folderSignature, FolderCount: folderCount,
	}
	if encodedCursor := strings.TrimSpace(options.Cursor); encodedCursor != "" {
		decoded, decodeErr := maildomain.DecodeRemoteSearchCursor(encodedCursor)
		if decodeErr != nil {
			return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorUsage, "remote search cursor is invalid; use next_cursor from the same remote query", false, decodeErr)
		}
		if !decoded.AllFolders || decoded.Version != 2 {
			return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorConflict, "remote search cursor belongs to a one-folder search", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		if decoded.AccountID != accountValue.ID {
			return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorConflict, "remote search cursor belongs to another account", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		if decoded.QueryHash != queryHash {
			return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorConflict, "remote search cursor belongs to a different query or filter set", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		if decoded.Limit != options.Limit {
			return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorConflict, "remote search cursor belongs to a different result limit", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		if decoded.FolderCount != folderCount || decoded.FolderSignature != folderSignature {
			return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorConflict, "remote search cursor is stale because the selectable folder set or mailbox identity changed", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		cursor = decoded
	}

	work := newAllFolderRemoteSearchWork()
	if !cursor.InventoryComplete {
		if err := commands.seedRemoteSearchFolders(ctx, state, accountValue, password, criteria, &cursor, work); err != nil {
			return maildomain.RemoteSearchPage{}, err
		}
		if !cursor.InventoryComplete {
			return encodeAllFolderRemoteSearchPage(cursor, nil, false, true, work)
		}
	}

	if err := commands.fillMissingRemoteSearchHeads(ctx, state, accountValue, password, criteria, &cursor, work); err != nil {
		return maildomain.RemoteSearchPage{}, err
	}
	if !allRemoteSearchFoldersReady(cursor.Folders) {
		return encodeAllFolderRemoteSearchPage(cursor, nil, false, true, work)
	}

	hits := make([]maildomain.RemoteSearchHit, 0, options.Limit)
	for len(hits) < options.Limit {
		index := newestRemoteSearchHeadIndex(cursor.Folders)
		if index < 0 {
			break
		}
		folderState := &cursor.Folders[index]
		folderValue, resolveErr := resolveRemoteCursorFolder(ctx, state, accountValue.ID, *folderState)
		if resolveErr != nil {
			return maildomain.RemoteSearchPage{}, resolveErr
		}
		head := *folderState.Head
		key := remoteSearchHeadKey{folderID: folderState.FolderID, uid: head.UID}
		summary, ok := work.summaries[key]
		if !ok {
			if work.providerCalls >= maxRemoteProviderCallsPerInvocation {
				break
			}
			result, fetchErr := commands.fetchRemoteSummary(ctx, accountValue, password, folderValue, maildomain.RemoteReference{
				AccountID: accountValue.ID, FolderID: folderState.FolderID, UIDValidity: folderState.UIDValidity, UID: head.UID,
			})
			if fetchErr != nil {
				return maildomain.RemoteSearchPage{}, fetchErr
			}
			work.summaryFetches++
			work.providerCalls++
			work.contacted[folderState.FolderID] = struct{}{}
			if result.UIDValidity != folderState.UIDValidity || result.Message.UID != head.UID || !result.Message.InternalDate.UTC().Equal(head.InternalDate.UTC()) {
				return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorConflict, "remote search cursor is stale because a buffered merge head changed", false, maildomain.ErrStaleRemoteSearchCursor)
			}
			summary = result.Message
		}
		hit, hitErr := commands.remoteSearchHit(ctx, state, accountValue.ID, folderValue, folderState.UIDValidity, summary, options)
		if hitErr != nil {
			return maildomain.RemoteSearchPage{}, hitErr
		}
		hits = append(hits, hit)
		delete(work.summaries, key)
		folderState.Head = nil
		if folderState.NextUID == 0 && len(folderState.PendingUIDs) == 0 {
			folderState.Done = true
			continue
		}
		if len(hits) >= options.Limit {
			break
		}
		if work.providerCalls >= maxRemoteProviderCallsPerInvocation {
			break
		}
		folderValue.UIDValidity = folderState.UIDValidity
		if err := commands.advanceRemoteSearchFolder(ctx, accountValue, password, folderValue, criteria, folderState, work); err != nil {
			return maildomain.RemoteSearchPage{}, err
		}
		if !allRemoteSearchFoldersReady(cursor.Folders) {
			break
		}
	}

	if allRemoteSearchFoldersDone(cursor.Folders) {
		sortRemoteSearchHits(hits)
		return maildomain.RemoteSearchPage{
			Hits: hits, Count: len(hits), Folders: len(work.contacted), CandidateUIDs: work.candidateUIDs,
		}, nil
	}
	more := hasBufferedRemoteSearchHead(cursor.Folders)
	partial := !allRemoteSearchFoldersReady(cursor.Folders)
	if !more && !partial {
		// A non-terminal cursor must always explain why another invocation is
		// useful. This is defensive against future state-shape changes.
		partial = true
	}
	sortRemoteSearchHits(hits)
	return encodeAllFolderRemoteSearchPage(cursor, hits, more, partial, work)
}

func (commands accountCommands) seedRemoteSearchFolders(
	ctx context.Context,
	state *store.Store,
	accountValue account.Account,
	password []byte,
	criteria transport.RemoteSearchCriteria,
	cursor *maildomain.RemoteSearchCursor,
	work *allFolderRemoteSearchWork,
) error {
	remaining := maxRemoteProviderCallsPerInvocation - work.providerCalls
	if remaining <= 0 {
		return nil
	}
	values, more, _, err := state.Folders().ListSelectablePage(ctx, accountValue.ID, remaining, cursor.FolderAfterID)
	if err != nil {
		return cli.WrapAppError(cli.ErrorInternal, "cannot page selectable folders for remote search", false, err)
	}
	if len(values) == 0 {
		return cli.NewAppError(cli.ErrorInternal, "selectable folder traversal ended before the remote cursor inventory was complete", false)
	}
	for _, folderValue := range values {
		folderState := maildomain.RemoteSearchFolderCursor{FolderID: folderValue.ID}
		if err := commands.advanceRemoteSearchFolder(ctx, accountValue, password, folderValue, criteria, &folderState, work); err != nil {
			return err
		}
		cursor.Folders = append(cursor.Folders, folderState)
		cursor.FolderAfterID = folderValue.ID
	}
	if !more {
		if len(cursor.Folders) != cursor.FolderCount {
			return cli.NewAppError(cli.ErrorInternal, "selectable folder count changed during bounded remote-search inventory traversal", false)
		}
		cursor.InventoryComplete = true
		cursor.FolderAfterID = ""
	}
	return nil
}

func (commands accountCommands) fillMissingRemoteSearchHeads(
	ctx context.Context,
	state *store.Store,
	accountValue account.Account,
	password []byte,
	criteria transport.RemoteSearchCriteria,
	cursor *maildomain.RemoteSearchCursor,
	work *allFolderRemoteSearchWork,
) error {
	for index := range cursor.Folders {
		folderState := &cursor.Folders[index]
		if folderState.Done || folderState.Head != nil {
			continue
		}
		if work.providerCalls >= maxRemoteProviderCallsPerInvocation {
			return nil
		}
		folderValue, err := resolveRemoteCursorFolder(ctx, state, accountValue.ID, *folderState)
		if err != nil {
			return err
		}
		folderValue.UIDValidity = folderState.UIDValidity
		if err := commands.advanceRemoteSearchFolder(ctx, accountValue, password, folderValue, criteria, folderState, work); err != nil {
			return err
		}
	}
	return nil
}

func (commands accountCommands) advanceRemoteSearchFolder(
	ctx context.Context,
	accountValue account.Account,
	password []byte,
	folderValue folder.Folder,
	criteria transport.RemoteSearchCriteria,
	state *maildomain.RemoteSearchFolderCursor,
	work *allFolderRemoteSearchWork,
) error {
	if work.providerCalls >= maxRemoteProviderCallsPerInvocation {
		return cli.NewAppError(cli.ErrorInternal, "remote-search folder-scan budget was exceeded", false)
	}
	if state.UIDValidity != 0 {
		folderValue.UIDValidity = state.UIDValidity
	}
	page, err := commands.searchRemoteFolder(ctx, accountValue, password, folderValue, criteria, 1, state.NextUID, state.PendingUIDs)
	if err != nil {
		return err
	}
	work.searchCalls++
	work.providerCalls++
	work.candidateUIDs += page.CandidateUIDs
	work.contacted[folderValue.ID] = struct{}{}
	if page.UIDValidity == 0 || (state.UIDValidity != 0 && page.UIDValidity != state.UIDValidity) {
		return cli.WrapAppError(cli.ErrorConflict, "remote search cursor is stale because a folder mailbox identity changed", false, maildomain.ErrStaleRemoteSearchCursor)
	}
	if len(page.Messages) > 1 {
		return cli.NewAppError(cli.ErrorInternal, "bounded remote-search head scan returned more than one merge head", false)
	}
	hasContinuation := page.NextUID != 0 || len(page.PendingUIDs) != 0
	if (page.MoreAvailable || page.Partial) != hasContinuation {
		return cli.NewAppError(cli.ErrorInternal, "remote-search folder scan returned an inconsistent continuation frontier", false)
	}
	state.UIDValidity = page.UIDValidity
	state.NextUID = page.NextUID
	state.PendingUIDs = append(state.PendingUIDs[:0], page.PendingUIDs...)
	state.Head = nil
	state.Done = false
	if len(page.Messages) == 0 {
		if !hasContinuation {
			state.Done = true
		}
		return nil
	}
	summary := page.Messages[0]
	if !remoteDateMatches(summary.InternalDate, criteria.After, criteria.Before) {
		return cli.NewAppError(cli.ErrorInternal, "remote-search folder scan returned a merge head outside the exact date filter", false)
	}
	state.Head = &maildomain.RemoteSearchCursorHead{UID: summary.UID, InternalDate: summary.InternalDate.UTC()}
	work.summaries[remoteSearchHeadKey{folderID: folderValue.ID, uid: summary.UID}] = summary
	return nil
}

func remoteSelectableFolderSignature(ctx context.Context, state *store.Store, accountID string) (string, int, error) {
	hash := sha256.New()
	after := ""
	count := 0
	for {
		values, more, next, err := state.Folders().ListSelectablePage(ctx, accountID, remoteFolderSignaturePageSize, after)
		if err != nil {
			return "", 0, cli.WrapAppError(cli.ErrorInternal, "cannot inspect selectable folders for remote search", false, err)
		}
		for _, value := range values {
			count++
			if _, err := fmt.Fprintf(hash, "%d:%s:%d\n", len(value.ID), value.ID, value.UIDValidity); err != nil {
				return "", 0, cli.WrapAppError(cli.ErrorInternal, "cannot hash selectable folder state", false, err)
			}
		}
		if !more {
			break
		}
		if next == "" || next == after {
			return "", 0, cli.NewAppError(cli.ErrorInternal, "selectable folder keyset traversal did not advance", false)
		}
		after = next
	}
	if _, err := fmt.Fprintf(hash, "count:%d\n", count); err != nil {
		return "", 0, cli.WrapAppError(cli.ErrorInternal, "cannot finalize selectable folder state hash", false, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), count, nil
}

func resolveRemoteSearchFolder(ctx context.Context, state *store.Store, accountID, selector string) (folder.Folder, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		selector = string(folder.KindInbox)
	}
	value, err := state.Folders().Resolve(ctx, accountID, selector)
	if err != nil {
		return folder.Folder{}, mapFolderError(err)
	}
	if !value.Present || value.Stale || !value.Selectable {
		return folder.Folder{}, cli.NewAppError(cli.ErrorConflict, "selected remote-search folder is not current and selectable", false)
	}
	return value, nil
}

func resolveRemoteCursorFolder(ctx context.Context, state *store.Store, accountID string, cursor maildomain.RemoteSearchFolderCursor) (folder.Folder, error) {
	value, err := state.Folders().Resolve(ctx, accountID, cursor.FolderID)
	if err != nil {
		return folder.Folder{}, mapFolderError(err)
	}
	if !value.Present || value.Stale || !value.Selectable {
		return folder.Folder{}, cli.WrapAppError(cli.ErrorConflict, "remote search cursor is stale because a folder is no longer current and selectable", false, maildomain.ErrStaleRemoteSearchCursor)
	}
	if value.UIDValidity != 0 && cursor.UIDValidity != 0 && value.UIDValidity != cursor.UIDValidity {
		return folder.Folder{}, cli.WrapAppError(cli.ErrorConflict, "remote search cursor is stale because a folder mailbox identity changed", false, maildomain.ErrStaleRemoteSearchCursor)
	}
	return value, nil
}

func allRemoteSearchFoldersReady(states []maildomain.RemoteSearchFolderCursor) bool {
	for _, state := range states {
		if !state.Done && state.Head == nil {
			return false
		}
	}
	return true
}

func allRemoteSearchFoldersDone(states []maildomain.RemoteSearchFolderCursor) bool {
	for _, state := range states {
		if !state.Done {
			return false
		}
	}
	return true
}

func hasBufferedRemoteSearchHead(states []maildomain.RemoteSearchFolderCursor) bool {
	for _, state := range states {
		if state.Head != nil {
			return true
		}
	}
	return false
}

func newestRemoteSearchHeadIndex(states []maildomain.RemoteSearchFolderCursor) int {
	selected := -1
	for index := range states {
		head := states[index].Head
		if head == nil {
			continue
		}
		if selected < 0 {
			selected = index
			continue
		}
		current := states[selected].Head
		if head.InternalDate.After(current.InternalDate) ||
			(head.InternalDate.Equal(current.InternalDate) && states[index].FolderID < states[selected].FolderID) ||
			(head.InternalDate.Equal(current.InternalDate) && states[index].FolderID == states[selected].FolderID && head.UID > current.UID) {
			selected = index
		}
	}
	return selected
}

func encodeAllFolderRemoteSearchPage(cursor maildomain.RemoteSearchCursor, hits []maildomain.RemoteSearchHit, more, partial bool, work *allFolderRemoteSearchWork) (maildomain.RemoteSearchPage, error) {
	if hits == nil {
		hits = []maildomain.RemoteSearchHit{}
	}
	encoded, err := maildomain.EncodeRemoteSearchCursor(cursor)
	if err != nil {
		return maildomain.RemoteSearchPage{}, cli.WrapAppError(cli.ErrorInternal, "cannot encode the all-folder remote search continuation", false, err)
	}
	return maildomain.RemoteSearchPage{
		Hits: hits, Count: len(hits), More: more, Partial: partial, NextCursor: encoded,
		Folders: len(work.contacted), CandidateUIDs: work.candidateUIDs,
	}, nil
}

func resolveRemoteReference(ctx context.Context, state *store.Store, accountID, encoded string) (maildomain.RemoteReference, folder.Folder, error) {
	reference, err := maildomain.DecodeRemoteReference(encoded)
	if err != nil || reference.AccountID != accountID {
		return maildomain.RemoteReference{}, folder.Folder{}, cli.WrapAppError(cli.ErrorUsage, "remote message reference is invalid for the selected account", false, maildomain.ErrInvalidRemoteReference)
	}
	folderValue, err := state.Folders().Resolve(ctx, accountID, reference.FolderID)
	if err != nil {
		return maildomain.RemoteReference{}, folder.Folder{}, mapFolderError(err)
	}
	if !folderValue.Present || folderValue.Stale || !folderValue.Selectable {
		return maildomain.RemoteReference{}, folder.Folder{}, cli.WrapAppError(cli.ErrorConflict, "remote message folder is no longer current and selectable", false, maildomain.ErrStaleRemoteReference)
	}
	if folderValue.UIDValidity != 0 && folderValue.UIDValidity != reference.UIDValidity {
		return maildomain.RemoteReference{}, folder.Folder{}, cli.WrapAppError(cli.ErrorConflict, "remote message reference is stale because the folder identity changed", false, maildomain.ErrStaleRemoteReference)
	}
	return reference, folderValue, nil
}

func (commands accountCommands) searchRemoteFolder(ctx context.Context, accountValue account.Account, password []byte, folderValue folder.Folder, criteria transport.RemoteSearchCriteria, limit int, startUID uint32, pendingUIDs []uint32) (transport.RemoteSearchPage, error) {
	var lastErr error
	for _, username := range imapUsernameCandidates(accountValue) {
		page, err := commands.remoteClient().SearchRemote(ctx, transport.AuthRequest{Username: username, Password: password}, transport.RemoteSearchRequest{
			Mailbox: folderValue.Name, ExpectedUIDValidity: folderValue.UIDValidity,
			Criteria: criteria, Limit: limit, StartUID: startUID, PendingUIDs: append([]uint32(nil), pendingUIDs...),
		})
		if err == nil {
			return page, nil
		}
		lastErr = err
		if !isAuthenticationFailure(err) {
			break
		}
	}
	return transport.RemoteSearchPage{}, mapRemoteTransportError(lastErr, "remote search", folderValue.Name)
}

func remoteSearchQueryHash(options maildomain.SearchOptions) (string, error) {
	type signature struct {
		Version      int      `json:"v"`
		Text         []string `json:"text,omitempty"`
		Sender       string   `json:"sender,omitempty"`
		Participant  string   `json:"participant,omitempty"`
		Subject      string   `json:"subject,omitempty"`
		RFCMessageID string   `json:"rfc_id,omitempty"`
		Unread       *bool    `json:"unread,omitempty"`
		Flagged      *bool    `json:"flagged,omitempty"`
		After        string   `json:"after,omitempty"`
		Before       string   `json:"before,omitempty"`
	}
	value := signature{
		Version: 1, Text: strings.Fields(options.Query), Sender: strings.TrimSpace(options.Sender),
		Participant: strings.TrimSpace(options.Participant), Subject: strings.TrimSpace(options.Subject),
		RFCMessageID: strings.Trim(strings.TrimSpace(options.RFCMessageID), "<>"),
		Unread:       options.Unread, Flagged: options.Flagged,
	}
	if options.After != nil {
		value.After = options.After.UTC().Format(time.RFC3339Nano)
	}
	if options.Before != nil {
		value.Before = options.Before.UTC().Format(time.RFC3339Nano)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func (commands accountCommands) fetchRemoteSummary(ctx context.Context, accountValue account.Account, password []byte, folderValue folder.Folder, reference maildomain.RemoteReference) (transport.RemoteSummaryResult, error) {
	var lastErr error
	for _, username := range imapUsernameCandidates(accountValue) {
		result, err := commands.remoteClient().FetchRemoteSummary(ctx, transport.AuthRequest{Username: username, Password: password}, transport.RemoteFetchRequest{
			Mailbox: folderValue.Name, ExpectedUIDValidity: reference.UIDValidity, UID: reference.UID,
		})
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isAuthenticationFailure(err) {
			break
		}
	}
	return transport.RemoteSummaryResult{}, mapRemoteTransportError(lastErr, "remote message lookup", folderValue.Name)
}

func (commands accountCommands) fetchRemoteMessage(ctx context.Context, accountValue account.Account, password []byte, folderValue folder.Folder, reference maildomain.RemoteReference, stager blob.Stager) (transport.RemoteFetchResult, error) {
	var lastErr error
	for _, username := range imapUsernameCandidates(accountValue) {
		result, err := commands.remoteClient().FetchRemoteMessage(ctx, transport.AuthRequest{Username: username, Password: password}, transport.RemoteFetchRequest{
			Mailbox: folderValue.Name, ExpectedUIDValidity: reference.UIDValidity, UID: reference.UID,
		}, stager)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isAuthenticationFailure(err) {
			break
		}
	}
	return transport.RemoteFetchResult{}, mapRemoteTransportError(lastErr, "remote message hydration", folderValue.Name)
}

func remoteCriteria(options maildomain.SearchOptions) transport.RemoteSearchCriteria {
	return transport.RemoteSearchCriteria{
		Text: strings.Fields(options.Query), Sender: options.Sender, Participant: options.Participant,
		Subject: options.Subject, RFCMessageID: options.RFCMessageID,
		Unread: options.Unread, Flagged: options.Flagged, After: options.After, Before: options.Before,
	}
}

func remoteDateMatches(value time.Time, after, before *time.Time) bool {
	if after != nil && value.Before(after.UTC()) {
		return false
	}
	if before != nil && !value.Before(before.UTC()) {
		return false
	}
	return true
}

func (commands accountCommands) remoteSearchHits(
	ctx context.Context,
	state *store.Store,
	accountID string,
	folderValue folder.Folder,
	uidValidity uint32,
	values []transport.RemoteMessageSummary,
	options maildomain.SearchOptions,
) ([]maildomain.RemoteSearchHit, error) {
	hits := make([]maildomain.RemoteSearchHit, 0, len(values))
	for _, value := range values {
		if !remoteDateMatches(value.InternalDate, options.After, options.Before) {
			continue
		}
		hit, err := commands.remoteSearchHit(ctx, state, accountID, folderValue, uidValidity, value, options)
		if err != nil {
			return nil, err
		}
		hits = append(hits, hit)
	}
	return hits, nil
}

func (commands accountCommands) remoteSearchHit(
	ctx context.Context,
	state *store.Store,
	accountID string,
	folderValue folder.Folder,
	uidValidity uint32,
	value transport.RemoteMessageSummary,
	options maildomain.SearchOptions,
) (maildomain.RemoteSearchHit, error) {
	reference, err := maildomain.EncodeRemoteReference(maildomain.RemoteReference{
		AccountID: accountID, FolderID: folderValue.ID, UIDValidity: uidValidity, UID: value.UID,
	})
	if err != nil {
		return maildomain.RemoteSearchHit{}, cli.WrapAppError(cli.ErrorInternal, "cannot encode a remote message reference", false, err)
	}
	hit := remoteSearchHit(accountID, folderValue, uidValidity, reference, value, remoteMatchedField(options))
	if cached, cachedErr := state.Mail().MessageByProviderLocation(ctx, accountID, folderValue.ID, uidValidity, value.UID); cachedErr == nil {
		hit.CachedMessageID = cached.ID
	} else if !errors.Is(cachedErr, maildomain.ErrMessageNotFound) {
		return maildomain.RemoteSearchHit{}, cli.WrapAppError(cli.ErrorInternal, "cannot inspect the local cache for a remote result", false, cachedErr)
	}
	return hit, nil
}

func sortRemoteSearchHits(hits []maildomain.RemoteSearchHit) {
	sort.Slice(hits, func(left, right int) bool {
		if !hits[left].InternalDate.Equal(hits[right].InternalDate) {
			return hits[left].InternalDate.After(hits[right].InternalDate)
		}
		if hits[left].FolderID != hits[right].FolderID {
			return hits[left].FolderID < hits[right].FolderID
		}
		return hits[left].UID > hits[right].UID
	})
}

func remoteSearchHit(accountID string, folderValue folder.Folder, uidValidity uint32, reference string, value transport.RemoteMessageSummary, matchedField string) maildomain.RemoteSearchHit {
	return maildomain.RemoteSearchHit{
		Reference: reference, AccountID: accountID, FolderID: folderValue.ID, FolderName: folderValue.Name,
		UIDValidity: uidValidity, UID: value.UID, InternalDate: value.InternalDate,
		From: firstEnvelopeAddress(value.Envelope.From), To: envelopeAddresses(value.Envelope.To),
		CC: envelopeAddresses(value.Envelope.CC), Subject: value.Envelope.Subject,
		RFCMessageID: strings.Trim(strings.TrimSpace(value.Envelope.MessageID), "<>"),
		Flags:        maildomain.ParseLocationFlags(value.Flags), Snippet: value.Snippet,
		RFC822Size: value.RFC822Size, MatchedField: matchedField,
	}
}

func envelopeAddresses(values []transport.EnvelopeAddress) []maildomain.Address {
	result := make([]maildomain.Address, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.Email) != "" {
			result = append(result, maildomain.Address{DisplayName: value.Name, Email: value.Email})
		}
	}
	return result
}

func firstEnvelopeAddress(values []transport.EnvelopeAddress) maildomain.Address {
	addresses := envelopeAddresses(values)
	if len(addresses) == 0 {
		return maildomain.Address{}
	}
	return addresses[0]
}

func remoteMatchedField(options maildomain.SearchOptions) string {
	switch {
	case strings.TrimSpace(options.Query) != "":
		return "provider_text"
	case strings.TrimSpace(options.Sender) != "":
		return "sender"
	case strings.TrimSpace(options.Participant) != "":
		return "participant"
	case strings.TrimSpace(options.Subject) != "":
		return "subject"
	case strings.TrimSpace(options.RFCMessageID) != "":
		return "rfc_message_id"
	case options.Unread != nil:
		return "unread"
	case options.Flagged != nil:
		return "flagged"
	case options.After != nil || options.Before != nil:
		return "date"
	default:
		return "remote"
	}
}

func providerLocationID(message maildomain.Message, folderID string, uidValidity, uid uint32) string {
	for _, location := range message.Locations {
		if location.FolderID == folderID && location.UIDValidity == uidValidity && location.UID == uid {
			return location.ID
		}
	}
	return ""
}

func isAuthenticationFailure(err error) bool {
	failure, ok := transport.AsFailure(err)
	return ok && failure.Kind == transport.FailureAuthentication
}

func mapRemoteTransportError(err error, operation, folderName string) error {
	var validity *transport.UIDValidityError
	if errors.As(err, &validity) {
		return cli.WrapAppError(cli.ErrorConflict, "remote message reference is stale because the folder identity changed", false, maildomain.ErrStaleRemoteReference)
	}
	failure, ok := transport.AsFailure(err)
	if !ok {
		return cli.WrapAppError(cli.ErrorUnavailable, operation+" failed", false, err)
	}
	code := cli.ErrorUnavailable
	if failure.Kind == transport.FailureAuthentication {
		code = cli.ErrorAuthentication
	} else if failure.Kind == transport.FailureConfiguration {
		code = cli.ErrorConfiguration
	}
	message := operation + " failed"
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
