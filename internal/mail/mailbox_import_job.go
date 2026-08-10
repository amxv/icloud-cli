package mail

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrMailboxImportJobNotFound = errors.New("mailbox import job not found")
	ErrMailboxImportJobConflict = errors.New("mailbox import job conflicts with durable state")
)

type MailboxImportJobStatus string
type MailboxImportItemStatus string

const (
	MailboxImportJobRunning   MailboxImportJobStatus = "running"
	MailboxImportJobPartial   MailboxImportJobStatus = "partial"
	MailboxImportJobConfirmed MailboxImportJobStatus = "confirmed"

	MailboxImportItemPrepared  MailboxImportItemStatus = "prepared"
	MailboxImportItemConfirmed MailboxImportItemStatus = "confirmed"
	MailboxImportItemFailed    MailboxImportItemStatus = "failed"
)

type MailboxImportRequest struct {
	Folder      string `json:"folder"`
	Mode        string `json:"mode,omitempty"`
	MaxMessages int    `json:"max_messages,omitempty"`
}

type MailboxImportPlan struct {
	FolderID          string `json:"folder_id"`
	FolderName        string `json:"folder_name"`
	SourceContentHash string `json:"source_content_hash"`
	SourceByteSize    int64  `json:"source_byte_size"`
	StartOffset       int64  `json:"start_offset"`
	StartIndex        int    `json:"start_index"`
	MaxMessages       int    `json:"max_messages"`
	Mode              string `json:"mode"`
	ExistingJobID     string `json:"existing_job_id,omitempty"`
	AlreadyImported   int    `json:"already_imported"`
	Complete          bool   `json:"complete"`
}

type MailboxImportJob struct {
	ID                string                 `json:"id"`
	AccountID         string                 `json:"account_id"`
	FolderID          string                 `json:"folder_id"`
	SourcePath        string                 `json:"-"`
	SourceContentHash string                 `json:"-"`
	SourceByteSize    int64                  `json:"source_byte_size"`
	NextOffset        int64                  `json:"next_offset"`
	NextIndex         int                    `json:"next_index"`
	ImportedCount     int                    `json:"imported_count"`
	Status            MailboxImportJobStatus `json:"status"`
	ErrorCode         string                 `json:"error_code,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

type MailboxImportItem struct {
	JobID          string                  `json:"job_id"`
	Index          int                     `json:"index"`
	StartOffset    int64                   `json:"start_offset"`
	NextOffset     int64                   `json:"next_offset"`
	RawContentHash string                  `json:"-"`
	RawByteSize    int64                   `json:"raw_byte_size"`
	IdempotencyKey string                  `json:"-"`
	Status         MailboxImportItemStatus `json:"status"`
	AttemptCount   int                     `json:"attempt_count"`
	RetryNewKey    bool                    `json:"-"`
	ImportID       string                  `json:"import_id,omitempty"`
	MessageID      string                  `json:"message_id,omitempty"`
	LocationID     string                  `json:"location_id,omitempty"`
	ErrorCode      string                  `json:"error_code,omitempty"`
	CreatedAt      time.Time               `json:"created_at"`
	UpdatedAt      time.Time               `json:"updated_at"`
}

type BeginMailboxImportJob struct {
	JobID             string
	AccountID         string
	FolderID          string
	SourcePath        string
	SourceContentHash string
	SourceByteSize    int64
	Now               time.Time
}

type PrepareMailboxImportItem struct {
	JobID          string
	Index          int
	StartOffset    int64
	NextOffset     int64
	RawContentHash string
	RawByteSize    int64
	Now            time.Time
}

func MailboxImportItemKey(jobID string, index, attempt int, rawHash string) string {
	rawHash = strings.ToLower(strings.TrimSpace(rawHash))
	if len(rawHash) > 16 {
		rawHash = rawHash[:16]
	}
	return "mbox:" + strings.TrimSpace(jobID) + ":" + itoa(index) + ":" + itoa(attempt) + ":" + rawHash
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var buffer [24]byte
	position := len(buffer)
	for value > 0 {
		position--
		buffer[position] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		position--
		buffer[position] = '-'
	}
	return string(buffer[position:])
}

type MailboxImportSummary struct {
	JobID          string                 `json:"job_id"`
	FolderID       string                 `json:"folder_id"`
	Mode           string                 `json:"mode"`
	Status         MailboxImportJobStatus `json:"status"`
	Attempted      int                    `json:"attempted"`
	Confirmed      int                    `json:"confirmed"`
	Failed         int                    `json:"failed"`
	Reused         int                    `json:"reused"`
	ImportedCount  int                    `json:"imported_count"`
	NextIndex      int                    `json:"next_index"`
	NextOffset     int64                  `json:"next_offset"`
	SourceByteSize int64                  `json:"source_byte_size"`
	Partial        bool                   `json:"partial"`
	More           bool                   `json:"more"`
	Complete       bool                   `json:"complete"`
}

type MailboxImportRun struct {
	Job     MailboxImportJob          `json:"job"`
	Items   []MailboxImportItemResult `json:"items,omitempty"`
	Summary MailboxImportSummary      `json:"summary"`
}

type MailboxImportItemResult struct {
	Index       int    `json:"index"`
	StartOffset int64  `json:"start_offset"`
	NextOffset  int64  `json:"next_offset"`
	ImportID    string `json:"import_id,omitempty"`
	MessageID   string `json:"message_id,omitempty"`
	LocationID  string `json:"location_id,omitempty"`
	Status      string `json:"status"`
	ErrorCode   string `json:"error_code,omitempty"`
	Reused      bool   `json:"reused,omitempty"`
}
