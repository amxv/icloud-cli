package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestMailPageCommitIsAtomicBoundedAndIdempotent(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 4, 45, 0, 0, time.UTC)
	record := mailRecordFixture(accountID, folderID, 100, 21, "fixture-21@example.test")
	result, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000504",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100, Bootstrap: true,
		LastCommittedUID: 21, LastScannedUID: 21, AttemptedAt: now, Records: []maildomain.IngestRecord{record},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 1 || result.Updated != 0 || !result.Checkpoint.BootstrapComplete || result.Checkpoint.LastCommittedUID != 21 {
		t.Fatalf("first commit = %+v", result)
	}
	messages, more, err := state.Mail().Inbox(context.Background(), accountID, folderID, 1)
	if err != nil || more || len(messages) != 1 {
		t.Fatalf("inbox len=%d more=%v err=%v", len(messages), more, err)
	}
	message := messages[0]
	if message.RFCMessageID != "fixture-21@example.test" || !message.Unread || !message.Flagged || len(message.To) != 1 || len(message.References) != 1 || len(message.Locations) != 1 {
		t.Fatalf("message = %+v", message)
	}

	retryRecord := mailRecordFixture(accountID, folderID, 100, 21, "fixture-21@example.test")
	retryRecord.MessageID = "msg_0198d704-6c00-7000-8000-000000009999"
	retryRecord.LocationID = "loc_0198d704-6c00-7000-8000-000000009999"
	retryRecord.Flags = []string{`\Seen`}
	result, err = state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000504",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100,
		LastCommittedUID: 21, LastScannedUID: 30, AttemptedAt: now.Add(time.Minute), Records: []maildomain.IngestRecord{retryRecord},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 0 || result.Updated != 1 || !result.Checkpoint.BootstrapComplete || result.Checkpoint.LastScannedUID != 30 {
		t.Fatalf("repeat commit = %+v", result)
	}
	messages, _, err = state.Mail().Inbox(context.Background(), accountID, folderID, 1)
	if err != nil || len(messages) != 1 || messages[0].Unread {
		t.Fatalf("repeat inbox = %+v err=%v", messages, err)
	}
	if messages[0].ID != record.MessageID || messages[0].Locations[0].ID != record.LocationID {
		t.Fatalf("retry changed immutable IDs: message=%s location=%s", messages[0].ID, messages[0].Locations[0].ID)
	}
}

func TestMailTransactionFailureDoesNotAdvanceCheckpointOrPersistMessage(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 4, 50, 0, 0, time.UTC)
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000504",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100, Bootstrap: true,
		LastCommittedUID: 20, LastScannedUID: 20, AttemptedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.Exec(`
CREATE TRIGGER synthetic_checkpoint_failure
BEFORE UPDATE ON sync_checkpoints
WHEN NEW.last_committed_uid = 22
BEGIN
    SELECT RAISE(ABORT, 'synthetic transaction failure');
END;`); err != nil {
		t.Fatal(err)
	}
	_, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000504",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100,
		LastCommittedUID: 22, LastScannedUID: 22, AttemptedAt: now.Add(time.Minute),
		Records: []maildomain.IngestRecord{mailRecordFixture(accountID, folderID, 100, 21, "fixture-21@example.test"), mailRecordFixture(accountID, folderID, 100, 22, "fixture-22@example.test")},
	})
	if err == nil || !strings.Contains(err.Error(), "synthetic transaction failure") {
		t.Fatalf("transaction error = %v", err)
	}
	checkpoint, err := state.Mail().Checkpoint(context.Background(), accountID, folderID)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.LastCommittedUID != 20 || checkpoint.LastScannedUID != 20 || checkpoint.State != maildomain.CheckpointIdle {
		t.Fatalf("checkpoint advanced: %+v", checkpoint)
	}
	messages, more, err := state.Mail().Inbox(context.Background(), accountID, folderID, 20)
	if err != nil || more || len(messages) != 0 {
		t.Fatalf("rolled-back messages=%d more=%v err=%v", len(messages), more, err)
	}
}

