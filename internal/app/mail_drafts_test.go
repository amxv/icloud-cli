package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

type fakeDraftAppender struct {
	calls    int
	requests []transport.MailboxAppendRequest
	messages [][]byte
	results  []transport.MailboxAppendResult
	errors   []error
}

func (appender *fakeDraftAppender) AppendMailbox(_ context.Context, _ transport.AuthRequest, request transport.MailboxAppendRequest) (transport.MailboxAppendResult, error) {
	appender.calls++
	content, err := io.ReadAll(request.Message)
	if err != nil {
		return transport.MailboxAppendResult{}, err
	}
	request.Message = nil
	appender.requests = append(appender.requests, request)
	appender.messages = append(appender.messages, content)
	index := appender.calls - 1
	if index < len(appender.errors) && appender.errors[index] != nil {
		return transport.MailboxAppendResult{}, appender.errors[index]
	}
	if index < len(appender.results) {
		return appender.results[index], nil
	}
	return transport.MailboxAppendResult{}, nil
}

type appendedDraftInboxClient struct {
	appender        *fakeDraftAppender
	calls           int
	failAfterAppend error
	uid             uint32
	uidValidity     uint32
}

func (client *appendedDraftInboxClient) FetchInboxPage(ctx context.Context, _ transport.AuthRequest, request transport.InboxFetchRequest, stager blob.Stager) (transport.InboxPage, error) {
	client.calls++
	uidValidity := client.uidValidity
	if uidValidity == 0 {
		uidValidity = 300
	}
	page := transport.InboxPage{Mailbox: request.Mailbox, UIDValidity: uidValidity, Bootstrap: request.Bootstrap}
	if client.calls == 1 || client.appender == nil || len(client.appender.messages) == 0 {
		return page, nil
	}
	if client.failAfterAppend != nil {
		return transport.InboxPage{}, client.failAfterAppend
	}
	uid := client.uid
	if uid == 0 {
		uid = 41
	}
	raw := client.appender.messages[len(client.appender.messages)-1]
	staged, err := stager.Stage(ctx, bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return transport.InboxPage{}, err
	}
	page.Messages = []transport.FetchedMessage{{
		UID: uid, Flags: []string{`\Draft`, `\Seen`}, InternalDate: time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC),
		RFC822Size: int64(len(raw)), Raw: staged,
	}}
	page.LastCommittedUID = uid
	page.LastScannedUID = uid
	return page, nil
}

func addDraftFolders(t *testing.T, home string) folder.Folder {
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
	now := time.Date(2026, 8, 7, 0, 30, 0, 0, time.UTC)
	counter := 550
	values, err := state.Folders().Reconcile(context.Background(), accountValue.ID, []folder.RemoteObservation{
		{Name: "Sent Messages", WireName: "Sent Messages", Kind: folder.KindSent, KindSource: "special_use", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 500, StatusKnown: true},
		{Name: "Drafts", WireName: "Drafts", Kind: folder.KindDrafts, KindSource: "special_use", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 300, StatusKnown: true},
	}, now, func() (string, error) {
		counter++
		return fmt.Sprintf("fld_0198d704-6c00-7000-8000-%012d", counter), nil
	})
	if err != nil || len(values) != 2 {
		t.Fatalf("add Sent/Drafts folders values=%+v err=%v", values, err)
	}
	for _, value := range values {
		if value.Kind == folder.KindDrafts {
			return value
		}
	}
	t.Fatal("Drafts role missing after reconcile")
	return folder.Folder{}
}

func setupDraftAccount(t *testing.T, commands accountCommands) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-draft-secret")
	addSendAccount(t, commands, home)
	addDraftFolders(t, home)
	return home
}

