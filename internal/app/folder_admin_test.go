package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	"github.com/amxv/icloud-cli/internal/transport"
)

type fakeFolderAdminClient struct {
	requests  []transport.FolderAdminRequest
	calls     int
	renameErr error
}

func (client *fakeFolderAdminClient) AdministerFolder(_ context.Context, _ transport.AuthRequest, request transport.FolderAdminRequest) (transport.FolderAdminResult, error) {
	client.calls++
	client.requests = append(client.requests, request)
	switch request.Action {
	case transport.FolderAdminCreate:
		return transport.FolderAdminResult{Action: request.Action, Folder: transport.RemoteFolder{
			Name: request.NewMailbox, WireName: request.NewMailbox, ParentName: "Projects", ParentWireName: "Projects",
			Delimiter: "/", Kind: "other", Selectable: true, Subscription: "unknown",
		}}, nil
	case transport.FolderAdminRename:
		if client.renameErr != nil {
			err := client.renameErr
			client.renameErr = nil
			return transport.FolderAdminResult{}, err
		}
		return transport.FolderAdminResult{Action: request.Action, PreviousName: request.Mailbox, Folder: transport.RemoteFolder{
			Name: request.NewMailbox, WireName: request.NewMailbox, OldName: request.Mailbox, OldWireName: request.Mailbox,
			ParentName: "Projects", ParentWireName: "Projects", Delimiter: "/", Kind: "other", Selectable: true, Subscription: "unknown",
		}}, nil
	case transport.FolderAdminSubscribe:
		return transport.FolderAdminResult{Action: request.Action, Folder: transport.RemoteFolder{Name: request.Mailbox, WireName: request.Mailbox, Delimiter: "/", Kind: "other", Selectable: true, Subscription: "subscribed"}}, nil
	case transport.FolderAdminUnsubscribe:
		return transport.FolderAdminResult{Action: request.Action, Folder: transport.RemoteFolder{Name: request.Mailbox, WireName: request.Mailbox, Delimiter: "/", Kind: "other", Selectable: true, Subscription: "unsubscribed"}}, nil
	case transport.FolderAdminDelete:
		if !request.AllowNonEmpty {
			return transport.FolderAdminResult{}, &transport.FolderNonEmptyError{Messages: 1}
		}
		return transport.FolderAdminResult{Action: request.Action, Folder: transport.RemoteFolder{Name: request.Mailbox, WireName: request.Mailbox, Delimiter: "/", Kind: "other", Selectable: true}, Deleted: true, SourceMessages: 1}, nil
	default:
		return transport.FolderAdminResult{}, errors.New("unexpected folder admin request")
	}
}

