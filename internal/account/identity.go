package account

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrIdentityNotFound        = errors.New("mail identity not found")
	ErrIdentityConflict        = errors.New("mail identity conflicts with existing state")
	ErrIdentityInvalidCursor   = errors.New("invalid mail identity list cursor")
	ErrIdentityProtected       = errors.New("mail identity is protected")
	ErrIdentityDisabled        = errors.New("mail identity is disabled")
	ErrIdentityNoEnabledSender = errors.New("mail identity change would leave no enabled sender")
)

type MailIdentityKind string

const (
	MailIdentityPrimary      MailIdentityKind = "primary"
	MailIdentityICloudAlias  MailIdentityKind = "icloud_alias"
	MailIdentityCustomDomain MailIdentityKind = "custom_domain"
	MailIdentityHideMyEmail  MailIdentityKind = "hide_my_email"
	MailIdentityOther        MailIdentityKind = "other"
)

type MailIdentityProvenance string

const (
	MailIdentityMigratedPrimary MailIdentityProvenance = "migrated_primary"
	MailIdentityAccountPrimary  MailIdentityProvenance = "account_primary"
	MailIdentityUserConfigured  MailIdentityProvenance = "user_configured"
)

type MailIdentityVerification string

const (
	MailIdentityUnverified MailIdentityVerification = "unverified"
	MailIdentityVerified   MailIdentityVerification = "verified"
	MailIdentityRejected   MailIdentityVerification = "rejected"
)

// MailIdentity is an account-owned address that counts as self and may become
// eligible for outbound sending. Authentication credentials remain owned by
// Account and never move into this model.
type MailIdentity struct {
	ID           string                   `json:"id"`
	AccountID    string                   `json:"account_id"`
	Address      string                   `json:"address"`
	DisplayName  string                   `json:"display_name"`
	Kind         MailIdentityKind         `json:"kind"`
	Provenance   MailIdentityProvenance   `json:"provenance"`
	Verification MailIdentityVerification `json:"verification"`
	Enabled      bool                     `json:"enabled"`
	Default      bool                     `json:"default"`
	CreatedAt    time.Time                `json:"created_at"`
	UpdatedAt    time.Time                `json:"updated_at"`
}

type MailIdentityCreateRecord struct {
	IdentityID   string
	AccountID    string
	Address      string
	DisplayName  string
	Kind         MailIdentityKind
	Provenance   MailIdentityProvenance
	Verification MailIdentityVerification
	Enabled      bool
	MakeDefault  bool
	Now          time.Time
}

type MailIdentityAddInput struct {
	Address     string
	DisplayName string
	Kind        MailIdentityKind
	MakeDefault bool
}

type MailIdentityUpdateInput struct {
	Address     *string
	DisplayName *string
	Kind        *MailIdentityKind
}

type DirectionReclassificationSummary struct {
	AccountID string `json:"account_id"`
	Selected  int    `json:"selected"`
	Promoted  int    `json:"promoted"`
	More      bool   `json:"more"`
	Pending   bool   `json:"pending"`
}

func NormalizeMailIdentityKind(value MailIdentityKind) (MailIdentityKind, error) {
	value = MailIdentityKind(strings.ToLower(strings.TrimSpace(string(value))))
	switch value {
	case MailIdentityPrimary, MailIdentityICloudAlias, MailIdentityCustomDomain, MailIdentityHideMyEmail, MailIdentityOther:
		return value, nil
	default:
		return "", fmt.Errorf("%w: identity kind must be primary, icloud_alias, custom_domain, hide_my_email, or other", ErrInvalidInput)
	}
}

func NormalizeMailIdentityDisplayName(value string) (string, error) {
	return NormalizeName(value)
}
