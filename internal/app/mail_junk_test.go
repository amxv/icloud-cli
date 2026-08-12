package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestJunkAndNotJunkReuseExactLocationMutationPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-junk-secret")
	commands, _ := newTestAccountCommands()
	client := &fakeLocationClient{}
	commands.locations = client
	addSendAccount(t, commands, home)
	fixture := seedLocationMutationFixture(t, home, []locationSeed{{folder.KindInbox, 1, `\Seen`}})
	client.results = []transport.LocationMutationResult{
		{
			Operation: transport.LocationMutationMove, ProviderMethod: "move",
			SourceMailbox:     fixture.folders[folder.KindInbox].Name,
			SourceUIDValidity: fixture.locations[folder.KindInbox].UIDValidity,
			SourceUID:         fixture.locations[folder.KindInbox].UID,
			SourceFlags:       []string{`\Seen`, `$Fixture`}, SourceModSequence: 10, SourcePresent: false,
			DestinationMailbox:     fixture.folders[folder.KindJunk].Name,
			DestinationUIDValidity: fixture.folders[folder.KindJunk].UIDValidity,
			DestinationUID:         11, DestinationFlags: []string{`\Seen`, `$Fixture`}, DestinationModSequence: 3,
			DestinationKnown: true, ProviderMutationObserved: true,
		},
		{
			Operation: transport.LocationMutationMove, ProviderMethod: "move",
			SourceMailbox:     fixture.folders[folder.KindJunk].Name,
			SourceUIDValidity: fixture.folders[folder.KindJunk].UIDValidity,
			SourceUID:         11,
			SourceFlags:       []string{`\Seen`, `$Fixture`}, SourceModSequence: 3, SourcePresent: false,
			DestinationMailbox:     fixture.folders[folder.KindInbox].Name,
			DestinationUIDValidity: fixture.folders[folder.KindInbox].UIDValidity,
			DestinationUID:         12, DestinationFlags: []string{`\Seen`, `$Fixture`}, DestinationModSequence: 4,
			DestinationKnown: true, ProviderMutationObserved: true,
		},
	}

	junkArgs := []string{
		"--home", home, "mail", "junk", fixture.messageID,
		"--idempotency-key", "junk-junk-01",
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", append(append([]string(nil), junkArgs...), "--dry-run")...)
	if code != 0 || stderr != "" || client.calls != 0 || !compactContainsAll(stdout,
		"action=junk", "operation=move",
		"source_folder="+fixture.folders[folder.KindInbox].ID,
		"destination_folder="+fixture.folders[folder.KindJunk].ID,
		"imap_contacted=false", "intent_created=false",
	) {
		t.Fatalf("junk dry-run code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", junkArgs...)
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "requires --yes") || client.calls != 0 {
		t.Fatalf("junk confirmation code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", append(append([]string(nil), junkArgs...), "--yes")...)
	if code != 0 || stderr != "" || client.calls != 1 || !compactContainsAll(stdout, "action=junk", "status=confirmed", "provider_method=move", "reused=false") {
		t.Fatalf("junk code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
	if request := client.requests[0]; request.Operation != transport.LocationMutationMove || request.SourceMailbox != fixture.folders[folder.KindInbox].Name || request.DestinationMailbox != fixture.folders[folder.KindJunk].Name || request.ExpectedSourceUIDValidity != fixture.locations[folder.KindInbox].UIDValidity || request.SourceUID != fixture.locations[folder.KindInbox].UID {
		t.Fatalf("junk provider request=%+v", request)
	}

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	message, err := state.Mail().Message(context.Background(), accountValue.ID, fixture.messageID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	var inboxPresent, junkPresent bool
	for _, location := range message.Locations {
		switch location.FolderID {
		case fixture.folders[folder.KindInbox].ID:
			inboxPresent = location.Present
		case fixture.folders[folder.KindJunk].ID:
			junkPresent = location.Present
		}
	}
	mutations, _, err := state.LocationMutations().List(context.Background(), accountValue.ID, 10)
	state.Close()
	if err != nil || inboxPresent || !junkPresent || len(mutations) != 1 || mutations[0].Action != maildomain.LocationActionJunk {
		t.Fatalf("junk local message=%+v mutations=%+v err=%v", message, mutations, err)
	}
	code, replayOut, replayErr := runWithAccountCommands(t, commands, "", append(append([]string(nil), junkArgs...), "--yes")...)
	if code != 0 || replayErr != "" || client.calls != 1 || !strings.Contains(replayOut, "reused=true") {
		t.Fatalf("junk replay code=%d stdout=%q stderr=%q calls=%d", code, replayOut, replayErr, client.calls)
	}

	notJunkArgs := []string{
		"--home", home, "mail", "not-junk", fixture.messageID,
		"--idempotency-key", "junk-not-junk-01",
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", append(append([]string(nil), notJunkArgs...), "--dry-run")...)
	if code != 0 || stderr != "" || client.calls != 1 || !compactContainsAll(stdout,
		"action=not_junk", "source_folder="+fixture.folders[folder.KindJunk].ID,
		"destination_folder="+fixture.folders[folder.KindInbox].ID,
	) {
		t.Fatalf("not-junk dry-run code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", append(append([]string(nil), notJunkArgs...), "--yes")...)
	if code != 0 || stderr != "" || client.calls != 2 || !compactContainsAll(stdout, "action=not_junk", "status=confirmed", "reused=false") {
		t.Fatalf("not-junk code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
	if request := client.requests[1]; request.Operation != transport.LocationMutationMove || request.SourceMailbox != fixture.folders[folder.KindJunk].Name || request.DestinationMailbox != fixture.folders[folder.KindInbox].Name || request.ExpectedSourceUIDValidity != fixture.folders[folder.KindJunk].UIDValidity || request.SourceUID != 11 {
		t.Fatalf("not-junk provider request=%+v", request)
	}
}

func TestJunkSafetyAmbiguityAndBoundRole(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-junk-secret")
	commands, _ := newTestAccountCommands()
	client := &fakeLocationClient{}
	commands.locations = client
	addSendAccount(t, commands, home)
	fixture := seedLocationMutationFixture(t, home, []locationSeed{
		{folder.KindInbox, 1, `\Seen`},
		{folder.KindOther, 2, `\Seen`},
	})
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "junk", fixture.messageID, "--dry-run")
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "multiple eligible source locations") || client.calls != 0 {
		t.Fatalf("ambiguous junk code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	ordinary := fixture.folders[folder.KindOther]
	binding, err := state.FolderRoles().Set(context.Background(), accountValue.ID, folder.RoleJunk, ordinary.ID, time.Date(2026, 8, 10, 7, 0, 0, 0, time.UTC))
	state.Close()
	if err != nil || binding.FolderID != ordinary.ID {
		t.Fatalf("bound junk=%+v err=%v", binding, err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "junk", fixture.messageID, "--from", "INBOX", "--dry-run")
	if code != 0 || stderr != "" || client.calls != 0 || !strings.Contains(stdout, "destination_folder="+ordinary.ID) {
		t.Fatalf("bound junk preview code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "not-junk", fixture.messageID, "--from", "INBOX", "--dry-run")
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "not-junk requires a current Junk source location") || client.calls != 0 {
		t.Fatalf("wrong-source not-junk code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}

	sentHome := t.TempDir()
	addSendAccount(t, commands, sentHome)
	sentFixture := seedLocationMutationFixture(t, sentHome, []locationSeed{{folder.KindSent, 1, `\Seen`}})
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", sentHome, "mail", "junk", sentFixture.messageID, "--from", "sent", "--dry-run")
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "junk never moves a Sent location") || client.calls != 0 {
		t.Fatalf("Sent junk code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, client.calls)
	}
}
