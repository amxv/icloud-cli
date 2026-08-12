package cli

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type structuredReadAccountService struct {
	unavailableAccountService
	listCalls int
	limit     int
	cursor    string
}

func (service *structuredReadAccountService) ListPage(_ context.Context, _ string, limit int, cursor string) ([]account.Account, bool, string, int64, error) {
	service.listCalls++
	service.limit = limit
	service.cursor = cursor
	return []account.Account{{ID: "acct_read", Name: "Read"}}, false, "", 19, nil
}

type structuredReadFolderService struct {
	unavailableFolderService
	listCalls   int
	showCalls   int
	statusCalls int
	showTarget  string
}

func (service *structuredReadFolderService) FolderList(context.Context, string, string, int, bool) (account.Account, []folder.Folder, bool, int64, error) {
	service.listCalls++
	return account.Account{ID: "acct_read"}, []folder.Folder{{ID: "fld_inbox", Name: "Inbox", Kind: folder.KindInbox, Present: true}}, false, 19, nil
}

func (service *structuredReadFolderService) FolderShow(_ context.Context, _, _, selector string) (account.Account, folder.Folder, int64, error) {
	service.showCalls++
	service.showTarget = selector
	return account.Account{ID: "acct_read"}, folder.Folder{ID: "fld_read", Name: selector, WireName: selector, Kind: folder.KindOther, Present: true}, 19, nil
}

func (service *structuredReadFolderService) FolderStatus(context.Context, string, string) (account.Account, []folder.Folder, int64, error) {
	service.statusCalls++
	return account.Account{ID: "acct_read"}, []folder.Folder{{ID: "fld_inbox", Name: "Inbox", Kind: folder.KindInbox, Present: true}}, 19, nil
}

type structuredReadContentSource struct {
	bodyCalls int
	rawCalls  int
}

func (source *structuredReadContentSource) WriteRaw(_ context.Context, writer io.Writer) error {
	source.rawCalls++
	_, err := io.WriteString(writer, "PRIVATE-RAW-PAYLOAD")
	return err
}

func (source *structuredReadContentSource) WriteBody(_ context.Context, _ maildomain.ContentFormat, writer io.Writer) error {
	source.bodyCalls++
	_, err := io.WriteString(writer, "PRIVATE-BODY-PAYLOAD")
	return err
}

type structuredReadMailService struct {
	unavailableMailService
	searchCalls       int
	remoteSearchCalls int
	searchFolder      string
	searchAllFolders  bool
	searchOptions     maildomain.SearchOptions
	threadCalls       int
	threadTarget      string
	threadOptions     maildomain.ThreadReadOptions
	messageCalls      int
	messageTarget     string
	attachmentsCalls  int
	attachmentTarget  string
	attachmentCursor  string
	attachmentLimit   int
	syncStatusCalls   int
	syncFolder        string
	checkpointCalls   int
	checkpointCursor  string
	checkpointLimit   int
	outboundCalls     int
	mutationCalls     int
	operationCursor   string
	operationLimit    int
	content           *structuredReadContentSource
}

func (service *structuredReadMailService) MailSearch(_ context.Context, _, _, folderSelector string, options maildomain.SearchOptions) (account.Account, folder.Folder, maildomain.SearchPage, int64, error) {
	service.searchCalls++
	service.searchFolder = folderSelector
	service.searchOptions = options
	page := maildomain.SearchPage{Query: maildomain.SearchQueryMeta{ResultKind: options.ResultKind, Sort: "newest", IndexState: "ready"}}
	if options.ResultKind == maildomain.SearchThreads {
		thread := maildomain.Thread{ID: "thr_read", AccountID: "acct_read", CanonicalSubject: "Read thread", Status: "complete", Reconstruction: "rfc", ProjectionState: "ready"}
		page.Hits = []maildomain.SearchHit{{Kind: maildomain.SearchThreads, Thread: &thread}}
	} else {
		message := maildomain.Message{ID: "msg_read", AccountID: "acct_read", Subject: "Read message", From: maildomain.Address{Email: "sender@example.test"}}
		page.Hits = []maildomain.SearchHit{{Kind: maildomain.SearchMessages, Message: &message}}
	}
	return account.Account{ID: "acct_read"}, folder.Folder{ID: "fld_read", Name: folderSelector, Kind: folder.KindOther}, page, 19, nil
}

