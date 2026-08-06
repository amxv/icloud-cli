package store

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestThreadGraphDirectReplyAndLatestAggregates(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	base := time.Date(2026, 8, 6, 8, 0, 0, 0, time.UTC)
	root := threadRecordFixture(accountID, folderID, 100, 101, "root-direct@example.test", "Direct topic", "alice@example.test", "owner@example.test", base)
	root.Flags = nil
	reply := threadRecordFixture(accountID, folderID, 100, 102, "reply-direct@example.test", "Re: Direct topic", "owner@example.test", "alice@example.test", base.Add(time.Minute))
	reply.Parsed.References = []string{"root-direct@example.test"}
	reply.Parsed.InReplyTo = "root-direct@example.test"
	reply.Flags = []string{`\Seen`}
	commitThreadRecords(t, state, accountID, folderID, "chk_thread_direct", 100, base.Add(2*time.Minute), root, reply)

	page, err := state.Threads().Show(context.Background(), accountID, root.MessageID, 20)
	if err != nil {
		t.Fatal(err)
	}
	wantThreadID := provisionalThreadID(root.MessageID)
	if page.Thread.ID != wantThreadID || page.Thread.RootRFCMessageID != "root-direct@example.test" ||
		page.Thread.CanonicalSubject != "Direct topic" || page.Thread.MessageCount != 2 ||
		page.Thread.UnreadCount != 1 || page.Thread.AttachmentCount != 0 || page.More {
		t.Fatalf("thread = %+v", page.Thread)
	}
	if got := messageIDs(page.Thread.Messages); !reflect.DeepEqual(got, []string{root.MessageID, reply.MessageID}) {
		t.Fatalf("chronological message IDs = %v", got)
	}
	if !reflect.DeepEqual(page.Thread.Participants, []string{"alice@example.test", "owner@example.test"}) {
		t.Fatalf("participants = %v", page.Thread.Participants)
	}
	resolved := 0
	for _, edge := range page.Edges {
		if edge.ChildMessageID == reply.MessageID && edge.ParentMessageID == root.MessageID && edge.Status == "resolved" {
			resolved++
		}
	}
	if resolved != 2 {
		t.Fatalf("resolved reply edges = %+v", page.Edges)
	}
	latest, more, err := state.Threads().Latest(context.Background(), accountID, 20)
	if err != nil || more || len(latest) != 1 || latest[0].ID != wantThreadID {
		t.Fatalf("latest=%+v more=%v err=%v", latest, more, err)
	}
}

func TestThreadGraphLateBridgeRetainsCanonicalIDAndRedirectAlias(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	base := time.Date(2026, 8, 6, 8, 10, 0, 0, time.UTC)
	left := threadRecordFixture(accountID, folderID, 100, 111, "left-bridge@example.test", "Left conversation", "alice@example.test", "owner@example.test", base)
	right := threadRecordFixture(accountID, folderID, 100, 112, "right-bridge@example.test", "Right conversation", "bob@example.test", "owner@example.test", base.Add(time.Minute))
	commitThreadRecords(t, state, accountID, folderID, "chk_thread_bridge_a", 100, base.Add(2*time.Minute), left)
	commitThreadRecords(t, state, accountID, folderID, "chk_thread_bridge_b", 100, base.Add(3*time.Minute), right)
	leftThread := provisionalThreadID(left.MessageID)
	rightThread := provisionalThreadID(right.MessageID)

	bridge := threadRecordFixture(accountID, folderID, 100, 113, "bridge@example.test", "Re: Left conversation", "owner@example.test", "alice@example.test", base.Add(4*time.Minute))
	bridge.Parsed.References = []string{"left-bridge@example.test", "right-bridge@example.test"}
	bridge.Parsed.InReplyTo = "right-bridge@example.test"
	commitThreadRecords(t, state, accountID, folderID, "chk_thread_bridge_c", 100, base.Add(5*time.Minute), bridge)

	page, err := state.Threads().Show(context.Background(), accountID, rightThread, 20)
	if err != nil {
		t.Fatal(err)
	}
	if page.Thread.ID != leftThread || page.Thread.RedirectedFrom != rightThread || page.Thread.MessageCount != 3 {
		t.Fatalf("redirected merged thread = %+v", page.Thread)
	}
	gotIDs := messageIDs(page.Thread.Messages)
	if !reflect.DeepEqual(gotIDs, []string{left.MessageID, right.MessageID, bridge.MessageID}) {
		t.Fatalf("merged message IDs = %v", gotIDs)
	}
	for _, record := range []maildomain.IngestRecord{left, right, bridge} {
		message, err := state.Mail().Message(context.Background(), accountID, record.MessageID)
		if err != nil {
			t.Fatal(err)
		}
		if message.ID != record.MessageID || len(message.Locations) != 1 || message.Locations[0].ID != record.LocationID {
			t.Fatalf("immutable identity changed: record=%+v message=%+v", record, message)
		}
	}
}

