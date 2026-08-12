package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type threadMutationMailService struct {
	locationBulkMailService
	threadPage   maildomain.ThreadMutationTargetPage
	threadErr    error
	threadCalls  int
	threadTarget string
	threadCursor string
	threadLimit  int
	flagRequests []struct {
		messageID string
		folder    string
		mutation  maildomain.FlagMutation
	}
}

func (service *threadMutationMailService) MailThreadMutationTargets(_ context.Context, _, _, target, cursor string, limit int, _ string, _ bool) (account.Account, maildomain.ThreadMutationTargetPage, int64, error) {
	service.threadCalls++
	service.threadTarget = target
	service.threadCursor = cursor
	service.threadLimit = limit
	if service.threadErr != nil {
		return account.Account{}, maildomain.ThreadMutationTargetPage{}, 26, service.threadErr
	}
	return account.Account{ID: "acct_threadMutation"}, service.threadPage, 26, nil
}

func (service *threadMutationMailService) MailUpdateFlag(_ context.Context, _, _, messageID, folderSelector string, mutation maildomain.FlagMutation) (account.Account, folder.Folder, maildomain.Message, maildomain.Location, int64, error) {
	service.flagRequests = append(service.flagRequests, struct {
		messageID string
		folder    string
		mutation  maildomain.FlagMutation
	}{messageID: messageID, folder: folderSelector, mutation: mutation})
	now := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	return account.Account{ID: "acct_threadMutation"}, folder.Folder{ID: "fld_inbox", Kind: folder.KindInbox}, maildomain.Message{ID: messageID}, maildomain.Location{
		ID: "loc_" + strings.TrimPrefix(messageID, "msg_"), FolderID: "fld_inbox", UIDValidity: 100, UID: uint32(len(service.flagRequests)),
		Flags: maildomain.LocationFlags{Seen: mutation == maildomain.FlagMutationMarkRead}, FlagsCheckedAt: &now,
	}, 26, nil
}

func TestThreadMutationRequiresExplicitProviderCopyScope(t *testing.T) {
	service := &threadMutationMailService{threadPage: maildomain.ThreadMutationTargetPage{ThreadID: "thr_canonical", MessageIDs: []string{"msg_one"}, MessageCount: 1, ProjectionState: "ready"}}
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "threads", "mark-read", "thr_canonical")
	if result.ExitCode != ExitUsage || stdout != "" || service.threadCalls != 0 || len(service.flagRequests) != 0 || !strings.Contains(stderr, "requires --folder") {
		t.Fatalf("flag result=%+v calls=%d requests=%v stdout=%q stderr=%q", result, service.threadCalls, service.flagRequests, stdout, stderr)
	}
	result, stdout, stderr = executeWithMail(t, service, "", "mail", "threads", "archive", "thr_canonical", "--dry-run")
	if result.ExitCode != ExitUsage || stdout != "" || service.threadCalls != 0 || len(service.requests) != 0 || !strings.Contains(stderr, "requires --from") {
		t.Fatalf("location result=%+v calls=%d requests=%v stdout=%q stderr=%q", result, service.threadCalls, service.requests, stdout, stderr)
	}
}

func TestThreadLargeFirstPageRequiresExplicitPagingBeforeWrites(t *testing.T) {
	service := &threadMutationMailService{threadPage: maildomain.ThreadMutationTargetPage{
		ThreadID: "thr_large", MessageIDs: []string{"msg_one", "msg_two"}, MessageCount: 101, More: true, NextCursor: "cur_next", ProjectionState: "ready",
	}}
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "threads", "mark-read", "thr_large", "--folder", "inbox", "--limit", "2", "--jsonl")
	if result.ExitCode != ExitSafety || stdout != "" || len(service.flagRequests) != 0 || !strings.Contains(stderr, "refuse an implicit partial whole-thread mutation") || !strings.Contains(stderr, "--page") {
		t.Fatalf("result=%+v requests=%v stdout=%q stderr=%q", result, service.flagRequests, stdout, stderr)
	}

	result, stdout, stderr = executeWithMail(t, service, "", "mail", "threads", "mark-read", "thr_large", "--folder", "inbox", "--limit", "2", "--page", "--jsonl")
	if result.ExitCode != 0 || stderr != "" || len(service.flagRequests) != 2 {
		t.Fatalf("paged result=%+v requests=%v stdout=%q stderr=%q", result, service.flagRequests, stdout, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], `"message_id":"msg_one"`) || !strings.Contains(lines[1], `"message_id":"msg_two"`) {
		t.Fatalf("ordered page output=%q", stdout)
	}
	var completed JSONLEvent
	if err := json.Unmarshal([]byte(lines[2]), &completed); err != nil || completed.Event != "completed" {
		t.Fatalf("completed=%+v err=%v raw=%q", completed, err, lines[2])
	}
	payload := completed.Payload.(map[string]any)
	if payload["thread_id"] != "thr_large" || payload["more"] != true || payload["next_cursor"] != "cur_next" || int(payload["thread_message_count"].(float64)) != 101 {
		t.Fatalf("completed payload=%#v", payload)
	}
}

