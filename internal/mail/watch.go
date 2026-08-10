package mail

import "time"

// MaxWatchAccounts is the explicit foreground-process worker ceiling. Account
// inventory is paged independently; this bounds concurrent provider sessions
// and goroutines rather than truncating the configured account list.
const MaxWatchAccounts = 100

type WatchMode string

const (
	WatchModeIdle    WatchMode = "idle"
	WatchModePolling WatchMode = "polling"
)

type WatchEventType string

const (
	WatchEventStartup   WatchEventType = "startup"
	WatchEventMode      WatchEventType = "mode"
	WatchEventIdle      WatchEventType = "idle"
	WatchEventChange    WatchEventType = "change"
	WatchEventSync      WatchEventType = "sync_result"
	WatchEventReconnect WatchEventType = "reconnect"
	WatchEventDegraded  WatchEventType = "degraded"
	WatchEventStopped   WatchEventType = "stopped"
	WatchEventCompleted WatchEventType = "completed"
)

type WatchOptions struct {
	AllAccounts       bool
	Limit             int
	PollInterval      time.Duration
	ReconcileInterval time.Duration
	OperationTimeout  time.Duration
	ReconnectMin      time.Duration
	ReconnectMax      time.Duration
}

type WatchError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Stage     string `json:"stage,omitempty"`
}

type WatchEvent struct {
	Type              WatchEventType `json:"event"`
	Sequence          uint64         `json:"sequence"`
	AccountID         string         `json:"account_id"`
	Timestamp         time.Time      `json:"timestamp"`
	FolderID          string         `json:"folder_id,omitempty"`
	FolderName        string         `json:"folder_name,omitempty"`
	Mode              WatchMode      `json:"mode,omitempty"`
	Reason            string         `json:"reason,omitempty"`
	Capabilities      []string       `json:"capabilities,omitempty"`
	Attempt           int            `json:"attempt,omitempty"`
	Backoff           time.Duration  `json:"-"`
	PollInterval      time.Duration  `json:"-"`
	ReconcileInterval time.Duration  `json:"-"`
	Summary           *SyncSummary   `json:"summary,omitempty"`
	Error             *WatchError    `json:"error,omitempty"`
	Quiet             bool           `json:"-"`
}
