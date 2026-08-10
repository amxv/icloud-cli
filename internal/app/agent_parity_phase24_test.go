package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-msgauth/dkim"
)

type phase24OneClickClient struct {
	calls   int
	targets []string
	err     error
}

func (client *phase24OneClickClient) UnsubscribeOneClick(_ context.Context, target string) error {
	client.calls++
	client.targets = append(client.targets, target)
	return client.err
}

func phase24UnsignedMessage(unsubscribe, post string) string {
	lines := []string{
		"From: Example List <list@example.test>",
		"To: owner@icloud.com",
		"Date: Mon, 10 Aug 2026 20:20:00 +0000",
		"Message-ID: <phase24@example.test>",
		"Subject: Phase 24 unsubscribe fixture",
		"List-Unsubscribe: " + unsubscribe,
	}
	if post != "" {
		lines = append(lines, "List-Unsubscribe-Post: "+post)
	}
	lines = append(lines, "Content-Type: text/plain; charset=utf-8", "", "phase24 body")
	return strings.Join(lines, "\r\n") + "\r\n"
}

func phase24SignedMessage(t *testing.T, signPost bool) (string, string) {
	t.Helper()
	unsigned := phase24UnsignedMessage("<https://unsubscribe.example.test/u/OPAQUE-TOKEN>, <mailto:leave@example.test?subject=unsubscribe&body=remove%20me>", "List-Unsubscribe=One-Click")
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	headerKeys := []string{"From", "To", "Date", "Message-ID", "Subject", "List-Unsubscribe"}
	if signPost {
		headerKeys = append(headerKeys, "List-Unsubscribe-Post")
	}
	var signed bytes.Buffer
	if err := dkim.Sign(&signed, strings.NewReader(unsigned), &dkim.SignOptions{
		Domain: "lists.example.test", Selector: "phase24", Signer: key,
		HeaderCanonicalization: dkim.CanonicalizationRelaxed,
		BodyCanonicalization:   dkim.CanonicalizationRelaxed,
		HeaderKeys:             headerKeys,
	}); err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return signed.String(), "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(der)
}

func seedPhase24ImportedMessage(t *testing.T, commands *accountCommands, home, envName, raw, idempotencyKey string) maildomain.Message {
	t.Helper()
	seedPhase21Checkpoint(t, *commands, home, envName)
	path := filepath.Join(t.TempDir(), "phase24.eml")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	status := &phase21IMAPClient{statuses: []transport.MailboxStatus{{UIDValidity: 100, UIDNext: 21, Messages: 20}}}
	appender := &fakeDraftAppender{results: []transport.MailboxAppendResult{{Mailbox: "INBOX", UIDValidity: 100, UID: 21}}}
	remote := &phase21RemoteClient{uidValidity: 100, rawByUID: map[uint32]string{21: raw}}
	commands.imap, commands.appendMailbox, commands.remote = status, appender, remote
	_, _, _, importValue, message, _, err := commands.MailMessageImport(context.Background(), home, "", path, maildomain.MessageImportRequest{Folder: "inbox", IdempotencyKey: idempotencyKey})
	if err != nil || importValue.Status != maildomain.MessageImportConfirmed || message.ID == "" || appender.calls != 1 {
		t.Fatalf("seed import=%+v message=%+v appends=%d err=%v", importValue, message, appender.calls, err)
	}
	return message
}

