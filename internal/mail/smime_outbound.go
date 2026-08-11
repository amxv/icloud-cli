package mail

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/smallstep/pkcs7"
)

type SMIMEComposeMode string

const (
	SMIMEComposeNone        SMIMEComposeMode = ""
	SMIMEComposeSign        SMIMEComposeMode = "sign"
	SMIMEComposeEncrypt     SMIMEComposeMode = "encrypt"
	SMIMEComposeSignEncrypt SMIMEComposeMode = "sign-encrypt"
)

type SMIMERecipientCertificate struct {
	Email             string `json:"email"`
	CertificateID     string `json:"certificate_id"`
	FingerprintSHA256 string `json:"fingerprint_sha256"`
}

type SMIMEOutboundPlan struct {
	Mode                    SMIMEComposeMode            `json:"mode"`
	SMIMEIdentityID         string                      `json:"smime_identity_id"`
	SenderCertificateID     string                      `json:"sender_certificate_id"`
	SenderFingerprintSHA256 string                      `json:"sender_fingerprint_sha256"`
	RecipientCertificates   []SMIMERecipientCertificate `json:"recipient_certificates,omitempty"`
}

type SMIMEOutboundMaterial struct {
	Plan                   SMIMEOutboundPlan
	SignerCertificate      *x509.Certificate
	SignerPrivateKey       crypto.PrivateKey
	EncryptionCertificates []*x509.Certificate
}

var (
	ErrSMIMEOutboundInvalid               = errors.New("S/MIME outbound request is invalid")
	ErrSMIMERecipientCertificateMissing   = errors.New("S/MIME recipient certificate is missing")
	ErrSMIMERecipientCertificateUntrusted = errors.New("S/MIME recipient certificate is untrusted or unusable")
	ErrSMIMEOutboundKeyUnavailable        = errors.New("S/MIME outbound private key is unavailable")
	ErrSMIMEOutboundUnsupported           = errors.New("S/MIME outbound algorithm is unsupported")
)

func NormalizeSMIMEComposeMode(value SMIMEComposeMode) (SMIMEComposeMode, error) {
	switch SMIMEComposeMode(strings.ToLower(strings.TrimSpace(string(value)))) {
	case "", "none":
		return SMIMEComposeNone, nil
	case SMIMEComposeSign:
		return SMIMEComposeSign, nil
	case SMIMEComposeEncrypt:
		return SMIMEComposeEncrypt, nil
	case SMIMEComposeSignEncrypt:
		return SMIMEComposeSignEncrypt, nil
	default:
		return "", fmt.Errorf("%w: mode must be none, sign, encrypt, or sign-encrypt", ErrSMIMEOutboundInvalid)
	}
}

func (value SMIMEComposeMode) Signs() bool {
	return value == SMIMEComposeSign || value == SMIMEComposeSignEncrypt
}

func (value SMIMEComposeMode) Encrypts() bool {
	return value == SMIMEComposeEncrypt || value == SMIMEComposeSignEncrypt
}

func NormalizeSMIMEOutboundPlan(value SMIMEOutboundPlan, expected SMIMEComposeMode) (SMIMEOutboundPlan, error) {
	mode, err := NormalizeSMIMEComposeMode(value.Mode)
	if err != nil || mode == SMIMEComposeNone || mode != expected {
		return SMIMEOutboundPlan{}, ErrSMIMEOutboundInvalid
	}
	value.Mode = mode
	value.SMIMEIdentityID = strings.TrimSpace(value.SMIMEIdentityID)
	value.SenderCertificateID = strings.TrimSpace(value.SenderCertificateID)
	value.SenderFingerprintSHA256 = strings.ToLower(strings.TrimSpace(value.SenderFingerprintSHA256))
	if !strings.HasPrefix(value.SMIMEIdentityID, "smi_") || !strings.HasPrefix(value.SenderCertificateID, "smc_") || len(value.SenderFingerprintSHA256) != 64 {
		return SMIMEOutboundPlan{}, ErrSMIMEOutboundInvalid
	}
	result := make([]SMIMERecipientCertificate, 0, len(value.RecipientCertificates))
	seen := make(map[string]struct{}, len(value.RecipientCertificates))
	for _, certificate := range value.RecipientCertificates {
		certificate.Email = normalizeCertificateEmail(certificate.Email)
		certificate.CertificateID = strings.TrimSpace(certificate.CertificateID)
		certificate.FingerprintSHA256 = strings.ToLower(strings.TrimSpace(certificate.FingerprintSHA256))
		if certificate.Email == "" || !strings.Contains(certificate.Email, "@") || !strings.HasPrefix(certificate.CertificateID, "smc_") || len(certificate.FingerprintSHA256) != 64 {
			return SMIMEOutboundPlan{}, ErrSMIMEOutboundInvalid
		}
		key := certificate.Email + "\x00" + certificate.CertificateID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, certificate)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Email == result[j].Email {
			return result[i].CertificateID < result[j].CertificateID
		}
		return result[i].Email < result[j].Email
	})
	if mode.Encrypts() && len(result) == 0 {
		return SMIMEOutboundPlan{}, ErrSMIMEOutboundInvalid
	}
	if !mode.Encrypts() {
		result = nil
	}
	value.RecipientCertificates = result
	return value, nil
}

