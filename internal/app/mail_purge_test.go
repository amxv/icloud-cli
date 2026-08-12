package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestPurgePagesExactLocationsAndSkipsAlreadyDeleted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-purge-secret")
	commands, _ := newTestAccountCommands()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	commands.now = func() time.Time { return now }
	addSendAccount(t, commands, home)
	fixture := seedLocationMutationFixture(t, home, []locationSeed{{folder.KindOther, 1, `$Fixture`}})
	custom := fixture.folders[folder.KindOther]
	inbox := fixture.folders[folder.KindInbox]

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	base := now.Add(time.Minute)
	sharedHash := strings.Repeat("a", 64)
	duplicate := maildomain.IngestRecord{
		MessageID: "msg_purge_same_folder_duplicate", LocationID: "loc_purge_same_folder_duplicate",
		AccountID: accountValue.ID, FolderID: custom.ID, UIDValidity: custom.UIDValidity, UID: 2,
		Flags: []string{`$Fixture`}, InternalDate: base, RFC822Size: 512,
		RawContentHash: sharedHash, RawStorageKey: "raw/sha256/aa/" + sharedHash + ".eml", RawByteSize: 512,
		Parsed: maildomain.ParsedMessage{
			RFCMessageID: "location-fixture@example.test", From: maildomain.Address{Email: "sender@example.test"},
			To: []maildomain.Address{{Email: accountValue.Email}}, Subject: fixture.subject,
			Snippet: "location mutation fixture body", HeaderDate: &base,
		},
	}
	later := threadMutationThreadRecord(accountValue.ID, custom.ID, custom.UIDValidity, 3, "msg_purge_later", "purge-later@example.test", "Purge later", "later@example.test", accountValue.Email, base.Add(time.Minute), 0)
	alreadyDeleted := threadMutationThreadRecord(accountValue.ID, custom.ID, custom.UIDValidity, 4, "msg_purge_deleted", "purge-deleted@example.test", "Purge deleted", "deleted@example.test", accountValue.Email, base.Add(2*time.Minute), 0)
	alreadyDeleted.Flags = []string{`\Deleted`, `$Fixture`}
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_purge_custom", AccountID: accountValue.ID, FolderID: custom.ID,
		UIDValidity: custom.UIDValidity, Bootstrap: true, LastCommittedUID: 4, LastScannedUID: 4,
		AttemptedAt: base.Add(2 * time.Minute), Records: []maildomain.IngestRecord{duplicate, later, alreadyDeleted},
	}); err != nil {
		state.Close()
		t.Fatal(err)
	}
	inboxCopy := duplicate
	inboxCopy.MessageID = "msg_purge_inbox_copy"
	inboxCopy.LocationID = "loc_purge_inbox_copy"
	inboxCopy.FolderID = inbox.ID
	inboxCopy.UIDValidity = inbox.UIDValidity
	inboxCopy.UID = 10
	inboxCopy.InternalDate = base.Add(3 * time.Minute)
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_purge_inbox", AccountID: accountValue.ID, FolderID: inbox.ID,
		UIDValidity: inbox.UIDValidity, Bootstrap: true, LastCommittedUID: 10, LastScannedUID: 10,
		AttemptedAt: base.Add(3 * time.Minute), Records: []maildomain.IngestRecord{inboxCopy},
	}); err != nil {
		state.Close()
		t.Fatal(err)
	}
	state.Close()

	request := maildomain.PurgeCreateRequest{ScopeKind: maildomain.PurgeScopeFolder, FolderSelector: custom.ID, Limit: 2, TTL: 5 * time.Minute}
	_, _, first, schemaVersion, err := commands.MailPurgeCreate(context.Background(), home, "", request)
	if err != nil || schemaVersion != store.SchemaVersion || first.ItemCount != 2 || !first.More || len(first.Items) != 2 || first.Items[0].UID != 1 || first.Items[1].UID != 2 || first.Items[0].MessageID != first.Items[1].MessageID || first.Items[0].LocationID == first.Items[1].LocationID {
		t.Fatalf("first purge=%+v schema=%d err=%v", first, schemaVersion, err)
	}
	reopened, _ := newTestAccountCommands()
	reopened.now = func() time.Time { return now.Add(time.Minute) }
	_, reopenedFolder, reopenedPurge, _, err := reopened.MailPurge(context.Background(), home, "", first.ID, request)
	if err != nil || reopenedFolder.ID != custom.ID || reopenedPurge.RequestHash != first.RequestHash || len(reopenedPurge.Items) != 2 {
		t.Fatalf("reopened folder=%+v purge=%+v err=%v", reopenedFolder, reopenedPurge, err)
	}

	results := make([]transport.LocationMutationResult, 0, 2)
	for _, item := range first.Items {
		results = append(results, transport.LocationMutationResult{
			Operation: transport.LocationMutationDelete, ProviderMethod: "uid_expunge",
			SourceMailbox: custom.Name, SourceUIDValidity: item.UIDValidity, SourceUID: item.UID,
			SourceFlags: []string{`$Fixture`}, SourceModSequence: item.ModSequence,
			SourcePresent: false, ProviderMutationObserved: true,
		})
	}
	client := &fakeLocationClient{results: results}
	commands.locations = client
	for _, item := range first.Items {
		snapshot := item.SelectionItem(custom.ID)
		_, _, _, mutation, reused, _, err := commands.MailMutateLocation(context.Background(), home, "", maildomain.LocationMutationRequest{
			Action: maildomain.LocationActionDelete, MessageID: item.MessageID, FromFolder: custom.ID,
			IdempotencyKey:     "purge:test:" + item.LocationID,
			ExpectedLocationID: snapshot.LocationID, ExpectedUIDValidity: snapshot.UIDValidity,
			ExpectedUID: snapshot.UID, ExpectedModSequence: snapshot.ModSequence,
		})
		if err != nil || reused || mutation.Status != maildomain.LocationMutationConfirmed {
			t.Fatalf("delete item=%+v mutation=%+v reused=%t err=%v", item, mutation, reused, err)
		}
	}
	if client.calls != 2 || client.requests[0].SourceUID != 1 || client.requests[1].SourceUID != 2 {
		t.Fatalf("exact provider calls=%d requests=%+v", client.calls, client.requests)
	}

	_, _, second, _, err := commands.MailPurgeCreate(context.Background(), home, "", request)
	if err != nil || second.ItemCount != 1 || second.More || len(second.Items) != 1 || second.Items[0].UID != 3 {
		t.Fatalf("second purge=%+v err=%v", second, err)
	}
	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	message, err := state.Mail().Message(context.Background(), accountValue.ID, first.Items[0].MessageID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	presentInbox := false
	for _, location := range message.Locations {
		if location.FolderID == inbox.ID && location.Present {
			presentInbox = true
		}
		if location.FolderID == custom.ID && location.UID == 4 && !location.Flags.Deleted {
			state.Close()
			t.Fatalf("already-deleted fixture lost its deleted evidence: %+v", location)
		}
	}
	state.Close()
	if !presentInbox {
		t.Fatalf("logical message lost surviving Inbox provider copy: %+v", message.Locations)
	}

	now = first.ExpiresAt.Add(time.Nanosecond)
	_, _, _, _, err = commands.MailPurge(context.Background(), home, "", first.ID, request)
	if normalized := cli.NormalizeError(err); normalized == nil || normalized.Code != cli.ErrorConflict || !strings.Contains(normalized.Message, "expired") {
		t.Fatalf("expired purge err=%v normalized=%+v", err, normalized)
	}
}

