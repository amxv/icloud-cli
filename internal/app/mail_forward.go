package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
)

func (commands accountCommands) MailForwardPreview(
	ctx context.Context,
	home, accountSelector string,
	request maildomain.ForwardRequest,
) (account.Account, maildomain.ForwardPreview, int64, error) {
	accountValue, plan, schemaVersion, err := commands.resolveForwardPlan(ctx, home, accountSelector, request)
	if err != nil {
		return account.Account{}, maildomain.ForwardPreview{}, schemaVersion, err
	}
	_, preview, schemaVersion, err := commands.MailSendPreview(ctx, home, accountValue.ID, plan.Send)
	if err != nil {
		return account.Account{}, maildomain.ForwardPreview{}, schemaVersion, err
	}
	return accountValue, forwardPreview(plan, preview), schemaVersion, nil
}

func (commands accountCommands) MailForward(
	ctx context.Context,
	home, accountSelector string,
	request maildomain.ForwardRequest,
) (account.Account, maildomain.OutboundAttempt, bool, int64, error) {
	accountValue, plan, schemaVersion, err := commands.resolveForwardPlan(ctx, home, accountSelector, request)
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, schemaVersion, err
	}
	return commands.MailSend(ctx, home, accountValue.ID, plan.Send)
}

func (commands accountCommands) resolveForwardPlan(
	ctx context.Context,
	home, accountSelector string,
	request maildomain.ForwardRequest,
) (account.Account, maildomain.ForwardPlan, int64, error) {
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		return account.Account{}, maildomain.ForwardPlan{}, 0, cli.WrapAppError(cli.ErrorConfiguration, "cannot resolve local forward content", false, err)
	}
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.ForwardPlan{}, 0, err
	}
	defer state.Close()

	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.ForwardPlan{}, state.SchemaVersion(), mapAccountError(err)
	}
	sourceMessage, err := state.Mail().Message(ctx, accountValue.ID, strings.TrimSpace(request.SourceMessageID))
	if err != nil {
		if errors.Is(err, maildomain.ErrMessageNotFound) {
			return account.Account{}, maildomain.ForwardPlan{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "forward source message not found", false, err)
		}
		return account.Account{}, maildomain.ForwardPlan{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read the forward source message", false, err)
	}

	threadPage, err := state.Threads().Show(ctx, accountValue.ID, sourceMessage.ID, "", 1)
	if err != nil {
		return account.Account{}, maildomain.ForwardPlan{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot resolve the forward source thread", false, err)
	}
	mode, err := maildomain.NormalizeForwardOriginalMode(request.OriginalMode)
	if err != nil {
		return account.Account{}, maildomain.ForwardPlan{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorUsage, "forward original representation is invalid", false, err)
	}
	var originalText string
	if mode == maildomain.ForwardOriginalQuoted {
		var contentSource maildomain.ContentSource
		sourceMessage, contentSource, err = commands.threadMessageSource(ctx, state, paths, accountValue.ID, sourceMessage, maildomain.ThreadReadOptions{
			RequireSource: true, HydrateContent: true,
		})
		if err != nil {
			return account.Account{}, maildomain.ForwardPlan{}, state.SchemaVersion(), mapForwardContentError(err)
		}
		originalText, err = readForwardText(ctx, contentSource)
		if err != nil {
			return account.Account{}, maildomain.ForwardPlan{}, state.SchemaVersion(), mapForwardContentError(err)
		}
	}

	originalAttachments, originalMeta, err := commands.forwardOriginalAttachments(
		ctx, state.Mail(), paths, accountValue.ID, sourceMessage.ID, request.OriginalAttachmentIDs,
	)
	if err != nil {
		return account.Account{}, maildomain.ForwardPlan{}, state.SchemaVersion(), err
	}
	var originalEML *maildomain.OutboundAttachment
	if mode == maildomain.ForwardOriginalEML {
		value, err := forwardRawAttachment(ctx, paths, sourceMessage)
		if err != nil {
			return account.Account{}, maildomain.ForwardPlan{}, state.SchemaVersion(), err
		}
		originalEML = &value
	}
	request.OriginalMode = mode
	plan, err := maildomain.BuildForwardPlan(
		sourceMessage, threadPage.Thread.ID, request, originalText,
		originalAttachments, originalMeta, originalEML,
	)
	if err != nil {
		if errors.Is(err, maildomain.ErrInvalidForwardRequest) {
			return account.Account{}, maildomain.ForwardPlan{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorSafety, "cannot construct a safe forward from the selected message", false, err)
		}
		return account.Account{}, maildomain.ForwardPlan{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot construct the forward", false, err)
	}
	return accountValue, plan, state.SchemaVersion(), nil
}

func (commands accountCommands) forwardOriginalAttachments(
	ctx context.Context,
	stateMail *store.MailRepository,
	paths config.AppPaths,
	accountID, sourceMessageID string,
	ids []string,
) ([]maildomain.OutboundAttachment, []maildomain.ForwardOriginalAttachment, error) {
	seen := make(map[string]struct{}, len(ids))
	attachments := make([]maildomain.OutboundAttachment, 0, len(ids))
	metadata := make([]maildomain.ForwardOriginalAttachment, 0, len(ids))
	blobStore := commands.attachmentBlobStore(paths)
	if len(ids) > 0 && blobStore == nil {
		return nil, nil, cli.NewAppError(cli.ErrorUnavailable, "decoded forward attachment storage is unavailable", false)
	}
	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if !strings.HasPrefix(id, "att_") {
			return nil, nil, cli.NewAppError(cli.ErrorUsage, "--include-attachment requires stable att_… IDs", false)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, nil, cli.NewAppError(cli.ErrorUsage, "--include-attachment repeats the same attachment ID", false)
		}
		seen[id] = struct{}{}
		value, err := stateMail.Attachment(ctx, accountID, id)
		if err != nil {
			if errors.Is(err, maildomain.ErrAttachmentNotFound) {
				return nil, nil, cli.WrapAppError(cli.ErrorNotFound, "forward source attachment not found", false, err)
			}
			return nil, nil, cli.WrapAppError(cli.ErrorInternal, "cannot read a forward source attachment", false, err)
		}
		if value.MessageID != sourceMessageID {
			return nil, nil, cli.NewAppError(cli.ErrorConflict, "selected attachment does not belong to the forward source message", false)
		}
		if value.Availability != maildomain.AttachmentAvailable || value.StorageKey == "" || value.ContentHash == "" {
			return nil, nil, cli.NewAppError(cli.ErrorUnavailable, "selected forward attachment content is unavailable locally", false)
		}
		attachment := value
		filename := forwardAttachmentFilename(attachment)
		mediaType := strings.TrimSpace(attachment.MediaType)
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		attachments = append(attachments, maildomain.OutboundAttachment{
			Filename: filename, MediaType: mediaType,
			Disposition: outboundDispositionFromAttachment(attachment), ContentID: attachment.ContentID,
			ContentHash: attachment.ContentHash, ByteSize: attachment.ByteSize,
			Open: func() (io.ReadCloser, error) {
				return blobStore.OpenVerifiedContext(ctx, attachment.StorageKey, attachment.ContentHash, attachment.ByteSize)
			},
		})
		metadata = append(metadata, maildomain.ForwardOriginalAttachment{
			ID: attachment.ID, Filename: filename, MediaType: mediaType,
			Disposition: outboundDispositionFromAttachment(attachment), ContentID: attachment.ContentID,
			ByteSize: attachment.ByteSize,
		})
	}
	return attachments, metadata, nil
}

func forwardAttachmentFilename(value maildomain.Attachment) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			return '_'
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, strings.ToValidUTF8(strings.TrimSpace(value.Filename), ""))
	name = strings.Trim(name, ". ")
	if name == "" {
		name = fmt.Sprintf("attachment-%d.bin", value.Ordinal+1)
	}
	for len([]byte(name)) > 240 && len([]rune(name)) > 1 {
		runes := []rune(name)
		name = string(runes[:len(runes)-1])
	}
	return name
}

