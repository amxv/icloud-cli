package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestFolderReconciliationPreservesIdentityPolicyAndMissingState(t *testing.T) {
	state, accountValue := newFolderTestState(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 4, 0, 0, 0, time.UTC)
	counter := 0
	newID := func() (string, error) {
		counter++
		return fmt.Sprintf("fld_0198d704-6c00-7000-8000-%012d", counter), nil
	}
	initial := []folder.RemoteObservation{
		{Name: "INBOX", WireName: "INBOX", Delimiter: "/", Kind: folder.KindInbox, KindSource: "inbox", Selectable: true, Subscription: folder.SubscriptionUnknown, Messages: 15, Unseen: 2, UIDNext: 20, UIDValidity: 100, StatusKnown: true},
		{Name: "Sent Messages", WireName: "Sent Messages", Delimiter: "/", Kind: folder.KindSent, KindSource: "attribute", Selectable: true, Subscription: folder.SubscriptionUnknown, Messages: 4, UIDNext: 5, UIDValidity: 101, StatusKnown: true},
		{Name: "Projects", WireName: "Projects", Delimiter: "/", Kind: folder.KindOther, Selectable: false, Subscription: folder.SubscriptionUnknown},
		{Name: "Projects/Alpha", WireName: "Projects/Alpha", ParentName: "Projects", ParentWireName: "Projects", Delimiter: "/", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionSubscribed, Messages: 1, Unseen: 1, UIDNext: 2, UIDValidity: 102, StatusKnown: true},
		{Name: "Unicode-日本語", WireName: "Unicode-&ZeVnLIqe-", Delimiter: "/", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionUnknown, Messages: 2, UIDNext: 3, UIDValidity: 103, StatusKnown: true},
	}
	values, err := state.Folders().Reconcile(context.Background(), accountValue.ID, initial, now, newID)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 5 || counter != 5 {
		t.Fatalf("folders=%d generated=%d", len(values), counter)
	}
	byName := foldersByName(values)
	if !byName["INBOX"].SyncEnabled || !byName["Sent Messages"].SyncEnabled {
		t.Fatalf("system defaults: inbox=%+v sent=%+v", byName["INBOX"], byName["Sent Messages"])
	}
	if byName["Projects/Alpha"].SyncEnabled || byName["Unicode-日本語"].SyncEnabled {
		t.Fatalf("ordinary folder enabled by default: %+v", byName)
	}
	if byName["Projects"].Selectable || byName["Projects"].StatusAt != nil {
		t.Fatalf("container status = %+v", byName["Projects"])
	}
	if byName["Projects/Alpha"].ParentName != "Projects" || byName["Projects/Alpha"].Subscription != folder.SubscriptionSubscribed {
		t.Fatalf("nested metadata = %+v", byName["Projects/Alpha"])
	}
	unicodeID := byName["Unicode-日本語"].ID
	alphaID := byName["Projects/Alpha"].ID
	sentID := byName["Sent Messages"].ID
	projectsID := byName["Projects"].ID
	if _, err := state.Folders().SetSync(context.Background(), accountValue.ID, unicodeID, true, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	rediscovered := []folder.RemoteObservation{
		{Name: "INBOX", WireName: "INBOX", Delimiter: "/", Kind: folder.KindInbox, KindSource: "inbox", Selectable: true, Subscription: folder.SubscriptionUnknown, Messages: 16, Unseen: 3, UIDNext: 21, UIDValidity: 100, StatusKnown: true},
		{Name: "Envoyés", WireName: "Envoy&AOk-s", Delimiter: "/", Kind: folder.KindSent, KindSource: "attribute", Selectable: true, Subscription: folder.SubscriptionUnknown, Messages: 5, UIDNext: 6, UIDValidity: 101, StatusKnown: true},
		{Name: "Projects/Beta", WireName: "Projects/Beta", OldName: "Projects/Alpha", OldWireName: "Projects/Alpha", ParentName: "Projects", ParentWireName: "Projects", Delimiter: "/", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionUnknown, Messages: 2, UIDNext: 3, UIDValidity: 104, StatusKnown: true},
		{Name: "Unicode-日本語", WireName: "Unicode-&ZeVnLIqe-", Delimiter: "/", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionUnknown, Messages: 3, Unseen: 1, UIDNext: 4, UIDValidity: 103, StatusKnown: true},
	}
	values, err = state.Folders().Reconcile(context.Background(), accountValue.ID, rediscovered, now.Add(2*time.Minute), newID)
	if err != nil {
		t.Fatal(err)
	}
	if counter != 5 {
		t.Fatalf("rename created new IDs: %d", counter)
	}
	byName = foldersByName(values)
	if byName["Envoyés"].ID != sentID || !byName["Envoyés"].SyncEnabled {
		t.Fatalf("special-use rename = %+v", byName["Envoyés"])
	}
	if byName["Projects/Beta"].ID != alphaID {
		t.Fatalf("OLDNAME rename = %+v", byName["Projects/Beta"])
	}
	if byName["Unicode-日本語"].ID != unicodeID || !byName["Unicode-日本語"].SyncEnabled {
		t.Fatalf("policy not preserved = %+v", byName["Unicode-日本語"])
	}
	missing := byName["Projects"]
	if missing.ID != projectsID || missing.Present || !missing.Stale || missing.MissingSince == nil {
		t.Fatalf("missing folder = %+v", missing)
	}
	present, more, err := state.Folders().List(context.Background(), accountValue.ID, 100, false)
	if err != nil || more || len(present) != 4 {
		t.Fatalf("present list len=%d more=%v err=%v", len(present), more, err)
	}

	reappeared := append(rediscovered, folder.RemoteObservation{Name: "Projects", WireName: "Projects", Delimiter: "/", Kind: folder.KindOther, Selectable: false, Subscription: folder.SubscriptionUnknown})
	values, err = state.Folders().Reconcile(context.Background(), accountValue.ID, reappeared, now.Add(3*time.Minute), newID)
	if err != nil {
		t.Fatal(err)
	}
	if counter != 5 {
		t.Fatalf("reappearance created new ID: %d", counter)
	}
	value := foldersByName(values)["Projects"]
	if value.ID != projectsID || !value.Present || value.Stale || value.MissingSince != nil {
		t.Fatalf("reappeared folder = %+v", value)
	}
}

func TestFolderSelectionIsAccountScopedAndAmbiguousNamesFail(t *testing.T) {
	state, accountValue := newFolderTestState(t)
	defer state.Close()
	counter := 0
	newID := func() (string, error) {
		counter++
		return fmt.Sprintf("fld_0198d704-6c00-7000-9000-%012d", counter), nil
	}
	_, err := state.Folders().Reconcile(context.Background(), accountValue.ID, []folder.RemoteObservation{
		{Name: "Duplicate", WireName: "Parent/Duplicate", Delimiter: "/", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionUnknown, StatusKnown: true},
		{Name: "Duplicate", WireName: "Other/Duplicate", Delimiter: "/", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionUnknown, StatusKnown: true},
	}, time.Now().UTC(), newID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Folders().Resolve(context.Background(), accountValue.ID, "Duplicate"); !errors.Is(err, folder.ErrAmbiguous) {
		t.Fatalf("resolve duplicate = %v", err)
	}
	value, err := state.Folders().Resolve(context.Background(), accountValue.ID, "Parent/Duplicate")
	if err != nil || value.WireName != "Parent/Duplicate" {
		t.Fatalf("resolve wire = %+v err=%v", value, err)
	}
	if _, err := state.Folders().Resolve(context.Background(), "acct_missing", value.ID); !errors.Is(err, folder.ErrNotFound) {
		t.Fatalf("cross-account resolve = %v", err)
	}
	values, more, err := state.Folders().List(context.Background(), accountValue.ID, 1, true)
	if err != nil || !more || len(values) != 1 {
		t.Fatalf("bounded list len=%d more=%v err=%v", len(values), more, err)
	}
}

func TestFolderRoleSelectionNeverTreatsMailboxNameAsSpecialUseEvidence(t *testing.T) {
	state, accountValue := newFolderTestState(t)
	defer state.Close()
	counter := 0
	newID := func() (string, error) {
		counter++
		return fmt.Sprintf("fld_0198d704-6c00-7000-9100-%012d", counter), nil
	}
	_, err := state.Folders().Reconcile(context.Background(), accountValue.ID, []folder.RemoteObservation{
		{Name: "Archive", WireName: "Archive", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 300, StatusKnown: true},
		{Name: "Archivio", WireName: "Archivio", Kind: folder.KindArchive, KindSource: "attribute", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 301, StatusKnown: true},
	}, time.Now().UTC(), newID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Folders().Resolve(context.Background(), accountValue.ID, "Archive"); !errors.Is(err, folder.ErrAmbiguous) {
		t.Fatalf("generic Archive selector should be ambiguous across name/role evidence: %v", err)
	}
	byRole, err := state.Folders().ResolveKind(context.Background(), accountValue.ID, folder.KindArchive)
	if err != nil || byRole.Name != "Archivio" || byRole.Kind != folder.KindArchive {
		t.Fatalf("Archive role selection = %+v err=%v", byRole, err)
	}
	if _, err := state.Folders().ResolveKind(context.Background(), accountValue.ID, folder.KindTrash); !errors.Is(err, folder.ErrNotFound) {
		t.Fatalf("missing Trash role = %v", err)
	}
}

func TestListSelectableIgnoresContainerFoldersBeforeApplyingBound(t *testing.T) {
	state, accountValue := newFolderTestState(t)
	defer state.Close()
	counter := 0
	newID := func() (string, error) {
		counter++
		return fmt.Sprintf("fld_0198d704-6c00-7000-a000-%012d", counter), nil
	}
	observations := make([]folder.RemoteObservation, 0, 14)
	for index := 0; index < 12; index++ {
		observations = append(observations, folder.RemoteObservation{
			Name: fmt.Sprintf("Container %02d", index), WireName: fmt.Sprintf("Container %02d", index),
			Kind: folder.KindOther, Selectable: false, Subscription: folder.SubscriptionUnknown,
		})
	}
	observations = append(observations,
		folder.RemoteObservation{Name: "INBOX", WireName: "INBOX", Kind: folder.KindInbox, KindSource: "inbox", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 100, StatusKnown: true},
		folder.RemoteObservation{Name: "Archive", WireName: "Archive", Kind: folder.KindArchive, KindSource: "attribute", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 101, StatusKnown: true},
	)
	if _, err := state.Folders().Reconcile(context.Background(), accountValue.ID, observations, time.Now().UTC(), newID); err != nil {
		t.Fatal(err)
	}
	values, more, err := state.Folders().ListSelectable(context.Background(), accountValue.ID, 10)
	if err != nil || more || len(values) != 2 {
		t.Fatalf("selectable list len=%d more=%v err=%v", len(values), more, err)
	}
	if values[0].Kind != folder.KindInbox || values[1].Kind != folder.KindArchive {
		t.Fatalf("selectable folders = %+v", values)
	}
}

func TestFolderAdministrationPreservesStableIdentityPolicyLocationsAndSubscription(t *testing.T) {
	state, accountID, inboxID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC)
	custom, err := state.Folders().CreateObserved(context.Background(), accountID, folder.RemoteObservation{
		Name: "Projects/Alpha", WireName: "Projects/Alpha", ParentName: "Projects", ParentWireName: "Projects",
		Delimiter: "/", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionUnknown,
		UIDValidity: 200, StatusKnown: true,
	}, now, func() (string, error) { return "fld_0198d704-6c00-7000-b000-000000000601", nil })
	if err != nil {
		t.Fatal(err)
	}
	if custom.SyncEnabled || custom.ID == "" {
		t.Fatalf("created custom folder = %+v", custom)
	}
	custom, err = state.Folders().SetSync(context.Background(), accountID, custom.ID, true, now.Add(time.Minute))
	if err != nil || !custom.SyncEnabled {
		t.Fatalf("enable custom folder = %+v err=%v", custom, err)
	}

	inboxRecord := mailRecordFixture(accountID, inboxID, 100, 41, "folder-admin@example.test")
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-b000-000000000602", AccountID: accountID, FolderID: inboxID,
		UIDValidity: 100, Bootstrap: true, LastCommittedUID: 41, LastScannedUID: 41, AttemptedAt: now.Add(2 * time.Minute),
		Records: []maildomain.IngestRecord{inboxRecord},
	}); err != nil {
		t.Fatal(err)
	}
	customRecord := mailRecordFixture(accountID, custom.ID, 200, 7, "folder-admin@example.test")
	// This fixture represents a provider copy of the same immutable message, so
	// corroborate the shared RFC Message-ID with identical content evidence.
	customRecord.ContentFingerprint = inboxRecord.ContentFingerprint
	customRecord.RawContentHash = inboxRecord.RawContentHash
	customRecord.RawStorageKey = inboxRecord.RawStorageKey
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-b000-000000000603", AccountID: accountID, FolderID: custom.ID,
		UIDValidity: 200, Bootstrap: true, LastCommittedUID: 7, LastScannedUID: 7, AttemptedAt: now.Add(3 * time.Minute),
		Records: []maildomain.IngestRecord{customRecord},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := state.Mail().Message(context.Background(), accountID, inboxRecord.MessageID)
	if err != nil || len(before.Locations) != 2 {
		t.Fatalf("logical message before rename = %+v err=%v", before, err)
	}
	threadsBefore, more, err := state.Threads().Latest(context.Background(), accountID, 10)
	if err != nil || more || len(threadsBefore) != 1 {
		t.Fatalf("threads before rename = %+v more=%v err=%v", threadsBefore, more, err)
	}
	checkpointBefore, err := state.Mail().Checkpoint(context.Background(), accountID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}

	renamed, err := state.Folders().ApplyObservation(context.Background(), accountID, custom.ID, folder.RemoteObservation{
		Name: "Projects/日本語", WireName: "Projects/&ZeVnLIqe-", OldName: custom.Name, OldWireName: custom.WireName,
		ParentName: "Projects", ParentWireName: "Projects", Delimiter: "/", Kind: folder.KindOther,
		Selectable: true, Subscription: folder.SubscriptionUnknown,
	}, now.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ID != custom.ID || renamed.Name != "Projects/日本語" || !renamed.SyncEnabled {
		t.Fatalf("renamed folder = %+v", renamed)
	}
	checkpointAfter, err := state.Mail().Checkpoint(context.Background(), accountID, custom.ID)
	if err != nil || checkpointAfter.ID != checkpointBefore.ID || checkpointAfter.LastCommittedUID != checkpointBefore.LastCommittedUID {
		t.Fatalf("checkpoint after rename = %+v err=%v", checkpointAfter, err)
	}
	customMessages, more, err := state.Mail().MessagesInFolder(context.Background(), accountID, custom.ID, 10)
	if err != nil || more || len(customMessages) != 1 || customMessages[0].ID != before.ID {
		t.Fatalf("renamed folder messages = %+v more=%v err=%v", customMessages, more, err)
	}

	if _, err := state.Folders().SetSubscription(context.Background(), accountID, custom.ID, folder.SubscriptionSubscribed, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	values, err := state.Folders().Reconcile(context.Background(), accountID, []folder.RemoteObservation{
		{Name: "INBOX", WireName: "INBOX", Delimiter: "/", Kind: folder.KindInbox, KindSource: "inbox", Selectable: true, Subscription: folder.SubscriptionUnknown, UIDValidity: 100, StatusKnown: true},
		{Name: renamed.Name, WireName: renamed.WireName, Delimiter: "/", ParentName: "Projects", ParentWireName: "Projects", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionUnknown, UIDValidity: 200, StatusKnown: true},
	}, now.Add(6*time.Minute), func() (string, error) { return "fld_unexpected", nil })
	if err != nil {
		t.Fatal(err)
	}
	if refreshed := foldersByName(values)[renamed.Name]; refreshed.ID != custom.ID || refreshed.Subscription != folder.SubscriptionSubscribed || !refreshed.SyncEnabled {
		t.Fatalf("subscription/policy after unknown LIST state = %+v", refreshed)
	}

	deleted, err := state.Folders().MarkDeleted(context.Background(), accountID, custom.ID, now.Add(7*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Present || !deleted.Stale || deleted.SyncEnabled {
		t.Fatalf("deleted folder = %+v", deleted)
	}
	after, err := state.Mail().Message(context.Background(), accountID, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	present, absent := 0, 0
	for _, location := range after.Locations {
		if location.Present {
			present++
		} else {
			absent++
		}
	}
	threadsAfter, more, err := state.Threads().Latest(context.Background(), accountID, 10)
	if err != nil || more || len(threadsAfter) != 1 || threadsAfter[0].ID != threadsBefore[0].ID {
		t.Fatalf("threads after folder deletion = %+v more=%v err=%v", threadsAfter, more, err)
	}
	if present != 1 || absent != 1 {
		t.Fatalf("logical message after folder deletion = %+v", after)
	}
}

func TestFolderAdministrationRejectsDuplicateObservedWireAndMissingMutationTarget(t *testing.T) {
	state, accountValue := newFolderTestState(t)
	defer state.Close()
	now := time.Date(2026, 8, 7, 1, 20, 0, 0, time.UTC)
	created, err := state.Folders().CreateObserved(context.Background(), accountValue.ID, folder.RemoteObservation{
		Name: "Receipts", WireName: "Receipts", Delimiter: "/", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionUnknown,
	}, now, func() (string, error) { return "fld_0198d704-6c00-7000-b000-000000000611", nil })
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.Folders().CreateObserved(context.Background(), accountValue.ID, folder.RemoteObservation{
		Name: "Receipts", WireName: "Receipts", Delimiter: "/", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionUnknown,
	}, now.Add(time.Minute), func() (string, error) { return "fld_0198d704-6c00-7000-b000-000000000612", nil })
	if !errors.Is(err, folder.ErrAmbiguous) {
		t.Fatalf("duplicate create = %v", err)
	}
	if _, err := state.Folders().MarkDeleted(context.Background(), accountValue.ID, "fld_missing", now); !errors.Is(err, folder.ErrNotFound) {
		t.Fatalf("missing delete = %v", err)
	}
	if _, err := state.Folders().SetSubscription(context.Background(), accountValue.ID, created.ID, folder.SubscriptionUnknown, now); err == nil {
		t.Fatal("expected unknown subscription update to fail")
	}
}

func TestFolderRenameRecoveryPreservesStableIdentityWhenProviderOmitsOldName(t *testing.T) {
	state, accountID, inboxID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 7, 2, 0, 0, 0, time.UTC)
	custom, err := state.Folders().CreateObserved(context.Background(), accountID, folder.RemoteObservation{
		Name: "Projects/Before", WireName: "Projects/Before", ParentName: "Projects", ParentWireName: "Projects",
		Delimiter: "/", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionSubscribed,
		UIDValidity: 500, StatusKnown: true,
	}, now, func() (string, error) { return "fld_0198d704-6c00-7000-b000-000000000621", nil })
	if err != nil {
		t.Fatal(err)
	}
	custom, err = state.Folders().SetSync(context.Background(), accountID, custom.ID, true, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	record := mailRecordFixture(accountID, custom.ID, 500, 9, "rename-recovery@example.test")
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_0198d704-6c00-7000-b000-000000000622", AccountID: accountID, FolderID: custom.ID,
		UIDValidity: 500, Bootstrap: true, LastCommittedUID: 9, LastScannedUID: 9, AttemptedAt: now.Add(2 * time.Minute),
		Records: []maildomain.IngestRecord{record},
	}); err != nil {
		t.Fatal(err)
	}
	recovery, err := state.Folders().BeginRenameRecovery(context.Background(), accountID, custom.ID, custom.Name, "Projects/After-日本語", now.Add(3*time.Minute), func() (string, error) {
		return "frr_0198d704-6c00-7000-b000-000000000623", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	observations := []folder.RemoteObservation{
		{Name: "INBOX", WireName: "INBOX", Delimiter: "/", Kind: folder.KindInbox, KindSource: "inbox", Selectable: true, Subscription: folder.SubscriptionUnknown, UIDValidity: 100, StatusKnown: true},
		{Name: "Projects/After-日本語", WireName: "Projects/After-&ZeVnLIqe-", ParentName: "Projects", ParentWireName: "Projects", Delimiter: "/", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionUnknown, Messages: 1, UIDValidity: 500, StatusKnown: true},
	}
	resolved, notApplied, err := state.Folders().ReconcilePendingRenames(context.Background(), accountID, observations, now.Add(4*time.Minute))
	if err != nil || resolved != 1 || notApplied != 0 {
		t.Fatalf("rename recovery result resolved=%d notApplied=%d err=%v", resolved, notApplied, err)
	}
	values, err := state.Folders().Reconcile(context.Background(), accountID, observations, now.Add(4*time.Minute), func() (string, error) { return "fld_unexpected", nil })
	if err != nil {
		t.Fatal(err)
	}
	renamed := foldersByName(values)["Projects/After-日本語"]
	if renamed.ID != custom.ID || !renamed.SyncEnabled || renamed.Subscription != folder.SubscriptionSubscribed || !renamed.Present || renamed.Stale {
		t.Fatalf("renamed stable folder = %+v", renamed)
	}
	checkpoint, err := state.Mail().Checkpoint(context.Background(), accountID, custom.ID)
	if err != nil || checkpoint.LastCommittedUID != 9 || checkpoint.UIDValidity != 500 {
		t.Fatalf("renamed checkpoint = %+v err=%v", checkpoint, err)
	}
	message, err := state.Mail().Message(context.Background(), accountID, record.MessageID)
	if err != nil || len(message.Locations) != 1 || message.Locations[0].FolderID != custom.ID || !message.Locations[0].Present {
		t.Fatalf("renamed message locations = %+v err=%v", message.Locations, err)
	}
	recovery, err = state.Folders().RenameRecovery(context.Background(), recovery.ID)
	if err != nil || recovery.Status != folder.RenameRecoveryResolved || recovery.NewName != "Projects/After-日本語" {
		t.Fatalf("resolved recovery = %+v err=%v", recovery, err)
	}

	retry, err := state.Folders().BeginRenameRecovery(context.Background(), accountID, custom.ID, renamed.Name, "Projects/NeverApplied", now.Add(5*time.Minute), func() (string, error) {
		return "frr_0198d704-6c00-7000-b000-000000000624", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, notApplied, err = state.Folders().ReconcilePendingRenames(context.Background(), accountID, observations, now.Add(6*time.Minute))
	if err != nil || resolved != 0 || notApplied != 1 {
		t.Fatalf("not-applied recovery result resolved=%d notApplied=%d err=%v", resolved, notApplied, err)
	}
	retry, err = state.Folders().RenameRecovery(context.Background(), retry.ID)
	if err != nil || retry.Status != folder.RenameRecoveryNotApplied {
		t.Fatalf("not-applied recovery = %+v err=%v", retry, err)
	}
	_ = inboxID
}

func TestFolderInventoryKeysetPaginationReachesPastOneThousand(t *testing.T) {
	state, accountValue := newFolderTestState(t)
	defer state.Close()
	now := time.Date(2026, 8, 8, 4, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := state.db.ExecContext(context.Background(), `
WITH RECURSIVE n(x) AS (
    SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x < 1005
)
INSERT INTO folders (
    folder_id, account_id, name, wire_name, delimiter, attributes_json,
    kind, kind_source, selectable, subscription, present, stale, sync_enabled,
    discovered_at, created_at, updated_at
)
SELECT printf('fld_scale_%04d', x), ?, printf('Scale/%04d', x), printf('Scale/%04d', x), '/', '[]',
       'other', '', 1, 'subscribed', 1, 0, 1, ?, ?, ?
FROM n`, accountValue.ID, now, now, now); err != nil {
		t.Fatal(err)
	}

	seen := map[string]struct{}{}
	cursor := ""
	pages := 0
	for {
		values, more, next, err := state.Folders().ListPage(context.Background(), accountValue.ID, 137, false, cursor)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, value := range values {
			if _, duplicate := seen[value.ID]; duplicate {
				t.Fatalf("folder %s repeated across keyset pages", value.ID)
			}
			seen[value.ID] = struct{}{}
		}
		if !more {
			if next != "" {
				t.Fatalf("terminal folder page returned cursor %q", next)
			}
			break
		}
		if next == "" || next == cursor {
			t.Fatalf("folder cursor did not advance: current=%q next=%q", cursor, next)
		}
		cursor = next
	}
	if len(seen) != 1005 || pages < 2 {
		t.Fatalf("paged folder inventory reached %d folders in %d pages", len(seen), pages)
	}

	seen = map[string]struct{}{}
	cursor = ""
	for {
		values, more, next, err := state.Folders().ListSyncEnabledPage(context.Background(), accountValue.ID, 113, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			seen[value.ID] = struct{}{}
		}
		if !more {
			break
		}
		if next == "" || next == cursor {
			t.Fatalf("enabled scheduling cursor did not advance: current=%q next=%q", cursor, next)
		}
		cursor = next
	}
	if len(seen) != 1005 {
		t.Fatalf("enabled folder scheduler reached %d folders; want 1005", len(seen))
	}

	planRows, err := state.db.QueryContext(context.Background(), `
EXPLAIN QUERY PLAN
SELECT folder_id
FROM folders
WHERE account_id = ? AND present = 1 AND stale = 0 AND selectable = 1
  AND sync_enabled = 1 AND folder_id > ?
ORDER BY folder_id
LIMIT 114`, accountValue.ID, "fld_scale_0500")
	if err != nil {
		t.Fatal(err)
	}
	plan := ""
	for planRows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := planRows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			planRows.Close()
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if err := planRows.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "folders_account_enabled_page_idx") {
		t.Fatalf("enabled folder keyset query plan did not use its pagination index:\n%s", plan)
	}
}

func newFolderTestState(t *testing.T) (*Store, account.Account) {
	t.Helper()
	paths, err := config.ResolveAppPaths(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 6, 3, 55, 0, 0, time.UTC)
	value, err := state.Accounts().Create(context.Background(), account.CreateRecord{
		AccountID:        "acct_0198d704-6c00-7000-8000-000000000501",
		CredentialID:     "cred_0198d704-6c00-7000-8000-000000000502",
		Name:             "Folder Test",
		Email:            "folder-test@icloud.com",
		IMAPUsername:     "folder-test",
		CredentialScheme: account.CredentialEnv,
		CredentialRef:    "env:FOLDER_TEST_PASSWORD",
		Now:              now,
	})
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	return state, value
}

func foldersByName(values []folder.Folder) map[string]folder.Folder {
	result := make(map[string]folder.Folder, len(values))
	for _, value := range values {
		result[value.Name] = value
	}
	return result
}