func TestPurgeUsesSemanticBindingsAndCustomFolderFailsClosed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-purge-role-secret")
	commands, _ := newTestAccountCommands()
	commands.now = func() time.Time { return time.Date(2026, 8, 10, 8, 30, 0, 0, time.UTC) }
	addSendAccount(t, commands, home)
	fixture := seedLocationMutationFixture(t, home, []locationSeed{{folder.KindOther, 1, `$Fixture`}})
	custom := fixture.folders[folder.KindOther]

	if _, binding, _, _, err := commands.FolderRoleSet(context.Background(), home, "", string(folder.RoleTrash), custom.ID); err != nil || binding.FolderID != custom.ID {
		t.Fatalf("trash binding=%+v err=%v", binding, err)
	}
	_, resolvedTrash, trashPurge, _, err := commands.MailPurgeCreate(context.Background(), home, "", maildomain.PurgeCreateRequest{ScopeKind: maildomain.PurgeScopeTrash, Limit: 10})
	if err != nil || resolvedTrash.ID != custom.ID || trashPurge.FolderID != custom.ID || trashPurge.ScopeKind != maildomain.PurgeScopeTrash {
		t.Fatalf("trash folder=%+v purge=%+v err=%v", resolvedTrash, trashPurge, err)
	}
	_, _, _, _, err = commands.MailPurgeCreate(context.Background(), home, "", maildomain.PurgeCreateRequest{ScopeKind: maildomain.PurgeScopeFolder, FolderSelector: custom.ID, Limit: 10})
	if normalized := cli.NormalizeError(err); normalized == nil || normalized.Code != cli.ErrorSafety {
		t.Fatalf("custom form accepted bound Trash folder err=%v normalized=%+v", err, normalized)
	}
	if _, _, _, err := commands.FolderRoleClear(context.Background(), home, "", string(folder.RoleTrash)); err != nil {
		t.Fatal(err)
	}
	if _, binding, _, _, err := commands.FolderRoleSet(context.Background(), home, "", string(folder.RoleJunk), custom.ID); err != nil || binding.FolderID != custom.ID {
		t.Fatalf("junk binding=%+v err=%v", binding, err)
	}
	_, resolvedJunk, junkPurge, _, err := commands.MailPurgeCreate(context.Background(), home, "", maildomain.PurgeCreateRequest{ScopeKind: maildomain.PurgeScopeJunk, Limit: 10})
	if err != nil || resolvedJunk.ID != custom.ID || junkPurge.FolderID != custom.ID || junkPurge.ScopeKind != maildomain.PurgeScopeJunk {
		t.Fatalf("junk folder=%+v purge=%+v err=%v", resolvedJunk, junkPurge, err)
	}
	if _, _, _, _, err := commands.MailPurgeCreate(context.Background(), home, "", maildomain.PurgeCreateRequest{ScopeKind: maildomain.PurgeScopeFolder, FolderSelector: fixture.folders[folder.KindInbox].ID, Limit: 10}); cli.NormalizeError(err) == nil || cli.NormalizeError(err).Code != cli.ErrorSafety {
		t.Fatalf("custom form accepted Inbox: %v", err)
	}
}

