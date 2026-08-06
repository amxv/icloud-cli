package icloud

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

func TestAppendSentStreamsExactSeenMessageAndReturnsUIDEvidence(t *testing.T) {
	const (
		username = "sent-user"
		password = "sent-password"
	)
	memServer := imapmemserver.New()
	user := imapmemserver.NewUser(username, password)
	if err := user.Create("Sent Messages", nil); err != nil {
		t.Fatal(err)
	}
	memServer.AddUser(user)
	certificate := newTestCertificate(t)
	server := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memServer.NewSession(), nil, nil
		},
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{certificate.certificate}, MinVersion: tls.VersionTLS12},
		Caps:      imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}},
	})
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLSConfig(certificate))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	client := New(Config{
		// This test streams a multi-megabyte literal through an in-process TLS
		// server. Keep the deadline generous enough for loaded and CGO-disabled
		// builders; timeout classification is covered by dedicated tests.
		IMAPHost: "127.0.0.1", IMAPPort: port, StageTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{RootCAs: certificate.pool, ServerName: "localhost", MinVersion: tls.VersionTLS12},
	})
	raw := bytes.Repeat([]byte("From: sender@example.test\r\nTo: recipient@example.test\r\nMessage-ID: <sent-append@example.test>\r\n\r\nstreamed-sent-token\r\n"), 12000)
	source := &boundedSentSource{reader: bytes.NewReader(raw), maximumRead: 64 << 10}
	when := time.Date(2026, 8, 6, 18, 0, 0, 0, time.UTC)
	result, err := client.AppendSent(context.Background(), transport.AuthRequest{
		Username: username, Password: []byte(password),
	}, transport.SentAppendRequest{
		Mailbox: "Sent Messages", Message: source, Size: int64(len(raw)), InternalDate: when,
	})
	if err != nil {
		t.Fatal(err)
	}
	if source.largestRead > source.maximumRead {
		t.Fatalf("append source read was not bounded: largest=%d", source.largestRead)
	}
	if result.Mailbox != "Sent Messages" || result.UIDValidity != 1 || result.UID != 1 {
		t.Fatalf("append result = %+v", result)
	}
	page, err := client.FetchInboxPage(context.Background(), transport.AuthRequest{
		Username: username, Password: []byte(password),
	}, transport.InboxFetchRequest{Mailbox: "Sent Messages", Bootstrap: true, Limit: 20}, blob.New(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer abortFetched(page.Messages)
	if len(page.Messages) != 1 || page.Messages[0].UID != 1 || !slices.Contains(page.Messages[0].Flags, `\Seen`) {
		t.Fatalf("fetched Sent page = %+v", page)
	}
	reader, err := page.Messages[0].Raw.Open()
	if err != nil {
		t.Fatal(err)
	}
	stored, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr == nil {
		readErr = closeErr
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(stored, raw) {
		t.Fatalf("Sent bytes changed: got=%d want=%d", len(stored), len(raw))
	}
}

func TestAppendSentRejectsDeclaredSizeMismatch(t *testing.T) {
	server := newIMAPServer(t, imapServerOptions{AuthOK: true})
	defer server.Close()
	client := New(Config{
		IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: 250 * time.Millisecond,
		TLSConfig: server.ClientTLSConfig(),
	})
	_, err := client.AppendSent(context.Background(), transport.AuthRequest{
		Username: "owner", Password: []byte("synthetic"),
	}, transport.SentAppendRequest{Mailbox: "Sent Messages", Message: bytes.NewReader([]byte("short")), Size: 20})
	if err == nil {
		t.Fatal("expected declared-size mismatch")
	}
	failure, ok := transport.AsFailure(err)
	if !ok || failure.Stage != transport.StageIMAPAppend {
		t.Fatalf("append mismatch error = %#v", err)
	}
}

type boundedSentSource struct {
	reader      io.Reader
	maximumRead int
	largestRead int
}

func (reader *boundedSentSource) Read(buffer []byte) (int, error) {
	if len(buffer) > reader.largestRead {
		reader.largestRead = len(buffer)
	}
	if len(buffer) > reader.maximumRead {
		return 0, errors.New("Sent source received an unbounded read buffer")
	}
	return reader.reader.Read(buffer)
}
