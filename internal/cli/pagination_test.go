package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type paginationAccountPageService struct {
	unavailableAccountService
	cursor   string
	terminal bool
}

func (service *paginationAccountPageService) ListPage(_ context.Context, _ string, _ int, cursor string) ([]account.Account, bool, string, int64, error) {
	service.cursor = cursor
	if service.terminal {
		return []account.Account{{ID: "acct_pagination", Name: "Pagination", Email: "pagination@example.test", IMAPUsername: "pagination"}}, false, "", 19, nil
	}
	return []account.Account{{ID: "acct_pagination", Name: "Pagination", Email: "pagination@example.test", IMAPUsername: "pagination"}}, true, "acur_next", 19, nil
}

type paginationMailPageService struct {
	unavailableMailService
	attachmentCursor string
	outboundCursor   string
	mutationCursor   string
	checkpointCursor string
	terminal         bool
}

func (service *paginationMailPageService) pageCursor(next string) (bool, string) {
	if service.terminal {
		return false, ""
	}
	return true, next
}

func (service *paginationMailPageService) MailAttachmentsPage(_ context.Context, _, _, selector, cursor string, _ int) (account.Account, maildomain.AttachmentTarget, []maildomain.Attachment, bool, string, int64, error) {
	service.attachmentCursor = cursor
	more, next := service.pageCursor("atcur_next")
	return account.Account{ID: "acct_pagination"}, maildomain.AttachmentTarget{Kind: "message", ID: selector}, []maildomain.Attachment{{ID: "att_pagination", MessageID: selector, PartID: "prt_pagination", Ordinal: 1, Availability: maildomain.AttachmentUnavailable}}, more, next, 19, nil
}

func (service *paginationMailPageService) MailOutboundStatusPage(_ context.Context, _, _, cursor string, _ int) (account.Account, []maildomain.OutboundAttempt, bool, string, int64, error) {
	service.outboundCursor = cursor
	more, next := service.pageCursor("ocur_next")
	return account.Account{ID: "acct_pagination"}, []maildomain.OutboundAttempt{{ID: "send_pagination", Status: maildomain.OutboundFailed, CreatedAt: time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)}}, more, next, 19, nil
}

func (service *paginationMailPageService) MailLocationMutationStatusPage(_ context.Context, _, _, cursor string, _ int) (account.Account, []maildomain.LocationMutation, bool, string, int64, error) {
	service.mutationCursor = cursor
	more, next := service.pageCursor("mcur_next")
	return account.Account{ID: "acct_pagination"}, []maildomain.LocationMutation{{ID: "mut_pagination", AccountID: "acct_pagination", MessageID: "msg_pagination", Action: maildomain.LocationActionDelete, Status: maildomain.LocationMutationPending}}, more, next, 19, nil
}

func (service *paginationMailPageService) MailSyncCheckpointsPage(_ context.Context, _, _, cursor string, _ int) (account.Account, []maildomain.Checkpoint, bool, string, int64, error) {
	service.checkpointCursor = cursor
	more, next := service.pageCursor("ccur_next")
	return account.Account{ID: "acct_pagination"}, []maildomain.Checkpoint{{ID: "chk_pagination", AccountID: "acct_pagination", FolderID: "fld_pagination", State: maildomain.CheckpointIdle}}, more, next, 19, nil
}

func TestPublicListsForwardCursorAndExposeContinuation(t *testing.T) {
	accounts := &paginationAccountPageService{}
	var stdout, stderr bytes.Buffer
	result := Execute(context.Background(), []string{"account", "list", "--limit", "1", "--cursor", "acur_resume", "--json"}, Config{
		Version: "test", SchemaVersion: 19, Stdout: &stdout, Stderr: &stderr, Accounts: accounts,
		RequestIDGen: func() string { return "req_pagination" },
	})
	if result.ExitCode != 0 || stderr.String() != "" || accounts.cursor != "acur_resume" || !strings.Contains(stdout.String(), `"next_cursor":"acur_next"`) {
		t.Fatalf("account page result=%+v cursor=%q stdout=%q stderr=%q", result, accounts.cursor, stdout.String(), stderr.String())
	}

	mail := &paginationMailPageService{}
	tests := []struct {
		name       string
		args       []string
		wantCursor *string
		wantInput  string
		wantNext   string
	}{
		{name: "attachments", args: []string{"mail", "attachments", "list", "msg_pagination", "--cursor", "atcur_resume", "--json"}, wantCursor: &mail.attachmentCursor, wantInput: "atcur_resume", wantNext: "atcur_next"},
		{name: "outbound", args: []string{"mail", "status", "--outbound", "--cursor", "ocur_resume", "--json"}, wantCursor: &mail.outboundCursor, wantInput: "ocur_resume", wantNext: "ocur_next"},
		{name: "mutations", args: []string{"mail", "status", "--mutations", "--cursor", "mcur_resume", "--json"}, wantCursor: &mail.mutationCursor, wantInput: "mcur_resume", wantNext: "mcur_next"},
		{name: "checkpoints", args: []string{"mail", "sync", "checkpoints", "--cursor", "ccur_resume", "--json"}, wantCursor: &mail.checkpointCursor, wantInput: "ccur_resume", wantNext: "ccur_next"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, output, errOutput := executeWithMail(t, mail, "", test.args...)
			if result.ExitCode != 0 || errOutput != "" || *test.wantCursor != test.wantInput || !strings.Contains(output, `"next_cursor":"`+test.wantNext+`"`) {
				t.Fatalf("result=%+v cursor=%q stdout=%q stderr=%q", result, *test.wantCursor, output, errOutput)
			}
		})
	}
}

func TestPublicListsOmitContinuationOnTerminalPage(t *testing.T) {
	accounts := &paginationAccountPageService{terminal: true}
	var stdout, stderr bytes.Buffer
	result := Execute(context.Background(), []string{"account", "list", "--json"}, Config{
		Version: "test", SchemaVersion: 19, Stdout: &stdout, Stderr: &stderr, Accounts: accounts,
		RequestIDGen: func() string { return "req_pagination_terminal" },
	})
	if result.ExitCode != 0 || stderr.String() != "" || strings.Contains(stdout.String(), `"next_cursor"`) {
		t.Fatalf("account terminal result=%+v stdout=%q stderr=%q", result, stdout.String(), stderr.String())
	}

	mail := &paginationMailPageService{terminal: true}
	for _, args := range [][]string{
		{"mail", "attachments", "list", "msg_pagination", "--json"},
		{"mail", "status", "--outbound", "--json"},
		{"mail", "status", "--mutations", "--json"},
		{"mail", "sync", "checkpoints", "--json"},
	} {
		mailResult, output, errOutput := executeWithMail(t, mail, "", args...)
		if mailResult.ExitCode != 0 || errOutput != "" || strings.Contains(output, `"next_cursor"`) {
			t.Fatalf("args=%v result=%+v stdout=%q stderr=%q", args, mailResult, output, errOutput)
		}
	}
}
