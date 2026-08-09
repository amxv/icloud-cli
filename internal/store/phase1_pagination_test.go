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

func TestPhase1AccountListPagesEveryAccount(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	for i := 0; i < 124; i++ {
		if _, err := state.db.ExecContext(ctx, `
INSERT INTO accounts (account_id, friendly_name, email_address, imap_username, status, is_default, created_at, updated_at)
VALUES (?, ?, ?, ?, 'pending_validation', 0, ?, ?)`,
			fmt.Sprintf("acct_page_%03d", i), fmt.Sprintf("Page Account %03d", i), fmt.Sprintf("page-%03d@example.test", i), fmt.Sprintf("page-%03d", i), now, now); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		values, more, next, err := state.Accounts().ListPage(ctx, 17, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			if seen[value.ID] {
				t.Fatalf("duplicate account %s", value.ID)
			}
			seen[value.ID] = true
		}
		assertPhase1CursorContract(t, cursor, more, next)
		if !more {
			break
		}
		cursor = next
	}
	if len(seen) != 125 || !seen[accountID] {
		t.Fatalf("enumerated %d accounts; default present=%v", len(seen), seen[accountID])
	}
	if _, _, _, err := state.Accounts().ListPage(ctx, 10, "ocur_not-an-account-cursor"); !errors.Is(err, account.ErrInvalidCursor) {
		t.Fatalf("foreign cursor error = %v", err)
	}
}

func TestPhase1AccountCursorUsesSQLiteSortKeyForUnicodeNames(t *testing.T) {
	state, _, _ := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 1, 30, 0, 0, time.UTC).Format(time.RFC3339Nano)
	for index, name := range []string{"Älpha", "Ömega"} {
		if _, err := state.db.ExecContext(ctx, `
INSERT INTO accounts (account_id, friendly_name, email_address, imap_username, status, is_default, created_at, updated_at)
VALUES (?, ?, ?, ?, 'pending_validation', 0, ?, ?)`,
			fmt.Sprintf("acct_unicode_%d", index), name, fmt.Sprintf("unicode-%d@example.test", index), fmt.Sprintf("unicode-%d", index), now, now); err != nil {
			t.Fatal(err)
		}
	}
	first, more, cursor, err := state.Accounts().ListPage(ctx, 2, "")
	if err != nil || !more || len(first) != 2 || first[1].Name != "Älpha" || cursor == "" {
		t.Fatalf("first unicode page=%+v more=%v cursor=%q err=%v", first, more, cursor, err)
	}
	second, more, next, err := state.Accounts().ListPage(ctx, 2, cursor)
	if err != nil || more || next != "" || len(second) != 1 || second[0].Name != "Ömega" {
		t.Fatalf("second unicode page=%+v more=%v next=%q err=%v", second, more, next, err)
	}
}

