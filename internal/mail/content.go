package mail

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"strings"

	message "github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"
	gomail "github.com/emersion/go-message/mail"
)

const maxMIMEParts = 2048

var (
	ErrContentCorrupt     = errors.New("message content is corrupt")
	ErrContentUnavailable = errors.New("message content is unavailable")
	ErrAttachmentStorage  = errors.New("attachment storage failed")
)

type HeaderField struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ParseDiagnostic struct {
	Path string `json:"path,omitempty"`
	Code string `json:"code"`
}

type MIMEPart struct {
	ID               string            `json:"id,omitempty"`
	Ordinal          int               `json:"ordinal"`
	Path             string            `json:"path"`
	ParentPath       string            `json:"parent_path,omitempty"`
	MediaType        string            `json:"media_type"`
	MediaParams      map[string]string `json:"media_params,omitempty"`
	Charset          string            `json:"charset,omitempty"`
	TransferEncoding string            `json:"transfer_encoding,omitempty"`
	Disposition      string            `json:"disposition,omitempty"`
	Filename         string            `json:"filename,omitempty"`
	ContentID        string            `json:"content_id,omitempty"`
	Inline           bool              `json:"inline"`
	Attachment       bool              `json:"attachment"`
	EmbeddedMessage  bool              `json:"embedded_message"`
	DecodedSize      int64             `json:"decoded_size,omitempty"`
	Diagnostics      []string          `json:"diagnostics,omitempty"`
}

type AttachmentAvailability string

const (
	AttachmentAvailable   AttachmentAvailability = "available"
	AttachmentUnavailable AttachmentAvailability = "unavailable"
)

type Attachment struct {
	ID           string                 `json:"id"`
	MessageID    string                 `json:"message_id"`
	PartID       string                 `json:"part_id"`
	Ordinal      int                    `json:"ordinal"`
	PartPath     string                 `json:"part_path"`
	Filename     string                 `json:"filename,omitempty"`
	MediaType    string                 `json:"media_type,omitempty"`
	MediaParams  map[string]string      `json:"media_params,omitempty"`
	Disposition  string                 `json:"disposition,omitempty"`
	ContentID    string                 `json:"content_id,omitempty"`
	Inline       bool                   `json:"inline"`
	ContentHash  string                 `json:"content_hash,omitempty"`
	StorageKey   string                 `json:"-"`
	ByteSize     int64                  `json:"byte_size"`
	Availability AttachmentAvailability `json:"availability"`
	ErrorCode    string                 `json:"error_code,omitempty"`
}

type AttachmentBlob struct {
	ContentHash string
	StorageKey  string
	ByteSize    int64
	Open        func() (io.ReadCloser, error)
}

type AttachmentSink func(context.Context, MIMEPart, io.Reader) (AttachmentBlob, error)

type attachmentSinkError struct{ err error }

func (err attachmentSinkError) Error() string { return "store decoded attachment: " + err.err.Error() }
func (err attachmentSinkError) Unwrap() error { return errors.Join(ErrAttachmentStorage, err.err) }

type NormalizedContent struct {
	Preview            ParsedMessage     `json:"preview"`
	Headers            []HeaderField     `json:"headers"`
	Parts              []MIMEPart        `json:"parts"`
	Attachments        []Attachment      `json:"attachments,omitempty"`
	AttachmentsIndexed bool              `json:"attachments_indexed"`
	Diagnostics        []ParseDiagnostic `json:"diagnostics,omitempty"`
	TextPaths          []string          `json:"text_paths,omitempty"`
	HTMLPaths          []string          `json:"html_paths,omitempty"`
}

type ContentFormat string

const (
	ContentText ContentFormat = "text"
	ContentHTML ContentFormat = "html"
	ContentRaw  ContentFormat = "raw"
)

type ContentSource interface {
	WriteRaw(context.Context, io.Writer) error
	WriteBody(context.Context, ContentFormat, io.Writer) error
}

type AttachmentSource interface {
	Write(context.Context, io.Writer) error
}

