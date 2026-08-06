package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
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

type fakeFolderClient struct {
	results   map[string][]transport.FolderDiscoveryResult
	errors    map[string]error
	usernames []string
	calls     int
}

func (client *fakeFolderClient) DiscoverFolders(_ context.Context, request transport.AuthRequest) (transport.FolderDiscoveryResult, error) {
	client.usernames = append(client.usernames, request.Username)
	client.calls++
	if err := client.errors[request.Username]; err != nil {
		return transport.FolderDiscoveryResult{}, err
	}
	values := client.results[request.Username]
	if len(values) == 0 {
		return transport.FolderDiscoveryResult{}, nil
	}
	result := values[0]
	if len(values) > 1 {
		client.results[request.Username] = values[1:]
	}
	return result, nil
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

func compactContainsAll(output string, values ...string) bool {
	for _, value := range values {
		if !strings.Contains(output, value) {
			return false
		}
	}
	return true
}

func compactBlockCount(output, kind string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if line == kind {
			count++
		}
	}
	return count
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
		"Mail commands:",
		"Documentation commands:",
		"account",
		"capabilities",
		"doctor",
		"mail",
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

func TestMailMessageHelpAdvertisesEveryExplicitContentModeWithoutCreatingState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, test := range []struct {
		args []string
		want []string
	}{
		{[]string{"--home", home, "mail", "message", "--help"}, []string{"show", "raw", "export"}},
		{[]string{"--home", home, "mail", "message", "show", "--help"}, []string{"--full", "--format", "--headers", "--body-only", "--metadata-only", "--verbose", "--remote", "--cache", "--json"}},
		{[]string{"--home", home, "mail", "search", "--help"}, []string{"--remote", "--all-folders", "--folder", "--attachment-name", "--result"}},
		{[]string{"--home", home, "mail", "sync", "--help"}, []string{"--hydrate", "--folder", "--limit"}},
		{[]string{"--home", home, "mail", "message", "raw", "--help"}, []string{"exact locally stored RFC822"}},
		{[]string{"--home", home, "mail", "message", "export", "--help"}, []string{"--output", "--force", "atomically"}},
	} {
		code, stdout, stderr := run(t, test.args...)
		if code != 0 || stderr != "" {
			t.Fatalf("help %v code=%d stdout=%q stderr=%q", test.args, code, stdout, stderr)
		}
		for _, want := range test.want {
			if !strings.Contains(stdout, want) {
				t.Fatalf("help %v missing %q:\n%s", test.args, want, stdout)
			}
		}
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("message help created state: %v", err)
	}
}

func TestFolderDiscoveryAndPolicyWorkflow(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_FOLDER_PASSWORD", "synthetic-folder-secret")
	commands, _ := newTestAccountCommands()
	folderClient := &fakeFolderClient{
		errors: map[string]error{
			"owner@icloud.com": transport.NewFailure(transport.StageIMAPAuth, transport.FailureAuthentication, false, errors.New("rejected")),
		},
		results: map[string][]transport.FolderDiscoveryResult{
			"owner": {folderDiscoveryFixture(false), folderDiscoveryFixture(true)},
		},
	}
	commands.folders = folderClient

	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home,
		"account", "add", "--name", "Personal", "--email", "owner@icloud.com", "--credential-env", "ICLOUD_FOLDER_PASSWORD")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "status=pending_validation") {
		t.Fatalf("add code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "list")
	if exitCode != 0 || stderr != "" || !compactContainsAll(stdout, "folders\n", "account=acct_", "count=0") || folderClient.calls != 0 {
		t.Fatalf("empty local list code=%d stdout=%q stderr=%q calls=%d", exitCode, stdout, stderr, folderClient.calls)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "status", "--verbose")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("status code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	for _, want := range []string{
		"name=INBOX", "kind=inbox",
		`name="Sent Messages"`, "kind=sent",
		"name=Projects", "kind=other", "selectable=no",
		"name=Projects/Alpha", "parent=Projects",
		"name=Unicode-日本語",
		"wire_name=Unicode-&ZeVnLIqe-",
		"folder-status\n", "account=acct_",
		"count=6",
		"present=6",
		"enabled=2",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "synthetic-folder-secret") || strings.Contains(stdout, "ICLOUD_FOLDER_PASSWORD") {
		t.Fatalf("status exposed credential material: %q", stdout)
	}
	if len(folderClient.usernames) < 2 || folderClient.usernames[0] != "owner@icloud.com" || folderClient.usernames[1] != "owner" {
		t.Fatalf("folder usernames = %v", folderClient.usernames)
	}
	if lines, bytes := strings.Count(stdout, "\n"), len(stdout); lines > 130 || bytes > 2200 {
		t.Fatalf("folder status budget lines=%d bytes=%d", lines, bytes)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "show")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "imap_username=owner") {
		t.Fatalf("working username not persisted code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	priorCalls := folderClient.calls
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "list")
	if exitCode != 0 || stderr != "" || folderClient.calls != priorCalls || compactBlockCount(stdout, "folder") != 6 {
		t.Fatalf("local list code=%d stdout=%q stderr=%q calls=%d prior=%d", exitCode, stdout, stderr, folderClient.calls, priorCalls)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "enable", "Receipts")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "sync=enabled") {
		t.Fatalf("enable code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "enable", "Projects")
	if exitCode != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "not selectable") {
		t.Fatalf("enable container code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "status")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("second status code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	for _, want := range []string{
		"name=Projects/Beta",
		"name=Receipts", "kind=other", "selectable=yes", "subscription=unknown", "present=no", "stale=true", "sync=enabled",
		"count=6",
		"present=5",
		"stale=1",
		"enabled=3",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("second status missing %q:\n%s", want, stdout)
		}
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "list")
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, "name=Receipts") || !strings.Contains(stdout, "count=5") {
		t.Fatalf("present-only list code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "list", "--include-missing", "--json")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, `"name":"Receipts"`) || !strings.Contains(stdout, `"present":false`) || strings.Contains(stdout, "ICLOUD_FOLDER_PASSWORD") {
		t.Fatalf("missing JSON code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestFolderStatusPersistsAllButBoundsOutput(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_FOLDER_PASSWORD", "synthetic-folder-secret")
	commands, _ := newTestAccountCommands()
	values := make([]transport.RemoteFolder, 0, 25)
	for index := 0; index < 25; index++ {
		name := fmt.Sprintf("Folder-%02d", index)
		kind, source := "other", ""
		if index == 0 {
			name, kind, source = "INBOX", "inbox", "inbox"
		}
		values = append(values, transport.RemoteFolder{
			Name: name, WireName: name, Delimiter: "/", Kind: kind, KindSource: source,
			Selectable: true, Subscription: "unknown",
			Status: &transport.MailboxStatus{Mailbox: name, Messages: uint32(index), UIDNext: uint32(index + 1), UIDValidity: uint32(100 + index)},
		})
	}
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {{Folders: values}},
	}}
	exitCode, _, stderr := runWithAccountCommands(t, commands, "", "--home", home,
		"account", "add", "--name", "Personal", "--email", "owner@icloud.com", "--credential-env", "ICLOUD_FOLDER_PASSWORD")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("add code=%d stderr=%q", exitCode, stderr)
	}
	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "status")
	if exitCode != 0 || stderr != "" || compactBlockCount(stdout, "folder") != 20 || !compactContainsAll(stdout, "folder-status\n", "count=20", "total=25", "more=true") {
		t.Fatalf("bounded status code=%d lines=%d stdout=%q stderr=%q", exitCode, strings.Count(stdout, "\n"), stdout, stderr)
	}
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "list", "--limit", "100", "--json")
	if exitCode != 0 || stderr != "" || strings.Count(stdout, `"id":"fld_`) != 25 || !strings.Contains(stdout, `"count":25`) {
		t.Fatalf("persisted list code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestFolderDiscoveryStopsBeforeNetworkWhenCredentialIsMissing(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	commands, _ := newTestAccountCommands()
	folderClient := &fakeFolderClient{}
	commands.folders = folderClient
	exitCode, _, stderr := runWithAccountCommands(t, commands, "", "--home", home,
		"account", "add", "--name", "Personal", "--email", "owner@icloud.com", "--credential-env", "ICLOUD_FOLDER_MISSING")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("add code=%d stderr=%q", exitCode, stderr)
	}
	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "status", "--json")
	if exitCode != cli.ExitAuthentication || stdout != "" || folderClient.calls != 0 {
		t.Fatalf("status code=%d stdout=%q stderr=%q calls=%d", exitCode, stdout, stderr, folderClient.calls)
	}
	if strings.Contains(stderr, "ICLOUD_FOLDER_MISSING") {
		t.Fatalf("error exposed credential reference: %q", stderr)
	}
}

