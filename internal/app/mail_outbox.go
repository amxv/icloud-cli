package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/amxv/icloud-cli/internal/transport"
)

func (commands accountCommands) MailOutboxListPage(ctx context.Context, home, accountSelector, cursor string, limit int) (account.Account, []maildomain.OutboxItem, bool, string, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, nil, false, "", 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, nil, false, "", state.SchemaVersion(), mapAccountError(err)
	}
	values, more, next, err := state.Outbox().ListPage(ctx, accountValue.ID, limit, cursor)
	if err != nil {
		if errors.Is(err, maildomain.ErrOutboxInvalidCursor) || errors.Is(err, maildomain.ErrOutboxInvalid) {
			return account.Account{}, nil, false, "", state.SchemaVersion(), cli.WrapAppError(cli.ErrorUsage, "outbox cursor or page request is invalid", false, err)
		}
		return account.Account{}, nil, false, "", state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read Outbox items", false, err)
	}
	return accountValue, values, more, next, state.SchemaVersion(), nil
}

func (commands accountCommands) MailOutboxItem(ctx context.Context, home, accountSelector, outboxID string) (account.Account, maildomain.OutboxItem, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.OutboxItem{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.OutboxItem{}, state.SchemaVersion(), mapAccountError(err)
	}
	item, err := state.Outbox().Item(ctx, accountValue.ID, outboxID)
	if err != nil {
		return account.Account{}, maildomain.OutboxItem{}, state.SchemaVersion(), mapOutboxError(err)
	}
	return accountValue, item, state.SchemaVersion(), nil
}

