package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

const durableOutcomeTimeout = 10 * time.Second

// durableOutcomeContext is only for short local persistence after an outbound
// attempt has already been created. Provider I/O must continue to use the
// caller's context so --timeout remains a real network-operation boundary.
// Once an SMTP outcome exists, however, a canceled caller context must not
// strand the durable attempt in "pending" or lose a proven acceptance.
func durableOutcomeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), durableOutcomeTimeout)
}

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
	sender, err := resolveSelectedMailIdentity(ctx, state, accountValue.ID, request.SenderIdentityID)
	if err != nil {
		return account.Account{}, maildomain.SendPreview{}, state.SchemaVersion(), err
	}
	request.Attachments, err = inspectOutboundAttachments(ctx, request.Attachments)
	if err != nil {
		return account.Account{}, maildomain.SendPreview{}, state.SchemaVersion(), mapOutboundAttachmentError(err)
	}
	normalized, hash, err := normalizeAccountSendRequest(sender, request)
	if err != nil {
		return account.Account{}, maildomain.SendPreview{}, state.SchemaVersion(), mapSendRequestError(err)
	}
	preview := maildomain.SendPreview{
		Kind: normalized.Kind, ParentMessageID: normalized.ParentMessageID,
		ParentLocationID: normalized.ParentLocationID,
		SourceMessageID:  normalized.SourceMessageID,
		SenderIdentityID: normalized.SenderIdentityID,
		AccountID:        accountValue.ID, From: normalized.From,
		To: normalized.To, CC: normalized.CC, BCC: normalized.BCC,
		ReplyTo: normalized.ReplyTo, Subject: normalized.Subject,
		TextBody: normalized.TextBody, HTMLBody: normalized.HTMLBody,
		Headers: normalized.Headers, InReplyTo: normalized.InReplyTo,
		References: normalized.References, Attachments: normalized.Attachments,
		BodyParts: normalized.BodyPartCount(), AttachmentCount: len(normalized.Attachments),
		AttachmentBytes: normalized.AttachmentByteSize(), RequestHash: hash,
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
	sender, err := resolveSelectedMailIdentity(ctx, state, accountValue.ID, request.SenderIdentityID)
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), err
	}
	var stagedFiles *stagedOutboundFiles
	request.Attachments, stagedFiles, err = stageOutboundAttachments(ctx, commands.attachmentBlobStore(paths), request.Attachments)
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), mapOutboundAttachmentError(err)
	}
	defer stagedFiles.Abort()
	normalized, requestHash, err := normalizeAccountSendRequest(sender, request)
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), mapSendRequestError(err)
	}
	if err := validateOutboundRetry(ctx, state.Outbound(), accountValue.ID, normalized, requestHash); err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), err
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
	rfcMessageID, err := maildomain.GenerateOutboundMessageID(sender.Address)
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate an RFC Message-ID", false, err)
	}
	composedAt := commands.timeNow()
	staged, err := stageOutboundMessage(ctx, commands.outboundBlobStager(paths), normalized, rfcMessageID, composedAt)
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), mapOutboundPreparationError(err)
	}
	defer staged.Abort()
	reader, err := staged.Open()
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reopen the composed message", false, err)
	}
	attachmentSet := newStagedAttachmentSet(commands.attachmentBlobStore(paths))
	defer attachmentSet.Abort()
	content, parseErr := maildomain.ParseNormalizedWithAttachmentsContext(ctx, reader, attachmentSet.Sink)
	closeErr := reader.Close()
	if parseErr == nil {
		parseErr = closeErr
	}
	if parseErr != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot verify the composed outbound message", false, parseErr)
	}
	if err := verifyOutboundAttachmentContent(normalized.Attachments, content.Attachments); err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "composed outbound attachments failed verification", false, err)
	}
	if err := assignContentIDs(commands, messageID, &content); err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate outbound content IDs", false, err)
	}
	if err := staged.Commit(); err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot commit the composed outbound message", true, err)
	}
	if err := attachmentSet.Commit(); err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot commit outbound attachment content", true, err)
	}
	attempt, existing, err := state.Outbound().Begin(ctx, maildomain.BeginOutboundAttempt{
		AttemptID: attemptID, AccountID: accountValue.ID, SenderIdentityID: sender.ID, ProposedMessageID: messageID,
		RequestHash: requestHash, IdempotencyKey: normalized.IdempotencyKey,
		RFCMessageID: rfcMessageID, RawContentHash: staged.ContentHash,
		RawStorageKey: staged.StorageKey, RawByteSize: staged.ByteSize,
		ToCount: len(normalized.To), CCCount: len(normalized.CC), BCCCount: len(normalized.BCC),
		BodyPartCount: normalized.BodyPartCount(), Now: composedAt,
		Attachments:      outboundAttemptAttachmentRecords(content.Attachments),
		Kind:             normalized.Operation(),
		ParentMessageID:  normalized.ParentMessageID,
		ParentLocationID: normalized.ParentLocationID,
		SourceMessageID:  normalized.SourceMessageID,
		RetryOfAttemptID: normalized.RetryOfAttemptID,
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
		Envelope: transport.SMTPEnvelope{From: normalized.From.Email, Recipients: normalized.EnvelopeRecipients()},
		Message:  rawReader,
	})
	_ = rawReader.Close()
	identityEvidenceErr := recordSenderProviderEvidence(ctx, state, sender, sendResult, sendErr, commands.timeNow())
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
		if isPermanentSenderRejection(sendErr) {
			mapped = cli.WrapAppError(cli.ErrorConfiguration, "selected mail identity was rejected by the provider; no fallback sender was used", false, sendErr)
		}
		if identityEvidenceErr != nil {
			mapped = errors.Join(mapped, cli.WrapAppError(cli.ErrorInternal, "mail identity provider-validation state could not be persisted", false, identityEvidenceErr))
		}
		attempt, err = persistOutboundOutcome(ctx, state.Outbound(), attempt, status, code, commands.timeNow(), mapped)
		if err != nil {
			return accountValue, attempt, false, state.SchemaVersion(), err
		}
		return accountValue, attempt, false, state.SchemaVersion(), withAttemptDetails(mapped, attempt)
	}
	outcomeCtx, outcomeCancel := durableOutcomeContext(ctx)
	completedAttempt, completionErr := state.Outbound().CompleteSent(outcomeCtx, maildomain.CompleteOutboundSend{
		AttemptID: attempt.ID, Request: normalized, Content: content, AcceptedAt: commands.timeNow(),
	})
	outcomeCancel()
	if completionErr != nil {
		acceptedAt := commands.timeNow()
		mapped := cli.WrapAppError(cli.ErrorInternal, "SMTP accepted the message but local completion is incomplete; do not retry automatically", false, completionErr)
		outcomeCtx, outcomeCancel = durableOutcomeContext(ctx)
		acceptedAttempt, outcomeErr := state.Outbound().MarkAcceptedIncomplete(outcomeCtx, attempt.ID, "local_completion", acceptedAt)
		outcomeCancel()
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
	if identityEvidenceErr != nil {
		return accountValue, attempt, false, state.SchemaVersion(), withAttemptDetails(cli.WrapAppError(cli.ErrorInternal, "SMTP accepted the message but mail identity provider-validation state could not be persisted; do not resend", false, identityEvidenceErr), attempt)
	}
	if ctx.Err() != nil {
		return markAcceptedSentCopyIssue(ctx, state.Outbound(), accountValue, attempt, "sent_copy_deferred", commands.timeNow(), ctx.Err(), state.SchemaVersion())
	}
	sentResolution, folderErr := state.FolderRoles().Resolve(ctx, accountValue.ID, folder.RoleSent)
	sentFolder := sentResolution.Folder
	if folderErr == nil && sentFolder.Present && sentFolder.Selectable {
		rawReader, openErr := blob.New(paths.BlobRoot).OpenVerifiedContext(ctx, staged.StorageKey, staged.ContentHash, staged.ByteSize)
		if openErr != nil {
			return markAcceptedSentCopyIssue(ctx, state.Outbound(), accountValue, attempt, "sent_copy_raw_unavailable", commands.timeNow(), openErr, state.SchemaVersion())
		}
		_, appendErr := commands.sentAppender().AppendSent(ctx, transport.AuthRequest{
			Username: accountValue.IMAPUsername, Password: password,
		}, transport.SentAppendRequest{
			Mailbox: sentFolder.Name, Message: rawReader, Size: staged.ByteSize, InternalDate: commands.timeNow(),
		})
		closeErr := rawReader.Close()
		if appendErr == nil {
			appendErr = closeErr
		}
		if appendErr != nil {
			return markAcceptedSentCopyIssue(ctx, state.Outbound(), accountValue, attempt, "sent_copy_append", commands.timeNow(), appendErr, state.SchemaVersion())
		}
	} else if folderErr != nil && !errors.Is(folderErr, folder.ErrNotFound) {
		return markAcceptedSentCopyIssue(ctx, state.Outbound(), accountValue, attempt, "sent_folder_lookup", commands.timeNow(), mapFolderRoleError(folderErr), state.SchemaVersion())
	}
	return accountValue, attempt, false, state.SchemaVersion(), nil
}

