package transport

import (
	"context"
	"errors"
	"io"
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
	StageIMAPAppend     Stage = "imap_append"
	StageIMAPStore      Stage = "imap_store"
	StageIMAPLogout     Stage = "imap_logout"
	StageSMTPGreeting   Stage = "smtp_greeting"
	StageSMTPStartTLS   Stage = "smtp_starttls"
	StageSMTPAuth       Stage = "smtp_auth"
	StageSMTPMail       Stage = "smtp_mail"
	StageSMTPRecipient  Stage = "smtp_recipient"
	StageSMTPData       Stage = "smtp_data"
	StageSMTPWrite      Stage = "smtp_write"
	StageSMTPAccept     Stage = "smtp_accept"
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
	TailOnlyBootstrap   bool
	Limit               int
	ReconcileUIDs       []uint32
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
	ModSequence  uint64          `json:"mod_sequence,omitempty"`
	Envelope     MessageEnvelope `json:"envelope,omitempty"`
	Raw          *blob.Staged    `json:"-"`
}

type LocationState struct {
	UID         uint32   `json:"uid"`
	Flags       []string `json:"flags,omitempty"`
	ModSequence uint64   `json:"mod_sequence,omitempty"`
	Present     bool     `json:"present"`
}

type InboxPage struct {
	Mailbox          string           `json:"mailbox"`
	UIDValidity      uint32           `json:"uid_validity"`
	LastCommittedUID uint32           `json:"last_committed_uid"`
	LastScannedUID   uint32           `json:"last_scanned_uid"`
	MoreAvailable    bool             `json:"more_available"`
	Bootstrap        bool             `json:"bootstrap"`
	Messages         []FetchedMessage `json:"messages"`
	LocationStates   []LocationState  `json:"location_states,omitempty"`
}

type UIDValidityError struct {
	Expected uint32
	Observed uint32
}

func (err *UIDValidityError) Error() string { return "IMAP UID validity changed" }

type IMAPInboxClient interface {
	FetchInboxPage(context.Context, AuthRequest, InboxFetchRequest, blob.Stager) (InboxPage, error)
}

type SentAppendRequest struct {
	Mailbox      string
	Message      io.Reader
	Size         int64
	InternalDate time.Time
}

type SentAppendResult struct {
	Mailbox     string `json:"mailbox"`
	UIDValidity uint32 `json:"uid_validity,omitempty"`
	UID         uint32 `json:"uid,omitempty"`
}

type IMAPSentAppender interface {
	AppendSent(context.Context, AuthRequest, SentAppendRequest) (SentAppendResult, error)
}

type RemoteSearchCriteria struct {
	Text         []string
	Sender       string
	Participant  string
	Subject      string
	RFCMessageID string
	Unread       *bool
	Flagged      *bool
	After        *time.Time
	Before       *time.Time
}

type RemoteSearchRequest struct {
	Mailbox             string
	ExpectedUIDValidity uint32
	Criteria            RemoteSearchCriteria
	Limit               int
	MaxCandidateUIDs    int
}

type RemoteMessageSummary struct {
	UID          uint32
	Flags        []string
	InternalDate time.Time
	RFC822Size   int64
	Envelope     MessageEnvelope
	Snippet      string
}

type RemoteSearchPage struct {
	Mailbox       string
	UIDValidity   uint32
	Messages      []RemoteMessageSummary
	MoreAvailable bool
	Partial       bool
	CandidateUIDs int
}

type RemoteFetchRequest struct {
	Mailbox             string
	ExpectedUIDValidity uint32
	UID                 uint32
}

type RemoteFetchResult struct {
	Mailbox     string
	UIDValidity uint32
	Message     FetchedMessage
}

type RemoteSummaryResult struct {
	Mailbox     string
	UIDValidity uint32
	Message     RemoteMessageSummary
}

type IMAPRemoteClient interface {
	SearchRemote(context.Context, AuthRequest, RemoteSearchRequest) (RemoteSearchPage, error)
	FetchRemoteSummary(context.Context, AuthRequest, RemoteFetchRequest) (RemoteSummaryResult, error)
	FetchRemoteMessage(context.Context, AuthRequest, RemoteFetchRequest, blob.Stager) (RemoteFetchResult, error)
}

type FlagMutationRequest struct {
	Mailbox             string
	ExpectedUIDValidity uint32
	UID                 uint32
	ExpectedModSequence uint64
	Flag                string
	Add                 bool
}

type FlagMutationResult struct {
	Mailbox     string
	UIDValidity uint32
	UID         uint32
	Flags       []string
	ModSequence uint64
	Present     bool
}

type IMAPFlagClient interface {
	UpdateFlags(context.Context, AuthRequest, FlagMutationRequest) (FlagMutationResult, error)
}

type IMAPFolderClient interface {
	DiscoverFolders(context.Context, AuthRequest) (FolderDiscoveryResult, error)
}

type SMTPValidator interface {
	ValidateSMTP(context.Context, AuthRequest) (SMTPResult, error)
}

type SMTPEnvelope struct {
	From       string
	Recipients []string
}

type SMTPSendRequest struct {
	Envelope SMTPEnvelope
	Message  io.Reader
}

type SMTPSendResult struct {
	Accepted   bool
	StatusText string
}

type SMTPSendOutcome string

const (
	SMTPSendFailed  SMTPSendOutcome = "failed"
	SMTPSendUnknown SMTPSendOutcome = "unknown"
)

type SMTPSendError struct {
	Outcome SMTPSendOutcome
	Failure *Failure
}

func (err *SMTPSendError) Error() string {
	if err == nil || err.Failure == nil {
		return "SMTP send failed"
	}
	return err.Failure.Error()
}

func (err *SMTPSendError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Failure
}

func AsSMTPSendError(err error) (*SMTPSendError, bool) {
	var sendErr *SMTPSendError
	if errors.As(err, &sendErr) {
		return sendErr, true
	}
	return nil, false
}

type SMTPSender interface {
	SendSMTP(context.Context, AuthRequest, SMTPSendRequest) (SMTPSendResult, error)
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
