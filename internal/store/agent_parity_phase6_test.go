package store

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestAgentParityPhase6HistoricalDirectionReclassificationIsQueuedBoundedResumableAndMonotonic(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	state, err := Open(context.Background(), testPaths(t, home))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	accountValue := createPhase4Account(t, state, 1, "primary@example.test")
	folders, err := state.Folders().Reconcile(context.Background(), accountValue.ID, []folder.RemoteObservation{{
		Name: "INBOX", WireName: "INBOX", Kind: folder.KindInbox, KindSource: "provider",
		Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 611, StatusKnown: true,
	}}, now, func() (string, error) { return "fld_phase6_inbox", nil })
	if err != nil || len(folders) != 1 {
		t.Fatalf("folders=%+v err=%v", folders, err)
	}
	folderID := folders[0].ID
	aliasAddress := "historical-alias@example.test"
	records := make([]maildomain.IngestRecord, 0, 41)
	for index := 1; index <= 40; index++ {
		from := fmt.Sprintf("external-%d@example.test", index)
		if index%9 == 0 {
			from = aliasAddress
		}
		record := threadRecordFixture(accountValue.ID, folderID, 611, uint32(index), fmt.Sprintf("phase6-%d@example.test", index), "Historical alias fixture", from, accountValue.Email, now.Add(time.Duration(index)*time.Minute))
		record.Direction = maildomain.DirectionInbound
		records = append(records, record)
	}
	commitThreadRecords(t, state, accountValue.ID, folderID, "chk_phase6_direction", 611, now.Add(10*time.Minute), records...)

	threadBefore, err := state.Threads().Show(context.Background(), accountValue.ID, records[0].MessageID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	searchBefore, err := state.Search().Search(context.Background(), accountValue.ID, maildomain.SearchOptions{Query: "Historical alias fixture", ResultKind: maildomain.SearchMessages, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	searchIDsBefore := phase6SearchMessageIDs(searchBefore)

	alias, err := state.Identities().Create(context.Background(), account.MailIdentityCreateRecord{
		IdentityID: "mid_0198e8c0-6000-7000-8000-000000000001", AccountID: accountValue.ID,
		Address: aliasAddress, DisplayName: "Historical Alias", Kind: account.MailIdentityICloudAlias,
		Provenance: account.MailIdentityUserConfigured, Verification: account.MailIdentityUnverified,
		Enabled: true, Now: now.Add(11 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := state.Mail().Message(context.Background(), accountValue.ID, records[0].MessageID)
	if err != nil || message.Direction != maildomain.DirectionInbound {
		t.Fatalf("identity creation scanned/reclassified synchronously: direction=%q err=%v", message.Direction, err)
	}

	first, err := state.Identities().ReclassifyDirectionPage(context.Background(), accountValue.ID, 7, now.Add(50*time.Minute))
	if err != nil || first.Selected != 7 || first.Promoted != 0 || !first.More || !first.Pending {
		t.Fatalf("first reclassification=%+v err=%v", first, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = Open(context.Background(), testPaths(t, home))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	second, err := state.Identities().ReclassifyDirectionPage(context.Background(), accountValue.ID, 7, now.Add(51*time.Minute))
	if err != nil || second.Selected != 7 || second.Promoted != 1 || !second.More || !second.Pending {
		t.Fatalf("resumed reclassification=%+v err=%v", second, err)
	}
	var totals account.DirectionReclassificationSummary
	for page := 0; page < 10; page++ {
		current, err := state.Identities().ReclassifyDirectionPage(context.Background(), accountValue.ID, 7, now.Add(time.Duration(52+page)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		totals.Selected += current.Selected
		totals.Promoted += current.Promoted
		if !current.More {
			if current.Pending {
				t.Fatalf("terminal reclassification still pending: %+v", current)
			}
			break
		}
	}
	if first.Selected+second.Selected+totals.Selected != len(records) || first.Promoted+second.Promoted+totals.Promoted != 4 {
		t.Fatalf("bounded totals first=%+v second=%+v rest=%+v records=%d", first, second, totals, len(records))
	}
	done, err := state.Identities().ReclassifyDirectionPage(context.Background(), accountValue.ID, 7, now.Add(70*time.Minute))
	if err != nil || done.Selected != 0 || done.Promoted != 0 || done.More || done.Pending {
		t.Fatalf("completed reclassification=%+v err=%v", done, err)
	}
	for index, record := range records {
		message, err := state.Mail().Message(context.Background(), accountValue.ID, record.MessageID)
		want := maildomain.DirectionInbound
		if (index+1)%9 == 0 {
			want = maildomain.DirectionOutbound
		}
		if err != nil || message.Direction != want {
			t.Fatalf("message %s direction=%q want=%q err=%v", record.MessageID, message.Direction, want, err)
		}
	}
	threadAfter, err := state.Threads().Show(context.Background(), accountValue.ID, records[0].MessageID, "", 20)
	if err != nil || threadAfter.Thread.ID != threadBefore.Thread.ID {
		t.Fatalf("thread changed before=%s after=%s err=%v", threadBefore.Thread.ID, threadAfter.Thread.ID, err)
	}
	searchAfter, err := state.Search().Search(context.Background(), accountValue.ID, maildomain.SearchOptions{Query: "Historical alias fixture", ResultKind: maildomain.SearchMessages, Limit: 20})
	if err != nil || !reflect.DeepEqual(phase6SearchMessageIDs(searchAfter), searchIDsBefore) {
		t.Fatalf("search IDs changed before=%v after=%v err=%v", searchIDsBefore, phase6SearchMessageIDs(searchAfter), err)
	}
	if _, err := state.Identities().SetEnabled(context.Background(), accountValue.ID, alias.ID, false, now.Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for index, record := range records {
		message, err := state.Mail().Message(context.Background(), accountValue.ID, record.MessageID)
		if (index+1)%9 == 0 && (err != nil || message.Direction != maildomain.DirectionOutbound) {
			t.Fatalf("direction demoted after identity disable: message=%s direction=%q err=%v", record.MessageID, message.Direction, err)
		}
	}
}

func phase6SearchMessageIDs(page maildomain.SearchPage) []string {
	ids := make([]string, 0, len(page.Hits))
	for _, hit := range page.Hits {
		if hit.Message != nil {
			ids = append(ids, hit.Message.ID)
		}
	}
	sort.Strings(ids)
	return ids
}
