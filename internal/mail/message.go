package mail

import (
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	message "github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"
	gomail "github.com/emersion/go-message/mail"
)

var (
	ErrMessageNotFound    = errors.New("message not found")
	ErrCheckpointNotFound = errors.New("sync checkpoint not found")
	ErrRepairRequired     = errors.New("mailbox synchronization requires repair")
	ErrInvalidMessage     = errors.New("message cannot be normalized")
)

const (
	maxStoredSnippetRunes = 320
	maxPreviewPartBytes   = 256 << 10
	maxHeaderValueBytes   = 16 << 10
)

type Direction string

const (
	DirectionInbound  Direction = "inbound"
	DirectionOutbound Direction = "outbound"
)

type RecipientKind string

const (
	RecipientTo  RecipientKind = "to"
	RecipientCC  RecipientKind = "cc"
	RecipientBCC RecipientKind = "bcc"
)

type CheckpointState string

const (
	CheckpointIdle           CheckpointState = "idle"
	CheckpointFailed         CheckpointState = "failed"
	CheckpointRepairRequired CheckpointState = "repair_required"
)

type DateSource string

const (
	DateSourceHeader       DateSource = "header"
	DateSourceInternalDate DateSource = "internal_date"
	DateSourceUnknown      DateSource = "unknown"
)

type Address struct {
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email"`
}

type ParsedMessage struct {
	RFCMessageID    string
	InReplyTo       string
	References      []string
	From            Address
	ReplyTo         Address
	To              []Address
	CC              []Address
	BCC             []Address
	Subject         string
	Snippet         string
	HeaderDate      *time.Time
	AttachmentCount int
}

