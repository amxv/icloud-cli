package mail

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"net/textproto"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxSendBodyBytes               = 8 << 20
	MaxSendMessageBytes            = 20 << 20
	MaxSendRecipients              = 100
	MaxSendCustomHeaders           = 32
	MaxSendHeaderValue             = 8 << 10
	MaxSendSubjectRunes            = 998
	MaxSendAttachments             = 32
	MaxSendAttachmentBytes         = 64 << 20
	MaxSendAttachmentTotalBytes    = 128 << 20
	MaxSendAttachmentFilenameBytes = 255
)

var (
	ErrInvalidSendRequest      = errors.New("invalid outbound mail request")
	ErrIdempotencyConflict     = errors.New("idempotency key was already used for another request")
	ErrOutboundAttemptNotFound = errors.New("outbound attempt not found")
)

type OutboundStatus string

const (
	OutboundPending OutboundStatus = "pending"
	OutboundSent    OutboundStatus = "sent"
	OutboundFailed  OutboundStatus = "failed"
	OutboundUnknown OutboundStatus = "unknown"
)

type ReconciliationState string

const (
	ReconciliationNone      ReconciliationState = "none"
	ReconciliationPending   ReconciliationState = "pending"
	ReconciliationConfirmed ReconciliationState = "confirmed"
	ReconciliationUnknown   ReconciliationState = "unknown"
)

type OutboundKind string

const (
	OutboundKindSend     OutboundKind = "send"
	OutboundKindReply    OutboundKind = "reply"
	OutboundKindReplyAll OutboundKind = "reply_all"
	OutboundKindForward  OutboundKind = "forward"
)

type CustomHeader struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type OutboundAttachment struct {
	Filename    string `json:"filename"`
	MediaType   string `json:"media_type"`
	ContentHash string `json:"content_hash"`
	ByteSize    int64  `json:"byte_size"`

	SourcePath string                        `json:"-"`
	Open       func() (io.ReadCloser, error) `json:"-"`
}

type SendRequest struct {
	Kind                  OutboundKind         `json:"kind,omitempty"`
	ParentMessageID       string               `json:"parent_message_id,omitempty"`
	SourceMessageID       string               `json:"source_message_id,omitempty"`
	SenderIdentityID      string               `json:"sender_identity_id,omitempty"`
	From                  Address              `json:"from"`
	To                    []Address            `json:"to"`
	CC                    []Address            `json:"cc,omitempty"`
	BCC                   []Address            `json:"bcc,omitempty"`
	ReplyTo               *Address             `json:"reply_to,omitempty"`
	Subject               string               `json:"subject"`
	TextBody              string               `json:"text_body,omitempty"`
	HTMLBody              string               `json:"html_body,omitempty"`
	Headers               []CustomHeader       `json:"headers,omitempty"`
	InReplyTo             string               `json:"in_reply_to,omitempty"`
	References            []string             `json:"references,omitempty"`
	Attachments           []OutboundAttachment `json:"attachments,omitempty"`
	IdempotencyKey        string               `json:"-"`
	RetryOfAttemptID      string               `json:"-"`
	AuthorizeUnknownRetry bool                 `json:"-"`
}

type SendPreview struct {
	Kind             OutboundKind         `json:"kind,omitempty"`
	ParentMessageID  string               `json:"parent_message_id,omitempty"`
	SourceMessageID  string               `json:"source_message_id,omitempty"`
	SenderIdentityID string               `json:"sender_identity_id"`
	AccountID        string               `json:"account_id"`
	From             Address              `json:"from"`
	To               []Address            `json:"to"`
	CC               []Address            `json:"cc,omitempty"`
	BCC              []Address            `json:"bcc,omitempty"`
	ReplyTo          *Address             `json:"reply_to,omitempty"`
	Subject          string               `json:"subject"`
	TextBody         string               `json:"text_body,omitempty"`
	HTMLBody         string               `json:"html_body,omitempty"`
	Headers          []CustomHeader       `json:"headers,omitempty"`
	InReplyTo        string               `json:"in_reply_to,omitempty"`
	References       []string             `json:"references,omitempty"`
	Attachments      []OutboundAttachment `json:"attachments,omitempty"`
	BodyParts        int                  `json:"body_parts"`
	AttachmentCount  int                  `json:"attachment_count"`
	AttachmentBytes  int64                `json:"attachment_bytes"`
	RequestHash      string               `json:"request_hash"`
}

