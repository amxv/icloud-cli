package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

type fakeSMTPSender struct {
	calls    int
	auth     []transport.AuthRequest
	requests []transport.SMTPSendRequest
	messages [][]byte
	err      error
}

type deadlineSMTPSender struct {
	calls    int
	accepted bool
	cancel   context.CancelFunc
}

func (sender *deadlineSMTPSender) SendSMTP(ctx context.Context, _ transport.AuthRequest, _ transport.SMTPSendRequest) (transport.SMTPSendResult, error) {
	sender.calls++
	if sender.cancel != nil {
		sender.cancel()
	}
	<-ctx.Done()
	if sender.accepted {
		return transport.SMTPSendResult{Accepted: true, StatusText: "accepted at deadline"}, nil
	}
	return transport.SMTPSendResult{}, &transport.SMTPSendError{
		Outcome: transport.SMTPSendFailed,
		Failure: transport.NewFailure(transport.StageTimeout, transport.FailureUnavailable, true, ctx.Err()),
	}
}

type fakeSentAppender struct {
	calls    int
	auth     []transport.AuthRequest
	requests []transport.SentAppendRequest
	messages [][]byte
	err      error
}

func (appender *fakeSentAppender) AppendSent(_ context.Context, auth transport.AuthRequest, request transport.SentAppendRequest) (transport.SentAppendResult, error) {
	appender.calls++
	appender.auth = append(appender.auth, transport.AuthRequest{Username: auth.Username})
	appender.requests = append(appender.requests, transport.SentAppendRequest{
		Mailbox: request.Mailbox, Size: request.Size, InternalDate: request.InternalDate,
	})
	content, err := io.ReadAll(request.Message)
	if err != nil {
		return transport.SentAppendResult{}, err
	}
	appender.messages = append(appender.messages, content)
	if appender.err != nil {
		return transport.SentAppendResult{}, appender.err
	}
	return transport.SentAppendResult{Mailbox: request.Mailbox, UIDValidity: 500, UID: uint32(appender.calls)}, nil
}