func setupUnclassifiedDraftAccount(t *testing.T, commands accountCommands) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-draft-secret")
	addSendAccount(t, commands, home)
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 7, 0, 45, 0, 0, time.UTC)
	nextID := 300
	values, err := state.Folders().Reconcile(context.Background(), accountValue.ID, []folder.RemoteObservation{
		{Name: "Sent Messages", WireName: "Sent Messages", Kind: folder.KindSent, KindSource: "special_use", Selectable: true,
			Subscription: folder.SubscriptionSubscribed, UIDValidity: 500, StatusKnown: true},
		{Name: "Drafts", WireName: "Drafts", Kind: folder.KindOther, Selectable: true,
			Subscription: folder.SubscriptionUnknown, UIDValidity: 300, StatusKnown: true},
	}, now, func() (string, error) {
		value := fmt.Sprintf("fld_0198d704-6c00-7000-8000-%012d", nextID)
		nextID++
		return value, nil
	})
	var drafts folder.Folder
	for _, value := range values {
		if value.Name == "Drafts" {
			drafts = value
			break
		}
	}
	if err != nil || drafts.ID == "" || drafts.Kind != folder.KindOther {
		t.Fatalf("add unclassified Drafts values=%+v err=%v", values, err)
	}
	return home
}

func emptyDraftSyncResult(mailbox string, uidValidity uint32) fakeInboxPageResult {
	return fakeInboxPageResult{page: transport.InboxPage{Mailbox: mailbox, UIDValidity: uidValidity, Bootstrap: true}}
}

