package app

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/blob"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestAgentParityPhase34RecipientTrustBCCAndIdempotentEncryptedSend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "phase34-synthetic-account-secret")
	commands, _ := newTestAccountCommands()
	now := time.Now().UTC().Truncate(time.Second)
	commands.now = func() time.Time { return now }
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)

	senderCert, senderKey := phase33RSACertificate(t, "owner@icloud.com", now)
	_, senderCertificate, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "owner@icloud.com", senderCert.Raw, false)
	if err != nil {
		t.Fatal(err)
	}
	senderPrivatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(senderKey)})
	_, senderIdentity, _, err := commands.MailSMIMEIdentityConfigure(context.Background(), home, "", "owner@icloud.com", senderCertificate.ID, account.CredentialSetup{Scheme: account.CredentialKeyring, Secret: senderPrivatePEM})
	if err != nil {
		t.Fatal(err)
	}

	recipientCert, recipientKey := phase33RSACertificate(t, "recipient@example.test", now.Add(time.Second))
	_, recipientCertificate, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "recipient@example.test", recipientCert.Raw, false)
	if err != nil {
		t.Fatal(err)
	}
	request := maildomain.SendRequest{
		To:             []maildomain.Address{{DisplayName: "Recipient", Email: "recipient@example.test"}},
		Subject:        "[ICLOUD-CLI-FIXTURE] Phase 34 encrypted send",
		TextBody:       "phase34 encrypted application body",
		SMIMEMode:      maildomain.SMIMEComposeSignEncrypt,
		IdempotencyKey: "phase34-encrypted-idempotency",
	}
	if _, _, _, err := commands.MailSendPreview(context.Background(), home, "", request); err == nil || !strings.Contains(err.Error(), "trusted usable certificate") {
		t.Fatalf("untrusted recipient preview error=%v", err)
	}
	if sender.calls != 0 {
		t.Fatalf("untrusted recipient contacted SMTP %d times", sender.calls)
	}
	_, recipientCertificate, _, err = commands.MailSMIMECertificateImport(context.Background(), home, "", "recipient@example.test", recipientCert.Raw, true)
	if err != nil {
		t.Fatal(err)
	}
	copyCert, copyKey := phase33RSACertificate(t, "copy@example.test", now.Add(1500*time.Millisecond))
	_, copyCertificate, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "copy@example.test", copyCert.Raw, true)
	if err != nil {
		t.Fatal(err)
	}
	request.CC = []maildomain.Address{{Email: "copy@example.test"}}

	hiddenCert, hiddenKey := phase33RSACertificate(t, "hidden@example.test", now.Add(2*time.Second))
	_, hiddenCertificate, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "hidden@example.test", hiddenCert.Raw, true)
	if err != nil {
		t.Fatal(err)
	}
	request.BCC = []maildomain.Address{{Email: "hidden@example.test"}}
	_, preview, _, err := commands.MailSendPreview(context.Background(), home, "", request)
	if err != nil {
		t.Fatal(err)
	}
	if preview.SMIME == nil || preview.SMIME.Mode != maildomain.SMIMEComposeSignEncrypt || preview.SMIME.SMIMEIdentityID != senderIdentity.ID || len(preview.SMIME.RecipientCertificates) != 3 {
		t.Fatalf("S/MIME preview=%+v", preview.SMIME)
	}
	coverage := map[string]string{}
	for _, value := range preview.SMIME.RecipientCertificates {
		coverage[value.Email] = value.CertificateID
	}
	if coverage["recipient@example.test"] != recipientCertificate.ID || coverage["copy@example.test"] != copyCertificate.ID || coverage["hidden@example.test"] != hiddenCertificate.ID {
		t.Fatalf("recipient coverage=%v", coverage)
	}
	if sender.calls != 0 {
		t.Fatalf("dry-run contacted SMTP %d times", sender.calls)
	}

	_, attempt, reused, _, err := commands.MailSend(context.Background(), home, "", request)
	if err != nil || reused || attempt.ID == "" || sender.calls != 1 || len(sender.messages) != 1 {
		t.Fatalf("send attempt=%+v reused=%t calls=%d messages=%d err=%v", attempt, reused, sender.calls, len(sender.messages), err)
	}
	raw := sender.messages[0]
	rawDigest := sha256.Sum256(raw)
	if attempt.RawContentHash != hex.EncodeToString(rawDigest[:]) || attempt.RawByteSize != int64(len(raw)) {
		t.Fatalf("attempt final raw binding hash=%q size=%d want_hash=%q want_size=%d", attempt.RawContentHash, attempt.RawByteSize, hex.EncodeToString(rawDigest[:]), len(raw))
	}
	lowerRaw := strings.ToLower(string(raw))
	if !strings.Contains(lowerRaw, "smime-type=enveloped-data") || strings.Contains(lowerRaw, "hidden@example.test") || bytes.Contains(raw, []byte("phase34 encrypted application body")) {
		t.Fatalf("encrypted outbound raw leaked Bcc/plaintext: %q", raw)
	}
	if !slices.Contains(sender.requests[0].Envelope.Recipients, "recipient@example.test") || !slices.Contains(sender.requests[0].Envelope.Recipients, "copy@example.test") || !slices.Contains(sender.requests[0].Envelope.Recipients, "hidden@example.test") {
		t.Fatalf("SMTP envelope recipients=%v", sender.requests[0].Envelope.Recipients)
	}
	envelope, err := maildomain.ParseSMIMEEnvelopeContext(context.Background(), bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		cert *x509.Certificate
		key  *rsa.PrivateKey
		name string
	}{{recipientCert, recipientKey, "To"}, {copyCert, copyKey, "Cc"}, {hiddenCert, hiddenKey, "Bcc"}} {
		matched, matchErr := envelope.Matches(pair.cert)
		if matchErr != nil || !matched {
			t.Fatalf("%s recipient match=%t err=%v", pair.name, matched, matchErr)
		}
		plaintext, decryptErr := envelope.Decrypt(context.Background(), pair.cert, pair.key, maildomain.MaxSendMessageBytes)
		if decryptErr != nil || !bytes.Contains(plaintext, []byte("phase34 encrypted application body")) || !bytes.Contains(plaintext, []byte("multipart/signed")) {
			t.Fatalf("%s decrypt contains body/signature=%t/%t err=%v", pair.name, bytes.Contains(plaintext, []byte("phase34 encrypted application body")), bytes.Contains(plaintext, []byte("multipart/signed")), decryptErr)
		}
	}

	_, replay, reused, _, err := commands.MailSend(context.Background(), home, "", request)
	if err != nil || !reused || replay.ID != attempt.ID || sender.calls != 1 || len(sender.messages) != 1 {
		t.Fatalf("replay=%+v reused=%t calls=%d messages=%d err=%v", replay, reused, sender.calls, len(sender.messages), err)
	}
}

