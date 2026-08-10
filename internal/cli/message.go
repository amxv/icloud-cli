package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

const maxJSONMessageContentBytes = 8 << 20

type messageContentData struct {
	TextAvailable    bool                         `json:"text_available"`
	HTMLAvailable    bool                         `json:"html_available"`
	RawAvailable     bool                         `json:"raw_available"`
	HeadersAvailable bool                         `json:"headers_available"`
	PartsAvailable   bool                         `json:"parts_available"`
	PartCount        int                          `json:"part_count"`
	ParsedComplete   bool                         `json:"parsed_complete"`
	Diagnostics      []maildomain.ParseDiagnostic `json:"diagnostics,omitempty"`
	Headers          []maildomain.HeaderField     `json:"headers,omitempty"`
	Parts            []maildomain.MIMEPart        `json:"parts,omitempty"`
	TextBody         *string                      `json:"text_body,omitempty"`
	HTMLBody         *string                      `json:"html_body,omitempty"`
	RawBase64        *string                      `json:"raw_base64,omitempty"`
}

type mailMessageData struct {
	AccountID     string             `json:"account_id"`
	Message       publicMessage      `json:"message"`
	Content       messageContentData `json:"content"`
	SchemaVersion int64              `json:"schema_version"`
}

type remoteMessageData struct {
	AccountID     string                     `json:"account_id"`
	Message       maildomain.RemoteSearchHit `json:"message"`
	SchemaVersion int64                      `json:"schema_version"`
}

type messageImportService interface {
	MailMessageImportPreview(context.Context, string, string, string, maildomain.MessageImportRequest) (account.Account, folder.Folder, maildomain.MessageImportPlan, int64, error)
	MailMessageImport(context.Context, string, string, string, maildomain.MessageImportRequest) (account.Account, folder.Folder, maildomain.MessageImportPlan, maildomain.MessageImport, maildomain.Message, int64, error)
}

type mailMessageImportPreviewData struct {
	AccountID     string                       `json:"account_id"`
	Plan          maildomain.MessageImportPlan `json:"plan"`
	SchemaVersion int64                        `json:"schema_version"`
}

type mailMessageImportData struct {
	AccountID     string                   `json:"account_id"`
	FolderID      string                   `json:"folder_id"`
	Import        maildomain.MessageImport `json:"import"`
	Message       publicMessage            `json:"message"`
	SchemaVersion int64                    `json:"schema_version"`
}

func newMailMessageCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "message",
		Short:   "Read, export, or deliberately import complete RFC messages",
		Args:    cobra.NoArgs,
		Example: "  icloud mail message show msg_…\n  icloud mail message raw msg_…\n  icloud mail message export msg_… --output message.eml\n  icloud mail message import message.eml --to inbox --dry-run",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	command.AddCommand(newMailMessageShowCommand(state))
	command.AddCommand(newMailMessageRawCommand(state))
	command.AddCommand(newMailMessageExportCommand(state))
	command.AddCommand(newMailMessageImportCommand(state))
	return command
}

