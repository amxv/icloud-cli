package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/store"
)

func TestIdentityCLIIsLocalStrictAndAccountScoped(t *testing.T) {
	commands, _ := newTestAccountCommands()
	home := t.TempDir()
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home,
		"account", "add", "--name", "Primary Sender", "--email", "Owner@Example.Test", "--credential-env", "IDENTITY_PRIMARY_PASSWORD")
	if code != 0 || stderr != "" {
		t.Fatalf("add account code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "identities", "list", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("list code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var listed struct {
		Data struct {
			Identities []account.MailIdentity `json:"identities"`
			More       bool                   `json:"more"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil || len(listed.Data.Identities) != 1 || listed.Data.More {
		t.Fatalf("list=%+v err=%v raw=%q", listed, err, stdout)
	}
	primary := listed.Data.Identities[0]
	if primary.Kind != account.MailIdentityPrimary || primary.Address != "owner@example.test" || !primary.Enabled || !primary.Default || primary.Verification != account.MailIdentityUnverified {
		t.Fatalf("primary=%+v", primary)
	}

	input := `{"address":"Alias@Example.Test","display_name":"Alias Sender","kind":"icloud_alias","default":true}`
	code, stdout, stderr = runWithAccountCommands(t, commands, input, "--home", home, "mail", "identities", "add", "--input-json", "-", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("add identity code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var added struct {
		Data struct {
			Identity account.MailIdentity `json:"identity"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &added); err != nil {
		t.Fatal(err)
	}
	alias := added.Data.Identity
	if alias.Address != "alias@example.test" || alias.DisplayName != "Alias Sender" || alias.Kind != account.MailIdentityICloudAlias ||
		alias.Provenance != account.MailIdentityUserConfigured || alias.Verification != account.MailIdentityUnverified || !alias.Enabled || !alias.Default || !strings.HasPrefix(alias.ID, "mid_") {
		t.Fatalf("alias=%+v raw=%q", alias, stdout)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "identities", "show", "ALIAS@example.test", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, alias.ID) {
		t.Fatalf("show code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "identities", "update", alias.ID, "--display-name", "Updated Alias")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "display_name=\"Updated Alias\"") {
		t.Fatalf("update code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "identities", "disable", alias.ID, "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"enabled":false`) || !strings.Contains(stdout, `"default":false`) {
		t.Fatalf("disable code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "identities", "default", alias.ID)
	if code != 6 || stdout != "" || !strings.Contains(stderr, "enable the mail identity") {
		t.Fatalf("disabled default code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "identities", "enable", alias.ID)
	if code != 0 || stderr != "" {
		t.Fatalf("enable code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "identities", "default", alias.ID)
	if code != 0 || stderr != "" {
		t.Fatalf("default code=%d stderr=%q", code, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "identities", "remove", alias.ID)
	if code != 8 || stdout != "" || !strings.Contains(stderr, "requires --yes") {
		t.Fatalf("remove safety code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "identities", "remove", alias.ID, "--yes")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "identity-removed") {
		t.Fatalf("remove code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "identities", "disable", primary.ID)
	if code != 6 || stdout != "" || !strings.Contains(stderr, "primary mail identity") {
		t.Fatalf("primary disable code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, _, stderr = runWithAccountCommands(t, commands, "", "--home", home,
		"account", "add", "--name", "Other Account", "--email", "other@example.test", "--credential-env", "IDENTITY_OTHER_PASSWORD")
	if code != 0 || stderr != "" {
		t.Fatalf("add other account code=%d stderr=%q", code, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "--account", "Other Account", "mail", "identities", "show", "alias@example.test")
	if code != 7 || stdout != "" || !strings.Contains(stderr, "mail identity not found") {
		t.Fatalf("account isolation code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestIdentityConfigurationPreservesAccountCredentialAndStableIDs(t *testing.T) {
	commands, _ := newTestAccountCommands()
	home := t.TempDir()
	code, _, stderr := runWithAccountCommands(t, commands, "", "--home", home,
		"account", "add", "--name", "Stable", "--email", "stable@example.test", "--credential-env", "STABLE_IDENTITY_PASSWORD")
	if code != 0 || stderr != "" {
		t.Fatalf("account add code=%d stderr=%q", code, stderr)
	}
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	before, credentialBefore, err := state.Accounts().Credential(context.Background(), "stable@example.test")
	state.Close()
	if err != nil {
		t.Fatal(err)
	}
	code, _, stderr = runWithAccountCommands(t, commands, `{"address":"second@example.test","kind":"other"}`, "--home", home,
		"mail", "identities", "add", "--input-json", "-")
	if code != 0 || stderr != "" {
		t.Fatalf("identity add code=%d stderr=%q", code, stderr)
	}
	state, err = store.Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	after, credentialAfter, err := state.Accounts().Credential(context.Background(), "stable@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if before.ID != after.ID || credentialBefore.ID != credentialAfter.ID || credentialBefore.Reference != credentialAfter.Reference || credentialAfter.Scheme != account.CredentialEnv {
		t.Fatalf("account/credential changed before=%+v/%+v after=%+v/%+v", before, credentialBefore, after, credentialAfter)
	}
}
