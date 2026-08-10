package app

import (
	"context"
	"errors"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
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

func (commands accountCommands) MailThread(ctx context.Context, home, accountSelector, selector string, options maildomain.ThreadReadOptions) (account.Account, maildomain.ThreadPage, []maildomain.ContentSource, int64, error) {
	var (
		state  *store.Store
		paths  config.AppPaths
		unlock = func() {}
		err    error
	)
	if options.HydrateContent {
		state, paths, unlock, err = commands.openWriteState(ctx, home)
	} else {
		paths, err = config.ResolveAppPaths(home)
		if err == nil {
			state, err = openState(ctx, home)
		}
	}
	if err != nil {
		return account.Account{}, maildomain.ThreadPage{}, nil, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.ThreadPage{}, nil, state.SchemaVersion(), mapAccountError(err)
	}
	page, err := state.Threads().Show(ctx, accountValue.ID, strings.TrimSpace(selector), options.Cursor, options.Limit)
	if err != nil {
		if errors.Is(err, maildomain.ErrThreadNotFound) {
			return account.Account{}, maildomain.ThreadPage{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "mail thread not found", false, err)
		}
		if errors.Is(err, maildomain.ErrInvalidThreadCursor) {
			return account.Account{}, maildomain.ThreadPage{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorUsage, "mail thread cursor is invalid or belongs to another thread", false, err)
		}
		return account.Account{}, maildomain.ThreadPage{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read the reconstructed mail thread", false, err)
	}
	sources := make([]maildomain.ContentSource, len(page.Thread.Messages))
	for index := range page.Thread.Messages {
		message, source, loadErr := commands.threadMessageSource(ctx, state, paths, accountValue.ID, page.Thread.Messages[index], options)
		if loadErr != nil {
			return account.Account{}, maildomain.ThreadPage{}, nil, state.SchemaVersion(), loadErr
		}
		page.Thread.Messages[index] = message
		sources[index] = source
	}
	return accountValue, page, sources, state.SchemaVersion(), nil
}

func (commands accountCommands) MailThreadMutationTargets(ctx context.Context, home, accountSelector, selector, cursor string, limit int, sourceFolderSelector string, includeDeleted bool) (account.Account, maildomain.ThreadMutationTargetPage, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.ThreadMutationTargetPage{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.ThreadMutationTargetPage{}, state.SchemaVersion(), mapAccountError(err)
	}
	page, err := state.Threads().Show(ctx, accountValue.ID, strings.TrimSpace(selector), strings.TrimSpace(cursor), limit)
	if err != nil {
		if errors.Is(err, maildomain.ErrThreadNotFound) {
			return account.Account{}, maildomain.ThreadMutationTargetPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "mail thread not found", false, err)
		}
		if errors.Is(err, maildomain.ErrInvalidThreadCursor) {
			return account.Account{}, maildomain.ThreadMutationTargetPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorUsage, "mail thread cursor is invalid or belongs to another thread", false, err)
		}
		return account.Account{}, maildomain.ThreadMutationTargetPage{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot resolve thread mutation targets", false, err)
	}
	if page.Thread.ProjectionState != "" && page.Thread.ProjectionState != "ready" {
		err := cli.NewAppError(cli.ErrorConflict, "thread projection is not complete; repair it before a whole-thread mutation", false)
		err.Details = map[string]any{
			"thread_id":          page.Thread.ID,
			"projection_state":   page.Thread.ProjectionState,
			"projection_reason":  page.Thread.ProjectionReason,
			"projection_pending": page.Thread.ProjectionPending,
			"next":               "run `icloud mail sync repair --apply`, then review the thread again before mutating it",
		}
		return account.Account{}, maildomain.ThreadMutationTargetPage{}, state.SchemaVersion(), err
	}
	sourceFolder, err := state.Folders().Resolve(ctx, accountValue.ID, strings.TrimSpace(sourceFolderSelector))
	if err != nil {
		return account.Account{}, maildomain.ThreadMutationTargetPage{}, state.SchemaVersion(), mapFolderError(err)
	}
	if !sourceFolder.Present || sourceFolder.Stale || !sourceFolder.Selectable {
		return account.Account{}, maildomain.ThreadMutationTargetPage{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorConflict, "selected thread mutation folder is not current and selectable", false)
	}
	messageIDs := make([]string, 0, len(page.Thread.Messages))
	for _, message := range page.Thread.Messages {
		for _, location := range message.Locations {
			if location.FolderID == sourceFolder.ID && location.Present && (includeDeleted || location.Flags.Eligible()) {
				messageIDs = append(messageIDs, message.ID)
				break
			}
		}
	}
	return accountValue, maildomain.ThreadMutationTargetPage{
		ThreadID: page.Thread.ID, RedirectedFrom: page.Thread.RedirectedFrom,
		MessageIDs: messageIDs, MessageCount: page.Thread.MessageCount,
		More: page.More, NextCursor: page.NextCursor,
		ProjectionState: page.Thread.ProjectionState, ProjectionReason: page.Thread.ProjectionReason,
		ProjectionPending: page.Thread.ProjectionPending,
	}, state.SchemaVersion(), nil
}

func (commands accountCommands) threadMessageSource(ctx context.Context, state *store.Store, paths config.AppPaths, accountID string, message maildomain.Message, options maildomain.ThreadReadOptions) (maildomain.Message, maildomain.ContentSource, error) {
	rawSource, ok := rawMessageSource(message)
	if !ok {
		if !options.RequireSource && !options.HydrateContent {
			return message, nil, nil
		}
		return maildomain.Message{}, nil, cli.NewAppError(cli.ErrorUnavailable, "raw message content is not available locally", false)
	}
	blobStore := commands.inboundBlobStore(paths)
	if options.HydrateContent && (message.Content == nil || !message.Content.AttachmentsIndexed) {
		reader, openErr := blobStore.OpenVerifiedContext(ctx, rawSource.StorageKey, rawSource.ContentHash, rawSource.ByteSize)
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
		if err := state.Mail().SaveMessageContent(ctx, accountID, message.ID, rawSource.LocationID, normalized, commands.timeNow()); err != nil {
			return maildomain.Message{}, nil, cli.WrapAppError(cli.ErrorInternal, "cannot persist local thread message content metadata", true, err)
		}
		var reloadErr error
		message, reloadErr = state.Mail().Message(ctx, accountID, message.ID)
		if reloadErr != nil {
			return maildomain.Message{}, nil, cli.WrapAppError(cli.ErrorInternal, "cannot reload local thread message content metadata", true, reloadErr)
		}
		rawSource, ok = rawMessageSource(message)
		if !ok {
			return maildomain.Message{}, nil, cli.NewAppError(cli.ErrorUnavailable, "raw thread message content is not available locally", false)
		}
	}
	content := maildomain.NormalizedContent{}
	if message.Content != nil {
		content = *message.Content
	}
	source := &messageContentSource{store: blobStore, raw: rawSource, content: content}
	return message, source, nil
}
