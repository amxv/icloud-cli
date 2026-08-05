package account

import (
	"errors"
	"time"
)

var (
	ErrNotFound  = errors.New("account not found")
	ErrAmbiguous = errors.New("account selector is ambiguous")
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
