package mail

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"

	message "github.com/emersion/go-message"
	gomail "github.com/emersion/go-message/mail"
)

func GenerateOutboundMessageID(fromEmail string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	domain := "icloud.local"
	if _, suffix, found := strings.Cut(strings.TrimSpace(fromEmail), "@"); found && strings.TrimSpace(suffix) != "" {
		domain = strings.ToLower(strings.TrimSpace(suffix))
	}
	return hex.EncodeToString(random) + "@" + domain, nil
}

func ComposeOutboundMessage(writer io.Writer, request SendRequest, messageID string, sentAt time.Time) error {
	request, err := NormalizeSendRequest(request)
	if err != nil {
		return err
	}
	return composeMessage(writer, request, messageID, sentAt, false)
}

func ComposeDraftMessage(writer io.Writer, request SendRequest, messageID string, savedAt time.Time) error {
	request, err := NormalizeDraftRequest(request)
	if err != nil {
		return err
	}
	return composeMessage(writer, request, messageID, savedAt, true)
}

func composeMessage(writer io.Writer, request SendRequest, messageID string, sentAt time.Time, includeBCC bool) error {
	if writer == nil {
		return errors.New("outbound message writer is required")
	}
	messageID = strings.Trim(strings.TrimSpace(messageID), "<>")
	if messageID == "" || sentAt.IsZero() {
		return errors.New("outbound Message-ID and sent time are required")
	}
	var header gomail.Header
	header.SetAddressList("From", []*gomail.Address{toGoMailAddress(request.From)})
	if len(request.To) > 0 {
		header.SetAddressList("To", toGoMailAddresses(request.To))
	}
	if len(request.CC) > 0 {
		header.SetAddressList("Cc", toGoMailAddresses(request.CC))
	}
	if includeBCC && len(request.BCC) > 0 {
		header.SetAddressList("Bcc", toGoMailAddresses(request.BCC))
	}
	if request.ReplyTo != nil {
		header.SetAddressList("Reply-To", []*gomail.Address{toGoMailAddress(*request.ReplyTo)})
	}
	header.SetSubject(request.Subject)
	header.SetDate(sentAt.UTC())
	header.SetMessageID(messageID)
	if request.InReplyTo != "" {
		header.SetMsgIDList("In-Reply-To", []string{request.InReplyTo})
	}
	if len(request.References) > 0 {
		header.SetMsgIDList("References", request.References)
	}
	for _, field := range request.Headers {
		header.Set(field.Key, field.Value)
	}
	if hasInlineOutboundAttachment(request.Attachments) {
		return composeMessageWithInlineAttachments(writer, header, request)
	}
	if len(request.Attachments) > 0 {
		mixed, err := gomail.CreateWriter(writer, header)
		if err != nil {
			return err
		}
		if err := writeMixedBody(mixed, request); err != nil {
			_ = mixed.Close()
			return err
		}
		for _, attachment := range request.Attachments {
			if attachment.Open == nil {
				_ = mixed.Close()
				return errors.New("outbound attachment source is unavailable")
			}
			var attachmentHeader gomail.AttachmentHeader
			attachmentHeader.SetContentType(attachment.MediaType, map[string]string{"name": attachment.Filename})
			attachmentHeader.SetFilename(attachment.Filename)
			part, err := mixed.CreateAttachment(attachmentHeader)
			if err != nil {
				_ = mixed.Close()
				return err
			}
			source, err := attachment.Open()
			if err != nil {
				_ = part.Close()
				_ = mixed.Close()
				return err
			}
			_, copyErr := io.Copy(part, source)
			closeSourceErr := source.Close()
			closePartErr := part.Close()
			if copyErr != nil {
				_ = mixed.Close()
				return copyErr
			}
			if closeSourceErr != nil {
				_ = mixed.Close()
				return closeSourceErr
			}
			if closePartErr != nil {
				_ = mixed.Close()
				return closePartErr
			}
		}
		return mixed.Close()
	}
	if request.HTMLBody == "" {
		header.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		part, err := gomail.CreateSingleInlineWriter(writer, header)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(part, normalizeCRLFBody(request.TextBody)); err != nil {
			_ = part.Close()
			return err
		}
		return part.Close()
	}
	inline, err := gomail.CreateInlineWriter(writer, header)
	if err != nil {
		return err
	}
	textHeader := gomail.InlineHeader{}
	textHeader.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
	textPart, err := inline.CreatePart(textHeader)
	if err != nil {
		_ = inline.Close()
		return err
	}
	textBody := request.TextBody
	if textBody == "" {
		textBody = stripSendHTML(request.HTMLBody)
	}
	if _, err := io.WriteString(textPart, normalizeCRLFBody(textBody)); err != nil {
		_ = textPart.Close()
		_ = inline.Close()
		return err
	}
	if err := textPart.Close(); err != nil {
		_ = inline.Close()
		return err
	}
	htmlHeader := gomail.InlineHeader{}
	htmlHeader.SetContentType("text/html", map[string]string{"charset": "utf-8"})
	htmlPart, err := inline.CreatePart(htmlHeader)
	if err != nil {
		_ = inline.Close()
		return err
	}
	if _, err := io.WriteString(htmlPart, normalizeCRLFBody(request.HTMLBody)); err != nil {
		_ = htmlPart.Close()
		_ = inline.Close()
		return err
	}
	if err := htmlPart.Close(); err != nil {
		_ = inline.Close()
		return err
	}
	return inline.Close()
}

