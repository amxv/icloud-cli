package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type phase27SignatureMailService struct {
	unavailableMailService
	page            maildomain.SignaturePage
	created         []maildomain.SignatureInput
	updates         []maildomain.SignatureUpdate
	updateIDs       []string
	defaultIdentity string
	defaultOp       maildomain.OutboundKind
	defaultSelector string
	sendRequest     maildomain.SendRequest
}

func (service *phase27SignatureMailService) MailSignaturesList(_ context.Context, _, _, cursor string, limit int) (account.Account, maildomain.SignaturePage, int64, error) {
	return account.Account{ID: "acct_phase27"}, service.page, 34, nil
}

func (service *phase27SignatureMailService) MailSignatureShow(_ context.Context, _, _, signatureID string) (account.Account, maildomain.Signature, int64, error) {
	return account.Account{ID: "acct_phase27"}, maildomain.Signature{
		ID: signatureID, AccountID: "acct_phase27", TextBody: "PHASE27_EXPLICIT_SHOW_BODY", HTMLBody: "<p>PHASE27_EXPLICIT_SHOW_HTML</p>",
		CreatedAt: time.Date(2026, 8, 11, 6, 30, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 8, 11, 6, 31, 0, 0, time.UTC),
	}, 34, nil
}

func (service *phase27SignatureMailService) MailSignatureCreate(_ context.Context, _, _ string, input maildomain.SignatureInput) (account.Account, maildomain.Signature, int64, error) {
	service.created = append(service.created, input)
	return account.Account{ID: "acct_phase27"}, maildomain.Signature{
		ID: "sig_phase27_created", AccountID: "acct_phase27", IdentityID: input.IdentityID,
		TextBody: input.TextBody, HTMLBody: input.HTMLBody, CreatedAt: time.Date(2026, 8, 11, 6, 32, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 8, 11, 6, 32, 0, 0, time.UTC),
	}, 34, nil
}

func (service *phase27SignatureMailService) MailSignatureUpdate(_ context.Context, _, _, signatureID string, update maildomain.SignatureUpdate) (account.Account, maildomain.Signature, int64, error) {
	service.updateIDs = append(service.updateIDs, signatureID)
	service.updates = append(service.updates, update)
	value := maildomain.Signature{ID: signatureID, AccountID: "acct_phase27", TextBody: "updated", UpdatedAt: time.Date(2026, 8, 11, 6, 33, 0, 0, time.UTC)}
	if update.TextBody != nil {
		value.TextBody = *update.TextBody
	}
	if update.HTMLBody != nil {
		value.HTMLBody = *update.HTMLBody
	}
	if update.IdentityID != nil {
		value.IdentityID = *update.IdentityID
	}
	return account.Account{ID: "acct_phase27"}, value, 34, nil
}

func (service *phase27SignatureMailService) MailSignatureRemove(_ context.Context, _, _, signatureID string) (account.Account, maildomain.Signature, int64, error) {
	return account.Account{ID: "acct_phase27"}, maildomain.Signature{ID: signatureID, AccountID: "acct_phase27"}, 34, nil
}

func (service *phase27SignatureMailService) MailSignatureDefault(_ context.Context, _, _, identity string, operation maildomain.OutboundKind) (account.Account, maildomain.SignatureDefault, int64, error) {
	service.defaultIdentity, service.defaultOp = identity, operation
	return account.Account{ID: "acct_phase27"}, maildomain.SignatureDefault{
		AccountID: "acct_phase27", IdentityID: identity, Operation: operation, SignatureID: "sig_phase27_default", UpdatedAt: time.Date(2026, 8, 11, 6, 34, 0, 0, time.UTC),
	}, 34, nil
}

func (service *phase27SignatureMailService) MailSignatureSetDefault(_ context.Context, _, _, identity string, operation maildomain.OutboundKind, selector string) (account.Account, maildomain.SignatureDefault, int64, error) {
	service.defaultIdentity, service.defaultOp, service.defaultSelector = identity, operation, selector
	return account.Account{ID: "acct_phase27"}, maildomain.SignatureDefault{
		AccountID: "acct_phase27", IdentityID: identity, Operation: operation, SignatureID: func() string {
			if selector == "none" {
				return ""
			}
			return selector
		}(), None: selector == "none", UpdatedAt: time.Date(2026, 8, 11, 6, 35, 0, 0, time.UTC),
	}, 34, nil
}

func (service *phase27SignatureMailService) MailSendPreview(_ context.Context, _, _ string, request maildomain.SendRequest) (account.Account, maildomain.SendPreview, int64, error) {
	service.sendRequest = request
	return account.Account{ID: "acct_phase27"}, maildomain.SendPreview{
		Kind: maildomain.OutboundKindSend, AccountID: "acct_phase27", From: maildomain.Address{Email: "owner@example.test"},
		To: request.To, Subject: request.Subject, TextBody: request.TextBody, SignatureID: request.SignatureSelector, BodyParts: 1, RequestHash: "phase27-hash",
	}, 34, nil
}

