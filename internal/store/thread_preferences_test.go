package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestThreadPreferenceFollowsAliasMergeSplitAndRestart(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "state")
	state, accountID, folderID := newMailStoreFixtureAt(t, root)
	base := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)

	left := threadRecordFixture(accountID, folderID, 100, 281, "mute-left@example.test", "Mute split anchor", "alice@example.test", "owner@example.test", base)
	right := threadRecordFixture(accountID, folderID, 100, 282, "mute-right@example.test", "Separate muted", "owner@example.test", "alice@example.test", base.Add(time.Minute))
	commitThreadRecords(t, state, accountID, folderID, "chk_thread_mute_left", 100, base.Add(2*time.Minute), left)
	commitThreadRecords(t, state, accountID, folderID, "chk_thread_mute_right", 100, base.Add(3*time.Minute), right)
	leftThread := provisionalThreadID(left.MessageID)
	retiredRightThread := provisionalThreadID(right.MessageID)

	beforeLocation, err := state.Mail().Location(ctx, accountID, right.LocationID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	preference, err := state.Threads().SetMuted(ctx, accountID, retiredRightThread, true, base.Add(4*time.Minute))
	if err != nil || !preference.Muted || preference.ThreadID != retiredRightThread || preference.AnchorMessageID != right.MessageID || !preference.LocalOnly {
		state.Close()
		t.Fatalf("initial preference=%+v err=%v", preference, err)
	}
	rightPage, err := state.Threads().Show(ctx, accountID, right.MessageID, "", 20)
	if err != nil || !rightPage.Thread.Muted || len(rightPage.Thread.Messages) != 1 || !rightPage.Thread.Messages[0].ThreadMuted {
		state.Close()
		t.Fatalf("initial muted projection=%+v err=%v", rightPage.Thread, err)
	}
	afterLocation, err := state.Mail().Location(ctx, accountID, right.LocationID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeLocation.Flags, afterLocation.Flags) || beforeLocation.Present != afterLocation.Present || beforeLocation.ModSequence != afterLocation.ModSequence || !beforeLocation.UpdatedAt.Equal(afterLocation.UpdatedAt) {
		state.Close()
		t.Fatalf("mute changed provider location state: before=%+v after=%+v", beforeLocation, afterLocation)
	}

	// Preference durability must not depend on the process lifetime.
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = Open(ctx, testPaths(t, root))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	rightPage, err = state.Threads().Show(ctx, accountID, right.MessageID, "", 20)
	if err != nil || !rightPage.Thread.Muted || !rightPage.Thread.Messages[0].ThreadMuted {
		t.Fatalf("restart projection=%+v err=%v", rightPage.Thread, err)
	}

	// A late fallback merge retires rightThread. The local preference follows
	// current membership rather than being orphaned on the retired thread row.
	mergedRight := right
	mergedRight.Parsed.Subject = "Re: Mute split anchor"
	commitThreadRecords(t, state, accountID, folderID, "chk_thread_mute_merge", 100, base.Add(5*time.Minute), mergedRight)
	merged, err := state.Threads().Show(ctx, accountID, retiredRightThread, "", 20)
	if err != nil || merged.Thread.ID != leftThread || merged.Thread.RedirectedFrom != retiredRightThread || merged.Thread.MessageCount != 2 || !merged.Thread.Muted {
		t.Fatalf("merged alias projection=%+v err=%v", merged.Thread, err)
	}
	for _, message := range merged.Thread.Messages {
		if !message.ThreadMuted {
			t.Fatalf("merged message %s lost thread mute: %+v", message.ID, message)
		}
	}

	// Updating through the retired alias resolves at the repository boundary.
	preference, err = state.Threads().SetMuted(ctx, accountID, retiredRightThread, false, base.Add(6*time.Minute))
	if err != nil || preference.Muted || preference.ThreadID != leftThread || preference.AnchorMessageID != right.MessageID {
		t.Fatalf("alias unmute preference=%+v err=%v", preference, err)
	}
	merged, err = state.Threads().Show(ctx, accountID, leftThread, "", 20)
	if err != nil || merged.Thread.Muted {
		t.Fatalf("alias unmute projection=%+v err=%v", merged.Thread, err)
	}
	preference, err = state.Threads().SetMuted(ctx, accountID, retiredRightThread, true, base.Add(7*time.Minute))
	if err != nil || !preference.Muted || preference.ThreadID != leftThread || preference.AnchorMessageID != right.MessageID {
		t.Fatalf("alias remute preference=%+v err=%v", preference, err)
	}

	// Stronger contradictory reference evidence splits right back out. Because
	// the stable preference anchor is right.MessageID, only the right component
	// remains muted; unrelated left mail must not inherit it.
	splitRight := mergedRight
	splitRight.Parsed.InReplyTo = "missing-muted-split-parent@example.test"
	splitRight.Parsed.References = []string{"missing-muted-split-parent@example.test"}
	commitThreadRecords(t, state, accountID, folderID, "chk_thread_mute_split", 100, base.Add(8*time.Minute), splitRight)

	rightPage, err = state.Threads().Show(ctx, accountID, right.MessageID, "", 20)
	if err != nil || rightPage.Thread.ID == leftThread || rightPage.Thread.ID == retiredRightThread || !rightPage.Thread.Muted || len(rightPage.Thread.Messages) != 1 || !rightPage.Thread.Messages[0].ThreadMuted {
		t.Fatalf("split right projection=%+v err=%v", rightPage.Thread, err)
	}
	leftPage, err := state.Threads().Show(ctx, accountID, left.MessageID, "", 20)
	if err != nil || leftPage.Thread.ID != leftThread || leftPage.Thread.Muted || len(leftPage.Thread.Messages) != 1 || leftPage.Thread.Messages[0].ThreadMuted {
		t.Fatalf("split left projection=%+v err=%v", leftPage.Thread, err)
	}
	aliasPage, err := state.Threads().Show(ctx, accountID, retiredRightThread, "", 20)
	if err != nil || aliasPage.Thread.ID != leftThread || aliasPage.Thread.Muted {
		t.Fatalf("retired alias target after split=%+v err=%v", aliasPage.Thread, err)
	}

	allMuted, confident, err := state.Threads().AllMessagesMuted(ctx, accountID, []string{right.MessageID})
	if err != nil || !confident || !allMuted {
		t.Fatalf("right muted classification all=%v confident=%v err=%v", allMuted, confident, err)
	}
	allMuted, confident, err = state.Threads().AllMessagesMuted(ctx, accountID, []string{left.MessageID, right.MessageID})
	if err != nil || !confident || allMuted {
		t.Fatalf("mixed classification all=%v confident=%v err=%v", allMuted, confident, err)
	}

	search, err := state.Search().Search(ctx, accountID, maildomain.SearchOptions{ResultKind: maildomain.SearchThreads, RFCMessageID: "mute-right@example.test", Limit: 20})
	if err != nil || len(search.Hits) != 1 || search.Hits[0].Thread == nil || !search.Hits[0].Thread.Muted {
		t.Fatalf("muted thread search=%+v err=%v", search, err)
	}
	message, err := state.Mail().Message(ctx, accountID, right.MessageID)
	if err != nil || !message.ThreadMuted || message.Subject != splitRight.Parsed.Subject {
		t.Fatalf("muted message read=%+v err=%v", message, err)
	}

	// Cross-account selectors cannot reach another account's local preference.
	second, err := state.Accounts().Create(ctx, account.CreateRecord{
		AccountID: "acct_thread_pref_other", CredentialID: "cred_thread_pref_other",
		Name: "Other", Email: "other@icloud.com", IMAPUsername: "other",
		CredentialScheme: account.CredentialEnv, CredentialRef: "env:OTHER_PASSWORD", Now: base.Add(9 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Threads().SetMuted(ctx, second.ID, rightPage.Thread.ID, true, base.Add(10*time.Minute)); !errors.Is(err, maildomain.ErrThreadNotFound) {
		t.Fatalf("cross-account mute err=%v", err)
	}

	preference, err = state.Threads().SetMuted(ctx, accountID, rightPage.Thread.ID, false, base.Add(11*time.Minute))
	if err != nil || preference.Muted {
		t.Fatalf("final unmute preference=%+v err=%v", preference, err)
	}
	rightPage, err = state.Threads().Show(ctx, accountID, right.MessageID, "", 20)
	if err != nil || rightPage.Thread.Muted || rightPage.Thread.Messages[0].ThreadMuted {
		t.Fatalf("final unmute projection=%+v err=%v", rightPage.Thread, err)
	}
}