type OutboundAttempt struct {
	ID                      string              `json:"id"`
	AccountID               string              `json:"account_id"`
	SenderIdentityID        string              `json:"sender_identity_id"`
	ProposedMessageID       string              `json:"proposed_message_id"`
	RequestHash             string              `json:"request_hash"`
	Status                  OutboundStatus      `json:"status"`
	RFCMessageID            string              `json:"rfc_message_id"`
	ToCount                 int                 `json:"to_count"`
	CCCount                 int                 `json:"cc_count"`
	BCCCount                int                 `json:"bcc_count"`
	BodyPartCount           int                 `json:"body_part_count"`
	AttachmentCount         int                 `json:"attachment_count"`
	AttachmentBytes         int64               `json:"attachment_bytes"`
	Kind                    OutboundKind        `json:"kind"`
	ParentMessageID         string              `json:"parent_message_id,omitempty"`
	SourceMessageID         string              `json:"source_message_id,omitempty"`
	MessageID               string              `json:"message_id,omitempty"`
	ThreadID                string              `json:"thread_id,omitempty"`
	RetryOfAttemptID        string              `json:"retry_of_attempt_id,omitempty"`
	Reconciliation          ReconciliationState `json:"reconciliation"`
	ReconciledLocationID    string              `json:"reconciled_location_id,omitempty"`
	ReconciliationErrorCode string              `json:"reconciliation_error_code,omitempty"`
	ErrorCode               string              `json:"error_code,omitempty"`
	AcceptedAt              *time.Time          `json:"accepted_at,omitempty"`
	CreatedAt               time.Time           `json:"created_at"`
	UpdatedAt               time.Time           `json:"updated_at"`

	IdempotencyKey string `json:"-"`
	RawContentHash string `json:"-"`
	RawStorageKey  string `json:"-"`
	RawByteSize    int64  `json:"-"`
}

type BeginOutboundAttempt struct {
	AttemptID         string
	AccountID         string
	SenderIdentityID  string
	ProposedMessageID string
	RequestHash       string
	IdempotencyKey    string
	RFCMessageID      string
	RawContentHash    string
	RawStorageKey     string
	RawByteSize       int64
	ToCount           int
	CCCount           int
	BCCCount          int
	BodyPartCount     int
	Attachments       []OutboundAttachmentRecord
	Kind              OutboundKind
	ParentMessageID   string
	SourceMessageID   string
	RetryOfAttemptID  string
	Now               time.Time
}

type OutboundAttachmentRecord struct {
	Ordinal     int
	Filename    string
	MediaType   string
	ContentHash string
	StorageKey  string
	ByteSize    int64
}

type CompleteOutboundSend struct {
	AttemptID  string
	Request    SendRequest
	Content    NormalizedContent
	AcceptedAt time.Time
}

type LocalRawSource struct {
	ContentHash string `json:"-"`
	StorageKey  string `json:"-"`
	ByteSize    int64  `json:"-"`
}

type OutboundReconcileSummary struct {
	Scanned         int `json:"scanned"`
	Confirmed       int `json:"confirmed"`
	ResolvedUnknown int `json:"resolved_unknown"`
	Ambiguous       int `json:"ambiguous"`
	Unmatched       int `json:"unmatched"`
}

func NormalizeSendRequest(value SendRequest) (SendRequest, error) {
	return normalizeMessageRequest(value, true, true, true)
}

func NormalizeDraftRequest(value SendRequest) (SendRequest, error) {
	return normalizeMessageRequest(value, false, false, false)
}

