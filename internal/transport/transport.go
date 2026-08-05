package transport

import (
	"context"
	"errors"
	"sort"
	"strings"
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

type SMTPResult struct {
	Capabilities []string `json:"capabilities,omitempty"`
}

type IMAPValidator interface {
	ValidateIMAP(context.Context, AuthRequest) (IMAPResult, error)
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
