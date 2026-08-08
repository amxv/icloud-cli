package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestMailSearchFTSStructuredFiltersAndAccountIsolation(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	base := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)

	invoice := searchRecordFixture(accountID, folderID, 100, 201, "invoice-201@example.test", base,
		"Quarterly résumé", "Alice Billing", "alice@example.test", "The nebula orchestration total is ready.",
		"invoice-q3.pdf", "application/pdf")
	invoice.Parsed.To = []maildomain.Address{{DisplayName: "Owner", Email: "mail-test@icloud.com"}}
	invoice.Parsed.CC = []maildomain.Address{{DisplayName: "Bob Reviewer", Email: "bob@example.test"}}
	invoice.Parsed.BCC = []maildomain.Address{{DisplayName: "Private", Email: "bcc-secret@example.test"}}
	invoice.Flags = []string{`\Flagged`}
	launch := searchRecordFixture(accountID, folderID, 100, 202, "launch-202@example.test", base.Add(time.Minute),
		"Launch update", "Charlie", "charlie@example.test", "Nebula notes without the invoice token.", "", "")
	launch.Flags = []string{`\Seen`}
	other := searchRecordFixture(accountID, folderID, 100, 203, "other-203@example.test", base.Add(2*time.Minute),
		"Ordinary note", "Dana", "dana@example.test", "Nothing relevant here.", "notes.txt", "text/plain")
	other.Flags = nil
	commitSearchRecords(t, state, accountID, folderID, "chk_search_main", base.Add(3*time.Minute), invoice, launch, other)

	secondAccountID, secondFolderID := addSearchAccountFixture(t, state, base)
	foreign := searchRecordFixture(secondAccountID, secondFolderID, 200, 301, "foreign-301@example.test", base.Add(4*time.Minute),
		"Foreign résumé", "Mallory", "mallory@example.test", "nebula orchestration foreign account", "invoice-foreign.pdf", "application/pdf")
	commitSearchRecords(t, state, secondAccountID, secondFolderID, "chk_search_foreign", base.Add(5*time.Minute), foreign)

	for name, testCase := range map[string]struct {
		options   maildomain.SearchOptions
		wantID    string
		wantField string
	}{
		"decoded body": {
			options: maildomain.SearchOptions{Query: "orchestration", ResultKind: maildomain.SearchMessages, Limit: 20},
			wantID:  invoice.MessageID, wantField: "body",
		},
		"unicode subject": {
			options: maildomain.SearchOptions{Query: "resume", ResultKind: maildomain.SearchMessages, Limit: 20},
			wantID:  invoice.MessageID, wantField: "subject",
		},
		"participant": {
			options: maildomain.SearchOptions{Query: "bob@example.test", ResultKind: maildomain.SearchMessages, Limit: 20},
			wantID:  invoice.MessageID, wantField: "participant",
		},
		"attachment": {
			options: maildomain.SearchOptions{Query: "invoice-q3", ResultKind: maildomain.SearchMessages, Limit: 20},
			wantID:  invoice.MessageID, wantField: "attachment_filename",
		},
	} {
		t.Run(name, func(t *testing.T) {
			page, err := state.Search().Search(context.Background(), accountID, testCase.options)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Hits) != 1 || page.Hits[0].Message == nil || page.Hits[0].Message.ID != testCase.wantID || page.Hits[0].MatchedField != testCase.wantField {
				t.Fatalf("hits = %+v", page.Hits)
			}
			if strings.Contains(page.Hits[0].Excerpt, "foreign account") {
				t.Fatalf("cross-account excerpt = %q", page.Hits[0].Excerpt)
			}
		})
	}

	unread, flagged := true, true
	after, before := base.Add(-time.Minute), base.Add(time.Minute)
	combined, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{
		FolderID: folderID, Unread: &unread, Flagged: &flagged,
		Sender: "alice", Participant: "bob", Subject: "quarterly",
		RFCMessageID: "<invoice-201@example.test>", AttachmentFilename: "q3.pdf",
		AttachmentType: "application/pdf", After: &after, Before: &before,
		ResultKind: maildomain.SearchMessages, Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(combined.Hits) != 1 || combined.Hits[0].Message.ID != invoice.MessageID || combined.Query.FilterCount != 11 {
		t.Fatalf("combined search = %+v", combined)
	}

	private, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{
		Query: "bcc-secret", ResultKind: maildomain.SearchMessages, Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(private.Hits) != 0 {
		t.Fatalf("BCC leaked through search: %+v", private.Hits)
	}
	foreignPage, err := state.Search().Search(context.Background(), secondAccountID, maildomain.SearchOptions{
		Query: "foreign account", ResultKind: maildomain.SearchMessages, Limit: 20,
	})
	if err != nil || len(foreignPage.Hits) != 1 || foreignPage.Hits[0].Message.ID != foreign.MessageID {
		t.Fatalf("foreign page = %+v err=%v", foreignPage, err)
	}
}

func TestMailSearchCursorStableAcrossEqualTimesAndBoundToQuery(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	stamp := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	records := make([]maildomain.IngestRecord, 0, 7)
	for uid := uint32(211); uid <= 217; uid++ {
		record := searchRecordFixture(accountID, folderID, 100, uid, fmt.Sprintf("equal-%d@example.test", uid), stamp,
			"Equal timestamp", "Pager", "pager@example.test", "stable pagination token", "", "")
		record.InternalDate = stamp
		record.Parsed.HeaderDate = &stamp
		records = append(records, record)
	}
	commitSearchRecords(t, state, accountID, folderID, "chk_search_equal", stamp.Add(time.Minute), records...)

	options := maildomain.SearchOptions{Query: "stable pagination", ResultKind: maildomain.SearchMessages, Limit: 2}
	seen := []string{}
	for {
		page, err := state.Search().Search(context.Background(), accountID, options)
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range page.Hits {
			seen = append(seen, hit.Message.ID)
		}
		if !page.More {
			break
		}
		if page.NextCursor == "" {
			t.Fatal("more page omitted next cursor")
		}
		if len(seen) == 2 {
			changed := options
			changed.Query = "different query"
			changed.Cursor = page.NextCursor
			if _, err := state.Search().Search(context.Background(), accountID, changed); !errors.Is(err, maildomain.ErrInvalidSearchCursor) {
				t.Fatalf("changed-query cursor error = %v", err)
			}
		}
		options.Cursor = page.NextCursor
	}
	if len(seen) != len(records) {
		t.Fatalf("seen = %v", seen)
	}
	want := make([]string, 0, len(records))
	for _, record := range records {
		want = append(want, record.MessageID)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(want)))
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("paged IDs = %v want %v", seen, want)
	}
}

