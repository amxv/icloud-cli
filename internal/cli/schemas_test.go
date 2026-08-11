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
	for _, required := range []string{"account", "mail-identity", "folder", "message", "thread", "selection", "purge", "attachment", "checkpoint", "attempt", "outbox", "smime-certificate", "smime-identity", "draft", "event", "error"} {
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
	if !envelope.Data.MailOutbox {
		t.Fatal("capabilities did not advertise the safe-retry Outbox")
	}
	if !envelope.Data.MailSMIMEInspection || !envelope.Data.MailSMIMECertificates || !envelope.Data.MailSMIMEIdentities || !envelope.Data.MailSMIMEDecryption {
		t.Fatalf("capabilities did not advertise S/MIME surfaces: inspection=%v certificates=%v identities=%v decryption=%v", envelope.Data.MailSMIMEInspection, envelope.Data.MailSMIMECertificates, envelope.Data.MailSMIMEIdentities, envelope.Data.MailSMIMEDecryption)
	}
	for _, target := range []string{"account", "mail-identity", "folder", "message", "thread", "selection", "purge", "attachment", "checkpoint", "attempt", "outbox", "smime-certificate", "smime-identity", "draft", "event", "error"} {
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
	contracts := make(map[string]capabilityCommandContract, len(envelope.Data.Automation.Commands))
	for _, contract := range envelope.Data.Automation.Commands {
		contracts[contract.Command] = contract
	}
	for _, pair := range [][2]string{
		{"mail messages list", "mail inbox"},
		{"mail folders refresh", "mail folders status"},
		{"mail threads list", "mail latest"},
		{"mail threads show", "mail thread show"},
	} {
		canonical, canonicalOK := contracts[pair[0]]
		alias, aliasOK := contracts[pair[1]]
		if !canonicalOK || !aliasOK || canonical.InputSchema != alias.InputSchema {
			t.Fatalf("canonical/alias schema mismatch %v: canonical=%+v alias=%+v", pair, canonical, alias)
		}
	}
	if got := contracts["mail send"].InputSchema; got != SchemaBaseID+"send-input.schema.json" {
		t.Fatalf("existing write contract changed: mail send input=%q", got)
	}
	if got := contracts["mail search"].InputSchema; got != SchemaBaseID+"mail-search-read-input.schema.json" {
		t.Fatalf("mail search read contract=%q", got)
	}
	if !envelope.Data.MailIdentities {
		t.Fatal("capabilities did not advertise local mail identities")
	}
	if !envelope.Data.MailFolderRoles {
		t.Fatal("capabilities did not advertise local semantic folder-role bindings")
	}
	if !envelope.Data.MailReplyAnswered {
		t.Fatal("capabilities did not advertise proven provider Answered reply reconciliation")
	}
	if envelope.Data.ColoredFlags != "unsupported" {
		t.Fatalf("colored-flags capability=%q", envelope.Data.ColoredFlags)
	}
	if envelope.Data.ProviderForwardedState != "unsupported" {
		t.Fatalf("provider forwarded-state capability=%q", envelope.Data.ProviderForwardedState)
	}
	if !envelope.Data.MailJunk {
		t.Fatal("capabilities did not advertise provider-native Junk/Not Junk location workflows")
	}
	if !envelope.Data.MailSelections {
		t.Fatal("capabilities did not advertise immutable reviewed MailSelection snapshots")
	}
	if !envelope.Data.MailSenderPreferences {
		t.Fatal("capabilities did not advertise local sender preferences")
	}
	if !envelope.Data.MailThreadPreferences {
		t.Fatal("capabilities did not advertise local thread preferences")
	}
	if !envelope.Data.MailSignatures {
		t.Fatal("capabilities did not advertise local mail signatures")
	}
	for command, schema := range map[string]string{
		"mail senders list":    "sender-list-input",
		"mail senders show":    "sender-preference-input",
		"mail senders vip":     "sender-preference-input",
		"mail senders unvip":   "sender-preference-input",
		"mail senders block":   "sender-preference-input",
		"mail senders unblock": "sender-preference-input",
	} {
		if got := contracts[command].InputSchema; got != SchemaBaseID+schema+".schema.json" {
			t.Fatalf("sender preference contract %s=%q", command, got)
		}
	}
	for _, command := range []string{"mail threads mute", "mail threads unmute"} {
		if got := contracts[command].InputSchema; got != SchemaBaseID+"thread-preference-input.schema.json" {
			t.Fatalf("thread preference contract %s=%q", command, got)
		}
	}
	for command, schema := range map[string]string{
		"mail signatures list":    "signature-list-input",
		"mail signatures show":    "signature-target-input",
		"mail signatures create":  "signature-create-input",
		"mail signatures update":  "signature-update-input",
		"mail signatures delete":  "signature-target-input",
		"mail signatures default": "signature-default-input",
	} {
		if got := contracts[command].InputSchema; got != SchemaBaseID+schema+".schema.json" {
			t.Fatalf("signature contract %s=%q", command, got)
		}
	}
	for command, schema := range map[string]string{
		"mail selection create": "selection-create-input",
		"mail selection show":   "selection-target-read-input",
		"mail selection delete": "selection-target-input",
		"mail selection apply":  "selection-apply-input",
	} {
		if got := contracts[command].InputSchema; got != SchemaBaseID+schema+".schema.json" {
			t.Fatalf("MailSelection contract %s=%q", command, got)
		}
	}
	for command, schema := range map[string]string{
		"mail empty trash":  "mail-empty-role-input",
		"mail empty junk":   "mail-empty-role-input",
		"mail empty folder": "mail-empty-folder-input",
	} {
		if got := contracts[command].InputSchema; got != SchemaBaseID+schema+".schema.json" {
			t.Fatalf("mail purge contract %s=%q", command, got)
		}
		if got := contracts[command].JSONLSchema; got != SchemaBaseID+"event.schema.json" {
			t.Fatalf("mail purge JSONL contract %s=%q", command, got)
		}
	}
	for command, schema := range map[string]string{
		"mail identities list":       "mail-identity-list-read-input",
		"mail identities show":       "mail-identity-show-read-input",
		"mail identities add":        "mail-identity-add-input",
		"mail identities update":     "mail-identity-update-input",
		"mail identities enable":     "mail-identity-selector-input",
		"mail identities disable":    "mail-identity-selector-input",
		"mail identities default":    "mail-identity-selector-input",
		"mail identities remove":     "mail-identity-selector-input",
		"mail identities reclassify": "mail-identity-reclassify-input",
	} {
		if got := contracts[command].InputSchema; got != SchemaBaseID+schema+".schema.json" {
			t.Fatalf("identity contract %s=%q", command, got)
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

func TestPhase33SMIMEIdentitySchemasExposeReferencesOnlyBySafeSelector(t *testing.T) {
	generated, err := GeneratedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 4, 30, 0, 0, time.UTC)
	values := map[string]any{
		"smime-identity":                   maildomain.SMIMEIdentity{ID: "smi_schema", MailIdentityID: "mid_schema", Email: "owner@example.test", CertificateID: "smc_schema", FingerprintSHA256: strings.Repeat("a", 64), Subject: "CN=Recipient", NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: []string{"key-encipherment"}, ExtendedKeyUsage: []string{"email-protection"}, PrivateKeyScheme: account.CredentialFile, PrivateKeyConfigured: true, PrivateKeyReference: "file:/private/secret.pem", CreatedAt: now, UpdatedAt: now},
		"smime-identity-list-read-input":   smimeIdentityListReadJSONRequest{Limit: 20},
		"smime-identity-target-read-input": smimeIdentityTargetJSONRequest{Identity: "smi_schema"},
		"smime-identity-configure-input":   smimeIdentityConfigureJSONRequest{MailIdentity: "mid_schema", CertificateID: "smc_schema", PrivateKeyEnv: "SMIME_PRIVATE_KEY"},
		"smime-identity-target-input":      smimeIdentityTargetJSONRequest{Identity: "smi_schema"},
	}
	var combined bytes.Buffer
	for name, typed := range values {
		encodedSchema, ok := generated[name+".schema.json"]
		if !ok {
			t.Fatalf("missing generated Phase 33 schema %s", name)
		}
		combined.Write(encodedSchema)
		var schema map[string]any
		if err := json.Unmarshal(encodedSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
			t.Fatalf("Phase 33 schema %s must reject unknown fields", name)
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
			t.Fatalf("representative %s does not validate: %v", name, err)
		}
	}
	all := strings.ToLower(combined.String())
	for _, forbidden := range []string{`"private_key_reference"`, `"private_key_bytes"`, `"private_key_pem"`, `"secret"`, `"password"`, `"pkcs12"`, `"pfx"`} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("Phase 33 public/request schemas expose private key material field %s", forbidden)
		}
	}
	if !strings.Contains(all, `"private_key_env"`) || !strings.Contains(all, `"private_key_file"`) {
		t.Fatalf("Phase 33 configure schema does not expose safe external key selectors: %s", all)
	}
}

func TestPhase3ReadRequestSchemasAreStrictPublicAndPrivateFieldFree(t *testing.T) {
	generated, err := GeneratedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{
		"account-list-read-input":    accountListReadJSONRequest{Limit: 20, Cursor: "acur_test"},
		"folder-list-read-input":     folderListReadJSONRequest{Limit: 20, Cursor: "fcur_test", IncludeMissing: true, Verbose: true},
		"folder-show-read-input":     folderShowReadJSONRequest{Folder: "Inbox", Verbose: true},
		"folder-refresh-read-input":  folderRefreshReadJSONRequest{Limit: 20, Cursor: "fcur_test", Verbose: true},
		"message-list-read-input":    messageListReadJSONRequest{Folder: "inbox", Query: "invoice", Limit: 20, Unread: true},
		"mail-search-read-input":     mailSearchReadJSONRequest{Query: "invoice", Result: "messages", Limit: 20, Remote: true},
		"thread-list-read-input":     threadListReadJSONRequest{Query: "launch", Limit: 20},
		"thread-show-read-input":     threadShowReadJSONRequest{Target: "thr_test", Limit: 20, MetadataOnly: true},
		"message-show-read-input":    messageShowReadJSONRequest{MessageID: "msg_test", MetadataOnly: true},
		"attachment-list-read-input": attachmentListReadJSONRequest{Target: "msg_test", Limit: 20},
		"operation-list-read-input":  operationListReadJSONRequest{Operation: "outbound", Limit: 20},
		"sync-status-read-input":     syncStatusReadJSONRequest{Folder: "inbox"},
		"checkpoint-list-read-input": checkpointListReadJSONRequest{Limit: 20, Cursor: "ccur_test"},
	}
	var combined bytes.Buffer
	for name, typed := range values {
		t.Run(name, func(t *testing.T) {
			encodedSchema, ok := generated[name+".schema.json"]
			if !ok {
				t.Fatalf("missing generated read schema %s", name)
			}
			combined.Write(encodedSchema)
			var schema map[string]any
			if err := json.Unmarshal(encodedSchema, &schema); err != nil {
				t.Fatal(err)
			}
			if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
				t.Fatalf("read schema %s must reject unknown fields: %#v", name, schema["additionalProperties"])
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
	all := combined.String()
	for _, forbidden := range []string{
		`"bcc"`, `"body"`, `"credential_ref"`, `"credential_reference"`, `"password"`,
		`"raw_storage_key"`, `"raw_content_hash"`, `"storage_key"`, `"idempotency_key"`,
	} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("read request schemas expose private/internal field %s", forbidden)
		}
	}
	for _, required := range []string{
		"account list", "mail folders list", "mail folders show", "mail folders refresh",
		"mail messages list", "mail search", "mail threads list", "mail threads show",
		"mail message show", "mail attachments list", "mail operations outbound list",
		"mail operations mutations list", "mail outbox list", "mail smime certificates list", "mail status", "mail sync status", "mail sync checkpoints",
	} {
		found := false
		for _, contract := range automationCommandContracts() {
			if contract.Command == required && strings.Contains(contract.InputSchema, "-read-input.schema.json") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("read capability contract missing: %s", required)
		}
	}
}

func TestPhase4MailIdentityRequestSchemasAreStrictAndSecretFree(t *testing.T) {
	generated, err := GeneratedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	address := "updated@example.test"
	display := "Updated"
	kind := "custom_domain"
	values := map[string]any{
		"mail-identity-list-read-input":  mailIdentityListReadJSONRequest{Limit: 20, Cursor: "icur_test"},
		"mail-identity-show-read-input":  mailIdentityShowReadJSONRequest{Identity: "mid_test"},
		"mail-identity-add-input":        mailIdentityAddJSONRequest{Address: "alias@example.test", DisplayName: "Alias", Kind: "icloud_alias", Default: true},
		"mail-identity-update-input":     mailIdentityUpdateJSONRequest{Identity: "mid_test", Address: &address, DisplayName: &display, Kind: &kind},
		"mail-identity-selector-input":   mailIdentitySelectorJSONRequest{Identity: "mid_test"},
		"mail-identity-reclassify-input": mailIdentityReclassifyJSONRequest{Limit: 20},
	}
	var combined bytes.Buffer
	for name, typed := range values {
		encodedSchema, ok := generated[name+".schema.json"]
		if !ok {
			t.Fatalf("missing generated identity schema %s", name)
		}
		combined.Write(encodedSchema)
		var schema map[string]any
		if err := json.Unmarshal(encodedSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
			t.Fatalf("identity schema %s must reject unknown fields", name)
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
			t.Fatalf("representative %s does not validate: %v", name, err)
		}
	}
	for _, forbidden := range []string{"password", "credential", "raw_storage", "raw_content", "bcc", "idempotency"} {
		if strings.Contains(strings.ToLower(combined.String()), forbidden) {
			t.Fatalf("identity request schemas expose private field %q", forbidden)
		}
	}
}

func TestPhase31OutboxSchemasAreStrictAndPrivateReplayDataFree(t *testing.T) {
	generated, err := GeneratedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 1, 30, 0, 0, time.UTC)
	values := map[string]any{
		"outbox":                   publicOutboxItem{ID: "out_schema", SourceAttemptID: "send_schema", SenderIdentityID: "mid_schema", Status: maildomain.OutboxRetryable, Kind: maildomain.OutboundKindSend, ToCount: 1, BCCCount: 1, CreatedAt: now, UpdatedAt: now},
		"outbox-list-read-input":   outboxListReadJSONRequest{Limit: 20, Cursor: "obcur_test"},
		"outbox-target-read-input": outboxTargetJSONRequest{OutboxID: "out_schema"},
		"outbox-target-input":      outboxTargetJSONRequest{OutboxID: "out_schema"},
	}
	var combined bytes.Buffer
	for name, typed := range values {
		encodedSchema, ok := generated[name+".schema.json"]
		if !ok {
			t.Fatalf("missing generated Outbox schema %s", name)
		}
		combined.Write(encodedSchema)
		var schema map[string]any
		if err := json.Unmarshal(encodedSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
			t.Fatalf("Outbox schema %s must reject unknown fields", name)
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
			t.Fatalf("representative %s does not validate: %v", name, err)
		}
	}
	all := combined.String()
	for _, forbidden := range []string{`"raw_storage_key"`, `"raw_content_hash"`, `"request_hash"`, `"rfc_message_id"`, `"envelope"`, `"subject"`, `"body"`, `"idempotency_key"`, `"reply_to"`} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("Outbox public/request schemas expose private replay field %s", forbidden)
		}
	}
}