func TestMailUIDValidityMismatchRequiresVisibleRepairWithoutCursorReset(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 4, 55, 0, 0, time.UTC)
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000504",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100, Bootstrap: true,
		LastCommittedUID: 40, LastScannedUID: 40, AttemptedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000504",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 200,
		LastCommittedUID: 0, LastScannedUID: 0, AttemptedAt: now.Add(time.Minute),
	}); !errors.Is(err, maildomain.ErrRepairRequired) {
		t.Fatalf("mismatch error = %v", err)
	}
	checkpoint, err := state.Mail().RequireRepair(context.Background(), "chk_0198d704-6c00-7000-8000-000000000504", accountID, folderID, 200, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.State != maildomain.CheckpointRepairRequired || checkpoint.UIDValidity != 200 || checkpoint.LastCommittedUID != 40 || checkpoint.LastScannedUID != 40 || checkpoint.LastErrorCode != "uid_validity_changed" {
		t.Fatalf("repair checkpoint = %+v", checkpoint)
	}
}

func TestMailContentMetadataAndPartIDsPersistAcrossRetry(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 5, 5, 0, 0, time.UTC)
	record := mailRecordFixture(accountID, folderID, 100, 51, "content-51@example.test")
	record.Content = &maildomain.NormalizedContent{
		Preview:   record.Parsed,
		Headers:   []maildomain.HeaderField{{Key: "Message-Id", Value: "<content-51@example.test>"}, {Key: "Subject", Value: "Fixture message"}},
		TextPaths: []string{"1"},
		HTMLPaths: []string{"2"},
		Parts: []maildomain.MIMEPart{
			{ID: "prt_0198d704-6c00-7000-8000-000000000551", Ordinal: 0, Path: "0", MediaType: "multipart/alternative"},
			{ID: "prt_0198d704-6c00-7000-8000-000000000552", Ordinal: 1, Path: "1", ParentPath: "0", MediaType: "text/plain", Charset: "utf-8", DecodedSize: 14},
			{ID: "prt_0198d704-6c00-7000-8000-000000000553", Ordinal: 2, Path: "2", ParentPath: "0", MediaType: "text/html", Charset: "utf-8", DecodedSize: 21},
		},
	}
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000504",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100, Bootstrap: true,
		LastCommittedUID: 51, LastScannedUID: 51, AttemptedAt: now, Records: []maildomain.IngestRecord{record},
	}); err != nil {
		t.Fatal(err)
	}
	message, err := state.Mail().Message(context.Background(), accountID, record.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.Content == nil || len(message.Content.Headers) != 2 || len(message.Content.Parts) != 3 ||
		strings.Join(message.Content.TextPaths, ",") != "1" || strings.Join(message.Content.HTMLPaths, ",") != "2" {
		t.Fatalf("content = %+v", message.Content)
	}
	originalPartIDs := []string{message.Content.Parts[0].ID, message.Content.Parts[1].ID, message.Content.Parts[2].ID}

	retry := record
	retry.Content = &maildomain.NormalizedContent{
		Preview:   record.Parsed,
		Headers:   record.Content.Headers,
		TextPaths: []string{"1"}, HTMLPaths: []string{"2"},
		Parts: []maildomain.MIMEPart{
			{ID: "prt_0198d704-6c00-7000-8000-000000009551", Ordinal: 0, Path: "0", MediaType: "multipart/alternative"},
			{ID: "prt_0198d704-6c00-7000-8000-000000009552", Ordinal: 1, Path: "1", ParentPath: "0", MediaType: "text/plain", Charset: "utf-8", DecodedSize: 15},
			{ID: "prt_0198d704-6c00-7000-8000-000000009553", Ordinal: 2, Path: "2", ParentPath: "0", MediaType: "text/html", Charset: "utf-8", DecodedSize: 22},
		},
	}
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000504",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100,
		LastCommittedUID: 51, LastScannedUID: 60, AttemptedAt: now.Add(time.Minute), Records: []maildomain.IngestRecord{retry},
	}); err != nil {
		t.Fatal(err)
	}
	message, err = state.Mail().Message(context.Background(), accountID, record.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	for index, part := range message.Content.Parts {
		if part.ID != originalPartIDs[index] {
			t.Fatalf("part %s changed ID: got %s want %s", part.Path, part.ID, originalPartIDs[index])
		}
	}
	if message.Content.Parts[1].DecodedSize != 15 || message.Content.Parts[2].DecodedSize != 22 {
		t.Fatalf("part metadata was not refreshed: %+v", message.Content.Parts)
	}
}

