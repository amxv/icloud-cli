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

func TestLocationMutationIntentIdempotencyAndCopyApply(t *testing.T) {
	state, accountID, inboxID := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 7, 2, 0, 0, 0, time.UTC)
	folders := reconcileLocationMutationFolders(t, state, accountID, base)
	if folders[folder.KindInbox] != inboxID {
		t.Fatalf("inbox ID changed: got=%s want=%s", folders[folder.KindInbox], inboxID)
	}
	record := mailRecordFixture(accountID, inboxID, 100, 41, "mutation-copy@example.test")
	record.Parsed.InReplyTo = ""
	record.Parsed.References = nil
	record.Flags = []string{`\Flagged`, `$Source`}
	commitSentRecord(t, state, record, "chk_mutation_copy", base)
	threadBefore, err := state.Threads().Show(ctx, accountID, record.MessageID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	begin := maildomain.BeginLocationMutation{
		MutationID: "mut_0198d704-6c00-7000-8000-000000000701",
		AccountID:  accountID, MessageID: record.MessageID,
		Action:           maildomain.LocationActionCopy,
		SourceLocationID: record.LocationID, SourceFolderID: inboxID,
		DestinationFolderID: folders[folder.KindArchive],
		ProposedLocationID:  "loc_0198d704-6c00-7000-8000-000000000702",
		RequestHash:         hash, IdempotencyKey: "copy-intent-01", Now: base.Add(time.Minute),
	}
	intent, reused, err := state.LocationMutations().Begin(ctx, begin)
	if err != nil || reused || intent.Status != maildomain.LocationMutationPending {
		t.Fatalf("begin intent=%+v reused=%v err=%v", intent, reused, err)
	}
	replayed, reused, err := state.LocationMutations().Begin(ctx, begin)
	if err != nil || !reused || replayed.ID != intent.ID {
		t.Fatalf("replay intent=%+v reused=%v err=%v", replayed, reused, err)
	}
	conflicting := begin
	conflicting.MutationID = "mut_0198d704-6c00-7000-8000-000000000703"
	conflicting.RequestHash = strings.Repeat("b", 64)
	if _, _, err := state.LocationMutations().Begin(ctx, conflicting); !errors.Is(err, maildomain.ErrMutationConflict) {
		t.Fatalf("conflicting idempotency error = %v", err)
	}

	applied, err := state.LocationMutations().Apply(ctx, maildomain.ApplyLocationMutation{
		MutationID: intent.ID, ProviderMethod: "copy",
		SourceFlags:       maildomain.ParseLocationFlags([]string{`\Flagged`, `$Source`}),
		SourceModSequence: 18, SourcePresent: true,
		DestinationKnown: true, DestinationUIDValidity: 404, DestinationUID: 9,
		DestinationFlags:       maildomain.ParseLocationFlags([]string{`\Flagged`, `$Source`}),
		DestinationModSequence: 2,
		Status:                 maildomain.LocationMutationConfirmed, Now: base.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Status != maildomain.LocationMutationConfirmed || applied.SourcePresent != true ||
		!applied.MappingKnown || applied.DestinationLocationID != begin.ProposedLocationID ||
		applied.DestinationUIDValidity != 404 || applied.DestinationUID != 9 {
		t.Fatalf("applied copy = %+v", applied)
	}
	message, err := state.Mail().Message(ctx, accountID, record.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Locations) != 2 || !message.Flagged {
		t.Fatalf("copy locations/aggregate = %+v", message)
	}
	var source, destination maildomain.Location
	for _, location := range message.Locations {
		switch location.ID {
		case record.LocationID:
			source = location
		case begin.ProposedLocationID:
			destination = location
		}
	}
	if !source.Present || source.ModSequence != 18 || !source.Flags.Flagged ||
		!destination.Present || destination.FolderID != folders[folder.KindArchive] ||
		destination.UIDValidity != 404 || destination.UID != 9 ||
		destination.RawContentHash != source.RawContentHash || destination.RawStorageKey != source.RawStorageKey {
		t.Fatalf("copy source=%+v destination=%+v", source, destination)
	}
	threadAfter, err := state.Threads().Show(ctx, accountID, record.MessageID, "", 20)
	if err != nil || threadAfter.Thread.ID != threadBefore.Thread.ID || threadAfter.Thread.MessageCount != 1 {
		t.Fatalf("copy thread changed before=%+v after=%+v err=%v", threadBefore.Thread, threadAfter.Thread, err)
	}
	values, more, err := state.LocationMutations().List(ctx, accountID, 1)
	if err != nil || more || len(values) != 1 || values[0].ID != intent.ID {
		t.Fatalf("mutation list=%+v more=%v err=%v", values, more, err)
	}
}

func TestLocationMutationMoveAndDeletePreserveLogicalMessageWithOtherLocation(t *testing.T) {
	state, accountID, inboxID := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 7, 3, 0, 0, 0, time.UTC)
	folders := reconcileLocationMutationFolders(t, state, accountID, base)
	record := mailRecordFixture(accountID, inboxID, 100, 42, "mutation-move@example.test")
	record.Parsed.InReplyTo = ""
	record.Parsed.References = nil
	record.Flags = []string{`\Seen`, `$Keep`}
	commitSentRecord(t, state, record, "chk_mutation_move", base)

	copyIntent, _, err := state.LocationMutations().Begin(ctx, maildomain.BeginLocationMutation{
		MutationID: "mut_0198d704-6c00-7000-8000-000000000711",
		AccountID:  accountID, MessageID: record.MessageID, Action: maildomain.LocationActionCopy,
		SourceLocationID: record.LocationID, SourceFolderID: inboxID,
		DestinationFolderID: folders[folder.KindArchive],
		ProposedLocationID:  "loc_0198d704-6c00-7000-8000-000000000712",
		RequestHash:         strings.Repeat("c", 64), Now: base.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	copyApplied, err := state.LocationMutations().Apply(ctx, maildomain.ApplyLocationMutation{
		MutationID: copyIntent.ID, ProviderMethod: "copy",
		SourceFlags: maildomain.ParseLocationFlags(record.Flags), SourcePresent: true,
		DestinationKnown: true, DestinationUIDValidity: 501, DestinationUID: 3,
		DestinationFlags: maildomain.ParseLocationFlags(record.Flags),
		Status:           maildomain.LocationMutationConfirmed, Now: base.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	moveIntent, _, err := state.LocationMutations().Begin(ctx, maildomain.BeginLocationMutation{
		MutationID: "mut_0198d704-6c00-7000-8000-000000000713",
		AccountID:  accountID, MessageID: record.MessageID, Action: maildomain.LocationActionMove,
		SourceLocationID: record.LocationID, SourceFolderID: inboxID,
		DestinationFolderID: folders[folder.KindTrash],
		ProposedLocationID:  "loc_0198d704-6c00-7000-8000-000000000714",
		RequestHash:         strings.Repeat("d", 64), IdempotencyKey: "move-intent-01", Now: base.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	moveApplied, err := state.LocationMutations().Apply(ctx, maildomain.ApplyLocationMutation{
		MutationID: moveIntent.ID, ProviderMethod: "move",
		SourceFlags: maildomain.ParseLocationFlags(record.Flags), SourcePresent: false,
		DestinationKnown: true, DestinationUIDValidity: 601, DestinationUID: 5,
		DestinationFlags: maildomain.ParseLocationFlags(record.Flags),
		Status:           maildomain.LocationMutationConfirmed, Now: base.Add(4 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if moveApplied.SourcePresent || moveApplied.DestinationLocationID == "" {
		t.Fatalf("move applied = %+v", moveApplied)
	}
	replay, err := state.LocationMutations().ByIdempotencyKey(ctx, accountID, "move-intent-01")
	if err != nil || replay.ID != moveIntent.ID {
		t.Fatalf("move replay=%+v err=%v", replay, err)
	}

	deleteIntent, _, err := state.LocationMutations().Begin(ctx, maildomain.BeginLocationMutation{
		MutationID: "mut_0198d704-6c00-7000-8000-000000000715",
		AccountID:  accountID, MessageID: record.MessageID, Action: maildomain.LocationActionDelete,
		SourceLocationID: copyApplied.DestinationLocationID,
		SourceFolderID:   folders[folder.KindArchive],
		RequestHash:      strings.Repeat("e", 64), Now: base.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := state.LocationMutations().Apply(ctx, maildomain.ApplyLocationMutation{
		MutationID: deleteIntent.ID, ProviderMethod: "uid_expunge",
		SourceFlags: maildomain.ParseLocationFlags(record.Flags), SourcePresent: false,
		Status: maildomain.LocationMutationConfirmed, Now: base.Add(6 * time.Minute),
	})
	if err != nil || deleted.SourcePresent {
		t.Fatalf("delete applied=%+v err=%v", deleted, err)
	}
	message, err := state.Mail().Message(ctx, accountID, record.MessageID)
	if err != nil {
		t.Fatalf("logical message was purged: %v", err)
	}
	present := 0
	for _, location := range message.Locations {
		if location.Present {
			present++
		}
	}
	if present != 1 || len(message.Locations) != 3 || message.Unread {
		t.Fatalf("logical message after one-location delete = %+v", message)
	}
	if _, err := state.Threads().Show(ctx, accountID, message.ID, "", 20); err != nil {
		t.Fatalf("thread lost after location delete: %v", err)
	}
}

func TestLocationMutationPersistsUnresolvedEvidenceAndFailedIntent(t *testing.T) {
	state, accountID, inboxID := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	folders := reconcileLocationMutationFolders(t, state, accountID, base)
	record := mailRecordFixture(accountID, inboxID, 100, 43, "mutation-unresolved@example.test")
	record.Parsed.InReplyTo = ""
	record.Parsed.References = nil
	commitSentRecord(t, state, record, "chk_mutation_unresolved", base)
	intent, _, err := state.LocationMutations().Begin(ctx, maildomain.BeginLocationMutation{
		MutationID: "mut_0198d704-6c00-7000-8000-000000000721",
		AccountID:  accountID, MessageID: record.MessageID, Action: maildomain.LocationActionCopy,
		SourceLocationID: record.LocationID, SourceFolderID: inboxID,
		DestinationFolderID: folders[folder.KindArchive],
		ProposedLocationID:  "loc_0198d704-6c00-7000-8000-000000000722",
		RequestHash:         strings.Repeat("f", 64), Now: base.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	unresolved, err := state.LocationMutations().Apply(ctx, maildomain.ApplyLocationMutation{
		MutationID: intent.ID, ProviderMethod: "copy",
		SourceFlags: maildomain.ParseLocationFlags(record.Flags), SourcePresent: true,
		Status: maildomain.LocationMutationUnresolved, ReconciliationRequired: true,
		ErrorCode: "imap_fetch", Now: base.Add(2 * time.Minute),
	})
	if err != nil || unresolved.Status != maildomain.LocationMutationUnresolved ||
		!unresolved.ReconciliationRequired || unresolved.MappingKnown || unresolved.DestinationLocationID != "" {
		t.Fatalf("unresolved mutation=%+v err=%v", unresolved, err)
	}
	message, err := state.Mail().Message(ctx, accountID, record.MessageID)
	if err != nil || len(message.Locations) != 1 || !message.Locations[0].Present {
		t.Fatalf("unresolved copy changed source=%+v err=%v", message, err)
	}
	failedIntent, _, err := state.LocationMutations().Begin(ctx, maildomain.BeginLocationMutation{
		MutationID: "mut_0198d704-6c00-7000-8000-000000000723",
		AccountID:  accountID, MessageID: record.MessageID, Action: maildomain.LocationActionDelete,
		SourceLocationID: record.LocationID, SourceFolderID: inboxID,
		RequestHash: strings.Repeat("1", 64), Now: base.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := state.LocationMutations().MarkFailed(ctx, failedIntent.ID, "imap_expunge", base.Add(4*time.Minute))
	if err != nil || failed.Status != maildomain.LocationMutationFailed || failed.ErrorCode != "imap_expunge" {
		t.Fatalf("failed mutation=%+v err=%v", failed, err)
	}
}

func reconcileLocationMutationFolders(t *testing.T, state *Store, accountID string, now time.Time) map[folder.Kind]string {
	t.Helper()
	observations := []folder.RemoteObservation{
		{Name: "INBOX", WireName: "INBOX", Kind: folder.KindInbox, KindSource: "inbox", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 100, StatusKnown: true},
		{Name: "Archive Localized", WireName: "Archive Localized", Kind: folder.KindArchive, KindSource: "attribute", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 404, StatusKnown: true},
		{Name: "Trash Localized", WireName: "Trash Localized", Kind: folder.KindTrash, KindSource: "attribute", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 601, StatusKnown: true},
		{Name: "Projects", WireName: "Projects", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 701, StatusKnown: true},
	}
	next := 730
	values, err := state.Folders().Reconcile(context.Background(), accountID, observations, now, func() (string, error) {
		next++
		return fmt.Sprintf("fld_0198d704-6c00-7000-8000-%012d", next), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result := map[folder.Kind]string{}
	for _, value := range values {
		result[value.Kind] = value.ID
	}
	return result
}