func hasInlineOutboundAttachment(values []OutboundAttachment) bool {
	for _, value := range values {
		if value.Disposition == AttachmentDispositionInline {
			return true
		}
	}
	return false
}

func composeMessageWithInlineAttachments(writer io.Writer, header gomail.Header, request SendRequest) error {
	ordinary := make([]OutboundAttachment, 0, len(request.Attachments))
	inline := make([]OutboundAttachment, 0, len(request.Attachments))
	for _, attachment := range request.Attachments {
		if attachment.Disposition == AttachmentDispositionInline {
			inline = append(inline, attachment)
		} else {
			ordinary = append(ordinary, attachment)
		}
	}

	rootType := "multipart/related"
	if len(ordinary) > 0 {
		rootType = "multipart/mixed"
	}
	rootHeader := header.Header.Copy()
	rootHeader.SetContentType(rootType, nil)
	root, err := message.CreateWriter(writer, rootHeader)
	if err != nil {
		return err
	}
	closeRootWith := func(cause error) error {
		closeErr := root.Close()
		if cause != nil {
			return cause
		}
		return closeErr
	}

	related := root
	if len(ordinary) > 0 {
		var relatedHeader message.Header
		relatedHeader.SetContentType("multipart/related", nil)
		related, err = root.CreatePart(relatedHeader)
		if err != nil {
			return closeRootWith(err)
		}
	}
	if err := writeOutboundBodyEntity(related, request); err != nil {
		if related != root {
			_ = related.Close()
		}
		return closeRootWith(err)
	}
	for _, attachment := range inline {
		if err := writeOutboundAttachmentEntity(related, attachment); err != nil {
			if related != root {
				_ = related.Close()
			}
			return closeRootWith(err)
		}
	}
	if related != root {
		if err := related.Close(); err != nil {
			return closeRootWith(err)
		}
	}
	for _, attachment := range ordinary {
		if err := writeOutboundAttachmentEntity(root, attachment); err != nil {
			return closeRootWith(err)
		}
	}
	return root.Close()
}

func writeOutboundBodyEntity(parent *message.Writer, request SendRequest) error {
	if request.HTMLBody == "" {
		return writeOutboundTextPart(parent, "text/plain", request.TextBody)
	}
	var alternativeHeader message.Header
	alternativeHeader.SetContentType("multipart/alternative", nil)
	alternative, err := parent.CreatePart(alternativeHeader)
	if err != nil {
		return err
	}
	textBody := request.TextBody
	if textBody == "" {
		textBody = stripSendHTML(request.HTMLBody)
	}
	if err := writeOutboundTextPart(alternative, "text/plain", textBody); err != nil {
		_ = alternative.Close()
		return err
	}
	if err := writeOutboundTextPart(alternative, "text/html", request.HTMLBody); err != nil {
		_ = alternative.Close()
		return err
	}
	return alternative.Close()
}