func TestMailDraftCreateOfflinePreservesCanonicalContentBCCAndAttachment(t *testing.T) {
	commands, _ := newTestAccountCommands()
	home := setupUnclassifiedDraftAccount(t, commands)
	attachment := []byte("draft-attachment-token")
	attachmentPath := t.TempDir() + "/note.txt"
	if err := os.WriteFile(attachmentPath, attachment, 0o600); err != nil {
		t.Fatal(err)
	}
	accountValue, value, schemaVersion, err := commands.MailDraftCreate(context.Background(), home, "", maildomain.SendRequest{
		To:          []maildomain.Address{{Email: "primary@example.test"}},
		BCC:         []maildomain.Address{{Email: "hidden-bcc@example.test"}},
		Subject:     "Offline draft",
		TextBody:    "offline-draft-body-token",
		Attachments: []maildomain.OutboundAttachment{{SourcePath: attachmentPath, Filename: "note.txt", MediaType: "text/plain"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if schemaVersion != store.SchemaVersion || value.SyncState != maildomain.DraftLocal || value.Revision != 1 || value.Message.Direction != maildomain.DirectionOutbound || value.Message.Unread || len(value.Message.BCC) != 1 || value.Message.AttachmentCount != 1 {
		t.Fatalf("account=%+v draft=%+v schema=%d", accountValue, value, schemaVersion)
	}
	_, listed, more, _, err := commands.MailDraftList(context.Background(), home, "", 10)
	if err != nil || more || len(listed) != 1 || listed[0].ID != value.ID {
		t.Fatalf("list=%+v more=%v err=%v", listed, more, err)
	}
	_, shown, source, _, err := commands.MailDraftShow(context.Background(), home, "", value.ID)
	if err != nil || source == nil || shown.Message.Content == nil || len(shown.Message.Content.Attachments) != 1 {
		t.Fatalf("shown=%+v source=%T err=%v", shown, source, err)
	}
	var raw bytes.Buffer
	if err := source.WriteRaw(context.Background(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Bcc: <hidden-bcc@example.test>", "offline-draft-body-token", "note.txt", "ZHJhZnQtYXR0YWNobWVudC10b2tlbg=="} {
		if !strings.Contains(raw.String(), want) {
			t.Fatalf("raw draft missing %q: %q", want, raw.String())
		}
	}
}

func TestMailDraftSyncAPPENDUIDReplacesThenDeletesPrevious(t *testing.T) {
	commands, _ := newTestAccountCommands()
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{
		{Mailbox: "Drafts", UIDValidity: 300, UID: 10},
		{Mailbox: "Drafts", UIDValidity: 300, UID: 11},
	}}
	commands.appendMailbox = appender
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{
		emptyDraftSyncResult("Drafts", 300), emptyDraftSyncResult("Drafts", 300),
	}}
	locations := &fakeLocationClient{results: []transport.LocationMutationResult{{
		Operation: transport.LocationMutationDelete, ProviderMethod: "uid-store-expunge",
		SourceMailbox: "Drafts", SourceUIDValidity: 300, SourceUID: 10,
		SourceFlags: []string{`\Draft`, `\Seen`, `\Deleted`}, SourcePresent: false, ProviderMutationObserved: true,
	}}}
	commands.locations = locations
	home := setupDraftAccount(t, commands)
	_, created, _, err := commands.MailDraftCreate(context.Background(), home, "", maildomain.SendRequest{Subject: "v1", TextBody: "first draft version"})
	if err != nil {
		t.Fatal(err)
	}
	_, firstSummary, firstValues, _, err := commands.MailDraftSync(context.Background(), home, "", "", created.ID, 20)
	if err != nil || appender.calls != 1 || firstSummary.Appended != 1 || len(firstValues) != 1 || firstValues[0].SyncState != maildomain.DraftSynced || firstValues[0].RemoteLocationID == "" {
		t.Fatalf("first sync summary=%+v values=%+v append=%d err=%v", firstSummary, firstValues, appender.calls, err)
	}
	oldLocation := firstValues[0].RemoteLocationID
	_, _, firstSource, _, err := commands.MailDraftShow(context.Background(), home, "", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var firstRaw bytes.Buffer
	if err := firstSource.WriteRaw(context.Background(), &firstRaw); err != nil {
		t.Fatal(err)
	}
	_, updated, _, err := commands.MailDraftUpdate(context.Background(), home, "", created.ID, maildomain.SendRequest{Subject: "v2", TextBody: "replacement draft version"})
	if err != nil || updated.SyncState != maildomain.DraftLocal || updated.RemoteLocationID != "" || updated.PreviousLocationID != oldLocation {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{
		page: transport.InboxPage{Mailbox: "Drafts", UIDValidity: 300, LastCommittedUID: 10, LastScannedUID: 10,
			Messages: []transport.FetchedMessage{{UID: 10, Flags: []string{`\Draft`, `\Seen`}, InternalDate: time.Date(2026, 8, 7, 0, 35, 0, 0, time.UTC)}},
		}, raw: map[uint32]string{10: firstRaw.String()},
	}}}
	_, secondSummary, secondValues, _, err := commands.MailDraftSync(context.Background(), home, "", "", created.ID, 20)
	if err != nil || appender.calls != 2 || locations.calls != 1 || secondSummary.Appended != 1 || secondSummary.Cleaned != 1 || len(secondValues) != 1 {
		t.Fatalf("second sync summary=%+v values=%+v append=%d delete=%d err=%v", secondSummary, secondValues, appender.calls, locations.calls, err)
	}
	final := secondValues[0]
	if final.SyncState != maildomain.DraftSynced || final.PreviousLocationID != "" || final.RemoteLocationID == "" || final.RemoteLocationID == oldLocation || final.Revision != 2 || final.Message.Subject != "v2" {
		t.Fatalf("final draft=%+v", final)
	}
	if locations.requests[0].SourceUID != 10 || appender.requests[1].Mailbox != "Drafts" || !strings.Contains(strings.Join(appender.requests[1].Flags, ","), `\Draft`) {
		t.Fatalf("append/delete requests append=%+v delete=%+v", appender.requests, locations.requests)
	}
}

func TestMailDraftSyncWithoutAPPENDUIDReconcilesExactMessageID(t *testing.T) {
	commands, _ := newTestAccountCommands()
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "Drafts"}}}
	inbox := &appendedDraftInboxClient{appender: appender, uid: 41, uidValidity: 300}
	commands.appendMailbox = appender
	commands.mail = inbox
	home := setupDraftAccount(t, commands)
	_, created, _, err := commands.MailDraftCreate(context.Background(), home, "", maildomain.SendRequest{Subject: "No APPENDUID", TextBody: "exact-id-reconcile-token"})
	if err != nil {
		t.Fatal(err)
	}
	_, summary, values, _, err := commands.MailDraftSync(context.Background(), home, "", "", created.ID, 20)
	if err != nil || appender.calls != 1 || inbox.calls != 2 || summary.Appended != 1 || summary.Reconciled != 1 || len(values) != 1 {
		t.Fatalf("summary=%+v values=%+v append=%d fetch=%d err=%v", summary, values, appender.calls, inbox.calls, err)
	}
	if values[0].SyncState != maildomain.DraftSynced || values[0].RemoteLocationID == "" || values[0].Message.RFCMessageID != created.Message.RFCMessageID {
		t.Fatalf("reconciled draft=%+v created=%+v", values[0], created)
	}
}

