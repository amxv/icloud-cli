package mail

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/smallstep/pkcs7"
)

type smimeTestIdentity struct {
	root    *x509.Certificate
	rootKey *ecdsa.PrivateKey
	leaf    *x509.Certificate
	leafKey *ecdsa.PrivateKey
}

func TestPhase32DetachedSMIMETrustedExactBytesAndControlPartIsNotAttachment(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	identity := newSMIMETestIdentity(t, "alice@example.test", now, x509.KeyUsageDigitalSignature, []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection})
	signed := []byte("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 7bit\r\n\r\nhello exact signed world")
	raw := detachedSMIMEFixture(t, "alice@example.test", signed, detachedSignature(t, signed, identity))
	roots := x509.NewCertPool()
	roots.AddCert(identity.root)
	content, err := ParseNormalizedWithAttachmentsAndSMIMEContext(t.Context(), bytes.NewReader(raw), nil, &SMIMEPolicy{Roots: roots, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if content.SMIME == nil || content.SMIME.Status != SMIMEStatusValidTrusted || !content.SMIME.SignatureValid || content.SMIME.Trust != SMIMETrustTrusted {
		t.Fatalf("S/MIME status = %+v", content.SMIME)
	}
	if content.SMIME.Signer == nil || content.SMIME.Signer.FingerprintSHA256 != certificateFingerprint(identity.leaf) || len(content.SMIME.Signer.Emails) != 1 || content.SMIME.Signer.Emails[0] != "alice@example.test" {
		t.Fatalf("signer = %+v", content.SMIME.Signer)
	}
	if content.Preview.Snippet != "hello exact signed world" {
		t.Fatalf("snippet=%q", content.Preview.Snippet)
	}
	if content.Preview.AttachmentCount != 0 || len(content.Attachments) != 0 {
		t.Fatalf("signature control leaked as attachment: count=%d attachments=%+v", content.Preview.AttachmentCount, content.Attachments)
	}
	var signaturePart *MIMEPart
	for i := range content.Parts {
		if content.Parts[i].MediaType == "application/pkcs7-signature" {
			signaturePart = &content.Parts[i]
			break
		}
	}
	if signaturePart == nil || signaturePart.Attachment || signaturePart.Inline {
		t.Fatalf("signature control part=%+v", signaturePart)
	}
}

func TestPhase32OpenSSLDetachedSMIMEInteroperabilityFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/phase32_openssl_signed.eml")
	if err != nil {
		t.Fatal(err)
	}
	rootPEM, err := os.ReadFile("testdata/phase32_openssl_root.pem")
	if err != nil {
		t.Fatal(err)
	}
	root, err := ParseSMIMECertificate(rootPEM)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	content, err := ParseNormalizedWithAttachmentsAndSMIMEContext(t.Context(), bytes.NewReader(raw), nil, &SMIMEPolicy{Roots: roots, Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if content.SMIME == nil || content.SMIME.Status != SMIMEStatusValidTrusted || content.SMIME.Container != SMIMEContainerMultipartSigned || !content.SMIME.SignatureValid || content.SMIME.Trust != SMIMETrustTrusted {
		t.Fatalf("OpenSSL fixture status=%+v", content.SMIME)
	}
	matchedEmail := false
	if content.SMIME.Signer != nil {
		for _, email := range content.SMIME.Signer.Emails {
			if email == "openssl-signer@example.test" {
				matchedEmail = true
				break
			}
		}
	}
	if content.SMIME.Signer == nil || !matchedEmail {
		t.Fatalf("OpenSSL fixture signer=%+v", content.SMIME.Signer)
	}
	if !strings.Contains(content.Preview.Snippet, "OpenSSL S/MIME interoperability fixture") {
		t.Fatalf("OpenSSL fixture body was not readable: %+v", content.Preview)
	}
}

func TestPhase32DetachedSMIMEStatusMatrixKeepsOrdinaryContentReadable(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	trusted := newSMIMETestIdentity(t, "alice@example.test", now, x509.KeyUsageDigitalSignature, []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection})
	signed := []byte("Content-Type: text/plain\r\n\r\nsigned phase32 content")
	signature := detachedSignature(t, signed, trusted)
	roots := x509.NewCertPool()
	roots.AddCert(trusted.root)

	tests := []struct {
		name      string
		from      string
		body      []byte
		signature []byte
		policy    *SMIMEPolicy
		want      SMIMEVerificationStatus
		valid     bool
		trust     SMIMETrustResult
	}{
		{name: "trusted", from: "alice@example.test", body: signed, signature: signature, policy: &SMIMEPolicy{Roots: roots, Now: now}, want: SMIMEStatusValidTrusted, valid: true, trust: SMIMETrustTrusted},
		{name: "untrusted", from: "alice@example.test", body: signed, signature: signature, policy: &SMIMEPolicy{Roots: x509.NewCertPool(), Now: now}, want: SMIMEStatusValidUntrusted, valid: true, trust: SMIMETrustUntrusted},
		{name: "identity mismatch", from: "mallory@example.test", body: signed, signature: signature, policy: &SMIMEPolicy{Roots: roots, Now: now}, want: SMIMEStatusIdentityMismatch, valid: true, trust: SMIMETrustTrusted},
		{name: "tampered", from: "alice@example.test", body: []byte("Content-Type: text/plain\r\n\r\ntampered phase32 content"), signature: signature, policy: &SMIMEPolicy{Roots: roots, Now: now}, want: SMIMEStatusInvalid, valid: false, trust: SMIMETrustNotEvaluated},
		{name: "malformed", from: "alice@example.test", body: signed, signature: []byte("not cms"), policy: &SMIMEPolicy{Roots: roots, Now: now}, want: SMIMEStatusMalformed, valid: false, trust: SMIMETrustNotEvaluated},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			raw := detachedSMIMEFixture(t, testCase.from, testCase.body, testCase.signature)
			content, err := ParseNormalizedWithAttachmentsAndSMIMEContext(t.Context(), bytes.NewReader(raw), nil, testCase.policy)
			if err != nil {
				t.Fatal(err)
			}
			if content.SMIME == nil || content.SMIME.Status != testCase.want || content.SMIME.SignatureValid != testCase.valid || content.SMIME.Trust != testCase.trust {
				t.Fatalf("status=%+v", content.SMIME)
			}
			if !strings.Contains(content.Preview.Snippet, "phase32 content") {
				t.Fatalf("ordinary body disappeared after S/MIME failure: %+v", content.Preview)
			}
		})
	}
}