func TestPhase27SignatureListIsMetadataOnlyButShowIsExplicitContent(t *testing.T) {
	secret := "PHASE27_LIST_BODY_MUST_NOT_LEAK"
	service := &phase27SignatureMailService{page: maildomain.SignaturePage{
		Signatures: []maildomain.Signature{{
			ID: "sig_phase27_private", AccountID: "acct_phase27", IdentityID: "mid_phase27", TextBody: secret, HTMLBody: "<p>" + secret + "</p>",
			UpdatedAt: time.Date(2026, 8, 11, 6, 20, 0, 0, time.UTC),
		}},
	}}

	result, stdout, stderr := executeWithMail(t, service, "", "mail", "signatures", "list", "--json")
	if result.ExitCode != 0 || stderr != "" || strings.Contains(stdout, secret) || strings.Contains(stdout, "<p>") || !strings.Contains(stdout, `"text_bytes":`) || !strings.Contains(stdout, `"local_only":true`) {
		t.Fatalf("metadata list result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	result, stdout, stderr = executeWithMail(t, service, "", "mail", "signatures", "list")
	if result.ExitCode != 0 || stderr != "" || strings.Contains(stdout, secret) || !strings.Contains(stdout, "text_bytes=") || !strings.Contains(stdout, "scope=local") {
		t.Fatalf("compact metadata list result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}

	result, stdout, stderr = executeWithMail(t, service, "", "mail", "signatures", "show", "sig_phase27_private", "--json")
	if result.ExitCode != 0 || stderr != "" || !strings.Contains(stdout, "PHASE27_EXPLICIT_SHOW_BODY") || !strings.Contains(stdout, "PHASE27_EXPLICIT_SHOW_HTML") || !strings.Contains(stdout, `"local_only":true`) {
		t.Fatalf("explicit show result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
}

func TestPhase27SignatureCreateUpdateAndDefaultUseStrictStructuredInput(t *testing.T) {
	service := &phase27SignatureMailService{}
	createInput := `{"identity_id":"mid_phase27","text_body":"Thanks,\nAlex","html_body":"<p>Thanks,<br>Alex</p>"}`
	result, stdout, stderr := executeWithMail(t, service, createInput, "mail", "signatures", "create", "--input-json", "-", "--json")
	if result.ExitCode != 0 || stderr != "" || len(service.created) != 1 || service.created[0].IdentityID != "mid_phase27" || service.created[0].TextBody != "Thanks,\nAlex" || service.created[0].HTMLBody != "<p>Thanks,<br>Alex</p>" || !strings.Contains(stdout, `"local_only":true`) {
		t.Fatalf("create result=%+v created=%+v stdout=%q stderr=%q", result, service.created, stdout, stderr)
	}

	before := len(service.updates)
	result, stdout, stderr = executeWithMail(t, service, `{"signature_id":"sig_phase27_created","text_body":"changed","unexpected":true}`, "mail", "signatures", "update", "--input-json", "-", "--json")
	if result.ExitCode != ExitUsage || stdout != "" || len(service.updates) != before || !strings.Contains(stderr, "invalid --input-json request") {
		t.Fatalf("strict update result=%+v calls=%d stdout=%q stderr=%q", result, len(service.updates), stdout, stderr)
	}

	defaultInput := `{"operation":"reply-all","identity_id":"mid_phase27","signature_id":"none"}`
	result, stdout, stderr = executeWithMail(t, service, defaultInput, "mail", "signatures", "default", "--input-json", "-", "--json")
	if result.ExitCode != 0 || stderr != "" || service.defaultIdentity != "mid_phase27" || service.defaultOp != maildomain.OutboundKindReplyAll || service.defaultSelector != "none" || !strings.Contains(stdout, `"none":true`) {
		t.Fatalf("default result=%+v identity=%q op=%q selector=%q stdout=%q stderr=%q", result, service.defaultIdentity, service.defaultOp, service.defaultSelector, stdout, stderr)
	}

	result, stdout, stderr = executeWithMail(t, service, `{"signature_id":"sig_phase27_created"}`, "mail", "signatures", "delete", "--input-json", "-", "--json")
	if result.ExitCode != 0 || stderr != "" || !strings.Contains(stdout, `"signature_id":"sig_phase27_created"`) || !strings.Contains(stdout, `"removed":true`) {
		t.Fatalf("delete result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
}

func TestPhase27SendOverrideReachesNormalDryRunRequestPath(t *testing.T) {
	service := &phase27SignatureMailService{}
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "send", "--to", "recipient@example.test", "--subject", "Phase 27", "--body", "hello", "--signature", "sig_phase27", "--dry-run", "--json")
	if result.ExitCode != 0 || stderr != "" || service.sendRequest.SignatureSelector != "sig_phase27" || !strings.Contains(stdout, `"signature_id":"sig_phase27"`) {
		t.Fatalf("flag override result=%+v request=%+v stdout=%q stderr=%q", result, service.sendRequest, stdout, stderr)
	}

	input := `{"to":["recipient@example.test"],"subject":"Phase 27 JSON","body":"hello","signature":"none"}`
	result, _, stderr = executeWithMail(t, service, input, "mail", "send", "--input-json", "-", "--dry-run", "--json")
	if result.ExitCode != 0 || stderr != "" || service.sendRequest.SignatureSelector != "none" {
		t.Fatalf("JSON override result=%+v request=%+v stderr=%q", result, service.sendRequest, stderr)
	}
}

func TestPhase27SignatureListJSONDecodesWithoutBodyFields(t *testing.T) {
	service := &phase27SignatureMailService{page: maildomain.SignaturePage{Signatures: []maildomain.Signature{{ID: "sig_phase27", TextBody: "secret"}}}}
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "signatures", "list", "--json")
	if result.ExitCode != 0 || stderr != "" {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	var envelope struct {
		Data struct {
			Signatures []map[string]any `json:"signatures"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil || len(envelope.Data.Signatures) != 1 {
		t.Fatalf("envelope=%+v err=%v", envelope, err)
	}
	if _, exists := envelope.Data.Signatures[0]["text_body"]; exists {
		t.Fatalf("list leaked text_body: %q", stdout)
	}
	if _, exists := envelope.Data.Signatures[0]["html_body"]; exists {
		t.Fatalf("list leaked html_body: %q", stdout)
	}
}
