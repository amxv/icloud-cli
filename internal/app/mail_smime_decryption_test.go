package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/smallstep/pkcs7"
)

func TestOnDemandDecryptUsesExternalKeyAndLeavesCanonicalRawEncrypted(t *testing.T) {
	home := t.TempDir()
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	now := time.Date(2026, 8, 11, 4, 30, 0, 0, time.UTC)
	cert, key := smimeDecryptionRSACertificate(t, "owner@icloud.com", now)
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	decryptedEntity := []byte("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 7bit\r\n\r\nsmime decryption secret body")
	raw := smimeDecryptionEncryptedMessage(t, "outside@example.test", "owner@icloud.com", "smime-decryption-decrypt@example.test", "[ICLOUD-CLI-FIXTURE] S/MIME decryption decrypt", decryptedEntity, cert)
	messageID := seedSMIMEDecryptionEncryptedMessage(t, commands, home, raw, now)

	_, ordinary, ordinarySource, _, err := commands.MailMessage(context.Background(), home, "", messageID)
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.Content == nil || ordinary.Content.SMIME == nil || !ordinary.Content.SMIME.Encrypted || ordinary.Content.SMIME.Decryption != nil || strings.Contains(ordinary.Snippet, "smime decryption secret") {
		t.Fatalf("ordinary encrypted message=%+v", ordinary)
	}
	var rawBefore bytes.Buffer
	if err := ordinarySource.WriteRaw(context.Background(), &rawBefore); err != nil || rawBefore.String() != raw {
		t.Fatalf("ordinary raw exact=%v err=%v", rawBefore.String() == raw, err)
	}
	if _, _, _, _, err := commands.MailMessageDecrypt(context.Background(), home, "", messageID); err == nil {
		t.Fatal("decrypt without configured identity unexpectedly succeeded")
	}

	_, certificate, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "owner@icloud.com", cert.Raw, false)
	if err != nil {
		t.Fatal(err)
	}
	_, identity, _, err := commands.MailSMIMEIdentityConfigure(context.Background(), home, "", "owner@icloud.com", certificate.ID, account.CredentialSetup{Scheme: account.CredentialKeyring, Secret: append([]byte(nil), privatePEM...)})
	if err != nil || identity.ID == "" || identity.PrivateKeyScheme != account.CredentialKeyring || !identity.PrivateKeyConfigured || identity.PrivateKeyReference == "" {
		t.Fatalf("configured identity=%+v err=%v", identity, err)
	}
	_, decrypted, source, _, err := commands.MailMessageDecrypt(context.Background(), home, "", messageID)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted.Content == nil || decrypted.Content.SMIME == nil || decrypted.Content.SMIME.Decryption == nil || decrypted.Content.SMIME.Decryption.Status != "decrypted" || decrypted.Content.SMIME.Decryption.SMIMEIdentityID != identity.ID || !strings.Contains(decrypted.Snippet, "smime decryption secret body") || !decrypted.Content.SMIME.Encrypted {
		t.Fatalf("decrypted message=%+v", decrypted)
	}
	var body bytes.Buffer
	if err := source.WriteBody(context.Background(), maildomain.ContentText, &body); err != nil || !strings.Contains(body.String(), "smime decryption secret body") {
		t.Fatalf("decrypted body=%q err=%v", body.String(), err)
	}
	var rawAfter bytes.Buffer
	if err := source.WriteRaw(context.Background(), &rawAfter); err != nil || rawAfter.String() != raw {
		t.Fatalf("decrypted source raw must remain provider ciphertext exact=%v err=%v", rawAfter.String() == raw, err)
	}
	_, persisted, _, _, err := commands.MailMessage(context.Background(), home, "", messageID)
	if err != nil || persisted.Content == nil || persisted.Content.SMIME == nil || persisted.Content.SMIME.Decryption != nil || strings.Contains(persisted.Snippet, "smime decryption secret body") {
		t.Fatalf("decryption persisted plaintext unexpectedly: message=%+v err=%v", persisted, err)
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID, "--decrypt", "--full")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "smime_decryption=decrypted") || !strings.Contains(stdout, "smime decryption secret body") || strings.Contains(stdout, identity.PrivateKeyReference) {
		t.Fatalf("public decrypt code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID, "--decrypt", "--format", "raw")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "raw always means the exact encrypted provider message") {
		t.Fatalf("decrypt raw safety code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestCLIConfiguresPrivateKeyFromRedirectedStdinWithoutLeakage(t *testing.T) {
	home := t.TempDir()
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	now := time.Date(2026, 8, 11, 4, 40, 0, 0, time.UTC)
	cert, key := smimeDecryptionRSACertificate(t, "owner@icloud.com", now)
	_, certificate, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "owner@icloud.com", cert.Raw, false)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	code, stdout, stderr := runWithAccountCommandsReader(commands, bytes.NewReader(privatePEM), "--home", home,
		"mail", "smime", "identities", "configure", "owner@icloud.com", "--certificate", certificate.ID, "--private-key-stdin", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"private_key_scheme":"keyring"`) || !strings.Contains(stdout, `"private_key_configured":true`) || strings.Contains(stdout, "PRIVATE KEY") || strings.Contains(stdout, "private_key_reference") {
		t.Fatalf("configure code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	databaseBytes, err := os.ReadFile(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(databaseBytes, privatePEM) || bytes.Contains(databaseBytes, []byte("-----BEGIN RSA PRIVATE KEY-----")) {
		t.Fatal("private key material was written into the SQLite state")
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "smime", "identities", "list", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"count":1`) || strings.Contains(stdout, "private_key_reference") || strings.Contains(stdout, "PRIVATE KEY") {
		t.Fatalf("list code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestConfigureRejectsMismatchedAndUnsafePrivateKeys(t *testing.T) {
	home := t.TempDir()
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	now := time.Date(2026, 8, 11, 4, 35, 0, 0, time.UTC)
	cert, _ := smimeDecryptionRSACertificate(t, "owner@icloud.com", now)
	_, wrongKey := smimeDecryptionRSACertificate(t, "owner@icloud.com", now.Add(time.Second))
	_, certificate, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "owner@icloud.com", cert.Raw, false)
	if err != nil {
		t.Fatal(err)
	}
	wrongPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(wrongKey)})
	if _, _, _, err := commands.MailSMIMEIdentityConfigure(context.Background(), home, "", "owner@icloud.com", certificate.ID, account.CredentialSetup{Scheme: account.CredentialKeyring, Secret: wrongPEM}); err == nil {
		t.Fatal("mismatched private key unexpectedly configured")
	}
	unsafePath := t.TempDir() + "/key.pem"
	if err := x509WritePrivateKeyFile(unsafePath, wrongKey, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := commands.MailSMIMEIdentityConfigure(context.Background(), home, "", "owner@icloud.com", certificate.ID, account.CredentialSetup{Scheme: account.CredentialFile, Reference: unsafePath}); err == nil {
		t.Fatal("world-readable private key file unexpectedly configured")
	}
}

func TestDecryptsNestedSignedContentWithOuterSenderIdentity(t *testing.T) {
	home := t.TempDir()
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
// PKCS#7 emits a real signing timestamp; certificates must cover it.
now := time.Now().UTC()
	commands.now = func() time.Time { return now }
	recipientCert, recipientKey := smimeDecryptionRSACertificate(t, "owner@icloud.com", now)
	signerCert, signerKey := smimeDecryptionRSACertificate(t, "outside@example.test", now.Add(time.Second))
	signedEntity := []byte("Content-Type: text/plain; charset=utf-8\r\n\r\nsmimeid nested signed secret")
	nested := smimeDecryptionDetachedSignedEntity(t, signedEntity, signerCert, signerKey)
	raw := smimeDecryptionEncryptedMessage(t, "outside@example.test", "owner@icloud.com", "smime-decryption-nested@example.test", "[ICLOUD-CLI-FIXTURE] S/MIME decryption nested signed", nested, recipientCert)
	messageID := seedSMIMEDecryptionEncryptedMessage(t, commands, home, raw, now)
	_, recipientStored, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "owner@icloud.com", recipientCert.Raw, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "outside@example.test", signerCert.Raw, true); err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(recipientKey)})
	if _, _, _, err := commands.MailSMIMEIdentityConfigure(context.Background(), home, "", "owner@icloud.com", recipientStored.ID, account.CredentialSetup{Scheme: account.CredentialKeyring, Secret: privatePEM}); err != nil {
		t.Fatal(err)
	}
	_, message, _, _, err := commands.MailMessageDecrypt(context.Background(), home, "", messageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.Content == nil || message.Content.SMIME == nil || !message.Content.SMIME.Encrypted || !message.Content.SMIME.Signed || message.Content.SMIME.Status != maildomain.SMIMEStatusValidTrusted || message.Content.SMIME.Trust != maildomain.SMIMETrustTrusted || message.Content.SMIME.IdentityMatched == nil || !*message.Content.SMIME.IdentityMatched || message.Content.SMIME.Decryption == nil || !strings.Contains(message.Snippet, "nested signed secret") {
		t.Fatalf("nested decrypted S/MIME=%+v message=%+v", message.Content.SMIME, message)
	}
}