func TestMailAttachmentsKeepDistinctMetadataForDuplicateContent(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 5, 15, 0, 0, time.UTC)
	record := mailRecordFixture(accountID, folderID, 100, 52, "attachments-52@example.test")
	hash := strings.Repeat("c", 64)
	record.Content = &maildomain.NormalizedContent{
		Preview:            record.Parsed,
		AttachmentsIndexed: true,
		Parts: []maildomain.MIMEPart{
			{ID: "prt_0198d704-6c00-7000-8000-000000000561", Ordinal: 0, Path: "0", MediaType: "multipart/mixed"},
			{ID: "prt_0198d704-6c00-7000-8000-000000000562", Ordinal: 1, Path: "1", ParentPath: "0", MediaType: "application/pdf", MediaParams: map[string]string{"name": "first.pdf"}, Filename: "first.pdf", Disposition: "attachment", Attachment: true, DecodedSize: 4},
			{ID: "prt_0198d704-6c00-7000-8000-000000000563", Ordinal: 2, Path: "2", ParentPath: "0", MediaType: "application/pdf", MediaParams: map[string]string{"name": "second.pdf"}, Filename: "second.pdf", Disposition: "attachment", Attachment: true, DecodedSize: 4},
		},
		Attachments: []maildomain.Attachment{
			{ID: "att_0198d704-6c00-7000-8000-000000000564", MessageID: record.MessageID, PartID: "prt_0198d704-6c00-7000-8000-000000000562", Ordinal: 1, PartPath: "1", Filename: "first.pdf", MediaType: "application/pdf", MediaParams: map[string]string{"name": "first.pdf"}, Disposition: "attachment", ContentHash: hash, StorageKey: "attachments/sha256/cc/" + hash + ".blob", ByteSize: 4, Availability: maildomain.AttachmentAvailable},
			{ID: "att_0198d704-6c00-7000-8000-000000000565", MessageID: record.MessageID, PartID: "prt_0198d704-6c00-7000-8000-000000000563", Ordinal: 2, PartPath: "2", Filename: "second.pdf", MediaType: "application/pdf", MediaParams: map[string]string{"name": "second.pdf"}, Disposition: "attachment", ContentHash: hash, StorageKey: "attachments/sha256/cc/" + hash + ".blob", ByteSize: 4, Availability: maildomain.AttachmentAvailable},
		},
	}
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000504",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100, Bootstrap: true,
		LastCommittedUID: 52, LastScannedUID: 52, AttemptedAt: now,
		Records: []maildomain.IngestRecord{record},
	}); err != nil {
		t.Fatal(err)
	}
	values, more, err := state.Mail().Attachments(context.Background(), accountID, record.MessageID, 20)
	if err != nil || more || len(values) != 2 {
		t.Fatalf("attachments=%+v more=%v err=%v", values, more, err)
	}
	if values[0].ID == values[1].ID || values[0].ContentHash != values[1].ContentHash || values[0].Filename == values[1].Filename ||
		values[0].MediaParams["name"] == values[1].MediaParams["name"] {
		t.Fatalf("duplicate-content metadata collapsed: %+v", values)
	}
	first, err := state.Mail().Attachment(context.Background(), accountID, values[0].ID)
	if err != nil || first.Filename != values[0].Filename {
		t.Fatalf("single attachment=%+v err=%v", first, err)
	}
}

