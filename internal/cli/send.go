package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"os"
	"sort"
	"strings"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type sendFlags struct {
	To             []string
	CC             []string
	BCC            []string
	ReplyTo        string
	Subject        string
	Body           string
	BodyFile       string
	HTMLBody       string
	HTMLBodyFile   string
	Headers        []string
	IdempotencyKey string
	DryRun         bool
	Yes            bool
	JSON           bool
	Verbose        bool
	ShowHeaders    bool
	Format         string
}

type mailSendData struct {
	AccountID     string                `json:"account_id"`
	Attempt       publicOutboundAttempt `json:"attempt"`
	Reused        bool                  `json:"reused"`
	SchemaVersion int64                 `json:"schema_version"`
}

type publicOutboundAttempt struct {
	ID             string                         `json:"id"`
	Status         maildomain.OutboundStatus      `json:"status"`
	MessageID      string                         `json:"message_id,omitempty"`
	ThreadID       string                         `json:"thread_id,omitempty"`
	Reconciliation maildomain.ReconciliationState `json:"reconciliation"`
	ErrorCode      string                         `json:"error_code,omitempty"`
	AcceptedAt     *time.Time                     `json:"accepted_at,omitempty"`
}

type mailSendPreviewData struct {
	Mode          string                 `json:"mode"`
	Preview       maildomain.SendPreview `json:"preview"`
	Safety        string                 `json:"safety"`
	SchemaVersion int64                  `json:"schema_version"`
}

func newMailSendCommand(state *executionState) *cobra.Command {
	var flags sendFlags
	command := &cobra.Command{
		Use:   "send",
		Short: "Preview or send a new text/HTML message safely and idempotently",
		Args:  cobra.NoArgs,
		Example: "  icloud mail send --to client@example.com --subject 'Update' --body-file message.txt --dry-run\n" +
			"  icloud mail send --to 'Client <client@example.com>' --subject 'Update' --body 'Hello' --yes --idempotency-key send-01\n" +
			"  icloud mail send --to client@example.com --subject 'Update' --body-file - --yes --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flags.DryRun && flags.Yes {
				return NewAppError(ErrorUsage, "--dry-run and --yes cannot be combined", false)
			}
			if !flags.DryRun && (flags.Verbose || flags.ShowHeaders || strings.TrimSpace(flags.Format) != "") {
				return NewAppError(ErrorUsage, "--verbose, --headers, and --format are dry-run preview expansions", false)
			}
			request, stdinConsumed, err := sendRequestFromFlags(state, flags)
			if err != nil {
				return err
			}
			if flags.DryRun {
				_, preview, schemaVersion, err := state.mail.MailSendPreview(cmd.Context(), state.global.Home, state.global.Account, request)
				if err != nil {
					return err
				}
				return renderSendPreview(cmd.Context(), state, preview, flags, schemaVersion)
			}
			interactive := isTerminalReader(state.stdin) && !state.global.NoInput
			if !flags.Yes && !interactive {
				return NewAppError(ErrorSafety, "non-interactive mail send requires --yes", false)
			}
			if !flags.Yes && stdinConsumed {
				return NewAppError(ErrorSafety, "mail body consumed stdin; rerun with --yes after reviewing a dry-run", false)
			}
			if !flags.Yes {
				_, preview, schemaVersion, err := state.mail.MailSendPreview(cmd.Context(), state.global.Home, state.global.Account, request)
				if err != nil {
					return err
				}
				if err := renderCompactSendPreview(state.stdout, preview, false, false); err != nil {
					return err
				}
				confirmed, err := confirmMailSend(state.stdin, state.stderr)
				if err != nil {
					return err
				}
				if !confirmed {
					return NewAppError(ErrorSafety, "mail send canceled before contacting SMTP", false)
				}
				_ = schemaVersion
			}
			accountValue, attempt, reused, schemaVersion, err := state.mail.MailSend(cmd.Context(), state.global.Home, state.global.Account, request)
			if err != nil {
				return err
			}
			if flags.JSON {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSendData{
					AccountID: accountValue.ID, Attempt: sanitizeOutboundAttempt(attempt), Reused: reused, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderSendOutcome(NewCompactTextRenderer(state.stdout), attempt, reused)
		},
	}
	command.Flags().StringArrayVar(&flags.To, "to", nil, "Recipient address; repeat or provide a comma-separated address list")
	command.Flags().StringArrayVar(&flags.CC, "cc", nil, "Cc recipient address; repeat as needed")
	command.Flags().StringArrayVar(&flags.BCC, "bcc", nil, "Bcc envelope recipient; omitted from message headers")
	command.Flags().StringVar(&flags.ReplyTo, "reply-to", "", "Optional Reply-To address")
	command.Flags().StringVar(&flags.Subject, "subject", "", "Message subject")
	command.Flags().StringVar(&flags.Body, "body", "", "Inline text body")
	command.Flags().StringVar(&flags.BodyFile, "body-file", "", "Text body file path, or - for stdin")
	command.Flags().StringVar(&flags.HTMLBody, "html-body", "", "Inline HTML body")
	command.Flags().StringVar(&flags.HTMLBodyFile, "html-body-file", "", "HTML body file path, or - for stdin")
	command.Flags().StringArrayVar(&flags.Headers, "header", nil, "Safe custom header as Name: Value; repeat as needed")
	command.Flags().StringVar(&flags.IdempotencyKey, "idempotency-key", "", "Return the original attempt when the same normalized request is repeated")
	command.Flags().BoolVar(&flags.DryRun, "dry-run", false, "Preview the normalized message without creating an attempt or contacting SMTP")
	command.Flags().BoolVar(&flags.Yes, "yes", false, "Confirm an intentional non-interactive send")
	command.Flags().BoolVar(&flags.JSON, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&flags.Verbose, "verbose", false, "With --dry-run, include normalized custom-header detail and HTML byte count")
	command.Flags().BoolVar(&flags.ShowHeaders, "headers", false, "With --dry-run, print normalized safe header fields (Bcc remains omitted)")
	command.Flags().StringVar(&flags.Format, "format", "", "With --dry-run, output text, html, or raw preview content")
	return command
}