func TestPurgeReportsUnresolvedExactDeletesAsBlocked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-purge-unresolved-secret")
	commands, _ := newTestAccountCommands()
	now := time.Date(2026, 8, 10, 8, 45, 0, 0, time.UTC)
	commands.now = func() time.Time { return now }
	addSendAccount(t, commands, home)
	fixture := seedLocationMutationFixture(t, home, []locationSeed{{folder.KindOther, 1, `$Fixture`}})
	custom := fixture.folders[folder.KindOther]
	location := fixture.locations[folder.KindOther]

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	mutation, reused, err := state.LocationMutations().Begin(context.Background(), maildomain.BeginLocationMutation{
		MutationID: "mut_purge_unresolved", AccountID: accountValue.ID, MessageID: fixture.messageID,
		Action: maildomain.LocationActionDelete, SourceLocationID: location.ID, SourceFolderID: custom.ID,
		RequestHash: strings.Repeat("c", 64), IdempotencyKey: "purge-unresolved", Now: now,
	})
	if err != nil || reused {
		state.Close()
		t.Fatalf("begin mutation=%+v reused=%t err=%v", mutation, reused, err)
	}
	mutation, err = state.LocationMutations().Apply(context.Background(), maildomain.ApplyLocationMutation{
		MutationID: mutation.ID, ProviderMethod: "uid_expunge",
		SourceFlags: location.Flags, SourceModSequence: location.ModSequence, SourcePresent: true,
		Status: maildomain.LocationMutationUnresolved, ReconciliationRequired: true,
		ErrorCode: "synthetic_disconnect_after_provider_observation", Now: now.Add(time.Second),
	})
	state.Close()
	if err != nil || mutation.Status != maildomain.LocationMutationUnresolved || !mutation.ReconciliationRequired {
		t.Fatalf("unresolved mutation=%+v err=%v", mutation, err)
	}

	_, resolved, purge, schemaVersion, err := commands.MailPurgeCreate(context.Background(), home, "", maildomain.PurgeCreateRequest{
		ScopeKind: maildomain.PurgeScopeFolder, FolderSelector: custom.ID, Limit: 10,
	})
	if err != nil || schemaVersion != store.SchemaVersion || resolved.ID != custom.ID || purge.ItemCount != 0 || purge.More || purge.BlockedUnresolved != 1 || purge.ID != "" {
		t.Fatalf("blocked purge folder=%+v purge=%+v schema=%d err=%v", resolved, purge, schemaVersion, err)
	}
}
