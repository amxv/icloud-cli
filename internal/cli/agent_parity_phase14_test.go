package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type phase14PurgeMailService struct {
	phase13SelectionMailService
	purge          maildomain.MailPurge
	purgeFolder    folder.Folder
	createCalls    int
	loadCalls      int
	providerCalls  int
	mutationsByKey map[string]maildomain.LocationMutation
	mutationStatus maildomain.LocationMutationStatus
}

func (service *phase14PurgeMailService) MailPurgeCreate(_ context.Context, _, _ string, request maildomain.PurgeCreateRequest) (account.Account, folder.Folder, maildomain.MailPurge, int64, error) {
	service.createCalls++
	value := service.purge
	value.ScopeKind = request.ScopeKind
	return account.Account{ID: value.AccountID}, service.purgeFolder, value, 28, nil
}

func (service *phase14PurgeMailService) MailPurge(_ context.Context, _, _, _ string, request maildomain.PurgeCreateRequest) (account.Account, folder.Folder, maildomain.MailPurge, int64, error) {
	service.loadCalls++
	value := service.purge
	if value.ScopeKind == "" {
		value.ScopeKind = request.ScopeKind
	}
	return account.Account{ID: value.AccountID}, service.purgeFolder, value, 28, nil
}

func (service *phase14PurgeMailService) MailPurgeApplyHash(_ context.Context, purge maildomain.MailPurge, mode string, planHashes []string) (string, error) {
	payload := purge.ID + "\x00" + purge.RequestHash + "\x00" + mode + "\x00" + strings.Join(planHashes, ",")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:]), nil
}

func (service *phase14PurgeMailService) MailLocationPreview(_ context.Context, _, _ string, request maildomain.LocationMutationRequest) (account.Account, maildomain.LocationMutationPlan, int64, error) {
	service.events = append(service.events, "preview:"+request.ExpectedLocationID)
	sum := sha256.Sum256([]byte(request.MessageID + "\x00" + request.FromFolder + "\x00" + request.ExpectedLocationID + "\x00" + string(request.Action)))
	return account.Account{ID: service.purge.AccountID}, maildomain.LocationMutationPlan{
		Action: request.Action, Operation: request.Action.TransportOperation(), MessageID: request.MessageID,
		SourceLocation: maildomain.Location{
			ID: request.ExpectedLocationID, FolderID: request.FromFolder,
			UIDValidity: request.ExpectedUIDValidity, UID: request.ExpectedUID,
			ModSequence: request.ExpectedModSequence, Present: true,
		},
		SourceFolderID: request.FromFolder, SourceFolderName: service.purgeFolder.Name,
		IdempotencyKey: request.IdempotencyKey, RequestHash: hex.EncodeToString(sum[:]),
	}, 28, nil
}

func (service *phase14PurgeMailService) MailMutateLocation(_ context.Context, _, _ string, request maildomain.LocationMutationRequest) (account.Account, maildomain.LocationMutationPlan, maildomain.Message, maildomain.LocationMutation, bool, int64, error) {
	service.requests = append(service.requests, request)
	if service.mutationsByKey == nil {
		service.mutationsByKey = map[string]maildomain.LocationMutation{}
	}
	if existing, ok := service.mutationsByKey[request.IdempotencyKey]; ok {
		return account.Account{ID: service.purge.AccountID}, maildomain.LocationMutationPlan{}, maildomain.Message{ID: request.MessageID}, existing, true, 28, nil
	}
	service.providerCalls++
	status := service.mutationStatus
	if status == "" {
		status = maildomain.LocationMutationConfirmed
	}
	mutation := maildomain.LocationMutation{
		ID:        "mut_" + strings.TrimPrefix(request.ExpectedLocationID, "loc_"),
		AccountID: service.purge.AccountID, MessageID: request.MessageID,
		Action: maildomain.LocationActionDelete, SourceLocationID: request.ExpectedLocationID,
		SourceFolderID: request.FromFolder, Status: status, ProviderMethod: "uid_expunge",
		SourcePresent:          status != maildomain.LocationMutationConfirmed,
		ReconciliationRequired: status == maildomain.LocationMutationUnresolved,
	}
	service.mutationsByKey[request.IdempotencyKey] = mutation
	return account.Account{ID: service.purge.AccountID}, maildomain.LocationMutationPlan{}, maildomain.Message{ID: request.MessageID}, mutation, false, 28, nil
}

