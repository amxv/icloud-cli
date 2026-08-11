package mail

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/emersion/go-message"
	"github.com/smallstep/pkcs7"
)

const maxSMIMEContainerBytes = 64 << 20

const MaxSMIMECertificatesPerAccount = 1000

type SMIMEVerificationStatus string

type SMIMETrustResult string

type SMIMEContainerKind string

const (
	SMIMEStatusAbsent           SMIMEVerificationStatus = "absent"
	SMIMEStatusValidTrusted     SMIMEVerificationStatus = "valid-trusted"
	SMIMEStatusValidUntrusted   SMIMEVerificationStatus = "valid-untrusted"
	SMIMEStatusInvalid          SMIMEVerificationStatus = "invalid"
	SMIMEStatusExpired          SMIMEVerificationStatus = "expired"
	SMIMEStatusIdentityMismatch SMIMEVerificationStatus = "identity-mismatch"
	SMIMEStatusMalformed        SMIMEVerificationStatus = "malformed"

	SMIMETrustNotEvaluated SMIMETrustResult = "not-evaluated"
	SMIMETrustTrusted      SMIMETrustResult = "trusted"
	SMIMETrustUntrusted    SMIMETrustResult = "untrusted"

	SMIMEContainerMultipartSigned SMIMEContainerKind = "multipart-signed"
	SMIMEContainerOpaqueSigned    SMIMEContainerKind = "opaque-signed"
	SMIMEContainerOpaqueEncrypted SMIMEContainerKind = "opaque-encrypted"
)

type SMIMESigner struct {
	FingerprintSHA256 string     `json:"fingerprint_sha256"`
	Subject           string     `json:"subject,omitempty"`
	Issuer            string     `json:"issuer,omitempty"`
	Emails            []string   `json:"emails,omitempty"`
	NotBefore         time.Time  `json:"not_before"`
	NotAfter          time.Time  `json:"not_after"`
	KeyUsage          []string   `json:"key_usage,omitempty"`
	ExtendedKeyUsage  []string   `json:"extended_key_usage,omitempty"`
	SigningTime       *time.Time `json:"signing_time,omitempty"`
}

type SMIMEStatus struct {
	Status          SMIMEVerificationStatus `json:"status"`
	Container       SMIMEContainerKind      `json:"container,omitempty"`
	Path            string                  `json:"path,omitempty"`
	Signed          bool                    `json:"signed"`
	Encrypted       bool                    `json:"encrypted"`
	SignatureValid  bool                    `json:"signature_valid"`
	Trust           SMIMETrustResult        `json:"trust"`
	IdentityMatched *bool                   `json:"identity_matched,omitempty"`
	Signer          *SMIMESigner            `json:"signer,omitempty"`
	Reason          string                  `json:"reason,omitempty"`
	PolicyRevision  int64                   `json:"-"`
}

type SMIMECertificateMaterial struct {
	Email       string
	Trusted     bool
	Certificate *x509.Certificate
}

type SMIMEPolicy struct {
	Roots        *x509.CertPool
	Certificates []SMIMECertificateMaterial
	Now          time.Time
	Revision     int64
}