func newMailMessageImportCommand(state *executionState) *cobra.Command {
	var destination, internalDateText, idempotencyKey string
	var flags []string
	var dryRun, yes, jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "import <file.eml>",
		Short:   "Append one validated RFC message and ingest the exact provider copy",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail message import message.eml --to inbox --dry-run\n  icloud mail message import message.eml --to Archive --flag seen --idempotency-key import-01 --yes\n  icloud mail message import --input-json request.json --dry-run --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun && yes {
				return NewAppError(ErrorUsage, "--dry-run and --yes cannot be combined", false)
			}
			service, ok := state.mail.(messageImportService)
			if !ok {
				return NewAppError(ErrorConfiguration, "message import is unavailable", false)
			}
			sourcePath := ""
			if len(args) == 1 {
				sourcePath = args[0]
			}
			request := maildomain.MessageImportRequest{Folder: destination, Flags: append([]string(nil), flags...), IdempotencyKey: idempotencyKey}
			if strings.TrimSpace(internalDateText) != "" {
				parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(internalDateText))
				if err != nil {
					return NewAppError(ErrorUsage, "--internal-date must be an RFC3339 timestamp", false)
				}
				parsed = parsed.UTC()
				request.InternalDate = &parsed
			}
			var jsonRequest messageImportJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest, "to", "internal-date", "flag", "idempotency-key"); err != nil {
				return err
			} else if used {
				sourcePath = jsonRequest.File
				request = maildomain.MessageImportRequest{
					Folder: jsonRequest.To, InternalDate: jsonRequest.InternalDate,
					Flags: append([]string(nil), jsonRequest.Flags...), IdempotencyKey: jsonRequest.IdempotencyKey,
				}
			}
			if strings.TrimSpace(sourcePath) == "" {
				return NewAppError(ErrorUsage, "one .eml source file is required", false)
			}
			if dryRun {
				accountValue, _, plan, schemaVersion, err := service.MailMessageImportPreview(cmd.Context(), state.global.Home, state.global.Account, sourcePath, request)
				if err != nil {
					return err
				}
				if jsonOutput {
					return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailMessageImportPreviewData{
						AccountID: accountValue.ID, Plan: plan, SchemaVersion: schemaVersion,
					}, Meta: EnvelopeMeta{RequestID: state.requestID}})
				}
				return renderMessageImportPreview(state.stdout, plan, true)
			}
			if strings.TrimSpace(request.IdempotencyKey) == "" {
				return NewAppError(ErrorUsage, "message import requires --idempotency-key", false)
			}
			interactive := isTerminalReader(state.stdin) && !state.global.NoInput
			if !yes && !interactive {
				return NewAppError(ErrorSafety, "non-interactive message import requires --yes", false)
			}
			if !yes {
				_, _, plan, _, err := service.MailMessageImportPreview(cmd.Context(), state.global.Home, state.global.Account, sourcePath, request)
				if err != nil {
					return err
				}
				if err := renderMessageImportPreview(state.stdout, plan, false); err != nil {
					return err
				}
				confirmed, err := confirmMessageImport(state.stdin, state.stderr, plan.FolderName)
				if err != nil {
					return err
				}
				if !confirmed {
					return NewAppError(ErrorSafety, "message import canceled before creating intent or contacting IMAP", false)
				}
			}
			accountValue, folderValue, _, importValue, message, schemaVersion, err := service.MailMessageImport(cmd.Context(), state.global.Home, state.global.Account, sourcePath, request)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailMessageImportData{
					AccountID: accountValue.ID, FolderID: folderValue.ID, Import: importValue,
					Message: sanitizeMessage(message), SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("message-import", importValue.ID,
				Field("message", message.ID), Field("folder", folderValue.Name), Field("folder_id", folderValue.ID),
				Field("status", importValue.Status), Field("provider_method", importValue.ProviderMethod),
				Field("uid_validity", importValue.ProviderUIDValidity), Field("uid", importValue.ProviderUID),
				Field("reconciliation_required", importValue.ReconciliationRequired),
			)
		},
	}
	command.Flags().StringVar(&destination, "to", "", "Current selectable destination folder kind, ID, name, or wire name")
	command.Flags().StringVar(&internalDateText, "internal-date", "", "Provider INTERNALDATE as RFC3339; defaults to the message Date header or current time")
	command.Flags().StringArrayVar(&flags, "flag", nil, "Initial provider flag: seen, flagged, or answered; repeat as needed")
	command.Flags().StringVar(&idempotencyKey, "idempotency-key", "", idempotencyKeyHelp)
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Validate and review the exact source locally without creating import intent or contacting IMAP")
	command.Flags().BoolVar(&yes, "yes", false, "Confirm an intentional non-interactive provider APPEND")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func renderMessageImportPreview(writer io.Writer, plan maildomain.MessageImportPlan, dryRun bool) error {
	return NewCompactTextRenderer(writer).Summary("message-import-review",
		Field("folder", plan.FolderName), Field("folder_id", plan.FolderID),
		Field("raw_bytes", plan.RawByteSize), Field("raw_sha256", plan.RawContentHash),
		Field("rfc_message_id", plan.RFCMessageID), Field("subject", Excerpt(plan.Subject, DefaultExcerptRunes)),
		Field("attachments", plan.AttachmentCount), Field("flags", plan.Flags), Field("internal_date", plan.InternalDate),
		Field("request_hash", plan.RequestHash), Field("dry_run", dryRun),
		Field("imap_contacted", "false"), Field("intent_created", "false"),
	)
}