type Message struct {
	ID              string     `json:"id"`
	AccountID       string     `json:"account_id"`
	RFCMessageID    string     `json:"rfc_message_id,omitempty"`
	InReplyTo       string     `json:"in_reply_to,omitempty"`
	References      []string   `json:"references,omitempty"`
	Direction       Direction  `json:"direction"`
	From            Address    `json:"from"`
	ReplyTo         Address    `json:"reply_to,omitempty"`
	To              []Address  `json:"to,omitempty"`
	CC              []Address  `json:"cc,omitempty"`
	BCC             []Address  `json:"bcc,omitempty"`
	Subject         string     `json:"subject,omitempty"`
	Snippet         string     `json:"snippet,omitempty"`
	HeaderDate      *time.Time `json:"header_date,omitempty"`
	InternalDate    *time.Time `json:"internal_date,omitempty"`
	SortAt          *time.Time `json:"sort_at,omitempty"`
	DateSource      DateSource `json:"date_source"`
	AttachmentCount int        `json:"attachment_count,omitempty"`
	Unread          bool       `json:"unread"`
	Flagged         bool       `json:"flagged"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Locations       []Location `json:"locations,omitempty"`
}

type Location struct {
	ID             string     `json:"id"`
	MessageID      string     `json:"message_id"`
	AccountID      string     `json:"account_id"`
	FolderID       string     `json:"folder_id"`
	UIDValidity    uint32     `json:"uid_validity"`
	UID            uint32     `json:"uid"`
	Flags          []string   `json:"flags,omitempty"`
	InternalDate   *time.Time `json:"internal_date,omitempty"`
	RFC822Size     int64      `json:"rfc822_size,omitempty"`
	RawContentHash string     `json:"-"`
	RawStorageKey  string     `json:"-"`
	RawByteSize    int64      `json:"raw_byte_size"`
	Present        bool       `json:"present"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type Checkpoint struct {
	ID                string          `json:"id"`
	AccountID         string          `json:"account_id"`
	FolderID          string          `json:"folder_id"`
	UIDValidity       uint32          `json:"uid_validity,omitempty"`
	LastCommittedUID  uint32          `json:"last_committed_uid,omitempty"`
	LastScannedUID    uint32          `json:"last_scanned_uid,omitempty"`
	BootstrapComplete bool            `json:"bootstrap_complete"`
	State             CheckpointState `json:"state"`
	LastErrorCode     string          `json:"last_error_code,omitempty"`
	MoreAvailable     bool            `json:"more_available"`
	LastAttemptAt     *time.Time      `json:"last_attempt_at,omitempty"`
	LastSuccessAt     *time.Time      `json:"last_success_at,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type IngestRecord struct {
	MessageID      string
	LocationID     string
	AccountID      string
	FolderID       string
	UIDValidity    uint32
	UID            uint32
	Flags          []string
	InternalDate   time.Time
	RFC822Size     int64
	RawContentHash string
	RawStorageKey  string
	RawByteSize    int64
	Parsed         ParsedMessage
}

type CommitPage struct {
	CheckpointID     string
	AccountID        string
	FolderID         string
	UIDValidity      uint32
	Bootstrap        bool
	LastCommittedUID uint32
	LastScannedUID   uint32
	MoreAvailable    bool
	AttemptedAt      time.Time
	Records          []IngestRecord
}

type CommitResult struct {
	Created    int        `json:"created"`
	Updated    int        `json:"updated"`
	Checkpoint Checkpoint `json:"checkpoint"`
}

type SyncSummary struct {
	AccountID     string     `json:"account_id"`
	FolderID      string     `json:"folder_id"`
	FolderName    string     `json:"folder_name"`
	Fetched       int        `json:"fetched"`
	Created       int        `json:"created"`
	Updated       int        `json:"updated"`
	LastUID       uint32     `json:"last_uid,omitempty"`
	MoreAvailable bool       `json:"more_available"`
	Bootstrap     bool       `json:"bootstrap"`
	CompletedAt   time.Time  `json:"completed_at"`
	Checkpoint    Checkpoint `json:"checkpoint"`
}

func ParsePreview(reader io.Reader) (ParsedMessage, error) {
	if reader == nil {
		return ParsedMessage{}, fmt.Errorf("%w: raw message is missing", ErrInvalidMessage)
	}
	mailReader, err := gomail.CreateReader(reader)
	if mailReader == nil {
		return ParsedMessage{}, fmt.Errorf("%w: read RFC822 message: %v", ErrInvalidMessage, err)
	}
	if err != nil && !message.IsUnknownCharset(err) {
		return ParsedMessage{}, fmt.Errorf("%w: read RFC822 message: %v", ErrInvalidMessage, err)
	}
	defer mailReader.Close()

	parsed := ParsedMessage{}
	parsed.RFCMessageID, _ = mailReader.Header.MessageID()
	parsed.RFCMessageID = normalizeMessageID(parsed.RFCMessageID)
	if values, listErr := mailReader.Header.MsgIDList("In-Reply-To"); listErr == nil && len(values) > 0 {
		parsed.InReplyTo = normalizeMessageID(values[len(values)-1])
	}
	if values, listErr := mailReader.Header.MsgIDList("References"); listErr == nil {
		parsed.References = normalizeReferences(values)
	}
	parsed.From = firstHeaderAddress(&mailReader.Header, "From")
	parsed.ReplyTo = firstHeaderAddress(&mailReader.Header, "Reply-To")
	parsed.To = headerAddresses(&mailReader.Header, "To")
	parsed.CC = headerAddresses(&mailReader.Header, "Cc")
	parsed.BCC = headerAddresses(&mailReader.Header, "Bcc")
	if subject, subjectErr := mailReader.Header.Subject(); subjectErr == nil {
		parsed.Subject = cleanHeaderText(subject)
	} else {
		parsed.Subject = cleanHeaderText(mailReader.Header.Get("Subject"))
	}
	if value, dateErr := mailReader.Header.Date(); dateErr == nil && !value.IsZero() {
		value = value.UTC()
		parsed.HeaderDate = &value
	}

	var textPreview, htmlPreview string
	for {
		part, partErr := mailReader.NextPart()
		if errors.Is(partErr, io.EOF) {
			break
		}
		if part == nil {
			if partErr != nil {
				return ParsedMessage{}, fmt.Errorf("%w: read MIME part: %v", ErrInvalidMessage, partErr)
			}
			continue
		}
		mediaType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		mediaType = strings.ToLower(strings.TrimSpace(mediaType))
		_, attachment := part.Header.(*gomail.AttachmentHeader)
		if attachment || (!strings.HasPrefix(mediaType, "text/") && mediaType != "") {
			parsed.AttachmentCount++
			if _, copyErr := io.Copy(io.Discard, part.Body); copyErr != nil {
				return ParsedMessage{}, fmt.Errorf("%w: skip attachment body: %v", ErrInvalidMessage, copyErr)
			}
			continue
		}
		content, readErr := readPreviewPart(part.Body)
		if readErr != nil {
			return ParsedMessage{}, fmt.Errorf("%w: read preview body: %v", ErrInvalidMessage, readErr)
		}
		switch mediaType {
		case "text/plain", "":
			if textPreview == "" {
				textPreview = content
			}
		case "text/html":
			if htmlPreview == "" {
				htmlPreview = content
			}
		}
		if partErr != nil && !message.IsUnknownCharset(partErr) {
			return ParsedMessage{}, fmt.Errorf("%w: decode MIME part: %v", ErrInvalidMessage, partErr)
		}
	}
	preview := textPreview
	if strings.TrimSpace(preview) == "" {
		preview = stripHTML(htmlPreview)
	}
	parsed.Snippet = excerpt(cleanText(preview), maxStoredSnippetRunes)
	return parsed, nil
}

func BuildIngestRecord(messageID, locationID, accountID, folderID string, uidValidity, uid uint32, flags []string, internalDate time.Time, rfc822Size int64, rawHash, rawKey string, rawSize int64, parsed ParsedMessage) IngestRecord {
	return IngestRecord{
		MessageID:      strings.TrimSpace(messageID),
		LocationID:     strings.TrimSpace(locationID),
		AccountID:      accountID,
		FolderID:       folderID,
		UIDValidity:    uidValidity,
		UID:            uid,
		Flags:          NormalizeFlags(flags),
		InternalDate:   internalDate.UTC(),
		RFC822Size:     rfc822Size,
		RawContentHash: strings.ToLower(strings.TrimSpace(rawHash)),
		RawStorageKey:  strings.TrimSpace(rawKey),
		RawByteSize:    rawSize,
		Parsed:         parsed,
	}
}

func NormalizeFlags(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 128 || strings.ContainsFunc(value, unicode.IsControl) {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	if len(result) > 64 {
		result = result[:64]
	}
	return result
}

func HasFlag(flags []string, target string) bool {
	for _, value := range flags {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func MessageTimes(parsed ParsedMessage, internalDate time.Time) (*time.Time, *time.Time, DateSource) {
	var internal *time.Time
	if !internalDate.IsZero() {
		value := internalDate.UTC()
		internal = &value
	}
	if parsed.HeaderDate != nil && !parsed.HeaderDate.IsZero() {
		value := parsed.HeaderDate.UTC()
		return internal, &value, DateSourceHeader
	}
	if internal != nil {
		value := *internal
		return internal, &value, DateSourceInternalDate
	}
	return nil, nil, DateSourceUnknown
}

func normalizeMessageID(value string) string {
	return strings.Trim(strings.TrimSpace(value), "<>")
}

func normalizeReferences(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeMessageID(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	if len(result) > 100 {
		result = result[len(result)-100:]
	}
	return result
}

func firstHeaderAddress(header *gomail.Header, key string) Address {
	values := headerAddresses(header, key)
	if len(values) == 0 {
		return Address{}
	}
	return values[0]
}

func headerAddresses(header *gomail.Header, key string) []Address {
	values, err := header.AddressList(key)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]Address, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		emailAddress := strings.ToLower(strings.TrimSpace(value.Address))
		if emailAddress == "" || !utf8.ValidString(emailAddress) || strings.ContainsFunc(emailAddress, unicode.IsControl) {
			continue
		}
		if _, exists := seen[emailAddress]; exists {
			continue
		}
		seen[emailAddress] = struct{}{}
		result = append(result, Address{DisplayName: cleanHeaderText(value.Name), Email: emailAddress})
	}
	return result
}

func cleanHeaderText(value string) string {
	if len(value) > maxHeaderValueBytes {
		value = value[:maxHeaderValueBytes]
	}
	return cleanText(value)
}

func cleanText(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func readPreviewPart(reader io.Reader) (string, error) {
	limited := io.LimitReader(reader, maxPreviewPartBytes+1)
	content, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return "", err
	}
	if len(content) > maxPreviewPartBytes {
		content = content[:maxPreviewPartBytes]
	}
	return string(content), nil
}

var htmlTagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

func stripHTML(value string) string {
	value = htmlTagPattern.ReplaceAllString(value, " ")
	return html.UnescapeString(value)
}

func excerpt(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 || utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}
