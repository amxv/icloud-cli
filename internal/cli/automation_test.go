package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type bulkStateMailService struct {
	unavailableMailService
	calls []string
	fail  map[string]error
}

type sendJSONMailService struct {
	unavailableMailService
	request   maildomain.SendRequest
	sendCalls int
}

func (service *sendJSONMailService) MailSendPreview(_ context.Context, _, _ string, request maildomain.SendRequest) (account.Account, maildomain.SendPreview, int64, error) {
	service.request = request
	return account.Account{ID: "acct_test"}, maildomain.SendPreview{
		Kind: maildomain.OutboundKindSend, AccountID: "acct_test", From: maildomain.Address{Email: "owner@example.test"},
		To: request.To, CC: request.CC, BCC: request.BCC, ReplyTo: request.ReplyTo, Subject: request.Subject,
		TextBody: request.TextBody, HTMLBody: request.HTMLBody, Headers: request.Headers,
		BodyParts: 1, RequestHash: "sha256:test",
	}, 18, nil
}

func (service *sendJSONMailService) MailSend(_ context.Context, _, _ string, request maildomain.SendRequest) (account.Account, maildomain.OutboundAttempt, bool, int64, error) {
	service.sendCalls++
	service.request = request
	return account.Account{ID: "acct_test"}, maildomain.OutboundAttempt{ID: "send_test", Status: maildomain.OutboundSent}, false, 18, nil
}

type addJSONAccountService struct {
	unavailableAccountService
	input account.AddInput
	calls int
}

func (service *addJSONAccountService) Add(_ context.Context, _ string, input account.AddInput) (account.Account, int64, error) {
	service.calls++
	service.input = input
	return account.Account{ID: "acct_json", Name: input.Name, Email: input.Email, IMAPUsername: input.IMAPUsername, Default: true, CredentialScheme: string(input.Credential.Scheme)}, 18, nil
}

func (service *bulkStateMailService) MailUpdateFlag(_ context.Context, _, _, messageID, folderSelector string, mutation maildomain.FlagMutation) (account.Account, folder.Folder, maildomain.Message, maildomain.Location, int64, error) {
	service.calls = append(service.calls, messageID+"@"+folderSelector)
	if err := service.fail[messageID]; err != nil {
		return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, 0, err
	}
	flags := maildomain.LocationFlags{Seen: mutation != maildomain.FlagMutationMarkUnread, Flagged: mutation == maildomain.FlagMutationFlag}
	when := time.Date(2026, 8, 7, 3, 0, 0, 0, time.UTC)
	location := maildomain.Location{ID: "loc_" + strings.TrimPrefix(messageID, "msg_"), FolderID: "fld_test", UIDValidity: 1, UID: uint32(len(service.calls)), Flags: flags, FlagsCheckedAt: &when, Present: true}
	message := maildomain.Message{ID: messageID, Unread: !flags.Seen, Flagged: flags.Flagged}
	return account.Account{ID: "acct_test"}, folder.Folder{ID: "fld_test", Name: "Inbox"}, message, location, 18, nil
}

func executeWithMail(t *testing.T, service MailService, stdin string, args ...string) (Result, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	result := Execute(context.Background(), args, Config{
		Version: "test", SchemaVersion: 18, Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr, Mail: service,
		RequestIDGen: func() string { return "req_automation" },
	})
	return result, stdout.String(), stderr.String()
}

