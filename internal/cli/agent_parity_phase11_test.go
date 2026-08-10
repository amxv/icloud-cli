package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type phase11LocationMailService struct {
	unavailableMailService
	events      []string
	requests    []maildomain.LocationMutationRequest
	previewFail map[string]error
	mutateFail  map[string]error
}

func (service *phase11LocationMailService) MailLocationPreview(_ context.Context, _, _ string, request maildomain.LocationMutationRequest) (account.Account, maildomain.LocationMutationPlan, int64, error) {
	service.events = append(service.events, "preview:"+request.MessageID)
	if err := service.previewFail[request.MessageID]; err != nil {
		return account.Account{}, maildomain.LocationMutationPlan{}, 26, err
	}
	destination := request.ToFolder
	if destination == "" {
		destination = "fld_semantic"
	}
	return account.Account{ID: "acct_phase11"}, maildomain.LocationMutationPlan{
		Action: request.Action, Operation: request.Action.TransportOperation(), MessageID: request.MessageID,
		SourceLocation: maildomain.Location{ID: "loc_" + strings.TrimPrefix(request.MessageID, "msg_"), UIDValidity: 100, UID: 1, Present: true},
		SourceFolderID: "fld_source", DestinationFolderID: destination,
		RequestHash: strings.Repeat("a", 64),
	}, 26, nil
}

func (service *phase11LocationMailService) MailMutateLocation(_ context.Context, _, _ string, request maildomain.LocationMutationRequest) (account.Account, maildomain.LocationMutationPlan, maildomain.Message, maildomain.LocationMutation, bool, int64, error) {
	service.events = append(service.events, "mutate:"+request.MessageID)
	service.requests = append(service.requests, request)
	mutation := maildomain.LocationMutation{
		ID: "mut_" + strings.TrimPrefix(request.MessageID, "msg_"), AccountID: "acct_phase11", MessageID: request.MessageID,
		Action: request.Action, Status: maildomain.LocationMutationConfirmed, ProviderMethod: "move", SourcePresent: false, MappingKnown: true,
	}
	if err := service.mutateFail[request.MessageID]; err != nil {
		mutation.Status = maildomain.LocationMutationFailed
		return account.Account{ID: "acct_phase11"}, maildomain.LocationMutationPlan{}, maildomain.Message{}, mutation, false, 26, err
	}
	return account.Account{ID: "acct_phase11"}, maildomain.LocationMutationPlan{}, maildomain.Message{ID: request.MessageID}, mutation, false, 26, nil
}

func TestPhase11LocationBulkValidationFailsBeforeProviderWrites(t *testing.T) {
	service := &phase11LocationMailService{previewFail: map[string]error{
		"msg_bad": NewAppError(ErrorNotFound, "synthetic missing target", false),
	}}
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "move", "msg_one", "msg_bad", "msg_three", "--from", "inbox", "--to", "archive", "--yes")
	if result.ExitCode != ExitNotFound || stdout != "" || len(service.requests) != 0 {
		t.Fatalf("result=%+v requests=%+v stdout=%q stderr=%q", result, service.requests, stdout, stderr)
	}
	if got := strings.Join(service.events, ","); got != "preview:msg_one,preview:msg_bad" {
		t.Fatalf("validation events=%q", got)
	}
	if !strings.Contains(stderr, "before provider contact") {
		t.Fatalf("stderr=%q", stderr)
	}
}

func TestPhase11LocationBulkAtomicStopsAfterCommittedThenFailedItem(t *testing.T) {
	service := &phase11LocationMailService{mutateFail: map[string]error{
		"msg_bad": NewAppError(ErrorUnavailable, "synthetic provider failure", true),
	}}
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "move", "msg_one", "msg_bad", "msg_three", "--from", "inbox", "--to", "archive", "--yes", "--mode", "atomic")
	if result.ExitCode != ExitUnavailable || len(service.requests) != 2 {
		t.Fatalf("result=%+v requests=%+v stdout=%q stderr=%q", result, service.requests, stdout, stderr)
	}
	if got := strings.Join(service.events, ","); got != "preview:msg_one,preview:msg_bad,preview:msg_three,mutate:msg_one,mutate:msg_bad" {
		t.Fatalf("events=%q", got)
	}
	for _, want := range []string{"mail-location-item", "mail-location-error", "mode=atomic", "attempted=2", "completed=1", "failed=1", "skipped=1", "partial=true"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q: %q", want, stdout)
		}
	}
	if !strings.Contains(stderr, "prior provider writes remain committed") {
		t.Fatalf("stderr=%q", stderr)
	}
}

