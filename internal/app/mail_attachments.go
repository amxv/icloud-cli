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

func (commands accountCommands) MailAttachments(ctx context.Context, home, accountSelector, messageID string, limit int) (account.Account, maildomain.Message, []maildomain.Attachment, bool, int64, error) {
	accountValue, message, _, schemaVersion, err := commands.MailMessage(ctx, home, accountSelector, messageID)
	if err != nil {
		return account.Account{}, maildomain.Message{}, nil, false, schemaVersion, err
	}
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.Message{}, nil, false, 0, err
	}
	defer state.Close()
	values, more, err := state.Mail().Attachments(ctx, accountValue.ID, message.ID, limit)
	if err != nil {
		return account.Account{}, maildomain.Message{}, nil, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read local attachments", false, err)
	}
	return accountValue, message, values, more, state.SchemaVersion(), nil
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
		return errors.Join(maildomain.ErrContentUnavailable, err)
	}
	defer reader.Close()
	_, err = io.Copy(writer, &appContextReader{ctx: ctx, reader: reader})
	return err
}

var _ maildomain.AttachmentSource = (*attachmentContentSource)(nil)