func TestBulkStateWriteAcceptsStrictJSONFromStdin(t *testing.T) {
	service := &bulkStateMailService{}
	input := `{"message_ids":["msg_one","msg_two"],"folder":"inbox"}`
	result, stdout, stderr := executeWithMail(t, service, input, "mail", "mark-read", "--input-json", "-", "--json")
	if result.ExitCode != 0 || stderr != "" {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	if got := strings.Join(service.calls, ","); got != "msg_one@inbox,msg_two@inbox" {
		t.Fatalf("calls=%q", got)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Mode    string `json:"mode"`
			Count   int    `json:"count"`
			Failed  int    `json:"failed"`
			Partial bool   `json:"partial"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil || !envelope.OK || envelope.Data.Mode != "atomic" || envelope.Data.Count != 2 || envelope.Data.Failed != 0 || envelope.Data.Partial {
		t.Fatalf("envelope=%+v err=%v raw=%q", envelope, err, stdout)
	}
}

func TestSendDryRunAcceptsJSONStdinThroughNormalValidationPath(t *testing.T) {
	service := &sendJSONMailService{}
	input := `{"to":["Client <client@example.test>"],"bcc":["hidden@example.test"],"subject":"JSON request","body":"hello","headers":["X-Test: automation"],"idempotency_key":"send-json-01"}`
	result, stdout, stderr := executeWithMail(t, service, input, "mail", "send", "--input-json", "-", "--dry-run", "--json")
	if result.ExitCode != 0 || stderr != "" {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	if len(service.request.To) != 1 || service.request.To[0].Email != "client@example.test" || len(service.request.BCC) != 1 || service.request.Subject != "JSON request" || service.request.TextBody != "hello" || service.request.IdempotencyKey != "send-json-01" {
		t.Fatalf("request=%+v", service.request)
	}
	if !strings.Contains(stdout, "client@example.test") || !strings.Contains(stdout, "hidden@example.test") || !strings.Contains(stdout, "JSON request") {
		t.Fatalf("dry-run JSON should remain explicit private review content: %q", stdout)
	}
}

func TestSendJSONStdinStillRequiresNonInteractiveYes(t *testing.T) {
	service := &sendJSONMailService{}
	input := `{"to":["client@example.test"],"subject":"JSON request","body":"hello","idempotency_key":"send-json-02"}`
	result, stdout, stderr := executeWithMail(t, service, input, "mail", "send", "--input-json", "-")
	if result.ExitCode != ExitSafety || stdout != "" || service.sendCalls != 0 || !strings.Contains(stderr, "requires --yes") {
		t.Fatalf("result=%+v stdout=%q stderr=%q calls=%d", result, stdout, stderr, service.sendCalls)
	}
}

func TestAccountAddAcceptsJSONStdinWithoutEmbeddingCredentialSecret(t *testing.T) {
	accounts := &addJSONAccountService{}
	var stdout, stderr bytes.Buffer
	input := `{"name":"Automation","email":"owner@icloud.com","imap_username":"owner","default":true,"credential_env":"ICLOUD_AUTOMATION_PASSWORD"}`
	result := Execute(context.Background(), []string{"account", "add", "--input-json", "-", "--json"}, Config{Version: "test", SchemaVersion: 18, Stdin: strings.NewReader(input), Stdout: &stdout, Stderr: &stderr, Accounts: accounts})
	if result.ExitCode != 0 || stderr.Len() != 0 || accounts.calls != 1 {
		t.Fatalf("result=%+v calls=%d stdout=%q stderr=%q", result, accounts.calls, stdout.String(), stderr.String())
	}
	if accounts.input.Name != "Automation" || accounts.input.Email != "owner@icloud.com" || accounts.input.IMAPUsername != "owner" || accounts.input.Credential.Scheme != account.CredentialEnv || accounts.input.Credential.Reference != "ICLOUD_AUTOMATION_PASSWORD" || len(accounts.input.Credential.Secret) != 0 {
		t.Fatalf("input=%+v", accounts.input)
	}
	if strings.Contains(stdout.String(), "ICLOUD_AUTOMATION_PASSWORD") || strings.Contains(stdout.String(), "owner@icloud.com") {
		t.Fatalf("mutation output leaked submitted reference/address: %q", stdout.String())
	}
}

func TestStrictJSONInputRejectsUnknownAndMixedWriteFields(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		args  []string
	}{
		{"unknown", `{"message_ids":["msg_one"],"mystery":true}`, []string{"mail", "mark-read", "--input-json", "-"}},
		{"mixed", `{"message_ids":["msg_one"],"folder":"inbox"}`, []string{"mail", "mark-read", "--input-json", "-", "--folder", "inbox"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &bulkStateMailService{}
			result, stdout, stderr := executeWithMail(t, service, test.input, test.args...)
			if result.ExitCode != ExitUsage || stdout != "" || len(service.calls) != 0 || !strings.Contains(stderr, "code=usage") {
				t.Fatalf("result=%+v stdout=%q stderr=%q calls=%v", result, stdout, stderr, service.calls)
			}
		})
	}
}

func TestStrictJSONInputSizeBoundRejectsBeforeProviderCalls(t *testing.T) {
	service := &bulkStateMailService{}
	input := `{"message_ids":["msg_one"],"padding":"` + strings.Repeat("x", MaximumJSONInputBytes) + `"}`
	result, stdout, stderr := executeWithMail(t, service, input, "mail", "mark-read", "--input-json", "-")
	if result.ExitCode != ExitSafety || stdout != "" || len(service.calls) != 0 || !strings.Contains(stderr, "exceeds") {
		t.Fatalf("result=%+v stdout=%q stderr=%q calls=%v", result, stdout, stderr, service.calls)
	}
}

func TestJSONInputExplicitZeroLimitUsesSameValidationAsFlags(t *testing.T) {
	service := &bulkStateMailService{}
	result, stdout, stderr := executeWithMail(t, service, `{"limit":0}`, "mail", "sync", "--input-json", "-")
	if result.ExitCode != ExitUsage || stdout != "" || !strings.Contains(stderr, "--limit must be between 1 and 100") {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
}

func TestFolderAdminJSONRejectsFieldsFromAnotherAction(t *testing.T) {
	var stdout, stderr bytes.Buffer
	result := Execute(context.Background(), []string{"mail", "folders", "create", "--input-json", "-", "--dry-run"}, Config{
		Version: "test", SchemaVersion: 18, Stdin: strings.NewReader(`{"name":"Projects","folder":"Inbox"}`), Stdout: &stdout, Stderr: &stderr,
	})
	if result.ExitCode != ExitUsage || stdout.Len() != 0 || !strings.Contains(stderr.String(), "invalid --input-json request") {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout.String(), stderr.String())
	}
}

func TestBulkStateContinueOnErrorJSONLReportsEveryItemAndPartial(t *testing.T) {
	service := &bulkStateMailService{fail: map[string]error{"msg_bad": NewAppError(ErrorUnavailable, "synthetic provider failure", true)}}
	input := strings.Join([]string{
		`{"message_id":"msg_one","folder":"inbox"}`,
		`{"message_id":"msg_bad","folder":"inbox"}`,
		`{"message_id":"msg_three","folder":"inbox"}`,
	}, "\n") + "\n"
	result, stdout, stderr := executeWithMail(t, service, input, "mail", "flag", "--input-jsonl", "-", "--mode", "continue-on-error", "--jsonl")
	if result.ExitCode != ExitUnavailable || len(service.calls) != 3 {
		t.Fatalf("result=%+v calls=%v stdout=%q stderr=%q", result, service.calls, stdout, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 4 {
		t.Fatalf("stdout lines=%d: %q", len(lines), stdout)
	}
	var completed JSONLEvent
	if err := json.Unmarshal([]byte(lines[3]), &completed); err != nil || completed.Event != "completed" {
		t.Fatalf("completed=%+v err=%v raw=%q", completed, err, lines[3])
	}
	payload, ok := completed.Payload.(map[string]any)
	if !ok || payload["mode"] != "continue-on-error" || payload["partial"] != true || int(payload["count"].(float64)) != 2 || int(payload["failed"].(float64)) != 1 {
		t.Fatalf("completed payload=%#v", completed.Payload)
	}
	if !strings.Contains(stderr, `"event":"error"`) || !strings.Contains(stderr, `"partial":true`) {
		t.Fatalf("stderr did not preserve typed terminal error/partial detail: %q", stderr)
	}
}

func TestBulkStateAtomicFailureStopsAndSaysPartial(t *testing.T) {
	service := &bulkStateMailService{fail: map[string]error{"msg_bad": NewAppError(ErrorUnavailable, "synthetic provider failure", true)}}
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "mark-unread", "msg_one", "msg_bad", "msg_three", "--mode", "atomic")
	if result.ExitCode != ExitUnavailable || len(service.calls) != 2 {
		t.Fatalf("result=%+v calls=%v stdout=%q stderr=%q", result, service.calls, stdout, stderr)
	}
	for _, want := range []string{"mail-state", "mail-state-error", "mode=atomic", "updated=1", "failed=1", "partial=true"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q: %q", want, stdout)
		}
	}
	if !strings.Contains(stderr, "partially succeeded") {
		t.Fatalf("stderr=%q", stderr)
	}
}

func TestBulkStateBoundRejectsBeforeProviderCalls(t *testing.T) {
	service := &bulkStateMailService{}
	args := []string{"mail", "flag"}
	for index := 0; index < MaximumBulkItems+1; index++ {
		args = append(args, fmt.Sprintf("msg_%03d", index))
	}
	result, stdout, stderr := executeWithMail(t, service, "", args...)
	if result.ExitCode != ExitUsage || stdout != "" || len(service.calls) != 0 || !strings.Contains(stderr, "between 1 and 100") {
		t.Fatalf("result=%+v stdout=%q stderr=%q calls=%v", result, stdout, stderr, service.calls)
	}
}

func TestBulkStateCompactOutputBudget(t *testing.T) {
	service := &bulkStateMailService{}
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "flag", "msg_one", "msg_two", "msg_three")
	if result.ExitCode != 0 || stderr != "" {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	lines := strings.Count(stdout, "\n")
	if lines > 30 || len(stdout) > 1200 {
		t.Fatalf("bulk compact budget lines=%d bytes=%d output=%q", lines, len(stdout), stdout)
	}
	if strings.Contains(stdout, "uid_validity") || strings.Contains(stdout, "mod_sequence") {
		t.Fatalf("compact bulk output expanded provider metadata: %q", stdout)
	}
}

func TestStructuredInputCommandContractMatchesCommandTree(t *testing.T) {
	state := &executionState{version: "test", schemaVersion: 18, stdin: strings.NewReader(""), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accounts: unavailableAccountService{}, folders: unavailableFolderService{}, mail: unavailableMailService{}}
	root := newRootCommand(state)
	contracts := automationCommandContracts()
	seen := make(map[string]bool, len(contracts))
	for _, contract := range contracts {
		parts := strings.Fields(contract.Command)
		command := findSubcommand(root, parts...)
		if command == nil {
			t.Fatalf("contract command missing from tree: %s", contract.Command)
		}
		annotation := writeCommandAnnotation
		kind := "write"
		if strings.Contains(contract.InputSchema, "-read-input.schema.json") {
			annotation = readCommandAnnotation
			kind = "read"
		}
		if command.Annotations == nil || command.Annotations[annotation] != "true" {
			t.Fatalf("%s command is not annotated: %s", kind, contract.Command)
		}
		if command.Flags().Lookup("input-json") == nil {
			t.Fatalf("structured-input command has no --input-json: %s", contract.Command)
		}
		if contract.InputSchema == "" || contract.ResponseSchema == "" {
			t.Fatalf("structured-input command missing schema link: %+v", contract)
		}
		seen[contract.Command] = true
	}
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		if command.Annotations != nil && (command.Annotations[writeCommandAnnotation] == "true" || command.Annotations[readCommandAnnotation] == "true") {
			path := strings.TrimPrefix(command.CommandPath(), command.Root().Name()+" ")
			if !seen[path] {
				t.Fatalf("annotated structured-input command missing capability contract: %s", path)
			}
		}
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(root)
}

func TestAutomationFlagVocabularyAndHelpStayConsistent(t *testing.T) {
	state := &executionState{version: "test", schemaVersion: 18, stdin: strings.NewReader(""), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accounts: unavailableAccountService{}, folders: unavailableFolderService{}, mail: unavailableMailService{}}
	root := newRootCommand(state)
	for _, name := range []string{"account", "home", "no-input", "request-id", "timeout"} {
		if flag := root.PersistentFlags().Lookup(name); flag == nil || strings.TrimSpace(flag.Usage) == "" {
			t.Fatalf("global flag --%s missing or undocumented", name)
		}
	}
	idempotencyFlags := 0
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		if flag := command.Flags().Lookup("idempotency-key"); flag != nil {
			idempotencyFlags++
			if flag.Usage != idempotencyKeyHelp {
				t.Fatalf("%s --idempotency-key help=%q want=%q", command.CommandPath(), flag.Usage, idempotencyKeyHelp)
			}
		}
		if flag := command.Flags().Lookup("input-json"); flag != nil {
			want := "cannot be mixed with equivalent write flags"
			if command.Annotations != nil && command.Annotations[readCommandAnnotation] == "true" {
				want = "cannot be mixed with equivalent read flags"
			}
			if !strings.Contains(flag.Usage, want) {
				t.Fatalf("%s --input-json help lost precedence rule: %q", command.CommandPath(), flag.Usage)
			}
		}
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(root)
	if idempotencyFlags < 10 {
		t.Fatalf("expected idempotency help on all outbound/location writes, found %d commands", idempotencyFlags)
	}
}