func TestFileReferenceSurvivesCommandRestartAndMissingEnvFailsClosed(t *testing.T) {
	home := t.TempDir()
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	now := time.Date(2026, 8, 11, 4, 50, 0, 0, time.UTC)
	commands.now = func() time.Time { return now }
	cert, key := smimeDecryptionRSACertificate(t, "owner@icloud.com", now)
	raw := smimeDecryptionEncryptedMessage(t, "outside@example.test", "owner@icloud.com", "smime-decryption-restart@example.test", "[ICLOUD-CLI-FIXTURE] S/MIME decryption restart", []byte("Content-Type: text/plain\r\n\r\nrestart secret"), cert)
	messageID := seedSMIMEDecryptionEncryptedMessage(t, commands, home, raw, now)
	_, certificate, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "owner@icloud.com", cert.Raw, false)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := t.TempDir() + "/smime-key.pem"
	if err := x509WritePrivateKeyFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := commands.MailSMIMEIdentityConfigure(context.Background(), home, "", "owner@icloud.com", certificate.ID, account.CredentialSetup{Scheme: account.CredentialFile, Reference: keyPath}); err != nil {
		t.Fatal(err)
	}
	restarted, _ := newTestAccountCommands()
	restarted.now = commands.now
	_, decrypted, _, _, err := restarted.MailMessageDecrypt(context.Background(), home, "", messageID)
	if err != nil || !strings.Contains(decrypted.Snippet, "restart secret") {
		t.Fatalf("file-reference restart decrypt message=%+v err=%v", decrypted, err)
	}
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	t.Setenv("SMIME_DECRYPTION_PRIVATE_KEY", privatePEM)
	if _, _, _, err := commands.MailSMIMEIdentityConfigure(context.Background(), home, "", "owner@icloud.com", certificate.ID, account.CredentialSetup{Scheme: account.CredentialEnv, Reference: "SMIME_DECRYPTION_PRIVATE_KEY"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv("SMIME_DECRYPTION_PRIVATE_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := restarted.MailMessageDecrypt(context.Background(), home, "", messageID); err == nil {
		t.Fatal("decrypt with missing external environment key unexpectedly succeeded")
	}
}

func TestAccountRemovalDeletesOwnedSMIMEKeyringSecret(t *testing.T) {
	home := t.TempDir()
	commands, keyringStore := newTestAccountCommands()
	addSendAccount(t, commands, home)
	now := time.Date(2026, 8, 11, 4, 55, 0, 0, time.UTC)
	cert, key := smimeDecryptionRSACertificate(t, "owner@icloud.com", now)
	_, certificate, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "owner@icloud.com", cert.Raw, false)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	_, identity, _, err := commands.MailSMIMEIdentityConfigure(context.Background(), home, "", "owner@icloud.com", certificate.ID, account.CredentialSetup{Scheme: account.CredentialKeyring, Secret: privatePEM})
	if err != nil {
		t.Fatal(err)
	}
	manager := secret.NewManager(keyringStore)
	credential := account.Credential{Scheme: account.CredentialKeyring, Reference: identity.PrivateKeyReference, Configured: true}
	resolved, err := manager.Resolve(context.Background(), credential)
	if err != nil || len(resolved) == 0 {
		t.Fatalf("configured keyring secret unavailable before account removal: %v", err)
	}
	zeroBytes(resolved)
	if _, _, _, err := commands.Remove(context.Background(), home, "owner@icloud.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Resolve(context.Background(), credential); !errors.Is(err, secret.ErrMissing) {
		t.Fatalf("S/MIME keyring secret survived account removal: %v", err)
	}
}

