package cli

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

const longRunningAnnotation = "icloud.long_running"

type watchEventPayload struct {
	FolderID          string                  `json:"folder_id,omitempty"`
	FolderName        string                  `json:"folder_name,omitempty"`
	Mode              maildomain.WatchMode    `json:"mode,omitempty"`
	Reason            string                  `json:"reason,omitempty"`
	Capabilities      []string                `json:"capabilities,omitempty"`
	Attempt           int                     `json:"attempt,omitempty"`
	Backoff           string                  `json:"backoff,omitempty"`
	PollInterval      string                  `json:"poll_interval,omitempty"`
	ReconcileInterval string                  `json:"reconcile_interval,omitempty"`
	Summary           *maildomain.SyncSummary `json:"summary,omitempty"`
}

func newMailWatchCommand(state *executionState) *cobra.Command {
	var allAccounts bool
	var limit int
	var pollInterval time.Duration
	var reconcileInterval time.Duration
	var verbose bool
	var jsonl bool
	command := &cobra.Command{
		Use:         "watch",
		Short:       "Keep enabled mail folders fresh with IDLE or explicit polling fallback",
		Long:        "Keep enabled iCloud Mail folders fresh in the foreground. The command uses IMAP IDLE for high-priority enabled folders when supported, falls back explicitly to polling otherwise, periodically refreshes folder state, and stops on Ctrl-C. --timeout bounds each provider/sync attempt rather than the lifetime of this foreground stream.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{longRunningAnnotation: "true"},
		Example:     "  icloud mail watch\n  icloud mail watch --all-accounts\n  icloud mail watch --poll-interval 2m --reconcile-interval 15m\n  icloud mail watch --jsonl",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSONL: jsonl, Verbose: verbose}, OutputSupport{JSONL: true, Verbose: true}); err != nil {
				return err
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, "--limit must be between 1 and 100", false)
			}
			if pollInterval < time.Second {
				return NewAppError(ErrorUsage, "--poll-interval must be at least 1s", false)
			}
			if reconcileInterval < 10*time.Second {
				return NewAppError(ErrorUsage, "--reconcile-interval must be at least 10s", false)
			}
			if allAccounts && state.global.Account != "" {
				return NewAppError(ErrorUsage, "--all-accounts cannot be combined with --account", false)
			}

			watchCtx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stopSignals()
			writer := &watchEventWriter{state: state, verbose: verbose, jsonl: jsonl}
			return state.mail.MailWatch(watchCtx, state.global.Home, state.global.Account, maildomain.WatchOptions{
				AllAccounts:       allAccounts,
				Limit:             limit,
				PollInterval:      pollInterval,
				ReconcileInterval: reconcileInterval,
				OperationTimeout:  state.global.Timeout,
			}, writer.Write)
		},
	}
	command.Flags().BoolVar(&allAccounts, "all-accounts", false, fmt.Sprintf("Watch all configured accounts (maximum %d workers)", maildomain.MaxWatchAccounts))
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, "Maximum messages committed per folder sync (1-100)")
	command.Flags().DurationVar(&pollInterval, "poll-interval", time.Minute, "Polling interval when IDLE is unavailable")
	command.Flags().DurationVar(&reconcileInterval, "reconcile-interval", 15*time.Minute, "Interval for full enabled-folder and folder-state reconciliation")
	command.Flags().BoolVar(&verbose, "verbose", false, "Include idle and unchanged synchronization lifecycle detail")
	command.Flags().BoolVar(&jsonl, "jsonl", false, "Emit one typed JSON object per lifecycle event")
	return command
}

type watchEventWriter struct {
	state   *executionState
	verbose bool
	jsonl   bool
	mu      sync.Mutex
}

func (writer *watchEventWriter) Write(event maildomain.WatchEvent) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.jsonl {
		payload := watchEventPayload{
			FolderID: event.FolderID, FolderName: event.FolderName, Mode: event.Mode,
			Reason: event.Reason, Capabilities: append([]string(nil), event.Capabilities...),
			Attempt: event.Attempt, Summary: event.Summary,
		}
		if event.Backoff > 0 {
			payload.Backoff = event.Backoff.String()
		}
		if event.PollInterval > 0 {
			payload.PollInterval = event.PollInterval.String()
		}
		if event.ReconcileInterval > 0 {
			payload.ReconcileInterval = event.ReconcileInterval.String()
		}
		var eventErr *AppError
		if event.Error != nil {
			eventErr = NewAppError(ErrorCode(event.Error.Code), event.Error.Message, event.Error.Retryable)
			if event.Error.Stage != "" {
				eventErr.Details = map[string]any{"stage": event.Error.Stage}
			}
		}
		return WriteJSONLEvent(writer.state.stdout, JSONLEvent{
			Event: string(event.Type), Sequence: event.Sequence, AccountID: event.AccountID,
			Timestamp: event.Timestamp, Payload: payload, Error: eventErr,
		})
	}
	if event.Quiet && !writer.verbose {
		return nil
	}
	renderer := NewCompactTextRenderer(writer.state.stdout)
	switch event.Type {
	case maildomain.WatchEventStartup:
		return renderer.Record("watch-started", event.AccountID,
			Field("poll", event.PollInterval), Field("reconcile", event.ReconcileInterval))
	case maildomain.WatchEventMode:
		return renderer.Record("watch-mode", event.AccountID,
			Field("mode", event.Mode), Field("reason", event.Reason), Field("capabilities", event.Capabilities))
	case maildomain.WatchEventIdle:
		return renderer.Record("watch-idle", event.AccountID,
			Field("folder", event.FolderName), Field("folder_id", event.FolderID))
	case maildomain.WatchEventChange:
		return renderer.Record("watch-change", event.AccountID,
			Field("folder", event.FolderName), Field("folder_id", event.FolderID), Field("reason", event.Reason))
	case maildomain.WatchEventSync:
		if event.Summary == nil {
			return nil
		}
		summary := event.Summary
		return renderer.Record("watch-sync", event.AccountID,
			Field("folder", summary.FolderName), Field("folder_id", summary.FolderID),
			Field("fetched", summary.Fetched), Field("created", summary.Created), Field("updated", summary.Updated),
			Field("reconciled", summary.Reconciled), Field("state_changes", summary.StateChanges), Field("more", summary.MoreAvailable))
	case maildomain.WatchEventReconnect:
		return renderer.Record("watch-reconnect", event.AccountID,
			Field("attempt", event.Attempt), Field("backoff", event.Backoff), Field("stage", event.Reason))
	case maildomain.WatchEventDegraded:
		if event.Error == nil {
			return renderer.Record("watch-degraded", event.AccountID)
		}
		return renderer.Record("watch-degraded", event.AccountID,
			Field("code", event.Error.Code), Field("retryable", event.Error.Retryable), Field("stage", event.Error.Stage))
	case maildomain.WatchEventStopped:
		return renderer.Record("watch-stopped", event.AccountID, Field("reason", event.Reason))
	case maildomain.WatchEventCompleted:
		return renderer.Record("watch-completed", event.AccountID)
	default:
		return nil
	}
}
