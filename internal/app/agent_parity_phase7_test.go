package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestAgentParityPhase7SentReplyRepairsAnsweredWithoutSMTPReplay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase7-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	answerFailure := transport.NewFailure(transport.StageIMAPStore, transport.FailureUnavailable, true, fmt.Errorf("synthetic answered STORE failure"))
	flags := &fakeFlagClient{
		errors: []error{answerFailure, nil},
		results: []transport.FlagMutationResult{{
			Mailbox: "INBOX", UIDValidity: 700, UID: 1,
			Flags: []string{`\Seen`, `\Answered`, "$Keep"}, ModSequence: 9, Present: true,
		}},
	}
	commands.flags = flags
	addSendAccount(t, commands, home)

	threadID := seedReplyMessage(t, home, maildomain.IngestRecord{
		MessageID: "msg_phase7_answer_parent",
		Parsed: maildomain.ParsedMessage{
			RFCMessageID: "phase7-answer-parent@example.test",
			From:         maildomain.Address{Email: "sender@example.test"},
			To:           []maildomain.Address{{Email: "owner@icloud.com"}},
			Subject:      "Phase 7 answered state",
			Snippet:      "phase7 answer fixture",
		},
		Direction: maildomain.DirectionInbound, ModSequence: 8,
		InternalDate: time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC),
	})

	args := []string{
		"--home", home, "mail", "reply", "msg_phase7_answer_parent",
		"--body", "phase7 reply body", "--idempotency-key", "phase7-answer-reply-01", "--yes",
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || sender.calls != 1 || len(flags.requests) != 1 {
		t.Fatalf("reply code=%d stdout=%q stderr=%q smtp=%d flags=%+v", code, stdout, stderr, sender.calls, flags.requests)
	}
	if !compactContainsAll(stdout,
		"status=sent", "parent_location_id=loc_phase7_answer_parent",
		"answer_reconciliation=pending", "answer_error=imap_store", "reused=false",
	) {
		t.Fatalf("pending answer output=%q", stdout)
	}
	if request := flags.requests[0]; request.Mailbox != "INBOX" || request.ExpectedUIDValidity != 700 || request.UID != 1 || request.ExpectedModSequence != 8 || request.Flag != `\Answered` || !request.Add {
		t.Fatalf("answered request=%+v", request)
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
	attempts, _, err := state.Outbound().List(context.Background(), accountValue.ID, 5)
	if err != nil || len(attempts) != 1 {
		state.Close()
		t.Fatalf("attempts=%+v err=%v", attempts, err)
	}
	attempt := attempts[0]
	if attempt.ParentMessageID != "msg_phase7_answer_parent" || attempt.ParentLocationID != "loc_phase7_answer_parent" || attempt.AnswerReconciliation != maildomain.AnswerReconciliationPending || attempt.AnswerErrorCode != "imap_store" {
		state.Close()
		t.Fatalf("pending attempt=%+v", attempt)
	}
	parent, err := state.Mail().Message(context.Background(), accountValue.ID, "msg_phase7_answer_parent")
	if err != nil || !parent.Replied || parent.Answered || parent.ReplyState != "provider_pending" {
		state.Close()
		t.Fatalf("pending parent=%+v err=%v", parent, err)
	}
	page, err := state.Threads().Show(context.Background(), accountValue.ID, threadID, "", 20)
	if err != nil || !page.Thread.Replied {
		state.Close()
		t.Fatalf("pending thread=%+v err=%v", page.Thread, err)
	}
	state.Close()

	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "operations", "outbound", "reconcile-answer", attempt.ID)
	if code != 0 || stderr != "" || sender.calls != 1 || len(flags.requests) != 2 {
		t.Fatalf("repair code=%d stdout=%q stderr=%q smtp=%d flags=%d", code, stdout, stderr, sender.calls, len(flags.requests))
	}
	if !compactContainsAll(stdout, "reply-answer\n"+attempt.ID, "answer_reconciliation=confirmed", "smtp_retried=false") {
		t.Fatalf("repair output=%q", stdout)
	}
	if request := flags.requests[1]; request.ExpectedModSequence != 8 || request.Flag != `\Answered` || !request.Add {
		t.Fatalf("repair answered request=%+v", request)
	}

	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	parent, err = state.Mail().Message(context.Background(), accountValue.ID, "msg_phase7_answer_parent")
	if err != nil || !parent.Replied || !parent.Answered || parent.ReplyState != "answered" {
		state.Close()
		t.Fatalf("confirmed parent=%+v err=%v", parent, err)
	}
	var found bool
	for _, location := range parent.Locations {
		if location.ID == "loc_phase7_answer_parent" {
			found = true
			if !location.Flags.Answered || location.ModSequence != 9 || len(location.Flags.Unknown) != 1 || location.Flags.Unknown[0] != "$Keep" {
				state.Close()
				t.Fatalf("confirmed location=%+v", location)
			}
		}
	}
	if !found {
		state.Close()
		t.Fatal("confirmed parent location missing")
	}
	state.Close()

	code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || sender.calls != 1 || len(flags.requests) != 2 || !strings.Contains(stdout, "reused=true") || !strings.Contains(stdout, "answer_reconciliation=confirmed") {
		t.Fatalf("idempotent reply code=%d stdout=%q stderr=%q smtp=%d flags=%d", code, stdout, stderr, sender.calls, len(flags.requests))
	}
	code, _, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "operations", "outbound", "reconcile-answer", attempt.ID)
	if code != 0 || stderr != "" || sender.calls != 1 || len(flags.requests) != 2 {
		t.Fatalf("repeat repair code=%d stderr=%q smtp=%d flags=%d", code, stderr, sender.calls, len(flags.requests))
	}
}

