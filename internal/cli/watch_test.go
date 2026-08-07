package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type watchTestMailService struct {
	unavailableMailService
	run func(context.Context, maildomain.WatchOptions, func(maildomain.WatchEvent) error) error
}

func (service watchTestMailService) MailWatch(ctx context.Context, _ string, _ string, options maildomain.WatchOptions, emit func(maildomain.WatchEvent) error) error {
	return service.run(ctx, options, emit)
}

func executeWatchTest(t *testing.T, service MailService, args ...string) (Result, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	result := Execute(context.Background(), args, Config{
		Version: "test", SchemaVersion: 17, Stdout: &stdout, Stderr: &stderr, Mail: service,
	})
	return result, stdout.String(), stderr.String()
}

func TestMailWatchUsesTimeoutPerOperationNotStreamLifetime(t *testing.T) {
	now := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	service := watchTestMailService{run: func(ctx context.Context, options maildomain.WatchOptions, emit func(maildomain.WatchEvent) error) error {
		if options.OperationTimeout != 5*time.Millisecond {
			t.Fatalf("operation timeout = %v", options.OperationTimeout)
		}
		if err := emit(maildomain.WatchEvent{Type: maildomain.WatchEventStartup, Sequence: 1, AccountID: "acct_a", Timestamp: now}); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("watch lifetime inherited operation timeout: %v", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
		return emit(maildomain.WatchEvent{Type: maildomain.WatchEventCompleted, Sequence: 2, AccountID: "acct_a", Timestamp: now.Add(time.Second)})
	}}
	result, stdout, stderr := executeWatchTest(t, service,
		"--timeout", "5ms", "mail", "watch", "--poll-interval", "1s", "--reconcile-interval", "10s")
	if result.ExitCode != 0 || stderr != "" || !strings.Contains(stdout, "watch-started") || !strings.Contains(stdout, "watch-completed") {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
}

func TestMailWatchCompactSuppressesIdleAndUnchangedChatter(t *testing.T) {
	now := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	quiet := maildomain.SyncSummary{AccountID: "acct_a", FolderID: "fld_inbox", FolderName: "INBOX"}
	changed := quiet
	changed.Fetched = 1
	changed.Created = 1
	service := watchTestMailService{run: func(_ context.Context, _ maildomain.WatchOptions, emit func(maildomain.WatchEvent) error) error {
		events := []maildomain.WatchEvent{
			{Type: maildomain.WatchEventStartup, Sequence: 1, AccountID: "acct_a", Timestamp: now, PollInterval: time.Minute, ReconcileInterval: 15 * time.Minute},
			{Type: maildomain.WatchEventMode, Sequence: 2, AccountID: "acct_a", Timestamp: now, Mode: maildomain.WatchModeIdle, Reason: "idle_supported", Capabilities: []string{"IDLE", "IMAP4REV1"}},
			{Type: maildomain.WatchEventIdle, Sequence: 3, AccountID: "acct_a", Timestamp: now, FolderID: "fld_inbox", FolderName: "INBOX", Quiet: true},
			{Type: maildomain.WatchEventSync, Sequence: 4, AccountID: "acct_a", Timestamp: now, Summary: &quiet, Quiet: true},
			{Type: maildomain.WatchEventChange, Sequence: 5, AccountID: "acct_a", Timestamp: now, FolderID: "fld_inbox", FolderName: "INBOX", Reason: "change"},
			{Type: maildomain.WatchEventSync, Sequence: 6, AccountID: "acct_a", Timestamp: now, Summary: &changed},
			{Type: maildomain.WatchEventCompleted, Sequence: 7, AccountID: "acct_a", Timestamp: now},
		}
		for _, event := range events {
			if err := emit(event); err != nil {
				return err
			}
		}
		return nil
	}}
	result, stdout, stderr := executeWatchTest(t, service, "mail", "watch")
	if result.ExitCode != 0 || stderr != "" || strings.Contains(stdout, "watch-idle") || strings.Count(stdout, "watch-sync\n") != 1 ||
		!strings.Contains(stdout, "watch-change") || !strings.Contains(stdout, "fetched=1") {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	if lines, bytes := strings.Count(stdout, "\n"), len(stdout); lines > 30 || bytes > 700 {
		t.Fatalf("compact lifecycle budget lines=%d bytes=%d output=%q", lines, bytes, stdout)
	}
}

func TestMailWatchJSONLIsTypedAndOrderedPerAccount(t *testing.T) {
	now := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	service := watchTestMailService{run: func(_ context.Context, options maildomain.WatchOptions, emit func(maildomain.WatchEvent) error) error {
		if !options.AllAccounts {
			t.Fatal("all-account option was not forwarded")
		}
		events := []maildomain.WatchEvent{
			{Type: maildomain.WatchEventStartup, Sequence: 1, AccountID: "acct_a", Timestamp: now},
			{Type: maildomain.WatchEventStartup, Sequence: 1, AccountID: "acct_b", Timestamp: now},
			{Type: maildomain.WatchEventMode, Sequence: 2, AccountID: "acct_a", Timestamp: now, Mode: maildomain.WatchModeIdle},
			{Type: maildomain.WatchEventCompleted, Sequence: 2, AccountID: "acct_b", Timestamp: now},
			{Type: maildomain.WatchEventCompleted, Sequence: 3, AccountID: "acct_a", Timestamp: now},
		}
		for _, event := range events {
			if err := emit(event); err != nil {
				return err
			}
		}
		return nil
	}}
	result, stdout, stderr := executeWatchTest(t, service, "mail", "watch", "--all-accounts", "--jsonl")
	if result.ExitCode != 0 || stderr != "" {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	last := map[string]uint64{}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 5 {
		t.Fatalf("JSONL lines=%d output=%q", len(lines), stdout)
	}
	for _, line := range lines {
		var event JSONLEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid JSONL %q: %v", line, err)
		}
		if event.Sequence != last[event.AccountID]+1 {
			t.Fatalf("out-of-order event account=%s sequence=%d last=%d", event.AccountID, event.Sequence, last[event.AccountID])
		}
		last[event.AccountID] = event.Sequence
	}
	if strings.Contains(stdout, "\x1b[") {
		t.Fatalf("JSONL contains ANSI: %q", stdout)
	}
}

func TestMailWatchJSONLFailureStaysJSONL(t *testing.T) {
	service := watchTestMailService{run: func(context.Context, maildomain.WatchOptions, func(maildomain.WatchEvent) error) error {
		return NewAppError(ErrorConflict, "watch lock busy", true)
	}}
	result, stdout, stderr := executeWatchTest(t, service, "mail", "watch", "--jsonl")
	if result.ExitCode != ExitConflict || stdout != "" {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	var event JSONLEvent
	if err := json.Unmarshal([]byte(stderr), &event); err != nil || event.Event != "error" || event.Error == nil || event.Error.Code != ErrorConflict {
		t.Fatalf("error JSONL event=%+v err=%v raw=%q", event, err, stderr)
	}
}
