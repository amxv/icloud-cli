package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type attachmentSelectorMailService struct {
	unavailableMailService
	selectors   []string
	attachments []maildomain.Attachment
}

func (service *attachmentSelectorMailService) MailAttachments(_ context.Context, _, _, selector string, _ int) (account.Account, maildomain.AttachmentTarget, []maildomain.Attachment, bool, int64, error) {
	service.selectors = append(service.selectors, selector)
	kind := "message"
	if strings.HasPrefix(selector, "thr_") {
		kind = "thread"
	}
	return account.Account{ID: "acct_test"}, maildomain.AttachmentTarget{Kind: kind, ID: selector}, append([]maildomain.Attachment(nil), service.attachments...), false, 18, nil
}

func (service *attachmentSelectorMailService) MailAttachment(_ context.Context, _, _, attachmentID string) (account.Account, maildomain.Attachment, maildomain.AttachmentSource, int64, error) {
	for _, attachment := range service.attachments {
		if attachment.ID == attachmentID {
			return account.Account{ID: "acct_test"}, attachment, attachmentBytesSource([]byte("thread-attachment-" + attachmentID)), 18, nil
		}
	}
	return account.Account{}, maildomain.Attachment{}, nil, 18, NewAppError(ErrorNotFound, "attachment not found", false)
}

type attachmentBytesSource []byte

func (source attachmentBytesSource) Write(ctx context.Context, writer io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := writer.Write(source)
	return err
}

func TestAttachmentCompactTwentyRecordBudgetAndPrivacy(t *testing.T) {
	var output bytes.Buffer
	renderer := NewCompactTextRenderer(&output)
	for index := 0; index < 20; index++ {
		attachment := maildomain.Attachment{
			ID:           "att_0198d704-6c00-7000-8000-000000000001",
			MessageID:    "msg_0198d704-6c00-7000-8000-000000000002",
			PartID:       "prt_0198d704-6c00-7000-8000-000000000003",
			Filename:     strings.Repeat("very-long-filename-", 100) + ".bin",
			MediaType:    "application/octet-stream",
			ByteSize:     int64(index),
			Inline:       index%2 == 0,
			Disposition:  "attachment",
			ContentID:    strings.Repeat("content-id-", 100),
			ContentHash:  strings.Repeat("a", 64),
			StorageKey:   "attachments/sha256/private.blob",
			Availability: maildomain.AttachmentAvailable,
		}
		if err := renderAttachment(renderer, attachment); err != nil {
			t.Fatal(err)
		}
	}
	if err := renderer.Summary("attachments", Field("message", "msg_fixture"), Field("count", 20), Field("more", "true")); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if strings.Count(text, "\n") > 225 || len(text) > 22000 {
		t.Fatalf("compact attachment budget lines=%d bytes=%d", strings.Count(text, "\n"), len(text))
	}
	for _, forbidden := range []string{"attachments/sha256", strings.Repeat("a", 64)} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("compact attachment output exposed %q", forbidden)
		}
	}
}