func (commands accountCommands) MailOutboxCancel(ctx context.Context, home, accountSelector, outboxID string) (account.Account, maildomain.OutboxItem, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.OutboxItem{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.OutboxItem{}, state.SchemaVersion(), mapAccountError(err)
	}
	if err := state.Outbox().RepairInterrupted(ctx, accountValue.ID, commands.timeNow()); err != nil {
		return account.Account{}, maildomain.OutboxItem{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot repair interrupted Outbox state", false, err)
	}
	item, err := state.Outbox().Cancel(ctx, accountValue.ID, outboxID, commands.timeNow())
	if err != nil {
		return account.Account{}, maildomain.OutboxItem{}, state.SchemaVersion(), mapOutboxError(err)
	}
	return accountValue, item, state.SchemaVersion(), nil
}

func (commands accountCommands) MailOutboxRetry(ctx context.Context, home, accountSelector, outboxID string) (account.Account, maildomain.OutboxItem, maildomain.OutboundAttempt, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, 0, err
	}
	defer state.Close()
	defer unlock()

	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, state.SchemaVersion(), mapAccountError(err)
	}
	if err := state.Outbox().RepairInterrupted(ctx, accountValue.ID, commands.timeNow()); err != nil {
		return accountValue, maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot repair interrupted Outbox state", false, err)
	}
	item, err := state.Outbox().Item(ctx, accountValue.ID, outboxID)
	if err != nil {
		return accountValue, maildomain.OutboxItem{}, maildomain.OutboundAttempt{}, state.SchemaVersion(), mapOutboxError(err)
	}
	if !item.Retryable() {
		return accountValue, item, maildomain.OutboundAttempt{}, state.SchemaVersion(), mapOutboxError(maildomain.ErrOutboxNotRetryable)
	}
	if err := validateOutboxPrivateEnvelope(item); err != nil {
		return accountValue, item, maildomain.OutboundAttempt{}, state.SchemaVersion(), mapOutboxError(err)
	}

	sender, err := state.Identities().Resolve(ctx, accountValue.ID, item.SenderIdentityID)
	if err != nil {
		return accountValue, item, maildomain.OutboundAttempt{}, state.SchemaVersion(), mapMailIdentityError(err)
	}
	if !sender.Enabled {
		return accountValue, item, maildomain.OutboundAttempt{}, state.SchemaVersion(), mapMailIdentityError(account.ErrIdentityDisabled)
	}
	if err := requireMailIdentityOutboundReady(sender); err != nil {
		return accountValue, item, maildomain.OutboundAttempt{}, state.SchemaVersion(), err
	}

	_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
	if err != nil {
		return accountValue, item, maildomain.OutboundAttempt{}, state.SchemaVersion(), mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return accountValue, item, maildomain.OutboundAttempt{}, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)

	content, attachmentRecords, err := commands.prepareOutboxReplayContent(ctx, paths, item)
	if err != nil {
		return accountValue, item, maildomain.OutboundAttempt{}, state.SchemaVersion(), err
	}
	messageID, err := commands.generateID("msg")
	if err != nil {
		return accountValue, item, maildomain.OutboundAttempt{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate an Outbox replay message ID", false, err)
	}
	if err := assignContentIDs(commands, messageID, &content); err != nil {
		return accountValue, item, maildomain.OutboundAttempt{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate Outbox replay content IDs", false, err)
	}
	attemptID, err := commands.generateID("send")
	if err != nil {
		return accountValue, item, maildomain.OutboundAttempt{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate an Outbox replay attempt ID", false, err)
	}
	parentAttemptID := item.LatestAttemptID
	if parentAttemptID == "" {
		parentAttemptID = item.SourceAttemptID
	}
	item, err = state.Outbox().PrepareRetry(ctx, accountValue.ID, item.ID, attemptID, commands.timeNow())
	if err != nil {
		return accountValue, item, maildomain.OutboundAttempt{}, state.SchemaVersion(), mapOutboxError(err)
	}
	attempt, _, err := state.Outbound().Begin(ctx, maildomain.BeginOutboundAttempt{
		AttemptID: attemptID, AccountID: accountValue.ID, SenderIdentityID: item.SenderIdentityID,
		ProposedMessageID: messageID, RequestHash: item.RequestHash,
		IdempotencyKey: "outbox:" + item.ID + ":" + attemptID,
		RFCMessageID:   item.RFCMessageID, RawContentHash: item.RawContentHash,
		RawStorageKey: item.RawStorageKey, RawByteSize: item.RawByteSize,
		ToCount: item.ToCount, CCCount: item.CCCount, BCCCount: item.BCCCount,
		BodyPartCount: item.BodyPartCount, Attachments: attachmentRecords,
		Kind: item.Kind, ParentMessageID: item.ParentMessageID, ParentLocationID: item.ParentLocationID,
		SourceMessageID: item.SourceMessageID, RetryOfAttemptID: parentAttemptID, Now: commands.timeNow(),
	})
	if err != nil {
		outcomeCtx, outcomeCancel := durableOutcomeContext(ctx)
		_ = state.Outbox().ReleasePreparedRetry(outcomeCtx, accountValue.ID, item.ID, attemptID, commands.timeNow())
		outcomeCancel()
		return accountValue, item, maildomain.OutboundAttempt{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot create the linked Outbox replay attempt", false, err)
	}

	rawReader, err := blob.New(paths.BlobRoot).OpenVerifiedContext(ctx, item.RawStorageKey, item.RawContentHash, item.RawByteSize)
	if err != nil {
		outcomeCtx, outcomeCancel := durableOutcomeContext(ctx)
		item, attempt, markErr := state.Outbox().MarkRetryOutcome(outcomeCtx, accountValue.ID, item.ID, attempt.ID, maildomain.OutboundFailed, true, "raw_unavailable", "raw_unavailable", commands.timeNow())
		outcomeCancel()
		if markErr != nil {
			return accountValue, item, attempt, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "Outbox replay raw source is unavailable and its safe failure could not be recorded", false, errors.Join(err, markErr))
		}
		return accountValue, item, attempt, state.SchemaVersion(), withOutboxDetails(withAttemptDetails(mapBlobReadError(err), attempt), item)
	}
	sendResult, sendErr := commands.smtpSender().SendSMTP(ctx, transport.AuthRequest{Username: accountValue.Email, Password: password}, transport.SMTPSendRequest{
		Envelope: transport.SMTPEnvelope{From: item.Envelope.From.Email, Recipients: item.Envelope.EnvelopeRecipients()},
		Message:  rawReader,
	})
	_ = rawReader.Close()
	identityEvidenceErr := recordSenderProviderEvidence(ctx, state, sender, sendResult, sendErr, commands.timeNow())
	if sendErr == nil && !sendResult.Accepted {
		sendErr = &transport.SMTPSendError{Outcome: transport.SMTPSendUnknown, OutboxEligible: false, Failure: transport.NewFailure(transport.StageSMTPAccept, transport.FailureUnavailable, false, errors.New("SMTP transport returned without proving acceptance"))}
	}
	if sendErr != nil {
		status, code, outboxEligible := maildomain.OutboundFailed, "smtp", false
		var typed *transport.SMTPSendError
		if errors.As(sendErr, &typed) {
			outboxEligible = typed.Outcome == transport.SMTPSendFailed && typed.OutboxEligible
			if typed.Outcome == transport.SMTPSendUnknown {
				status = maildomain.OutboundUnknown
			}
			if typed.Failure != nil && typed.Failure.Stage != "" {
				code = string(typed.Failure.Stage)
			}
		}
		mapped := mapSendTransportError(sendErr)
		if isPermanentSenderRejection(sendErr) {
			mapped = cli.WrapAppError(cli.ErrorConfiguration, "selected mail identity was rejected by the provider; no fallback sender was used", false, sendErr)
		}
		if identityEvidenceErr != nil {
			mapped = errors.Join(mapped, cli.WrapAppError(cli.ErrorInternal, "mail identity provider-validation state could not be persisted", false, identityEvidenceErr))
		}
		outcomeCtx, cancel := durableOutcomeContext(ctx)
		item, attempt, err = state.Outbox().MarkRetryOutcome(outcomeCtx, accountValue.ID, item.ID, attempt.ID, status, outboxEligible, code, code, commands.timeNow())
		cancel()
		if err != nil {
			return accountValue, item, attempt, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "Outbox replay outcome could not be recorded; do not retry until inspecting the linked attempt", false, errors.Join(mapped, err))
		}
		return accountValue, item, attempt, state.SchemaVersion(), withOutboxDetails(withAttemptDetails(mapped, attempt), item)
	}

	replayRequest := maildomain.SendRequest{
		Kind: item.Kind, ParentMessageID: item.ParentMessageID, ParentLocationID: item.ParentLocationID,
		SourceMessageID: item.SourceMessageID, SenderIdentityID: item.SenderIdentityID,
		From: item.Envelope.From, To: append([]maildomain.Address(nil), item.Envelope.To...),
		CC: append([]maildomain.Address(nil), item.Envelope.CC...), BCC: append([]maildomain.Address(nil), item.Envelope.BCC...),
		ReplyTo: item.Envelope.ReplyTo,
	}
	outcomeCtx, cancel := durableOutcomeContext(ctx)
	attempt, completionErr := state.Outbound().CompleteSent(outcomeCtx, maildomain.CompleteOutboundSend{AttemptID: attempt.ID, Request: replayRequest, Content: content, AcceptedAt: commands.timeNow()})
	cancel()
	if completionErr != nil {
		outcomeCtx, cancel = durableOutcomeContext(ctx)
		attempt, err = state.Outbound().MarkAcceptedIncomplete(outcomeCtx, attempt.ID, "local_completion", commands.timeNow())
		cancel()
		if err != nil {
			return accountValue, item, attempt, state.SchemaVersion(), withOutboxDetails(withAttemptDetails(cli.WrapAppError(cli.ErrorInternal, "SMTP accepted the Outbox replay but its accepted outcome could not be recorded; do not retry automatically", false, errors.Join(completionErr, err)), attempt), item)
		}
		outcomeCtx, cancel = durableOutcomeContext(ctx)
		item, err = state.Outbox().MarkRetrySent(outcomeCtx, accountValue.ID, item.ID, attempt.ID, commands.timeNow())
		cancel()
		if err != nil {
			return accountValue, item, attempt, state.SchemaVersion(), withAttemptDetails(cli.WrapAppError(cli.ErrorInternal, "SMTP accepted the Outbox replay but the Outbox lifecycle could not be completed; do not resend", false, err), attempt)
		}
		return accountValue, item, attempt, state.SchemaVersion(), withOutboxDetails(withAttemptDetails(cli.WrapAppError(cli.ErrorInternal, "SMTP accepted the Outbox replay but local message completion is incomplete; do not resend", false, completionErr), attempt), item)
	}
	outcomeCtx, cancel = durableOutcomeContext(ctx)
	item, err = state.Outbox().MarkRetrySent(outcomeCtx, accountValue.ID, item.ID, attempt.ID, commands.timeNow())
	cancel()
	if err != nil {
		return accountValue, item, attempt, state.SchemaVersion(), withAttemptDetails(cli.WrapAppError(cli.ErrorInternal, "SMTP accepted the Outbox replay but the Outbox lifecycle could not be completed; do not resend", false, err), attempt)
	}
	if identityEvidenceErr != nil {
		return accountValue, item, attempt, state.SchemaVersion(), withOutboxDetails(withAttemptDetails(cli.WrapAppError(cli.ErrorInternal, "SMTP accepted the Outbox replay but mail identity provider-validation state could not be persisted; do not resend", false, identityEvidenceErr), attempt), item)
	}

	if ctx.Err() != nil {
		_, _ = state.Outbound().MarkReconciliationIssue(context.WithoutCancel(ctx), attempt.ID, "sent_copy_deferred", commands.timeNow())
		return accountValue, item, attempt, state.SchemaVersion(), nil
	}
	sentResolution, folderErr := state.FolderRoles().Resolve(ctx, accountValue.ID, folder.RoleSent)
	if folderErr == nil && sentResolution.Folder.Present && sentResolution.Folder.Selectable {
		rawReader, openErr := blob.New(paths.BlobRoot).OpenVerifiedContext(ctx, item.RawStorageKey, item.RawContentHash, item.RawByteSize)
		if openErr == nil {
			_, appendErr := commands.sentAppender().AppendSent(ctx, transport.AuthRequest{Username: accountValue.IMAPUsername, Password: password}, transport.SentAppendRequest{
				Mailbox: sentResolution.Folder.Name, Message: rawReader, Size: item.RawByteSize, InternalDate: commands.timeNow(),
			})
			closeErr := rawReader.Close()
			if appendErr == nil {
				appendErr = closeErr
			}
			if appendErr != nil {
				_, _ = state.Outbound().MarkReconciliationIssue(context.WithoutCancel(ctx), attempt.ID, "sent_copy_append", commands.timeNow())
			}
		} else {
			_, _ = state.Outbound().MarkReconciliationIssue(context.WithoutCancel(ctx), attempt.ID, "sent_copy_raw_unavailable", commands.timeNow())
		}
	} else if folderErr != nil && !errors.Is(folderErr, folder.ErrNotFound) {
		_, _ = state.Outbound().MarkReconciliationIssue(context.WithoutCancel(ctx), attempt.ID, "sent_folder_lookup", commands.timeNow())
	}
	return accountValue, item, attempt, state.SchemaVersion(), nil
}

func (commands accountCommands) prepareOutboxReplayContent(ctx context.Context, paths config.AppPaths, item maildomain.OutboxItem) (maildomain.NormalizedContent, []maildomain.OutboundAttachmentRecord, error) {
	reader, err := blob.New(paths.BlobRoot).OpenVerifiedContext(ctx, item.RawStorageKey, item.RawContentHash, item.RawByteSize)
	if err != nil {
		return maildomain.NormalizedContent{}, nil, mapBlobReadError(err)
	}
	attachments := newStagedAttachmentSet(commands.attachmentBlobStore(paths))
	defer attachments.Abort()
	content, parseErr := maildomain.ParseNormalizedWithAttachmentsContext(ctx, reader, attachments.Sink)
	closeErr := reader.Close()
	if parseErr == nil {
		parseErr = closeErr
	}
	if parseErr != nil {
		return maildomain.NormalizedContent{}, nil, cli.WrapAppError(cli.ErrorInternal, "cannot verify the exact Outbox replay message", false, parseErr)
	}
	if err := attachments.Commit(); err != nil {
		return maildomain.NormalizedContent{}, nil, cli.WrapAppError(cli.ErrorInternal, "cannot verify Outbox replay attachment content", false, err)
	}
	records := outboundAttemptAttachmentRecords(content.Attachments)
	if len(records) != item.AttachmentCount {
		return maildomain.NormalizedContent{}, nil, cli.NewAppError(cli.ErrorInternal, "Outbox replay attachment count no longer matches the reviewed message", false)
	}
	return content, records, nil
}

func mapOutboxError(err error) error {
	switch {
	case errors.Is(err, maildomain.ErrOutboxNotFound):
		return cli.WrapAppError(cli.ErrorNotFound, "Outbox item not found", false, err)
	case errors.Is(err, maildomain.ErrOutboxNotRetryable):
		return cli.WrapAppError(cli.ErrorConflict, "Outbox item is not retryable", false, err)
	case errors.Is(err, maildomain.ErrOutboxInvalid), errors.Is(err, maildomain.ErrOutboxInvalidCursor):
		return cli.WrapAppError(cli.ErrorUsage, "Outbox request is invalid", false, err)
	default:
		return cli.WrapAppError(cli.ErrorInternal, "cannot update Outbox state", false, err)
	}
}

func validateOutboxPrivateEnvelope(item maildomain.OutboxItem) error {
	if err := item.Envelope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(item.RawStorageKey) == "" || len(strings.TrimSpace(item.RawContentHash)) != 64 || item.RawByteSize < 0 {
		return fmt.Errorf("%w: private raw replay coordinates are incomplete", maildomain.ErrOutboxInvalid)
	}
	return nil
}