func confirmMessageImport(reader io.Reader, stderr io.Writer, folderName string) (bool, error) {
	if _, err := fmt.Fprintf(stderr, "Append the reviewed RFC message to %s and ingest that exact provider copy? [y/N]: ", folderName); err != nil {
		return false, WrapAppError(ErrorInternal, "cannot write the message import confirmation prompt", false, err)
	}
	line, err := bufio.NewReader(io.LimitReader(reader, 64)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, WrapAppError(ErrorConfiguration, "cannot read message import confirmation", false, err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func newMailMessageShowCommand(state *executionState) *cobra.Command {
	var options OutputOptions
	var remote bool
	var cache bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "show <message-id>",
		Short:   "Show bounded metadata or explicitly requested complete content",
		Args:    exactReadArgsOrJSONInput(&input, 1),
		Example: "  icloud mail message show msg_…\n  icloud mail message show rem_… --remote\n  icloud mail message show rem_… --remote --cache --full\n  icloud mail message show msg_… --format html --body-only\n  icloud mail message show msg_… --headers --json\n  icloud mail message show --input-json request.json --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			messageID := ""
			if len(args) == 1 {
				messageID = args[0]
			}
			request := messageShowReadJSONRequest{MessageID: messageID, Format: "compact"}
			if used, err := readJSONInput(cmd, state, input, &request,
				"remote", "cache", "full", "verbose", "format", "headers", "body-only", "metadata-only"); err != nil {
				return err
			} else if used {
				messageID = request.MessageID
				remote = request.Remote
				cache = request.Cache
				options = request.outputOptions(options.JSON)
			}
			if strings.TrimSpace(messageID) == "" {
				return NewAppError(ErrorUsage, "message_id is required", false)
			}
			if err := ValidateOutputOptions(options, OutputSupport{
				JSON: true, Full: true, Verbose: true, Text: true, HTML: true, Raw: true,
				Headers: true, BodyOnly: true, MetadataOnly: true,
			}); err != nil {
				return err
			}
			format, err := effectiveMessageFormat(options)
			if err != nil {
				return err
			}
			if cache && !remote {
				return NewAppError(ErrorUsage, "--cache requires --remote", false)
			}
			if remote && !cache {
				if remoteContentExpansionRequested(options, format) {
					return NewAppError(ErrorUsage, "remote content expansion requires --cache so the verified message can use the normal local read path", false)
				}
				accountValue, hit, schemaVersion, err := state.mail.MailRemoteSummary(
					cmd.Context(), state.global.Home, state.global.Account, messageID,
				)
				if err != nil {
					return err
				}
				if options.JSON {
					return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: remoteMessageData{
						AccountID: accountValue.ID, Message: hit, SchemaVersion: schemaVersion,
					}, Meta: EnvelopeMeta{RequestID: state.requestID}})
				}
				return renderRemoteMessageSummary(NewCompactTextRenderer(state.stdout), hit)
			}
			if remote {
				_, outcomes, _, err := state.mail.MailHydrate(cmd.Context(), state.global.Home, state.global.Account, []string{messageID})
				if err != nil {
					return err
				}
				if len(outcomes) != 1 || strings.TrimSpace(outcomes[0].MessageID) == "" {
					return NewAppError(ErrorInternal, "remote message hydration returned no stable local message ID", false)
				}
				messageID = outcomes[0].MessageID
			}
			accountValue, message, source, schemaVersion, err := state.mail.MailMessage(
				cmd.Context(), state.global.Home, state.global.Account, messageID,
			)
			if err != nil {
				return err
			}
			if options.JSON {
				content, contentErr := buildJSONMessageContent(cmd.Context(), message, source, options, format)
				if contentErr != nil {
					return contentErr
				}
				return WriteJSON(state.stdout, OutputEnvelope{
					OK: true,
					Data: mailMessageData{
						AccountID: accountValue.ID, Message: sanitizeMessage(message),
						Content: content, SchemaVersion: schemaVersion,
					},
					Meta: EnvelopeMeta{RequestID: state.requestID},
				})
			}
			if format == maildomain.ContentRaw {
				if err := source.WriteRaw(cmd.Context(), state.stdout); err != nil {
					return messageContentError(err)
				}
				return nil
			}
			wroteMetadata := false
			if !options.BodyOnly {
				renderer := NewCompactTextRenderer(state.stdout)
				if err := renderMessageShow(renderer, accountValue.ID, message, options.MetadataOnly); err != nil {
					return err
				}
				if options.Verbose && message.Content != nil {
					for _, part := range message.Content.Parts {
						if err := renderMIMEPart(renderer, part); err != nil {
							return err
						}
					}
				}
				wroteMetadata = true
			}
			if options.Headers {
				if err := writeHeaderSection(state.stdout, publicHeaders(message.Content), wroteMetadata); err != nil {
					return err
				}
				wroteMetadata = true
			}
			if format == maildomain.ContentText || format == maildomain.ContentHTML {
				if !options.BodyOnly {
					if wroteMetadata {
						if _, err := io.WriteString(state.stdout, "\n"); err != nil {
							return err
						}
					}
					if _, err := io.WriteString(state.stdout, "body\n"); err != nil {
						return err
					}
				}
				if err := source.WriteBody(cmd.Context(), format, state.stdout); err != nil {
					return messageContentError(err)
				}
			}
			return nil
		},
	}
	command.Flags().BoolVar(&options.JSON, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&options.Full, "full", false, "Include the complete decoded text body")
	command.Flags().BoolVar(&options.Verbose, "verbose", false, "Include MIME part metadata and parse diagnostics")
	command.Flags().StringVar(&options.Format, "format", "compact", "Content format: compact, text, html, or raw")
	command.Flags().BoolVar(&options.Headers, "headers", false, "Include decoded headers (BCC remains omitted)")
	command.Flags().BoolVar(&options.BodyOnly, "body-only", false, "Write only the selected body representation")
	command.Flags().BoolVar(&options.MetadataOnly, "metadata-only", false, "Show metadata without a decoded excerpt or content")
	command.Flags().BoolVar(&remote, "remote", false, "Treat the argument as a stale-safe rem_… provider reference")
	command.Flags().BoolVar(&cache, "cache", false, "Persist the referenced remote message through the normal local ingestion path")
	bindReadJSONInput(command, &input)
	return command
}

