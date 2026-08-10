package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestMailForwardQuotesExactSourceSelectsAttachmentsAndKeepsSourceIndependent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-forward-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)

	sourceDir := t.TempDir()
	selectedPath := filepath.Join(sourceDir, "selected-original.txt")
	omittedPath := filepath.Join(sourceDir, "omitted-original.txt")
	newPath := filepath.Join(sourceDir, "new-forward.txt")
	for path, content := range map[string]string{
		selectedPath: "selected-original-token",
		omittedPath:  "omitted-original-token",
		newPath:      "new-forward-token",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, sourceOut, sourceErr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "send",
		"--to", "Visible Source <visible-source@example.test>",
		"--cc", "Source Copy <source-copy@example.test>",
		"--bcc", "Prior Hidden <prior-hidden@example.test>",
		"--subject", "Fwd: FW: Forward integration",
		"--body", "original-forward-body-token",
		"--attach", selectedPath, "--attach", omittedPath,
		"--idempotency-key", "forward-source-01", "--yes")
	if code != 0 || sourceErr != "" || sender.calls != 1 {
		t.Fatalf("source send code=%d stdout=%q stderr=%q calls=%d", code, sourceOut, sourceErr, sender.calls)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	attempts, _, err := state.Outbound().List(context.Background(), accountValue.ID, 10)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("source attempts=%+v err=%v", attempts, err)
	}
	sourceAttempt := attempts[0]
	sourceMessage, err := state.Mail().Message(context.Background(), accountValue.ID, sourceAttempt.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if sourceMessage.Forwarded {
		t.Fatalf("fresh source is already forwarded: %+v", sourceMessage)
	}
	sourceThread, err := state.Threads().Show(context.Background(), accountValue.ID, sourceMessage.ID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if sourceThread.Thread.Forwarded {
		t.Fatalf("fresh source thread is already forwarded: %+v", sourceThread.Thread)
	}
	sourceAttachments, more, err := state.Mail().Attachments(context.Background(), accountValue.ID, sourceMessage.ID, 20)
	if err != nil || more || len(sourceAttachments) != 2 {
		t.Fatalf("source attachments=%+v more=%v err=%v", sourceAttachments, more, err)
	}
	selectedID := ""
	for _, value := range sourceAttachments {
		if value.Filename == filepath.Base(selectedPath) {
			selectedID = value.ID
		}
	}
	if selectedID == "" {
		t.Fatalf("selected source attachment missing: %+v", sourceAttachments)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	dryArgs := []string{
		"--home", home, "mail", "forward", sourceMessage.ID,
		"--to", "Forward Target <target@example.test>",
		"--bcc", "New Hidden <new-hidden@example.test>",
		"--body", "Please review this forward.",
		"--include-attachment", selectedID,
		"--attach", newPath,
		"--idempotency-key", "forward-exact-source-01",
		"--dry-run", "--headers",
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", dryArgs...)
	if code != 0 || stderr != "" || sender.calls != 1 {
		t.Fatalf("forward dry-run code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	if !compactContainsAll(stdout,
		"forward-preview\n"+sourceMessage.ID+" kind=forward",
		"source_thread_id="+sourceThread.Thread.ID,
		"original=quoted", "original_attachments=1", "new_attachments=1",
		"subject=\"Fwd: Forward integration\"", "attachments=2",
		"original-attachment\n"+selectedID,
		"attachment\nselected-original.txt", "attachment\nnew-forward.txt",
		"header\nSubject value=\"Fwd: Forward integration\"",
		"smtp_contacted=false", "attempt_created=false", "source_mutated=false",
	) {
		t.Fatalf("forward dry-run output = %q", stdout)
	}
	for _, forbidden := range []string{
		"prior-hidden@example.test", "omitted-original.txt", "In-Reply-To", "References", "synthetic-forward-secret",
	} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("forward preview exposed %q: %q", forbidden, stdout)
		}
	}
	if lines, size := strings.Count(stdout, "\n"), len(stdout); lines > 62 || size > 3000 {
		t.Fatalf("forward preview budget lines=%d bytes=%d output=%q", lines, size, stdout)
	}

	realArgs := append([]string(nil), dryArgs[:len(dryArgs)-2]...)
	realArgs = append(realArgs, "--yes")
	code, stdout, stderr = runWithAccountCommands(t, commands, "", realArgs...)
	if code != 0 || stderr != "" || sender.calls != 2 {
		t.Fatalf("forward send code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	if !compactContainsAll(stdout,
		"mail-forward\nsend_", "kind=forward", "source_message_id="+sourceMessage.ID,
		"status=sent", "reconciliation=pending", "reused=false",
	) {
		t.Fatalf("forward outcome = %q", stdout)
	}
	for _, forbidden := range []string{"target@example.test", "new-hidden@example.test", "Please review", selectedPath, newPath} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("forward outcome exposed %q: %q", forbidden, stdout)
		}
	}

	forwardRaw := sender.messages[1]
	parsed, err := maildomain.ParseNormalized(bytes.NewReader(forwardRaw))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Preview.Subject != "Fwd: Forward integration" || parsed.Preview.InReplyTo != "" || len(parsed.Preview.References) != 0 {
		t.Fatalf("forward parsed relationship = %+v", parsed.Preview)
	}
	if !strings.Contains(parsed.Preview.Snippet, "Please review this forward") || strings.Contains(string(forwardRaw), "prior-hidden@example.test") || strings.Contains(string(forwardRaw), "new-hidden@example.test") {
		t.Fatalf("forward body/BCC raw = %q", forwardRaw)
	}
	if got := strings.Join(sender.requests[1].Envelope.Recipients, ","); got != "target@example.test,new-hidden@example.test" {
		t.Fatalf("forward SMTP envelope = %s", got)
	}
	forwardAttachments := collectForwardAttachmentBytes(t, forwardRaw)
	if string(forwardAttachments["selected-original.txt"]) != "selected-original-token" ||
		string(forwardAttachments["new-forward.txt"]) != "new-forward-token" ||
		forwardAttachments["omitted-original.txt"] != nil {
		t.Fatalf("forward attachment bytes = %+v", forwardAttachments)
	}

	code, repeatOut, repeatErr := runWithAccountCommands(t, commands, "", realArgs...)
	if code != 0 || repeatErr != "" || sender.calls != 2 || !strings.Contains(repeatOut, "reused=true") {
		t.Fatalf("idempotent forward code=%d stdout=%q stderr=%q calls=%d", code, repeatOut, repeatErr, sender.calls)
	}
	jsonArgs := append(append([]string(nil), realArgs...), "--json")
	code, jsonOut, jsonErr := runWithAccountCommands(t, commands, "", jsonArgs...)
	if code != 0 || jsonErr != "" || sender.calls != 2 ||
		!strings.Contains(jsonOut, `"kind":"forward"`) ||
		!strings.Contains(jsonOut, `"source_message_id":"`+sourceMessage.ID+`"`) ||
		!strings.Contains(jsonOut, `"reused":true`) {
		t.Fatalf("forward JSON code=%d stdout=%q stderr=%q calls=%d", code, jsonOut, jsonErr, sender.calls)
	}
	for _, forbidden := range []string{"target@example.test", "new-hidden@example.test", "Please review", selectedPath, newPath} {
		if strings.Contains(jsonOut, forbidden) {
			t.Fatalf("forward JSON exposed %q: %q", forbidden, jsonOut)
		}
	}

	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	attempts, _, err = state.Outbound().List(context.Background(), accountValue.ID, 10)
	if err != nil || len(attempts) != 2 {
		t.Fatalf("forward attempts=%+v err=%v", attempts, err)
	}
	var forwardAttempt maildomain.OutboundAttempt
	for _, value := range attempts {
		if value.Kind == maildomain.OutboundKindForward {
			forwardAttempt = value
		}
	}
	if forwardAttempt.SourceMessageID != sourceMessage.ID || forwardAttempt.ParentMessageID != "" || forwardAttempt.ThreadID == sourceThread.Thread.ID {
		t.Fatalf("forward provenance/thread = %+v source_thread=%s", forwardAttempt, sourceThread.Thread.ID)
	}
	sourceAfter, err := state.Mail().Message(context.Background(), accountValue.ID, sourceMessage.ID)
	if err != nil {
		t.Fatal(err)
	}
	sourceThreadAfter, err := state.Threads().Show(context.Background(), accountValue.ID, sourceMessage.ID, "", 20)
	if err != nil || sourceThreadAfter.Thread.ID != sourceThread.Thread.ID || sourceThreadAfter.Thread.MessageCount != 1 ||
		len(sourceAfter.Locations) != len(sourceMessage.Locations) || sourceAfter.Subject != sourceMessage.Subject || sourceAfter.Unread != sourceMessage.Unread ||
		!sourceAfter.Forwarded || !sourceThreadAfter.Thread.Forwarded {
		t.Fatalf("source changed after forward message=%+v thread=%+v err=%v", sourceAfter, sourceThreadAfter, err)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", sourceMessage.ID, "--metadata-only")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "forwarded=true") {
		t.Fatalf("forwarded message output code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "threads", "show", sourceThread.Thread.ID, "--metadata-only")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "forwarded=true") {
		t.Fatalf("forwarded thread output code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestMailForwardFailedOrUnknownSMTPDoesNotCreateForwardedHistory(t *testing.T) {
	for _, test := range []struct {
		name    string
		outcome transport.SMTPSendOutcome
	}{
		{name: "failed", outcome: transport.SMTPSendFailed},
		{name: "unknown", outcome: transport.SMTPSendUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-forward-secret")
			commands, _ := newTestAccountCommands()
			sender := &fakeSMTPSender{}
			commands.sender = sender
			addSendAccount(t, commands, home)
			code, _, stderr := runWithAccountCommands(t, commands, "",
				"--home", home, "mail", "send", "--to", "source@example.test",
				"--subject", "Forward failure source", "--body", "source", "--idempotency-key", "forward-failure-source-"+test.name, "--yes")
			if code != 0 || stderr != "" || sender.calls != 1 {
				t.Fatalf("source send code=%d stderr=%q calls=%d", code, stderr, sender.calls)
			}
			state, err := openState(context.Background(), home)
			if err != nil {
				t.Fatal(err)
			}
			accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
			attempts, _, err := state.Outbound().List(context.Background(), accountValue.ID, 5)
			if err != nil || len(attempts) != 1 {
				state.Close()
				t.Fatalf("source attempts=%+v err=%v", attempts, err)
			}
			sourceID := attempts[0].MessageID
			sourceThread, err := state.Threads().Show(context.Background(), accountValue.ID, sourceID, "", 5)
			state.Close()
			if err != nil {
				t.Fatal(err)
			}
			sender.err = &transport.SMTPSendError{
				Outcome: test.outcome,
				Failure: transport.NewFailure(transport.StageSMTPData, transport.FailureUnavailable, test.outcome == transport.SMTPSendUnknown, errors.New("synthetic forward outcome")),
			}
			code, _, _ = runWithAccountCommands(t, commands, "",
				"--home", home, "mail", "forward", sourceID, "--to", "target@example.test", "--original", "none", "--body", "forward",
				"--idempotency-key", "forward-failure-"+test.name, "--yes")
			if code == 0 || sender.calls != 2 {
				t.Fatalf("forward outcome=%s code=%d calls=%d", test.name, code, sender.calls)
			}
			state, err = openState(context.Background(), home)
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			source, err := state.Mail().Message(context.Background(), accountValue.ID, sourceID)
			if err != nil || source.Forwarded {
				t.Fatalf("source after %s=%+v err=%v", test.name, source, err)
			}
			thread, err := state.Threads().Show(context.Background(), accountValue.ID, sourceThread.Thread.ID, "", 5)
			if err != nil || thread.Thread.Forwarded {
				t.Fatalf("thread after %s=%+v err=%v", test.name, thread.Thread, err)
			}
		})
	}
}

func TestMailForwardAttachedEMLRoundTripsExactSourceAndSafetyGuards(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-forward-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	largeBody := strings.Repeat("large-forward-source-token ", 90000)
	code, sourceOut, sourceErr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "send", "--to", "source@example.test",
		"--subject", "Large attached source", "--body", largeBody,
		"--idempotency-key", "forward-eml-source-01", "--yes")
	if code != 0 || sourceErr != "" || sender.calls != 1 {
		t.Fatalf("large source code=%d stdout=%q stderr=%q calls=%d", code, sourceOut, sourceErr, sender.calls)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	attempts, _, err := state.Outbound().List(context.Background(), accountValue.ID, 5)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("EML source attempts=%+v err=%v", attempts, err)
	}
	sourceID := attempts[0].MessageID
	_ = state.Close()

	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "forward", sourceID,
		"--to", "target@example.test", "--original", "eml", "--dry-run")
	if code != 0 || stderr != "" || sender.calls != 1 ||
		!strings.Contains(stdout, "original=eml") || !strings.Contains(stdout, "attached_eml=true") ||
		!strings.Contains(stdout, "attachment\nforwarded-message.eml") {
		t.Fatalf("EML preview code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "forward", sourceID,
		"--to", "target@example.test", "--original", "eml",
		"--idempotency-key", "forward-eml-01", "--yes")
	if code != 0 || stderr != "" || sender.calls != 2 {
		t.Fatalf("EML forward code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	attachments := collectForwardAttachmentBytes(t, sender.messages[1])
	if !bytes.Equal(attachments["forwarded-message.eml"], sender.messages[0]) {
		t.Fatalf("forwarded EML changed: got=%d want=%d", len(attachments["forwarded-message.eml"]), len(sender.messages[0]))
	}
	parsed, err := maildomain.ParseNormalized(bytes.NewReader(sender.messages[1]))
	if err != nil || parsed.Preview.InReplyTo != "" || len(parsed.Preview.References) != 0 {
		t.Fatalf("EML forward threading=%+v err=%v", parsed.Preview, err)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "forward", sourceID,
		"--to", "target@example.test", "--original", "none")
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "requires --yes") || sender.calls != 2 {
		t.Fatalf("non-TTY guard code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "forward", sourceID,
		"--to", "target@example.test", "--original", "none", "--dry-run")
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "cannot construct a safe forward") || sender.calls != 2 {
		t.Fatalf("none-without-body code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "forward", "msg_missing",
		"--to", "target@example.test", "--dry-run")
	if code != cli.ExitNotFound || stdout != "" || !strings.Contains(stderr, "forward source message not found") || sender.calls != 2 {
		t.Fatalf("missing forward source code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
}

func collectForwardAttachmentBytes(t *testing.T, raw []byte) map[string][]byte {
	t.Helper()
	values := map[string][]byte{}
	_, err := maildomain.ParseNormalizedWithAttachmentsContext(context.Background(), bytes.NewReader(raw), func(_ context.Context, part maildomain.MIMEPart, reader io.Reader) (maildomain.AttachmentBlob, error) {
		content, err := io.ReadAll(reader)
		if err != nil {
			return maildomain.AttachmentBlob{}, err
		}
		values[part.Filename] = content
		sum := sha256.Sum256(content)
		return maildomain.AttachmentBlob{
			ContentHash: hex.EncodeToString(sum[:]), StorageKey: "test/" + part.Path, ByteSize: int64(len(content)),
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func TestForwardAttachmentFilenameSanitizesProviderMetadata(t *testing.T) {
	value := maildomain.Attachment{Ordinal: 2, Filename: "../unsafe\\name:\x00?.txt"}
	if got := forwardAttachmentFilename(value); got != "_unsafe_name__.txt" {
		t.Fatalf("sanitized forward filename = %q", got)
	}
	if got := forwardAttachmentFilename(maildomain.Attachment{Ordinal: 4}); got != "attachment-5.bin" {
		t.Fatalf("unnamed forward filename = %q", got)
	}
}
