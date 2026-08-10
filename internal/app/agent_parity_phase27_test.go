package app

import (
	"context"
	"strings"
	"testing"

	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestAgentParityPhase27PreviewOverrideAndIdempotencyUseExpandedContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase27-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	ctx := context.Background()

	accountValue, signature, schemaVersion, err := commands.MailSignatureCreate(ctx, home, "", maildomain.SignatureInput{
		TextBody: "-- phase27 signature v1", HTMLBody: "<p>-- phase27 signature v1</p>",
	})
	if err != nil || signature.ID == "" || schemaVersion != 34 {
		t.Fatalf("create signature=%+v schema=%d err=%v", signature, schemaVersion, err)
	}
	configured, err := func() (maildomain.SignatureDefault, error) {
		_, value, _, setErr := commands.MailSignatureSetDefault(ctx, home, accountValue.ID, "", maildomain.OutboundKindSend, signature.ID)
		return value, setErr
	}()
	if err != nil || configured.SignatureID != signature.ID {
		t.Fatalf("default=%+v err=%v", configured, err)
	}

	base := maildomain.SendRequest{
		To: []maildomain.Address{{Email: "recipient@example.test"}}, Subject: "Phase 27 signature preview",
		TextBody: "plain body", HTMLBody: "<p>html body</p>", IdempotencyKey: "phase27-signature-key",
	}
	_, preview, _, err := commands.MailSendPreview(ctx, home, accountValue.ID, base)
	if err != nil || preview.SignatureID != signature.ID || preview.TextBody != "plain body\n\n-- phase27 signature v1" || preview.HTMLBody != "<p>html body</p><br><br><p>-- phase27 signature v1</p>" {
		t.Fatalf("default preview=%+v err=%v", preview, err)
	}
	overrideNone := base
	overrideNone.SignatureSelector = "none"
	_, previewNone, _, err := commands.MailSendPreview(ctx, home, accountValue.ID, overrideNone)
	if err != nil || previewNone.SignatureID != "" || previewNone.TextBody != "plain body" || previewNone.HTMLBody != "<p>html body</p>" {
		t.Fatalf("none preview=%+v err=%v", previewNone, err)
	}

	_, firstAttempt, reused, _, err := commands.MailSend(ctx, home, accountValue.ID, base)
	if err != nil || reused || firstAttempt.Status != maildomain.OutboundSent || sender.calls != 1 {
		t.Fatalf("first send attempt=%+v reused=%v calls=%d err=%v", firstAttempt, reused, sender.calls, err)
	}
	if raw := string(sender.messages[0]); !strings.Contains(raw, "-- phase27 signature v1") {
		t.Fatalf("SMTP payload omitted expanded signature: %q", raw)
	}

	changed := "-- phase27 signature v2"
	if _, _, _, err := commands.MailSignatureUpdate(ctx, home, accountValue.ID, signature.ID, maildomain.SignatureUpdate{TextBody: &changed}); err != nil {
		t.Fatal(err)
	}
	_, secondAttempt, reused, _, err := commands.MailSend(ctx, home, accountValue.ID, base)
	if err == nil || cli.NormalizeError(err).Code != cli.ErrorConflict || reused || secondAttempt.ID != "" || sender.calls != 1 {
		t.Fatalf("changed-signature idempotency attempt=%+v reused=%v calls=%d err=%v", secondAttempt, reused, sender.calls, err)
	}
	if !strings.Contains(err.Error(), "idempotency key") {
		t.Fatalf("idempotency conflict lost public reason: %v", err)
	}
}