func TestAgentParityPhase34MissingRecipientCertificateFailsBeforeAttemptOrSMTP(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "phase34-synthetic-account-secret")
	commands, _ := newTestAccountCommands()
	now := time.Now().UTC().Truncate(time.Second)
	commands.now = func() time.Time { return now }
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	senderCert, senderKey := phase33RSACertificate(t, "owner@icloud.com", now)
	_, senderCertificate, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "owner@icloud.com", senderCert.Raw, false)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(senderKey)})
	if _, _, _, err := commands.MailSMIMEIdentityConfigure(context.Background(), home, "", "owner@icloud.com", senderCertificate.ID, account.CredentialSetup{Scheme: account.CredentialKeyring, Secret: privatePEM}); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err = commands.MailSend(context.Background(), home, "", maildomain.SendRequest{
		To: []maildomain.Address{{Email: "missing@example.test"}}, Subject: "Phase 34 missing cert", TextBody: "must not send",
		SMIMEMode: maildomain.SMIMEComposeEncrypt, IdempotencyKey: "phase34-missing-cert",
	})
	if err == nil || !strings.Contains(err.Error(), "requires a certificate") || sender.calls != 0 {
		t.Fatalf("missing recipient err=%v smtp_calls=%d", err, sender.calls)
	}
	state, openErr := openState(context.Background(), home)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer state.Close()
	accountValue, resolveErr := state.Accounts().ResolveSelected(context.Background(), "")
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	attempts, more, listErr := state.Outbound().List(context.Background(), accountValue.ID, 10)
	if listErr != nil || more || len(attempts) != 0 {
		t.Fatalf("missing recipient created outbound attempts=%+v more=%t err=%v", attempts, more, listErr)
	}
}