func SMIMERecipientCertificateUsable(cert *x509.Certificate, email string, policy *SMIMEPolicy, now time.Time) bool {
	if cert == nil || cert.IsCA || normalizeCertificateEmail(email) == "" || !certificateHasEmail(cert, email) {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) || !allowsEmailProtection(cert) {
		return false
	}
	if cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageKeyEncipherment == 0 {
		return false
	}
	if _, ok := cert.PublicKey.(*rsa.PublicKey); !ok {
		return false
	}
	return evaluateSMIMETrust(cert, nil, email, policy, now) == SMIMETrustTrusted
}

func SMIMESignerCertificateUsable(cert *x509.Certificate, email string, now time.Time) bool {
	if cert == nil || cert.IsCA || !certificateHasEmail(cert, email) {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) || !allowsEmailProtection(cert) {
		return false
	}
	return cert.KeyUsage == 0 || cert.KeyUsage&x509.KeyUsageDigitalSignature != 0
}

func SMIMEEncryptionCertificateUsableForSelf(cert *x509.Certificate, email string, now time.Time) bool {
	if cert == nil || cert.IsCA || !certificateHasEmail(cert, email) {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) || !allowsEmailProtection(cert) {
		return false
	}
	if cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageKeyEncipherment == 0 {
		return false
	}
	_, ok := cert.PublicKey.(*rsa.PublicKey)
	return ok
}

func WrapOutboundSMIME(writer io.Writer, ordinary []byte, material SMIMEOutboundMaterial) error {
	if writer == nil || len(ordinary) == 0 {
		return ErrSMIMEOutboundInvalid
	}
	plan, err := NormalizeSMIMEOutboundPlan(material.Plan, material.Plan.Mode)
	if err != nil {
		return err
	}
	outer, entity, err := splitOutboundRFCEntity(ordinary)
	if err != nil {
		return err
	}
	wrappedEntity := entity
	if plan.Mode.Signs() {
		if material.SignerCertificate == nil || material.SignerPrivateKey == nil {
			return ErrSMIMEOutboundKeyUnavailable
		}
		wrappedEntity, err = detachedSignedSMIMEEntity(entity, material.SignerCertificate, material.SignerPrivateKey)
		if err != nil {
			return err
		}
	}
	if plan.Mode.Encrypts() {
		if len(material.EncryptionCertificates) == 0 {
			return ErrSMIMERecipientCertificateMissing
		}
		wrappedEntity, err = encryptedSMIMEEntity(wrappedEntity, material.EncryptionCertificates)
		if err != nil {
			return err
		}
	}
	if _, err := writer.Write(outer); err != nil {
		return err
	}
	_, err = writer.Write(wrappedEntity)
	return err
}

