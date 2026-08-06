package mail

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"

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
	if writer == nil {
		return errors.New("outbound message writer is required")
	}
	request, err := NormalizeSendRequest(request)
	if err != nil {
		return err
	}
	messageID = strings.Trim(strings.TrimSpace(messageID), "<>")
	if messageID == "" || sentAt.IsZero() {
		return errors.New("outbound Message-ID and sent time are required")
	}
	var header gomail.Header
	header.SetAddressList("From", []*gomail.Address{toGoMailAddress(request.From)})
	header.SetAddressList("To", toGoMailAddresses(request.To))
	header.SetAddressList("Cc", toGoMailAddresses(request.CC))
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
