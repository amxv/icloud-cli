package mail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"strings"
	"unicode/utf8"

	message "github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"
	gomail "github.com/emersion/go-message/mail"
)

const (
	maxMIMEParts        = 2048
	maxIndexedBodyBytes = 1 << 20
)

var (
	ErrContentCorrupt     = errors.New("message content is corrupt")
	ErrContentUnavailable = errors.New("message content is unavailable")
	ErrContentLimit       = errors.New("message content exceeds the configured inbound limit")
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

type AttachmentTarget struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
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
	Preview             ParsedMessage     `json:"preview"`
	Headers             []HeaderField     `json:"headers"`
	Parts               []MIMEPart        `json:"parts"`
	Attachments         []Attachment      `json:"attachments,omitempty"`
	AttachmentsIndexed  bool              `json:"attachments_indexed"`
	Diagnostics         []ParseDiagnostic `json:"diagnostics,omitempty"`
	SMIME               *SMIMEStatus      `json:"smime,omitempty"`
	TextPaths           []string          `json:"text_paths,omitempty"`
	HTMLPaths           []string          `json:"html_paths,omitempty"`
	SearchText          string            `json:"-"`
	SearchTextTruncated bool              `json:"-"`
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
	return parseNormalizedWithOptions(ctx, reader, sink, nil)
}

// ParseNormalizedWithAttachmentsAndSMIMEContext preserves the ordinary MIME
// normalization contract while additionally inspecting recognized S/MIME
// control containers. Only S/MIME entities are captured, and capture is
// bounded; an inspection failure never makes otherwise-readable MIME content
// disappear.
func ParseNormalizedWithAttachmentsAndSMIMEContext(ctx context.Context, reader io.Reader, sink AttachmentSink, policy *SMIMEPolicy) (NormalizedContent, error) {
	return parseNormalizedWithOptions(ctx, reader, sink, policy)
}

func parseNormalizedWithOptions(ctx context.Context, reader io.Reader, sink AttachmentSink, policy *SMIMEPolicy) (NormalizedContent, error) {
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
	var inspector *smimeWalkInspector
	if policy != nil {
		expectedEmail := result.Preview.From.Email
		if strings.TrimSpace(policy.ExpectedSignerEmail) != "" {
			expectedEmail = policy.ExpectedSignerEmail
		}
		inspector = newSMIMEWalkInspector(expectedEmail, policy)
	}

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
		indexLimit := 0
		if !part.Attachment && !part.EmbeddedMessage && (part.MediaType == "text/plain" || part.MediaType == "text/html" || part.MediaType == "") {
			indexLimit = maxIndexedBodyBytes - len(result.SearchText)
			if result.SearchText != "" {
				indexLimit--
			}
			if indexLimit < 0 {
				indexLimit = 0
			}
		}
		preview, indexed, size, readErr := readPreviewIndexAndCount(body, indexLimit)
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
			appendIndexedBody(&result, indexed, size > int64(len(indexed)))
			if textPreview == "" {
				textPreview = preview
			}
		case "text/html":
			result.HTMLPaths = append(result.HTMLPaths, part.Path)
			appendIndexedBody(&result, stripHTML(indexed), size > int64(len(indexed)))
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

	err := walkMIMEEntityWithOptions(entity, "0", "", false, false, rootErr, handleLeaf, handleEmbedded, mimeWalkOptions{smime: inspector})
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
	if inspector != nil {
		status := inspector.Status()
		result.SMIME = &status
	}
	preview := textPreview
	if strings.TrimSpace(preview) == "" {
		preview = stripHTML(htmlPreview)
	}
	result.Preview.Snippet = excerpt(cleanText(strings.ToValidUTF8(preview, "�")), maxStoredSnippetRunes)
	return result, nil
}