func TestAgentParityPhase34DraftPersistsProtectionIntentAndEncryptsOnlyAtSend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "phase34-synthetic-account-secret")
	commands, _ := newTestAccountCommands()
	now := time.Now().UTC().Truncate(time.Second)
	commands.now = func() time.Time { return now }
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)

	_, draft, schemaVersion, err := commands.MailDraftCreate(context.Background(), home, "", maildomain.SendRequest{
		To: []maildomain.Address{{Email: "draft-recipient@example.test"}}, Subject: "Phase 34 protected draft",
		TextBody: "phase34 editable draft body", SMIMEMode: maildomain.SMIMEComposeEncrypt,
	})
	if err != nil || schemaVersion != 40 || draft.SMIMEMode != maildomain.SMIMEComposeEncrypt {
		t.Fatalf("draft=%+v schema=%d err=%v", draft, schemaVersion, err)
	}
	if draft.Message.Content == nil || !strings.Contains(draft.Message.Snippet, "phase34 editable draft body") || (draft.Message.Content.SMIME != nil && draft.Message.Content.SMIME.Encrypted) {
		t.Fatalf("saved draft should remain ordinary editable MIME: %+v", draft.Message)
	}
	_, reopened, _, _, err := commands.MailDraftShow(context.Background(), home, "", draft.ID)
	if err != nil || reopened.SMIMEMode != maildomain.SMIMEComposeEncrypt || !strings.Contains(reopened.Message.Snippet, "phase34 editable draft body") {
		t.Fatalf("reopened draft=%+v err=%v", reopened, err)
	}
	if _, _, _, _, err := commands.MailDraftSendPreview(context.Background(), home, "", draft.ID, ""); err == nil || !strings.Contains(err.Error(), "no configured S/MIME identity") {
		t.Fatalf("draft preview without sender S/MIME identity error=%v", err)
	}
	if sender.calls != 0 {
		t.Fatalf("draft preview contacted SMTP %d times", sender.calls)
	}

	senderCert, senderKey := phase33RSACertificate(t, "owner@icloud.com", now)
	_, senderCertificate, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "owner@icloud.com", senderCert.Raw, false)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(senderKey)})
	if _, _, _, err := commands.MailSMIMEIdentityConfigure(context.Background(), home, "", "owner@icloud.com", senderCertificate.ID, account.CredentialSetup{Scheme: account.CredentialKeyring, Secret: privatePEM}); err != nil {
		t.Fatal(err)
	}
	recipientCert, recipientKey := phase33RSACertificate(t, "draft-recipient@example.test", now.Add(time.Second))
	if _, _, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "draft-recipient@example.test", recipientCert.Raw, true); err != nil {
		t.Fatal(err)
	}
	_, _, preview, _, err := commands.MailDraftSendPreview(context.Background(), home, "", draft.ID, "")
	if err != nil || preview.SMIME == nil || preview.SMIME.Mode != maildomain.SMIMEComposeEncrypt || len(preview.SMIME.RecipientCertificates) != 1 {
		t.Fatalf("protected draft preview=%+v err=%v", preview, err)
	}
	_, _, attempt, reused, _, err := commands.MailDraftSend(context.Background(), home, "", draft.ID, "")
	if err != nil || reused || attempt.Status != maildomain.OutboundSent || sender.calls != 1 || len(sender.messages) != 1 {
		t.Fatalf("draft send attempt=%+v reused=%t calls=%d messages=%d err=%v", attempt, reused, sender.calls, len(sender.messages), err)
	}
	if bytes.Contains(sender.messages[0], []byte("phase34 editable draft body")) {
		t.Fatal("draft plaintext leaked into encrypted outbound raw")
	}
	envelope, err := maildomain.ParseSMIMEEnvelopeContext(context.Background(), bytes.NewReader(sender.messages[0]))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := envelope.Decrypt(context.Background(), recipientCert, recipientKey, maildomain.MaxSendMessageBytes)
	if err != nil || !bytes.Contains(plaintext, []byte("phase34 editable draft body")) {
		t.Fatalf("decrypted draft contains body=%t err=%v", bytes.Contains(plaintext, []byte("phase34 editable draft body")), err)
	}
	if _, _, _, _, err := commands.MailDraftShow(context.Background(), home, "", draft.ID); err == nil {
		t.Fatal("successfully sent local draft was not removed")
	}
}

