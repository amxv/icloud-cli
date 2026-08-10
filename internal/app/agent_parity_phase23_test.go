package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func phase23Raw(index int) string {
	return strings.Join([]string{
		"From: Archive Sender <sender@example.test>",
		"To: owner@icloud.com",
		"Date: Mon, 10 Aug 2026 20:00:00 +0000",
		"Message-ID: <phase23-" + itoaTest(index) + "@example.test>",
		"Subject: Phase 23 record " + itoaTest(index),
		"Content-Type: text/plain; charset=utf-8",
		"",
		"archive record " + itoaTest(index) + "\r\nFrom body delimiter must round trip\r\n>From already quoted\r\n",
	}, "\r\n")
}

func itoaTest(value int) string {
	if value == 0 {
		return "0"
	}
	var buf [24]byte
	pos := len(buf)
	for value > 0 {
		pos--
		buf[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[pos:])
}

func writePhase23Mbox(t *testing.T, raws ...string) string {
	t.Helper()
	var archive bytes.Buffer
	for index, raw := range raws {
		if _, err := maildomain.WriteMboxRDMessage(context.Background(), &archive, "sender@example.test", time.Date(2026, 8, 10, 20, index, 0, 0, time.UTC), strings.NewReader(raw)); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "archive.mbox")
	if err := os.WriteFile(path, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAgentParityPhase23MailboxImportResumesBoundedJobWithoutDuplicateAppendOrSyncFrontierChange(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE23_PASSWORD", "synthetic-phase23-secret")
	commands, _ := newTestAccountCommands()
	seedPhase21Checkpoint(t, commands, home, "ICLOUD_PHASE23_PASSWORD")

	raws := []string{phase23Raw(1), phase23Raw(2), phase23Raw(3)}
	path := writePhase23Mbox(t, raws...)
	status := &phase21IMAPClient{statuses: []transport.MailboxStatus{
		{UIDValidity: 100, UIDNext: 21}, {UIDValidity: 100, UIDNext: 22}, {UIDValidity: 100, UIDNext: 23},
	}}
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{
		{Mailbox: "INBOX", UIDValidity: 100, UID: 21},
		{Mailbox: "INBOX", UIDValidity: 100, UID: 22},
		{Mailbox: "INBOX", UIDValidity: 100, UID: 23},
	}}
	remote := &phase21RemoteClient{uidValidity: 100, rawByUID: map[uint32]string{21: raws[0], 22: raws[1], 23: raws[2]}}
	commands.imap, commands.appendMailbox, commands.remote = status, appender, remote

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	folderValue, _ := state.Folders().Resolve(context.Background(), accountValue.ID, "inbox")
	before, err := state.Mail().Checkpoint(context.Background(), accountValue.ID, folderValue.ID)
	state.Close()
	if err != nil {
		t.Fatal(err)
	}

	args := []string{"--home", home, "mail", "mailbox", "import", path, "--to", "inbox", "--max-messages", "2", "--yes", "--json"}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || appender.calls != 2 || !strings.Contains(stdout, `"complete":false`) || !strings.Contains(stdout, `"imported_count":2`) || strings.Contains(stdout, path) || strings.Contains(stdout, "mbox:") {
		t.Fatalf("first pass code=%d stdout=%q stderr=%q appends=%d", code, stdout, stderr, appender.calls)
	}
	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	job, err := state.MailboxImportJobs().Resolve(context.Background(), accountValue.ID, folderValue.ID, path, fileSHA256(t, path), fileSize(t, path))
	if err != nil || job.NextIndex != 3 || job.ImportedCount != 2 || job.Status != maildomain.MailboxImportJobRunning {
		state.Close()
		t.Fatalf("first job=%+v err=%v", job, err)
	}
	state.Close()

	code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || appender.calls != 3 || !strings.Contains(stdout, `"complete":true`) || !strings.Contains(stdout, `"imported_count":3`) {
		t.Fatalf("resume code=%d stdout=%q stderr=%q appends=%d", code, stdout, stderr, appender.calls)
	}
	for index, request := range appender.requests {
		wantDate := time.Date(2026, 8, 10, 20, index, 0, 0, time.UTC)
		if !request.InternalDate.Equal(wantDate) {
			t.Fatalf("append %d internal_date=%v want=%v", index, request.InternalDate, wantDate)
		}
	}
	code, replay, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || appender.calls != 3 || !strings.Contains(replay, `"complete":true`) {
		t.Fatalf("replay code=%d stdout=%q stderr=%q appends=%d", code, replay, stderr, appender.calls)
	}

	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	after, err := state.Mail().Checkpoint(context.Background(), accountValue.ID, folderValue.ID)
	job, jobErr := state.MailboxImportJobs().Resolve(context.Background(), accountValue.ID, folderValue.ID, path, fileSHA256(t, path), fileSize(t, path))
	state.Close()
	if err != nil || jobErr != nil || job.Status != maildomain.MailboxImportJobConfirmed || job.NextIndex != 4 || job.NextOffset != job.SourceByteSize || job.ImportedCount != 3 {
		t.Fatalf("final job=%+v checkpoint=%+v err=%v jobErr=%v", job, after, err, jobErr)
	}
	if after.LastCommittedUID != before.LastCommittedUID || after.LastScannedUID != before.LastScannedUID {
		t.Fatalf("mailbox import changed normal sync checkpoint before=%+v after=%+v", before, after)
	}
}

func TestAgentParityPhase23ContinueOnErrorRetainsEarliestFailedFrontierWhileLaterItemsConfirm(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE23_PARTIAL_PASSWORD", "synthetic-phase23-partial-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	addAndDiscoverInbox(t, commands, home, "ICLOUD_PHASE23_PARTIAL_PASSWORD")
	valid1, valid3 := phase23Raw(1), phase23Raw(3)
	malformed := "Subject: " + strings.Repeat("x", (1<<20)+1024) + "\r\n\r\nbody\r\n"
	path := writePhase23Mbox(t, valid1, malformed, valid3)
	status := &phase21IMAPClient{statuses: []transport.MailboxStatus{{UIDValidity: 100, UIDNext: 21}, {UIDValidity: 100, UIDNext: 22}}}
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{
		{Mailbox: "INBOX", UIDValidity: 100, UID: 21}, {Mailbox: "INBOX", UIDValidity: 100, UID: 22},
	}}
	remote := &phase21RemoteClient{uidValidity: 100, rawByUID: map[uint32]string{21: valid1, 22: valid3}}
	commands.imap, commands.appendMailbox, commands.remote = status, appender, remote

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "mailbox", "import", path,
		"--to", "inbox", "--mode", "continue-on-error", "--max-messages", "3", "--yes", "--jsonl")
	if code == 0 || stderr != "" || appender.calls != 2 || strings.Count(stdout, `"event":"item"`) != 3 || !strings.Contains(stdout, `"event":"completed"`) || !strings.Contains(stdout, `"failed":1`) || !strings.Contains(stdout, `"imported_count":2`) || !strings.Contains(stdout, `"partial":true`) {
		t.Fatalf("partial code=%d stdout=%q stderr=%q appends=%d", code, stdout, stderr, appender.calls)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	folderValue, _ := state.Folders().Resolve(context.Background(), accountValue.ID, "inbox")
	job, err := state.MailboxImportJobs().Resolve(context.Background(), accountValue.ID, folderValue.ID, path, fileSHA256(t, path), fileSize(t, path))
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	item2, err2 := state.MailboxImportJobs().Item(context.Background(), job.ID, 2)
	item3, err3 := state.MailboxImportJobs().Item(context.Background(), job.ID, 3)
	state.Close()
	if job.Status != maildomain.MailboxImportJobPartial || job.NextIndex != 2 || job.ImportedCount != 2 || err2 != nil || err3 != nil || item2.Status != maildomain.MailboxImportItemFailed || item3.Status != maildomain.MailboxImportItemConfirmed {
		t.Fatalf("job=%+v item2=%+v err2=%v item3=%+v err3=%v", job, item2, err2, item3, err3)
	}

	code, _, _ = runWithAccountCommands(t, commands, "", "--home", home, "mail", "mailbox", "import", path, "--to", "inbox", "--yes")
	if code == 0 || appender.calls != 2 {
		t.Fatalf("failed-frontier replay code=%d appends=%d", code, appender.calls)
	}
}

func TestAgentParityPhase23AmbiguousAppendResumesSamePhase21ImportWithoutReappend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE23_AMBIG_PASSWORD", "synthetic-phase23-ambiguous-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	addAndDiscoverInbox(t, commands, home, "ICLOUD_PHASE23_AMBIG_PASSWORD")
	raw := phase23Raw(1)
	path := writePhase23Mbox(t, raw)
	status := &phase21IMAPClient{statuses: []transport.MailboxStatus{
		{UIDValidity: 100, UIDNext: 21}, {UIDValidity: 100, UIDNext: 22}, {UIDValidity: 100, UIDNext: 22},
	}}
	appender := &fakeDraftAppender{errors: []error{transport.NewFailure(transport.StageIMAPAppend, transport.FailureUnavailable, true, errors.New("synthetic disconnect after acceptance"))}}
	remote := &phase21RemoteClient{uidValidity: 100, rawByUID: map[uint32]string{}}
	commands.imap, commands.appendMailbox, commands.remote = status, appender, remote
	args := []string{"--home", home, "mail", "mailbox", "import", path, "--to", "inbox", "--yes"}

	code, _, _ := runWithAccountCommands(t, commands, "", args...)
	if code == 0 || appender.calls != 1 {
		t.Fatalf("ambiguous first pass code=%d appends=%d", code, appender.calls)
	}
	remote.rawByUID[21] = raw
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || appender.calls != 1 || !compactContainsAll(stdout, "mailbox-import", "status=confirmed", "complete=true") {
		t.Fatalf("ambiguous resume code=%d stdout=%q stderr=%q appends=%d", code, stdout, stderr, appender.calls)
	}
}

