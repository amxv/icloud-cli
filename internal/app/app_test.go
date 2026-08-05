package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/gofrs/flock"
	keyring "github.com/zalando/go-keyring"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), args, &stdout, &stderr)
	return exitCode, stdout.String(), stderr.String()
}

type fakeKeyring struct {
	values      map[string]string
	setError    error
	getError    error
	deleteError error
}

type fakeIMAPValidator struct {
	results   map[string]transport.IMAPResult
	errors    map[string]error
	usernames []string
}

func (validator *fakeIMAPValidator) ValidateIMAP(_ context.Context, request transport.AuthRequest) (transport.IMAPResult, error) {
	validator.usernames = append(validator.usernames, request.Username)
	if err := validator.errors[request.Username]; err != nil {
		return transport.IMAPResult{}, err
	}
	return validator.results[request.Username], nil
}

type fakeSMTPValidator struct {
	result    transport.SMTPResult
	err       error
	usernames []string
}

func (validator *fakeSMTPValidator) ValidateSMTP(_ context.Context, request transport.AuthRequest) (transport.SMTPResult, error) {
	validator.usernames = append(validator.usernames, request.Username)
	return validator.result, validator.err
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{values: map[string]string{}}
}

func (keyringStore *fakeKeyring) Set(service, user, password string) error {
	if keyringStore.setError != nil {
		return keyringStore.setError
	}
	keyringStore.values[service+"\x00"+user] = password
	return nil
}

