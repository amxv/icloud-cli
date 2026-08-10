package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestAgentParityPhase12ThreadAliasMutationsReuseExactMessageEnginesAndFolderScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase12-secret")
	commands, _ := newTestAccountCommands()
	locationClient := &fakeLocationClient{}
	commands.locations = locationClient
	addSendAccount(t, commands, home)
	fixture := seedLocationMutationFixture(t, home, []locationSeed{{folder.KindInbox, 1, `$Fixture`}})

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	inbox := fixture.folders[folder.KindInbox]
	sent := fixture.folders[folder.KindSent]
	projects := fixture.folders[folder.KindOther]
	base := time.Date(2026, 8, 10, 5, 30, 0, 0, time.UTC)

	left := phase12ThreadRecord(accountValue.ID, inbox.ID, inbox.UIDValidity, 10, "msg_phase12_left", "phase12-left@example.test", "Phase 12 canonical thread", "alice@example.test", accountValue.Email, base, 21)
	phase12CommitRecord(t, state, accountValue.ID, inbox, left, base)
	leftPage, err := state.Threads().Show(context.Background(), accountValue.ID, left.MessageID, "", 20)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	canonicalThreadID := leftPage.Thread.ID

	right := phase12ThreadRecord(accountValue.ID, sent.ID, sent.UIDValidity, 11, "msg_phase12_right", "phase12-right@example.test", "Independent right half", accountValue.Email, "bob@example.test", base.Add(time.Minute), 31)
	phase12CommitRecord(t, state, accountValue.ID, sent, right, base.Add(time.Minute))
	rightPage, err := state.Threads().Show(context.Background(), accountValue.ID, right.MessageID, "", 20)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	retiredThreadID := rightPage.Thread.ID
	if retiredThreadID == canonicalThreadID {
		state.Close()
		t.Fatalf("pre-bridge threads unexpectedly matched: %s", canonicalThreadID)
	}

	unrelated := phase12ThreadRecord(accountValue.ID, inbox.ID, inbox.UIDValidity, 12, "msg_phase12_unrelated", "phase12-unrelated@example.test", "Phase 12 canonical thread", "stranger@example.test", "different-recipient@example.test", base.Add(2*time.Minute), 41)
	phase12CommitRecord(t, state, accountValue.ID, inbox, unrelated, base.Add(2*time.Minute))

	bridge := phase12ThreadRecord(accountValue.ID, inbox.ID, inbox.UIDValidity, 13, "msg_phase12_bridge", "phase12-bridge@example.test", "Re: Phase 12 canonical thread", accountValue.Email, "alice@example.test", base.Add(3*time.Minute), 51)
	bridge.Parsed.References = []string{"phase12-left@example.test", "phase12-right@example.test"}
	bridge.Parsed.InReplyTo = "phase12-right@example.test"
	phase12CommitRecord(t, state, accountValue.ID, inbox, bridge, base.Add(3*time.Minute))

	merged, err := state.Threads().Show(context.Background(), accountValue.ID, retiredThreadID, "", 20)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	if merged.Thread.ID != canonicalThreadID || merged.Thread.RedirectedFrom != retiredThreadID || merged.Thread.MessageCount != 3 {
		state.Close()
		t.Fatalf("merged alias thread=%+v", merged.Thread)
	}
	if got := phase12MessageIDs(merged.Thread.Messages); strings.Join(got, ",") != "msg_phase12_left,msg_phase12_right,msg_phase12_bridge" {
		state.Close()
		t.Fatalf("merged members=%v", got)
	}
	unrelatedPage, err := state.Threads().Show(context.Background(), accountValue.ID, unrelated.MessageID, "", 20)
	if err != nil || unrelatedPage.Thread.ID == canonicalThreadID {
		state.Close()
		t.Fatalf("same-subject unrelated message joined canonical thread: %+v err=%v", unrelatedPage.Thread, err)
	}
	state.Close()

	locationClient.results = []transport.LocationMutationResult{
		phase12CopyResult(inbox, projects, 10, 110),
		phase12CopyResult(inbox, projects, 13, 113),
	}
	copyArgs := []string{
		"--home", home, "mail", "threads", "copy", retiredThreadID,
		"--from", "inbox", "--to", projects.ID,
		"--idempotency-key", "phase12-thread-copy", "--yes", "--jsonl",
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", copyArgs...)
	if code != 0 || stderr != "" || locationClient.calls != 2 {
		t.Fatalf("thread copy code=%d calls=%d stdout=%q stderr=%q", code, locationClient.calls, stdout, stderr)
	}
	for index, wantUID := range []uint32{10, 13} {
		request := locationClient.requests[index]
		if request.SourceMailbox != inbox.Name || request.DestinationMailbox != projects.Name || request.SourceUID != wantUID {
			t.Fatalf("copy request %d=%+v", index, request)
		}
	}
	if strings.Contains(stdout, unrelated.MessageID) || strings.Contains(stdout, right.MessageID) || !strings.Contains(stdout, `"thread_id":"`+canonicalThreadID+`"`) || !strings.Contains(stdout, `"redirected_from":"`+retiredThreadID+`"`) || !strings.Contains(stdout, `"thread_message_count":3`) {
		t.Fatalf("copy output=%q", stdout)
	}

	// Replay through the canonical ID rather than the retired alias. Per-message
	// thread idempotency is bound to the canonical graph, so no provider write repeats.
	replayArgs := append([]string(nil), copyArgs...)
	for index := range replayArgs {
		if replayArgs[index] == retiredThreadID {
			replayArgs[index] = canonicalThreadID
			break
		}
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", replayArgs...)
	if code != 0 || stderr != "" || locationClient.calls != 2 || strings.Count(stdout, `"reused":true`) != 2 {
		t.Fatalf("thread copy replay code=%d calls=%d stdout=%q stderr=%q", code, locationClient.calls, stdout, stderr)
	}

	flagClient := &fakeFlagClient{results: []transport.FlagMutationResult{
		{Mailbox: inbox.Name, UIDValidity: inbox.UIDValidity, UID: 10, Flags: []string{`\Seen`, `$Keep`}, ModSequence: 22, Present: true},
		{Mailbox: inbox.Name, UIDValidity: inbox.UIDValidity, UID: 13, Flags: []string{`\Seen`, `$Keep`}, ModSequence: 52, Present: true},
	}}
	commands.flags = flagClient
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "threads", "mark-read", retiredThreadID,
		"--folder", "inbox", "--jsonl")
	if code != 0 || stderr != "" || len(flagClient.requests) != 2 {
		t.Fatalf("thread mark-read code=%d requests=%+v stdout=%q stderr=%q", code, flagClient.requests, stdout, stderr)
	}
	for index, wantUID := range []uint32{10, 13} {
		request := flagClient.requests[index]
		if request.Mailbox != inbox.Name || request.ExpectedUIDValidity != inbox.UIDValidity || request.UID != wantUID || request.ExpectedModSequence != 0 || request.Flag != `\Seen` || !request.Add {
			t.Fatalf("conditional STORE request %d=%+v", index, request)
		}
	}

	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	for _, messageID := range []string{left.MessageID, bridge.MessageID} {
		message, readErr := state.Mail().Message(context.Background(), accountValue.ID, messageID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		seenInbox, unseenProject := false, false
		for _, location := range message.Locations {
			switch location.FolderID {
			case inbox.ID:
				seenInbox = location.Present && location.Flags.Seen
			case projects.ID:
				unseenProject = location.Present && !location.Flags.Seen
			}
		}
		if !seenInbox || !unseenProject {
			t.Fatalf("thread mark-read escaped selected Inbox copy for %s: %+v", messageID, message.Locations)
		}
	}
	rightMessage, err := state.Mail().Message(context.Background(), accountValue.ID, right.MessageID)
	if err != nil || len(rightMessage.Locations) != 1 || rightMessage.Locations[0].FolderID != sent.ID || !rightMessage.Locations[0].Present || rightMessage.Locations[0].Flags.Seen {
		t.Fatalf("Sent-only canonical member was mutated by Inbox-scoped action: %+v err=%v", rightMessage, err)
	}
	unrelatedMessage, err := state.Mail().Message(context.Background(), accountValue.ID, unrelated.MessageID)
	if err != nil || !unrelatedMessage.Unread {
		t.Fatalf("unrelated same-subject message changed: %+v err=%v", unrelatedMessage, err)
	}
}

func phase12ThreadRecord(accountID, folderID string, uidValidity, uid uint32, messageID, rfcID, subject, from, to string, at time.Time, modSequence uint64) maildomain.IngestRecord {
	hash := fmt.Sprintf("%064x", uid+12000)
	return maildomain.IngestRecord{
		MessageID: messageID, LocationID: "loc_" + strings.TrimPrefix(messageID, "msg_"),
		AccountID: accountID, FolderID: folderID, UIDValidity: uidValidity, UID: uid,
		Flags: []string{`$Keep`}, ModSequence: modSequence, InternalDate: at,
		RFC822Size: 512, RawContentHash: hash, RawStorageKey: "raw/sha256/" + hash[:2] + "/" + hash + ".eml", RawByteSize: 512,
		Direction: maildomain.DirectionInbound, ContentFingerprint: "content:" + hash,
		Parsed: maildomain.ParsedMessage{
			RFCMessageID: rfcID, From: maildomain.Address{Email: from}, To: []maildomain.Address{{Email: to}},
			Subject: subject, Snippet: "Phase 12 thread fixture", HeaderDate: &at,
		},
	}
}

func phase12CommitRecord(t *testing.T, state *store.Store, accountID string, folderValue folder.Folder, record maildomain.IngestRecord, at time.Time) {
	t.Helper()
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_phase12_thread", AccountID: accountID, FolderID: folderValue.ID,
		UIDValidity: folderValue.UIDValidity, Bootstrap: true, LastCommittedUID: record.UID, LastScannedUID: record.UID,
		AttemptedAt: at, Records: []maildomain.IngestRecord{record},
	}); err != nil {
		t.Fatal(err)
	}
}

func phase12CopyResult(source, destination folder.Folder, sourceUID, destinationUID uint32) transport.LocationMutationResult {
	return transport.LocationMutationResult{
		Operation: transport.LocationMutationCopy, ProviderMethod: "copy_uid",
		SourceMailbox: source.Name, SourceUIDValidity: source.UIDValidity, SourceUID: sourceUID,
		SourceFlags: []string{`$Keep`}, SourcePresent: true,
		DestinationMailbox: destination.Name, DestinationUIDValidity: destination.UIDValidity, DestinationUID: destinationUID,
		DestinationFlags: []string{`$Keep`}, DestinationKnown: true, ProviderMutationObserved: true,
	}
}

func phase12MessageIDs(messages []maildomain.Message) []string {
	result := make([]string, 0, len(messages))
	for _, message := range messages {
		result = append(result, message.ID)
	}
	return result
}
