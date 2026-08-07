package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestMultiFolderSyncJSONLReportsCommittedFolderBeforeLaterFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MAIL_PARTIAL_PASSWORD", "synthetic-mail-partial-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {{Folders: []transport.RemoteFolder{
			{Name: "INBOX", WireName: "INBOX", Kind: "inbox", KindSource: "inbox", Selectable: true, Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: "INBOX", Messages: 1, UIDNext: 22, UIDValidity: 100}},
			{Name: "Sent Messages", WireName: "Sent Messages", Kind: "sent", KindSource: "special_use", Selectable: true, Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: "Sent Messages", Messages: 1, UIDNext: 42, UIDValidity: 200}},
		}}},
	}}
	inboxRaw := syntheticMailFixture(21, "partial-sync-21@example.test", "Partial sync committed", "partial-sync-committed-token", "")
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{
		{page: transport.InboxPage{
			Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 21, LastScannedUID: 21, Bootstrap: true,
			Messages: []transport.FetchedMessage{{UID: 21, InternalDate: time.Date(2026, 8, 7, 5, 0, 0, 0, time.UTC)}},
		}, raw: map[uint32]string{21: inboxRaw}},
		{err: transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, true, errors.New("synthetic later-folder failure"))},
	}}
	addAndDiscoverInbox(t, commands, home, "ICLOUD_MAIL_PARTIAL_PASSWORD")

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--all-folders", "--continue-on-error", "--jsonl")
	if code != cli.ExitUnavailable {
		t.Fatalf("partial sync exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], `"event":"item"`) || !strings.Contains(lines[0], `"status":"committed"`) || !strings.Contains(lines[0], `"folder_name":"INBOX"`) ||
		!strings.Contains(lines[1], `"event":"item"`) || !strings.Contains(lines[1], `"status":"error"`) || !strings.Contains(lines[1], `"folder_name":"Sent Messages"`) ||
		!strings.Contains(lines[2], `"event":"completed"`) || !strings.Contains(lines[2], `"completed":1`) || !strings.Contains(lines[2], `"failed":1`) || !strings.Contains(lines[2], `"partial":true`) {
		t.Fatalf("partial sync JSONL stdout=%q", stdout)
	}
	if !strings.Contains(stderr, `"event":"error"`) || !strings.Contains(stderr, `"completed_count":1`) {
		t.Fatalf("partial sync JSONL stderr=%q", stderr)
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
	inbox, err := state.Folders().ResolveKind(context.Background(), accountValue.ID, "inbox")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := state.Mail().Checkpoint(context.Background(), accountValue.ID, inbox.ID)
	if err != nil || checkpoint.LastCommittedUID != 21 {
		t.Fatalf("committed first-folder checkpoint=%+v err=%v", checkpoint, err)
	}
}
