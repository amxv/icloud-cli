package app

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/amxv/icloud-cli/internal/transport"
)

func (commands accountCommands) MailSendPreview(
	ctx context.Context,
	home, accountSelector string,
	request maildomain.SendRequest,
) (account.Account, maildomain.SendPreview, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.SendPreview{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.SendPreview{}, state.SchemaVersion(), mapAccountError(err)
	}
	normalized, hash, err := normalizeAccountSendRequest(accountValue, request)
	if err != nil {
		return account.Account{}, maildomain.SendPreview{}, state.SchemaVersion(), mapSendRequestError(err)
	}
	preview := maildomain.SendPreview{
		AccountID: accountValue.ID, From: normalized.From,
		To: normalized.To, CC: normalized.CC, BCC: normalized.BCC,
		ReplyTo: normalized.ReplyTo, Subject: normalized.Subject,
		TextBody: normalized.TextBody, HTMLBody: normalized.HTMLBody,
		Headers: normalized.Headers, BodyParts: normalized.BodyPartCount(),
		AttachmentCount: 0, RequestHash: hash,
	}
	return accountValue, preview, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSend(
	ctx context.Context,
	home, accountSelector string,
	request maildomain.SendRequest,
) (account.Account, maildomain.OutboundAttempt, bool, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), mapAccountError(err)
	}
	normalized, requestHash, err := normalizeAccountSendRequest(accountValue, request)
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), mapSendRequestError(err)
	}
	if existing, found, resolveErr := state.Outbound().ResolveIdempotency(ctx, accountValue.ID, normalized.IdempotencyKey, requestHash); resolveErr != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), mapIdempotencyError(resolveErr)
	} else if found {
		return accountValue, existing, true, state.SchemaVersion(), nil
	}
	attemptID, err := commands.generateID("send")
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate an outbound attempt ID", false, err)
	}
	messageID, err := commands.generateID("msg")
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate an outbound message ID", false, err)
	}
	rfcMessageID, err := maildomain.GenerateOutboundMessageID(accountValue.Email)
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate an RFC Message-ID", false, err)
	}
	composedAt := commands.timeNow()
	staged, err := stageOutboundMessage(ctx, commands.blobStager(paths), normalized, rfcMessageID, composedAt)
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), mapOutboundPreparationError(err)
	}
	defer staged.Abort()
	reader, err := staged.Open()
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reopen the composed message", false, err)
	}
	content, parseErr := maildomain.ParseNormalizedWithAttachmentsContext(ctx, reader, rejectOutboundAttachment)
	closeErr := reader.Close()
	if parseErr == nil {
		parseErr = closeErr
	}
	if parseErr != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot verify the composed outbound message", false, parseErr)
	}
	if err := assignContentIDs(commands, messageID, &content); err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate outbound content IDs", false, err)
	}
	if err := staged.Commit(); err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot commit the composed outbound message", true, err)
	}
	attempt, existing, err := state.Outbound().Begin(ctx, maildomain.BeginOutboundAttempt{
		AttemptID: attemptID, AccountID: accountValue.ID, ProposedMessageID: messageID,
		RequestHash: requestHash, IdempotencyKey: normalized.IdempotencyKey,
		RFCMessageID: rfcMessageID, RawContentHash: staged.ContentHash,
		RawStorageKey: staged.StorageKey, RawByteSize: staged.ByteSize,
		ToCount: len(normalized.To), CCCount: len(normalized.CC), BCCCount: len(normalized.BCC),
		BodyPartCount: normalized.BodyPartCount(), Now: composedAt,
	})
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), mapIdempotencyError(err)
	}
	if existing {
		return accountValue, attempt, true, state.SchemaVersion(), nil
	}
	_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
	if err != nil {
		mapped := mapAccountError(err)
		attempt, err = persistOutboundOutcome(ctx, state.Outbound(), attempt, maildomain.OutboundFailed, "credential", commands.timeNow(), mapped)
		if err != nil {
			return accountValue, attempt, false, state.SchemaVersion(), err
		}
		return accountValue, attempt, false, state.SchemaVersion(), withAttemptDetails(mapped, attempt)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		mapped := mapCredentialError(err)
		attempt, err = persistOutboundOutcome(ctx, state.Outbound(), attempt, maildomain.OutboundFailed, "credential", commands.timeNow(), mapped)
		if err != nil {
			return accountValue, attempt, false, state.SchemaVersion(), err
		}
		return accountValue, attempt, false, state.SchemaVersion(), withAttemptDetails(mapped, attempt)
	}
	defer zeroBytes(password)
	rawReader, err := blob.New(paths.BlobRoot).OpenVerifiedContext(ctx, staged.StorageKey, staged.ContentHash, staged.ByteSize)
	if err != nil {
		mapped := mapBlobReadError(err)
		attempt, err = persistOutboundOutcome(ctx, state.Outbound(), attempt, maildomain.OutboundFailed, "raw_unavailable", commands.timeNow(), mapped)
		if err != nil {
			return accountValue, attempt, false, state.SchemaVersion(), err
		}
		return accountValue, attempt, false, state.SchemaVersion(), withAttemptDetails(mapped, attempt)
	}
	sendResult, sendErr := commands.smtpSender().SendSMTP(ctx, transport.AuthRequest{
		Username: accountValue.Email, Password: password,
	}, transport.SMTPSendRequest{
		Envelope: transport.SMTPEnvelope{From: accountValue.Email, Recipients: normalized.EnvelopeRecipients()},
		Message:  rawReader,
	})
	_ = rawReader.Close()
	if sendErr == nil && !sendResult.Accepted {
		sendErr = &transport.SMTPSendError{
			Outcome: transport.SMTPSendUnknown,
			Failure: transport.NewFailure(
				transport.StageSMTPAccept,
				transport.FailureUnavailable,
				false,
				errors.New("SMTP transport returned without proving acceptance"),
			),
		}
	}
	if sendErr != nil {
		status, code := maildomain.OutboundFailed, "smtp"
		var sendFailure *transport.SMTPSendError
		if errors.As(sendErr, &sendFailure) {
			if sendFailure.Outcome == transport.SMTPSendUnknown {
				status = maildomain.OutboundUnknown
			}
			if sendFailure.Failure != nil && sendFailure.Failure.Stage != "" {
				code = string(sendFailure.Failure.Stage)
			}
		}
		mapped := mapSendTransportError(sendErr)
		attempt, err = persistOutboundOutcome(ctx, state.Outbound(), attempt, status, code, commands.timeNow(), mapped)
		if err != nil {
			return accountValue, attempt, false, state.SchemaVersion(), err
		}
		return accountValue, attempt, false, state.SchemaVersion(), withAttemptDetails(mapped, attempt)
	}
	completedAttempt, completionErr := state.Outbound().CompleteSent(ctx, maildomain.CompleteOutboundSend{
		AttemptID: attempt.ID, Request: normalized, Content: content, AcceptedAt: commands.timeNow(),
	})
	if completionErr != nil {
		acceptedAt := commands.timeNow()
		mapped := cli.WrapAppError(cli.ErrorInternal, "SMTP accepted the message but local completion is incomplete; do not retry automatically", false, completionErr)
		acceptedAttempt, outcomeErr := state.Outbound().MarkAcceptedIncomplete(ctx, attempt.ID, "local_completion", acceptedAt)
		if outcomeErr != nil {
			combined := cli.WrapAppError(
				cli.ErrorInternal,
				"SMTP accepted the message but its accepted outcome could not be recorded; do not retry automatically",
				false,
				errors.Join(completionErr, outcomeErr),
			)
			return accountValue, attempt, false, state.SchemaVersion(), withAttemptDetails(combined, attempt)
		}
		return accountValue, acceptedAttempt, false, state.SchemaVersion(), withAttemptDetails(mapped, acceptedAttempt)
	}
	attempt = completedAttempt
	return accountValue, attempt, false, state.SchemaVersion(), nil
}

