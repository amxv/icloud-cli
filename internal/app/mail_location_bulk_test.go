package app

import (
	"context"
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

func TestLocationBulkAtomicAndContinuePersistTruthfulPerItemOutcomes(t *testing.T) {
	for _, test := range []struct {
		name          string
		mode          string
		wantCalls     int
		wantMutations int
		wantMoved     int
		wantSkipped   int
	}{
		{name: "atomic", mode: "atomic", wantCalls: 2, wantMutations: 2, wantMoved: 1, wantSkipped: 1},
		{name: "continue", mode: "continue-on-error", wantCalls: 3, wantMutations: 3, wantMoved: 2, wantSkipped: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-locationBulk-secret")
			commands, _ := newTestAccountCommands()
			client := &fakeLocationClient{}
			commands.locations = client
			addSendAccount(t, commands, home)
			fixture, messageIDs := seedBulkLocationMessages(t, home)
			client.results = []transport.LocationMutationResult{
				locationBulkMoveResult(fixture, 1, 11),
				{},
				locationBulkMoveResult(fixture, 3, 13),
			}
			client.errors = []error{
				nil,
				transport.NewFailure(transport.StageIMAPMove, transport.FailureUnavailable, true, errors.New("synthetic second-item provider failure")),
				nil,
			}

			args := []string{"--home", home, "mail", "move"}
			args = append(args, messageIDs...)
			args = append(args,
				"--from", "inbox", "--to", fixture.folders[folder.KindOther].ID,
				"--idempotency-key", "locationBulk-"+test.name, "--mode", test.mode, "--yes", "--jsonl",
			)
			code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
			if code != cli.ExitUnavailable || client.calls != test.wantCalls {
				t.Fatalf("code=%d calls=%d stdout=%q stderr=%q", code, client.calls, stdout, stderr)
			}
			if !strings.Contains(stdout, fmt.Sprintf(`"skipped":%d`, test.wantSkipped)) || !strings.Contains(stdout, `"partial":true`) || !strings.Contains(stderr, `"event":"error"`) {
				t.Fatalf("bulk accounting stdout=%q stderr=%q", stdout, stderr)
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
			mutations, _, err := state.LocationMutations().List(context.Background(), accountValue.ID, 10)
			if err != nil {
				state.Close()
				t.Fatal(err)
			}
			confirmed, failed := 0, 0
			for _, mutation := range mutations {
				switch mutation.Status {
				case maildomain.LocationMutationConfirmed:
					confirmed++
				case maildomain.LocationMutationFailed:
					failed++
				}
			}
			if len(mutations) != test.wantMutations || confirmed != test.wantMoved || failed != 1 {
				state.Close()
				t.Fatalf("mutations=%+v confirmed=%d failed=%d", mutations, confirmed, failed)
			}
			moved := 0
			for _, messageID := range messageIDs {
				message, readErr := state.Mail().Message(context.Background(), accountValue.ID, messageID)
				if readErr != nil {
					state.Close()
					t.Fatal(readErr)
				}
				for _, location := range message.Locations {
					if location.FolderID == fixture.folders[folder.KindOther].ID && location.Present {
						moved++
					}
				}
			}
			state.Close()
			if moved != test.wantMoved {
				t.Fatalf("moved destination copies=%d want=%d", moved, test.wantMoved)
			}
		})
	}
}

func TestSuccessfulBulkReplayDoesNotRepeatProviderWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-locationBulk-secret")
	commands, _ := newTestAccountCommands()
	client := &fakeLocationClient{}
	commands.locations = client
	addSendAccount(t, commands, home)
	fixture, messageIDs := seedBulkLocationMessages(t, home)
	messageIDs = messageIDs[:2]
	client.results = []transport.LocationMutationResult{
		locationBulkMoveResult(fixture, 1, 21),
		locationBulkMoveResult(fixture, 2, 22),
	}
	args := []string{"--home", home, "mail", "move"}
	args = append(args, messageIDs...)
	args = append(args,
		"--from", "inbox", "--to", fixture.folders[folder.KindOther].ID,
		"--idempotency-key", "locationBulk-replay", "--yes",
	)
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || client.calls != 2 || !strings.Contains(stdout, "completed=2") {
		t.Fatalf("first code=%d calls=%d stdout=%q stderr=%q", code, client.calls, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || client.calls != 2 || strings.Count(stdout, "reused=true") != 2 || !strings.Contains(stdout, "completed=2") {
		t.Fatalf("replay code=%d calls=%d stdout=%q stderr=%q", code, client.calls, stdout, stderr)
	}
}

func seedBulkLocationMessages(t *testing.T, home string) (locationMutationFixture, []string) {
	t.Helper()
	fixture := seedLocationMutationFixture(t, home, []locationSeed{{folder.KindInbox, 1, `\Seen`}})
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	inbox := fixture.folders[folder.KindInbox]
	base := time.Date(2026, 8, 10, 4, 30, 0, 0, time.UTC)
	messageIDs := []string{fixture.messageID, "msg_locationBulk_two", "msg_locationBulk_three"}
	records := make([]maildomain.IngestRecord, 0, 2)
	for index, messageID := range messageIDs[1:] {
		uid := uint32(index + 2)
		at := base.Add(time.Duration(index) * time.Minute)
		hashByte := string(rune('b' + index))
		rawHash := strings.Repeat(hashByte, 64)
		records = append(records, maildomain.IngestRecord{
			MessageID: messageID, LocationID: fmt.Sprintf("loc_locationBulk_%d", uid),
			AccountID: accountValue.ID, FolderID: inbox.ID, UIDValidity: inbox.UIDValidity, UID: uid,
			Flags: []string{`\Seen`, `$Fixture`}, InternalDate: at,
			RFC822Size: 512, RawContentHash: rawHash, RawStorageKey: "raw/sha256/" + hashByte + hashByte + "/" + rawHash + ".eml", RawByteSize: 512,
			Parsed: maildomain.ParsedMessage{
				RFCMessageID: fmt.Sprintf("locationBulk-%d@example.test", uid),
				From:         maildomain.Address{Email: "sender@example.test"}, To: []maildomain.Address{{Email: accountValue.Email}},
				Subject: fmt.Sprintf("Bulk location mutation bulk %d", uid), Snippet: "bulk location mutation bulk fixture", HeaderDate: &at,
			},
		})
	}
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_locationBulk_bulk", AccountID: accountValue.ID, FolderID: inbox.ID,
		UIDValidity: inbox.UIDValidity, Bootstrap: true, LastCommittedUID: 3, LastScannedUID: 3,
		AttemptedAt: base.Add(2 * time.Minute), Records: records,
	}); err != nil {
		t.Fatal(err)
	}
	return fixture, messageIDs
}

func locationBulkMoveResult(fixture locationMutationFixture, sourceUID, destinationUID uint32) transport.LocationMutationResult {
	return transport.LocationMutationResult{
		Operation: transport.LocationMutationMove, ProviderMethod: "move",
		SourceMailbox: fixture.folders[folder.KindInbox].Name, SourceUIDValidity: fixture.folders[folder.KindInbox].UIDValidity, SourceUID: sourceUID,
		SourceFlags: []string{`\Seen`, `$Fixture`}, SourcePresent: false,
		DestinationMailbox: fixture.folders[folder.KindOther].Name, DestinationUIDValidity: fixture.folders[folder.KindOther].UIDValidity, DestinationUID: destinationUID,
		DestinationFlags: []string{`\Seen`, `$Fixture`}, DestinationKnown: true, ProviderMutationObserved: true,
	}
}