func remoteContentExpansionRequested(options OutputOptions, format maildomain.ContentFormat) bool {
	return options.Full || options.Verbose || options.Headers || options.BodyOnly || format != ""
}

func renderRemoteMessageSummary(renderer *CompactTextRenderer, hit maildomain.RemoteSearchHit) error {
	state := "read"
	if !hit.Flags.Seen {
		state = "unread"
	}
	return renderer.Record("remote-message", hit.Reference,
		Field("folder", hit.FolderName),
		Field("folder_id", hit.FolderID),
		Field("uid_validity", hit.UIDValidity),
		Field("uid", hit.UID),
		Field("from", formatAddress(hit.From)),
		Field("to", formatAddresses(hit.To)),
		Field("cc", formatAddresses(hit.CC)),
		Field("subject", hit.Subject),
		Field("at", hit.InternalDate),
		Field("state", state),
		Field("flagged", hit.Flags.Flagged),
		Field("cached_message", hit.CachedMessageID),
		Field("excerpt", Excerpt(hit.Snippet, DefaultExcerptRunes)),
	)
}

func newMailMessageRawCommand(state *executionState) *cobra.Command {
	return &cobra.Command{
		Use:     "raw <message-id>",
		Short:   "Stream the exact locally stored RFC822 message to stdout",
		Args:    cobra.ExactArgs(1),
		Example: "  icloud mail message raw msg_… > message.eml",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _, source, _, err := state.mail.MailMessage(cmd.Context(), state.global.Home, state.global.Account, args[0])
			if err != nil {
				return err
			}
			if err := source.WriteRaw(cmd.Context(), state.stdout); err != nil {
				return messageContentError(err)
			}
			return nil
		},
	}
}

