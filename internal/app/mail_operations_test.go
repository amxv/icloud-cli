package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

type fakeBackfillPage struct {
	page transport.BackfillPage
	raw  map[uint32]string
	err  error
}

type fakeMailOperationsClient struct {
	pages          []fakeBackfillPage
	requests       []transport.BackfillFetchRequest
	status         transport.MailboxStatus
	statusRequests []string
}

func (client *fakeMailOperationsClient) ValidateIMAP(context.Context, transport.AuthRequest) (transport.IMAPResult, error) {
	return transport.IMAPResult{}, nil
}

func (client *fakeMailOperationsClient) FetchBackfillPage(ctx context.Context, _ transport.AuthRequest, request transport.BackfillFetchRequest, stager blob.Stager) (transport.BackfillPage, error) {
	client.requests = append(client.requests, request)
	if len(client.pages) == 0 {
		return transport.BackfillPage{}, errors.New("unexpected backfill fetch")
	}
	result := client.pages[0]
	client.pages = client.pages[1:]
	if result.err != nil {
		return transport.BackfillPage{}, result.err
	}
	page := result.page
	for index := range page.Messages {
		raw := result.raw[page.Messages[index].UID]
		staged, err := stager.Stage(ctx, strings.NewReader(raw), int64(len(raw)))
		if err != nil {
			return transport.BackfillPage{}, err
		}
		page.Messages[index].Raw = staged
		page.Messages[index].RFC822Size = int64(len(raw))
	}
	return page, nil
}

func (client *fakeMailOperationsClient) MailboxStatus(_ context.Context, _ transport.AuthRequest, mailbox string) (transport.MailboxStatus, error) {
	client.statusRequests = append(client.statusRequests, mailbox)
	status := client.status
	status.Mailbox = mailbox
	return status, nil
}

