package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

type messageRewriteReplacementSyncClient struct {
	appender    *fakeDraftAppender
	uid         uint32
	uidValidity uint32
	calls       int
	err         error
}

func (client *messageRewriteReplacementSyncClient) FetchInboxPage(ctx context.Context, _ transport.AuthRequest, request transport.InboxFetchRequest, stager blob.Stager) (transport.InboxPage, error) {
	client.calls++
	if client.err != nil {
		return transport.InboxPage{}, client.err
	}
	uidValidity := client.uidValidity
	if uidValidity == 0 {
		uidValidity = 100
	}
	page := transport.InboxPage{Mailbox: request.Mailbox, UIDValidity: uidValidity, Bootstrap: request.Bootstrap}
	if client.appender == nil || len(client.appender.messages) == 0 {
		return page, nil
	}
	uid := client.uid
	if uid == 0 {
		uid = 90
	}
	raw := client.appender.messages[len(client.appender.messages)-1]
	staged, err := stager.Stage(ctx, bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return transport.InboxPage{}, err
	}
	page.Messages = []transport.FetchedMessage{{UID: uid, InternalDate: time.Date(2026, 8, 10, 17, 0, 0, 0, time.UTC), RFC822Size: int64(len(raw)), Raw: staged}}
	page.LastCommittedUID = uid
	page.LastScannedUID = uid
	return page, nil
}

type messageRewriteRemoteProbeClient struct {
	requests []transport.RemoteFetchRequest
	absent   bool
}

func (client *messageRewriteRemoteProbeClient) SearchRemote(context.Context, transport.AuthRequest, transport.RemoteSearchRequest) (transport.RemoteSearchPage, error) {
	return transport.RemoteSearchPage{}, errors.New("unexpected remote search")
}

func (client *messageRewriteRemoteProbeClient) FetchRemoteSummary(_ context.Context, _ transport.AuthRequest, request transport.RemoteFetchRequest) (transport.RemoteSummaryResult, error) {
	client.requests = append(client.requests, request)
	if client.absent {
		return transport.RemoteSummaryResult{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, false, transport.ErrRemoteMessageUnavailable)
	}
	return transport.RemoteSummaryResult{Mailbox: request.Mailbox, UIDValidity: request.ExpectedUIDValidity, Message: transport.RemoteMessageSummary{UID: request.UID}}, nil
}

func (client *messageRewriteRemoteProbeClient) FetchRemoteMessage(context.Context, transport.AuthRequest, transport.RemoteFetchRequest, blob.Stager) (transport.RemoteFetchResult, error) {
	return transport.RemoteFetchResult{}, errors.New("unexpected remote fetch")
}

type messageRewriteFixture struct {
	message    maildomain.Message
	attachment maildomain.Attachment
	folder     folder.Folder
	threadID   string
}