func TestPhase32SMIMECertificateSchemasAreStrictAndPrivateKeyFree(t *testing.T) {
	generated, err := GeneratedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 2, 30, 0, 0, time.UTC)
	values := map[string]any{
		"smime-certificate":                 maildomain.SMIMECertificate{ID: "smc_schema", Email: "sender@example.test", FingerprintSHA256: strings.Repeat("a", 64), Subject: "CN=Signer", Issuer: "CN=Root", NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), Trusted: false, KeyUsage: []string{"digital-signature"}, ExtendedKeyUsage: []string{"email-protection"}, CreatedAt: now, UpdatedAt: now},
		"smime-certificate-list-read-input": smimeCertificateListReadJSONRequest{Email: "sender@example.test", Limit: 20},
		"smime-certificate-import-input":    smimeCertificateImportJSONRequest{Email: "sender@example.test", CertificateFile: "/tmp/public.pem", Trust: false},
		"smime-certificate-target-input":    smimeCertificateTargetJSONRequest{CertificateID: "smc_schema"},
	}
	var combined bytes.Buffer
	for name, typed := range values {
		encodedSchema, ok := generated[name+".schema.json"]
		if !ok {
			t.Fatalf("missing generated Phase 32 schema %s", name)
		}
		combined.Write(encodedSchema)
		var schema map[string]any
		if err := json.Unmarshal(encodedSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
			t.Fatalf("Phase 32 schema %s must reject unknown fields", name)
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
			t.Fatalf("representative %s does not validate: %v", name, err)
		}
	}
	all := strings.ToLower(combined.String())
	for _, forbidden := range []string{`"der"`, `"private_key"`, `"private-key"`, `"password"`, `"credential"`, `"pkcs12"`, `"pfx"`} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("Phase 32 public/request schemas expose private key/certificate bytes field %s", forbidden)
		}
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
	listCalls     int
	showCalls     int
	identityCalls int
	values        []account.Account
	identities    []account.MailIdentity
}

