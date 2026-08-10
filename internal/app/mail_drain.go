package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/secret"
)

func (commands accountCommands) MailSyncUntilCurrent(
	ctx context.Context,
	home, accountSelector, folderSelector string,
	limit int,
	allFolders bool,
	maxPages, maxMessages int,
) (account.Account, []folder.Folder, maildomain.SyncDrainSummary, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, nil, maildomain.SyncDrainSummary{}, 0, err
	}
	defer state.Close()
	defer unlock()

	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, nil, maildomain.SyncDrainSummary{}, state.SchemaVersion(), mapAccountError(err)
	}
	selector := strings.TrimSpace(folderSelector)
	if allFolders && selector != "" {
		return account.Account{}, nil, maildomain.SyncDrainSummary{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorUsage, "--all-folders cannot be combined with --folder", false)
	}

	selected := make([]folder.Folder, 0)
	if selector != "" {
		value, resolveErr := state.Folders().Resolve(ctx, accountValue.ID, selector)
		if resolveErr != nil {
			return account.Account{}, nil, maildomain.SyncDrainSummary{}, state.SchemaVersion(), mapFolderError(resolveErr)
		}
		if err := validateSyncFolder(value); err != nil {
			return account.Account{}, nil, maildomain.SyncDrainSummary{}, state.SchemaVersion(), err
		}
		selected = append(selected, value)
	} else {
		cursor := ""
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
				return accountValue, selected, maildomain.SyncDrainSummary{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot page schedulable mail folders", false, err)
			}
			selected = append(selected, values...)
			if !more {
				break
			}
			if next == "" || next == cursor {
				return accountValue, selected, maildomain.SyncDrainSummary{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorInternal, "folder scheduling pagination did not advance", false)
			}
			cursor = next
		}
	}
	if len(selected) == 0 {
		return accountValue, nil, maildomain.SyncDrainSummary{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorConfiguration, "no schedulable mail folders are available; refresh folder status or enable a folder", false)
	}

	_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
	if err != nil {
		return account.Account{}, nil, maildomain.SyncDrainSummary{}, state.SchemaVersion(), mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return account.Account{}, nil, maildomain.SyncDrainSummary{}, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)

	result := maildomain.SyncDrainSummary{Pages: make([]maildomain.SyncSummary, 0, min(maxPages, len(selected)*2))}
	active := append([]folder.Folder(nil), selected...)
	for len(active) > 0 {
		nextActive := make([]folder.Folder, 0, len(active))
		for _, folderValue := range active {
			if result.PageCount >= maxPages {
				result.MoreAvailable = true
				result.StopReason = maildomain.DrainStopMaxPages
				return accountValue, selected, result, state.SchemaVersion(), nil
			}
			remainingMessages := maxMessages - result.Fetched
			if remainingMessages <= 0 {
				result.MoreAvailable = true
				result.StopReason = maildomain.DrainStopMaxMessages
				return accountValue, selected, result, state.SchemaVersion(), nil
			}
			select {
			case <-ctx.Done():
				result.MoreAvailable = true
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					result.StopReason = maildomain.DrainStopDeadline
					return accountValue, selected, result, state.SchemaVersion(), cli.WrapAppError(cli.ErrorUnavailable, "sync drain reached the overall command deadline; committed pages are preserved and the next invocation resumes from checkpoints", true, ctx.Err())
				}
				result.StopReason = maildomain.DrainStopError
				return accountValue, selected, result, state.SchemaVersion(), ctx.Err()
			default:
			}
			pageLimit := min(limit, remainingMessages)
			summary, usedUsername, syncErr := commands.syncMailbox(ctx, state, paths, accountValue, password, folderValue, pageLimit)
			if summary.FolderID != "" {
				result.Pages = append(result.Pages, summary)
				result.PageCount++
				result.Fetched += summary.Fetched
			}
			if usedUsername != "" && usedUsername != accountValue.IMAPUsername {
				accountValue, err = state.Accounts().UpdateIMAPUsername(ctx, accountValue.ID, usedUsername, commands.timeNow())
				if err != nil {
					return accountValue, selected, result, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "folder synchronization committed but the working IMAP username could not be persisted", false, err)
				}
			}
			if syncErr != nil {
				result.MoreAvailable = true
				if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(syncErr, context.DeadlineExceeded) {
					result.StopReason = maildomain.DrainStopDeadline
				} else {
					result.StopReason = maildomain.DrainStopError
				}
				return accountValue, selected, result, state.SchemaVersion(), syncErr
			}
			if summary.MoreAvailable {
				nextActive = append(nextActive, folderValue)
			}
		}
		active = nextActive
	}
	result.StopReason = maildomain.DrainStopComplete
	return accountValue, selected, result, state.SchemaVersion(), nil
}

func (commands accountCommands) MailBackfillUntilComplete(
	ctx context.Context,
	home, accountSelector, folderSelector string,
	after, before *time.Time,
	limit, maxPages, maxMessages int,
) (account.Account, folder.Folder, maildomain.BackfillDrainSummary, int64, error) {
	result := maildomain.BackfillDrainSummary{Pages: make([]maildomain.BackfillSummary, 0, maxPages)}
	var accountValue account.Account
	var folderValue folder.Folder
	var schemaVersion int64
	for {
		if result.PageCount >= maxPages {
			result.StopReason = maildomain.DrainStopMaxPages
			return accountValue, folderValue, result, schemaVersion, nil
		}
		remainingMessages := maxMessages - result.Fetched
		if remainingMessages <= 0 {
			result.StopReason = maildomain.DrainStopMaxMessages
			return accountValue, folderValue, result, schemaVersion, nil
		}
		pageLimit := min(limit, remainingMessages)
		pageAccount, pageFolder, page, pageSchemaVersion, err := commands.MailBackfill(ctx, home, accountSelector, folderSelector, after, before, pageLimit)
		if pageAccount.ID != "" {
			accountValue = pageAccount
		}
		if pageFolder.ID != "" {
			folderValue = pageFolder
		}
		if pageSchemaVersion != 0 {
			schemaVersion = pageSchemaVersion
		}
		if page.Frontier.ID != "" || page.Complete {
			result.Pages = append(result.Pages, page)
			result.PageCount++
			result.Fetched += page.Fetched
		}
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
				result.StopReason = maildomain.DrainStopDeadline
			} else {
				result.StopReason = maildomain.DrainStopError
			}
			return accountValue, folderValue, result, schemaVersion, err
		}
		if page.Complete {
			result.Complete = true
			result.StopReason = maildomain.DrainStopComplete
			return accountValue, folderValue, result, schemaVersion, nil
		}
	}
}
