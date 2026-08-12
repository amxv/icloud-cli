package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestReplyAllUsesEveryEnabledIdentityAsSelfAndSelectedAliasAsSender(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-identity-aware-reply-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	primary := primarySenderIdentityFixture(t, commands, home)
	alias := createSenderIdentityFixture(t, commands, home, "identity-aware-alias@example.test", false)

	seedReplyMessage(t, home, maildomain.IngestRecord{
		MessageID: "msg_identity_aware_reply_parent",
		Parsed: maildomain.ParsedMessage{
			RFCMessageID: "identity-aware-reply-parent@example.test",
			From:         maildomain.Address{DisplayName: "External Sender", Email: "external-sender@example.test"},
			To: []maildomain.Address{
				{Email: primary.Address},
				{Email: alias.Address},
				{DisplayName: "Visible Peer", Email: "visible-peer@example.test"},
			},
			CC: []maildomain.Address{
				{Email: strings.ToUpper(alias.Address)},
				{DisplayName: "Copy Peer", Email: "copy-peer@example.test"},
			},
			Subject: "Identity-aware mail reply identity",
			Snippet: "identityAware parent",
		},
		Direction: maildomain.DirectionInbound, InternalDate: time.Date(2026, 8, 10, 7, 0, 0, 0, time.UTC),
	})

	args := []string{
		"--home", home, "mail", "reply-all", "msg_identity_aware_reply_parent",
		"--from", alias.ID,
		"--body", "identity-aware-reply-body-token",
		"--idempotency-key", "identity-aware-reply-self-set",
		"--dry-run",
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || sender.calls != 0 {
		t.Fatalf("preview code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	for _, want := range []string{
		"sender_identity=" + alias.ID,
		"from=\"Alternate Sender <identity-aware-alias@example.test>\"",
		"derived_to=\"External Sender <external-sender@example.test>\"",
		"derived_cc=\"Visible Peer <visible-peer@example.test>,Copy Peer <copy-peer@example.test>\"",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("reply-all preview missing %q: %q", want, stdout)
		}
	}
	if strings.Contains(stdout, "derived_cc=identity-aware-alias@example.test") || strings.Contains(stdout, "derived_cc="+primary.Address) {
		t.Fatalf("reply-all retained an enabled self address: %q", stdout)
	}
	jsonInput := `{"message_id":"msg_identity_aware_reply_parent","message":{"from":"` + alias.Address + `","body":"identity-aware-json-reply-token"}}`
	code, stdout, stderr = runWithAccountCommands(t, commands, jsonInput,
		"--home", home, "mail", "reply-all", "--input-json", "-", "--dry-run")
	if code != 0 || stderr != "" || sender.calls != 0 || !strings.Contains(stdout, "sender_identity="+alias.ID) || !strings.Contains(stdout, "identity-aware-json-reply-token") {
		t.Fatalf("json reply-all code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}

	real := append([]string(nil), args[:len(args)-1]...)
	real = append(real, "--yes")
	code, stdout, stderr = runWithAccountCommands(t, commands, "", real...)
	if code != 0 || stderr != "" || sender.calls != 1 {
		t.Fatalf("send code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	if sender.auth[0].Username != primary.Address || sender.requests[0].Envelope.From != alias.Address {
		t.Fatalf("auth=%+v envelope=%+v primary=%+v alias=%+v", sender.auth, sender.requests[0].Envelope, primary, alias)
	}
	if got := strings.Join(sender.requests[0].Envelope.Recipients, ","); got != "external-sender@example.test,visible-peer@example.test,copy-peer@example.test" {
		t.Fatalf("reply-all recipients=%s", got)
	}
	parsed, err := maildomain.ParseNormalized(bytes.NewReader(sender.messages[0]))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Preview.From.Email != alias.Address || parsed.Preview.InReplyTo != "identity-aware-reply-parent@example.test" {
		t.Fatalf("parsed reply=%+v", parsed.Preview)
	}
}

func TestForwardAcceptsTheSameIdentitySelectorWithoutChangingSourceThread(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-identity-aware-forward-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	alias := createSenderIdentityFixture(t, commands, home, "identity-aware-forward@example.test", false)
	threadID := seedReplyMessage(t, home, maildomain.IngestRecord{
		MessageID: "msg_identity_aware_forward_source",
		Parsed: maildomain.ParsedMessage{
			RFCMessageID: "identity-aware-forward-source@example.test",
			From:         maildomain.Address{Email: "source@example.test"}, To: []maildomain.Address{{Email: "owner@icloud.com"}},
			Subject: "Identity-aware mail forward source", Snippet: "source",
		},
		Direction: maildomain.DirectionInbound, InternalDate: time.Date(2026, 8, 10, 7, 30, 0, 0, time.UTC),
	})
	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "mail", "forward", "msg_identity_aware_forward_source",
		"--from", alias.Address, "--to", "target@example.test", "--original", "none",
		"--body", "identity-aware-forward-body-token", "--idempotency-key", "identity-aware-forward-sender", "--yes")
	if code != 0 || stderr != "" || sender.calls != 1 {
		t.Fatalf("forward code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	if sender.requests[0].Envelope.From != alias.Address || !strings.Contains(stdout, "source_message_id=msg_identity_aware_forward_source") {
		t.Fatalf("forward envelope=%+v stdout=%q", sender.requests[0].Envelope, stdout)
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
	page, err := state.Threads().Show(context.Background(), accountValue.ID, "msg_identity_aware_forward_source", "", 1)
	if err != nil || page.Thread.ID != threadID {
		t.Fatalf("source thread changed before=%s after=%s err=%v", threadID, page.Thread.ID, err)
	}
}

func TestFreshDirectionUsesEnabledIdentitySetOnly(t *testing.T) {
	home := t.TempDir()
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	alias := createSenderIdentityFixture(t, commands, home, "identity-aware-direction@example.test", false)
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	addresses, err := enabledMailIdentityAddresses(context.Background(), state, accountValue.ID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	self := mailIdentityAddressSet(addresses)
	inbox := folderFixtureForIdentityAware()
	if got := messageDirection(self, inbox, maildomain.ParsedMessage{From: maildomain.Address{Email: strings.ToUpper(alias.Address)}}, maildomain.LocationFlags{}); got != maildomain.DirectionOutbound {
		state.Close()
		t.Fatalf("enabled alias direction=%q", got)
	}
	if got := messageDirection(self, inbox, maildomain.ParsedMessage{From: maildomain.Address{Email: "external@example.test"}}, maildomain.LocationFlags{}); got != maildomain.DirectionInbound {
		state.Close()
		t.Fatalf("external direction=%q", got)
	}
	state.Close()
	if _, _, _, err := commands.IdentitySetEnabled(context.Background(), home, "", alias.ID, false); err != nil {
		t.Fatal(err)
	}
	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	addresses, err = enabledMailIdentityAddresses(context.Background(), state, accountValue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := messageDirection(mailIdentityAddressSet(addresses), inbox, maildomain.ParsedMessage{From: maildomain.Address{Email: alias.Address}}, maildomain.LocationFlags{}); got != maildomain.DirectionInbound {
		t.Fatalf("disabled alias direction=%q", got)
	}
}

func TestFetchedNormalizationClassifiesEnabledAliasOutbound(t *testing.T) {
	home := t.TempDir()
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	alias := createSenderIdentityFixture(t, commands, home, "identity-aware-ingest@example.test", false)
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
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
	raw := strings.Join([]string{
		"From: Phase Six Alias <" + strings.ToUpper(alias.Address) + ">",
		"To: recipient@example.test",
		"Subject: Identity-aware mail alias ingestion",
		"Message-ID: <identity-aware-alias-ingest@example.test>",
		"Date: Mon, 10 Aug 2026 08:30:00 +0000",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"identity-aware-ingest-token",
	}, "\r\n")
	staged, err := commands.inboundBlobStore(paths).Stage(context.Background(), strings.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Abort()
	attachments := newStagedAttachmentSet(commands.attachmentBlobStore(paths))
	defer attachments.Abort()
	records, err := commands.prepareFetchedMailRecords(context.Background(), state, accountValue, folderFixtureForIdentityAware(), 901, []transport.FetchedMessage{{
		UID: 1, InternalDate: time.Date(2026, 8, 10, 8, 30, 0, 0, time.UTC), RFC822Size: int64(len(raw)), Raw: staged,
	}}, attachments)
	if err != nil || len(records) != 1 || records[0].Direction != maildomain.DirectionOutbound || !strings.EqualFold(records[0].Parsed.From.Email, alias.Address) {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}

func TestPublicReclassificationCommandAdvancesQueuedLocalWork(t *testing.T) {
	home := t.TempDir()
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	seedReplyMessage(t, home, maildomain.IngestRecord{
		MessageID: "msg_identity_aware_reclassify",
		Parsed: maildomain.ParsedMessage{
			RFCMessageID: "identity-aware-reclassify@example.test", From: maildomain.Address{Email: "new-self@example.test"},
			To: []maildomain.Address{{Email: "owner@icloud.com"}}, Subject: "Identity-aware mail reclassify",
		},
		Direction: maildomain.DirectionInbound, InternalDate: time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC),
	})
	if _, _, _, err := commands.IdentityAdd(context.Background(), home, "", account.MailIdentityAddInput{Address: "new-self@example.test", Kind: account.MailIdentityOther}); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "identities", "reclassify", "--limit", "1", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"selected":1`) || !strings.Contains(stdout, `"promoted":1`) || !strings.Contains(stdout, `"pending":false`) {
		t.Fatalf("reclassify code=%d stdout=%q stderr=%q", code, stdout, stderr)
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
	message, err := state.Mail().Message(context.Background(), accountValue.ID, "msg_identity_aware_reclassify")
	if err != nil || message.Direction != maildomain.DirectionOutbound {
		t.Fatalf("reclassified message=%+v err=%v", message, err)
	}
}

func folderFixtureForIdentityAware() folder.Folder {
	return folder.Folder{ID: "fld_identity_aware", Name: "Inbox", Kind: folder.KindInbox, Present: true, Selectable: true}
}