func folderDiscoveryFixture(second bool) transport.FolderDiscoveryResult {
	values := []transport.RemoteFolder{
		{Name: "INBOX", WireName: "INBOX", Delimiter: "/", Kind: "inbox", KindSource: "inbox", Selectable: true, Subscription: "unknown", Status: &transport.MailboxStatus{Mailbox: "INBOX", Messages: 15, Unseen: 2, UIDNext: 20, UIDValidity: 100}},
		{Name: "Sent Messages", WireName: "Sent Messages", Delimiter: "/", Attributes: []string{"\\Sent"}, Kind: "sent", KindSource: "attribute", Selectable: true, Subscription: "unknown", Status: &transport.MailboxStatus{Mailbox: "Sent Messages", Messages: 4, UIDNext: 5, UIDValidity: 101}},
		{Name: "Projects", WireName: "Projects", Delimiter: "/", Attributes: []string{"\\Noselect"}, Kind: "other", Selectable: false, Subscription: "unknown"},
		{Name: "Projects/Alpha", WireName: "Projects/Alpha", ParentName: "Projects", ParentWireName: "Projects", Delimiter: "/", Kind: "other", Selectable: true, Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: "Projects/Alpha", Messages: 1, Unseen: 1, UIDNext: 2, UIDValidity: 102}},
		{Name: "Receipts", WireName: "Receipts", Delimiter: "/", Kind: "other", Selectable: true, Subscription: "unknown", Status: &transport.MailboxStatus{Mailbox: "Receipts", Messages: 1, UIDNext: 2, UIDValidity: 103}},
		{Name: "Unicode-日本語", WireName: "Unicode-&ZeVnLIqe-", Delimiter: "/", Kind: "other", Selectable: true, Subscription: "unknown", Status: &transport.MailboxStatus{Mailbox: "Unicode-日本語", Messages: 2, UIDNext: 3, UIDValidity: 104}},
	}
	if second {
		values = []transport.RemoteFolder{
			values[0], values[1], values[2],
			{Name: "Projects/Beta", WireName: "Projects/Beta", OldName: "Projects/Alpha", OldWireName: "Projects/Alpha", ParentName: "Projects", ParentWireName: "Projects", Delimiter: "/", Kind: "other", Selectable: true, Subscription: "unknown", Status: &transport.MailboxStatus{Mailbox: "Projects/Beta", Messages: 2, UIDNext: 3, UIDValidity: 105}},
			values[5],
		}
	}
	return transport.FolderDiscoveryResult{Capabilities: []string{"IMAP4REV1", "SPECIAL-USE"}, Folders: values}
}

