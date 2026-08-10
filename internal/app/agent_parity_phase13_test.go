package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestAgentParityPhase13SearchSelectionIsImmutableExpiresAndRejectsCrossAccountOrStaleLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase13-secret")
	commands, _ := newTestAccountCommands()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	commands.now = func() time.Time { return now }
	addSendAccount(t, commands, home)
	fixture := seedLocationMutationFixture(t, home, []locationSeed{{folder.KindInbox, 1, `$Fixture`}})

	request := maildomain.SelectionCreateRequest{
		Search:       &maildomain.SearchOptions{Query: fixture.subject, ResultKind: maildomain.SearchMessages, Limit: 100},
		SearchFolder: "inbox", ScopeFolder: "inbox", TTL: 5 * time.Minute, MaximumMembership: 100,
	}
	accountValue, selection, schemaVersion, err := commands.MailSelectionCreate(context.Background(), home, "", request)
	if err != nil || schemaVersion != 27 || selection.SourceKind != maildomain.SelectionSourceSearch || selection.SourceDescription != "one bounded local search page" || selection.ItemCount != 1 || len(selection.Items) != 1 || selection.Items[0].MessageID != fixture.messageID || selection.Items[0].LocationID == "" || selection.Items[0].FolderID != fixture.folders[folder.KindInbox].ID || len(selection.SourceQueryHash) != 64 || len(selection.RequestHash) != 64 {
		t.Fatalf("create selection account=%+v selection=%+v schema=%d err=%v", accountValue, selection, schemaVersion, err)
	}
	if strings.Contains(selection.SourceDescription, fixture.subject) {
		t.Fatalf("selection retained query/message content: %+v", selection)
	}

	// Add another message that now matches the original query. The immutable
	// selection must continue to contain only the reviewed first message.
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	inbox := fixture.folders[folder.KindInbox]
	late := phase12ThreadRecord(accountValue.ID, inbox.ID, inbox.UIDValidity, 2, "msg_phase13_late", "phase13-late@example.test", fixture.subject, "later@example.test", accountValue.Email, now.Add(time.Minute), 0)
	phase12CommitRecord(t, state, accountValue.ID, inbox, late, now.Add(time.Minute))
	searchPage, err := state.Search().Search(context.Background(), accountValue.ID, maildomain.SearchOptions{Query: fixture.subject, FolderID: inbox.ID, ResultKind: maildomain.SearchMessages, Limit: 100})
	state.Close()
	if err != nil || len(searchPage.Hits) != 2 {
		t.Fatalf("fresh live query hits=%d err=%v", len(searchPage.Hits), err)
	}
	_, frozen, messages, _, err := commands.MailSelectionForApply(context.Background(), home, "", selection.ID, true)
	if err != nil || frozen.ItemCount != 1 || len(messages) != 1 || messages[0].ID != fixture.messageID {
		t.Fatalf("selection drifted frozen=%+v messages=%+v err=%v", frozen, messages, err)
	}

	// A completely new command service/process can reopen the same durable
	// unexpired immutable selection without rerunning the source query.
	reopened, _ := newTestAccountCommands()
	reopened.now = func() time.Time { return now.Add(2 * time.Minute) }
	_, reopenedSelection, _, err := reopened.MailSelection(context.Background(), home, "", selection.ID)
	if err != nil || reopenedSelection.RequestHash != selection.RequestHash || len(reopenedSelection.Items) != 1 || reopenedSelection.Items[0] != selection.Items[0] {
		t.Fatalf("reopened selection=%+v err=%v", reopenedSelection, err)
	}

	// Account binding is checked before target execution.
	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "account", "add", "--name", "Other Sender", "--email", "other@icloud.com", "--credential-env", "ICLOUD_SEND_PASSWORD")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "account-add") {
		t.Fatalf("add other account code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	_, _, _, _, err = commands.MailSelectionForApply(context.Background(), home, "Other Sender", selection.ID, true)
	if normalized := cli.NormalizeError(err); normalized == nil || normalized.Code != cli.ErrorConflict {
		t.Fatalf("cross-account apply err=%v normalized=%+v", err, normalized)
	}

	// A provider-state refresh that changes the reviewed exact location evidence
	// makes a state apply stale before any later provider write can start.
	staleSelection := selection
	flagClient := &fakeFlagClient{results: []transport.FlagMutationResult{{
		Mailbox: inbox.Name, UIDValidity: inbox.UIDValidity, UID: 1,
		Flags: []string{`\Seen`, `$Fixture`}, ModSequence: 77, Present: true,
	}}}
	commands.flags = flagClient
	if _, _, _, _, _, err := commands.MailUpdateFlag(context.Background(), home, "", fixture.messageID, "inbox", maildomain.FlagMutationMarkRead); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err = commands.MailSelectionForApply(context.Background(), home, "", staleSelection.ID, true)
	if normalized := cli.NormalizeError(err); normalized == nil || normalized.Code != cli.ErrorConflict || !strings.Contains(normalized.Message, "reviewed snapshot") {
		t.Fatalf("stale location err=%v normalized=%+v", err, normalized)
	}

	now = selection.ExpiresAt.Add(time.Nanosecond)
	_, _, _, _, err = commands.MailSelectionForApply(context.Background(), home, "", selection.ID, false)
	if normalized := cli.NormalizeError(err); normalized == nil || normalized.Code != cli.ErrorConflict || !strings.Contains(normalized.Message, "expired") {
		t.Fatalf("expired selection err=%v normalized=%+v", err, normalized)
	}
}

func TestAgentParityPhase13ExplicitRetiredThreadAliasSnapshotsCanonicalMembersAndBoundsExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase13-secret")
	commands, _ := newTestAccountCommands()
	commands.now = func() time.Time { return time.Date(2026, 8, 10, 6, 30, 0, 0, time.UTC) }
	addSendAccount(t, commands, home)
	fixture := seedLocationMutationFixture(t, home, []locationSeed{{folder.KindInbox, 1, `$Fixture`}})
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	inbox := fixture.folders[folder.KindInbox]
	base := time.Date(2026, 8, 10, 6, 31, 0, 0, time.UTC)
	left := phase12ThreadRecord(accountValue.ID, inbox.ID, inbox.UIDValidity, 10, "msg_phase13_alias_left", "phase13-alias-left@example.test", "Phase 13 alias", "left@example.test", accountValue.Email, base, 0)
	phase12CommitRecord(t, state, accountValue.ID, inbox, left, base)
	leftPage, err := state.Threads().Show(context.Background(), accountValue.ID, left.MessageID, "", 100)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	right := phase12ThreadRecord(accountValue.ID, inbox.ID, inbox.UIDValidity, 11, "msg_phase13_alias_right", "phase13-alias-right@example.test", "Independent", "right@example.test", accountValue.Email, base.Add(time.Minute), 0)
	phase12CommitRecord(t, state, accountValue.ID, inbox, right, base.Add(time.Minute))
	rightPage, err := state.Threads().Show(context.Background(), accountValue.ID, right.MessageID, "", 100)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	retiredID := rightPage.Thread.ID
	bridge := phase12ThreadRecord(accountValue.ID, inbox.ID, inbox.UIDValidity, 12, "msg_phase13_alias_bridge", "phase13-alias-bridge@example.test", "Re: Phase 13 alias", accountValue.Email, "left@example.test", base.Add(2*time.Minute), 0)
	bridge.Parsed.InReplyTo = "phase13-alias-right@example.test"
	bridge.Parsed.References = []string{"phase13-alias-left@example.test", "phase13-alias-right@example.test"}
	phase12CommitRecord(t, state, accountValue.ID, inbox, bridge, base.Add(2*time.Minute))
	merged, err := state.Threads().Show(context.Background(), accountValue.ID, retiredID, "", 100)
	state.Close()
	if err != nil || merged.Thread.ID != leftPage.Thread.ID || merged.Thread.RedirectedFrom != retiredID || merged.Thread.MessageCount != 3 {
		t.Fatalf("merged=%+v err=%v", merged.Thread, err)
	}
	_, selection, _, err := commands.MailSelectionCreate(context.Background(), home, "", maildomain.SelectionCreateRequest{Targets: []string{retiredID}, ScopeFolder: "inbox", TTL: 15 * time.Minute, MaximumMembership: 100})
	if err != nil || selection.ItemCount != 3 || selection.SourceKind != maildomain.SelectionSourceExplicit {
		t.Fatalf("alias selection=%+v err=%v", selection, err)
	}
	got := make([]string, 0, len(selection.Items))
	for _, item := range selection.Items {
		got = append(got, item.MessageID)
	}
	if strings.Join(got, ",") != "msg_phase13_alias_left,msg_phase13_alias_right,msg_phase13_alias_bridge" {
		t.Fatalf("alias selection members=%v", got)
	}

	targets := make([]string, 101)
	for index := range targets {
		targets[index] = fmt.Sprintf("msg_over_%03d", index)
	}
	_, _, _, err = commands.MailSelectionCreate(context.Background(), home, "", maildomain.SelectionCreateRequest{Targets: targets, TTL: 15 * time.Minute, MaximumMembership: 100})
	if normalized := cli.NormalizeError(err); normalized == nil || normalized.Code != cli.ErrorSafety {
		t.Fatalf("oversized explicit selection err=%v normalized=%+v", err, normalized)
	}
}