func TestAgentParityPhase7FailedOrUnknownSMTPNeverMarksAnswered(t *testing.T) {
	for _, test := range []struct {
		name    string
		outcome transport.SMTPSendOutcome
		status  maildomain.OutboundStatus
	}{
		{name: "failed", outcome: transport.SMTPSendFailed, status: maildomain.OutboundFailed},
		{name: "unknown", outcome: transport.SMTPSendUnknown, status: maildomain.OutboundUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase7-secret")
			commands, _ := newTestAccountCommands()
			sender := &fakeSMTPSender{err: &transport.SMTPSendError{
				Outcome: test.outcome,
				Failure: transport.NewFailure(transport.StageSMTPData, transport.FailureUnavailable, test.outcome == transport.SMTPSendUnknown, fmt.Errorf("synthetic %s SMTP", test.name)),
			}}
			commands.sender = sender
			flags := &fakeFlagClient{results: []transport.FlagMutationResult{{Flags: []string{`\Answered`}, Present: true}}}
			commands.flags = flags
			addSendAccount(t, commands, home)
			threadID := seedReplyMessage(t, home, maildomain.IngestRecord{
				MessageID: "msg_phase7_" + test.name,
				Parsed:    maildomain.ParsedMessage{RFCMessageID: "phase7-" + test.name + "@example.test", From: maildomain.Address{Email: "sender@example.test"}, To: []maildomain.Address{{Email: "owner@icloud.com"}}, Subject: "Phase 7 " + test.name},
				Direction: maildomain.DirectionInbound, InternalDate: time.Date(2026, 8, 10, 2, 0, 0, 0, time.UTC),
			})
			code, _, _ := runWithAccountCommands(t, commands, "",
				"--home", home, "mail", "reply", "msg_phase7_"+test.name, "--body", "reply", "--yes", "--idempotency-key", "phase7-"+test.name)
			if code == 0 || sender.calls != 1 || len(flags.requests) != 0 {
				t.Fatalf("SMTP %s code=%d calls=%d flag_requests=%+v", test.name, code, sender.calls, flags.requests)
			}
			state, err := openState(context.Background(), home)
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
			attempts, _, err := state.Outbound().List(context.Background(), accountValue.ID, 5)
			if err != nil || len(attempts) != 1 || attempts[0].Status != test.status || attempts[0].AnswerReconciliation != maildomain.AnswerReconciliationNone {
				t.Fatalf("SMTP %s attempt=%+v err=%v", test.name, attempts, err)
			}
			parent, err := state.Mail().Message(context.Background(), accountValue.ID, "msg_phase7_"+test.name)
			if err != nil || parent.Answered || parent.Replied || parent.ReplyState != "none" {
				t.Fatalf("SMTP %s parent=%+v err=%v", test.name, parent, err)
			}
			page, err := state.Threads().Show(context.Background(), accountValue.ID, threadID, "", 10)
			if err != nil || page.Thread.Replied {
				t.Fatalf("SMTP %s thread=%+v err=%v", test.name, page.Thread, err)
			}
		})
	}
}