func TestPhase32DetachedSMIMEExpiredWrongUsageAndMultipleSigner(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	signed := []byte("Content-Type: text/plain\r\n\r\nphase32 policy matrix")

	t.Run("expired at evaluation time", func(t *testing.T) {
		identity := newSMIMETestIdentityWithLifetime(t, "alice@example.test", now.Add(-time.Hour), now.Add(time.Hour), x509.KeyUsageDigitalSignature, []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection})
		roots := x509.NewCertPool()
		roots.AddCert(identity.root)
		status := VerifySMIMEDetached(detachedSignature(t, signed, identity), signed, "alice@example.test", "0", &SMIMEPolicy{Roots: roots, Now: now.Add(2 * time.Hour)})
		if status.Status != SMIMEStatusExpired || !status.SignatureValid {
			t.Fatalf("expired status=%+v", status)
		}
	})

	t.Run("wrong key usage", func(t *testing.T) {
		identity := newSMIMETestIdentity(t, "alice@example.test", now, x509.KeyUsageKeyEncipherment, []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection})
		roots := x509.NewCertPool()
		roots.AddCert(identity.root)
		status := VerifySMIMEDetached(detachedSignature(t, signed, identity), signed, "alice@example.test", "0", &SMIMEPolicy{Roots: roots, Now: now})
		if status.Status != SMIMEStatusInvalid || status.Reason != "digital_signature_key_usage_missing" || !status.SignatureValid {
			t.Fatalf("wrong usage status=%+v", status)
		}
	})

	t.Run("multiple signer", func(t *testing.T) {
		first := newSMIMETestIdentity(t, "alice@example.test", now, x509.KeyUsageDigitalSignature, []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection})
		second := newSMIMETestIdentity(t, "bob@example.test", now, x509.KeyUsageDigitalSignature, []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection})
		sd, err := pkcs7.NewSignedData(signed)
		if err != nil {
			t.Fatal(err)
		}
		sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
		if err := sd.AddSignerChain(first.leaf, first.leafKey, []*x509.Certificate{first.root}, pkcs7.SignerInfoConfig{}); err != nil {
			t.Fatal(err)
		}
		if err := sd.AddSignerChain(second.leaf, second.leafKey, []*x509.Certificate{second.root}, pkcs7.SignerInfoConfig{}); err != nil {
			t.Fatal(err)
		}
		sd.Detach()
		der, err := sd.Finish()
		if err != nil {
			t.Fatal(err)
		}
		status := VerifySMIMEDetached(der, signed, "alice@example.test", "0", &SMIMEPolicy{Roots: x509.NewCertPool(), Now: now})
		if status.Status != SMIMEStatusMalformed || status.Reason != "signer_count" {
			t.Fatalf("multiple-signer status=%+v", status)
		}
	})
}

