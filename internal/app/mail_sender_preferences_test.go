package app

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
)

func TestSenderPreferencesStayLocalWithoutProviderClients(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	seedAccount(t, home)
	commands, _ := newTestAccountCommands()
	// Intentionally leave every IMAP/SMTP/provider client nil. The sender
	// preference path must succeed without credential resolution or network use.

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "senders", "vip", "Alerts@Example.TEST", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("vip code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var setEnvelope struct {
		OK   bool `json:"ok"`
		Data struct {
			AccountID  string                      `json:"account_id"`
			Preference maildomain.SenderPreference `json:"preference"`
			LocalOnly  bool                        `json:"local_only"`
			Schema     int64                       `json:"schema_version"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &setEnvelope); err != nil || !setEnvelope.OK || !setEnvelope.Data.LocalOnly ||
		setEnvelope.Data.Preference.Email != "alerts@example.test" || !setEnvelope.Data.Preference.VIP || setEnvelope.Data.Schema != store.SchemaVersion {
		t.Fatalf("set envelope=%+v err=%v raw=%q", setEnvelope, err, stdout)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "senders", "block", "alerts@example.test")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "scope=local") || !strings.Contains(stdout, "vip=true") || !strings.Contains(stdout, "blocked=true") {
		t.Fatalf("block code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "senders", "list", "--limit", "1", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"local_only":true`) || !strings.Contains(stdout, `"email":"alerts@example.test"`) {
		t.Fatalf("list code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
