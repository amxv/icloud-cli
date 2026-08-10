package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
)

const (
	defaultMailSelectionTTL   = 15 * time.Minute
	maximumMailSelectionTTL   = 24 * time.Hour
	maximumMailSelectionItems = 100
)

func (commands accountCommands) MailSelectionCreate(ctx context.Context, home, accountSelector string, request maildomain.SelectionCreateRequest) (account.Account, maildomain.MailSelection, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.MailSelection{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), mapAccountError(err)
	}
	limit := request.MaximumMembership
	if limit == 0 {
		limit = maximumMailSelectionItems
	}
	if limit < 1 || limit > maximumMailSelectionItems {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorUsage, "MailSelection membership limit must be between 1 and 100", false)
	}
	ttl := request.TTL
	if ttl == 0 {
		ttl = defaultMailSelectionTTL
	}
	if ttl < time.Minute || ttl > maximumMailSelectionTTL {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorUsage, "MailSelection TTL must be between 1m and 24h", false)
	}
	if (len(request.Targets) > 0) == (request.Search != nil) {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorUsage, "MailSelection create requires exactly one source: explicit targets or one local search page", false)
	}

	var (
		sourceKind        maildomain.SelectionSourceKind
		sourceDescription string
		sourceQueryHash   string
		messageIDs        []string
	)
	if request.Search != nil {
		sourceKind = maildomain.SelectionSourceSearch
		sourceDescription = "one bounded local search page"
		messageIDs, sourceQueryHash, err = resolveSelectionSearchPage(ctx, state, accountValue.ID, request.SearchFolder, *request.Search, limit)
	} else {
		sourceKind = maildomain.SelectionSourceExplicit
		sourceDescription = "explicit stable message/thread targets"
		messageIDs, sourceQueryHash, err = resolveSelectionExplicitTargets(ctx, state, accountValue.ID, request.Targets, limit)
	}
	if err != nil {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), err
	}

	var scopeFolder folder.Folder
	if strings.TrimSpace(request.ScopeFolder) != "" {
		scopeFolder, err = resolveMailFolder(ctx, state, accountValue.ID, request.ScopeFolder)
		if err != nil {
			return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), err
		}
	}
	items := make([]maildomain.MailSelectionItem, 0, len(messageIDs))
	for _, messageID := range messageIDs {
		message, readErr := state.Mail().Message(ctx, accountValue.ID, messageID)
		if readErr != nil {
			if errors.Is(readErr, maildomain.ErrMessageNotFound) {
				return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "a reviewed logical target disappeared while creating MailSelection", false, readErr)
			}
			return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read MailSelection target", false, readErr)
		}
		item := maildomain.MailSelectionItem{MessageID: message.ID}
		if scopeFolder.ID != "" {
			matches := make([]maildomain.Location, 0, 1)
			for _, location := range message.Locations {
				if location.FolderID == scopeFolder.ID && location.Present && location.Flags.Eligible() {
					matches = append(matches, location)
				}
			}
			if len(matches) == 0 {
				continue
			}
			if len(matches) > 1 {
				return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorConflict, "MailSelection scope contains multiple eligible provider copies for one logical message", false)
			}
			location := matches[0]
			item.LocationID, item.FolderID = location.ID, location.FolderID
			item.UIDValidity, item.UID, item.ModSequence = location.UIDValidity, location.UID, location.ModSequence
		}
		item.Ordinal = len(items)
		items = append(items, item)
	}
	if len(items) == 0 {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorNotFound, "MailSelection source has no targets in the reviewed scope", false)
	}
	if len(items) > limit {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorSafety, "MailSelection membership exceeds the bounded review limit", false)
	}
	now := commands.timeNow()
	selection := maildomain.MailSelection{
		AccountID: accountValue.ID, SourceKind: sourceKind, SourceDescription: sourceDescription,
		SourceQueryHash: sourceQueryHash, ScopeFolderID: scopeFolder.ID,
		ItemCount: len(items), CreatedAt: now, ExpiresAt: now.Add(ttl), Items: items,
	}
	selection.RequestHash, err = mailSelectionHash(selection)
	if err != nil {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot hash MailSelection", false, err)
	}
	selection.ID, err = commands.generateID("sel")
	if err != nil {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate MailSelection ID", false, err)
	}
	selection, err = state.Selections().Create(ctx, selection)
	if err != nil {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist MailSelection", false, err)
	}
	return accountValue, selection, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSelection(ctx context.Context, home, accountSelector, selectionID string) (account.Account, maildomain.MailSelection, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.MailSelection{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), mapAccountError(err)
	}
	selection, err := state.Selections().ByID(ctx, selectionID)
	if err != nil {
		if errors.Is(err, maildomain.ErrSelectionNotFound) {
			return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "MailSelection not found", false, err)
		}
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read MailSelection", false, err)
	}
	if selection.AccountID != accountValue.ID {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorConflict, "MailSelection belongs to another account", false)
	}
	return accountValue, selection, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSelectionForApply(ctx context.Context, home, accountSelector, selectionID string, validateSnapshot bool) (account.Account, maildomain.MailSelection, []maildomain.Message, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.MailSelection{}, nil, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.MailSelection{}, nil, state.SchemaVersion(), mapAccountError(err)
	}
	selection, err := state.Selections().ByID(ctx, selectionID)
	if err != nil {
		if errors.Is(err, maildomain.ErrSelectionNotFound) {
			return account.Account{}, maildomain.MailSelection{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "MailSelection not found", false, err)
		}
		return account.Account{}, maildomain.MailSelection{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read MailSelection", false, err)
	}
	if selection.AccountID != accountValue.ID {
		return account.Account{}, maildomain.MailSelection{}, nil, state.SchemaVersion(), cli.NewAppError(cli.ErrorConflict, "MailSelection belongs to another account", false)
	}
	if selection.Expired(commands.timeNow()) {
		return account.Account{}, maildomain.MailSelection{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "MailSelection expired; create and review a new selection", false, maildomain.ErrSelectionExpired)
	}
	messages := make([]maildomain.Message, 0, len(selection.Items))
	for _, item := range selection.Items {
		message, readErr := state.Mail().Message(ctx, accountValue.ID, item.MessageID)
		if readErr != nil {
			return account.Account{}, maildomain.MailSelection{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "MailSelection logical target is no longer available", false, readErr)
		}
		if validateSnapshot && item.LocationID != "" {
			matched := false
			for _, location := range message.Locations {
				if location.ID == item.LocationID && location.FolderID == item.FolderID && location.Present && location.Flags.Eligible() && location.UIDValidity == item.UIDValidity && location.UID == item.UID && location.ModSequence == item.ModSequence {
					matched = true
					break
				}
			}
			if !matched {
				appErr := cli.NewAppError(cli.ErrorConflict, "MailSelection provider location no longer matches the reviewed snapshot", false)
				appErr.Details = map[string]any{"selection_id": selection.ID, "message_id": item.MessageID, "location_id": item.LocationID, "next": "create and review a new MailSelection before retrying"}
				return account.Account{}, maildomain.MailSelection{}, nil, state.SchemaVersion(), appErr
			}
		}
		messages = append(messages, message)
	}
	return accountValue, selection, messages, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSelectionDelete(ctx context.Context, home, accountSelector, selectionID string) (account.Account, maildomain.MailSelection, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.MailSelection{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), mapAccountError(err)
	}
	selection, err := state.Selections().ByID(ctx, selectionID)
	if err != nil {
		if errors.Is(err, maildomain.ErrSelectionNotFound) {
			return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "MailSelection not found", false, err)
		}
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read MailSelection", false, err)
	}
	if selection.AccountID != accountValue.ID {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorConflict, "MailSelection belongs to another account", false)
	}
	deleted, err := state.Selections().Delete(ctx, accountValue.ID, selection.ID)
	if err != nil {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot delete MailSelection", false, err)
	}
	if !deleted {
		return account.Account{}, maildomain.MailSelection{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "MailSelection not found", false, maildomain.ErrSelectionNotFound)
	}
	return accountValue, selection, state.SchemaVersion(), nil
}