func sendRequestFromFlags(state *executionState, flags sendFlags) (maildomain.SendRequest, bool, error) {
	to, err := parseSendAddresses(flags.To)
	if err != nil {
		return maildomain.SendRequest{}, false, NewAppError(ErrorUsage, "invalid --to address", false)
	}
	cc, err := parseSendAddresses(flags.CC)
	if err != nil {
		return maildomain.SendRequest{}, false, NewAppError(ErrorUsage, "invalid --cc address", false)
	}
	bcc, err := parseSendAddresses(flags.BCC)
	if err != nil {
		return maildomain.SendRequest{}, false, NewAppError(ErrorUsage, "invalid --bcc address", false)
	}
	var replyTo *maildomain.Address
	if strings.TrimSpace(flags.ReplyTo) != "" {
		value, err := maildomain.ParseSendAddress(flags.ReplyTo)
		if err != nil {
			return maildomain.SendRequest{}, false, NewAppError(ErrorUsage, "invalid --reply-to address", false)
		}
		replyTo = &value
	}
	text, textStdin, err := readSendBody(state.stdin, flags.Body, flags.BodyFile, "text")
	if err != nil {
		return maildomain.SendRequest{}, false, err
	}
	html, htmlStdin, err := readSendBody(state.stdin, flags.HTMLBody, flags.HTMLBodyFile, "HTML")
	if err != nil {
		return maildomain.SendRequest{}, false, err
	}
	if textStdin && htmlStdin {
		return maildomain.SendRequest{}, false, NewAppError(ErrorUsage, "text and HTML bodies cannot both read from stdin", false)
	}
	headers := make([]maildomain.CustomHeader, 0, len(flags.Headers))
	for _, value := range flags.Headers {
		key, body, ok := strings.Cut(value, ":")
		if !ok {
			return maildomain.SendRequest{}, false, NewAppError(ErrorUsage, "--header must use Name: Value", false)
		}
		headers = append(headers, maildomain.CustomHeader{Key: key, Value: body})
	}
	return maildomain.SendRequest{
		To: to, CC: cc, BCC: bcc, ReplyTo: replyTo,
		Subject: flags.Subject, TextBody: text, HTMLBody: html,
		Headers: headers, IdempotencyKey: flags.IdempotencyKey,
	}, textStdin || htmlStdin, nil
}

