package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

func phase29HideMyEmailFixture(t *testing.T, commands accountCommands, home, address, label string, verification account.MailIdentityVerification) account.MailIdentity {
	t.Helper()
	_, identity, _, err := commands.IdentityAdd(context.Background(), home, "", account.MailIdentityAddInput{
		Address: address, DisplayName: "Private Sender", Label: label, Kind: account.MailIdentityHideMyEmail,
	})
	if err != nil {
		t.Fatal(err)
	}
	if verification == account.MailIdentityUnverified {
		return identity
	}
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	identity, err = state.Identities().SetVerification(context.Background(), identity.AccountID, identity.ID, verification, time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func TestAgentParityPhase29VerifiedHideMyEmailIsPreferredForUnambiguousReplyAndExcludedAsSelf(t *testing.T) {
	home := t.TempDir()
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	primary := phase5PrimaryIdentity(t, commands, home)
	hme := phase29HideMyEmailFixture(t, commands, home, "private-token@icloud.com", "Shopping", account.MailIdentityVerified)

	seedReplyMessage(t, home, maildomain.IngestRecord{
		MessageID: "msg_phase29_hme_parent",
		Parsed: maildomain.ParsedMessage{
			RFCMessageID: "phase29-hme-parent@example.test",
			From:         maildomain.Address{DisplayName: "Merchant", Email: "merchant@example.test"},
			To:           []maildomain.Address{{Email: hme.Address}, {Email: primary.Address}},
			CC:           []maildomain.Address{{Email: "peer@example.test"}, {Email: strings.ToUpper(hme.Address)}},
			Subject:      "Private receipt",
		},
		Direction:    maildomain.DirectionInbound,
		InternalDate: time.Date(2026, 8, 11, 0, 10, 0, 0, time.UTC),
	})

	_, preview, _, err := commands.MailReplyPreview(context.Background(), home, "", maildomain.ReplyRequest{
		ParentMessageID: "msg_phase29_hme_parent", ReplyAll: true, TextBody: "reply through private identity",
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Send.SenderIdentityID != hme.ID || preview.Send.From.Email != hme.Address {
		t.Fatalf("reply sender=%+v hme=%+v", preview.Send, hme)
	}
	if len(preview.DerivedTo) != 1 || preview.DerivedTo[0].Email != "merchant@example.test" || len(preview.DerivedCC) != 1 || preview.DerivedCC[0].Email != "peer@example.test" {
		t.Fatalf("reply recipients to=%+v cc=%+v", preview.DerivedTo, preview.DerivedCC)
	}
}

func TestAgentParityPhase29UnverifiedOrAmbiguousHideMyEmailRequiresExplicitReplySender(t *testing.T) {
	home := t.TempDir()
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	primary := phase5PrimaryIdentity(t, commands, home)
	first := phase29HideMyEmailFixture(t, commands, home, "private-one@icloud.com", "One", account.MailIdentityUnverified)
	second := phase29HideMyEmailFixture(t, commands, home, "private-two@icloud.com", "Two", account.MailIdentityVerified)

	seedReplyMessage(t, home, maildomain.IngestRecord{
		MessageID: "msg_phase29_unverified_parent",
		Parsed:    maildomain.ParsedMessage{RFCMessageID: "phase29-unverified@example.test", From: maildomain.Address{Email: "sender@example.test"}, To: []maildomain.Address{{Email: first.Address}}, Subject: "Unverified HME"},
		Direction: maildomain.DirectionInbound, InternalDate: time.Date(2026, 8, 11, 0, 20, 0, 0, time.UTC),
	})
	_, _, _, err := commands.MailReplyPreview(context.Background(), home, "", maildomain.ReplyRequest{ParentMessageID: "msg_phase29_unverified_parent", TextBody: "body"})
	if err == nil || cli.NormalizeError(err).Code != cli.ErrorSafety {
		t.Fatalf("unverified auto sender err=%v", err)
	}
	_, explicit, _, err := commands.MailReplyPreview(context.Background(), home, "", maildomain.ReplyRequest{ParentMessageID: "msg_phase29_unverified_parent", SenderIdentityID: primary.ID, TextBody: "body"})
	if err != nil || explicit.Send.SenderIdentityID != primary.ID || explicit.Send.From.Email != primary.Address {
		t.Fatalf("explicit sender preview=%+v err=%v", explicit, err)
	}

	seedReplyMessage(t, home, maildomain.IngestRecord{
		MessageID: "msg_reply_later",
		Parsed:    maildomain.ParsedMessage{RFCMessageID: "phase29-ambiguous@example.test", From: maildomain.Address{Email: "sender@example.test"}, To: []maildomain.Address{{Email: first.Address}, {Email: second.Address}}, Subject: "Ambiguous HME"},
		Direction: maildomain.DirectionInbound, InternalDate: time.Date(2026, 8, 11, 0, 30, 0, 0, time.UTC),
	})
	_, _, _, err = commands.MailReplyPreview(context.Background(), home, "", maildomain.ReplyRequest{ParentMessageID: "msg_reply_later", TextBody: "body"})
	if err == nil || cli.NormalizeError(err).Code != cli.ErrorSafety || !strings.Contains(err.Error(), "multiple Hide My Email") {
		t.Fatalf("ambiguous auto sender err=%v", err)
	}
}

func TestAgentParityPhase29HideMyEmailOutboundRequiresEvidenceAndRejectionKeepsSelfIdentityEnabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase29-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	hme := phase29HideMyEmailFixture(t, commands, home, "private-send@icloud.com", "Private", account.MailIdentityUnverified)

	request := maildomain.SendRequest{
		SenderIdentityID: hme.ID, To: []maildomain.Address{{Email: "recipient@example.test"}}, Subject: "HME proof gate", TextBody: "private-body", IdempotencyKey: "phase29-unverified-hme",
	}
	_, _, _, _, err := commands.MailSend(context.Background(), home, "", request)
	if err == nil || cli.NormalizeError(err).Code != cli.ErrorConfiguration || sender.calls != 0 {
		t.Fatalf("unverified HME send err=%v calls=%d", err, sender.calls)
	}

	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	hme, err = state.Identities().SetVerification(context.Background(), hme.AccountID, hme.ID, account.MailIdentityVerified, time.Date(2026, 8, 11, 0, 40, 0, 0, time.UTC))
	state.Close()
	if err != nil {
		t.Fatal(err)
	}

	sender.err = &transport.SMTPSendError{Outcome: transport.SMTPSendFailed, Failure: transport.NewFailure(transport.StageSMTPMail, transport.FailureUnavailable, false, errors.New("synthetic HME sender rejected"))}
	request.IdempotencyKey = "phase29-rejected-hme"
	_, attempt, _, _, err := commands.MailSend(context.Background(), home, "", request)
	if err == nil || cli.NormalizeError(err).Code != cli.ErrorConfiguration || sender.calls != 1 || attempt.Status != maildomain.OutboundFailed {
		t.Fatalf("rejected HME attempt=%+v err=%v calls=%d", attempt, err, sender.calls)
	}
	_, rejected, _, showErr := commands.IdentityShow(context.Background(), home, "", hme.ID)
	if showErr != nil || !rejected.Enabled || rejected.Verification != account.MailIdentityRejected {
		t.Fatalf("rejected HME=%+v err=%v", rejected, showErr)
	}
	_, _, _, _, err = commands.MailSend(context.Background(), home, "", request)
	if err == nil || cli.NormalizeError(err).Code != cli.ErrorConfiguration || sender.calls != 1 {
		t.Fatalf("rejected HME resend err=%v calls=%d", err, sender.calls)
	}
}

func TestAgentParityPhase29HideMyEmailLabelPrivacyDefaultGateAndCapabilityStates(t *testing.T) {
	home := t.TempDir()
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	primary := phase5PrimaryIdentity(t, commands, home)

	code, stdout, stderr := runWithAccountCommands(t, commands, `{"address":"private-capability@icloud.com","label":"Shopping receipts","kind":"hide_my_email"}`,
		"--home", home, "mail", "identities", "add", "--input-json", "-", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("add HME code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var added struct {
		Data struct {
			Identity account.MailIdentity `json:"identity"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &added); err != nil {
		t.Fatal(err)
	}
	hme := added.Data.Identity
	if hme.Kind != account.MailIdentityHideMyEmail || hme.Label != "Shopping receipts" || hme.DisplayName != "" || hme.Verification != account.MailIdentityUnverified {
		t.Fatalf("HME=%+v", hme)
	}
	for _, forbidden := range []string{"forwarding_destination", "forwarding_address", "forward_to"} {
		if strings.Contains(strings.ToLower(stdout), forbidden) {
			t.Fatalf("identity output exposed forbidden HME provider metadata %q: %q", forbidden, stdout)
		}
	}

	code, _, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "identities", "default", hme.ID)
	if code != 3 || !strings.Contains(stderr, "Hide My Email outbound use is unavailable") {
		t.Fatalf("unverified HME default code=%d stderr=%q", code, stderr)
	}
	_, currentPrimary, _, err := commands.IdentityShow(context.Background(), home, "", primary.ID)
	if err != nil || !currentPrimary.Default {
		t.Fatalf("primary default changed=%+v err=%v", currentPrimary, err)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "--account", "owner@icloud.com", "capabilities", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"hide_my_email_outbound":"unverified"`) {
		t.Fatalf("unverified capability code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = state.Identities().SetVerification(context.Background(), hme.AccountID, hme.ID, account.MailIdentityVerified, time.Date(2026, 8, 11, 1, 0, 0, 0, time.UTC)); err != nil {
		state.Close()
		t.Fatal(err)
	}
	state.Close()

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "--account", "owner@icloud.com", "capabilities", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"hide_my_email_outbound":"supported"`) {
		t.Fatalf("supported capability code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, `{"identity":"`+hme.ID+`","label":"Updated private label"}`,
		"--home", home, "mail", "identities", "update", "--input-json", "-", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"label":"Updated private label"`) || !strings.Contains(stdout, `"verification":"verified"`) {
		t.Fatalf("label update code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	state, err = store.Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = state.Identities().SetVerification(context.Background(), hme.AccountID, hme.ID, account.MailIdentityRejected, time.Date(2026, 8, 11, 1, 10, 0, 0, time.UTC)); err != nil {
		state.Close()
		t.Fatal(err)
	}
	state.Close()
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "--account", "owner@icloud.com", "capabilities", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"hide_my_email_outbound":"unavailable"`) {
		t.Fatalf("rejected capability code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	schemas, err := cli.GeneratedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	identitySchema := string(schemas["mail-identity.schema.json"])
	if !strings.Contains(identitySchema, `"label"`) {
		t.Fatalf("identity schema omitted HME label: %s", identitySchema)
	}
	for _, forbidden := range []string{"forwarding_destination", "forwarding_address", "forward_to"} {
		if strings.Contains(strings.ToLower(identitySchema), forbidden) {
			t.Fatalf("identity schema exposed forbidden HME metadata %q: %s", forbidden, identitySchema)
		}
	}
}
