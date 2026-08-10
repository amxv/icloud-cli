package mail

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidMessageRewrite  = errors.New("invalid message rewrite")
	ErrMessageRewriteNotFound = errors.New("message rewrite not found")
	ErrMessageRewriteConflict = errors.New("message rewrite conflicts with existing state")
)

type MessageRewriteStatus string

const (
	MessageRewritePending          MessageRewriteStatus = "pending"
	MessageRewriteAppendUnresolved MessageRewriteStatus = "append_unresolved"
	MessageRewriteAppended         MessageRewriteStatus = "appended"
	MessageRewriteDeleteUnresolved MessageRewriteStatus = "delete_unresolved"
	MessageRewriteCommitPending    MessageRewriteStatus = "commit_pending"
	MessageRewriteConfirmed        MessageRewriteStatus = "confirmed"
	MessageRewriteFailed           MessageRewriteStatus = "failed"
)

type MessageRewritePlan struct {
	MessageID             string       `json:"message_id"`
	SourceLocation        Location     `json:"source_location"`
	SourceFolderID        string       `json:"source_folder_id"`
	SourceFolderName      string       `json:"source_folder_name"`
	RemovedAttachments    []Attachment `json:"removed_attachments"`
	BeforeAttachmentCount int          `json:"before_attachment_count"`
	AfterAttachmentCount  int          `json:"after_attachment_count"`
	BeforeRawBytes        int64        `json:"before_raw_bytes"`
	AfterRawBytes         int64        `json:"after_raw_bytes"`
	NewRawContentHash     string       `json:"new_raw_content_hash"`
	RequestHash           string       `json:"request_hash"`
	IdempotencyKey        string       `json:"-"`
}

type AttachmentRemoveRequest struct {
	MessageID      string   `json:"message_id"`
	AttachmentIDs  []string `json:"attachment_ids"`
	Folder         string   `json:"folder,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
}

type MessageRewrite struct {
	ID                     string               `json:"id"`
	AccountID              string               `json:"account_id"`
	MessageID              string               `json:"message_id"`
	SourceLocationID       string               `json:"source_location_id"`
	SourceFolderID         string               `json:"source_folder_id"`
	SourceUIDValidity      uint32               `json:"source_uid_validity"`
	SourceUID              uint32               `json:"source_uid"`
	SourceModSequence      uint64               `json:"source_mod_sequence,omitempty"`
	OldRawContentHash      string               `json:"old_raw_content_hash"`
	OldRawStorageKey       string               `json:"-"`
	OldRawByteSize         int64                `json:"old_raw_byte_size"`
	NewRawContentHash      string               `json:"new_raw_content_hash"`
	NewRawStorageKey       string               `json:"-"`
	NewRawByteSize         int64                `json:"new_raw_byte_size"`
	BeforeAttachmentCount  int                  `json:"before_attachment_count"`
	AfterAttachmentCount   int                  `json:"after_attachment_count"`
	RemovedAttachments     []Attachment         `json:"removed_attachments"`
	ProposedLocationID     string               `json:"-"`
	NewLocationID          string               `json:"new_location_id,omitempty"`
	NewUIDValidity         uint32               `json:"new_uid_validity,omitempty"`
	NewUID                 uint32               `json:"new_uid,omitempty"`
	NewModSequence         uint64               `json:"new_mod_sequence,omitempty"`
	RequestHash            string               `json:"-"`
	IdempotencyKey         string               `json:"-"`
	Status                 MessageRewriteStatus `json:"status"`
	ProviderMethod         string               `json:"provider_method,omitempty"`
	SourcePresent          bool                 `json:"source_present"`
	ReconciliationRequired bool                 `json:"reconciliation_required"`
	ErrorCode              string               `json:"error_code,omitempty"`
	CreatedAt              time.Time            `json:"created_at"`
	UpdatedAt              time.Time            `json:"updated_at"`
}

type BeginMessageRewrite struct {
	RewriteID             string
	AccountID             string
	MessageID             string
	SourceLocationID      string
	SourceFolderID        string
	SourceUIDValidity     uint32
	SourceUID             uint32
	SourceModSequence     uint64
	OldRawContentHash     string
	OldRawStorageKey      string
	OldRawByteSize        int64
	NewRawContentHash     string
	NewRawStorageKey      string
	NewRawByteSize        int64
	BeforeAttachmentCount int
	AfterAttachmentCount  int
	RemovedAttachments    []Attachment
	ProposedLocationID    string
	RequestHash           string
	IdempotencyKey        string
	Now                   time.Time
}

type ConfirmRewriteAppend struct {
	RewriteID      string
	LocationID     string
	UIDValidity    uint32
	UID            uint32
	ModSequence    uint64
	ProviderMethod string
	Now            time.Time
}

type ConfirmRewriteDelete struct {
	RewriteID         string
	SourceFlags       LocationFlags
	SourceModSequence uint64
	ProviderMethod    string
	Now               time.Time
}

type FinalizeMessageRewrite struct {
	RewriteID    string
	Content      NormalizedContent
	Parsed       ParsedMessage
	Direction    Direction
	InternalDate time.Time
	Now          time.Time
}

func MessageRewriteHash(plan MessageRewritePlan) (string, error) {
	ids := make([]string, 0, len(plan.RemovedAttachments))
	for _, attachment := range plan.RemovedAttachments {
		ids = append(ids, strings.TrimSpace(attachment.ID))
	}
	sort.Strings(ids)
	payload, err := json.Marshal(struct {
		MessageID         string   `json:"message_id"`
		SourceLocationID  string   `json:"source_location_id"`
		SourceUIDValidity uint32   `json:"source_uid_validity"`
		SourceUID         uint32   `json:"source_uid"`
		OldRawHash        string   `json:"old_raw_hash"`
		NewRawHash        string   `json:"new_raw_hash"`
		AttachmentIDs     []string `json:"attachment_ids"`
	}{
		MessageID: strings.TrimSpace(plan.MessageID), SourceLocationID: strings.TrimSpace(plan.SourceLocation.ID),
		SourceUIDValidity: plan.SourceLocation.UIDValidity, SourceUID: plan.SourceLocation.UID,
		OldRawHash: strings.ToLower(strings.TrimSpace(plan.SourceLocation.RawContentHash)),
		NewRawHash: strings.ToLower(strings.TrimSpace(plan.NewRawContentHash)), AttachmentIDs: ids,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