type SMIMECertificate struct {
	ID                string    `json:"id"`
	AccountID         string    `json:"account_id,omitempty"`
	Email             string    `json:"email"`
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
	Subject           string    `json:"subject,omitempty"`
	Issuer            string    `json:"issuer,omitempty"`
	Emails            []string  `json:"emails,omitempty"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
	Trusted           bool      `json:"trusted"`
	IsCA              bool      `json:"is_ca"`
	KeyUsage          []string  `json:"key_usage,omitempty"`
	ExtendedKeyUsage  []string  `json:"extended_key_usage,omitempty"`
	DER               []byte    `json:"-"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type SMIMECertificatePage struct {
	Certificates []SMIMECertificate `json:"certificates"`
	More         bool               `json:"more"`
	NextCursor   string             `json:"next_cursor,omitempty"`
}

var (
	ErrSMIMECertificateInvalid  = errors.New("S/MIME certificate is invalid")
	ErrSMIMECertificateNotFound = errors.New("S/MIME certificate not found")
	ErrSMIMECertificateLimit    = errors.New("S/MIME certificate limit reached")
	ErrSMIMEInvalidCursor       = errors.New("S/MIME certificate cursor is invalid")
)

type boundedSMIMECapture struct {
	buffer   bytes.Buffer
	maximum  int
	overflow bool
}

func newBoundedSMIMECapture(maximum int) *boundedSMIMECapture {
	return &boundedSMIMECapture{maximum: maximum}
}

func (capture *boundedSMIMECapture) Write(value []byte) (int, error) {
	if capture == nil {
		return len(value), nil
	}
	if !capture.overflow {
		remaining := capture.maximum - capture.buffer.Len()
		if remaining > len(value) {
			remaining = len(value)
		}
		if remaining > 0 {
			_, _ = capture.buffer.Write(value[:remaining])
		}
		if remaining < len(value) {
			capture.overflow = true
		}
	}
	return len(value), nil
}

func (capture *boundedSMIMECapture) Bytes() []byte {
	if capture == nil {
		return nil
	}
	return capture.buffer.Bytes()
}

type smimeWalkInspector struct {
	expectedEmail string
	policy        *SMIMEPolicy
	status        SMIMEStatus
	seen          bool
}

func newSMIMEWalkInspector(expectedEmail string, policy *SMIMEPolicy) *smimeWalkInspector {
	revision := int64(0)
	if policy != nil {
		revision = policy.Revision
	}
	return &smimeWalkInspector{
		expectedEmail: normalizeCertificateEmail(expectedEmail),
		policy:        policy,
		status:        SMIMEStatus{Status: SMIMEStatusAbsent, Trust: SMIMETrustNotEvaluated, PolicyRevision: revision},
	}
}

func (inspector *smimeWalkInspector) Status() SMIMEStatus {
	if inspector == nil {
		return SMIMEStatus{Status: SMIMEStatusAbsent, Trust: SMIMETrustNotEvaluated}
	}
	return inspector.status
}

func (inspector *smimeWalkInspector) consider(candidate SMIMEStatus) {
	if inspector == nil || inspector.seen {
		return
	}
	if inspector.policy != nil {
		candidate.PolicyRevision = inspector.policy.Revision
	}
	inspector.seen = true
	inspector.status = candidate
}

func (inspector *smimeWalkInspector) InspectMultipartSigned(path, boundary string, capture *boundedSMIMECapture) {
	if inspector == nil || inspector.seen {
		return
	}
	if capture == nil || capture.overflow {
		inspector.consider(NewSMIMEMalformedStatus(SMIMEContainerMultipartSigned, path, "container_too_large"))
		return
	}
	if boundary == "" {
		inspector.consider(NewSMIMEMalformedStatus(SMIMEContainerMultipartSigned, path, "boundary_missing"))
		return
	}
	signedEntity, signatureEntity, err := splitSMIMEMultipartSigned(capture.Bytes(), boundary)
	if err != nil {
		inspector.consider(NewSMIMEMalformedStatus(SMIMEContainerMultipartSigned, path, "multipart_structure"))
		return
	}
	signature, err := decodeSMIMESignatureEntity(signatureEntity)
	if err != nil {
		inspector.consider(NewSMIMEMalformedStatus(SMIMEContainerMultipartSigned, path, "signature_entity"))
		return
	}
	inspector.consider(VerifySMIMEDetached(signature, signedEntity, inspector.expectedEmail, path, inspector.policy))
}

func (inspector *smimeWalkInspector) InspectOpaque(path string, kind SMIMEContainerKind, capture *boundedSMIMECapture) []byte {
	if inspector == nil || inspector.seen {
		return nil
	}
	if capture == nil || capture.overflow {
		inspector.consider(NewSMIMEMalformedStatus(kind, path, "container_too_large"))
		return nil
	}
	if kind == SMIMEContainerOpaqueEncrypted {
		inspector.consider(NewSMIMEEncryptedStatus(path))
		return nil
	}
	status, content := inspectSMIMEOpaque(capture.Bytes(), inspector.expectedEmail, path, inspector.policy)
	inspector.consider(status)
	return content
}

func isSMIMEMultipartSigned(mediaType string, params map[string]string) bool {
	if strings.ToLower(strings.TrimSpace(mediaType)) != "multipart/signed" {
		return false
	}
	protocol := strings.ToLower(strings.TrimSpace(params["protocol"]))
	return protocol == "application/pkcs7-signature" || protocol == "application/x-pkcs7-signature"
}

func smimeOpaqueContainerKind(mediaType string, params map[string]string) SMIMEContainerKind {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != "application/pkcs7-mime" && mediaType != "application/x-pkcs7-mime" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(params["smime-type"])) {
	case "signed-data":
		return SMIMEContainerOpaqueSigned
	case "enveloped-data", "":
		return SMIMEContainerOpaqueEncrypted
	default:
		return ""
	}
}

