package cli

import (
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

func newMailMessageCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "message",
		Short:   "Read complete local message content by stable ID",
		Args:    cobra.NoArgs,
		Example: "  icloud mail message show msg_…\n  icloud mail message raw msg_…\n  icloud mail message export msg_… --output message.eml",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	command.AddCommand(newMailMessageShowCommand(state))
	command.AddCommand(newMailMessageRawCommand(state))
	command.AddCommand(newMailMessageExportCommand(state))
	return command
}

func newMailMessageShowCommand(state *executionState) *cobra.Command {
	var options OutputOptions
	command := &cobra.Command{
		Use:     "show <message-id>",
		Short:   "Show bounded metadata or explicitly requested complete content",
		Args:    cobra.ExactArgs(1),
		Example: "  icloud mail message show msg_…\n  icloud mail message show msg_… --full\n  icloud mail message show msg_… --format html --body-only\n  icloud mail message show msg_… --headers --json",
		RunE: func(cmd *cobra.Command, args []string) error {
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
			accountValue, message, source, schemaVersion, err := state.mail.MailMessage(
				cmd.Context(), state.global.Home, state.global.Account, args[0],
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
	return command
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
	var force bool
	command := &cobra.Command{
		Use:     "export <message-id>",
		Short:   "Export the exact locally stored RFC822 message atomically",
		Args:    cobra.ExactArgs(1),
		Example: "  icloud mail message export msg_… --output message.eml\n  icloud mail message export msg_… --output - > message.eml",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(output) == "" {
				return NewAppError(ErrorUsage, "--output is required", false)
			}
			_, message, source, _, err := state.mail.MailMessage(cmd.Context(), state.global.Home, state.global.Account, args[0])
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
			return NewCompactTextRenderer(state.stdout).Record("message-export", message.ID,
				Field("bytes", rawMessageSize(message)),
			)
		},
	}
	command.Flags().StringVar(&output, "output", "", "Destination path, or - for stdout")
	command.Flags().BoolVar(&force, "force", false, "Replace an existing regular file")
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
		Field("locations", len(message.Locations)),
		Field("folders", messageFolderIDs(message)),
		Field("attachments", message.AttachmentCount),
		Field("content", contentKinds),
		Field("raw", true),
		Field("parts", partCount),
		Field("diagnostics", diagnostics),
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
