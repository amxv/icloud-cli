package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/transport"
)

type countingEmptyInboxClient struct {
	calls     int
	mailboxes []string
}

func (client *countingEmptyInboxClient) FetchInboxPage(_ context.Context, _ transport.AuthRequest, request transport.InboxFetchRequest, _ blob.Stager) (transport.InboxPage, error) {
	client.calls++
	client.mailboxes = append(client.mailboxes, request.Mailbox)
	return transport.InboxPage{
		Mailbox: request.Mailbox, UIDValidity: 1, Bootstrap: request.Bootstrap,
		LastCommittedUID: request.LastCommittedUID, LastScannedUID: request.LastScannedUID,
	}, nil
}

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
	if stderr != "" {
		t.Fatalf("partial sync JSONL emitted contradictory terminal stderr=%q", stderr)
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

func TestMultiFolderSyncJSONLPreservesFailedFirstFolderOrderAndAccounting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MAIL_PARTIAL_PASSWORD", "synthetic-mail-partial-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {{Folders: []transport.RemoteFolder{
			{Name: "INBOX", WireName: "INBOX", Kind: "inbox", KindSource: "inbox", Selectable: true, Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: "INBOX", Messages: 1, UIDNext: 22, UIDValidity: 100}},
			{Name: "Sent Messages", WireName: "Sent Messages", Kind: "sent", KindSource: "special_use", Selectable: true, Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: "Sent Messages", Messages: 1, UIDNext: 42, UIDValidity: 200}},
		}}},
	}}
	sentRaw := syntheticMailFixture(41, "partial-sync-41@example.test", "Later folder succeeds", "partial-sync-later-token", "")
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{
		{err: transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, true, errors.New("synthetic first-folder failure"))},
		{page: transport.InboxPage{
			Mailbox: "Sent Messages", UIDValidity: 200, LastCommittedUID: 41, LastScannedUID: 41, Bootstrap: true,
			Messages: []transport.FetchedMessage{{UID: 41, InternalDate: time.Date(2026, 8, 8, 2, 0, 0, 0, time.UTC)}},
		}, raw: map[uint32]string{41: sentRaw}},
	}}
	addAndDiscoverInbox(t, commands, home, "ICLOUD_MAIL_PARTIAL_PASSWORD")

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--all-folders", "--continue-on-error", "--jsonl")
	if code != cli.ExitUnavailable || stderr != "" {
		t.Fatalf("first-folder failure exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 ||
		!strings.Contains(lines[0], `"index":0`) || !strings.Contains(lines[0], `"status":"error"`) || !strings.Contains(lines[0], `"folder_name":"INBOX"`) || !strings.Contains(lines[0], `"committed_state":"not_committed"`) ||
		!strings.Contains(lines[1], `"index":1`) || !strings.Contains(lines[1], `"status":"committed"`) || !strings.Contains(lines[1], `"folder_name":"Sent Messages"`) || !strings.Contains(lines[1], `"committed_state":"committed"`) ||
		!strings.Contains(lines[2], `"attempted":2`) || !strings.Contains(lines[2], `"completed":1`) || !strings.Contains(lines[2], `"failed":1`) || !strings.Contains(lines[2], `"partial":true`) {
		t.Fatalf("ordered first-folder failure JSONL=%q", stdout)
	}
}

func TestAllFolderSyncSchedulesEverySelectableFolderPastOneThousand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MAIL_FOLDER_SCALE_PASSWORD", "synthetic-folder-scale-secret")
	commands, _ := newTestAccountCommands()
	remoteFolders := make([]transport.RemoteFolder, 0, 1001)
	for index := 0; index < 1001; index++ {
		name := fmt.Sprintf("Scale/%04d", index)
		remoteFolders = append(remoteFolders, transport.RemoteFolder{
			Name: name, WireName: name, Kind: "other", KindSource: "", Selectable: true,
			Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: name, UIDValidity: 1, UIDNext: 1},
		})
	}
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {{Folders: remoteFolders}},
	}}
	mailClient := &countingEmptyInboxClient{}
	commands.mail = mailClient
	addAndDiscoverInbox(t, commands, home, "ICLOUD_MAIL_FOLDER_SCALE_PASSWORD")

	_, folders, summaries, _, err := commands.MailSyncAll(context.Background(), home, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 1001 || len(summaries) != 1001 || mailClient.calls != 1001 {
		t.Fatalf("all-folder scheduling folders=%d summaries=%d calls=%d", len(folders), len(summaries), mailClient.calls)
	}
	seen := make(map[string]struct{}, len(mailClient.mailboxes))
	for _, mailbox := range mailClient.mailboxes {
		seen[mailbox] = struct{}{}
	}
	if len(seen) != 1001 {
		t.Fatalf("all-folder scheduling reached %d unique folders; want 1001", len(seen))
	}
}
