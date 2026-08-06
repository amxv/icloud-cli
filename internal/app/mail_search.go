package app

import (
	"context"
	"errors"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func (commands accountCommands) MailSearch(
	ctx context.Context,
	home, accountSelector, folderSelector string,
	options maildomain.SearchOptions,
) (account.Account, folder.Folder, maildomain.SearchPage, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.SearchPage{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.SearchPage{}, state.SchemaVersion(), mapAccountError(err)
	}
	var folderValue folder.Folder
	if strings.TrimSpace(folderSelector) != "" {
		folderValue, err = resolveMailFolder(ctx, state, accountValue.ID, folderSelector)
		if err != nil {
			return account.Account{}, folder.Folder{}, maildomain.SearchPage{}, state.SchemaVersion(), err
		}
		options.FolderID = folderValue.ID
	}
	page, err := state.Search().Search(ctx, accountValue.ID, options)
	if err != nil {
		if errors.Is(err, maildomain.ErrInvalidSearchCursor) {
			return account.Account{}, folder.Folder{}, maildomain.SearchPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorUsage, "mail search cursor is invalid or belongs to another query", false, err)
		}
		return account.Account{}, folder.Folder{}, maildomain.SearchPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot search synchronized mail", false, err)
	}
	return accountValue, folderValue, page, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSearchRepair(
	ctx context.Context,
	home, accountSelector string,
	limit int,
) (account.Account, maildomain.SearchRepairSummary, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.SearchRepairSummary{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.SearchRepairSummary{}, state.SchemaVersion(), mapAccountError(err)
	}
	items, err := state.Search().Pending(ctx, accountValue.ID, limit)
	if err != nil {
		return account.Account{}, maildomain.SearchRepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot inspect the local search index", false, err)
	}
	summary := maildomain.SearchRepairSummary{AccountID: accountValue.ID, Selected: len(items)}
	blobStore := blob.New(paths.BlobRoot)
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return account.Account{}, maildomain.SearchRepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorUnavailable, "search repair was canceled", false, err)
		}
		message, readErr := state.Mail().Message(ctx, accountValue.ID, item.MessageID)
		if readErr != nil {
			summary.Failed++
			_ = state.Search().RecordBodyError(ctx, accountValue.ID, item.MessageID, "message_unavailable", commands.timeNow())
			continue
		}
		location, ok := rawMessageLocation(message)
		if !ok {
			summary.Failed++
			_ = state.Search().RecordBodyError(ctx, accountValue.ID, item.MessageID, "raw_unavailable", commands.timeNow())
			continue
		}
		reader, openErr := blobStore.OpenVerifiedContext(ctx, location.RawStorageKey, location.RawContentHash, location.RawByteSize)
		if openErr != nil {
			summary.Failed++
			_ = state.Search().RecordBodyError(ctx, accountValue.ID, item.MessageID, "raw_unavailable", commands.timeNow())
			continue
		}
		normalized, parseErr := maildomain.ParseNormalizedContext(ctx, reader)
		closeErr := reader.Close()
		if parseErr == nil {
			parseErr = closeErr
		}
		if parseErr != nil {
			summary.Failed++
			_ = state.Search().RecordBodyError(ctx, accountValue.ID, item.MessageID, "message_parse", commands.timeNow())
			continue
		}
		if err := state.Search().SaveBody(ctx, accountValue.ID, item.MessageID, normalized.SearchText, normalized.SearchTextTruncated, commands.timeNow()); err != nil {
			summary.Failed++
			_ = state.Search().RecordBodyError(ctx, accountValue.ID, item.MessageID, "index_write", commands.timeNow())
			continue
		}
		summary.Repaired++
	}
	pending, failed, err := state.Search().Counts(ctx, accountValue.ID)
	if err != nil {
		return account.Account{}, maildomain.SearchRepairSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot verify the local search index", false, err)
	}
	summary.Remaining = pending
	summary.Errors = failed
	return accountValue, summary, state.SchemaVersion(), nil
}
