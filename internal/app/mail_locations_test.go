package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestMailLocationMoveDryRunConfirmedPersistenceAndIdempotentReplay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-location-secret")
	commands, _ := newTestAccountCommands()
	client := &fakeLocationClient{}
	commands.locations = client
	addSendAccount(t, commands, home)
	fixture := seedLocationMutationFixture(t, home, []locationSeed{{folder.KindInbox, 1, `\Flagged`}})
	client.results = []transport.LocationMutationResult{{
		Operation: transport.LocationMutationMove, ProviderMethod: "move",
		SourceMailbox:     fixture.folders[folder.KindInbox].WireName,
		SourceUIDValidity: fixture.locations[folder.KindInbox].UIDValidity,
		SourceUID:         fixture.locations[folder.KindInbox].UID,
		SourceFlags:       []string{`\Flagged`, `$Live`}, SourceModSequence: 31, SourcePresent: false,
		DestinationMailbox:     fixture.folders[folder.KindOther].WireName,
		DestinationUIDValidity: fixture.folders[folder.KindOther].UIDValidity,
		DestinationUID:         8, DestinationFlags: []string{`\Flagged`, `$Live`}, DestinationModSequence: 4,
		DestinationKnown: true, ProviderMutationObserved: true,
	}}
	args := []string{
		"--home", home, "mail", "move", fixture.messageID,
		"--from", "inbox", "--to", fixture.folders[folder.KindOther].ID,
		"--idempotency-key", "location-move-01",
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", append(append([]string(nil), args...), "--dry-run")...)
	if code != 0 || stderr != "" || client.calls != 0 {
		t.Fatalf("move dry-run code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
	if !compactContainsAll(stdout,
		"mail-location-preview\n"+fixture.messageID+" action=move",
		"operation=move", "source_location="+fixture.locations[folder.KindInbox].ID,
		"source_folder="+fixture.folders[folder.KindInbox].ID,
		"destination_folder="+fixture.folders[folder.KindOther].ID,
		"imap_contacted=false", "intent_created=false", "logical_message_deleted=false",
	) {
		t.Fatalf("move dry-run = %q", stdout)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	mutations, _, err := state.LocationMutations().List(context.Background(), accountValue.ID, 10)
	if err != nil || len(mutations) != 0 {
		t.Fatalf("dry-run mutations=%+v err=%v", mutations, err)
	}
	_ = state.Close()

	code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "requires --yes") || client.calls != 0 {
		t.Fatalf("move safety code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", append(append([]string(nil), args...), "--yes")...)
	if code != 0 || stderr != "" || client.calls != 1 {
		t.Fatalf("move code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
	if !compactContainsAll(stdout,
		"mail-location\nmut_", "action=move", "message_id="+fixture.messageID,
		"status=confirmed", "provider_method=move", "source_present=false",
		"mapping_known=true", "reconciliation_required=false", "reused=false", "next=none",
	) {
		t.Fatalf("move outcome = %q", stdout)
	}
	request := client.requests[0]
	if request.Operation != transport.LocationMutationMove ||
		request.SourceMailbox != fixture.folders[folder.KindInbox].WireName ||
		request.DestinationMailbox != fixture.folders[folder.KindOther].WireName ||
		request.ExpectedSourceUIDValidity != fixture.locations[folder.KindInbox].UIDValidity ||
		request.SourceUID != fixture.locations[folder.KindInbox].UID {
		t.Fatalf("provider move request = %+v", request)
	}
	for _, forbidden := range []string{"synthetic-location-secret", fixture.subject, "sender@example.test"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("move outcome exposed %q: %q", forbidden, stdout)
		}
	}

	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	message, err := state.Mail().Message(context.Background(), accountValue.ID, fixture.messageID)
	if err != nil {
		t.Fatal(err)
	}
	threadAfter, err := state.Threads().Show(context.Background(), accountValue.ID, fixture.messageID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	mutations, _, err = state.LocationMutations().List(context.Background(), accountValue.ID, 10)
	if err != nil || len(mutations) != 1 {
		t.Fatalf("move mutations=%+v err=%v", mutations, err)
	}
	_ = state.Close()
	if threadAfter.Thread.ID != fixture.threadID || len(message.Locations) != 2 || !message.Flagged {
		t.Fatalf("move message/thread=%+v thread=%+v", message, threadAfter.Thread)
	}
	var source, destination maildomain.Location
	for _, location := range message.Locations {
		if location.ID == fixture.locations[folder.KindInbox].ID {
			source = location
		} else if location.FolderID == fixture.folders[folder.KindOther].ID {
			destination = location
		}
	}
	if source.Present || !destination.Present || destination.UID != 8 || destination.ModSequence != 4 ||
		destination.RawContentHash != source.RawContentHash {
		t.Fatalf("move source=%+v destination=%+v", source, destination)
	}

	code, repeatOut, repeatErr := runWithAccountCommands(t, commands, "", append(append([]string(nil), args...), "--yes")...)
	if code != 0 || repeatErr != "" || client.calls != 1 || !strings.Contains(repeatOut, "reused=true") {
		t.Fatalf("move replay code=%d stdout=%q stderr=%q calls=%d", code, repeatOut, repeatErr, client.calls)
	}
	code, statusOut, statusErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "status", "--mutations")
	if code != 0 || statusErr != "" || !compactContainsAll(statusOut, "mutation\n"+mutations[0].ID+" action=move", "status=confirmed", "mutation-status") {
		t.Fatalf("mutation status code=%d stdout=%q stderr=%q", code, statusOut, statusErr)
	}
}

func TestMailArchiveNeverTouchesSentAndAmbiguityRequiresFrom(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-location-secret")
	commands, _ := newTestAccountCommands()
	client := &fakeLocationClient{}
	commands.locations = client
	addSendAccount(t, commands, home)
	fixture := seedLocationMutationFixture(t, home, []locationSeed{
		{folder.KindInbox, 1, `\Seen`},
		{folder.KindSent, 2, `\Seen`},
	})
	client.results = []transport.LocationMutationResult{{
		Operation: transport.LocationMutationMove, ProviderMethod: "move",
		SourceFlags: []string{`\Seen`}, SourcePresent: false,
		DestinationUIDValidity: fixture.folders[folder.KindArchive].UIDValidity,
		DestinationUID:         7, DestinationFlags: []string{`\Seen`},
		DestinationKnown: true, ProviderMutationObserved: true,
	}}
	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "archive", fixture.messageID,
		"--idempotency-key", "archive-not-sent-01", "--yes")
	if code != 0 || stderr != "" || client.calls != 1 {
		t.Fatalf("archive code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
	if client.requests[0].SourceMailbox != fixture.folders[folder.KindInbox].WireName ||
		client.requests[0].DestinationMailbox != fixture.folders[folder.KindArchive].WireName {
		t.Fatalf("archive provider request = %+v", client.requests[0])
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	message, err := state.Mail().Message(context.Background(), accountValue.ID, fixture.messageID)
	if err != nil {
		t.Fatal(err)
	}
	_ = state.Close()
	var sentPresent, inboxPresent, archivePresent bool
	for _, location := range message.Locations {
		switch location.FolderKind {
		case string(folder.KindSent):
			sentPresent = location.Present
		case string(folder.KindInbox):
			inboxPresent = location.Present
		case string(folder.KindArchive):
			archivePresent = location.Present
		}
	}
	if !sentPresent || inboxPresent || !archivePresent {
		t.Fatalf("archive locations = %+v", message.Locations)
	}

	ambiguousHome := t.TempDir()
	addSendAccount(t, commands, ambiguousHome)
	ambiguous := seedLocationMutationFixture(t, ambiguousHome, []locationSeed{
		{folder.KindInbox, 1, `\Seen`},
		{folder.KindOther, 2, `\Seen`},
	})
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", ambiguousHome, "mail", "archive", ambiguous.messageID, "--dry-run")
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "multiple eligible source locations") || client.calls != 1 {
		t.Fatalf("ambiguous archive code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
}

func TestMailLocationPartialCopyEvidenceAndPermanentDeleteKeepLogicalMessage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-location-secret")
	commands, _ := newTestAccountCommands()
	client := &fakeLocationClient{}
	commands.locations = client
	addSendAccount(t, commands, home)
	fixture := seedLocationMutationFixture(t, home, []locationSeed{
		{folder.KindInbox, 1, `\Seen`},
		{folder.KindArchive, 2, `\Seen`},
	})
	client.results = []transport.LocationMutationResult{
		{
			Operation: transport.LocationMutationMove, ProviderMethod: "copy_only",
			SourceFlags: []string{`\Seen`}, SourcePresent: true,
			DestinationUIDValidity: fixture.folders[folder.KindOther].UIDValidity,
			DestinationUID:         13, DestinationFlags: []string{`\Seen`},
			DestinationKnown: true, ReconciliationRequired: true, ProviderMutationObserved: true,
		},
		{
			Operation: transport.LocationMutationDelete, ProviderMethod: "uid_expunge",
			SourceFlags: []string{`\Seen`}, SourcePresent: false, ProviderMutationObserved: true,
		},
	}
	client.errors = []error{
		transport.NewFailure(transport.StageIMAPExpunge, transport.FailureUnavailable, false, errors.New("safe exact source removal failed")),
		nil,
	}
	moveArgs := []string{
		"--home", home, "mail", "move", fixture.messageID,
		"--from", "inbox", "--to", fixture.folders[folder.KindOther].ID,
		"--idempotency-key", "partial-move-01", "--yes",
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", moveArgs...)
	if code != cli.ExitUnavailable || stdout != "" || !strings.Contains(stderr, "provider copy may exist") || client.calls != 1 {
		t.Fatalf("partial move code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	mutations, _, err := state.LocationMutations().List(context.Background(), accountValue.ID, 10)
	if err != nil || len(mutations) != 1 || mutations[0].Status != maildomain.LocationMutationUnresolved ||
		!mutations[0].SourcePresent || !mutations[0].MappingKnown || !mutations[0].ReconciliationRequired {
		t.Fatalf("partial mutation=%+v err=%v", mutations, err)
	}
	message, err := state.Mail().Message(context.Background(), accountValue.ID, fixture.messageID)
	if err != nil {
		t.Fatal(err)
	}
	_ = state.Close()
	if len(message.Locations) != 3 {
		t.Fatalf("partial move locations = %+v", message.Locations)
	}
	code, replayOut, replayErr := runWithAccountCommands(t, commands, "", moveArgs...)
	if code != 0 || replayErr != "" || client.calls != 1 || !strings.Contains(replayOut, "status=unresolved") || !strings.Contains(replayOut, "reused=true") {
		t.Fatalf("partial replay code=%d stdout=%q stderr=%q calls=%d", code, replayOut, replayErr, client.calls)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "delete", fixture.messageID,
		"--from", "archive", "--idempotency-key", "delete-one-location-01", "--yes")
	if code != 0 || stderr != "" || client.calls != 2 || !strings.Contains(stdout, "action=delete") || !strings.Contains(stdout, "status=confirmed") {
		t.Fatalf("delete code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	message, err = state.Mail().Message(context.Background(), accountValue.ID, fixture.messageID)
	if err != nil {
		t.Fatalf("logical message was deleted: %v", err)
	}
	thread, threadErr := state.Threads().Show(context.Background(), accountValue.ID, fixture.messageID, "", 20)
	_ = state.Close()
	if threadErr != nil || thread.Thread.ID != fixture.threadID {
		t.Fatalf("logical thread lost after delete: %+v err=%v", thread.Thread, threadErr)
	}
	present := 0
	for _, location := range message.Locations {
		if location.Present {
			present++
		}
	}
	if present != 2 {
		t.Fatalf("remaining locations after one delete = %+v", message.Locations)
	}
}

func TestMailLocationConvenienceSafetyAndProviderConflict(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-location-secret")
	commands, _ := newTestAccountCommands()
	client := &fakeLocationClient{errors: []error{&transport.UIDValidityError{Expected: 100, Observed: 999}}}
	commands.locations = client
	addSendAccount(t, commands, home)
	fixture := seedLocationMutationFixture(t, home, []locationSeed{{folder.KindTrash, 1, `\Seen`}})
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "restore", fixture.messageID, "--dry-run")
	if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, "restore requires a deliberate --to") || client.calls != 0 {
		t.Fatalf("restore missing destination code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "delete", fixture.messageID, "--from", "trash", "--yes")
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "source mailbox identity changed") || client.calls != 1 {
		t.Fatalf("UIDVALIDITY conflict code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	message, err := state.Mail().Message(context.Background(), accountValue.ID, fixture.messageID)
	mutations, _, listErr := state.LocationMutations().List(context.Background(), accountValue.ID, 10)
	_ = state.Close()
	if err != nil || listErr != nil || len(mutations) != 1 || mutations[0].Status != maildomain.LocationMutationFailed ||
		!message.Locations[0].Present {
		t.Fatalf("conflict persisted message=%+v mutations=%+v err=%v list=%v", message, mutations, err, listErr)
	}
}

func TestMailLocationShortcutsRequireProviderRolesAndExplicitSourceCannotBypassRole(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-location-secret")
	commands, _ := newTestAccountCommands()
	client := &fakeLocationClient{}
	commands.locations = client
	addSendAccount(t, commands, home)
	fixture := seedLocationMutationFixture(t, home, []locationSeed{
		{folder.KindInbox, 1, `\Seen`},
		{folder.KindSent, 2, `\Seen`},
	})

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`
UPDATE folders
SET name = 'Archive', wire_name = 'Archive', kind = 'other', kind_source = ''
WHERE folder_id = ?`, fixture.folders[folder.KindArchive].ID)
		return err
	}); err != nil {
		_ = state.Close()
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "archive", fixture.messageID, "--from", "inbox", "--dry-run")
	if code != cli.ExitNotFound || stdout != "" || !strings.Contains(stderr, "folder not found") || client.calls != 0 {
		t.Fatalf("ordinary Archive shortcut code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "unarchive", fixture.messageID, "--from", "sent", "--dry-run")
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "requires a current Archive source") || client.calls != 0 {
		t.Fatalf("explicit unarchive source code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "restore", fixture.messageID, "--from", "inbox", "--to", "sent", "--dry-run")
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "requires a current Trash source") || client.calls != 0 {
		t.Fatalf("explicit restore source code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
}

type fakeLocationClient struct {
	calls    int
	requests []transport.LocationMutationRequest
	auth     []transport.AuthRequest
	results  []transport.LocationMutationResult
	errors   []error
}

func (client *fakeLocationClient) MutateLocation(_ context.Context, auth transport.AuthRequest, request transport.LocationMutationRequest) (transport.LocationMutationResult, error) {
	index := client.calls
	client.calls++
	client.auth = append(client.auth, auth)
	client.requests = append(client.requests, request)
	var result transport.LocationMutationResult
	if index < len(client.results) {
		result = client.results[index]
	}
	var err error
	if index < len(client.errors) {
		err = client.errors[index]
	}
	return result, err
}

type locationSeed struct {
	kind folder.Kind
	uid  uint32
	flag string
}

type locationMutationFixture struct {
	messageID string
	threadID  string
	subject   string
	folders   map[folder.Kind]folder.Folder
	locations map[folder.Kind]maildomain.Location
}

func seedLocationMutationFixture(t *testing.T, home string, seeds []locationSeed) locationMutationFixture {
	t.Helper()
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 7, 5, 0, 0, 0, time.UTC)
	observations := []folder.RemoteObservation{
		{Name: "INBOX", WireName: "INBOX", Kind: folder.KindInbox, KindSource: "inbox", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 100, StatusKnown: true},
		{Name: "Sent Messages", WireName: "Sent Messages", Kind: folder.KindSent, KindSource: "attribute", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 200, StatusKnown: true},
		{Name: "Archive Localized", WireName: "Archive Localized", Kind: folder.KindArchive, KindSource: "attribute", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 300, StatusKnown: true},
		{Name: "Trash Localized", WireName: "Trash Localized", Kind: folder.KindTrash, KindSource: "attribute", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 400, StatusKnown: true},
		{Name: "Projects", WireName: "Projects", Kind: folder.KindOther, Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 500, StatusKnown: true},
	}
	next := 800
	values, err := state.Folders().Reconcile(context.Background(), accountValue.ID, observations, base, func() (string, error) {
		next++
		return fmt.Sprintf("fld_location_%03d", next), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	folders := make(map[folder.Kind]folder.Folder, len(values))
	for _, value := range values {
		folders[value.Kind] = value
	}
	messageID := "msg_location_fixture"
	subject := "Location mutation fixture"
	for index, seed := range seeds {
		folderValue := folders[seed.kind]
		internal := base.Add(time.Duration(index) * time.Minute)
		record := maildomain.IngestRecord{
			MessageID:  fmt.Sprintf("%s_proposed_%d", messageID, index),
			LocationID: fmt.Sprintf("loc_location_fixture_%d", index),
			AccountID:  accountValue.ID, FolderID: folderValue.ID,
			UIDValidity: folderValue.UIDValidity, UID: seed.uid,
			Flags: []string{seed.flag, `$Fixture`}, InternalDate: internal,
			RFC822Size: 512, RawContentHash: strings.Repeat(fmt.Sprintf("%x", index+10), 64)[:64],
			RawStorageKey: fmt.Sprintf("raw/sha256/%02d/location-%d.eml", index, index), RawByteSize: 512,
			Parsed: maildomain.ParsedMessage{
				RFCMessageID: "location-fixture@example.test",
				From:         maildomain.Address{Email: "sender@example.test"},
				To:           []maildomain.Address{{Email: accountValue.Email}},
				Subject:      subject, Snippet: "location mutation fixture body", HeaderDate: &internal,
			},
		}
		_, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
			CheckpointID: fmt.Sprintf("chk_location_%d", index),
			AccountID:    accountValue.ID, FolderID: folderValue.ID,
			UIDValidity: folderValue.UIDValidity,
			Bootstrap:   true, LastCommittedUID: seed.uid, LastScannedUID: seed.uid,
			AttemptedAt: internal, Records: []maildomain.IngestRecord{record},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	message, err := state.Mail().Message(context.Background(), accountValue.ID, messageID)
	if err != nil {
		// Identity resolution keeps the first proposed stable ID.
		message, err = state.Mail().Message(context.Background(), accountValue.ID, messageID+"_proposed_0")
	}
	if err != nil {
		t.Fatal(err)
	}
	page, err := state.Threads().Show(context.Background(), accountValue.ID, message.ID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	locations := map[folder.Kind]maildomain.Location{}
	for _, location := range message.Locations {
		for kind, value := range folders {
			if location.FolderID == value.ID {
				locations[kind] = location
			}
		}
	}
	return locationMutationFixture{
		messageID: message.ID, threadID: page.Thread.ID, subject: subject,
		folders: folders, locations: locations,
	}
}

var _ transport.IMAPLocationClient = (*fakeLocationClient)(nil)