func TestSMIMEIdentityRemovalRequiresYesAndDeletesOwnedKeyringSecret(t *testing.T) {
	home := t.TempDir()
	commands, keyringStore := newTestAccountCommands()
	addSendAccount(t, commands, home)
	now := time.Date(2026, 8, 11, 4, 57, 0, 0, time.UTC)
	cert, key := smimeDecryptionRSACertificate(t, "owner@icloud.com", now)
	_, certificate, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "owner@icloud.com", cert.Raw, false)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	_, identity, _, err := commands.MailSMIMEIdentityConfigure(context.Background(), home, "", "owner@icloud.com", certificate.ID, account.CredentialSetup{Scheme: account.CredentialKeyring, Secret: privatePEM})
	if err != nil {
		t.Fatal(err)
	}
	manager := secret.NewManager(keyringStore)
	credential := account.Credential{Scheme: account.CredentialKeyring, Reference: identity.PrivateKeyReference, Configured: true}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "smime", "identities", "remove", identity.ID)
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "requires --yes") {
		t.Fatalf("unguarded remove code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	resolved, err := manager.Resolve(context.Background(), credential)
	if err != nil || len(resolved) == 0 {
		t.Fatalf("unguarded remove changed keyring secret: %v", err)
	}
	zeroBytes(resolved)
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "smime", "identities", "remove", identity.ID, "--yes")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "removed=true") {
		t.Fatalf("authorized remove code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := manager.Resolve(context.Background(), credential); !errors.Is(err, secret.ErrMissing) {
		t.Fatalf("owned keyring secret survived authorized S/MIME identity removal: %v", err)
	}
}