func TestThreadPagedScopeCanAdvanceAcrossZeroEligibleMembers(t *testing.T) {
	service := &threadMutationMailService{threadPage: maildomain.ThreadMutationTargetPage{
		ThreadID: "thr_sparse", MessageIDs: nil, MessageCount: 150, More: true, NextCursor: "cur_sparse_next", ProjectionState: "ready",
	}}
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "threads", "mark-read", "thr_sparse", "--folder", "inbox", "--limit", "100", "--page", "--jsonl")
	if result.ExitCode != 0 || stderr != "" || len(service.flagRequests) != 0 {
		t.Fatalf("result=%+v requests=%v stdout=%q stderr=%q", result, service.flagRequests, stdout, stderr)
	}
	var completed JSONLEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &completed); err != nil || completed.Event != "completed" {
		t.Fatalf("completed=%+v err=%v raw=%q", completed, err, stdout)
	}
	payload := completed.Payload.(map[string]any)
	if payload["thread_id"] != "thr_sparse" || payload["more"] != true || payload["next_cursor"] != "cur_sparse_next" || int(payload["eligible"].(float64)) != 0 || int(payload["attempted"].(float64)) != 0 {
		t.Fatalf("payload=%#v", payload)
	}
}

func TestThreadAliasFlagMutationUsesCanonicalOrderedMembersOnly(t *testing.T) {
	service := &threadMutationMailService{threadPage: maildomain.ThreadMutationTargetPage{
		ThreadID: "thr_canonical", RedirectedFrom: "thr_retired", MessageIDs: []string{"msg_one", "msg_two", "msg_three"}, MessageCount: 3, ProjectionState: "ready",
	}}
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "threads", "flag", "thr_retired", "--folder", "inbox", "--json")
	if result.ExitCode != 0 || stderr != "" || len(service.flagRequests) != 3 {
		t.Fatalf("result=%+v requests=%v stdout=%q stderr=%q", result, service.flagRequests, stdout, stderr)
	}
	for index, want := range []string{"msg_one", "msg_two", "msg_three"} {
		if service.flagRequests[index].messageID != want || service.flagRequests[index].folder != "inbox" || service.flagRequests[index].mutation != maildomain.FlagMutationFlag {
			t.Fatalf("request %d=%+v", index, service.flagRequests[index])
		}
	}
	if !strings.Contains(stdout, `"thread_id":"thr_canonical"`) || !strings.Contains(stdout, `"redirected_from":"thr_retired"`) || !strings.Contains(stdout, `"thread_message_count":3`) {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestThreadLocationUsesSharedPlannerAndStablePerMessageReplayKeys(t *testing.T) {
	service := &threadMutationMailService{threadPage: maildomain.ThreadMutationTargetPage{
		ThreadID: "thr_canonical", RedirectedFrom: "thr_retired", MessageIDs: []string{"msg_one", "msg_two"}, MessageCount: 2, ProjectionState: "ready",
	}}
	input := `{"target":"thr_retired","from":"inbox","to":"Projects","idempotency_key":"threadMutation-thread","mode":"atomic"}`
	result, stdout, stderr := executeWithMail(t, service, input, "mail", "threads", "move", "--input-json", "-", "--yes", "--json")
	if result.ExitCode != 0 || stderr != "" || len(service.requests) != 2 {
		t.Fatalf("result=%+v requests=%+v stdout=%q stderr=%q", result, service.requests, stdout, stderr)
	}
	for index, request := range service.requests {
		if request.FromFolder != "inbox" || request.ToFolder != "Projects" || !strings.HasPrefix(request.IdempotencyKey, "thread:") {
			t.Fatalf("request %d=%+v", index, request)
		}
	}
	if service.requests[0].IdempotencyKey == service.requests[1].IdempotencyKey {
		t.Fatalf("per-message replay keys collided: %+v", service.requests)
	}
	if got := strings.Join(service.events, ","); got != "preview:msg_one,preview:msg_two,mutate:msg_one,mutate:msg_two" {
		t.Fatalf("events=%q", got)
	}
	if !strings.Contains(stdout, `"thread_id":"thr_canonical"`) || !strings.Contains(stdout, `"redirected_from":"thr_retired"`) || !strings.Contains(stdout, `"completed":2`) {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestThreadProjectionConflictStopsBeforePerMessageEngines(t *testing.T) {
	conflict := NewAppError(ErrorConflict, "thread projection is not complete; repair it before a whole-thread mutation", false)
	conflict.Details = map[string]any{"projection_state": "degraded", "projection_reason": "affected_scope_limit"}
	service := &threadMutationMailService{threadErr: conflict}
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "threads", "move", "thr_large", "--from", "inbox", "--to", "Projects", "--dry-run")
	if result.ExitCode != ExitConflict || stdout != "" || len(service.requests) != 0 || len(service.flagRequests) != 0 || !strings.Contains(stderr, "projection is not complete") {
		t.Fatalf("result=%+v requests=%v flags=%v stdout=%q stderr=%q", result, service.requests, service.flagRequests, stdout, stderr)
	}
}

var _ MailService = (*threadMutationMailService)(nil)
