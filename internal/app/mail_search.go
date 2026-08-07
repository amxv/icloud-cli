package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
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
	summary, err := commands.repairSearchBodies(ctx, state, paths, accountValue, limit)
	if err != nil {
		return account.Account{}, maildomain.SearchRepairSummary{}, state.SchemaVersion(), err
	}
	return accountValue, summary, state.SchemaVersion(), nil
}

func (commands accountCommands) repairSearchBodies(ctx context.Context, state *store.Store, paths config.AppPaths, accountValue account.Account, limit int) (maildomain.SearchRepairSummary, error) {
	items, err := state.Search().Pending(ctx, accountValue.ID, limit)
	if err != nil {
		return maildomain.SearchRepairSummary{}, cli.WrapAppError(cli.ErrorInternal, "cannot inspect the local search index", false, err)
	}
	summary := maildomain.SearchRepairSummary{AccountID: accountValue.ID, Selected: len(items)}
	blobStore := commands.inboundBlobStore(paths)
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return maildomain.SearchRepairSummary{}, cli.WrapAppError(cli.ErrorUnavailable, "search repair was canceled", false, err)
		}
		message, readErr := state.Mail().Message(ctx, accountValue.ID, item.MessageID)
		if readErr != nil {
			summary.Failed++
			if err := recordSearchRepairBodyError(ctx, state, accountValue.ID, item.MessageID, "message_unavailable", commands.timeNow()); err != nil {
				return summary, err
			}
			continue
		}
		rawSource, ok := rawMessageSource(message)
		if !ok {
			summary.Failed++
			if err := recordSearchRepairBodyError(ctx, state, accountValue.ID, item.MessageID, "raw_unavailable", commands.timeNow()); err != nil {
				return summary, err
			}
			continue
		}
		reader, openErr := blobStore.OpenVerifiedContext(ctx, rawSource.StorageKey, rawSource.ContentHash, rawSource.ByteSize)
		if openErr != nil {
			summary.Failed++
			if err := recordSearchRepairBodyError(ctx, state, accountValue.ID, item.MessageID, "raw_unavailable", commands.timeNow()); err != nil {
				return summary, err
			}
			continue
		}
		normalized, parseErr := maildomain.ParseNormalizedContext(ctx, reader)
		closeErr := reader.Close()
		if parseErr == nil {
			parseErr = closeErr
		}
		if parseErr != nil {
			summary.Failed++
			if err := recordSearchRepairBodyError(ctx, state, accountValue.ID, item.MessageID, "message_parse", commands.timeNow()); err != nil {
				return summary, err
			}
			continue
		}
		if err := state.Search().SaveBody(ctx, accountValue.ID, item.MessageID, normalized.SearchText, normalized.SearchTextTruncated, commands.timeNow()); err != nil {
			summary.Failed++
			if recordErr := recordSearchRepairBodyError(ctx, state, accountValue.ID, item.MessageID, "index_write", commands.timeNow()); recordErr != nil {
				return summary, recordErr
			}
			continue
		}
		summary.Repaired++
	}
	pending, failed, err := state.Search().Counts(ctx, accountValue.ID)
	if err != nil {
		return maildomain.SearchRepairSummary{}, cli.WrapAppError(cli.ErrorInternal, "cannot verify the local search index", false, err)
	}
	summary.Remaining = pending
	summary.Errors = failed
	return summary, nil
}

func recordSearchRepairBodyError(ctx context.Context, state *store.Store, accountID, messageID, code string, now time.Time) error {
	if err := state.Search().RecordBodyError(ctx, accountID, messageID, code, now); err != nil {
		return cli.WrapAppError(cli.ErrorInternal, "cannot persist search repair failure state", false, err)
	}
	return nil
}