func TestAgentParityPhase34AlternateSignedUnknownOutcomeReplaysWithoutResigning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "phase34-synthetic-account-secret")
	commands, _ := newTestAccountCommands()
	now := time.Now().UTC().Truncate(time.Second)
	commands.now = func() time.Time { return now }
	sender := &fakeSMTPSender{err: &transport.SMTPSendError{
		Outcome: transport.SMTPSendUnknown,
		Failure: transport.NewFailure(transport.StageSMTPAccept, transport.FailureUnavailable, true, errors.New("phase34 synthetic post-DATA disconnect")),
	}}
	commands.sender = sender
	addSendAccount(t, commands, home)
	alias := phase5IdentityFixture(t, commands, home, "signed-alias@example.test", false)
	aliasCert, aliasKey := phase33RSACertificate(t, alias.Address, now)
	_, certificate, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", alias.Address, aliasCert.Raw, true)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(aliasKey)})
	if _, _, _, err := commands.MailSMIMEIdentityConfigure(context.Background(), home, "", alias.ID, certificate.ID, account.CredentialSetup{Scheme: account.CredentialKeyring, Secret: privatePEM}); err != nil {
		t.Fatal(err)
	}
	request := maildomain.SendRequest{
		SenderIdentityID: alias.ID,
		To:               []maildomain.Address{{Email: "recipient@example.test"}},
		Subject:          "[ICLOUD-CLI-FIXTURE] Phase 34 alternate signed unknown",
		TextBody:         "phase34 alternate signed visible body",
		SMIMEMode:        maildomain.SMIMEComposeSign,
		IdempotencyKey:   "phase34-alternate-signed-unknown",
	}
	_, attempt, reused, _, err := commands.MailSend(context.Background(), home, "", request)
	if err == nil || reused || attempt.Status != maildomain.OutboundUnknown || sender.calls != 1 || len(sender.messages) != 1 {
		t.Fatalf("unknown signed attempt=%+v reused=%t calls=%d messages=%d err=%v", attempt, reused, sender.calls, len(sender.messages), err)
	}
	raw := sender.messages[0]
	lowerRaw := strings.ToLower(string(raw))
	if !strings.Contains(lowerRaw, "from: \"alternate sender\" <signed-alias@example.test>") || !strings.Contains(lowerRaw, "multipart/signed") || !strings.Contains(string(raw), "phase34 alternate signed visible body") {
		t.Fatalf("alternate signed raw=%q", raw)
	}
	content, parseErr := maildomain.ParseNormalizedWithAttachmentsAndSMIMEContext(context.Background(), bytes.NewReader(raw), nil, &maildomain.SMIMEPolicy{
		Now: now, Certificates: []maildomain.SMIMECertificateMaterial{{ID: certificate.ID, Email: alias.Address, Trusted: true, Certificate: aliasCert}},
	})
	if parseErr != nil || content.SMIME == nil || !content.SMIME.SignatureValid {
		t.Fatalf("alternate signed verification=%+v err=%v", content.SMIME, parseErr)
	}
	firstHash, firstSize := attempt.RawContentHash, attempt.RawByteSize
	_, replay, reused, _, err := commands.MailSend(context.Background(), home, "", request)
	if err != nil || !reused || replay.ID != attempt.ID || replay.RawContentHash != firstHash || replay.RawByteSize != firstSize || sender.calls != 1 || len(sender.messages) != 1 {
		t.Fatalf("unknown replay=%+v reused=%t calls=%d messages=%d err=%v", replay, reused, sender.calls, len(sender.messages), err)
	}
}