func TestMailDraftInterruptedReplacementWithoutAPPENDUIDKeepsOldVersionRecoverable(t *testing.T) {
	commands, _ := newTestAccountCommands()
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{
		{Mailbox: "Drafts", UIDValidity: 300, UID: 10}, {Mailbox: "Drafts"},
	}}
	commands.appendMailbox = appender
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{emptyDraftSyncResult("Drafts", 300)}}
	home := setupDraftAccount(t, commands)
	_, created, _, err := commands.MailDraftCreate(context.Background(), home, "", maildomain.SendRequest{Subject: "Recoverable v1", TextBody: "recover-v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := commands.MailDraftSync(context.Background(), home, "", "Drafts", created.ID, 20); err != nil {
		t.Fatal(err)
	}
	_, first, _, _, err := commands.MailDraftShow(context.Background(), home, "", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldLocation := first.RemoteLocationID
	if _, _, _, err := commands.MailDraftUpdate(context.Background(), home, "", created.ID, maildomain.SendRequest{Subject: "Recoverable v2", TextBody: "recover-v2"}); err != nil {
		t.Fatal(err)
	}
	commands.mail = &appendedDraftInboxClient{appender: appender, uidValidity: 300, failAfterAppend: errors.New("synthetic reconciliation disconnect")}
	_, _, _, _, err = commands.MailDraftSync(context.Background(), home, "", "", created.ID, 20)
	if err == nil {
		t.Fatal("expected reconciliation failure")
	}
	_, after, _, _, showErr := commands.MailDraftShow(context.Background(), home, "", created.ID)
	if showErr != nil {
		t.Fatal(showErr)
	}
	if after.SyncState != maildomain.DraftReconcile || after.PreviousLocationID != oldLocation || after.RemoteLocationID != "" || len(after.Message.Locations) == 0 {
		t.Fatalf("recoverable draft=%+v", after)
	}
	if _, _, _, _, _, sendErr := commands.MailDraftSend(context.Background(), home, "", created.ID, ""); sendErr == nil || cli.NormalizeError(sendErr).Code != cli.ErrorConflict {
		t.Fatalf("unresolved draft send err=%v", sendErr)
	}
	if _, _, _, _, deleteErr := commands.MailDraftDelete(context.Background(), home, "", created.ID); deleteErr == nil || cli.NormalizeError(deleteErr).Code != cli.ErrorConflict {
		t.Fatalf("unresolved draft delete err=%v", deleteErr)
	}
	_, preserved, _, _, preserveErr := commands.MailDraftShow(context.Background(), home, "", created.ID)
	if preserveErr != nil || preserved.SyncState != maildomain.DraftReconcile || preserved.PreviousLocationID != oldLocation {
		t.Fatalf("unresolved recovery evidence lost draft=%+v err=%v", preserved, preserveErr)
	}
}

func TestMailDraftSyncAdoptsProviderNativeDraftAsOutboundAndNotUnread(t *testing.T) {
	commands, _ := newTestAccountCommands()
	raw := strings.Join([]string{
		"From: Owner <owner@icloud.com>", "To: teammate@example.test", "Bcc: native-hidden@example.test",
		"Date: Fri, 07 Aug 2026 01:00:00 +0000", "Message-ID: <native-draft@example.test>",
		"Subject: Native provider draft", "X-Synthetic-Draft-Meta: retained", "Content-Type: text/plain; charset=utf-8", "", "native-provider-draft-token",
	}, "\r\n")
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{
		page: transport.InboxPage{Mailbox: "Drafts", UIDValidity: 300, Bootstrap: true, LastCommittedUID: 7, LastScannedUID: 7,
			Messages: []transport.FetchedMessage{{UID: 7, Flags: []string{`\Draft`, `\Seen`}, InternalDate: time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC)}},
		}, raw: map[uint32]string{7: raw},
	}}}
	home := setupUnclassifiedDraftAccount(t, commands)
	_, summary, values, _, err := commands.MailDraftSync(context.Background(), home, "", "Drafts", "", 20)
	if err != nil || summary.Observed != 1 || len(values) != 1 {
		t.Fatalf("summary=%+v values=%+v err=%v", summary, values, err)
	}
	value := values[0]
	if value.SyncState != maildomain.DraftSynced || value.Message.Direction != maildomain.DirectionOutbound || value.Message.Unread || value.Message.RFCMessageID != "native-draft@example.test" || len(value.Message.BCC) != 1 {
		t.Fatalf("native draft=%+v", value)
	}
	if len(value.CustomHeaders) != 1 || value.CustomHeaders[0].Key != "X-Synthetic-Draft-Meta" || value.CustomHeaders[0].Value != "retained" {
		t.Fatalf("native custom headers=%+v", value.CustomHeaders)
	}
	_, _, preview, _, err := commands.MailDraftSendPreview(context.Background(), home, "", value.ID, "")
	if err != nil || len(preview.Headers) != 1 || preview.Headers[0] != value.CustomHeaders[0] {
		t.Fatalf("native draft preview headers=%+v err=%v", preview.Headers, err)
	}
}

