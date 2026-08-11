package mail

import (
	"errors"
	"fmt"
	"html"
	"strings"
	"time"
)

type ForwardOriginalMode string

const (
	ForwardOriginalQuoted ForwardOriginalMode = "quoted"
	ForwardOriginalEML    ForwardOriginalMode = "eml"
	ForwardOriginalNone   ForwardOriginalMode = "none"
)

var ErrInvalidForwardRequest = errors.New("invalid forward request")

type ForwardRequest struct {
	SourceMessageID       string
	SenderIdentityID      string
	SignatureSelector     string
	SMIMEMode             SMIMEComposeMode
	SMIMEPlan             *SMIMEOutboundPlan
	OriginalMode          ForwardOriginalMode
	OriginalAttachmentIDs []string
	To                    []Address
	CC                    []Address
	BCC                   []Address
	ReplyTo               *Address
	TextBody              string
	HTMLBody              string
	Headers               []CustomHeader
	Attachments           []OutboundAttachment
	IdempotencyKey        string
	RetryOfAttemptID      string
	AuthorizeUnknownRetry bool
}

type ForwardOriginalAttachment struct {
	ID          string                `json:"id"`
	Filename    string                `json:"filename"`
	MediaType   string                `json:"media_type"`
	Disposition AttachmentDisposition `json:"disposition"`
	ContentID   string                `json:"content_id,omitempty"`
	ByteSize    int64                 `json:"byte_size"`
}

type ForwardPlan struct {
	SourceMessageID     string                      `json:"source_message_id"`
	SourceThreadID      string                      `json:"source_thread_id,omitempty"`
	OriginalMode        ForwardOriginalMode         `json:"original_mode"`
	OriginalAttachments []ForwardOriginalAttachment `json:"original_attachments,omitempty"`
	NewAttachmentCount  int                         `json:"new_attachment_count"`
	AttachedOriginalEML bool                        `json:"attached_original_eml"`
	OriginalEMLByteSize int64                       `json:"original_eml_byte_size,omitempty"`
	Send                SendRequest                 `json:"-"`
}

type ForwardPreview struct {
	SourceMessageID     string                      `json:"source_message_id"`
	SourceThreadID      string                      `json:"source_thread_id,omitempty"`
	OriginalMode        ForwardOriginalMode         `json:"original_mode"`
	OriginalAttachments []ForwardOriginalAttachment `json:"original_attachments,omitempty"`
	NewAttachmentCount  int                         `json:"new_attachment_count"`
	AttachedOriginalEML bool                        `json:"attached_original_eml"`
	OriginalEMLByteSize int64                       `json:"original_eml_byte_size,omitempty"`
	Send                SendPreview                 `json:"send"`
}

func NormalizeForwardOriginalMode(value ForwardOriginalMode) (ForwardOriginalMode, error) {
	switch ForwardOriginalMode(strings.ToLower(strings.TrimSpace(string(value)))) {
	case "", ForwardOriginalQuoted:
		return ForwardOriginalQuoted, nil
	case ForwardOriginalEML:
		return ForwardOriginalEML, nil
	case ForwardOriginalNone:
		return ForwardOriginalNone, nil
	default:
		return "", fmt.Errorf("%w: original representation must be quoted, eml, or none", ErrInvalidForwardRequest)
	}
}

func ForwardSubject(value string) string {
	value = cleanSendHeaderText(value)
	for {
		trimmed := strings.TrimSpace(value)
		lower := strings.ToLower(trimmed)
		switch {
		case strings.HasPrefix(lower, "fwd:"):
			value = strings.TrimSpace(trimmed[4:])
		case strings.HasPrefix(lower, "fw:"):
			value = strings.TrimSpace(trimmed[3:])
		default:
			value = trimmed
			if value == "" {
				return "Fwd:"
			}
			return "Fwd: " + value
		}
	}
}

