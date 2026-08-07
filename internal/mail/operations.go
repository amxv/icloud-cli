package mail

import (
	"errors"
	"time"
)

var ErrBackfillFrontierNotFound = errors.New("mail backfill frontier not found")

type BackfillFrontier struct {
	ID            string     `json:"id"`
	AccountID     string     `json:"account_id"`
	FolderID      string     `json:"folder_id"`
	UIDValidity   uint32     `json:"uid_validity"`
	BeforeUID     uint32     `json:"before_uid"`
	After         *time.Time `json:"after,omitempty"`
	Before        *time.Time `json:"before,omitempty"`
	Complete      bool       `json:"complete"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastErrorCode string     `json:"last_error_code,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type CommitBackfillPage struct {
	FrontierID     string
	AccountID      string
	FolderID       string
	UIDValidity    uint32
	StartBeforeUID uint32
	NextBeforeUID  uint32
	After          *time.Time
	Before         *time.Time
	Complete       bool
	AttemptedAt    time.Time
	Records        []IngestRecord
}

type BackfillCommitResult struct {
	Created         int              `json:"created"`
	Updated         int              `json:"updated"`
	IdentityMerges  int              `json:"identity_merges"`
	DedupeConflicts int              `json:"dedupe_conflicts"`
	Frontier        BackfillFrontier `json:"frontier"`
}

type BackfillSummary struct {
	AccountID       string           `json:"account_id"`
	FolderID        string           `json:"folder_id"`
	FolderName      string           `json:"folder_name"`
	Fetched         int              `json:"fetched"`
	Created         int              `json:"created"`
	Updated         int              `json:"updated"`
	IdentityMerges  int              `json:"identity_merges"`
	DedupeConflicts int              `json:"dedupe_conflicts"`
	StartBeforeUID  uint32           `json:"start_before_uid"`
	NextBeforeUID   uint32           `json:"next_before_uid"`
	Complete        bool             `json:"complete"`
	Frontier        BackfillFrontier `json:"frontier"`
}

type RebaselineSummary struct {
	AccountID          string          `json:"account_id"`
	FolderID           string          `json:"folder_id"`
	FolderName         string          `json:"folder_name"`
	UIDValidity        uint32          `json:"uid_validity"`
	PreviousCommitted  uint32          `json:"previous_committed_uid"`
	PreviousScanned    uint32          `json:"previous_scanned_uid"`
	PreviousState      CheckpointState `json:"previous_state"`
	RebaselineTail     uint32          `json:"rebaseline_tail_uid"`
	CachedMessagesKept int             `json:"cached_messages_kept"`
	Before             Checkpoint      `json:"before"`
	After              Checkpoint      `json:"after"`
}

type RepairMode string

const (
	RepairCheck RepairMode = "check"
	RepairApply RepairMode = "apply"
)

type RepairOptions struct {
	Mode           RepairMode `json:"mode"`
	Limit          int        `json:"limit"`
	CleanupOrphans bool       `json:"cleanup_orphans,omitempty"`
}

type RepairFinding struct {
	Kind       string `json:"kind"`
	ID         string `json:"id,omitempty"`
	FolderID   string `json:"folder_id,omitempty"`
	Action     string `json:"action,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Applicable bool   `json:"applicable"`
	Applied    bool   `json:"applied"`
}

type RepairSummary struct {
	AccountID              string          `json:"account_id"`
	Mode                   RepairMode      `json:"mode"`
	Checked                int             `json:"checked"`
	Changed                int             `json:"changed"`
	NextAction             string          `json:"next_action"`
	PendingSearch          int             `json:"pending_search"`
	FailedSearch           int             `json:"failed_search"`
	StaleFTSRows           int             `json:"stale_fts_rows"`
	OrphanTempFiles        int             `json:"orphan_temp_files"`
	DedupeConflicts        int             `json:"dedupe_conflicts"`
	RepairCheckpoints      int             `json:"repair_checkpoints"`
	FrontierProblems       int             `json:"frontier_problems"`
	ForeignKeyProblems     int             `json:"foreign_key_problems"`
	IntegrityProblems      int             `json:"integrity_problems"`
	LocationProblems       int             `json:"location_uniqueness_problems"`
	ThreadRedirectProblems int             `json:"thread_redirect_problems"`
	CanonicalBlobIssues    int             `json:"canonical_blob_issues"`
	OutboundUnknown        int             `json:"outbound_unknown"`
	CleanupSkipped         int             `json:"cleanup_skipped"`
	More                   bool            `json:"more"`
	Findings               []RepairFinding `json:"findings,omitempty"`
}
