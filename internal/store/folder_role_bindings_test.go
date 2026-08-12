package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
)

func TestFolderRoleBindingIsSeparateStableAndFailSafe(t *testing.T) {
	state, accountValue := newFolderTestState(t)
	defer state.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	counter := 0
	newID := func() (string, error) {
		counter++
		return fmt.Sprintf("fld_folderRole_%03d", counter), nil
	}
	initial := []folder.RemoteObservation{
		{Name: "INBOX", WireName: "INBOX", Kind: folder.KindInbox, KindSource: "inbox", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 100, StatusKnown: true},
		{Name: "Archive", WireName: "Archive", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 101, StatusKnown: true},
		{Name: "Sent", WireName: "Sent", Kind: folder.KindSent, KindSource: "fallback", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 102, StatusKnown: true},
		{Name: "Posta inviata", WireName: "Posta inviata", Kind: folder.KindSent, KindSource: "attribute", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 103, StatusKnown: true},
		{Name: "Cestino", WireName: "Cestino", Kind: folder.KindTrash, KindSource: "attribute", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 104, StatusKnown: true},
	}
	values, err := state.Folders().Reconcile(ctx, accountValue.ID, initial, base, newID)
	if err != nil {
		t.Fatal(err)
	}
	byName := foldersByName(values)
	ordinaryArchive := byName["Archive"]
	if ordinaryArchive.Kind != folder.KindOther || ordinaryArchive.KindSource != "" {
		t.Fatalf("ordinary Archive was reclassified: %+v", ordinaryArchive)
	}
	if _, err := state.FolderRoles().Resolve(ctx, accountValue.ID, folder.RoleArchive); !errors.Is(err, folder.ErrNotFound) {
		t.Fatalf("ordinary folder name became archive role without binding: %v", err)
	}
	sent, err := state.FolderRoles().Resolve(ctx, accountValue.ID, folder.RoleSent)
	if err != nil || sent.Folder.Name != "Posta inviata" || sent.Source != folder.RoleResolutionProvider {
		t.Fatalf("provider Sent must beat fallback: %+v err=%v", sent, err)
	}

	binding, err := state.FolderRoles().Set(ctx, accountValue.ID, folder.RoleArchive, ordinaryArchive.ID, base.Add(time.Minute))
	if err != nil || binding.FolderID != ordinaryArchive.ID {
		t.Fatalf("set archive binding=%+v err=%v", binding, err)
	}
	resolved, err := state.FolderRoles().Resolve(ctx, accountValue.ID, folder.RoleArchive)
	if err != nil || resolved.Source != folder.RoleResolutionBinding || resolved.Folder.ID != ordinaryArchive.ID || resolved.Folder.Kind != folder.KindOther {
		t.Fatalf("bound Archive resolution=%+v err=%v", resolved, err)
	}
	if _, err := state.FolderRoles().Set(ctx, accountValue.ID, folder.RoleTrash, ordinaryArchive.ID, base.Add(2*time.Minute)); !errors.Is(err, folder.ErrRoleAlreadyBound) {
		t.Fatalf("same folder bound to conflicting role: %v", err)
	}

	refreshed := []folder.RemoteObservation{
		{Name: "INBOX", WireName: "INBOX", Kind: folder.KindInbox, KindSource: "inbox", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 100, StatusKnown: true},
		{Name: "Filed", WireName: "Filed", OldWireName: "Archive", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 101, StatusKnown: true},
		{Name: "Archivio", WireName: "Archivio", Kind: folder.KindArchive, KindSource: "attribute", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 105, StatusKnown: true},
		{Name: "Sent", WireName: "Sent", Kind: folder.KindSent, KindSource: "fallback", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 102, StatusKnown: true},
		{Name: "Posta inviata", WireName: "Posta inviata", Kind: folder.KindSent, KindSource: "attribute", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 103, StatusKnown: true},
		{Name: "Cestino", WireName: "Cestino", Kind: folder.KindTrash, KindSource: "attribute", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 104, StatusKnown: true},
	}
	if _, err := state.Folders().Reconcile(ctx, accountValue.ID, refreshed, base.Add(3*time.Minute), newID); err != nil {
		t.Fatal(err)
	}
	resolved, err = state.FolderRoles().Resolve(ctx, accountValue.ID, folder.RoleArchive)
	if err != nil || resolved.Folder.ID != ordinaryArchive.ID || resolved.Folder.Name != "Filed" || resolved.Folder.Kind != folder.KindOther {
		t.Fatalf("binding did not survive stable-ID rename: %+v err=%v", resolved, err)
	}
	cleared, err := state.FolderRoles().Clear(ctx, accountValue.ID, folder.RoleArchive)
	if err != nil || cleared.FolderID != ordinaryArchive.ID {
		t.Fatalf("clear binding=%+v err=%v", cleared, err)
	}
	resolved, err = state.FolderRoles().Resolve(ctx, accountValue.ID, folder.RoleArchive)
	if err != nil || resolved.Source != folder.RoleResolutionProvider || resolved.Folder.Name != "Archivio" {
		t.Fatalf("provider Archive did not resume after clear: %+v err=%v", resolved, err)
	}
	if _, err := state.FolderRoles().Set(ctx, accountValue.ID, folder.RoleArchive, ordinaryArchive.ID, base.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Folders().MarkDeleted(ctx, accountValue.ID, ordinaryArchive.ID, base.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := state.FolderRoles().Resolve(ctx, accountValue.ID, folder.RoleArchive); !errors.Is(err, folder.ErrRoleBindingInvalid) {
		t.Fatalf("stale binding silently fell back to provider role: %v", err)
	}

	second, err := state.Accounts().Create(ctx, account.CreateRecord{
		AccountID: "acct_folderRole_second", CredentialID: "cred_folderRole_second", Name: "Folder role second",
		Email: "folderRole-second@icloud.com", IMAPUsername: "folderRole-second", CredentialScheme: account.CredentialEnv,
		CredentialRef: "env:FOLDER_ROLE_SECOND", Now: base.Add(6 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.FolderRoles().Set(ctx, second.ID, folder.RoleArchive, ordinaryArchive.ID, base.Add(7*time.Minute)); !errors.Is(err, folder.ErrNotFound) {
		t.Fatalf("cross-account binding accepted: %v", err)
	}
	if _, err := state.FolderRoles().Resolve(ctx, second.ID, folder.RoleArchive); !errors.Is(err, folder.ErrNotFound) {
		t.Fatalf("cross-account role leaked: %v", err)
	}
}

func TestSentFallbackIsLastPrecedence(t *testing.T) {
	state, accountValue := newFolderTestState(t)
	defer state.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 10, 5, 30, 0, 0, time.UTC)
	counter := 0
	newID := func() (string, error) {
		counter++
		return fmt.Sprintf("fld_folderRole_sent_%03d", counter), nil
	}
	values, err := state.Folders().Reconcile(ctx, accountValue.ID, []folder.RemoteObservation{
		{Name: "Sent", WireName: "Sent", Kind: folder.KindSent, KindSource: "fallback", Selectable: true, Subscription: folder.SubscriptionUnknown, UIDValidity: 200, StatusKnown: true},
	}, base, newID)
	if err != nil || len(values) != 1 {
		t.Fatalf("fallback folders=%+v err=%v", values, err)
	}
	resolved, err := state.FolderRoles().Resolve(ctx, accountValue.ID, folder.RoleSent)
	if err != nil || resolved.Source != folder.RoleResolutionFallback || resolved.Folder.ID != values[0].ID {
		t.Fatalf("Sent fallback resolution=%+v err=%v", resolved, err)
	}
}
