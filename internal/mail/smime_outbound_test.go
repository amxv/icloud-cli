package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/smallstep/pkcs7"
)

func TestOutboundSMIMESignEncryptPreservesRFCEnvelopeAndVerifiesAfterDecrypt(t *testing.T) {
	now := time.Date(2026, 8, 11, 6, 0, 0, 0, time.UTC)
	senderCert, senderKey := smimeOutboundMailCertificate(t, "sender@example.test", now)
	recipientCert, recipientKey := smimeOutboundMailCertificate(t, "recipient@example.test", now.Add(time.Second))
	request := SendRequest{
		From:     Address{DisplayName: "Sender", Email: "sender@example.test"},
		To:       []Address{{DisplayName: "Recipient", Email: "recipient@example.test"}},
		BCC:      []Address{{Email: "hidden@example.test"}},
		Subject:  "S/MIME sign encrypt",
		TextBody: "smime signed encrypted body",
		HTMLBody: `<p>smime signed encrypted body<img src="cid:inline-smime@example.test"></p>`,
		Attachments: []OutboundAttachment{
			smimeOutboundAttachment("inline.png", "image/png", AttachmentDispositionInline, "inline-smime@example.test", []byte("smime-inline-image")),
			smimeOutboundAttachment("report.txt", "text/plain", AttachmentDispositionAttachment, "", []byte("smime-attachment-body")),
		},
	}
	var ordinary bytes.Buffer
	if err := ComposeOutboundMessage(&ordinary, request, "smime-sign-encrypt@example.test", now); err != nil {
		t.Fatal(err)
	}
	plan := SMIMEOutboundPlan{
		Mode: SMIMEComposeSignEncrypt, SMIMEIdentityID: "smi_smime_outbound", SenderCertificateID: "smc_sender",
		SenderFingerprintSHA256: SMIMECertificateFromX509(senderCert).FingerprintSHA256,
		RecipientCertificates:   []SMIMERecipientCertificate{{Email: "recipient@example.test", CertificateID: "smc_recipient", FingerprintSHA256: SMIMECertificateFromX509(recipientCert).FingerprintSHA256}},
	}
	var wrapped bytes.Buffer
	if err := WrapOutboundSMIME(&wrapped, ordinary.Bytes(), SMIMEOutboundMaterial{
		Plan: plan, SignerCertificate: senderCert, SignerPrivateKey: senderKey,
		EncryptionCertificates: []*x509.Certificate{senderCert, recipientCert},
	}); err != nil {
		t.Fatal(err)
	}
	raw := wrapped.String()
	lowerRaw := strings.ToLower(raw)
	if !strings.Contains(lowerRaw, "message-id: <smime-sign-encrypt@example.test>") || !strings.Contains(lowerRaw, "to:") || !strings.Contains(lowerRaw, "recipient@example.test") || !strings.Contains(lowerRaw, "smime-type=enveloped-data") {
		t.Fatalf("wrapped RFC envelope missing required visible metadata:\n%s", raw)
	}
	if strings.Contains(lowerRaw, "\r\nbcc:") || strings.Contains(lowerRaw, "hidden@example.test") || strings.Contains(raw, "smime signed encrypted body") {
		t.Fatalf("wrapped ciphertext leaked Bcc or plaintext:\n%s", raw)
	}
	envelope, err := ParseSMIMEEnvelopeContext(context.Background(), strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	matched, err := envelope.Matches(recipientCert)
	if err != nil || !matched {
		t.Fatalf("recipient match=%t err=%v", matched, err)
	}
	decrypted, err := envelope.Decrypt(context.Background(), recipientCert, recipientKey, MaxSendMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(decrypted, []byte("multipart/signed")) || !bytes.Contains(decrypted, []byte("smime signed encrypted body")) || !bytes.Contains(decrypted, []byte("inline-smime@example.test")) || !bytes.Contains(decrypted, []byte("report.txt")) || bytes.Contains(bytes.ToLower(decrypted), []byte("message-id:")) {
		t.Fatalf("decrypted MIME entity=%q", decrypted)
	}
	roots := x509.NewCertPool()
	roots.AddCert(senderCert)
	policy := &SMIMEPolicy{
		Roots: roots, Now: now, ExpectedSignerEmail: "sender@example.test",
		Certificates: []SMIMECertificateMaterial{{ID: "smc_sender", Email: "sender@example.test", Trusted: true, Certificate: senderCert}},
	}
	content, err := ParseNormalizedWithAttachmentsAndSMIMEContext(context.Background(), bytes.NewReader(decrypted), nil, policy)
	if err != nil {
		t.Fatal(err)
	}
	if content.SMIME == nil || !content.SMIME.Signed || !content.SMIME.SignatureValid || content.SMIME.Status != SMIMEStatusValidTrusted || content.SMIME.Trust != SMIMETrustTrusted {
		t.Fatalf("decrypted signature status=%+v", content.SMIME)
	}
	if len(content.Attachments) != 2 || len(content.HTMLPaths) == 0 {
		t.Fatalf("decrypted canonical MIME content=%+v", content)
	}
}

func TestOutboundSMIMEDetachedSignatureIsVerifiableAndHeadersRemainVisible(t *testing.T) {
	now := time.Date(2026, 8, 11, 6, 5, 0, 0, time.UTC)
	cert, key := smimeOutboundMailCertificate(t, "sender@example.test", now)
	request := SendRequest{
		From: Address{Email: "sender@example.test"}, To: []Address{{Email: "recipient@example.test"}},
		Subject: "S/MIME signed", TextBody: "signed body",
	}
	var ordinary bytes.Buffer
	if err := ComposeOutboundMessage(&ordinary, request, "smime-signed@example.test", now); err != nil {
		t.Fatal(err)
	}
	plan := SMIMEOutboundPlan{Mode: SMIMEComposeSign, SMIMEIdentityID: "smi_smime_outbound", SenderCertificateID: "smc_sender", SenderFingerprintSHA256: SMIMECertificateFromX509(cert).FingerprintSHA256}
	var wrapped bytes.Buffer
	if err := WrapOutboundSMIME(&wrapped, ordinary.Bytes(), SMIMEOutboundMaterial{Plan: plan, SignerCertificate: cert, SignerPrivateKey: key}); err != nil {
		t.Fatal(err)
	}
	lowerWrapped := strings.ToLower(wrapped.String())
	if !strings.Contains(lowerWrapped, "message-id: <smime-signed@example.test>") || !strings.Contains(lowerWrapped, "multipart/signed") || !strings.Contains(wrapped.String(), "signed body") {
		t.Fatalf("signed output=%q", wrapped.String())
	}
	_, expectedEntity, err := splitOutboundRFCEntity(ordinary.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	headerEnd := bytes.Index(wrapped.Bytes(), []byte("\r\n\r\n"))
	boundaryStart := strings.Index(wrapped.String(), "boundary=\"")
	if headerEnd < 0 || boundaryStart < 0 {
		t.Fatalf("cannot locate signed multipart framing: %q", wrapped.String())
	}
	boundaryStart += len("boundary=\"")
	boundaryEnd := strings.Index(wrapped.String()[boundaryStart:], "\"")
	if boundaryEnd < 0 {
		t.Fatal("cannot locate signed multipart boundary terminator")
	}
	boundary := wrapped.String()[boundaryStart : boundaryStart+boundaryEnd]
	extractedEntity, signatureEntity, err := splitSMIMEMultipartSigned(wrapped.Bytes()[headerEnd+4:], boundary)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(extractedEntity, expectedEntity) {
		t.Fatalf("signed entity changed on wire: expected=%q extracted=%q", expectedEntity, extractedEntity)
	}
	signatureDER, err := decodeSMIMESignatureEntity(signatureEntity)
	if err != nil {
		t.Fatal(err)
	}
	p7, err := pkcs7.Parse(signatureDER)
	if err != nil {
		t.Fatal(err)
	}
	p7.Content = extractedEntity
	if err := p7.Verify(); err != nil {
		t.Fatalf("direct detached CMS verify failed: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	content, err := ParseNormalizedWithAttachmentsAndSMIMEContext(context.Background(), bytes.NewReader(wrapped.Bytes()), nil, &SMIMEPolicy{
		Roots: roots, Now: now, Certificates: []SMIMECertificateMaterial{{ID: "smc_sender", Email: "sender@example.test", Trusted: true, Certificate: cert}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if content.SMIME == nil || content.SMIME.Status != SMIMEStatusValidTrusted || !content.SMIME.SignatureValid {
		t.Fatalf("signed content status=%+v", content.SMIME)
	}
}

func TestSMIMEPlanNormalizationAndRecipientUsabilityFailClosed(t *testing.T) {
	if _, err := NormalizeSMIMEComposeMode("downgrade"); !errors.Is(err, ErrSMIMEOutboundInvalid) {
		t.Fatalf("invalid mode error=%v", err)
	}
	now := time.Date(2026, 8, 11, 6, 10, 0, 0, time.UTC)
	cert, _ := smimeOutboundMailCertificate(t, "recipient@example.test", now)
	roots := x509.NewCertPool()
	policy := &SMIMEPolicy{Roots: roots, Now: now, Certificates: []SMIMECertificateMaterial{{ID: "smc_recipient", Email: "recipient@example.test", Certificate: cert}}}
	if SMIMERecipientCertificateUsable(cert, "recipient@example.test", policy, now) {
		t.Fatal("untrusted recipient certificate became usable")
	}
	roots.AddCert(cert)
	if !SMIMERecipientCertificateUsable(cert, "recipient@example.test", policy, now) {
		t.Fatal("trusted recipient certificate was rejected")
	}
}

func smimeOutboundMailCertificate(t *testing.T, email string, now time.Time) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: "S/MIME outbound " + email}, EmailAddresses: []string{email},
		NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(24 * time.Hour), BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func smimeOutboundAttachment(filename, mediaType string, disposition AttachmentDisposition, contentID string, value []byte) OutboundAttachment {
	digest := sha256.Sum256(value)
	copyValue := append([]byte(nil), value...)
	return OutboundAttachment{
		Filename: filename, MediaType: mediaType, Disposition: disposition, ContentID: contentID,
		ContentHash: hex.EncodeToString(digest[:]), ByteSize: int64(len(copyValue)),
		Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(copyValue)), nil },
	}
}