func TestPhase1OutboundAndMutationHistoryPagingPreservesTiesAndScope(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 10, 2, 0, 0, 123, time.UTC)
	secondID := "acct_phase1_second"
	if _, err := state.Accounts().Create(ctx, account.CreateRecord{
		AccountID: secondID, CredentialID: "cred_phase1_second", Name: "Phase1 Second", Email: "phase1-second@example.test",
		IMAPUsername: "phase1-second", CredentialScheme: account.CredentialEnv, CredentialRef: "env:PHASE1_SECOND", Now: base,
	}); err != nil {
		t.Fatal(err)
	}
	rawHash := strings.Repeat("a", 64)
	requestHash := strings.Repeat("b", 64)
	for i := 0; i < 123; i++ {
		_, reused, err := state.Outbound().Begin(ctx, maildomain.BeginOutboundAttempt{
			AttemptID: fmt.Sprintf("send_page_%03d", i), AccountID: accountID, ProposedMessageID: fmt.Sprintf("msg_page_send_%03d", i),
			RequestHash: requestHash, RFCMessageID: fmt.Sprintf("phase1-send-%03d@example.test", i), RawContentHash: rawHash,
			RawStorageKey: "raw/phase1-pagination.eml", RawByteSize: 1, Now: base,
		})
		if err != nil || reused {
			t.Fatalf("begin outbound %d reused=%v err=%v", i, reused, err)
		}
	}
	outboundSeen := map[string]bool{}
	cursor := ""
	firstOutboundCursor := ""
	lastID := ""
	for {
		values, more, next, err := state.Outbound().ListPage(ctx, accountID, 19, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			if outboundSeen[value.ID] {
				t.Fatalf("duplicate outbound %s", value.ID)
			}
			if lastID != "" && value.CreatedAt.Equal(base) && value.ID >= lastID {
				t.Fatalf("equal-time outbound order %s after %s", value.ID, lastID)
			}
			lastID = value.ID
			outboundSeen[value.ID] = true
		}
		assertPhase1CursorContract(t, cursor, more, next)
		if firstOutboundCursor == "" && more {
			firstOutboundCursor = next
		}
		if !more {
			break
		}
		cursor = next
	}
	if len(outboundSeen) != 123 {
		t.Fatalf("enumerated %d outbound attempts", len(outboundSeen))
	}
	if _, _, _, err := state.Outbound().ListPage(ctx, secondID, 10, firstOutboundCursor); !errors.Is(err, maildomain.ErrInvalidListCursor) {
		t.Fatalf("cross-account outbound cursor error = %v", err)
	}

	record := mailRecordFixture(accountID, folderID, 100, 71, "phase1-mutation-source@example.test")
	record.Parsed.InReplyTo = ""
	record.Parsed.References = nil
	commitSentRecord(t, state, record, "chk_phase1_mutation_source", base)
	for i := 0; i < 123; i++ {
		_, reused, err := state.LocationMutations().Begin(ctx, maildomain.BeginLocationMutation{
			MutationID: fmt.Sprintf("mut_page_%03d", i), AccountID: accountID, MessageID: record.MessageID,
			Action: maildomain.LocationActionDelete, SourceLocationID: record.LocationID, SourceFolderID: folderID,
			RequestHash: strings.Repeat("c", 64), Now: base.Add(time.Minute),
		})
		if err != nil || reused {
			t.Fatalf("begin mutation %d reused=%v err=%v", i, reused, err)
		}
	}
	mutationSeen := map[string]bool{}
	cursor = ""
	firstMutationCursor := ""
	lastID = ""
	for {
		values, more, next, err := state.LocationMutations().ListPage(ctx, accountID, 23, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			if mutationSeen[value.ID] {
				t.Fatalf("duplicate mutation %s", value.ID)
			}
			if lastID != "" && value.UpdatedAt.Equal(base.Add(time.Minute)) && value.ID >= lastID {
				t.Fatalf("equal-time mutation order %s after %s", value.ID, lastID)
			}
			lastID = value.ID
			mutationSeen[value.ID] = true
		}
		assertPhase1CursorContract(t, cursor, more, next)
		if firstMutationCursor == "" && more {
			firstMutationCursor = next
		}
		if !more {
			break
		}
		cursor = next
	}
	if len(mutationSeen) != 123 {
		t.Fatalf("enumerated %d mutations", len(mutationSeen))
	}
	if _, _, _, err := state.LocationMutations().ListPage(ctx, secondID, 10, firstMutationCursor); !errors.Is(err, maildomain.ErrInvalidListCursor) {
		t.Fatalf("cross-account mutation cursor error = %v", err)
	}
	if _, _, _, err := state.LocationMutations().ListPage(ctx, accountID, 10, firstOutboundCursor); !errors.Is(err, maildomain.ErrInvalidListCursor) {
		t.Fatalf("cross-status cursor error = %v", err)
	}
}