func TestMailLocationFlagsReconcileAndDeriveSummariesAcrossEligibleLocations(t *testing.T) {
	state, accountID, inboxID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 5, 25, 0, 0, time.UTC)
	archiveID := "fld_0198d704-6c00-7000-8000-000000000570"
	if _, err := state.db.Exec(`
INSERT INTO folders (
    folder_id, account_id, name, wire_name, kind, kind_source, selectable,
    subscription, present, stale, sync_enabled, uid_validity,
    discovered_at, status_at, created_at, updated_at
) VALUES (?, ?, 'Archive', 'Archive', 'archive', 'special_use', 1,
          'subscribed', 1, 0, 1, 200, ?, ?, ?, ?)`,
		archiveID, accountID, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
	); err != nil {
		t.Fatal(err)
	}
	inboxRecord := mailRecordFixture(accountID, inboxID, 100, 61, "state-summary@example.test")
	inboxRecord.Flags = []string{`\Seen`, `$InboxKeyword`}
	inboxRecord.ModSequence = 10
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000571",
		AccountID:    accountID, FolderID: inboxID, UIDValidity: 100, Bootstrap: true,
		LastCommittedUID: 61, LastScannedUID: 61, AttemptedAt: now,
		Records: []maildomain.IngestRecord{inboxRecord},
	}); err != nil {
		t.Fatal(err)
	}
	archiveRecord := mailRecordFixture(accountID, archiveID, 200, 9, "state-summary@example.test")
	archiveRecord.Flags = []string{`\Flagged`, `$ArchiveKeyword`}
	archiveRecord.ModSequence = 20
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000572",
		AccountID:    accountID, FolderID: archiveID, UIDValidity: 200, Bootstrap: true,
		LastCommittedUID: 9, LastScannedUID: 9, AttemptedAt: now,
		Records: []maildomain.IngestRecord{archiveRecord},
	}); err != nil {
		t.Fatal(err)
	}
	message, err := state.Mail().Message(context.Background(), accountID, inboxRecord.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if !message.Unread || !message.Flagged || len(message.Locations) != 2 {
		t.Fatalf("cross-location summary = %+v", message)
	}
	var archiveLocation maildomain.Location
	for _, location := range message.Locations {
		if location.FolderID == archiveID {
			archiveLocation = location
		}
	}
	if archiveLocation.ModSequence != 20 || !archiveLocation.Flags.Flagged ||
		len(archiveLocation.Flags.Unknown) != 1 || archiveLocation.Flags.Unknown[0] != "$ArchiveKeyword" {
		t.Fatalf("archive location = %+v", archiveLocation)
	}
	commit, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000572",
		AccountID:    accountID, FolderID: archiveID, UIDValidity: 200,
		LastCommittedUID: 9, LastScannedUID: 9, AttemptedAt: now.Add(time.Minute),
		Reconciled: []maildomain.ReconcileRecord{{
			UID: 9, Flags: maildomain.ParseLocationFlags([]string{`\Deleted`, `$ArchiveKeyword`, `$NewKeyword`}),
			ModSequence: 21, Present: true,
		}},
	})
	if err != nil || commit.Reconciled != 1 || commit.StateChanges != 1 {
		t.Fatalf("reconcile commit=%+v err=%v", commit, err)
	}
	message, err = state.Mail().Message(context.Background(), accountID, inboxRecord.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.Unread || message.Flagged {
		t.Fatalf("deleted location affected summary = %+v", message)
	}
	uids, err := state.Mail().ReconcileUIDs(context.Background(), accountID, archiveID, 200, 100)
	if err != nil || len(uids) != 1 || uids[0] != 9 {
		t.Fatalf("reconcile UIDs=%v err=%v", uids, err)
	}
	missingCommit, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000571",
		AccountID:    accountID, FolderID: inboxID, UIDValidity: 100,
		LastCommittedUID: 61, LastScannedUID: 61, AttemptedAt: now.Add(90 * time.Second),
		Reconciled: []maildomain.ReconcileRecord{{UID: 61, Present: false}},
	})
	if err != nil || missingCommit.Reconciled != 1 || missingCommit.StateChanges != 1 || missingCommit.Checkpoint.LastCommittedUID != 61 {
		t.Fatalf("missing reconcile commit=%+v err=%v", missingCommit, err)
	}
	inboxUIDs, err := state.Mail().ReconcileUIDs(context.Background(), accountID, inboxID, 100, 100)
	if err != nil || len(inboxUIDs) != 0 {
		t.Fatalf("missing Inbox reconcile UIDs=%v err=%v", inboxUIDs, err)
	}
	message, err = state.Mail().Message(context.Background(), accountID, inboxRecord.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	inboxPresent := true
	for _, location := range message.Locations {
		if location.FolderID == inboxID {
			inboxPresent = location.Present
		}
	}
	if message.Unread || message.Flagged || inboxPresent {
		t.Fatalf("missing location summary = %+v", message)
	}
	updated, err := state.Mail().UpdateLocationState(
		context.Background(), accountID, archiveLocation.ID, 21,
		maildomain.ParseLocationFlags([]string{`\Flagged`, `$Restored`}), 22, true, now.Add(2*time.Minute),
	)
	if err != nil || updated.ModSequence != 22 || !updated.Flags.Flagged || updated.Flags.Deleted {
		t.Fatalf("updated location=%+v err=%v", updated, err)
	}
	if _, err := state.Mail().UpdateLocationState(
		context.Background(), accountID, archiveLocation.ID, 21,
		maildomain.LocationFlags{}, 23, true, now.Add(3*time.Minute),
	); !errors.Is(err, maildomain.ErrLocationConflict) {
		t.Fatalf("stale location update error = %v", err)
	}
}

