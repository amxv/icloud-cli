package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

func TestGeneratedSchemasMatchCommittedArtifacts(t *testing.T) {
	generated, err := GeneratedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	directory := filepath.Join(root, "schemas", "v1")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		want, ok := generated[name]
		if !ok {
			t.Fatalf("unexpected committed schema %s", name)
		}
		got, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("schema drift for %s; run go run ./scripts/schema-gen.go", name)
		}
		seen[name] = true
	}
	for name := range generated {
		if !seen[name] {
			t.Fatalf("generated schema not committed: %s", name)
		}
	}
	publicDirectory := filepath.Join(root, "public", "schemas", "v1")
	for name, want := range generated {
		got, err := os.ReadFile(filepath.Join(publicDirectory, name))
		if err != nil {
			t.Fatalf("public schema mirror %s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("public schema mirror drift for %s; run make schemas", name)
		}
	}
	for _, required := range []string{"account", "folder", "message", "thread", "attachment", "checkpoint", "attempt", "draft", "event", "error"} {
		name := required + ".schema.json"
		if _, ok := generated[name]; !ok {
			t.Fatalf("required public resource schema missing: %s", name)
		}
	}
}

func TestCapabilitiesPublishesVersionedAutomationAndSchemaContracts(t *testing.T) {
	result, stdout, stderr := execute(t, "capabilities", "--json")
	if result.ExitCode != 0 || stderr != "" {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	var envelope struct {
		OK   bool             `json:"ok"`
		Data capabilitiesData `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil || !envelope.OK {
		t.Fatalf("envelope=%+v err=%v raw=%q", envelope, err, stdout)
	}
	if envelope.Data.Automation.ContractVersion != AutomationContractVersion || !envelope.Data.Automation.InputJSON || !envelope.Data.Automation.InputJSONL || envelope.Data.Automation.BulkMaximum != MaximumBulkItems {
		t.Fatalf("automation=%+v", envelope.Data.Automation)
	}
	for _, target := range []string{"account", "folder", "message", "thread", "attachment", "checkpoint", "attempt", "draft", "event", "error"} {
		if got := envelope.Data.Schemas[target]; got != SchemaBaseID+target+".schema.json" {
			t.Fatalf("schema %s=%q", target, got)
		}
	}
	if len(envelope.Data.Automation.Commands) < 30 {
		t.Fatalf("command contracts=%d", len(envelope.Data.Automation.Commands))
	}
	for _, command := range envelope.Data.Automation.Commands {
		if !strings.HasPrefix(command.InputSchema, SchemaBaseID) || command.ResponseSchema != SchemaBaseID+"output-envelope.schema.json" {
			t.Fatalf("bad command contract=%+v", command)
		}
	}
	generated, err := GeneratedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(generated["output-envelope.schema.json"], &schema); err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal([]byte(stdout), &value); err != nil {
		t.Fatal(err)
	}
	if err := validateSchemaValue(schema, value, "$", true); err != nil {
		t.Fatalf("capabilities envelope does not validate against published schema: %v", err)
	}
}

func TestCapabilitiesCompactOutputBudget(t *testing.T) {
	result, stdout, stderr := execute(t, "capabilities")
	if result.ExitCode != 0 || stderr != "" {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	if lines, size := strings.Count(stdout, "\n"), len(stdout); lines > 36 || size > 800 {
		t.Fatalf("compact capabilities budget lines=%d bytes=%d output=%q", lines, size, stdout)
	}
	for _, forbidden := range []string{SchemaBaseID, "account-add-input", "output-envelope.schema.json"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("compact capabilities expanded schema detail %q: %q", forbidden, stdout)
		}
	}
}

type localAccountService struct {
	unavailableAccountService
	listCalls int
	showCalls int
	values    []account.Account
}

func (service *localAccountService) List(context.Context, string, int) ([]account.Account, bool, int64, error) {
	service.listCalls++
	return append([]account.Account(nil), service.values...), false, 18, nil
}

func (service *localAccountService) Show(_ context.Context, _, selector string) (account.Account, int64, error) {
	service.showCalls++
	for _, value := range service.values {
		if value.ID == selector {
			return value, 18, nil
		}
	}
	return account.Account{}, 18, NewAppError(ErrorNotFound, "account not found", false)
}

type localFolderService struct {
	unavailableFolderService
	listCalls   int
	statusCalls int
	values      []folder.Folder
}

func (service *localFolderService) FolderList(context.Context, string, string, int, bool) (account.Account, []folder.Folder, bool, int64, error) {
	service.listCalls++
	return account.Account{ID: "acct_local"}, append([]folder.Folder(nil), service.values...), false, 18, nil
}

func (service *localFolderService) FolderStatus(context.Context, string, string) (account.Account, []folder.Folder, int64, error) {
	service.statusCalls++
	return account.Account{}, nil, 0, NewAppError(ErrorInternal, "network-like folder status must not power completion", false)
}

func TestDynamicCompletionUsesOnlyLocalAccountAndFolderRepositories(t *testing.T) {
	accounts := &localAccountService{values: []account.Account{{ID: "acct_123", Name: "Personal", Email: "owner@example.test"}}}
	folders := &localFolderService{values: []folder.Folder{{ID: "fld_123", Name: "Receipts", Kind: folder.KindOther, Present: true, Selectable: true}}}
	state := &executionState{version: "test", schemaVersion: 18, stdin: strings.NewReader(""), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accounts: accounts, folders: folders, mail: unavailableMailService{}}
	root := newRootCommand(state)
	globalCompletion, ok := root.GetFlagCompletionFunc("account")
	if !ok {
		t.Fatal("root --account has no dynamic completion")
	}
	globalValues, directive := globalCompletion(root, nil, "acct_")
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 || len(globalValues) != 1 || !strings.HasPrefix(globalValues[0], "acct_123\tPersonal") {
		t.Fatalf("global account completion values=%v directive=%v", globalValues, directive)
	}
	accountCommand := findSubcommand(root, "account", "show")
	accountValues, directive := accountCommand.ValidArgsFunction(accountCommand, nil, "acct_")
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 || len(accountValues) != 1 || !strings.HasPrefix(accountValues[0], "acct_123\tPersonal") {
		t.Fatalf("account completion values=%v directive=%v", accountValues, directive)
	}
	folderCommand := findSubcommand(root, "mail", "folders", "show")
	folderValues, directive := folderCommand.ValidArgsFunction(folderCommand, nil, "fld_")
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 || len(folderValues) != 1 || !strings.HasPrefix(folderValues[0], "fld_123\tReceipts") {
		t.Fatalf("folder completion values=%v directive=%v", folderValues, directive)
	}
	if accounts.listCalls != 2 || folders.listCalls != 1 || folders.statusCalls != 0 {
		t.Fatalf("completion calls accounts=%d folder-list=%d folder-status=%d", accounts.listCalls, folders.listCalls, folders.statusCalls)
	}
}

func TestCapabilitiesSelectedAccountUsesCachedProviderCapabilitiesOnly(t *testing.T) {
	accounts := &localAccountService{values: []account.Account{{ID: "acct_cached", IMAPCapabilities: []string{"IMAP4rev1", "IDLE"}, SMTPCapabilities: []string{"8BITMIME"}}}}
	var stdout, stderr bytes.Buffer
	result := Execute(context.Background(), []string{"--account", "acct_cached", "capabilities", "--json"}, Config{Version: "test", SchemaVersion: 18, Stdout: &stdout, Stderr: &stderr, Accounts: accounts})
	if result.ExitCode != 0 || stderr.Len() != 0 || accounts.showCalls != 1 {
		t.Fatalf("result=%+v show=%d stdout=%q stderr=%q", result, accounts.showCalls, stdout.String(), stderr.String())
	}
	var envelope struct {
		Data capabilitiesData `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil || envelope.Data.Provider == nil || !reflect.DeepEqual(envelope.Data.Provider.IMAPCapabilities, []string{"IMAP4rev1", "IDLE"}) {
		t.Fatalf("envelope=%+v err=%v", envelope, err)
	}
}

func TestPublicResourceSchemasValidateRepresentativePublicValues(t *testing.T) {
	generated, err := GeneratedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	message := publicMessage{
		ID: "msg_schema", AccountID: "acct_schema", Direction: maildomain.DirectionInbound,
		From: maildomain.Address{Email: "sender@example.test"}, DateSource: maildomain.DateSourceInternalDate,
		CreatedAt: now, UpdatedAt: now,
	}
	values := map[string]any{
		"account":    account.Account{ID: "acct_schema", Name: "Schema", Email: "owner@example.test", IMAPUsername: "owner", Status: account.StatusConnected, CreatedAt: now, UpdatedAt: now},
		"folder":     folder.Folder{ID: "fld_schema", AccountID: "acct_schema", Name: "Inbox", WireName: "INBOX", Kind: folder.KindInbox, Present: true, Selectable: true, CreatedAt: now, UpdatedAt: now, DiscoveredAt: now},
		"message":    message,
		"thread":     publicThread{ID: "thr_schema", AccountID: "acct_schema", MessageCount: 1, Status: "complete", Reconstruction: "rfc", FirstMessageAt: &now, LastMessageAt: &now, Messages: []publicThreadMessage{}},
		"attachment": publicAttachment{ID: "att_schema", MessageID: "msg_schema", PartID: "part_schema", PartPath: "1", Availability: maildomain.AttachmentAvailable},
		"checkpoint": maildomain.Checkpoint{ID: "chk_schema", AccountID: "acct_schema", FolderID: "fld_schema", State: maildomain.CheckpointIdle, CreatedAt: now, UpdatedAt: now},
		"attempt":    publicOutboundAttempt{ID: "send_schema", Status: maildomain.OutboundSent, Reconciliation: maildomain.ReconciliationPending, Kind: maildomain.OutboundKindSend, CreatedAt: now, UpdatedAt: now},
		"draft":      publicDraft{ID: "drf_schema", AccountID: "acct_schema", MessageID: "msg_schema", Revision: 1, SyncState: maildomain.DraftLocal, CreatedAt: now, UpdatedAt: now, Message: message},
		"event":      JSONLEvent{Event: "completed", Sequence: 1, Timestamp: now},
		"error":      NewAppError(ErrorUsage, "synthetic public error", false),
	}
	for name, typed := range values {
		t.Run(name, func(t *testing.T) {
			var schema map[string]any
			if err := json.Unmarshal(generated[name+".schema.json"], &schema); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(typed)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			if err := validateSchemaValue(schema, value, "$", true); err != nil {
				t.Fatalf("representative %s does not validate: %v\n%s", name, err, encoded)
			}
		})
	}
	resourceSchemas := make([][]byte, 0, len(values))
	for name := range values {
		resourceSchemas = append(resourceSchemas, generated[name+".schema.json"])
	}
	allSchemas := string(bytes.Join(resourceSchemas, nil))
	for _, forbidden := range []string{"raw_storage_key", "raw_content_hash", "idempotency_key\"", "credential_ref", "credential_reference"} {
		if strings.Contains(allSchemas, forbidden) {
			t.Fatalf("public resource schemas expose private/internal field %q", forbidden)
		}
	}
}

func mapValuesSorted(values map[string][]byte) [][]byte {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([][]byte, 0, len(keys))
	for _, key := range keys {
		result = append(result, values[key])
	}
	return result
}

func validateSchemaValue(schema map[string]any, value any, path string, enforceAdditional bool) error {
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: expected object", path)
		}
		properties, _ := schema["properties"].(map[string]any)
		required, _ := schema["required"].([]any)
		for _, item := range required {
			name, _ := item.(string)
			if _, ok := object[name]; !ok {
				return fmt.Errorf("%s: missing required %s", path, name)
			}
		}
		if additional, ok := schema["additionalProperties"].(bool); ok && !additional && enforceAdditional {
			for name := range object {
				if _, ok := properties[name]; !ok {
					return fmt.Errorf("%s: unexpected property %s", path, name)
				}
			}
		}
		keys := make([]string, 0, len(properties))
		for name := range properties {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			childValue, ok := object[name]
			if !ok {
				continue
			}
			child, _ := properties[name].(map[string]any)
			if err := validateSchemaValue(child, childValue, path+"."+name, enforceAdditional); err != nil {
				return err
			}
		}
	case "array":
		array, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s: expected array", path)
		}
		child, _ := schema["items"].(map[string]any)
		for index, item := range array {
			if err := validateSchemaValue(child, item, fmt.Sprintf("%s[%d]", path, index), enforceAdditional); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s: expected string", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: expected boolean", path)
		}
	case "integer":
		number, ok := value.(float64)
		if !ok || number != float64(int64(number)) {
			return fmt.Errorf("%s: expected integer", path)
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("%s: expected number", path)
		}
	}
	return nil
}
