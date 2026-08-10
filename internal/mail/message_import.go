package mail

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidMessageImport  = errors.New("invalid message import")
	ErrMessageImportNotFound = errors.New("message import not found")
	ErrMessageImportConflict = errors.New("message import conflicts with existing state")
)

type MessageImportStatus string

const (
	MessageImportPending          MessageImportStatus = "pending"
	MessageImportAppendUnresolved MessageImportStatus = "append_unresolved"
	MessageImportAppended         MessageImportStatus = "appended"
	MessageImportConfirmed        MessageImportStatus = "confirmed"
	MessageImportFailed           MessageImportStatus = "failed"
)

type MessageImportRequest struct {
	Folder         string     `json:"folder"`
	InternalDate   *time.Time `json:"internal_date,omitempty"`
	Flags          []string   `json:"flags,omitempty"`
	IdempotencyKey string     `json:"idempotency_key"`
}

type MessageImportPlan struct {
	FolderID        string     `json:"folder_id"`
	FolderName      string     `json:"folder_name"`
	RawContentHash  string     `json:"raw_content_hash"`
	RawByteSize     int64      `json:"raw_byte_size"`
	RFCMessageID    string     `json:"rfc_message_id,omitempty"`
	Subject         string     `json:"subject,omitempty"`
	AttachmentCount int        `json:"attachment_count"`
	Flags           []string   `json:"flags,omitempty"`
	InternalDate    *time.Time `json:"internal_date,omitempty"`
	RequestHash     string     `json:"request_hash"`
	IdempotencyKey  string     `json:"-"`
}

type MessageImport struct {
	ID                     string              `json:"id"`
	AccountID              string              `json:"account_id"`
	FolderID               string              `json:"folder_id"`
	RawContentHash         string              `json:"raw_content_hash"`
	RawStorageKey          string              `json:"-"`
	RawByteSize            int64               `json:"raw_byte_size"`
	RFCMessageID           string              `json:"rfc_message_id,omitempty"`
	InternalDate           time.Time           `json:"internal_date"`
	Flags                  []string            `json:"flags,omitempty"`
	BaselineUIDValidity    uint32              `json:"baseline_uid_validity"`
	BaselineUIDNext        uint32              `json:"baseline_uid_next"`
	CandidateUIDUpper      uint32              `json:"candidate_uid_upper,omitempty"`
	ProviderUIDValidity    uint32              `json:"provider_uid_validity,omitempty"`
	ProviderUID            uint32              `json:"provider_uid,omitempty"`
	MessageID              string              `json:"message_id,omitempty"`
	LocationID             string              `json:"location_id,omitempty"`
	RequestHash            string              `json:"-"`
	IdempotencyKey         string              `json:"-"`
	Status                 MessageImportStatus `json:"status"`
	ProviderMethod         string              `json:"provider_method,omitempty"`
	ProviderRawVariant     string              `json:"provider_raw_variant,omitempty"`
	ReconciliationRequired bool                `json:"reconciliation_required"`
	ErrorCode              string              `json:"error_code,omitempty"`
	CreatedAt              time.Time           `json:"created_at"`
	UpdatedAt              time.Time           `json:"updated_at"`
}

type BeginMessageImport struct {
	ImportID            string
	AccountID           string
	FolderID            string
	RawContentHash      string
	RawStorageKey       string
	RawByteSize         int64
	RFCMessageID        string
	InternalDate        time.Time
	Flags               []string
	BaselineUIDValidity uint32
	BaselineUIDNext     uint32
	RequestHash         string
	IdempotencyKey      string
	Now                 time.Time
}

func NormalizeImportFlags(values []string) ([]string, error) {
	seen := map[string]bool{}
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "seen", `\seen`:
			seen[`\Seen`] = true
		case "flagged", `\flagged`:
			seen[`\Flagged`] = true
		case "answered", `\answered`:
			seen[`\Answered`] = true
		case "":
			continue
		default:
			return nil, ErrInvalidMessageImport
		}
	}
	ordered := []string{`\Seen`, `\Flagged`, `\Answered`}
	result := make([]string, 0, len(seen))
	for _, value := range ordered {
		if seen[value] {
			result = append(result, value)
		}
	}
	return result, nil
}

func MessageImportHash(accountID string, plan MessageImportPlan, explicitInternalDate bool) (string, error) {
	date := ""
	if explicitInternalDate && plan.InternalDate != nil {
		date = plan.InternalDate.UTC().Format(time.RFC3339Nano)
	}
	payload, err := json.Marshal(struct {
		AccountID       string   `json:"account_id"`
		FolderID        string   `json:"folder_id"`
		RawContentHash  string   `json:"raw_content_hash"`
		RawByteSize     int64    `json:"raw_byte_size"`
		Flags           []string `json:"flags,omitempty"`
		InternalDate    string   `json:"internal_date,omitempty"`
		DateWasExplicit bool     `json:"date_was_explicit"`
	}{
		AccountID: strings.TrimSpace(accountID), FolderID: strings.TrimSpace(plan.FolderID),
		RawContentHash: strings.ToLower(strings.TrimSpace(plan.RawContentHash)), RawByteSize: plan.RawByteSize,
		Flags: append([]string(nil), plan.Flags...), InternalDate: date, DateWasExplicit: explicitInternalDate,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