func newMailMessageExportCommand(state *executionState) *cobra.Command {
	var output string
	var force, jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "export <message-id>",
		Short:   "Export the exact locally stored RFC822 message atomically",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail message export msg_… --output message.eml\n  icloud mail message export msg_… --output - > message.eml",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			messageID := ""
			if len(args) == 1 {
				messageID = args[0]
			}
			var jsonRequest messageExportJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest, "output", "force"); err != nil {
				return err
			} else if used {
				messageID, output, force = jsonRequest.MessageID, jsonRequest.Output, jsonRequest.Force
			}
			if strings.TrimSpace(output) == "" {
				return NewAppError(ErrorUsage, "--output is required", false)
			}
			if output == "-" && jsonOutput {
				return NewAppError(ErrorUsage, "--json cannot be combined with --output -", false)
			}
			_, message, source, _, err := state.mail.MailMessage(cmd.Context(), state.global.Home, state.global.Account, messageID)
			if err != nil {
				return err
			}
			if output == "-" {
				if err := source.WriteRaw(cmd.Context(), state.stdout); err != nil {
					return messageContentError(err)
				}
				return nil
			}
			if err := exportRawMessage(cmd.Context(), source, output, force); err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: map[string]any{"message_id": message.ID, "bytes": rawMessageSize(message), "saved": true}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("message-export", message.ID,
				Field("bytes", rawMessageSize(message)),
			)
		},
	}
	command.Flags().StringVar(&output, "output", "", "Destination path, or - for stdout")
	command.Flags().BoolVar(&force, "force", false, "Replace an existing regular file")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope after file export")
	bindJSONInput(command, &input)
	return command
}

func effectiveMessageFormat(options OutputOptions) (maildomain.ContentFormat, error) {
	format := strings.ToLower(strings.TrimSpace(options.Format))
	if format == "" || format == "compact" {
		if options.Full || options.BodyOnly {
			return maildomain.ContentText, nil
		}
		return "", nil
	}
	if options.Full && format != "text" {
		return "", NewAppError(ErrorUsage, "--full can only be combined with --format text", false)
	}
	if format == "raw" && options.Verbose && !options.JSON {
		return "", NewAppError(ErrorUsage, "plain raw output cannot be combined with --verbose", false)
	}
	return maildomain.ContentFormat(format), nil
}