func normalizeAccountSendRequest(sender account.MailIdentity, request maildomain.SendRequest) (maildomain.SendRequest, string, error) {
	request.SenderIdentityID = sender.ID
	request.From = maildomain.Address{DisplayName: sender.DisplayName, Email: sender.Address}
	normalized, err := maildomain.NormalizeSendRequest(request)
	if err != nil {
		return maildomain.SendRequest{}, "", err
	}
	hash, err := maildomain.SendRequestHash(normalized)
	return normalized, hash, err
}

func resolveSelectedMailIdentity(ctx context.Context, state *store.Store, accountID, selector string) (account.MailIdentity, error) {
	var (
		value account.MailIdentity
		err   error
	)
	if strings.TrimSpace(selector) == "" {
		value, err = state.Identities().Default(ctx, accountID)
	} else {
		value, err = state.Identities().Resolve(ctx, accountID, selector)
	}
	if err != nil {
		return account.MailIdentity{}, mapMailIdentityError(err)
	}
	if !value.Enabled {
		return account.MailIdentity{}, mapMailIdentityError(account.ErrIdentityDisabled)
	}
	return value, nil
}

func recordSenderProviderEvidence(ctx context.Context, state *store.Store, sender account.MailIdentity, result transport.SMTPSendResult, sendErr error, now time.Time) error {
	verification := account.MailIdentityVerification("")
	if sendErr == nil && result.Accepted {
		verification = account.MailIdentityVerified
	} else {
		var typed *transport.SMTPSendError
		if !errors.As(sendErr, &typed) || typed.Failure == nil {
			return nil
		}
		switch typed.Failure.Stage {
		case transport.StageSMTPRecipient, transport.StageSMTPData, transport.StageSMTPWrite, transport.StageSMTPAccept:
			verification = account.MailIdentityVerified
		case transport.StageSMTPMail:
			if !typed.Failure.Retryable && sender.Kind != account.MailIdentityPrimary {
				outcomeCtx, cancel := durableOutcomeContext(ctx)
				defer cancel()
				if _, err := state.Identities().SetEnabled(outcomeCtx, sender.AccountID, sender.ID, false, now); err != nil {
					return err
				}
				verification = account.MailIdentityRejected
			}
		}
	}
	if verification == "" {
		return nil
	}
	outcomeCtx, cancel := durableOutcomeContext(ctx)
	defer cancel()
	_, err := state.Identities().SetVerification(outcomeCtx, sender.AccountID, sender.ID, verification, now)
	return err
}