func (service *structuredReadMailService) MailRemoteSearch(_ context.Context, _, _, folderSelector string, allFolders bool, options maildomain.SearchOptions) (account.Account, maildomain.RemoteSearchPage, int64, error) {
	service.remoteSearchCalls++
	service.searchFolder = folderSelector
	service.searchAllFolders = allFolders
	service.searchOptions = options
	return account.Account{ID: "acct_read"}, maildomain.RemoteSearchPage{Count: 0, Folders: 1}, 19, nil
}

func (service *structuredReadMailService) MailThread(_ context.Context, _, _, target string, options maildomain.ThreadReadOptions) (account.Account, maildomain.ThreadPage, []maildomain.ContentSource, int64, error) {
	service.threadCalls++
	service.threadTarget = target
	service.threadOptions = options
	return account.Account{ID: "acct_read"}, maildomain.ThreadPage{Thread: maildomain.Thread{
		ID: target, AccountID: "acct_read", CanonicalSubject: "Read thread", Status: "complete", Reconstruction: "rfc", ProjectionState: "ready",
	}}, nil, 19, nil
}

func (service *structuredReadMailService) MailMessage(_ context.Context, _, _, target string) (account.Account, maildomain.Message, maildomain.ContentSource, int64, error) {
	service.messageCalls++
	service.messageTarget = target
	if service.content == nil {
		service.content = &structuredReadContentSource{}
	}
	message := maildomain.Message{
		ID: target, AccountID: "acct_read", Subject: "Read message", Snippet: "bounded excerpt",
		From:    maildomain.Address{Email: "sender@example.test"},
		Content: &maildomain.NormalizedContent{TextPaths: []string{"1"}, Headers: []maildomain.HeaderField{{Key: "Subject", Value: "Read message"}}},
	}
	return account.Account{ID: "acct_read"}, message, service.content, 19, nil
}

func (service *structuredReadMailService) MailAttachmentsPage(_ context.Context, _, _, target, cursor string, limit int) (account.Account, maildomain.AttachmentTarget, []maildomain.Attachment, bool, string, int64, error) {
	service.attachmentsCalls++
	service.attachmentTarget = target
	service.attachmentCursor = cursor
	service.attachmentLimit = limit
	return account.Account{ID: "acct_read"}, maildomain.AttachmentTarget{Kind: "message", ID: target}, nil, false, "", 19, nil
}

func (service *structuredReadMailService) MailSyncStatus(_ context.Context, _, _, folderSelector string) (account.Account, folder.Folder, maildomain.Checkpoint, int64, error) {
	service.syncStatusCalls++
	service.syncFolder = folderSelector
	return account.Account{ID: "acct_read"}, folder.Folder{ID: "fld_read", Name: folderSelector}, maildomain.Checkpoint{ID: "chk_read", AccountID: "acct_read", FolderID: "fld_read", State: maildomain.CheckpointIdle}, 19, nil
}

func (service *structuredReadMailService) MailSyncCheckpointsPage(_ context.Context, _, _, cursor string, limit int) (account.Account, []maildomain.Checkpoint, bool, string, int64, error) {
	service.checkpointCalls++
	service.checkpointCursor = cursor
	service.checkpointLimit = limit
	return account.Account{ID: "acct_read"}, nil, false, "", 19, nil
}

func (service *structuredReadMailService) MailOutboundStatusPage(_ context.Context, _, _, cursor string, limit int) (account.Account, []maildomain.OutboundAttempt, bool, string, int64, error) {
	service.outboundCalls++
	service.operationCursor = cursor
	service.operationLimit = limit
	return account.Account{ID: "acct_read"}, nil, false, "", 19, nil
}

func (service *structuredReadMailService) MailLocationMutationStatusPage(_ context.Context, _, _, cursor string, limit int) (account.Account, []maildomain.LocationMutation, bool, string, int64, error) {
	service.mutationCalls++
	service.operationCursor = cursor
	service.operationLimit = limit
	return account.Account{ID: "acct_read"}, nil, false, "", 19, nil
}

