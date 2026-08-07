package cli

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

const (
	AutomationContractVersion = "1.0.0"
	SchemaBaseID              = "https://icloud.ashray.xyz/schemas/v1/"
)

type schemaSpec struct {
	Filename string
	Title    string
	Type     reflect.Type
}

func GeneratedSchemas() (map[string][]byte, error) {
	specs := []schemaSpec{
		{"account.schema.json", "iCloud CLI account", reflect.TypeOf(account.Account{})},
		{"folder.schema.json", "iCloud CLI folder", reflect.TypeOf(folder.Folder{})},
		{"message.schema.json", "iCloud CLI message", reflect.TypeOf(publicMessage{})},
		{"thread.schema.json", "iCloud CLI thread", reflect.TypeOf(publicThread{})},
		{"attachment.schema.json", "iCloud CLI attachment", reflect.TypeOf(publicAttachment{})},
		{"checkpoint.schema.json", "iCloud CLI checkpoint", reflect.TypeOf(maildomain.Checkpoint{})},
		{"attempt.schema.json", "iCloud CLI outbound attempt", reflect.TypeOf(publicOutboundAttempt{})},
		{"draft.schema.json", "iCloud CLI draft", reflect.TypeOf(publicDraft{})},
		{"event.schema.json", "iCloud CLI JSONL event", reflect.TypeOf(JSONLEvent{})},
		{"error.schema.json", "iCloud CLI error", reflect.TypeOf(AppError{})},
		{"output-envelope.schema.json", "iCloud CLI output envelope", reflect.TypeOf(OutputEnvelope{})},
		{"account-add-input.schema.json", "account add JSON input", reflect.TypeOf(accountAddJSONRequest{})},
		{"account-selector-input.schema.json", "account selector JSON input", reflect.TypeOf(accountSelectorJSONRequest{})},
		{"account-rename-input.schema.json", "account rename JSON input", reflect.TypeOf(accountRenameJSONRequest{})},
		{"credential-replace-input.schema.json", "credential replace JSON input", reflect.TypeOf(credentialReplaceJSONRequest{})},
		{"folder-policy-input.schema.json", "folder policy JSON input", reflect.TypeOf(folderPolicyJSONRequest{})},
		{"folder-create-input.schema.json", "folder create JSON input", reflect.TypeOf(folderCreateJSONRequest{})},
		{"folder-rename-input.schema.json", "folder rename JSON input", reflect.TypeOf(folderRenameJSONRequest{})},
		{"folder-selector-input.schema.json", "folder selector JSON input", reflect.TypeOf(folderSelectorJSONRequest{})},
		{"folder-delete-input.schema.json", "folder delete JSON input", reflect.TypeOf(folderDeleteJSONRequest{})},
		{"mail-sync-input.schema.json", "mail sync JSON input", reflect.TypeOf(mailSyncJSONRequest{})},
		{"mail-backfill-input.schema.json", "mail backfill JSON input", reflect.TypeOf(mailBackfillJSONRequest{})},
		{"mail-rebaseline-input.schema.json", "mail rebaseline JSON input", reflect.TypeOf(mailRebaselineJSONRequest{})},
		{"mail-repair-input.schema.json", "mail repair JSON input", reflect.TypeOf(mailRepairJSONRequest{})},
		{"search-repair-input.schema.json", "search repair JSON input", reflect.TypeOf(mailSearchRepairJSONRequest{})},
		{"message-export-input.schema.json", "message export JSON input", reflect.TypeOf(messageExportJSONRequest{})},
		{"attachment-save-input.schema.json", "attachment save JSON input", reflect.TypeOf(attachmentSaveJSONRequest{})},
		{"attachments-save-all-input.schema.json", "attachment set export JSON input", reflect.TypeOf(attachmentsSaveAllJSONRequest{})},
		{"send-input.schema.json", "mail send JSON input", reflect.TypeOf(composeJSONRequest{})},
		{"reply-input.schema.json", "mail reply JSON input", reflect.TypeOf(replyJSONRequest{})},
		{"forward-input.schema.json", "mail forward JSON input", reflect.TypeOf(forwardJSONRequest{})},
		{"draft-save-input.schema.json", "draft create/update JSON input", reflect.TypeOf(draftSaveJSONRequest{})},
		{"draft-sync-input.schema.json", "draft sync JSON input", reflect.TypeOf(draftSyncJSONRequest{})},
		{"draft-target-input.schema.json", "draft target JSON input", reflect.TypeOf(draftTargetJSONRequest{})},
		{"location-input.schema.json", "mail location mutation JSON input", reflect.TypeOf(locationJSONRequest{})},
		{"flag-bulk-input.schema.json", "bulk mail state JSON input", reflect.TypeOf(flagBulkJSONRequest{})},
		{"flag-bulk-item.schema.json", "bulk mail state JSONL item", reflect.TypeOf(flagBulkJSONLItem{})},
	}
	result := make(map[string][]byte, len(specs))
	for _, spec := range specs {
		document := schemaForType(spec.Type)
		document["$schema"] = "https://json-schema.org/draft/2020-12/schema"
		document["$id"] = SchemaBaseID + spec.Filename
		document["title"] = spec.Title
		document["x-icloud-contract-version"] = AutomationContractVersion
		encoded, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal schema %s: %w", spec.Filename, err)
		}
		result[spec.Filename] = append(encoded, '\n')
	}
	return result, nil
}

