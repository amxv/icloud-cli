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

type selectionMailService struct {
	threadMutationMailService
	selection      maildomain.MailSelection
	messages       []maildomain.Message
	selectionCalls int
	deleteCalls    int
	flagSnapshots  []maildomain.MailSelectionItem
}

func (service *selectionMailService) MailSelectionCreate(_ context.Context, _, _ string, request maildomain.SelectionCreateRequest) (account.Account, maildomain.MailSelection, int64, error) {
	service.selectionCalls++
	return account.Account{ID: service.selection.AccountID}, service.selection, 27, nil
}

func (service *selectionMailService) MailSelection(_ context.Context, _, _, _ string) (account.Account, maildomain.MailSelection, int64, error) {
	service.selectionCalls++
	return account.Account{ID: service.selection.AccountID}, service.selection, 27, nil
}

func (service *selectionMailService) MailSelectionForApply(_ context.Context, _, _, _ string, _ bool) (account.Account, maildomain.MailSelection, []maildomain.Message, int64, error) {
	service.selectionCalls++
	return account.Account{ID: service.selection.AccountID}, service.selection, append([]maildomain.Message(nil), service.messages...), 27, nil
}

func (service *selectionMailService) MailSelectionDelete(_ context.Context, _, _, _ string) (account.Account, maildomain.MailSelection, int64, error) {
	service.deleteCalls++
	return account.Account{ID: service.selection.AccountID}, service.selection, 27, nil
}

func (service *selectionMailService) MailSelectionApplyHash(_ context.Context, selection maildomain.MailSelection, action, mode, destination string, planHashes []string) (string, error) {
	payload := selection.ID + "\x00" + selection.RequestHash + "\x00" + action + "\x00" + mode + "\x00" + destination + "\x00" + strings.Join(planHashes, ",")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:]), nil
}

func (service *selectionMailService) MailUpdateFlagSnapshot(ctx context.Context, home, accountSelector, messageID, folderSelector string, snapshot maildomain.MailSelectionItem, mutation maildomain.FlagMutation) (account.Account, folder.Folder, maildomain.Message, maildomain.Location, int64, error) {
	service.flagSnapshots = append(service.flagSnapshots, snapshot)
	return service.MailUpdateFlag(ctx, home, accountSelector, messageID, folderSelector, mutation)
}

func (service *selectionMailService) MailLocationPreview(_ context.Context, _, _ string, request maildomain.LocationMutationRequest) (account.Account, maildomain.LocationMutationPlan, int64, error) {
	service.events = append(service.events, "preview:"+request.MessageID+":"+request.ToFolder)
	sum := sha256.Sum256([]byte(request.MessageID + "\x00" + request.FromFolder + "\x00" + request.ToFolder + "\x00" + string(request.Action)))
	return account.Account{ID: service.selection.AccountID}, maildomain.LocationMutationPlan{
		Action: request.Action, Operation: request.Action.TransportOperation(), MessageID: request.MessageID,
		SourceLocation: maildomain.Location{ID: request.ExpectedLocationID, UIDValidity: request.ExpectedUIDValidity, UID: request.ExpectedUID, ModSequence: request.ExpectedModSequence, Present: true},
		SourceFolderID: request.FromFolder, DestinationFolderID: request.ToFolder,
		RequestHash: hex.EncodeToString(sum[:]),
	}, 27, nil
}