func TestMailDraftSyncDoesNotAdoptOrdinaryMessageFromExplicitFallbackFolder(t *testing.T) {
	commands, _ := newTestAccountCommands()
	raw := strings.Join([]string{
		"From: teammate@example.test", "To: owner@icloud.com",
		"Date: Fri, 07 Aug 2026 01:10:00 +0000", "Message-ID: <ordinary-in-drafts@example.test>",
		"Subject: Ordinary message", "Content-Type: text/plain; charset=utf-8", "", "ordinary-message-token",
	}, "\r\n")
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{
		page: transport.InboxPage{Mailbox: "Drafts", UIDValidity: 300, Bootstrap: true, LastCommittedUID: 8, LastScannedUID: 8,
			Messages: []transport.FetchedMessage{{UID: 8, Flags: []string{`\Seen`}, InternalDate: time.Date(2026, 8, 7, 1, 10, 0, 0, time.UTC)}},
		}, raw: map[uint32]string{8: raw},
	}}}
	home := setupUnclassifiedDraftAccount(t, commands)
	_, summary, values, _, err := commands.MailDraftSync(context.Background(), home, "", "Drafts", "", 20)
	if err != nil || summary.Observed != 0 || len(values) != 0 {
		t.Fatalf("summary=%+v values=%+v err=%v", summary, values, err)
	}
}

