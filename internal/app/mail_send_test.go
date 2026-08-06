package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

type fakeSMTPSender struct {
	calls    int
	requests []transport.SMTPSendRequest
	messages [][]byte
	err      error
}

func (sender *fakeSMTPSender) SendSMTP(_ context.Context, _ transport.AuthRequest, request transport.SMTPSendRequest) (transport.SMTPSendResult, error) {
	sender.calls++
	content, err := io.ReadAll(request.Message)
	if err != nil {
		return transport.SMTPSendResult{}, err
	}
	request.Message = nil
	sender.requests = append(sender.requests, request)
	sender.messages = append(sender.messages, content)
	if sender.err != nil {
		return transport.SMTPSendResult{}, sender.err
	}
	return transport.SMTPSendResult{Accepted: true, StatusText: "accepted"}, nil
}

type testTerminalReader struct{ *strings.Reader }

func (testTerminalReader) IsTerminal() bool { return true }

func TestMailSendDryRunAndNonTTYGuardCreateNoAttemptOrSMTPCall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)

	args := []string{
		"--home", home, "mail", "send",
		"--to", "Primary Person <primary@example.com>",
		"--cc", "Visible Copy <copy@example.com>",
		"--bcc", "Hidden Copy <hidden@example.com>",
		"--reply-to", "Replies <reply@example.com>",
		"--subject", "Phase 14 dry run",
		"--body", "dry-run-body-token",
		"--html-body", "<p>dry-run-html-token</p>",
		"--header", "X-Synthetic: phase14",
		"--idempotency-key", "dry-run-key",
		"--dry-run", "--headers",
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || sender.calls != 0 {
		t.Fatalf("dry-run code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	if !compactContainsAll(stdout,
		"send-preview\n", "from=\"Ashray Sender <owner@icloud.com>\"",
		"to=\"Primary Person <primary@example.com>\"",
		"cc=\"Visible Copy <copy@example.com>\"",
		"bcc=\"Hidden Copy <hidden@example.com>\"",
		"subject=\"Phase 14 dry run\"", "body_parts=2", "attachments=0",
		"header\nFrom value=\"Ashray Sender <owner@icloud.com>\"",
		"header\nTo value=\"Primary Person <primary@example.com>\"",
		"header\nCc value=\"Visible Copy <copy@example.com>\"",
		"header\nReply-To value=\"Replies <reply@example.com>\"",
		"header\nSubject value=\"Phase 14 dry run\"",
		"header\nX-Synthetic value=phase14",
		"text-body\ndry-run-body-token\n", "smtp_contacted=false", "attempt_created=false",
	) || strings.Count(stdout, "dry-run-body-token") != 1 {
		t.Fatalf("dry-run output = %q", stdout)
	}
	if strings.Count(stdout, "hidden@example.com") != 1 || strings.Contains(stdout, "header\nBcc") || strings.Contains(stdout, "synthetic-send-secret") {
		t.Fatalf("dry-run privacy/output = %q", stdout)
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
	if _, found, err := state.Outbound().ResolveIdempotency(context.Background(), accountValue.ID, "dry-run-key", strings.Repeat("0", 64)); err != nil || found {
		state.Close()
		t.Fatalf("dry-run idempotency found=%v err=%v", found, err)
	}
	_ = state.Close()

	withoutDryRun := append([]string(nil), args[:len(args)-2]...)
	code, stdout, stderr = runWithAccountCommands(t, commands, "", withoutDryRun...)
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "requires --yes") || sender.calls != 0 {
		t.Fatalf("non-TTY guard code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
}

func TestMailSendCompactPreviewAndOutcomeBudgets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)

	base := []string{
		"--home", home, "mail", "send",
		"--to", "primary@example.com",
		"--subject", "Compact budget",
		"--body", "compact-body-token",
		"--idempotency-key", "compact-budget-key",
	}
	previewArgs := append(append([]string(nil), base...), "--dry-run")
	code, stdout, stderr := runWithAccountCommands(t, commands, "", previewArgs...)
	if code != 0 || stderr != "" || sender.calls != 0 {
		t.Fatalf("preview code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	if lines, size := strings.Count(stdout, "\n"), len(stdout); lines > 20 || size > 760 {
		t.Fatalf("send preview compact budget lines=%d bytes=%d output=%q", lines, size, stdout)
	}
	if strings.Count(stdout, "compact-body-token") != 1 || strings.Contains(stdout, "synthetic-send-secret") {
		t.Fatalf("send preview privacy/output = %q", stdout)
	}

	sendArgs := append(append([]string(nil), base...), "--yes")
	code, stdout, stderr = runWithAccountCommands(t, commands, "", sendArgs...)
	if code != 0 || stderr != "" || sender.calls != 1 {
		t.Fatalf("send code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	if lines, size := strings.Count(stdout, "\n"), len(stdout); lines != 6 || size > 320 {
		t.Fatalf("send outcome compact budget lines=%d bytes=%d output=%q", lines, size, stdout)
	}
	for _, forbidden := range []string{"compact-body-token", "primary@example.com", "Compact budget", "compact-budget-key", "synthetic-send-secret"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("send outcome exposed %q: %q", forbidden, stdout)
		}
	}
}

func TestMailSendInteractiveCancelAndConfirm(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	args := []string{
		"--home", home, "mail", "send",
		"--to", "primary@example.com", "--subject", "Interactive send",
		"--body", "interactive-body-token", "--idempotency-key", "interactive-send-key",
	}

	code, stdout, stderr := runWithAccountCommandsReader(commands, testTerminalReader{strings.NewReader("n\n")}, args...)
	if code != cli.ExitSafety || !strings.Contains(stdout, "send-preview") || !strings.Contains(stderr, "Send this message?") || sender.calls != 0 {
		t.Fatalf("interactive cancel code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	code, stdout, stderr = runWithAccountCommandsReader(commands, testTerminalReader{strings.NewReader("yes\n")}, args...)
	if code != 0 || !compactContainsAll(stdout, "send-preview\n", "mail-send\nsend_", "status=sent", "reconciliation=pending") || !strings.Contains(stderr, "Send this message?") || sender.calls != 1 {
		t.Fatalf("interactive confirm code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	if strings.Count(stdout, "interactive-body-token") != 1 {
		t.Fatalf("interactive preview repeated body: %q", stdout)
	}
}

func TestMailSendAcceptedIdempotentSearchableAndPayloadFree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	args := []string{
		"--home", home, "mail", "send",
		"--to", "Primary Person <primary@example.com>",
		"--cc", "Visible Copy <copy@example.com>",
		"--bcc", "Hidden Copy <hidden@example.com>",
		"--reply-to", "Replies <reply@example.com>",
		"--subject", "Phase 14 accepted",
		"--body", "accepted-search-token plain body",
		"--html-body", "<p>accepted-search-token HTML body</p>",
		"--header", "X-Synthetic: phase14",
		"--idempotency-key", "accepted-send-key",
		"--yes", "--json",
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || sender.calls != 1 {
		t.Fatalf("send code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	var envelope struct {
		Data struct {
			Attempt maildomain.OutboundAttempt `json:"attempt"`
			Reused  bool                       `json:"reused"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	attempt := envelope.Data.Attempt
	if envelope.Data.Reused || attempt.Status != maildomain.OutboundSent || !strings.HasPrefix(attempt.ID, "send_") || !strings.HasPrefix(attempt.MessageID, "msg_") || !strings.HasPrefix(attempt.ThreadID, "thr_") || attempt.Reconciliation != maildomain.ReconciliationPending {
		t.Fatalf("send envelope = %+v", envelope)
	}
	for _, forbidden := range []string{"accepted-search-token", "hidden@example.com", "Phase 14 accepted", "synthetic-send-secret", "idempotency_key", "raw_storage"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("send success exposed %q: %s", forbidden, stdout)
		}
	}
	request := sender.requests[0]
	if request.Envelope.From != "owner@icloud.com" || len(request.Envelope.Recipients) != 3 || !strings.Contains(strings.Join(request.Envelope.Recipients, ","), "hidden@example.com") {
		t.Fatalf("SMTP envelope = %+v", request.Envelope)
	}
	raw := string(sender.messages[0])
	if !strings.Contains(raw, "Ashray Sender") || !strings.Contains(raw, "accepted-search-token") || !strings.Contains(raw, "multipart/alternative") || strings.Contains(strings.ToLower(raw), "\nbcc:") || strings.Contains(raw, "hidden@example.com") {
		t.Fatalf("SMTP raw = %q", raw)
	}

	code, searchOut, searchErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "accepted-search-token", "--json")
	if code != 0 || searchErr != "" || !strings.Contains(searchOut, attempt.MessageID) || !strings.Contains(searchOut, `"index_state":"complete"`) || strings.Contains(searchOut, "hidden@example.com") {
		t.Fatalf("outbound local search code=%d stdout=%q stderr=%q", code, searchOut, searchErr)
	}
	code, bodyOut, bodyErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", attempt.MessageID, "--full")
	if code != 0 || bodyErr != "" || !strings.Contains(bodyOut, "accepted-search-token plain body") {
		t.Fatalf("outbound body code=%d stdout=%q stderr=%q", code, bodyOut, bodyErr)
	}
	code, threadOut, threadErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "thread", "show", attempt.ThreadID, "--full")
	if code != 0 || threadErr != "" || !strings.Contains(threadOut, "accepted-search-token plain body") {
		t.Fatalf("outbound thread body code=%d stdout=%q stderr=%q", code, threadOut, threadErr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || sender.calls != 1 || !strings.Contains(stdout, `"reused":true`) || !strings.Contains(stdout, attempt.ID) {
		t.Fatalf("idempotent repeat code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	conflictArgs := append([]string(nil), args...)
	for index := range conflictArgs {
		if conflictArgs[index] == "accepted-search-token plain body" {
			conflictArgs[index] = "different normalized body"
		}
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", conflictArgs...)
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "idempotency key") || sender.calls != 1 {
		t.Fatalf("idempotency conflict code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
}

func TestMailSendAcceptedLocalCompletionFailureRemainsSentAndNeverResubmits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)

	database, err := sql.Open("sqlite", filepath.Join(home, "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
CREATE TRIGGER synthetic_accepted_local_completion_failure
BEFORE INSERT ON message_search_documents
WHEN NEW.subject = 'Accepted local completion failure'
BEGIN
    SELECT RAISE(ABORT, 'synthetic accepted local completion failure');
END;`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	args := []string{
		"--home", home, "mail", "send",
		"--to", "primary@example.com",
		"--subject", "Accepted local completion failure",
		"--body", "accepted-local-failure-token",
		"--idempotency-key", "accepted-local-failure-key",
		"--yes", "--json",
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != cli.ExitInternal || stdout != "" || sender.calls != 1 ||
		!strings.Contains(stderr, "SMTP accepted the message") ||
		!strings.Contains(stderr, `"status":"sent"`) ||
		!strings.Contains(stderr, "do not retry automatically") {
		t.Fatalf("accepted incomplete code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || sender.calls != 1 ||
		!strings.Contains(stdout, `"status":"sent"`) ||
		!strings.Contains(stdout, `"reused":true`) ||
		!strings.Contains(stdout, `"error_code":"local_completion"`) {
		t.Fatalf("accepted incomplete repeat code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
}

func TestMailSendUnknownOutcomeBlocksAutomaticRetry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{err: &transport.SMTPSendError{
		Outcome: transport.SMTPSendUnknown,
		Failure: transport.NewFailure(transport.StageSMTPAccept, transport.FailureUnavailable, true, errors.New("synthetic post-DATA disconnect")),
	}}
	commands.sender = sender
	addSendAccount(t, commands, home)
	args := []string{
		"--home", home, "mail", "send", "--to", "primary@example.com",
		"--subject", "Unknown send", "--body", "unknown-body-token",
		"--idempotency-key", "unknown-send-key", "--yes", "--json",
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != cli.ExitUnavailable || stdout != "" || !strings.Contains(stderr, "outcome is unknown") || !strings.Contains(stderr, "attempt_id") || !strings.Contains(stderr, `"status":"unknown"`) || !strings.Contains(stderr, `"retryable":false`) || sender.calls != 1 {
		t.Fatalf("unknown send code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || sender.calls != 1 || !strings.Contains(stdout, `"status":"unknown"`) || !strings.Contains(stdout, `"reused":true`) {
		t.Fatalf("unknown retry code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	code, searchOut, searchErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "unknown-body-token", "--json")
	if code != 0 || searchErr != "" || !strings.Contains(searchOut, `"count":0`) {
		t.Fatalf("unknown send local search code=%d stdout=%q stderr=%q", code, searchOut, searchErr)
	}
}

type failingOutboundOutcomeWriter struct{}

func (failingOutboundOutcomeWriter) MarkOutcome(context.Context, string, maildomain.OutboundStatus, string, time.Time) (maildomain.OutboundAttempt, error) {
	return maildomain.OutboundAttempt{}, errors.New("synthetic outcome persistence failure")
}

func TestPersistOutboundOutcomeNeverHidesUnknownPersistenceFailure(t *testing.T) {
	attempt := maildomain.OutboundAttempt{
		ID:     "send_0198d704-6c00-7000-8000-000000000999",
		Status: maildomain.OutboundPending,
	}
	stored, err := persistOutboundOutcome(
		context.Background(), failingOutboundOutcomeWriter{}, attempt,
		maildomain.OutboundUnknown, "smtp_accept", time.Date(2026, 8, 6, 15, 0, 0, 0, time.UTC),
		errors.New("synthetic post-DATA disconnect"),
	)
	if stored.ID != attempt.ID || stored.Status != maildomain.OutboundPending {
		t.Fatalf("stored attempt = %+v", stored)
	}
	var appErr *cli.AppError
	if !errors.As(err, &appErr) || appErr.Code != cli.ErrorInternal || appErr.Retryable ||
		!strings.Contains(appErr.Message, "do not retry automatically") ||
		appErr.Details["attempt_id"] != attempt.ID || appErr.Details["status"] != maildomain.OutboundPending {
		t.Fatalf("persistence error = %#v", err)
	}
}

func TestMailSendBodyFilesStdinAndRawDryRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	textPath := filepath.Join(t.TempDir(), "body.txt")
	htmlPath := filepath.Join(t.TempDir(), "body.html")
	if err := os.WriteFile(textPath, []byte("file-text-body-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(htmlPath, []byte("<p>file-html-body-token</p>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "send", "--to", "primary@example.com",
		"--bcc", "hidden@example.com", "--subject", "File preview",
		"--body-file", textPath, "--html-body-file", htmlPath,
		"--dry-run", "--format", "raw",
	)
	if code != 0 || stderr != "" || sender.calls != 0 || !strings.Contains(stdout, "file-text-body-token") || !strings.Contains(stdout, "file-html-body-token") || !strings.Contains(stdout, "multipart/alternative") || strings.Contains(strings.ToLower(stdout), "\nbcc:") || strings.Contains(stdout, "hidden@example.com") {
		t.Fatalf("raw dry-run code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "stdin-body-token\n",
		"--home", home, "mail", "send", "--to", "primary@example.com",
		"--subject", "Stdin preview", "--body-file", "-", "--dry-run",
	)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "stdin-body-token") || sender.calls != 0 {
		t.Fatalf("stdin dry-run code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "shared-stdin",
		"--home", home, "mail", "send", "--to", "primary@example.com",
		"--subject", "Bad stdin", "--body-file", "-", "--html-body-file", "-", "--dry-run",
	)
	if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, "cannot both read from stdin") || sender.calls != 0 {
		t.Fatalf("dual stdin code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
}

func addSendAccount(t *testing.T, commands accountCommands, home string) {
	t.Helper()
	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "account", "add",
		"--name", "Ashray Sender", "--email", "owner@icloud.com",
		"--credential-env", "ICLOUD_SEND_PASSWORD",
	)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "account-add") {
		t.Fatalf("add send account code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func runWithAccountCommandsReader(commands accountCommands, stdin io.Reader, args ...string) (int, string, string) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithAccounts(context.Background(), args, stdin, &stdout, &stderr, commands)
	return exitCode, stdout.String(), stderr.String()
}