func TestSelectionApplyHashBindsReviewedDestinationAndReusesFrozenItems(t *testing.T) {
	selection := maildomain.MailSelection{
		ID: "sel_mailSelection", AccountID: "acct_mailSelection", SourceKind: maildomain.SelectionSourceSearch,
		SourceDescription: "one bounded local search page", SourceQueryHash: strings.Repeat("1", 64), ScopeFolderID: "fld_inbox",
		RequestHash: strings.Repeat("2", 64), ItemCount: 1,
		CreatedAt: time.Date(2026, 8, 10, 7, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 8, 10, 7, 15, 0, 0, time.UTC),
		Items: []maildomain.MailSelectionItem{{Ordinal: 0, MessageID: "msg_reviewed", LocationID: "loc_reviewed", FolderID: "fld_inbox", UIDValidity: 100, UID: 7, ModSequence: 55}},
	}
	service := &selectionMailService{selection: selection, messages: []maildomain.Message{{ID: "msg_reviewed"}}}

	result, stdout, stderr := executeWithMail(t, service, "", "mail", "selection", "apply", selection.ID, "--action", "copy", "--to", "fld_projects", "--dry-run", "--json")
	if result.ExitCode != 0 || stderr != "" || len(service.requests) != 0 {
		t.Fatalf("preview result=%+v requests=%+v stdout=%q stderr=%q", result, service.requests, stdout, stderr)
	}
	var preview struct {
		OK   bool                          `json:"ok"`
		Data mailSelectionApplyPreviewData `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &preview); err != nil || !preview.OK || preview.Data.Count != 1 || len(preview.Data.RequestHash) != 64 || preview.Data.LocationPlans[0].SourceLocation.ID != "loc_reviewed" || preview.Data.ProviderContacted {
		t.Fatalf("preview=%+v err=%v raw=%q", preview, err, stdout)
	}
	applyHash := preview.Data.RequestHash

	// Same selection, different destination, old review hash: reject before mutation.
	result, stdout, stderr = executeWithMail(t, service, "", "mail", "selection", "apply", selection.ID, "--action", "copy", "--to", "fld_other", "--request-hash", applyHash, "--yes")
	if result.ExitCode != ExitConflict || stdout != "" || len(service.requests) != 0 || !strings.Contains(stderr, "reviewed request hash") {
		t.Fatalf("changed request result=%+v requests=%+v stdout=%q stderr=%q", result, service.requests, stdout, stderr)
	}

	result, stdout, stderr = executeWithMail(t, service, "", "mail", "selection", "apply", selection.ID, "--action", "copy", "--to", "fld_projects", "--request-hash", applyHash, "--yes", "--jsonl")
	if result.ExitCode != 0 || stderr != "" || len(service.requests) != 1 || service.requests[0].MessageID != "msg_reviewed" || service.requests[0].ExpectedLocationID != "loc_reviewed" || service.requests[0].ExpectedUIDValidity != 100 || service.requests[0].ExpectedUID != 7 || service.requests[0].ExpectedModSequence != 55 || !strings.HasPrefix(service.requests[0].IdempotencyKey, "selection:") {
		t.Fatalf("apply result=%+v requests=%+v stdout=%q stderr=%q", result, service.requests, stdout, stderr)
	}
	if !strings.Contains(stdout, `"selection_id":"sel_mailSelection"`) || !strings.Contains(stdout, `"apply_request_hash":"`+applyHash+`"`) || strings.Contains(stdout, "unreviewed") {
		t.Fatalf("apply stdout=%q", stdout)
	}
}

func TestSelectionStatePreviewRequiresHashAndCarriesFrozenSnapshot(t *testing.T) {
	selection := maildomain.MailSelection{
		ID: "sel_state", AccountID: "acct_mailSelection", SourceKind: maildomain.SelectionSourceExplicit,
		SourceDescription: "explicit stable message/thread targets", SourceQueryHash: strings.Repeat("3", 64), ScopeFolderID: "fld_inbox",
		RequestHash: strings.Repeat("4", 64), ItemCount: 1,
		CreatedAt: time.Date(2026, 8, 10, 7, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 8, 10, 7, 15, 0, 0, time.UTC),
		Items: []maildomain.MailSelectionItem{{Ordinal: 0, MessageID: "msg_state", LocationID: "loc_state", FolderID: "fld_inbox", UIDValidity: 100, UID: 9, ModSequence: 88}},
	}
	service := &selectionMailService{selection: selection}

	result, stdout, stderr := executeWithMail(t, service, "", "mail", "selection", "apply", selection.ID, "--action", "mark-read", "--dry-run", "--json")
	if result.ExitCode != 0 || stderr != "" || len(service.flagRequests) != 0 {
		t.Fatalf("preview result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	var preview struct {
		Data mailSelectionApplyPreviewData `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &preview); err != nil || len(preview.Data.RequestHash) != 64 {
		t.Fatalf("preview=%+v err=%v raw=%q", preview, err, stdout)
	}
	result, stdout, stderr = executeWithMail(t, service, "", "mail", "selection", "apply", selection.ID, "--action", "mark-read", "--json")
	if result.ExitCode != ExitSafety || stdout != "" || len(service.flagRequests) != 0 || !strings.Contains(stderr, "requires --request-hash") {
		t.Fatalf("missing hash result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}

	result, stdout, stderr = executeWithMail(t, service, "", "mail", "selection", "apply", selection.ID, "--action", "mark-read", "--request-hash", preview.Data.RequestHash, "--json")
	if result.ExitCode != 0 || stderr != "" || len(service.flagRequests) != 1 || service.flagRequests[0].messageID != "msg_state" || service.flagRequests[0].folder != "fld_inbox" || len(service.flagSnapshots) != 1 || service.flagSnapshots[0].LocationID != "loc_state" || service.flagSnapshots[0].ModSequence != 88 {
		t.Fatalf("state apply result=%+v requests=%+v stdout=%q stderr=%q", result, service.flagRequests, stdout, stderr)
	}
}

func TestSelectionResourceSchemaIsStrictAndMessagePayloadFree(t *testing.T) {
	generated, err := GeneratedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	encoded, ok := generated["selection.schema.json"]
	if !ok {
		t.Fatal("selection resource schema is missing")
	}
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
		t.Fatalf("selection schema must reject unknown fields: %#v", schema["additionalProperties"])
	}
	text := string(encoded)
	for _, forbidden := range []string{`"subject"`, `"snippet"`, `"from"`, `"to"`, `"cc"`, `"bcc"`, `"body"`, `"html"`, `"raw_content_hash"`, `"raw_storage_key"`, `"attachment"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("selection resource schema exposes message payload field %s", forbidden)
		}
	}
}

var _ MailService = (*selectionMailService)(nil)
