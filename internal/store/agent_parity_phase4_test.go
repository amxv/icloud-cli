package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
)

func createPhase4Account(t *testing.T, state *Store, ordinal int, email string) account.Account {
	t.Helper()
	now := time.Date(2026, 8, 10, 0, ordinal, 0, 0, time.UTC)
	value, err := state.Accounts().Create(context.Background(), account.CreateRecord{
		AccountID:        fmt.Sprintf("acct_0198e8c0-0000-7000-8000-%012d", ordinal),
		CredentialID:     fmt.Sprintf("cred_0198e8c0-0000-7000-8000-%012d", ordinal),
		Name:             fmt.Sprintf("Identity Account %d", ordinal),
		Email:            email,
		IMAPUsername:     email,
		MakeDefault:      ordinal == 1,
		CredentialScheme: account.CredentialEnv,
		CredentialRef:    fmt.Sprintf("env:IDENTITY_PASSWORD_%d", ordinal),
		Now:              now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAgentParityPhase4AccountCreationSeedsProtectedPrimaryIdentity(t *testing.T) {
	state, err := Open(context.Background(), testPaths(t, filepath.Join(t.TempDir(), "state")))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	value := createPhase4Account(t, state, 1, "owner@example.test")
	identity, err := state.Identities().Default(context.Background(), value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ID != primaryMailIdentityID(value.ID) || identity.Address != value.Email || identity.DisplayName != value.Name ||
		identity.Kind != account.MailIdentityPrimary || identity.Provenance != account.MailIdentityAccountPrimary ||
		identity.Verification != account.MailIdentityUnverified || !identity.Enabled || !identity.Default {
		t.Fatalf("primary identity=%+v account=%+v", identity, value)
	}
	if _, err := state.Identities().SetEnabled(context.Background(), value.ID, identity.ID, false, time.Now()); !errors.Is(err, account.ErrIdentityProtected) {
		t.Fatalf("disable primary error=%v", err)
	}
	if _, err := state.Identities().Remove(context.Background(), value.ID, identity.ID, time.Now()); !errors.Is(err, account.ErrIdentityProtected) {
		t.Fatalf("remove primary error=%v", err)
	}
}

func TestAgentParityPhase4IdentityLifecycleDefaultReassignmentAndAccountIsolation(t *testing.T) {
	state, err := Open(context.Background(), testPaths(t, filepath.Join(t.TempDir(), "state")))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	first := createPhase4Account(t, state, 1, "owner-one@example.test")
	second := createPhase4Account(t, state, 2, "owner-two@example.test")
	now := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	alias, err := state.Identities().Create(context.Background(), account.MailIdentityCreateRecord{
		IdentityID: "mid_0198e8c0-1000-7000-8000-000000000001", AccountID: first.ID,
		Address: "alias@example.test", DisplayName: "Alias One", Kind: account.MailIdentityICloudAlias,
		Provenance: account.MailIdentityUserConfigured, Verification: account.MailIdentityUnverified,
		Enabled: true, MakeDefault: true, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !alias.Default || !alias.Enabled {
		t.Fatalf("added alias=%+v", alias)
	}
	if _, err := state.Identities().Create(context.Background(), account.MailIdentityCreateRecord{
		IdentityID: "mid_0198e8c0-1000-7000-8000-000000000002", AccountID: first.ID,
		Address: "ALIAS@example.test", DisplayName: "Duplicate", Kind: account.MailIdentityOther,
		Provenance: account.MailIdentityUserConfigured, Verification: account.MailIdentityUnverified,
		Enabled: true, Now: now,
	}); !errors.Is(err, account.ErrIdentityConflict) {
		t.Fatalf("case-insensitive duplicate error=%v", err)
	}
	if _, err := state.Identities().Create(context.Background(), account.MailIdentityCreateRecord{
		IdentityID: "mid_0198e8c0-1000-7000-8000-000000000003", AccountID: second.ID,
		Address: "alias@example.test", DisplayName: "Same Address Other Account", Kind: account.MailIdentityOther,
		Provenance: account.MailIdentityUserConfigured, Verification: account.MailIdentityUnverified,
		Enabled: true, Now: now,
	}); err != nil {
		t.Fatalf("same address in another account should be isolated: %v", err)
	}
	if _, err := state.Identities().Resolve(context.Background(), second.ID, alias.ID); !errors.Is(err, account.ErrIdentityNotFound) {
		t.Fatalf("cross-account stable ID resolved: %v", err)
	}
	disabled, err := state.Identities().SetEnabled(context.Background(), first.ID, alias.ID, false, now.Add(time.Minute))
	if err != nil || disabled.Enabled || disabled.Default {
		t.Fatalf("disabled alias=%+v err=%v", disabled, err)
	}
	primary, err := state.Identities().Default(context.Background(), first.ID)
	if err != nil || primary.Kind != account.MailIdentityPrimary || !primary.Enabled || !primary.Default {
		t.Fatalf("replacement default=%+v err=%v", primary, err)
	}
	if _, err := state.Identities().SetDefault(context.Background(), first.ID, alias.ID, now.Add(2*time.Minute)); !errors.Is(err, account.ErrIdentityDisabled) {
		t.Fatalf("disabled default selection error=%v", err)
	}
	if _, err := state.Identities().SetEnabled(context.Background(), first.ID, alias.ID, true, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Identities().SetDefault(context.Background(), first.ID, alias.ID, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	removed, err := state.Identities().Remove(context.Background(), first.ID, alias.ID, now.Add(5*time.Minute))
	if err != nil || removed.ID != alias.ID {
		t.Fatalf("removed=%+v err=%v", removed, err)
	}
	primary, err = state.Identities().Default(context.Background(), first.ID)
	if err != nil || primary.Kind != account.MailIdentityPrimary || !primary.Default {
		t.Fatalf("default after remove=%+v err=%v", primary, err)
	}
}

func TestAgentParityPhase4IdentityPaginationIsCompleteAndScopeBound(t *testing.T) {
	state, err := Open(context.Background(), testPaths(t, filepath.Join(t.TempDir(), "state")))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	first := createPhase4Account(t, state, 1, "owner-one@example.test")
	second := createPhase4Account(t, state, 2, "owner-two@example.test")
	now := time.Date(2026, 8, 10, 2, 0, 0, 0, time.UTC)
	for index := 0; index < 105; index++ {
		_, err := state.Identities().Create(context.Background(), account.MailIdentityCreateRecord{
			IdentityID: fmt.Sprintf("mid_0198e8c0-2000-7000-8000-%012d", index), AccountID: first.ID,
			Address: fmt.Sprintf("alias-%03d@example.test", index), DisplayName: fmt.Sprintf("Alias %03d", index),
			Kind: account.MailIdentityOther, Provenance: account.MailIdentityUserConfigured, Verification: account.MailIdentityUnverified,
			Enabled: true, Now: now.Add(time.Duration(index) * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		values, more, next, err := state.Identities().ListPage(context.Background(), first.ID, 17, cursor)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, value := range values {
			if seen[value.ID] {
				t.Fatalf("duplicate identity %s", value.ID)
			}
			seen[value.ID] = true
		}
		if !more {
			if next != "" {
				t.Fatalf("terminal cursor=%q", next)
			}
			break
		}
		if next == "" || next == cursor {
			t.Fatalf("pagination did not advance: cursor=%q next=%q", cursor, next)
		}
		if pages == 1 {
			if _, _, _, err := state.Identities().ListPage(context.Background(), second.ID, 17, next); !errors.Is(err, account.ErrIdentityInvalidCursor) {
				t.Fatalf("cross-account cursor error=%v", err)
			}
		}
		cursor = next
	}
	if len(seen) != 106 {
		t.Fatalf("identity count=%d want=106", len(seen))
	}
}

func TestAgentParityPhase4AccountRemovalCascadesIdentities(t *testing.T) {
	state, err := Open(context.Background(), testPaths(t, filepath.Join(t.TempDir(), "state")))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	value := createPhase4Account(t, state, 1, "owner@example.test")
	if _, err := state.Identities().Create(context.Background(), account.MailIdentityCreateRecord{
		IdentityID: "mid_0198e8c0-3000-7000-8000-000000000001", AccountID: value.ID,
		Address: "alias@example.test", DisplayName: "Alias", Kind: account.MailIdentityOther,
		Provenance: account.MailIdentityUserConfigured, Verification: account.MailIdentityUnverified, Enabled: true, Now: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.Accounts().Remove(context.Background(), value.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := state.db.QueryRowContext(context.Background(), `SELECT count(*) FROM mail_identities WHERE account_id = ?`, value.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("identity rows after account delete=%d", count)
	}
}