func TestDisabledMailIdentityCannotDecryptUntilReenabled(t *testing.T) {
	home := t.TempDir()
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	now := time.Date(2026, 8, 11, 4, 58, 0, 0, time.UTC)
	commands.now = func() time.Time { return now }
	const alias = "smime-decryption-disabled@example.test"
	_, mailIdentity, _, err := commands.IdentityAdd(context.Background(), home, "", account.MailIdentityAddInput{Address: alias, DisplayName: "Disabled decrypt", Kind: account.MailIdentityOther})
	if err != nil {
		t.Fatal(err)
	}
	cert, key := smimeDecryptionRSACertificate(t, alias, now)
	raw := smimeDecryptionEncryptedMessage(t, "outside@example.test", alias, "smime-decryption-disabled@example.test", "[ICLOUD-CLI-FIXTURE] S/MIME decryption disabled identity", []byte("Content-Type: text/plain\r\n\r\ndisabled identity secret"), cert)
	messageID := seedSMIMEDecryptionEncryptedMessage(t, commands, home, raw, now)
	_, certificate, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", alias, cert.Raw, false)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if _, _, _, err := commands.MailSMIMEIdentityConfigure(context.Background(), home, "", alias, certificate.ID, account.CredentialSetup{Scheme: account.CredentialKeyring, Secret: privatePEM}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := commands.IdentitySetEnabled(context.Background(), home, "", mailIdentity.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := commands.MailMessageDecrypt(context.Background(), home, "", messageID); err == nil || !strings.Contains(err.Error(), "no configured S/MIME identity matches") {
		t.Fatalf("disabled identity decrypt error=%v", err)
	}
	if _, _, _, err := commands.IdentitySetEnabled(context.Background(), home, "", mailIdentity.ID, true); err != nil {
		t.Fatal(err)
	}
	_, message, _, _, err := commands.MailMessageDecrypt(context.Background(), home, "", messageID)
	if err != nil || !strings.Contains(message.Snippet, "disabled identity secret") {
		t.Fatalf("reenabled identity decrypt message=%+v err=%v", message, err)
	}
}