func BuildForwardPlan(
	source Message,
	sourceThreadID string,
	input ForwardRequest,
	decodedOriginalText string,
	originalAttachments []OutboundAttachment,
	originalAttachmentMeta []ForwardOriginalAttachment,
	originalEML *OutboundAttachment,
) (ForwardPlan, error) {
	sourceID := strings.TrimSpace(input.SourceMessageID)
	if sourceID == "" {
		sourceID = strings.TrimSpace(source.ID)
	}
	if !strings.HasPrefix(sourceID, "msg_") || source.ID != sourceID {
		return ForwardPlan{}, fmt.Errorf("%w: one exact local source message is required", ErrInvalidForwardRequest)
	}
	mode, err := NormalizeForwardOriginalMode(input.OriginalMode)
	if err != nil {
		return ForwardPlan{}, err
	}
	to, err := normalizeAddressList(input.To)
	if err != nil {
		return ForwardPlan{}, fmt.Errorf("%w: invalid To recipient", ErrInvalidForwardRequest)
	}
	cc, err := normalizeAddressList(input.CC)
	if err != nil {
		return ForwardPlan{}, fmt.Errorf("%w: invalid Cc recipient", ErrInvalidForwardRequest)
	}
	bcc, err := normalizeAddressList(input.BCC)
	if err != nil {
		return ForwardPlan{}, fmt.Errorf("%w: invalid Bcc recipient", ErrInvalidForwardRequest)
	}
	if len(to) == 0 {
		return ForwardPlan{}, fmt.Errorf("%w: at least one To recipient is required", ErrInvalidForwardRequest)
	}

	textBody := normalizeBody(input.TextBody)
	htmlBody := normalizeBody(input.HTMLBody)
	attachments := append([]OutboundAttachment(nil), input.Attachments...)
	attachedEML := false
	var emlBytes int64
	switch mode {
	case ForwardOriginalQuoted:
		quoted := ForwardQuotedText(source, decodedOriginalText)
		textBody = joinForwardBody(textBody, quoted)
		if htmlBody != "" {
			htmlBody = joinForwardHTML(htmlBody, ForwardQuotedHTML(source, decodedOriginalText))
		}
	case ForwardOriginalEML:
		if originalEML == nil {
			return ForwardPlan{}, fmt.Errorf("%w: exact raw source is unavailable for EML forwarding", ErrInvalidForwardRequest)
		}
		attachments = append(attachments, *originalEML)
		attachedEML = true
		emlBytes = originalEML.ByteSize
		if textBody == "" && htmlBody == "" {
			textBody = "Forwarded message attached."
		}
	case ForwardOriginalNone:
		if textBody == "" && htmlBody == "" {
			return ForwardPlan{}, fmt.Errorf("%w: a text or HTML body is required when the original is omitted", ErrInvalidForwardRequest)
		}
	}
	attachments = append(attachments, originalAttachments...)
	if len(textBody) > MaxSendBodyBytes || len(htmlBody) > MaxSendBodyBytes {
		return ForwardPlan{}, fmt.Errorf("%w: forwarded body exceeds %d bytes", ErrInvalidForwardRequest, MaxSendBodyBytes)
	}

	send := SendRequest{
		Kind: OutboundKindForward, SourceMessageID: sourceID, SenderIdentityID: strings.TrimSpace(input.SenderIdentityID), SignatureSelector: strings.TrimSpace(input.SignatureSelector), SMIMEMode: input.SMIMEMode, SMIMEPlan: input.SMIMEPlan,
		To: to, CC: cc, BCC: bcc, ReplyTo: input.ReplyTo,
		Subject: ForwardSubject(source.Subject), TextBody: textBody, HTMLBody: htmlBody,
		Headers: input.Headers, Attachments: attachments,
		IdempotencyKey: input.IdempotencyKey, RetryOfAttemptID: input.RetryOfAttemptID,
		AuthorizeUnknownRetry: input.AuthorizeUnknownRetry,
	}
	return ForwardPlan{
		SourceMessageID: sourceID, SourceThreadID: strings.TrimSpace(sourceThreadID),
		OriginalMode: mode, OriginalAttachments: append([]ForwardOriginalAttachment(nil), originalAttachmentMeta...),
		NewAttachmentCount: len(input.Attachments), AttachedOriginalEML: attachedEML,
		OriginalEMLByteSize: emlBytes, Send: send,
	}, nil
}

func ForwardQuotedText(source Message, body string) string {
	var builder strings.Builder
	builder.WriteString("---------- Forwarded message ----------\n")
	writeForwardMetadata(&builder, source)
	builder.WriteByte('\n')
	body = normalizeBody(body)
	if body == "" {
		builder.WriteString("> [No decoded text body available]")
		return builder.String()
	}
	for index, line := range strings.Split(body, "\n") {
		if index > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString("> ")
		builder.WriteString(line)
	}
	return builder.String()
}

func ForwardQuotedHTML(source Message, body string) string {
	return "<hr><p><strong>Forwarded message</strong></p><pre>" +
		html.EscapeString(forwardMetadataText(source)+"\n"+normalizeBody(body)) + "</pre>"
}

func writeForwardMetadata(builder *strings.Builder, source Message) {
	builder.WriteString(forwardMetadataText(source))
}

func forwardMetadataText(source Message) string {
	lines := make([]string, 0, 5)
	if value := forwardAddress(source.From); value != "" {
		lines = append(lines, "From: "+value)
	}
	if value := forwardDate(source); value != "" {
		lines = append(lines, "Date: "+value)
	}
	if value := cleanSendHeaderText(source.Subject); value != "" {
		lines = append(lines, "Subject: "+value)
	}
	if value := forwardAddresses(source.To); value != "" {
		lines = append(lines, "To: "+value)
	}
	if value := forwardAddresses(source.CC); value != "" {
		lines = append(lines, "Cc: "+value)
	}
	return strings.Join(lines, "\n")
}

func forwardDate(source Message) string {
	for _, value := range []*time.Time{source.HeaderDate, source.InternalDate, source.SortAt} {
		if value != nil && !value.IsZero() {
			return value.UTC().Format(time.RFC1123Z)
		}
	}
	return ""
}

func forwardAddress(value Address) string {
	value.DisplayName = cleanSendHeaderText(value.DisplayName)
	value.Email = strings.TrimSpace(value.Email)
	if value.Email == "" {
		return ""
	}
	if value.DisplayName == "" {
		return value.Email
	}
	return value.DisplayName + " <" + value.Email + ">"
}

func forwardAddresses(values []Address) string {
	formatted := make([]string, 0, len(values))
	for _, value := range values {
		if item := forwardAddress(value); item != "" {
			formatted = append(formatted, item)
		}
	}
	return strings.Join(formatted, ", ")
}

func joinForwardBody(prefix, original string) string {
	prefix, original = normalizeBody(prefix), normalizeBody(original)
	if prefix == "" {
		return original
	}
	if original == "" {
		return prefix
	}
	return prefix + "\n\n" + original
}

func joinForwardHTML(prefix, original string) string {
	prefix, original = strings.TrimSpace(prefix), strings.TrimSpace(original)
	if prefix == "" {
		return original
	}
	if original == "" {
		return prefix
	}
	return prefix + original
}
