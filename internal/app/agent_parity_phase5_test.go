package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func phase5IdentityFixture(t *testing.T, commands accountCommands, home, address string, makeDefault bool) account.MailIdentity {
	t.Helper()
	_, identity, _, err := commands.IdentityAdd(context.Background(), home, "", account.MailIdentityAddInput{
		Address: address, DisplayName: "Alternate Sender", Kind: account.MailIdentityCustomDomain, MakeDefault: makeDefault,
	})
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func phase5PrimaryIdentity(t *testing.T, commands accountCommands, home string) account.MailIdentity {
	t.Helper()
	_, values, _, _, _, err := commands.IdentityListPage(context.Background(), home, "", 20, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if value.Kind == account.MailIdentityPrimary {
			return value
		}
	}
	t.Fatal("primary identity missing")
	return account.MailIdentity{}
}

func TestAgentParityPhase5SelectedIdentityOwnsEnvelopeHeadersMessageIDAndRetryIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	primary := phase5PrimaryIdentity(t, commands, home)
	alias := phase5IdentityFixture(t, commands, home, "alias@example.test", false)

	request := maildomain.SendRequest{
		SenderIdentityID: alias.ID,
		To:               []maildomain.Address{{Email: "recipient@example.test"}},
		Subject:          "Identity selection",
		TextBody:         "identity-selection-token",
		IdempotencyKey:   "phase5-selected-identity",
	}
	accountValue, preview, _, err := commands.MailSendPreview(context.Background(), home, "", request)
	if err != nil {
		t.Fatal(err)
	}
	if preview.SenderIdentityID != alias.ID || preview.From.Email != alias.Address || preview.From.DisplayName != alias.DisplayName {
		t.Fatalf("preview=%+v alias=%+v", preview, alias)
	}
	_, attempt, reused, _, err := commands.MailSend(context.Background(), home, "", request)
	if err != nil || reused || sender.calls != 1 {
		t.Fatalf("attempt=%+v reused=%v calls=%d err=%v", attempt, reused, sender.calls, err)
	}
	if attempt.SenderIdentityID != alias.ID {
		t.Fatalf("attempt sender=%q want=%q", attempt.SenderIdentityID, alias.ID)
	}
	if len(sender.auth) != 1 || sender.auth[0].Username != accountValue.Email || sender.auth[0].Username == alias.Address {
		t.Fatalf("smtp auth=%+v account=%+v alias=%+v", sender.auth, accountValue, alias)
	}
	if len(sender.requests) != 1 || sender.requests[0].Envelope.From != alias.Address {
		t.Fatalf("smtp envelope=%+v", sender.requests)
	}
	raw := string(sender.messages[0])
	for _, want := range []string{
		`From: "Alternate Sender" <alias@example.test>`,
		"@example.test>",
		"identity-selection-token",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("raw message missing %q: %q", want, raw)
		}
	}
	_, verified, _, err := commands.IdentityShow(context.Background(), home, "", alias.ID)
	if err != nil || verified.Verification != account.MailIdentityVerified || !verified.Enabled {
		t.Fatalf("verified identity=%+v err=%v", verified, err)
	}

	changedSender := request
	changedSender.SenderIdentityID = primary.ID
	_, _, _, _, err = commands.MailSend(context.Background(), home, "", changedSender)
	if err == nil || cli.NormalizeError(err).Code != cli.ErrorConflict || sender.calls != 1 {
		t.Fatalf("sender-change retry err=%v calls=%d", err, sender.calls)
	}
}

func TestAgentParityPhase5DefaultAndDisabledIdentitySelection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	alias := phase5IdentityFixture(t, commands, home, "default@example.test", true)

	_, preview, _, err := commands.MailSendPreview(context.Background(), home, "", maildomain.SendRequest{
		To: []maildomain.Address{{Email: "recipient@example.test"}}, Subject: "Default sender", TextBody: "default-token",
	})
	if err != nil || preview.SenderIdentityID != alias.ID || preview.From.Email != alias.Address {
		t.Fatalf("default preview=%+v err=%v", preview, err)
	}
	if _, _, _, err := commands.IdentitySetEnabled(context.Background(), home, "", alias.ID, false); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = commands.MailSendPreview(context.Background(), home, "", maildomain.SendRequest{
		SenderIdentityID: alias.ID,
		To:               []maildomain.Address{{Email: "recipient@example.test"}}, Subject: "Disabled", TextBody: "disabled-token",
	})
	if err == nil || cli.NormalizeError(err).Code != cli.ErrorConflict {
		t.Fatalf("disabled identity preview err=%v", err)
	}
	primary := phase5PrimaryIdentity(t, commands, home)
	_, preview, _, err = commands.MailSendPreview(context.Background(), home, "", maildomain.SendRequest{
		To: []maildomain.Address{{Email: "recipient@example.test"}}, Subject: "Fallback default", TextBody: "primary-token",
	})
	if err != nil || preview.SenderIdentityID != primary.ID || preview.From.Email != primary.Address {
		t.Fatalf("primary default preview=%+v err=%v", preview, err)
	}
}