// ParseNormalized walks one RFC822 message once, retaining bounded previews
// and complete metadata while draining every decoded part. Raw bytes remain
// authoritative in the blob store and are never reconstructed from this view.
func ParseNormalized(reader io.Reader) (NormalizedContent, error) {
	return ParseNormalizedContext(context.Background(), reader)
}

func ParseNormalizedContext(ctx context.Context, reader io.Reader) (NormalizedContent, error) {
	return ParseNormalizedWithAttachmentsContext(ctx, reader, nil)
}

func ParseNormalizedWithAttachmentsContext(ctx context.Context, reader io.Reader, sink AttachmentSink) (NormalizedContent, error) {
	if reader == nil {
		return NormalizedContent{}, fmt.Errorf("%w: raw message is missing", ErrInvalidMessage)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	entity, rootErr := message.ReadWithOptions(&contextReader{ctx: ctx, reader: reader}, &message.ReadOptions{MaxHeaderBytes: 1 << 20})
	if entity == nil {
		return NormalizedContent{}, fmt.Errorf("%w: read RFC822 message: %w", ErrInvalidMessage, rootErr)
	}
	result := NormalizedContent{}
	if rootErr != nil {
		if code := diagnosticCode(rootErr); code != "" {
			appendDiagnostic(&result.Diagnostics, ParseDiagnostic{Path: "0", Code: code})
		} else {
			return NormalizedContent{}, fmt.Errorf("%w: read RFC822 message: %v", ErrInvalidMessage, rootErr)
		}
	}

	header := gomail.Header{Header: entity.Header}
	result.Headers = canonicalHeaders(&header, &result.Diagnostics)
	result.Preview = previewFromHeader(&header)

	var textPreview, htmlPreview string
	partCount := 0
	result.AttachmentsIndexed = sink != nil
	registerPart := func(part MIMEPart) (MIMEPart, error) {
		partCount++
		if partCount > maxMIMEParts {
			return MIMEPart{}, errors.New("MIME part count exceeds the supported bound")
		}
		part.Ordinal = partCount - 1
		return part, nil
	}
	recordPart := func(part MIMEPart) {
		result.Parts = append(result.Parts, part)
		for _, code := range part.Diagnostics {
			appendDiagnostic(&result.Diagnostics, ParseDiagnostic{Path: part.Path, Code: code})
		}
	}
	recordAttachment := func(part MIMEPart, blob AttachmentBlob, errorCode string) {
		result.Preview.AttachmentCount++
		result.Attachments = append(result.Attachments, attachmentFromPart(part, blob, errorCode))
	}

	handleLeaf := func(part MIMEPart, body io.Reader) error {
		var err error
		part, err = registerPart(part)
		if err != nil {
			return err
		}
		if body == nil {
			recordPart(part)
			if part.Attachment {
				recordAttachment(part, AttachmentBlob{}, "content_unavailable")
			}
			return nil
		}
		if part.Attachment && sink != nil {
			blob, sinkErr := sink(ctx, part, body)
			part.DecodedSize = blob.ByteSize
			recordPart(part)
			if sinkErr != nil {
				return attachmentSinkError{err: sinkErr}
			}
			recordAttachment(part, blob, "")
			return nil
		}
		preview, size, readErr := readPreviewAndCount(body)
		part.DecodedSize = size
		recordPart(part)
		if part.Attachment {
			recordAttachment(part, AttachmentBlob{ByteSize: size}, "content_not_indexed")
		}
		if readErr != nil {
			if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
				return readErr
			}
			appendDiagnostic(&result.Diagnostics, ParseDiagnostic{Path: part.Path, Code: "part_read"})
			return nil
		}
		if part.Attachment || part.EmbeddedMessage {
			return nil
		}
		switch part.MediaType {
		case "text/plain", "":
			result.TextPaths = append(result.TextPaths, part.Path)
			if textPreview == "" {
				textPreview = preview
			}
		case "text/html":
			result.HTMLPaths = append(result.HTMLPaths, part.Path)
			if htmlPreview == "" {
				htmlPreview = preview
			}
		}
		return nil
	}

	var handleEmbedded mimeEmbeddedHandler
	if sink != nil {
		handleEmbedded = func(part MIMEPart, body io.Reader) (io.ReadCloser, error) {
			var err error
			part, err = registerPart(part)
			if err != nil {
				return nil, err
			}
			blob, sinkErr := sink(ctx, part, body)
			part.DecodedSize = blob.ByteSize
			recordPart(part)
			if sinkErr != nil {
				return nil, attachmentSinkError{err: sinkErr}
			}
			recordAttachment(part, blob, "")
			if blob.Open == nil {
				return nil, errors.New("attachment sink cannot reopen an embedded message")
			}
			return blob.Open()
		}
	}

	err := walkMIMEEntity(entity, "0", "", false, rootErr, handleLeaf, handleEmbedded)
	if err != nil {
		if ctx.Err() != nil {
			return NormalizedContent{}, ctx.Err()
		}
		var sinkErr attachmentSinkError
		if errors.As(err, &sinkErr) {
			return NormalizedContent{}, sinkErr
		}
		appendDiagnostic(&result.Diagnostics, ParseDiagnostic{Path: "0", Code: "mime_structure"})
	}
	preview := textPreview
	if strings.TrimSpace(preview) == "" {
		preview = stripHTML(htmlPreview)
	}
	result.Preview.Snippet = excerpt(cleanText(strings.ToValidUTF8(preview, "�")), maxStoredSnippetRunes)
	return result, nil
}

