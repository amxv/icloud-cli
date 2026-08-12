package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

type emlImportIMAPClient struct {
	statuses []transport.MailboxStatus
	requests []string
}

func (client *emlImportIMAPClient) ValidateIMAP(context.Context, transport.AuthRequest) (transport.IMAPResult, error) {
	status := transport.MailboxStatus{Mailbox: "INBOX", UIDValidity: 100, UIDNext: 21}
	if len(client.statuses) > 0 {
		status = client.statuses[0]
	}
	return transport.IMAPResult{Capabilities: []string{"IMAP4rev1", "UIDPLUS"}, Mailboxes: 1, Inbox: status}, nil
}

func (client *emlImportIMAPClient) MailboxStatus(_ context.Context, _ transport.AuthRequest, mailbox string) (transport.MailboxStatus, error) {
	client.requests = append(client.requests, mailbox)
	if len(client.statuses) == 0 {
		return transport.MailboxStatus{}, errors.New("unexpected mailbox status request")
	}
	result := client.statuses[0]
	if len(client.statuses) > 1 {
		client.statuses = client.statuses[1:]
	}
	result.Mailbox = mailbox
	return result, nil
}

type emlImportRemoteClient struct {
	uidValidity uint32
	rawByUID    map[uint32]string
	flagsByUID  map[uint32][]string
	dateByUID   map[uint32]time.Time
	requests    []transport.RemoteFetchRequest
}

func (client *emlImportRemoteClient) SearchRemote(context.Context, transport.AuthRequest, transport.RemoteSearchRequest) (transport.RemoteSearchPage, error) {
	return transport.RemoteSearchPage{}, errors.New("unexpected remote search")
}

func (client *emlImportRemoteClient) FetchRemoteSummary(context.Context, transport.AuthRequest, transport.RemoteFetchRequest) (transport.RemoteSummaryResult, error) {
	return transport.RemoteSummaryResult{}, errors.New("unexpected remote summary")
}

func (client *emlImportRemoteClient) FetchRemoteMessage(ctx context.Context, _ transport.AuthRequest, request transport.RemoteFetchRequest, stager blob.Stager) (transport.RemoteFetchResult, error) {
	client.requests = append(client.requests, request)
	uidValidity := client.uidValidity
	if uidValidity == 0 {
		uidValidity = 100
	}
	if request.ExpectedUIDValidity != uidValidity {
		return transport.RemoteFetchResult{}, &transport.UIDValidityError{Expected: request.ExpectedUIDValidity, Observed: uidValidity}
	}
	raw, ok := client.rawByUID[request.UID]
	if !ok {
		return transport.RemoteFetchResult{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, false, transport.ErrRemoteMessageUnavailable)
	}
	staged, err := stager.Stage(ctx, strings.NewReader(raw), int64(len(raw)))
	if err != nil {
		return transport.RemoteFetchResult{}, err
	}
	internalDate := client.dateByUID[request.UID]
	if internalDate.IsZero() {
		internalDate = time.Date(2026, 8, 9, 12, 34, 56, 0, time.UTC)
	}
	return transport.RemoteFetchResult{
		Mailbox: request.Mailbox, UIDValidity: uidValidity,
		Message: transport.FetchedMessage{
			UID: request.UID, Flags: append([]string(nil), client.flagsByUID[request.UID]...),
			InternalDate: internalDate, RFC822Size: int64(len(raw)), Raw: staged,
		},
	}, nil
}

func emlImportMultipartEML() string {
	return strings.Join([]string{
		"From: Import Sender <sender@example.test>",
		"To: owner@icloud.com",
		"Date: Sun, 09 Aug 2026 12:34:56 +0000",
		"Message-ID: <emlImport-import@example.test>",
		"Subject: =?UTF-8?Q?EML_Import_=E6=97=A5=E6=9C=AC=E8=AA=9E?=",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=emlImport-boundary",
		"",
		"--emlImport-boundary",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		"EML import canonical import body 日本語 café.",
		"--emlImport-boundary",
		"Content-Type: text/plain; charset=utf-8; name=fixture.txt",
		"Content-Disposition: attachment; filename=fixture.txt",
		"Content-Transfer-Encoding: base64",
		"",
		"cGhhc2UyMS1hdHRhY2htZW50Cg==",
		"--emlImport-boundary--",
		"",
	}, "\r\n")
}