func TestMailSearchRefreshesAfterReparseRethreadAndDelete(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	base := time.Date(2026, 8, 6, 11, 0, 0, 0, time.UTC)
	left := searchRecordFixture(accountID, folderID, 100, 221, "search-left@example.test", base,
		"Left thread", "Alice", "alice@example.test", "shared bridgeword", "", "")
	right := searchRecordFixture(accountID, folderID, 100, 222, "search-right@example.test", base.Add(time.Minute),
		"Right thread", "Bob", "bob@example.test", "shared bridgeword", "", "")
	left.Parsed.InReplyTo, left.Parsed.References = "", nil
	right.Parsed.InReplyTo, right.Parsed.References = "", nil
	commitSearchRecords(t, state, accountID, folderID, "chk_search_threads_a", base.Add(2*time.Minute), left, right)

	threadOptions := maildomain.SearchOptions{Query: "bridgeword", ResultKind: maildomain.SearchThreads, Limit: 20}
	before, err := state.Search().Search(context.Background(), accountID, threadOptions)
	if err != nil || len(before.Hits) != 2 {
		t.Fatalf("before rethread = %+v err=%v", before, err)
	}
	bridge := searchRecordFixture(accountID, folderID, 100, 223, "search-bridge@example.test", base.Add(3*time.Minute),
		"Re: Left thread", "Owner", "mail-test@icloud.com", "bridge connector", "", "")
	bridge.Parsed.References = []string{left.Parsed.RFCMessageID, right.Parsed.RFCMessageID}
	bridge.Parsed.InReplyTo = right.Parsed.RFCMessageID
	commitSearchRecords(t, state, accountID, folderID, "chk_search_threads_b", base.Add(4*time.Minute), bridge)
	after, err := state.Search().Search(context.Background(), accountID, threadOptions)
	if err != nil || len(after.Hits) != 1 || after.Hits[0].Thread.MessageCount != 3 {
		t.Fatalf("after rethread = %+v err=%v", after, err)
	}

	message, err := state.Mail().Message(context.Background(), accountID, left.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	content := *message.Content
	content.SearchText = "replacement body uniquereparse"
	content.SearchTextTruncated = false
	if err := state.Mail().SaveMessageContent(context.Background(), accountID, left.MessageID, left.LocationID, content, base.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	oldBody, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{Query: "shared bridgeword", ResultKind: maildomain.SearchMessages, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range oldBody.Hits {
		if hit.Message.ID == left.MessageID {
			t.Fatalf("old body remained searchable: %+v", oldBody.Hits)
		}
	}
	newBody, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{Query: "uniquereparse", ResultKind: maildomain.SearchMessages, Limit: 20})
	if err != nil || len(newBody.Hits) != 1 || newBody.Hits[0].Message.ID != left.MessageID {
		t.Fatalf("new body = %+v err=%v", newBody, err)
	}

	if _, err := state.db.Exec(`DELETE FROM messages WHERE message_id = ?`, left.MessageID); err != nil {
		t.Fatal(err)
	}
	deleted, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{Query: "uniquereparse", ResultKind: maildomain.SearchMessages, Limit: 20})
	if err != nil || len(deleted.Hits) != 0 {
		t.Fatalf("deleted search = %+v err=%v", deleted, err)
	}
	var documents, ftsRows int
	if err := state.db.QueryRow(`SELECT count(*) FROM message_search_documents WHERE message_id = ?`, left.MessageID).Scan(&documents); err != nil {
		t.Fatal(err)
	}
	if err := state.db.QueryRow(`SELECT count(*) FROM message_search_fts WHERE rowid NOT NULL AND message_search_fts MATCH 'uniquereparse'`).Scan(&ftsRows); err != nil {
		t.Fatal(err)
	}
	if documents != 0 || ftsRows != 0 {
		t.Fatalf("purged search rows documents=%d fts=%d", documents, ftsRows)
	}
}

func TestMailSearchKeepsIndexedBodyBoundToRepresentativeContent(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	base := time.Date(2026, 8, 6, 11, 30, 0, 0, time.UTC)
	first := searchRecordFixture(accountID, folderID, 100, 225, "representative-search@example.test", base,
		"Representative content", "Alice", "alice@example.test", "originalcanonicaltoken", "", "")
	commitSearchRecords(t, state, accountID, folderID, "chk_search_representative_a", base.Add(time.Minute), first)

	second := searchRecordFixture(accountID, folderID, 100, 226, "representative-search@example.test", base.Add(2*time.Minute),
		"Representative content", "Alice", "alice@example.test", "nonrepresentativetoken", "", "")
	// This test exercises representative-content selection for another provider
	// location of the same immutable message, not an RFC Message-ID collision.
	// Corroborate the identity with the same immutable raw evidence.
	second.RawContentHash = first.RawContentHash
	second.RawStorageKey = first.RawStorageKey
	second.RawByteSize = first.RawByteSize
	commitSearchRecords(t, state, accountID, folderID, "chk_search_representative_a", base.Add(3*time.Minute), second)

	original, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{
		Query: "originalcanonicaltoken", ResultKind: maildomain.SearchMessages, Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(original.Hits) != 1 || original.Hits[0].Message.ID != first.MessageID {
		t.Fatalf("representative search = %+v", original.Hits)
	}
	replacement, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{
		Query: "nonrepresentativetoken", ResultKind: maildomain.SearchMessages, Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(replacement.Hits) != 0 {
		t.Fatalf("non-representative body became searchable: %+v", replacement.Hits)
	}
	message, err := state.Mail().Message(context.Background(), accountID, first.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.ContentLocationID != first.LocationID {
		t.Fatalf("representative location = %q want %q", message.ContentLocationID, first.LocationID)
	}
}

func TestMailSearchIndexFailureRollsBackCheckpointAndCanRepairPendingBody(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	base := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	if _, err := state.db.Exec(`
CREATE TRIGGER synthetic_search_index_failure
BEFORE INSERT ON message_search_documents
BEGIN
    SELECT RAISE(ABORT, 'synthetic search index failure');
END;`); err != nil {
		t.Fatal(err)
	}
	record := searchRecordFixture(accountID, folderID, 100, 231, "rollback-search@example.test", base,
		"Rollback", "Alice", "alice@example.test", "rollback body", "", "")
	_, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_search_rollback", AccountID: accountID, FolderID: folderID,
		UIDValidity: 100, Bootstrap: true, LastCommittedUID: 231, LastScannedUID: 231,
		AttemptedAt: base, Records: []maildomain.IngestRecord{record},
	})
	if err == nil || !strings.Contains(err.Error(), "synthetic search index failure") {
		t.Fatalf("commit error = %v", err)
	}
	if _, err := state.Mail().Checkpoint(context.Background(), accountID, folderID); !errors.Is(err, maildomain.ErrCheckpointNotFound) {
		t.Fatalf("checkpoint error = %v", err)
	}
	var messages int
	if err := state.db.QueryRow(`SELECT count(*) FROM messages WHERE account_id = ?`, accountID).Scan(&messages); err != nil || messages != 0 {
		t.Fatalf("messages=%d err=%v", messages, err)
	}
	if _, err := state.db.Exec(`DROP TRIGGER synthetic_search_index_failure`); err != nil {
		t.Fatal(err)
	}
	pending := mailRecordFixture(accountID, folderID, 100, 232, "pending-search@example.test")
	pending.Parsed.Subject = "Pending search"
	pending.Parsed.Snippet = "preview only"
	commitSearchRecords(t, state, accountID, folderID, "chk_search_pending", base.Add(time.Minute), pending)
	if err := state.Search().RecordBodyError(context.Background(), accountID, pending.MessageID, "synthetic_corrupt_body", base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	pendingCount, failedCount, err := state.Search().Counts(context.Background(), accountID)
	if err != nil || pendingCount != 0 || failedCount != 1 {
		t.Fatalf("pending=%d failed=%d err=%v", pendingCount, failedCount, err)
	}
	errorPage, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{Subject: "Pending search", ResultKind: maildomain.SearchMessages, Limit: 20})
	if err != nil || errorPage.Query.IndexState != "error" || errorPage.Query.ErrorCount != 1 {
		t.Fatalf("error-state page = %+v err=%v", errorPage, err)
	}
	if err := state.Search().SaveBody(context.Background(), accountID, pending.MessageID, "repaired body token", false, base.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	repaired, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{Query: "repaired token", ResultKind: maildomain.SearchMessages, Limit: 20})
	if err != nil || len(repaired.Hits) != 1 || repaired.Query.IndexState != "complete" {
		t.Fatalf("repaired page = %+v err=%v", repaired, err)
	}
}

func TestMailSearchHonorsCancellationAndConcurrentSyncCommit(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	base := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	records := make([]maildomain.IngestRecord, 0, 25)
	for uid := uint32(401); uid <= 425; uid++ {
		records = append(records, searchRecordFixture(
			accountID, folderID, 100, uid, fmt.Sprintf("concurrent-%d@example.test", uid),
			base.Add(time.Duration(uid-401)*time.Second), "Concurrent fixture", "Reader",
			"reader@example.test", "concurrent local search body", "", "",
		))
	}
	commitSearchRecords(t, state, accountID, folderID, "chk_search_concurrent_a", base.Add(time.Minute), records...)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := state.Search().Search(canceled, accountID, maildomain.SearchOptions{
		Query: "concurrent", ResultKind: maildomain.SearchMessages, Limit: 20,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled search error = %v", err)
	}

	start := make(chan struct{})
	errorsCh := make(chan error, 9)
	var readers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			for iteration := 0; iteration < 12; iteration++ {
				page, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{
					Query: "concurrent", ResultKind: maildomain.SearchMessages, Limit: 10,
				})
				if err != nil {
					errorsCh <- err
					return
				}
				if len(page.Hits) != 10 || !page.More {
					errorsCh <- fmt.Errorf("reader page count=%d more=%v", len(page.Hits), page.More)
					return
				}
			}
		}()
	}
	close(start)
	newRecord := searchRecordFixture(
		accountID, folderID, 100, 426, "concurrent-426@example.test", base.Add(2*time.Minute),
		"Concurrent committed", "Writer", "writer@example.test", "concurrent writer marker", "", "",
	)
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_search_concurrent_a", AccountID: accountID, FolderID: folderID,
		UIDValidity: 100, Bootstrap: true, LastCommittedUID: 426, LastScannedUID: 426,
		AttemptedAt: base.Add(3 * time.Minute), Records: []maildomain.IngestRecord{newRecord},
	}); err != nil {
		errorsCh <- err
	}
	readers.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	finalPage, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{
		Query: "writer marker", ResultKind: maildomain.SearchMessages, Limit: 20,
	})
	if err != nil || len(finalPage.Hits) != 1 || finalPage.Hits[0].Message.ID != newRecord.MessageID {
		t.Fatalf("final concurrent page = %+v err=%v", finalPage, err)
	}
}