func executeStructuredRead(t *testing.T, stdin string, accounts AccountService, folders FolderService, mail MailService, args ...string) (Result, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if accounts == nil {
		accounts = unavailableAccountService{}
	}
	if folders == nil {
		folders = unavailableFolderService{}
	}
	if mail == nil {
		mail = unavailableMailService{}
	}
	result := Execute(context.Background(), args, Config{
		Version: "test", SchemaVersion: 19, Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
		Accounts: accounts, Folders: folders, Mail: mail, RequestIDGen: func() string { return "req_structured_read" },
	})
	return result, stdout.String(), stderr.String()
}

func TestMessageListReadJSONMatchesFlagRequest(t *testing.T) {
	flagService := &structuredReadMailService{}
	flagResult, flagOut, flagErr := executeStructuredRead(t, "", nil, nil, flagService,
		"mail", "messages", "list", "--folder", "sent", "--query", "invoice", "--from", "billing@example.test",
		"--after", "2026-08-01", "--unread", "--limit", "7", "--cursor", "cur_same", "--json")
	if flagResult.ExitCode != 0 || flagErr != "" {
		t.Fatalf("flag result=%+v stdout=%q stderr=%q", flagResult, flagOut, flagErr)
	}

	jsonService := &structuredReadMailService{}
	input := `{"folder":"sent","query":"invoice","from":"billing@example.test","after":"2026-08-01","unread":true,"limit":7,"cursor":"cur_same"}`
	jsonResult, jsonOut, jsonErr := executeStructuredRead(t, input, nil, nil, jsonService,
		"mail", "messages", "list", "--input-json", "-", "--json")
	if jsonResult.ExitCode != 0 || jsonErr != "" {
		t.Fatalf("json result=%+v stdout=%q stderr=%q", jsonResult, jsonOut, jsonErr)
	}
	if flagService.searchFolder != jsonService.searchFolder || !reflect.DeepEqual(flagService.searchOptions, jsonService.searchOptions) {
		t.Fatalf("flag folder/options=%q %+v json=%q %+v", flagService.searchFolder, flagService.searchOptions, jsonService.searchFolder, jsonService.searchOptions)
	}
	if flagOut != jsonOut {
		t.Fatalf("flag/json output diverged\nflag=%s\njson=%s", flagOut, jsonOut)
	}
}

func TestSearchReadJSONMatchesLocalAndRemoteFlags(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		flags []string
	}{
		{
			name:  "local",
			input: `{"query":"launch","folder":"inbox","participant":"team@example.test","result":"threads","limit":9}`,
			flags: []string{"mail", "search", "launch", "--folder", "inbox", "--participant", "team@example.test", "--result", "threads", "--limit", "9", "--json"},
		},
		{
			name:  "remote",
			input: `{"query":"launch","folder":"inbox","subject":"roadmap","remote":true,"limit":9}`,
			flags: []string{"mail", "search", "launch", "--folder", "inbox", "--subject", "roadmap", "--remote", "--limit", "9", "--json"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			flagService := &structuredReadMailService{}
			flagResult, flagOut, flagErr := executeStructuredRead(t, "", nil, nil, flagService, test.flags...)
			if flagResult.ExitCode != 0 || flagErr != "" {
				t.Fatalf("flag result=%+v stdout=%q stderr=%q", flagResult, flagOut, flagErr)
			}
			jsonService := &structuredReadMailService{}
			jsonResult, jsonOut, jsonErr := executeStructuredRead(t, test.input, nil, nil, jsonService, "mail", "search", "--input-json", "-", "--json")
			if jsonResult.ExitCode != 0 || jsonErr != "" {
				t.Fatalf("json result=%+v stdout=%q stderr=%q", jsonResult, jsonOut, jsonErr)
			}
			if flagService.searchFolder != jsonService.searchFolder || flagService.searchAllFolders != jsonService.searchAllFolders || !reflect.DeepEqual(flagService.searchOptions, jsonService.searchOptions) {
				t.Fatalf("flag request=%q %t %+v json=%q %t %+v", flagService.searchFolder, flagService.searchAllFolders, flagService.searchOptions, jsonService.searchFolder, jsonService.searchAllFolders, jsonService.searchOptions)
			}
			if flagOut != jsonOut {
				t.Fatalf("flag/json output diverged\nflag=%s\njson=%s", flagOut, jsonOut)
			}
		})
	}
}

