package mail

import (
	"errors"
	"time"
)

var (
	ErrSelectionNotFound = errors.New("mail selection not found")
	ErrSelectionExpired  = errors.New("mail selection expired")
)

type SelectionSourceKind string

const (
	SelectionSourceExplicit SelectionSourceKind = "explicit"
	SelectionSourceSearch   SelectionSourceKind = "search"
)

// MailSelection is a short-lived immutable reviewed target set. SourceQueryHash
// identifies how the set was produced without retaining the user's query text.
// RequestHash binds the ordered logical/location evidence itself.
type MailSelection struct {
	ID                string              `json:"id"`
	AccountID         string              `json:"account_id"`
	SourceKind        SelectionSourceKind `json:"source_kind"`
	SourceDescription string              `json:"source_description"`
	SourceQueryHash   string              `json:"source_query_hash"`
	ScopeFolderID     string              `json:"scope_folder_id,omitempty"`
	RequestHash       string              `json:"request_hash"`
	ItemCount         int                 `json:"item_count"`
	CreatedAt         time.Time           `json:"created_at"`
	ExpiresAt         time.Time           `json:"expires_at"`
	Items             []MailSelectionItem `json:"items,omitempty"`
}

func (selection MailSelection) Expired(now time.Time) bool {
	return !selection.ExpiresAt.After(now.UTC())
}

// MailSelectionItem stores stable logical identity and, when the selection was
// created with a provider-copy scope, the exact reviewed location evidence.
// It intentionally stores no subject, participant, body, or raw-content data.
type MailSelectionItem struct {
	Ordinal     int    `json:"ordinal"`
	MessageID   string `json:"message_id"`
	LocationID  string `json:"location_id,omitempty"`
	FolderID    string `json:"folder_id,omitempty"`
	UIDValidity uint32 `json:"uid_validity,omitempty"`
	UID         uint32 `json:"uid,omitempty"`
	ModSequence uint64 `json:"mod_sequence,omitempty"`
}

type SelectionCreateRequest struct {
	Targets           []string
	Search            *SearchOptions
	SearchFolder      string
	ScopeFolder       string
	TTL               time.Duration
	MaximumMembership int
}

type SelectionApplyPreview struct {
	SelectionID   string                 `json:"selection_id"`
	SelectionHash string                 `json:"selection_hash"`
	Action        string                 `json:"action"`
	Mode          string                 `json:"mode"`
	RequestHash   string                 `json:"request_hash"`
	Count         int                    `json:"count"`
	Folders       []string               `json:"folders,omitempty"`
	LocationPlans []LocationMutationPlan `json:"location_plans,omitempty"`
	ExpiresAt     time.Time              `json:"expires_at"`
	SchemaVersion int64                  `json:"schema_version,omitempty"`
}
