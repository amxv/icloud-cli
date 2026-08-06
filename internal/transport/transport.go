package transport

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/blob"
)

type Stage string

const (
	StageCredential     Stage = "credential"
	StageDNS            Stage = "dns"
	StageConnect        Stage = "connect"
	StageTLS            Stage = "tls"
	StageIMAPGreeting   Stage = "imap_greeting"
	StageIMAPCapability Stage = "imap_capability"
	StageIMAPAuth       Stage = "imap_auth"
	StageIMAPList       Stage = "imap_list"
	StageIMAPStatus     Stage = "imap_status"
	StageIMAPSelect     Stage = "imap_select"
	StageIMAPSearch     Stage = "imap_search"
	StageIMAPFetch      Stage = "imap_fetch"
	StageIMAPLogout     Stage = "imap_logout"
	StageSMTPGreeting   Stage = "smtp_greeting"
	StageSMTPStartTLS   Stage = "smtp_starttls"
	StageSMTPAuth       Stage = "smtp_auth"
	StageSMTPNoop       Stage = "smtp_noop"
	StageSMTPQuit       Stage = "smtp_quit"
	StageTimeout        Stage = "timeout"
)

type FailureKind string

const (
	FailureConfiguration  FailureKind = "configuration"
	FailureAuthentication FailureKind = "authentication"
	FailureUnavailable    FailureKind = "unavailable"
)

// Failure is a redacted protocol failure. Cause is retained only for
// errors.Is/errors.As and is never serialized or returned in public output.
type Failure struct {
	Stage     Stage       `json:"stage"`
	Kind      FailureKind `json:"kind"`
	Retryable bool        `json:"retryable"`
	cause     error
}

func NewFailure(stage Stage, kind FailureKind, retryable bool, cause error) *Failure {
	return &Failure{Stage: stage, Kind: kind, Retryable: retryable, cause: cause}
}

func (failure *Failure) Error() string {
	if failure == nil {
		return ""
	}
	return string(failure.Stage) + " failed"
}

func (failure *Failure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

func AsFailure(err error) (*Failure, bool) {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure, true
	}
	return nil, false
}

type AuthRequest struct {
	Username string
	Password []byte
}

type MailboxStatus struct {
	Mailbox     string `json:"mailbox"`
	Messages    uint32 `json:"messages"`
	Unseen      uint32 `json:"unseen"`
	UIDNext     uint32 `json:"uid_next"`
	UIDValidity uint32 `json:"uid_validity"`
}

type IMAPResult struct {
	Capabilities []string      `json:"capabilities,omitempty"`
	Mailboxes    int           `json:"mailboxes"`
	Inbox        MailboxStatus `json:"inbox"`
}

type RemoteFolder struct {
	Name           string         `json:"name"`
	WireName       string         `json:"wire_name"`
	OldName        string         `json:"old_name,omitempty"`
	OldWireName    string         `json:"old_wire_name,omitempty"`
	ParentName     string         `json:"parent_name,omitempty"`
	ParentWireName string         `json:"parent_wire_name,omitempty"`
	Delimiter      string         `json:"delimiter,omitempty"`
	Attributes     []string       `json:"attributes,omitempty"`
	Kind           string         `json:"kind"`
	KindSource     string         `json:"kind_source,omitempty"`
	Selectable     bool           `json:"selectable"`
	Subscription   string         `json:"subscription"`
	Status         *MailboxStatus `json:"status,omitempty"`
}

type FolderDiscoveryResult struct {
	Capabilities []string       `json:"capabilities,omitempty"`
	Folders      []RemoteFolder `json:"folders"`
}

type SMTPResult struct {
	Capabilities []string `json:"capabilities,omitempty"`
}

type IMAPValidator interface {
	ValidateIMAP(context.Context, AuthRequest) (IMAPResult, error)
}

type InboxFetchRequest struct {
	Mailbox             string
	ExpectedUIDValidity uint32
	LastCommittedUID    uint32
	LastScannedUID      uint32
	Bootstrap           bool
	Limit               int
}

type EnvelopeAddress struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

type MessageEnvelope struct {
	Date      time.Time         `json:"date,omitempty"`
	Subject   string            `json:"subject,omitempty"`
	From      []EnvelopeAddress `json:"from,omitempty"`
	ReplyTo   []EnvelopeAddress `json:"reply_to,omitempty"`
	To        []EnvelopeAddress `json:"to,omitempty"`
	CC        []EnvelopeAddress `json:"cc,omitempty"`
	BCC       []EnvelopeAddress `json:"bcc,omitempty"`
	InReplyTo []string          `json:"in_reply_to,omitempty"`
	MessageID string            `json:"message_id,omitempty"`
}

type FetchedMessage struct {
	UID          uint32          `json:"uid"`
	Flags        []string        `json:"flags,omitempty"`
	InternalDate time.Time       `json:"internal_date,omitempty"`
	RFC822Size   int64           `json:"rfc822_size,omitempty"`
	Envelope     MessageEnvelope `json:"envelope,omitempty"`
	Raw          *blob.Staged    `json:"-"`
}

type InboxPage struct {
	Mailbox          string           `json:"mailbox"`
	UIDValidity      uint32           `json:"uid_validity"`
	LastCommittedUID uint32           `json:"last_committed_uid"`
	LastScannedUID   uint32           `json:"last_scanned_uid"`
	MoreAvailable    bool             `json:"more_available"`
	Bootstrap        bool             `json:"bootstrap"`
	Messages         []FetchedMessage `json:"messages"`
}

type UIDValidityError struct {
	Expected uint32
	Observed uint32
}

func (err *UIDValidityError) Error() string { return "IMAP UID validity changed" }

type IMAPInboxClient interface {
	FetchInboxPage(context.Context, AuthRequest, InboxFetchRequest, blob.Stager) (InboxPage, error)
}

type IMAPFolderClient interface {
	DiscoverFolders(context.Context, AuthRequest) (FolderDiscoveryResult, error)
}

type SMTPValidator interface {
	ValidateSMTP(context.Context, AuthRequest) (SMTPResult, error)
}

func SanitizeCapabilities(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "" || len(value) > 64 || !safeCapability(value) {
			continue
		}
		set[value] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	if len(result) > 32 {
		result = result[:32]
	}
	return result
}

func safeCapability(value string) bool {
	for _, character := range value {
		switch {
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case strings.ContainsRune("=._+-", character):
		default:
			return false
		}
	}
	return true
}