func TestAgentParityPhase5PermanentMailFromRejectionDisablesAlternateWithoutFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{err: &transport.SMTPSendError{
		Outcome: transport.SMTPSendFailed,
		Failure: transport.NewFailure(transport.StageSMTPMail, transport.FailureUnavailable, false, errors.New("synthetic sender rejected")),
	}}
	commands.sender = sender
	addSendAccount(t, commands, home)
	alias := phase5IdentityFixture(t, commands, home, "rejected@example.test", false)

	_, attempt, _, _, err := commands.MailSend(context.Background(), home, "", maildomain.SendRequest{
		SenderIdentityID: alias.ID,
		To:               []maildomain.Address{{Email: "recipient@example.test"}},
		Subject:          "Rejected sender", TextBody: "rejected-token", IdempotencyKey: "phase5-rejected",
	})
	if err == nil || cli.NormalizeError(err).Code != cli.ErrorConfiguration || sender.calls != 1 || attempt.Status != maildomain.OutboundFailed {
		t.Fatalf("attempt=%+v calls=%d err=%v", attempt, sender.calls, err)
	}
	if sender.requests[0].Envelope.From != alias.Address {
		t.Fatalf("provider saw envelope=%+v", sender.requests[0].Envelope)
	}
	_, rejected, _, showErr := commands.IdentityShow(context.Background(), home, "", alias.ID)
	if showErr != nil || rejected.Enabled || rejected.Verification != account.MailIdentityRejected {
		t.Fatalf("rejected identity=%+v err=%v", rejected, showErr)
	}
	_, _, _, previewErr := commands.MailSendPreview(context.Background(), home, "", maildomain.SendRequest{
		SenderIdentityID: alias.ID,
		To:               []maildomain.Address{{Email: "recipient@example.test"}}, Subject: "No fallback", TextBody: "no-fallback-token",
	})
	if previewErr == nil || cli.NormalizeError(previewErr).Code != cli.ErrorConflict || sender.calls != 1 {
		t.Fatalf("rejected identity reuse err=%v calls=%d", previewErr, sender.calls)
	}
	_, enabled, _, enableErr := commands.IdentitySetEnabled(context.Background(), home, "", alias.ID, true)
	if enableErr != nil || !enabled.Enabled || enabled.Verification != account.MailIdentityUnverified {
		t.Fatalf("reenabled=%+v err=%v", enabled, enableErr)
	}
}

func TestAgentParityPhase5MailFromAcceptanceIsRecordedEvenWhenRecipientFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	commands.sender = &fakeSMTPSender{err: &transport.SMTPSendError{
		Outcome: transport.SMTPSendFailed,
		Failure: transport.NewFailure(transport.StageSMTPRecipient, transport.FailureUnavailable, false, errors.New("synthetic recipient rejected")),
	}}
	addSendAccount(t, commands, home)
	alias := phase5IdentityFixture(t, commands, home, "accepted-from@example.test", false)
	_, _, _, _, err := commands.MailSend(context.Background(), home, "", maildomain.SendRequest{
		SenderIdentityID: alias.ID,
		To:               []maildomain.Address{{Email: "recipient@example.test"}},
		Subject:          "Accepted sender", TextBody: "accepted-sender-token", IdempotencyKey: "phase5-mail-from-accepted",
	})
	if err == nil {
		t.Fatal("expected recipient failure")
	}
	_, identity, _, showErr := commands.IdentityShow(context.Background(), home, "", alias.ID)
	if showErr != nil || identity.Verification != account.MailIdentityVerified || !identity.Enabled {
		t.Fatalf("identity=%+v err=%v", identity, showErr)
	}
}

func TestAgentParityPhase5DraftIdentityProvenanceRawAndSendOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-send-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	primary := phase5PrimaryIdentity(t, commands, home)
	alias := phase5IdentityFixture(t, commands, home, "draft-alias@example.test", true)

	_, created, _, err := commands.MailDraftCreate(context.Background(), home, "", maildomain.SendRequest{
		To: []maildomain.Address{{Email: "recipient@example.test"}}, Subject: "Draft identity", TextBody: "draft-identity-token",
	})
	if err != nil || created.SenderIdentityID != alias.ID || created.Message.From.Email != alias.Address {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	_, shown, source, _, err := commands.MailDraftShow(context.Background(), home, "", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	if err := source.WriteRaw(context.Background(), &raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw.String(), `From: "Alternate Sender" <draft-alias@example.test>`) || !strings.Contains(raw.String(), "@example.test>") || shown.SenderIdentityID != alias.ID {
		t.Fatalf("draft raw=%q shown=%+v", raw.String(), shown)
	}

	_, updated, _, err := commands.MailDraftUpdate(context.Background(), home, "", created.ID, maildomain.SendRequest{
		SenderIdentityID: primary.ID,
		To:               []maildomain.Address{{Email: "recipient@example.test"}}, Subject: "Updated identity", TextBody: "updated-draft-token",
	})
	if err != nil || updated.SenderIdentityID != primary.ID || updated.Message.From.Email != primary.Address {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	_, _, preview, _, err := commands.MailDraftSendPreview(context.Background(), home, "", created.ID, alias.ID)
	if err != nil || preview.SenderIdentityID != alias.ID || preview.From.Email != alias.Address {
		t.Fatalf("send preview=%+v err=%v", preview, err)
	}
	_, _, attempt, _, _, err := commands.MailDraftSend(context.Background(), home, "", created.ID, alias.ID)
	if err != nil || sender.calls != 1 || attempt.SenderIdentityID != alias.ID || sender.requests[0].Envelope.From != alias.Address {
		t.Fatalf("send attempt=%+v calls=%d requests=%+v err=%v", attempt, sender.calls, sender.requests, err)
	}
}