func forwardRawAttachment(ctx context.Context, paths config.AppPaths, message maildomain.Message) (maildomain.OutboundAttachment, error) {
	raw, ok := rawMessageSource(message)
	if !ok {
		return maildomain.OutboundAttachment{}, cli.NewAppError(cli.ErrorUnavailable, "exact raw source is unavailable for attached-EML forwarding", false)
	}
	store := blob.NewWithLimits(paths.BlobRoot, paths.InboundLimits.RawMessageBytes, paths.InboundLimits.AttachmentBytes)
	return maildomain.OutboundAttachment{
		Filename: "forwarded-message.eml", MediaType: "message/rfc822",
		ContentHash: raw.ContentHash, ByteSize: raw.ByteSize,
		Open: func() (io.ReadCloser, error) {
			return store.OpenVerifiedContext(ctx, raw.StorageKey, raw.ContentHash, raw.ByteSize)
		},
	}, nil
}

func readForwardText(ctx context.Context, source maildomain.ContentSource) (string, error) {
	if source == nil {
		return "", maildomain.ErrContentUnavailable
	}
	writer := &forwardBodyWriter{limit: maildomain.MaxSendBodyBytes}
	if err := source.WriteBody(ctx, maildomain.ContentText, writer); err != nil {
		return "", err
	}
	return writer.buffer.String(), nil
}

type forwardBodyWriter struct {
	buffer bytes.Buffer
	limit  int
}

func (writer *forwardBodyWriter) Write(value []byte) (int, error) {
	if writer.buffer.Len()+len(value) > writer.limit {
		remaining := writer.limit - writer.buffer.Len()
		if remaining > 0 {
			_, _ = writer.buffer.Write(value[:remaining])
		}
		return max(remaining, 0), blob.ErrTooLarge
	}
	return writer.buffer.Write(value)
}

func forwardPreview(plan maildomain.ForwardPlan, preview maildomain.SendPreview) maildomain.ForwardPreview {
	return maildomain.ForwardPreview{
		SourceMessageID: plan.SourceMessageID, SourceThreadID: plan.SourceThreadID,
		OriginalMode: plan.OriginalMode, OriginalAttachments: plan.OriginalAttachments,
		NewAttachmentCount: plan.NewAttachmentCount, AttachedOriginalEML: plan.AttachedOriginalEML,
		OriginalEMLByteSize: plan.OriginalEMLByteSize, Send: preview,
	}
}

func mapForwardSourceError(err error) error {
	var appErr *cli.AppError
	if errors.As(err, &appErr) && appErr.Code == cli.ErrorNotFound {
		return cli.WrapAppError(cli.ErrorNotFound, "forward source message not found", false, err)
	}
	return err
}

func mapForwardContentError(err error) error {
	switch {
	case errors.Is(err, blob.ErrTooLarge):
		return cli.WrapAppError(cli.ErrorSafety, "decoded forward source exceeds the 8 MiB body limit; use --original eml or none", false, err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return cli.WrapAppError(cli.ErrorUnavailable, "forward source decoding was canceled", true, err)
	case errors.Is(err, maildomain.ErrContentCorrupt):
		return cli.WrapAppError(cli.ErrorUnavailable, "forward source content is corrupt", false, err)
	default:
		return cli.WrapAppError(cli.ErrorUnavailable, "decoded forward source text is unavailable; use --original eml or none", false, err)
	}
}

var _ io.Writer = (*forwardBodyWriter)(nil)