func (service *localAccountService) IdentityListPage(_ context.Context, _, selector string, _ int, _ string) (account.Account, []account.MailIdentity, bool, string, int64, error) {
	service.identityCalls++
	for _, value := range service.values {
		if value.ID == selector {
			return value, append([]account.MailIdentity(nil), service.identities...), false, "", 18, nil
		}
	}
	return account.Account{}, nil, false, "", 18, NewAppError(ErrorNotFound, "account not found", false)
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
		"account":       account.Account{ID: "acct_schema", Name: "Schema", Email: "owner@example.test", IMAPUsername: "owner", Status: account.StatusConnected, CreatedAt: now, UpdatedAt: now},
		"mail-identity": account.MailIdentity{ID: "mid_schema", AccountID: "acct_schema", Address: "owner@example.test", DisplayName: "Schema", Kind: account.MailIdentityPrimary, Provenance: account.MailIdentityAccountPrimary, Verification: account.MailIdentityUnverified, Enabled: true, Default: true, CreatedAt: now, UpdatedAt: now},
		"folder":        folder.Folder{ID: "fld_schema", AccountID: "acct_schema", Name: "Inbox", WireName: "INBOX", Kind: folder.KindInbox, Present: true, Selectable: true, CreatedAt: now, UpdatedAt: now, DiscoveredAt: now},
		"message":       message,
		"thread":        publicThread{ID: "thr_schema", AccountID: "acct_schema", MessageCount: 1, Status: "complete", Reconstruction: "rfc", FirstMessageAt: &now, LastMessageAt: &now, Messages: []publicThreadMessage{}},
		"attachment":    publicAttachment{ID: "att_schema", MessageID: "msg_schema", PartID: "part_schema", PartPath: "1", Availability: maildomain.AttachmentAvailable},
		"checkpoint":    maildomain.Checkpoint{ID: "chk_schema", AccountID: "acct_schema", FolderID: "fld_schema", State: maildomain.CheckpointIdle, CreatedAt: now, UpdatedAt: now},
		"attempt":       publicOutboundAttempt{ID: "send_schema", Status: maildomain.OutboundSent, Reconciliation: maildomain.ReconciliationPending, Kind: maildomain.OutboundKindSend, CreatedAt: now, UpdatedAt: now},
		"draft":         publicDraft{ID: "drf_schema", AccountID: "acct_schema", MessageID: "msg_schema", Revision: 1, SyncState: maildomain.DraftLocal, CreatedAt: now, UpdatedAt: now, Message: message},
		"event":         JSONLEvent{Event: "completed", Sequence: 1, Timestamp: now},
		"error":         NewAppError(ErrorUsage, "synthetic public error", false),
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
