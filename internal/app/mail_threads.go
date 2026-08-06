package app

import (
	"context"
	"errors"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
)

func (commands accountCommands) MailLatest(ctx context.Context, home, accountSelector string, limit int) (account.Account, []maildomain.Thread, bool, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, nil, false, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, nil, false, state.SchemaVersion(), mapAccountError(err)
	}
	threads, more, err := state.Threads().Latest(ctx, accountValue.ID, limit)
	if err != nil {
		return account.Account{}, nil, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read reconstructed mail threads", false, err)
	}
	return accountValue, threads, more, state.SchemaVersion(), nil
}

func (commands accountCommands) MailThread(ctx context.Context, home, accountSelector, selector string, limit int) (account.Account, maildomain.ThreadPage, []maildomain.ContentSource, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.ThreadPage{}, nil, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.ThreadPage{}, nil, state.SchemaVersion(), mapAccountError(err)
	}
	page, err := state.Threads().Show(ctx, accountValue.ID, strings.TrimSpace(selector), limit)
	if err != nil {
		if errors.Is(err, maildomain.ErrThreadNotFound) {
			return account.Account{}, maildomain.ThreadPage{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "mail thread not found", false, err)
		}
		return account.Account{}, maildomain.ThreadPage{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read the reconstructed mail thread", false, err)
	}
	sources := make([]maildomain.ContentSource, len(page.Thread.Messages))
	for index := range page.Thread.Messages {
		message, source, loadErr := commands.threadMessageSource(ctx, state, paths, accountValue.ID, page.Thread.Messages[index])
		if loadErr != nil {
			return account.Account{}, maildomain.ThreadPage{}, nil, state.SchemaVersion(), loadErr
		}
		page.Thread.Messages[index] = message
		sources[index] = source
	}
	return accountValue, page, sources, state.SchemaVersion(), nil
}

func (commands accountCommands) threadMessageSource(ctx context.Context, state *store.Store, paths config.AppPaths, accountID string, message maildomain.Message) (maildomain.Message, maildomain.ContentSource, error) {
	location, ok := rawMessageLocation(message)
	if !ok {
		return maildomain.Message{}, nil, cli.NewAppError(cli.ErrorUnavailable, "raw message content is not available locally", false)
	}
	blobStore := blob.New(paths.BlobRoot)
	if message.Content == nil || !message.Content.AttachmentsIndexed {
		reader, openErr := blobStore.OpenVerifiedContext(ctx, location.RawStorageKey, location.RawContentHash, location.RawByteSize)
		if openErr != nil {
			return maildomain.Message{}, nil, mapBlobReadError(openErr)
		}
		attachmentSet := newStagedAttachmentSet(commands.attachmentBlobStore(paths))
		defer attachmentSet.Abort()
		normalized, parseErr := maildomain.ParseNormalizedWithAttachmentsContext(ctx, reader, attachmentSet.Sink)
		closeErr := reader.Close()
		if parseErr == nil {
			parseErr = closeErr
		}
		if parseErr != nil {
			return maildomain.Message{}, nil, cli.WrapAppError(cli.ErrorUnavailable, "cannot decode a local thread message", false, parseErr)
		}
		if idErr := assignContentIDs(commands, message.ID, &normalized); idErr != nil {
			return maildomain.Message{}, nil, cli.WrapAppError(cli.ErrorInternal, "cannot allocate thread message-content IDs", false, idErr)
		}
		if err := attachmentSet.Commit(); err != nil {
			return maildomain.Message{}, nil, cli.WrapAppError(cli.ErrorInternal, "cannot commit a decoded thread attachment", true, err)
		}
		if err := state.Mail().SaveMessageContent(ctx, accountID, message.ID, location.ID, normalized, commands.timeNow()); err != nil {
			return maildomain.Message{}, nil, cli.WrapAppError(cli.ErrorInternal, "cannot persist local thread message content metadata", true, err)
		}
		var reloadErr error
		message, reloadErr = state.Mail().Message(ctx, accountID, message.ID)
		if reloadErr != nil {
			return maildomain.Message{}, nil, cli.WrapAppError(cli.ErrorInternal, "cannot reload local thread message content metadata", true, reloadErr)
		}
		location, ok = rawMessageLocation(message)
		if !ok {
			return maildomain.Message{}, nil, cli.NewAppError(cli.ErrorUnavailable, "raw thread message content is not available locally", false)
		}
	}
	source := &messageContentSource{store: blobStore, location: location, content: *message.Content}
	return message, source, nil
}