func TestMailDraftSendIsIdempotentAndCleansProviderOnlyAfterProvenSent(t *testing.T) {
	commands, _ := newTestAccountCommands()
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "Drafts", UIDValidity: 300, UID: 10}}}
	commands.appendMailbox = appender
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{emptyDraftSyncResult("Drafts", 300)}}
	sender := &fakeSMTPSender{}
	sent := &fakeSentAppender{}
	commands.sender = sender
	commands.sent = sent
	locations := &fakeLocationClient{
		errors: []error{errors.New("synthetic provider draft cleanup failure"), nil},
		results: []transport.LocationMutationResult{{}, {
			Operation: transport.LocationMutationDelete, ProviderMethod: "uid-store-expunge", SourceMailbox: "Drafts",
			SourceUIDValidity: 300, SourceUID: 10, SourceFlags: []string{`\Draft`, `\Seen`, `\Deleted`}, SourcePresent: false, ProviderMutationObserved: true,
		}},
	}
	commands.locations = locations
	home := setupUnclassifiedDraftAccount(t, commands)
	_, created, _, err := commands.MailDraftCreate(context.Background(), home, "", maildomain.SendRequest{
		To: []maildomain.Address{{Email: "primary@example.test"}}, Subject: "Send once", TextBody: "send-once-draft-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := commands.MailDraftSync(context.Background(), home, "", "Drafts", created.ID, 20); err != nil {
		t.Fatal(err)
	}
	_, _, firstAttempt, firstReused, _, firstErr := commands.MailDraftSend(context.Background(), home, "", created.ID, "")
	if firstErr == nil || firstAttempt.Status != maildomain.OutboundSent || firstReused || sender.calls != 1 || sent.calls != 1 || locations.calls != 1 {
		t.Fatalf("first send attempt=%+v reused=%v smtp=%d sent=%d cleanup=%d err=%v", firstAttempt, firstReused, sender.calls, sent.calls, locations.calls, firstErr)
	}
	_, pending, _, _, err := commands.MailDraftShow(context.Background(), home, "", created.ID)
	if err != nil || pending.SyncState != maildomain.DraftCleanupPending || pending.OutboundAttemptID != firstAttempt.ID {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	_, _, secondAttempt, secondReused, _, secondErr := commands.MailDraftSend(context.Background(), home, "", created.ID, "")
	if secondErr != nil || secondAttempt.ID != firstAttempt.ID || !secondReused || sender.calls != 1 || sent.calls != 1 || locations.calls != 2 {
		t.Fatalf("second send attempt=%+v reused=%v smtp=%d sent=%d cleanup=%d err=%v", secondAttempt, secondReused, sender.calls, sent.calls, locations.calls, secondErr)
	}
	if _, _, _, _, err := commands.MailDraftShow(context.Background(), home, "", created.ID); err == nil {
		t.Fatal("draft still exists after idempotent cleanup retry")
	}
}

func TestMailDraftUnknownSendPreservesDraftAndProviderCopy(t *testing.T) {
	commands, _ := newTestAccountCommands()
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "Drafts", UIDValidity: 300, UID: 10}}}
	commands.appendMailbox = appender
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{emptyDraftSyncResult("Drafts", 300)}}
	commands.sender = &fakeSMTPSender{err: &transport.SMTPSendError{
		Outcome: transport.SMTPSendUnknown,
		Failure: transport.NewFailure(transport.StageSMTPAccept, transport.FailureUnavailable, true, errors.New("synthetic post-DATA disconnect")),
	}}
	locations := &fakeLocationClient{}
	commands.locations = locations
	home := setupDraftAccount(t, commands)
	_, created, _, err := commands.MailDraftCreate(context.Background(), home, "", maildomain.SendRequest{
		To: []maildomain.Address{{Email: "primary@example.test"}}, Subject: "Unknown send draft", TextBody: "unknown-draft-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := commands.MailDraftSync(context.Background(), home, "", "", created.ID, 20); err != nil {
		t.Fatal(err)
	}
	_, _, attempt, _, _, sendErr := commands.MailDraftSend(context.Background(), home, "", created.ID, "")
	if sendErr == nil || attempt.Status != maildomain.OutboundUnknown || locations.calls != 0 {
		t.Fatalf("attempt=%+v cleanup=%d err=%v", attempt, locations.calls, sendErr)
	}
	_, preserved, _, _, err := commands.MailDraftShow(context.Background(), home, "", created.ID)
	if err != nil || preserved.SyncState != maildomain.DraftSendUnknown || preserved.RemoteLocationID == "" || preserved.OutboundAttemptID != attempt.ID {
		t.Fatalf("preserved=%+v err=%v", preserved, err)
	}
}