func normalizeMessageRequest(value SendRequest, requireTo, requireSubject, requireBody bool) (SendRequest, error) {
	value.Kind = normalizeOutboundKind(value.Kind)
	value.ParentMessageID = strings.TrimSpace(value.ParentMessageID)
	value.SourceMessageID = strings.TrimSpace(value.SourceMessageID)
	value.SenderIdentityID = strings.TrimSpace(value.SenderIdentityID)
	if err := validateOutboundThreading(&value); err != nil {
		return SendRequest{}, err
	}
	var err error
	value.From, err = normalizeOneAddress(value.From, true)
	if err != nil {
		return SendRequest{}, fmt.Errorf("%w: from address", ErrInvalidSendRequest)
	}
	value.To, err = normalizeAddressList(value.To)
	if err != nil {
		return SendRequest{}, fmt.Errorf("%w: To recipients", ErrInvalidSendRequest)
	}
	value.CC, err = normalizeAddressList(value.CC)
	if err != nil {
		return SendRequest{}, fmt.Errorf("%w: Cc recipients", ErrInvalidSendRequest)
	}
	value.BCC, err = normalizeAddressList(value.BCC)
	if err != nil {
		return SendRequest{}, fmt.Errorf("%w: Bcc recipients", ErrInvalidSendRequest)
	}
	if requireTo && len(value.To) == 0 {
		return SendRequest{}, fmt.Errorf("%w: at least one To recipient is required", ErrInvalidSendRequest)
	}
	if len(value.To)+len(value.CC)+len(value.BCC) > MaxSendRecipients {
		return SendRequest{}, fmt.Errorf("%w: recipient count exceeds %d", ErrInvalidSendRequest, MaxSendRecipients)
	}
	dedupeCrossLists(&value)
	if value.ReplyTo != nil {
		normalized, normalizeErr := normalizeOneAddress(*value.ReplyTo, true)
		if normalizeErr != nil {
			return SendRequest{}, fmt.Errorf("%w: Reply-To address", ErrInvalidSendRequest)
		}
		value.ReplyTo = &normalized
	}
	value.Subject = cleanSendHeaderText(value.Subject)
	if (requireSubject && value.Subject == "") || utf8.RuneCountInString(value.Subject) > MaxSendSubjectRunes {
		return SendRequest{}, fmt.Errorf("%w: subject is required and must fit RFC line bounds", ErrInvalidSendRequest)
	}
	value.TextBody = normalizeBody(value.TextBody)
	value.HTMLBody = normalizeBody(value.HTMLBody)
	if requireBody && value.TextBody == "" && value.HTMLBody == "" {
		return SendRequest{}, fmt.Errorf("%w: a text or HTML body is required", ErrInvalidSendRequest)
	}
	if len(value.TextBody) > MaxSendBodyBytes || len(value.HTMLBody) > MaxSendBodyBytes {
		return SendRequest{}, fmt.Errorf("%w: body exceeds %d bytes", ErrInvalidSendRequest, MaxSendBodyBytes)
	}
	value.Headers, err = normalizeCustomHeaders(value.Headers)
	if err != nil {
		return SendRequest{}, err
	}
	value.Attachments, err = normalizeOutboundAttachments(value.Attachments)
	if err != nil {
		return SendRequest{}, err
	}
	value.IdempotencyKey = strings.TrimSpace(value.IdempotencyKey)
	if len(value.IdempotencyKey) > 256 || strings.ContainsFunc(value.IdempotencyKey, unicode.IsControl) {
		return SendRequest{}, fmt.Errorf("%w: invalid idempotency key", ErrInvalidSendRequest)
	}
	value.RetryOfAttemptID = strings.TrimSpace(value.RetryOfAttemptID)
	return value, nil
}

func ParseSendAddress(value string) (Address, error) {
	parsed, err := mail.ParseAddress(strings.TrimSpace(value))
	if err != nil {
		return Address{}, err
	}
	return normalizeOneAddress(Address{DisplayName: parsed.Name, Email: parsed.Address}, true)
}