func TestMultipleConfiguredRecipientsFailAsAmbiguous(t *testing.T) {
	home := t.TempDir()
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	now := time.Date(2026, 8, 11, 5, 0, 0, 0, time.UTC)
	commands.now = func() time.Time { return now }
	primaryCert, primaryKey := smimeDecryptionRSACertificate(t, "owner@icloud.com", now)
	aliasCert, aliasKey := smimeDecryptionRSACertificate(t, "decrypt-alias@example.test", now.Add(time.Second))
	if _, _, _, err := commands.IdentityAdd(context.Background(), home, "", account.MailIdentityAddInput{Address: "decrypt-alias@example.test", DisplayName: "Decrypt Alias", Kind: account.MailIdentityOther}); err != nil {
		t.Fatal(err)
	}
	primaryStoredAccount, primaryStored, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "owner@icloud.com", primaryCert.Raw, false)
	if err != nil || primaryStoredAccount.ID == "" {
		t.Fatal(err)
	}
	_, aliasStored, _, err := commands.MailSMIMECertificateImport(context.Background(), home, "", "decrypt-alias@example.test", aliasCert.Raw, false)
	if err != nil {
		t.Fatal(err)
	}
	primaryPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(primaryKey)})
	aliasPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(aliasKey)})
	if _, _, _, err := commands.MailSMIMEIdentityConfigure(context.Background(), home, "", "owner@icloud.com", primaryStored.ID, account.CredentialSetup{Scheme: account.CredentialKeyring, Secret: primaryPEM}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := commands.MailSMIMEIdentityConfigure(context.Background(), home, "", "decrypt-alias@example.test", aliasStored.ID, account.CredentialSetup{Scheme: account.CredentialKeyring, Secret: aliasPEM}); err != nil {
		t.Fatal(err)
	}
	raw := smimeDecryptionEncryptedMessage(t, "outside@example.test", "owner@icloud.com", "smime-decryption-ambiguous@example.test", "[ICLOUD-CLI-FIXTURE] S/MIME decryption ambiguous", []byte("Content-Type: text/plain\r\n\r\nambiguous secret"), primaryCert, aliasCert)
	messageID := seedSMIMEDecryptionEncryptedMessage(t, commands, home, raw, now)
	if _, _, _, _, err := commands.MailMessageDecrypt(context.Background(), home, "", messageID); err == nil || !strings.Contains(err.Error(), "multiple configured S/MIME identities") {
		t.Fatalf("ambiguous recipient decrypt error=%v", err)
	}
}

func TestDecryptedAttachmentSinkAvoidsRetainingOrdinaryPlaintext(t *testing.T) {
	ctx := context.Background()
	sink := memorySMIMEAttachmentSink(128)
	ordinary := []byte("ordinary decrypted attachment")
	blobValue, err := sink(ctx, maildomain.MIMEPart{Attachment: true}, bytes.NewReader(ordinary))
	if err != nil || blobValue.ByteSize != int64(len(ordinary)) || blobValue.Open != nil || !strings.HasPrefix(blobValue.StorageKey, "memory:") {
		t.Fatalf("ordinary decrypted attachment blob=%+v err=%v", blobValue, err)
	}
	embedded := []byte("From: nested@example.test\r\n\r\nnested")
	blobValue, err = sink(ctx, maildomain.MIMEPart{Attachment: true, EmbeddedMessage: true}, bytes.NewReader(embedded))
	if err != nil || blobValue.Open == nil || blobValue.ByteSize != int64(len(embedded)) {
		t.Fatalf("embedded decrypted attachment blob=%+v err=%v", blobValue, err)
	}
	reader, err := blobValue.Open()
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil || !bytes.Equal(reopened, embedded) {
		t.Fatalf("embedded decrypted attachment reopen=%q err=%v close=%v", reopened, err, closeErr)
	}
	if _, err := sink(ctx, maildomain.MIMEPart{Attachment: true}, bytes.NewReader(bytes.Repeat([]byte("x"), 129))); !errors.Is(err, maildomain.ErrContentLimit) {
		t.Fatalf("ordinary decrypted attachment limit error=%v", err)
	}
}

func smimeDecryptionRSACertificate(t *testing.T, email string, now time.Time) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: "SMIMEDecryption Recipient"}, EmailAddresses: []string{email},
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