func isPermanentSenderRejection(err error) bool {
	var typed *transport.SMTPSendError
	return errors.As(err, &typed) && typed.Failure != nil && typed.Failure.Stage == transport.StageSMTPMail && !typed.Failure.Retryable
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

func (commands accountCommands) outboundBlobStager(paths config.AppPaths) blob.Stager {
	if commands.blobs != nil {
		return commands.blobs(paths)
	}
	return blob.NewWithLimit(paths.BlobRoot, maildomain.MaxSendMessageBytes)
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

func (commands accountCommands) sentAppender() transport.IMAPSentAppender {
	if commands.sent != nil {
		return commands.sent
	}
	if candidate, ok := commands.imap.(transport.IMAPSentAppender); ok {
		return candidate
	}
	return unavailableSentAppender{}
}

type unavailableSMTPSender struct{}

func (unavailableSMTPSender) SendSMTP(context.Context, transport.AuthRequest, transport.SMTPSendRequest) (transport.SMTPSendResult, error) {
	return transport.SMTPSendResult{}, &transport.SMTPSendError{
		Outcome: transport.SMTPSendFailed,
		Failure: transport.NewFailure(transport.StageConnect, transport.FailureConfiguration, false, errors.New("SMTP send unavailable")),
	}
}

type unavailableSentAppender struct{}

func (unavailableSentAppender) AppendSent(context.Context, transport.AuthRequest, transport.SentAppendRequest) (transport.SentAppendResult, error) {
	return transport.SentAppendResult{}, transport.NewFailure(transport.StageIMAPAppend, transport.FailureConfiguration, false, errors.New("Sent append transport unavailable"))
}

func markAcceptedSentCopyIssue(
	ctx context.Context,
	repository *store.OutboundRepository,
	accountValue account.Account,
	attempt maildomain.OutboundAttempt,
	code string,
	now time.Time,
	cause error,
	schemaVersion int64,
) (account.Account, maildomain.OutboundAttempt, bool, int64, error) {
	outcomeCtx, cancel := durableOutcomeContext(ctx)
	defer cancel()
	updated, updateErr := repository.MarkReconciliationIssue(outcomeCtx, attempt.ID, code, now)
	if updateErr != nil {
		combined := cli.WrapAppError(cli.ErrorInternal, "SMTP accepted the message but the Sent-copy outcome could not be recorded; do not resend", false, errors.Join(cause, updateErr))
		return accountValue, attempt, false, schemaVersion, withAttemptDetails(combined, attempt)
	}
	mapped := cli.WrapAppError(cli.ErrorUnavailable, "SMTP accepted the message but its Sent copy could not be confirmed; do not resend", false, cause)
	return accountValue, updated, false, schemaVersion, withAttemptDetails(mapped, updated)
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
		return cli.WrapAppError(cli.ErrorSafety, "composed message exceeds iCloud Mail's 20 MiB message limit", false, err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return cli.WrapAppError(cli.ErrorUnavailable, "outbound composition was canceled", true, err)
	}
	return cli.WrapAppError(cli.ErrorInternal, "cannot compose the outbound message", false, err)
}

func mapOutboundAttachmentError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return cli.WrapAppError(cli.ErrorUnavailable, "outbound attachment review was canceled", false, err)
	}
	return cli.WrapAppError(cli.ErrorSafety, "outbound attachment review failed", false, err)
}

