package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

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
