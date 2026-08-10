package app

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

const mailboxExportPageSize = 100

func (commands accountCommands) MailMailboxExport(ctx context.Context, home, accountSelector, folderSelector string, writer io.Writer) (account.Account, folder.Folder, maildomain.MailboxExportSummary, int64, error) {
	if writer == nil {
		return account.Account{}, folder.Folder{}, maildomain.MailboxExportSummary{}, 0, cli.NewAppError(cli.ErrorUsage, "mailbox export writer is required", false)
	}
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailboxExportSummary{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailboxExportSummary{}, state.SchemaVersion(), mapAccountError(err)
	}
	folderValue, err := state.Folders().Resolve(ctx, accountValue.ID, strings.TrimSpace(folderSelector))
	if err != nil {
		return accountValue, folder.Folder{}, maildomain.MailboxExportSummary{}, state.SchemaVersion(), mapFolderError(err)
	}

	summary := maildomain.MailboxExportSummary{Format: "mboxrd", Complete: false}
	blobStore := commands.inboundBlobStore(paths)
	var cursor *maildomain.MailboxExportCursor
	for {
		page, pageErr := state.Mail().MailboxExportPage(ctx, accountValue.ID, folderValue.ID, mailboxExportPageSize, cursor)
		if pageErr != nil {
			return accountValue, folderValue, summary, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot page synchronized mailbox export records", false, pageErr)
		}
		for _, record := range page.Records {
			if strings.TrimSpace(record.RawStorageKey) == "" || strings.TrimSpace(record.RawContentHash) == "" {
				return accountValue, folderValue, summary, state.SchemaVersion(), mailboxExportRecordError(record, cli.NewAppError(cli.ErrorUnavailable, "synchronized provider copy has no local raw source", false))
			}
			reader, openErr := blobStore.OpenVerifiedContext(ctx, record.RawStorageKey, record.RawContentHash, record.RawByteSize)
			if openErr != nil {
				return accountValue, folderValue, summary, state.SchemaVersion(), mailboxExportRecordError(record, mapBlobReadError(openErr))
			}
			written, writeErr := maildomain.WriteMboxRDMessage(ctx, writer, record.EnvelopeSender, record.EnvelopeDate, reader)
			closeErr := reader.Close()
			if writeErr == nil {
				writeErr = closeErr
			}
			if writeErr != nil {
				if errors.Is(writeErr, context.Canceled) || errors.Is(writeErr, context.DeadlineExceeded) {
					return accountValue, folderValue, summary, state.SchemaVersion(), mailboxExportRecordError(record, cli.WrapAppError(cli.ErrorUnavailable, "mailbox export was canceled", false, writeErr))
				}
				return accountValue, folderValue, summary, state.SchemaVersion(), mailboxExportRecordError(record, cli.WrapAppError(cli.ErrorUnavailable, "cannot stream the mboxrd archive", false, writeErr))
			}
			summary.Count++
			summary.Bytes += written
			current := maildomain.MailboxExportCursor{UIDValidity: record.UIDValidity, UID: record.UID, LocationID: record.LocationID}
			summary.LastCursor = &current
		}
		if !page.More {
			break
		}
		if page.NextCursor == nil {
			return accountValue, folderValue, summary, state.SchemaVersion(), cli.NewAppError(cli.ErrorInternal, "mailbox export paging ended without a continuation cursor", false)
		}
		cursor = page.NextCursor
	}
	summary.Complete = true
	return accountValue, folderValue, summary, state.SchemaVersion(), nil
}

func mailboxExportRecordError(record maildomain.MailboxExportRecord, err error) error {
	appErr := cli.NormalizeError(err)
	if appErr.Details == nil {
		appErr.Details = map[string]any{}
	}
	appErr.Details["message_id"] = record.MessageID
	appErr.Details["location_id"] = record.LocationID
	appErr.Details["uid_validity"] = record.UIDValidity
	appErr.Details["uid"] = record.UID
	return appErr
}
