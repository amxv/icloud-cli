package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestMailReplyUsesExactParentDerivesRecipientsAndKeepsExistingThread(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-reply-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	commands.flags = &fakeFlagClient{results: []transport.FlagMutationResult{{
		Mailbox: "INBOX", UIDValidity: 700, UID: 1, Flags: []string{`\Seen`, `\Answered`, "$Custom"}, ModSequence: 1, Present: true,
	}}}
	addSendAccount(t, commands, home)

	rootThread := seedReplyMessage(t, home, maildomain.IngestRecord{
		MessageID: "msg_reply_root",
		Parsed: maildomain.ParsedMessage{
			RFCMessageID: "reply-root@example.test",
			From:         maildomain.Address{DisplayName: "Root Sender", Email: "root-sender@example.test"},
			ReplyTo:      maildomain.Address{DisplayName: "Root Replies", Email: "root-replies@example.test"},
			To:           []maildomain.Address{{DisplayName: "Owner", Email: "owner@icloud.com"}},
			CC:           []maildomain.Address{{DisplayName: "Visible Copy", Email: "visible@example.test"}},
			BCC:          []maildomain.Address{{DisplayName: "Prior Hidden", Email: "prior-hidden@example.test"}},
			Subject:      "Re: Reply integration",
			Snippet:      "root message",
		},
		Direction:    maildomain.DirectionInbound,
		InternalDate: time.Date(2026, 8, 6, 1, 0, 0, 0, time.UTC),
	})
	childThread := seedReplyMessage(t, home, maildomain.IngestRecord{
		MessageID: "msg_reply_later",
		Parsed: maildomain.ParsedMessage{
			RFCMessageID: "reply-later@example.test",
			InReplyTo:    "reply-root@example.test",
			References:   []string{"reply-root@example.test"},
			From:         maildomain.Address{DisplayName: "Later Sender", Email: "later@example.test"},
			To:           []maildomain.Address{{Email: "owner@icloud.com"}},
			Subject:      "Re: Reply integration",
			Snippet:      "later message",
		},
		Direction:    maildomain.DirectionInbound,
		InternalDate: time.Date(2026, 8, 6, 1, 30, 0, 0, time.UTC),
	})
	if childThread != rootThread {
		t.Fatalf("fixture thread split root=%s child=%s", rootThread, childThread)
	}

	attachmentPath := filepath.Join(t.TempDir(), "reply-note.txt")
	if err := os.WriteFile(attachmentPath, []byte("reply attachment token"), 0o600); err != nil {
		t.Fatal(err)
	}
	dryRunArgs := []string{
		"--home", home, "mail", "reply-all", "msg_reply_root",
		"--to", "Added Person <added@example.test>",
		"--cc", "Visible Duplicate <VISIBLE@example.test>",
		"--bcc", "New Hidden <new-hidden@example.test>",
		"--body", "reply-body-token", "--attach", attachmentPath,
		"--idempotency-key", "reply-exact-parent-01", "--dry-run", "--headers",
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", dryRunArgs...)
	if code != 0 || stderr != "" || sender.calls != 0 {
		t.Fatalf("reply dry-run code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	if !compactContainsAll(stdout,
		"reply-preview\nmsg_reply_root kind=reply_all",
		"thread_id="+rootThread,
		"parent_rfc_message_id=reply-root@example.test",
		"threading=true",
		"derived_to=\"Root Replies <root-replies@example.test>\"",
		"derived_cc=\"Visible Copy <visible@example.test>\"",
		"added_to=\"Added Person <added@example.test>\"",
		"added_bcc=\"New Hidden <new-hidden@example.test>\"",
		"header\nIn-Reply-To value=<reply-root@example.test>",
		"header\nReferences value=<reply-root@example.test>",
		"attachment\nreply-note.txt",
		"text-body\nreply-body-token\n",
		"smtp_contacted=false", "attempt_created=false",
	) {
		t.Fatalf("reply dry-run output = %q", stdout)
	}
	for _, forbidden := range []string{"prior-hidden@example.test", "later@example.test", "synthetic-reply-secret"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("reply preview exposed or selected %q: %q", forbidden, stdout)
		}
	}
	if lines, size := strings.Count(stdout, "\n"), len(stdout); lines > 56 || size > 2400 {
		t.Fatalf("reply preview budget lines=%d bytes=%d output=%q", lines, size, stdout)
	}

	realArgs := append([]string(nil), dryRunArgs[:len(dryRunArgs)-2]...)
	realArgs = append(realArgs, "--yes")
	code, stdout, stderr = runWithAccountCommands(t, commands, "", realArgs...)
	if code != 0 || stderr != "" || sender.calls != 1 {
		t.Fatalf("reply send code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	if !compactContainsAll(stdout,
		"mail-reply\nsend_", "kind=reply_all", "parent_message_id=msg_reply_root",
		"status=sent", "thread_id="+rootThread, "reconciliation=pending", "answer_reconciliation=confirmed", "reused=false",
	) {
		t.Fatalf("reply outcome = %q", stdout)
	}
	if lines, size := strings.Count(stdout, "\n"), len(stdout); lines > 13 || size > 620 {
		t.Fatalf("reply outcome budget lines=%d bytes=%d output=%q", lines, size, stdout)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("captured messages = %d", len(sender.messages))
	}
	parsed, err := maildomain.ParseNormalized(bytes.NewReader(sender.messages[0]))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Preview.InReplyTo != "reply-root@example.test" ||
		strings.Join(parsed.Preview.References, ",") != "reply-root@example.test" ||
		parsed.Preview.Subject != "Re: Reply integration" {
		t.Fatalf("captured threading/subject = %+v", parsed.Preview)
	}
	if got := strings.Join(appAddressEmails(parsed.Preview.To), ","); got != "root-replies@example.test,added@example.test" {
		t.Fatalf("captured To = %s", got)
	}
	if got := strings.Join(appAddressEmails(parsed.Preview.CC), ","); got != "visible@example.test" {
		t.Fatalf("captured Cc = %s", got)
	}
	if strings.Contains(string(sender.messages[0]), "new-hidden@example.test") || strings.Contains(string(sender.messages[0]), "prior-hidden@example.test") {
		t.Fatalf("captured raw message exposed BCC: %q", sender.messages[0])
	}
	if got := strings.Join(sender.requests[0].Envelope.Recipients, ","); got != "root-replies@example.test,added@example.test,visible@example.test,new-hidden@example.test" {
		t.Fatalf("SMTP envelope recipients = %s", got)
	}

	code, repeatOut, repeatErr := runWithAccountCommands(t, commands, "", realArgs...)
	if code != 0 || repeatErr != "" || sender.calls != 1 || !strings.Contains(repeatOut, "reused=true") {
		t.Fatalf("idempotent reply code=%d stdout=%q stderr=%q calls=%d", code, repeatOut, repeatErr, sender.calls)
	}
	jsonArgs := append([]string(nil), realArgs...)
	jsonArgs = append(jsonArgs, "--json")
	code, jsonOut, jsonErr := runWithAccountCommands(t, commands, "", jsonArgs...)
	if code != 0 || jsonErr != "" || sender.calls != 1 ||
		!strings.Contains(jsonOut, `"kind":"reply_all"`) ||
		!strings.Contains(jsonOut, `"parent_message_id":"msg_reply_root"`) ||
		!strings.Contains(jsonOut, `"reused":true`) {
		t.Fatalf("idempotent reply JSON code=%d stdout=%q stderr=%q calls=%d", code, jsonOut, jsonErr, sender.calls)
	}
	for _, forbidden := range []string{"reply-body-token", "root-replies@example.test", "new-hidden@example.test", attachmentPath} {
		if strings.Contains(jsonOut, forbidden) {
			t.Fatalf("real reply JSON exposed %q: %q", forbidden, jsonOut)
		}
	}

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	attempts, _, err := state.Outbound().List(context.Background(), accountValue.ID, 5)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("outbound attempts=%+v err=%v", attempts, err)
	}
	if attempts[0].Kind != maildomain.OutboundKindReplyAll || attempts[0].ParentMessageID != "msg_reply_root" || attempts[0].ThreadID != rootThread {
		t.Fatalf("reply attempt metadata = %+v", attempts[0])
	}
}

func TestMailReplySafetyGuardsAndMalformedParentThreading(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-reply-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	seedReplyMessage(t, home, maildomain.IngestRecord{
		MessageID: "msg_reply_malformed",
		Parsed: maildomain.ParsedMessage{
			RFCMessageID: "not a valid message id",
			From:         maildomain.Address{Email: "sender@example.test"},
			To:           []maildomain.Address{{Email: "owner@icloud.com"}},
			Subject:      "Malformed parent",
		},
		Direction: maildomain.DirectionInbound, InternalDate: time.Date(2026, 8, 6, 2, 0, 0, 0, time.UTC),
	})

	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "reply", "msg_reply_malformed", "--body", "unsafe non-tty")
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "requires --yes") || sender.calls != 0 {
		t.Fatalf("reply non-TTY guard code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "reply", "msg_reply_malformed", "--body", "preview", "--dry-run", "--headers")
	if code != 0 || stderr != "" || sender.calls != 0 || !strings.Contains(stdout, "threading=false") || strings.Contains(stdout, "header\nIn-Reply-To") || strings.Contains(stdout, "header\nReferences") {
		t.Fatalf("malformed parent preview code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "reply", "msg_missing", "--body", "missing", "--dry-run")
	if code != cli.ExitNotFound || stdout != "" || !strings.Contains(stderr, "reply parent message not found") || sender.calls != 0 {
		t.Fatalf("missing parent code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
}

func seedReplyMessage(t *testing.T, home string, record maildomain.IngestRecord) string {
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
	folderValue, err := state.Folders().Resolve(context.Background(), accountValue.ID, "inbox")
	if err != nil {
		values, reconcileErr := state.Folders().Reconcile(context.Background(), accountValue.ID, []folder.RemoteObservation{{
			Name: "INBOX", WireName: "INBOX", Kind: folder.KindInbox, KindSource: "provider",
			Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 700, StatusKnown: true,
		}}, record.InternalDate, func() (string, error) { return "fld_reply_inbox", nil })
		if reconcileErr != nil || len(values) != 1 {
			t.Fatalf("create reply fixture inbox values=%+v err=%v", values, reconcileErr)
		}
		folderValue = values[0]
	}
	uid := uint32(1)
	if record.MessageID == "msg_reply_later" {
		uid = 2
	}
	record.AccountID = accountValue.ID
	record.FolderID = folderValue.ID
	record.LocationID = "loc_" + strings.TrimPrefix(record.MessageID, "msg_")
	record.UIDValidity = 700
	record.UID = uid
	record.Flags = []string{"\\Seen"}
	record.RFC822Size = 128
	record.RawContentHash = strings.Repeat("a", 64)
	record.RawStorageKey = "sha256/aa/" + strings.Repeat("a", 62)
	record.RawByteSize = 128
	if record.InternalDate.IsZero() {
		record.InternalDate = time.Date(2026, 8, 6, 1, 0, 0, 0, time.UTC)
	}
	_, err = state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_reply_fixture", AccountID: accountValue.ID, FolderID: folderValue.ID,
		UIDValidity: 700, Bootstrap: true, LastCommittedUID: uid, LastScannedUID: uid,
		AttemptedAt: record.InternalDate, Records: []maildomain.IngestRecord{record},
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := state.Threads().Show(context.Background(), accountValue.ID, record.MessageID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	return page.Thread.ID
}

func appAddressEmails(values []maildomain.Address) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.Email)
	}
	return result
}