func TestFolderAdministrationCLIIsPreviewableSafeAndPreservesStableID(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_FOLDER_ADMIN_PASSWORD", "synthetic-folder-admin-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {folderDiscoveryFixture(false)},
	}}
	admin := &fakeFolderAdminClient{}
	commands.folderAdmin = admin
	addAndDiscoverInbox(t, commands, home, "ICLOUD_FOLDER_ADMIN_PASSWORD")

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "create", "Projects/FolderAdmin-日本語", "--dry-run")
	if code != 0 || stderr != "" || admin.calls != 0 || !compactContainsAll(stdout, "folder-admin-preview", "action=create", "requested_name=Projects/FolderAdmin-日本語", "imap_contacted=false") {
		t.Fatalf("create dry-run code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, admin.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "create", "Projects/FolderAdmin-日本語")
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "requires --yes") || admin.calls != 0 {
		t.Fatalf("create authorization code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, admin.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "create", "Projects/FolderAdmin-日本語", "--yes")
	if code != 0 || stderr != "" || admin.calls != 1 || !compactContainsAll(stdout, "folder-admin", "action=create", "name=Projects/FolderAdmin-日本語", "present=yes", "sync=disabled") {
		t.Fatalf("create code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, admin.calls)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		_ = state.Close()
		t.Fatal(err)
	}
	created, err := state.Folders().Resolve(context.Background(), accountValue.ID, "Projects/FolderAdmin-日本語")
	if err != nil {
		_ = state.Close()
		t.Fatal(err)
	}
	createdID := created.ID
	_ = state.Close()

	priorCalls := admin.calls
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "rename", "sent", "Old Sent", "--yes")
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "protected") || admin.calls != priorCalls {
		t.Fatalf("protected rename code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, admin.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "rename", "Projects", "Projects-Old", "--yes")
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "child mailboxes") || admin.calls != priorCalls {
		t.Fatalf("parent rename code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, admin.calls)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "rename", createdID, "Projects/FolderAdmin-Renamed", "--yes")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "action=rename", "previous_name=Projects/FolderAdmin-日本語", "name=Projects/FolderAdmin-Renamed") {
		t.Fatalf("rename code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := state.Folders().Resolve(context.Background(), created.AccountID, "Projects/FolderAdmin-Renamed")
	if err != nil || renamed.ID != createdID {
		_ = state.Close()
		t.Fatalf("stable rename = %+v err=%v", renamed, err)
	}
	_ = state.Close()

	for _, test := range []struct {
		command string
		want    folder.Subscription
	}{
		{"subscribe", folder.SubscriptionSubscribed},
		{"unsubscribe", folder.SubscriptionUnsubscribed},
	} {
		code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", test.command, createdID, "--yes")
		if code != 0 || stderr != "" || !strings.Contains(stdout, "subscription="+string(test.want)) {
			t.Fatalf("%s code=%d stdout=%q stderr=%q", test.command, code, stdout, stderr)
		}
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "delete", createdID, "--yes")
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "not empty at iCloud") {
		t.Fatalf("non-empty delete code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "delete", createdID, "--allow-non-empty", "--yes")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "action=delete", "present=no", "deleted=true") {
		t.Fatalf("delete code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := state.Folders().Resolve(context.Background(), created.AccountID, createdID)
	_ = state.Close()
	if err != nil || deleted.Present || !deleted.Stale || deleted.SyncEnabled {
		t.Fatalf("deleted local folder = %+v err=%v", deleted, err)
	}
	if strings.Contains(stdout+stderr, "synthetic-folder-admin-secret") || strings.Contains(stdout+stderr, "ICLOUD_FOLDER_ADMIN_PASSWORD") {
		t.Fatal("folder administration exposed credential material")
	}
}

func TestFolderRenameTimeoutRecoversStableIDWithoutProviderOldName(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_RENAME_RECOVERY_PASSWORD", "synthetic-rename-recovery-secret")
	commands, _ := newTestAccountCommands()
	before := transport.FolderDiscoveryResult{Folders: []transport.RemoteFolder{
		{Name: "INBOX", WireName: "INBOX", Delimiter: "/", Kind: "inbox", KindSource: "inbox", Selectable: true, Subscription: "unknown", Status: &transport.MailboxStatus{Mailbox: "INBOX", UIDValidity: 100}},
		{Name: "Projects/Before", WireName: "Projects/Before", ParentName: "Projects", ParentWireName: "Projects", Delimiter: "/", Kind: "other", Selectable: true, Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: "Projects/Before", Messages: 1, UIDNext: 2, UIDValidity: 700}},
	}}
	after := transport.FolderDiscoveryResult{Folders: []transport.RemoteFolder{
		{Name: "INBOX", WireName: "INBOX", Delimiter: "/", Kind: "inbox", KindSource: "inbox", Selectable: true, Subscription: "unknown", Status: &transport.MailboxStatus{Mailbox: "INBOX", UIDValidity: 100}},
		{Name: "Projects/After-日本語", WireName: "Projects/After-&ZeVnLIqe-", ParentName: "Projects", ParentWireName: "Projects", Delimiter: "/", Kind: "other", Selectable: true, Subscription: "unknown", Status: &transport.MailboxStatus{Mailbox: "Projects/After-日本語", Messages: 1, UIDNext: 2, UIDValidity: 700}},
	}}
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {before, after},
	}}
	admin := &fakeFolderAdminClient{renameErr: &transport.FolderAdminTargetError{
		TargetMailbox: "Projects/After-日本語",
		Err:           transport.NewFailure(transport.StageIMAPRename, transport.FailureUnavailable, false, errors.New("response timeout after provider command")),
	}}
	commands.folderAdmin = admin
	addAndDiscoverInbox(t, commands, home, "ICLOUD_RENAME_RECOVERY_PASSWORD")
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "enable", "Projects/Before")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "sync=enabled") {
		t.Fatalf("enable before rename code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		_ = state.Close()
		t.Fatal(err)
	}
	original, err := state.Folders().Resolve(context.Background(), accountValue.ID, "Projects/Before")
	_ = state.Close()
	if err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "rename", original.ID, "Projects/After-日本語", "--yes")
	if code != cli.ExitUnavailable || stdout != "" || !strings.Contains(stderr, "refresh folder status before retrying") || admin.calls != 1 {
		t.Fatalf("ambiguous rename code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, admin.calls)
	}
	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := state.Folders().LatestRenameRecoveryForFolder(context.Background(), accountValue.ID, original.ID)
	if err != nil || recovery.Status != folder.RenameRecoveryPending {
		_ = state.Close()
		t.Fatalf("pending recovery = %+v err=%v", recovery, err)
	}
	_ = state.Close()

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "status")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "name=Projects/After-日本語") || strings.Contains(stdout, "name=Projects/Before") {
		t.Fatalf("recovery status code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := state.Folders().Resolve(context.Background(), accountValue.ID, "Projects/After-日本語")
	if err != nil || renamed.ID != original.ID || !renamed.SyncEnabled || renamed.Subscription != folder.SubscriptionSubscribed {
		_ = state.Close()
		t.Fatalf("recovered stable folder = %+v err=%v", renamed, err)
	}
	recovery, err = state.Folders().LatestRenameRecoveryForFolder(context.Background(), accountValue.ID, original.ID)
	if err != nil || recovery.Status != folder.RenameRecoveryResolved {
		_ = state.Close()
		t.Fatalf("resolved recovery = %+v err=%v", recovery, err)
	}
	_ = state.Close()
}

func TestExplicitAllFolderSyncSelectionIncludesDisabledSelectableFolders(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_ALL_FOLDER_PASSWORD", "synthetic-all-folder-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {folderDiscoveryFixture(false)},
	}}
	addAndDiscoverInbox(t, commands, home, "ICLOUD_ALL_FOLDER_PASSWORD")
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		_ = state.Close()
		t.Fatal(err)
	}
	enabled, err := state.Folders().CountSelectable(context.Background(), accountValue.ID, true)
	if err != nil {
		_ = state.Close()
		t.Fatal(err)
	}
	allCount, err := state.Folders().CountSelectable(context.Background(), accountValue.ID, false)
	if err != nil {
		_ = state.Close()
		t.Fatal(err)
	}
	all, more, next, err := state.Folders().ListSelectablePage(context.Background(), accountValue.ID, 3, "")
	if err != nil {
		_ = state.Close()
		t.Fatal(err)
	}
	if more {
		second, secondMore, _, pageErr := state.Folders().ListSelectablePage(context.Background(), accountValue.ID, 3, next)
		if pageErr != nil {
			_ = state.Close()
			t.Fatal(pageErr)
		}
		all = append(all, second...)
		more = secondMore
	}
	_ = state.Close()
	if enabled != 2 || allCount != 5 || len(all) != 5 || more {
		t.Fatalf("enabled=%d all_count=%d paged=%d more=%v", enabled, allCount, len(all), more)
	}
	for _, value := range all {
		if !value.Selectable || !value.Present || value.Stale {
			t.Fatalf("unsafe all-folder candidate: %+v", value)
		}
	}

	mailCalls := &fakeInboxClient{}
	commands.mail = mailCalls
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--all-folders", "--folder", "inbox")
	if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, "cannot be combined") || mailCalls.calls != 0 {
		t.Fatalf("all/folder conflict code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, mailCalls.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--all-folders", "--hydrate", "rem_0198d704-6c00-7000-8000-000000000001")
	if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, "cannot be combined") || mailCalls.calls != 0 {
		t.Fatalf("all/hydrate conflict code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, mailCalls.calls)
	}
}

