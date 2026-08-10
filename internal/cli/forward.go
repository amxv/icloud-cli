package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type forwardFlags struct {
	sendFlags
	Original            string
	OriginalAttachments []string
}

type mailForwardPreviewData struct {
	Mode          string                    `json:"mode"`
	Preview       maildomain.ForwardPreview `json:"preview"`
	Safety        string                    `json:"safety"`
	SchemaVersion int64                     `json:"schema_version"`
}

func newMailForwardCommand(state *executionState) *cobra.Command {
	var flags forwardFlags
	var input jsonInputOptions
	command := &cobra.Command{
		Use:   "forward <msg_…>",
		Short: "Forward one explicitly selected local message as a new conversation",
		Args:  exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail forward msg_… --to teammate@example.com --body 'For context' --dry-run\n" +
			"  icloud mail forward msg_… --from mid_… --to teammate@example.com --original eml --include-attachment att_… --idempotency-key forward-01 --yes",
		RunE: func(cmd *cobra.Command, args []string) error {
			sourceID := ""
			if len(args) == 1 {
				sourceID = strings.TrimSpace(args[0])
			}
			var jsonRequest forwardJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest,
				"from", "to", "cc", "bcc", "reply-to", "body", "body-file", "html-body", "html-body-file",
				"header", "attach", "attach-name", "attach-type", "original", "include-attachment", "idempotency-key", "retry-of", "retry-unknown"); err != nil {
				return err
			} else if used {
				if jsonInputConsumesStdin(input) && forwardComposeConsumesStdin(jsonRequest.Compose) {
					return NewAppError(ErrorUsage, "body_file cannot read stdin when --input-json also reads stdin", false)
				}
				process := flags.sendFlags
				flags = forwardFlags{sendFlags: jsonRequest.Compose.sendFlags(), Original: jsonRequest.Original, OriginalAttachments: append([]string(nil), jsonRequest.IncludeAttachments...)}
				flags.DryRun, flags.Yes, flags.JSON, flags.Verbose, flags.ShowHeaders, flags.Format = process.DryRun, process.Yes, process.JSON, process.Verbose, process.ShowHeaders, process.Format
				sourceID = strings.TrimSpace(jsonRequest.MessageID)
			}
			if flags.DryRun && flags.Yes {
				return NewAppError(ErrorUsage, "--dry-run and --yes cannot be combined", false)
			}
			if flags.RetryUnknown && (strings.TrimSpace(flags.RetryOf) == "" || !flags.Yes || flags.DryRun) {
				return NewAppError(ErrorSafety, "--retry-unknown requires --retry-of and --yes on a real forward", false)
			}
			if !flags.DryRun && (flags.Verbose || flags.ShowHeaders || strings.TrimSpace(flags.Format) != "") {
				return NewAppError(ErrorUsage, "--verbose, --headers, and --format are dry-run preview expansions", false)
			}
			request, stdinConsumed, err := forwardRequestFromFlags(state, sourceID, flags)
			if err != nil {
				return err
			}
			if flags.DryRun {
				_, preview, schemaVersion, err := state.mail.MailForwardPreview(cmd.Context(), state.global.Home, state.global.Account, request)
				if err != nil {
					return err
				}
				return renderForwardPreview(cmd.Context(), state, preview, flags, schemaVersion)
			}
			interactive := isTerminalReader(state.stdin) && !state.global.NoInput
			if !flags.Yes && !interactive {
				return NewAppError(ErrorSafety, "non-interactive mail forward requires --yes", false)
			}
			if !flags.Yes && stdinConsumed {
				return NewAppError(ErrorSafety, "mail forward body consumed stdin; rerun with --yes after reviewing a dry-run", false)
			}
			if !flags.Yes {
				_, preview, _, err := state.mail.MailForwardPreview(cmd.Context(), state.global.Home, state.global.Account, request)
				if err != nil {
					return err
				}
				if err := renderCompactForwardPreview(state.stdout, preview, false, false); err != nil {
					return err
				}
				confirmed, err := confirmMailSend(state.stdin, state.stderr)
				if err != nil {
					return err
				}
				if !confirmed {
					return NewAppError(ErrorSafety, "mail forward canceled before contacting SMTP", false)
				}
				if preview.NewAttachmentCount > 0 {
					request.Attachments = append([]maildomain.OutboundAttachment(nil), preview.Send.Attachments[:preview.NewAttachmentCount]...)
				}
			}
			accountValue, attempt, reused, schemaVersion, err := state.mail.MailForward(cmd.Context(), state.global.Home, state.global.Account, request)
			if err != nil {
				return err
			}
			if flags.JSON {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSendData{
					AccountID: accountValue.ID, Attempt: sanitizeOutboundAttempt(attempt), Reused: reused, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderForwardOutcome(NewCompactTextRenderer(state.stdout), attempt, reused)
		},
	}
	bindForwardFlags(command, &flags)
	bindJSONInput(command, &input)
	return command
}

func bindForwardFlags(command *cobra.Command, flags *forwardFlags) {
	command.Flags().StringVar(&flags.From, "from", "", "Sender identity by stable mid_… ID or configured address (defaults to the account's local default identity)")
	command.Flags().StringArrayVar(&flags.To, "to", nil, "Recipient address; repeat or provide a comma-separated address list")
	command.Flags().StringArrayVar(&flags.CC, "cc", nil, "Cc recipient address; repeat as needed")
	command.Flags().StringArrayVar(&flags.BCC, "bcc", nil, "Bcc envelope recipient; omitted from message headers")
	command.Flags().StringVar(&flags.ReplyTo, "reply-to", "", "Optional Reply-To address")
	command.Flags().StringVar(&flags.Body, "body", "", "Optional introductory text body")
	command.Flags().StringVar(&flags.BodyFile, "body-file", "", "Introductory text body file path, or - for stdin")
	command.Flags().StringVar(&flags.HTMLBody, "html-body", "", "Optional introductory HTML body")
	command.Flags().StringVar(&flags.HTMLBodyFile, "html-body-file", "", "Introductory HTML body file path, or - for stdin")
	command.Flags().StringArrayVar(&flags.Headers, "header", nil, "Safe custom header as Name: Value; repeat as needed")
	command.Flags().StringArrayVar(&flags.Attachments, "attach", nil, "New regular file path to attach; repeat as needed")
	command.Flags().StringArrayVar(&flags.AttachmentNames, "attach-name", nil, "Override one new attachment filename as path=filename; repeat as needed")
	command.Flags().StringArrayVar(&flags.AttachmentTypes, "attach-type", nil, "Override one new attachment media type as path=type/subtype; repeat as needed")
	command.Flags().StringVar(&flags.Original, "original", string(maildomain.ForwardOriginalQuoted), "Original representation: quoted, eml, or none")
	command.Flags().StringArrayVar(&flags.OriginalAttachments, "include-attachment", nil, "Include one original att_… attachment by stable ID; repeat as needed")
	command.Flags().StringVar(&flags.IdempotencyKey, "idempotency-key", "", idempotencyKeyHelp)
	command.Flags().StringVar(&flags.RetryOf, "retry-of", "", "Link an explicit retry to one failed or unknown send_… attempt")
	command.Flags().BoolVar(&flags.RetryUnknown, "retry-unknown", false, "Authorize one duplicate-risk retry of an unknown attempt; requires --retry-of and --yes")
	command.Flags().BoolVar(&flags.DryRun, "dry-run", false, "Preview the resolved forward without creating an attempt or contacting SMTP")
	command.Flags().BoolVar(&flags.Yes, "yes", false, "Confirm an intentional non-interactive forward")
	command.Flags().BoolVar(&flags.JSON, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&flags.Verbose, "verbose", false, "With --dry-run, include custom-header detail and HTML byte count")
	command.Flags().BoolVar(&flags.ShowHeaders, "headers", false, "With --dry-run, print normalized headers; forward reply headers remain absent")
	command.Flags().StringVar(&flags.Format, "format", "", "With --dry-run, output text, html, or raw preview content")
}

func forwardRequestFromFlags(state *executionState, sourceID string, flags forwardFlags) (maildomain.ForwardRequest, bool, error) {
	parsed, stdinConsumed, err := sendRequestFromFlags(state, flags.sendFlags)
	if err != nil {
		return maildomain.ForwardRequest{}, false, err
	}
	mode, err := maildomain.NormalizeForwardOriginalMode(maildomain.ForwardOriginalMode(flags.Original))
	if err != nil {
		return maildomain.ForwardRequest{}, false, NewAppError(ErrorUsage, "--original must be quoted, eml, or none", false)
	}
	return maildomain.ForwardRequest{
		SourceMessageID: sourceID, SenderIdentityID: parsed.SenderIdentityID, OriginalMode: mode,
		OriginalAttachmentIDs: append([]string(nil), flags.OriginalAttachments...),
		To:                    parsed.To, CC: parsed.CC, BCC: parsed.BCC, ReplyTo: parsed.ReplyTo,
		TextBody: parsed.TextBody, HTMLBody: parsed.HTMLBody,
		Headers: parsed.Headers, Attachments: parsed.Attachments,
		IdempotencyKey: parsed.IdempotencyKey, RetryOfAttemptID: parsed.RetryOfAttemptID,
		AuthorizeUnknownRetry: parsed.AuthorizeUnknownRetry,
	}, stdinConsumed, nil
}

func renderForwardPreview(ctx context.Context, state *executionState, preview maildomain.ForwardPreview, flags forwardFlags, schemaVersion int64) error {
	if flags.JSON {
		return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailForwardPreviewData{
			Mode: "dry-run", Preview: preview,
			Safety: "no SMTP connection or outbound attempt was created", SchemaVersion: schemaVersion,
		}, Meta: EnvelopeMeta{RequestID: state.requestID}})
	}
	switch format := strings.ToLower(strings.TrimSpace(flags.Format)); format {
	case "":
		return renderCompactForwardPreview(state.stdout, preview, flags.ShowHeaders, flags.Verbose)
	case "text":
		_, err := io.WriteString(state.stdout, preview.Send.TextBody)
		return err
	case "html":
		if preview.Send.HTMLBody == "" {
			return NewAppError(ErrorUnavailable, "HTML body is not present in this forward preview", false)
		}
		_, err := io.WriteString(state.stdout, preview.Send.HTMLBody)
		return err
	case "raw":
		messageID, err := maildomain.GenerateOutboundMessageID(preview.Send.From.Email)
		if err != nil {
			return WrapAppError(ErrorInternal, "cannot allocate a forward preview Message-ID", false, err)
		}
		return maildomain.ComposeOutboundMessage(state.stdout, previewToRequest(preview.Send), messageID, time.Now().UTC())
	default:
		return NewAppError(ErrorUsage, "--format must be text, html, or raw", false)
	}
}