func TestAgentParityPhase23ChangedSourceRefusesResumeBeforeAnotherAppend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE23_CHANGE_PASSWORD", "synthetic-phase23-change-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	addAndDiscoverInbox(t, commands, home, "ICLOUD_PHASE23_CHANGE_PASSWORD")
	raws := []string{phase23Raw(1), phase23Raw(2)}
	path := writePhase23Mbox(t, raws...)
	status := &phase21IMAPClient{statuses: []transport.MailboxStatus{{UIDValidity: 100, UIDNext: 21}}}
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "INBOX", UIDValidity: 100, UID: 21}}}
	remote := &phase21RemoteClient{uidValidity: 100, rawByUID: map[uint32]string{21: raws[0]}}
	commands.imap, commands.appendMailbox, commands.remote = status, appender, remote

	args := []string{"--home", home, "mail", "mailbox", "import", path, "--to", "inbox", "--max-messages", "1", "--yes"}
	code, _, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || appender.calls != 1 {
		t.Fatalf("first pass code=%d stderr=%q appends=%d", code, stderr, appender.calls)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString("changed")
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "source file changed") || appender.calls != 1 {
		t.Fatalf("changed source code=%d stdout=%q stderr=%q appends=%d", code, stdout, stderr, appender.calls)
	}
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func TestAgentParityPhase23DefinitePreAppendFailureAdvancesAttemptKeyBeforeSafeRetry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE23_PREAPPEND_PASSWORD", "synthetic-phase23-preappend-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	addAndDiscoverInbox(t, commands, home, "ICLOUD_PHASE23_PREAPPEND_PASSWORD")
	raw := phase23Raw(1)
	path := writePhase23Mbox(t, raw)
	status := &phase21IMAPClient{statuses: []transport.MailboxStatus{
		{UIDValidity: 100, UIDNext: 21}, {UIDValidity: 100, UIDNext: 21},
	}}
	authFailure := transport.NewFailure(transport.StageIMAPAuth, transport.FailureAuthentication, false, errors.New("synthetic pre-append authentication failure"))
	appender := &fakeDraftAppender{
		errors:  []error{authFailure, authFailure, nil},
		results: []transport.MailboxAppendResult{{}, {}, {Mailbox: "INBOX", UIDValidity: 100, UID: 21}},
	}
	remote := &phase21RemoteClient{uidValidity: 100, rawByUID: map[uint32]string{21: raw}}
	commands.imap, commands.appendMailbox, commands.remote = status, appender, remote
	args := []string{"--home", home, "mail", "mailbox", "import", path, "--to", "inbox", "--yes"}

	code, _, _ := runWithAccountCommands(t, commands, "", args...)
	if code == 0 || appender.calls != 2 {
		t.Fatalf("pre-append first pass code=%d appends=%d", code, appender.calls)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	folderValue, _ := state.Folders().Resolve(context.Background(), accountValue.ID, "inbox")
	job, err := state.MailboxImportJobs().Resolve(context.Background(), accountValue.ID, folderValue.ID, path, fileSHA256(t, path), fileSize(t, path))
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	first, err := state.MailboxImportJobs().Item(context.Background(), job.ID, 1)
	state.Close()
	if err != nil || first.Status != maildomain.MailboxImportItemFailed || !first.RetryNewKey || first.AttemptCount != 1 {
		t.Fatalf("first durable item=%+v err=%v", first, err)
	}
	firstKey := first.IdempotencyKey

	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || appender.calls != 3 || !compactContainsAll(stdout, "mailbox-import", "status=confirmed", "complete=true") {
		t.Fatalf("pre-append retry code=%d stdout=%q stderr=%q appends=%d", code, stdout, stderr, appender.calls)
	}
	state, err = openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.MailboxImportJobs().Item(context.Background(), job.ID, 1)
	state.Close()
	if err != nil || second.Status != maildomain.MailboxImportItemConfirmed || second.AttemptCount != 2 || second.RetryNewKey || second.IdempotencyKey == firstKey {
		t.Fatalf("retried durable item=%+v err=%v first_key_reused=%t", second, err, second.IdempotencyKey == firstKey)
	}
}

