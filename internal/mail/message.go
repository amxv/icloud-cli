package mail

import (
	"errors"
	"html"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	_ "github.com/emersion/go-message/charset"
	gomail "github.com/emersion/go-message/mail"
)

var (
	ErrMessageNotFound    = errors.New("message not found")
	ErrAttachmentNotFound = errors.New("attachment not found")
	ErrCheckpointNotFound = errors.New("sync checkpoint not found")
	ErrLocationNotFound   = errors.New("message location not found")
	ErrLocationAmbiguous  = errors.New("message has multiple eligible locations")
	ErrLocationConflict   = errors.New("message location changed concurrently")
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
	ID              string             `json:"id"`
	AccountID       string             `json:"account_id"`
	RFCMessageID    string             `json:"rfc_message_id,omitempty"`
	InReplyTo       string             `json:"in_reply_to,omitempty"`
	References      []string           `json:"references,omitempty"`
	Direction       Direction          `json:"direction"`
	From            Address            `json:"from"`
	ReplyTo         Address            `json:"reply_to,omitempty"`
	To              []Address          `json:"to,omitempty"`
	CC              []Address          `json:"cc,omitempty"`
	BCC             []Address          `json:"bcc,omitempty"`
	Subject         string             `json:"subject,omitempty"`
	Snippet         string             `json:"snippet,omitempty"`
	HeaderDate      *time.Time         `json:"header_date,omitempty"`
	InternalDate    *time.Time         `json:"internal_date,omitempty"`
	SortAt          *time.Time         `json:"sort_at,omitempty"`
	DateSource      DateSource         `json:"date_source"`
	AttachmentCount int                `json:"attachment_count,omitempty"`
	Unread          bool               `json:"unread"`
	Flagged         bool               `json:"flagged"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	Locations       []Location         `json:"locations,omitempty"`
	Content         *NormalizedContent `json:"content,omitempty"`
}

type LocationFlags struct {
	Seen     bool     `json:"seen"`
	Flagged  bool     `json:"flagged"`
	Answered bool     `json:"answered"`
	Draft    bool     `json:"draft"`
	Deleted  bool     `json:"deleted"`
	Unknown  []string `json:"unknown,omitempty"`
}

type FlagMutation string

const (
	FlagMutationMarkRead   FlagMutation = "mark_read"
	FlagMutationMarkUnread FlagMutation = "mark_unread"
	FlagMutationFlag       FlagMutation = "flag"
	FlagMutationUnflag     FlagMutation = "unflag"
)

func (mutation FlagMutation) ProviderFlag() (string, bool, bool) {
	switch mutation {
	case FlagMutationMarkRead:
		return `\Seen`, true, true
	case FlagMutationMarkUnread:
		return `\Seen`, false, true
	case FlagMutationFlag:
		return `\Flagged`, true, true
	case FlagMutationUnflag:
		return `\Flagged`, false, true
	default:
		return "", false, false
	}
}

func (mutation FlagMutation) Achieved(flags LocationFlags) bool {
	switch mutation {
	case FlagMutationMarkRead:
		return flags.Seen
	case FlagMutationMarkUnread:
		return !flags.Seen
	case FlagMutationFlag:
		return flags.Flagged
	case FlagMutationUnflag:
		return !flags.Flagged
	default:
		return false
	}
}

func ParseLocationFlags(values []string) LocationFlags {
	var result LocationFlags
	unknown := make([]string, 0, len(values))
	seenUnknown := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 128 || strings.ContainsFunc(value, unicode.IsControl) {
			continue
		}
		switch strings.ToLower(value) {
		case `\seen`:
			result.Seen = true
		case `\flagged`:
			result.Flagged = true
		case `\answered`:
			result.Answered = true
		case `\draft`:
			result.Draft = true
		case `\deleted`:
			result.Deleted = true
		default:
			key := strings.ToLower(value)
			if _, exists := seenUnknown[key]; exists {
				continue
			}
			seenUnknown[key] = struct{}{}
			unknown = append(unknown, value)
		}
	}
	sort.Slice(unknown, func(i, j int) bool {
		left, right := strings.ToLower(unknown[i]), strings.ToLower(unknown[j])
		if left == right {
			return unknown[i] < unknown[j]
		}
		return left < right
	})
	if len(unknown) > 59 {
		unknown = unknown[:59]
	}
	result.Unknown = unknown
	return result
}

func (flags LocationFlags) All() []string {
	values := make([]string, 0, 5+len(flags.Unknown))
	if flags.Seen {
		values = append(values, `\Seen`)
	}
	if flags.Flagged {
		values = append(values, `\Flagged`)
	}
	if flags.Answered {
		values = append(values, `\Answered`)
	}
	if flags.Draft {
		values = append(values, `\Draft`)
	}
	if flags.Deleted {
		values = append(values, `\Deleted`)
	}
	values = append(values, ParseLocationFlags(flags.Unknown).Unknown...)
	return values
}

func (flags LocationFlags) Eligible() bool { return !flags.Deleted }

type Location struct {
	ID             string        `json:"id"`
	MessageID      string        `json:"message_id"`
	AccountID      string        `json:"account_id"`
	FolderID       string        `json:"folder_id"`
	UIDValidity    uint32        `json:"uid_validity"`
	UID            uint32        `json:"uid"`
	Flags          LocationFlags `json:"flags"`
	ModSequence    uint64        `json:"mod_sequence,omitempty"`
	FlagsCheckedAt *time.Time    `json:"flags_checked_at,omitempty"`
	InternalDate   *time.Time    `json:"internal_date,omitempty"`
	RFC822Size     int64         `json:"rfc822_size,omitempty"`
	RawContentHash string        `json:"-"`
	RawStorageKey  string        `json:"-"`
	RawByteSize    int64         `json:"raw_byte_size"`
	Present        bool          `json:"present"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
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
	ModSequence    uint64
	InternalDate   time.Time
	RFC822Size     int64
	RawContentHash string
	RawStorageKey  string
	RawByteSize    int64
	Parsed         ParsedMessage
	Content        *NormalizedContent
}

type ReconcileRecord struct {
	UID         uint32
	Flags       LocationFlags
	ModSequence uint64
	Present     bool
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
	Reconciled       []ReconcileRecord
}

type CommitResult struct {
	Created      int        `json:"created"`
	Updated      int        `json:"updated"`
	Reconciled   int        `json:"reconciled"`
	StateChanges int        `json:"state_changes"`
	Checkpoint   Checkpoint `json:"checkpoint"`
}

type SyncSummary struct {
	AccountID     string     `json:"account_id"`
	FolderID      string     `json:"folder_id"`
	FolderName    string     `json:"folder_name"`
	Fetched       int        `json:"fetched"`
	Created       int        `json:"created"`
	Updated       int        `json:"updated"`
	Reconciled    int        `json:"reconciled"`
	StateChanges  int        `json:"state_changes"`
	LastUID       uint32     `json:"last_uid,omitempty"`
	MoreAvailable bool       `json:"more_available"`
	Bootstrap     bool       `json:"bootstrap"`
	CompletedAt   time.Time  `json:"completed_at"`
	Checkpoint    Checkpoint `json:"checkpoint"`
}

func ParsePreview(reader io.Reader) (ParsedMessage, error) {
	normalized, err := ParseNormalized(reader)
	return normalized.Preview, err
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
	return ParseLocationFlags(values).All()
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
