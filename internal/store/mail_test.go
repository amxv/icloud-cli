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

func TestMailHydrationUsesCanonicalIngestionWithoutChangingCheckpoint(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 4, 52, 0, 0, time.UTC)
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000504",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100, Bootstrap: true,
		LastCommittedUID: 20, LastScannedUID: 20, AttemptedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	record := mailRecordFixture(accountID, folderID, 100, 5, "hydrated@example.test")
	result, err := state.Mail().CommitHydration(context.Background(), maildomain.HydratePage{
		AccountID: accountID, FolderID: folderID, UIDValidity: 100,
		AttemptedAt: now.Add(time.Minute), Records: []maildomain.IngestRecord{record},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 1 || result.Updated != 0 {
		t.Fatalf("hydration result = %+v", result)
	}
	message, err := state.Mail().MessageByProviderLocation(context.Background(), accountID, folderID, 100, 5)
	if err != nil {
		t.Fatal(err)
	}
	if message.ID != record.MessageID || len(message.Locations) != 1 || message.Locations[0].ID != record.LocationID {
		t.Fatalf("hydrated message = %+v", message)
	}
	checkpoint, err := state.Mail().Checkpoint(context.Background(), accountID, folderID)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.LastCommittedUID != 20 || checkpoint.LastScannedUID != 20 || !checkpoint.BootstrapComplete || checkpoint.State != maildomain.CheckpointIdle {
		t.Fatalf("hydration changed checkpoint: %+v", checkpoint)
	}

	retry := mailRecordFixture(accountID, folderID, 100, 5, "hydrated@example.test")
	retry.MessageID = "msg_0198d704-6c00-7000-8000-000000009998"
	retry.LocationID = "loc_0198d704-6c00-7000-8000-000000009998"
	retry.Flags = []string{`\Seen`}
	result, err = state.Mail().CommitHydration(context.Background(), maildomain.HydratePage{
		AccountID: accountID, FolderID: folderID, UIDValidity: 100,
		AttemptedAt: now.Add(2 * time.Minute), Records: []maildomain.IngestRecord{retry},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 0 || result.Updated != 1 {
		t.Fatalf("retry hydration result = %+v", result)
	}
	message, err = state.Mail().MessageByProviderLocation(context.Background(), accountID, folderID, 100, 5)
	if err != nil {
		t.Fatal(err)
	}
	if message.ID != record.MessageID || message.Locations[0].ID != record.LocationID || message.Unread {
		t.Fatalf("retry changed identity or flags: %+v", message)
	}
	checkpointAfter, err := state.Mail().Checkpoint(context.Background(), accountID, folderID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(checkpoint, checkpointAfter) {
		t.Fatalf("retry hydration changed checkpoint: before=%+v after=%+v", checkpoint, checkpointAfter)
	}
}

func TestMailHydrationFailureRollsBackWithoutChangingCheckpoint(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 4, 53, 0, 0, time.UTC)
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000504",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100, Bootstrap: true,
		LastCommittedUID: 20, LastScannedUID: 20, AttemptedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	before, err := state.Mail().Checkpoint(context.Background(), accountID, folderID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.Exec(`
CREATE TRIGGER synthetic_hydration_failure
BEFORE INSERT ON message_search_documents
WHEN NEW.subject = 'Hydration rollback'
BEGIN
    SELECT RAISE(ABORT, 'synthetic hydration failure');
END;`); err != nil {
		t.Fatal(err)
	}
	record := mailRecordFixture(accountID, folderID, 100, 6, "hydration-failure@example.test")
	record.Parsed.Subject = "Hydration rollback"
	_, err = state.Mail().CommitHydration(context.Background(), maildomain.HydratePage{
		AccountID: accountID, FolderID: folderID, UIDValidity: 100,
		AttemptedAt: now.Add(time.Minute), Records: []maildomain.IngestRecord{record},
	})
	if err == nil || !strings.Contains(err.Error(), "synthetic hydration failure") {
		t.Fatalf("hydration failure = %v", err)
	}
	if _, err := state.Mail().MessageByProviderLocation(context.Background(), accountID, folderID, 100, 6); !errors.Is(err, maildomain.ErrMessageNotFound) {
		t.Fatalf("rolled-back hydration message error = %v", err)
	}
	after, err := state.Mail().Checkpoint(context.Background(), accountID, folderID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("failed hydration changed checkpoint: before=%+v after=%+v", before, after)
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
		SMIME:     &maildomain.SMIMEStatus{Status: maildomain.SMIMEStatusValidUntrusted, Container: maildomain.SMIMEContainerMultipartSigned, Signed: true, SignatureValid: true, Trust: maildomain.SMIMETrustUntrusted, Signer: &maildomain.SMIMESigner{FingerprintSHA256: strings.Repeat("f", 64), Emails: []string{"sender@example.test"}}},
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
	if message.Content == nil || len(message.Content.Headers) != 2 || len(message.Content.Parts) != 3 || message.Content.SMIME == nil || message.Content.SMIME.Status != maildomain.SMIMEStatusValidUntrusted || message.Content.SMIME.Signer == nil || message.Content.SMIME.Signer.FingerprintSHA256 != strings.Repeat("f", 64) ||
		strings.Join(message.Content.TextPaths, ",") != "1" || strings.Join(message.Content.HTMLPaths, ",") != "2" ||
		message.ContentLocationID != record.LocationID {
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
	if message.ContentLocationID != record.LocationID {
		t.Fatalf("content source location changed: %s", message.ContentLocationID)
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
	threadID, err := state.Threads().Resolve(context.Background(), accountID, record.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	threadValues, threadMore, err := state.Mail().AttachmentsForThread(context.Background(), accountID, threadID, 20)
	if err != nil || threadMore || len(threadValues) != 2 {
		t.Fatalf("thread attachments=%+v more=%v err=%v", threadValues, threadMore, err)
	}
	if threadValues[0].ID == threadValues[1].ID || threadValues[0].ContentHash != threadValues[1].ContentHash {
		t.Fatalf("thread duplicate-content attachment identities collapsed: %+v", threadValues)
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
	archiveRecord.RawContentHash = inboxRecord.RawContentHash
	archiveRecord.RawStorageKey = inboxRecord.RawStorageKey
	archiveRecord.RawByteSize = inboxRecord.RawByteSize
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

func TestMailSentIdentityMergeKeepsIndependentLocationsAndUnreadState(t *testing.T) {
	state, accountID, inboxID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 7, 0, 0, 0, time.UTC)
	sentID := insertMailFolderFixture(t, state, accountID, folder.KindSent, "Sent Messages", 200, now)

	inboxRecord := mailRecordFixture(accountID, inboxID, 100, 71, "shared-location@example.test")
	inboxRecord.Direction = maildomain.DirectionInbound
	inboxRecord.Flags = []string{`\Seen`}
	inboxRecord.ContentFingerprint = "content:" + strings.Repeat("1", 64)
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000901",
		AccountID:    accountID, FolderID: inboxID, UIDValidity: 100, Bootstrap: true,
		LastCommittedUID: 71, LastScannedUID: 71, AttemptedAt: now,
		Records: []maildomain.IngestRecord{inboxRecord},
	}); err != nil {
		t.Fatal(err)
	}

	sentRecord := mailRecordFixture(accountID, sentID, 200, 9, "shared-location@example.test")
	sentRecord.Direction = maildomain.DirectionOutbound
	sentRecord.Flags = []string{`\Flagged`}
	sentRecord.ContentFingerprint = inboxRecord.ContentFingerprint
	commit, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000902",
		AccountID:    accountID, FolderID: sentID, UIDValidity: 200, Bootstrap: false,
		LastCommittedUID: 9, LastScannedUID: 9, AttemptedAt: now.Add(time.Minute),
		Records: []maildomain.IngestRecord{sentRecord},
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit.Created != 0 || commit.Updated != 1 || commit.IdentityMerges != 1 || commit.DedupeConflicts != 0 {
		t.Fatalf("sent merge commit = %+v", commit)
	}

	message, err := state.Mail().Message(context.Background(), accountID, inboxRecord.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.Direction != maildomain.DirectionOutbound || message.Unread || !message.Flagged || len(message.Locations) != 2 {
		t.Fatalf("merged message = %+v", message)
	}
	locations := map[string]maildomain.Location{}
	for _, location := range message.Locations {
		locations[location.FolderKind] = location
	}
	if locations[string(folder.KindInbox)].UID != 71 || !locations[string(folder.KindInbox)].Flags.Seen ||
		locations[string(folder.KindSent)].UID != 9 || !locations[string(folder.KindSent)].Flags.Flagged || locations[string(folder.KindSent)].Flags.Seen {
		t.Fatalf("independent locations = %+v", locations)
	}
	sentMessages, more, err := state.Mail().MessagesInFolder(context.Background(), accountID, sentID, 20)
	if err != nil || more || len(sentMessages) != 1 || sentMessages[0].ID != inboxRecord.MessageID {
		t.Fatalf("sent messages=%+v more=%v err=%v", sentMessages, more, err)
	}
	providerEvidence := 0
	for _, evidence := range message.Identity {
		if evidence.Kind == maildomain.IdentityProviderLocation {
			providerEvidence++
		}
	}
	if providerEvidence != 2 || len(message.DedupeConflicts) != 0 {
		t.Fatalf("identity evidence=%+v conflicts=%+v", message.Identity, message.DedupeConflicts)
	}
}

func TestMailContentFingerprintDoesNotCollapseDistinctProviderLocations(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 7, 10, 0, 0, time.UTC)
	fingerprint := "content:" + strings.Repeat("2", 64)

	first := mailRecordFixture(accountID, folderID, 100, 81, "distinct-one@example.test")
	first.ContentFingerprint = fingerprint
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000911",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100, Bootstrap: true,
		LastCommittedUID: 81, LastScannedUID: 81, AttemptedAt: now,
		Records: []maildomain.IngestRecord{first},
	}); err != nil {
		t.Fatal(err)
	}
	second := mailRecordFixture(accountID, folderID, 100, 82, "distinct-two@example.test")
	second.ContentFingerprint = fingerprint
	commit, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000911",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100,
		LastCommittedUID: 82, LastScannedUID: 82, AttemptedAt: now.Add(time.Minute),
		Records: []maildomain.IngestRecord{second},
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit.Created != 1 || commit.IdentityMerges != 0 || commit.DedupeConflicts != 1 {
		t.Fatalf("collision commit = %+v", commit)
	}
	messages, more, err := state.Mail().MessagesInFolder(context.Background(), accountID, folderID, 20)
	if err != nil || more || len(messages) != 2 || messages[0].ID == messages[1].ID {
		t.Fatalf("messages=%+v more=%v err=%v", messages, more, err)
	}
	for _, message := range messages {
		if len(message.DedupeConflicts) != 1 || message.DedupeConflicts[0].Reason != "content_fingerprint_provider_collision" {
			t.Fatalf("message conflict telemetry = %+v", message.DedupeConflicts)
		}
	}
}

func TestMailReusedRFCMessageIDDoesNotCollapseContradictoryProviderMessages(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 8, 2, 30, 0, 0, time.UTC)
	sharedRFC := "reused-rfc-message-id@example.test"

	first := mailRecordFixture(accountID, folderID, 100, 83, sharedRFC)
	first.Parsed.Subject = "First logical message"
	first.Parsed.Snippet = "first provider body"
	first.ContentFingerprint = "content:" + strings.Repeat("6", 64)
	first.RawContentHash = strings.Repeat("6", 64)
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000913",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100, Bootstrap: true,
		LastCommittedUID: 83, LastScannedUID: 83, AttemptedAt: now,
		Records: []maildomain.IngestRecord{first},
	}); err != nil {
		t.Fatal(err)
	}

	second := mailRecordFixture(accountID, folderID, 100, 84, sharedRFC)
	second.Parsed.Subject = "Second logical message"
	second.Parsed.Snippet = "different provider body"
	second.ContentFingerprint = "content:" + strings.Repeat("7", 64)
	second.RawContentHash = strings.Repeat("7", 64)
	commit, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000913",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100,
		LastCommittedUID: 84, LastScannedUID: 84, AttemptedAt: now.Add(time.Minute),
		Records: []maildomain.IngestRecord{second},
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit.Created != 1 || commit.IdentityMerges != 0 || commit.DedupeConflicts != 1 {
		t.Fatalf("reused RFC Message-ID commit = %+v", commit)
	}
	if first.MessageID == second.MessageID {
		t.Fatalf("fixture unexpectedly reused immutable message ID %q", first.MessageID)
	}
	firstMessage, err := state.Mail().Message(context.Background(), accountID, first.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	secondMessage, err := state.Mail().Message(context.Background(), accountID, second.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if firstMessage.Subject != first.Parsed.Subject || secondMessage.Subject != second.Parsed.Subject ||
		len(firstMessage.Locations) != 1 || firstMessage.Locations[0].UID != 83 ||
		len(secondMessage.Locations) != 1 || secondMessage.Locations[0].UID != 84 {
		t.Fatalf("distinct provider messages collapsed: first=%+v second=%+v", firstMessage, secondMessage)
	}
	if len(firstMessage.DedupeConflicts) != 1 || len(secondMessage.DedupeConflicts) != 1 {
		t.Fatalf("reused RFC conflict telemetry: first=%+v second=%+v", firstMessage.DedupeConflicts, secondMessage.DedupeConflicts)
	}
	conflict := secondMessage.DedupeConflicts[0]
	if conflict.EvidenceKind != maildomain.IdentityRFCMessageID || conflict.EvidenceValue != sharedRFC ||
		conflict.Reason != "rfc_message_id_uncorroborated_provider_collision" ||
		conflict.ExistingMessageID != first.MessageID || conflict.ObservedMessageID != second.MessageID {
		t.Fatalf("reused RFC conflict = %+v", conflict)
	}
}

func TestMailKnownGeneratedMessageIDMergesRemoteSentObservation(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 7, 20, 0, 0, time.UTC)
	sentID := insertMailFolderFixture(t, state, accountID, folder.KindSent, "Sent Messages", 300, now)
	localMessageID := "msg_0198d704-6c00-7000-8000-000000000921"
	timestamp := now.Format(time.RFC3339Nano)
	if _, err := state.db.Exec(`
INSERT INTO messages (
    message_id, account_id, rfc_message_id, direction, from_email,
    subject, snippet, attachment_count, date_source, created_at, updated_at
) VALUES (?, ?, '', 'outbound', 'mail-test@icloud.com',
          'Generated outbound', 'Queued locally', 0, 'unknown', ?, ?)`,
		localMessageID, accountID, timestamp, timestamp,
	); err != nil {
		t.Fatal(err)
	}
	generatedID := "generated-phase10@example.test"
	if err := state.Mail().RegisterGeneratedMessageID(context.Background(), accountID, localMessageID, generatedID, now); err != nil {
		t.Fatal(err)
	}
	remote := mailRecordFixture(accountID, sentID, 300, 11, generatedID)
	remote.Direction = maildomain.DirectionOutbound
	remote.Parsed.From.Email = "mail-test@icloud.com"
	remote.ContentFingerprint = "content:" + strings.Repeat("3", 64)
	commit, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000922",
		AccountID:    accountID, FolderID: sentID, UIDValidity: 300, Bootstrap: false,
		LastCommittedUID: 11, LastScannedUID: 11, AttemptedAt: now.Add(time.Minute),
		Records: []maildomain.IngestRecord{remote},
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit.Created != 0 || commit.Updated != 1 || commit.IdentityMerges != 1 || commit.DedupeConflicts != 0 {
		t.Fatalf("generated-ID merge commit = %+v", commit)
	}
	message, err := state.Mail().Message(context.Background(), accountID, localMessageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.ID != localMessageID || message.RFCMessageID != generatedID || message.Direction != maildomain.DirectionOutbound || len(message.Locations) != 1 {
		t.Fatalf("generated-ID merged message = %+v", message)
	}
	generatedEvidence := false
	for _, evidence := range message.Identity {
		if evidence.Kind == maildomain.IdentityGeneratedMessageID && evidence.Value == generatedID {
			generatedEvidence = true
		}
	}
	if !generatedEvidence {
		t.Fatalf("generated identity evidence = %+v", message.Identity)
	}
}

func TestMailGuardedFingerprintMergesOnlyOneLocationlessCandidate(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 7, 25, 0, 0, time.UTC)
	localMessageID := "msg_0198d704-6c00-7000-8000-000000000931"
	fingerprint := "content:" + strings.Repeat("4", 64)
	timestamp := now.Format(time.RFC3339Nano)
	if _, err := state.db.Exec(`
INSERT INTO messages (
    message_id, account_id, rfc_message_id, direction, from_email,
    subject, snippet, attachment_count, date_source, created_at, updated_at
) VALUES (?, ?, '', 'outbound', 'mail-test@icloud.com',
          'Locationless candidate', 'Queued locally', 0, 'unknown', ?, ?)`,
		localMessageID, accountID, timestamp, timestamp,
	); err != nil {
		t.Fatal(err)
	}
	seedIdentityEvidence(t, state, accountID, localMessageID, maildomain.IdentityContentFingerprint, fingerprint, maildomain.IdentityGuarded, "", now)

	remote := mailRecordFixture(accountID, folderID, 100, 91, "")
	remote.Parsed.RFCMessageID = ""
	remote.Direction = maildomain.DirectionOutbound
	remote.ContentFingerprint = fingerprint
	commit, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000932",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100, Bootstrap: true,
		LastCommittedUID: 91, LastScannedUID: 91, AttemptedAt: now.Add(time.Minute),
		Records: []maildomain.IngestRecord{remote},
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit.Created != 0 || commit.Updated != 1 || commit.IdentityMerges != 1 || commit.DedupeConflicts != 0 {
		t.Fatalf("guarded fingerprint merge commit = %+v", commit)
	}
	message, err := state.Mail().Message(context.Background(), accountID, localMessageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.ID != localMessageID || len(message.Locations) != 1 || message.Locations[0].UID != 91 {
		t.Fatalf("guarded fingerprint merged message = %+v", message)
	}
}

func TestMailProviderLocationConflictPreservesBothStrongIdentities(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 7, 27, 0, 0, time.UTC)
	first := mailRecordFixture(accountID, folderID, 100, 95, "provider-first@example.test")
	second := mailRecordFixture(accountID, folderID, 100, 96, "provider-second@example.test")
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000946",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100, Bootstrap: true,
		LastCommittedUID: 96, LastScannedUID: 96, AttemptedAt: now,
		Records: []maildomain.IngestRecord{first, second},
	}); err != nil {
		t.Fatal(err)
	}

	conflictingRefetch := mailRecordFixture(accountID, folderID, 100, 95, "provider-second@example.test")
	conflictingRefetch.Parsed.Subject = first.Parsed.Subject
	conflictingRefetch.ContentFingerprint = "content:" + strings.Repeat("5", 64)
	commit, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000000946",
		AccountID:    accountID, FolderID: folderID, UIDValidity: 100,
		LastCommittedUID: 96, LastScannedUID: 96, AttemptedAt: now.Add(time.Minute),
		Records: []maildomain.IngestRecord{conflictingRefetch},
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit.Created != 0 || commit.Updated != 1 || commit.IdentityMerges != 0 || commit.DedupeConflicts != 1 {
		t.Fatalf("provider conflict commit = %+v", commit)
	}
	firstMessage, err := state.Mail().Message(context.Background(), accountID, first.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	secondMessage, err := state.Mail().Message(context.Background(), accountID, second.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if firstMessage.RFCMessageID != "provider-first@example.test" || secondMessage.RFCMessageID != "provider-second@example.test" ||
		len(firstMessage.Locations) != 1 || firstMessage.Locations[0].UID != 95 ||
		len(secondMessage.Locations) != 1 || secondMessage.Locations[0].UID != 96 {
		t.Fatalf("strong identities changed: first=%+v second=%+v", firstMessage, secondMessage)
	}
	if len(firstMessage.DedupeConflicts) != 1 || len(secondMessage.DedupeConflicts) != 1 {
		t.Fatalf("provider conflict telemetry: first=%+v second=%+v", firstMessage.DedupeConflicts, secondMessage.DedupeConflicts)
	}
	conflict := firstMessage.DedupeConflicts[0]
	if conflict.Reason != "strong_identity_disagrees_with_provider_location" ||
		conflict.EvidenceKind != maildomain.IdentityRFCMessageID ||
		conflict.ExistingMessageID != second.MessageID || conflict.ObservedMessageID != first.MessageID {
		t.Fatalf("provider conflict = %+v", conflict)
	}
	for _, evidence := range firstMessage.Identity {
		if evidence.Kind == maildomain.IdentityContentFingerprint && evidence.Value == conflictingRefetch.ContentFingerprint {
			t.Fatalf("conflicting fingerprint was attached to first message: %+v", firstMessage.Identity)
		}
	}
}

func seedIdentityEvidence(t *testing.T, state *Store, accountID, messageID string, kind maildomain.IdentityKind, value string, strength maildomain.IdentityStrength, locationID string, now time.Time) {
	t.Helper()
	tx, err := state.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := persistIdentityEvidence(context.Background(), tx, accountID, messageID, kind, value, strength, locationID, now.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func insertMailFolderFixture(t *testing.T, state *Store, accountID string, kind folder.Kind, name string, uidValidity uint32, now time.Time) string {
	t.Helper()
	folderID := fmt.Sprintf("fld_0198d704-6c00-7000-8000-%012d", uidValidity)
	timestamp := now.UTC().Format(time.RFC3339Nano)
	if _, err := state.db.Exec(`
INSERT INTO folders (
    folder_id, account_id, name, wire_name, kind, kind_source, selectable,
    subscription, present, stale, sync_enabled, uid_validity,
    discovered_at, status_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 'special_use', 1,
          'subscribed', 1, 0, 1, ?, ?, ?, ?, ?)`,
		folderID, accountID, name, name, kind, uidValidity,
		timestamp, timestamp, timestamp, timestamp,
	); err != nil {
		t.Fatal(err)
	}
	return folderID
}

func newMailStoreFixture(t *testing.T) (*Store, string, string) {
	t.Helper()
	return newMailStoreFixtureAt(t, t.TempDir())
}

func newMailStoreFixtureAt(t *testing.T, root string) (*Store, string, string) {
	t.Helper()
	state, err := Open(context.Background(), testPaths(t, root))
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

func TestReconcileLocationReportsOnlyActualStateChangesForWatchIdentity(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	now := time.Date(2026, 8, 11, 5, 45, 0, 0, time.UTC)
	record := mailRecordFixture(accountID, folderID, 100, 611, "phase26-reconcile@example.test")
	if _, err := state.Mail().CommitInboxPage(ctx, maildomain.CommitPage{
		CheckpointID: "chk_phase26_reconcile", AccountID: accountID, FolderID: folderID, UIDValidity: 100,
		LastCommittedUID: 611, LastScannedUID: 611, Bootstrap: true, AttemptedAt: now,
		Records: []maildomain.IngestRecord{record},
	}); err != nil {
		t.Fatal(err)
	}

	unchanged, err := state.Mail().CommitInboxPage(ctx, maildomain.CommitPage{
		CheckpointID: "chk_phase26_reconcile", AccountID: accountID, FolderID: folderID, UIDValidity: 100,
		LastCommittedUID: 611, LastScannedUID: 611, AttemptedAt: now.Add(time.Minute),
		Reconciled: []maildomain.ReconcileRecord{{UID: 611, Flags: maildomain.ParseLocationFlags([]string{`\Flagged`}), Present: true}},
	})
	if err != nil || unchanged.Reconciled != 1 || unchanged.StateChanges != 0 || len(unchanged.ChangedMessageIDs) != 0 {
		t.Fatalf("unchanged reconciliation=%+v err=%v", unchanged, err)
	}

	changed, err := state.Mail().CommitInboxPage(ctx, maildomain.CommitPage{
		CheckpointID: "chk_phase26_reconcile", AccountID: accountID, FolderID: folderID, UIDValidity: 100,
		LastCommittedUID: 611, LastScannedUID: 611, AttemptedAt: now.Add(2 * time.Minute),
		Reconciled: []maildomain.ReconcileRecord{{UID: 611, Flags: maildomain.ParseLocationFlags([]string{`\Flagged`, `\Seen`}), ModSequence: 9, Present: true}},
	})
	if err != nil || changed.StateChanges != 1 || len(changed.ChangedMessageIDs) != 1 || changed.ChangedMessageIDs[0] != record.MessageID {
		t.Fatalf("changed reconciliation=%+v err=%v", changed, err)
	}
}