func attachmentFromPart(part MIMEPart, blob AttachmentBlob, errorCode string) Attachment {
	availability := AttachmentUnavailable
	if blob.StorageKey != "" && blob.ContentHash != "" && errorCode == "" {
		availability = AttachmentAvailable
	}
	return Attachment{
		Ordinal:      part.Ordinal,
		PartPath:     part.Path,
		Filename:     part.Filename,
		MediaType:    part.MediaType,
		MediaParams:  cloneStringMap(part.MediaParams),
		Disposition:  part.Disposition,
		ContentID:    part.ContentID,
		Inline:       part.Inline,
		ContentHash:  blob.ContentHash,
		StorageKey:   blob.StorageKey,
		ByteSize:     blob.ByteSize,
		Availability: availability,
		ErrorCode:    errorCode,
	}
}

func appendDiagnostic(values *[]ParseDiagnostic, candidate ParseDiagnostic) {
	for _, value := range *values {
		if value.Path == candidate.Path && value.Code == candidate.Code {
			return
		}
	}
	*values = append(*values, candidate)
}

func previewFromHeader(header *gomail.Header) ParsedMessage {
	parsed := ParsedMessage{}
	parsed.RFCMessageID, _ = header.MessageID()
	parsed.RFCMessageID = normalizeMessageID(parsed.RFCMessageID)
	if values, err := header.MsgIDList("In-Reply-To"); err == nil && len(values) > 0 {
		parsed.InReplyTo = normalizeMessageID(values[len(values)-1])
	}
	if values, err := header.MsgIDList("References"); err == nil {
		parsed.References = normalizeReferences(values)
	}
	parsed.From = firstHeaderAddress(header, "From")
	parsed.ReplyTo = firstHeaderAddress(header, "Reply-To")
	parsed.To = headerAddresses(header, "To")
	parsed.CC = headerAddresses(header, "Cc")
	parsed.BCC = headerAddresses(header, "Bcc")
	if subject, err := header.Subject(); err == nil {
		parsed.Subject = cleanHeaderText(subject)
	} else {
		parsed.Subject = cleanHeaderText(header.Get("Subject"))
	}
	if value, err := header.Date(); err == nil && !value.IsZero() {
		value = value.UTC()
		parsed.HeaderDate = &value
	}
	return parsed
}