func TestPhase11LocationBulkContinueJSONLReportsOrderedTruth(t *testing.T) {
	service := &phase11LocationMailService{mutateFail: map[string]error{
		"msg_bad": NewAppError(ErrorUnavailable, "synthetic provider failure", true),
	}}
	input := strings.Join([]string{
		`{"message_id":"msg_one"}`,
		`{"message_id":"msg_bad"}`,
		`{"message_id":"msg_three"}`,
	}, "\n") + "\n"
	result, stdout, stderr := executeWithMail(t, service, input, "mail", "move", "--input-jsonl", "-", "--from", "inbox", "--to", "archive", "--yes", "--mode", "continue-on-error", "--jsonl")
	if result.ExitCode != ExitUnavailable || len(service.requests) != 3 {
		t.Fatalf("result=%+v requests=%+v stdout=%q stderr=%q", result, service.requests, stdout, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 4 {
		t.Fatalf("stdout lines=%d: %q", len(lines), stdout)
	}
	for index, want := range []string{`"message_id":"msg_one"`, `"message_id":"msg_bad"`, `"message_id":"msg_three"`} {
		if !strings.Contains(lines[index], want) {
			t.Fatalf("line %d missing %s: %q", index, want, lines[index])
		}
	}
	var completed JSONLEvent
	if err := json.Unmarshal([]byte(lines[3]), &completed); err != nil || completed.Event != "completed" {
		t.Fatalf("completed=%+v err=%v raw=%q", completed, err, lines[3])
	}
	payload := completed.Payload.(map[string]any)
	if payload["mode"] != "continue-on-error" || int(payload["requested"].(float64)) != 3 || int(payload["attempted"].(float64)) != 3 || int(payload["completed"].(float64)) != 2 || int(payload["failed"].(float64)) != 1 || int(payload["skipped"].(float64)) != 0 || payload["partial"] != true {
		t.Fatalf("completed payload=%#v", payload)
	}
	if !strings.Contains(stderr, `"event":"error"`) || !strings.Contains(stderr, `"partial":true`) {
		t.Fatalf("stderr=%q", stderr)
	}
}

func TestPhase11LocationBulkJSONSupportsPerItemOverridesAndBatchIdempotency(t *testing.T) {
	service := &phase11LocationMailService{}
	input := `{"items":[{"message_id":"msg_one","from":"inbox","to":"Archive A"},{"message_id":"msg_two","from":"sent","to":"Archive B","idempotency_key":"item-two"}],"idempotency_key":"batch-phase11"}`
	result, stdout, stderr := executeWithMail(t, service, input, "mail", "move", "--input-json", "-", "--yes", "--json")
	if result.ExitCode != 0 || stderr != "" || len(service.requests) != 2 {
		t.Fatalf("result=%+v requests=%+v stdout=%q stderr=%q", result, service.requests, stdout, stderr)
	}
	if service.requests[0].FromFolder != "inbox" || service.requests[0].ToFolder != "Archive A" || !strings.HasPrefix(service.requests[0].IdempotencyKey, "bulk:") || service.requests[1].FromFolder != "sent" || service.requests[1].ToFolder != "Archive B" || service.requests[1].IdempotencyKey != "item-two" || service.requests[0].IdempotencyKey == service.requests[1].IdempotencyKey {
		t.Fatalf("requests=%+v", service.requests)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Requested int `json:"requested"`
			Completed int `json:"completed"`
			Failed    int `json:"failed"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil || !envelope.OK || envelope.Data.Requested != 2 || envelope.Data.Completed != 2 || envelope.Data.Failed != 0 {
		t.Fatalf("envelope=%+v err=%v raw=%q", envelope, err, stdout)
	}
}

func TestPhase11LocationBulkBoundRejectsBeforePreview(t *testing.T) {
	service := &phase11LocationMailService{}
	args := []string{"mail", "trash"}
	for index := 0; index < MaximumBulkItems+1; index++ {
		args = append(args, fmt.Sprintf("msg_%03d", index))
	}
	result, stdout, stderr := executeWithMail(t, service, "", args...)
	if result.ExitCode != ExitUsage || stdout != "" || len(service.events) != 0 || !strings.Contains(stderr, "between 1 and 100") {
		t.Fatalf("result=%+v stdout=%q stderr=%q events=%v", result, stdout, stderr, service.events)
	}
}

var _ MailService = (*phase11LocationMailService)(nil)