func renderMessageShow(renderer *CompactTextRenderer, accountID string, message maildomain.Message, metadataOnly bool) error {
	contentKinds := make([]string, 0, 2)
	partCount := 0
	diagnostics := 0
	unsubscribe := ""
	if message.Content != nil {
		if len(message.Content.TextPaths) > 0 || len(message.Content.HTMLPaths) > 0 {
			if len(message.Content.TextPaths) > 0 {
				contentKinds = append(contentKinds, "text")
			}
			if len(message.Content.HTMLPaths) > 0 {
				contentKinds = append(contentKinds, "html")
			}
		}
		partCount = len(message.Content.Parts)
		diagnostics = len(message.Content.Diagnostics)
	}
	if message.Unsubscribe != nil && message.Unsubscribe.Available {
		parts := make([]string, 0, len(message.Unsubscribe.Methods))
		for _, method := range message.Unsubscribe.Methods {
			parts = append(parts, method.ID+":"+method.Host)
		}
		unsubscribe = strings.Join(parts, ",")
	}
	fields := []CompactField{
		Field("account", accountID),
		Field("from", formatAddress(message.From)),
		Field("to", formatAddresses(message.To)),
		Field("cc", formatAddresses(message.CC)),
		Field("subject", message.Subject),
		Field("at", message.SortAt),
		Field("date_source", message.DateSource),
		Field("state", messageReadState(message)),
		Field("flagged", message.Flagged),
		Field("answered", message.Answered),
		Field("replied", message.Replied),
		Field("reply_state", message.ReplyState),
		Field("forwarded", message.Forwarded),
		Field("locations", len(message.Locations)),
		Field("folders", messageFolderIDs(message)),
		Field("attachments", message.AttachmentCount),
		Field("content", contentKinds),
		Field("raw", true),
		Field("parts", partCount),
		Field("diagnostics", diagnostics),
		Field("unsubscribe", unsubscribe),
	}
	if !metadataOnly {
		fields = append(fields, Field("excerpt", Excerpt(message.Snippet, DefaultExcerptRunes)))
		if len(contentKinds) > 0 {
			fields = append(fields, Field("hint", "use --full, --format html, --headers, or message raw"))
		}
	}
	return renderer.Record("message", message.ID, fields...)
}

func messageReadState(message maildomain.Message) string {
	if message.Unread {
		return "unread"
	}
	return "read"
}

func renderMIMEPart(renderer *CompactTextRenderer, part maildomain.MIMEPart) error {
	partID := part.ID
	if partID == "" {
		partID = part.Path
	}
	return renderer.Record("part", partID,
		Field("ordinal", fmt.Sprintf("%d", part.Ordinal)),
		Field("path", part.Path),
		Field("parent", part.ParentPath),
		Field("type", part.MediaType),
		Field("charset", part.Charset),
		Field("encoding", part.TransferEncoding),
		Field("disposition", part.Disposition),
		Field("filename", part.Filename),
		Field("content_id", part.ContentID),
		Field("inline", part.Inline),
		Field("attachment", part.Attachment),
		Field("embedded", part.EmbeddedMessage),
		Field("bytes", part.DecodedSize),
		Field("diagnostics", part.Diagnostics),
	)
}

func buildJSONMessageContent(ctx context.Context, message maildomain.Message, source maildomain.ContentSource, options OutputOptions, format maildomain.ContentFormat) (messageContentData, error) {
	content := messageContentData{RawAvailable: source != nil || messageHasRawSource(message)}
	if message.Content != nil {
		content.TextAvailable = len(message.Content.TextPaths) > 0 || len(message.Content.HTMLPaths) > 0
		content.HTMLAvailable = len(message.Content.HTMLPaths) > 0
		content.HeadersAvailable = len(message.Content.Headers) > 0
		content.PartsAvailable = len(message.Content.Parts) > 0
		content.PartCount = len(message.Content.Parts)
		content.ParsedComplete = len(message.Content.Diagnostics) == 0
		content.Diagnostics = append([]maildomain.ParseDiagnostic(nil), message.Content.Diagnostics...)
		if options.Headers {
			content.Headers = publicHeaders(message.Content)
		}
		if options.Verbose || options.MetadataOnly {
			content.Parts = append([]maildomain.MIMEPart(nil), message.Content.Parts...)
		}
	}
	if format == "" {
		return content, nil
	}
	if source == nil {
		return messageContentData{}, NewAppError(ErrorUnavailable, "requested message content is not available locally", false)
	}
	buffer := &boundedContentBuffer{max: maxJSONMessageContentBytes}
	switch format {
	case maildomain.ContentRaw:
		if err := source.WriteRaw(ctx, buffer); err != nil {
			return messageContentData{}, messageContentError(err)
		}
		encoded := base64.StdEncoding.EncodeToString(buffer.Bytes())
		content.RawBase64 = &encoded
	case maildomain.ContentText, maildomain.ContentHTML:
		if err := source.WriteBody(ctx, format, buffer); err != nil {
			return messageContentData{}, messageContentError(err)
		}
		value := buffer.String()
		if format == maildomain.ContentHTML {
			content.HTMLBody = &value
		} else {
			content.TextBody = &value
		}
	}
	return content, nil
}