func canonicalHeaders(header *gomail.Header, diagnostics *[]ParseDiagnostic) []HeaderField {
	fields := header.Fields()
	result := make([]HeaderField, 0, fields.Len())
	for fields.Next() {
		key := strings.TrimSpace(fields.Key())
		if key == "" {
			continue
		}
		value, err := fields.Text()
		if err != nil {
			value = fields.Value()
			appendDiagnostic(diagnostics, ParseDiagnostic{Path: "0", Code: "header_decode"})
		}
		result = append(result, HeaderField{Key: key, Value: cleanHeaderText(value)})
	}
	return result
}

type mimeLeafHandler func(MIMEPart, io.Reader) error
type mimeEmbeddedHandler func(MIMEPart, io.Reader) (io.ReadCloser, error)

func walkMIMEEntity(entity *message.Entity, path, parentPath string, embedded bool, entityErr error, handle mimeLeafHandler, handleEmbedded mimeEmbeddedHandler) error {
	part := describePart(entity, path, parentPath, embedded, entityErr)
	if multipartReader := entity.MultipartReader(); multipartReader != nil {
		if err := handle(part, nil); err != nil {
			return err
		}
		for index := 1; ; index++ {
			child, err := multipartReader.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if child == nil {
				return err
			}
			childPath := fmt.Sprintf("%s.%d", path, index)
			if path == "0" {
				childPath = fmt.Sprintf("%d", index)
			}
			if walkErr := walkMIMEEntity(child, childPath, path, embedded, err, handle, handleEmbedded); walkErr != nil {
				return walkErr
			}
		}
		return nil
	}
	if part.MediaType == "message/rfc822" {
		if handleEmbedded != nil {
			reader, err := handleEmbedded(part, entity.Body)
			if err != nil {
				return err
			}
			defer reader.Close()
			nested, readErr := message.ReadWithOptions(reader, &message.ReadOptions{MaxHeaderBytes: 1 << 20})
			if nested == nil {
				return fmt.Errorf("read embedded message: %w", readErr)
			}
			return walkMIMEEntity(nested, path+".1", path, true, readErr, handle, handleEmbedded)
		}
		if err := handle(part, nil); err != nil {
			return err
		}
		nested, err := message.ReadWithOptions(entity.Body, &message.ReadOptions{MaxHeaderBytes: 1 << 20})
		if nested == nil {
			return fmt.Errorf("read embedded message: %w", err)
		}
		return walkMIMEEntity(nested, path+".1", path, true, err, handle, handleEmbedded)
	}
	return handle(part, entity.Body)
}

func describePart(entity *message.Entity, path, parentPath string, embedded bool, entityErr error) MIMEPart {
	mediaType, params, _ := entity.Header.ContentType()
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	disposition, dispositionParams, _ := entity.Header.ContentDisposition()
	disposition = strings.ToLower(strings.TrimSpace(disposition))
	filename := strings.TrimSpace(dispositionParams["filename"])
	if filename == "" {
		filename = strings.TrimSpace(params["name"])
	}
	contentID := strings.Trim(strings.TrimSpace(entity.Header.Get("Content-ID")), "<>")
	embeddedMessage := mediaType == "message/rfc822"
	attachment := filename != "" || disposition == "attachment" || embeddedMessage ||
		(mediaType != "" && !strings.HasPrefix(mediaType, "text/") && !strings.HasPrefix(mediaType, "multipart/"))
	part := MIMEPart{
		Path:             path,
		ParentPath:       parentPath,
		MediaType:        mediaType,
		MediaParams:      cleanMediaParams(params),
		Charset:          strings.TrimSpace(params["charset"]),
		TransferEncoding: strings.ToLower(strings.TrimSpace(entity.Header.Get("Content-Transfer-Encoding"))),
		Disposition:      disposition,
		Filename:         cleanHeaderText(filename),
		ContentID:        cleanHeaderText(contentID),
		Inline:           disposition == "inline" || (contentID != "" && disposition != "attachment"),
		Attachment:       attachment,
		EmbeddedMessage:  embeddedMessage || embedded,
	}
	if code := diagnosticCode(entityErr); code != "" {
		part.Diagnostics = append(part.Diagnostics, code)
	}
	return part
}

