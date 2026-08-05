package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/gofrs/flock"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), args, &stdout, &stderr)
	return exitCode, stdout.String(), stderr.String()
}

func TestRootHelpUsesICloudIdentityAndGroups(t *testing.T) {
	exitCode, stdout, stderr := run(t, "--help")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	for _, want := range []string{
		"icloud controls iCloud services from the command line.",
		"Core commands:",
		"Account commands:",
		"Documentation commands:",
		"account",
		"capabilities",
		"completion",
		"man",
		"version",
		"--no-input",
		"--timeout",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("help missing %q:\n%s", want, stdout)
		}
	}
	for _, unwanted := range []string{"icloud-cli", "hello", "template"} {
		if strings.Contains(stdout, unwanted) {
			t.Fatalf("help unexpectedly contains %q:\n%s", unwanted, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("help wrote stderr: %q", stderr)
	}
}

func TestVersionCompactDefault(t *testing.T) {
	exitCode, stdout, stderr := run(t, "version")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	if stdout != "version dev\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestVersionJSONEnvelope(t *testing.T) {
	exitCode, stdout, stderr := run(t, "--request-id", "req_test", "version", "--json")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Command string `json:"command"`
			Version string `json:"version"`
		} `json:"data"`
		Meta struct {
			RequestID string `json:"request_id"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if !envelope.OK || envelope.Data.Command != "icloud" || envelope.Data.Version != "dev" {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	if envelope.Meta.RequestID != "req_test" {
		t.Fatalf("request id = %q", envelope.Meta.RequestID)
	}
}

func TestCapabilitiesCompactGoldenBudget(t *testing.T) {
	exitCode, stdout, stderr := run(t, "capabilities")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	const want = "capabilities command=icloud version=dev schema=1 default_output=compact json=true completion=bash,zsh,fish,powershell man=true\n"
	if stdout != want {
		t.Fatalf("stdout mismatch\nwant: %q\n got: %q", want, stdout)
	}
	if lines := strings.Count(stdout, "\n"); lines > 1 {
		t.Fatalf("line budget exceeded: %d", lines)
	}
	if len(stdout) > 180 {
		t.Fatalf("byte budget exceeded: %d", len(stdout))
	}
	for _, unwanted := range []string{"{", "}", "\x1b[", "│", "Command:", "Version:"} {
		if strings.Contains(stdout, unwanted) {
			t.Fatalf("compact output contains %q: %q", unwanted, stdout)
		}
	}
}

func TestCapabilitiesJSONEnvelope(t *testing.T) {
	exitCode, stdout, stderr := run(t, "--request-id=req_caps", "capabilities", "--json")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if envelope["ok"] != true {
		t.Fatalf("ok = %#v", envelope["ok"])
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok || data["data_schema_version"] != float64(1) {
		t.Fatalf("data = %#v", envelope["data"])
	}
	if strings.Contains(stdout, "\x1b[") {
		t.Fatalf("JSON contains ANSI: %q", stdout)
	}
}

func TestUnknownCommandIsCompactUsageErrorWithSuggestion(t *testing.T) {
	exitCode, stdout, stderr := run(t, "versoin")
	if exitCode != 2 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	for _, want := range []string{"error", "code=usage", "versoin", "version"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q: %q", want, stderr)
		}
	}
	if strings.Count(stderr, "\n") != 1 {
		t.Fatalf("error line budget exceeded: %q", stderr)
	}
}

func TestUnknownCommandJSONErrorEnvelope(t *testing.T) {
	exitCode, stdout, stderr := run(t, "--request-id", "req_error", "versoin", "--json")
	if exitCode != 2 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
		Meta struct {
			RequestID string `json:"request_id"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(stderr), &envelope); err != nil {
		t.Fatalf("invalid JSON error: %v\n%s", err, stderr)
	}
	if envelope.OK || envelope.Error.Code != "usage" || envelope.Error.Retryable {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	if !strings.Contains(stderr, "did you mean") || !strings.Contains(stderr, "version") {
		t.Fatalf("JSON error lost the command suggestion: %q", stderr)
	}
	if envelope.Meta.RequestID != "req_error" {
		t.Fatalf("request id = %q", envelope.Meta.RequestID)
	}
}

func TestHelpWritesStdoutAndFlagErrorsWriteStderr(t *testing.T) {
	exitCode, stdout, stderr := run(t, "version", "--help")
	if exitCode != 0 || !strings.Contains(stdout, "Usage:") || stderr != "" {
		t.Fatalf("help result code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = run(t, "version", "--unsupported")
	if exitCode != 2 || stdout != "" || !strings.Contains(stderr, "code=usage") {
		t.Fatalf("flag result code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestNoColorDoesNotChangeCompactOutput(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	exitCode, stdout, stderr := run(t, "capabilities")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", exitCode, stderr)
	}
	if strings.Contains(stdout, "\x1b[") {
		t.Fatalf("output contains ANSI: %q", stdout)
	}
}

func TestAccountListCreatesAndReopensPrivateZeroState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	for runIndex := 0; runIndex < 2; runIndex++ {
		exitCode, stdout, stderr := run(t, "--home", home, "account", "list")
		if exitCode != 0 || stdout != "accounts count=0\n" || stderr != "" {
			t.Fatalf("run=%d code=%d stdout=%q stderr=%q", runIndex, exitCode, stdout, stderr)
		}
	}

	for _, path := range []string{
		home,
		filepath.Join(home, "blobs"),
		filepath.Join(home, "locks"),
		filepath.Join(home, "mail.db"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("state path %s: %v", path, err)
		}
	}

	exitCode, stdout, stderr := run(t,
		"--home", home,
		"--request-id", "req_accounts",
		"account", "list", "--json",
	)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", exitCode, stderr)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Accounts      []any `json:"accounts"`
			Count         int   `json:"count"`
			SchemaVersion int64 `json:"schema_version"`
		} `json:"data"`
		Meta struct {
			RequestID string `json:"request_id"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if !envelope.OK || envelope.Data.Count != 0 || len(envelope.Data.Accounts) != 0 || envelope.Data.SchemaVersion != 1 {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	if envelope.Meta.RequestID != "req_accounts" {
		t.Fatalf("request id = %q", envelope.Meta.RequestID)
	}
}

func TestAccountListUsesICloudHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "environment-state")
	t.Setenv("ICLOUD_HOME", home)
	exitCode, stdout, stderr := run(t, "account", "list")
	if exitCode != 0 || stdout != "accounts count=0\n" || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(home, "mail.db")); err != nil {
		t.Fatal(err)
	}
}

func TestAccountHomesAreIsolated(t *testing.T) {
	populatedHome := filepath.Join(t.TempDir(), "populated")
	emptyHome := filepath.Join(t.TempDir(), "empty")
	seedAccount(t, populatedHome)

	exitCode, populatedOutput, populatedError := run(t, "--home", populatedHome, "account", "list")
	if exitCode != 0 || populatedError != "" || !strings.Contains(populatedOutput, "accounts count=1") {
		t.Fatalf("populated code=%d stdout=%q stderr=%q", exitCode, populatedOutput, populatedError)
	}
	exitCode, emptyOutput, emptyError := run(t, "--home", emptyHome, "account", "list")
	if exitCode != 0 || emptyOutput != "accounts count=0\n" || emptyError != "" {
		t.Fatalf("empty code=%d stdout=%q stderr=%q", exitCode, emptyOutput, emptyError)
	}
	if strings.Contains(emptyOutput, "owner@example.com") {
		t.Fatalf("isolated home leaked another account: %q", emptyOutput)
	}
}

func TestAccountShowMissingUsesNotFoundExitAndJSON(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	exitCode, stdout, stderr := run(t, "--home", home, "account", "show", "missing")
	if exitCode != 7 || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if stderr != "error code=not_found message=\"account not found\"\n" {
		t.Fatalf("stderr = %q", stderr)
	}

	exitCode, stdout, stderr = run(t,
		"--home", home,
		"--request-id", "req_missing",
		"account", "show", "missing", "--json",
	)
	if exitCode != 7 || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Meta struct {
			RequestID string `json:"request_id"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(stderr), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stderr)
	}
	if envelope.OK || envelope.Error.Code != "not_found" || envelope.Meta.RequestID != "req_missing" {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
}

func TestAccountListAndShowReadStoredAccountCompactly(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	seedAccount(t, home)

	exitCode, stdout, stderr := run(t, "--home", home, "account", "list")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", exitCode, stderr)
	}
	const wantList = "account acct_0198d704-6c00-7000-8000-000000000001 name=Personal email=owner@example.com imap_username=owner status=pending_validation default=true credential=env\naccounts count=1\n"
	if stdout != wantList {
		t.Fatalf("stdout mismatch\nwant: %q\n got: %q", wantList, stdout)
	}
	if strings.Count(stdout, "\n") != 2 || len(stdout) > 220 {
		t.Fatalf("compact budget exceeded: lines=%d bytes=%d", strings.Count(stdout, "\n"), len(stdout))
	}
	for _, private := range []string{"ICLOUD_TEST_APP_PASSWORD", "credential_ref", "reference="} {
		if strings.Contains(stdout, private) {
			t.Fatalf("compact output contains private storage detail %q: %q", private, stdout)
		}
	}

	exitCode, stdout, stderr = run(t, "--home", home, "account", "show", "OWNER@example.com")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", exitCode, stderr)
	}
	const wantShow = "account acct_0198d704-6c00-7000-8000-000000000001 name=Personal email=owner@example.com imap_username=owner status=pending_validation default=true credential=env\n"
	if stdout != wantShow {
		t.Fatalf("stdout mismatch\nwant: %q\n got: %q", wantShow, stdout)
	}

	exitCode, stdout, stderr = run(t,
		"--home", home,
		"--request-id", "req_account",
		"account", "show", "Personal", "--json",
	)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", exitCode, stderr)
	}
	if strings.Contains(stdout, "ICLOUD_TEST_APP_PASSWORD") || strings.Contains(stdout, `"reference"`) {
		t.Fatalf("JSON exposed credential reference: %s", stdout)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Account struct {
				ID               string `json:"id"`
				CredentialScheme string `json:"credential_scheme"`
			} `json:"account"`
			SchemaVersion int64 `json:"schema_version"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if !envelope.OK || envelope.Data.Account.ID != "acct_0198d704-6c00-7000-8000-000000000001" ||
		envelope.Data.Account.CredentialScheme != "env" || envelope.Data.SchemaVersion != 1 {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
}

func TestAccountListIsBoundedAndReportsMore(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	seedAccount(t, home)
	seedSecondAccount(t, home)

	exitCode, stdout, stderr := run(t, "--home", home, "account", "list", "--limit", "1")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", exitCode, stderr)
	}
	const want = "account acct_0198d704-6c00-7000-8000-000000000001 name=Personal email=owner@example.com imap_username=owner status=pending_validation default=true credential=env\naccounts count=1 more=true\n"
	if stdout != want {
		t.Fatalf("stdout mismatch\nwant: %q\n got: %q", want, stdout)
	}

	exitCode, stdout, stderr = run(t,
		"--home", home,
		"--request-id", "req_bounded",
		"account", "list", "--limit", "1", "--json",
	)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", exitCode, stderr)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Accounts []any `json:"accounts"`
			Count    int   `json:"count"`
			More     bool  `json:"more"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if !envelope.OK || envelope.Data.Count != 1 || len(envelope.Data.Accounts) != 1 || !envelope.Data.More {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}

	unusedHome := filepath.Join(t.TempDir(), "unused")
	for _, limit := range []string{"0", "101"} {
		exitCode, stdout, stderr = run(t, "--home", unusedHome, "account", "list", "--limit", limit)
		if exitCode != 2 || stdout != "" || !strings.Contains(stderr, "--limit must be between 1 and 100") {
			t.Fatalf("limit=%s code=%d stdout=%q stderr=%q", limit, exitCode, stdout, stderr)
		}
	}
	if _, err := os.Stat(unusedHome); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid limit created state: %v", err)
	}
}

