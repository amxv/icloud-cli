package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type mboxExportSeed struct {
	accountID    string
	folderID     string
	rawByUID     map[uint32][]byte
	storageByUID map[uint32]string
}

func seedMboxExportMailbox(t *testing.T, commands accountCommands, home, envName string, count int) mboxExportSeed {
	t.Helper()
	commands.folders = oneInboxFolderClient()
	addAndDiscoverInbox(t, commands, home, envName)
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	folderValue, err := state.Folders().Resolve(context.Background(), accountValue.ID, "inbox")
	if err != nil {
		t.Fatal(err)
	}
	records := make([]maildomain.IngestRecord, 0, count)
	rawByUID := make(map[uint32][]byte, count)
	storageByUID := make(map[uint32]string, count)
	for index := 1; index <= count; index++ {
		uid := uint32(index)
		raw := mboxExportMailboxFixture(uid)
		staged, err := commands.blobStager(paths).Stage(context.Background(), bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			t.Fatal(err)
		}
		if err := staged.Commit(); err != nil {
			t.Fatal(err)
		}
		parsed, err := maildomain.ParsePreview(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, maildomain.BuildIngestRecord(
			fmt.Sprintf("msg_mboxExport_%03d", index), fmt.Sprintf("loc_mboxExport_%03d", index),
			accountValue.ID, folderValue.ID, 100, uid, []string{`\Seen`},
			time.Date(2026, 8, 10, 10, 0, index%60, 0, time.UTC), int64(len(raw)),
			staged.ContentHash, staged.StorageKey, staged.ByteSize, parsed,
		))
		rawByUID[uid] = raw
		storageByUID[uid] = staged.StorageKey
	}
	_, err = state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: "chk_mboxExport", AccountID: accountValue.ID, FolderID: folderValue.ID,
		UIDValidity: 100, Bootstrap: true, LastCommittedUID: uint32(count), LastScannedUID: uint32(count),
		AttemptedAt: time.Date(2026, 8, 10, 19, 0, 0, 0, time.UTC), Records: records,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mboxExportSeed{accountID: accountValue.ID, folderID: folderValue.ID, rawByUID: rawByUID, storageByUID: storageByUID}
}

func mboxExportMailboxFixture(uid uint32) []byte {
	lineBreak := "\r\n"
	if uid%2 == 0 {
		lineBreak = "\n"
	}
	body := fmt.Sprintf("mboxExport body uid=%d", uid)
	if uid == 55 {
		body = "From bare separator-looking body" + lineBreak + ">From already quoted" + lineBreak + ">>>From deeply quoted"
	}
	if uid == 42 {
		body = strings.Join([]string{
			"--mboxExport-boundary",
			"Content-Type: text/plain; charset=utf-8",
			"",
			"large attachment marker",
			"--mboxExport-boundary",
			"Content-Type: application/octet-stream",
			"Content-Disposition: attachment; filename=large.bin",
			"Content-Transfer-Encoding: base64",
			"",
			strings.Repeat("QUFB", 300000),
			"--mboxExport-boundary--",
		}, lineBreak)
	}
	headers := []string{
		"From: Fixture Sender <sender@example.test>",
		"To: owner@icloud.com",
		"Date: Mon, 10 Aug 2026 10:00:00 +0000",
		fmt.Sprintf("Message-ID: <mboxExport-%03d@example.test>", uid),
		fmt.Sprintf("Subject: mbox export 日本語 café %03d", uid),
	}
	if uid == 42 {
		headers = append(headers, "MIME-Version: 1.0", "Content-Type: multipart/mixed; boundary=mboxExport-boundary")
	} else {
		headers = append(headers, "Content-Type: text/plain; charset=utf-8")
	}
	return []byte(strings.Join(append(headers, "", body), lineBreak) + lineBreak)
}