func TestAgentParityPhase23CanceledRecordLeavesFrontierAndRetriesWithoutProviderDuplication(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE23_CANCEL_PASSWORD", "synthetic-phase23-cancel-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	addAndDiscoverInbox(t, commands, home, "ICLOUD_PHASE23_CANCEL_PASSWORD")
	raw := phase23Raw(1)
	path := writePhase23Mbox(t, raw)
	status := &phase21IMAPClient{statuses: []transport.MailboxStatus{{UIDValidity: 100, UIDNext: 21}}}
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "INBOX", UIDValidity: 100, UID: 21}}}
	remote := &phase21RemoteClient{uidValidity: 100, rawByUID: map[uint32]string{21: raw}}
	commands.imap, commands.appendMailbox, commands.remote = status, appender, remote
	commands.blobs = func(config.AppPaths) blob.Stager { return failingBlobStager{err: context.Canceled} }
	args := []string{"--home", home, "mail", "mailbox", "import", path, "--to", "inbox", "--yes"}

	code, _, _ := runWithAccountCommands(t, commands, "", args...)
	if code != cli.ExitUnavailable || appender.calls != 0 || len(status.requests) != 0 {
		t.Fatalf("canceled pass code=%d appends=%d status_requests=%v", code, appender.calls, status.requests)
	}
	commands.blobs = nil
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || appender.calls != 1 || !compactContainsAll(stdout, "mailbox-import", "status=confirmed", "complete=true") {
		t.Fatalf("canceled resume code=%d stdout=%q stderr=%q appends=%d", code, stdout, stderr, appender.calls)
	}
}