func splitOutboundRFCEntity(raw []byte) ([]byte, []byte, error) {
	separator := bytes.Index(raw, []byte("\r\n\r\n"))
	if separator < 0 {
		return nil, nil, fmt.Errorf("%w: composed RFC message has no header/body boundary", ErrSMIMEOutboundInvalid)
	}
	headerBlock := raw[:separator]
	body := raw[separator+4:]
	lines := bytes.Split(headerBlock, []byte("\r\n"))
	type field struct {
		name  string
		lines [][]byte
	}
	fields := make([]field, 0, len(lines))
	for _, line := range lines {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			if len(fields) == 0 {
				return nil, nil, ErrSMIMEOutboundInvalid
			}
			fields[len(fields)-1].lines = append(fields[len(fields)-1].lines, line)
			continue
		}
		name, _, ok := bytes.Cut(line, []byte(":"))
		if !ok || len(bytes.TrimSpace(name)) == 0 {
			return nil, nil, ErrSMIMEOutboundInvalid
		}
		fields = append(fields, field{name: strings.ToLower(string(bytes.TrimSpace(name))), lines: [][]byte{line}})
	}
	var outer, content bytes.Buffer
	for _, value := range fields {
		target := &outer
		if strings.HasPrefix(value.name, "content-") {
			target = &content
		}
		for _, line := range value.lines {
			target.Write(line)
			target.WriteString("\r\n")
		}
	}
	if content.Len() == 0 {
		return nil, nil, fmt.Errorf("%w: composed message has no MIME content headers", ErrSMIMEOutboundInvalid)
	}
	content.WriteString("\r\n")
	content.Write(body)
	return outer.Bytes(), content.Bytes(), nil
}

func detachedSignedSMIMEEntity(entity []byte, cert *x509.Certificate, privateKey crypto.PrivateKey) ([]byte, error) {
	signed, err := pkcs7.NewSignedData(entity)
	if err != nil {
		return nil, fmt.Errorf("%w: initialize signed data: %v", ErrSMIMEOutboundInvalid, err)
	}
	signed.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := signed.AddSigner(cert, privateKey, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, fmt.Errorf("%w: sign MIME entity: %v", ErrSMIMEOutboundUnsupported, err)
	}
	signed.Detach()
	signature, err := signed.Finish()
	if err != nil {
		return nil, fmt.Errorf("%w: finish signed data: %v", ErrSMIMEOutboundInvalid, err)
	}
	boundary, err := randomSMIMEBoundary()
	if err != nil {
		return nil, err
	}
	var result bytes.Buffer
	fmt.Fprintf(&result, "Content-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; micalg=sha-256; boundary=\"%s\"\r\n\r\n", boundary)
	fmt.Fprintf(&result, "--%s\r\n", boundary)
	result.Write(entity)
	// The CRLF immediately before a multipart boundary belongs to the
	// boundary, not the signed entity. Always emit a distinct separator even
	// when the signed entity itself already ends in CRLF, otherwise a verifier
	// correctly strips the entity's final CRLF and the detached signature no
	// longer matches the transmitted octets.
	result.WriteString("\r\n")
	fmt.Fprintf(&result, "--%s\r\n", boundary)
	result.WriteString("Content-Type: application/pkcs7-signature; name=smime.p7s\r\n")
	result.WriteString("Content-Transfer-Encoding: base64\r\n")
	result.WriteString("Content-Disposition: attachment; filename=smime.p7s\r\n\r\n")
	writeSMIMEBase64(&result, signature)
	fmt.Fprintf(&result, "--%s--\r\n", boundary)
	return result.Bytes(), nil
}

var outboundSMIMEEncryptMu sync.Mutex

func encryptedSMIMEEntity(entity []byte, recipients []*x509.Certificate) ([]byte, error) {
	outboundSMIMEEncryptMu.Lock()
	previous := pkcs7.ContentEncryptionAlgorithm
	pkcs7.ContentEncryptionAlgorithm = pkcs7.EncryptionAlgorithmAES256CBC
	encrypted, err := pkcs7.Encrypt(entity, recipients)
	pkcs7.ContentEncryptionAlgorithm = previous
	outboundSMIMEEncryptMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("%w: encrypt MIME entity: %v", ErrSMIMEOutboundUnsupported, err)
	}
	var result bytes.Buffer
	result.WriteString("Content-Type: application/pkcs7-mime; smime-type=enveloped-data; name=smime.p7m\r\n")
	result.WriteString("Content-Transfer-Encoding: base64\r\n")
	result.WriteString("Content-Disposition: attachment; filename=smime.p7m\r\n\r\n")
	writeSMIMEBase64(&result, encrypted)
	return result.Bytes(), nil
}

func randomSMIMEBoundary() (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "icloud-cli-smime-" + hex.EncodeToString(value), nil
}

func writeSMIMEBase64(writer io.Writer, value []byte) {
	for len(value) > 0 {
		count := min(57, len(value))
		encoded := make([]byte, base64.StdEncoding.EncodedLen(count))
		base64.StdEncoding.Encode(encoded, value[:count])
		_, _ = writer.Write(encoded)
		_, _ = io.WriteString(writer, "\r\n")
		value = value[count:]
	}
}