func TestPhase1CheckpointPagingTraversesPriorityGroups(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 10, 3, 0, 0, 456, time.UTC)
	observations := make([]folder.RemoteObservation, 0, 123)
	for i := 0; i < 123; i++ {
		observations = append(observations, folder.RemoteObservation{
			Name: fmt.Sprintf("Page Folder %03d", i), WireName: fmt.Sprintf("Page Folder %03d", i), Kind: folder.KindOther,
			Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: uint32(1000 + i), StatusKnown: true,
		})
	}
	nextID := 0
	folders, err := state.Folders().Reconcile(ctx, accountID, observations, base, func() (string, error) {
		nextID++
		return fmt.Sprintf("fld_page_%03d", nextID), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	stamp := base.Format(time.RFC3339Nano)
	for i, value := range folders {
		checkpointState := "idle"
		if i%3 == 0 {
			checkpointState = "repair_required"
		} else if i%3 == 1 {
			checkpointState = "failed"
		}
		if _, err := state.db.ExecContext(ctx, `
INSERT INTO sync_checkpoints (checkpoint_id, account_id, folder_id, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`, fmt.Sprintf("chk_page_%03d", i), accountID, value.ID, checkpointState, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	firstCursor := ""
	lastRank := 3
	lastFolder := ""
	for {
		values, more, next, err := state.Mail().CheckpointsPage(ctx, accountID, 16, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			if seen[value.ID] {
				t.Fatalf("duplicate checkpoint %s", value.ID)
			}
			rank := checkpointPhase1Rank(value.State)
			if rank > lastRank {
				t.Fatalf("checkpoint priority regressed from %d to %d", lastRank, rank)
			}
			if rank == lastRank && lastFolder != "" && value.FolderID <= lastFolder {
				t.Fatalf("equal-priority checkpoint folder order %s after %s", value.FolderID, lastFolder)
			}
			if rank != lastRank {
				lastFolder = ""
			}
			lastRank, lastFolder = rank, value.FolderID
			seen[value.ID] = true
		}
		assertPhase1CursorContract(t, cursor, more, next)
		if firstCursor == "" && more {
			firstCursor = next
		}
		if !more {
			break
		}
		cursor = next
	}
	if len(seen) != len(folders) {
		t.Fatalf("enumerated %d checkpoints, want %d", len(seen), len(folders))
	}
	if _, _, _, err := state.Mail().CheckpointsPage(ctx, "acct_other", 10, firstCursor); !errors.Is(err, maildomain.ErrInvalidListCursor) {
		t.Fatalf("cross-account checkpoint cursor error = %v", err)
	}
}

func TestPhase1AttachmentPagingReachesPastOneHundredAndBindsTarget(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 10, 4, 0, 0, 789, time.UTC)
	root := mailRecordFixture(accountID, folderID, 100, 81, "phase1-attachments-root@example.test")
	root.InternalDate = base
	root.Parsed.HeaderDate = &base
	root.Parsed.InReplyTo = ""
	root.Parsed.References = nil
	reply := mailRecordFixture(accountID, folderID, 100, 82, "phase1-attachments-reply@example.test")
	reply.InternalDate = base
	reply.Parsed.HeaderDate = &base
	reply.Parsed.InReplyTo = root.Parsed.RFCMessageID
	reply.Parsed.References = []string{root.Parsed.RFCMessageID}
	if _, err := state.Mail().CommitInboxPage(ctx, maildomain.CommitPage{
		CheckpointID: "chk_phase1_attachments", AccountID: accountID, FolderID: folderID, UIDValidity: 100,
		LastCommittedUID: reply.UID, LastScannedUID: reply.UID, Bootstrap: true, AttemptedAt: base,
		Records: []maildomain.IngestRecord{root, reply},
	}); err != nil {
		t.Fatal(err)
	}
	threadPage, err := state.Threads().Show(ctx, accountID, root.MessageID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if threadPage.Thread.MessageCount != 2 {
		t.Fatalf("fixture did not form one thread: %+v", threadPage.Thread)
	}
	for messageIndex, messageID := range []string{root.MessageID, reply.MessageID} {
		for ordinal := 0; ordinal < 63; ordinal++ {
			partID := fmt.Sprintf("prt_phase1_%d_%03d", messageIndex, ordinal)
			attachmentID := fmt.Sprintf("att_phase1_%d_%03d", messageIndex, ordinal)
			path := fmt.Sprintf("%d.%d", messageIndex+1, ordinal+1)
			if _, err := state.db.ExecContext(ctx, `
INSERT INTO message_parts (part_id, message_id, ordinal, path, attachment, created_at, updated_at)
VALUES (?, ?, ?, ?, 1, ?, ?)`, partID, messageID, ordinal, path, base.Format(time.RFC3339Nano), base.Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			if _, err := state.db.ExecContext(ctx, `
INSERT INTO attachments (attachment_id, message_id, part_id, ordinal, part_path, filename, media_type, availability, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, 'application/octet-stream', 'unavailable', ?, ?)`,
				attachmentID, messageID, partID, ordinal, path, fmt.Sprintf("fixture-%03d.bin", ordinal), base.Format(time.RFC3339Nano), base.Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
		}
	}

	messageSeen, messageOrder, firstMessageCursor := collectPhase1AttachmentPages(t, func(cursor string) ([]maildomain.Attachment, bool, string, error) {
		return state.Mail().AttachmentsPage(ctx, accountID, root.MessageID, 31, cursor)
	})
	if len(messageSeen) != 63 {
		t.Fatalf("enumerated %d root-message attachments", len(messageSeen))
	}
	for index, attachment := range messageOrder {
		if attachment.MessageID != root.MessageID || attachment.Ordinal != index {
			t.Fatalf("message attachment order index=%d attachment=%+v", index, attachment)
		}
	}
	if _, _, _, err := state.Mail().AttachmentsPage(ctx, accountID, reply.MessageID, 10, firstMessageCursor); !errors.Is(err, maildomain.ErrInvalidListCursor) {
		t.Fatalf("cross-message attachment cursor error = %v", err)
	}

	threadSeen, threadOrder, firstThreadCursor := collectPhase1AttachmentPages(t, func(cursor string) ([]maildomain.Attachment, bool, string, error) {
		return state.Mail().AttachmentsForThreadPage(ctx, accountID, threadPage.Thread.ID, 37, cursor)
	})
	if len(threadSeen) != 126 {
		t.Fatalf("enumerated %d thread attachments, want 126", len(threadSeen))
	}
	if !threadSeen["att_phase1_1_062"] {
		t.Fatal("thread pagination did not reach metadata beyond the former first 100 attachments")
	}
	for index, attachment := range threadOrder {
		wantMessage := root.MessageID
		wantOrdinal := index
		if index >= 63 {
			wantMessage = reply.MessageID
			wantOrdinal = index - 63
		}
		if attachment.MessageID != wantMessage || attachment.Ordinal != wantOrdinal {
			t.Fatalf("thread attachment order index=%d attachment=%+v want_message=%s want_ordinal=%d", index, attachment, wantMessage, wantOrdinal)
		}
	}
	if _, _, _, err := state.Mail().AttachmentsPage(ctx, accountID, root.MessageID, 10, firstThreadCursor); !errors.Is(err, maildomain.ErrInvalidListCursor) {
		t.Fatalf("thread cursor reused for message target: %v", err)
	}
}

func collectPhase1AttachmentPages(t *testing.T, page func(string) ([]maildomain.Attachment, bool, string, error)) (map[string]bool, []maildomain.Attachment, string) {
	t.Helper()
	seen := map[string]bool{}
	order := make([]maildomain.Attachment, 0)
	cursor := ""
	first := ""
	for {
		values, more, next, err := page(cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			if seen[value.ID] {
				t.Fatalf("duplicate attachment %s", value.ID)
			}
			seen[value.ID] = true
			order = append(order, value)
		}
		assertPhase1CursorContract(t, cursor, more, next)
		if first == "" && more {
			first = next
		}
		if !more {
			return seen, order, first
		}
		cursor = next
	}
}

func assertPhase1CursorContract(t *testing.T, current string, more bool, next string) {
	t.Helper()
	if more && (next == "" || next == current) {
		t.Fatalf("more=%v current=%q next=%q", more, current, next)
	}
	if !more && next != "" {
		t.Fatalf("terminal page returned cursor %q", next)
	}
}

func checkpointPhase1Rank(state maildomain.CheckpointState) int {
	switch state {
	case maildomain.CheckpointRepairRequired:
		return 3
	case maildomain.CheckpointFailed:
		return 2
	default:
		return 1
	}
}