func TestMailReconcileUIDsRotatesBoundedSweep(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 5, 35, 0, 0, time.UTC)
	records := []maildomain.IngestRecord{
		mailRecordFixture(accountID, folderID, 100, 1, "rotation-1@example.test"),
		mailRecordFixture(accountID, folderID, 100, 2, "rotation-2@example.test"),
		mailRecordFixture(accountID, folderID, 100, 3, "rotation-3@example.test"),
	}
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000581",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100, Bootstrap: true,
		LastCommittedUID: 3, LastScannedUID: 3, AttemptedAt: now, Records: records,
	}); err != nil {
		t.Fatal(err)
	}
	first, err := state.Mail().ReconcileUIDs(context.Background(), accountID, folderID, 100, 2)
	if err != nil || !reflect.DeepEqual(first, []uint32{3, 2}) {
		t.Fatalf("first reconciliation page=%v err=%v", first, err)
	}
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000581",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100,
		LastCommittedUID: 3, LastScannedUID: 3, AttemptedAt: now.Add(time.Minute),
		Reconciled: []maildomain.ReconcileRecord{
			{UID: 3, Flags: maildomain.LocationFlags{}, Present: true},
			{UID: 2, Flags: maildomain.LocationFlags{}, Present: true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	second, err := state.Mail().ReconcileUIDs(context.Background(), accountID, folderID, 100, 2)
	if err != nil || !reflect.DeepEqual(second, []uint32{1, 3}) {
		t.Fatalf("rotated reconciliation page=%v err=%v", second, err)
	}
}

func newMailStoreFixture(t *testing.T) (*Store, string, string) {
	t.Helper()
	state, err := Open(context.Background(), testPaths(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 6, 4, 30, 0, 0, time.UTC)
	accountID := "acct_0198d704-6c00-7000-8000-000000000501"
	if _, err := state.Accounts().Create(context.Background(), account.CreateRecord{
		AccountID: accountID, CredentialID: "cred_0198d704-6c00-7000-8000-000000000502",
		Name: "Mail Test", Email: "mail-test@icloud.com", IMAPUsername: "mail-test",
		CredentialScheme: account.CredentialEnv, CredentialRef: "env:MAIL_TEST_PASSWORD", MakeDefault: true, Now: now,
	}); err != nil {
		state.Close()
		t.Fatal(err)
	}
	folders, err := state.Folders().Reconcile(context.Background(), accountID, []folder.RemoteObservation{{
		Name: "INBOX", WireName: "INBOX", Kind: folder.KindInbox, KindSource: "inbox",
		Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 100, StatusKnown: true,
	}}, now, func() (string, error) { return "fld_0198d704-6c00-7000-8000-000000000503", nil })
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	return state, accountID, folders[0].ID
}

func mailRecordFixture(accountID, folderID string, uidValidity, uid uint32, rfcMessageID string) maildomain.IngestRecord {
	internal := time.Date(2026, 8, 6, 4, int(uid%60), 0, 0, time.UTC)
	hash := strings.Repeat(string(rune('a'+uid%20)), 64)
	parsed := maildomain.ParsedMessage{
		RFCMessageID: rfcMessageID, InReplyTo: "parent@example.test", References: []string{"root@example.test"},
		From:    maildomain.Address{DisplayName: "Fixture Sender", Email: "sender@example.test"},
		ReplyTo: maildomain.Address{Email: "reply@example.test"},
		To:      []maildomain.Address{{Email: "mail-test@icloud.com"}},
		Subject: "Fixture message", Snippet: "Synthetic durable Inbox message.", HeaderDate: &internal, AttachmentCount: 1,
	}
	messageID := fmt.Sprintf("msg_0198d704-6c00-7000-8000-%012d", uid)
	locationID := fmt.Sprintf("loc_0198d704-6c00-7000-8000-%012d", uid)
	return maildomain.BuildIngestRecord(messageID, locationID, accountID, folderID, uidValidity, uid, []string{`\Flagged`}, internal, 256,
		hash, "raw/sha256/aa/"+hash+".eml", 256, parsed)
}
