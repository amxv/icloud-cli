package account

import (
	"errors"
	"time"
)

var (
	ErrNotFound          = errors.New("account not found")
	ErrAmbiguous         = errors.New("account selector is ambiguous")
	ErrInvalidInput      = errors.New("account input is invalid")
	ErrConflict          = errors.New("account conflicts with existing state")
	ErrDefaultRemoval    = errors.New("default account cannot be removed while other accounts exist")
	ErrCredentialMissing = errors.New("account credential is not configured")
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
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Email            string    `json:"email"`
	IMAPUsername     string    `json:"imap_username"`
	Status           Status    `json:"status"`
	Default          bool      `json:"default"`
	CredentialScheme string    `json:"credential_scheme,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
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
