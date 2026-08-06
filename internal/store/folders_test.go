package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/folder"
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