func normalizeAccountSendRequest(accountValue account.Account, request maildomain.SendRequest) (maildomain.SendRequest, string, error) {
	request.From = maildomain.Address{DisplayName: accountValue.Name, Email: accountValue.Email}
	normalized, err := maildomain.NormalizeSendRequest(request)
	if err != nil {
		return maildomain.SendRequest{}, "", err
	}
	hash, err := maildomain.SendRequestHash(normalized)
	return normalized, hash, err
}

func stageOutboundMessage(ctx context.Context, stager blob.Stager, request maildomain.SendRequest, messageID string, sentAt time.Time) (*blob.Staged, error) {
	if stager == nil {
		return nil, errors.New("outbound blob stager is unavailable")
	}
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := maildomain.ComposeOutboundMessage(writer, request, messageID, sentAt)
		_ = writer.CloseWithError(err)
		done <- err
	}()
	staged, stageErr := stager.Stage(ctx, reader, -1)
	_ = reader.Close()
	composeErr := <-done
	if stageErr != nil {
		if staged != nil {
			_ = staged.Abort()
		}
		return nil, stageErr
	}
	if composeErr != nil {
		_ = staged.Abort()
		return nil, composeErr
	}
	return staged, nil
}

func rejectOutboundAttachment(context.Context, maildomain.MIMEPart, io.Reader) (maildomain.AttachmentBlob, error) {
	return maildomain.AttachmentBlob{}, errors.New("outbound attachments are not supported in this phase")
}