func messageHasRawSource(message maildomain.Message) bool {
	for _, location := range message.Locations {
		if location.Present && location.RawStorageKey != "" && location.RawContentHash != "" && location.RawByteSize >= 0 {
			return true
		}
	}
	if message.LocalRaw != nil && message.LocalRaw.StorageKey != "" && message.LocalRaw.ContentHash != "" && message.LocalRaw.ByteSize >= 0 {
		return true
	}
	return false
}

func publicHeaders(content *maildomain.NormalizedContent) []maildomain.HeaderField {
	if content == nil {
		return nil
	}
	result := make([]maildomain.HeaderField, 0, len(content.Headers))
	for _, field := range content.Headers {
		if strings.EqualFold(strings.TrimSpace(field.Key), "Bcc") {
			continue
		}
		result = append(result, field)
	}
	return result
}

func writeHeaderSection(writer io.Writer, headers []maildomain.HeaderField, separated bool) error {
	if separated {
		if _, err := io.WriteString(writer, "\n"); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(writer, "headers\n"); err != nil {
		return err
	}
	for _, field := range headers {
		if _, err := fmt.Fprintf(writer, "%s: %s\n", field.Key, field.Value); err != nil {
			return err
		}
	}
	return nil
}

func formatAddress(address maildomain.Address) string {
	if strings.TrimSpace(address.Email) == "" {
		return ""
	}
	if strings.TrimSpace(address.DisplayName) == "" {
		return address.Email
	}
	return fmt.Sprintf("%s <%s>", address.DisplayName, address.Email)
}

func formatAddresses(addresses []maildomain.Address) string {
	values := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if value := formatAddress(address); value != "" {
			values = append(values, value)
		}
	}
	return strings.Join(values, ",")
}