func TestAgentParityPhase7ReplyParentLocationIsExplicitWhenAmbiguous(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase7-secret")
	commands, _ := newTestAccountCommands()
	commands.sender = &fakeSMTPSender{}
	addSendAccount(t, commands, home)
	base := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	record := maildomain.IngestRecord{
		MessageID: "msg_phase7_ambiguous",
		Parsed:    maildomain.ParsedMessage{RFCMessageID: "phase7-ambiguous@example.test", From: maildomain.Address{Email: "sender@example.test"}, To: []maildomain.Address{{Email: "owner@icloud.com"}}, Subject: "Phase 7 ambiguous"},
		Direction: maildomain.DirectionInbound, InternalDate: base,
	}
	seedReplyMessage(t, home, record)

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	folders, err := state.Folders().Reconcile(context.Background(), accountValue.ID, []folder.RemoteObservation{
		{
			Name: "INBOX", WireName: "INBOX", Kind: folder.KindInbox, KindSource: "provider", Selectable: true,
			Subscription: folder.SubscriptionSubscribed, UIDValidity: 700, StatusKnown: true,
		},
		{
			Name: "Archive", WireName: "Archive", Kind: folder.KindArchive, KindSource: "provider", Selectable: true,
			Subscription: folder.SubscriptionSubscribed, UIDValidity: 701, StatusKnown: true,
		},
	}, base.Add(time.Minute), func() (string, error) { return "fld_phase7_archive", nil })
	if err != nil || len(folders) != 2 {
		state.Close()
		t.Fatalf("archive folders=%+v err=%v", folders, err)
	}
	var archive folder.Folder
	for _, value := range folders {
		if value.ID == "fld_phase7_archive" {
			archive = value
		}
	}
	if archive.ID == "" {
		state.Close()
		t.Fatalf("archive folder missing from %+v", folders)
	}
	record.AccountID = accountValue.ID
	record.FolderID = archive.ID
	record.LocationID = "loc_phase7_archive"
	record.UIDValidity = 701
	record.UID = 2
	record.Flags = []string{`\Seen`}
	record.RawContentHash = strings.Repeat("b", 64)
	record.RawStorageKey = "sha256/bb/" + strings.Repeat("b", 62)
	record.RawByteSize = 128
	record.RFC822Size = 128
	_, err = state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_phase7_archive", AccountID: accountValue.ID, FolderID: archive.ID,
		UIDValidity: 701, Bootstrap: true, LastCommittedUID: 2, LastScannedUID: 2,
		AttemptedAt: base.Add(time.Minute), Records: []maildomain.IngestRecord{record},
	})
	state.Close()
	if err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "reply", "msg_phase7_ambiguous", "--body", "reply", "--dry-run")
	if code == 0 || stdout != "" || !strings.Contains(stderr, "multiple eligible locations") {
		t.Fatalf("ambiguous preview code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	requestPath := filepath.Join(t.TempDir(), "reply.json")
	if err := os.WriteFile(requestPath, []byte(`{"message_id":"msg_phase7_ambiguous","folder":"INBOX","message":{"body":"reply"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "reply", "--input-json", requestPath, "--dry-run")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "parent_location_id=loc_phase7_ambiguous", "parent_folder_id=fld_reply_inbox") {
		t.Fatalf("selected preview code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