func (sender *fakeSMTPSender) SendSMTP(_ context.Context, auth transport.AuthRequest, request transport.SMTPSendRequest) (transport.SMTPSendResult, error) {
	sender.calls++
	sender.auth = append(sender.auth, transport.AuthRequest{Username: auth.Username})
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
	if lines, size := strings.Count(stdout, "\n"), len(stdout); lines != 7 || size > 360 {
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
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	attempts, _, err := state.Outbound().List(context.Background(), accountValue.ID, 10)
	if err != nil || len(attempts) != 1 {
		state.Close()
		t.Fatalf("unknown attempts=%+v err=%v", attempts, err)
	}
	originalAttemptID := attempts[0].ID
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	retryArgs := append([]string(nil), args...)
	for index := range retryArgs {
		if retryArgs[index] == "unknown-send-key" {
			retryArgs[index] = "unknown-send-retry-key"
		}
	}
	retryArgs = append(retryArgs, "--retry-of", originalAttemptID)
	sender.err = nil
	code, stdout, stderr = runWithAccountCommands(t, commands, "", retryArgs...)
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "retrying an unknown send") || sender.calls != 1 {
		t.Fatalf("unknown unauthorized retry code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	retryArgs = append(retryArgs, "--retry-unknown")
	code, stdout, stderr = runWithAccountCommands(t, commands, "", retryArgs...)
	if code != 0 || stderr != "" || sender.calls != 2 || !strings.Contains(stdout, `"status":"sent"`) || !strings.Contains(stdout, `"retry_of_attempt_id":"`+originalAttemptID+`"`) {
		t.Fatalf("unknown authorized retry code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
}

func TestMailSendFailedAttemptAllowsExplicitLinkedRetry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{err: &transport.SMTPSendError{
		Outcome: transport.SMTPSendFailed,
		Failure: transport.NewFailure(transport.StageSMTPAccept, transport.FailureUnavailable, false, errors.New("synthetic final rejection")),
	}}
	commands.sender = sender
	addSendAccount(t, commands, home)
	args := []string{
		"--home", home, "mail", "send", "--to", "primary@example.com",
		"--subject", "Failed retry", "--body", "failed-retry-token",
		"--idempotency-key", "failed-send-key", "--yes", "--json",
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != cli.ExitUnavailable || stdout != "" || sender.calls != 1 || !strings.Contains(stderr, `"status":"failed"`) {
		t.Fatalf("failed send code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
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
	attempts, _, err := state.Outbound().List(context.Background(), accountValue.ID, 10)
	if err != nil || len(attempts) != 1 || attempts[0].Status != maildomain.OutboundFailed {
		state.Close()
		t.Fatalf("failed attempts=%+v err=%v", attempts, err)
	}
	originalAttemptID := attempts[0].ID
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	sender.err = nil
	retryArgs := append([]string(nil), args...)
	for index := range retryArgs {
		if retryArgs[index] == "failed-send-key" {
			retryArgs[index] = "failed-send-retry-key"
		}
	}
	retryArgs = append(retryArgs, "--retry-of", originalAttemptID)
	code, stdout, stderr = runWithAccountCommands(t, commands, "", retryArgs...)
	if code != 0 || stderr != "" || sender.calls != 2 || !strings.Contains(stdout, `"status":"sent"`) || !strings.Contains(stdout, `"retry_of_attempt_id":"`+originalAttemptID+`"`) {
		t.Fatalf("failed linked retry code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
}

func TestMailSendDeadlinePersistsTerminalOutcomeAfterCallerContextExpires(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &deadlineSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	request := maildomain.SendRequest{
		To: []maildomain.Address{{Email: "primary@example.com"}}, Subject: "Deadline failure",
		TextBody: "deadline-failure-token", IdempotencyKey: "deadline-failure-key",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sender.cancel = cancel
	_, attempt, reused, _, err := commands.MailSend(ctx, home, "", request)
	if err == nil || reused || sender.calls != 1 || attempt.Status != maildomain.OutboundFailed {
		t.Fatalf("attempt=%+v reused=%v calls=%d err=%v", attempt, reused, sender.calls, err)
	}
	state, openErr := openState(context.Background(), home)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer state.Close()
	stored, readErr := state.Outbound().Attempt(context.Background(), attempt.ID)
	if readErr != nil || stored.Status != maildomain.OutboundFailed || stored.ErrorCode != string(transport.StageTimeout) {
		t.Fatalf("stored=%+v err=%v", stored, readErr)
	}
}

func TestMailSendAcceptanceAtCallerDeadlinePersistsSentAndDefersSentCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &deadlineSMTPSender{accepted: true}
	commands.sender = sender
	sent := &fakeSentAppender{}
	commands.sent = sent
	addSendAccount(t, commands, home)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sender.cancel = cancel
	_, attempt, reused, _, err := commands.MailSend(ctx, home, "", maildomain.SendRequest{
		To: []maildomain.Address{{Email: "primary@example.com"}}, Subject: "Deadline acceptance",
		TextBody: "deadline-acceptance-token", IdempotencyKey: "deadline-acceptance-key",
	})
	if err == nil || reused || sender.calls != 1 || sent.calls != 0 || attempt.Status != maildomain.OutboundSent || attempt.MessageID == "" {
		t.Fatalf("attempt=%+v reused=%v smtp=%d sent=%d err=%v", attempt, reused, sender.calls, sent.calls, err)
	}
	if attempt.Reconciliation != maildomain.ReconciliationUnknown || attempt.ReconciliationErrorCode != "sent_copy_deferred" {
		t.Fatalf("accepted deadline reconciliation=%s code=%q", attempt.Reconciliation, attempt.ReconciliationErrorCode)
	}
	state, openErr := openState(context.Background(), home)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer state.Close()
	stored, readErr := state.Outbound().Attempt(context.Background(), attempt.ID)
	if readErr != nil || stored.Status != maildomain.OutboundSent || stored.MessageID == "" {
		t.Fatalf("stored=%+v err=%v", stored, readErr)
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

func TestMailSendAttachmentsPersistBytesMetadataAndIdempotency(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)

	root := t.TempDir()
	smallPath := filepath.Join(root, "small.txt")
	largePath := filepath.Join(root, "large.bin")
	small := []byte("small outbound attachment token\n")
	large := bytes.Repeat([]byte("0123456789abcdef"), (9<<20)/16)
	if err := os.WriteFile(smallPath, small, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(largePath, large, 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{
		"--home", home, "mail", "send", "--to", "primary@example.com",
		"--subject", "Attachment send", "--body", "See attached.",
		"--attach", smallPath, "--attach", largePath,
		"--attach-name", smallPath + "=renamed-note.txt",
		"--attach-type", smallPath + "=text/custom",
		"--idempotency-key", "attachment-send-key",
	}
	dry := append(append([]string(nil), base...), "--dry-run", "--json")
	code, stdout, stderr := runWithAccountCommands(t, commands, "", dry...)
	if code != 0 || stderr != "" || sender.calls != 0 || !strings.Contains(stdout, `"attachment_count":2`) ||
		!strings.Contains(stdout, `"filename":"renamed-note.txt"`) || !strings.Contains(stdout, `"media_type":"text/custom"`) ||
		strings.Contains(stdout, smallPath) || strings.Contains(stdout, largePath) {
		t.Fatalf("attachment preview code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}

	send := append(append([]string(nil), base...), "--yes", "--json")
	code, stdout, stderr = runWithAccountCommands(t, commands, "", send...)
	if code != 0 || stderr != "" || sender.calls != 1 {
		t.Fatalf("attachment send code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	var sent struct {
		Data struct {
			Attempt maildomain.OutboundAttempt `json:"attempt"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Data.Attempt.AttachmentCount != 2 || sent.Data.Attempt.AttachmentBytes != int64(len(small)+len(large)) {
		t.Fatalf("attachment attempt = %+v", sent.Data.Attempt)
	}
	if !strings.Contains(string(sender.messages[0]), "multipart/mixed") || strings.Contains(string(sender.messages[0]), smallPath) {
		t.Fatalf("SMTP attachment MIME metadata = %q", string(sender.messages[0][:min(len(sender.messages[0]), 4096)]))
	}

	code, listOut, listErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "attachments", "list", sent.Data.Attempt.MessageID, "--json")
	if code != 0 || listErr != "" {
		t.Fatalf("attachment list code=%d stdout=%q stderr=%q", code, listOut, listErr)
	}
	var listed struct {
		Data struct {
			Attachments []struct {
				ID           string                            `json:"id"`
				Filename     string                            `json:"filename"`
				MediaType    string                            `json:"media_type"`
				ContentHash  string                            `json:"content_hash"`
				ByteSize     int64                             `json:"byte_size"`
				Availability maildomain.AttachmentAvailability `json:"availability"`
			} `json:"attachments"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(listOut), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data.Attachments) != 2 || listed.Data.Attachments[0].Filename != "renamed-note.txt" ||
		listed.Data.Attachments[0].MediaType != "text/custom" || listed.Data.Attachments[0].Availability != maildomain.AttachmentAvailable {
		t.Fatalf("listed attachments = %+v", listed.Data.Attachments)
	}
	destination := filepath.Join(t.TempDir(), "saved.txt")
	code, _, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "attachments", "save", listed.Data.Attachments[0].ID, "--output", destination)
	if code != 0 || stderr != "" {
		t.Fatalf("attachment save code=%d stderr=%q", code, stderr)
	}
	saved, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(saved, small) {
		t.Fatalf("saved attachment bytes=%d err=%v", len(saved), err)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", send...)
	if code != 0 || stderr != "" || sender.calls != 1 || !strings.Contains(stdout, `"reused":true`) {
		t.Fatalf("attachment idempotent repeat code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
}

type mutatingTerminalReader struct {
	path    string
	mutated bool
}

func (reader *mutatingTerminalReader) Read(buffer []byte) (int, error) {
	if !reader.mutated {
		reader.mutated = true
		if err := os.WriteFile(reader.path, []byte("changed after review"), 0o600); err != nil {
			return 0, err
		}
	}
	return strings.NewReader("yes\n").Read(buffer)
}

func (*mutatingTerminalReader) IsTerminal() bool { return true }

func TestMailSendAttachmentMutationAndUnsafeSourcesStopBeforeSMTP(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	root := t.TempDir()
	path := filepath.Join(root, "reviewed.txt")
	if err := os.WriteFile(path, []byte("reviewed bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--home", home, "mail", "send", "--to", "primary@example.com",
		"--subject", "Mutation guard", "--body", "Body", "--attach", path,
		"--idempotency-key", "mutation-guard-key",
	}
	code, stdout, stderr := runWithAccountCommandsReader(commands, &mutatingTerminalReader{path: path}, args...)
	if code != cli.ExitSafety || !strings.Contains(stdout, "send-preview") || !strings.Contains(stderr, "outbound attachment review failed") || sender.calls != 0 {
		t.Fatalf("attachment mutation code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}

	oversize := filepath.Join(root, "oversize.bin")
	if err := os.WriteFile(oversize, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(oversize, maildomain.MaxSendAttachmentBytes+1); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{filepath.Join(root, "missing.bin"), root, oversize} {
		caseArgs := []string{
			"--home", home, "mail", "send", "--to", "primary@example.com",
			"--subject", "Unsafe attachment", "--body", "Body", "--attach", unsafe,
			"--idempotency-key", "unsafe-" + filepath.Base(unsafe), "--yes",
		}
		code, stdout, stderr = runWithAccountCommands(t, commands, "", caseArgs...)
		if code != cli.ExitSafety || stdout != "" || stderr == "" || sender.calls != 0 {
			t.Fatalf("unsafe attachment %q code=%d stdout=%q stderr=%q calls=%d", unsafe, code, stdout, stderr, sender.calls)
		}
	}

	providerOversize := filepath.Join(root, "provider-oversize.bin")
	if err := os.WriteFile(providerOversize, bytes.Repeat([]byte("p"), 16<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "send", "--to", "primary@example.com",
		"--subject", "Provider message limit", "--body", "Body", "--attach", providerOversize,
		"--idempotency-key", "provider-message-limit-key", "--yes")
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "20 MiB message limit") || sender.calls != 0 {
		t.Fatalf("provider message limit code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}

	commands.attachmentBlobs = func(config.AppPaths) attachmentBlobStore {
		return failingAttachmentBlobStore{err: errors.New("synthetic attachment cache failure")}
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "send", "--to", "primary@example.com",
		"--subject", "Cache failure", "--body", "Body", "--attach", path, "--idempotency-key", "cache-failure-key", "--yes")
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "outbound attachment review failed") || sender.calls != 0 {
		t.Fatalf("attachment cache failure code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
}

func TestMailPhaseFifteenHelpAndOutboundStatusRemainCompactAndPayloadFree(t *testing.T) {
	commands, _ := newTestAccountCommands()
	for _, test := range []struct {
		args []string
		want []string
	}{
		{args: []string{"mail", "send", "--help"}, want: []string{"--attach", "--attach-name", "--attach-type", "--retry-of", "--retry-unknown"}},
		{args: []string{"mail", "reply", "--help"}, want: []string{"reply <msg_…>", "--replace-to", "--replace-cc", "prior Bcc recipients are never inferred", "--dry-run", "--yes"}},
		{args: []string{"mail", "reply-all", "--help"}, want: []string{"reply-all <msg_…>", "visible participants", "--replace-to", "--replace-cc", "--attach"}},
		{args: []string{"mail", "forward", "--help"}, want: []string{"forward <msg_…>", "--original", "quoted, eml, or none", "--include-attachment", "new conversation", "--dry-run", "--yes"}},
		{args: []string{"mail", "move", "--help"}, want: []string{"move <message-id>", "--from", "--to", "--idempotency-key", "--dry-run", "--yes"}},
		{args: []string{"mail", "copy", "--help"}, want: []string{"copy <message-id>", "--from", "--to", "--idempotency-key", "--dry-run", "--yes"}},
		{args: []string{"mail", "archive", "--help"}, want: []string{"archive <message-id>", "non-Sent", "--from", "--dry-run", "--yes"}},
		{args: []string{"mail", "unarchive", "--help"}, want: []string{"unarchive <message-id>", "Archive location", "--dry-run", "--yes"}},
		{args: []string{"mail", "trash", "--help"}, want: []string{"trash <message-id>", "Trash folder", "--from", "--dry-run", "--yes"}},
		{args: []string{"mail", "restore", "--help"}, want: []string{"restore <message-id>", "--to", "Trash location", "--dry-run", "--yes"}},
		{args: []string{"mail", "delete", "--help"}, want: []string{"delete <message-id>", "Permanently", "--from", "--dry-run", "--yes"}},
		{args: []string{"mail", "sync", "--help"}, want: []string{"--reconcile-outbound"}},
		{args: []string{"mail", "status", "--help"}, want: []string{"--outbound", "--mutations", "--limit", "--json"}},
	} {
		code, stdout, stderr := runWithAccountCommands(t, commands, "", test.args...)
		if code != 0 || stderr != "" {
			t.Fatalf("help %v code=%d stdout=%q stderr=%q", test.args, code, stdout, stderr)
		}
		for _, want := range test.want {
			if !strings.Contains(stdout, want) {
				t.Fatalf("help %v missing %q: %q", test.args, want, stdout)
			}
		}
	}

	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "send", "--to", "primary@example.com",
		"--subject", "Outbound status private subject", "--body", "outbound-status-private-body",
		"--idempotency-key", "outbound-status-private-key", "--yes",
	)
	if code != 0 || stderr != "" || sender.calls != 1 {
		t.Fatalf("status fixture send code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "status", "--outbound", "--limit", "1")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "outbound\nsend_", "status=sent", "reconciliation=pending", "outbound-status\naccount=acct_", "count=1") {
		t.Fatalf("outbound status code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if lines, size := strings.Count(stdout, "\n"), len(stdout); lines > 20 || size > 800 {
		t.Fatalf("outbound status compact budget lines=%d bytes=%d output=%q", lines, size, stdout)
	}
	for _, forbidden := range []string{"primary@example.com", "Outbound status private subject", "outbound-status-private-body", "outbound-status-private-key", "synthetic-send-secret"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("outbound status exposed %q: %q", forbidden, stdout)
		}
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "status", "--outbound", "--limit", "1", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, fmt.Sprintf(`"schema_version":%d`, store.SchemaVersion)) || !strings.Contains(stdout, `"count":1`) || !strings.Contains(stdout, `"status":"sent"`) || !strings.Contains(stdout, `"kind":"send"`) {
		t.Fatalf("outbound status JSON code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, forbidden := range []string{"primary@example.com", "Outbound status private subject", "outbound-status-private-body", "outbound-status-private-key", "synthetic-send-secret"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("outbound status JSON exposed %q: %q", forbidden, stdout)
		}
	}
}

func TestMailSyncReconcileOutboundResolvesUnknownFromFetchedSentCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{err: &transport.SMTPSendError{
		Outcome: transport.SMTPSendUnknown,
		Failure: transport.NewFailure(transport.StageSMTPAccept, transport.FailureUnavailable, true, errors.New("synthetic post-DATA disconnect")),
	}}
	commands.sender = sender
	addSendAccount(t, commands, home)

	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "send", "--to", "primary@example.com",
		"--subject", "Sent reconciliation fixture", "--body", "sent-reconciliation-token",
		"--idempotency-key", "sent-reconciliation-key", "--yes", "--json",
	)
	if code != cli.ExitUnavailable || stdout != "" || sender.calls != 1 || len(sender.messages) != 1 || !strings.Contains(stderr, `"status":"unknown"`) {
		t.Fatalf("unknown fixture code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	parsed, err := maildomain.ParseNormalized(bytes.NewReader(sender.messages[0]))
	if err != nil || parsed.Preview.RFCMessageID == "" {
		t.Fatalf("parse outbound fixture message-id=%q err=%v", parsed.Preview.RFCMessageID, err)
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
	observedAt := time.Date(2026, 8, 6, 17, 30, 0, 0, time.UTC)
	folders, err := state.Folders().Reconcile(context.Background(), accountValue.ID, []folder.RemoteObservation{{
		Name: "Sent Messages", WireName: "Sent Messages", Kind: folder.KindSent, KindSource: "special_use",
		Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 500, StatusKnown: true,
	}}, observedAt, func() (string, error) { return "fld_0198d704-6c00-7000-8000-000000000500", nil })
	if err != nil || len(folders) != 1 {
		state.Close()
		t.Fatalf("reconcile Sent folders=%+v err=%v", folders, err)
	}
	if _, err := state.Folders().SetSync(context.Background(), accountValue.ID, folders[0].ID, true, observedAt); err != nil {
		state.Close()
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{
		page: transport.InboxPage{
			Mailbox: "Sent Messages", UIDValidity: 500, LastCommittedUID: 1, LastScannedUID: 1,
			Bootstrap: true, Messages: []transport.FetchedMessage{{UID: 1, InternalDate: observedAt.Add(time.Minute)}},
		},
		raw: map[uint32]string{1: string(sender.messages[0])},
	}}}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--reconcile-outbound", "--limit", "20")
	if code != 0 || stderr != "" || !compactContainsAll(stdout,
		"mail-sync\nfld_", "folder=\"Sent Messages\"", "fetched=1", "outbound_resolved_unknown=1",
	) {
		t.Fatalf("Sent reconciliation sync code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "status", "--outbound", "--limit", "1", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"status":"sent"`) || !strings.Contains(stdout, `"reconciliation":"confirmed"`) || !strings.Contains(stdout, `"reconciled_location_id":"loc_`) {
		t.Fatalf("reconciled outbound status code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, forbidden := range []string{"primary@example.com", "Sent reconciliation fixture", "sent-reconciliation-token", "sent-reconciliation-key", "synthetic-send-secret"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("reconciled status exposed %q: %q", forbidden, stdout)
		}
	}
}

func TestMailSendAppendsAcceptedRawMessageToDiscoveredSentMailbox(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	appender := &fakeSentAppender{}
	commands.sender = sender
	commands.sent = appender
	addSendAccount(t, commands, home)
	addSendSentFolder(t, home)

	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "send", "--to", "primary@example.com",
		"--subject", "Sent append proof", "--body", "sent-append-token",
		"--idempotency-key", "sent-append-key", "--yes", "--json",
	)
	if code != 0 || stderr != "" || sender.calls != 1 || appender.calls != 1 {
		t.Fatalf("Sent append code=%d stdout=%q stderr=%q smtp=%d append=%d", code, stdout, stderr, sender.calls, appender.calls)
	}
	if len(appender.auth) != 1 || appender.auth[0].Username != "owner@icloud.com" ||
		len(appender.requests) != 1 || appender.requests[0].Mailbox != "Sent Messages" ||
		appender.requests[0].Size != int64(len(sender.messages[0])) || len(appender.messages) != 1 ||
		!bytes.Equal(appender.messages[0], sender.messages[0]) {
		t.Fatalf("Sent append auth=%+v requests=%+v messages=%d SMTP-bytes=%d", appender.auth, appender.requests, len(appender.messages), len(sender.messages[0]))
	}
	if !strings.Contains(stdout, `"status":"sent"`) || !strings.Contains(stdout, `"reconciliation":"pending"`) {
		t.Fatalf("Sent append output = %q", stdout)
	}
}

func TestMailSendSentAppendFailurePreservesAcceptedAttemptAndNeverResends(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	appender := &fakeSentAppender{err: transport.NewFailure(
		transport.StageIMAPAppend, transport.FailureUnavailable, true, errors.New("synthetic append disconnect"),
	)}
	commands.sender = sender
	commands.sent = appender
	addSendAccount(t, commands, home)
	addSendSentFolder(t, home)
	args := []string{
		"--home", home, "mail", "send", "--to", "primary@example.com",
		"--subject", "Sent append failure", "--body", "sent-append-failure-token",
		"--idempotency-key", "sent-append-failure-key", "--yes", "--json",
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != cli.ExitUnavailable || stdout != "" || sender.calls != 1 || appender.calls != 1 ||
		!strings.Contains(stderr, "SMTP accepted the message but its Sent copy could not be confirmed; do not resend") ||
		!strings.Contains(stderr, `"status":"sent"`) {
		t.Fatalf("Sent append failure code=%d stdout=%q stderr=%q smtp=%d append=%d", code, stdout, stderr, sender.calls, appender.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || sender.calls != 1 || appender.calls != 1 ||
		!strings.Contains(stdout, `"reused":true`) || !strings.Contains(stdout, `"status":"sent"`) ||
		!strings.Contains(stdout, `"reconciliation":"unknown"`) {
		t.Fatalf("Sent append idempotent replay code=%d stdout=%q stderr=%q smtp=%d append=%d", code, stdout, stderr, sender.calls, appender.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "status", "--outbound", "--limit", "1")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "status=sent") || !strings.Contains(stdout, "reconciliation=unknown") ||
		!strings.Contains(stdout, "reconciliation_error=sent_copy_append") || !strings.Contains(stdout, "do not resend") {
		t.Fatalf("Sent append status code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func addSendSentFolder(t *testing.T, home string) folder.Folder {
	t.Helper()
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 6, 18, 15, 0, 0, time.UTC)
	values, err := state.Folders().Reconcile(context.Background(), accountValue.ID, []folder.RemoteObservation{{
		Name: "Sent Messages", WireName: "Sent Messages", Kind: folder.KindSent, KindSource: "special_use",
		Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 500, StatusKnown: true,
	}}, now, func() (string, error) { return "fld_0198d704-6c00-7000-8000-000000000550", nil })
	if err != nil || len(values) != 1 {
		t.Fatalf("add Sent folder values=%+v err=%v", values, err)
	}
	if _, err := state.Folders().SetSync(context.Background(), accountValue.ID, values[0].ID, true, now); err != nil {
		t.Fatal(err)
	}
	return values[0]
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