func TestAgentParityPhase27DraftRevisionOwnsExpandedBodyUntilExplicitUpdate(t *testing.T) {
	commands, _ := newTestAccountCommands()
	home := setupUnclassifiedDraftAccount(t, commands)
	ctx := context.Background()
	accountValue, signature, _, err := commands.MailSignatureCreate(ctx, home, "", maildomain.SignatureInput{TextBody: "Draft signature A"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := commands.MailSignatureSetDefault(ctx, home, accountValue.ID, "", maildomain.OutboundKindSend, signature.ID); err != nil {
		t.Fatal(err)
	}

	_, created, _, err := commands.MailDraftCreate(ctx, home, accountValue.ID, maildomain.SendRequest{
		To: []maildomain.Address{{Email: "recipient@example.test"}}, Subject: "Phase 27 draft", TextBody: "Draft body",
	})
	if err != nil || created.Revision != 1 {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	_, _, preview, _, err := commands.MailDraftSendPreview(ctx, home, accountValue.ID, created.ID, "")
	if err != nil || preview.TextBody != "Draft body\n\nDraft signature A" {
		t.Fatalf("draft preview v1=%+v err=%v", preview, err)
	}

	changed := "Draft signature B"
	if _, _, _, err := commands.MailSignatureUpdate(ctx, home, accountValue.ID, signature.ID, maildomain.SignatureUpdate{TextBody: &changed}); err != nil {
		t.Fatal(err)
	}
	_, _, unchangedPreview, _, err := commands.MailDraftSendPreview(ctx, home, accountValue.ID, created.ID, "")
	if err != nil || unchangedPreview.TextBody != "Draft body\n\nDraft signature A" || strings.Contains(unchangedPreview.TextBody, "signature B") {
		t.Fatalf("saved revision changed after signature edit: preview=%+v err=%v", unchangedPreview, err)
	}

	_, updated, _, err := commands.MailDraftUpdate(ctx, home, accountValue.ID, created.ID, maildomain.SendRequest{
		To: []maildomain.Address{{Email: "recipient@example.test"}}, Subject: "Phase 27 draft", TextBody: "Draft body revised",
	})
	if err != nil || updated.Revision != 2 {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	_, _, updatedPreview, _, err := commands.MailDraftSendPreview(ctx, home, accountValue.ID, created.ID, "")
	if err != nil || updatedPreview.TextBody != "Draft body revised\n\nDraft signature B" || strings.Contains(updatedPreview.TextBody, "signature A") {
		t.Fatalf("explicit draft update did not apply current signature: preview=%+v err=%v", updatedPreview, err)
	}
}

func TestAgentParityPhase27SignatureCrudIsLocalWithoutProviderClients(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase27-local-secret")
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	ctx := context.Background()

	accountValue, signature, _, err := commands.MailSignatureCreate(ctx, home, "", maildomain.SignatureInput{TextBody: "Local only"})
	if err != nil {
		t.Fatal(err)
	}
	if _, page, _, err := commands.MailSignaturesList(ctx, home, accountValue.ID, "", 20); err != nil || len(page.Signatures) != 1 || page.Signatures[0].ID != signature.ID {
		t.Fatalf("signature page=%+v err=%v", page, err)
	}
	if _, shown, _, err := commands.MailSignatureShow(ctx, home, accountValue.ID, signature.ID); err != nil || shown.TextBody != "Local only" {
		t.Fatalf("shown=%+v err=%v", shown, err)
	}
	if _, removed, _, err := commands.MailSignatureRemove(ctx, home, accountValue.ID, signature.ID); err != nil || removed.ID != signature.ID {
		t.Fatalf("removed=%+v err=%v", removed, err)
	}
}

func TestAgentParityPhase27OperationFamilyDefaultsSelectAtSharedPreviewBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase27-operation-secret")
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	ctx := context.Background()

	accountValue, _, _, err := commands.MailSignatureCreate(ctx, home, "", maildomain.SignatureInput{TextBody: "bootstrap"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		operation maildomain.OutboundKind
		request   maildomain.SendRequest
	}{
		{name: "new", operation: maildomain.OutboundKindSend, request: maildomain.SendRequest{}},
		{name: "reply", operation: maildomain.OutboundKindReply, request: maildomain.SendRequest{Kind: maildomain.OutboundKindReply, ParentMessageID: "msg_phase27_parent"}},
		{name: "reply-all", operation: maildomain.OutboundKindReplyAll, request: maildomain.SendRequest{Kind: maildomain.OutboundKindReplyAll, ParentMessageID: "msg_phase27_parent"}},
		{name: "forward", operation: maildomain.OutboundKindForward, request: maildomain.SendRequest{Kind: maildomain.OutboundKindForward, SourceMessageID: "msg_phase27_source"}},
	}
	for index := range tests {
		test := &tests[index]
		_, signature, _, err := commands.MailSignatureCreate(ctx, home, accountValue.ID, maildomain.SignatureInput{TextBody: "signature-" + test.name})
		if err != nil {
			t.Fatalf("create %s signature: %v", test.name, err)
		}
		if _, configured, _, err := commands.MailSignatureSetDefault(ctx, home, accountValue.ID, "", test.operation, signature.ID); err != nil || configured.SignatureID != signature.ID {
			t.Fatalf("configure %s default=%+v err=%v", test.name, configured, err)
		}
		test.request.To = []maildomain.Address{{Email: "recipient@example.test"}}
		test.request.Subject = "Phase 27 " + test.name
		test.request.TextBody = "body-" + test.name
		_, preview, _, err := commands.MailSendPreview(ctx, home, accountValue.ID, test.request)
		if err != nil || preview.SignatureID != signature.ID || preview.TextBody != "body-"+test.name+"\n\nsignature-"+test.name {
			t.Fatalf("%s preview=%+v signature=%+v err=%v", test.name, preview, signature, err)
		}
	}
}