func parseSendAddresses(values []string) ([]maildomain.Address, error) {
	result := []maildomain.Address{}
	for _, value := range values {
		parsed, err := mail.ParseAddressList(strings.TrimSpace(value))
		if err != nil {
			return nil, err
		}
		for _, address := range parsed {
			if address != nil {
				result = append(result, maildomain.Address{DisplayName: address.Name, Email: address.Address})
			}
		}
	}
	return result, nil
}

func readSendBody(stdin io.Reader, inline, path, kind string) (string, bool, error) {
	if inline != "" && strings.TrimSpace(path) != "" {
		return "", false, NewAppError(ErrorUsage, "choose either inline "+kind+" body or its body-file flag", false)
	}
	if inline != "" {
		if len(inline) > maildomain.MaxSendBodyBytes {
			return "", false, NewAppError(ErrorSafety, kind+" body exceeds the 8 MiB limit", false)
		}
		return inline, false, nil
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false, nil
	}
	reader := stdin
	consumed := path == "-"
	var file *os.File
	if !consumed {
		var err error
		file, err = os.Open(path)
		if err != nil {
			return "", false, WrapAppError(ErrorConfiguration, "cannot open the "+kind+" body file", false, err)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return "", false, NewAppError(ErrorSafety, kind+" body path must be a regular file", false)
		}
		if info.Size() > maildomain.MaxSendBodyBytes {
			return "", false, NewAppError(ErrorSafety, kind+" body exceeds the 8 MiB limit", false)
		}
		reader = file
	}
	content, err := io.ReadAll(io.LimitReader(reader, maildomain.MaxSendBodyBytes+1))
	if err != nil {
		return "", consumed, WrapAppError(ErrorConfiguration, "cannot read the "+kind+" body", false, err)
	}
	if len(content) > maildomain.MaxSendBodyBytes {
		return "", consumed, NewAppError(ErrorSafety, kind+" body exceeds the 8 MiB limit", false)
	}
	return string(content), consumed, nil
}

func renderSendPreview(ctx context.Context, state *executionState, preview maildomain.SendPreview, flags sendFlags, schemaVersion int64) error {
	if flags.JSON {
		return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSendPreviewData{
			Mode: "dry-run", Preview: preview,
			Safety: "no SMTP connection or outbound attempt was created", SchemaVersion: schemaVersion,
		}, Meta: EnvelopeMeta{RequestID: state.requestID}})
	}
	format := strings.ToLower(strings.TrimSpace(flags.Format))
	switch format {
	case "":
		return renderCompactSendPreview(state.stdout, preview, flags.ShowHeaders, flags.Verbose)
	case "text":
		_, err := io.WriteString(state.stdout, preview.TextBody)
		return err
	case "html":
		if preview.HTMLBody == "" {
			return NewAppError(ErrorUnavailable, "HTML body is not present in this preview", false)
		}
		_, err := io.WriteString(state.stdout, preview.HTMLBody)
		return err
	case "raw":
		messageID, err := maildomain.GenerateOutboundMessageID(preview.From.Email)
		if err != nil {
			return WrapAppError(ErrorInternal, "cannot allocate a dry-run Message-ID", false, err)
		}
		return maildomain.ComposeOutboundMessage(state.stdout, previewToRequest(preview), messageID, time.Now().UTC())
	default:
		return NewAppError(ErrorUsage, "--format must be text, html, or raw", false)
	}
}