type smimeBoundaryLine struct {
	start   int
	end     int
	newline int
	final   bool
}

// splitSMIMEMultipartSigned returns the exact wire representation of the
// signed MIME entity and the raw signature-control MIME entity. Per RFC 2046,
// the CRLF immediately before a boundary belongs to that boundary rather than
// to the preceding child, so it is excluded from both returned parts.
func splitSMIMEMultipartSigned(body []byte, boundary string) ([]byte, []byte, error) {
	if boundary == "" || len(boundary) > 70 {
		return nil, nil, errors.New("invalid multipart boundary")
	}
	prefix := []byte("--" + boundary)
	var lines []smimeBoundaryLine
	for start := 0; start <= len(body); {
		end := bytes.IndexByte(body[start:], '\n')
		newline := 0
		lineEnd := len(body)
		if end >= 0 {
			lineEnd = start + end
			newline = 1
			if lineEnd > start && body[lineEnd-1] == '\r' {
				lineEnd--
				newline = 2
			}
		}
		line := body[start:lineEnd]
		if bytes.HasPrefix(line, prefix) {
			rest := bytes.TrimRight(line[len(prefix):], " \t")
			if len(rest) == 0 || bytes.Equal(rest, []byte("--")) {
				physicalEnd := lineEnd
				if newline == 2 {
					physicalEnd += 2
				} else if newline == 1 {
					physicalEnd++
				}
				lines = append(lines, smimeBoundaryLine{start: start, end: physicalEnd, newline: newline, final: bytes.Equal(rest, []byte("--"))})
			}
		}
		if end < 0 {
			break
		}
		start += end + 1
	}
	if len(lines) != 3 || lines[0].final || lines[1].final || !lines[2].final {
		return nil, nil, errors.New("multipart/signed must contain exactly two parts")
	}
	firstEnd := boundarySeparatedPartEnd(body, lines[1].start)
	secondEnd := boundarySeparatedPartEnd(body, lines[2].start)
	if lines[0].end > firstEnd || lines[1].end > secondEnd {
		return nil, nil, errors.New("multipart/signed part bounds are invalid")
	}
	first := append([]byte(nil), body[lines[0].end:firstEnd]...)
	second := append([]byte(nil), body[lines[1].end:secondEnd]...)
	if len(first) == 0 || len(second) == 0 {
		return nil, nil, errors.New("multipart/signed part is empty")
	}
	return first, second, nil
}

func boundarySeparatedPartEnd(body []byte, boundaryStart int) int {
	end := boundaryStart
	if end >= 2 && body[end-2] == '\r' && body[end-1] == '\n' {
		return end - 2
	}
	if end >= 1 && body[end-1] == '\n' {
		return end - 1
	}
	return end
}