func TestPhase32OpaqueEncryptedIsControlNotAttachment(t *testing.T) {
	raw := []byte("From: alice@example.test\r\nSubject: encrypted\r\nContent-Type: application/pkcs7-mime; smime-type=enveloped-data; name=smime.p7m\r\nContent-Disposition: attachment; filename=smime.p7m\r\nContent-Transfer-Encoding: base64\r\n\r\nYWJj\r\n")
	content, err := ParseNormalizedWithAttachmentsAndSMIMEContext(t.Context(), bytes.NewReader(raw), nil, &SMIMEPolicy{Roots: x509.NewCertPool(), Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if content.SMIME == nil || !content.SMIME.Encrypted || content.SMIME.Signed || content.SMIME.Container != SMIMEContainerOpaqueEncrypted || content.SMIME.Status != SMIMEStatusAbsent {
		t.Fatalf("encrypted status=%+v", content.SMIME)
	}
	if content.Preview.AttachmentCount != 0 || len(content.Attachments) != 0 || len(content.Parts) != 1 || content.Parts[0].Attachment {
		t.Fatalf("encrypted control projected as attachment: preview=%+v parts=%+v attachments=%+v", content.Preview, content.Parts, content.Attachments)
	}
}

func TestPhase32OpaqueSignedAndLargeOrdinaryAttachmentCoexistence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	identity := newSMIMETestIdentity(t, "alice@example.test", now, x509.KeyUsageDigitalSignature, []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection})
	roots := x509.NewCertPool()
	roots.AddCert(identity.root)

	t.Run("opaque signed", func(t *testing.T) {
		contentBytes := []byte("Content-Type: text/plain\r\n\r\nopaque signed payload")
		sd, err := pkcs7.NewSignedData(contentBytes)
		if err != nil {
			t.Fatal(err)
		}
		sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
		if err := sd.AddSignerChain(identity.leaf, identity.leafKey, []*x509.Certificate{identity.root}, pkcs7.SignerInfoConfig{}); err != nil {
			t.Fatal(err)
		}
		der, err := sd.Finish()
		if err != nil {
			t.Fatal(err)
		}
		raw := []byte("From: alice@example.test\r\nSubject: opaque signed\r\nMIME-Version: 1.0\r\nContent-Type: application/pkcs7-mime; smime-type=signed-data\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString(der) + "\r\n")
		parsed, err := ParseNormalizedWithAttachmentsAndSMIMEContext(t.Context(), bytes.NewReader(raw), nil, &SMIMEPolicy{Roots: roots, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		if parsed.SMIME == nil || parsed.SMIME.Status != SMIMEStatusValidTrusted || parsed.SMIME.Container != SMIMEContainerOpaqueSigned || len(parsed.Attachments) != 0 || parsed.Preview.AttachmentCount != 0 {
			t.Fatalf("opaque signed parse=%+v attachments=%+v", parsed.SMIME, parsed.Attachments)
		}
		if parsed.Preview.Snippet != "opaque signed payload" || !strings.Contains(parsed.SearchText, "opaque signed payload") || len(parsed.TextPaths) != 1 {
			t.Fatalf("opaque signed content was not projected as readable/searchable MIME: preview=%+v search=%q text_paths=%v parts=%+v", parsed.Preview, parsed.SearchText, parsed.TextPaths, parsed.Parts)
		}
	})

	t.Run("detached signed mixed with large attachment", func(t *testing.T) {
		innerBoundary := "phase32-mixed-boundary"
		large := bytes.Repeat([]byte("phase32-large-attachment-"), 16384)
		var signed bytes.Buffer
		signed.WriteString("Content-Type: multipart/mixed; boundary=\"" + innerBoundary + "\"\r\n\r\n")
		signed.WriteString("--" + innerBoundary + "\r\nContent-Type: text/plain\r\n\r\nlarge coexistence body\r\n")
		signed.WriteString("--" + innerBoundary + "\r\nContent-Type: application/octet-stream; name=large.bin\r\nContent-Disposition: attachment; filename=large.bin\r\nContent-Transfer-Encoding: base64\r\n\r\n")
		signed.WriteString(base64.StdEncoding.EncodeToString(large))
		signed.WriteString("\r\n--" + innerBoundary + "--")
		signedBytes := signed.Bytes()
		raw := detachedSMIMEFixture(t, "alice@example.test", signedBytes, detachedSignature(t, signedBytes, identity))
		parsed, err := ParseNormalizedWithAttachmentsAndSMIMEContext(t.Context(), bytes.NewReader(raw), nil, &SMIMEPolicy{Roots: roots, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		if parsed.SMIME == nil || parsed.SMIME.Status != SMIMEStatusValidTrusted || parsed.Preview.AttachmentCount != 1 {
			t.Fatalf("large coexistence smime=%+v preview=%+v parts=%+v", parsed.SMIME, parsed.Preview, parsed.Parts)
		}
		attachmentParts := 0
		for _, part := range parsed.Parts {
			if part.Attachment {
				attachmentParts++
			}
		}
		if attachmentParts != 1 {
			t.Fatalf("attachment parts=%d parts=%+v", attachmentParts, parsed.Parts)
		}
	})
}

func detachedSMIMEFixture(t *testing.T, from string, signed, signature []byte) []byte {
	t.Helper()
	boundary := "phase32-signed-boundary"
	var raw bytes.Buffer
	raw.WriteString("From: " + from + "\r\n")
	raw.WriteString("Subject: Phase 32 signed fixture\r\n")
	raw.WriteString("MIME-Version: 1.0\r\n")
	raw.WriteString("Content-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; micalg=sha-256; boundary=\"" + boundary + "\"\r\n\r\n")
	raw.WriteString("--" + boundary + "\r\n")
	raw.Write(signed)
	raw.WriteString("\r\n--" + boundary + "\r\n")
	raw.WriteString("Content-Type: application/pkcs7-signature; name=smime.p7s\r\n")
	raw.WriteString("Content-Transfer-Encoding: base64\r\n")
	raw.WriteString("Content-Disposition: attachment; filename=smime.p7s\r\n\r\n")
	raw.WriteString(base64.StdEncoding.EncodeToString(signature))
	raw.WriteString("\r\n--" + boundary + "--\r\n")
	return raw.Bytes()
}

func detachedSignature(t *testing.T, content []byte, identity smimeTestIdentity) []byte {
	t.Helper()
	sd, err := pkcs7.NewSignedData(content)
	if err != nil {
		t.Fatal(err)
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := sd.AddSignerChain(identity.leaf, identity.leafKey, []*x509.Certificate{identity.root}, pkcs7.SignerInfoConfig{}); err != nil {
		t.Fatal(err)
	}
	sd.Detach()
	der, err := sd.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func newSMIMETestIdentity(t *testing.T, email string, now time.Time, usage x509.KeyUsage, extUsage []x509.ExtKeyUsage) smimeTestIdentity {
	t.Helper()
	return newSMIMETestIdentityWithLifetime(t, email, now.Add(-time.Hour), now.Add(24*time.Hour), usage, extUsage)
}

func newSMIMETestIdentityWithLifetime(t *testing.T, email string, notBefore, notAfter time.Time, usage x509.KeyUsage, extUsage []x509.ExtKeyUsage) smimeTestIdentity {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Phase 32 Test Root"}, NotBefore: notBefore.Add(-time.Hour), NotAfter: notAfter.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Phase 32 Signer"}, EmailAddresses: []string{email}, NotBefore: notBefore, NotAfter: notAfter, BasicConstraintsValid: true, KeyUsage: usage, ExtKeyUsage: extUsage}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, root, &leafKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}
	return smimeTestIdentity{root: root, rootKey: rootKey, leaf: leaf, leafKey: leafKey}
}