func TestAgentParityPhase13LocationSelectionRetryReusesItsReviewedMutationAfterSourceChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase13-secret")
	commands, _ := newTestAccountCommands()
	commands.now = func() time.Time { return time.Date(2026, 8, 10, 6, 45, 0, 0, time.UTC) }
	addSendAccount(t, commands, home)
	fixture := seedLocationMutationFixture(t, home, []locationSeed{{folder.KindInbox, 1, `$Fixture`}})

	_, selection, _, err := commands.MailSelectionCreate(context.Background(), home, "", maildomain.SelectionCreateRequest{
		Targets: []string{fixture.messageID}, ScopeFolder: "inbox", TTL: 15 * time.Minute, MaximumMembership: 100,
	})
	if err != nil || len(selection.Items) != 1 {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
	item := selection.Items[0]
	other := fixture.folders[folder.KindOther]
	client := &fakeLocationClient{results: []transport.LocationMutationResult{{
		ProviderMethod: "move", ProviderMutationObserved: true,
		SourceUIDValidity: fixture.folders[folder.KindInbox].UIDValidity, SourceUID: item.UID,
		SourceFlags: []string{`$Fixture`}, SourceModSequence: item.ModSequence + 17, SourcePresent: false,
		DestinationKnown: true, DestinationUIDValidity: other.UIDValidity, DestinationUID: 9,
		DestinationFlags: []string{`$Fixture`}, DestinationModSequence: 3,
	}}}
	commands.locations = client
	request := maildomain.LocationMutationRequest{
		Action: maildomain.LocationActionMove, MessageID: item.MessageID,
		FromFolder: item.FolderID, ToFolder: other.ID, IdempotencyKey: "selection:phase13-retry",
		ExpectedLocationID: item.LocationID, ExpectedUIDValidity: item.UIDValidity,
		ExpectedUID: item.UID, ExpectedModSequence: item.ModSequence,
	}
	_, firstPlan, _, firstMutation, reused, _, err := commands.MailMutateLocation(context.Background(), home, "", request)
	if err != nil || reused || client.calls != 1 || firstMutation.Status != maildomain.LocationMutationConfirmed {
		t.Fatalf("first plan=%+v mutation=%+v reused=%t calls=%d err=%v", firstPlan, firstMutation, reused, client.calls, err)
	}

	_, replayPlan, _, replayMutation, reused, _, err := commands.MailMutateLocation(context.Background(), home, "", request)
	if err != nil || !reused || client.calls != 1 || replayMutation.ID != firstMutation.ID || replayPlan.RequestHash != firstPlan.RequestHash {
		t.Fatalf("replay plan=%+v mutation=%+v reused=%t calls=%d err=%v", replayPlan, replayMutation, reused, client.calls, err)
	}
}