func TestPhase14PurgeRequiresReviewHashAndAppliesFrozenExactLocations(t *testing.T) {
	purge := phase14PurgeFixture(maildomain.PurgeScopeFolder, true)
	service := &phase14PurgeMailService{
		purge: purge, purgeFolder: folder.Folder{ID: purge.FolderID, Name: "Disposable Folder", Kind: folder.KindOther, Present: true, Selectable: true},
	}

	result, stdout, stderr := executeWithMail(t, service, "", "mail", "empty", "folder", "Disposable Folder", "--limit", "2", "--dry-run", "--json")
	if result.ExitCode != 0 || stderr != "" || service.createCalls != 1 || service.providerCalls != 0 || len(service.requests) != 0 {
		t.Fatalf("preview result=%+v create=%d provider=%d requests=%+v stdout=%q stderr=%q", result, service.createCalls, service.providerCalls, service.requests, stdout, stderr)
	}
	var preview struct {
		OK   bool                 `json:"ok"`
		Data mailPurgePreviewData `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &preview); err != nil || !preview.OK || preview.Data.Count != 2 || !preview.Data.More || preview.Data.ProviderContacted || len(preview.Data.ApplyRequestHash) != 64 || preview.Data.Purge.ID != purge.ID || len(preview.Data.LocationPlans) != 2 {
		t.Fatalf("preview=%+v err=%v raw=%q", preview, err, stdout)
	}
	applyHash := preview.Data.ApplyRequestHash

	result, stdout, stderr = executeWithMail(t, service, "", "mail", "empty", "folder", "Disposable Folder", "--purge-id", purge.ID, "--mode", "continue-on-error", "--request-hash", applyHash, "--yes")
	if result.ExitCode != ExitConflict || service.providerCalls != 0 || len(service.requests) != 0 || stdout != "" || !strings.Contains(stderr, "reviewed request hash") {
		t.Fatalf("changed mode result=%+v provider=%d requests=%+v stdout=%q stderr=%q", result, service.providerCalls, service.requests, stdout, stderr)
	}

	result, stdout, stderr = executeWithMail(t, service, "", "mail", "empty", "folder", "Disposable Folder", "--purge-id", purge.ID, "--request-hash", applyHash, "--yes", "--jsonl")
	if result.ExitCode != 0 || stderr != "" || service.providerCalls != 2 || len(service.requests) != 2 {
		t.Fatalf("apply result=%+v provider=%d requests=%+v stdout=%q stderr=%q", result, service.providerCalls, service.requests, stdout, stderr)
	}
	for index, request := range service.requests {
		item := purge.Items[index]
		if request.Action != maildomain.LocationActionDelete || request.FromFolder != purge.FolderID || request.ExpectedLocationID != item.LocationID || request.ExpectedUIDValidity != item.UIDValidity || request.ExpectedUID != item.UID || request.ExpectedModSequence != item.ModSequence || !strings.HasPrefix(request.IdempotencyKey, "purge:") {
			t.Fatalf("request %d=%+v item=%+v", index, request, item)
		}
	}
	if !strings.Contains(stdout, `"purge_id":"pur_phase14"`) || !strings.Contains(stdout, `"more":true`) || !strings.Contains(stdout, `"apply_request_hash":"`+applyHash+`"`) {
		t.Fatalf("apply stdout=%q", stdout)
	}

	result, _, stderr = executeWithMail(t, service, "", "mail", "empty", "folder", "Disposable Folder", "--purge-id", purge.ID, "--request-hash", applyHash, "--yes", "--jsonl")
	if result.ExitCode != 0 || stderr != "" || service.providerCalls != 2 || len(service.requests) != 4 {
		t.Fatalf("replay result=%+v provider=%d requests=%d stderr=%q", result, service.providerCalls, len(service.requests), stderr)
	}
}

func TestPhase14PurgeUnresolvedReplayIsBlockedWithoutProviderRetry(t *testing.T) {
	purge := phase14PurgeFixture(maildomain.PurgeScopeTrash, false)
	purge.Items = purge.Items[:1]
	purge.ItemCount = 1
	purge.More = false
	service := &phase14PurgeMailService{
		purge: purge, purgeFolder: folder.Folder{ID: purge.FolderID, Name: "Trash Bound", Kind: folder.KindOther, Present: true, Selectable: true},
		mutationStatus: maildomain.LocationMutationUnresolved,
	}
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "empty", "trash", "--purge-id", purge.ID, "--dry-run", "--json")
	if result.ExitCode != 0 || stderr != "" || service.providerCalls != 0 {
		t.Fatalf("review result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	var preview struct {
		Data mailPurgePreviewData `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &preview); err != nil {
		t.Fatal(err)
	}
	result, stdout, stderr = executeWithMail(t, service, "", "mail", "empty", "trash", "--purge-id", purge.ID, "--request-hash", preview.Data.ApplyRequestHash, "--yes")
	if result.ExitCode != ExitConflict || service.providerCalls != 1 || !strings.Contains(stdout, "mail-location-error") || !strings.Contains(stderr, "will not be retried automatically") {
		t.Fatalf("first unresolved result=%+v provider=%d stdout=%q stderr=%q", result, service.providerCalls, stdout, stderr)
	}
	result, _, stderr = executeWithMail(t, service, "", "mail", "empty", "trash", "--purge-id", purge.ID, "--request-hash", preview.Data.ApplyRequestHash, "--yes")
	if result.ExitCode != ExitConflict || service.providerCalls != 1 || !strings.Contains(stderr, "will not be retried automatically") {
		t.Fatalf("replay unresolved result=%+v provider=%d stderr=%q", result, service.providerCalls, stderr)
	}
}