func TestAgentParityPhase34FinalPostWrapSizeLimitIsAuthoritative(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	senderCert, senderKey := phase33RSACertificate(t, "owner@icloud.com", now)
	recipientCert, _ := phase33RSACertificate(t, "recipient@example.test", now.Add(time.Second))
	plan := maildomain.SMIMEOutboundPlan{
		Mode: maildomain.SMIMEComposeEncrypt, SMIMEIdentityID: "smi_phase34_size", SenderCertificateID: "smc_phase34_sender",
		SenderFingerprintSHA256: maildomain.SMIMECertificateFromX509(senderCert).FingerprintSHA256,
		RecipientCertificates: []maildomain.SMIMERecipientCertificate{{
			Email: "recipient@example.test", CertificateID: "smc_phase34_recipient", FingerprintSHA256: maildomain.SMIMECertificateFromX509(recipientCert).FingerprintSHA256,
		}},
	}
	material := maildomain.SMIMEOutboundMaterial{
		Plan: plan, SignerCertificate: senderCert, SignerPrivateKey: senderKey,
		EncryptionCertificates: []*x509.Certificate{senderCert, recipientCert},
	}
	base := maildomain.SendRequest{
		From: maildomain.Address{Email: "owner@icloud.com"}, To: []maildomain.Address{{Email: "recipient@example.test"}},
		Subject: "Phase 34 final size", TextBody: "size boundary", SMIMEMode: maildomain.SMIMEComposeEncrypt, SMIMEPlan: &plan,
	}
	for _, test := range []struct {
		name      string
		bytes     int
		wantLarge bool
	}{{"within-final-limit", 10 << 20, false}, {"post-wrap-overflow", 12 << 20, true}} {
		t.Run(test.name, func(t *testing.T) {
			payload := bytes.Repeat([]byte("x"), test.bytes)
			digest := sha256.Sum256(payload)
			request := base
			request.Attachments = []maildomain.OutboundAttachment{{
				Filename: "large.bin", MediaType: "application/octet-stream", Disposition: maildomain.AttachmentDispositionAttachment,
				ContentHash: hex.EncodeToString(digest[:]), ByteSize: int64(len(payload)),
				Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(payload)), nil },
			}}
			stager := blob.NewWithLimit(t.TempDir(), maildomain.MaxSendMessageBytes)
			final, verification, err := stageOutboundMessage(context.Background(), stager, request, "phase34-size@example.test", now, material)
			if test.wantLarge {
				if !errors.Is(err, blob.ErrTooLarge) || final != nil || verification != nil {
					t.Fatalf("post-wrap overflow final=%v verification=%v err=%v", final, verification, err)
				}
				return
			}
			if err != nil || final == nil || verification == nil || final.ByteSize <= verification.ByteSize || final.ByteSize > maildomain.MaxSendMessageBytes {
				t.Fatalf("bounded wrap final=%+v verification=%+v err=%v", final, verification, err)
			}
			_ = final.Abort()
			if verification != final {
				_ = verification.Abort()
			}
		})
	}
}
