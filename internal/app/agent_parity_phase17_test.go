package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestAgentParityPhase17SyncUntilCurrentStopsAndResumesFromCommittedCheckpoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE17_SYNC_PASSWORD", "synthetic-phase17-sync-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	client := &fakeInboxClient{results: []fakeInboxPageResult{
		{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 21, LastScannedUID: 21, MoreAvailable: true, Bootstrap: true, Messages: []transport.FetchedMessage{{UID: 21, InternalDate: time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)}}}, raw: map[uint32]string{21: syntheticMailFixture(21, "phase17-sync-21@example.test", "Phase17 21", "body", "")}},
		{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 22, LastScannedUID: 22, MoreAvailable: true, Messages: []transport.FetchedMessage{{UID: 22, InternalDate: time.Date(2026, 8, 10, 10, 1, 0, 0, time.UTC)}}}, raw: map[uint32]string{22: syntheticMailFixture(22, "phase17-sync-22@example.test", "Phase17 22", "body", "")}},
		{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 23, LastScannedUID: 23, Messages: []transport.FetchedMessage{{UID: 23, InternalDate: time.Date(2026, 8, 10, 10, 2, 0, 0, time.UTC)}}}, raw: map[uint32]string{23: syntheticMailFixture(23, "phase17-sync-23@example.test", "Phase17 23", "body", "")}},
	}}
	commands.mail = client
	addAndDiscoverInbox(t, commands, home, "ICLOUD_PHASE17_SYNC_PASSWORD")

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--folder", "inbox", "--until-current", "--limit", "1", "--max-pages", "10", "--max-messages", "2")
	if code != 0 || stderr != "" || strings.Count(stdout, "mail-sync\n") != 2 || !compactContainsAll(stdout, "sync-drain\n", "pages=2", "fetched=2", "more=true", "stop_reason=max_messages") {
		t.Fatalf("bounded drain code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(client.requests) != 2 || client.requests[1].LastCommittedUID != 21 {
		t.Fatalf("first drain requests=%+v", client.requests)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--folder", "inbox", "--until-current", "--limit", "1", "--max-pages", "5", "--max-messages", "5", "--jsonl")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"event":"item"`) || !strings.Contains(stdout, `"event":"completed"`) || !strings.Contains(stdout, `"stop_reason":"complete"`) {
		t.Fatalf("resumed drain code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(client.requests) != 3 || client.requests[2].LastCommittedUID != 22 {
		t.Fatalf("resumed request=%+v", client.requests)
	}
}

func TestAgentParityPhase17SyncDrainReportsIntermediateFailureAndResumes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE17_SYNC_FAILURE_PASSWORD", "synthetic-phase17-sync-failure-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	client := &fakeInboxClient{results: []fakeInboxPageResult{
		{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 31, LastScannedUID: 31, MoreAvailable: true, Bootstrap: true, Messages: []transport.FetchedMessage{{UID: 31, InternalDate: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)}}}, raw: map[uint32]string{31: syntheticMailFixture(31, "phase17-sync-failure-31@example.test", "Phase17 failure 31", "body", "")}},
		{err: transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, true, context.Canceled)},
		{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 32, LastScannedUID: 32, Messages: []transport.FetchedMessage{{UID: 32, InternalDate: time.Date(2026, 8, 10, 12, 1, 0, 0, time.UTC)}}}, raw: map[uint32]string{32: syntheticMailFixture(32, "phase17-sync-failure-32@example.test", "Phase17 failure 32", "body", "")}},
	}}
	commands.mail = client
	addAndDiscoverInbox(t, commands, home, "ICLOUD_PHASE17_SYNC_FAILURE_PASSWORD")

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--folder", "inbox", "--until-current", "--limit", "1", "--max-pages", "5", "--max-messages", "5", "--jsonl")
	if code != cli.ExitUnavailable || stderr != "" || strings.Count(strings.TrimSpace(stdout), "\n") != 1 || !strings.Contains(stdout, `"status":"committed"`) || !strings.Contains(stdout, `"stop_reason":"error"`) || !strings.Contains(stdout, `"error":{"code":"unavailable"`) {
		t.Fatalf("interrupted drain code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(client.requests) != 2 || client.requests[1].LastCommittedUID != 31 {
		t.Fatalf("interrupted requests=%+v", client.requests)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--folder", "inbox", "--until-current", "--limit", "1", "--max-pages", "5", "--max-messages", "5")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "last_uid=32", "stop_reason=complete") {
		t.Fatalf("resumed drain code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(client.requests) != 3 || client.requests[2].LastCommittedUID != 31 {
		t.Fatalf("resume did not use committed checkpoint: %+v", client.requests)
	}
}

func TestAgentParityPhase17SyncDrainReportsDeadlineTruthfully(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE17_SYNC_DEADLINE_PASSWORD", "synthetic-phase17-sync-deadline-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{err: context.DeadlineExceeded}}}
	addAndDiscoverInbox(t, commands, home, "ICLOUD_PHASE17_SYNC_DEADLINE_PASSWORD")

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--folder", "inbox", "--until-current", "--limit", "1", "--max-pages", "5", "--max-messages", "5", "--jsonl")
	if code != cli.ExitUnavailable || stderr != "" || !strings.Contains(stdout, `"stop_reason":"deadline"`) || !strings.Contains(stdout, `"error":{"code":"unavailable"`) {
		t.Fatalf("deadline drain code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestAgentParityPhase17BackfillUntilCompleteStopsAndResumesPersistedFrontier(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE17_BACKFILL_PASSWORD", "synthetic-phase17-backfill-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 20, LastScannedUID: 20, Bootstrap: true}}}}
	history := &fakePhase20Client{pages: []fakePhase20Page{
		{page: transport.BackfillPage{Mailbox: "INBOX", UIDValidity: 100, BeforeUID: 21, NextBeforeUID: 10, Messages: []transport.FetchedMessage{{UID: 10, InternalDate: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)}}}, raw: map[uint32]string{10: syntheticMailFixture(10, "phase17-backfill-10@example.test", "Phase17 history 10", "body", "")}},
		{page: transport.BackfillPage{Mailbox: "INBOX", UIDValidity: 100, BeforeUID: 10, NextBeforeUID: 1, Complete: true, Messages: []transport.FetchedMessage{{UID: 5, InternalDate: time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)}}}, raw: map[uint32]string{5: syntheticMailFixture(5, "phase17-backfill-5@example.test", "Phase17 history 5", "body", "")}},
	}}
	commands.imap = history
	addAndDiscoverInbox(t, commands, home, "ICLOUD_PHASE17_BACKFILL_PASSWORD")
	if code, _, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--folder", "inbox", "--limit", "1"); code != 0 || stderr != "" {
		t.Fatalf("baseline sync code=%d stderr=%q", code, stderr)
	}

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "backfill", "--folder", "inbox", "--until-complete", "--limit", "1", "--max-pages", "1", "--max-messages", "10")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "backfill-drain\n", "pages=1", "fetched=1", "complete=false", "stop_reason=max_pages") {
		t.Fatalf("bounded backfill code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "backfill", "--folder", "inbox", "--until-complete", "--limit", "1", "--max-pages", "5", "--max-messages", "5", "--jsonl")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"stop_reason":"complete"`) {
		t.Fatalf("resumed backfill code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(history.requests) != 2 || history.requests[0].BeforeUID != 21 || history.requests[1].BeforeUID != 10 {
		t.Fatalf("backfill requests=%+v", history.requests)
	}

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	acct, _ := state.Accounts().ResolveSelected(context.Background(), "")
	fld, _ := state.Folders().Resolve(context.Background(), acct.ID, "inbox")
	frontier, err := state.Mail().BackfillFrontier(context.Background(), acct.ID, fld.ID, nil, nil)
	if err != nil || !frontier.Complete {
		t.Fatalf("frontier=%+v err=%v", frontier, err)
	}
}

func TestAgentParityPhase17BackfillDrainPreservesFrontierAcrossIntermediateFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE17_BACKFILL_FAILURE_PASSWORD", "synthetic-phase17-backfill-failure-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 20, LastScannedUID: 20, Bootstrap: true}}}}
	history := &fakePhase20Client{pages: []fakePhase20Page{
		{page: transport.BackfillPage{Mailbox: "INBOX", UIDValidity: 100, BeforeUID: 21, NextBeforeUID: 10, Messages: []transport.FetchedMessage{{UID: 10, InternalDate: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)}}}, raw: map[uint32]string{10: syntheticMailFixture(10, "phase17-backfill-failure-10@example.test", "Phase17 history failure 10", "body", "")}},
		{err: transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, true, context.Canceled)},
		{page: transport.BackfillPage{Mailbox: "INBOX", UIDValidity: 100, BeforeUID: 10, NextBeforeUID: 1, Complete: true, Messages: []transport.FetchedMessage{{UID: 5, InternalDate: time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)}}}, raw: map[uint32]string{5: syntheticMailFixture(5, "phase17-backfill-failure-5@example.test", "Phase17 history failure 5", "body", "")}},
	}}
	commands.imap = history
	addAndDiscoverInbox(t, commands, home, "ICLOUD_PHASE17_BACKFILL_FAILURE_PASSWORD")
	if code, _, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--folder", "inbox", "--limit", "1"); code != 0 || stderr != "" {
		t.Fatalf("baseline sync code=%d stderr=%q", code, stderr)
	}

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "backfill", "--folder", "inbox", "--until-complete", "--limit", "1", "--max-pages", "5", "--max-messages", "5", "--jsonl")
	if code != cli.ExitUnavailable || stderr != "" || !strings.Contains(stdout, `"status":"committed"`) || !strings.Contains(stdout, `"stop_reason":"error"`) || !strings.Contains(stdout, `"error":{"code":"unavailable"`) {
		t.Fatalf("interrupted backfill code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	acct, _ := state.Accounts().ResolveSelected(context.Background(), "")
	fld, _ := state.Folders().Resolve(context.Background(), acct.ID, "inbox")
	frontier, err := state.Mail().BackfillFrontier(context.Background(), acct.ID, fld.ID, nil, nil)
	_ = state.Close()
	if err != nil || frontier.BeforeUID != 10 || frontier.Complete {
		t.Fatalf("interrupted frontier=%+v err=%v", frontier, err)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "backfill", "--folder", "inbox", "--until-complete", "--limit", "1", "--max-pages", "5", "--max-messages", "5")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "before_uid=10", "complete=yes", "stop_reason=complete") {
		t.Fatalf("resumed backfill code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(history.requests) != 3 || history.requests[2].BeforeUID != 10 {
		t.Fatalf("backfill resume requests=%+v", history.requests)
	}
}
