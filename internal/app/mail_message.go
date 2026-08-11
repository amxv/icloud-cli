package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func (commands accountCommands) MailMessage(ctx context.Context, home, accountSelector, messageID string) (account.Account, maildomain.Message, maildomain.ContentSource, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.Message{}, nil, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), mapAccountError(err)
	}
	message, err := state.Mail().Message(ctx, accountValue.ID, strings.TrimSpace(messageID))
	if err != nil {
		if errors.Is(err, maildomain.ErrMessageNotFound) {
			return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "message not found", false, err)
		}
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read the local message", false, err)
	}
	rawSource, ok := rawMessageSource(message)
	if !ok {
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), cli.NewAppError(cli.ErrorUnavailable, "raw message content is not available locally", false)
	}
	blobStore := commands.inboundBlobStore(paths)
	if message.Content == nil || !message.Content.AttachmentsIndexed || message.Content.SMIME == nil {
		reader, openErr := blobStore.OpenVerifiedContext(ctx, rawSource.StorageKey, rawSource.ContentHash, rawSource.ByteSize)
		if openErr != nil {
			return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), mapBlobReadError(openErr)
		}
		attachmentSet := newStagedAttachmentSet(commands.attachmentBlobStore(paths))
		defer attachmentSet.Abort()
		smimePolicy, policyErr := commands.inboundSMIMEPolicy(ctx, state, accountValue.ID)
		if policyErr != nil {
			_ = reader.Close()
			return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read local S/MIME trust state", false, policyErr)
		}
		normalized, parseErr := maildomain.ParseNormalizedWithAttachmentsAndSMIMEContext(ctx, reader, attachmentSet.Sink, smimePolicy)
		closeErr := reader.Close()
		if parseErr == nil {
			parseErr = closeErr
		}
		if parseErr != nil {
			return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorUnavailable, "cannot decode the local message", false, parseErr)
		}
		if idErr := assignContentIDs(commands, message.ID, &normalized); idErr != nil {
			return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate message-content IDs", false, idErr)
		}
		if err := attachmentSet.Commit(); err != nil {
			return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot commit a decoded message attachment", true, err)
		}
		if err := state.Mail().SaveMessageContent(ctx, accountValue.ID, message.ID, rawSource.LocationID, normalized, commands.timeNow()); err != nil {
			return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist local message content metadata", true, err)
		}
		message, err = state.Mail().Message(ctx, accountValue.ID, message.ID)
		if err != nil {
			return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reload local message content metadata", true, err)
		}
	}
	source := &messageContentSource{store: blobStore, raw: rawSource, content: *message.Content}
	capability := maildomain.ParseUnsubscribeCapability(message.Content.Headers)
	if capability.Available || capability.OneClickDeclared {
		message.Unsubscribe = &capability
	}
	return accountValue, message, source, state.SchemaVersion(), nil
}

type messageContentSource struct {
	store   *blob.Store
	raw     messageRawSource
	content maildomain.NormalizedContent
}

func (source *messageContentSource) WriteRaw(ctx context.Context, writer io.Writer) error {
	if source == nil || writer == nil {
		return errors.New("raw message writer is required")
	}
	reader, err := source.store.OpenVerifiedContext(ctx, source.raw.StorageKey, source.raw.ContentHash, source.raw.ByteSize)
	if err != nil {
		return mapContentSourceError(err)
	}
	defer reader.Close()
	_, err = io.Copy(writer, &appContextReader{ctx: ctx, reader: reader})
	return err
}

func (source *messageContentSource) WriteBody(ctx context.Context, format maildomain.ContentFormat, writer io.Writer) error {
	if source == nil || writer == nil {
		return errors.New("message body writer is required")
	}
	paths := source.content.TextPaths
	if format == maildomain.ContentHTML {
		paths = source.content.HTMLPaths
	} else if len(paths) == 0 {
		paths = source.content.HTMLPaths
	}
	if len(paths) == 0 {
		return errors.New("requested message body is not available")
	}
	reader, err := source.store.OpenVerifiedContext(ctx, source.raw.StorageKey, source.raw.ContentHash, source.raw.ByteSize)
	if err != nil {
		return mapContentSourceError(err)
	}
	defer reader.Close()
	return maildomain.WriteSelectedBody(ctx, reader, format, paths, writer)
}

type messageRawSource struct {
	LocationID  string
	ContentHash string
	StorageKey  string
	ByteSize    int64
}

func rawMessageSource(message maildomain.Message) (messageRawSource, bool) {
	if sourceLocationID := strings.TrimSpace(message.ContentLocationID); sourceLocationID != "" {
		for _, location := range message.Locations {
			if location.ID == sourceLocationID && location.Present && location.RawStorageKey != "" && location.RawContentHash != "" {
				return messageRawSource{LocationID: location.ID, ContentHash: location.RawContentHash, StorageKey: location.RawStorageKey, ByteSize: location.RawByteSize}, true
			}
		}
		for _, location := range message.Locations {
			if location.ID == sourceLocationID && location.RawStorageKey != "" && location.RawContentHash != "" {
				return messageRawSource{LocationID: location.ID, ContentHash: location.RawContentHash, StorageKey: location.RawStorageKey, ByteSize: location.RawByteSize}, true
			}
		}
	}
	for _, location := range message.Locations {
		if location.Present && location.RawStorageKey != "" && location.RawContentHash != "" {
			return messageRawSource{LocationID: location.ID, ContentHash: location.RawContentHash, StorageKey: location.RawStorageKey, ByteSize: location.RawByteSize}, true
		}
	}
	for _, location := range message.Locations {
		if location.RawStorageKey != "" && location.RawContentHash != "" {
			return messageRawSource{LocationID: location.ID, ContentHash: location.RawContentHash, StorageKey: location.RawStorageKey, ByteSize: location.RawByteSize}, true
		}
	}
	if message.LocalRaw != nil && message.LocalRaw.StorageKey != "" && message.LocalRaw.ContentHash != "" {
		return messageRawSource{ContentHash: message.LocalRaw.ContentHash, StorageKey: message.LocalRaw.StorageKey, ByteSize: message.LocalRaw.ByteSize}, true
	}
	return messageRawSource{}, false
}

func mapBlobReadError(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return cli.WrapAppError(cli.ErrorUnavailable, "local message content verification timed out", true, err)
	case errors.Is(err, context.Canceled):
		return cli.WrapAppError(cli.ErrorUnavailable, "local message content read was canceled", false, err)
	case errors.Is(err, blob.ErrCorrupt):
		return cli.WrapAppError(cli.ErrorUnavailable, "local raw message content is corrupt", false, err)
	default:
		return cli.WrapAppError(cli.ErrorUnavailable, "local raw message content is unavailable", false, err)
	}
}

func mapContentSourceError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, blob.ErrCorrupt) {
		return fmt.Errorf("%w: %v", maildomain.ErrContentCorrupt, err)
	}
	if errors.Is(err, blob.ErrTooLarge) {
		return fmt.Errorf("%w: %v", maildomain.ErrContentLimit, err)
	}
	return fmt.Errorf("%w: %v", maildomain.ErrContentUnavailable, err)
}

type appContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *appContextReader) Read(buffer []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.reader.Read(buffer)
	}
}

var _ maildomain.ContentSource = (*messageContentSource)(nil)