func TestThreadAndFolderReadJSONMatchFlags(t *testing.T) {
	flagMail := &structuredReadMailService{}
	flagResult, flagOut, flagErr := executeStructuredRead(t, "", nil, nil, flagMail,
		"mail", "threads", "show", "thr_read", "--limit", "3", "--cursor", "tcur_same", "--metadata-only", "--json")
	if flagResult.ExitCode != 0 || flagErr != "" {
		t.Fatalf("thread flag result=%+v stdout=%q stderr=%q", flagResult, flagOut, flagErr)
	}
	jsonMail := &structuredReadMailService{}
	jsonResult, jsonOut, jsonErr := executeStructuredRead(t,
		`{"target":"thr_read","limit":3,"cursor":"tcur_same","metadata_only":true}`,
		nil, nil, jsonMail, "mail", "threads", "show", "--input-json", "-", "--json")
	if jsonResult.ExitCode != 0 || jsonErr != "" || jsonMail.threadCalls != 1 || jsonMail.threadTarget != "thr_read" {
		t.Fatalf("thread JSON result=%+v calls=%d target=%q stdout=%q stderr=%q", jsonResult, jsonMail.threadCalls, jsonMail.threadTarget, jsonOut, jsonErr)
	}
	if !reflect.DeepEqual(flagMail.threadOptions, jsonMail.threadOptions) || flagOut != jsonOut {
		t.Fatalf("thread flag/JSON diverged options flag=%+v json=%+v\nflag=%s\njson=%s", flagMail.threadOptions, jsonMail.threadOptions, flagOut, jsonOut)
	}
	if jsonMail.threadOptions.RequireSource || jsonMail.threadOptions.HydrateContent {
		t.Fatalf("metadata-only structured thread read expanded content: %+v", jsonMail.threadOptions)
	}

	flagFolders := &structuredReadFolderService{}
	flagResult, flagOut, flagErr = executeStructuredRead(t, "", nil, flagFolders, nil,
		"mail", "folders", "show", "Receipts", "--verbose")
	if flagResult.ExitCode != 0 || flagErr != "" {
		t.Fatalf("folder flag result=%+v stdout=%q stderr=%q", flagResult, flagOut, flagErr)
	}
	jsonFolders := &structuredReadFolderService{}
	jsonResult, jsonOut, jsonErr = executeStructuredRead(t, `{"folder":"Receipts","verbose":true}`, nil, jsonFolders, nil,
		"mail", "folders", "show", "--input-json", "-")
	if jsonResult.ExitCode != 0 || jsonErr != "" || jsonFolders.showCalls != 1 || jsonFolders.showTarget != "Receipts" || !strings.Contains(jsonOut, "wire_name") {
		t.Fatalf("folder JSON result=%+v calls=%d target=%q stdout=%q stderr=%q", jsonResult, jsonFolders.showCalls, jsonFolders.showTarget, jsonOut, jsonErr)
	}
	if flagOut != jsonOut {
		t.Fatalf("folder flag/JSON output diverged\nflag=%s\njson=%s", flagOut, jsonOut)
	}
}

func TestMessageReadJSONKeepsContentExpansionExplicit(t *testing.T) {
	service := &structuredReadMailService{content: &structuredReadContentSource{}}
	result, stdout, stderr := executeStructuredRead(t, `{"message_id":"msg_private"}`, nil, nil, service,
		"mail", "message", "show", "--input-json", "-", "--json")
	if result.ExitCode != 0 || stderr != "" || service.content.bodyCalls != 0 || service.content.rawCalls != 0 {
		t.Fatalf("default result=%+v body=%d raw=%d stdout=%q stderr=%q", result, service.content.bodyCalls, service.content.rawCalls, stdout, stderr)
	}
	if strings.Contains(stdout, "PRIVATE-BODY-PAYLOAD") || strings.Contains(stdout, "PRIVATE-RAW-PAYLOAD") || strings.Contains(stdout, `"text_body"`) || strings.Contains(stdout, `"raw_base64"`) {
		t.Fatalf("default structured read expanded private content: %q", stdout)
	}

	service = &structuredReadMailService{content: &structuredReadContentSource{}}
	result, stdout, stderr = executeStructuredRead(t, `{"message_id":"msg_private","full":true}`, nil, nil, service,
		"mail", "message", "show", "--input-json", "-", "--json")
	if result.ExitCode != 0 || stderr != "" || service.content.bodyCalls != 1 || !strings.Contains(stdout, "PRIVATE-BODY-PAYLOAD") {
		t.Fatalf("explicit full result=%+v body=%d stdout=%q stderr=%q", result, service.content.bodyCalls, stdout, stderr)
	}
}

