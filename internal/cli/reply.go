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

type replyFlags struct {
	sendFlags
	ReplaceTo bool
	ReplaceCC bool
	Folder    string
}

type mailReplyPreviewData struct {
	Mode          string                  `json:"mode"`
	Preview       maildomain.ReplyPreview `json:"preview"`
	Safety        string                  `json:"safety"`
	SchemaVersion int64                   `json:"schema_version"`
}

func newMailReplyCommand(state *executionState, replyAll bool) *cobra.Command {
	var flags replyFlags
	var input jsonInputOptions
	name, short := "reply", "Reply to one explicitly selected local message"
	if replyAll {
		name, short = "reply-all", "Reply to all visible participants of one explicitly selected local message"
	}
	command := &cobra.Command{
		Use:   name + " <msg_…>",
		Short: short,
		Args:  exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail " + name + " msg_… --body-file reply.txt --dry-run\n" +
			"  icloud mail " + name + " msg_… --from mid_… --body 'Thanks' --idempotency-key reply-01 --yes",
		RunE: func(cmd *cobra.Command, args []string) error {
			parentID := ""
			if len(args) == 1 {
				parentID = strings.TrimSpace(args[0])
			}
			var jsonRequest replyJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest,
				"folder", "from", "to", "cc", "bcc", "replace-to", "replace-cc", "body", "body-file", "html-body", "html-body-file",
				"header", "attach", "attach-name", "attach-type", "idempotency-key", "retry-of", "retry-unknown"); err != nil {
				return err
			} else if used {
				if jsonInputConsumesStdin(input) && replyComposeConsumesStdin(jsonRequest.Compose) {
					return NewAppError(ErrorUsage, "body_file cannot read stdin when --input-json also reads stdin", false)
				}
				process := flags.sendFlags
				flags = replyFlags{sendFlags: jsonRequest.Compose.sendFlags(), ReplaceTo: jsonRequest.ReplaceTo, ReplaceCC: jsonRequest.ReplaceCC, Folder: jsonRequest.Folder}
				flags.DryRun, flags.Yes, flags.JSON, flags.Verbose, flags.ShowHeaders, flags.Format = process.DryRun, process.Yes, process.JSON, process.Verbose, process.ShowHeaders, process.Format
				parentID = strings.TrimSpace(jsonRequest.MessageID)
			}
			if flags.DryRun && flags.Yes {
				return NewAppError(ErrorUsage, "--dry-run and --yes cannot be combined", false)
			}
			if flags.RetryUnknown && (strings.TrimSpace(flags.RetryOf) == "" || !flags.Yes || flags.DryRun) {
				return NewAppError(ErrorSafety, "--retry-unknown requires --retry-of and --yes on a real reply", false)
			}
			if !flags.DryRun && (flags.Verbose || flags.ShowHeaders || strings.TrimSpace(flags.Format) != "") {
				return NewAppError(ErrorUsage, "--verbose, --headers, and --format are dry-run preview expansions", false)
			}
			request, stdinConsumed, err := replyRequestFromFlags(state, parentID, replyAll, flags)
			if err != nil {
				return err
			}
			if flags.DryRun {
				_, preview, schemaVersion, err := state.mail.MailReplyPreview(cmd.Context(), state.global.Home, state.global.Account, request)
				if err != nil {
					return err
				}
				return renderReplyPreview(cmd.Context(), state, preview, flags, schemaVersion)
			}
			interactive := isTerminalReader(state.stdin) && !state.global.NoInput
			if !flags.Yes && !interactive {
				return NewAppError(ErrorSafety, "non-interactive mail reply requires --yes", false)
			}
			if !flags.Yes && stdinConsumed {
				return NewAppError(ErrorSafety, "mail reply body consumed stdin; rerun with --yes after reviewing a dry-run", false)
			}
			if !flags.Yes {
				_, preview, _, err := state.mail.MailReplyPreview(cmd.Context(), state.global.Home, state.global.Account, request)
				if err != nil {
					return err
				}
				if err := renderCompactReplyPreview(state.stdout, preview, false, false); err != nil {
					return err
				}
				confirmed, err := confirmMailSend(state.stdin, state.stderr)
				if err != nil {
					return err
				}
				if !confirmed {
					return NewAppError(ErrorSafety, "mail reply canceled before contacting SMTP", false)
				}
				request.Attachments = preview.Send.Attachments
			}
			accountValue, attempt, reused, schemaVersion, err := state.mail.MailReply(cmd.Context(), state.global.Home, state.global.Account, request)
			if err != nil {
				return err
			}
			if flags.JSON {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSendData{
					AccountID: accountValue.ID, Attempt: sanitizeOutboundAttempt(attempt), Reused: reused, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderReplyOutcome(NewCompactTextRenderer(state.stdout), attempt, reused)
		},
	}
	bindReplyFlags(command, &flags)
	bindJSONInput(command, &input)
	return command
}

func bindReplyFlags(command *cobra.Command, flags *replyFlags) {
	command.Flags().StringVar(&flags.Folder, "folder", "", "Select the exact current provider parent location when the message has multiple locations")
	command.Flags().StringVar(&flags.From, "from", "", "Sender identity by stable mid_… ID or configured address (defaults to the account's local default identity)")
	command.Flags().StringArrayVar(&flags.To, "to", nil, "Add a To recipient; repeat or provide a comma-separated address list")
	command.Flags().StringArrayVar(&flags.CC, "cc", nil, "Add a Cc recipient; repeat as needed")
	command.Flags().StringArrayVar(&flags.BCC, "bcc", nil, "Add a new Bcc envelope recipient; prior Bcc recipients are never inferred")
	command.Flags().BoolVar(&flags.ReplaceTo, "replace-to", false, "Replace derived To recipients with the addresses supplied by --to")
	command.Flags().BoolVar(&flags.ReplaceCC, "replace-cc", false, "Replace derived Cc recipients with the addresses supplied by --cc")
	command.Flags().StringVar(&flags.Body, "body", "", "Inline text body")
	command.Flags().StringVar(&flags.BodyFile, "body-file", "", "Text body file path, or - for stdin")
	command.Flags().StringVar(&flags.HTMLBody, "html-body", "", "Inline HTML body")
	command.Flags().StringVar(&flags.HTMLBodyFile, "html-body-file", "", "HTML body file path, or - for stdin")
	command.Flags().StringArrayVar(&flags.Headers, "header", nil, "Safe custom header as Name: Value; repeat as needed")
	command.Flags().StringArrayVar(&flags.Attachments, "attach", nil, "Regular file path to attach; repeat as needed")
	command.Flags().StringArrayVar(&flags.AttachmentNames, "attach-name", nil, "Override one attachment filename as path=filename; repeat as needed")
	command.Flags().StringArrayVar(&flags.AttachmentTypes, "attach-type", nil, "Override one attachment media type as path=type/subtype; repeat as needed")
	command.Flags().StringVar(&flags.IdempotencyKey, "idempotency-key", "", idempotencyKeyHelp)
	command.Flags().StringVar(&flags.RetryOf, "retry-of", "", "Link an explicit retry to one failed or unknown send_… attempt")
	command.Flags().BoolVar(&flags.RetryUnknown, "retry-unknown", false, "Authorize one duplicate-risk retry of an unknown attempt; requires --retry-of and --yes")
	command.Flags().BoolVar(&flags.DryRun, "dry-run", false, "Preview the resolved reply without creating an attempt or contacting SMTP")
	command.Flags().BoolVar(&flags.Yes, "yes", false, "Confirm an intentional non-interactive reply")
	command.Flags().BoolVar(&flags.JSON, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&flags.Verbose, "verbose", false, "With --dry-run, include custom-header detail and HTML byte count")
	command.Flags().BoolVar(&flags.ShowHeaders, "headers", false, "With --dry-run, print normalized headers including RFC threading fields")
	command.Flags().StringVar(&flags.Format, "format", "", "With --dry-run, output text, html, or raw preview content")
}

func replyRequestFromFlags(state *executionState, parentID string, replyAll bool, flags replyFlags) (maildomain.ReplyRequest, bool, error) {
	parsed, stdinConsumed, err := sendRequestFromFlags(state, flags.sendFlags)
	if err != nil {
		return maildomain.ReplyRequest{}, false, err
	}
	if flags.ReplaceTo && len(parsed.To) == 0 {
		return maildomain.ReplyRequest{}, false, NewAppError(ErrorUsage, "--replace-to requires at least one --to address", false)
	}
	return maildomain.ReplyRequest{
		ParentMessageID: parentID, ParentFolder: flags.Folder, SenderIdentityID: parsed.SenderIdentityID, ReplyAll: replyAll,
		To: parsed.To, CC: parsed.CC, BCC: parsed.BCC,
		ReplaceTo: flags.ReplaceTo, ReplaceCC: flags.ReplaceCC,
		TextBody: parsed.TextBody, HTMLBody: parsed.HTMLBody,
		Headers: parsed.Headers, Attachments: parsed.Attachments,
		IdempotencyKey: parsed.IdempotencyKey, RetryOfAttemptID: parsed.RetryOfAttemptID,
		AuthorizeUnknownRetry: parsed.AuthorizeUnknownRetry,
	}, stdinConsumed, nil
}

func renderReplyPreview(ctx context.Context, state *executionState, preview maildomain.ReplyPreview, flags replyFlags, schemaVersion int64) error {
	if flags.JSON {
		return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailReplyPreviewData{
			Mode: "dry-run", Preview: preview,
			Safety: "no SMTP connection or outbound attempt was created", SchemaVersion: schemaVersion,
		}, Meta: EnvelopeMeta{RequestID: state.requestID}})
	}
	switch format := strings.ToLower(strings.TrimSpace(flags.Format)); format {
	case "":
		return renderCompactReplyPreview(state.stdout, preview, flags.ShowHeaders, flags.Verbose)
	case "text":
		_, err := io.WriteString(state.stdout, preview.Send.TextBody)
		return err
	case "html":
		if preview.Send.HTMLBody == "" {
			return NewAppError(ErrorUnavailable, "HTML body is not present in this reply preview", false)
		}
		_, err := io.WriteString(state.stdout, preview.Send.HTMLBody)
		return err
	case "raw":
		messageID, err := maildomain.GenerateOutboundMessageID(preview.Send.From.Email)
		if err != nil {
			return WrapAppError(ErrorInternal, "cannot allocate a reply preview Message-ID", false, err)
		}
		return maildomain.ComposeOutboundMessage(state.stdout, previewToRequest(preview.Send), messageID, time.Now().UTC())
	default:
		return NewAppError(ErrorUsage, "--format must be text, html, or raw", false)
	}
}