func TestAgentParityPhase23LargeAttachmentRecordUsesSingleMessageCanonicalPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE23_LARGE_PASSWORD", "synthetic-phase23-large-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	addAndDiscoverInbox(t, commands, home, "ICLOUD_PHASE23_LARGE_PASSWORD")

	largeBase64 := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo0NTY3ODkw\r\n", 32768)
	raw := strings.Join([]string{
		"From: Archive Sender <sender@example.test>",
		"To: owner@icloud.com",
		"Date: Mon, 10 Aug 2026 20:00:00 +0000",
		"Message-ID: <phase23-large@example.test>",
		"Subject: Phase 23 large attachment",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=phase23-large-boundary",
		"",
		"--phase23-large-boundary",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"large attachment batch record",
		"--phase23-large-boundary",
		"Content-Type: application/octet-stream; name=large.bin",
		"Content-Disposition: attachment; filename=large.bin",
		"Content-Transfer-Encoding: base64",
		"",
		largeBase64,
		"--phase23-large-boundary--",
		"",
	}, "\r\n")
	path := writePhase23Mbox(t, raw)
	status := &phase21IMAPClient{statuses: []transport.MailboxStatus{{UIDValidity: 100, UIDNext: 21}}}
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "INBOX", UIDValidity: 100, UID: 21}}}
	remote := &phase21RemoteClient{uidValidity: 100, rawByUID: map[uint32]string{21: raw}}
	commands.imap, commands.appendMailbox, commands.remote = status, appender, remote

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "mailbox", "import", path, "--to", "inbox", "--yes", "--json")
	if code != 0 || stderr != "" || appender.calls != 1 || !strings.Contains(stdout, `"complete":true`) {
		t.Fatalf("large attachment import code=%d stdout=%q stderr=%q appends=%d", code, stdout, stderr, appender.calls)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	folderValue, _ := state.Folders().Resolve(context.Background(), accountValue.ID, "inbox")
	job, err := state.MailboxImportJobs().Resolve(context.Background(), accountValue.ID, folderValue.ID, path, fileSHA256(t, path), fileSize(t, path))
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	item, err := state.MailboxImportJobs().Item(context.Background(), job.ID, 1)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	message, err := state.Mail().Message(context.Background(), accountValue.ID, item.MessageID)
	state.Close()
	if err != nil || message.AttachmentCount != 1 || message.RFCMessageID != "phase23-large@example.test" {
		t.Fatalf("large canonical message=%+v err=%v item=%+v", message, err, item)
	}
}