func decodeSMIMESignatureEntity(raw []byte) ([]byte, error) {
	entity, err := message.Read(bytes.NewReader(raw))
	if entity == nil {
		return nil, err
	}
	mediaType, _, _ := entity.Header.ContentType()
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != "application/pkcs7-signature" && mediaType != "application/x-pkcs7-signature" {
		return nil, errors.New("multipart/signed control part is not a PKCS#7 signature")
	}
	value, err := io.ReadAll(io.LimitReader(entity.Body, maxSMIMEContainerBytes+1))
	if err != nil {
		return nil, err
	}
	if len(value) == 0 || len(value) > maxSMIMEContainerBytes {
		return nil, errors.New("PKCS#7 signature exceeds supported bound")
	}
	return value, nil
}

func ParseSMIMECertificate(data []byte) (*x509.Certificate, error) {
	if len(data) == 0 || len(data) > 64<<10 {
		return nil, fmt.Errorf("%w: certificate must be 1-65536 bytes", ErrSMIMECertificateInvalid)
	}
	der := data
	if block, rest := pem.Decode(data); block != nil {
		if len(strings.TrimSpace(string(rest))) != 0 || block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("%w: expected exactly one PEM CERTIFICATE block", ErrSMIMECertificateInvalid)
		}
		der = block.Bytes
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("%w: parse X.509 certificate: %v", ErrSMIMECertificateInvalid, err)
	}
	return cert, nil
}

func SMIMECertificateFromX509(cert *x509.Certificate) SMIMECertificate {
	if cert == nil {
		return SMIMECertificate{}
	}
	return SMIMECertificate{
		FingerprintSHA256: certificateFingerprint(cert),
		Subject:           boundedCertificateName(cert.Subject.String()),
		Issuer:            boundedCertificateName(cert.Issuer.String()),
		Emails:            certificateEmails(cert),
		NotBefore:         cert.NotBefore.UTC(),
		NotAfter:          cert.NotAfter.UTC(),
		IsCA:              cert.IsCA,
		KeyUsage:          keyUsageNames(cert.KeyUsage),
		ExtendedKeyUsage:  extendedKeyUsageNames(cert.ExtKeyUsage),
		DER:               append([]byte(nil), cert.Raw...),
	}
}

func VerifySMIMEDetached(signatureDER, signedContent []byte, expectedEmail, path string, policy *SMIMEPolicy) SMIMEStatus {
	return verifySMIMEPKCS7(signatureDER, signedContent, expectedEmail, path, SMIMEContainerMultipartSigned, policy)
}

func VerifySMIMEOpaque(signedData []byte, expectedEmail, path string, policy *SMIMEPolicy) SMIMEStatus {
	status, _ := inspectSMIMEOpaque(signedData, expectedEmail, path, policy)
	return status
}

func inspectSMIMEOpaque(signedData []byte, expectedEmail, path string, policy *SMIMEPolicy) (SMIMEStatus, []byte) {
	status := SMIMEStatus{Status: SMIMEStatusMalformed, Container: SMIMEContainerOpaqueSigned, Path: path, Signed: true, Trust: SMIMETrustNotEvaluated}
	p7, err := pkcs7.Parse(signedData)
	if err != nil {
		status.Reason = "cms_parse"
		return status, nil
	}
	content := append([]byte(nil), p7.Content...)
	return verifyParsedSMIMEPKCS7(p7, expectedEmail, path, SMIMEContainerOpaqueSigned, policy), content
}

func NewSMIMEEncryptedStatus(path string) SMIMEStatus {
	return SMIMEStatus{Status: SMIMEStatusAbsent, Container: SMIMEContainerOpaqueEncrypted, Path: path, Encrypted: true, Trust: SMIMETrustNotEvaluated}
}

func NewSMIMEMalformedStatus(container SMIMEContainerKind, path, reason string) SMIMEStatus {
	return SMIMEStatus{Status: SMIMEStatusMalformed, Container: container, Path: path, Signed: container != SMIMEContainerOpaqueEncrypted, Encrypted: container == SMIMEContainerOpaqueEncrypted, Trust: SMIMETrustNotEvaluated, Reason: boundedSMIMEReason(reason)}
}