func TestBackfillResumesAcrossCancellationWhileForwardSyncContinues(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_BACKFILL_PASSWORD", "synthetic-backfill-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {
			{Folders: []transport.RemoteFolder{
				{Name: "INBOX", WireName: "INBOX", Kind: "inbox", KindSource: "inbox", Selectable: true, Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: "INBOX", Messages: 24, UIDNext: 25, UIDValidity: 100}},
			}},
		},
	}}
	forward23 := syntheticMailFixture(23, "forward-sync-23@example.test", "Forward 23", "forward body 23", "")
	forward24 := syntheticMailFixture(24, "forward-sync-24@example.test", "Forward 24", "forward body 24", "")
	forward := &fakeInboxClient{results: []fakeInboxPageResult{
		{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 23, LastScannedUID: 23, Bootstrap: true, Messages: []transport.FetchedMessage{{UID: 23, InternalDate: time.Date(2026, 8, 6, 23, 0, 0, 0, time.UTC)}}}, raw: map[uint32]string{23: forward23}},
		{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 24, LastScannedUID: 24, Messages: []transport.FetchedMessage{{UID: 24, InternalDate: time.Date(2026, 8, 7, 0, 1, 0, 0, time.UTC)}}}, raw: map[uint32]string{24: forward24}},
	}}
	commands.mail = forward
	backfill10 := syntheticMailFixture(10, "backfill-10@example.test", "Historical 10", "history body 10", "")
	backfill5 := syntheticMailFixture(5, "backfill-5@example.test", "Historical 5", "history body 5", "")
	operations := &fakeMailOperationsClient{pages: []fakeBackfillPage{
		{page: transport.BackfillPage{Mailbox: "INBOX", UIDValidity: 100, BeforeUID: 24, NextBeforeUID: 10, Messages: []transport.FetchedMessage{{UID: 10, InternalDate: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)}}}, raw: map[uint32]string{10: backfill10}},
		{err: context.Canceled},
		{page: transport.BackfillPage{Mailbox: "INBOX", UIDValidity: 100, BeforeUID: 10, NextBeforeUID: 1, Complete: true, Messages: []transport.FetchedMessage{{UID: 5, InternalDate: time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)}}}, raw: map[uint32]string{5: backfill5}},
	}}
	commands.imap = operations
	addAndDiscoverInbox(t, commands, home, "ICLOUD_BACKFILL_PASSWORD")

	if code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--limit", "1"); code != 0 || stderr != "" || !strings.Contains(stdout, "last_uid=23") {
		t.Fatalf("initial sync code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "backfill", "--folder", "inbox", "--limit", "1")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "mail-backfill\n", "fetched=1", "before_uid=24", "next_before_uid=10", "complete=no") {
		t.Fatalf("first backfill code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if lines, bytes := strings.Count(stdout, "\n"), len(stdout); lines > 14 || bytes > 650 {
		t.Fatalf("backfill compact budget lines=%d bytes=%d output=%q", lines, bytes, stdout)
	}
	if code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--limit", "1"); code != 0 || stderr != "" || !strings.Contains(stdout, "last_uid=24") {
		t.Fatalf("forward while backfill pending code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "status"); code != 0 || stderr != "" || !compactContainsAll(stdout, "committed=24", "scanned=24") {
		t.Fatalf("forward status code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "backfill", "--folder", "inbox", "--limit", "1")
	if code != cli.ExitUnavailable || stdout != "" || !strings.Contains(stderr, "mail synchronization failed") {
		t.Fatalf("cancelled backfill code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	folderValue, err := state.Folders().Resolve(context.Background(), accountValue.ID, "inbox")
	if err != nil {
		t.Fatal(err)
	}
	frontier, err := state.Mail().BackfillFrontier(context.Background(), accountValue.ID, folderValue.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if frontier.BeforeUID != 10 || frontier.Complete {
		t.Fatalf("cancel advanced frontier: %+v", frontier)
	}
	_ = state.Close()

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "backfill", "--folder", "inbox", "--limit", "1")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "before_uid=10", "next_before_uid=1", "complete=yes") {
		t.Fatalf("resumed backfill code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(operations.requests) != 3 || operations.requests[0].BeforeUID != 24 || operations.requests[1].BeforeUID != 10 || operations.requests[2].BeforeUID != 10 {
		t.Fatalf("backfill requests = %+v", operations.requests)
	}
}

func TestRebaselineAndRepairPreserveCacheAndRequireExplicitOrphanCleanup(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_REPAIR_PASSWORD", "synthetic-repair-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	raw := syntheticMailFixture(20, "repair-20@example.test", "Repair fixture", "repair body", "")
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 20, LastScannedUID: 20, Bootstrap: true, Messages: []transport.FetchedMessage{{UID: 20, InternalDate: time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)}}}, raw: map[uint32]string{20: raw}}}}
	operations := &fakeMailOperationsClient{pages: []fakeBackfillPage{{page: transport.BackfillPage{Mailbox: "INBOX", UIDValidity: 100, BeforeUID: 20, NextBeforeUID: 10}}}, status: transport.MailboxStatus{Messages: 30, UIDNext: 31, UIDValidity: 200}}
	commands.imap = operations
	addAndDiscoverInbox(t, commands, home, "ICLOUD_REPAIR_PASSWORD")
	if code, _, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync"); code != 0 || stderr != "" {
		t.Fatalf("sync code=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "backfill", "--folder", "inbox"); code != 0 || stderr != "" {
		t.Fatalf("backfill code=%d stderr=%q", code, stderr)
	}

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "rebaseline", "--folder", "inbox", "--current-tail")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "before_committed=20", "after_committed=30", "uid_validity=200", "cached_kept=1", "deleted=no") {
		t.Fatalf("rebaseline code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if lines, bytes := strings.Count(stdout, "\n"), len(stdout); lines > 16 || bytes > 720 {
		t.Fatalf("rebaseline compact budget lines=%d bytes=%d output=%q", lines, bytes, stdout)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "inbox")
	if code != 0 || stderr != "" || compactBlockCount(stdout, "message") != 1 {
		t.Fatalf("cache after rebaseline code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(paths.BlobRoot, ".staging"), 0o700); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(paths.BlobRoot, ".staging", "synthetic-orphan.tmp")
	if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE message_search_documents SET subject='stale repair subject' WHERE account_id=?`, accountValue.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_ = state.Close()

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "repair", "--check")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "mode=check", "changed=0", "stale_search=1", "temp_orphans=1", "frontier_problems=1", "next=run_apply_cleanup_orphans") {
		t.Fatalf("repair check code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if lines, bytes := strings.Count(stdout, "\n"), len(stdout); lines > 18 || bytes > 850 {
		t.Fatalf("repair compact budget lines=%d bytes=%d output=%q", lines, bytes, stdout)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("check removed orphan: %v", err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "repair", "--apply")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "mode=apply", "next=rerun_apply_cleanup_orphans") {
		t.Fatalf("repair apply code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("apply without cleanup removed orphan: %v", err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "repair", "--apply", "--cleanup-orphans")
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "requires --yes") {
		t.Fatalf("cleanup safety code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "repair", "--apply", "--cleanup-orphans", "--yes")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "temp_orphans=0", "frontier_problems=0", "stale_search=0", "next=none") {
		t.Fatalf("cleanup apply code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan still exists: %v", err)
	}
}

func TestRepairUsesTargetedStatusToRecoverUIDValidityWithoutDeletingCache(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_UID_REPAIR_PASSWORD", "synthetic-uid-repair-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	raw := syntheticMailFixture(20, "uid-repair-20@example.test", "UID fixture", "uid body", "")
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{
		{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 20, LastScannedUID: 20, Bootstrap: true, Messages: []transport.FetchedMessage{{UID: 20, InternalDate: time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)}}}, raw: map[uint32]string{20: raw}},
		{err: &transport.UIDValidityError{Expected: 100, Observed: 300}},
	}}
	operations := &fakeMailOperationsClient{status: transport.MailboxStatus{Messages: 40, UIDNext: 41, UIDValidity: 300}}
	commands.imap = operations
	addAndDiscoverInbox(t, commands, home, "ICLOUD_UID_REPAIR_PASSWORD")
	if code, _, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync"); code != 0 || stderr != "" {
		t.Fatalf("initial sync code=%d stderr=%q", code, stderr)
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync")
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "folder identity changed") {
		t.Fatalf("UID mismatch code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "repair", "--check")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "uid_repairs=1", "next=run_apply") {
		t.Fatalf("repair check code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "repair", "--apply")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "uid_repairs=0", "next=none") || len(operations.statusRequests) != 1 || operations.statusRequests[0] != "INBOX" {
		t.Fatalf("repair apply code=%d stdout=%q stderr=%q status=%v", code, stdout, stderr, operations.statusRequests)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "status")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "uid_validity=300", "committed=40", "state=idle") {
		t.Fatalf("repaired status code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "inbox")
	if code != 0 || stderr != "" || compactBlockCount(stdout, "message") != 1 {
		t.Fatalf("UID repair deleted cache code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestBackfillUIDValidityRepairWorksWithoutForwardCheckpoint(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_BACKFILL_UID_PASSWORD", "synthetic-backfill-uid-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	firstRaw := syntheticMailFixture(10, "backfill-uid-10@example.test", "Historical namespace one", "history namespace one", "")
	operations := &fakeMailOperationsClient{
		pages: []fakeBackfillPage{
			{page: transport.BackfillPage{Mailbox: "INBOX", UIDValidity: 100, BeforeUID: 21, NextBeforeUID: 10, Messages: []transport.FetchedMessage{{UID: 10, InternalDate: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)}}}, raw: map[uint32]string{10: firstRaw}},
			{err: &transport.UIDValidityError{Expected: 100, Observed: 200}},
			{page: transport.BackfillPage{Mailbox: "INBOX", UIDValidity: 200, BeforeUID: 31, NextBeforeUID: 1, Complete: true}},
		},
		status: transport.MailboxStatus{Messages: 30, UIDNext: 31, UIDValidity: 200},
	}
	commands.imap = operations
	addAndDiscoverInbox(t, commands, home, "ICLOUD_BACKFILL_UID_PASSWORD")

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "backfill", "--limit", "1")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "before_uid=21", "next_before_uid=10") {
		t.Fatalf("first backfill code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "backfill", "--limit", "1")
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "run mail sync repair") {
		t.Fatalf("changed namespace code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "repair", "--check")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "frontier_problems=1", "uid_repairs=0", "next=run_apply") {
		t.Fatalf("backfill repair check code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "repair", "--apply")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "frontier_problems=0", "next=none") || len(operations.statusRequests) != 1 || operations.statusRequests[0] != "INBOX" {
		t.Fatalf("backfill repair apply code=%d stdout=%q stderr=%q status=%v", code, stdout, stderr, operations.statusRequests)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "backfill", "--limit", "1")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "before_uid=31", "complete=yes") {
		t.Fatalf("backfill after repair code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(operations.requests) != 3 || operations.requests[2].ExpectedUIDValidity != 200 || operations.requests[2].BeforeUID != 31 {
		t.Fatalf("post-repair backfill requests = %+v", operations.requests)
	}
}

var _ = maildomain.RepairCheck

func TestRepairReportsMissingCanonicalBlobWithoutInventingReplacement(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_BLOB_REPAIR_PASSWORD", "synthetic-blob-repair-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	raw := syntheticMailFixture(20, "blob-repair-20@example.test", "Blob fixture", "blob body", "")
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 20, LastScannedUID: 20, Bootstrap: true, Messages: []transport.FetchedMessage{{UID: 20, InternalDate: time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)}}}, raw: map[uint32]string{20: raw}}}}
	commands.imap = &fakeMailOperationsClient{}
	addAndDiscoverInbox(t, commands, home, "ICLOUD_BLOB_REPAIR_PASSWORD")
	if code, _, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync"); code != 0 || stderr != "" {
		t.Fatalf("sync code=%d stderr=%q", code, stderr)
	}
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	folderValue, err := state.Folders().Resolve(context.Background(), accountValue.ID, "inbox")
	if err != nil {
		t.Fatal(err)
	}
	messages, _, err := state.Mail().Inbox(context.Background(), accountValue.ID, folderValue.ID, 20)
	if err != nil || len(messages) != 1 {
		t.Fatalf("messages=%d err=%v", len(messages), err)
	}
	storageKey := messages[0].Locations[0].RawStorageKey
	_ = state.Close()
	if err := os.Remove(filepath.Join(paths.BlobRoot, filepath.FromSlash(storageKey))); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "repair", "--check", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"canonical_blob_issues":1`) || !strings.Contains(stdout, `"kind":"canonical_blob"`) || !strings.Contains(stdout, `"applicable":false`) || !strings.Contains(stdout, `"next_action":"review_verbose_or_json"`) || !strings.Contains(stdout, `targeted provider recovery or restore from backup`) {
		t.Fatalf("blob check code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "repair", "--apply", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"canonical_blob_issues":1`) || strings.Contains(stdout, `"applied":true`) {
		t.Fatalf("blob apply guessed replacement code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(paths.BlobRoot, filepath.FromSlash(storageKey))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repair recreated missing blob without evidence: %v", err)
	}
}
