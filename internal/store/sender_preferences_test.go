package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestSenderPreferenceRepositoryNormalizesIsolatesFlagsAndCursors(t *testing.T) {
	ctx := context.Background()
	state, accountOneID, _ := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
	accountTwo, err := state.Accounts().Create(ctx, account.CreateRecord{
		AccountID: "acct_0198dd00-0000-7000-8000-000000000002", CredentialID: "cred_0198dd00-0000-7000-8000-000000000002",
		Name: "Second", Email: "second@icloud.com", IMAPUsername: "second",
		CredentialScheme: account.CredentialEnv, CredentialRef: "env:SECOND_PASSWORD", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	preferences := state.SenderPreferences()
	first, err := preferences.SetVIP(ctx, accountOneID, "  Alice@Example.COM  ", true, now)
	if err != nil || first.Email != "alice@example.com" || !first.VIP || first.Blocked {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := preferences.SetBlocked(ctx, accountOneID, "alice@example.com", true, now.Add(time.Minute))
	if err != nil || !second.VIP || !second.Blocked || !second.CreatedAt.Equal(first.CreatedAt) || !second.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("second=%+v first=%+v err=%v", second, first, err)
	}
	third, err := preferences.SetVIP(ctx, accountOneID, "ALICE@example.com", false, now.Add(2*time.Minute))
	if err != nil || third.VIP || !third.Blocked {
		t.Fatalf("third=%+v err=%v", third, err)
	}
	isolated, err := preferences.SetVIP(ctx, accountTwo.ID, "alice@example.com", true, now)
	if err != nil || !isolated.VIP || isolated.Blocked || isolated.AccountID != accountTwo.ID {
		t.Fatalf("isolated=%+v err=%v", isolated, err)
	}
	if _, err := preferences.SetVIP(ctx, accountOneID, "Alice <alice@example.com>", true, now); !errors.Is(err, maildomain.ErrInvalidSenderPreference) {
		t.Fatalf("display-name input err=%v", err)
	}
	if _, err := preferences.SetVIP(ctx, accountOneID, "bob@example.com", true, now); err != nil {
		t.Fatal(err)
	}
	page, err := preferences.ListPage(ctx, accountOneID, "", 1)
	if err != nil || len(page.Preferences) != 1 || !page.More || page.NextCursor == "" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if _, err := preferences.ListPage(ctx, accountTwo.ID, page.NextCursor, 1); !errors.Is(err, maildomain.ErrInvalidListCursor) {
		t.Fatalf("cross-account cursor err=%v", err)
	}
	next, err := preferences.ListPage(ctx, accountOneID, page.NextCursor, 1)
	if err != nil || len(next.Preferences) != 1 || next.Preferences[0].Email <= page.Preferences[0].Email {
		t.Fatalf("next=%+v first=%+v err=%v", next, page, err)
	}

	rows, err := state.db.QueryContext(ctx, `EXPLAIN QUERY PLAN
SELECT 1 FROM sender_preferences AS sp INDEXED BY sender_preferences_account_vip_email_idx
WHERE sp.account_id = ? AND sp.vip = 1 AND sp.email_address = ?`, accountOneID, "bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	plan := ""
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	rows.Close()
	if !strings.Contains(plan, "sender_preferences_account_vip_email_idx") {
		t.Fatalf("query plan does not use VIP index:\n%s", plan)
	}
}

func TestSenderPreferencesProjectIntoMessagesThreadsAndIndexedSearchFilters(t *testing.T) {
	ctx := context.Background()
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	records := make([]maildomain.IngestRecord, 0, 3)
	for index, email := range []string{"vip@example.test", "blocked@example.test", "plain@example.test"} {
		record := mailRecordFixture(accountID, folderID, 100, uint32(index+1), "phase25-"+email)
		record.Parsed.From = maildomain.Address{DisplayName: "Sender", Email: email}
		record.Parsed.Subject = "Phase 25 " + email
		record.Parsed.InReplyTo = "phase25-root@example.test"
		record.Parsed.References = []string{"phase25-root@example.test"}
		records = append(records, record)
	}
	_, err := state.Mail().CommitInboxPage(ctx, maildomain.CommitPage{
		CheckpointID: "chk_0198dd00-0000-7000-8000-000000000001", AccountID: accountID, FolderID: folderID,
		UIDValidity: 100, LastCommittedUID: 3, LastScannedUID: 3, AttemptedAt: time.Date(2026, 8, 11, 2, 30, 0, 0, time.UTC), Records: records,
	})
	if err != nil {
		t.Fatal(err)
	}
	var beforeMessageUpdatedAt, beforeLocationUpdatedAt string
	var beforeSeen, beforeFlagged, beforeAnswered, beforeDeleted int
	if err := state.db.QueryRowContext(ctx, `SELECT updated_at FROM messages WHERE message_id = ?`, records[0].MessageID).Scan(&beforeMessageUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := state.db.QueryRowContext(ctx, `
SELECT updated_at, seen, flagged, answered, deleted
FROM message_locations WHERE location_id = ?`, records[0].LocationID).Scan(
		&beforeLocationUpdatedAt, &beforeSeen, &beforeFlagged, &beforeAnswered, &beforeDeleted,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := state.SenderPreferences().SetVIP(ctx, accountID, "vip@example.test", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := state.SenderPreferences().SetBlocked(ctx, accountID, "blocked@example.test", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	var afterMessageUpdatedAt, afterLocationUpdatedAt string
	var afterSeen, afterFlagged, afterAnswered, afterDeleted int
	if err := state.db.QueryRowContext(ctx, `SELECT updated_at FROM messages WHERE message_id = ?`, records[0].MessageID).Scan(&afterMessageUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := state.db.QueryRowContext(ctx, `
SELECT updated_at, seen, flagged, answered, deleted
FROM message_locations WHERE location_id = ?`, records[0].LocationID).Scan(
		&afterLocationUpdatedAt, &afterSeen, &afterFlagged, &afterAnswered, &afterDeleted,
	); err != nil {
		t.Fatal(err)
	}
	if beforeMessageUpdatedAt != afterMessageUpdatedAt || beforeLocationUpdatedAt != afterLocationUpdatedAt ||
		beforeSeen != afterSeen || beforeFlagged != afterFlagged || beforeAnswered != afterAnswered || beforeDeleted != afterDeleted {
		t.Fatalf("sender preferences mutated message/provider-location state: message_updated=%q->%q location_updated=%q->%q flags=(%d,%d,%d,%d)->(%d,%d,%d,%d)",
			beforeMessageUpdatedAt, afterMessageUpdatedAt, beforeLocationUpdatedAt, afterLocationUpdatedAt,
			beforeSeen, beforeFlagged, beforeAnswered, beforeDeleted, afterSeen, afterFlagged, afterAnswered, afterDeleted)
	}

	message, err := state.Mail().Message(ctx, accountID, records[0].MessageID)
	if err != nil || message.SenderPreference == nil || !message.SenderPreference.Explicit || !message.SenderPreference.VIP || message.SenderPreference.Blocked {
		t.Fatalf("message preference=%+v err=%v", message.SenderPreference, err)
	}
	plain, err := state.Mail().Message(ctx, accountID, records[2].MessageID)
	if err != nil || plain.SenderPreference == nil || plain.SenderPreference.Explicit || plain.SenderPreference.Email != "plain@example.test" {
		t.Fatalf("plain preference=%+v err=%v", plain.SenderPreference, err)
	}

	vip := true
	vipPage, err := state.Search().Search(ctx, accountID, maildomain.SearchOptions{VIP: &vip, Limit: 10})
	if err != nil || len(vipPage.Hits) != 1 || vipPage.Hits[0].Message == nil || vipPage.Hits[0].Message.From.Email != "vip@example.test" || vipPage.Hits[0].Message.SenderPreference == nil || !vipPage.Hits[0].Message.SenderPreference.VIP {
		t.Fatalf("vip search=%+v err=%v", vipPage, err)
	}
	blocked := true
	blockedPage, err := state.Search().Search(ctx, accountID, maildomain.SearchOptions{Blocked: &blocked, Limit: 10})
	if err != nil || len(blockedPage.Hits) != 1 || blockedPage.Hits[0].Message == nil || blockedPage.Hits[0].Message.From.Email != "blocked@example.test" {
		t.Fatalf("blocked search=%+v err=%v", blockedPage, err)
	}

	threads, _, err := state.Threads().Latest(ctx, accountID, 10)
	if err != nil || len(threads) == 0 {
		t.Fatalf("threads=%+v err=%v", threads, err)
	}
	var aggregate *maildomain.ThreadSenderPreferenceSummary
	for index := range threads {
		if threads[index].SenderPreferences != nil && threads[index].SenderPreferences.DistinctSenders >= 3 {
			aggregate = threads[index].SenderPreferences
			break
		}
	}
	if aggregate == nil || aggregate.DistinctSenders != 3 || aggregate.ExplicitSenders != 2 || aggregate.VIPSenders != 1 || aggregate.BlockedSenders != 1 || aggregate.UnpreferredSenders != 1 {
		t.Fatalf("thread aggregate=%+v threads=%+v", aggregate, threads)
	}

	planRows, err := state.db.QueryContext(ctx, `EXPLAIN QUERY PLAN
SELECT m.message_id
FROM messages AS m INDEXED BY messages_account_sort_idx
WHERE m.account_id = ? AND EXISTS (
  SELECT 1 FROM sender_preferences AS sv INDEXED BY sender_preferences_account_vip_email_idx
  WHERE sv.account_id = m.account_id AND sv.email_address = lower(trim(m.from_email)) AND sv.vip = 1
)
	ORDER BY m.sort_at DESC, m.message_id DESC LIMIT 10`, accountID)
	if err != nil {
		t.Fatal(err)
	}
	plan := ""
	for planRows.Next() {
		var id, parent, notused int
		var detail string
		if err := planRows.Scan(&id, &parent, &notused, &detail); err != nil {
			planRows.Close()
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	planRows.Close()
	if !strings.Contains(plan, "messages_account_sort_idx") || !strings.Contains(plan, "sender_preferences_account_vip_email_idx") {
		t.Fatalf("preference search plan not bounded by intended indexes:\n%s", plan)
	}
}