func seedMessageRewriteFixture(t *testing.T, commands *accountCommands, home, envName string, uid uint32) messageRewriteFixture {
	t.Helper()
	commands.folders = oneInboxFolderClient()
	raw, _, _ := multipartMessageFixture()
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{
		page: transport.InboxPage{
			Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: uid, LastScannedUID: uid, Bootstrap: true,
			Messages: []transport.FetchedMessage{{UID: uid, ModSequence: uint64(1000 + uid), InternalDate: time.Date(2026, 8, 10, 16, 30, 0, 0, time.UTC)}},
		}, raw: map[uint32]string{uid: raw},
	}}}
	addAndDiscoverInbox(t, *commands, home, envName)
	code, stdout, stderr := runWithAccountCommands(t, *commands, "", "--home", home, "mail", "sync", "--folder", "inbox", "--limit", "1")
	if code != 0 || stderr != "" {
		t.Fatalf("seed rewrite sync code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	folderValue, err := state.Folders().Resolve(context.Background(), accountValue.ID, "inbox")
	if err != nil {
		t.Fatal(err)
	}
	message, err := state.Mail().MessageByProviderLocation(context.Background(), accountValue.ID, folderValue.ID, 100, uid)
	if err != nil {
		t.Fatal(err)
	}
	if message.Content == nil || len(message.Content.Attachments) != 1 {
		t.Fatalf("seed rewrite message=%+v", message)
	}
	threadID, err := state.Threads().Resolve(context.Background(), accountValue.ID, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	return messageRewriteFixture{message: message, attachment: message.Content.Attachments[0], folder: folderValue, threadID: threadID}
}

func addMessageRewriteUnrelatedArchiveCopy(t *testing.T, home string, fixture messageRewriteFixture, uid uint32) folder.Folder {
	t.Helper()
	ctx := context.Background()
	state, err := openState(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	next := 0
	folders, err := state.Folders().Reconcile(ctx, accountValue.ID, []folder.RemoteObservation{
		{Name: "INBOX", WireName: "INBOX", Kind: folder.KindInbox, KindSource: "inbox", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 100, StatusKnown: true},
		{Name: "Archive", WireName: "Archive", Kind: folder.KindArchive, KindSource: "attribute", Selectable: true, Subscription: folder.SubscriptionSubscribed, UIDValidity: 200, StatusKnown: true},
	}, time.Date(2026, 8, 10, 16, 45, 0, 0, time.UTC), func() (string, error) {
		next++
		return fmt.Sprintf("fld_messageRewrite_archive_%d", next), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var archive folder.Folder
	for _, value := range folders {
		if value.Kind == folder.KindArchive {
			archive = value
			break
		}
	}
	if archive.ID == "" {
		t.Fatal("messageRewrite archive folder was not reconciled")
	}
	if len(fixture.message.Locations) == 0 {
		t.Fatal("messageRewrite fixture has no source location")
	}
	source := fixture.message.Locations[0]
	internal := time.Date(2026, 8, 10, 16, 46, 0, 0, time.UTC)
	record := maildomain.IngestRecord{
		MessageID: "msg_messageRewrite_archive_copy", LocationID: "loc_messageRewrite_archive_copy",
		AccountID: accountValue.ID, FolderID: archive.ID, UIDValidity: archive.UIDValidity, UID: uid,
		Flags: []string{`\Seen`}, InternalDate: internal, RFC822Size: source.RFC822Size,
		RawContentHash: source.RawContentHash, RawStorageKey: source.RawStorageKey, RawByteSize: source.RawByteSize,
		Parsed: maildomain.ParsedMessage{
			RFCMessageID: fixture.message.RFCMessageID, InReplyTo: fixture.message.InReplyTo, References: append([]string(nil), fixture.message.References...),
			From: fixture.message.From, ReplyTo: fixture.message.ReplyTo, To: append([]maildomain.Address(nil), fixture.message.To...), CC: append([]maildomain.Address(nil), fixture.message.CC...),
			Subject: fixture.message.Subject, Snippet: fixture.message.Snippet, HeaderDate: fixture.message.HeaderDate, AttachmentCount: fixture.message.AttachmentCount,
		},
	}
	if _, err := state.Mail().CommitInboxPage(ctx, maildomain.CommitPage{
		CheckpointID: "chk_messageRewrite_archive_copy", AccountID: accountValue.ID, FolderID: archive.ID,
		UIDValidity: archive.UIDValidity, Bootstrap: true, LastCommittedUID: uid, LastScannedUID: uid,
		AttemptedAt: internal, Records: []maildomain.IngestRecord{record},
	}); err != nil {
		t.Fatal(err)
	}
	message, err := state.Mail().Message(ctx, accountValue.ID, fixture.message.ID)
	if err != nil || len(message.Locations) != 2 {
		t.Fatalf("messageRewrite archive copy message=%+v err=%v", message, err)
	}
	return archive
}

func TestAttachmentRemoveReplacesExactCopyAndReplaysWithoutProviderWork(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MESSAGE_REWRITE_REWRITE_PASSWORD", "synthetic-messageRewrite-rewrite-secret")
	commands, _ := newTestAccountCommands()
	fixture := seedMessageRewriteFixture(t, &commands, home, "ICLOUD_MESSAGE_REWRITE_REWRITE_PASSWORD", 70)
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "INBOX", UIDValidity: 100, UID: 71}}}
	locations := &fakeLocationClient{results: []transport.LocationMutationResult{{
		Operation: transport.LocationMutationDelete, ProviderMethod: "uid_expunge",
		SourceMailbox: "INBOX", SourceUIDValidity: 100, SourceUID: 70,
		SourcePresent: false, ProviderMutationObserved: true,
	}}}
	commands.appendMailbox = appender
	commands.locations = locations

	dryArgs := []string{"--home", home, "mail", "attachments", "remove", fixture.message.ID, "--attachment", fixture.attachment.ID, "--dry-run"}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", dryArgs...)
	if code != 0 || stderr != "" || appender.calls != 0 || locations.calls != 0 || !compactContainsAll(stdout,
		"remove-attachment\n"+fixture.attachment.ID, "before_attachments=1", "after_attachments=0",
		"imap_contacted=false", "intent_created=false", "dry_run=true") {
		t.Fatalf("rewrite dry-run code=%d stdout=%q stderr=%q append=%d delete=%d", code, stdout, stderr, appender.calls, locations.calls)
	}

	args := []string{
		"--home", home, "mail", "attachments", "remove", fixture.message.ID,
		"--attachment", fixture.attachment.ID, "--idempotency-key", "messageRewrite-rewrite-success", "--yes", "--json",
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || appender.calls != 1 || locations.calls != 1 {
		t.Fatalf("rewrite code=%d stdout=%q stderr=%q append=%d delete=%d", code, stdout, stderr, appender.calls, locations.calls)
	}
	var envelope struct {
		Data struct {
			MessageID string                    `json:"message_id"`
			Rewrite   maildomain.MessageRewrite `json:"rewrite"`
			Count     int                       `json:"attachment_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.MessageID != fixture.message.ID || envelope.Data.Rewrite.Status != maildomain.MessageRewriteConfirmed || envelope.Data.Count != 0 || envelope.Data.Rewrite.SourcePresent || envelope.Data.Rewrite.NewUID != 71 {
		t.Fatalf("rewrite result=%+v", envelope.Data)
	}
	if len(appender.messages) != 1 {
		t.Fatalf("replacement raw count=%d", len(appender.messages))
	}
	rewritten, err := maildomain.ParseNormalizedContext(context.Background(), strings.NewReader(string(appender.messages[0])))
	if err != nil || len(rewritten.Attachments) != 0 || rewritten.Preview.AttachmentCount != 0 || rewritten.Preview.RFCMessageID != fixture.message.RFCMessageID || !strings.Contains(rewritten.SearchText, "Complete plain text body") {
		t.Fatalf("replacement parse=%+v err=%v", rewritten, err)
	}
	if len(locations.requests) != 1 || locations.requests[0].Operation != transport.LocationMutationDelete || locations.requests[0].SourceMailbox != "INBOX" || locations.requests[0].ExpectedSourceUIDValidity != 100 || locations.requests[0].SourceUID != 70 || locations.requests[0].ExpectedSourceModSequence != 1070 {
		t.Fatalf("exact delete request=%+v", locations.requests)
	}

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	message, err := state.Mail().Message(context.Background(), accountValue.ID, fixture.message.ID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	threadID, threadErr := state.Threads().Resolve(context.Background(), accountValue.ID, fixture.message.ID)
	threadPage, pageErr := state.Threads().Show(context.Background(), accountValue.ID, threadID, "", 20)
	state.Close()
	if threadErr != nil || pageErr != nil || threadID != fixture.threadID || message.ID != fixture.message.ID || message.RFCMessageID != fixture.message.RFCMessageID || message.AttachmentCount != 0 || message.Content == nil || len(message.Content.Attachments) != 0 || threadPage.Thread.AttachmentCount != 0 {
		t.Fatalf("stable rewritten message=%+v thread=%+v threadErr=%v pageErr=%v", message, threadPage.Thread, threadErr, pageErr)
	}
	var oldPresent, newPresent bool
	for _, location := range message.Locations {
		if location.UID == 70 {
			oldPresent = location.Present
		}
		if location.UID == 71 {
			newPresent = location.Present && strings.EqualFold(location.RawContentHash, envelope.Data.Rewrite.NewRawContentHash)
		}
	}
	if oldPresent || !newPresent {
		t.Fatalf("rewritten locations=%+v", message.Locations)
	}

	code, searchOut, searchErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "Complete plain text body", "--limit", "5", "--json")
	if code != 0 || searchErr != "" || !strings.Contains(searchOut, fixture.message.ID) {
		t.Fatalf("rewritten FTS code=%d stdout=%q stderr=%q", code, searchOut, searchErr)
	}

	code, replayOut, replayErr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || replayErr != "" || appender.calls != 1 || locations.calls != 1 || !strings.Contains(replayOut, `"status":"confirmed"`) {
		t.Fatalf("rewrite replay code=%d stdout=%q stderr=%q append=%d delete=%d", code, replayOut, replayErr, appender.calls, locations.calls)
	}
}

func TestAttachmentRemoveRequiresStableRetryIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MESSAGE_REWRITE_REWRITE_KEY_PASSWORD", "synthetic-messageRewrite-rewrite-key-secret")
	commands, _ := newTestAccountCommands()
	fixture := seedMessageRewriteFixture(t, &commands, home, "ICLOUD_MESSAGE_REWRITE_REWRITE_KEY_PASSWORD", 72)
	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "attachments", "remove", fixture.message.ID, "--attachment", fixture.attachment.ID, "--yes")
	if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, "idempotency") {
		t.Fatalf("missing retry identity code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestProtectedMIMERewriteFailsClosed(t *testing.T) {
	for _, mediaType := range []string{
		"multipart/signed", "multipart/encrypted", "application/pkcs7-mime", "application/pkcs7-signature",
	} {
		t.Run(mediaType, func(t *testing.T) {
			content := &maildomain.NormalizedContent{Parts: []maildomain.MIMEPart{{MediaType: mediaType}}}
			if !rewriteHasProtectedMIME(content) {
				t.Fatalf("protected MIME %q was not fenced", mediaType)
			}
		})
	}
	if rewriteHasProtectedMIME(&maildomain.NormalizedContent{Parts: []maildomain.MIMEPart{{MediaType: "message/rfc822"}, {MediaType: "image/png"}}}) {
		t.Fatal("ordinary nested MIME was incorrectly fenced")
	}
}

func TestMultipleProviderLocationsRequireSelectionAndPreserveUnrelatedCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MESSAGE_REWRITE_MULTICOPY_PASSWORD", "synthetic-messageRewrite-multicopy-secret")
	commands, _ := newTestAccountCommands()
	fixture := seedMessageRewriteFixture(t, &commands, home, "ICLOUD_MESSAGE_REWRITE_MULTICOPY_PASSWORD", 74)
	archive := addMessageRewriteUnrelatedArchiveCopy(t, home, fixture, 9)
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "INBOX", UIDValidity: 100, UID: 75}}}
	locations := &fakeLocationClient{results: []transport.LocationMutationResult{{
		Operation: transport.LocationMutationDelete, ProviderMethod: "uid_expunge",
		SourceMailbox: "INBOX", SourceUIDValidity: 100, SourceUID: 74, SourcePresent: false, ProviderMutationObserved: true,
	}}}
	commands.appendMailbox = appender
	commands.locations = locations

	base := []string{"--home", home, "mail", "attachments", "remove", fixture.message.ID, "--attachment", fixture.attachment.ID}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", append(append([]string(nil), base...), "--dry-run")...)
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "multiple eligible locations") || appender.calls != 0 || locations.calls != 0 {
		t.Fatalf("multi-copy ambiguity code=%d stdout=%q stderr=%q append=%d delete=%d", code, stdout, stderr, appender.calls, locations.calls)
	}

	selected := append(append([]string(nil), base...), "--folder", "inbox", "--idempotency-key", "messageRewrite-multicopy", "--yes", "--json")
	code, stdout, stderr = runWithAccountCommands(t, commands, "", selected...)
	if code != 0 || stderr != "" || appender.calls != 1 || locations.calls != 1 {
		t.Fatalf("multi-copy rewrite code=%d stdout=%q stderr=%q append=%d delete=%d", code, stdout, stderr, appender.calls, locations.calls)
	}

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	message, messageErr := state.Mail().Message(context.Background(), accountValue.ID, fixture.message.ID)
	state.Close()
	if messageErr != nil || message.AttachmentCount != 0 || message.Content == nil || len(message.Content.Attachments) != 0 {
		t.Fatalf("multi-copy canonical message=%+v err=%v", message, messageErr)
	}
	var oldInboxPresent, replacementPresent, archivePresent bool
	for _, location := range message.Locations {
		switch {
		case location.FolderID == fixture.folder.ID && location.UID == 74:
			oldInboxPresent = location.Present
		case location.FolderID == fixture.folder.ID && location.UID == 75:
			replacementPresent = location.Present && location.RawContentHash != fixture.message.Locations[0].RawContentHash
		case location.FolderID == archive.ID && location.UID == 9:
			archivePresent = location.Present && strings.EqualFold(location.RawContentHash, fixture.message.Locations[0].RawContentHash)
		}
	}
	if oldInboxPresent || !replacementPresent || !archivePresent {
		t.Fatalf("multi-copy locations=%+v", message.Locations)
	}
}

func TestInteractiveCancellationCreatesNoIntentOrProviderTraffic(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MESSAGE_REWRITE_CANCEL_PASSWORD", "synthetic-messageRewrite-cancel-secret")
	commands, _ := newTestAccountCommands()
	fixture := seedMessageRewriteFixture(t, &commands, home, "ICLOUD_MESSAGE_REWRITE_CANCEL_PASSWORD", 76)
	appender := &fakeDraftAppender{}
	locations := &fakeLocationClient{}
	commands.appendMailbox = appender
	commands.locations = locations
	args := []string{
		"--home", home, "mail", "attachments", "remove", fixture.message.ID,
		"--attachment", fixture.attachment.ID, "--idempotency-key", "messageRewrite-cancel",
	}
	code, stdout, stderr := runWithAccountCommandsReader(commands, testTerminalReader{Reader: strings.NewReader("n\n")}, args...)
	if code != cli.ExitSafety || !strings.Contains(stdout, "attachment-rewrite-review") || !strings.Contains(stderr, "Replace the reviewed provider message") || appender.calls != 0 || locations.calls != 0 {
		t.Fatalf("interactive cancel code=%d stdout=%q stderr=%q append=%d delete=%d", code, stdout, stderr, appender.calls, locations.calls)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	_, err = state.MessageRewrites().ByIdempotency(context.Background(), accountValue.ID, "messageRewrite-cancel")
	state.Close()
	if !errors.Is(err, maildomain.ErrMessageRewriteNotFound) {
		t.Fatalf("canceled rewrite lookup err=%v", err)
	}
}

func TestAppendAmbiguityReconcilesWithoutSecondAppend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MESSAGE_REWRITE_APPEND_AMBIG_PASSWORD", "synthetic-messageRewrite-append-ambig-secret")
	commands, _ := newTestAccountCommands()
	fixture := seedMessageRewriteFixture(t, &commands, home, "ICLOUD_MESSAGE_REWRITE_APPEND_AMBIG_PASSWORD", 80)
	appender := &fakeDraftAppender{errors: []error{transport.NewFailure(transport.StageIMAPAppend, transport.FailureUnavailable, true, errors.New("synthetic disconnect after append"))}}
	locations := &fakeLocationClient{results: []transport.LocationMutationResult{{
		Operation: transport.LocationMutationDelete, ProviderMethod: "uid_expunge",
		SourceMailbox: "INBOX", SourceUIDValidity: 100, SourceUID: 80, SourcePresent: false, ProviderMutationObserved: true,
	}}}
	commands.appendMailbox = appender
	commands.locations = locations
	commands.mail = &messageRewriteReplacementSyncClient{err: transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, true, errors.New("synthetic reconciliation unavailable"))}
	args := []string{"--home", home, "mail", "attachments", "remove", fixture.message.ID, "--attachment", fixture.attachment.ID, "--idempotency-key", "messageRewrite-append-ambiguous", "--yes"}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != cli.ExitUnavailable || stdout != "" || !strings.Contains(stderr, "may have been accepted") || appender.calls != 1 || locations.calls != 0 {
		t.Fatalf("ambiguous append code=%d stdout=%q stderr=%q append=%d delete=%d", code, stdout, stderr, appender.calls, locations.calls)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	rewrite, err := state.MessageRewrites().ByIdempotency(context.Background(), accountValue.ID, "messageRewrite-append-ambiguous")
	state.Close()
	if err != nil || rewrite.Status != maildomain.MessageRewriteAppendUnresolved || !rewrite.ReconciliationRequired {
		t.Fatalf("ambiguous append journal=%+v err=%v", rewrite, err)
	}

	reconciler := &messageRewriteReplacementSyncClient{appender: appender, uid: 81, uidValidity: 100}
	commands.mail = reconciler
	code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || appender.calls != 1 || locations.calls != 1 || reconciler.calls != 1 || !strings.Contains(stdout, "status=confirmed") {
		state, _ := openState(context.Background(), home)
		if state != nil {
			acct, _ := state.Accounts().ResolveSelected(context.Background(), "")
			if observed, observedErr := state.Mail().MessageByProviderLocation(context.Background(), acct.ID, fixture.folder.ID, 100, 81); observedErr == nil {
				t.Logf("observed replacement message=%s rfc=%s locations=%+v", observed.ID, observed.RFCMessageID, observed.Locations)
			} else {
				t.Logf("replacement lookup err=%v", observedErr)
			}
			state.Close()
		}
		t.Fatalf("append reconciliation code=%d stdout=%q stderr=%q append=%d delete=%d sync=%d", code, stdout, stderr, appender.calls, locations.calls, reconciler.calls)
	}
}

func TestMissingAppendUIDReconcilesReplacementBeforeDelete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MESSAGE_REWRITE_NO_APPEND_UID_PASSWORD", "synthetic-messageRewrite-no-append-uid-secret")
	commands, _ := newTestAccountCommands()
	fixture := seedMessageRewriteFixture(t, &commands, home, "ICLOUD_MESSAGE_REWRITE_NO_APPEND_UID_PASSWORD", 82)
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "INBOX"}}}
	reconciler := &messageRewriteReplacementSyncClient{appender: appender, uid: 83, uidValidity: 100}
	locations := &fakeLocationClient{results: []transport.LocationMutationResult{{
		Operation: transport.LocationMutationDelete, ProviderMethod: "uid_expunge",
		SourceMailbox: "INBOX", SourceUIDValidity: 100, SourceUID: 82, SourcePresent: false, ProviderMutationObserved: true,
	}}}
	commands.appendMailbox = appender
	commands.mail = reconciler
	commands.locations = locations
	args := []string{"--home", home, "mail", "attachments", "remove", fixture.message.ID, "--attachment", fixture.attachment.ID, "--idempotency-key", "messageRewrite-no-appenduid", "--yes", "--json"}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || appender.calls != 1 || reconciler.calls != 1 || locations.calls != 1 || !strings.Contains(stdout, `"new_uid":83`) || !strings.Contains(stdout, `"status":"confirmed"`) {
		state, _ := openState(context.Background(), home)
		if state != nil {
			acct, _ := state.Accounts().ResolveSelected(context.Background(), "")
			if observed, observedErr := state.Mail().MessageByProviderLocation(context.Background(), acct.ID, fixture.folder.ID, 100, 83); observedErr == nil {
				t.Logf("observed missing-appenduid replacement message=%s rfc=%s locations=%+v", observed.ID, observed.RFCMessageID, observed.Locations)
			} else {
				t.Logf("missing-appenduid replacement lookup err=%v", observedErr)
			}
			state.Close()
		}
		t.Fatalf("missing APPENDUID code=%d stdout=%q stderr=%q append=%d sync=%d delete=%d", code, stdout, stderr, appender.calls, reconciler.calls, locations.calls)
	}
}

func TestDeleteAmbiguityProbesAbsenceBeforeFinalizationWithoutSecondDelete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MESSAGE_REWRITE_DELETE_AMBIG_PASSWORD", "synthetic-messageRewrite-delete-ambig-secret")
	commands, _ := newTestAccountCommands()
	fixture := seedMessageRewriteFixture(t, &commands, home, "ICLOUD_MESSAGE_REWRITE_DELETE_AMBIG_PASSWORD", 84)
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "INBOX", UIDValidity: 100, UID: 85}}}
	locations := &fakeLocationClient{
		results: []transport.LocationMutationResult{{
			Operation: transport.LocationMutationDelete, ProviderMethod: "uid_expunge",
			SourceMailbox: "INBOX", SourceUIDValidity: 100, SourceUID: 84, SourcePresent: false,
			ProviderMutationObserved: true, ReconciliationRequired: true,
		}},
		errors: []error{transport.NewFailure(transport.StageIMAPExpunge, transport.FailureUnavailable, true, errors.New("synthetic disconnect after expunge"))},
	}
	commands.appendMailbox = appender
	commands.locations = locations
	args := []string{"--home", home, "mail", "attachments", "remove", fixture.message.ID, "--attachment", fixture.attachment.ID, "--idempotency-key", "messageRewrite-delete-ambiguous", "--yes"}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != cli.ExitUnavailable || stdout != "" || appender.calls != 1 || locations.calls != 1 {
		t.Fatalf("ambiguous delete code=%d stdout=%q stderr=%q append=%d delete=%d", code, stdout, stderr, appender.calls, locations.calls)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	rewrite, err := state.MessageRewrites().ByIdempotency(context.Background(), accountValue.ID, "messageRewrite-delete-ambiguous")
	message, messageErr := state.Mail().Message(context.Background(), accountValue.ID, fixture.message.ID)
	state.Close()
	if err != nil || rewrite.Status != maildomain.MessageRewriteDeleteUnresolved || messageErr != nil || message.AttachmentCount != 1 || message.ContentLocationID != fixture.message.ContentLocationID {
		t.Fatalf("ambiguous delete journal=%+v message=%+v err=%v messageErr=%v", rewrite, message, err, messageErr)
	}
	var sourceStillPresent, replacementAlreadyPresent bool
	for _, location := range message.Locations {
		if location.UID == 84 {
			sourceStillPresent = location.Present && strings.EqualFold(location.RawContentHash, rewrite.OldRawContentHash)
		}
		if location.UID == 85 {
			replacementAlreadyPresent = location.Present && strings.EqualFold(location.RawContentHash, rewrite.NewRawContentHash)
		}
	}
	if !sourceStillPresent || !replacementAlreadyPresent {
		t.Fatalf("ambiguous delete switched canonical state too early: %+v", message.Locations)
	}
	probe := &messageRewriteRemoteProbeClient{absent: true}
	commands.remote = probe
	code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || appender.calls != 1 || locations.calls != 1 || len(probe.requests) != 1 || probe.requests[0].UID != 84 || probe.requests[0].ExpectedUIDValidity != 100 || !strings.Contains(stdout, "status=confirmed") {
		t.Fatalf("delete reconciliation code=%d stdout=%q stderr=%q append=%d delete=%d probes=%+v", code, stdout, stderr, appender.calls, locations.calls, probe.requests)
	}
}

func TestDefinitePreAppendFailureCannotDeleteOrReplaySameRewrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MESSAGE_REWRITE_PREAPPEND_PASSWORD", "synthetic-messageRewrite-preappend-secret")
	commands, _ := newTestAccountCommands()
	fixture := seedMessageRewriteFixture(t, &commands, home, "ICLOUD_MESSAGE_REWRITE_PREAPPEND_PASSWORD", 86)
	authFailure := transport.NewFailure(transport.StageIMAPAuth, transport.FailureAuthentication, false, errors.New("synthetic auth failure"))
	appender := &fakeDraftAppender{errors: []error{authFailure, authFailure}}
	locations := &fakeLocationClient{}
	commands.appendMailbox = appender
	commands.locations = locations
	args := []string{"--home", home, "mail", "attachments", "remove", fixture.message.ID, "--attachment", fixture.attachment.ID, "--idempotency-key", "messageRewrite-preappend-failed", "--yes"}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code == 0 || stdout != "" || appender.calls != 2 || locations.calls != 0 {
		t.Fatalf("pre-append failure code=%d stdout=%q stderr=%q append=%d delete=%d", code, stdout, stderr, appender.calls, locations.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "new idempotency key") || appender.calls != 2 || locations.calls != 0 {
		t.Fatalf("failed rewrite replay code=%d stdout=%q stderr=%q append=%d delete=%d", code, stdout, stderr, appender.calls, locations.calls)
	}
}