func renderCompactSendPreview(writer io.Writer, preview maildomain.SendPreview, showHeaders, verbose bool) error {
	renderer := NewCompactTextRenderer(writer)
	if err := renderer.Record("send-preview", preview.AccountID,
		Field("from", formatAddress(preview.From)),
		Field("to", formatAddresses(preview.To)),
		Field("cc", formatAddresses(preview.CC)),
		Field("bcc", formatAddresses(preview.BCC)),
		Field("reply_to", optionalAddress(preview.ReplyTo)),
		Field("subject", preview.Subject),
		Field("body_parts", fmt.Sprintf("%d", preview.BodyParts)),
		Field("attachments", fmt.Sprintf("%d", preview.AttachmentCount)),
		Field("request_hash", preview.RequestHash),
		Field("html_bytes", verboseValue(verbose, len(preview.HTMLBody))),
	); err != nil {
		return err
	}
	if showHeaders {
		headers := sendPreviewHeaders(preview)
		for _, header := range headers {
			if err := renderer.Record("header", header.Key, Field("value", header.Value)); err != nil {
				return err
			}
		}
	}
	if preview.TextBody != "" {
		if _, err := fmt.Fprintf(writer, "\ntext-body\n%s\n", preview.TextBody); err != nil {
			return err
		}
	}
	return renderer.Summary("send-safety",
		Field("dry_run", "true"),
		Field("smtp_contacted", "false"),
		Field("attempt_created", "false"),
		Field("next", "review, then rerun without --dry-run and use --yes when non-interactive"),
	)
}

func sendPreviewHeaders(preview maildomain.SendPreview) []maildomain.CustomHeader {
	headers := []maildomain.CustomHeader{
		{Key: "From", Value: formatAddress(preview.From)},
		{Key: "To", Value: formatAddresses(preview.To)},
		{Key: "Subject", Value: preview.Subject},
	}
	if value := formatAddresses(preview.CC); value != "" {
		headers = append(headers, maildomain.CustomHeader{Key: "Cc", Value: value})
	}
	if value := optionalAddress(preview.ReplyTo); value != "" {
		headers = append(headers, maildomain.CustomHeader{Key: "Reply-To", Value: value})
	}
	custom := append([]maildomain.CustomHeader(nil), preview.Headers...)
	sort.Slice(custom, func(i, j int) bool {
		return strings.ToLower(custom[i].Key) < strings.ToLower(custom[j].Key)
	})
	return append(headers, custom...)
}

func renderSendOutcome(renderer *CompactTextRenderer, attempt maildomain.OutboundAttempt, reused bool) error {
	return renderer.Record("mail-send", attempt.ID,
		Field("status", attempt.Status),
		Field("message_id", attempt.MessageID),
		Field("thread_id", attempt.ThreadID),
		Field("reconciliation", attempt.Reconciliation),
		Field("error_code", attempt.ErrorCode),
		Field("reused", fmt.Sprintf("%t", reused)),
	)
}

func sanitizeOutboundAttempt(value maildomain.OutboundAttempt) publicOutboundAttempt {
	return publicOutboundAttempt{
		ID: value.ID, Status: value.Status, MessageID: value.MessageID,
		ThreadID: value.ThreadID, Reconciliation: value.Reconciliation,
		ErrorCode: value.ErrorCode, AcceptedAt: value.AcceptedAt,
	}
}

func confirmMailSend(reader io.Reader, stderr io.Writer) (bool, error) {
	if _, err := fmt.Fprint(stderr, "Send this message? [y/N]: "); err != nil {
		return false, WrapAppError(ErrorInternal, "cannot write the send confirmation prompt", false, err)
	}
	line, err := bufio.NewReader(io.LimitReader(reader, 64)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, WrapAppError(ErrorConfiguration, "cannot read send confirmation", false, err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func previewToRequest(preview maildomain.SendPreview) maildomain.SendRequest {
	return maildomain.SendRequest{
		From: preview.From, To: preview.To, CC: preview.CC, BCC: preview.BCC,
		ReplyTo: preview.ReplyTo, Subject: preview.Subject,
		TextBody: preview.TextBody, HTMLBody: preview.HTMLBody, Headers: preview.Headers,
	}
}

func optionalAddress(value *maildomain.Address) string {
	if value == nil {
		return ""
	}
	return formatAddress(*value)
}

func verboseValue(enabled bool, value int) any {
	if !enabled {
		return nil
	}
	return value
}

type terminalInput struct {
	io.Reader
	Terminal bool
}

func (value terminalInput) IsTerminal() bool { return value.Terminal }