func resolveSelectionExplicitTargets(ctx context.Context, state *store.Store, accountID string, targets []string, limit int) ([]string, string, error) {
	if len(targets) < 1 || len(targets) > limit {
		return nil, "", cli.NewAppError(cli.ErrorSafety, "explicit MailSelection source exceeds the bounded target limit", false)
	}
	messageIDs := make([]string, 0, len(targets))
	seen := map[string]struct{}{}
	for _, raw := range targets {
		target := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(target, "msg_"):
			message, err := state.Mail().Message(ctx, accountID, target)
			if err != nil {
				if errors.Is(err, maildomain.ErrMessageNotFound) {
					return nil, "", cli.WrapAppError(cli.ErrorNotFound, "MailSelection message target not found", false, err)
				}
				return nil, "", cli.WrapAppError(cli.ErrorInternal, "cannot read MailSelection message target", false, err)
			}
			if _, ok := seen[message.ID]; !ok {
				seen[message.ID] = struct{}{}
				messageIDs = append(messageIDs, message.ID)
			}
		case strings.HasPrefix(target, "thr_"):
			page, err := state.Threads().Show(ctx, accountID, target, "", maximumMailSelectionItems)
			if err != nil {
				if errors.Is(err, maildomain.ErrThreadNotFound) {
					return nil, "", cli.WrapAppError(cli.ErrorNotFound, "MailSelection thread target not found", false, err)
				}
				return nil, "", cli.WrapAppError(cli.ErrorInternal, "cannot read MailSelection thread target", false, err)
			}
			if err := validateSelectionThread(page); err != nil {
				return nil, "", err
			}
			for _, message := range page.Thread.Messages {
				if _, ok := seen[message.ID]; ok {
					continue
				}
				seen[message.ID] = struct{}{}
				messageIDs = append(messageIDs, message.ID)
				if len(messageIDs) > limit {
					return nil, "", cli.NewAppError(cli.ErrorSafety, "expanded MailSelection membership exceeds the bounded review limit", false)
				}
			}
		default:
			return nil, "", cli.NewAppError(cli.ErrorUsage, "MailSelection explicit targets must use stable msg_… or thr_… IDs", false)
		}
	}
	hash, err := hashSelectionValue(struct {
		Kind    string   `json:"kind"`
		Targets []string `json:"targets"`
	}{Kind: "explicit", Targets: messageIDs})
	return messageIDs, hash, err
}

