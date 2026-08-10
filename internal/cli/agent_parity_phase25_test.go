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

type phase25SenderMailService struct {
	unavailableMailService
	listPage      maildomain.SenderPreferencePage
	searchOptions maildomain.SearchOptions
	setVIPCalls   []phase25SenderToggle
	setBlockCalls []phase25SenderToggle
	showEmail     string
	listCursor    string
	listLimit     int
}

type phase25SenderToggle struct {
	email string
	value bool
}

func (service *phase25SenderMailService) MailSendersList(_ context.Context, _, _, cursor string, limit int) (account.Account, maildomain.SenderPreferencePage, int64, error) {
	service.listCursor = cursor
	service.listLimit = limit
	return account.Account{ID: "acct_phase25"}, service.listPage, 32, nil
}

func (service *phase25SenderMailService) MailSenderShow(_ context.Context, _, _, email string) (account.Account, maildomain.SenderPreference, int64, error) {
	service.showEmail = email
	return account.Account{ID: "acct_phase25"}, maildomain.SenderPreference{
		AccountID: "acct_phase25", Email: strings.ToLower(strings.TrimSpace(email)), VIP: true,
	}, 32, nil
}

func (service *phase25SenderMailService) MailSenderSetVIP(_ context.Context, _, _, email string, value bool) (account.Account, maildomain.SenderPreference, int64, error) {
	service.setVIPCalls = append(service.setVIPCalls, phase25SenderToggle{email: email, value: value})
	return account.Account{ID: "acct_phase25"}, maildomain.SenderPreference{
		AccountID: "acct_phase25", Email: strings.ToLower(strings.TrimSpace(email)), VIP: value,
	}, 32, nil
}

func (service *phase25SenderMailService) MailSenderSetBlocked(_ context.Context, _, _, email string, value bool) (account.Account, maildomain.SenderPreference, int64, error) {
	service.setBlockCalls = append(service.setBlockCalls, phase25SenderToggle{email: email, value: value})
	return account.Account{ID: "acct_phase25"}, maildomain.SenderPreference{
		AccountID: "acct_phase25", Email: strings.ToLower(strings.TrimSpace(email)), Blocked: value,
	}, 32, nil
}

func (service *phase25SenderMailService) MailSearch(_ context.Context, _, _, folderSelector string, options maildomain.SearchOptions) (account.Account, folder.Folder, maildomain.SearchPage, int64, error) {
	service.searchOptions = options
	message := maildomain.Message{
		ID: "msg_phase25", AccountID: "acct_phase25", From: maildomain.Address{Email: "vip@example.test"},
		Subject: "Phase 25", SenderPreference: &maildomain.SenderPreferenceState{Email: "vip@example.test", Explicit: true, VIP: true, Blocked: true},
	}
	return account.Account{ID: "acct_phase25"}, folder.Folder{ID: "fld_phase25", Name: folderSelector, Kind: folder.KindInbox}, maildomain.SearchPage{
		Hits:  []maildomain.SearchHit{{Kind: maildomain.SearchMessages, Message: &message}},
		Query: maildomain.SearchQueryMeta{ResultKind: maildomain.SearchMessages, Sort: "newest", IndexState: "ready"},
	}, 32, nil
}

