package store

import (
	"context"
	"errors"
	"fmt"
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
