package mail

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html"
	"io"
	"regexp"
	"sort"
	"strconv"
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
	ID                string             `json:"id"`
	AccountID         string             `json:"account_id"`
	RFCMessageID      string             `json:"rfc_message_id,omitempty"`
	InReplyTo         string             `json:"in_reply_to,omitempty"`
	References        []string           `json:"references,omitempty"`
	Direction         Direction          `json:"direction"`
	From              Address            `json:"from"`
	ReplyTo           Address            `json:"reply_to,omitempty"`
	To                []Address          `json:"to,omitempty"`
	CC                []Address          `json:"cc,omitempty"`
	BCC               []Address          `json:"bcc,omitempty"`
	Subject           string             `json:"subject,omitempty"`
	Snippet           string             `json:"snippet,omitempty"`
	HeaderDate        *time.Time         `json:"header_date,omitempty"`
	InternalDate      *time.Time         `json:"internal_date,omitempty"`
	SortAt            *time.Time         `json:"sort_at,omitempty"`
	DateSource        DateSource         `json:"date_source"`
	AttachmentCount   int                `json:"attachment_count,omitempty"`
	Unread            bool               `json:"unread"`
	Flagged           bool               `json:"flagged"`
	Answered          bool               `json:"answered"`
	Replied           bool               `json:"replied"`
	ReplyState        string             `json:"reply_state"`
	Forwarded         bool               `json:"forwarded"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
	Locations         []Location         `json:"locations,omitempty"`
	Content           *NormalizedContent `json:"content,omitempty"`
	ContentLocationID string             `json:"content_location_id,omitempty"`
	Identity          []IdentityEvidence `json:"identity,omitempty"`
	DedupeConflicts   []DedupeConflict   `json:"dedupe_conflicts,omitempty"`
	LocalRaw          *LocalRawSource    `json:"-"`
}

type IdentityKind string

const (
	IdentityRFCMessageID       IdentityKind = "rfc_message_id"
	IdentityGeneratedMessageID IdentityKind = "generated_message_id"
	IdentityProviderLocation   IdentityKind = "provider_location"
	IdentityContentFingerprint IdentityKind = "content_fingerprint"
)

type IdentityStrength string

const (
	IdentityStrong  IdentityStrength = "strong"
	IdentityGuarded IdentityStrength = "guarded"
)

type IdentityEvidence struct {
	ID               string           `json:"id"`
	AccountID        string           `json:"account_id"`
	MessageID        string           `json:"message_id"`
	Kind             IdentityKind     `json:"kind"`
	Value            string           `json:"value"`
	Strength         IdentityStrength `json:"strength"`
	SourceLocationID string           `json:"source_location_id,omitempty"`
	FirstSeenAt      time.Time        `json:"first_seen_at"`
	LastSeenAt       time.Time        `json:"last_seen_at"`
}

type DedupeConflict struct {
	ID                 string       `json:"id"`
	AccountID          string       `json:"account_id"`
	EvidenceKind       IdentityKind `json:"evidence_kind"`
	EvidenceValue      string       `json:"evidence_value"`
	ExistingMessageID  string       `json:"existing_message_id"`
	ObservedMessageID  string       `json:"observed_message_id"`
	ExistingLocationID string       `json:"existing_location_id,omitempty"`
	ObservedLocationID string       `json:"observed_location_id,omitempty"`
	Reason             string       `json:"reason"`
	Status             string       `json:"status"`
	CreatedAt          time.Time    `json:"created_at"`
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
	FlagMutationAnswer     FlagMutation = "answer"
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
	case FlagMutationAnswer:
		return `\Answered`, true, true
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
	case FlagMutationAnswer:
		return flags.Answered
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
	FolderKind     string        `json:"folder_kind,omitempty"`
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
	MessageID          string
	LocationID         string
	AccountID          string
	FolderID           string
	UIDValidity        uint32
	UID                uint32
	Flags              []string
	ModSequence        uint64
	InternalDate       time.Time
	RFC822Size         int64
	RawContentHash     string
	RawStorageKey      string
	RawByteSize        int64
	Parsed             ParsedMessage
	Content            *NormalizedContent
	Direction          Direction
	ContentFingerprint string
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
	Created         int        `json:"created"`
	Updated         int        `json:"updated"`
	Reconciled      int        `json:"reconciled"`
	StateChanges    int        `json:"state_changes"`
	IdentityMerges  int        `json:"identity_merges"`
	DedupeConflicts int        `json:"dedupe_conflicts"`
	Checkpoint      Checkpoint `json:"checkpoint"`
}

type SyncSummary struct {
	AccountID         string                    `json:"account_id"`
	FolderID          string                    `json:"folder_id"`
	FolderName        string                    `json:"folder_name"`
	Fetched           int                       `json:"fetched"`
	Created           int                       `json:"created"`
	Updated           int                       `json:"updated"`
	Reconciled        int                       `json:"reconciled"`
	StateChanges      int                       `json:"state_changes"`
	IdentityMerges    int                       `json:"identity_merges"`
	DedupeConflicts   int                       `json:"dedupe_conflicts"`
	LastUID           uint32                    `json:"last_uid,omitempty"`
	MoreAvailable     bool                      `json:"more_available"`
	Bootstrap         bool                      `json:"bootstrap"`
	CompletedAt       time.Time                 `json:"completed_at"`
	Checkpoint        Checkpoint                `json:"checkpoint"`
	OutboundReconcile *OutboundReconcileSummary `json:"outbound_reconcile,omitempty"`
}

type DrainStopReason string

const (
	DrainStopComplete    DrainStopReason = "complete"
	DrainStopMaxPages    DrainStopReason = "max_pages"
	DrainStopMaxMessages DrainStopReason = "max_messages"
	DrainStopDeadline    DrainStopReason = "deadline"
	DrainStopError       DrainStopReason = "error"
)

type SyncDrainSummary struct {
	Pages         []SyncSummary   `json:"pages"`
	PageCount     int             `json:"page_count"`
	Fetched       int             `json:"fetched"`
	MoreAvailable bool            `json:"more_available"`
	StopReason    DrainStopReason `json:"stop_reason"`
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

func NormalizeMessageID(value string) string {
	return strings.ToLower(normalizeMessageID(value))
}

func ProviderLocationIdentity(folderID string, uidValidity, uid uint32) string {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" || uidValidity == 0 || uid == 0 {
		return ""
	}
	return folderID + ":" + strconv.FormatUint(uint64(uidValidity), 10) + ":" + strconv.FormatUint(uint64(uid), 10)
}

func BuildContentFingerprint(accountID string, parsed ParsedMessage, internalDate time.Time, attachments []Attachment) string {
	references := canonicalIdentityTokens(parsed.References)
	recipients := make([]string, 0, len(parsed.To)+len(parsed.CC))
	for _, address := range append(append([]Address(nil), parsed.To...), parsed.CC...) {
		if value := strings.ToLower(strings.TrimSpace(address.Email)); value != "" {
			recipients = append(recipients, value)
		}
	}
	sort.Strings(recipients)
	hashes := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		if value := strings.ToLower(strings.TrimSpace(attachment.ContentHash)); value != "" {
			hashes = append(hashes, value)
		}
	}
	sort.Strings(hashes)
	moment := ""
	if parsed.HeaderDate != nil && !parsed.HeaderDate.IsZero() {
		moment = parsed.HeaderDate.UTC().Truncate(time.Minute).Format(time.RFC3339)
	} else if !internalDate.IsZero() {
		moment = internalDate.UTC().Truncate(time.Minute).Format(time.RFC3339)
	}
	parts := []string{
		strings.TrimSpace(accountID),
		strings.ToLower(strings.TrimSpace(parsed.Subject)),
		strings.ToLower(strings.TrimSpace(parsed.From.Email)),
		strings.ToLower(strings.TrimSpace(parsed.ReplyTo.Email)),
		NormalizeMessageID(parsed.InReplyTo),
		strings.Join(references, ","),
		strings.Join(recipients, ","),
		strings.Join(hashes, ","),
		moment,
	}
	blank := true
	for _, value := range parts[1:] {
		if value != "" {
			blank = false
			break
		}
	}
	if blank {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return "content:" + hex.EncodeToString(sum[:])
}

func canonicalIdentityTokens(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if normalized := NormalizeMessageID(value); normalized != "" {
			result = append(result, normalized)
		}
	}
	sort.Strings(result)
	return result
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