func renderCompactReplyPreview(writer io.Writer, preview maildomain.ReplyPreview, showHeaders, verbose bool) error {
	renderer := NewCompactTextRenderer(writer)
	if err := renderer.Record("reply-preview", preview.ParentMessageID,
		Field("kind", preview.Kind),
		Field("thread_id", preview.ThreadID),
		Field("parent_location_id", preview.ParentLocationID),
		Field("parent_folder_id", preview.ParentFolderID),
		Field("parent_rfc_message_id", preview.ParentRFCID),
		Field("threading", fmt.Sprintf("%t", preview.Threading)),
		Field("sender_identity", preview.Send.SenderIdentityID),
		Field("from", formatAddress(preview.Send.From)),
		Field("derived_to", formatAddresses(preview.DerivedTo)),
		Field("derived_cc", formatAddresses(preview.DerivedCC)),
		Field("added_to", formatAddresses(preview.AddedTo)),
		Field("added_cc", formatAddresses(preview.AddedCC)),
		Field("added_bcc", formatAddresses(preview.AddedBCC)),
		Field("replace_to", fmt.Sprintf("%t", preview.ReplaceTo)),
		Field("replace_cc", fmt.Sprintf("%t", preview.ReplaceCC)),
		Field("to", formatAddresses(preview.Send.To)),
		Field("cc", formatAddresses(preview.Send.CC)),
		Field("bcc", formatAddresses(preview.Send.BCC)),
		Field("subject", preview.Send.Subject),
		Field("body_parts", preview.Send.BodyParts),
		Field("attachments", preview.Send.AttachmentCount),
		Field("request_hash", preview.Send.RequestHash),
		Field("html_bytes", verboseValue(verbose, len(preview.Send.HTMLBody))),
	); err != nil {
		return err
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
	if preview.Send.TextBody != "" {
		if _, err := fmt.Fprintf(writer, "\ntext-body\n%s\n", preview.Send.TextBody); err != nil {
			return err
		}
	}
	return renderer.Summary("reply-safety",
		Field("dry_run", "true"), Field("smtp_contacted", "false"), Field("attempt_created", "false"),
		Field("next", "review the explicit parent and resolved recipients, then rerun without --dry-run and use --yes when non-interactive"),
	)
}

func renderReplyOutcome(renderer *CompactTextRenderer, attempt maildomain.OutboundAttempt, reused bool) error {
	return renderer.Record("mail-reply", attempt.ID,
		Field("kind", attempt.Kind),
		Field("sender_identity", attempt.SenderIdentityID),
		Field("parent_message_id", attempt.ParentMessageID),
		Field("parent_location_id", attempt.ParentLocationID),
		Field("status", attempt.Status),
		Field("message_id", attempt.MessageID),
		Field("thread_id", attempt.ThreadID),
		Field("reconciliation", attempt.Reconciliation),
		Field("answer_reconciliation", attempt.AnswerReconciliation),
		Field("answer_error", attempt.AnswerErrorCode),
		Field("error_code", attempt.ErrorCode),
		Field("reused", fmt.Sprintf("%t", reused)),
	)
}