func messageFolderIDs(message maildomain.Message) []string {
	set := map[string]struct{}{}
	for _, location := range message.Locations {
		if location.Present && strings.TrimSpace(location.FolderID) != "" {
			set[location.FolderID] = struct{}{}
		}
	}
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func rawMessageSize(message maildomain.Message) int64 {
	for _, location := range message.Locations {
		if location.Present && location.RawByteSize > 0 {
			return location.RawByteSize
		}
	}
	if message.LocalRaw != nil {
		return message.LocalRaw.ByteSize
	}
	return 0
}

func exportRawMessage(ctx context.Context, source maildomain.ContentSource, output string, force bool) error {
	output = filepath.Clean(strings.TrimSpace(output))
	if output == "" || output == "." {
		return NewAppError(ErrorUsage, "invalid export destination", false)
	}
	info, err := os.Stat(output)
	if err == nil {
		if info.IsDir() || !info.Mode().IsRegular() {
			return NewAppError(ErrorSafety, "export destination is not a regular file", false)
		}
		if !force {
			return NewAppError(ErrorConflict, "export destination already exists; use --force to replace it", false)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return WrapAppError(ErrorConfiguration, "cannot inspect export destination", false, err)
	}
	directory := filepath.Dir(output)
	temporary, err := os.CreateTemp(directory, ".icloud-message-*.tmp")
	if err != nil {
		return WrapAppError(ErrorConfiguration, "cannot create the export file", false, err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o600); err != nil {
		cleanup()
		return WrapAppError(ErrorConfiguration, "cannot secure the export file", false, err)
	}
	if err := source.WriteRaw(ctx, temporary); err != nil {
		cleanup()
		return messageContentError(err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return WrapAppError(ErrorInternal, "cannot flush the export file", true, err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return WrapAppError(ErrorInternal, "cannot close the export file", true, err)
	}
	if err := installExportFile(temporaryPath, output, force); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

func installExportFile(temporaryPath, output string, force bool) error {
	if !force {
		if err := os.Link(temporaryPath, output); err != nil {
			if errors.Is(err, os.ErrExist) {
				return NewAppError(ErrorConflict, "export destination already exists; use --force to replace it", false)
			}
			return WrapAppError(ErrorConfiguration, "cannot install the export file", false, err)
		}
		if err := os.Remove(temporaryPath); err != nil {
			return WrapAppError(ErrorInternal, "export installed but temporary-file cleanup failed", true, err)
		}
		return nil
	}
	if err := os.Rename(temporaryPath, output); err == nil {
		return nil
	}
	backup, err := os.CreateTemp(filepath.Dir(output), ".icloud-message-backup-*.tmp")
	if err != nil {
		return WrapAppError(ErrorConfiguration, "cannot prepare export replacement", false, err)
	}
	backupPath := backup.Name()
	if err := backup.Close(); err != nil {
		_ = os.Remove(backupPath)
		return WrapAppError(ErrorInternal, "cannot prepare export replacement", true, err)
	}
	if err := os.Remove(backupPath); err != nil {
		return WrapAppError(ErrorInternal, "cannot prepare export replacement", true, err)
	}
	if err := os.Rename(output, backupPath); err != nil {
		return WrapAppError(ErrorConfiguration, "cannot preserve the existing export file", false, err)
	}
	if err := os.Rename(temporaryPath, output); err != nil {
		if restoreErr := os.Rename(backupPath, output); restoreErr != nil {
			return WrapAppError(ErrorInternal, "cannot install the export file or restore the previous file", false, errors.Join(err, restoreErr))
		}
		return WrapAppError(ErrorConfiguration, "cannot install the export file", false, err)
	}
	if err := os.Remove(backupPath); err != nil {
		return WrapAppError(ErrorInternal, "export replaced but previous-file cleanup failed", true, err)
	}
	return nil
}

func messageContentError(err error) error {
	if errors.Is(err, errJSONContentTooLarge) {
		return WrapAppError(ErrorSafety, "requested JSON content exceeds the 8 MiB limit; use plain streaming output", false, err)
	}
	if errors.Is(err, maildomain.ErrContentLimit) {
		return WrapAppError(ErrorConfiguration, "local content exceeds the configured inbound limit; raise ICLOUD_MAX_RAW_MESSAGE_BYTES or ICLOUD_MAX_ATTACHMENT_BYTES as appropriate and retry", false, err)
	}
	if errors.Is(err, maildomain.ErrContentCorrupt) {
		return WrapAppError(ErrorUnavailable, "local raw message content is corrupt", false, err)
	}
	if errors.Is(err, maildomain.ErrContentUnavailable) {
		return WrapAppError(ErrorUnavailable, "local raw message content is unavailable", false, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return WrapAppError(ErrorUnavailable, "message content read timed out", true, err)
	}
	if errors.Is(err, context.Canceled) {
		return WrapAppError(ErrorUnavailable, "message content read was canceled", false, err)
	}
	return WrapAppError(ErrorUnavailable, "cannot read the local message content", false, err)
}

var errJSONContentTooLarge = errors.New("JSON message content exceeds the supported limit")

type boundedContentBuffer struct {
	bytes.Buffer
	max int
}

func (buffer *boundedContentBuffer) Write(value []byte) (int, error) {
	if buffer.Len()+len(value) > buffer.max {
		return 0, errJSONContentTooLarge
	}
	return buffer.Buffer.Write(value)
}

func (buffer *boundedContentBuffer) WriteString(value string) (int, error) {
	if buffer.Len()+len(value) > buffer.max {
		return 0, errJSONContentTooLarge
	}
	return buffer.Buffer.WriteString(value)
}
