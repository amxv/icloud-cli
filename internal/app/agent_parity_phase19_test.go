package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestAgentParityPhase19CLIInlineCIDBindsIdempotencyAndParsesBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase19-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)

	root := t.TempDir()
	ordinaryPath := filepath.Join(root, "report.txt")
	inlinePath := filepath.Join(root, "hero.png")
	if err := os.WriteFile(ordinaryPath, []byte("phase19 ordinary attachment"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inlinePath, []byte("phase19 inline image bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--home", home, "mail", "send", "--to", "recipient@example.test",
		"--subject", "Phase 19 inline CID", "--body", "Plain fallback",
		"--html-body", `<p>Inline:</p><img src="cid:hero@example.test">`,
		"--attach", ordinaryPath, "--attach", inlinePath,
		"--attach-disposition", inlinePath + "=inline", "--attach-cid", inlinePath + "=hero@example.test",
		"--idempotency-key", "phase19-inline-cid", "--yes", "--json",
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
	if code != 0 || stderr != "" || sender.calls != 1 || !strings.Contains(stdout, `"attachment_count":2`) {
		t.Fatalf("inline send code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
	content, err := maildomain.ParseNormalizedWithAttachmentsContext(context.Background(), strings.NewReader(string(sender.messages[0])), func(_ context.Context, _ maildomain.MIMEPart, body io.Reader) (maildomain.AttachmentBlob, error) {
		hash := sha256.New()
		size, copyErr := io.Copy(hash, body)
		return maildomain.AttachmentBlob{ContentHash: hex.EncodeToString(hash.Sum(nil)), StorageKey: "phase19/app", ByteSize: size}, copyErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(content.Attachments) != 2 || content.Attachments[0].Filename != "hero.png" || !content.Attachments[0].Inline || content.Attachments[0].Disposition != "inline" || content.Attachments[0].ContentID != "hero@example.test" || content.Attachments[1].Filename != "report.txt" || content.Attachments[1].Disposition != "attachment" {
		t.Fatalf("parsed outbound attachments=%+v", content.Attachments)
	}

	changed := append([]string(nil), args...)
	for index, value := range changed {
		if value == inlinePath+"=hero@example.test" {
			changed[index] = inlinePath + "=changed@example.test"
		}
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", changed...)
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "idempotency key") || sender.calls != 1 {
		t.Fatalf("changed-CID idempotency code=%d stdout=%q stderr=%q calls=%d", code, stdout, stderr, sender.calls)
	}
}

func TestAgentParityPhase19StructuredComposeAndHelpExposeInlineControls(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-phase19-json-secret")
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	inlinePath := filepath.Join(t.TempDir(), "json-inline.png")
	if err := os.WriteFile(inlinePath, []byte("phase19 json inline bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{
		"to": []string{"recipient@example.test"}, "subject": "Phase 19 structured inline",
		"body": "Plain fallback", "html_body": `<img src="cid:json-inline@example.test">`,
		"attachments": []map[string]any{{
			"path": inlinePath, "filename": "json-inline.png", "media_type": "image/png",
			"disposition": "inline", "content_id": "json-inline@example.test",
		}},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(t.TempDir(), "send.json")
	if err := os.WriteFile(requestPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "send", "--input-json", requestPath, "--dry-run", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"disposition":"inline"`) || !strings.Contains(stdout, `"content_id":"json-inline@example.test"`) {
		t.Fatalf("structured inline preview code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, args := range [][]string{
		{"--home", home, "mail", "send", "--help"},
		{"--home", home, "mail", "reply", "--help"},
		{"--home", home, "mail", "reply-all", "--help"},
		{"--home", home, "mail", "forward", "--help"},
		{"--home", home, "mail", "draft", "create", "--help"},
		{"--home", home, "mail", "draft", "update", "--help"},
	} {
		code, stdout, stderr = runWithAccountCommands(t, commands, "", args...)
		if code != 0 || stderr != "" || !strings.Contains(stdout, "--attach-disposition") || !strings.Contains(stdout, "--attach-cid") {
			t.Fatalf("inline help args=%v code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
}

func TestAgentParityPhase19DraftProviderRoundTripRetainsInlineCID(t *testing.T) {
	commands, _ := newTestAccountCommands()
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "Drafts"}}}
	inbox := &appendedDraftInboxClient{appender: appender, uid: 77, uidValidity: 300}
	commands.appendMailbox = appender
	commands.mail = inbox
	home := setupDraftAccount(t, commands)
	inlinePath := filepath.Join(t.TempDir(), "draft-inline.png")
	if err := os.WriteFile(inlinePath, []byte("phase19 draft inline bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, created, _, err := commands.MailDraftCreate(context.Background(), home, "", maildomain.SendRequest{
		To: []maildomain.Address{{Email: "recipient@example.test"}}, Subject: "Phase19 draft inline",
		TextBody: "Plain draft fallback", HTMLBody: `<img src="cid:draft-hero@example.test">`,
		Attachments: []maildomain.OutboundAttachment{{
			SourcePath: inlinePath, Filename: "draft-inline.png", MediaType: "image/png",
			Disposition: maildomain.AttachmentDispositionInline, ContentID: "draft-hero@example.test",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Message.Content == nil || len(created.Message.Content.Attachments) != 1 || !created.Message.Content.Attachments[0].Inline || created.Message.Content.Attachments[0].ContentID != "draft-hero@example.test" {
		t.Fatalf("created draft content=%+v", created.Message.Content)
	}
	_, summary, values, _, err := commands.MailDraftSync(context.Background(), home, "", "", created.ID, 20)
	if err != nil || appender.calls != 1 || inbox.calls != 2 || summary.Appended != 1 || summary.Reconciled != 1 || len(values) != 1 || values[0].SyncState != maildomain.DraftSynced {
		t.Fatalf("draft roundtrip summary=%+v values=%+v append=%d fetch=%d err=%v", summary, values, appender.calls, inbox.calls, err)
	}
	if values[0].Message.Content == nil || len(values[0].Message.Content.Attachments) != 1 {
		t.Fatalf("provider-roundtrip content=%+v", values[0].Message.Content)
	}
	attachment := values[0].Message.Content.Attachments[0]
	if attachment.Disposition != "inline" || !attachment.Inline || attachment.ContentID != "draft-hero@example.test" {
		t.Fatalf("provider-roundtrip attachment=%+v", attachment)
	}
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	reconstructed, err := draftSendRequest(context.Background(), paths, values[0], "")
	if err != nil || len(reconstructed.Attachments) != 1 || reconstructed.Attachments[0].Disposition != maildomain.AttachmentDispositionInline || reconstructed.Attachments[0].ContentID != "draft-hero@example.test" {
		t.Fatalf("reconstructed draft request=%+v err=%v", reconstructed.Attachments, err)
	}
}