func appendIndexedBody(content *NormalizedContent, value string, truncated bool) {
	value = cleanText(strings.ToValidUTF8(value, "�"))
	if value == "" {
		content.SearchTextTruncated = content.SearchTextTruncated || truncated
		return
	}
	remaining := maxIndexedBodyBytes - len(content.SearchText)
	if content.SearchText != "" {
		remaining--
	}
	if remaining <= 0 {
		content.SearchTextTruncated = true
		return
	}
	if len(value) > remaining {
		value = value[:remaining]
		for !utf8.ValidString(value) && len(value) > 0 {
			value = value[:len(value)-1]
		}
		truncated = true
	}
	if content.SearchText != "" && value != "" {
		content.SearchText += "\n"
	}
	content.SearchText += value
	content.SearchTextTruncated = content.SearchTextTruncated || truncated
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
	return walkMIMEEntityWithOptions(entity, path, parentPath, embedded, false, entityErr, handle, handleEmbedded, mimeWalkOptions{})
}

type mimeWalkOptions struct {
	smime *smimeWalkInspector
}

func walkMIMEEntityWithOptions(entity *message.Entity, path, parentPath string, embedded, control bool, entityErr error, handle mimeLeafHandler, handleEmbedded mimeEmbeddedHandler, options mimeWalkOptions) error {
	mediaType, mediaParams, _ := entity.Header.ContentType()
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	multipartSigned := options.smime != nil && isSMIMEMultipartSigned(mediaType, mediaParams)
	var capture *boundedSMIMECapture
	if multipartSigned {
		capture = newBoundedSMIMECapture(maxSMIMEContainerBytes)
		entity.Body = io.TeeReader(entity.Body, capture)
	}
	opaqueKind := SMIMEContainerKind("")
	if options.smime != nil {
		opaqueKind = smimeOpaqueContainerKind(mediaType, mediaParams)
		if opaqueKind != "" {
			control = true
			capture = newBoundedSMIMECapture(maxSMIMEContainerBytes)
			entity.Body = io.TeeReader(entity.Body, capture)
		}
	}
	part := describePart(entity, path, parentPath, embedded, control, entityErr)
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
			childControl := multipartSigned && index == 2
			if walkErr := walkMIMEEntityWithOptions(child, childPath, path, embedded, childControl, err, handle, handleEmbedded, options); walkErr != nil {
				return walkErr
			}
		}
		if multipartSigned {
			options.smime.InspectMultipartSigned(path, strings.TrimSpace(mediaParams["boundary"]), capture)
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
			return walkMIMEEntityWithOptions(nested, path+".1", path, true, false, readErr, handle, handleEmbedded, options)
		}
		if err := handle(part, nil); err != nil {
			return err
		}
		nested, err := message.ReadWithOptions(entity.Body, &message.ReadOptions{MaxHeaderBytes: 1 << 20})
		if nested == nil {
			return fmt.Errorf("read embedded message: %w", err)
		}
		return walkMIMEEntityWithOptions(nested, path+".1", path, true, false, err, handle, handleEmbedded, options)
	}
	if err := handle(part, entity.Body); err != nil {
		return err
	}
	if opaqueKind != "" {
		encapsulated := options.smime.InspectOpaque(path, opaqueKind, capture)
		if opaqueKind == SMIMEContainerOpaqueSigned && len(encapsulated) > 0 {
			nested, readErr := message.ReadWithOptions(bytes.NewReader(encapsulated), &message.ReadOptions{MaxHeaderBytes: 1 << 20})
			if nested != nil {
				return walkMIMEEntityWithOptions(nested, path+".1", path, embedded, false, readErr, handle, handleEmbedded, options)
			}
		}
	}
	return nil
}

func describePart(entity *message.Entity, path, parentPath string, embedded, control bool, entityErr error) MIMEPart {
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
	if control {
		attachment = false
	}
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
		Inline:           !control && (disposition == "inline" || (contentID != "" && disposition != "attachment")),
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

func readPreviewIndexAndCount(reader io.Reader, indexLimit int) (string, string, int64, error) {
	var preview strings.Builder
	var indexed strings.Builder
	if indexLimit < 0 {
		indexLimit = 0
	}
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
			if indexed.Len() < indexLimit {
				remaining := indexLimit - indexed.Len()
				if remaining > n {
					remaining = n
				}
				indexed.Write(buffer[:remaining])
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return strings.ToValidUTF8(preview.String(), "�"), strings.ToValidUTF8(indexed.String(), "�"), total, err
		}
	}
	return strings.ToValidUTF8(preview.String(), "�"), strings.ToValidUTF8(indexed.String(), "�"), total, nil
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
