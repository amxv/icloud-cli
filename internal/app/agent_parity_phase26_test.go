package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestAgentParityPhase26ThreadMuteStaysLocalWithoutProviderClients(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_PHASE26_PASSWORD", "synthetic-phase26-secret")
	seedCommands, _ := newTestAccountCommands()
	seedCommands.folders = oneInboxFolderClient()
	raw := syntheticThreadMailFixture(
		321, "phase26-local-root@example.test", "Phase 26 local mute", "root body",
		"sender@example.test", "owner@icloud.com", nil, "", "",
		time.Date(2026, 8, 11, 4, 30, 0, 0, time.UTC),
	)
	seedCommands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{
		page: transport.InboxPage{
			Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 321, LastScannedUID: 321, Bootstrap: true,
			Messages: []transport.FetchedMessage{{UID: 321, InternalDate: time.Date(2026, 8, 11, 4, 30, 0, 0, time.UTC)}},
		},
		raw: map[uint32]string{321: raw},
	}}}
	addAndDiscoverInbox(t, seedCommands, home, "ICLOUD_PHASE26_PASSWORD")
	if _, _, summaries, _, err := seedCommands.MailSync(context.Background(), home, "", "inbox", 20); err != nil || len(summaries) != 1 || summaries[0].Created != 1 {
		t.Fatalf("seed sync summaries=%+v err=%v", summaries, err)
	}
	_, threads, _, _, err := seedCommands.MailLatest(context.Background(), home, "", 20)
	if err != nil || len(threads) != 1 {
		t.Fatalf("seed threads=%+v err=%v", threads, err)
	}
	threadID := threads[0].ID

	// Use a fresh command service with every provider client absent. These
	// public local commands must not resolve credentials or contact iCloud.
	localCommands, _ := newTestAccountCommands()
	code, stdout, stderr := runWithAccountCommands(t, localCommands, "", "--home", home, "mail", "threads", "mute", threadID, "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"local_only":true`) || !strings.Contains(stdout, `"muted":true`) || !strings.Contains(stdout, `"thread_id":"`+threadID+`"`) {
		t.Fatalf("mute code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, localCommands, "", "--home", home, "mail", "threads", "show", threadID, "--metadata-only", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"muted":true`) || !strings.Contains(stdout, `"thread_muted":true`) {
		t.Fatalf("muted show code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, localCommands, "", "--home", home, "mail", "threads", "unmute", threadID)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "scope=local") || !strings.Contains(stdout, "muted=false") {
		t.Fatalf("unmute code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestAgentParityPhase26WatchSyncClassifiesMutedThreadsAfterCommitWithoutDroppingMail(t *testing.T) {
	for _, test := range []struct {
		name      string
		muted     bool
		wantQuiet bool
	}{
		{name: "muted", muted: true, wantQuiet: true},
		{name: "unmuted", muted: false, wantQuiet: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "state")
			t.Setenv("ICLOUD_PHASE26_WATCH_PASSWORD", "synthetic-phase26-watch-secret")
			commands, _ := newTestAccountCommands()
			commands.folders = oneInboxFolderClient()
			rootAt := time.Date(2026, 8, 11, 5, 0, 0, 0, time.UTC)
			replyAt := rootAt.Add(time.Minute)
			rootRFC := "phase26-watch-root@example.test"
			replyRFC := "phase26-watch-reply@example.test"
			unrelatedRFC := "phase26-watch-unrelated@example.test"
			rootRaw := syntheticThreadMailFixture(401, rootRFC, "Phase 26 watch", "root", "sender@example.test", "owner@icloud.com", nil, "", "", rootAt)
			replyRaw := syntheticThreadMailFixture(402, replyRFC, "Re: Phase 26 watch", "reply", "sender@example.test", "owner@icloud.com", []string{rootRFC}, rootRFC, "", replyAt)
			unrelatedRaw := syntheticThreadMailFixture(400, unrelatedRFC, "Phase 26 unrelated", "unrelated", "other@example.test", "owner@icloud.com", nil, "", "", rootAt.Add(-time.Minute))
			mailClient := &fakeInboxClient{results: []fakeInboxPageResult{
				{page: transport.InboxPage{
					Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 401, LastScannedUID: 401, Bootstrap: true,
					Messages: []transport.FetchedMessage{{UID: 400, InternalDate: rootAt.Add(-time.Minute)}, {UID: 401, InternalDate: rootAt}},
				}, raw: map[uint32]string{400: unrelatedRaw, 401: rootRaw}},
				{page: transport.InboxPage{
					Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 402, LastScannedUID: 402,
					Messages: []transport.FetchedMessage{{UID: 402, InternalDate: replyAt}},
					LocationStates: []transport.LocationState{
						{UID: 400, Present: true},
						{UID: 401, Present: true},
					},
				}, raw: map[uint32]string{402: replyRaw}},
			}}
			commands.mail = mailClient
			addAndDiscoverInbox(t, commands, home, "ICLOUD_PHASE26_WATCH_PASSWORD")

			accountValue, _, initial, _, err := commands.MailSync(context.Background(), home, "", "inbox", 20)
			if err != nil || len(initial) != 1 || initial[0].Created != 2 {
				t.Fatalf("initial sync=%+v err=%v", initial, err)
			}
			_, threads, _, _, err := commands.MailLatest(context.Background(), home, accountValue.ID, 20)
			if err != nil || len(threads) != 2 {
				t.Fatalf("initial threads=%+v err=%v", threads, err)
			}
			var mutedThreadID string
			for _, thread := range threads {
				if thread.CanonicalSubject == "Phase 26 watch" {
					mutedThreadID = thread.ID
				}
			}
			if mutedThreadID == "" {
				t.Fatalf("initial muted target missing: %+v", threads)
			}
			if test.muted {
				if _, preference, _, err := commands.MailThreadSetMuted(context.Background(), home, accountValue.ID, mutedThreadID, true); err != nil || !preference.Muted {
					t.Fatalf("mute preference=%+v err=%v", preference, err)
				}
			}

			var events []maildomain.WatchEvent
			emitter := &accountWatchEmitter{
				accountID: accountValue.ID,
				now:       func() time.Time { return replyAt.Add(time.Minute) },
				emit: func(event maildomain.WatchEvent) error {
					events = append(events, event)
					return nil
				},
			}
			changeQuiet := false
			suppressed, err := commands.runWatchSync(
				context.Background(), home, accountValue.ID, "inbox", false, false,
				maildomain.WatchOptions{Limit: 20, OperationTimeout: 5 * time.Second},
				&watchSyncGate{}, emitter,
				func(quiet bool) error {
					changeQuiet = quiet
					return emitter.send(maildomain.WatchEvent{Type: maildomain.WatchEventChange, FolderName: "INBOX", Reason: "change", Quiet: quiet})
				},
			)
			if err != nil || suppressed != test.wantQuiet || changeQuiet != test.wantQuiet {
				t.Fatalf("watch suppressed=%v change_quiet=%v want=%v err=%v events=%+v", suppressed, changeQuiet, test.wantQuiet, err, events)
			}
			if len(events) != 2 || events[0].Type != maildomain.WatchEventChange || events[1].Type != maildomain.WatchEventSync || events[1].Quiet != test.wantQuiet || events[1].Summary == nil || events[1].Summary.Created != 1 || events[1].Summary.Fetched != 1 {
				t.Fatalf("watch events=%+v", events)
			}
			if len(events[1].Summary.ChangedMessageIDs) != 1 {
				t.Fatalf("watch changed IDs=%v", events[1].Summary.ChangedMessageIDs)
			}

			_, current, _, _, err := commands.MailLatest(context.Background(), home, accountValue.ID, 20)
			if err != nil || len(current) != 2 {
				t.Fatalf("post-watch threads=%+v err=%v", current, err)
			}
			foundMutedThread := false
			for _, thread := range current {
				if thread.ID == mutedThreadID {
					foundMutedThread = thread.MessageCount == 2 && thread.Muted == test.muted
				}
			}
			if !foundMutedThread {
				t.Fatalf("post-watch muted thread=%+v", current)
			}
			_, _, search, _, err := commands.MailSearch(context.Background(), home, accountValue.ID, "", maildomain.SearchOptions{
				ResultKind: maildomain.SearchMessages, RFCMessageID: replyRFC, Limit: 20,
			})
			if err != nil || len(search.Hits) != 1 || search.Hits[0].Message == nil || search.Hits[0].Message.RFCMessageID != replyRFC || search.Hits[0].Message.ThreadMuted != test.muted {
				t.Fatalf("post-watch search=%+v err=%v", search, err)
			}
		})
	}
}