func SchemaIDs() map[string]string {
	generated, err := GeneratedSchemas()
	if err != nil {
		return nil
	}
	keys := make([]string, 0, len(generated))
	for filename := range generated {
		keys = append(keys, filename)
	}
	sort.Strings(keys)
	result := make(map[string]string, len(keys))
	for _, filename := range keys {
		name := strings.TrimSuffix(filename, ".schema.json")
		result[name] = SchemaBaseID + filename
	}
	return result
}

type capabilityCommandContract struct {
	Command        string `json:"command"`
	InputSchema    string `json:"input_schema"`
	ResponseSchema string `json:"response_schema,omitempty"`
	JSONLSchema    string `json:"jsonl_schema,omitempty"`
}

func automationCommandContracts() []capabilityCommandContract {
	ids := SchemaIDs()
	response := ids["output-envelope"]
	event := ids["event"]
	entries := []struct {
		command string
		input   string
		jsonl   bool
	}{
		{"account add", "account-add-input", false},
		{"account rename", "account-rename-input", false},
		{"account default", "account-selector-input", false},
		{"account remove", "account-selector-input", false},
		{"account credential replace", "credential-replace-input", false},
		{"account credential delete", "account-selector-input", false},
		{"mail folders enable", "folder-policy-input", false},
		{"mail folders disable", "folder-policy-input", false},
		{"mail folders create", "folder-create-input", false},
		{"mail folders rename", "folder-rename-input", false},
		{"mail folders delete", "folder-delete-input", false},
		{"mail folders subscribe", "folder-selector-input", false},
		{"mail folders unsubscribe", "folder-selector-input", false},
		{"mail sync", "mail-sync-input", false},
		{"mail sync backfill", "mail-backfill-input", false},
		{"mail sync rebaseline", "mail-rebaseline-input", false},
		{"mail sync repair", "mail-repair-input", false},
		{"mail search repair", "search-repair-input", false},
		{"mail message export", "message-export-input", false},
		{"mail attachments save", "attachment-save-input", false},
		{"mail attachments save-all", "attachments-save-all-input", false},
		{"mail mark-read", "flag-bulk-input", true},
		{"mail mark-unread", "flag-bulk-input", true},
		{"mail flag", "flag-bulk-input", true},
		{"mail unflag", "flag-bulk-input", true},
		{"mail move", "location-input", false},
		{"mail copy", "location-input", false},
		{"mail archive", "location-input", false},
		{"mail unarchive", "location-input", false},
		{"mail trash", "location-input", false},
		{"mail restore", "location-input", false},
		{"mail delete", "location-input", false},
		{"mail send", "send-input", false},
		{"mail reply", "reply-input", false},
		{"mail reply-all", "reply-input", false},
		{"mail forward", "forward-input", false},
		{"mail draft create", "draft-save-input", false},
		{"mail draft update", "draft-save-input", false},
		{"mail draft sync", "draft-sync-input", false},
		{"mail draft send", "draft-target-input", false},
		{"mail draft delete", "draft-target-input", false},
	}
	result := make([]capabilityCommandContract, 0, len(entries))
	for _, entry := range entries {
		contract := capabilityCommandContract{Command: entry.command, InputSchema: ids[entry.input], ResponseSchema: response}
		if entry.jsonl {
			contract.JSONLSchema = event
		}
		result = append(result, contract)
	}
	return result
}

var timeType = reflect.TypeOf(time.Time{})

func schemaForType(value reflect.Type) map[string]any {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value == timeType {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch value.Kind() {
	case reflect.Struct:
		properties := map[string]any{}
		required := make([]string, 0)
		for index := 0; index < value.NumField(); index++ {
			field := value.Field(index)
			if !field.IsExported() {
				continue
			}
			name, omitEmpty, skip := jsonField(field)
			if skip {
				continue
			}
			properties[name] = schemaForType(field.Type)
			if !omitEmpty {
				required = append(required, name)
			}
		}
		result := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			sort.Strings(required)
			result["required"] = required
		}
		return result
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaForType(value.Elem())}
	case reflect.Map:
		if value.Key().Kind() == reflect.String {
			return map[string]any{"type": "object", "additionalProperties": schemaForType(value.Elem())}
		}
		return map[string]any{"type": "object"}
	case reflect.Interface:
		return map[string]any{}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	default:
		return map[string]any{}
	}
}

func jsonField(field reflect.StructField) (name string, omitEmpty, skip bool) {
	tag := field.Tag.Get("json")
	parts := strings.Split(tag, ",")
	if len(parts) > 0 && parts[0] == "-" {
		return "", false, true
	}
	name = field.Name
	if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
		name = parts[0]
	}
	for _, option := range parts[1:] {
		if option == "omitempty" {
			omitEmpty = true
		}
	}
	return name, omitEmpty, false
}