func writeOutboundTextPart(parent *message.Writer, mediaType, body string) error {
	var header message.Header
	header.SetContentType(mediaType, map[string]string{"charset": "utf-8"})
	header.SetContentDisposition("inline", nil)
	header.Set("Content-Transfer-Encoding", "quoted-printable")
	part, err := parent.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(part, normalizeCRLFBody(body)); err != nil {
		_ = part.Close()
		return err
	}
	return part.Close()
}

func writeOutboundAttachmentEntity(parent *message.Writer, attachment OutboundAttachment) error {
	if attachment.Open == nil {
		return errors.New("outbound attachment source is unavailable")
	}
	var header message.Header
	header.SetContentType(attachment.MediaType, map[string]string{"name": attachment.Filename})
	header.SetContentDisposition(string(attachment.Disposition), map[string]string{"filename": attachment.Filename})
	header.Set("Content-Transfer-Encoding", "base64")
	if attachment.ContentID != "" {
		header.Set("Content-ID", "<"+attachment.ContentID+">")
	}
	part, err := parent.CreatePart(header)
	if err != nil {
		return err
	}
	source, err := attachment.Open()
	if err != nil {
		_ = part.Close()
		return err
	}
	_, copyErr := io.Copy(part, source)
	closeSourceErr := source.Close()
	closePartErr := part.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeSourceErr != nil {
		return closeSourceErr
	}
	return closePartErr
}

func writeMixedBody(writer *gomail.Writer, request SendRequest) error {
	if request.HTMLBody == "" {
		header := gomail.InlineHeader{}
		header.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		part, err := writer.CreateSingleInline(header)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(part, normalizeCRLFBody(request.TextBody)); err != nil {
			_ = part.Close()
			return err
		}
		return part.Close()
	}
	inline, err := writer.CreateInline()
	if err != nil {
		return err
	}
	textHeader := gomail.InlineHeader{}
	textHeader.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
	textPart, err := inline.CreatePart(textHeader)
	if err != nil {
		_ = inline.Close()
		return err
	}
	textBody := request.TextBody
	if textBody == "" {
		textBody = stripSendHTML(request.HTMLBody)
	}
	if _, err := io.WriteString(textPart, normalizeCRLFBody(textBody)); err != nil {
		_ = textPart.Close()
		_ = inline.Close()
		return err
	}
	if err := textPart.Close(); err != nil {
		_ = inline.Close()
		return err
	}
	htmlHeader := gomail.InlineHeader{}
	htmlHeader.SetContentType("text/html", map[string]string{"charset": "utf-8"})
	htmlPart, err := inline.CreatePart(htmlHeader)
	if err != nil {
		_ = inline.Close()
		return err
	}
	if _, err := io.WriteString(htmlPart, normalizeCRLFBody(request.HTMLBody)); err != nil {
		_ = htmlPart.Close()
		_ = inline.Close()
		return err
	}
	if err := htmlPart.Close(); err != nil {
		_ = inline.Close()
		return err
	}
	return inline.Close()
}

func toGoMailAddress(value Address) *gomail.Address {
	return &gomail.Address{Name: value.DisplayName, Address: value.Email}
}

func toGoMailAddresses(values []Address) []*gomail.Address {
	result := make([]*gomail.Address, 0, len(values))
	for _, value := range values {
		result = append(result, toGoMailAddress(value))
	}
	return result
}

func normalizeCRLFBody(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}

func stripSendHTML(value string) string {
	var builder strings.Builder
	inside := false
	for _, character := range value {
		switch character {
		case '<':
			inside = true
		case '>':
			inside = false
			builder.WriteByte(' ')
		default:
			if !inside {
				builder.WriteRune(character)
			}
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}
