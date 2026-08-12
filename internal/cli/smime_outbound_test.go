package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type smimeOutboundComposeMailService struct {
	unavailableMailService
	sendRequest    maildomain.SendRequest
	replyRequest   maildomain.ReplyRequest
	forwardRequest maildomain.ForwardRequest
}

func smimeOutboundPreview(mode maildomain.SMIMEComposeMode) maildomain.SendPreview {
	plan := &maildomain.SMIMEOutboundPlan{
		Mode: mode, SMIMEIdentityID: "smi_smimeOutbound", SenderCertificateID: "smc_sender", SenderFingerprintSHA256: strings.Repeat("a", 64),
	}
	if mode.Encrypts() {
		plan.RecipientCertificates = []maildomain.SMIMERecipientCertificate{{Email: "recipient@example.test", CertificateID: "smc_recipient", FingerprintSHA256: strings.Repeat("b", 64)}}
	}
	return maildomain.SendPreview{
		Kind: maildomain.OutboundKindSend, AccountID: "acct_smimeOutbound", SenderIdentityID: "mid_smimeOutbound",
		From: maildomain.Address{Email: "sender@example.test"}, To: []maildomain.Address{{Email: "recipient@example.test"}},
		Subject: "Outbound S/MIME preview", TextBody: "smimeOutbound preview body", BodyParts: 1, RequestHash: "smimeOutbound-request-hash", SMIME: plan,
	}
}

func (service *smimeOutboundComposeMailService) MailSendPreview(_ context.Context, _, _ string, request maildomain.SendRequest) (account.Account, maildomain.SendPreview, int64, error) {
	service.sendRequest = request
	return account.Account{ID: "acct_smimeOutbound"}, smimeOutboundPreview(request.SMIMEMode), 40, nil
}

func (service *smimeOutboundComposeMailService) MailReplyPreview(_ context.Context, _, _ string, request maildomain.ReplyRequest) (account.Account, maildomain.ReplyPreview, int64, error) {
	service.replyRequest = request
	preview := smimeOutboundPreview(request.SMIMEMode)
	preview.Kind = maildomain.OutboundKindReply
	return account.Account{ID: "acct_smimeOutbound"}, maildomain.ReplyPreview{
		ParentMessageID: request.ParentMessageID, ThreadID: "thr_smimeOutbound", Kind: maildomain.OutboundKindReply, Threading: true, Send: preview,
	}, 40, nil
}

func (service *smimeOutboundComposeMailService) MailForwardPreview(_ context.Context, _, _ string, request maildomain.ForwardRequest) (account.Account, maildomain.ForwardPreview, int64, error) {
	service.forwardRequest = request
	preview := smimeOutboundPreview(request.SMIMEMode)
	preview.Kind = maildomain.OutboundKindForward
	return account.Account{ID: "acct_smimeOutbound"}, maildomain.ForwardPreview{
		SourceMessageID: request.SourceMessageID, SourceThreadID: "thr_smimeOutbound", OriginalMode: request.OriginalMode, Send: preview,
	}, 40, nil
}

func TestSendPreviewShowsCertificateCoverageAndRejectsFakeRaw(t *testing.T) {
	service := &smimeOutboundComposeMailService{}
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "send",
		"--to", "recipient@example.test", "--subject", "Outbound S/MIME", "--body", "hello", "--smime", "encrypt", "--dry-run")
	if result.ExitCode != 0 || stderr != "" || service.sendRequest.SMIMEMode != maildomain.SMIMEComposeEncrypt ||
		!strings.Contains(stdout, "smime=encrypt") || !strings.Contains(stdout, "smime-recipient") || !strings.Contains(stdout, "smc_recipient") {
		t.Fatalf("preview result=%+v request=%+v stdout=%q stderr=%q", result, service.sendRequest, stdout, stderr)
	}
	result, stdout, stderr = executeWithMail(t, service, "", "mail", "send",
		"--to", "recipient@example.test", "--subject", "Outbound S/MIME", "--body", "hello", "--smime", "sign", "--dry-run", "--format", "raw")
	if result.ExitCode != ExitSafety || stdout != "" || !strings.Contains(stderr, "raw dry-run output is unavailable for S/MIME") {
		t.Fatalf("raw preview result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
}

func TestReplyAndForwardStructuredProtectionModesReachService(t *testing.T) {
	service := &smimeOutboundComposeMailService{}
	result, stdout, stderr := executeWithMail(t, service, `{"message_id":"msg_smimeOutbound","message":{"smime":"sign-encrypt","body":"reply body"}}`,
		"mail", "reply", "--input-json", "-", "--dry-run", "--json")
	if result.ExitCode != 0 || stderr != "" || service.replyRequest.SMIMEMode != maildomain.SMIMEComposeSignEncrypt || !strings.Contains(stdout, `"mode":"sign-encrypt"`) {
		t.Fatalf("reply result=%+v request=%+v stdout=%q stderr=%q", result, service.replyRequest, stdout, stderr)
	}
	result, stdout, stderr = executeWithMail(t, service, `{"message_id":"msg_smimeOutbound","message":{"smime":"encrypt","to":["recipient@example.test"],"body":"forward body"},"original":"none"}`,
		"mail", "forward", "--input-json", "-", "--dry-run", "--json")
	if result.ExitCode != 0 || stderr != "" || service.forwardRequest.SMIMEMode != maildomain.SMIMEComposeEncrypt || !strings.Contains(stdout, `"mode":"encrypt"`) {
		t.Fatalf("forward result=%+v request=%+v stdout=%q stderr=%q", result, service.forwardRequest, stdout, stderr)
	}
}
