package icloud

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

func TestRemoteSearchSummaryAndHydrationFetchAgainstMemoryIMAPServer(t *testing.T) {
	const (
		username = "remote-user"
		password = "remote-password"
	)
	memServer := imapmemserver.New()
	user := imapmemserver.NewUser(username, password)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		messageID string
		subject   string
		body      string
		flags     []imap.Flag
		at        time.Time
	}{
		{"remote-1@example.test", "Needle old", "needle alpha body", []imap.Flag{imap.FlagSeen, imap.FlagFlagged}, time.Date(2026, 8, 4, 8, 0, 0, 0, time.UTC)},
		{"remote-2@example.test", "Other", "needle beta body", nil, time.Date(2026, 8, 5, 8, 0, 0, 0, time.UTC)},
		{"remote-3@example.test", "Needle newest", "needle gamma body", []imap.Flag{imap.FlagFlagged}, time.Date(2026, 8, 6, 8, 0, 0, 0, time.UTC)},
	}
	for _, fixture := range fixtures {
		raw := remoteMailFixture(fixture.messageID, fixture.subject, fixture.body)
		if _, err := user.Append("INBOX", &literalReader{Reader: strings.NewReader(raw), size: int64(len(raw))}, &imap.AppendOptions{
			Time: fixture.at, Flags: fixture.flags,
		}); err != nil {
			t.Fatal(err)
		}
	}
	memServer.AddUser(user)
	client, closeServer := newRemoteMemoryClient(t, memServer)
	defer closeServer()
	auth := transport.AuthRequest{Username: username, Password: []byte(password)}

	page, err := client.SearchRemote(context.Background(), auth, transport.RemoteSearchRequest{
		Mailbox: "INBOX", Limit: 1,
		Criteria: transport.RemoteSearchCriteria{Text: []string{"needle"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.UIDValidity != 1 || len(page.Messages) != 1 || page.Messages[0].UID != 3 || !page.MoreAvailable || page.Partial {
		t.Fatalf("remote page = %+v", page)
	}
	if page.CandidateUIDs != 3 || page.Messages[0].Envelope.Subject != "Needle newest" || !strings.Contains(page.Messages[0].Snippet, "needle gamma body") {
		t.Fatalf("remote summary = %+v", page.Messages[0])
	}
	if len(page.Messages[0].Snippet) > 1500 {
		t.Fatalf("remote snippet is not bounded: %d bytes", len(page.Messages[0].Snippet))
	}

	unreadFlagged, err := client.SearchRemote(context.Background(), auth, transport.RemoteSearchRequest{
		Mailbox: "INBOX", ExpectedUIDValidity: 1, Limit: 20,
		Criteria: transport.RemoteSearchCriteria{
			Subject: "Needle", Unread: boolPointer(true), Flagged: boolPointer(true),
			After: timePointerRemote(time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unreadFlagged.Messages) != 1 || unreadFlagged.Messages[0].UID != 3 {
		t.Fatalf("filtered remote page = %+v", unreadFlagged)
	}

	summary, err := client.FetchRemoteSummary(context.Background(), auth, transport.RemoteFetchRequest{
		Mailbox: "INBOX", ExpectedUIDValidity: 1, UID: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Message.UID != 2 || summary.Message.Envelope.MessageID != "remote-2@example.test" || !strings.Contains(summary.Message.Snippet, "needle beta body") {
		t.Fatalf("remote summary result = %+v", summary)
	}

	root := t.TempDir()
	fetched, err := client.FetchRemoteMessage(context.Background(), auth, transport.RemoteFetchRequest{
		Mailbox: "INBOX", ExpectedUIDValidity: 1, UID: 2,
	}, blob.New(root))
	if err != nil {
		t.Fatal(err)
	}
	defer abortFetched([]transport.FetchedMessage{fetched.Message})
	if fetched.Message.Raw == nil || fetched.Message.UID != 2 {
		t.Fatalf("remote fetched message = %+v", fetched)
	}
	reader, err := fetched.Message.Raw.Open()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil || !strings.Contains(string(raw), "needle beta body") {
		t.Fatalf("remote raw err=%v content=%q", err, raw)
	}

}

func TestRemoteSearchCandidateScanIsBoundedAndVisible(t *testing.T) {
	const (
		username = "bounded-user"
		password = "bounded-password"
	)
	memServer := imapmemserver.New()
	user := imapmemserver.NewUser(username, password)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 300; index++ {
		raw := remoteMailFixture("bounded@example.test", "Ordinary", "ordinary body")
		if _, err := user.Append("INBOX", &literalReader{Reader: strings.NewReader(raw), size: int64(len(raw))}, &imap.AppendOptions{
			Time: time.Date(2026, 8, 6, 0, index%60, 0, 0, time.UTC),
		}); err != nil {
			t.Fatal(err)
		}
	}
	memServer.AddUser(user)
	client, closeServer := newRemoteMemoryClient(t, memServer)
	defer closeServer()
	page, err := client.SearchRemote(context.Background(), transport.AuthRequest{
		Username: username, Password: []byte(password),
	}, transport.RemoteSearchRequest{
		Mailbox: "INBOX", Limit: 20, MaxCandidateUIDs: 256,
		Criteria: transport.RemoteSearchCriteria{Text: []string{"absent-token"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 0 || page.MoreAvailable || !page.Partial || page.CandidateUIDs != 256 {
		t.Fatalf("bounded remote page = %+v", page)
	}
}

func TestRemoteSearchCancellationClosesStalledSearchWithoutFetching(t *testing.T) {
	server := newIMAPServer(t, imapServerOptions{AuthOK: true, UIDValidity: 77, StallUIDSearch: true})
	defer server.Close()
	client := New(Config{
		IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: 40 * time.Millisecond,
		TLSConfig: server.ClientTLSConfig(),
	})
	_, err := client.SearchRemote(context.Background(), transport.AuthRequest{
		Username: "owner", Password: []byte("synthetic"),
	}, transport.RemoteSearchRequest{
		Mailbox: "INBOX", ExpectedUIDValidity: 77, Limit: 20,
		Criteria: transport.RemoteSearchCriteria{Subject: "fixture"},
	})
	assertFailure(t, err, transport.StageTimeout, transport.FailureUnavailable)
	commands := strings.Join(server.Commands(), ",")
	if !strings.Contains(commands, "UID") || strings.Contains(commands, "FETCH") || strings.Contains(commands, "STORE") || strings.Contains(commands, "APPEND") || strings.Contains(commands, "COPY") {
		t.Fatalf("remote cancellation commands = %q", commands)
	}
}

func TestRemoteSummaryRejectsStaleUIDValidityBeforeFetch(t *testing.T) {
	server := newIMAPServer(t, imapServerOptions{AuthOK: true, UIDValidity: 77})
	defer server.Close()
	client := New(Config{
		IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: time.Second,
		TLSConfig: server.ClientTLSConfig(),
	})
	_, err := client.FetchRemoteSummary(context.Background(), transport.AuthRequest{
		Username: "owner", Password: []byte("synthetic"),
	}, transport.RemoteFetchRequest{Mailbox: "INBOX", ExpectedUIDValidity: 99, UID: 1})
	var validity *transport.UIDValidityError
	if !errors.As(err, &validity) || validity.Expected != 99 || validity.Observed != 77 {
		t.Fatalf("stale UIDVALIDITY error = %v", err)
	}
	commands := strings.Join(server.Commands(), ",")
	if strings.Contains(commands, "UID") || strings.Contains(commands, "FETCH") {
		t.Fatalf("stale reference fetched message: %q", commands)
	}
}

func TestNormalizeRemoteSnippetDecodesMultipartPreview(t *testing.T) {
	value := strings.Join([]string{
		"----fixture-boundary",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"Synthetic remote preview with mango-orbit-2048 and caf=C3=A9.",
		"----fixture-boundary--",
	}, "\r\n")
	got := normalizeRemoteSnippet([]byte(value))
	if !strings.Contains(got, "mango-orbit-2048") || !strings.Contains(got, "café") || strings.Contains(got, "Content-Type") || strings.Contains(got, "boundary") {
		t.Fatalf("decoded remote snippet = %q", got)
	}
}

func TestNormalizeRemoteSnippetKeepsPlainTextStartingWithColonLabel(t *testing.T) {
	value := "Note: this is ordinary message text.\r\n\r\nSecond paragraph."
	got := normalizeRemoteSnippet([]byte(value))
	if got != "Note: this is ordinary message text. Second paragraph." {
		t.Fatalf("plain remote snippet = %q", got)
	}
}

func newRemoteMemoryClient(t *testing.T, memServer *imapmemserver.Server) (*Client, func()) {
	t.Helper()
	certificate := newTestCertificate(t)
	server := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memServer.NewSession(), nil, nil
		},
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{certificate.certificate}, MinVersion: tls.VersionTLS12},
		Caps:      imap.CapSet{imap.CapIMAP4rev1: {}},
	})
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLSConfig(certificate))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	client := New(Config{
		IMAPHost: "127.0.0.1", IMAPPort: port, StageTimeout: 3 * time.Second,
		TLSConfig: &tls.Config{RootCAs: certificate.pool, ServerName: "localhost", MinVersion: tls.VersionTLS12},
	})
	return client, func() { _ = server.Close() }
}

func remoteMailFixture(messageID, subject, body string) string {
	return strings.Join([]string{
		"From: Remote Sender <remote-sender@example.test>",
		"To: remote-user@example.test",
		"Cc: remote-cc@example.test",
		"Bcc: hidden-remote@example.test",
		"Date: Wed, 05 Aug 2026 20:00:00 +0000",
		"Message-ID: <" + messageID + ">",
		"Subject: " + subject,
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n")
}

func boolPointer(value bool) *bool { return &value }

func timePointerRemote(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}
