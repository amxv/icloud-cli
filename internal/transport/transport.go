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

var ErrRemoteMessageUnavailable = errors.New("remote message is no longer available")

type Stage string

const (
	StageCredential      Stage = "credential"
	StageDNS             Stage = "dns"
	StageConnect         Stage = "connect"
	StageTLS             Stage = "tls"
	StageIMAPGreeting    Stage = "imap_greeting"
	StageIMAPCapability  Stage = "imap_capability"
	StageIMAPAuth        Stage = "imap_auth"
	StageIMAPList        Stage = "imap_list"
	StageIMAPNamespace   Stage = "imap_namespace"
	StageIMAPStatus      Stage = "imap_status"
	StageIMAPCreate      Stage = "imap_create"
	StageIMAPRename      Stage = "imap_rename"
	StageIMAPDelete      Stage = "imap_delete"
	StageIMAPSubscribe   Stage = "imap_subscribe"
	StageIMAPUnsubscribe Stage = "imap_unsubscribe"
	StageIMAPSelect      Stage = "imap_select"
	StageIMAPSearch      Stage = "imap_search"
	StageIMAPFetch       Stage = "imap_fetch"
	StageIMAPAppend      Stage = "imap_append"
	StageIMAPStore       Stage = "imap_store"
	StageIMAPCopy        Stage = "imap_copy"
	StageIMAPMove        Stage = "imap_move"
	StageIMAPExpunge     Stage = "imap_expunge"
	StageIMAPIdle        Stage = "imap_idle"
	StageIMAPLogout      Stage = "imap_logout"
	StageSMTPGreeting    Stage = "smtp_greeting"
	StageSMTPStartTLS    Stage = "smtp_starttls"
	StageSMTPAuth        Stage = "smtp_auth"
	StageSMTPMail        Stage = "smtp_mail"
	StageSMTPRecipient   Stage = "smtp_recipient"
	StageSMTPData        Stage = "smtp_data"
	StageSMTPWrite       Stage = "smtp_write"
	StageSMTPAccept      Stage = "smtp_accept"
	StageSMTPNoop        Stage = "smtp_noop"
	StageSMTPQuit        Stage = "smtp_quit"
	StageHTTPUnsubscribe Stage = "http_unsubscribe"
	StageTimeout         Stage = "timeout"
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

type FolderAdminAction string

const (
	FolderAdminCreate      FolderAdminAction = "create"
	FolderAdminRename      FolderAdminAction = "rename"
	FolderAdminDelete      FolderAdminAction = "delete"
	FolderAdminSubscribe   FolderAdminAction = "subscribe"
	FolderAdminUnsubscribe FolderAdminAction = "unsubscribe"
)

type FolderAdminRequest struct {
	Action        FolderAdminAction
	Mailbox       string
	NewMailbox    string
	AllowNonEmpty bool
}

type FolderAdminResult struct {
	Action         FolderAdminAction
	Folder         RemoteFolder
	PreviousName   string
	Deleted        bool
	SourceMessages uint32
}

type FolderNonEmptyError struct {
	Messages uint32
}

func (err *FolderNonEmptyError) Error() string { return "folder is not empty" }

type FolderAdminTargetError struct {
	TargetMailbox string
	Err           error
}

func (err *FolderAdminTargetError) Error() string {
	if err == nil || err.Err == nil {
		return "folder administration failed"
	}
	return err.Err.Error()
}

func (err *FolderAdminTargetError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
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

type BackfillFetchRequest struct {
	Mailbox             string
	ExpectedUIDValidity uint32
	BeforeUID           uint32
	After               *time.Time
	Before              *time.Time
	Limit               int
}

type BackfillPage struct {
	Mailbox       string
	UIDValidity   uint32
	BeforeUID     uint32
	NextBeforeUID uint32
	Complete      bool
	Messages      []FetchedMessage
}

type IMAPBackfillClient interface {
	FetchBackfillPage(context.Context, AuthRequest, BackfillFetchRequest, blob.Stager) (BackfillPage, error)
}

type IMAPMailboxStatusClient interface {
	MailboxStatus(context.Context, AuthRequest, string) (MailboxStatus, error)
}

type MailboxWatchMode string

const (
	MailboxWatchIdle    MailboxWatchMode = "idle"
	MailboxWatchPolling MailboxWatchMode = "polling"
)

type MailboxWatchReason string

const (
	MailboxWatchChanged   MailboxWatchReason = "change"
	MailboxWatchReconcile MailboxWatchReason = "reconcile"
	MailboxWatchPoll      MailboxWatchReason = "poll"
)

type MailboxWatchInfo struct {
	Mailbox      string
	Mode         MailboxWatchMode
	Capabilities []string
}

type IMAPMailboxWatchSession interface {
	Info() MailboxWatchInfo
	Wait(context.Context, time.Duration) (MailboxWatchReason, error)
	Close(context.Context) error
}

type IMAPWatchClient interface {
	OpenMailboxWatch(context.Context, AuthRequest, string) (IMAPMailboxWatchSession, error)
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

type MailboxAppendRequest struct {
	Mailbox      string
	Message      io.Reader
	Size         int64
	InternalDate time.Time
	Flags        []string
}

type MailboxAppendResult struct {
	Mailbox     string `json:"mailbox"`
	UIDValidity uint32 `json:"uid_validity,omitempty"`
	UID         uint32 `json:"uid,omitempty"`
}

type IMAPMailboxAppender interface {
	AppendMailbox(context.Context, AuthRequest, MailboxAppendRequest) (MailboxAppendResult, error)
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
	StartUID            uint32
	PendingUIDs         []uint32
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
	NextUID       uint32
	PendingUIDs   []uint32
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

type LocationMutationOperation string

const (
	LocationMutationMove   LocationMutationOperation = "move"
	LocationMutationCopy   LocationMutationOperation = "copy"
	LocationMutationDelete LocationMutationOperation = "delete"
)

type LocationMutationRequest struct {
	Operation                 LocationMutationOperation
	SourceMailbox             string
	DestinationMailbox        string
	ExpectedSourceUIDValidity uint32
	SourceUID                 uint32
	ExpectedSourceModSequence uint64
}

type LocationMutationResult struct {
	Operation                LocationMutationOperation
	ProviderMethod           string
	SourceMailbox            string
	SourceUIDValidity        uint32
	SourceUID                uint32
	SourceFlags              []string
	SourceModSequence        uint64
	SourcePresent            bool
	DestinationMailbox       string
	DestinationUIDValidity   uint32
	DestinationUID           uint32
	DestinationFlags         []string
	DestinationModSequence   uint64
	DestinationKnown         bool
	ReconciliationRequired   bool
	ProviderMutationObserved bool
}

type LocationStateConflictError struct {
	ExpectedModSequence uint64
	ObservedModSequence uint64
	Present             bool
}

func (err *LocationStateConflictError) Error() string { return "message location state changed" }

type IMAPLocationClient interface {
	MutateLocation(context.Context, AuthRequest, LocationMutationRequest) (LocationMutationResult, error)
}

type IMAPFolderClient interface {
	DiscoverFolders(context.Context, AuthRequest) (FolderDiscoveryResult, error)
}

type IMAPFolderAdminClient interface {
	AdministerFolder(context.Context, AuthRequest, FolderAdminRequest) (FolderAdminResult, error)
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
	// OutboxEligible is true only when the transport can prove the failure
	// happened before SMTP DATA could have started carrying message bytes.
	// It is intentionally distinct from Failure.Retryable and from the wider
	// class of explicit failed attempts a caller may choose to retry manually.
	OutboxEligible bool
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

type OneClickUnsubscriber interface {
	UnsubscribeOneClick(context.Context, string) error
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
