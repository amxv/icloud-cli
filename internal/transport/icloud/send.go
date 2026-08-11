package icloud

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-sasl"
	smtp "github.com/emersion/go-smtp"
)

func (client *Client) SendSMTP(ctx context.Context, auth transport.AuthRequest, request transport.SMTPSendRequest) (transport.SMTPSendResult, error) {
	from := strings.TrimSpace(request.Envelope.From)
	if from == "" || len(request.Envelope.Recipients) == 0 || request.Message == nil {
		return transport.SMTPSendResult{}, smtpSendFailure(
			transport.SMTPSendFailed, transport.StageSMTPMail, transport.FailureConfiguration, false, true,
			errors.New("SMTP envelope and message are required"),
		)
	}
	address := net.JoinHostPort(client.config.SMTPHost, client.config.SMTPPort)
	conn, err := client.dial(ctx, "tcp", address)
	if err != nil {
		return transport.SMTPSendResult{}, wrapSMTPSendFailure(transport.SMTPSendFailed, classifyConnectError(err), true)
	}
	defer conn.Close()
	var smtpClient *smtp.Client
	if err := runWithDeadline(ctx, conn, client.config.StageTimeout, func() error {
		var startTLSErr error
		smtpClient, startTLSErr = smtp.NewClientStartTLS(conn, client.tlsConfig(client.config.SMTPHost))
		return startTLSErr
	}); err != nil {
		return transport.SMTPSendResult{}, smtpSendFailure(
			transport.SMTPSendFailed, transport.StageSMTPStartTLS, transport.FailureUnavailable, retryableNetworkError(err), true, err,
		)
	}
	defer smtpClient.Close()
	smtpClient.CommandTimeout = client.config.StageTimeout
	smtpClient.SubmissionTimeout = client.config.StageTimeout
	if err := runWithDeadline(ctx, conn, client.config.StageTimeout, func() error {
		return smtpClient.Auth(sasl.NewPlainClient("", auth.Username, string(auth.Password)))
	}); err != nil {
		failure := classifyAuthenticationFailure(transport.StageSMTPAuth, err)
		return transport.SMTPSendResult{}, wrapSMTPSendFailure(transport.SMTPSendFailed, failure, true)
	}
	if err := runWithDeadline(ctx, conn, client.config.StageTimeout, func() error {
		return smtpClient.Mail(from, nil)
	}); err != nil {
		return transport.SMTPSendResult{}, smtpCommandSendError(transport.StageSMTPMail, transport.SMTPSendFailed, err, true)
	}
	seen := map[string]struct{}{}
	for _, recipient := range request.Envelope.Recipients {
		recipient = strings.TrimSpace(recipient)
		key := strings.ToLower(recipient)
		if recipient == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		if err := runWithDeadline(ctx, conn, client.config.StageTimeout, func() error {
			return smtpClient.Rcpt(recipient, nil)
		}); err != nil {
			return transport.SMTPSendResult{}, smtpCommandSendError(transport.StageSMTPRecipient, transport.SMTPSendFailed, err, true)
		}
	}
	if len(seen) == 0 {
		return transport.SMTPSendResult{}, smtpSendFailure(
			transport.SMTPSendFailed, transport.StageSMTPRecipient, transport.FailureConfiguration, false, true,
			errors.New("at least one SMTP recipient is required"),
		)
	}
	data, err := runValueWithDeadline(ctx, conn, client.config.StageTimeout, smtpClient.Data)
	if err != nil {
		return transport.SMTPSendResult{}, smtpCommandSendError(transport.StageSMTPData, transport.SMTPSendFailed, err, true)
	}
	if err := runWithDeadline(ctx, conn, client.config.StageTimeout, func() error {
		_, copyErr := io.Copy(data, request.Message)
		return copyErr
	}); err != nil {
		// DATA may already be in flight. The deadline helper closes the
		// connection and joins the writer before returning; do not emit a DATA
		// terminator or touch the command again on this ambiguous path.
		return transport.SMTPSendResult{}, smtpCommandSendError(transport.StageSMTPWrite, transport.SMTPSendUnknown, err, false)
	}
	response, err := runValueWithDeadline(ctx, conn, client.config.StageTimeout, data.CloseWithResponse)
	if err != nil {
		outcome := transport.SMTPSendUnknown
		var smtpErr *smtp.SMTPError
		if errors.As(err, &smtpErr) && smtpErr.Code >= 400 && smtpErr.Code < 600 {
			outcome = transport.SMTPSendFailed
		}
		return transport.SMTPSendResult{}, smtpCommandSendError(transport.StageSMTPAccept, outcome, err, false)
	}
	// The final 250 response is the acceptance boundary. A later QUIT failure
	// cannot turn an accepted message into an unknown send.
	_ = smtpClient.Quit()
	return transport.SMTPSendResult{Accepted: true, StatusText: response.StatusText}, nil
}

func smtpCommandSendError(stage transport.Stage, outcome transport.SMTPSendOutcome, err error, outboxEligible bool) error {
	kind := transport.FailureUnavailable
	retryable := retryableNetworkError(err)
	var smtpErr *smtp.SMTPError
	if errors.As(err, &smtpErr) {
		retryable = smtpErr.Temporary()
	}
	return smtpSendFailure(outcome, stage, kind, retryable, outboxEligible, err)
}

func smtpSendFailure(outcome transport.SMTPSendOutcome, stage transport.Stage, kind transport.FailureKind, retryable, outboxEligible bool, err error) error {
	if isTimeout(err) {
		stage = transport.StageTimeout
		retryable = true
	}
	return &transport.SMTPSendError{
		Outcome:        outcome,
		Failure:        transport.NewFailure(stage, kind, retryable, err),
		OutboxEligible: outboxEligible,
	}
}

func wrapSMTPSendFailure(outcome transport.SMTPSendOutcome, err error, outboxEligible bool) error {
	failure, ok := transport.AsFailure(err)
	if !ok {
		failure = transport.NewFailure(transport.StageConnect, transport.FailureUnavailable, retryableNetworkError(err), err)
	}
	return &transport.SMTPSendError{Outcome: outcome, Failure: failure, OutboxEligible: outboxEligible}
}