func TestAgentParityPhase24OneClickRequiresSignedHeadersAndNeverLeaksEndpointOnReadOrPreview(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE24_ONECLICK_PASSWORD", "synthetic-phase24-oneclick-secret")
	commands, _ := newTestAccountCommands()
	raw, dnsRecord := phase24SignedMessage(t, true)
	message := seedPhase24ImportedMessage(t, &commands, home, "ICLOUD_PHASE24_ONECLICK_PASSWORD", raw, "phase24-oneclick-seed")
	oneClick := &phase24OneClickClient{}
	commands.unsubscribe = oneClick
	lookupCalls := 0
	commands.lookupTXT = func(_ context.Context, domain string) ([]string, error) {
		lookupCalls++
		if domain != "phase24._domainkey.lists.example.test" {
			return nil, fmt.Errorf("unexpected DKIM lookup %s", domain)
		}
		return []string{dnsRecord}, nil
	}

	_, stored, _, _, err := commands.MailMessage(context.Background(), home, "", message.ID)
	if err != nil || stored.Unsubscribe == nil || !stored.Unsubscribe.Available || stored.Unsubscribe.Methods[0].Kind != maildomain.UnsubscribeMethodOneClick {
		t.Fatalf("stored=%+v err=%v", stored.Unsubscribe, err)
	}
	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", message.ID, "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"unsubscribe"`) || !strings.Contains(stdout, `"host":"unsubscribe.example.test"`) || strings.Contains(stdout, "OPAQUE-TOKEN") || strings.Contains(stdout, "leave@example.test") || strings.Contains(stdout, "remove%20me") {
		t.Fatalf("message read code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	_, plan, _, err := commands.MailUnsubscribePreview(context.Background(), home, "", message.ID, "")
	if err != nil || plan.Selected == nil || plan.Selected.ID != "one-click" || lookupCalls != 0 || oneClick.calls != 0 {
		t.Fatalf("preview=%+v err=%v lookups=%d calls=%d", plan, err, lookupCalls, oneClick.calls)
	}
	encoded := fmt.Sprintf("%+v", plan)
	if strings.Contains(encoded, "OPAQUE-TOKEN") || strings.Contains(encoded, "leave@example.test") {
		t.Fatalf("preview leaked endpoint: %s", encoded)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "unsubscribe", message.ID, "--dry-run", "--json")
	if code != 0 || stderr != "" || lookupCalls != 0 || oneClick.calls != 0 || !strings.Contains(stdout, `"id":"one-click"`) || !strings.Contains(stdout, `"host":"unsubscribe.example.test"`) || strings.Contains(stdout, "OPAQUE-TOKEN") || strings.Contains(stdout, "leave@example.test") {
		t.Fatalf("unsubscribe dry-run code=%d stdout=%q stderr=%q lookups=%d calls=%d", code, stdout, stderr, lookupCalls, oneClick.calls)
	}
	jsonInput := fmt.Sprintf("{\"message_id\":%q}", message.ID)
	code, stdout, stderr = runWithAccountCommands(t, commands, jsonInput, "--home", home, "mail", "unsubscribe", "--input-json", "-", "--dry-run", "--json")
	if code != 0 || stderr != "" || lookupCalls != 0 || oneClick.calls != 0 || !strings.Contains(stdout, `"id":"one-click"`) || strings.Contains(stdout, "OPAQUE-TOKEN") {
		t.Fatalf("unsubscribe JSON dry-run code=%d stdout=%q stderr=%q lookups=%d calls=%d", code, stdout, stderr, lookupCalls, oneClick.calls)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "unsubscribe", message.ID)
	if code != cli.ExitSafety || stdout != "" || !strings.Contains(stderr, "requires --yes") || lookupCalls != 0 || oneClick.calls != 0 {
		t.Fatalf("non-tty guard code=%d stdout=%q stderr=%q lookups=%d calls=%d", code, stdout, stderr, lookupCalls, oneClick.calls)
	}
	code, stdout, stderr = runWithAccountCommandsReader(commands, testTerminalReader{Reader: strings.NewReader("n\n")}, "--home", home, "mail", "unsubscribe", message.ID)
	if code != cli.ExitSafety || !strings.Contains(stdout, "mail-unsubscribe-review") || !strings.Contains(stderr, "Unsubscribe this message") || lookupCalls != 0 || oneClick.calls != 0 {
		t.Fatalf("interactive cancel code=%d stdout=%q stderr=%q lookups=%d calls=%d", code, stdout, stderr, lookupCalls, oneClick.calls)
	}

	_, _, result, _, err := commands.MailUnsubscribe(context.Background(), home, "", message.ID, "")
	if err != nil || lookupCalls != 1 || oneClick.calls != 1 || len(oneClick.targets) != 1 || oneClick.targets[0] != "https://unsubscribe.example.test/u/OPAQUE-TOKEN" || result.Method != maildomain.UnsubscribeMethodOneClick || result.Host != "unsubscribe.example.test" || result.DKIMDomain != "lists.example.test" {
		t.Fatalf("result=%+v err=%v lookups=%d calls=%d targets=%q", result, err, lookupCalls, oneClick.calls, oneClick.targets)
	}
}

func TestAgentParityPhase24OneClickRejectsValidSignatureThatDoesNotCoverPostHeader(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE24_DKIM_PASSWORD", "synthetic-phase24-dkim-secret")
	commands, _ := newTestAccountCommands()
	raw, dnsRecord := phase24SignedMessage(t, false)
	message := seedPhase24ImportedMessage(t, &commands, home, "ICLOUD_PHASE24_DKIM_PASSWORD", raw, "phase24-dkim-seed")
	oneClick := &phase24OneClickClient{}
	commands.unsubscribe = oneClick
	commands.lookupTXT = func(_ context.Context, _ string) ([]string, error) { return []string{dnsRecord}, nil }
	_, _, _, _, err := commands.MailUnsubscribe(context.Background(), home, "", message.ID, "")
	if err == nil || cli.NormalizeError(err).Code != cli.ErrorSafety || oneClick.calls != 0 {
		t.Fatalf("err=%v normalized=%+v calls=%d", err, cli.NormalizeError(err), oneClick.calls)
	}
}

func TestAgentParityPhase24MailtoUsesDurableSendIdempotencyAndRequiresExplicitAlternativeSelection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE24_MAILTO_PASSWORD", "synthetic-phase24-mailto-secret")
	commands, _ := newTestAccountCommands()
	raw := phase24UnsignedMessage("<mailto:first@example.test?subject=remove&body=first>, <mailto:second@example.test?subject=unsubscribe&body=second>", "")
	message := seedPhase24ImportedMessage(t, &commands, home, "ICLOUD_PHASE24_MAILTO_PASSWORD", raw, "phase24-mailto-seed")
	sender := &fakeSMTPSender{}
	commands.sender = sender

	_, plan, _, err := commands.MailUnsubscribePreview(context.Background(), home, "", message.ID, "")
	if err != nil || !plan.SelectionRequired || plan.Selected != nil || len(plan.Alternatives) != 2 {
		t.Fatalf("preview=%+v err=%v", plan, err)
	}
	_, _, _, _, err = commands.MailUnsubscribe(context.Background(), home, "", message.ID, "")
	if err == nil || cli.NormalizeError(err).Code != cli.ErrorUsage || sender.calls != 0 {
		t.Fatalf("ambiguous err=%v calls=%d", err, sender.calls)
	}

	_, _, result, _, err := commands.MailUnsubscribe(context.Background(), home, "", message.ID, "mailto:2")
	if err != nil || sender.calls != 1 || result.Method != maildomain.UnsubscribeMethodMailto || result.Host != "example.test" || result.AttemptID == "" || result.Reused {
		t.Fatalf("first result=%+v err=%v calls=%d", result, err, sender.calls)
	}
	messageText := string(sender.messages[0])
	if !strings.Contains(messageText, "To: <second@example.test>") || !strings.Contains(messageText, "Subject: unsubscribe") || !strings.HasSuffix(messageText, "\r\nsecond") || strings.Contains(strings.ToLower(messageText), "bcc:") || strings.Contains(messageText, "first@example.test") {
		t.Fatalf("mailto message=%q", messageText)
	}

	_, _, replay, _, err := commands.MailUnsubscribe(context.Background(), home, "", message.ID, "mailto:2")
	if err != nil || sender.calls != 1 || !replay.Reused || replay.AttemptID != result.AttemptID {
		t.Fatalf("replay=%+v err=%v calls=%d first=%+v", replay, err, sender.calls, result)
	}
}

func TestAgentParityPhase24OneClickTransportFailureIsSanitized(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_PHASE24_HTTP_PASSWORD", "synthetic-phase24-http-secret")
	commands, _ := newTestAccountCommands()
	raw, dnsRecord := phase24SignedMessage(t, true)
	message := seedPhase24ImportedMessage(t, &commands, home, "ICLOUD_PHASE24_HTTP_PASSWORD", raw, "phase24-http-seed")
	commands.lookupTXT = func(_ context.Context, _ string) ([]string, error) { return []string{dnsRecord}, nil }
	commands.unsubscribe = &phase24OneClickClient{err: transport.NewFailure(transport.StageHTTPUnsubscribe, transport.FailureUnavailable, true, errors.New("endpoint OPAQUE-TOKEN failure"))}
	_, _, _, _, err := commands.MailUnsubscribe(context.Background(), home, "", message.ID, "one-click")
	if err == nil || cli.NormalizeError(err).Code != cli.ErrorUnavailable || strings.Contains(err.Error(), "OPAQUE-TOKEN") {
		t.Fatalf("err=%v normalized=%+v", err, cli.NormalizeError(err))
	}
}
