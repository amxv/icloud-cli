package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type draftTestMailService struct {
	unavailableMailService
	values      []maildomain.Draft
	show        maildomain.Draft
	sendCalls   int
	deleteCalls int
}

func (service *draftTestMailService) MailDraftList(context.Context, string, string, int) (account.Account, []maildomain.Draft, bool, int64, error) {
	return account.Account{ID: "acct_test"}, append([]maildomain.Draft(nil), service.values...), false, 18, nil
}

func (service *draftTestMailService) MailDraftShow(context.Context, string, string, string) (account.Account, maildomain.Draft, maildomain.ContentSource, int64, error) {
	return account.Account{ID: "acct_test"}, service.show, nil, 18, nil
}

func (service *draftTestMailService) MailDraftSend(context.Context, string, string, string) (account.Account, maildomain.Draft, maildomain.OutboundAttempt, bool, int64, error) {
	service.sendCalls++
	return account.Account{ID: "acct_test"}, service.show, maildomain.OutboundAttempt{ID: "out_test", Status: maildomain.OutboundSent}, false, 18, nil
}

func (service *draftTestMailService) MailDraftDelete(context.Context, string, string, string) (account.Account, maildomain.Draft, int, int64, error) {
	service.deleteCalls++
	return account.Account{ID: "acct_test"}, service.show, 1, 18, nil
}

func executeDraftCLITest(t *testing.T, service MailService, stdin string, args ...string) (Result, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	result := Execute(context.Background(), args, Config{
		Version: "test", SchemaVersion: 18, Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr, Mail: service,
	})
	return result, stdout.String(), stderr.String()
}

func privateDraftFixture() maildomain.Draft {
	now := time.Date(2026, 8, 7, 2, 0, 0, 0, time.UTC)
	return maildomain.Draft{
		ID: "drf_0198d704-6c00-7000-8000-000000000001", AccountID: "acct_test", MessageID: "msg_0198d704-6c00-7000-8000-000000000001",
		Revision: 2, SyncState: maildomain.DraftSynced, RemoteLocationID: "loc_remote", CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
		Message: maildomain.Message{
			ID: "msg_0198d704-6c00-7000-8000-000000000001", AccountID: "acct_test", Direction: maildomain.DirectionOutbound,
			To: []maildomain.Address{{Email: "visible@example.test"}}, BCC: []maildomain.Address{{Email: "hidden-bcc@example.test"}},
			Subject: "Private draft", Snippet: "bounded draft excerpt", AttachmentCount: 1,
			Locations: []maildomain.Location{{ID: "loc_remote", FolderKind: "drafts", Present: true}},
		},
	}
}

func TestDraftListCompactAndJSONHideBCCAddresses(t *testing.T) {
	value := privateDraftFixture()
	service := &draftTestMailService{values: []maildomain.Draft{value}, show: value}
	result, stdout, stderr := executeDraftCLITest(t, service, "", "mail", "draft", "list")
	if result.ExitCode != 0 || stderr != "" {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	if strings.Contains(stdout, "hidden-bcc@example.test") || !strings.Contains(stdout, "bcc=1") || !strings.Contains(stdout, "subject=\"Private draft\"") {
		t.Fatalf("compact draft privacy/output = %q", stdout)
	}
	if lines, size := strings.Count(stdout, "\n"), len(stdout); lines > 14 || size > 420 {
		t.Fatalf("compact draft budget lines=%d bytes=%d output=%q", lines, size, stdout)
	}
	result, stdout, stderr = executeDraftCLITest(t, service, "", "mail", "draft", "list", "--json")
	if result.ExitCode != 0 || stderr != "" || strings.Contains(stdout, "hidden-bcc@example.test") {
		t.Fatalf("JSON result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Drafts []struct {
				BCCCount int `json:"bcc_count"`
				Message  struct {
					BCC []maildomain.Address `json:"bcc"`
				} `json:"message"`
			} `json:"drafts"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil || !envelope.OK || len(envelope.Data.Drafts) != 1 || envelope.Data.Drafts[0].BCCCount != 1 || len(envelope.Data.Drafts[0].Message.BCC) != 0 {
		t.Fatalf("JSON envelope=%+v err=%v raw=%q", envelope, err, stdout)
	}
}

func TestDraftSendAndDeleteRequireExplicitNonTTYConfirmation(t *testing.T) {
	value := privateDraftFixture()
	service := &draftTestMailService{show: value}
	result, stdout, stderr := executeDraftCLITest(t, service, "", "mail", "draft", "send", value.ID)
	if result.ExitCode != ExitSafety || stdout != "" || service.sendCalls != 0 || !strings.Contains(stderr, "requires --yes") {
		t.Fatalf("send result=%+v stdout=%q stderr=%q calls=%d", result, stdout, stderr, service.sendCalls)
	}
	result, stdout, stderr = executeDraftCLITest(t, service, "", "mail", "draft", "delete", value.ID)
	if result.ExitCode != ExitSafety || stdout != "" || service.deleteCalls != 0 || !strings.Contains(stderr, "requires --yes") {
		t.Fatalf("delete result=%+v stdout=%q stderr=%q calls=%d", result, stdout, stderr, service.deleteCalls)
	}
}

func TestDraftDeleteDryRunReportsScopeWithoutMutation(t *testing.T) {
	value := privateDraftFixture()
	service := &draftTestMailService{show: value}
	result, stdout, stderr := executeDraftCLITest(t, service, "", "mail", "draft", "delete", value.ID, "--dry-run")
	if result.ExitCode != 0 || stderr != "" || service.deleteCalls != 0 || !strings.Contains(stdout, "provider_copies=1") || !strings.Contains(stdout, "no local or provider draft was deleted") {
		t.Fatalf("result=%+v stdout=%q stderr=%q calls=%d", result, stdout, stderr, service.deleteCalls)
	}
}

func TestDraftCommandHelpExposesLifecycleAndSafetyControls(t *testing.T) {
	service := &draftTestMailService{}
	for _, test := range []struct {
		args []string
		want []string
	}{
		{[]string{"mail", "draft", "--help"}, []string{"create", "update", "list", "show", "sync", "send", "delete"}},
		{[]string{"mail", "draft", "create", "--help"}, []string{"--to", "--bcc", "--attach", "may be empty"}},
		{[]string{"mail", "draft", "show", "--help"}, []string{"--format", "raw", "--body-only", "--headers"}},
		{[]string{"mail", "draft", "sync", "--help"}, []string{"--folder", "--limit", "provider Drafts"}},
		{[]string{"mail", "draft", "send", "--help"}, []string{"--dry-run", "--yes", "idempotent"}},
		{[]string{"mail", "draft", "delete", "--help"}, []string{"--dry-run", "--yes", "provider"}},
	} {
		result, stdout, stderr := executeDraftCLITest(t, service, "", test.args...)
		if result.ExitCode != 0 || stderr != "" {
			t.Fatalf("help %v result=%+v stdout=%q stderr=%q", test.args, result, stdout, stderr)
		}
		for _, want := range test.want {
			if !strings.Contains(stdout, want) {
				t.Fatalf("help %v missing %q: %q", test.args, want, stdout)
			}
		}
	}
}
