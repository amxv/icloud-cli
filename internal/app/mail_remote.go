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

const maxRemoteFolderFanout = 10

func (commands accountCommands) MailRemoteSearch(
	ctx context.Context,
	home, accountSelector, folderSelector string,
	allFolders bool,
	options maildomain.SearchOptions,
) (account.Account, maildomain.RemoteSearchPage, int64, error) {
	if err := validateRemoteSearchOptions(options); err != nil {
		return account.Account{}, maildomain.RemoteSearchPage{}, 0, err
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
	folders, err := resolveRemoteSearchFolders(ctx, state, accountValue.ID, folderSelector, allFolders)
	if err != nil {
		return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), err
	}
	if allFolders && strings.TrimSpace(options.Cursor) != "" {
		return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorUsage, "one-folder remote search cursors cannot be combined with --all-folders", false)
	}
	criteria := remoteCriteria(options)
	queryHash, err := remoteSearchQueryHash(options)
	if err != nil {
		return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot hash the remote search query", false, err)
	}
	startUID := uint32(0)
	resumeUIDValidity := uint32(0)
	var pendingUIDs []uint32
	if encodedCursor := strings.TrimSpace(options.Cursor); encodedCursor != "" {
		cursor, decodeErr := maildomain.DecodeRemoteSearchCursor(encodedCursor)
		if decodeErr != nil {
			return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorUsage, "remote search cursor is invalid; use next_cursor from the same one-folder remote query", false, decodeErr)
		}
		folderValue := folders[0]
		if cursor.AccountID != accountValue.ID {
			return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "remote search cursor belongs to another account", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		if cursor.FolderID != folderValue.ID {
			return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "remote search cursor belongs to another folder", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		if cursor.QueryHash != queryHash {
			return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "remote search cursor belongs to a different query or filter set", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		if cursor.Limit != options.Limit {
			return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "remote search cursor belongs to a different result limit", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		if folderValue.UIDValidity != 0 && cursor.UIDValidity != folderValue.UIDValidity {
			return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "remote search cursor is stale because the folder identity changed", false, maildomain.ErrStaleRemoteSearchCursor)
		}
		startUID = cursor.NextUID
		resumeUIDValidity = cursor.UIDValidity
		pendingUIDs = append([]uint32(nil), cursor.PendingUIDs...)
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

	hits := make([]maildomain.RemoteSearchHit, 0, options.Limit+1)
	more, partial, candidateUIDs := false, false, 0
	nextCursor := ""
	for folderIndex, folderValue := range folders {
		folderStartUID := uint32(0)
		var folderPendingUIDs []uint32
		if !allFolders && folderIndex == 0 {
			folderStartUID = startUID
			folderPendingUIDs = pendingUIDs
			if resumeUIDValidity != 0 {
				// A resumed provider traversal is bound to the cursor's mailbox
				// incarnation even when the cached folder metadata does not have a
				// UIDVALIDITY value. The live SELECT must therefore validate the
				// cursor value rather than weakening the check to the local cache.
				folderValue.UIDValidity = resumeUIDValidity
			}
		}
		page, callErr := commands.searchRemoteFolder(ctx, accountValue, password, folderValue, criteria, options.Limit, folderStartUID, folderPendingUIDs)
		if callErr != nil {
			return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), callErr
		}
		candidateUIDs += page.CandidateUIDs
		more = more || page.MoreAvailable
		partial = partial || page.Partial
		for _, remote := range page.Messages {
			if !remoteDateMatches(remote.InternalDate, options.After, options.Before) {
				continue
			}
			reference, encodeErr := maildomain.EncodeRemoteReference(maildomain.RemoteReference{
				AccountID: accountValue.ID, FolderID: folderValue.ID,
				UIDValidity: page.UIDValidity, UID: remote.UID,
			})
			if encodeErr != nil {
				return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot encode a remote message reference", false, encodeErr)
			}
			hit := remoteSearchHit(accountValue.ID, folderValue, page.UIDValidity, reference, remote, remoteMatchedField(options))
			if cached, cachedErr := state.Mail().MessageByProviderLocation(ctx, accountValue.ID, folderValue.ID, page.UIDValidity, remote.UID); cachedErr == nil {
				hit.CachedMessageID = cached.ID
			} else if !errors.Is(cachedErr, maildomain.ErrMessageNotFound) {
				return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot inspect the local cache for a remote result", false, cachedErr)
			}
			hits = append(hits, hit)
		}
		if !allFolders && (page.MoreAvailable || page.Partial) {
			if page.NextUID == 0 && len(page.PendingUIDs) == 0 {
				return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorInternal, "remote search reported older provider space without a continuation frontier", false)
			}
			nextCursor, err = maildomain.EncodeRemoteSearchCursor(maildomain.RemoteSearchCursor{
				AccountID: accountValue.ID, FolderID: folderValue.ID, UIDValidity: page.UIDValidity,
				QueryHash: queryHash, Limit: options.Limit, NextUID: page.NextUID,
				PendingUIDs: append([]uint32(nil), page.PendingUIDs...),
			})
			if err != nil {
				return account.Account{}, maildomain.RemoteSearchPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot encode the remote search continuation", false, err)
			}
		}
	}
	sort.Slice(hits, func(left, right int) bool {
		if !hits[left].InternalDate.Equal(hits[right].InternalDate) {
			return hits[left].InternalDate.After(hits[right].InternalDate)
		}
		if hits[left].FolderID != hits[right].FolderID {
			return hits[left].FolderID < hits[right].FolderID
		}
		return hits[left].UID > hits[right].UID
	})
	if len(hits) > options.Limit {
		more = true
		hits = hits[:options.Limit]
	}
	return accountValue, maildomain.RemoteSearchPage{
		Hits: hits, Count: len(hits), More: more, Partial: partial,
		Folders: len(folders), CandidateUIDs: candidateUIDs, NextCursor: nextCursor,
	}, state.SchemaVersion(), nil
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

func resolveRemoteSearchFolders(ctx context.Context, state *store.Store, accountID, selector string, all bool) ([]folder.Folder, error) {
	selector = strings.TrimSpace(selector)
	if all && selector != "" {
		return nil, cli.NewAppError(cli.ErrorUsage, "--folder and --all-folders cannot be combined", false)
	}
	if !all {
		if selector == "" {
			selector = string(folder.KindInbox)
		}
		value, err := state.Folders().Resolve(ctx, accountID, selector)
		if err != nil {
			return nil, mapFolderError(err)
		}
		if !value.Present || value.Stale || !value.Selectable {
			return nil, cli.NewAppError(cli.ErrorConflict, "selected remote-search folder is not current and selectable", false)
		}
		return []folder.Folder{value}, nil
	}
	values, more, err := state.Folders().ListSelectable(ctx, accountID, maxRemoteFolderFanout)
	if err != nil {
		return nil, cli.WrapAppError(cli.ErrorInternal, "cannot list remote-search folders", false, err)
	}
	if more {
		return nil, cli.NewAppError(cli.ErrorUsage, fmt.Sprintf("--all-folders is limited to %d current selectable folders; narrow with --folder", maxRemoteFolderFanout), false)
	}
	if len(values) == 0 {
		return nil, cli.NewAppError(cli.ErrorNotFound, "no current selectable folders are available for remote search", false)
	}
	return values, nil
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
