package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhase32PublicSMIMECertificateLifecycleAndSettingsTruth(t *testing.T) {
	commands, _ := newTestAccountCommands()
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE32_PASSWORD", "synthetic-phase32-secret")
	commands.folders = oneInboxFolderClient()
	addAndDiscoverInbox(t, commands, home, "ICLOUD_PHASE32_PASSWORD")

	fixture, err := os.ReadFile("../mail/testdata/phase32_openssl_root.pem")
	if err != nil {
		t.Fatal(err)
	}
	certificatePath := filepath.Join(t.TempDir(), "phase32-root.pem")
	if err := os.WriteFile(certificatePath, fixture, 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home,
		"mail", "smime", "certificates", "import", certificatePath,
		"--email", "openssl-signer@example.test", "--trust", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"trusted":true`) || strings.Contains(strings.ToLower(stdout), `"der"`) || strings.Contains(stdout, "PRIVATE KEY") {
		t.Fatalf("import code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var imported struct {
		Data struct {
			Certificate struct {
				ID string `json:"id"`
			} `json:"certificate"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &imported); err != nil || !strings.HasPrefix(imported.Data.Certificate.ID, "smc_") {
		t.Fatalf("import envelope=%+v err=%v", imported, err)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home,
		"mail", "smime", "certificates", "list", "--email", "openssl-signer@example.test", "--limit", "1", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, imported.Data.Certificate.ID) || !strings.Contains(stdout, `"count":1`) || strings.Contains(strings.ToLower(stdout), `"der"`) {
		t.Fatalf("list code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home,
		"mail", "message", "import", "../mail/testdata/phase32_openssl_signed.eml", "--to", "inbox", "--dry-run", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"attachment_count":0`) || !strings.Contains(stdout, `"rfc_message_id":"phase32-openssl-20260811@fixture.invalid"`) {
		t.Fatalf("S/MIME import dry-run code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home,
		"mail", "settings", "show")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "smime_inspection=supported") || !strings.Contains(stdout, "smime=unsupported") {
		t.Fatalf("settings code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home,
		"mail", "smime", "certificates", "remove", imported.Data.Certificate.ID, "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"removed":true`) {
		t.Fatalf("remove code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