func TestThreadGraphMissingParentLaterRootAndLongReferences(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	base := time.Date(2026, 8, 6, 8, 20, 0, 0, time.UTC)
	missing := "late-root@example.test"
	first := threadRecordFixture(accountID, folderID, 100, 121, "late-child-one@example.test", "Re: Late root", "owner@example.test", "alice@example.test", base)
	first.Parsed.References = []string{missing}
	first.Parsed.InReplyTo = missing
	second := threadRecordFixture(accountID, folderID, 100, 122, "late-child-two@example.test", "Re: Late root", "alice@example.test", "owner@example.test", base.Add(time.Minute))
	second.Parsed.References = append([]string{missing}, makeReferenceChain(40)...)
	second.Parsed.InReplyTo = "late-child-one@example.test"
	commitThreadRecords(t, state, accountID, folderID, "chk_thread_late_children", 100, base.Add(2*time.Minute), first, second)

	before, err := state.Threads().Show(context.Background(), accountID, first.MessageID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if before.Thread.MessageCount != 2 || before.Thread.RootRFCMessageID != missing {
		t.Fatalf("provisional missing-parent thread = %+v", before.Thread)
	}
	unresolved := false
	for _, edge := range before.Edges {
		if edge.ChildMessageID == first.MessageID && edge.Token == missing && edge.Status == "unresolved" {
			unresolved = true
		}
	}
	if !unresolved {
		t.Fatalf("missing parent edge = %+v", before.Edges)
	}

	root := threadRecordFixture(accountID, folderID, 100, 123, missing, "Late root", "alice@example.test", "owner@example.test", base.Add(-time.Minute))
	commitThreadRecords(t, state, accountID, folderID, "chk_thread_late_root", 100, base.Add(3*time.Minute), root)
	after, err := state.Threads().Show(context.Background(), accountID, first.MessageID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if after.Thread.ID != before.Thread.ID || after.Thread.MessageCount != 3 || after.Thread.RootRFCMessageID != missing {
		t.Fatalf("late-root merged thread = %+v", after.Thread)
	}
	if got := messageIDs(after.Thread.Messages); !reflect.DeepEqual(got, []string{root.MessageID, first.MessageID, second.MessageID}) {
		t.Fatalf("late-root chronology = %v", got)
	}
	resolvedRoot := false
	for _, edge := range after.Edges {
		if edge.Token == missing && edge.ParentMessageID == root.MessageID && edge.Status == "resolved" {
			resolvedRoot = true
		}
	}
	if !resolvedRoot {
		t.Fatalf("late root did not resolve evidence: %+v", after.Edges)
	}
}

func TestThreadGraphCyclesMalformedEvidenceAndFallbackBoundaries(t *testing.T) {
	t.Run("cycle remains visible", func(t *testing.T) {
		state, accountID, folderID := newMailStoreFixture(t)
		defer state.Close()
		base := time.Date(2026, 8, 6, 8, 30, 0, 0, time.UTC)
		left := threadRecordFixture(accountID, folderID, 100, 131, "cycle-left@example.test", "Cycle", "alice@example.test", "owner@example.test", base)
		left.Parsed.InReplyTo = "cycle-right@example.test"
		right := threadRecordFixture(accountID, folderID, 100, 132, "cycle-right@example.test", "Re: Cycle", "owner@example.test", "alice@example.test", base.Add(time.Minute))
		right.Parsed.InReplyTo = "cycle-left@example.test"
		commitThreadRecords(t, state, accountID, folderID, "chk_thread_cycle", 100, base.Add(2*time.Minute), left, right)
		page, err := state.Threads().Show(context.Background(), accountID, left.MessageID, 20)
		if err != nil {
			t.Fatal(err)
		}
		cycles := 0
		for _, edge := range page.Edges {
			if edge.Status == "cycle" {
				cycles++
			}
		}
		if page.Thread.MessageCount != 2 || cycles != 2 {
			t.Fatalf("cycle thread=%+v edges=%+v", page.Thread, page.Edges)
		}
	})

	t.Run("malformed evidence suppresses fallback", func(t *testing.T) {
		state, accountID, folderID := newMailStoreFixture(t)
		defer state.Close()
		base := time.Date(2026, 8, 6, 8, 40, 0, 0, time.UTC)
		first := threadRecordFixture(accountID, folderID, 100, 141, "malformed-one@example.test", "Malformed topic", "alice@example.test", "owner@example.test", base)
		first.Parsed.InReplyTo = "not a message id"
		second := threadRecordFixture(accountID, folderID, 100, 142, "malformed-two@example.test", "Re: Malformed topic", "owner@example.test", "alice@example.test", base.Add(time.Minute))
		second.Parsed.InReplyTo = "also-not-an-id"
		commitThreadRecords(t, state, accountID, folderID, "chk_thread_malformed", 100, base.Add(2*time.Minute), first, second)
		latest, _, err := state.Threads().Latest(context.Background(), accountID, 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(latest) != 2 {
			t.Fatalf("malformed evidence falsely merged threads: %+v", latest)
		}
		for _, record := range []maildomain.IngestRecord{first, second} {
			page, err := state.Threads().Show(context.Background(), accountID, record.MessageID, 20)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Edges) != 1 || page.Edges[0].Status != "malformed" || page.Edges[0].Relation != "in_reply_to" {
				t.Fatalf("malformed edge = %+v", page.Edges)
			}
		}
	})

	t.Run("narrow subject participant fallback", func(t *testing.T) {
		state, accountID, folderID := newMailStoreFixture(t)
		defer state.Close()
		base := time.Date(2026, 8, 6, 8, 50, 0, 0, time.UTC)
		root := threadRecordFixture(accountID, folderID, 100, 151, "fallback-root@example.test", "Fallback topic", "alice@example.test", "owner@example.test", base)
		reply := threadRecordFixture(accountID, folderID, 100, 152, "fallback-reply@example.test", "Re: Fallback topic", "owner@example.test", "alice@example.test", base.Add(time.Minute))
		duplicate := threadRecordFixture(accountID, folderID, 100, 153, "fallback-duplicate@example.test", "Fallback topic", "alice@example.test", "owner@example.test", base.Add(30*time.Minute))
		otherParticipants := threadRecordFixture(accountID, folderID, 100, 154, "fallback-other@example.test", "Re: Fallback topic", "carol@example.test", "owner@example.test", base.Add(2*time.Minute))
		commitThreadRecords(t, state, accountID, folderID, "chk_thread_fallback", 100, base.Add(time.Hour), root, reply, duplicate, otherParticipants)
		latest, _, err := state.Threads().Latest(context.Background(), accountID, 20)
		if err != nil {
			t.Fatal(err)
		}
		counts := []int{}
		for _, thread := range latest {
			counts = append(counts, thread.MessageCount)
		}
		sort.Ints(counts)
		if !reflect.DeepEqual(counts, []int{1, 1, 2}) {
			t.Fatalf("fallback thread sizes = %v threads=%+v", counts, latest)
		}
	})
}

func TestThreadGraphSplitFoldersAndUnreadAggregateRefresh(t *testing.T) {
	state, accountID, inboxID := newMailStoreFixture(t)
	defer state.Close()
	base := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	sentID := insertMailFolderFixture(t, state, accountID, folder.KindSent, "Sent Messages", 200, base)
	inbox := threadRecordFixture(accountID, inboxID, 100, 161, "split-location@example.test", "Split folder", "alice@example.test", "owner@example.test", base)
	inbox.Flags = nil
	commitThreadRecords(t, state, accountID, inboxID, "chk_thread_split_inbox", 100, base.Add(time.Minute), inbox)
	sent := threadRecordFixture(accountID, sentID, 200, 7, "split-location@example.test", "Split folder", "owner@example.test", "alice@example.test", base.Add(2*time.Minute))
	sent.Direction = maildomain.DirectionOutbound
	sent.Flags = nil
	commitThreadRecords(t, state, accountID, sentID, "chk_thread_split_sent", 200, base.Add(3*time.Minute), sent)

	page, err := state.Threads().Show(context.Background(), accountID, inbox.MessageID, 20)
	if err != nil {
		t.Fatal(err)
	}
	wantFolders := []string{inboxID, sentID}
	sort.Strings(wantFolders)
	if page.Thread.MessageCount != 1 || page.Thread.UnreadCount != 1 || !reflect.DeepEqual(page.Thread.FolderIDs, wantFolders) || len(page.Thread.Messages[0].Locations) != 2 {
		t.Fatalf("split-folder aggregate = %+v", page.Thread)
	}
	if _, err := state.Mail().UpdateLocationState(context.Background(), accountID, inbox.LocationID, 0, maildomain.LocationFlags{Seen: true}, 1, true, base.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	refreshed, err := state.Threads().Show(context.Background(), accountID, page.Thread.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Thread.UnreadCount != 0 || refreshed.Thread.MessageCount != 1 || !reflect.DeepEqual(refreshed.Thread.FolderIDs, wantFolders) {
		t.Fatalf("refreshed aggregate = %+v", refreshed.Thread)
	}
}

func threadRecordFixture(accountID, folderID string, uidValidity, uid uint32, rfcMessageID, subject, from, to string, at time.Time) maildomain.IngestRecord {
	record := mailRecordFixture(accountID, folderID, uidValidity, uid, rfcMessageID)
	record.InternalDate = at
	record.Direction = maildomain.DirectionInbound
	record.Flags = nil
	record.Parsed = maildomain.ParsedMessage{
		RFCMessageID: rfcMessageID,
		From:         maildomain.Address{Email: from},
		To:           []maildomain.Address{{Email: to}},
		Subject:      subject,
		Snippet:      "Snippet for " + subject,
		HeaderDate:   &at,
	}
	record.ContentFingerprint = "content:" + fmt.Sprintf("%064x", uid+1000)
	return record
}

func commitThreadRecords(t *testing.T, state *Store, accountID, folderID, checkpointID string, uidValidity uint32, attemptedAt time.Time, records ...maildomain.IngestRecord) maildomain.CommitResult {
	t.Helper()
	var lastUID uint32
	for _, record := range records {
		if record.UID > lastUID {
			lastUID = record.UID
		}
	}
	result, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: checkpointID,
		AccountID:    accountID, FolderID: folderID, UIDValidity: uidValidity,
		Bootstrap: true, LastCommittedUID: lastUID, LastScannedUID: lastUID,
		AttemptedAt: attemptedAt, Records: records,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func makeReferenceChain(count int) []string {
	values := make([]string, 0, count)
	for index := 0; index < count; index++ {
		values = append(values, fmt.Sprintf("chain-%03d@example.test", index))
	}
	return values
}
