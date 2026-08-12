package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
)

func TestFolderRoleCLIImmediatelyControlsArchiveWithoutChangingProviderKind(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-folderRole-secret")
	commands, _ := newTestAccountCommands()
	client := &fakeLocationClient{}
	commands.locations = client
	addSendAccount(t, commands, home)
	fixture := seedLocationMutationFixture(t, home, []locationSeed{{folder.KindInbox, 1, `\Seen`}})
	ordinary := fixture.folders[folder.KindOther]
	providerArchive := fixture.folders[folder.KindArchive]

	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "archive", fixture.messageID, "--dry-run")
	if code != 0 || stderr != "" || client.calls != 0 || !strings.Contains(stdout, "destination_folder="+providerArchive.ID) {
		t.Fatalf("provider archive preview code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "folders", "role", "set", "archive", ordinary.ID, "--dry-run")
	if code != 0 || stderr != "" || client.calls != 0 || !compactContainsAll(stdout,
		"folder-role-preview\narchive", "action=set", "folder_id="+ordinary.ID,
		"provider_kind=other", "provider_contact=false",
	) {
		t.Fatalf("role set preview code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "folders", "role", "list")
	if code != 0 || stderr != "" || strings.Contains(stdout, "folder-role\narchive") {
		t.Fatalf("dry-run persisted binding code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "folders", "role", "set", "archive", ordinary.ID)
	if code != 0 || stderr != "" || !compactContainsAll(stdout,
		"folder-role\narchive", "folder_id="+ordinary.ID, "provider_kind=other", "action=set",
	) {
		t.Fatalf("role set code=%d stdout=%q stderr=%q", code, stdout, stderr)
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
	unchanged, err := state.Folders().Resolve(context.Background(), accountValue.ID, ordinary.ID)
	if err != nil || unchanged.Kind != folder.KindOther || unchanged.KindSource != "" {
		state.Close()
		t.Fatalf("binding rewrote provider kind: %+v err=%v", unchanged, err)
	}
	state.Close()

	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "archive", fixture.messageID, "--dry-run")
	if code != 0 || stderr != "" || client.calls != 0 || !strings.Contains(stdout, "destination_folder="+ordinary.ID) {
		t.Fatalf("bound archive preview code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "folders", "role", "clear", "archive", "--dry-run")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "folder-role-preview\narchive", "action=clear", "folder_id="+ordinary.ID, "provider_contact=false") {
		t.Fatalf("role clear preview code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "folders", "role", "clear", "archive")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "action=cleared") {
		t.Fatalf("role clear code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "archive", fixture.messageID, "--dry-run")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "destination_folder="+providerArchive.ID) {
		t.Fatalf("provider archive did not resume code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	trashInput := filepath.Join(t.TempDir(), "trash-role.json")
	if err := os.WriteFile(trashInput, []byte(`{"role":"trash","folder":"`+ordinary.ID+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "folders", "role", "set", "--input-json", trashInput)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "folder-role\ntrash") {
		t.Fatalf("trash role set code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "trash", fixture.messageID, "--dry-run")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "destination_folder="+ordinary.ID) {
		t.Fatalf("bound trash preview code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "folders", "role", "clear", "trash")
	if code != 0 || stderr != "" {
		t.Fatalf("trash role clear code=%d stderr=%q", code, stderr)
	}

	code, _, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "folders", "role", "set", "archive", ordinary.ID)
	if code != 0 || stderr != "" {
		t.Fatalf("reset role code=%d stderr=%q", code, stderr)
	}
	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Folders().MarkDeleted(context.Background(), accountValue.ID, ordinary.ID, time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)); err != nil {
		state.Close()
		t.Fatal(err)
	}
	state.Close()
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "archive", fixture.messageID, "--dry-run")
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "clear or rebind") || client.calls != 0 {
		t.Fatalf("stale binding code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
}

func TestDraftsBindingEnablesImplicitDraftSync(t *testing.T) {
	commands, _ := newTestAccountCommands()
	home := setupUnclassifiedDraftAccount(t, commands)
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	drafts, err := state.Folders().Resolve(context.Background(), accountValue.ID, "Drafts")
	if err != nil || drafts.Kind != folder.KindOther {
		state.Close()
		t.Fatalf("unclassified drafts=%+v err=%v", drafts, err)
	}
	state.Close()

	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "folders", "role", "set", "drafts", drafts.ID)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "provider_kind=other") {
		t.Fatalf("drafts binding code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	inbox := &appendedDraftInboxClient{uidValidity: drafts.UIDValidity}
	commands.mail = inbox
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "draft", "sync", "--limit", "5")
	if code != 0 || stderr != "" || inbox.calls != 1 || !strings.Contains(stdout, "folder=Drafts") {
		t.Fatalf("implicit draft sync code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, inbox.calls)
	}
	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	unchanged, err := state.Folders().Resolve(context.Background(), accountValue.ID, drafts.ID)
	if err != nil || unchanged.Kind != folder.KindOther || unchanged.KindSource != "" {
		t.Fatalf("Drafts binding changed provider metadata: %+v err=%v", unchanged, err)
	}
}

func TestSentBindingControlsAppendTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-folderRole-sent-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	appender := &fakeSentAppender{}
	commands.sender = sender
	commands.sent = appender
	addSendAccount(t, commands, home)
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	values, err := state.Folders().Reconcile(context.Background(), accountValue.ID, []folder.RemoteObservation{{
		Name: "Agent Sent", WireName: "Agent Sent", Kind: folder.KindOther,
		Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 500, StatusKnown: true,
	}}, time.Date(2026, 8, 10, 6, 30, 0, 0, time.UTC), func() (string, error) { return "fld_folderRole_sent_bound", nil })
	if err != nil || len(values) != 1 {
		state.Close()
		t.Fatalf("sent ordinary folders=%+v err=%v", values, err)
	}
	sentFolder := values[0]
	state.Close()
	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "folders", "role", "set", "sent", sentFolder.ID)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "provider_kind=other") {
		t.Fatalf("sent binding code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "send", "--to", "recipient@example.test", "--subject", "Folder role sent binding",
		"--body", "folderRole sent body", "--idempotency-key", "folderRole-sent-binding-01", "--yes")
	if code != 0 || stderr != "" || sender.calls != 1 || appender.calls != 1 {
		t.Fatalf("bound sent code=%d stdout=%q stderr=%q smtp=%d append=%d", code, stdout, stderr, sender.calls, appender.calls)
	}
	if appender.requests[0].Mailbox != sentFolder.Name {
		t.Fatalf("Sent append mailbox=%q want=%q", appender.requests[0].Mailbox, sentFolder.Name)
	}
	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	unchanged, err := state.Folders().Resolve(context.Background(), accountValue.ID, sentFolder.ID)
	if err != nil || unchanged.Kind != folder.KindOther || unchanged.KindSource != "" {
		t.Fatalf("Sent binding changed provider metadata: %+v err=%v", unchanged, err)
	}
}