func TestAttachmentCommandsAcceptThreadTargetForListAndSaveAll(t *testing.T) {
	service := &attachmentSelectorMailService{attachments: []maildomain.Attachment{
		{ID: "att_thread_one", MessageID: "msg_thread_one", Filename: "one.txt", MediaType: "text/plain", ByteSize: 25, Availability: maildomain.AttachmentAvailable},
		{ID: "att_thread_two", MessageID: "msg_thread_two", Filename: "two.txt", MediaType: "text/plain", ByteSize: 25, Availability: maildomain.AttachmentAvailable},
	}}
	threadID := "thr_thread_attachment_fixture"
	result, stdout, stderr := executeWithMail(t, service, "", "mail", "attachments", "list", threadID, "--json")
	if result.ExitCode != 0 || stderr != "" || !strings.Contains(stdout, `"kind":"thread"`) || !strings.Contains(stdout, `"thread_id":"`+threadID+`"`) || strings.Count(stdout, `"id":"att_thread_`) != 2 {
		t.Fatalf("thread attachment list result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	directory := t.TempDir()
	result, stdout, stderr = executeWithMail(t, service, "", "mail", "attachments", "save-all", threadID, "--dir", directory, "--json")
	if result.ExitCode != 0 || stderr != "" || !strings.Contains(stdout, `"kind":"thread"`) {
		t.Fatalf("thread attachment save-all result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	for _, name := range []string{"one.txt", "two.txt"} {
		content, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || !strings.Contains(string(content), "thread-attachment-att_thread_") {
			t.Fatalf("saved %s content=%q err=%v", name, content, err)
		}
	}
	if len(service.selectors) != 2 || service.selectors[0] != threadID || service.selectors[1] != threadID {
		t.Fatalf("attachment selectors=%v", service.selectors)
	}
}

func TestSafeAttachmentFilenameStripsTraversalAndPreservesUnicode(t *testing.T) {
	values := []maildomain.Attachment{
		{ID: "att_one", Filename: "../../résumé.pdf", MediaType: "application/pdf"},
		{ID: "att_two", Filename: "CON", MediaType: "text/plain"},
		{ID: "att_three", Filename: "", MediaType: "message/rfc822"},
	}
	got := []string{
		safeAttachmentFilename(values[0]),
		safeAttachmentFilename(values[1]),
		safeAttachmentFilename(values[2]),
	}
	if got[0] != "_.._résumé.pdf" || got[1] != "_CON" || !strings.HasPrefix(got[2], "attachment-") || !strings.HasSuffix(got[2], ".eml") {
		t.Fatalf("safe names = %v", got)
	}
	for _, name := range got {
		if filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			t.Fatalf("unsafe derived filename = %q", name)
		}
	}
}

func TestUniqueAttachmentNamesUseDeterministicCaseInsensitiveSuffixes(t *testing.T) {
	values := []maildomain.Attachment{
		{Filename: "report.pdf"},
		{Filename: "REPORT.pdf"},
		{Filename: "report.pdf"},
	}
	got := uniqueAttachmentNames(values)
	want := []string{"report.pdf", "REPORT-2.pdf", "report-3.pdf"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("unique names = %v want %v", got, want)
	}
}

func TestInstallAttachmentBatchRollsBackNoClobberFailure(t *testing.T) {
	directory := t.TempDir()
	firstTemp := filepath.Join(directory, "first.tmp")
	secondTemp := filepath.Join(directory, "second.tmp")
	firstTarget := filepath.Join(directory, "first.bin")
	secondTarget := filepath.Join(directory, "second.bin")
	if err := os.WriteFile(firstTemp, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondTemp, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondTarget, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := installAttachmentBatch([]preparedAttachment{
		{temporary: firstTemp, target: firstTarget},
		{temporary: secondTemp, target: secondTarget},
	}, false)
	if err == nil {
		t.Fatal("batch install unexpectedly succeeded")
	}
	if _, statErr := os.Stat(firstTarget); !os.IsNotExist(statErr) {
		t.Fatalf("partial first target remained: %v", statErr)
	}
	content, readErr := os.ReadFile(secondTarget)
	if readErr != nil || string(content) != "existing" {
		t.Fatalf("existing target changed: %q err=%v", content, readErr)
	}
}

func TestInstallAttachmentBatchRollsBackWhenStagingCleanupFailsAfterLink(t *testing.T) {
	directory := t.TempDir()
	temporary := filepath.Join(directory, "first.tmp")
	target := filepath.Join(directory, "first.bin")
	if err := os.WriteFile(temporary, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := realAttachmentBatchFS
	failed := false
	fs.remove = func(path string) error {
		if path == temporary && !failed {
			failed = true
			return errors.New("injected staged cleanup failure")
		}
		return os.Remove(path)
	}
	err := installAttachmentBatchWithFS([]preparedAttachment{{temporary: temporary, target: target}}, false, fs)
	if err == nil {
		t.Fatal("batch install unexpectedly succeeded")
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("linked final target survived failed staged cleanup: %v", statErr)
	}
	if content, readErr := os.ReadFile(temporary); readErr != nil || string(content) != "first" {
		t.Fatalf("staged source should remain available for caller cleanup: %q err=%v", content, readErr)
	}
}

func TestInstallAttachmentBatchReportsBackupCleanupAfterCompleteForceInstall(t *testing.T) {
	directory := t.TempDir()
	temporary := filepath.Join(directory, "replacement.tmp")
	target := filepath.Join(directory, "target.bin")
	if err := os.WriteFile(temporary, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := realAttachmentBatchFS
	backupRemoves := 0
	fs.remove = func(path string) error {
		if strings.Contains(filepath.Base(path), ".icloud-attachment-backup-") {
			backupRemoves++
			if backupRemoves == 2 {
				return errors.New("injected backup cleanup failure")
			}
		}
		return os.Remove(path)
	}
	err := installAttachmentBatchWithFS([]preparedAttachment{{temporary: temporary, target: target}}, true, fs)
	if err == nil || !strings.Contains(err.Error(), "complete attachment set was installed") {
		t.Fatalf("backup cleanup error = %v", err)
	}
	content, readErr := os.ReadFile(target)
	if readErr != nil || string(content) != "new" {
		t.Fatalf("completed final target = %q err=%v", content, readErr)
	}
}

func TestAttachmentDestinationRejectsSymlinkAndDirectory(t *testing.T) {
	directory := t.TempDir()
	realFile := filepath.Join(directory, "real.bin")
	if err := os.WriteFile(realFile, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(directory, "linked.bin")
	if err := os.Symlink(realFile, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, target := range []string{symlink, directory} {
		err := preflightOutput(target, true)
		var appErr *AppError
		if !errors.As(err, &appErr) || appErr.Code != ErrorSafety {
			t.Fatalf("unsafe destination %q error = %#v", target, err)
		}
	}
	content, err := os.ReadFile(realFile)
	if err != nil || string(content) != "existing" {
		t.Fatalf("symlink target changed: %q err=%v", content, err)
	}
}
