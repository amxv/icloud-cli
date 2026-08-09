package account

import (
	"errors"
	"time"

	"github.com/amxv/icloud-cli/internal/transport"
)

var (
	ErrNotFound          = errors.New("account not found")
	ErrAmbiguous         = errors.New("account selector is ambiguous")
	ErrInvalidInput      = errors.New("account input is invalid")
	ErrConflict          = errors.New("account conflicts with existing state")
	ErrInvalidCursor     = errors.New("invalid account list cursor")
	ErrDefaultRemoval    = errors.New("default account cannot be removed while other accounts exist")
	ErrCredentialMissing = errors.New("account credential is not configured")
	ErrTooManyAccounts   = errors.New("too many accounts for one bounded operation")
)

type Status string

const (
	StatusPendingValidation Status = "pending_validation"
	StatusConnected         Status = "connected"
	StatusDegraded          Status = "degraded"
	StatusDisabled          Status = "disabled"
)

// Account is one configured iCloud Mail identity. Secret values are never part
// of this read model.
type Account struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Email            string     `json:"email"`
	IMAPUsername     string     `json:"imap_username"`
	Status           Status     `json:"status"`
	Default          bool       `json:"default"`
	CredentialScheme string     `json:"credential_scheme,omitempty"`
	IMAPCapabilities []string   `json:"imap_capabilities,omitempty"`
	SMTPCapabilities []string   `json:"smtp_capabilities,omitempty"`
	LastErrorCode    string     `json:"last_error_code,omitempty"`
	LastErrorAt      *time.Time `json:"last_error_at,omitempty"`
	LastHealthyAt    *time.Time `json:"last_healthy_at,omitempty"`
	LastTestedAt     *time.Time `json:"last_tested_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type CredentialScheme string

const (
	CredentialKeyring CredentialScheme = "keyring"
	CredentialEnv     CredentialScheme = "env"
	CredentialFile    CredentialScheme = "file"
)

// Credential describes persisted credential metadata. Reference is internal
// state used for resolution and cleanup and is never serialized publicly.
type Credential struct {
	ID         string           `json:"-"`
	AccountID  string           `json:"account_id"`
	Scheme     CredentialScheme `json:"scheme"`
	Reference  string           `json:"-"`
	Configured bool             `json:"configured"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
}

type CredentialSetup struct {
	Scheme    CredentialScheme
	Reference string
	Secret    []byte
}

type AddInput struct {
	Name         string
	Email        string
	IMAPUsername string
	MakeDefault  bool
	Credential   CredentialSetup
}

type CreateRecord struct {
	AccountID        string
	CredentialID     string
	Name             string
	Email            string
	IMAPUsername     string
	MakeDefault      bool
	CredentialScheme CredentialScheme
	CredentialRef    string
	Now              time.Time
}

type ProtocolCheck struct {
	OK           bool                    `json:"ok"`
	Stage        transport.Stage         `json:"stage,omitempty"`
	Kind         transport.FailureKind   `json:"kind,omitempty"`
	Retryable    bool                    `json:"retryable,omitempty"`
	Capabilities []string                `json:"capabilities,omitempty"`
	Mailboxes    int                     `json:"mailboxes,omitempty"`
	Inbox        transport.MailboxStatus `json:"inbox,omitempty"`
}

type HealthReport struct {
	AccountID    string        `json:"account_id"`
	Status       Status        `json:"status"`
	IMAPUsername string        `json:"imap_username,omitempty"`
	IMAP         ProtocolCheck `json:"imap"`
	SMTP         ProtocolCheck `json:"smtp"`
	CheckedAt    time.Time     `json:"checked_at"`
}

type DoctorReport struct {
	Account              Account       `json:"account"`
	CredentialConfigured bool          `json:"credential_configured"`
	Live                 *HealthReport `json:"live,omitempty"`
}

type HealthUpdate struct {
	AccountID        string
	Status           Status
	IMAPUsername     string
	IMAPCapabilities []string
	SMTPCapabilities []string
	LastErrorCode    string
	CheckedAt        time.Time
	Healthy          bool
}

type ValidationError struct {
	Reports []HealthReport
	Failure *transport.Failure
}

func (err *ValidationError) Error() string {
	if err == nil || err.Failure == nil {
		return "account validation failed"
	}
	return "account validation failed at " + string(err.Failure.Stage)
}

func (err *ValidationError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Failure
}