func TestEnabledCustomFolderUsesOrdinaryBoundedBootstrapAndOwnCheckpoint(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_CUSTOM_FOLDER_PASSWORD", "synthetic-custom-folder-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {folderDiscoveryFixture(false)},
	}}
	mailClient := &fakeInboxClient{results: []fakeInboxPageResult{{page: transport.InboxPage{
		Mailbox: "Unicode-日本語", UIDValidity: 104, LastCommittedUID: 1, LastScannedUID: 1, Bootstrap: true,
	}}}}
	commands.mail = mailClient
	addAndDiscoverInbox(t, commands, home, "ICLOUD_CUSTOM_FOLDER_PASSWORD")
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "enable", "Unicode-日本語")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "sync=enabled") {
		t.Fatalf("enable custom code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--folder", "Unicode-日本語", "--limit", "7")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "folder=Unicode-日本語", "bootstrap=true", "last_uid=1") {
		t.Fatalf("custom sync code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(mailClient.requests) != 1 || mailClient.requests[0].Mailbox != "Unicode-日本語" || strings.Contains(mailClient.requests[0].Mailbox, "&ZeVnLIqe-") || !mailClient.requests[0].Bootstrap || mailClient.requests[0].TailOnlyBootstrap || mailClient.requests[0].Limit != 7 {
		t.Fatalf("custom sync request = %+v", mailClient.requests)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "status", "--folder", "Unicode-日本語")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "folder=Unicode-日本語", "uid_validity=104", "committed=1", "scanned=1") {
		t.Fatalf("custom checkpoint code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