func verifySMIMEPKCS7(signatureDER, detached []byte, expectedEmail, path string, container SMIMEContainerKind, policy *SMIMEPolicy) SMIMEStatus {
	status := SMIMEStatus{Status: SMIMEStatusMalformed, Container: container, Path: path, Signed: true, Trust: SMIMETrustNotEvaluated}
	p7, err := pkcs7.Parse(signatureDER)
	if err != nil {
		status.Reason = "cms_parse"
		return status
	}
	if detached != nil {
		p7.Content = detached
	}
	return verifyParsedSMIMEPKCS7(p7, expectedEmail, path, container, policy)
}

func verifyParsedSMIMEPKCS7(p7 *pkcs7.PKCS7, expectedEmail, path string, container SMIMEContainerKind, policy *SMIMEPolicy) SMIMEStatus {
	status := SMIMEStatus{Status: SMIMEStatusMalformed, Container: container, Path: path, Signed: true, Trust: SMIMETrustNotEvaluated}
	if p7 == nil {
		status.Reason = "cms_parse"
		return status
	}
	if len(p7.Signers) != 1 {
		status.Reason = "signer_count"
		return status
	}
	signer := p7.GetOnlySigner()
	if signer == nil {
		status.Reason = "signer_certificate_missing"
		return status
	}
	metadata := signerMetadata(p7, signer)
	status.Signer = &metadata
	if err := p7.Verify(); err != nil {
		status.Status = SMIMEStatusInvalid
		status.Reason = "signature_invalid"
		return status
	}
	status.SignatureValid = true

	now := time.Now().UTC()
	if policy != nil && !policy.Now.IsZero() {
		now = policy.Now.UTC()
	}
	if now.Before(signer.NotBefore) || now.After(signer.NotAfter) {
		status.Status = SMIMEStatusExpired
		status.Reason = "certificate_not_current"
		return status
	}
	if signer.KeyUsage != 0 && signer.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		status.Status = SMIMEStatusInvalid
		status.Reason = "digital_signature_key_usage_missing"
		return status
	}
	if !allowsEmailProtection(signer) {
		status.Status = SMIMEStatusInvalid
		status.Reason = "email_protection_key_usage_missing"
		return status
	}

	expectedEmail = normalizeCertificateEmail(expectedEmail)
	matched := expectedEmail != "" && certificateHasEmail(signer, expectedEmail)
	status.IdentityMatched = &matched
	if !matched {
		status.Status = SMIMEStatusIdentityMismatch
		status.Trust = evaluateSMIMETrust(signer, p7.Certificates, expectedEmail, policy, now)
		status.Reason = "signer_email_mismatch"
		return status
	}

	status.Trust = evaluateSMIMETrust(signer, p7.Certificates, expectedEmail, policy, now)
	if status.Trust == SMIMETrustTrusted {
		status.Status = SMIMEStatusValidTrusted
	} else {
		status.Status = SMIMEStatusValidUntrusted
	}
	return status
}

func evaluateSMIMETrust(signer *x509.Certificate, embedded []*x509.Certificate, expectedEmail string, policy *SMIMEPolicy, now time.Time) SMIMETrustResult {
	if policy == nil || policy.Roots == nil {
		return SMIMETrustUntrusted
	}
	roots := policy.Roots.Clone()
	intermediates := x509.NewCertPool()
	for _, cert := range embedded {
		if cert != nil && !cert.Equal(signer) {
			intermediates.AddCert(cert)
		}
	}
	for _, material := range policy.Certificates {
		cert := material.Certificate
		if cert == nil || normalizeCertificateEmail(material.Email) != normalizeCertificateEmail(expectedEmail) {
			continue
		}
		if material.Trusted {
			roots.AddCert(cert)
		} else if !cert.Equal(signer) {
			intermediates.AddCert(cert)
		}
	}
	_, err := signer.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}})
	if err != nil {
		return SMIMETrustUntrusted
	}
	return SMIMETrustTrusted
}