func TestPhase14PurgeSafetyAndRetentionContracts(t *testing.T) {
	purge := phase14PurgeFixture(maildomain.PurgeScopeJunk, false)
	service := &phase14PurgeMailService{purge: purge, purgeFolder: folder.Folder{ID: purge.FolderID, Name: "Junk Bound", Kind: folder.KindOther, Present: true, Selectable: true}}

	result, stdout, stderr := executeWithMail(t, service, "", "mail", "empty", "junk", "--yes")
	if result.ExitCode != ExitSafety || stdout != "" || service.createCalls != 0 || service.loadCalls != 0 || !strings.Contains(stderr, "requires --purge-id") {
		t.Fatalf("unreviewed result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	result, stdout, stderr = executeWithMail(t, service, "", "mail", "empty", "junk", "--dry-run", "--json")
	if result.ExitCode != 0 || stderr != "" || !strings.Contains(stdout, `"retention_warning":"provider_auto_delete_possible"`) || service.providerCalls != 0 {
		t.Fatalf("junk review result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	result, _, stderr = executeWithMail(t, service, "", "mail", "empty", "folder", "--dry-run")
	if result.ExitCode != ExitUsage || !strings.Contains(stderr, "deliberately named custom folder") {
		t.Fatalf("implicit custom folder result=%+v stderr=%q", result, stderr)
	}
}

func TestPhase14PurgeBlockedOnlyReviewDoesNotClaimFolderIsEmpty(t *testing.T) {
	purge := phase14PurgeFixture(maildomain.PurgeScopeJunk, false)
	purge.ID = ""
	purge.RequestHash = ""
	purge.Items = nil
	purge.ItemCount = 0
	purge.BlockedUnresolved = 1
	service := &phase14PurgeMailService{
		purge:       purge,
		purgeFolder: folder.Folder{ID: purge.FolderID, Name: "Junk Bound", Kind: folder.KindOther, Present: true, Selectable: true},
	}
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "empty", "junk", "--dry-run", "--json")
	if result.ExitCode != 0 || stderr != "" || service.providerCalls != 0 {
		t.Fatalf("blocked review result=%+v provider=%d stdout=%q stderr=%q", result, service.providerCalls, stdout, stderr)
	}
	var envelope struct {
		Data mailPurgePreviewData `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Count != 0 || envelope.Data.More || envelope.Data.Empty || envelope.Data.BlockedUnresolved != 1 || envelope.Data.ProviderContacted {
		t.Fatalf("blocked review data=%+v", envelope.Data)
	}
}

func TestPhase14PurgeSchemasAreStrictAndRoleFormCannotSmuggleFolder(t *testing.T) {
	generated, err := GeneratedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	for _, filename := range []string{"purge.schema.json", "mail-empty-role-input.schema.json", "mail-empty-folder-input.schema.json"} {
		encoded, ok := generated[filename]
		if !ok {
			t.Fatalf("missing schema %s", filename)
		}
		var schema map[string]any
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
			t.Fatalf("schema %s must reject unknown fields: %#v", filename, schema["additionalProperties"])
		}
	}
	roleSchema := string(generated["mail-empty-role-input.schema.json"])
	if strings.Contains(roleSchema, `"folder"`) {
		t.Fatalf("Trash/Junk purge schema exposes custom folder selector: %s", roleSchema)
	}
	resource := string(generated["purge.schema.json"])
	for _, forbidden := range []string{`"subject"`, `"snippet"`, `"from"`, `"to"`, `"cc"`, `"bcc"`, `"body"`, `"html"`, `"raw_content_hash"`, `"raw_storage_key"`, `"attachment"`} {
		if strings.Contains(resource, forbidden) {
			t.Fatalf("purge resource schema exposes message payload field %s", forbidden)
		}
	}
}

func phase14PurgeFixture(scope maildomain.PurgeScopeKind, more bool) maildomain.MailPurge {
	return maildomain.MailPurge{
		ID: "pur_phase14", AccountID: "acct_phase14", FolderID: "fld_phase14", ScopeKind: scope,
		RequestHash: strings.Repeat("b", 64), ItemCount: 2, More: more,
		CreatedAt: time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 8, 10, 8, 15, 0, 0, time.UTC),
		Items: []maildomain.MailPurgeItem{
			{Ordinal: 0, MessageID: "msg_phase14_one", LocationID: "loc_phase14_one", UIDValidity: 700, UID: 11, ModSequence: 101},
			{Ordinal: 1, MessageID: "msg_phase14_one", LocationID: "loc_phase14_two", UIDValidity: 700, UID: 12, ModSequence: 102},
		},
	}
}

var _ mailPurgeService = (*phase14PurgeMailService)(nil)