func TestMailboxExportStreamsMultiplePagesInProviderOrderAndRoundTripsMboxRD(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MBOX_EXPORT_PASSWORD", "synthetic-mboxExport-secret")
	commands, _ := newTestAccountCommands()
	seed := seedMboxExportMailbox(t, commands, home, "ICLOUD_MBOX_EXPORT_PASSWORD", 105)
	if err := os.Unsetenv("ICLOUD_MBOX_EXPORT_PASSWORD"); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "inbox.mbox")
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "mailbox", "export", "inbox", "--output", archivePath, "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"count":105`) || !strings.Contains(stdout, `"complete":true`) || !strings.Contains(stdout, `"uid":1`) || strings.Contains(stdout, archivePath) {
		t.Fatalf("export code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("archive mode=%o", info.Mode().Perm())
	}
	var envelope struct {
		Data struct {
			Summary maildomain.MailboxExportSummary `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("decode export summary: %v stdout=%q", err, stdout)
	}
	if envelope.Data.Summary.Bytes != info.Size() || envelope.Data.Summary.Count != 105 || !envelope.Data.Summary.Complete {
		t.Fatalf("summary=%+v file_size=%d", envelope.Data.Summary, info.Size())
	}
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	messages := decodeMboxRDArchive(t, archive)
	if len(messages) != 105 {
		t.Fatalf("decoded message count=%d", len(messages))
	}
	for index, raw := range messages {
		uid := uint32(105 - index)
		want := seed.rawByUID[uid]
		if !bytes.Equal(raw, want) {
			t.Fatalf("uid=%d raw mismatch got=%d want=%d", uid, len(raw), len(want))
		}
	}
	if !bytes.Contains(archive, []byte("\n>From bare separator-looking body")) || !bytes.Contains(archive, []byte("\n>>From already quoted")) || !bytes.Contains(archive, []byte("\n>>>>From deeply quoted")) {
		t.Fatal("archive is missing required mboxrd escaping")
	}
	if len(seed.rawByUID[42]) < 1<<20 {
		t.Fatalf("large attachment fixture unexpectedly small: %d", len(seed.rawByUID[42]))
	}
	assertMboxExportPythonMailboxReference(t, archivePath, 105, "<mboxExport-105@example.test>", "<mboxExport-001@example.test>")
}

func TestMailboxExportStdoutIsNativeOnlyAndSummaryUsesStderr(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MBOX_EXPORT_STDOUT_PASSWORD", "synthetic-mboxExport-stdout-secret")
	commands, _ := newTestAccountCommands()
	seed := seedMboxExportMailbox(t, commands, home, "ICLOUD_MBOX_EXPORT_STDOUT_PASSWORD", 2)
	if err := os.Unsetenv("ICLOUD_MBOX_EXPORT_STDOUT_PASSWORD"); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "mailbox", "export", "inbox", "--output", "-")
	if code != 0 || !strings.HasPrefix(stdout, "From sender@example.test ") || strings.Contains(stdout, "mailbox-export") || !compactContainsAll(stderr, "mailbox-export", "count=2", "complete=true") {
		t.Fatalf("stdout export code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	messages := decodeMboxRDArchive(t, []byte(stdout))
	if len(messages) != 2 || !bytes.Equal(messages[0], seed.rawByUID[2]) || !bytes.Equal(messages[1], seed.rawByUID[1]) {
		t.Fatalf("stdout archive decoded=%d", len(messages))
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "mailbox", "export", "inbox", "--output", "-", "--json")
	if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, "cannot share stdout") {
		t.Fatalf("stdout json code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestMailboxExportCorruptRawFailsAtomicallyAndPreservesExistingTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MBOX_EXPORT_CORRUPT_PASSWORD", "synthetic-mboxExport-corrupt-secret")
	commands, _ := newTestAccountCommands()
	seed := seedMboxExportMailbox(t, commands, home, "ICLOUD_MBOX_EXPORT_CORRUPT_PASSWORD", 3)
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	corruptPath := filepath.Join(paths.BlobRoot, filepath.FromSlash(seed.storageByUID[2]))
	corrupt, err := os.ReadFile(corruptPath)
	if err != nil {
		t.Fatal(err)
	}
	corrupt[len(corrupt)/2] ^= 0xff
	if err := os.WriteFile(corruptPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv("ICLOUD_MBOX_EXPORT_CORRUPT_PASSWORD"); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	newTarget := filepath.Join(directory, "new.mbox")
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "mailbox", "export", "inbox", "--output", newTarget)
	if code != cli.ExitUnavailable || stdout != "" || !strings.Contains(stderr, "corrupt") {
		t.Fatalf("corrupt export code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(newTarget); !os.IsNotExist(err) {
		t.Fatalf("failed export installed target: %v", err)
	}
	existing := filepath.Join(directory, "existing.mbox")
	if err := os.WriteFile(existing, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "mailbox", "export", "inbox", "--output", existing, "--force")
	if code != cli.ExitUnavailable || stdout != "" || !strings.Contains(stderr, "corrupt") {
		t.Fatalf("forced corrupt export code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	value, err := os.ReadFile(existing)
	if err != nil || string(value) != "sentinel" {
		t.Fatalf("existing target changed value=%q err=%v", value, err)
	}
	if err := os.Remove(corruptPath); err != nil {
		t.Fatal(err)
	}
	missingTarget := filepath.Join(directory, "missing.mbox")
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "mailbox", "export", "inbox", "--output", missingTarget)
	if code != cli.ExitUnavailable || stdout != "" || stderr == "" {
		t.Fatalf("missing export code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(missingTarget); !os.IsNotExist(err) {
		t.Fatalf("missing-raw export installed target: %v", err)
	}
}

func TestMailboxExportStrictJSONInputAndNoClobberSymlinkSafety(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MBOX_EXPORT_JSON_PASSWORD", "synthetic-mboxExport-json-secret")
	commands, _ := newTestAccountCommands()
	seedMboxExportMailbox(t, commands, home, "ICLOUD_MBOX_EXPORT_JSON_PASSWORD", 1)
	if err := os.Unsetenv("ICLOUD_MBOX_EXPORT_JSON_PASSWORD"); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "archive.mbox")
	request := filepath.Join(directory, "request.json")
	payload, err := json.Marshal(map[string]any{"folder": "inbox", "format": "mboxrd", "output": target})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(request, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "mailbox", "export", "--input-json", request, "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"count":1`) || strings.Contains(stdout, target) {
		t.Fatalf("json export code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "mailbox", "export", "inbox", "--output", target)
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "already exists") {
		t.Fatalf("no-clobber code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	link := filepath.Join(directory, "link.mbox")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "mailbox", "export", "inbox", "--output", link, "--force")
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "not a regular file") {
		t.Fatalf("symlink code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func decodeMboxRDArchive(t *testing.T, archive []byte) [][]byte {
	t.Helper()
	if len(archive) == 0 {
		return nil
	}
	if !bytes.HasPrefix(archive, []byte("From ")) {
		t.Fatalf("archive does not start with mbox separator")
	}
	result := [][]byte{}
	position := 0
	for position < len(archive) {
		separatorEndRelative := bytes.IndexByte(archive[position:], '\n')
		if separatorEndRelative < 0 {
			t.Fatal("unterminated mbox separator")
		}
		messageStart := position + separatorEndRelative + 1
		nextRelative := bytes.Index(archive[messageStart:], []byte("\nFrom "))
		var messageEnd int
		if nextRelative >= 0 {
			messageEnd = messageStart + nextRelative
			position = messageEnd + 1
		} else {
			if archive[len(archive)-1] != '\n' {
				t.Fatal("mbox archive is missing final framing newline")
			}
			messageEnd = len(archive) - 1
			position = len(archive)
		}
		result = append(result, unescapeMboxRDMessage(archive[messageStart:messageEnd]))
	}
	return result
}

func unescapeMboxRDMessage(value []byte) []byte {
	var output bytes.Buffer
	for len(value) > 0 {
		lineEnd := bytes.IndexByte(value, '\n')
		line := value
		if lineEnd >= 0 {
			line = value[:lineEnd+1]
			value = value[lineEnd+1:]
		} else {
			value = nil
		}
		count := 0
		for count < len(line) && line[count] == '>' {
			count++
		}
		if count > 0 && bytes.HasPrefix(line[count:], []byte("From ")) {
			line = line[1:]
		}
		output.Write(line)
	}
	return output.Bytes()
}

func assertMboxExportPythonMailboxReference(t *testing.T, archivePath string, count int, firstID, lastID string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 reference mailbox parser is unavailable")
	}
	script := `
import json, mailbox, sys
box = mailbox.mbox(sys.argv[1], create=False)
items = list(box)
print(json.dumps({"count": len(items), "first": items[0]["Message-ID"], "last": items[-1]["Message-ID"]}))
`
	output, err := exec.Command(python, "-c", script, archivePath).CombinedOutput()
	if err != nil {
		t.Fatalf("python mailbox parser: %v: %s", err, output)
	}
	var parsed struct {
		Count int    `json:"count"`
		First string `json:"first"`
		Last  string `json:"last"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &parsed); err != nil {
		t.Fatalf("decode python mailbox result: %v: %s", err, output)
	}
	if parsed.Count != count || parsed.First != firstID || parsed.Last != lastID {
		t.Fatalf("reference parser result=%+v", parsed)
	}
}