func TestMailDraftDeadlineFailurePersistsAttemptAndPreservesProviderCopy(t *testing.T) {
	commands, _ := newTestAccountCommands()
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "Drafts", UIDValidity: 300, UID: 10}}}
	commands.appendMailbox = appender
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{emptyDraftSyncResult("Drafts", 300)}}
	sender := &deadlineSMTPSender{}
	commands.sender = sender
	locations := &fakeLocationClient{}
	commands.locations = locations
	home := setupDraftAccount(t, commands)
	_, created, _, err := commands.MailDraftCreate(context.Background(), home, "", maildomain.SendRequest{
		To: []maildomain.Address{{Email: "primary@example.test"}}, Subject: "Deadline draft", TextBody: "deadline-draft-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := commands.MailDraftSync(context.Background(), home, "", "", created.ID, 20); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sender.cancel = cancel
	_, _, attempt, _, _, sendErr := commands.MailDraftSend(ctx, home, "", created.ID, "")
	if sendErr == nil || attempt.Status != maildomain.OutboundFailed || sender.calls != 1 || locations.calls != 0 {
		t.Fatalf("attempt=%+v smtp=%d cleanup=%d err=%v", attempt, sender.calls, locations.calls, sendErr)
	}
	_, preserved, _, _, err := commands.MailDraftShow(context.Background(), home, "", created.ID)
	if err != nil || preserved.SyncState != maildomain.DraftSynced || preserved.RemoteLocationID == "" || preserved.OutboundAttemptID != attempt.ID {
		t.Fatalf("preserved=%+v err=%v", preserved, err)
	}
}

func TestMailDraftDeleteRemovesKnownProviderVersionsThenLocalState(t *testing.T) {
	commands, _ := newTestAccountCommands()
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "Drafts", UIDValidity: 300, UID: 10}}}
	commands.appendMailbox = appender
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{emptyDraftSyncResult("Drafts", 300)}}
	commands.locations = &fakeLocationClient{results: []transport.LocationMutationResult{{
		Operation: transport.LocationMutationDelete, ProviderMethod: "uid-store-expunge", SourceMailbox: "Drafts",
		SourceUIDValidity: 300, SourceUID: 10, SourceFlags: []string{`\Draft`, `\Seen`, `\Deleted`}, SourcePresent: false, ProviderMutationObserved: true,
	}}}
	home := setupUnclassifiedDraftAccount(t, commands)
	_, created, _, err := commands.MailDraftCreate(context.Background(), home, "", maildomain.SendRequest{Subject: "Delete me", TextBody: "delete-draft-token"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := commands.MailDraftSync(context.Background(), home, "", "Drafts", created.ID, 20); err != nil {
		t.Fatal(err)
	}
	_, _, providerCopies, _, err := commands.MailDraftDelete(context.Background(), home, "", created.ID)
	if err != nil || providerCopies != 1 {
		t.Fatalf("providerCopies=%d err=%v", providerCopies, err)
	}
	if _, _, _, _, err := commands.MailDraftShow(context.Background(), home, "", created.ID); err == nil {
		t.Fatal("deleted draft remains readable")
	}
}

func TestDraftReconnectHelpersUseExactSpecialUseRole(t *testing.T) {
	commands, _ := newTestAccountCommands()
	home := setupDraftAccount(t, commands)
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	value, err := state.Folders().ResolveKind(context.Background(), accountValue.ID, folder.KindDrafts)
	if err != nil || value.Name != "Drafts" || value.Kind != folder.KindDrafts {
		t.Fatalf("draft role=%+v err=%v", value, err)
	}
	_ = fmt.Sprint(value.ID)
}

func TestResolveDraftsFolderAllowsExplicitUnclassifiedMailboxWithoutReclassifyingIt(t *testing.T) {
	commands, _ := newTestAccountCommands()
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-draft-secret")
	addSendAccount(t, commands, home)
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 7, 1, 30, 0, 0, time.UTC)
	values, err := state.Folders().Reconcile(context.Background(), accountValue.ID, []folder.RemoteObservation{{
		Name: "Provider Working Copy", WireName: "Provider Working Copy", Kind: folder.KindOther,
		Selectable: true, Subscription: folder.SubscriptionUnknown, UIDValidity: 700, StatusKnown: true,
	}}, now, func() (string, error) { return "fld_0198d704-6c00-7000-8000-000000000700", nil })
	if err != nil || len(values) != 1 {
		t.Fatalf("reconcile values=%+v err=%v", values, err)
	}
	if _, err := resolveDraftsFolder(context.Background(), state, accountValue.ID, ""); !errors.Is(err, folder.ErrNotFound) {
		t.Fatalf("implicit role resolution err=%v", err)
	}
	selected, err := resolveDraftsFolder(context.Background(), state, accountValue.ID, values[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != values[0].ID || selected.Kind != folder.KindOther || selected.KindSource != "" {
		t.Fatalf("explicit draft folder = %+v", selected)
	}
}

func TestResolveDraftsFolderRejectsExplicitOverrideWhenProviderAdvertisesDraftsRole(t *testing.T) {
	commands, _ := newTestAccountCommands()
	home := setupDraftAccount(t, commands)
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 7, 1, 45, 0, 0, time.UTC)
	values, err := state.Folders().Reconcile(context.Background(), accountValue.ID, []folder.RemoteObservation{{
		Name: "Ordinary", WireName: "Ordinary", Kind: folder.KindOther,
		Selectable: true, Subscription: folder.SubscriptionUnknown, UIDValidity: 701, StatusKnown: true,
	}, {
		Name: "Drafts", WireName: "Drafts", Kind: folder.KindDrafts, KindSource: "special_use",
		Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 300, StatusKnown: true,
	}}, now, func() (string, error) { return "fld_0198d704-6c00-7000-8000-000000000701", nil })
	if err != nil {
		t.Fatalf("reconcile values=%+v err=%v", values, err)
	}
	var ordinary folder.Folder
	for _, value := range values {
		if value.Name == "Ordinary" {
			ordinary = value
			break
		}
	}
	if ordinary.ID == "" {
		t.Fatalf("ordinary folder missing after reconcile: %+v", values)
	}
	if _, err := resolveDraftsFolder(context.Background(), state, accountValue.ID, ordinary.ID); !errors.Is(err, errDraftFolderRoleConflict) {
		t.Fatalf("override err=%v", err)
	}
}

func TestMailDraftOperationLockRejectsConcurrentUpdateSendAndDelete(t *testing.T) {
	commands, _ := newTestAccountCommands()
	home := setupDraftAccount(t, commands)
	_, created, _, err := commands.MailDraftCreate(context.Background(), home, "", maildomain.SendRequest{Subject: "Locked", TextBody: "draft-lock-token"})
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := acquireDraftOperationLock(home, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, _, _, err := commands.MailDraftUpdate(context.Background(), home, "", created.ID, maildomain.SendRequest{Subject: "Blocked", TextBody: "blocked-update"}); err == nil || cli.NormalizeError(err).Code != cli.ErrorConflict {
		t.Fatalf("concurrent update err=%v", err)
	}
	if _, _, _, _, _, err := commands.MailDraftSend(context.Background(), home, "", created.ID, ""); err == nil || cli.NormalizeError(err).Code != cli.ErrorConflict {
		t.Fatalf("concurrent send err=%v", err)
	}
	if _, _, _, _, err := commands.MailDraftDelete(context.Background(), home, "", created.ID); err == nil || cli.NormalizeError(err).Code != cli.ErrorConflict {
		t.Fatalf("concurrent delete err=%v", err)
	}
}
