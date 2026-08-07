package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestDraftPaginationCoversMoreThanOneThousandAndExactOldDraftIsDirectlyAddressable(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	for index := 1; index <= 1001; index++ {
		messageID := fmt.Sprintf("msg_bulk_draft_%05d", index)
		draftID := maildomain.DraftIDForMessage(messageID)
		stamp := base.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano)
		if _, err := state.db.ExecContext(ctx, `
INSERT INTO messages (
    message_id, account_id, rfc_message_id, in_reply_to, direction,
    from_name, from_email, reply_to_name, reply_to_email, subject, snippet,
    date_source, attachment_count, created_at, updated_at
) VALUES (?, ?, ?, '', 'outbound', '', 'owner@icloud.com', '', '', ?, '', 'unknown', 0, ?, ?)`,
			messageID, accountID, fmt.Sprintf("bulk-draft-%05d@example.test", index), fmt.Sprintf("Bulk draft %05d", index), stamp, stamp); err != nil {
			t.Fatal(err)
		}
		stateValue := string(maildomain.DraftSynced)
		if index == 1 {
			stateValue = string(maildomain.DraftReconcile)
		}
		if _, err := state.db.ExecContext(ctx, `
INSERT INTO mail_drafts (
    draft_id, account_id, message_id, revision, sync_state, remote_location_id,
    previous_location_id, outbound_attempt_id, custom_headers_json, last_error_code,
    created_at, updated_at
) VALUES (?, ?, ?, 1, ?, '', '', '', '[]', '', ?, ?)`,
			draftID, accountID, messageID, stateValue, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	legacySlice, more, err := state.Drafts().List(ctx, accountID, 1000)
	if err != nil || !more || len(legacySlice) != 1000 {
		t.Fatalf("bounded first slice len=%d more=%v err=%v", len(legacySlice), more, err)
	}
	oldestID := "drf_bulk_draft_00001"
	for _, value := range legacySlice {
		if value.ID == oldestID {
			t.Fatal("oldest draft unexpectedly appeared in newest-1000 slice")
		}
	}
	exact, err := state.Drafts().Draft(ctx, accountID, oldestID)
	if err != nil || exact.ID != oldestID || exact.SyncState != maildomain.DraftReconcile {
		t.Fatalf("exact old draft=%+v err=%v", exact, err)
	}

	cursor := ""
	total := 0
	seenOldest := false
	for {
		values, pageMore, nextCursor, err := state.Drafts().ListPage(ctx, accountID, cursor, 73)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			total++
			seenOldest = seenOldest || value.ID == oldestID
		}
		if !pageMore {
			if nextCursor != "" {
				t.Fatalf("final page returned cursor %q", nextCursor)
			}
			break
		}
		if nextCursor == "" || nextCursor == cursor {
			t.Fatalf("pagination did not advance cursor old=%q new=%q", cursor, nextCursor)
		}
		cursor = nextCursor
	}
	if total != 1001 || !seenOldest {
		t.Fatalf("paginated drafts total=%d saw_oldest=%v", total, seenOldest)
	}

	actionable, actionableMore, err := state.Drafts().ListActionablePage(ctx, accountID, "", 10)
	if err != nil || actionableMore || len(actionable) != 1 || actionable[0].ID != oldestID {
		t.Fatalf("actionable old draft=%+v more=%v err=%v", actionable, actionableMore, err)
	}
}
