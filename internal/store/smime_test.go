package store

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestPhase32SMIMECertificateLifecycleIsScopedBoundedAndExplicitTrust(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
	cert := phase32StoreCertificate(t, "sender@example.test", now)
	value, err := state.SMIMECertificates().Import(context.Background(), accountID, "smc_0198d704-6c00-7000-8000-000000032001", "sender@example.test", cert, false, now)
	if err != nil || value.Trusted || value.Email != "sender@example.test" || value.FingerprintSHA256 == "" || len(value.DER) == 0 {
		t.Fatalf("import=%+v err=%v", value, err)
	}
	if revision, err := state.SMIMECertificates().PolicyRevision(context.Background(), accountID); err != nil || revision != 1 {
		t.Fatalf("policy revision after import=%d err=%v", revision, err)
	}
	page, err := state.SMIMECertificates().ListPage(context.Background(), accountID, "sender@example.test", "", 1)
	if err != nil || len(page.Certificates) != 1 || page.Certificates[0].ID != value.ID {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	value2, err := state.SMIMECertificates().Import(context.Background(), accountID, "smc_0198d704-6c00-7000-8000-000000032002", "sender@example.test", cert, true, now.Add(time.Minute))
	if err != nil || !value2.Trusted || value2.ID != value.ID {
		t.Fatalf("trusted upsert=%+v err=%v", value2, err)
	}
	if revision, err := state.SMIMECertificates().PolicyRevision(context.Background(), accountID); err != nil || revision != 2 {
		t.Fatalf("policy revision after trust update=%d err=%v", revision, err)
	}
	materials, err := state.SMIMECertificates().Materials(context.Background(), accountID)
	if err != nil || len(materials) != 1 || !materials[0].Trusted || materials[0].Certificate == nil {
		t.Fatalf("materials=%+v err=%v", materials, err)
	}
	if _, err := state.SMIMECertificates().Import(context.Background(), accountID, "smc_0198d704-6c00-7000-8000-000000032003", "wrong@example.test", cert, false, now); err == nil {
		t.Fatal("certificate imported under mismatched mailbox identity")
	}
	removed, err := state.SMIMECertificates().Remove(context.Background(), accountID, value.ID, now.Add(2*time.Minute))
	if err != nil || removed.ID != value.ID {
		t.Fatalf("remove=%+v err=%v", removed, err)
	}
	if revision, err := state.SMIMECertificates().PolicyRevision(context.Background(), accountID); err != nil || revision != 3 {
		t.Fatalf("policy revision after remove=%d err=%v", revision, err)
	}
	if _, err := state.SMIMECertificates().Resolve(context.Background(), accountID, value.ID); err != maildomain.ErrSMIMECertificateNotFound {
		t.Fatalf("resolve after remove=%v", err)
	}
}

func TestPhase32SMIMECertificateImportRollsBackWhenPolicyRevisionFails(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 11, 2, 10, 0, 0, time.UTC)
	cert := phase32StoreCertificate(t, "sender@example.test", now)
	if _, err := state.db.ExecContext(context.Background(), `DROP TABLE smime_policy_state`); err != nil {
		t.Fatal(err)
	}
	_, err := state.SMIMECertificates().Import(context.Background(), accountID, "smc_0198d704-6c00-7000-8000-000000032010", "sender@example.test", cert, true, now)
	if err == nil {
		t.Fatal("import unexpectedly committed when policy revision failed")
	}
	var count int
	if err := state.db.QueryRowContext(context.Background(), `SELECT count(*) FROM smime_certificates WHERE account_id = ?`, accountID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("certificate import was not rolled back: count=%d", count)
	}
}

func TestPhase32SMIMECertificateInventoryHasExplicitAccountBound(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 11, 2, 20, 0, 0, time.UTC)
	cert := phase32StoreCertificate(t, "sender@example.test", now)
	timestamp := now.Format(time.RFC3339Nano)
	tx, err := state.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maildomain.MaxSMIMECertificatesPerAccount; index++ {
		id := fmt.Sprintf("smc_%032d", index)
		fingerprint := fmt.Sprintf("%064x", index+1)
		if _, err := tx.ExecContext(context.Background(), `
INSERT INTO smime_certificates (certificate_id, account_id, email, fingerprint_sha256, der, trusted, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 0, ?, ?)`, id, accountID, "sender@example.test", fingerprint, cert.Raw, timestamp, timestamp); err != nil {
			t.Fatalf("seed certificate %d: %v", index, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	other := phase32StoreCertificate(t, "other@example.test", now)
	_, err = state.SMIMECertificates().Import(context.Background(), accountID, "smc_limit_overflow", "other@example.test", other, false, now.Add(time.Minute))
	if err != maildomain.ErrSMIMECertificateLimit {
		t.Fatalf("overflow error=%v", err)
	}
	page, err := state.SMIMECertificates().ListPage(context.Background(), accountID, "", "", 100)
	if err != nil || len(page.Certificates) != 100 || !page.More || !strings.HasPrefix(page.NextCursor, "smcur_") {
		t.Fatalf("bounded page=%+v err=%v", page, err)
	}
}

func TestPhase32SMIMEPolicyRevisionMakesCachedVerdictStaleWithoutMailboxRewrite(t *testing.T) {
	state, accountID, folderID := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 11, 2, 25, 0, 0, time.UTC)
	record := mailRecordFixture(accountID, folderID, 100, 32, "phase32-policy@example.test")
	record.Content = &maildomain.NormalizedContent{
		Preview: record.Parsed,
		Headers: []maildomain.HeaderField{{Key: "Message-ID", Value: "<phase32-policy@example.test>"}},
		SMIME: &maildomain.SMIMEStatus{
			Status: maildomain.SMIMEStatusValidUntrusted, Signed: true, SignatureValid: true,
			Trust: maildomain.SMIMETrustUntrusted, PolicyRevision: 0,
		},
		Parts: []maildomain.MIMEPart{{ID: "prt_phase32_policy", Ordinal: 0, Path: "0", MediaType: "text/plain"}},
	}
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_phase32_policy", AccountID: accountID, FolderID: folderID,
		UIDValidity: 100, Bootstrap: true, LastCommittedUID: 32, LastScannedUID: 32,
		AttemptedAt: now, Records: []maildomain.IngestRecord{record},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := state.Mail().Message(context.Background(), accountID, record.MessageID)
	if err != nil || before.Content == nil || before.Content.SMIME == nil {
		t.Fatalf("cached verdict before policy change=%+v err=%v", before.Content, err)
	}
	if err := bumpSMIMEPolicyRevision(context.Background(), state.db, accountID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	after, err := state.Mail().Message(context.Background(), accountID, record.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Content == nil || after.Content.SMIME != nil || len(after.Content.Parts) != 1 {
		t.Fatalf("stale verdict was not hidden while ordinary MIME stayed readable: %+v", after.Content)
	}
}

func phase32StoreCertificate(t *testing.T, email string, now time.Time) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(32), Subject: pkix.Name{CommonName: "Phase32 Store"}, EmailAddresses: []string{email}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