func (commands accountCommands) smtpSender() transport.SMTPSender {
	if commands.sender != nil {
		return commands.sender
	}
	if candidate, ok := commands.smtp.(transport.SMTPSender); ok {
		return candidate
	}
	return unavailableSMTPSender{}
}

type unavailableSMTPSender struct{}

func (unavailableSMTPSender) SendSMTP(context.Context, transport.AuthRequest, transport.SMTPSendRequest) (transport.SMTPSendResult, error) {
	return transport.SMTPSendResult{}, &transport.SMTPSendError{
		Outcome: transport.SMTPSendFailed,
		Failure: transport.NewFailure(transport.StageConnect, transport.FailureConfiguration, false, errors.New("SMTP send unavailable")),
	}
}

func mapSendRequestError(err error) error {
	return cli.WrapAppError(cli.ErrorUsage, "outbound mail request is invalid", false, err)
}

func mapIdempotencyError(err error) error {
	if errors.Is(err, maildomain.ErrIdempotencyConflict) {
		return cli.WrapAppError(cli.ErrorConflict, "idempotency key was already used for another normalized request", false, err)
	}
	return cli.WrapAppError(cli.ErrorInternal, "cannot coordinate outbound idempotency", false, err)
}

func mapOutboundPreparationError(err error) error {
	if errors.Is(err, blob.ErrTooLarge) {
		return cli.WrapAppError(cli.ErrorSafety, "composed message exceeds the supported raw-message bound", false, err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return cli.WrapAppError(cli.ErrorUnavailable, "outbound composition was canceled", true, err)
	}
	return cli.WrapAppError(cli.ErrorInternal, "cannot compose the outbound message", false, err)
}

func mapSendTransportError(err error) error {
	var sendErr *transport.SMTPSendError
	if !errors.As(err, &sendErr) || sendErr.Failure == nil {
		return cli.WrapAppError(cli.ErrorUnavailable, "mail send failed", false, err)
	}
	code := cli.ErrorUnavailable
	if sendErr.Failure.Kind == transport.FailureAuthentication {
		code = cli.ErrorAuthentication
	} else if sendErr.Failure.Kind == transport.FailureConfiguration {
		code = cli.ErrorConfiguration
	}
	message := "mail send failed"
	if sendErr.Outcome == transport.SMTPSendUnknown {
		message = "mail send outcome is unknown; do not retry automatically"
	}
	retryable := sendErr.Failure.Retryable
	if sendErr.Outcome == transport.SMTPSendUnknown {
		retryable = false
	}
	appErr := cli.WrapAppError(code, message, retryable, err)
	appErr.Details = map[string]any{"stage": sendErr.Failure.Stage, "outcome": sendErr.Outcome}
	return appErr
}

type outboundOutcomeWriter interface {
	MarkOutcome(context.Context, string, maildomain.OutboundStatus, string, time.Time) (maildomain.OutboundAttempt, error)
}

func persistOutboundOutcome(
	ctx context.Context,
	repository outboundOutcomeWriter,
	attempt maildomain.OutboundAttempt,
	status maildomain.OutboundStatus,
	errorCode string,
	now time.Time,
	primary error,
) (maildomain.OutboundAttempt, error) {
	updated, err := repository.MarkOutcome(ctx, attempt.ID, status, errorCode, now)
	if err == nil {
		return updated, nil
	}
	message := "mail send stopped but its attempt outcome could not be recorded; inspect the attempt before retrying"
	if status == maildomain.OutboundUnknown {
		message = "mail send outcome is unknown and could not be recorded; do not retry automatically"
	}
	appErr := cli.WrapAppError(cli.ErrorInternal, message, false, errors.Join(primary, err))
	return attempt, withAttemptDetails(appErr, attempt)
}

func withAttemptDetails(err error, attempt maildomain.OutboundAttempt) error {
	var appErr *cli.AppError
	if errors.As(err, &appErr) {
		if appErr.Details == nil {
			appErr.Details = map[string]any{}
		}
		appErr.Details["attempt_id"] = attempt.ID
		appErr.Details["status"] = attempt.Status
		return appErr
	}
	return err
}
