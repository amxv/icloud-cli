package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type threadMuteMailService struct {
	unavailableMailService
	targets []string
	values  []bool
}

func (service *threadMuteMailService) MailThreadSetMuted(_ context.Context, _, _, target string, muted bool) (account.Account, maildomain.ThreadPreference, int64, error) {
	service.targets = append(service.targets, target)
	service.values = append(service.values, muted)
	return account.Account{ID: "acct_threadMute"}, maildomain.ThreadPreference{
		AccountID: "acct_threadMute", ThreadID: "thr_threadMute_current", AnchorMessageID: "msg_threadMute_anchor",
		Muted: muted, LocalOnly: true, CreatedAt: time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 8, 11, 4, 1, 0, 0, time.UTC),
	}, 33, nil
}

func TestThreadMuteCommandsAreStrictLocalStructuredOperations(t *testing.T) {
	service := &threadMuteMailService{}
	result, stdout, stderr := executeWithMail(t, service, `{"target":"thr_threadMute_retired"}`, "mail", "threads", "mute", "--input-json", "-", "--json")
	if result.ExitCode != 0 || stderr != "" || len(service.targets) != 1 || service.targets[0] != "thr_threadMute_retired" || !service.values[0] {
		t.Fatalf("mute result=%+v targets=%v values=%v stdout=%q stderr=%q", result, service.targets, service.values, stdout, stderr)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			LocalOnly  bool                        `json:"local_only"`
			Preference maildomain.ThreadPreference `json:"preference"`
			Schema     int64                       `json:"schema_version"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil || !envelope.OK || !envelope.Data.LocalOnly || !envelope.Data.Preference.LocalOnly ||
		envelope.Data.Preference.ThreadID != "thr_threadMute_current" || envelope.Data.Preference.AnchorMessageID != "msg_threadMute_anchor" || !envelope.Data.Preference.Muted || envelope.Data.Schema != 33 {
		t.Fatalf("mute envelope=%+v err=%v raw=%q", envelope, err, stdout)
	}

	result, stdout, stderr = executeWithMail(t, service, "", "mail", "threads", "unmute", "msg_threadMute_member")
	if result.ExitCode != 0 || stderr != "" || len(service.targets) != 2 || service.targets[1] != "msg_threadMute_member" || service.values[1] {
		t.Fatalf("unmute result=%+v targets=%v values=%v stdout=%q stderr=%q", result, service.targets, service.values, stdout, stderr)
	}
	if !strings.Contains(stdout, "scope=local") || !strings.Contains(stdout, "muted=false") || !strings.Contains(stdout, "anchor=msg_threadMute_anchor") {
		t.Fatalf("compact unmute omitted explicit local result: %q", stdout)
	}

	before := len(service.targets)
	result, stdout, stderr = executeWithMail(t, service, `{"target":"thr_threadMute_current","unexpected":true}`, "mail", "threads", "mute", "--input-json", "-", "--json")
	if result.ExitCode != ExitUsage || stdout != "" || len(service.targets) != before || !strings.Contains(stderr, "invalid --input-json request") {
		t.Fatalf("strict input result=%+v calls=%d stdout=%q stderr=%q", result, len(service.targets), stdout, stderr)
	}
}

func TestMuteStateSurvivesPublicMessageThreadAndSearchProjection(t *testing.T) {
	message := maildomain.Message{ID: "msg_threadMute", ThreadMuted: true}
	publicMessage := sanitizeMessage(message)
	if !publicMessage.ThreadMuted {
		t.Fatalf("public message lost thread mute: %+v", publicMessage)
	}

	thread := maildomain.Thread{ID: "thr_threadMute", AccountID: "acct_threadMute", CanonicalSubject: "Muted thread", MessageCount: 1, Muted: true}
	publicThread, err := buildPublicThread(context.Background(), thread, nil, OutputOptions{MetadataOnly: true}, maildomain.ContentText)
	if err != nil || !publicThread.Muted {
		t.Fatalf("public thread=%+v err=%v", publicThread, err)
	}
	var compact strings.Builder
	if err := renderThreadSummary(NewCompactTextRenderer(&compact), thread); err != nil || !strings.Contains(compact.String(), "muted=true") {
		t.Fatalf("compact thread=%q err=%v", compact.String(), err)
	}

	var searchCompact strings.Builder
	if err := renderSearchHit(NewCompactTextRenderer(&searchCompact), maildomain.SearchHit{Kind: maildomain.SearchThreads, Thread: &thread}); err != nil || !strings.Contains(searchCompact.String(), "muted=true") {
		t.Fatalf("compact search=%q err=%v", searchCompact.String(), err)
	}
}

func TestMutedWatchChangesSuppressOnlyDefaultCompactPresentation(t *testing.T) {
	now := time.Date(2026, 8, 11, 5, 30, 0, 0, time.UTC)
	changed := maildomain.SyncSummary{AccountID: "acct_threadMute", FolderID: "fld_inbox", FolderName: "INBOX", Fetched: 1, Created: 1}
	service := watchTestMailService{run: func(_ context.Context, _ maildomain.WatchOptions, emit func(maildomain.WatchEvent) error) error {
		for _, event := range []maildomain.WatchEvent{
			{Type: maildomain.WatchEventChange, Sequence: 1, AccountID: "acct_threadMute", Timestamp: now, FolderID: "fld_inbox", FolderName: "INBOX", Reason: "change", Quiet: true},
			{Type: maildomain.WatchEventSync, Sequence: 2, AccountID: "acct_threadMute", Timestamp: now, FolderID: "fld_inbox", FolderName: "INBOX", Summary: &changed, Quiet: true},
			{Type: maildomain.WatchEventCompleted, Sequence: 3, AccountID: "acct_threadMute", Timestamp: now},
		} {
			if err := emit(event); err != nil {
				return err
			}
		}
		return nil
	}}

	result, stdout, stderr := executeWatchTest(t, service, "mail", "watch")
	if result.ExitCode != 0 || stderr != "" || strings.Contains(stdout, "watch-change") || strings.Contains(stdout, "watch-sync") || !strings.Contains(stdout, "watch-completed") {
		t.Fatalf("compact result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}

	result, stdout, stderr = executeWatchTest(t, service, "mail", "watch", "--verbose")
	if result.ExitCode != 0 || stderr != "" || !strings.Contains(stdout, "watch-change") || !strings.Contains(stdout, "watch-sync") || !strings.Contains(stdout, "fetched=1") {
		t.Fatalf("verbose result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}

	result, stdout, stderr = executeWatchTest(t, service, "mail", "watch", "--jsonl")
	if result.ExitCode != 0 || stderr != "" {
		t.Fatalf("jsonl result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("jsonl lines=%d output=%q", len(lines), stdout)
	}
	wantEvents := []string{string(maildomain.WatchEventChange), string(maildomain.WatchEventSync), string(maildomain.WatchEventCompleted)}
	for index, line := range lines {
		var event JSONLEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil || event.Event != wantEvents[index] {
			t.Fatalf("jsonl event %d=%+v err=%v raw=%q", index, event, err, line)
		}
		if event.Event == string(maildomain.WatchEventSync) {
			payload, ok := event.Payload.(map[string]any)
			if !ok {
				// Payload is decoded as interface{} only when unmarshalling a raw
				// JSONLEvent, so verify durable sync truth from the raw object too.
				var raw map[string]any
				if err := json.Unmarshal([]byte(line), &raw); err != nil || !strings.Contains(line, `"fetched":1`) || !strings.Contains(line, `"created":1`) {
					t.Fatalf("jsonl sync truth payload=%+v raw=%q", raw, line)
				}
			} else if payload == nil {
				t.Fatalf("jsonl sync payload missing: %q", line)
			}
		}
	}
}
