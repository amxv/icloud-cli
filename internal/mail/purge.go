package mail

import (
	"errors"
	"time"
)

var (
	ErrPurgeNotFound = errors.New("mail purge not found")
	ErrPurgeExpired  = errors.New("mail purge expired")
)

type PurgeScopeKind string

const (
	PurgeScopeTrash  PurgeScopeKind = "trash"
	PurgeScopeJunk   PurgeScopeKind = "junk"
	PurgeScopeFolder PurgeScopeKind = "folder"
)

type MailPurge struct {
	ID                string          `json:"id"`
	AccountID         string          `json:"account_id"`
	FolderID          string          `json:"folder_id"`
	ScopeKind         PurgeScopeKind  `json:"scope_kind"`
	RequestHash       string          `json:"request_hash"`
	ItemCount         int             `json:"item_count"`
	More              bool            `json:"more"`
	BlockedUnresolved int             `json:"blocked_unresolved"`
	CreatedAt         time.Time       `json:"created_at"`
	ExpiresAt         time.Time       `json:"expires_at"`
	Items             []MailPurgeItem `json:"items,omitempty"`
}

func (purge MailPurge) Expired(now time.Time) bool {
	return !purge.ExpiresAt.After(now.UTC())
}

type MailPurgeItem struct {
	Ordinal     int    `json:"ordinal"`
	MessageID   string `json:"message_id"`
	LocationID  string `json:"location_id"`
	UIDValidity uint32 `json:"uid_validity"`
	UID         uint32 `json:"uid"`
	ModSequence uint64 `json:"mod_sequence,omitempty"`
}

func (item MailPurgeItem) SelectionItem(folderID string) MailSelectionItem {
	return MailSelectionItem{
		Ordinal: item.Ordinal, MessageID: item.MessageID,
		LocationID: item.LocationID, FolderID: folderID,
		UIDValidity: item.UIDValidity, UID: item.UID, ModSequence: item.ModSequence,
	}
}

type PurgeCreateRequest struct {
	ScopeKind      PurgeScopeKind
	FolderSelector string
	Limit          int
	TTL            time.Duration
}
