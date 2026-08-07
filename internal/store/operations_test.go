package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestBackfillPageHasIndependentResumeFrontierAndCannotAdvanceForwardCheckpoint(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 7, 0, 30, 0, 0, time.UTC)
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000002000", AccountID: accountID, FolderID: folderID,
		UIDValidity: 100, Bootstrap: true, LastCommittedUID: 50, LastScannedUID: 50, AttemptedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	first, err := state.Mail().CommitBackfillPage(context.Background(), maildomain.CommitBackfillPage{
		FrontierID: "bf_0198d704-6c00-7000-8000-000000002001", AccountID: accountID, FolderID: folderID,
		UIDValidity: 100, StartBeforeUID: 40, NextBeforeUID: 10, AttemptedAt: now.Add(time.Minute),
		Records: []maildomain.IngestRecord{mailRecordFixture(accountID, folderID, 100, 20, "backfill-20@example.test"), mailRecordFixture(accountID, folderID, 100, 10, "backfill-10@example.test")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Frontier.BeforeUID != 10 || first.Frontier.Complete {
		t.Fatalf("frontier = %+v", first.Frontier)
	}
	checkpoint, err := state.Mail().Checkpoint(context.Background(), accountID, folderID)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.LastCommittedUID != 50 || checkpoint.LastScannedUID != 50 {
		t.Fatalf("backfill moved forward checkpoint: %+v", checkpoint)
	}

	if _, err := state.db.Exec(`CREATE TRIGGER synthetic_backfill_failure BEFORE UPDATE ON mail_backfill_frontiers WHEN NEW.before_uid = 5 BEGIN SELECT RAISE(ABORT, 'synthetic backfill failure'); END;`); err != nil {
		t.Fatal(err)
	}
	_, err = state.Mail().CommitBackfillPage(context.Background(), maildomain.CommitBackfillPage{
		FrontierID: first.Frontier.ID, AccountID: accountID, FolderID: folderID, UIDValidity: 100,
		StartBeforeUID: 10, NextBeforeUID: 5, AttemptedAt: now.Add(2 * time.Minute),
		Records: []maildomain.IngestRecord{mailRecordFixture(accountID, folderID, 100, 9, "backfill-9@example.test")},
	})
	if err == nil {
		t.Fatal("expected synthetic transaction failure")
	}
	frontier, err := state.Mail().BackfillFrontier(context.Background(), accountID, folderID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if frontier.BeforeUID != 10 {
		t.Fatalf("failed page advanced reverse frontier: %+v", frontier)
	}
	if _, err := state.Mail().MessageByProviderLocation(context.Background(), accountID, folderID, 100, 9); !errors.Is(err, maildomain.ErrMessageNotFound) {
		t.Fatalf("failed page persisted message: %v", err)
	}

	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	bounded, err := state.Mail().CommitBackfillPage(context.Background(), maildomain.CommitBackfillPage{
		FrontierID: "bf_0198d704-6c00-7000-8000-000000002002", AccountID: accountID, FolderID: folderID,
		UIDValidity: 100, StartBeforeUID: 40, NextBeforeUID: 1, After: &after, Complete: true, AttemptedAt: now.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if bounded.Frontier.ID == first.Frontier.ID || bounded.Frontier.After == nil {
		t.Fatalf("date-bounded run reused wrong frontier: %+v", bounded.Frontier)
	}
}

func TestRebaselinePreservesCachedMessagesAndHistoricalFrontier(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 7, 0, 45, 0, 0, time.UTC)
	record := mailRecordFixture(accountID, folderID, 100, 21, "keep-on-rebaseline@example.test")
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000002010", AccountID: accountID, FolderID: folderID,
		UIDValidity: 100, Bootstrap: true, LastCommittedUID: 21, LastScannedUID: 21, AttemptedAt: now, Records: []maildomain.IngestRecord{record},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Mail().CommitBackfillPage(context.Background(), maildomain.CommitBackfillPage{
		FrontierID: "bf_0198d704-6c00-7000-8000-000000002011", AccountID: accountID, FolderID: folderID,
		UIDValidity: 100, StartBeforeUID: 21, NextBeforeUID: 10, AttemptedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	after, err := state.Mail().RebaselineCheckpoint(context.Background(), "chk_unused", accountID, folderID, 200, 99, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if after.UIDValidity != 200 || after.LastCommittedUID != 99 || after.LastScannedUID != 99 || after.State != maildomain.CheckpointIdle {
		t.Fatalf("rebaselined checkpoint = %+v", after)
	}
	message, err := state.Mail().MessageByProviderLocation(context.Background(), accountID, folderID, 100, 21)
	if err != nil || message.ID != record.MessageID {
		t.Fatalf("cached old-namespace message missing: %+v err=%v", message, err)
	}
	frontier, err := state.Mail().BackfillFrontier(context.Background(), accountID, folderID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if frontier.UIDValidity != 100 || frontier.BeforeUID != 10 {
		t.Fatalf("rebaseline changed historical frontier: %+v", frontier)
	}
	audit, err := state.AuditMailRepair(context.Background(), accountID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Summary.FrontierProblems != 1 || len(audit.BackfillResets) != 1 {
		t.Fatalf("expected stale historical frontier finding: %+v", audit)
	}
}

func TestRepairAuditDetectsAndDeterministicallyRefreshesStaleSearchMetadataWithoutGuessingDedupe(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC)
	first := mailRecordFixture(accountID, folderID, 100, 21, "repair-21@example.test")
	second := mailRecordFixture(accountID, folderID, 100, 22, "repair-22@example.test")
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000002020", AccountID: accountID, FolderID: folderID,
		UIDValidity: 100, Bootstrap: true, LastCommittedUID: 22, LastScannedUID: 22, AttemptedAt: now, Records: []maildomain.IngestRecord{first, second},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.Exec(`UPDATE message_search_documents SET subject='stale synthetic subject' WHERE message_id=?`, first.MessageID); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.Exec(`INSERT INTO message_dedupe_conflicts (conflict_id,account_id,evidence_kind,evidence_value,existing_message_id,observed_message_id,reason,status,created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		"dcf_0198d704-6c00-7000-8000-000000002021", accountID, "content_fingerprint", "synthetic-conflict", first.MessageID, second.MessageID, "synthetic ambiguity", "open", now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	audit, err := state.AuditMailRepair(context.Background(), accountID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Summary.StaleFTSRows != 1 || audit.Summary.DedupeConflicts != 1 || len(audit.StaleSearchMessageIDs) != 1 {
		t.Fatalf("audit = %+v", audit)
	}
	staleSearch, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{Query: "stale synthetic", ResultKind: maildomain.SearchMessages, Limit: 20})
	if err != nil || len(staleSearch.Hits) != 1 || staleSearch.Hits[0].Message.ID != first.MessageID {
		t.Fatalf("stale FTS evidence = %+v err=%v", staleSearch, err)
	}
	changed, err := state.Search().RefreshMetadata(context.Background(), accountID, audit.StaleSearchMessageIDs, now.Add(time.Minute))
	if err != nil || changed != 1 {
		t.Fatalf("refresh changed=%d err=%v", changed, err)
	}
	staleSearch, err = state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{Query: "stale synthetic", ResultKind: maildomain.SearchMessages, Limit: 20})
	if err != nil || len(staleSearch.Hits) != 0 {
		t.Fatalf("stale FTS term survived repair = %+v err=%v", staleSearch, err)
	}
	canonicalSearch, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{Query: "fixture message", ResultKind: maildomain.SearchMessages, Limit: 20})
	if err != nil || len(canonicalSearch.Hits) != 2 {
		t.Fatalf("canonical FTS terms missing after repair = %+v err=%v", canonicalSearch, err)
	}
	after, err := state.AuditMailRepair(context.Background(), accountID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if after.Summary.StaleFTSRows != 0 || after.Summary.DedupeConflicts != 1 {
		t.Fatalf("post-repair audit guessed at conflict or left stale metadata: %+v", after)
	}
}

func TestRepairAuditFiltersProblemRowsBeforeBoundsAndBlobCategoriesDoNotStarveEachOther(t *testing.T) {
	state, accountID, inboxID := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	now := time.Date(2026, 8, 7, 1, 30, 0, 0, time.UTC)

	folderIDs := []string{inboxID}
	for index := 0; index < 5; index++ {
		id := fmt.Sprintf("fld_0198d704-6c00-7000-8000-%012d", 2100+index)
		name := fmt.Sprintf("Repair Scale %d", index)
		value, err := state.Folders().CreateObserved(ctx, accountID, folder.RemoteObservation{
			Name: name, WireName: name, Kind: folder.KindOther, KindSource: "none", Selectable: true,
			Subscription: folder.SubscriptionSubscribed, UIDValidity: uint32(200 + index), UIDNext: 10, StatusKnown: true,
		}, now.Add(time.Duration(index)*time.Second), func() (string, error) { return id, nil })
		if err != nil {
			t.Fatal(err)
		}
		folderIDs = append(folderIDs, value.ID)
	}

	for index, folderID := range folderIDs {
		checkpointID := fmt.Sprintf("chk_0198d704-6c00-7000-8000-%012d", 2200+index)
		if _, err := state.Mail().RebaselineCheckpoint(ctx, checkpointID, accountID, folderID, uint32(300+index), 9, now.Add(time.Duration(index)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if index >= 3 {
			if _, err := state.Mail().RequireRepair(ctx, checkpointID, accountID, folderID, uint32(900+index), now.Add(time.Duration(index+10)*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
	}

	afterHealthyA := now.Add(-72 * time.Hour)
	afterHealthyB := now.Add(-48 * time.Hour)
	afterProblemA := now.Add(-24 * time.Hour)
	afterProblemB := now.Add(-12 * time.Hour)
	for index, after := range []*time.Time{&afterHealthyA, &afterHealthyB, &afterProblemA, &afterProblemB} {
		frontierID := fmt.Sprintf("bf_0198d704-6c00-7000-8000-%012d", 2300+index)
		if _, err := state.Mail().CommitBackfillPage(ctx, maildomain.CommitBackfillPage{
			FrontierID: frontierID, AccountID: accountID, FolderID: inboxID, UIDValidity: 300,
			StartBeforeUID: 10, NextBeforeUID: 5, After: after, AttemptedAt: now.Add(time.Duration(index) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
		if index >= 2 {
			if err := state.Mail().RecordBackfillFailure(ctx, frontierID, accountID, inboxID, after, nil, 300, 5, "uid_validity_changed", now.Add(time.Duration(index+20)*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
	}

	first := mailRecordFixture(accountID, inboxID, 300, 31, "scale-blob-31@example.test")
	second := mailRecordFixture(accountID, inboxID, 300, 32, "scale-blob-32@example.test")
	if _, err := state.Mail().CommitInboxPage(ctx, maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-8000-000000002500", AccountID: accountID, FolderID: inboxID,
		UIDValidity: 300, Bootstrap: true, LastCommittedUID: 32, LastScannedUID: 32, AttemptedAt: now.Add(time.Hour), Records: []maildomain.IngestRecord{first, second},
	}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		conflictID := fmt.Sprintf("dcf_0198d704-6c00-7000-8000-%012d", 2400+index)
		if _, err := state.db.ExecContext(ctx, `INSERT INTO message_dedupe_conflicts
(conflict_id,account_id,evidence_kind,evidence_value,existing_message_id,observed_message_id,reason,status,created_at)
VALUES (?,?, 'content_fingerprint', ?, ?, ?, 'scale ambiguity', 'open', ?)`,
			conflictID, accountID, fmt.Sprintf("scale-%d", index), first.MessageID, second.MessageID, now.Add(time.Duration(index)*time.Second).Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	rawHash := strings.Repeat("e", 64)
	if _, _, err := state.Outbound().Begin(ctx, maildomain.BeginOutboundAttempt{
		AttemptID: "send_0198d704-6c00-7000-8000-000000002501", AccountID: accountID,
		ProposedMessageID: "msg_0198d704-6c00-7000-8000-000000002502", RequestHash: strings.Repeat("f", 64),
		RFCMessageID: "scale-outbound@example.test", RawContentHash: rawHash,
		RawStorageKey: "raw/sha256/ee/" + rawHash + ".eml", RawByteSize: 123, Now: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	audit, err := state.AuditMailRepair(ctx, accountID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Summary.RepairCheckpoints != 2 || audit.Summary.FrontierProblems != 2 || audit.Summary.DedupeConflicts != 2 || !audit.Summary.More {
		t.Fatalf("bounded problem audit = %+v", audit.Summary)
	}
	for _, checkpoint := range audit.RepairCheckpoints {
		if checkpoint.State != maildomain.CheckpointRepairRequired {
			t.Fatalf("healthy checkpoint leaked into repair candidates: %+v", checkpoint)
		}
	}
	for _, frontier := range audit.BackfillResets {
		if frontier.LastErrorCode != "uid_validity_changed" {
			t.Fatalf("healthy frontier leaked into repair candidates: %+v", frontier)
		}
	}

	refs, more, err := state.MailBlobReferences(ctx, accountID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !more {
		t.Fatal("expected message_raw category to report more")
	}
	kinds := map[string]bool{}
	for _, ref := range refs {
		kinds[ref.Kind] = true
	}
	if !kinds["message_raw"] || !kinds["outbound_raw"] {
		t.Fatalf("blob category starvation: refs=%+v", refs)
	}
}