func TestVersionCompactDefault(t *testing.T) {
	exitCode, stdout, stderr := run(t, "version")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	if stdout != "version\ndev\n" {
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
	const want = "capabilities\ncommand=icloud\nversion=dev\nschema=10\naccount_mode=validated_lifecycle\ncredentials=keyring,env,file\nnetwork_validation=true\ndiagnostics=true\nmail_folders=true\nmail_sync=true\nmail_content=true\nmail_attachments=true\nmail_flags=true\nmail_threads=true\nmail_search=true\nmail_remote_search=true\nmail_hydration=true\ndefault_output=compact\njson=true\ncompletion=bash,zsh,fish,powershell\nman=true\n"
	if stdout != want {
		t.Fatalf("stdout mismatch\nwant: %q\n got: %q", want, stdout)
	}
	if lines := strings.Count(stdout, "\n"); lines > 21 {
		t.Fatalf("line budget exceeded: %d", lines)
	}
	if len(stdout) > 480 {
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
	if !ok || data["data_schema_version"] != float64(store.SchemaVersion) || data["network_validation"] != true || data["diagnostics"] != true || data["mail_folders"] != true || data["mail_sync"] != true || data["mail_content"] != true || data["mail_attachments"] != true || data["mail_flags"] != true || data["mail_threads"] != true || data["mail_search"] != true || data["mail_remote_search"] != true || data["mail_hydration"] != true {
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
	if strings.Count(stderr, "\n") != 3 {
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
		if exitCode != 0 || stdout != "accounts\ncount=0\n" || stderr != "" {
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
	if !envelope.OK || envelope.Data.Count != 0 || len(envelope.Data.Accounts) != 0 || envelope.Data.SchemaVersion != store.SchemaVersion {
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
	if exitCode != 0 || stdout != "accounts\ncount=0\n" || stderr != "" {
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
	if exitCode != 0 || populatedError != "" || !compactContainsAll(populatedOutput, "accounts\n", "count=1") {
		t.Fatalf("populated code=%d stdout=%q stderr=%q", exitCode, populatedOutput, populatedError)
	}
	exitCode, emptyOutput, emptyError := run(t, "--home", emptyHome, "account", "list")
	if exitCode != 0 || emptyOutput != "accounts\ncount=0\n" || emptyError != "" {
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
	if stderr != "error\ncode=not_found\nmessage=\"account not found\"\n" {
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
	const wantList = "account\nacct_0198d704-6c00-7000-8000-000000000001 name=Personal\nemail=owner@example.com\nimap_username=owner\nstatus=pending_validation\ndefault=true\ncredential=env\n\naccounts\ncount=1\n"
	if stdout != wantList {
		t.Fatalf("stdout mismatch\nwant: %q\n got: %q", wantList, stdout)
	}
	if strings.Count(stdout, "\n") != 10 || len(stdout) > 220 {
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
	const wantShow = "account\nacct_0198d704-6c00-7000-8000-000000000001 name=Personal\nemail=owner@example.com\nimap_username=owner\nstatus=pending_validation\ndefault=true\ncredential=env\n"
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
		envelope.Data.Account.CredentialScheme != "env" || envelope.Data.SchemaVersion != store.SchemaVersion {
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
	const want = "account\nacct_0198d704-6c00-7000-8000-000000000001 name=Personal\nemail=owner@example.com\nimap_username=owner\nstatus=pending_validation\ndefault=true\ncredential=env\n\naccounts\ncount=1\nmore=true\n"
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
	const want = "error\ncode=conflict\nmessage=\"local state is busy while another icloud process migrates it\"\nretryable=true\n"
	if stderr != want {
		t.Fatalf("stderr mismatch\nwant: %q\n got: %q", want, stderr)
	}
}

func TestNonStateCommandsDoNotCreateHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "unused")
	exitCode, stdout, stderr := run(t, "--home", home, "version")
	if exitCode != 0 || stdout != "version\ndev\n" || stderr != "" {
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
	if stdout != "account-added\nacct_0198d704-6c00-7000-8000-000000000001 status=pending_validation\ndefault=true\ncredential=env\n" {
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
	if exitCode != 0 || stdout != "account-renamed\nacct_0198d704-6c00-7000-8000-000000000002\n" || stderr != "" {
		t.Fatalf("rename code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "account", "default", "Business")
	if exitCode != 0 || stdout != "account-default\nacct_0198d704-6c00-7000-8000-000000000002\n" || stderr != "" {
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
	if exitCode != 0 || stderr != "" || stdout != "account-removed\nacct_0198d704-6c00-7000-8000-000000000002\n" {
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
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, secondSecret) || stdout != "credential-replaced\nacct_0198d704-6c00-7000-8000-000000000001 scheme=keyring\n" {
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
	if exitCode != 0 || stdout != "accounts\ncount=0\n" || stderr != "" {
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
	if exitCode != 0 || stderr != "" || !compactContainsAll(stdout, "accounts\n", "count=1") || len(keyringStore.values) != 1 {
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
	if lines, bytes := strings.Count(stdout, "\n"), len(stdout); lines != 13 || bytes > 360 {
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
	if lines, bytes := strings.Count(stdout, "\n"), len(stdout); lines > 14 || bytes > 320 {
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
	if exitCode != 0 || stderr != "" || stdout != "doctor\nstatus=configuration_required\n" {
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
	if exitCode != 0 || stderr != "" || !compactContainsAll(stdout, "account-tests\n", "count=2", "healthy=2") || compactBlockCount(stdout, "account-test") != 2 {
		t.Fatalf("all test code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if lines, bytes := strings.Count(stdout, "\n"), len(stdout); lines > 24 || bytes > 650 {
		t.Fatalf("all-account compact budget exceeded: lines=%d bytes=%d", lines, bytes)
	}

	previousCalls := len(imapValidator.usernames) + len(smtpValidator.usernames)
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "doctor", "--all", "--live")
	if exitCode != 0 || stderr != "" || !compactContainsAll(stdout, "accounts=2", "status=healthy") || compactBlockCount(stdout, "doctor") != 3 {
		t.Fatalf("live doctor code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if calls := len(imapValidator.usernames) + len(smtpValidator.usernames); calls != previousCalls+4 {
		t.Fatalf("live doctor protocol calls=%d want=%d", calls, previousCalls+4)
	}
	if lines, bytes := strings.Count(stdout, "\n"), len(stdout); lines > 24 || bytes > 600 {
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

type fakeInboxPageResult struct {
	page transport.InboxPage
	raw  map[uint32]string
	err  error
}

type fakeInboxClient struct {
	results  []fakeInboxPageResult
	requests []transport.InboxFetchRequest
	calls    int
}

type fakeFlagClient struct {
	results   []transport.FlagMutationResult
	errors    []error
	requests  []transport.FlagMutationRequest
	usernames []string
}

func (client *fakeFlagClient) UpdateFlags(_ context.Context, auth transport.AuthRequest, request transport.FlagMutationRequest) (transport.FlagMutationResult, error) {
	client.usernames = append(client.usernames, auth.Username)
	client.requests = append(client.requests, request)
	if len(client.errors) > 0 {
		err := client.errors[0]
		client.errors = client.errors[1:]
		if err != nil {
			return transport.FlagMutationResult{}, err
		}
	}
	if len(client.results) == 0 {
		return transport.FlagMutationResult{}, errors.New("unexpected flag mutation")
	}
	result := client.results[0]
	client.results = client.results[1:]
	return result, nil
}

func (client *fakeInboxClient) FetchInboxPage(ctx context.Context, _ transport.AuthRequest, request transport.InboxFetchRequest, stager blob.Stager) (transport.InboxPage, error) {
	client.requests = append(client.requests, request)
	client.calls++
	if len(client.results) == 0 {
		return transport.InboxPage{}, errors.New("unexpected Inbox fetch")
	}
	result := client.results[0]
	client.results = client.results[1:]
	if result.err != nil {
		return transport.InboxPage{}, result.err
	}
	page := result.page
	for index := range page.Messages {
		if page.Messages[index].Raw != nil {
			continue
		}
		raw := result.raw[page.Messages[index].UID]
		staged, err := stager.Stage(ctx, strings.NewReader(raw), int64(len(raw)))
		if err != nil {
			return transport.InboxPage{}, err
		}
		page.Messages[index].Raw = staged
		if page.Messages[index].RFC822Size == 0 {
			page.Messages[index].RFC822Size = int64(len(raw))
		}
	}
	return page, nil
}

type failingBlobStager struct{ err error }

func (stager failingBlobStager) Stage(context.Context, io.Reader, int64) (*blob.Staged, error) {
	return nil, stager.err
}

type failingAttachmentBlobStore struct{ err error }

func (store failingAttachmentBlobStore) StageAttachment(context.Context, io.Reader, int64) (*blob.Staged, error) {
	return nil, store.err
}

func (store failingAttachmentBlobStore) OpenVerifiedContext(context.Context, string, string, int64) (io.ReadCloser, error) {
	return nil, store.err
}

func TestMailSyncIncrementalWorkflowAndCompactBudgets(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_MAIL_SYNC_PASSWORD", "synthetic-mail-sync-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {{Folders: []transport.RemoteFolder{{
			Name: "INBOX", WireName: "INBOX", Kind: "inbox", KindSource: "inbox",
			Selectable: true, Subscription: "subscribed",
			Status: &transport.MailboxStatus{Mailbox: "INBOX", Messages: 3, Unseen: 2, UIDNext: 24, UIDValidity: 100},
		}}}},
	}}
	fixture21 := syntheticMailFixture(21, "fixture-21@example.test", "First fixture", "First compact preview", "")
	fixture22 := syntheticMailFixture(22, "fixture-22@example.test", "Second fixture", "Second compact preview", "")
	fixture23 := syntheticMailFixture(23, "fixture-23@example.test", "Third fixture", "Third compact preview", "hidden-bcc@example.test")
	mailClient := &fakeInboxClient{results: []fakeInboxPageResult{
		{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 22, LastScannedUID: 22, MoreAvailable: true, Bootstrap: true, Messages: []transport.FetchedMessage{
			{UID: 21, Flags: []string{`\Flagged`}, InternalDate: time.Date(2026, 8, 6, 4, 21, 0, 0, time.UTC)},
			{UID: 22, Flags: []string{`\Seen`}, InternalDate: time.Date(2026, 8, 6, 4, 22, 0, 0, time.UTC)},
		}}, raw: map[uint32]string{21: fixture21, 22: fixture22}},
		{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 23, LastScannedUID: 23, Bootstrap: false, Messages: []transport.FetchedMessage{
			{UID: 23, InternalDate: time.Date(2026, 8, 6, 4, 23, 0, 0, time.UTC)},
		}, LocationStates: []transport.LocationState{
			{UID: 21, Flags: []string{`\Seen`, `$RemoteKeyword`}, ModSequence: 31, Present: true},
			{UID: 22, Flags: []string{`\Seen`, `\Flagged`}, ModSequence: 32, Present: true},
		}}, raw: map[uint32]string{23: fixture23}},
	}}
	commands.mail = mailClient

	addAndDiscoverInbox(t, commands, home, "ICLOUD_MAIL_SYNC_PASSWORD")
	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--limit", "2")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("first sync code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	for _, want := range []string{"fetched=2", "created=2", "last_uid=22", "more=true", "bootstrap=true"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("first sync missing %q: %s", want, stdout)
		}
	}
	if strings.Count(stdout, "\n") != 11 || len(stdout) > 520 {
		t.Fatalf("sync budget lines=%d bytes=%d output=%q", strings.Count(stdout, "\n"), len(stdout), stdout)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "inbox", "--limit", "2")
	if exitCode != 0 || stderr != "" || compactBlockCount(stdout, "message") != 2 || !compactContainsAll(stdout, "inbox\n", "count=2", "more=false") {
		t.Fatalf("inbox code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if !compactContainsAll(stdout, "unread=true", "flagged=true") || strings.Contains(stdout, "hidden-bcc") || strings.Count(stdout, "\n") > 24 || len(stdout) > 1400 {
		t.Fatalf("inbox compact privacy/budget failure: %q", stdout)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "status")
	if exitCode != 0 || stderr != "" || !compactContainsAll(stdout, "state=idle", "committed=22", "scanned=22", "more=true") {
		t.Fatalf("status code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--limit", "2")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "fetched=1") || !strings.Contains(stdout, "created=1") ||
		!strings.Contains(stdout, "reconciled=2") || !strings.Contains(stdout, "state_changes=2") ||
		!strings.Contains(stdout, "last_uid=23") || strings.Contains(stdout, "more=true") {
		t.Fatalf("second sync code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if len(mailClient.requests) != 2 || !mailClient.requests[0].Bootstrap || mailClient.requests[0].ExpectedUIDValidity != 0 ||
		mailClient.requests[1].Bootstrap || mailClient.requests[1].ExpectedUIDValidity != 100 || mailClient.requests[1].LastCommittedUID != 22 ||
		!reflect.DeepEqual(mailClient.requests[1].ReconcileUIDs, []uint32{22, 21}) {
		t.Fatalf("sync requests = %+v", mailClient.requests)
	}

	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "inbox", "--limit", "20", "--json")
	if exitCode != 0 || stderr != "" || strings.Count(stdout, `"id":"msg_`) != 3 || !strings.Contains(stdout, `"count":3`) {
		t.Fatalf("JSON inbox code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	for _, forbidden := range []string{"raw/sha256", "raw_storage_key", "raw_content_hash", "hidden-bcc@example.test", `"bcc"`} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("JSON Inbox exposed %q: %s", forbidden, stdout)
		}
	}
	if !strings.Contains(stdout, `"unknown":["$RemoteKeyword"]`) || !strings.Contains(stdout, `"mod_sequence":31`) {
		t.Fatalf("reconciled structured flags missing: %s", stdout)
	}
}

func TestMailSearchWorkflowFiltersCursorsAndRepair(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_MAIL_SEARCH_PASSWORD", "synthetic-mail-search-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {{Folders: []transport.RemoteFolder{{
			Name: "INBOX", WireName: "INBOX", Kind: "inbox", KindSource: "inbox",
			Selectable: true, Subscription: "subscribed",
			Status: &transport.MailboxStatus{Mailbox: "INBOX", Messages: 3, Unseen: 2, UIDNext: 304, UIDValidity: 100},
		}}}},
	}}
	firstRaw := syntheticMailFixture(301, "search-301@example.test", "Résumé invoice", "Alpha needle body", "hidden-search-bcc@example.test")
	secondRaw := syntheticMailFixture(302, "search-302@example.test", "Launch note", "Beta needle body", "")
	thirdRaw := syntheticMailFixture(303, "search-303@example.test", "Ordinary note", "Gamma body", "")
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{
		page: transport.InboxPage{
			Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 303, LastScannedUID: 303,
			Bootstrap: true, Messages: []transport.FetchedMessage{
				{UID: 301, Flags: []string{`\Flagged`}, InternalDate: time.Date(2026, 8, 6, 10, 1, 0, 0, time.UTC)},
				{UID: 302, Flags: []string{`\Seen`}, InternalDate: time.Date(2026, 8, 6, 10, 2, 0, 0, time.UTC)},
				{UID: 303, InternalDate: time.Date(2026, 8, 6, 10, 3, 0, 0, time.UTC)},
			},
		}, raw: map[uint32]string{301: firstRaw, 302: secondRaw, 303: thirdRaw},
	}}}
	addAndDiscoverInbox(t, commands, home, "ICLOUD_MAIL_SEARCH_PASSWORD")
	if code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync"); code != 0 || stderr != "" {
		t.Fatalf("sync code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "needle", "--limit", "1")
	if code != 0 || stderr != "" || compactBlockCount(stdout, "search-hit") != 1 ||
		!compactContainsAll(stdout, "kind=message", "match=body", "search-results\n", "count=1", "more=true", "next_cursor=cur_", "index=complete") {
		t.Fatalf("compact search code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stdout, "query=") || strings.Contains(stdout, "hidden-search-bcc") || strings.Contains(stdout, "synthetic-mail-search-secret") ||
		strings.Count(stdout, "\n") > 18 || len(stdout) > 1100 {
		t.Fatalf("compact search privacy/budget failure: %q", stdout)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "needle", "--limit", "1", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("first JSON search code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var firstPage struct {
		Data struct {
			Hits []struct {
				Message struct {
					ID string `json:"id"`
				} `json:"message"`
				MatchedField string `json:"matched_field"`
			} `json:"hits"`
			More       bool   `json:"more"`
			NextCursor string `json:"next_cursor"`
			Query      struct {
				HasText    bool   `json:"has_text"`
				ResultKind string `json:"result_kind"`
				IndexState string `json:"index_state"`
			} `json:"query"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &firstPage); err != nil || len(firstPage.Data.Hits) != 1 || !firstPage.Data.More ||
		firstPage.Data.NextCursor == "" || !firstPage.Data.Query.HasText || firstPage.Data.Query.ResultKind != "messages" ||
		firstPage.Data.Query.IndexState != "complete" || firstPage.Data.Hits[0].MatchedField != "body" {
		t.Fatalf("first JSON search err=%v page=%+v", err, firstPage)
	}
	if strings.Contains(stdout, "hidden-search-bcc") || strings.Contains(stdout, `"bcc"`) || strings.Contains(stdout, "raw_storage_key") {
		t.Fatalf("JSON search exposed private storage/header data: %s", stdout)
	}
	firstMessageID := firstPage.Data.Hits[0].Message.ID

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "needle", "--limit", "1", "--cursor", firstPage.Data.NextCursor, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("second JSON search code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var secondPage struct {
		Data struct {
			Hits []struct {
				Message struct {
					ID string `json:"id"`
				} `json:"message"`
			} `json:"hits"`
			More       bool   `json:"more"`
			NextCursor string `json:"next_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &secondPage); err != nil || len(secondPage.Data.Hits) != 1 || secondPage.Data.More ||
		secondPage.Data.NextCursor != "" || secondPage.Data.Hits[0].Message.ID == firstMessageID {
		t.Fatalf("second JSON search err=%v page=%+v", err, secondPage)
	}
	code, _, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "different", "--limit", "1", "--cursor", firstPage.Data.NextCursor)
	if code != cli.ExitUsage || !strings.Contains(stderr, "belongs to another query") {
		t.Fatalf("query-bound cursor code=%d stderr=%q", code, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "inbox", "--query", "alpha", "--unread", "--flagged")
	if code != 0 || stderr != "" || compactBlockCount(stdout, "search-hit") != 1 ||
		!compactContainsAll(stdout, `subject="Résumé invoice"`, "match=body", "inbox\n", "count=1", "more=false") {
		t.Fatalf("filtered inbox code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Count(stdout, "\n") > 18 || len(stdout) > 1100 {
		t.Fatalf("filtered inbox compact budget: lines=%d bytes=%d output=%q", strings.Count(stdout, "\n"), len(stdout), stdout)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "latest", "--query", "beta")
	if code != 0 || stderr != "" || compactBlockCount(stdout, "search-hit") != 1 ||
		!compactContainsAll(stdout, "kind=thread", `subject="Launch note"`, "match=body", "threads\n", "count=1") {
		t.Fatalf("filtered latest code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Count(stdout, "\n") > 18 || len(stdout) > 1100 {
		t.Fatalf("filtered latest compact budget: lines=%d bytes=%d output=%q", strings.Count(stdout, "\n"), len(stdout), stdout)
	}

	database, err := sql.Open("sqlite", filepath.Join(home, "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "alpha", "--json")
	if code != 0 || stderr != "" {
		database.Close()
		t.Fatalf("target search code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var targetPage struct {
		Data struct {
			Hits []struct {
				Message struct {
					ID string `json:"id"`
				} `json:"message"`
			} `json:"hits"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &targetPage); err != nil || len(targetPage.Data.Hits) != 1 {
		database.Close()
		t.Fatalf("target search err=%v page=%+v", err, targetPage)
	}
	repairMessageID := targetPage.Data.Hits[0].Message.ID
	if _, err := database.Exec(`UPDATE message_search_documents SET body = 'preview only', body_state = 'pending' WHERE message_id = ?`, repairMessageID); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "alpha", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"index_state":"pending"`) || !strings.Contains(stdout, `"count":0`) {
		t.Fatalf("pending search code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "repair", "--limit", "1")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "search-repair\n", "selected=1", "repaired=1", "failed=0", "remaining=0", "errors=0") {
		t.Fatalf("search repair code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "alpha", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"index_state":"complete"`) || !strings.Contains(stdout, repairMessageID) {
		t.Fatalf("repaired search code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestMailThreadReconstructionWorkflowAndCompactBudgets(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_MAIL_THREAD_PASSWORD", "synthetic-mail-thread-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {{Folders: []transport.RemoteFolder{{
			Name: "INBOX", WireName: "INBOX", Kind: "inbox", KindSource: "inbox",
			Selectable: true, Subscription: "subscribed",
			Status: &transport.MailboxStatus{Mailbox: "INBOX", Messages: 2, Unseen: 1, UIDNext: 203, UIDValidity: 100},
		}}}},
	}}
	rootRaw := syntheticThreadMailFixture(
		201, "phase11-root@example.test", "Phase 11 thread", "Root chronological body",
		"Fixture Sender <sender@example.test>", "owner@icloud.com", nil, "", "",
		time.Date(2026, 8, 6, 9, 20, 0, 0, time.UTC),
	)
	replyRaw := syntheticThreadMailFixture(
		202, "phase11-reply@example.test", "Re: Phase 11 thread", "Reply chronological body",
		"owner@icloud.com", "Fixture Sender <sender@example.test>", []string{"phase11-root@example.test"},
		"phase11-root@example.test", "hidden-thread-bcc@example.test",
		time.Date(2026, 8, 6, 9, 21, 0, 0, time.UTC),
	)
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{
		page: transport.InboxPage{
			Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 202, LastScannedUID: 202,
			Bootstrap: true, Messages: []transport.FetchedMessage{
				{UID: 201, InternalDate: time.Date(2026, 8, 6, 9, 20, 0, 0, time.UTC)},
				{UID: 202, Flags: []string{`\Seen`}, InternalDate: time.Date(2026, 8, 6, 9, 21, 0, 0, time.UTC)},
			},
		},
		raw: map[uint32]string{201: rootRaw, 202: replyRaw},
	}}}

	addAndDiscoverInbox(t, commands, home, "ICLOUD_MAIL_THREAD_PASSWORD")
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--folder", "inbox")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "fetched=2", "created=2") {
		t.Fatalf("thread fixture sync code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "latest")
	if code != 0 || stderr != "" || compactBlockCount(stdout, "thread") != 1 ||
		!compactContainsAll(stdout, `subject="Phase 11 thread"`, "messages=2", "unread=1", "reconstruction=local_rfc_graph", "threads\n", "count=1", "more=false") {
		t.Fatalf("latest compact code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stdout, "hidden-thread-bcc") || strings.Count(stdout, "\n") > 20 || len(stdout) > 1100 {
		t.Fatalf("latest compact privacy/budget failure: %q", stdout)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "latest", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("latest JSON code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var latestEnvelope struct {
		Data struct {
			Threads []struct {
				ID               string `json:"id"`
				MessageCount     int    `json:"message_count"`
				UnreadCount      int    `json:"unread_count"`
				Reconstruction   string `json:"reconstruction"`
				RootRFCMessageID string `json:"root_rfc_message_id"`
			} `json:"threads"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &latestEnvelope); err != nil || len(latestEnvelope.Data.Threads) != 1 {
		t.Fatalf("decode latest JSON err=%v envelope=%+v", err, latestEnvelope)
	}
	thread := latestEnvelope.Data.Threads[0]
	if !strings.HasPrefix(thread.ID, "thr_") || thread.MessageCount != 2 || thread.UnreadCount != 1 ||
		thread.Reconstruction != "local_rfc_graph" || thread.RootRFCMessageID != "phase11-root@example.test" {
		t.Fatalf("latest thread = %+v", thread)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "thread", "show", thread.ID)
	if code != 0 || stderr != "" || compactBlockCount(stdout, "thread") != 1 || compactBlockCount(stdout, "message") != 2 ||
		!compactContainsAll(stdout, "Root chronological body uid=201", "Reply chronological body uid=202", "thread-page\n", "count=2", "more=false") {
		t.Fatalf("thread compact code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stdout, "hidden-thread-bcc") || strings.Contains(stdout, "raw_storage_key") || strings.Count(stdout, "\n") > 32 || len(stdout) > 2100 {
		t.Fatalf("thread compact privacy/budget failure: %q", stdout)
	}
	if !strings.Contains(stdout, "\n\nthread-page\n") {
		t.Fatalf("thread compact summary is not separated from message blocks: %q", stdout)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "thread", "show", thread.ID, "--body-only")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Root chronological body uid=201") ||
		!strings.Contains(stdout, "Reply chronological body uid=202") || strings.Contains(stdout, "message=") || strings.Contains(stdout, "thread-page") ||
		strings.Index(stdout, "Root chronological body") > strings.Index(stdout, "Reply chronological body") {
		t.Fatalf("thread body-only code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "thread", "show", thread.ID, "--limit", "1", "--metadata-only", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("thread first cursor page code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var firstCursorPage struct {
		Data struct {
			Count      int    `json:"count"`
			More       bool   `json:"more"`
			NextCursor string `json:"next_cursor"`
			Thread     struct {
				Messages []struct {
					Message struct {
						ID string `json:"id"`
					} `json:"message"`
				} `json:"messages"`
			} `json:"thread"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &firstCursorPage); err != nil || firstCursorPage.Data.Count != 1 ||
		!firstCursorPage.Data.More || firstCursorPage.Data.NextCursor == "" || len(firstCursorPage.Data.Thread.Messages) != 1 {
		t.Fatalf("decode first cursor page err=%v page=%+v", err, firstCursorPage)
	}
	firstCursorMessageID := firstCursorPage.Data.Thread.Messages[0].Message.ID
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "thread", "show", thread.ID, "--limit", "1", "--cursor", firstCursorPage.Data.NextCursor, "--metadata-only", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("thread second cursor page code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var secondCursorPage struct {
		Data struct {
			Count      int    `json:"count"`
			More       bool   `json:"more"`
			NextCursor string `json:"next_cursor"`
			Thread     struct {
				Messages []struct {
					Message struct {
						ID string `json:"id"`
					} `json:"message"`
				} `json:"messages"`
			} `json:"thread"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &secondCursorPage); err != nil || secondCursorPage.Data.Count != 1 ||
		secondCursorPage.Data.More || secondCursorPage.Data.NextCursor != "" || len(secondCursorPage.Data.Thread.Messages) != 1 ||
		secondCursorPage.Data.Thread.Messages[0].Message.ID == firstCursorMessageID {
		t.Fatalf("decode second cursor page err=%v page=%+v", err, secondCursorPage)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "thread", "show", thread.ID, "--full")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Root chronological body uid=201") ||
		!strings.Contains(stdout, "Reply chronological body uid=202") || strings.Index(stdout, "Root chronological body") > strings.Index(stdout, "Reply chronological body") {
		t.Fatalf("thread full code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "thread", "show", thread.ID, "--metadata-only", "--verbose", "--json")
	if code != 0 || stderr != "" || strings.Count(stdout, `"status":"resolved"`) != 2 ||
		!strings.Contains(stdout, `"root_rfc_message_id":"phase11-root@example.test"`) ||
		!strings.Contains(stdout, `"message_count":2`) || !strings.Contains(stdout, `"count":2`) {
		t.Fatalf("thread metadata JSON code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stdout, `"id":"ted_`) {
		t.Fatalf("thread metadata JSON exposed internal edge IDs: %s", stdout)
	}
	for _, forbidden := range []string{"synthetic-mail-thread-secret", "hidden-thread-bcc@example.test", `"bcc"`, "raw_storage_key", "raw_content_hash", "Root chronological body", "Reply chronological body"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("thread metadata JSON exposed %q: %s", forbidden, stdout)
		}
	}

	var threadEnvelope struct {
		Data struct {
			Thread struct {
				Messages []struct {
					Message struct {
						ID string `json:"id"`
					} `json:"message"`
				} `json:"messages"`
			} `json:"thread"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &threadEnvelope); err != nil || len(threadEnvelope.Data.Thread.Messages) != 2 {
		t.Fatalf("decode thread metadata JSON err=%v envelope=%+v", err, threadEnvelope)
	}
	rootMessageID := threadEnvelope.Data.Thread.Messages[0].Message.ID
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "thread", "show", rootMessageID, "--metadata-only")
	if code != 0 || stderr != "" || !strings.Contains(stdout, thread.ID) || compactBlockCount(stdout, "message") != 2 {
		t.Fatalf("message selector code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "thread", "show", thread.ID, "--format", "raw")
	if code != cli.ExitUsage || !strings.Contains(stderr, "plain multi-message raw output is ambiguous") {
		t.Fatalf("thread raw guard code=%d stderr=%q", code, stderr)
	}

	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	localState, err := store.Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	selectedAccount, err := localState.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		localState.Close()
		t.Fatal(err)
	}
	rootMessage, err := localState.Mail().Message(context.Background(), selectedAccount.ID, rootMessageID)
	if err != nil {
		localState.Close()
		t.Fatal(err)
	}
	rootLocation, ok := rawMessageLocation(rootMessage)
	if !ok {
		localState.Close()
		t.Fatal("thread root has no raw location")
	}
	if err := localState.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(paths.BlobRoot, filepath.FromSlash(rootLocation.RawStorageKey))); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "thread", "show", thread.ID, "--metadata-only")
	if code != 0 || stderr != "" || !strings.Contains(stdout, thread.ID) || compactBlockCount(stdout, "message") != 2 {
		t.Fatalf("lazy compact thread read code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "thread", "show", thread.ID, "--full")
	if code == 0 || !strings.Contains(stderr, "content") {
		t.Fatalf("missing raw full-thread read code=%d stderr=%q", code, stderr)
	}
}

func TestMailSentTailBootstrapAndNewObservationMerge(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_MAIL_SENT_PASSWORD", "synthetic-mail-sent-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {{Folders: []transport.RemoteFolder{
			{
				Name: "INBOX", WireName: "INBOX", Kind: "inbox", KindSource: "inbox",
				Selectable: true, Subscription: "subscribed",
				Status: &transport.MailboxStatus{Mailbox: "INBOX", Messages: 1, UIDNext: 22, UIDValidity: 100},
			},
			{
				Name: "Sent Messages", WireName: "Sent Messages", Kind: "sent", KindSource: "special_use",
				Selectable: true, Subscription: "subscribed",
				Status: &transport.MailboxStatus{Mailbox: "Sent Messages", Messages: 50, UIDNext: 51, UIDValidity: 200},
			},
		}}},
	}}
	inboxRaw := syntheticMailFixture(21, "phase10-shared@example.test", "Phase 10 shared", "Inbox representative body", "")
	sentRaw := syntheticMailFixture(51, "phase10-shared@example.test", "Phase 10 shared", "Sent representative body", "hidden-sent-bcc@example.test")
	mailClient := &fakeInboxClient{results: []fakeInboxPageResult{
		{
			page: transport.InboxPage{
				Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 21, LastScannedUID: 21,
				Bootstrap: true, Messages: []transport.FetchedMessage{{
					UID: 21, Flags: []string{`\Seen`}, InternalDate: time.Date(2026, 8, 6, 7, 21, 0, 0, time.UTC),
				}},
			},
			raw: map[uint32]string{21: inboxRaw},
		},
		{
			page: transport.InboxPage{
				Mailbox: "Sent Messages", UIDValidity: 200, LastCommittedUID: 50, LastScannedUID: 50,
				Bootstrap: true,
			},
		},
		{
			page: transport.InboxPage{
				Mailbox: "Sent Messages", UIDValidity: 200, LastCommittedUID: 51, LastScannedUID: 51,
				Messages: []transport.FetchedMessage{{
					UID: 51, Flags: []string{`\Flagged`}, InternalDate: time.Date(2026, 8, 6, 7, 22, 0, 0, time.UTC),
				}},
			},
			raw: map[uint32]string{51: sentRaw},
		},
	}}
	commands.mail = mailClient

	addAndDiscoverInbox(t, commands, home, "ICLOUD_MAIL_SENT_PASSWORD")
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--limit", "2")
	if code != 0 || stderr != "" || compactBlockCount(stdout, "mail-sync") != 2 ||
		!compactContainsAll(stdout, "folder=INBOX", "fetched=1", "created=1") ||
		!compactContainsAll(stdout, `folder="Sent Messages"`, "fetched=0", "last_uid=50", "bootstrap=true") {
		t.Fatalf("initial multi-folder sync code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(mailClient.requests) != 2 || mailClient.requests[0].Mailbox != "INBOX" ||
		!mailClient.requests[0].Bootstrap || mailClient.requests[0].TailOnlyBootstrap ||
		mailClient.requests[1].Mailbox != "Sent Messages" || !mailClient.requests[1].Bootstrap ||
		!mailClient.requests[1].TailOnlyBootstrap || mailClient.requests[1].LastCommittedUID != 0 {
		t.Fatalf("initial folder requests = %+v", mailClient.requests)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--folder", "sent")
	if code != 0 || stderr != "" || compactBlockCount(stdout, "mail-sync") != 1 ||
		!compactContainsAll(stdout, `folder="Sent Messages"`, "fetched=1", "created=0", "updated=1", "identity_merges=1", "last_uid=51") {
		t.Fatalf("incremental Sent sync code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(mailClient.requests) != 3 || mailClient.requests[2].Mailbox != "Sent Messages" ||
		mailClient.requests[2].Bootstrap || mailClient.requests[2].TailOnlyBootstrap ||
		mailClient.requests[2].ExpectedUIDValidity != 200 || mailClient.requests[2].LastCommittedUID != 50 {
		t.Fatalf("incremental Sent request = %+v", mailClient.requests)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "inbox", "--folder", "sent", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"count":1`) ||
		!strings.Contains(stdout, `"direction":"outbound"`) || !strings.Contains(stdout, `"unread":false`) ||
		strings.Count(stdout, `"folder_kind"`) != 2 || !strings.Contains(stdout, `"folder_kind":"sent"`) ||
		!strings.Contains(stdout, `"folder_kind":"inbox"`) || !strings.Contains(stdout, `"kind":"rfc_message_id"`) ||
		strings.Count(stdout, `"kind":"provider_location"`) != 2 ||
		!strings.Contains(stdout, `"content_location_id":"loc_0198d704-6c00-7000-8000-000000000001"`) ||
		!strings.Contains(stdout, `"snippet":"Inbox representative body uid=21"`) || strings.Contains(stdout, "Sent representative body") {
		t.Fatalf("Sent local list code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, forbidden := range []string{"synthetic-mail-sent-secret", "raw_storage_key", "raw_content_hash", "hidden-sent-bcc@example.test", `"bcc"`} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("Sent JSON exposed %q: %s", forbidden, stdout)
		}
	}

	messageID := "msg_0198d704-6c00-7000-8000-000000000001"
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID, "--full")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Inbox representative body uid=21") || strings.Contains(stdout, "Sent representative body") {
		t.Fatalf("representative content code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "raw", messageID)
	if code != 0 || stderr != "" || stdout != inboxRaw {
		t.Fatalf("representative raw code=%d bytes=%d stderr=%q", code, len(stdout), stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "status", "--folder", "sent")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, `folder="Sent Messages"`, "uid_validity=200", "committed=51", "bootstrap=true") {
		t.Fatalf("Sent status code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestMailFlagMutationsUseOneLocationAndPersistServerReturnedState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_MAIL_FLAG_PASSWORD", "synthetic-mail-flag-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	mailClient := &fakeInboxClient{results: []fakeInboxPageResult{{
		page: transport.InboxPage{
			Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 70, LastScannedUID: 70,
			Bootstrap: true, Messages: []transport.FetchedMessage{{
				UID: 70, Flags: []string{"$Custom"}, ModSequence: 10,
				InternalDate: time.Date(2026, 8, 6, 4, 40, 0, 0, time.UTC),
			}},
		},
		raw: map[uint32]string{70: syntheticMailFixture(70, "fixture-70@example.test", "Flag fixture", "State fixture", "")},
	}}}
	commands.mail = mailClient
	flagClient := &fakeFlagClient{results: []transport.FlagMutationResult{
		{Mailbox: "INBOX", UIDValidity: 100, UID: 70, Flags: []string{`\Seen`, "$Custom"}, ModSequence: 11, Present: true},
		{Mailbox: "INBOX", UIDValidity: 100, UID: 70, Flags: []string{`\Seen`, `\Flagged`, "$Custom"}, ModSequence: 12, Present: true},
		{Mailbox: "INBOX", UIDValidity: 100, UID: 70, Flags: []string{`\Flagged`, "$Custom"}, ModSequence: 13, Present: true},
		{Mailbox: "INBOX", UIDValidity: 100, UID: 70, Flags: []string{"$Custom"}, ModSequence: 14, Present: true},
		{Mailbox: "INBOX", UIDValidity: 100, UID: 70, Flags: []string{"$Custom"}, ModSequence: 15, Present: true},
	}}
	commands.flags = flagClient
	addAndDiscoverInbox(t, commands, home, "ICLOUD_MAIL_FLAG_PASSWORD")
	if code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync"); code != 0 || stderr != "" {
		t.Fatalf("sync code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "inbox", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("inbox code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var inboxEnvelope struct {
		Data struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &inboxEnvelope); err != nil || len(inboxEnvelope.Data.Messages) != 1 {
		t.Fatalf("inbox envelope=%+v err=%v", inboxEnvelope, err)
	}
	messageID := inboxEnvelope.Data.Messages[0].ID

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "mark-read", messageID, "--folder", "INBOX")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "mail-state\n", "action=mark_read", "unread=false", "flagged=false") {
		t.Fatalf("mark-read code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "flag", messageID, "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"action":"flag"`) || !strings.Contains(stdout, `"flagged":true`) || !strings.Contains(stdout, `"unknown":["$Custom"]`) {
		t.Fatalf("flag code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "mark-unread", messageID)
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "action=mark_unread", "unread=true", "flagged=true") {
		t.Fatalf("mark-unread code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "unflag", messageID)
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "action=unflag", "unread=true", "flagged=false") {
		t.Fatalf("unflag code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(flagClient.requests) != 4 {
		t.Fatalf("flag requests = %+v", flagClient.requests)
	}
	for index, request := range flagClient.requests {
		if request.Mailbox != "INBOX" || request.ExpectedUIDValidity != 100 || request.UID != 70 || request.ExpectedModSequence != uint64(10+index) {
			t.Fatalf("request %d = %+v", index, request)
		}
	}
	if flagClient.requests[0].Flag != `\Seen` || !flagClient.requests[0].Add ||
		flagClient.requests[1].Flag != `\Flagged` || !flagClient.requests[1].Add ||
		flagClient.requests[2].Flag != `\Seen` || flagClient.requests[2].Add ||
		flagClient.requests[3].Flag != `\Flagged` || flagClient.requests[3].Add {
		t.Fatalf("mutation operations = %+v", flagClient.requests)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID, "--metadata-only", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"mod_sequence":14`) ||
		!strings.Contains(stdout, `"unknown":["$Custom"]`) || strings.Contains(stdout, "synthetic-mail-flag-secret") {
		t.Fatalf("stored state code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "mark-read", messageID, "--json")
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "changed concurrently") || !strings.Contains(stderr, `"mod_sequence":15`) {
		t.Fatalf("concurrent state code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

type staticFolderResolver map[string]folder.Folder

func (resolver staticFolderResolver) Resolve(_ context.Context, _ string, selector string) (folder.Folder, error) {
	for _, value := range resolver {
		if selector == value.ID || selector == value.Name || selector == value.WireName || selector == string(value.Kind) {
			return value, nil
		}
	}
	return folder.Folder{}, folder.ErrNotFound
}

func TestResolveMutationLocationRequiresUnambiguousEligibleLocation(t *testing.T) {
	resolver := staticFolderResolver{
		"inbox":   {ID: "fld_inbox", Name: "INBOX", WireName: "INBOX", Kind: folder.KindInbox, Present: true, Selectable: true},
		"archive": {ID: "fld_archive", Name: "Archive", WireName: "Archive", Kind: folder.KindArchive, Present: true, Selectable: true},
	}
	message := maildomain.Message{Locations: []maildomain.Location{
		{ID: "loc_inbox", FolderID: "fld_inbox", Present: true},
		{ID: "loc_archive", FolderID: "fld_archive", Present: true},
	}}
	_, _, err := resolveMutationLocation(context.Background(), resolver, "acct_fixture", message, "")
	var appErr *cli.AppError
	if !errors.As(err, &appErr) || appErr.Code != cli.ErrorConflict || !errors.Is(err, maildomain.ErrLocationAmbiguous) {
		t.Fatalf("ambiguous location error = %#v", err)
	}
	folderValue, location, err := resolveMutationLocation(context.Background(), resolver, "acct_fixture", message, "Archive")
	if err != nil || folderValue.ID != "fld_archive" || location.ID != "loc_archive" {
		t.Fatalf("selected folder=%+v location=%+v err=%v", folderValue, location, err)
	}
	message.Locations[1].Flags.Deleted = true
	folderValue, location, err = resolveMutationLocation(context.Background(), resolver, "acct_fixture", message, "")
	if err != nil || folderValue.ID != "fld_inbox" || location.ID != "loc_inbox" {
		t.Fatalf("eligible default folder=%+v location=%+v err=%v", folderValue, location, err)
	}
}

func TestMailSyncFailuresNeverAdvanceAndRetryIsIdempotent(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_MAIL_FAILURE_PASSWORD", "synthetic-mail-failure-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	mailClient := &fakeInboxClient{results: []fakeInboxPageResult{
		{err: transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, true, errors.New("synthetic fetch interruption"))},
		{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 24, LastScannedUID: 24, Bootstrap: true, Messages: []transport.FetchedMessage{{UID: 24, InternalDate: time.Date(2026, 8, 6, 4, 24, 0, 0, time.UTC)}}}, raw: map[uint32]string{24: syntheticMailFixture(24, "fixture-24@example.test", "Retry fixture", "Retry compact preview", "")}},
	}}
	commands.mail = mailClient
	addAndDiscoverInbox(t, commands, home, "ICLOUD_MAIL_FAILURE_PASSWORD")

	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync")
	if exitCode != cli.ExitUnavailable || stdout != "" || !strings.Contains(stderr, "imap_fetch") {
		t.Fatalf("failure code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "status")
	if exitCode != 0 || stderr != "" || !compactContainsAll(stdout, "state=failed", "committed=0", "scanned=0", "last_error=imap_fetch") {
		t.Fatalf("failed status code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync")
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "created=1") || !strings.Contains(stdout, "last_uid=24") {
		t.Fatalf("retry code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if len(mailClient.requests) != 2 || !mailClient.requests[1].Bootstrap || mailClient.requests[1].LastCommittedUID != 0 {
		t.Fatalf("retry request = %+v", mailClient.requests)
	}
}

func TestMailSyncParseBlobAndUIDValidityFailuresPreserveFrontier(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*accountCommands, *fakeInboxClient)
		result    fakeInboxPageResult
		wantCode  int
		wantState string
		wantError string
	}{
		{name: "parse", result: fakeInboxPageResult{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 30, LastScannedUID: 30, Bootstrap: true, Messages: []transport.FetchedMessage{{UID: 30}}}, raw: map[uint32]string{30: "\x00"}}, wantCode: cli.ExitUnavailable, wantState: "failed", wantError: "message_parse"},
		{name: "blob", configure: func(commands *accountCommands, _ *fakeInboxClient) {
			commands.blobs = func(config.AppPaths) blob.Stager { return failingBlobStager{err: errors.New("synthetic blob failure")} }
		}, result: fakeInboxPageResult{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 31, LastScannedUID: 31, Bootstrap: true, Messages: []transport.FetchedMessage{{UID: 31}}}, raw: map[uint32]string{31: syntheticMailFixture(31, "fixture-31@example.test", "Blob fixture", "Blob preview", "")}}, wantCode: cli.ExitUnavailable, wantState: "failed", wantError: "imap_fetch"},
		{name: "attachment-blob", configure: func(commands *accountCommands, _ *fakeInboxClient) {
			commands.attachmentBlobs = func(config.AppPaths) attachmentBlobStore {
				return failingAttachmentBlobStore{err: errors.New("synthetic attachment blob failure")}
			}
		}, result: fakeInboxPageResult{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 32, LastScannedUID: 32, Bootstrap: true, Messages: []transport.FetchedMessage{{UID: 32}}}, raw: map[uint32]string{32: strings.Join([]string{
			"From: sender@example.test", "To: owner@icloud.com", "Message-ID: <fixture-32@example.test>",
			"Content-Type: application/octet-stream", "Content-Disposition: attachment; filename=fixture.bin", "", "bytes",
		}, "\r\n")}}, wantCode: cli.ExitUnavailable, wantState: "failed", wantError: "attachment_blob_stage"},
		{name: "uid-validity", result: fakeInboxPageResult{err: &transport.UIDValidityError{Expected: 100, Observed: 200}}, wantCode: cli.ExitConflict, wantState: "repair_required", wantError: "uid_validity_changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "state")
			envName := "ICLOUD_MAIL_" + strings.ToUpper(strings.ReplaceAll(test.name, "-", "_")) + "_PASSWORD"
			t.Setenv(envName, "synthetic-secret")
			commands, _ := newTestAccountCommands()
			commands.folders = oneInboxFolderClient()
			client := &fakeInboxClient{}
			if test.name == "uid-validity" {
				client.results = []fakeInboxPageResult{{page: transport.InboxPage{Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 20, LastScannedUID: 20, Bootstrap: true}}, test.result}
			} else {
				client.results = []fakeInboxPageResult{test.result}
			}
			commands.mail = client
			if test.configure != nil {
				test.configure(&commands, client)
			}
			addAndDiscoverInbox(t, commands, home, envName)
			if test.name == "uid-validity" {
				exitCode, _, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync")
				if exitCode != 0 || stderr != "" {
					t.Fatalf("baseline sync code=%d stderr=%q", exitCode, stderr)
				}
			}
			exitCode, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync")
			if exitCode != test.wantCode || stdout != "" || stderr == "" {
				t.Fatalf("failure code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
			}
			exitCode, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "status")
			if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "state="+test.wantState) || !strings.Contains(stdout, "last_error="+test.wantError) {
				t.Fatalf("status code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
			}
			if test.name == "uid-validity" {
				if !compactContainsAll(stdout, "committed=20", "scanned=20", "uid_validity=200") {
					t.Fatalf("repair frontier changed: %q", stdout)
				}
				priorCalls := client.calls
				exitCode, _, _ = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync")
				if exitCode != cli.ExitConflict || client.calls != priorCalls {
					t.Fatalf("repair guard code=%d calls=%d prior=%d", exitCode, client.calls, priorCalls)
				}
			} else if !compactContainsAll(stdout, "committed=0", "scanned=0") {
				t.Fatalf("failure advanced frontier: %q", stdout)
			}
		})
	}
}

func TestMailMessageContentWorkflowRawRoundTripAndPrivacy(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	t.Setenv("ICLOUD_MAIL_CONTENT_PASSWORD", "synthetic-content-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	raw, plainBody, htmlBody := multipartMessageFixture()
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{
		page: transport.InboxPage{
			Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 61, LastScannedUID: 61, Bootstrap: true,
			Messages: []transport.FetchedMessage{{UID: 61, Flags: []string{`\Flagged`}, InternalDate: time.Date(2026, 8, 6, 5, 31, 0, 0, time.UTC)}},
		},
		raw: map[uint32]string{61: raw},
	}}}
	addAndDiscoverInbox(t, commands, home, "ICLOUD_MAIL_CONTENT_PASSWORD")
	if code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync"); code != 0 || stderr != "" {
		t.Fatalf("sync code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "inbox", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("inbox JSON code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var inboxEnvelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &inboxEnvelope); err != nil || !inboxEnvelope.OK || len(inboxEnvelope.Data.Messages) != 1 {
		t.Fatalf("inbox envelope=%+v err=%v", inboxEnvelope, err)
	}
	messageID := inboxEnvelope.Data.Messages[0].ID

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID)
	if code != 0 || stderr != "" {
		t.Fatalf("show code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, want := range []string{
		"message\n", messageID + " account=acct_", `from="Fixture Sender <sender@example.test>"`,
		"to=owner@icloud.com", `subject="Phase 7 content fixture"`, "flagged=true",
		"attachments=1", "content=text,html", "raw=true", "parts=5", "excerpt=",
		"hint=\"use --full, --format html, --headers, or message raw\"",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("default show missing %q:\n%s", want, stdout)
		}
	}
	for _, forbidden := range []string{plainBody, htmlBody, "hidden-bcc@example.test", "raw/sha256", `"text_body"`} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("default show exposed %q: %s", forbidden, stdout)
		}
	}
	if lines, size := strings.Count(stdout, "\n"), len(stdout); lines > 22 || size > 1600 {
		t.Fatalf("default show budget lines=%d bytes=%d\n%s", lines, size, stdout)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID, "--metadata-only")
	if code != 0 || stderr != "" || strings.Contains(stdout, "excerpt=") || strings.Contains(stdout, "hint=") {
		t.Fatalf("metadata-only code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID, "--full")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "body\n"+plainBody) || strings.Contains(stdout, htmlBody) {
		t.Fatalf("full text code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID, "--format", "text", "--body-only")
	if code != 0 || stderr != "" || stdout != plainBody {
		t.Fatalf("text body-only code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID, "--format", "html", "--body-only")
	if code != 0 || stderr != "" || stdout != htmlBody {
		t.Fatalf("HTML body-only code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID, "--headers")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "headers\n") || !strings.Contains(stdout, "Message-Id: <phase7-content@example.test>") || strings.Contains(strings.ToLower(stdout), "bcc:") {
		t.Fatalf("headers code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID, "--verbose")
	if code != 0 || stderr != "" || compactBlockCount(stdout, "part") != 5 || !strings.Contains(stdout, "filename=fixture.txt") || !strings.Contains(stdout, "attachment=true") {
		t.Fatalf("verbose code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "attachments", "list", messageID)
	if code != 0 || stderr != "" || compactBlockCount(stdout, "attachment") != 1 ||
		!compactContainsAll(stdout, "filename=fixture.txt", "type=text/plain", "bytes=16", "inline=false", "availability=available", "attachments\n", "count=1", "more=false") {
		t.Fatalf("attachment list code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "attachments", "list", messageID, "--json")
	if code != 0 || stderr != "" || strings.Contains(stdout, "storage_key") || strings.Contains(stdout, "attachments/sha256/") {
		t.Fatalf("attachment JSON code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var attachmentEnvelope struct {
		Data struct {
			Attachments []struct {
				ID          string `json:"id"`
				Filename    string `json:"filename"`
				ContentHash string `json:"content_hash"`
				ByteSize    int64  `json:"byte_size"`
			} `json:"attachments"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &attachmentEnvelope); err != nil || len(attachmentEnvelope.Data.Attachments) != 1 {
		t.Fatalf("attachment envelope=%+v err=%v", attachmentEnvelope, err)
	}
	attachmentID := attachmentEnvelope.Data.Attachments[0].ID
	if attachmentID == "" || attachmentEnvelope.Data.Attachments[0].Filename != "fixture.txt" || len(attachmentEnvelope.Data.Attachments[0].ContentHash) != 64 || attachmentEnvelope.Data.Attachments[0].ByteSize != 16 {
		t.Fatalf("attachment metadata = %+v", attachmentEnvelope.Data.Attachments[0])
	}
	code, _, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "attachments", "save", attachmentID, "--output", "-")
	if code != cli.ExitSafety || !strings.Contains(stderr, "binary stdout requires --binary") {
		t.Fatalf("binary guard code=%d stderr=%q", code, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "attachments", "save", attachmentID, "--output", "-", "--binary")
	if code != 0 || stderr != "" || stdout != "attachment bytes" {
		t.Fatalf("attachment stdout code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	attachmentPath := filepath.Join(t.TempDir(), "fixture.txt")
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "attachments", "save", attachmentID, "--output", attachmentPath)
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "attachment-save\n", attachmentID, "bytes=16") || strings.Contains(stdout, attachmentPath) {
		t.Fatalf("attachment save code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	attachmentBytes, err := os.ReadFile(attachmentPath)
	if err != nil || string(attachmentBytes) != "attachment bytes" {
		t.Fatalf("attachment file=%q err=%v", attachmentBytes, err)
	}
	if err := os.WriteFile(attachmentPath, []byte("changed export"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "attachments", "save", attachmentID, "--output", "-", "--binary")
	if code != 0 || stderr != "" || stdout != "attachment bytes" {
		t.Fatalf("canonical attachment changed after export mutation code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "attachments", "save", attachmentID, "--output", attachmentPath)
	if code != cli.ExitConflict || !strings.Contains(stderr, "already exists") {
		t.Fatalf("attachment conflict code=%d stderr=%q", code, stderr)
	}
	batchDir := filepath.Join(t.TempDir(), "saved")
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "attachments", "save-all", messageID, "--dir", batchDir)
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "attachments-saved\n", "count=1") {
		t.Fatalf("save-all code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	attachmentBytes, err = os.ReadFile(filepath.Join(batchDir, "fixture.txt"))
	if err != nil || string(attachmentBytes) != "attachment bytes" {
		t.Fatalf("save-all file=%q err=%v", attachmentBytes, err)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID, "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"text_available":true`) || !strings.Contains(stdout, `"html_available":true`) || !strings.Contains(stdout, `"raw_available":true`) || strings.Contains(stdout, `"text_body"`) || strings.Contains(stdout, `"raw_base64"`) || strings.Contains(stdout, `"headers":[{`) || strings.Contains(stdout, "hidden-bcc") {
		t.Fatalf("default JSON code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID, "--full", "--headers", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"text_body":`) || !strings.Contains(stdout, `"headers":[{`) || strings.Contains(stdout, "hidden-bcc") {
		t.Fatalf("content JSON code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var contentEnvelope struct {
		Data struct {
			Content struct {
				TextBody string `json:"text_body"`
				Headers  []struct {
					Key string `json:"key"`
				} `json:"headers"`
			} `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &contentEnvelope); err != nil || contentEnvelope.Data.Content.TextBody != plainBody || len(contentEnvelope.Data.Content.Headers) == 0 {
		t.Fatalf("content envelope=%+v err=%v", contentEnvelope, err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID, "--format", "raw", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("raw JSON code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var rawEnvelope struct {
		Data struct {
			Content struct {
				RawBase64 string `json:"raw_base64"`
			} `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &rawEnvelope); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(rawEnvelope.Data.Content.RawBase64)
	if err != nil || string(decoded) != raw {
		t.Fatalf("raw JSON round trip err=%v len=%d", err, len(decoded))
	}

	for _, args := range [][]string{
		{"mail", "message", "raw", messageID},
		{"mail", "message", "show", messageID, "--format", "raw"},
		{"mail", "message", "export", messageID, "--output", "-"},
	} {
		commandArgs := append([]string{"--home", home}, args...)
		code, stdout, stderr = runWithAccountCommands(t, commands, "", commandArgs...)
		if code != 0 || stderr != "" || stdout != raw {
			t.Fatalf("raw command %v code=%d bytes=%d stderr=%q", args, code, len(stdout), stderr)
		}
	}

	exportPath := filepath.Join(t.TempDir(), "message.eml")
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "export", messageID, "--output", exportPath)
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "message-export\n", messageID+" bytes=") {
		t.Fatalf("export code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	exported, err := os.ReadFile(exportPath)
	if err != nil || string(exported) != raw {
		t.Fatalf("exported err=%v len=%d", err, len(exported))
	}
	info, err := os.Stat(exportPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("export mode info=%v err=%v", info, err)
	}
	code, _, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "export", messageID, "--output", exportPath)
	if code != cli.ExitConflict || !strings.Contains(stderr, "already exists") {
		t.Fatalf("export conflict code=%d stderr=%q", code, stderr)
	}
	if err := os.WriteFile(exportPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "export", messageID, "--output", exportPath, "--force")
	if code != 0 || stderr != "" {
		t.Fatalf("force export code=%d stderr=%q", code, stderr)
	}
	exported, err = os.ReadFile(exportPath)
	if err != nil || string(exported) != raw {
		t.Fatalf("forced export err=%v len=%d", err, len(exported))
	}

	code, _, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", "msg_0198d704-6c00-7000-8000-000000999999")
	if code != cli.ExitNotFound || !strings.Contains(stderr, "message not found") {
		t.Fatalf("missing message code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID, "--headers", "--body-only")
	if code != cli.ExitUsage || !strings.Contains(stderr, "cannot be used together") {
		t.Fatalf("invalid output combination code=%d stderr=%q", code, stderr)
	}

	legacyDB, err := sql.Open("sqlite", filepath.Join(home, "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyDB.Exec(`DELETE FROM message_content WHERE message_id = ?`, messageID); err != nil {
		legacyDB.Close()
		t.Fatal(err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", messageID, "--verbose")
	if code != 0 || stderr != "" || compactBlockCount(stdout, "part") != 5 || !strings.Contains(stdout, "content=text,html") || !strings.Contains(stdout, "part\nprt_") {
		t.Fatalf("legacy lazy content code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	legacyDB, err = sql.Open("sqlite", filepath.Join(home, "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	var persistedParts int
	if err := legacyDB.QueryRow(`SELECT count(*) FROM message_parts WHERE message_id = ? AND part_id LIKE 'prt_%'`, messageID).Scan(&persistedParts); err != nil {
		legacyDB.Close()
		t.Fatal(err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatal(err)
	}
	if persistedParts != 5 {
		t.Fatalf("lazy UUIDv7 MIME parts = %d", persistedParts)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "attachments", "list", messageID, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("legacy attachment list code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var rehydratedEnvelope struct {
		Data struct {
			Attachments []struct {
				ID string `json:"id"`
			} `json:"attachments"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &rehydratedEnvelope); err != nil || len(rehydratedEnvelope.Data.Attachments) != 1 || rehydratedEnvelope.Data.Attachments[0].ID != attachmentID {
		t.Fatalf("rehydrated attachment IDs=%+v err=%v", rehydratedEnvelope, err)
	}

	var rawPath string
	err = filepath.WalkDir(filepath.Join(home, "blobs", "raw"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".eml") {
			rawPath = path
		}
		return nil
	})
	if err != nil || rawPath == "" {
		t.Fatalf("raw path=%q err=%v", rawPath, err)
	}
	corrupt := []byte(raw)
	corrupt[len(corrupt)-1] ^= 0x7f
	if err := os.WriteFile(rawPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "raw", messageID)
	if code != cli.ExitUnavailable || stdout != "" || !strings.Contains(stderr, "local raw message content is corrupt") {
		t.Fatalf("corrupt raw code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func addAndDiscoverInbox(t *testing.T, commands accountCommands, home, envName string) {
	t.Helper()
	exitCode, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home,
		"account", "add", "--name", "Personal", "--email", "owner@icloud.com", "--credential-env", envName)
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "pending_validation") {
		t.Fatalf("add code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	exitCode, _, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "status")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("folder status code=%d stderr=%q", exitCode, stderr)
	}
}

func oneInboxFolderClient() *fakeFolderClient {
	return &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {{Folders: []transport.RemoteFolder{{
			Name: "INBOX", WireName: "INBOX", Kind: "inbox", KindSource: "inbox", Selectable: true,
			Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: "INBOX", Messages: 1, UIDNext: 21, UIDValidity: 100},
		}}}},
	}}
}

func syntheticMailFixture(uid uint32, messageID, subject, body, bcc string) string {
	lines := []string{
		"From: Fixture Sender <sender@example.test>",
		"To: owner@icloud.com",
		"Date: Wed, 05 Aug 2026 20:00:00 +0000",
		"Message-ID: <" + messageID + ">",
		fmt.Sprintf("Subject: %s", subject),
		"Content-Type: text/plain; charset=utf-8",
	}
	if bcc != "" {
		lines = append(lines, "Bcc: "+bcc)
	}
	lines = append(lines, "", body+fmt.Sprintf(" uid=%d", uid))
	return strings.Join(lines, "\r\n")
}

func syntheticThreadMailFixture(uid uint32, messageID, subject, body, from, to string, references []string, inReplyTo, bcc string, date time.Time) string {
	lines := []string{
		"From: " + from,
		"To: " + to,
		"Date: " + date.UTC().Format(time.RFC1123Z),
		"Message-ID: <" + messageID + ">",
		fmt.Sprintf("Subject: %s", subject),
		"Content-Type: text/plain; charset=utf-8",
	}
	if len(references) > 0 {
		values := make([]string, 0, len(references))
		for _, reference := range references {
			values = append(values, "<"+reference+">")
		}
		lines = append(lines, "References: "+strings.Join(values, " "))
	}
	if inReplyTo != "" {
		lines = append(lines, "In-Reply-To: <"+inReplyTo+">")
	}
	if bcc != "" {
		lines = append(lines, "Bcc: "+bcc)
	}
	lines = append(lines, "", body+fmt.Sprintf(" uid=%d", uid))
	return strings.Join(lines, "\r\n")
}

func multipartMessageFixture() (raw, plainBody, htmlBody string) {
	plainBody = "Complete plain text body.\r\nSecond line for explicit full output."
	htmlBody = "<p>Complete <strong>HTML</strong> body.</p>"
	lines := []string{
		"From: Fixture Sender <sender@example.test>",
		"To: owner@icloud.com",
		"Cc: copy@example.test",
		"Bcc: hidden-bcc@example.test",
		"Date: Thu, 06 Aug 2026 05:30:00 +0000",
		"Message-ID: <phase7-content@example.test>",
		"Subject: Phase 7 content fixture",
		"Content-Type: multipart/mixed; boundary=mixed",
		"",
		"--mixed",
		"Content-Type: multipart/alternative; boundary=alternative",
		"",
		"--alternative",
		"Content-Type: text/plain; charset=utf-8",
		"",
		plainBody,
		"--alternative",
		"Content-Type: text/html; charset=utf-8",
		"",
		htmlBody,
		"--alternative--",
		"--mixed",
		"Content-Type: text/plain; charset=utf-8; name=fixture.txt",
		"Content-Disposition: attachment; filename=fixture.txt",
		"",
		"attachment bytes",
		"--mixed--",
		"",
	}
	return strings.Join(lines, "\r\n"), plainBody, htmlBody
}