func TestAccountListReturnsConflictWhileMigrationLockIsHeld(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.EnsureAppPaths(paths); err != nil {
		t.Fatal(err)
	}
	lock := flock.New(paths.MigrationLock, flock.SetPermissions(0o600))
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()

	exitCode, stdout, stderr := run(t, "--timeout", "50ms", "--home", home, "account", "list")
	if exitCode != 6 || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	const want = "error code=conflict message=\"local state is busy while another icloud process migrates it\" retryable=true\n"
	if stderr != want {
		t.Fatalf("stderr mismatch\nwant: %q\n got: %q", want, stderr)
	}
}

func TestNonStateCommandsDoNotCreateHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "unused")
	exitCode, stdout, stderr := run(t, "--home", home, "version")
	if exitCode != 0 || stdout != "version dev\n" || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("home was created or stat failed unexpectedly: %v", err)
	}
}

func seedAccount(t *testing.T, home string) {
	t.Helper()
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	now := time.Date(2026, 8, 6, 1, 30, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if err := state.WithTx(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(context.Background(), `
INSERT INTO accounts (
    account_id, friendly_name, email_address, imap_username, status,
    is_default, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			"acct_0198d704-6c00-7000-8000-000000000001",
			"Personal",
			"owner@example.com",
			"owner",
			account.StatusPendingValidation,
			1,
			now,
			now,
		); err != nil {
			return err
		}
		_, err := tx.ExecContext(context.Background(), `
INSERT INTO credential_refs (
    credential_ref_id, account_id, scheme, reference, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?)`,
			"cred_0198d704-6c00-7000-8000-000000000002",
			"acct_0198d704-6c00-7000-8000-000000000001",
			"env",
			"ICLOUD_TEST_APP_PASSWORD",
			now,
			now,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func seedSecondAccount(t *testing.T, home string) {
	t.Helper()
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	now := time.Date(2026, 8, 6, 1, 31, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if err := state.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `
INSERT INTO accounts (
    account_id, friendly_name, email_address, imap_username, status,
    is_default, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			"acct_0198d704-6c00-7000-8000-000000000003",
			"Work",
			"work@example.com",
			"work",
			account.StatusDisabled,
			0,
			now,
			now,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
