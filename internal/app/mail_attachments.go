package app

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func (commands accountCommands) MailAttachments(ctx context.Context, home, accountSelector, selector string, limit int) (account.Account, maildomain.AttachmentTarget, []maildomain.Attachment, bool, int64, error) {
	accountValue, target, values, more, _, schemaVersion, err := commands.MailAttachmentsPage(ctx, home, accountSelector, selector, "", limit)
	return accountValue, target, values, more, schemaVersion, err
}

func (commands accountCommands) MailAttachmentsPage(ctx context.Context, home, accountSelector, selector, cursor string, limit int) (account.Account, maildomain.AttachmentTarget, []maildomain.Attachment, bool, string, int64, error) {
	selector = strings.TrimSpace(selector)
	if strings.HasPrefix(selector, "msg_") {
		accountValue, message, _, schemaVersion, err := commands.MailMessage(ctx, home, accountSelector, selector)
		if err != nil {
			return account.Account{}, maildomain.AttachmentTarget{}, nil, false, "", schemaVersion, err
		}
		state, err := openState(ctx, home)
		if err != nil {
			return account.Account{}, maildomain.AttachmentTarget{}, nil, false, "", 0, err
		}
		defer state.Close()
		values, more, nextCursor, err := state.Mail().AttachmentsPage(ctx, accountValue.ID, message.ID, limit, cursor)
		if err != nil {
			if errors.Is(err, maildomain.ErrInvalidListCursor) {
				return account.Account{}, maildomain.AttachmentTarget{}, nil, false, "", state.SchemaVersion(), cli.WrapAppError(cli.ErrorUsage, "attachment list cursor is invalid or belongs to another target", false, err)
			}
			return account.Account{}, maildomain.AttachmentTarget{}, nil, false, "", state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read local attachments", false, err)
		}
		return accountValue, maildomain.AttachmentTarget{Kind: "message", ID: message.ID}, values, more, nextCursor, state.SchemaVersion(), nil
	}
	if !strings.HasPrefix(selector, "thr_") {
		return account.Account{}, maildomain.AttachmentTarget{}, nil, false, "", 0, cli.NewAppError(cli.ErrorUsage, "attachment target must be a stable msg_… message ID or thr_… thread ID", false)
	}
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.AttachmentTarget{}, nil, false, "", 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.AttachmentTarget{}, nil, false, "", state.SchemaVersion(), mapAccountError(err)
	}
	threadID, err := state.Threads().Resolve(ctx, accountValue.ID, selector)
	if err != nil {
		if errors.Is(err, maildomain.ErrThreadNotFound) {
			return account.Account{}, maildomain.AttachmentTarget{}, nil, false, "", state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "mail thread not found", false, err)
		}
		return account.Account{}, maildomain.AttachmentTarget{}, nil, false, "", state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot resolve mail thread", false, err)
	}
	values, more, nextCursor, err := state.Mail().AttachmentsForThreadPage(ctx, accountValue.ID, threadID, limit, cursor)
	if err != nil {
		if errors.Is(err, maildomain.ErrInvalidListCursor) {
			return account.Account{}, maildomain.AttachmentTarget{}, nil, false, "", state.SchemaVersion(), cli.WrapAppError(cli.ErrorUsage, "attachment list cursor is invalid or belongs to another target", false, err)
		}
		return account.Account{}, maildomain.AttachmentTarget{}, nil, false, "", state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read thread attachments", false, err)
	}
	return accountValue, maildomain.AttachmentTarget{Kind: "thread", ID: threadID}, values, more, nextCursor, state.SchemaVersion(), nil
}

func (commands accountCommands) MailAttachment(ctx context.Context, home, accountSelector, attachmentID string) (account.Account, maildomain.Attachment, maildomain.AttachmentSource, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.Attachment{}, nil, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.Attachment{}, nil, state.SchemaVersion(), mapAccountError(err)
	}
	attachment, err := state.Mail().Attachment(ctx, accountValue.ID, strings.TrimSpace(attachmentID))
	if err != nil {
		if errors.Is(err, maildomain.ErrAttachmentNotFound) {
			return account.Account{}, maildomain.Attachment{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "attachment not found", false, err)
		}
		return account.Account{}, maildomain.Attachment{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read the local attachment", false, err)
	}
	if attachment.Availability != maildomain.AttachmentAvailable || attachment.StorageKey == "" || attachment.ContentHash == "" {
		return account.Account{}, maildomain.Attachment{}, nil, state.SchemaVersion(), cli.NewAppError(cli.ErrorUnavailable, "decoded attachment content is not available locally", false)
	}
	source := &attachmentContentSource{store: commands.attachmentBlobStore(paths), attachment: attachment}
	return accountValue, attachment, source, state.SchemaVersion(), nil
}

type attachmentContentSource struct {
	store      attachmentBlobStore
	attachment maildomain.Attachment
}

func (source *attachmentContentSource) Write(ctx context.Context, writer io.Writer) error {
	if source == nil || source.store == nil || writer == nil {
		return errors.New("attachment writer is required")
	}
	reader, err := source.store.OpenVerifiedContext(ctx, source.attachment.StorageKey, source.attachment.ContentHash, source.attachment.ByteSize)
	if err != nil {
		if errors.Is(err, blob.ErrCorrupt) {
			return errors.Join(maildomain.ErrContentCorrupt, err)
		}
		if errors.Is(err, blob.ErrTooLarge) {
			return errors.Join(maildomain.ErrContentLimit, err)
		}
		return errors.Join(maildomain.ErrContentUnavailable, err)
	}
	defer reader.Close()
	_, err = io.Copy(writer, &appContextReader{ctx: ctx, reader: reader})
	return err
}

var _ maildomain.AttachmentSource = (*attachmentContentSource)(nil)