func smimeDecryptionEncryptedMessage(t *testing.T, from, to, messageID, subject string, plaintext []byte, certs ...*x509.Certificate) string {
	t.Helper()
	previous := pkcs7.ContentEncryptionAlgorithm
	pkcs7.ContentEncryptionAlgorithm = pkcs7.EncryptionAlgorithmAES256CBC
	der, err := pkcs7.Encrypt(plaintext, certs)
	pkcs7.ContentEncryptionAlgorithm = previous
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join([]string{
		"From: " + from,
		"To: " + to,
		"Date: Tue, 11 Aug 2026 04:30:00 +0000",
		"Message-ID: <" + messageID + ">",
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: application/pkcs7-mime; smime-type=enveloped-data; name=smime.p7m",
		"Content-Transfer-Encoding: base64",
		"Content-Disposition: attachment; filename=smime.p7m",
		"",
		base64.StdEncoding.EncodeToString(der),
		"",
	}, "\r\n")
}

func smimeDecryptionDetachedSignedEntity(t *testing.T, signed []byte, cert *x509.Certificate, key *rsa.PrivateKey) []byte {
	t.Helper()
	sd, err := pkcs7.NewSignedData(signed)
	if err != nil {
		t.Fatal(err)
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := sd.AddSigner(cert, key, pkcs7.SignerInfoConfig{}); err != nil {
		t.Fatal(err)
	}
	sd.Detach()
	signature, err := sd.Finish()
	if err != nil {
		t.Fatal(err)
	}
	boundary := "smimeid-nested-signed"
	var value bytes.Buffer
	value.WriteString("Content-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; micalg=sha-256; boundary=\"" + boundary + "\"\r\n\r\n")
	value.WriteString("--" + boundary + "\r\n")
	value.Write(signed)
	value.WriteString("\r\n--" + boundary + "\r\nContent-Type: application/pkcs7-signature; name=smime.p7s\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: attachment; filename=smime.p7s\r\n\r\n")
	value.WriteString(base64.StdEncoding.EncodeToString(signature))
	value.WriteString("\r\n--" + boundary + "--\r\n")
	return value.Bytes()
}

func seedSMIMEDecryptionEncryptedMessage(t *testing.T, commands accountCommands, home, raw string, now time.Time) string {
	t.Helper()
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := commands.blobStager(paths).Stage(context.Background(), strings.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if err := staged.Commit(); err != nil {
		t.Fatal(err)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	folders, err := state.Folders().Reconcile(context.Background(), accountValue.ID, []folder.RemoteObservation{{
		Name: "INBOX", WireName: "INBOX", Kind: folder.KindInbox, KindSource: "inbox", Selectable: true,
		Subscription: folder.SubscriptionSubscribed, UIDValidity: 3300, StatusKnown: true,
	}}, now, func() (string, error) { return "fld_smime_decryption_inbox", nil })
	if err != nil || len(folders) != 1 {
		t.Fatalf("folders=%+v err=%v", folders, err)
	}
	policy, err := commands.inboundSMIMEPolicy(context.Background(), state, accountValue.ID)
	if err != nil {
		t.Fatal(err)
	}
	attachmentSet := newStagedAttachmentSet(commands.attachmentBlobStore(paths))
	defer attachmentSet.Abort()
	normalized, err := maildomain.ParseNormalizedWithAttachmentsAndSMIMEContext(context.Background(), strings.NewReader(raw), attachmentSet.Sink, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := attachmentSet.Commit(); err != nil {
		t.Fatal(err)
	}
	messageID := "msg_0198d704-6c00-7000-8000-000000033100"
	locationID := "loc_0198d704-6c00-7000-8000-000000033100"
	if err := assignContentIDs(commands, messageID, &normalized); err != nil {
		t.Fatal(err)
	}
	record := maildomain.BuildIngestRecord(messageID, locationID, accountValue.ID, folders[0].ID, 3300, 1, nil, now, int64(len(raw)), staged.ContentHash, staged.StorageKey, staged.ByteSize, normalized.Preview)
	record.Content = &normalized
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_smime_decryption", AccountID: accountValue.ID, FolderID: folders[0].ID, UIDValidity: 3300,
		Bootstrap: true, LastCommittedUID: 1, LastScannedUID: 1, AttemptedAt: now, Records: []maildomain.IngestRecord{record},
	}); err != nil {
		t.Fatal(err)
	}
	return messageID
}

func x509WritePrivateKeyFile(path string, key *rsa.PrivateKey, mode uint32) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), os.FileMode(mode))
}