func TestMailSearchQueryPlansAndLargeMailboxStayBounded(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	unread := true
	plans, err := state.Search().SearchQueryPlan(context.Background(), accountID, maildomain.SearchOptions{
		FolderID: folderID, Unread: &unread, ResultKind: maildomain.SearchMessages, Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	joinedPlan := strings.Join(plans, "\n")
	if !strings.Contains(joinedPlan, "messages_account_sort_idx") ||
		!strings.Contains(joinedPlan, "message_locations_message_search_state_idx") {
		t.Fatalf("query plan did not use expected indexes:\n%s", joinedPlan)
	}

	tx, err := state.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	messageStatement, err := tx.Prepare(`
INSERT INTO messages (
    message_id, account_id, rfc_message_id, in_reply_to, direction,
    from_name, from_email, reply_to_name, reply_to_email, subject, snippet,
    header_date, internal_date, sort_at, date_source, attachment_count,
    created_at, updated_at
) VALUES (?, ?, ?, '', 'inbound', 'Bulk Sender', 'bulk@example.test', '', '', ?, ?, ?, ?, ?, 'header', 0, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	documentStatement, err := tx.Prepare(`
INSERT INTO message_search_documents (
    message_id, account_id, subject, sender, participants, body,
    attachment_names, attachment_types, rfc_message_id,
    body_state, body_truncated, body_error_code, indexed_at
) VALUES (?, ?, ?, 'Bulk Sender bulk@example.test', '', ?, '', '', ?, 'complete', 0, '', ?)`)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 6, 13, 0, 0, 0, time.UTC)
	for index := 0; index < 5000; index++ {
		messageID := fmt.Sprintf("msg_bulk_%08d", index)
		rfcID := fmt.Sprintf("bulk-%d@example.test", index)
		subject := fmt.Sprintf("Bulk item %d", index)
		body := fmt.Sprintf("representative mailbox needle token %d", index)
		stamp := base.Add(time.Duration(index) * time.Second).UTC().Format(time.RFC3339Nano)
		if _, err := messageStatement.Exec(messageID, accountID, rfcID, subject, body, stamp, stamp, stamp, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := documentStatement.Exec(messageID, accountID, subject, body, rfcID, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := messageStatement.Close(); err != nil {
		t.Fatal(err)
	}
	if err := documentStatement.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	page, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{
		Query: "needle token", ResultKind: maildomain.SearchMessages, Limit: 100,
	})
	duration := time.Since(started)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Hits) != 100 || !page.More || page.NextCursor == "" {
		t.Fatalf("large page count=%d more=%v cursor=%q", len(page.Hits), page.More, page.NextCursor)
	}
	maxDuration := 5 * time.Second
	if raceDetectorEnabled {
		// Keep the production responsiveness budget strict while allowing for
		// race-detector instrumentation on the same 5,000-row query fixture.
		maxDuration = 30 * time.Second
	}
	if duration > maxDuration {
		t.Fatalf("large search took %s", duration)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 64<<20 {
		t.Fatalf("large search allocated %d bytes", allocated)
	}
}

func TestMailSearchUnreadUsesCanonicalDraftEligibility(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	base := time.Date(2026, 8, 7, 4, 45, 0, 0, time.UTC)
	record := searchRecordFixture(
		accountID, folderID, 100, 427, "draft-unread-427@example.test", base,
		"Draft unread canonical marker", "Owner", "owner@icloud.com", "draft-unread-canonical-token", "", "",
	)
	// This intentionally models an explicitly selected provider Drafts fallback
	// that has no Drafts folder role metadata: the \Draft flag alone must exclude
	// the location from canonical unread state.
	record.Flags = []string{`\Draft`}
	commitSearchRecords(t, state, accountID, folderID, "chk_search_draft_unread", base.Add(time.Minute), record)

	message, err := state.Mail().Message(context.Background(), accountID, record.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.Unread {
		t.Fatalf("canonical message unread unexpectedly true: %+v", message.Locations)
	}
	unread := true
	page, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{
		Query: "draft-unread-canonical-token", FolderID: folderID, Unread: &unread,
		ResultKind: maildomain.SearchMessages, Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Hits) != 0 {
		t.Fatalf("structured unread search diverged from canonical unread state: %+v", page.Hits)
	}
}

func searchRecordFixture(
	accountID, folderID string,
	uidValidity, uid uint32,
	rfcMessageID string,
	stamp time.Time,
	subject, senderName, senderEmail, body, attachmentName, attachmentType string,
) maildomain.IngestRecord {
	record := mailRecordFixture(accountID, folderID, uidValidity, uid, rfcMessageID)
	record.InternalDate = stamp.UTC()
	record.Parsed.RFCMessageID = rfcMessageID
	record.Parsed.InReplyTo = ""
	record.Parsed.References = nil
	record.Parsed.From = maildomain.Address{DisplayName: senderName, Email: senderEmail}
	record.Parsed.ReplyTo = maildomain.Address{}
	record.Parsed.Subject = subject
	record.Parsed.Snippet = body
	record.Parsed.HeaderDate = &record.InternalDate
	record.Parsed.AttachmentCount = 0
	partID := fmt.Sprintf("prt_search_%d_0", uid)
	content := maildomain.NormalizedContent{
		Preview: record.Parsed, TextPaths: []string{"0"}, SearchText: body,
		Parts:              []maildomain.MIMEPart{{ID: partID, Ordinal: 0, Path: "0", MediaType: "text/plain", Charset: "utf-8", DecodedSize: int64(len(body))}},
		AttachmentsIndexed: true,
	}
	if attachmentName != "" {
		attachmentPartID := fmt.Sprintf("prt_search_%d_1", uid)
		content.Parts = append(content.Parts, maildomain.MIMEPart{
			ID: attachmentPartID, Ordinal: 1, Path: "1", MediaType: attachmentType,
			Filename: attachmentName, Disposition: "attachment", Attachment: true,
		})
		hash := strings.Repeat(fmt.Sprintf("%x", uid%16), 64)
		content.Attachments = append(content.Attachments, maildomain.Attachment{
			ID: fmt.Sprintf("att_search_%d", uid), MessageID: record.MessageID,
			PartID: attachmentPartID, Ordinal: 0, PartPath: "1",
			Filename: attachmentName, MediaType: attachmentType, Disposition: "attachment",
			ContentHash: hash[:64], StorageKey: "attachments/" + hash[:64], ByteSize: 32,
			Availability: maildomain.AttachmentAvailable,
		})
		record.Parsed.AttachmentCount = 1
		content.Preview.AttachmentCount = 1
	}
	record.Content = &content
	return record
}

func commitSearchRecords(t *testing.T, state *Store, accountID, folderID, checkpointID string, attemptedAt time.Time, records ...maildomain.IngestRecord) {
	t.Helper()
	lastUID := uint32(0)
	uidValidity := uint32(100)
	for _, record := range records {
		if record.UID > lastUID {
			lastUID = record.UID
		}
		uidValidity = record.UIDValidity
	}
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: checkpointID, AccountID: accountID, FolderID: folderID,
		UIDValidity: uidValidity, Bootstrap: true, LastCommittedUID: lastUID,
		LastScannedUID: lastUID, AttemptedAt: attemptedAt, Records: records,
	}); err != nil {
		t.Fatal(err)
	}
}

func addSearchAccountFixture(t *testing.T, state *Store, now time.Time) (string, string) {
	t.Helper()
	accountID := "acct_search_foreign"
	if _, err := state.Accounts().Create(context.Background(), account.CreateRecord{
		AccountID: accountID, CredentialID: "cred_search_foreign", Name: "Foreign Search",
		Email: "foreign-search@icloud.com", IMAPUsername: "foreign-search",
		CredentialScheme: account.CredentialEnv, CredentialRef: "env:FOREIGN_SEARCH_PASSWORD", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	folders, err := state.Folders().Reconcile(context.Background(), accountID, []folder.RemoteObservation{{
		Name: "INBOX", WireName: "INBOX", Kind: folder.KindInbox, KindSource: "inbox",
		Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 200, StatusKnown: true,
	}}, now, func() (string, error) { return "fld_search_foreign", nil })
	if err != nil {
		t.Fatal(err)
	}
	return accountID, folders[0].ID
}