func resolveSelectionSearchPage(ctx context.Context, state *store.Store, accountID, folderSelector string, options maildomain.SearchOptions, limit int) ([]string, string, error) {
	if options.Limit == 0 {
		options.Limit = limit
	}
	if options.Limit < 1 || options.Limit > limit {
		return nil, "", cli.NewAppError(cli.ErrorUsage, "MailSelection search page limit must be between 1 and 100", false)
	}
	if options.ResultKind == "" {
		options.ResultKind = maildomain.SearchMessages
	}
	if options.ResultKind != maildomain.SearchMessages && options.ResultKind != maildomain.SearchThreads {
		return nil, "", cli.NewAppError(cli.ErrorUsage, "MailSelection search result kind must be messages or threads", false)
	}
	if strings.TrimSpace(folderSelector) != "" {
		folderValue, err := resolveMailFolder(ctx, state, accountID, folderSelector)
		if err != nil {
			return nil, "", err
		}
		options.FolderID = folderValue.ID
	}
	querySignature, err := state.Search().Signature(accountID, options)
	if err != nil {
		return nil, "", cli.WrapAppError(cli.ErrorInternal, "cannot hash MailSelection search source", false, err)
	}
	page, err := state.Search().Search(ctx, accountID, options)
	if err != nil {
		if errors.Is(err, maildomain.ErrInvalidSearchCursor) {
			return nil, "", cli.WrapAppError(cli.ErrorUsage, "MailSelection search cursor is invalid or belongs to another query", false, err)
		}
		return nil, "", cli.WrapAppError(cli.ErrorInternal, "cannot resolve MailSelection search page", false, err)
	}
	messageIDs := make([]string, 0, len(page.Hits))
	seen := map[string]struct{}{}
	for _, hit := range page.Hits {
		if hit.Message != nil {
			if _, ok := seen[hit.Message.ID]; !ok {
				seen[hit.Message.ID] = struct{}{}
				messageIDs = append(messageIDs, hit.Message.ID)
			}
			continue
		}
		if hit.Thread == nil {
			continue
		}
		threadPage, readErr := state.Threads().Show(ctx, accountID, hit.Thread.ID, "", maximumMailSelectionItems)
		if readErr != nil {
			return nil, "", cli.WrapAppError(cli.ErrorInternal, "cannot expand MailSelection search thread", false, readErr)
		}
		if err := validateSelectionThread(threadPage); err != nil {
			return nil, "", err
		}
		for _, message := range threadPage.Thread.Messages {
			if _, ok := seen[message.ID]; ok {
				continue
			}
			seen[message.ID] = struct{}{}
			messageIDs = append(messageIDs, message.ID)
			if len(messageIDs) > limit {
				return nil, "", cli.NewAppError(cli.ErrorSafety, "expanded search MailSelection exceeds the bounded review limit", false)
			}
		}
	}
	if len(messageIDs) == 0 {
		return nil, "", cli.NewAppError(cli.ErrorNotFound, "MailSelection search page matched no local messages", false)
	}
	sourceHash, err := hashSelectionValue(struct {
		QuerySignature string `json:"query_signature"`
		Cursor         string `json:"cursor"`
		Limit          int    `json:"limit"`
		Kind           string `json:"kind"`
	}{QuerySignature: querySignature, Cursor: strings.TrimSpace(options.Cursor), Limit: options.Limit, Kind: string(options.ResultKind)})
	return messageIDs, sourceHash, err
}