func renderCompactForwardPreview(writer io.Writer, preview maildomain.ForwardPreview, showHeaders, verbose bool) error {
	renderer := NewCompactTextRenderer(writer)
	bodyExcerpt := Excerpt(preview.Send.TextBody, DefaultExcerptRunes)
	if err := renderer.Record("forward-preview", preview.SourceMessageID,
		Field("kind", maildomain.OutboundKindForward),
		Field("source_thread_id", preview.SourceThreadID),
		Field("sender_identity", preview.Send.SenderIdentityID),
		Field("from", formatAddress(preview.Send.From)),
		Field("original", preview.OriginalMode),
		Field("attached_eml", fmt.Sprintf("%t", preview.AttachedOriginalEML)),
		Field("original_eml_bytes", preview.OriginalEMLByteSize),
		Field("original_attachments", len(preview.OriginalAttachments)),
		Field("new_attachments", preview.NewAttachmentCount),
		Field("to", formatAddresses(preview.Send.To)),
		Field("cc", formatAddresses(preview.Send.CC)),
		Field("bcc", formatAddresses(preview.Send.BCC)),
		Field("subject", preview.Send.Subject),
		Field("body_parts", preview.Send.BodyParts),
		Field("attachments", preview.Send.AttachmentCount),
		Field("text_bytes", len(preview.Send.TextBody)),
		Field("text_excerpt", bodyExcerpt),
		Field("text_truncated", len([]rune(preview.Send.TextBody)) > DefaultExcerptRunes),
		Field("request_hash", preview.Send.RequestHash),
		Field("html_bytes", verboseValue(verbose, len(preview.Send.HTMLBody))),
	); err != nil {
		return err
	}
	for _, attachment := range preview.OriginalAttachments {
		if err := renderer.Record("original-attachment", attachment.ID,
			Field("filename", attachment.Filename), Field("media_type", attachment.MediaType), Field("bytes", attachment.ByteSize)); err != nil {
			return err
		}
	}
	if showHeaders {
		for _, header := range sendPreviewHeaders(preview.Send) {
			if err := renderer.Record("header", header.Key, Field("value", header.Value)); err != nil {
				return err
			}
		}
	}
	for _, attachment := range preview.Send.Attachments {
		if err := renderer.Record("attachment", attachment.Filename,
			Field("media_type", attachment.MediaType), Field("bytes", attachment.ByteSize), Field("sha256", attachment.ContentHash)); err != nil {
			return err
		}
	}
	return renderer.Summary("forward-safety",
		Field("dry_run", "true"), Field("smtp_contacted", "false"), Field("attempt_created", "false"),
		Field("source_mutated", "false"),
		Field("next", "review the original representation and included attachments, then rerun without --dry-run and use --yes when non-interactive"),
	)
}

func renderForwardOutcome(renderer *CompactTextRenderer, attempt maildomain.OutboundAttempt, reused bool) error {
	return renderer.Record("mail-forward", attempt.ID,
		Field("kind", attempt.Kind),
		Field("sender_identity", attempt.SenderIdentityID),
		Field("source_message_id", attempt.SourceMessageID),
		Field("status", attempt.Status),
		Field("message_id", attempt.MessageID),
		Field("thread_id", attempt.ThreadID),
		Field("reconciliation", attempt.Reconciliation),
		Field("error_code", attempt.ErrorCode),
		Field("reused", fmt.Sprintf("%t", reused)),
	)
}
