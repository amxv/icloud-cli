package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestPhase33SMIMEIdentityLifecycleIsBoundedScopedAndProtectsDependencies(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)

	aliases := []struct {
		id    string
		email string
		cert  string
		smi   string
	}{
		{"mid_0198d704-6c00-7000-8000-000000033001", "decrypt-a@example.test", "smc_0198d704-6c00-7000-8000-000000033001", "smi_0198d704-6c00-7000-8000-000000033001"},
		{"mid_0198d704-6c00-7000-8000-000000033002", "decrypt-b@example.test", "smc_0198d704-6c00-7000-8000-000000033002", "smi_0198d704-6c00-7000-8000-000000033002"},
	}
	for index, fixture := range aliases {
		if _, err := state.Identities().Create(ctx, account.MailIdentityCreateRecord{
			IdentityID: fixture.id, AccountID: accountID, Address: fixture.email, DisplayName: "Decrypt",
			Kind: account.MailIdentityOther, Provenance: account.MailIdentityUserConfigured, Verification: account.MailIdentityVerified,
			Enabled: true, Now: now.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
		cert := phase32StoreCertificate(t, fixture.email, now)
		if _, err := state.SMIMECertificates().Import(ctx, accountID, fixture.cert, fixture.email, cert, false, now); err != nil {
			t.Fatal(err)
		}
		value, err := state.SMIMEIdentities().Configure(ctx, accountID, fixture.smi, fixture.id, fixture.cert, account.CredentialEnv, "env:PHASE33_PRIVATE_KEY", now)
		if err != nil || value.ID != fixture.smi || value.MailIdentityID != fixture.id || value.Email != fixture.email || value.PrivateKeyScheme != account.CredentialEnv || !value.PrivateKeyConfigured || value.PrivateKeyReference != "env:PHASE33_PRIVATE_KEY" {
			t.Fatalf("configure=%+v err=%v", value, err)
		}
	}

	replacementAddress := "changed-decrypt-a@example.test"
	if _, err := state.Identities().Update(ctx, accountID, aliases[0].id, account.MailIdentityUpdateInput{Address: &replacementAddress}, now.Add(10*time.Second)); !errors.Is(err, maildomain.ErrSMIMEIdentityConflict) {
		t.Fatalf("bound mail identity address update error=%v", err)
	}
	unchanged, err := state.Identities().Resolve(ctx, accountID, aliases[0].id)
	if err != nil || unchanged.Address != aliases[0].email {
		t.Fatalf("bound mail identity changed despite conflict: identity=%+v err=%v", unchanged, err)
	}
	if _, err := state.Identities().SetEnabled(ctx, accountID, aliases[0].id, false, now.Add(11*time.Second)); err != nil {
		t.Fatal(err)
	}
	materials, err := state.SMIMEIdentities().Materials(ctx, accountID)
	if err != nil || len(materials) != 1 || materials[0].Identity.MailIdentityID != aliases[1].id {
		t.Fatalf("enabled S/MIME materials=%+v err=%v", materials, err)
	}
	if _, err := state.Identities().SetEnabled(ctx, accountID, aliases[0].id, true, now.Add(12*time.Second)); err != nil {
		t.Fatal(err)
	}

	page, err := state.SMIMEIdentities().ListPage(ctx, accountID, "", 1)
	if err != nil || len(page.Identities) != 1 || !page.More || !strings.HasPrefix(page.NextCursor, "smicur_") {
		t.Fatalf("first page=%+v err=%v", page, err)
	}
	page2, err := state.SMIMEIdentities().ListPage(ctx, accountID, page.NextCursor, 1)
	if err != nil || len(page2.Identities) != 1 || page2.More || page2.Identities[0].ID == page.Identities[0].ID {
		t.Fatalf("second page=%+v err=%v", page2, err)
	}
	resolved, err := state.SMIMEIdentities().Resolve(ctx, accountID, aliases[0].email)
	if err != nil || resolved.ID != aliases[0].smi {
		t.Fatalf("resolve=%+v err=%v", resolved, err)
	}
	if _, err := state.Identities().Remove(ctx, accountID, aliases[0].id, now.Add(time.Minute)); !errors.Is(err, maildomain.ErrSMIMEIdentityConflict) {
		t.Fatalf("mail identity removal while S/MIME bound error=%v", err)
	}
	if _, err := state.SMIMECertificates().Remove(ctx, accountID, aliases[0].cert, now.Add(time.Minute)); !errors.Is(err, maildomain.ErrSMIMEIdentityConflict) {
		t.Fatalf("certificate removal while S/MIME bound error=%v", err)
	}
	removed, err := state.SMIMEIdentities().Remove(ctx, accountID, aliases[0].smi)
	if err != nil || removed.ID != aliases[0].smi {
		t.Fatalf("remove S/MIME identity=%+v err=%v", removed, err)
	}
	if _, err := state.SMIMECertificates().Remove(ctx, accountID, aliases[0].cert, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("remove certificate after unbind: %v", err)
	}
	if _, err := state.Identities().Remove(ctx, accountID, aliases[0].id, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("remove mail identity after unbind: %v", err)
	}
}