func writeEMLImportFixture(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.eml")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func seedEMLImportCheckpoint(t *testing.T, commands accountCommands, home, envName string) {
	t.Helper()
	commands.folders = oneInboxFolderClient()
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{page: transport.InboxPage{
		Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 20, LastScannedUID: 20, Bootstrap: true,
	}}}}
	addAndDiscoverInbox(t, commands, home, envName)
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--folder", "inbox", "--limit", "1")
	if code != 0 || stderr != "" {
		t.Fatalf("seed sync code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestImportAPPENDUIDUsesCanonicalHydrationWithoutAdvancingSyncCheckpoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_EML_IMPORT_IMPORT_PASSWORD", "synthetic-emlImport-import-secret")
	commands, _ := newTestAccountCommands()
	seedEMLImportCheckpoint(t, commands, home, "ICLOUD_EML_IMPORT_IMPORT_PASSWORD")
	raw := emlImportMultipartEML()
	path := writeEMLImportFixture(t, raw)
	status := &emlImportIMAPClient{statuses: []transport.MailboxStatus{{UIDValidity: 100, UIDNext: 21, Messages: 20}}}
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "INBOX", UIDValidity: 100, UID: 21}}}
	remote := &emlImportRemoteClient{
		uidValidity: 100, rawByUID: map[uint32]string{21: raw},
		flagsByUID: map[uint32][]string{21: {`\Seen`, `\Flagged`}},
	}
	commands.imap = status
	commands.appendMailbox = appender
	commands.remote = remote

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	folderValue, _ := state.Folders().Resolve(context.Background(), accountValue.ID, "inbox")
	before, err := state.Mail().Checkpoint(context.Background(), accountValue.ID, folderValue.ID)
	state.Close()
	if err != nil || before.LastCommittedUID != 20 || before.LastScannedUID != 20 {
		t.Fatalf("before checkpoint=%+v err=%v", before, err)
	}

	args := []string{"--home", home, "mail", "message", "import", path, "--to", "inbox", "--flag", "seen", "--flag", "flagged", "--idempotency-key", "emlImport-import-appenduid", "--yes", "--json"}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"status":"confirmed"`) || !strings.Contains(stdout, `"provider_raw_variant":"exact"`) || strings.Contains(stdout, path) || strings.Contains(stdout, "raw/sha256/") || strings.Contains(stdout, "emlImport-import-appenduid") {
		t.Fatalf("import code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if appender.calls != 1 || len(appender.messages) != 1 || string(appender.messages[0]) != raw || len(appender.requests) != 1 || appender.requests[0].Mailbox != "INBOX" || !strings.EqualFold(appender.requests[0].InternalDate.Format(time.RFC3339), "2026-08-09T12:34:56Z") || !strings.Contains(strings.Join(appender.requests[0].Flags, ","), `\Seen`) || !strings.Contains(strings.Join(appender.requests[0].Flags, ","), `\Flagged`) {
		t.Fatalf("append calls=%d requests=%+v", appender.calls, appender.requests)
	}

	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	importValue, err := state.MessageImports().ByIdempotency(context.Background(), accountValue.ID, "emlImport-import-appenduid")
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	blobRefs, _, err := state.MailBlobReferences(context.Background(), accountValue.ID, 20)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	importRawAudited := false
	for _, ref := range blobRefs {
		if ref.Kind == "import_raw" && ref.ID == importValue.ID && strings.EqualFold(ref.ContentHash, importValue.RawContentHash) && ref.ByteSize == importValue.RawByteSize {
			importRawAudited = true
			break
		}
	}
	if !importRawAudited {
		state.Close()
		t.Fatalf("durable import raw evidence missing from repair audit: refs=%+v import=%+v", blobRefs, importValue)
	}
	message, err := state.Mail().Message(context.Background(), accountValue.ID, importValue.MessageID)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	after, err := state.Mail().Checkpoint(context.Background(), accountValue.ID, folderValue.ID)
	state.Close()
	if err != nil || after.LastCommittedUID != before.LastCommittedUID || after.LastScannedUID != before.LastScannedUID {
		t.Fatalf("targeted import advanced sync checkpoint before=%+v after=%+v err=%v", before, after, err)
	}
	if importValue.Status != maildomain.MessageImportConfirmed || importValue.ProviderUID != 21 || importValue.ProviderUIDValidity != 100 || importValue.ProviderMethod != "append_uid" || message.ID == "" || message.RFCMessageID != "emlImport-import@example.test" || message.AttachmentCount != 1 || message.Content == nil || !strings.Contains(message.Snippet, "日本語 café") {
		t.Fatalf("import=%+v message=%+v", importValue, message)
	}

	code, rawOut, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "raw", message.ID)
	if code != 0 || stderr != "" || rawOut != raw {
		t.Fatalf("raw readback code=%d exact=%t stderr=%q", code, rawOut == raw, stderr)
	}
	code, searchOut, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "canonical import body", "--limit", "5", "--json")
	if code != 0 || stderr != "" || !strings.Contains(searchOut, message.ID) {
		t.Fatalf("search code=%d stdout=%q stderr=%q", code, searchOut, stderr)
	}

	code, replayOut, replayErr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || replayErr != "" || appender.calls != 1 || !strings.Contains(replayOut, importValue.ID) || !strings.Contains(replayOut, message.ID) {
		t.Fatalf("replay code=%d stdout=%q stderr=%q append_calls=%d", code, replayOut, replayErr, appender.calls)
	}
}

func TestImportDryRunAndCancellationCreateNoIntentOrProviderTraffic(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_EML_IMPORT_REVIEW_PASSWORD", "synthetic-emlImport-review-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	addAndDiscoverInbox(t, commands, home, "ICLOUD_EML_IMPORT_REVIEW_PASSWORD")
	raw := emlImportMultipartEML()
	path := writeEMLImportFixture(t, raw)
	status := &emlImportIMAPClient{statuses: []transport.MailboxStatus{{UIDValidity: 100, UIDNext: 21}}}
	appender := &fakeDraftAppender{}
	commands.imap = status
	commands.appendMailbox = appender
	commands.remote = &emlImportRemoteClient{uidValidity: 100, rawByUID: map[uint32]string{}}

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "import", path, "--to", "inbox", "--flag", "seen", "--dry-run")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "message-import-review", "dry_run=true", "imap_contacted=false", "intent_created=false", "attachments=1") || appender.calls != 0 || len(status.requests) != 0 {
		t.Fatalf("dry-run code=%d stdout=%q stderr=%q append=%d status=%v", code, stdout, stderr, appender.calls, status.requests)
	}
	requestPath := filepath.Join(t.TempDir(), "import-request.json")
	payload, err := json.Marshal(map[string]any{
		"file": path, "to": "inbox", "flags": []string{"seen"}, "idempotency_key": "emlImport-json-review",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requestPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "import", "--input-json", requestPath, "--dry-run", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, fmt.Sprintf(`"schema_version":%d`, store.SchemaVersion)) || strings.Contains(stdout, path) || strings.Contains(stdout, "emlImport-json-review") || appender.calls != 0 || len(status.requests) != 0 {
		t.Fatalf("json dry-run code=%d stdout=%q stderr=%q append=%d status=%v", code, stdout, stderr, appender.calls, status.requests)
	}

	args := []string{"--home", home, "mail", "message", "import", path, "--to", "inbox", "--idempotency-key", "emlImport-cancel"}
	code, stdout, stderr = runWithAccountCommandsReader(commands, testTerminalReader{Reader: strings.NewReader("n\n")}, args...)
	if code != cli.ExitSafety || !strings.Contains(stdout, "message-import-review") || !strings.Contains(stderr, "Append the reviewed RFC message") || appender.calls != 0 || len(status.requests) != 0 {
		t.Fatalf("cancel code=%d stdout=%q stderr=%q append=%d status=%v", code, stdout, stderr, appender.calls, status.requests)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	_, err = state.MessageImports().ByIdempotency(context.Background(), accountValue.ID, "emlImport-cancel")
	state.Close()
	if !errors.Is(err, maildomain.ErrMessageImportNotFound) {
		t.Fatalf("canceled import lookup err=%v", err)
	}
}

func TestImportReconcilesMissingAPPENDUIDAndDisconnectWithoutDuplicateAppend(t *testing.T) {
	for _, test := range []struct {
		name      string
		result    transport.MailboxAppendResult
		appendErr error
		variant   string
	}{
		{name: "missing_appenduid", variant: "terminal_crlf"},
		{name: "disconnect_after_acceptance", appendErr: transport.NewFailure(transport.StageIMAPAppend, transport.FailureUnavailable, true, errors.New("synthetic disconnect after provider acceptance")), variant: "exact"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("ICLOUD_EML_IMPORT_RECONCILE_PASSWORD", "synthetic-emlImport-reconcile-secret")
			commands, _ := newTestAccountCommands()
			commands.folders = oneInboxFolderClient()
			addAndDiscoverInbox(t, commands, home, "ICLOUD_EML_IMPORT_RECONCILE_PASSWORD")
			raw := emlImportMultipartEML()
			path := writeEMLImportFixture(t, raw)
			status := &emlImportIMAPClient{statuses: []transport.MailboxStatus{
				{UIDValidity: 100, UIDNext: 30, Messages: 29},
				{UIDValidity: 100, UIDNext: 31, Messages: 30},
			}}
			appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{test.result}}
			if test.appendErr != nil {
				appender.errors = []error{test.appendErr}
			}
			providerRaw := raw
			if test.variant == "terminal_crlf" {
				providerRaw += "\r\n"
			}
			remote := &emlImportRemoteClient{uidValidity: 100, rawByUID: map[uint32]string{30: providerRaw}}
			commands.imap = status
			commands.appendMailbox = appender
			commands.remote = remote
			key := "emlImport-reconcile-" + test.name
			args := []string{"--home", home, "mail", "message", "import", path, "--to", "inbox", "--idempotency-key", key, "--yes", "--json"}

			code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
			if code != 0 || stderr != "" || appender.calls != 1 || len(status.requests) != 2 || !strings.Contains(stdout, `"provider_method":"exact_raw_reconcile"`) || !strings.Contains(stdout, `"provider_raw_variant":"`+test.variant+`"`) {
				t.Fatalf("reconciled import code=%d stdout=%q stderr=%q append=%d status=%v", code, stdout, stderr, appender.calls, status.requests)
			}
			state, err := openState(context.Background(), home)
			if err != nil {
				t.Fatal(err)
			}
			accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
			importValue, err := state.MessageImports().ByIdempotency(context.Background(), accountValue.ID, key)
			state.Close()
			if err != nil || importValue.Status != maildomain.MessageImportConfirmed || importValue.ProviderUID != 30 || importValue.CandidateUIDUpper != 30 || importValue.ProviderMethod != "exact_raw_reconcile" {
				t.Fatalf("reconciled import=%+v err=%v", importValue, err)
			}
			code, replayOut, replayErr := runWithAccountCommands(t, commands, "", args...)
			if code != 0 || replayErr != "" || appender.calls != 1 || !strings.Contains(replayOut, importValue.ID) {
				t.Fatalf("replay code=%d stdout=%q stderr=%q append=%d", code, replayOut, replayErr, appender.calls)
			}
		})
	}
}

func TestImportRefusesAmbiguousExactRawCopiesWithoutReappend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_EML_IMPORT_AMBIG_PASSWORD", "synthetic-emlImport-ambiguous-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	addAndDiscoverInbox(t, commands, home, "ICLOUD_EML_IMPORT_AMBIG_PASSWORD")
	raw := emlImportMultipartEML()
	path := writeEMLImportFixture(t, raw)
	status := &emlImportIMAPClient{statuses: []transport.MailboxStatus{
		{UIDValidity: 100, UIDNext: 30, Messages: 29},
		{UIDValidity: 100, UIDNext: 32, Messages: 31},
	}}
	appender := &fakeDraftAppender{}
	remote := &emlImportRemoteClient{uidValidity: 100, rawByUID: map[uint32]string{30: raw, 31: raw}}
	commands.imap = status
	commands.appendMailbox = appender
	commands.remote = remote
	args := []string{"--home", home, "mail", "message", "import", path, "--to", "inbox", "--idempotency-key", "emlImport-ambiguous-exact", "--yes"}

	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "multiple provider copies match the reviewed source bytes") || appender.calls != 1 {
		t.Fatalf("ambiguous import code=%d stdout=%q stderr=%q append=%d", code, stdout, stderr, appender.calls)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	importValue, err := state.MessageImports().ByIdempotency(context.Background(), accountValue.ID, "emlImport-ambiguous-exact")
	state.Close()
	if err != nil || importValue.Status != maildomain.MessageImportAppendUnresolved || importValue.CandidateUIDUpper != 31 || !importValue.ReconciliationRequired {
		t.Fatalf("ambiguous import state=%+v err=%v", importValue, err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
	if code != cli.ExitConflict || stdout != "" || appender.calls != 1 {
		t.Fatalf("ambiguous replay code=%d stdout=%q stderr=%q append=%d", code, stdout, stderr, appender.calls)
	}
}

func TestImportRejectsProviderRewriteBeyondSingleTerminalCRLF(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_EML_IMPORT_PROVIDER_REWRITE_PASSWORD", "synthetic-emlImport-provider-rewrite-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	addAndDiscoverInbox(t, commands, home, "ICLOUD_EML_IMPORT_PROVIDER_REWRITE_PASSWORD")
	raw := emlImportMultipartEML()
	path := writeEMLImportFixture(t, raw)
	status := &emlImportIMAPClient{statuses: []transport.MailboxStatus{{UIDValidity: 100, UIDNext: 21, Messages: 20}}}
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "INBOX", UIDValidity: 100, UID: 21}}}
	remote := &emlImportRemoteClient{uidValidity: 100, rawByUID: map[uint32]string{21: raw + "\n"}}
	commands.imap = status
	commands.appendMailbox = appender
	commands.remote = remote
	args := []string{"--home", home, "mail", "message", "import", path, "--to", "inbox", "--idempotency-key", "emlImport-provider-rewrite", "--yes"}

	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "beyond the exact iCloud terminal-CRLF normalization") || appender.calls != 1 {
		t.Fatalf("provider rewrite code=%d stdout=%q stderr=%q append=%d", code, stdout, stderr, appender.calls)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	importValue, err := state.MessageImports().ByIdempotency(context.Background(), accountValue.ID, "emlImport-provider-rewrite")
	state.Close()
	if err != nil || importValue.Status != maildomain.MessageImportAppended || importValue.ProviderUID != 21 {
		t.Fatalf("provider rewrite durable state=%+v err=%v", importValue, err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
	if code != cli.ExitConflict || stdout != "" || appender.calls != 1 {
		t.Fatalf("provider rewrite replay code=%d stdout=%q stderr=%q append=%d", code, stdout, stderr, appender.calls)
	}
}

func TestImportSafetyFencesInvalidSymlinkOversizeAndCancellationBeforeProviderContact(t *testing.T) {
	for _, test := range []struct {
		name     string
		prepare  func(t *testing.T, commands *accountCommands) string
		wantCode int
		wantErr  string
	}{
		{
			name: "invalid_rfc_message",
			prepare: func(t *testing.T, _ *accountCommands) string {
				return writeEMLImportFixture(t, "Subject: "+strings.Repeat("x", (1<<20)+1024)+"\r\n\r\nbody")
			},
			wantCode: cli.ExitUsage, wantErr: "not one parseable RFC/MIME message",
		},
		{
			name: "symlink_source",
			prepare: func(t *testing.T, _ *accountCommands) string {
				realPath := writeEMLImportFixture(t, emlImportMultipartEML())
				link := filepath.Join(t.TempDir(), "fixture-link.eml")
				if err := os.Symlink(realPath, link); err != nil {
					t.Fatal(err)
				}
				return link
			},
			wantCode: cli.ExitSafety, wantErr: "one regular .eml file",
		},
		{
			name: "canceled_staging",
			prepare: func(t *testing.T, commands *accountCommands) string {
				commands.blobs = func(_ config.AppPaths) blob.Stager { return failingBlobStager{err: context.Canceled} }
				return writeEMLImportFixture(t, emlImportMultipartEML())
			},
			wantCode: cli.ExitUnavailable, wantErr: "validation was canceled",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("ICLOUD_EML_IMPORT_SAFETY_PASSWORD", "synthetic-emlImport-safety-secret")
			commands, _ := newTestAccountCommands()
			commands.folders = oneInboxFolderClient()
			addAndDiscoverInbox(t, commands, home, "ICLOUD_EML_IMPORT_SAFETY_PASSWORD")
			path := test.prepare(t, &commands)
			appender := &fakeDraftAppender{}
			commands.appendMailbox = appender
			code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "import", path, "--to", "inbox", "--dry-run")
			if code != test.wantCode || stdout != "" || !strings.Contains(stderr, test.wantErr) || appender.calls != 0 {
				t.Fatalf("safety code=%d stdout=%q stderr=%q append=%d", code, stdout, stderr, appender.calls)
			}
		})
	}

	t.Run("configured_raw_limit", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("ICLOUD_EML_IMPORT_LIMIT_PASSWORD", "synthetic-emlImport-limit-secret")
		t.Setenv("ICLOUD_MAX_RAW_MESSAGE_BYTES", "128")
		commands, _ := newTestAccountCommands()
		commands.folders = oneInboxFolderClient()
		addAndDiscoverInbox(t, commands, home, "ICLOUD_EML_IMPORT_LIMIT_PASSWORD")
		path := writeEMLImportFixture(t, emlImportMultipartEML())
		appender := &fakeDraftAppender{}
		commands.appendMailbox = appender
		code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "import", path, "--to", "inbox", "--dry-run")
		if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "configured raw-message limit") || appender.calls != 0 {
			t.Fatalf("limit code=%d stdout=%q stderr=%q append=%d", code, stdout, stderr, appender.calls)
		}
	})
}

func TestImportDefinitePreAppendFailureIsDurableAndRequiresNewKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_EML_IMPORT_PREAPPEND_PASSWORD", "synthetic-emlImport-preappend-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	addAndDiscoverInbox(t, commands, home, "ICLOUD_EML_IMPORT_PREAPPEND_PASSWORD")
	path := writeEMLImportFixture(t, emlImportMultipartEML())
	status := &emlImportIMAPClient{statuses: []transport.MailboxStatus{{UIDValidity: 100, UIDNext: 21}}}
	authFailure := transport.NewFailure(transport.StageIMAPAuth, transport.FailureAuthentication, false, errors.New("synthetic authentication failure"))
	appender := &fakeDraftAppender{errors: []error{authFailure, authFailure}}
	commands.imap = status
	commands.appendMailbox = appender
	commands.remote = &emlImportRemoteClient{uidValidity: 100, rawByUID: map[uint32]string{}}
	args := []string{"--home", home, "mail", "message", "import", path, "--to", "inbox", "--idempotency-key", "emlImport-preappend-failure", "--yes"}

	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code == 0 || stdout != "" || appender.calls != 2 {
		t.Fatalf("pre-append code=%d stdout=%q stderr=%q append=%d", code, stdout, stderr, appender.calls)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	importValue, err := state.MessageImports().ByIdempotency(context.Background(), accountValue.ID, "emlImport-preappend-failure")
	state.Close()
	if err != nil || importValue.Status != maildomain.MessageImportFailed || importValue.ReconciliationRequired {
		t.Fatalf("failed import=%+v err=%v", importValue, err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "new idempotency key") || appender.calls != 2 {
		t.Fatalf("failed replay code=%d stdout=%q stderr=%q append=%d", code, stdout, stderr, appender.calls)
	}
}

func TestImportSupportsSelectableUnicodeDestinationAndExplicitMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_EML_IMPORT_UNICODE_PASSWORD", "synthetic-emlImport-unicode-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {folderDiscoveryFixture(false)},
	}}
	addAndDiscoverInbox(t, commands, home, "ICLOUD_EML_IMPORT_UNICODE_PASSWORD")
	raw := emlImportMultipartEML()
	path := writeEMLImportFixture(t, raw)
	status := &emlImportIMAPClient{statuses: []transport.MailboxStatus{{UIDValidity: 104, UIDNext: 3, Messages: 2}}}
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "Unicode-日本語", UIDValidity: 104, UID: 3}}}
	remote := &emlImportRemoteClient{uidValidity: 104, rawByUID: map[uint32]string{3: raw}, flagsByUID: map[uint32][]string{3: {`\Answered`}}}
	commands.imap = status
	commands.appendMailbox = appender
	commands.remote = remote

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "import", path,
		"--to", "Unicode-日本語", "--internal-date", "2026-08-10T03:04:05+05:30", "--flag", "answered", "--idempotency-key", "emlImport-unicode", "--yes", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"status":"confirmed"`) || appender.calls != 1 || len(status.requests) != 1 || status.requests[0] != "Unicode-日本語" {
		t.Fatalf("unicode import code=%d stdout=%q stderr=%q append=%d status=%v", code, stdout, stderr, appender.calls, status.requests)
	}
	if len(appender.requests) != 1 || appender.requests[0].Mailbox != "Unicode-日本語" || appender.requests[0].InternalDate.UTC().Format(time.RFC3339) != "2026-08-09T21:34:05Z" || len(appender.requests[0].Flags) != 1 || appender.requests[0].Flags[0] != `\Answered` {
		t.Fatalf("unicode append request=%+v", appender.requests)
	}
}