func SendRequestHash(value SendRequest) (string, error) {
	copy := value
	copy.IdempotencyKey = ""
	payload, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func (value SendRequest) Operation() OutboundKind {
	if value.Kind == "" {
		return OutboundKindSend
	}
	return value.Kind
}

func (value SendRequest) EnvelopeRecipients() []string {
	result := make([]string, 0, len(value.To)+len(value.CC)+len(value.BCC))
	for _, list := range [][]Address{value.To, value.CC, value.BCC} {
		for _, address := range list {
			result = append(result, address.Email)
		}
	}
	return result
}

func (value SendRequest) BodyPartCount() int {
	count := 0
	if value.TextBody != "" {
		count++
	}
	if value.HTMLBody != "" {
		count++
	}
	return count
}

func (value SendRequest) AttachmentByteSize() int64 {
	var total int64
	for _, attachment := range value.Attachments {
		total += attachment.ByteSize
	}
	return total
}

func normalizeOutboundAttachments(values []OutboundAttachment) ([]OutboundAttachment, error) {
	if len(values) > MaxSendAttachments {
		return nil, fmt.Errorf("%w: attachment count exceeds %d", ErrInvalidSendRequest, MaxSendAttachments)
	}
	result := make([]OutboundAttachment, 0, len(values))
	var total int64
	for _, value := range values {
		filename := strings.TrimSpace(value.Filename)
		if filename == "" || filename == "." || filename == ".." || filepath.Base(filename) != filename || strings.ContainsAny(filename, `/\`) ||
			len(filename) > MaxSendAttachmentFilenameBytes || strings.ContainsAny(filename, "\x00\r\n") || strings.ContainsFunc(filename, unicode.IsControl) {
			return nil, fmt.Errorf("%w: invalid attachment filename", ErrInvalidSendRequest)
		}
		mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value.MediaType))
		if err != nil || !strings.Contains(mediaType, "/") {
			return nil, fmt.Errorf("%w: invalid attachment media type", ErrInvalidSendRequest)
		}
		mediaType = strings.ToLower(mediaType)
		contentHash := strings.ToLower(strings.TrimSpace(value.ContentHash))
		decoded, err := hex.DecodeString(contentHash)
		if err != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("%w: invalid attachment content hash", ErrInvalidSendRequest)
		}
		if value.ByteSize < 0 || value.ByteSize > MaxSendAttachmentBytes {
			return nil, fmt.Errorf("%w: attachment exceeds %d bytes", ErrInvalidSendRequest, MaxSendAttachmentBytes)
		}
		total += value.ByteSize
		if total > MaxSendAttachmentTotalBytes {
			return nil, fmt.Errorf("%w: attachment total exceeds %d bytes", ErrInvalidSendRequest, MaxSendAttachmentTotalBytes)
		}
		value.Filename = filename
		value.MediaType = mediaType
		value.ContentHash = contentHash
		result = append(result, value)
	}
	return result, nil
}

func normalizeOneAddress(value Address, required bool) (Address, error) {
	value.DisplayName = cleanSendHeaderText(value.DisplayName)
	parsed, err := mail.ParseAddress(strings.TrimSpace(value.Email))
	if err != nil {
		if !required && strings.TrimSpace(value.Email) == "" {
			return Address{}, nil
		}
		return Address{}, err
	}
	value.Email = strings.ToLower(strings.TrimSpace(parsed.Address))
	if value.Email == "" || strings.ContainsAny(value.Email, "\r\n") {
		return Address{}, errors.New("empty address")
	}
	if value.DisplayName == "" {
		value.DisplayName = cleanSendHeaderText(parsed.Name)
	}
	return value, nil
}

func normalizeAddressList(values []Address) ([]Address, error) {
	result := make([]Address, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		normalized, err := normalizeOneAddress(value, true)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[normalized.Email]; exists {
			continue
		}
		seen[normalized.Email] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func dedupeCrossLists(value *SendRequest) {
	seen := map[string]struct{}{}
	filter := func(values []Address) []Address {
		result := values[:0]
		for _, address := range values {
			if _, exists := seen[address.Email]; exists {
				continue
			}
			seen[address.Email] = struct{}{}
			result = append(result, address)
		}
		return result
	}
	value.To = filter(value.To)
	value.CC = filter(value.CC)
	value.BCC = filter(value.BCC)
}

func SafeCustomHeaders(fields []HeaderField) []CustomHeader {
	if len(fields) == 0 {
		return nil
	}
	result := make([]CustomHeader, 0, min(len(fields), MaxSendCustomHeaders))
	seen := map[string]struct{}{}
	for _, field := range fields {
		if len(result) >= MaxSendCustomHeaders {
			break
		}
		normalized, err := normalizeCustomHeaders([]CustomHeader{{Key: field.Key, Value: field.Value}})
		if err != nil || len(normalized) != 1 {
			continue
		}
		key := strings.ToLower(normalized[0].Key)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, normalized[0])
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Key) < strings.ToLower(result[j].Key) })
	return result
}

func normalizeCustomHeaders(values []CustomHeader) ([]CustomHeader, error) {
	if len(values) > MaxSendCustomHeaders {
		return nil, fmt.Errorf("%w: too many custom headers", ErrInvalidSendRequest)
	}
	reserved := map[string]struct{}{
		"bcc": {}, "cc": {}, "content-transfer-encoding": {}, "content-type": {},
		"date": {}, "from": {}, "in-reply-to": {}, "message-id": {}, "mime-version": {}, "references": {}, "reply-to": {},
		"return-path": {}, "sender": {}, "subject": {}, "to": {},
	}
	result := make([]CustomHeader, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		key := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(value.Key))
		lower := strings.ToLower(key)
		if key == "" || len(key) > 64 || !validHeaderKey(key) {
			return nil, fmt.Errorf("%w: invalid custom header name", ErrInvalidSendRequest)
		}
		if _, blocked := reserved[lower]; blocked || strings.HasPrefix(lower, "resent-") || strings.HasPrefix(lower, "received") {
			return nil, fmt.Errorf("%w: custom header %s is reserved", ErrInvalidSendRequest, key)
		}
		text := strings.TrimSpace(value.Value)
		if text == "" || len(text) > MaxSendHeaderValue || strings.ContainsAny(text, "\r\n") || strings.ContainsFunc(text, unicode.IsControl) {
			return nil, fmt.Errorf("%w: invalid custom header value", ErrInvalidSendRequest)
		}
		if _, duplicate := seen[lower]; duplicate {
			return nil, fmt.Errorf("%w: duplicate custom header %s", ErrInvalidSendRequest, key)
		}
		seen[lower] = struct{}{}
		result = append(result, CustomHeader{Key: key, Value: text})
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Key) < strings.ToLower(result[j].Key) })
	return result, nil
}

func normalizeOutboundKind(value OutboundKind) OutboundKind {
	switch value {
	case "", OutboundKindSend:
		// Keep ordinary send as the zero value so request hashes created before
		// relationship-aware outbound workflows remain stable across upgrades.
		return ""
	case OutboundKindReply, OutboundKindReplyAll, OutboundKindForward:
		return value
	default:
		return value
	}
}

func validateOutboundThreading(value *SendRequest) error {
	if value == nil {
		return fmt.Errorf("%w: outbound request is missing", ErrInvalidSendRequest)
	}
	switch value.Kind {
	case "":
		if value.ParentMessageID != "" || value.SourceMessageID != "" || strings.TrimSpace(value.InReplyTo) != "" || len(value.References) > 0 {
			return fmt.Errorf("%w: relationship metadata requires the matching outbound operation", ErrInvalidSendRequest)
		}
	case OutboundKindReply, OutboundKindReplyAll:
		if !strings.HasPrefix(value.ParentMessageID, "msg_") {
			return fmt.Errorf("%w: reply parent message is required", ErrInvalidSendRequest)
		}
		if value.SourceMessageID != "" {
			return fmt.Errorf("%w: reply cannot name a forward source", ErrInvalidSendRequest)
		}
	case OutboundKindForward:
		if !strings.HasPrefix(value.SourceMessageID, "msg_") {
			return fmt.Errorf("%w: forward source message is required", ErrInvalidSendRequest)
		}
		if value.ParentMessageID != "" || strings.TrimSpace(value.InReplyTo) != "" || len(value.References) > 0 {
			return fmt.Errorf("%w: forward cannot contain reply relationship metadata", ErrInvalidSendRequest)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported outbound operation", ErrInvalidSendRequest)
	}
	if value.Kind == "" {
		return nil
	}
	threadingID, ok := NormalizeRFCMessageID(value.InReplyTo)
	if strings.TrimSpace(value.InReplyTo) != "" && !ok {
		return fmt.Errorf("%w: invalid In-Reply-To message identifier", ErrInvalidSendRequest)
	}
	value.InReplyTo = threadingID
	if value.InReplyTo == "" {
		value.References = nil
		return nil
	}
	value.References = NormalizeReferenceChain(value.References, value.InReplyTo)
	return nil
}

func validHeaderKey(value string) bool {
	for _, character := range value {
		if character <= 32 || character >= 127 || character == ':' {
			return false
		}
	}
	return true
}

func cleanSendHeaderText(value string) string {
	value = strings.Map(func(character rune) rune {
		if character == '\r' || character == '\n' || character == '\t' {
			return ' '
		}
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func normalizeBody(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.TrimRight(value, "\n")
}
