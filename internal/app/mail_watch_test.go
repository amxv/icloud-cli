package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

type watchInboxClient struct {
	mu                sync.Mutex
	calls             int
	requests          []transport.InboxFetchRequest
	onFetch           func(int, transport.InboxFetchRequest)
	blockUntilContext bool
}

func (client *watchInboxClient) FetchInboxPage(ctx context.Context, _ transport.AuthRequest, request transport.InboxFetchRequest, _ blob.Stager) (transport.InboxPage, error) {
	client.mu.Lock()
	client.calls++
	call := client.calls
	client.requests = append(client.requests, request)
	onFetch := client.onFetch
	blockUntilContext := client.blockUntilContext
	client.mu.Unlock()
	if blockUntilContext {
		<-ctx.Done()
		return transport.InboxPage{}, ctx.Err()
	}
	if onFetch != nil {
		onFetch(call, request)
	}
	return transport.InboxPage{
		Mailbox:          request.Mailbox,
		UIDValidity:      100,
		LastCommittedUID: request.LastCommittedUID,
		LastScannedUID:   request.LastScannedUID,
		Bootstrap:        request.Bootstrap,
	}, nil
}

func (client *watchInboxClient) snapshot() (int, []transport.InboxFetchRequest) {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.calls, append([]transport.InboxFetchRequest(nil), client.requests...)
}

type fakeMailboxWatchSession struct {
	info      transport.MailboxWatchInfo
	reason    transport.MailboxWatchReason
	err       error
	block     bool
	mu        sync.Mutex
	closed    bool
	waitCalls int
}

func (session *fakeMailboxWatchSession) Info() transport.MailboxWatchInfo { return session.info }

func (session *fakeMailboxWatchSession) Wait(ctx context.Context, _ time.Duration) (transport.MailboxWatchReason, error) {
	session.mu.Lock()
	session.waitCalls++
	session.mu.Unlock()
	if session.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return session.reason, session.err
}

func (session *fakeMailboxWatchSession) Close(context.Context) error {
	session.mu.Lock()
	session.closed = true
	session.mu.Unlock()
	return nil
}

func (session *fakeMailboxWatchSession) isClosed() bool {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.closed
}

type scriptedMailboxWatchClient struct {
	mu         sync.Mutex
	sessions   []transport.IMAPMailboxWatchSession
	errors     []error
	defaultErr error
	opens      int
	usernames  []string
	onOpen     func(int)
}

func (client *scriptedMailboxWatchClient) OpenMailboxWatch(_ context.Context, auth transport.AuthRequest, _ string) (transport.IMAPMailboxWatchSession, error) {
	client.mu.Lock()
	client.opens++
	open := client.opens
	client.usernames = append(client.usernames, auth.Username)
	var session transport.IMAPMailboxWatchSession
	var err error
	if len(client.sessions) > 0 {
		session = client.sessions[0]
		client.sessions = client.sessions[1:]
	}
	if len(client.errors) > 0 {
		err = client.errors[0]
		client.errors = client.errors[1:]
	} else if session == nil {
		err = client.defaultErr
	}
	onOpen := client.onOpen
	client.mu.Unlock()
	if onOpen != nil {
		onOpen(open)
	}
	return session, err
}

func (client *scriptedMailboxWatchClient) openCount() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.opens
}

func watchTestSetup(t *testing.T) (accountCommands, string, *watchInboxClient) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ICLOUD_WATCH_PASSWORD", "synthetic-watch-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	inbox := &watchInboxClient{}
	commands.mail = inbox
	addAndDiscoverInbox(t, commands, home, "ICLOUD_WATCH_PASSWORD")
	return commands, home, inbox
}

