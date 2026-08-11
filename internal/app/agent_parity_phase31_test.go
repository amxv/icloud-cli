package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestAgentParityPhase31SafePreDATAFailureQueuesAndReplaysExactPrivateEnvelope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase31-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{err: &transport.SMTPSendError{
		Outcome:        transport.SMTPSendFailed,
		OutboxEligible: true,
		Failure:        transport.NewFailure(transport.StageSMTPAuth, transport.FailureAuthentication, false, errors.New("synthetic pre-DATA auth failure")),
	}}
	commands.sender = sender
	addSendAccount(t, commands, home)
	attachmentPath := filepath.Join(t.TempDir(), "phase31-private.txt")
	if err := os.WriteFile(attachmentPath, []byte("phase31-private-attachment-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "send",
		"--to", "visible@example.test", "--bcc", "hidden-phase31@example.test",
		"--subject", "Phase 31 exact replay", "--body", "phase31-private-body-token",
		"--attach", attachmentPath,
		"--idempotency-key", "phase31-safe-failure", "--yes", "--json",
	)
	if code != cli.ExitAuthentication || stdout != "" || sender.calls != 1 || !strings.Contains(stderr, `"outbox_id":"out_`) || !strings.Contains(stderr, `"outbox_status":"retryable"`) {
		t.Fatalf("safe failure code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	if strings.Contains(strings.ToLower(string(sender.messages[0])), "\nbcc:") || strings.Contains(string(sender.messages[0]), "hidden-phase31@example.test") {
		t.Fatalf("raw message leaked BCC: %q", sender.messages[0])
	}
	if got := strings.Join(sender.requests[0].Envelope.Recipients, ","); !strings.Contains(got, "hidden-phase31@example.test") {
		t.Fatalf("private SMTP envelope lost BCC: %q", got)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "outbox", "list", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"count":1`) || !strings.Contains(stdout, `"status":"retryable"`) || !strings.Contains(stdout, `"bcc_count":1`) || !strings.Contains(stdout, `"attachment_count":1`) {
		t.Fatalf("outbox list code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, forbidden := range []string{"visible@example.test", "hidden-phase31@example.test", "phase31-private-body-token", "phase31-private-attachment-token", "phase31-private.txt", attachmentPath, "Phase 31 exact replay", "phase31-safe-failure", "raw_storage_key", "raw_content_hash", "raw/sha256"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("outbox list leaked %q: %q", forbidden, stdout)
		}
	}

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	items, _, _, err := state.Outbox().ListPage(context.Background(), accountValue.ID, 10, "")
	if err != nil || len(items) != 1 {
		state.Close()
		t.Fatalf("outbox items=%+v err=%v", items, err)
	}
	item := items[0]
	originalAttempt, err := state.Outbound().Attempt(context.Background(), item.SourceAttemptID)
	if err != nil || originalAttempt.Status != maildomain.OutboundFailed {
		state.Close()
		t.Fatalf("source attempt=%+v err=%v", originalAttempt, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	// Re-open the durable state through a fresh command service and use a
	// disjoint ID range to model process restart without relying on in-memory
	// ID generator state.
	restarted, _ := newTestAccountCommands()
	restartCounter := 1000
	restarted.newID = func(prefix string) (string, error) {
		restartCounter++
		return fmt.Sprintf("%s_0198d704-6c00-7000-8001-%012d", prefix, restartCounter), nil
	}
	restarted.sender = sender

	code, stdout, stderr = runWithAccountCommands(t, restarted, "", "--home", home, "mail", "outbox", "retry", item.ID, "--dry-run", "--json")
	if code != 0 || stderr != "" || sender.calls != 1 || !strings.Contains(stdout, `"status":"retryable"`) {
		t.Fatalf("outbox retry dry-run code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	for _, forbidden := range []string{"visible@example.test", "hidden-phase31@example.test", "phase31-private-body-token", "phase31-private-attachment-token", "raw_storage_key", "raw_content_hash"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("outbox retry dry-run leaked %q: %q", forbidden, stdout)
		}
	}
	code, stdout, stderr = runWithAccountCommands(t, restarted, "", "--home", home, "mail", "outbox", "retry", item.ID)
	if code != cli.ExitSafety || stdout != "" || sender.calls != 1 || !strings.Contains(stderr, "requires --yes") {
		t.Fatalf("outbox retry without authorization code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}

	sender.err = nil
	code, stdout, stderr = runWithAccountCommands(t, restarted, "", "--home", home, "mail", "outbox", "retry", item.ID, "--yes", "--json")
	if code != 0 || stderr != "" || sender.calls != 2 || !strings.Contains(stdout, `"status":"sent"`) || !strings.Contains(stdout, `"retry_of_attempt_id":"`+item.SourceAttemptID+`"`) {
		t.Fatalf("outbox retry code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	if string(sender.messages[0]) != string(sender.messages[1]) {
		t.Fatalf("Outbox replay recomposed bytes\nfirst=%q\nsecond=%q", sender.messages[0], sender.messages[1])
	}
	if got := strings.Join(sender.requests[1].Envelope.Recipients, ","); !strings.Contains(got, "hidden-phase31@example.test") || !strings.Contains(got, "visible@example.test") {
		t.Fatalf("replayed SMTP envelope=%q", got)
	}

	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := state.Outbox().Item(context.Background(), accountValue.ID, item.ID)
	if err != nil || stored.Status != maildomain.OutboxSent || stored.LatestAttemptID == "" {
		state.Close()
		t.Fatalf("sent outbox=%+v err=%v", stored, err)
	}
	retryAttempt, err := state.Outbound().Attempt(context.Background(), stored.LatestAttemptID)
	if err != nil || retryAttempt.Status != maildomain.OutboundSent || retryAttempt.RetryOfAttemptID != item.SourceAttemptID {
		state.Close()
		t.Fatalf("retry attempt=%+v err=%v", retryAttempt, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr = runWithAccountCommands(t, restarted, "", "--home", home, "mail", "outbox", "retry", item.ID, "--yes")
	if code != cli.ExitConflict || stdout != "" || sender.calls != 2 || !strings.Contains(stderr, "not retryable") {
		t.Fatalf("terminal retry code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
}

func TestAgentParityPhase31UnknownPostDATAAndAcceptedNeverCreateRetryableOutbox(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   int
		wantStatus string
	}{
		{name: "unknown post-DATA", err: &transport.SMTPSendError{Outcome: transport.SMTPSendUnknown, OutboxEligible: false, Failure: transport.NewFailure(transport.StageSMTPAccept, transport.FailureUnavailable, true, errors.New("synthetic disconnect"))}, wantCode: cli.ExitUnavailable, wantStatus: "unknown"},
		{name: "definite rejection after DATA", err: &transport.SMTPSendError{Outcome: transport.SMTPSendFailed, OutboxEligible: false, Failure: transport.NewFailure(transport.StageSMTPAccept, transport.FailureUnavailable, false, errors.New("synthetic final rejection"))}, wantCode: cli.ExitUnavailable, wantStatus: "failed"},
		{name: "accepted", wantCode: 0, wantStatus: "sent"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase31-secret")
			commands, _ := newTestAccountCommands()
			sender := &fakeSMTPSender{err: testCase.err}
			commands.sender = sender
			addSendAccount(t, commands, home)
			code, _, _ := runWithAccountCommands(t, commands, "", "--home", home, "mail", "send", "--to", "recipient@example.test", "--subject", testCase.name, "--body", "phase31-no-queue", "--idempotency-key", "phase31-"+strings.ReplaceAll(testCase.name, " ", "-"), "--yes")
			if code != testCase.wantCode || sender.calls != 1 {
				t.Fatalf("send code=%d want=%d calls=%d", code, testCase.wantCode, sender.calls)
			}
			code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "outbox", "list", "--json")
			if code != 0 || stderr != "" || !strings.Contains(stdout, `"count":0`) {
				t.Fatalf("outbox list code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "status", "--outbound", "--limit", "1", "--json")
			if code != 0 || stderr != "" || !strings.Contains(stdout, `"status":"`+testCase.wantStatus+`"`) {
				t.Fatalf("outbound status code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestAgentParityPhase31CancelAndUnknownReplayAreTerminal(t *testing.T) {
	newSafeItem := func(t *testing.T) (string, accountCommands, *fakeSMTPSender) {
		t.Helper()
		home := t.TempDir()
		t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase31-secret")
		commands, _ := newTestAccountCommands()
		sender := &fakeSMTPSender{err: &transport.SMTPSendError{Outcome: transport.SMTPSendFailed, OutboxEligible: true, Failure: transport.NewFailure(transport.StageConnect, transport.FailureUnavailable, true, errors.New("synthetic pre-DATA connect"))}}
		commands.sender = sender
		addSendAccount(t, commands, home)
		code, _, _ := runWithAccountCommands(t, commands, "", "--home", home, "mail", "send", "--to", "recipient@example.test", "--subject", "safe item", "--body", "phase31", "--idempotency-key", "phase31-safe-item", "--yes")
		if code != cli.ExitUnavailable {
			t.Fatalf("safe send code=%d", code)
		}
		state, err := openState(context.Background(), home)
		if err != nil {
			t.Fatal(err)
		}
		accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
		if err != nil {
			state.Close()
			t.Fatal(err)
		}
		items, _, _, err := state.Outbox().ListPage(context.Background(), accountValue.ID, 10, "")
		_ = state.Close()
		if err != nil || len(items) != 1 {
			t.Fatalf("items=%+v err=%v", items, err)
		}
		return home + "\x00" + items[0].ID, commands, sender
	}

	t.Run("cancel", func(t *testing.T) {
		packed, commands, sender := newSafeItem(t)
		parts := strings.SplitN(packed, "\x00", 2)
		home, outboxID := parts[0], parts[1]
		code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "outbox", "cancel", outboxID, "--json")
		if code != 0 || stderr != "" || !strings.Contains(stdout, `"status":"canceled"`) {
			t.Fatalf("cancel code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "outbox", "retry", outboxID, "--yes")
		if code != cli.ExitConflict || stdout != "" || sender.calls != 1 || !strings.Contains(stderr, "not retryable") {
			t.Fatalf("canceled retry code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
		}
	})

	t.Run("unknown replay", func(t *testing.T) {
		packed, commands, sender := newSafeItem(t)
		parts := strings.SplitN(packed, "\x00", 2)
		home, outboxID := parts[0], parts[1]
		sender.err = &transport.SMTPSendError{Outcome: transport.SMTPSendUnknown, OutboxEligible: false, Failure: transport.NewFailure(transport.StageSMTPAccept, transport.FailureUnavailable, true, errors.New("synthetic replay disconnect"))}
		code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "outbox", "retry", outboxID, "--yes", "--json")
		if code != cli.ExitUnavailable || stdout != "" || !strings.Contains(stderr, `"outbox_status":"unknown"`) || sender.calls != 2 {
			t.Fatalf("unknown retry code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
		}
		code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "outbox", "retry", outboxID, "--yes")
		if code != cli.ExitConflict || stdout != "" || sender.calls != 2 || !strings.Contains(stderr, "not retryable") {
			t.Fatalf("repeat unknown retry code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
		}
	})
}