func TestAgentParityPhase23MailboxImportDryRunAndCancellationCreateNoJobOrProviderTraffic(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE23_REVIEW_PASSWORD", "synthetic-phase23-review-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = oneInboxFolderClient()
	addAndDiscoverInbox(t, commands, home, "ICLOUD_PHASE23_REVIEW_PASSWORD")
	raw := phase23Raw(1)
	path := writePhase23Mbox(t, raw)
	status := &phase21IMAPClient{statuses: []transport.MailboxStatus{{UIDValidity: 100, UIDNext: 21}}}
	appender := &fakeDraftAppender{}
	commands.imap, commands.appendMailbox = status, appender
	commands.remote = &phase21RemoteClient{uidValidity: 100, rawByUID: map[uint32]string{}}

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "mailbox", "import", path, "--to", "inbox", "--dry-run", "--json")
	if code != 0 || stderr != "" || appender.calls != 0 || len(status.requests) != 0 {
		t.Fatalf("dry-run code=%d stdout=%q stderr=%q appends=%d status=%v", code, stdout, stderr, appender.calls, status.requests)
	}
	if strings.Contains(stdout, path) || strings.Contains(stdout, "mbox:") {
		t.Fatalf("dry-run leaked private source material: %q", stdout)
	}

	args := []string{"--home", home, "mail", "mailbox", "import", path, "--to", "inbox"}
	code, stdout, stderr = runWithAccountCommandsReader(commands, testTerminalReader{Reader: strings.NewReader("n\n")}, args...)
	if code != cli.ExitSafety || !strings.Contains(stdout, "mailbox-import-review") || !strings.Contains(stderr, "Import up to") || appender.calls != 0 || len(status.requests) != 0 {
		t.Fatalf("cancel code=%d stdout=%q stderr=%q appends=%d status=%v", code, stdout, stderr, appender.calls, status.requests)
	}

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, _ := state.Accounts().ResolveSelected(context.Background(), "")
	folderValue, _ := state.Folders().Resolve(context.Background(), accountValue.ID, "inbox")
	_, err = state.MailboxImportJobs().Resolve(context.Background(), accountValue.ID, folderValue.ID, path, fileSHA256(t, path), fileSize(t, path))
	state.Close()
	if !errors.Is(err, maildomain.ErrMailboxImportJobNotFound) {
		t.Fatalf("review/cancellation created mailbox job: %v", err)
	}
}