func validateSelectionThread(page maildomain.ThreadPage) error {
	if page.Thread.ProjectionState != "" && page.Thread.ProjectionState != "ready" {
		err := cli.NewAppError(cli.ErrorConflict, "MailSelection cannot snapshot an incomplete thread projection", false)
		err.Details = map[string]any{"thread_id": page.Thread.ID, "projection_state": page.Thread.ProjectionState, "projection_reason": page.Thread.ProjectionReason}
		return err
	}
	if page.More {
		return cli.NewAppError(cli.ErrorSafety, "MailSelection cannot silently truncate a thread larger than 100 messages", false)
	}
	return nil
}

func mailSelectionHash(selection maildomain.MailSelection) (string, error) {
	return hashSelectionValue(struct {
		AccountID       string                         `json:"account_id"`
		SourceKind      maildomain.SelectionSourceKind `json:"source_kind"`
		SourceQueryHash string                         `json:"source_query_hash"`
		ScopeFolderID   string                         `json:"scope_folder_id,omitempty"`
		Items           []maildomain.MailSelectionItem `json:"items"`
	}{
		AccountID: selection.AccountID, SourceKind: selection.SourceKind,
		SourceQueryHash: selection.SourceQueryHash, ScopeFolderID: selection.ScopeFolderID, Items: selection.Items,
	})
}

func hashSelectionValue(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func selectionApplyHash(selection maildomain.MailSelection, action, mode, destination string, planHashes []string) (string, error) {
	return hashSelectionValue(struct {
		SelectionID   string   `json:"selection_id"`
		SelectionHash string   `json:"selection_hash"`
		Action        string   `json:"action"`
		Mode          string   `json:"mode"`
		Destination   string   `json:"destination,omitempty"`
		PlanHashes    []string `json:"plan_hashes,omitempty"`
	}{
		SelectionID: selection.ID, SelectionHash: selection.RequestHash,
		Action: strings.TrimSpace(action), Mode: strings.TrimSpace(mode), Destination: strings.TrimSpace(destination), PlanHashes: planHashes,
	})
}

// MailSelectionApplyHash is used by the CLI after it has produced the same
// exact per-item plans that execution will use. It contains no message content.
func (commands accountCommands) MailSelectionApplyHash(_ context.Context, selection maildomain.MailSelection, action, mode, destination string, planHashes []string) (string, error) {
	return selectionApplyHash(selection, action, mode, destination, planHashes)
}
