package mail

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrDraftNotFound      = errors.New("mail draft not found")
	ErrInvalidDraftCursor = errors.New("invalid mail draft cursor")
)

type DraftSyncState string

const (
	DraftLocal          DraftSyncState = "local"
	DraftSynced         DraftSyncState = "synced"
	DraftReconcile      DraftSyncState = "reconcile"
	DraftCleanupPending DraftSyncState = "cleanup_pending"
	DraftSendUnknown    DraftSyncState = "send_unknown"
)

type Draft struct {
	ID                 string           `json:"id"`
	AccountID          string           `json:"account_id"`
	SenderIdentityID   string           `json:"sender_identity_id,omitempty"`
	SMIMEMode          SMIMEComposeMode `json:"smime_mode,omitempty"`
	MessageID          string           `json:"message_id"`
	Revision           int              `json:"revision"`
	SyncState          DraftSyncState   `json:"sync_state"`
	RemoteLocationID   string           `json:"remote_location_id,omitempty"`
	PreviousLocationID string           `json:"previous_location_id,omitempty"`
	OutboundAttemptID  string           `json:"outbound_attempt_id,omitempty"`
	CustomHeaders      []CustomHeader   `json:"custom_headers,omitempty"`
	LastErrorCode      string           `json:"last_error_code,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
	Message            Message          `json:"message"`
}

type DraftSave struct {
	DraftID        string
	AccountID      string
	MessageID      string
	Revision       int
	Request        SendRequest
	RFCMessageID   string
	RawContentHash string
	RawStorageKey  string
	RawByteSize    int64
	Content        NormalizedContent
	Now            time.Time
}

func DraftIDForMessage(messageID string) string {
	messageID = strings.TrimSpace(messageID)
	if !strings.HasPrefix(messageID, "msg_") {
		return ""
	}
	return "drf_" + strings.TrimPrefix(messageID, "msg_")
}

type DraftSyncSummary struct {
	AccountID     string      `json:"account_id"`
	FolderID      string      `json:"folder_id"`
	FolderName    string      `json:"folder_name"`
	Observed      int         `json:"observed"`
	Appended      int         `json:"appended"`
	Reconciled    int         `json:"reconciled"`
	Cleaned       int         `json:"cleaned"`
	Pending       int         `json:"pending"`
	MoreAvailable bool        `json:"more_available"`
	FolderSync    SyncSummary `json:"folder_sync"`
	CompletedAt   time.Time   `json:"completed_at"`
}