func TestMailWatchOperationTimeoutCancelsBlockingProvider(t *testing.T) {
	commands, home, inbox := watchTestSetup(t)
	inbox.mu.Lock()
	inbox.blockUntilContext = true
	inbox.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now()
	seenDegraded := false
	err := commands.MailWatch(ctx, home, "", maildomain.WatchOptions{
		OperationTimeout:  10 * time.Millisecond,
		PollInterval:      time.Second,
		ReconcileInterval: time.Minute,
	}, func(event maildomain.WatchEvent) error {
		if event.Type == maildomain.WatchEventDegraded {
			seenDegraded = true
			cancel()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !seenDegraded {
		t.Fatal("blocking provider did not emit degraded lifecycle event")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("operation timeout was not prompt: %v", elapsed)
	}
}

func TestMailWatchStopsIdleBeforeOrdinarySyncAndCancelsCleanly(t *testing.T) {
	commands, home, inbox := watchTestSetup(t)
	session := &fakeMailboxWatchSession{
		info:   transport.MailboxWatchInfo{Mailbox: "INBOX", Mode: transport.MailboxWatchIdle, Capabilities: []string{"IDLE", "IMAP4REV1"}},
		reason: transport.MailboxWatchChanged,
	}
	commands.watch = &scriptedMailboxWatchClient{sessions: []transport.IMAPMailboxWatchSession{session}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	events := []maildomain.WatchEvent{}
	syncEvents := 0
	err := commands.MailWatch(ctx, home, "", maildomain.WatchOptions{
		Limit: 20, PollInterval: time.Second, ReconcileInterval: time.Minute, OperationTimeout: time.Second,
	}, func(event maildomain.WatchEvent) error {
		mu.Lock()
		events = append(events, event)
		if event.Type == maildomain.WatchEventSync {
			syncEvents++
			if syncEvents == 2 {
				cancel()
			}
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	calls, requests := inbox.snapshot()
	if calls != 2 || len(requests) != 2 || !requests[0].Bootstrap || requests[1].Bootstrap || requests[1].Mailbox != "INBOX" {
		t.Fatalf("sync calls=%d requests=%+v", calls, requests)
	}
	if !session.isClosed() {
		t.Fatal("IDLE session was not closed before watch returned")
	}
	mu.Lock()
	defer mu.Unlock()
	want := []maildomain.WatchEventType{maildomain.WatchEventStartup, maildomain.WatchEventSync, maildomain.WatchEventMode, maildomain.WatchEventIdle, maildomain.WatchEventChange, maildomain.WatchEventSync, maildomain.WatchEventStopped, maildomain.WatchEventCompleted}
	got := make([]maildomain.WatchEventType, 0, len(events))
	for _, event := range events {
		got = append(got, event.Type)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("events = %v want %v", got, want)
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event %d sequence=%d", index, event.Sequence)
		}
	}
}

func TestMailWatchPollingFallbackRunsOrdinaryBoundedSync(t *testing.T) {
	commands, home, inbox := watchTestSetup(t)
	session := &fakeMailboxWatchSession{info: transport.MailboxWatchInfo{Mailbox: "INBOX", Mode: transport.MailboxWatchPolling, Capabilities: []string{"IMAP4REV1"}}}
	commands.watch = &scriptedMailboxWatchClient{sessions: []transport.IMAPMailboxWatchSession{session}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mode maildomain.WatchEvent
	syncs := 0
	err := commands.MailWatch(ctx, home, "", maildomain.WatchOptions{
		Limit: 7, PollInterval: 2 * time.Millisecond, ReconcileInterval: time.Second, OperationTimeout: time.Second,
	}, func(event maildomain.WatchEvent) error {
		if event.Type == maildomain.WatchEventMode {
			mode = event
		}
		if event.Type == maildomain.WatchEventSync {
			syncs++
			if syncs == 2 {
				cancel()
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	calls, requests := inbox.snapshot()
	if mode.Mode != maildomain.WatchModePolling || mode.Reason != "idle_unsupported" || calls != 2 || requests[1].Limit != 7 {
		t.Fatalf("mode=%+v calls=%d requests=%+v", mode, calls, requests)
	}
}

func TestMailWatchReconnectsAfterDroppedIdle(t *testing.T) {
	commands, home, _ := watchTestSetup(t)
	first := &fakeMailboxWatchSession{
		info: transport.MailboxWatchInfo{Mailbox: "INBOX", Mode: transport.MailboxWatchIdle, Capabilities: []string{"IDLE"}},
		err:  transport.NewFailure(transport.StageIMAPIdle, transport.FailureUnavailable, true, errors.New("connection dropped")),
	}
	second := &fakeMailboxWatchSession{info: first.info, block: true}
	ctx, cancel := context.WithCancel(context.Background())
	client := &scriptedMailboxWatchClient{sessions: []transport.IMAPMailboxWatchSession{first, second}}
	client.onOpen = func(open int) {
		if open == 2 {
			cancel()
		}
	}
	commands.watch = client
	var reconnect bool
	err := commands.MailWatch(ctx, home, "", maildomain.WatchOptions{
		PollInterval: time.Second, ReconcileInterval: time.Minute, OperationTimeout: time.Second,
		ReconnectMin: time.Millisecond, ReconnectMax: 5 * time.Millisecond,
	}, func(event maildomain.WatchEvent) error {
		if event.Type == maildomain.WatchEventReconnect {
			reconnect = true
			if event.Attempt != 1 || event.Backoff <= 0 || event.Backoff > 6*time.Millisecond {
				t.Fatalf("reconnect event = %+v", event)
			}
		}
		return nil
	})
	if err != nil || !reconnect || client.openCount() != 2 {
		t.Fatalf("err=%v reconnect=%v opens=%d", err, reconnect, client.openCount())
	}
}

func TestMailWatchAuthenticationFailureDoesNotHotLoop(t *testing.T) {
	commands, home, _ := watchTestSetup(t)
	client := &scriptedMailboxWatchClient{defaultErr: transport.NewFailure(transport.StageIMAPAuth, transport.FailureAuthentication, false, errors.New("rejected"))}
	commands.watch = client
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var reconnect maildomain.WatchEvent
	err := commands.MailWatch(ctx, home, "", maildomain.WatchOptions{
		PollInterval: time.Second, ReconcileInterval: time.Minute, OperationTimeout: time.Second,
		ReconnectMin: time.Millisecond, ReconnectMax: 30 * time.Millisecond,
	}, func(event maildomain.WatchEvent) error {
		if event.Type == maildomain.WatchEventReconnect {
			reconnect = event
			time.AfterFunc(5*time.Millisecond, cancel)
		}
		return nil
	})
	if err != nil || client.openCount() != 2 || reconnect.Backoff < 20*time.Millisecond {
		t.Fatalf("err=%v opens=%d reconnect=%+v", err, client.openCount(), reconnect)
	}
}

func TestMailWatchRejectsDuplicateWorkerForAccount(t *testing.T) {
	commands, home, _ := watchTestSetup(t)
	blocking := &fakeMailboxWatchSession{
		info:  transport.MailboxWatchInfo{Mailbox: "INBOX", Mode: transport.MailboxWatchIdle, Capabilities: []string{"IDLE"}},
		block: true,
	}
	commands.watch = &scriptedMailboxWatchClient{sessions: []transport.IMAPMailboxWatchSession{blocking}}
	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	ready := make(chan struct{}, 1)
	go func() {
		firstDone <- commands.MailWatch(ctx, home, "", maildomain.WatchOptions{OperationTimeout: time.Second}, func(event maildomain.WatchEvent) error {
			if event.Type == maildomain.WatchEventIdle {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
			return nil
		})
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("first watch did not enter IDLE")
	}
	secondErr := commands.MailWatch(context.Background(), home, "", maildomain.WatchOptions{OperationTimeout: time.Second}, func(maildomain.WatchEvent) error { return nil })
	appErr := cli.NormalizeError(secondErr)
	if appErr.Code != cli.ErrorConflict || !appErr.Retryable {
		cancel()
		t.Fatalf("second watch err=%v normalized=%+v", secondErr, appErr)
	}
	cancel()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("first watch did not stop after cancellation")
	}
}

func TestMailWatchAllAccountsRunsOneOrderedWorkerPerAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_WATCH_A", "synthetic-a")
	t.Setenv("ICLOUD_WATCH_B", "synthetic-b")
	commands, _ := newTestAccountCommands()
	folderResult := func() transport.FolderDiscoveryResult {
		return transport.FolderDiscoveryResult{Folders: []transport.RemoteFolder{{
			Name: "INBOX", WireName: "INBOX", Kind: "inbox", KindSource: "inbox", Selectable: true,
			Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: "INBOX", UIDNext: 1, UIDValidity: 100},
		}}}
	}
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {folderResult()},
		"work@icloud.com":  {folderResult()},
	}}
	commands.mail = &watchInboxClient{}
	for _, args := range [][]string{
		{"--home", home, "account", "add", "--name", "Personal", "--email", "owner@icloud.com", "--credential-env", "ICLOUD_WATCH_A"},
		{"--home", home, "account", "add", "--name", "Work", "--email", "work@icloud.com", "--credential-env", "ICLOUD_WATCH_B"},
	} {
		if code, _, stderr := runWithAccountCommands(t, commands, "", args...); code != 0 {
			t.Fatalf("account add %v code=%d stderr=%q", args, code, stderr)
		}
	}
	for _, selector := range []string{"owner@icloud.com", "work@icloud.com"} {
		if code, _, stderr := runWithAccountCommands(t, commands, "", "--home", home, "--account", selector, "mail", "folders", "status"); code != 0 {
			t.Fatalf("folder status %s code=%d stderr=%q", selector, code, stderr)
		}
	}
	client := &scriptedMailboxWatchClient{}
	client.defaultErr = nil
	client.onOpen = nil
	// Supply enough independent blocking sessions regardless of worker scheduling.
	client.sessions = []transport.IMAPMailboxWatchSession{
		&fakeMailboxWatchSession{info: transport.MailboxWatchInfo{Mode: transport.MailboxWatchIdle, Capabilities: []string{"IDLE"}}, block: true},
		&fakeMailboxWatchSession{info: transport.MailboxWatchInfo{Mode: transport.MailboxWatchIdle, Capabilities: []string{"IDLE"}}, block: true},
	}
	commands.watch = client
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	sequences := map[string][]uint64{}
	idleAccounts := map[string]bool{}
	err := commands.MailWatch(ctx, home, "", maildomain.WatchOptions{AllAccounts: true, OperationTimeout: time.Second}, func(event maildomain.WatchEvent) error {
		mu.Lock()
		sequences[event.AccountID] = append(sequences[event.AccountID], event.Sequence)
		if event.Type == maildomain.WatchEventIdle {
			idleAccounts[event.AccountID] = true
			if len(idleAccounts) == 2 {
				cancel()
			}
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sequences) != 2 || len(idleAccounts) != 2 {
		t.Fatalf("sequences=%v idle=%v", sequences, idleAccounts)
	}
	for accountID, values := range sequences {
		for index, sequence := range values {
			if sequence != uint64(index+1) {
				t.Fatalf("account %s sequences=%v", accountID, values)
			}
		}
	}
	if client.openCount() != 2 {
		t.Fatalf("watch opens=%d", client.openCount())
	}
}

func TestWatchReconnectDelayIsBoundedAndJittered(t *testing.T) {
	options := normalizeWatchOptions(maildomain.WatchOptions{ReconnectMin: time.Second, ReconnectMax: 8 * time.Second})
	for attempt := 1; attempt <= 20; attempt++ {
		delay := watchReconnectDelay(fmt.Sprintf("acct_%d", attempt), attempt, options, true)
		if delay < options.ReconnectMin/2 || delay > 9*time.Second {
			t.Fatalf("attempt=%d delay=%v", attempt, delay)
		}
	}
}