func signerMetadata(p7 *pkcs7.PKCS7, cert *x509.Certificate) SMIMESigner {
	metadata := SMIMESigner{
		FingerprintSHA256: certificateFingerprint(cert), Subject: boundedCertificateName(cert.Subject.String()), Issuer: boundedCertificateName(cert.Issuer.String()),
		Emails: certificateEmails(cert), NotBefore: cert.NotBefore.UTC(), NotAfter: cert.NotAfter.UTC(), KeyUsage: keyUsageNames(cert.KeyUsage), ExtendedKeyUsage: extendedKeyUsageNames(cert.ExtKeyUsage),
	}
	var signingTime time.Time
	if err := p7.UnmarshalSignedAttribute(pkcs7.OIDAttributeSigningTime, &signingTime); err == nil && !signingTime.IsZero() {
		signingTime = signingTime.UTC()
		metadata.SigningTime = &signingTime
	}
	return metadata
}

func certificateFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

func certificateEmails(cert *x509.Certificate) []string {
	if cert == nil {
		return nil
	}
	seen := map[string]struct{}{}
	values := make([]string, 0, len(cert.EmailAddresses)+1)
	appendEmail := func(value string) {
		value = normalizeCertificateEmail(value)
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	for _, value := range cert.EmailAddresses {
		appendEmail(value)
	}
	for _, name := range cert.Subject.Names {
		if name.Type.Equal([]int{1, 2, 840, 113549, 1, 9, 1}) {
			if value, ok := name.Value.(string); ok {
				appendEmail(value)
			}
		}
	}
	if len(values) > 16 {
		values = values[:16]
	}
	return values
}

func certificateHasEmail(cert *x509.Certificate, expected string) bool {
	expected = normalizeCertificateEmail(expected)
	for _, value := range certificateEmails(cert) {
		if value == expected {
			return true
		}
	}
	return false
}

func normalizeCertificateEmail(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) > 320 || !strings.Contains(value, "@") {
		return ""
	}
	return value
}

func allowsEmailProtection(cert *x509.Certificate) bool {
	if cert == nil || len(cert.ExtKeyUsage) == 0 {
		return true
	}
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageEmailProtection || usage == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}

func keyUsageNames(value x509.KeyUsage) []string {
	pairs := []struct {
		bit  x509.KeyUsage
		name string
	}{
		{x509.KeyUsageDigitalSignature, "digital-signature"}, {x509.KeyUsageContentCommitment, "content-commitment"},
		{x509.KeyUsageKeyEncipherment, "key-encipherment"}, {x509.KeyUsageDataEncipherment, "data-encipherment"},
		{x509.KeyUsageKeyAgreement, "key-agreement"}, {x509.KeyUsageCertSign, "cert-sign"}, {x509.KeyUsageCRLSign, "crl-sign"},
		{x509.KeyUsageEncipherOnly, "encipher-only"}, {x509.KeyUsageDecipherOnly, "decipher-only"},
	}
	var values []string
	for _, pair := range pairs {
		if value&pair.bit != 0 {
			values = append(values, pair.name)
		}
	}
	return values
}

func extendedKeyUsageNames(values []x509.ExtKeyUsage) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		name := "unknown"
		switch value {
		case x509.ExtKeyUsageAny:
			name = "any"
		case x509.ExtKeyUsageServerAuth:
			name = "server-auth"
		case x509.ExtKeyUsageClientAuth:
			name = "client-auth"
		case x509.ExtKeyUsageCodeSigning:
			name = "code-signing"
		case x509.ExtKeyUsageEmailProtection:
			name = "email-protection"
		case x509.ExtKeyUsageTimeStamping:
			name = "time-stamping"
		case x509.ExtKeyUsageOCSPSigning:
			name = "ocsp-signing"
		}
		result = append(result, name)
	}
	return result
}

func boundedCertificateName(value string) string {
	value = strings.ToValidUTF8(strings.TrimSpace(value), "�")
	if len(value) > 1024 {
		value = value[:1024]
	}
	return value
}

func boundedSMIMEReason(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 80 {
		value = value[:80]
	}
	return value
}