func verifyOutboundAttachmentContent(expected []maildomain.OutboundAttachment, actual []maildomain.Attachment) error {
	if len(expected) != len(actual) {
		return fmt.Errorf("attachment count changed: expected %d, got %d", len(expected), len(actual))
	}
	ordered := composerAttachmentOrder(expected)
	for index := range ordered {
		if ordered[index].Filename != actual[index].Filename || ordered[index].MediaType != actual[index].MediaType ||
			string(ordered[index].Disposition) != actual[index].Disposition || ordered[index].ContentID != actual[index].ContentID ||
			actual[index].Inline != (ordered[index].Disposition == maildomain.AttachmentDispositionInline) ||
			!strings.EqualFold(ordered[index].ContentHash, actual[index].ContentHash) || ordered[index].ByteSize != actual[index].ByteSize ||
			actual[index].Availability != maildomain.AttachmentAvailable {
			return fmt.Errorf("attachment %d changed during MIME composition", index+1)
		}
	}
	return nil
}

func composerAttachmentOrder(values []maildomain.OutboundAttachment) []maildomain.OutboundAttachment {
	if len(values) == 0 {
		return nil
	}
	hasInline := false
	for _, value := range values {
		if value.Disposition == maildomain.AttachmentDispositionInline {
			hasInline = true
			break
		}
	}
	if !hasInline {
		return values
	}
	result := make([]maildomain.OutboundAttachment, 0, len(values))
	for _, value := range values {
		if value.Disposition == maildomain.AttachmentDispositionInline {
			result = append(result, value)
		}
	}
	for _, value := range values {
		if value.Disposition != maildomain.AttachmentDispositionInline {
			result = append(result, value)
		}
	}
	return result
}

func outboundAttemptAttachmentRecords(values []maildomain.Attachment) []maildomain.OutboundAttachmentRecord {
	result := make([]maildomain.OutboundAttachmentRecord, 0, len(values))
	for index, value := range values {
		result = append(result, maildomain.OutboundAttachmentRecord{
			Ordinal: index, Filename: value.Filename, MediaType: value.MediaType,
			ContentHash: value.ContentHash, StorageKey: value.StorageKey, ByteSize: value.ByteSize,
		})
	}
	return result
}

func validateOutboundRetry(ctx context.Context, repository *store.OutboundRepository, accountID string, request maildomain.SendRequest, requestHash string) error {
	if request.RetryOfAttemptID == "" {
		if request.AuthorizeUnknownRetry {
			return cli.WrapAppError(cli.ErrorUsage, "--retry-unknown requires --retry-of", false, nil)
		}
		return nil
	}
	original, err := repository.Attempt(ctx, request.RetryOfAttemptID)
	if err != nil {
		return cli.WrapAppError(cli.ErrorUnavailable, "cannot find the outbound attempt selected for retry", false, err)
	}
	if original.AccountID != accountID || original.RequestHash != requestHash {
		return cli.WrapAppError(cli.ErrorConflict, "retry request does not match the original normalized outbound request", false, nil)
	}
	if request.IdempotencyKey == "" || request.IdempotencyKey == original.IdempotencyKey {
		return cli.WrapAppError(cli.ErrorUsage, "an explicit retry requires a new idempotency key", false, nil)
	}
	switch original.Status {
	case maildomain.OutboundFailed:
		return nil
	case maildomain.OutboundUnknown:
		if !request.AuthorizeUnknownRetry {
			return cli.WrapAppError(cli.ErrorSafety, "retrying an unknown send requires --retry-unknown and --yes after checking Sent and the recipient", false, nil)
		}
		return nil
	case maildomain.OutboundPending:
		return cli.WrapAppError(cli.ErrorConflict, "a pending outbound attempt cannot be retried", false, nil)
	default:
		return cli.WrapAppError(cli.ErrorConflict, "a sent outbound attempt cannot be retried", false, nil)
	}
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
	outcomeCtx, cancel := durableOutcomeContext(ctx)
	defer cancel()
	updated, err := repository.MarkOutcome(outcomeCtx, attempt.ID, status, errorCode, now)
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