func TestStrictReadJSONRejectsBeforeServiceWork(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
	}{
		{"unknown", `{"limit":20,"mystery":true}`},
		{"trailing", `{"limit":20}{"limit":20}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			folders := &structuredReadFolderService{}
			result, stdout, stderr := executeStructuredRead(t, test.input, nil, folders, nil,
				"mail", "folders", "refresh", "--input-json", "-")
			if result.ExitCode != ExitUsage || stdout != "" || folders.statusCalls != 0 || !strings.Contains(stderr, "code=usage") {
				t.Fatalf("result=%+v calls=%d stdout=%q stderr=%q", result, folders.statusCalls, stdout, stderr)
			}
		})
	}
}

func TestReadJSONSizeBoundRejectsBeforeServiceWork(t *testing.T) {
	service := &structuredReadMailService{}
	input := `{"query":"` + strings.Repeat("x", MaximumJSONInputBytes) + `"}`
	result, stdout, stderr := executeStructuredRead(t, input, nil, nil, service,
		"mail", "search", "--input-json", "-")
	if result.ExitCode != ExitSafety || stdout != "" || service.searchCalls != 0 || service.remoteSearchCalls != 0 || !strings.Contains(stderr, "exceeds") {
		t.Fatalf("result=%+v local=%d remote=%d stdout=%q stderr=%q", result, service.searchCalls, service.remoteSearchCalls, stdout, stderr)
	}
}

func TestReadJSONZeroLimitsAndMixedControlsUseNormalValidation(t *testing.T) {
	service := &structuredReadMailService{}
	result, stdout, stderr := executeStructuredRead(t, `{"limit":0}`, nil, nil, service,
		"mail", "messages", "list", "--input-json", "-")
	if result.ExitCode != ExitUsage || stdout != "" || service.searchCalls != 0 || !strings.Contains(stderr, "--limit must be between 1 and 100") {
		t.Fatalf("zero result=%+v calls=%d stdout=%q stderr=%q", result, service.searchCalls, stdout, stderr)
	}

	service = &structuredReadMailService{}
	result, stdout, stderr = executeStructuredRead(t, `{"query":"invoice","limit":3}`, nil, nil, service,
		"mail", "search", "--input-json", "-", "--limit", "3")
	if result.ExitCode != ExitUsage || stdout != "" || service.searchCalls != 0 || !strings.Contains(stderr, "--limit cannot be combined with --input-json") {
		t.Fatalf("mixed flag result=%+v calls=%d stdout=%q stderr=%q", result, service.searchCalls, stdout, stderr)
	}

	service = &structuredReadMailService{}
	result, stdout, stderr = executeStructuredRead(t, `{"query":"invoice"}`, nil, nil, service,
		"mail", "search", "invoice", "--input-json", "-")
	if result.ExitCode != ExitUsage || stdout != "" || service.searchCalls != 0 || !strings.Contains(stderr, "positional read arguments cannot be combined with --input-json") {
		t.Fatalf("stdin/positional result=%+v calls=%d stdout=%q stderr=%q", result, service.searchCalls, stdout, stderr)
	}

	service = &structuredReadMailService{}
	result, stdout, stderr = executeStructuredRead(t, `{"unread":true,"read":true}`, nil, nil, service,
		"mail", "search", "--input-json", "-")
	if result.ExitCode != ExitUsage || stdout != "" || service.searchCalls != 0 || !strings.Contains(stderr, "--unread and --read cannot be used together") {
		t.Fatalf("mutually exclusive result=%+v calls=%d stdout=%q stderr=%q", result, service.searchCalls, stdout, stderr)
	}
}

func TestOperationAndCheckpointReadJSONDispatch(t *testing.T) {
	service := &structuredReadMailService{}
	result, stdout, stderr := executeStructuredRead(t, `{"operation":"mutations","limit":4,"cursor":"mcur_same"}`, nil, nil, service,
		"mail", "status", "--input-json", "-", "--json")
	if result.ExitCode != 0 || stderr != "" || service.mutationCalls != 1 || service.outboundCalls != 0 || service.operationLimit != 4 || service.operationCursor != "mcur_same" {
		t.Fatalf("status result=%+v outbound=%d mutations=%d limit=%d cursor=%q stdout=%q stderr=%q", result, service.outboundCalls, service.mutationCalls, service.operationLimit, service.operationCursor, stdout, stderr)
	}

	service = &structuredReadMailService{}
	result, stdout, stderr = executeStructuredRead(t, `{"limit":6,"cursor":"ccur_same"}`, nil, nil, service,
		"mail", "sync", "checkpoints", "--input-json", "-")
	if result.ExitCode != 0 || stderr != "" || service.checkpointCalls != 1 || service.checkpointLimit != 6 || service.checkpointCursor != "ccur_same" {
		t.Fatalf("checkpoint result=%+v calls=%d limit=%d cursor=%q stdout=%q stderr=%q", result, service.checkpointCalls, service.checkpointLimit, service.checkpointCursor, stdout, stderr)
	}
}

func TestInventoryAndStatusReadJSONDispatch(t *testing.T) {
	accounts := &structuredReadAccountService{}
	result, stdout, stderr := executeStructuredRead(t, `{"limit":2,"cursor":"acur_same"}`, accounts, nil, nil,
		"account", "list", "--input-json", "-", "--json")
	if result.ExitCode != 0 || stderr != "" || accounts.listCalls != 1 || accounts.limit != 2 || accounts.cursor != "acur_same" {
		t.Fatalf("account result=%+v calls=%d limit=%d cursor=%q stdout=%q stderr=%q", result, accounts.listCalls, accounts.limit, accounts.cursor, stdout, stderr)
	}

	service := &structuredReadMailService{}
	result, stdout, stderr = executeStructuredRead(t, `{"target":"msg_read","limit":5,"cursor":"atcur_same"}`, nil, nil, service,
		"mail", "attachments", "list", "--input-json", "-")
	if result.ExitCode != 0 || stderr != "" || service.attachmentsCalls != 1 || service.attachmentTarget != "msg_read" || service.attachmentLimit != 5 || service.attachmentCursor != "atcur_same" {
		t.Fatalf("attachments result=%+v calls=%d target=%q limit=%d cursor=%q stdout=%q stderr=%q", result, service.attachmentsCalls, service.attachmentTarget, service.attachmentLimit, service.attachmentCursor, stdout, stderr)
	}

	service = &structuredReadMailService{}
	result, stdout, stderr = executeStructuredRead(t, `{"folder":"sent"}`, nil, nil, service,
		"mail", "sync", "status", "--input-json", "-")
	if result.ExitCode != 0 || stderr != "" || service.syncStatusCalls != 1 || service.syncFolder != "sent" {
		t.Fatalf("sync status result=%+v calls=%d folder=%q stdout=%q stderr=%q", result, service.syncStatusCalls, service.syncFolder, stdout, stderr)
	}
}

func TestRequiredReadTargetsRejectBeforeServiceWork(t *testing.T) {
	service := &structuredReadMailService{}
	for _, test := range []struct {
		name  string
		input string
		args  []string
	}{
		{"message", `{}`, []string{"mail", "message", "show", "--input-json", "-"}},
		{"thread", `{}`, []string{"mail", "threads", "show", "--input-json", "-"}},
		{"attachment", `{}`, []string{"mail", "attachments", "list", "--input-json", "-"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, stdout, stderr := executeStructuredRead(t, test.input, nil, nil, service, test.args...)
			if result.ExitCode != ExitUsage || stdout != "" || !strings.Contains(stderr, "code=usage") {
				t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
			}
		})
	}
	if service.messageCalls != 0 || service.threadCalls != 0 || service.attachmentsCalls != 0 {
		t.Fatalf("required-target validation reached service: message=%d thread=%d attachments=%d", service.messageCalls, service.threadCalls, service.attachmentsCalls)
	}
}
