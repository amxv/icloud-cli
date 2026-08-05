package folder

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrNotFound      = errors.New("folder not found")
	ErrAmbiguous     = errors.New("folder selector is ambiguous")
	ErrNotSelectable = errors.New("folder is not selectable")
)

type Kind string

const (
	KindInbox     Kind = "inbox"
	KindSent      Kind = "sent"
	KindDrafts    Kind = "drafts"
	KindArchive   Kind = "archive"
	KindTrash     Kind = "trash"
	KindJunk      Kind = "junk"
	KindAll       Kind = "all"
	KindFlagged   Kind = "flagged"
	KindImportant Kind = "important"
	KindOther     Kind = "other"
)

type Subscription string

const (
	SubscriptionUnknown      Subscription = "unknown"
	SubscriptionSubscribed   Subscription = "subscribed"
	SubscriptionUnsubscribed Subscription = "unsubscribed"
)

// Folder is one durable observation of an IMAP mailbox. Name is decoded for
// display and selection; WireName is the canonical modified UTF-7 form used
// for provider reconciliation. Neither value is a filesystem path.
type Folder struct {
	ID             string       `json:"id"`
	AccountID      string       `json:"account_id"`
	Name           string       `json:"name"`
	WireName       string       `json:"wire_name"`
	ParentName     string       `json:"parent_name,omitempty"`
	ParentWireName string       `json:"parent_wire_name,omitempty"`
	Delimiter      string       `json:"delimiter,omitempty"`
	Attributes     []string     `json:"attributes,omitempty"`
	Kind           Kind         `json:"kind"`
	KindSource     string       `json:"kind_source,omitempty"`
	Selectable     bool         `json:"selectable"`
	Subscription   Subscription `json:"subscription"`
	Present        bool         `json:"present"`
	Stale          bool         `json:"stale"`
	SyncEnabled    bool         `json:"sync_enabled"`
	Messages       uint32       `json:"messages,omitempty"`
	Unseen         uint32       `json:"unseen,omitempty"`
	UIDNext        uint32       `json:"uid_next,omitempty"`
	UIDValidity    uint32       `json:"uid_validity,omitempty"`
	DiscoveredAt   time.Time    `json:"discovered_at"`
	StatusAt       *time.Time   `json:"status_at,omitempty"`
	MissingSince   *time.Time   `json:"missing_since,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

type RemoteObservation struct {
	Name           string
	WireName       string
	OldName        string
	OldWireName    string
	ParentName     string
	ParentWireName string
	Delimiter      string
	Attributes     []string
	Kind           Kind
	KindSource     string
	Selectable     bool
	Subscription   Subscription
	Messages       uint32
	Unseen         uint32
	UIDNext        uint32
	UIDValidity    uint32
	StatusKnown    bool
}

func DefaultSyncEnabled(kind Kind) bool {
	return kind == KindInbox || kind == KindSent
}

func NormalizeKind(value string) Kind {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(KindInbox):
		return KindInbox
	case string(KindSent):
		return KindSent
	case string(KindDrafts):
		return KindDrafts
	case string(KindArchive):
		return KindArchive
	case string(KindTrash):
		return KindTrash
	case string(KindJunk):
		return KindJunk
	case string(KindAll):
		return KindAll
	case string(KindFlagged):
		return KindFlagged
	case string(KindImportant):
		return KindImportant
	default:
		return KindOther
	}
}

func NormalizeSubscription(value string) Subscription {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(SubscriptionSubscribed):
		return SubscriptionSubscribed
	case string(SubscriptionUnsubscribed):
		return SubscriptionUnsubscribed
	default:
		return SubscriptionUnknown
	}
}