func (keyringStore *fakeKeyring) Get(service, user string) (string, error) {
	if keyringStore.getError != nil {
		return "", keyringStore.getError
	}
	value, ok := keyringStore.values[service+"\x00"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (keyringStore *fakeKeyring) Delete(service, user string) error {
	if keyringStore.deleteError != nil {
		return keyringStore.deleteError
	}
	key := service + "\x00" + user
	if _, ok := keyringStore.values[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(keyringStore.values, key)
	return nil
}

func newTestAccountCommands() (accountCommands, *fakeKeyring) {
	keyringStore := newFakeKeyring()
	counters := map[string]int{}
	commands := accountCommands{
		keyring: keyringStore,
		now: func() time.Time {
			return time.Date(2026, 8, 6, 2, 30, 0, 0, time.UTC)
		},
		newID: func(prefix string) (string, error) {
			counters[prefix]++
			return fmt.Sprintf("%s_0198d704-6c00-7000-8000-%012d", prefix, counters[prefix]), nil
		},
	}
	return commands, keyringStore
}

func runWithAccountCommands(t *testing.T, commands accountCommands, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithAccounts(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr, commands)
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
		"doctor",
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
	const want = "capabilities command=icloud version=dev schema=2 account_mode=validated_lifecycle credentials=keyring,env,file network_validation=true diagnostics=true default_output=compact json=true completion=bash,zsh,fish,powershell man=true\n"
	if stdout != want {
		t.Fatalf("stdout mismatch\nwant: %q\n got: %q", want, stdout)
	}
	if lines := strings.Count(stdout, "\n"); lines > 1 {
		t.Fatalf("line budget exceeded: %d", lines)
	}
	if len(stdout) > 280 {
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
	if !ok || data["data_schema_version"] != float64(2) || data["network_validation"] != true || data["diagnostics"] != true {
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
	if !envelope.OK || envelope.Data.Count != 0 || len(envelope.Data.Accounts) != 0 || envelope.Data.SchemaVersion != 2 {
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
		envelope.Data.Account.CredentialScheme != "env" || envelope.Data.SchemaVersion != 2 {
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

func TestAccountProfileLifecycleWithEnvAndFileReferences(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	commands, _ := newTestAccountCommands()

	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home,
		"account", "add",
		"--name", "Personal",
		"--email", "Owner@iCloud.com",
		"--credential-env", "ICLOUD_PERSONAL_PASSWORD",
	)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("add personal code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if stdout != "account-added acct_0198d704-6c00-7000-8000-000000000001 status=pending_validation default=true credential=env\n" {
		t.Fatalf("personal output = %q", stdout)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home,
		"account", "add",
		"--name", "Work",
		"--email", "work@icloud.com",
		"--credential-file", "./private/work-password",
	)
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "credential=file") || strings.Contains(stdout, "private/work-password") {
		t.Fatalf("add work code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	references := readCredentialReferences(t, home)
	if references[account.CredentialEnv] != "env:ICLOUD_PERSONAL_PASSWORD" ||
		!strings.HasPrefix(references[account.CredentialFile], "file:") ||
		!strings.HasSuffix(references[account.CredentialFile], filepath.Join("private", "work-password")) {
		t.Fatalf("canonical references = %#v", references)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "rename", "Work", "Business")
	if exitCode != 0 || stdout != "account-renamed acct_0198d704-6c00-7000-8000-000000000002\n" || stderr != "" {
		t.Fatalf("rename code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "default", "Business")
	if exitCode != 0 || stdout != "account-default acct_0198d704-6c00-7000-8000-000000000002\n" || stderr != "" {
		t.Fatalf("default code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "show")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "name=Business") || !strings.Contains(stdout, "default=true") {
		t.Fatalf("selected default code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "credential", "show", "Business", "--json")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("credential show code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	for _, private := range []string{"private/work-password", `"reference"`, "credential_ref"} {
		if strings.Contains(stdout, private) {
			t.Fatalf("credential metadata leaked %q: %s", private, stdout)
		}
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "remove", "Business", "--yes")
	if exitCode != 6 || stdout != "" || !strings.Contains(stderr, "set another default account") {
		t.Fatalf("default removal code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "default", "Personal"); exitCode != 0 || stderr != "" {
		t.Fatalf("restore default code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "remove", "Business", "--yes")
	if exitCode != 0 || stderr != "" || stdout != "account-removed acct_0198d704-6c00-7000-8000-000000000002\n" {
		t.Fatalf("remove code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "--account", "Personal", "account", "show")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "email=owner@icloud.com") {
		t.Fatalf("global selection code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	assertStateExcludes(t, home, "not-a-secret-value")
}

func TestKeyringCredentialAddReplaceAndRemoveNeverPersistsOrPrintsSecrets(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	commands, keyringStore := newTestAccountCommands()
	firstSecret := "first-private-app-password"
	secondSecret := "second-private-app-password"

	exitCode, stdout, stderr := runWithAccountCommands(t, commands, firstSecret+"\n",
		"--home", home,
		"account", "add",
		"--name", "Personal",
		"--email", "owner@icloud.com",
		"--password-stdin",
	)
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, firstSecret) || !strings.Contains(stdout, "credential=keyring") {
		t.Fatalf("add code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if len(keyringStore.values) != 1 {
		t.Fatalf("keyring entries = %#v", keyringStore.values)
	}
	if reference := readCredentialReferences(t, home)[account.CredentialKeyring]; !strings.HasPrefix(reference, "keyring:icloud-cli-") || !strings.HasSuffix(reference, ":acct_0198d704-6c00-7000-8000-000000000001") {
		t.Fatalf("keyring reference = %q", reference)
	}
	for _, value := range keyringStore.values {
		if value != firstSecret {
			t.Fatalf("keyring value = %q", value)
		}
	}
	assertStateExcludes(t, home, firstSecret)

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, secondSecret+"\n",
		"--home", home,
		"account", "credential", "replace", "Personal",
		"--password-stdin",
	)
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, secondSecret) || stdout != "credential-replaced acct_0198d704-6c00-7000-8000-000000000001 scheme=keyring\n" {
		t.Fatalf("replace code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	for _, value := range keyringStore.values {
		if value != secondSecret {
			t.Fatalf("rotated keyring value = %q", value)
		}
	}
	assertStateExcludes(t, home, firstSecret, secondSecret)

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home,
		"account", "credential", "show", "Personal", "--json",
	)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("show code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	for _, private := range []string{firstSecret, secondSecret, `"reference"`, "icloud-cli-"} {
		if strings.Contains(stdout, private) {
			t.Fatalf("credential JSON leaked %q: %s", private, stdout)
		}
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "remove", "Personal", "--yes")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "keyring_deleted=true") {
		t.Fatalf("remove code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if len(keyringStore.values) != 0 {
		t.Fatalf("keyring was not cleaned: %#v", keyringStore.values)
	}
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "list")
	if exitCode != 0 || stdout != "accounts count=0\n" || stderr != "" {
		t.Fatalf("empty list code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestDuplicateAccountCleansNewKeyringEntryAndRedactsBackendErrors(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	commands, keyringStore := newTestAccountCommands()
	firstSecret := "first-private-secret"
	duplicateSecret := "duplicate-private-secret"

	if exitCode, _, stderr := runWithAccountCommands(t, commands, firstSecret+"\n",
		"--home", home, "account", "add", "--name", "Personal", "--email", "owner@icloud.com", "--password-stdin",
	); exitCode != 0 || stderr != "" {
		t.Fatalf("first add code=%d stderr=%q", exitCode, stderr)
	}
	exitCode, stdout, stderr := runWithAccountCommands(t, commands, duplicateSecret+"\n",
		"--home", home, "account", "add", "--name", "Duplicate", "--email", "OWNER@ICLOUD.COM", "--password-stdin",
	)
	if exitCode != 6 || stdout != "" || !strings.Contains(stderr, "already exists") {
		t.Fatalf("duplicate code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if strings.Contains(stderr, duplicateSecret) || len(keyringStore.values) != 1 {
		t.Fatalf("duplicate leaked or orphaned keyring: stderr=%q values=%#v", stderr, keyringStore.values)
	}
	assertStateExcludes(t, home, firstSecret, duplicateSecret)

	failingCommands, failingKeyring := newTestAccountCommands()
	failingKeyring.setError = errors.New("backend exposed " + duplicateSecret)
	failureHome := filepath.Join(t.TempDir(), "failure")
	exitCode, stdout, stderr = runWithAccountCommands(t, failingCommands, duplicateSecret+"\n",
		"--home", failureHome, "account", "add", "--name", "Failure", "--email", "failure@icloud.com", "--password-stdin",
	)
	if exitCode != 3 || stdout != "" || strings.Contains(stderr, duplicateSecret) || !strings.Contains(stderr, "native keyring is unavailable") {
		t.Fatalf("keyring failure code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestCredentialDeleteClearsKeyringButKeepsAccountProfile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	commands, keyringStore := newTestAccountCommands()
	privateValue := "credential-delete-private-value"
	if exitCode, _, stderr := runWithAccountCommands(t, commands, privateValue+"\n",
		"--home", home, "account", "add", "--name", "Personal", "--email", "owner@icloud.com", "--password-stdin",
	); exitCode != 0 || stderr != "" {
		t.Fatalf("add code=%d stderr=%q", exitCode, stderr)
	}

	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "account", "credential", "delete", "Personal")
	if exitCode != 8 || stdout != "" || !strings.Contains(stderr, "requires --yes") || len(keyringStore.values) != 1 {
		t.Fatalf("safety code=%d stdout=%q stderr=%q values=%#v", exitCode, stdout, stderr, keyringStore.values)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "credential", "delete", "Personal", "--yes")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "credential-deleted") || len(keyringStore.values) != 0 {
		t.Fatalf("delete code=%d stdout=%q stderr=%q values=%#v", exitCode, stdout, stderr, keyringStore.values)
	}
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "show", "Personal")
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, "credential=") || !strings.Contains(stdout, "status=pending_validation") {
		t.Fatalf("profile after delete code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "credential", "show", "Personal")
	if exitCode != 7 || stdout != "" || !strings.Contains(stderr, "credential is not configured") {
		t.Fatalf("metadata after delete code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	assertStateExcludes(t, home, privateValue)
}

func TestKeyringDeleteFailureLeavesAccountAndRedactsBackendCause(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	commands, keyringStore := newTestAccountCommands()
	privateValue := "keyring-delete-private-value"
	if exitCode, _, stderr := runWithAccountCommands(t, commands, privateValue+"\n",
		"--home", home, "account", "add", "--name", "Personal", "--email", "owner@icloud.com", "--password-stdin",
	); exitCode != 0 || stderr != "" {
		t.Fatalf("add code=%d stderr=%q", exitCode, stderr)
	}
	keyringStore.deleteError = errors.New("backend delete exposed " + privateValue)
	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "account", "remove", "Personal", "--yes")
	if exitCode != 3 || stdout != "" || strings.Contains(stderr, privateValue) || !strings.Contains(stderr, "native keyring is unavailable") {
		t.Fatalf("remove failure code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	keyringStore.deleteError = nil
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "list")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "accounts count=1") || len(keyringStore.values) != 1 {
		t.Fatalf("account changed after failed delete code=%d stdout=%q stderr=%q values=%#v", exitCode, stdout, stderr, keyringStore.values)
	}
}

func TestCredentialReplaceIDFailureRollsBackTemporaryKeyringEntry(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	commands, keyringStore := newTestAccountCommands()
	if exitCode, _, stderr := runWithAccountCommands(t, commands, "",
		"--home", home,
		"account", "add",
		"--name", "Personal",
		"--email", "owner@icloud.com",
		"--credential-env", "ICLOUD_PERSONAL_PASSWORD",
	); exitCode != 0 || stderr != "" {
		t.Fatalf("add code=%d stderr=%q", exitCode, stderr)
	}
	if exitCode, _, stderr := runWithAccountCommands(t, commands, "",
		"--home", home,
		"account", "credential", "delete", "Personal", "--yes",
	); exitCode != 0 || stderr != "" {
		t.Fatalf("delete code=%d stderr=%q", exitCode, stderr)
	}

	failingCommands := commands
	failingCommands.newID = func(string) (string, error) {
		return "", errors.New("forced ID allocation failure")
	}
	privateValue := "temporary-keyring-private-value"
	exitCode, stdout, stderr := runWithAccountCommands(t, failingCommands, privateValue+"\n",
		"--home", home,
		"account", "credential", "replace", "Personal", "--password-stdin",
	)
	if exitCode != 1 || stdout != "" || strings.Contains(stderr, privateValue) || !strings.Contains(stderr, "cannot generate a credential ID") {
		t.Fatalf("replace failure code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if len(keyringStore.values) != 0 {
		t.Fatalf("temporary keyring entry survived rollback: %#v", keyringStore.values)
	}
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "",
		"--home", home,
		"account", "credential", "show", "Personal",
	)
	if exitCode != 7 || stdout != "" || !strings.Contains(stderr, "credential is not configured") {
		t.Fatalf("credential changed after failed replace code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	assertStateExcludes(t, home, privateValue)
}

func TestRemovingOneAccountNeverDeletesAnotherKeyringCredential(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	commands, keyringStore := newTestAccountCommands()
	firstSecret := "first-account-private-value"
	secondSecret := "second-account-private-value"
	if exitCode, _, stderr := runWithAccountCommands(t, commands, firstSecret+"\n",
		"--home", home,
		"account", "add", "--name", "Personal", "--email", "owner@icloud.com", "--password-stdin",
	); exitCode != 0 || stderr != "" {
		t.Fatalf("first add code=%d stderr=%q", exitCode, stderr)
	}
	if exitCode, _, stderr := runWithAccountCommands(t, commands, secondSecret+"\n",
		"--home", home,
		"account", "add", "--name", "Work", "--email", "work@icloud.com", "--password-stdin",
	); exitCode != 0 || stderr != "" {
		t.Fatalf("second add code=%d stderr=%q", exitCode, stderr)
	}
	if len(keyringStore.values) != 2 {
		t.Fatalf("keyring entries before removal = %#v", keyringStore.values)
	}
	if exitCode, _, stderr := runWithAccountCommands(t, commands, "", "--home", home, "account", "default", "Work"); exitCode != 0 || stderr != "" {
		t.Fatalf("default code=%d stderr=%q", exitCode, stderr)
	}
	if exitCode, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "account", "remove", "Personal", "--yes"); exitCode != 0 || stderr != "" || !strings.Contains(stdout, "keyring_deleted=true") {
		t.Fatalf("remove code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if len(keyringStore.values) != 1 {
		t.Fatalf("unexpected keyring entries after removal = %#v", keyringStore.values)
	}
	for _, value := range keyringStore.values {
		if value != secondSecret {
			t.Fatalf("remaining keyring value = %q", value)
		}
	}
	assertStateExcludes(t, home, firstSecret, secondSecret)
}

func TestAccountAddWithoutExplicitNonTTYCredentialDoesNotPromptOrCreateState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "unused")
	commands, _ := newTestAccountCommands()
	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home,
		"--no-input",
		"account", "add",
		"--name", "Personal",
		"--email", "owner@icloud.com",
	)
	if exitCode != 8 || stdout != "" || !strings.Contains(stderr, "credential input is required") || strings.Contains(stderr, "App-specific password") {
		t.Fatalf("code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("noninteractive failure created state: %v", err)
	}
}

func TestAccountWritesReturnConflictWhileAccountLockIsHeld(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.EnsureAppPaths(paths); err != nil {
		t.Fatal(err)
	}
	lock := flock.New(paths.AccountLock, flock.SetPermissions(0o600))
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	commands, _ := newTestAccountCommands()
	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--timeout", "50ms",
		"--home", home,
		"account", "add",
		"--name", "Personal",
		"--email", "owner@icloud.com",
		"--credential-env", "ICLOUD_PASSWORD",
	)
	if exitCode != 6 || stdout != "" || !strings.Contains(stderr, "local account state is busy") || !strings.Contains(stderr, "retryable=true") {
		t.Fatalf("code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestAccountTestPersistsUsernameFallbackCapabilitiesAndHealthyState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_TEST_APP_PASSWORD", "synthetic-phase4-secret")
	commands, _ := newTestAccountCommands()
	imapValidator := &fakeIMAPValidator{
		results: map[string]transport.IMAPResult{
			"owner": {
				Capabilities: []string{"special-use", "IMAP4rev1", "IDLE", "IDLE"},
				Mailboxes:    6,
				Inbox: transport.MailboxStatus{
					Mailbox:     "INBOX",
					Messages:    42,
					Unseen:      3,
					UIDNext:     50,
					UIDValidity: 77,
				},
			},
		},
		errors: map[string]error{
			"owner@icloud.com": transport.NewFailure(transport.StageIMAPAuth, transport.FailureAuthentication, false, errors.New("provider text containing a secret")),
		},
	}
	smtpValidator := &fakeSMTPValidator{result: transport.SMTPResult{Capabilities: []string{"smtputf8", "AUTH", "AUTH"}}}
	commands.imap = imapValidator
	commands.smtp = smtpValidator

	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "account", "add",
		"--name", "Personal", "--email", "owner@icloud.com",
		"--credential-env", "ICLOUD_TEST_APP_PASSWORD",
	)
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "status=pending_validation") {
		t.Fatalf("add code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "test")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("test code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	for _, want := range []string{"status=connected", "imap=ok", "smtp=ok", "imap_username=owner", "mailboxes=6", "inbox_messages=42", "inbox_unseen=3"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("test output missing %q: %q", want, stdout)
		}
	}
	if lines, bytes := strings.Count(stdout, "\n"), len(stdout); lines != 2 || bytes > 360 {
		t.Fatalf("account test compact budget exceeded: lines=%d bytes=%d output=%q", lines, bytes, stdout)
	}
	if got := strings.Join(imapValidator.usernames, ","); got != "owner@icloud.com,owner" {
		t.Fatalf("IMAP usernames = %q", got)
	}
	if got := strings.Join(smtpValidator.usernames, ","); got != "owner@icloud.com" {
		t.Fatalf("SMTP usernames = %q", got)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "show", "--json")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("show code=%d stderr=%q", exitCode, stderr)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Account account.Account `json:"account"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	stored := envelope.Data.Account
	if !envelope.OK || stored.Status != account.StatusConnected || stored.IMAPUsername != "owner" || stored.LastHealthyAt == nil || stored.LastTestedAt == nil || stored.LastErrorCode != "" {
		t.Fatalf("stored account = %+v", stored)
	}
	if strings.Join(stored.IMAPCapabilities, ",") != "IDLE,IMAP4REV1,SPECIAL-USE" || strings.Join(stored.SMTPCapabilities, ",") != "AUTH,SMTPUTF8" {
		t.Fatalf("stored capabilities: imap=%v smtp=%v", stored.IMAPCapabilities, stored.SMTPCapabilities)
	}
	for _, private := range []string{"synthetic-phase4-secret", "ICLOUD_TEST_APP_PASSWORD", "provider text"} {
		if strings.Contains(stdout, private) || strings.Contains(stderr, private) {
			t.Fatalf("public output contains private value %q", private)
		}
	}

	priorCalls := len(imapValidator.usernames) + len(smtpValidator.usernames)
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "doctor")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "status=connected") || !strings.Contains(stdout, "status=healthy") {
		t.Fatalf("doctor code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if calls := len(imapValidator.usernames) + len(smtpValidator.usernames); calls != priorCalls {
		t.Fatalf("local doctor unexpectedly contacted protocols: calls=%d prior=%d", calls, priorCalls)
	}
	if lines, bytes := strings.Count(stdout, "\n"), len(stdout); lines != 2 || bytes > 320 {
		t.Fatalf("doctor compact budget exceeded: lines=%d bytes=%d output=%q", lines, bytes, stdout)
	}
}

func TestAccountTestFailureDegradesWithoutLosingLastHealthyAndRedactsDetails(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_TEST_APP_PASSWORD", "synthetic-health-secret")
	commands, _ := newTestAccountCommands()
	imapValidator := &fakeIMAPValidator{results: map[string]transport.IMAPResult{
		"owner@icloud.com": {Capabilities: []string{"IMAP4rev1"}, Mailboxes: 1},
	}}
	smtpValidator := &fakeSMTPValidator{result: transport.SMTPResult{Capabilities: []string{"AUTH"}}}
	commands.imap = imapValidator
	commands.smtp = smtpValidator
	if code, _, stderr := runWithAccountCommands(t, commands, "", "--home", home, "account", "add", "--name", "Personal", "--email", "owner@icloud.com", "--credential-env", "ICLOUD_TEST_APP_PASSWORD"); code != 0 {
		t.Fatalf("add code=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := runWithAccountCommands(t, commands, "", "--home", home, "account", "test"); code != 0 {
		t.Fatalf("initial test code=%d stderr=%q", code, stderr)
	}
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	before, err := state.Accounts().ResolveSelected(context.Background(), "")
	_ = state.Close()
	if err != nil || before.LastHealthyAt == nil {
		t.Fatalf("healthy account = %+v err=%v", before, err)
	}

	smtpValidator.err = transport.NewFailure(transport.StageSMTPAuth, transport.FailureAuthentication, false, errors.New("535 rejected synthetic-health-secret"))
	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "account", "test", "--json")
	if exitCode != 4 || stdout != "" {
		t.Fatalf("failure code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	for _, want := range []string{`"code":"authentication"`, `"stage":"smtp_auth"`, `"status":"degraded"`} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("JSON error missing %q: %q", want, stderr)
		}
	}
	for _, private := range []string{"synthetic-health-secret", "ICLOUD_TEST_APP_PASSWORD", "535 rejected"} {
		if strings.Contains(stderr, private) {
			t.Fatalf("JSON error leaked %q: %q", private, stderr)
		}
	}

	state, err = store.Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	after, err := state.Accounts().ResolveSelected(context.Background(), "")
	_ = state.Close()
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != account.StatusDegraded || after.LastErrorCode != string(transport.StageSMTPAuth) || after.LastErrorAt == nil || after.LastTestedAt == nil || after.LastHealthyAt == nil || !after.LastHealthyAt.Equal(*before.LastHealthyAt) {
		t.Fatalf("degraded account = %+v", after)
	}
}

func TestAccountTestMissingCredentialValueStopsBeforeNetwork(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	_ = os.Unsetenv("ICLOUD_ABSENT_APP_PASSWORD")
	commands, _ := newTestAccountCommands()
	imapValidator := &fakeIMAPValidator{}
	smtpValidator := &fakeSMTPValidator{}
	commands.imap = imapValidator
	commands.smtp = smtpValidator
	if code, _, stderr := runWithAccountCommands(t, commands, "", "--home", home, "account", "add", "--name", "Personal", "--email", "owner@icloud.com", "--credential-env", "ICLOUD_ABSENT_APP_PASSWORD"); code != 0 {
		t.Fatalf("add code=%d stderr=%q", code, stderr)
	}
	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "account", "test", "--json")
	if exitCode != 4 || stdout != "" || !strings.Contains(stderr, `"stage":"credential"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if len(imapValidator.usernames) != 0 || len(smtpValidator.usernames) != 0 || strings.Contains(stderr, "ICLOUD_ABSENT_APP_PASSWORD") {
		t.Fatalf("network calls or reference leak: imap=%v smtp=%v stderr=%q", imapValidator.usernames, smtpValidator.usernames, stderr)
	}
}

func TestDoctorZeroStateAndLiveMode(t *testing.T) {
	home := filepath.Join(t.TempDir(), "empty")
	commands, _ := newTestAccountCommands()
	commands.imap = &fakeIMAPValidator{}
	commands.smtp = &fakeSMTPValidator{}
	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "doctor")
	if exitCode != 0 || stderr != "" || stdout != "doctor status=configuration_required\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestAccountTestAndLiveDoctorSupportBoundedAllAccountRuns(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_PERSONAL_TEST", "personal-synthetic-secret")
	t.Setenv("ICLOUD_WORK_TEST", "work-synthetic-secret")
	commands, _ := newTestAccountCommands()
	imapValidator := &fakeIMAPValidator{results: map[string]transport.IMAPResult{
		"owner@icloud.com": {Capabilities: []string{"IMAP4rev1"}, Mailboxes: 3},
		"work@icloud.com":  {Capabilities: []string{"IMAP4rev1", "IDLE"}, Mailboxes: 4},
	}}
	smtpValidator := &fakeSMTPValidator{result: transport.SMTPResult{Capabilities: []string{"AUTH"}}}
	commands.imap = imapValidator
	commands.smtp = smtpValidator
	for _, args := range [][]string{
		{"--home", home, "account", "add", "--name", "Personal", "--email", "owner@icloud.com", "--credential-env", "ICLOUD_PERSONAL_TEST"},
		{"--home", home, "account", "add", "--name", "Work", "--email", "work@icloud.com", "--credential-env", "ICLOUD_WORK_TEST"},
	} {
		if code, _, stderr := runWithAccountCommands(t, commands, "", args...); code != 0 {
			t.Fatalf("add %v code=%d stderr=%q", args, code, stderr)
		}
	}
	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "account", "test", "--all")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "account-tests count=2 healthy=2") || strings.Count(stdout, "account-test ") != 2 {
		t.Fatalf("all test code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if lines, bytes := strings.Count(stdout, "\n"), len(stdout); lines != 3 || bytes > 650 {
		t.Fatalf("all-account compact budget exceeded: lines=%d bytes=%d", lines, bytes)
	}

	previousCalls := len(imapValidator.usernames) + len(smtpValidator.usernames)
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "doctor", "--all", "--live")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "doctor accounts=2 status=healthy") || strings.Count(stdout, "doctor acct_") != 2 {
		t.Fatalf("live doctor code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if calls := len(imapValidator.usernames) + len(smtpValidator.usernames); calls != previousCalls+4 {
		t.Fatalf("live doctor protocol calls=%d want=%d", calls, previousCalls+4)
	}
	if lines, bytes := strings.Count(stdout, "\n"), len(stdout); lines != 3 || bytes > 600 {
		t.Fatalf("live doctor compact budget exceeded: lines=%d bytes=%d", lines, bytes)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "--account", "Personal", "account", "test", "--all")
	if exitCode != 2 || stdout != "" || !strings.Contains(stderr, "--all cannot be combined") {
		t.Fatalf("selector conflict code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func assertStateExcludes(t *testing.T, home string, forbidden ...string) {
	t.Helper()
	err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, value := range forbidden {
			if value != "" && bytes.Contains(contents, []byte(value)) {
				return fmt.Errorf("%s contains forbidden value", filepath.Base(path))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readCredentialReferences(t *testing.T, home string) map[account.CredentialScheme]string {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(home, "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rows, err := database.Query(`SELECT scheme, reference FROM credential_refs ORDER BY scheme`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	references := map[account.CredentialScheme]string{}
	for rows.Next() {
		var scheme, reference string
		if err := rows.Scan(&scheme, &reference); err != nil {
			t.Fatal(err)
		}
		references[account.CredentialScheme(scheme)] = reference
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return references
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