func TestPhase25SenderCommandsExposeLocalOnlyStateAndStrictToggleInput(t *testing.T) {
	service := &phase25SenderMailService{listPage: maildomain.SenderPreferencePage{
		Preferences: []maildomain.SenderPreference{{AccountID: "acct_phase25", Email: "vip@example.test", VIP: true}},
		More:        true, NextCursor: "spcur_next",
	}}

	result, stdout, stderr := executeWithMail(t, service, "", "mail", "senders", "list", "--cursor", "spcur_prev", "--limit", "1", "--json")
	if result.ExitCode != 0 || stderr != "" || service.listCursor != "spcur_prev" || service.listLimit != 1 {
		t.Fatalf("list result=%+v cursor=%q limit=%d stdout=%q stderr=%q", result, service.listCursor, service.listLimit, stdout, stderr)
	}
	var listed struct {
		OK   bool `json:"ok"`
		Data struct {
			LocalOnly  bool   `json:"local_only"`
			More       bool   `json:"more"`
			NextCursor string `json:"next_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil || !listed.OK || !listed.Data.LocalOnly || !listed.Data.More || listed.Data.NextCursor != "spcur_next" {
		t.Fatalf("listed=%+v err=%v raw=%q", listed, err, stdout)
	}

	input := `{"email":"Alerts@Example.TEST"}`
	result, stdout, stderr = executeWithMail(t, service, input, "mail", "senders", "vip", "--input-json", "-", "--json")
	if result.ExitCode != 0 || stderr != "" || len(service.setVIPCalls) != 1 || service.setVIPCalls[0] != (phase25SenderToggle{email: "Alerts@Example.TEST", value: true}) {
		t.Fatalf("vip result=%+v calls=%+v stdout=%q stderr=%q", result, service.setVIPCalls, stdout, stderr)
	}
	var toggled struct {
		OK   bool `json:"ok"`
		Data struct {
			LocalOnly bool `json:"local_only"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &toggled); err != nil || !toggled.OK || !toggled.Data.LocalOnly {
		t.Fatalf("toggled=%+v err=%v raw=%q", toggled, err, stdout)
	}

	result, stdout, stderr = executeWithMail(t, service, `{"email":"x@example.test","unexpected":true}`, "mail", "senders", "block", "--input-json", "-", "--json")
	if result.ExitCode != ExitUsage || stdout != "" || len(service.setBlockCalls) != 0 || !strings.Contains(stderr, "invalid --input-json request") {
		t.Fatalf("strict result=%+v block_calls=%+v stdout=%q stderr=%q", result, service.setBlockCalls, stdout, stderr)
	}
}

func TestPhase25MessageListFiltersAndProjectsPreferenceContext(t *testing.T) {
	service := &phase25SenderMailService{}
	input := `{"vip":true,"blocked":true,"limit":5}`
	result, stdout, stderr := executeWithMail(t, service, input, "mail", "messages", "list", "--input-json", "-", "--json")
	if result.ExitCode != 0 || stderr != "" || service.searchOptions.VIP == nil || !*service.searchOptions.VIP || service.searchOptions.Blocked == nil || !*service.searchOptions.Blocked {
		t.Fatalf("result=%+v options=%+v stdout=%q stderr=%q", result, service.searchOptions, stdout, stderr)
	}
	if !strings.Contains(stdout, `"sender_preference":{"email":"vip@example.test","explicit":true,"vip":true,"blocked":true}`) {
		t.Fatalf("JSON message projection omitted sender preference: %q", stdout)
	}

	service = &phase25SenderMailService{}
	result, stdout, stderr = executeWithMail(t, service, "", "mail", "messages", "list", "--vip", "--blocked")
	if result.ExitCode != 0 || stderr != "" || !strings.Contains(stdout, "vip=true") || !strings.Contains(stdout, "blocked=true") {
		t.Fatalf("compact result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
}

func TestPhase25ThreadPreferenceSummaryIsVisibleWithoutPayloadExpansion(t *testing.T) {
	when := time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)
	thread := maildomain.Thread{
		ID: "thr_phase25", CanonicalSubject: "Phase 25 thread", MessageCount: 3, LastMessageAt: when,
		SenderPreferences: &maildomain.ThreadSenderPreferenceSummary{DistinctSenders: 3, ExplicitSenders: 2, VIPSenders: 1, BlockedSenders: 1, UnpreferredSenders: 1},
	}
	var compact strings.Builder
	if err := renderThreadSummary(NewCompactTextRenderer(&compact), thread); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compact.String(), "vip_senders=1") || !strings.Contains(compact.String(), "blocked_senders=1") {
		t.Fatalf("thread compact omitted sender preference summary: %q", compact.String())
	}

	public, err := buildPublicThread(context.Background(), thread, nil, OutputOptions{}, maildomain.ContentText)
	if err != nil || public.SenderPreferences == nil || public.SenderPreferences.VIPSenders != 1 || public.SenderPreferences.BlockedSenders != 1 {
		t.Fatalf("public thread=%+v err=%v", public, err)
	}
}

func TestPhase25RemoteSearchRejectsLocalPreferenceFiltersBeforeProviderCall(t *testing.T) {
	service := &phase25SenderMailService{}
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "search", "--remote", "--folder", "inbox", "--vip")
	if result.ExitCode != ExitUsage || stdout != "" || !strings.Contains(stderr, "local sender-preference filters") {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
}