func cleanMediaParams(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.ToLower(strings.TrimSpace(key))
		value = cleanHeaderText(value)
		if key == "" || value == "" || len(key) > 128 {
			continue
		}
		result[key] = value
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func diagnosticCode(err error) string {
	switch {
	case err == nil:
		return ""
	case message.IsUnknownCharset(err):
		return "unknown_charset"
	case message.IsUnknownEncoding(err):
		return "unknown_encoding"
	default:
		return ""
	}
}

func readPreviewAndCount(reader io.Reader) (string, int64, error) {
	var preview strings.Builder
	buffer := make([]byte, 32<<10)
	var total int64
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			total += int64(n)
			if preview.Len() < maxPreviewPartBytes {
				remaining := maxPreviewPartBytes - preview.Len()
				if remaining > n {
					remaining = n
				}
				preview.Write(buffer[:remaining])
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return strings.ToValidUTF8(preview.String(), "�"), total, err
		}
	}
	return strings.ToValidUTF8(preview.String(), "�"), total, nil
}

// WriteSelectedBody reparses the raw message through the same MIME walker and
// streams only the requested decoded body paths. It never materializes the
// complete body in memory.
func WriteSelectedBody(ctx context.Context, reader io.Reader, format ContentFormat, paths []string, writer io.Writer) error {
	if reader == nil || writer == nil {
		return errors.New("body reader and writer are required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	entity, err := message.ReadWithOptions(&contextReader{ctx: ctx, reader: reader}, &message.ReadOptions{MaxHeaderBytes: 1 << 20})
	if entity == nil {
		return fmt.Errorf("read RFC822 message body: %w", err)
	}
	targets := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		targets[strings.TrimSpace(path)] = struct{}{}
	}
	wrote := false
	err = walkMIMEEntity(entity, "0", "", false, err, func(part MIMEPart, body io.Reader) error {
		if body == nil {
			return nil
		}
		if _, ok := targets[part.Path]; !ok {
			_, copyErr := io.Copy(io.Discard, &contextReader{ctx: ctx, reader: body})
			return copyErr
		}
		if wrote {
			if _, writeErr := io.WriteString(writer, "\n"); writeErr != nil {
				return writeErr
			}
		}
		wrote = true
		if format == ContentText && part.MediaType == "text/html" {
			return streamHTMLText(ctx, body, writer)
		}
		_, copyErr := io.Copy(writer, &contextReader{ctx: ctx, reader: body})
		return copyErr
	}, nil)
	if err != nil {
		return err
	}
	if !wrote {
		return errors.New("requested message body is not available")
	}
	return nil
}

func streamHTMLText(ctx context.Context, reader io.Reader, writer io.Writer) error {
	buffer := make([]byte, 32<<10)
	inTag := false
	var entity strings.Builder
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			out := make([]byte, 0, n+16)
			for _, value := range buffer[:n] {
				if entity.Len() > 0 {
					entity.WriteByte(value)
					if value == ';' || entity.Len() > 32 {
						raw := entity.String()
						out = append(out, html.UnescapeString(raw)...)
						entity.Reset()
					}
					continue
				}
				switch value {
				case '<':
					inTag = true
				case '>':
					inTag = false
					out = append(out, ' ')
				case '&':
					if !inTag {
						entity.WriteByte(value)
					}
				default:
					if !inTag {
						out = append(out, value)
					}
				}
			}
			if _, writeErr := writer.Write(out); writeErr != nil {
				return writeErr
			}
		}
		if errors.Is(err, io.EOF) {
			if entity.Len() > 0 {
				if _, writeErr := io.WriteString(writer, html.UnescapeString(entity.String())); writeErr != nil {
					return writeErr
				}
			}
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.reader.Read(buffer)
	}
}
