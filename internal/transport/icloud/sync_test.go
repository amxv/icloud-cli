package icloud

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

func TestAdvanceIncrementalWindowCommitsOnlyFetchedContiguousPage(t *testing.T) {
	selected, committed, scanned, full, err := advanceIncrementalWindow(nil, []imap.UID{21, 22, 23}, 2, 21, 256, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(selected, []imap.UID{21, 22}) || committed != 22 || scanned != 22 || !full {
		t.Fatalf("selected=%v committed=%d scanned=%d full=%v", selected, committed, scanned, full)
	}
}

func TestAdvanceIncrementalWindowTracksEmptyGapSeparately(t *testing.T) {
	selected, committed, scanned, full, err := advanceIncrementalWindow(nil, nil, 20, 1001, 1256, 900)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 0 || committed != 900 || scanned != 1256 || full {
		t.Fatalf("selected=%v committed=%d scanned=%d full=%v", selected, committed, scanned, full)
	}
}

func TestAdvanceIncrementalWindowRejectsOutOfRangeProviderUID(t *testing.T) {
	if _, _, _, _, err := advanceIncrementalWindow(nil, []imap.UID{300}, 20, 1, 256, 0); err == nil {
		t.Fatal("expected bounded-range error")
	}
}

func TestAdvanceIncrementalWindowRejectsDuplicateProviderUID(t *testing.T) {
	if _, _, _, _, err := advanceIncrementalWindow(nil, []imap.UID{21, 21}, 20, 21, 256, 20); err == nil {
		t.Fatal("expected duplicate UID error")
	}
}

func TestFetchInboxPageAgainstMemoryIMAPServer(t *testing.T) {
	const (
		username = "fixture-user"
		password = "fixture-password"
	)
	memServer := imapmemserver.New()
	user := imapmemserver.NewUser(username, password)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	fixtures := []string{
		rawSyncFixture("fixture-1@example.test", "First"),
		rawSyncFixture("fixture-2@example.test", "Second"),
		rawSyncFixture("fixture-3@example.test", "Third"),
	}
	for index, raw := range fixtures {
		_, err := user.Append("INBOX", &literalReader{Reader: strings.NewReader(raw), size: int64(len(raw))}, &imap.AppendOptions{
			Time:  time.Date(2026, 8, 6, 1, index, 0, 0, time.UTC),
			Flags: []imap.Flag{imap.FlagFlagged},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	memServer.AddUser(user)

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
	defer server.Close()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	client := New(Config{
		IMAPHost:     "127.0.0.1",
		IMAPPort:     port,
		StageTimeout: 2 * time.Second,
		TLSConfig:    &tls.Config{RootCAs: certificate.pool, ServerName: "localhost", MinVersion: tls.VersionTLS12},
	})
	page, err := client.FetchInboxPage(context.Background(), transport.AuthRequest{
		Username: username,
		Password: []byte(password),
	}, transport.InboxFetchRequest{Mailbox: "INBOX", Bootstrap: true, Limit: 2}, blob.New(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer abortFetched(page.Messages)
	if page.UIDValidity != 1 || !page.Bootstrap || page.MoreAvailable || page.LastCommittedUID != 3 || page.LastScannedUID != 3 {
		t.Fatalf("page = %+v", page)
	}
	if len(page.Messages) != 2 || page.Messages[0].UID != 2 || page.Messages[1].UID != 3 {
		t.Fatalf("message UIDs = %+v", page.Messages)
	}
	for index, message := range page.Messages {
		if !slices.Contains(message.Flags, `\Flagged`) || message.Raw == nil || message.RFC822Size == 0 {
			t.Fatalf("message %d = %+v", index, message)
		}
		reader, err := message.Raw.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), fixtures[index+1]) {
			t.Fatalf("raw message %d did not match fixture", index)
		}
	}

	tailOnly, err := client.FetchInboxPage(context.Background(), transport.AuthRequest{
		Username: username,
		Password: []byte(password),
	}, transport.InboxFetchRequest{
		Mailbox: "INBOX", Bootstrap: true, TailOnlyBootstrap: true, Limit: 2,
	}, blob.New(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if !tailOnly.Bootstrap || len(tailOnly.Messages) != 0 || tailOnly.MoreAvailable ||
		tailOnly.LastCommittedUID != 3 || tailOnly.LastScannedUID != 3 {
		t.Fatalf("tail-only bootstrap = %+v", tailOnly)
	}
}

func TestReconcileAndUpdateFlagsAgainstMemoryIMAPServer(t *testing.T) {
	const (
		username = "state-user"
		password = "state-password"
	)
	memServer := imapmemserver.New()
	user := imapmemserver.NewUser(username, password)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	raw := rawSyncFixture("state-1@example.test", "State fixture")
	if _, err := user.Append("INBOX", &literalReader{Reader: strings.NewReader(raw), size: int64(len(raw))}, &imap.AppendOptions{
		Time: time.Date(2026, 8, 6, 1, 30, 0, 0, time.UTC),
		Flags: []imap.Flag{
			imap.FlagSeen, imap.FlagFlagged, imap.FlagAnswered, imap.FlagDraft,
			imap.Flag("$Forwarded"),
		},
	}); err != nil {
		t.Fatal(err)
	}
	memServer.AddUser(user)
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
	defer server.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	client := New(Config{
		IMAPHost: "127.0.0.1", IMAPPort: port, StageTimeout: 2 * time.Second,
		TLSConfig: &tls.Config{RootCAs: certificate.pool, ServerName: "localhost", MinVersion: tls.VersionTLS12},
	})
	auth := transport.AuthRequest{Username: username, Password: []byte(password)}
	page, err := client.FetchInboxPage(context.Background(), auth, transport.InboxFetchRequest{
		Mailbox: "INBOX", Bootstrap: true, Limit: 20,
	}, blob.New(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer abortFetched(page.Messages)
	if len(page.Messages) != 1 || !slices.Contains(page.Messages[0].Flags, `\Seen`) ||
		!slices.Contains(page.Messages[0].Flags, `\Flagged`) || !slices.Contains(page.Messages[0].Flags, `$Forwarded`) {
		t.Fatalf("initial flags = %+v", page.Messages)
	}
	result, err := client.UpdateFlags(context.Background(), auth, transport.FlagMutationRequest{
		Mailbox: "INBOX", ExpectedUIDValidity: page.UIDValidity, UID: 1,
		Flag: `\Seen`, Add: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Present || slices.Contains(result.Flags, `\Seen`) || !slices.Contains(result.Flags, `\Flagged`) || !slices.Contains(result.Flags, `$Forwarded`) {
		t.Fatalf("STORE result = %+v", result)
	}
	reconciled, err := client.FetchInboxPage(context.Background(), auth, transport.InboxFetchRequest{
		Mailbox: "INBOX", ExpectedUIDValidity: page.UIDValidity,
		LastCommittedUID: 1, LastScannedUID: 1, Limit: 20,
		ReconcileUIDs: []uint32{1},
	}, blob.New(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer abortFetched(reconciled.Messages)
	if len(reconciled.Messages) != 0 || len(reconciled.LocationStates) != 1 ||
		!reconciled.LocationStates[0].Present || slices.Contains(reconciled.LocationStates[0].Flags, `\Seen`) {
		t.Fatalf("reconciled page = %+v", reconciled)
	}
}

func TestUpdateFlagsTimesOutAtStoreAndDoesNotSearch(t *testing.T) {
	server := newIMAPServer(t, imapServerOptions{AuthOK: true, UIDValidity: 77, StallUIDStore: true})
	defer server.Close()
	client := New(Config{
		IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: 40 * time.Millisecond,
		TLSConfig: server.ClientTLSConfig(),
	})
	_, err := client.UpdateFlags(context.Background(), transport.AuthRequest{
		Username: "owner", Password: []byte("synthetic"),
	}, transport.FlagMutationRequest{
		Mailbox: "INBOX", ExpectedUIDValidity: 77, UID: 1,
		Flag: `\Seen`, Add: true,
	})
	assertFailure(t, err, transport.StageTimeout, transport.FailureUnavailable)
	commands := strings.Join(server.Commands(), ",")
	if !strings.Contains(commands, "UID") || strings.Contains(commands, "SEARCH") || strings.Contains(commands, "APPEND") || strings.Contains(commands, "COPY") {
		t.Fatalf("mutation commands = %q", commands)
	}
}

func TestUpdateFlagsReturnsCurrentStateAfterConditionalStoreConflict(t *testing.T) {
	server := newIMAPServer(t, imapServerOptions{
		AuthOK: true, UIDValidity: 77, CondStore: true, ConditionalStoreConflict: true,
	})
	defer server.Close()
	client := New(Config{
		IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: time.Second,
		TLSConfig: server.ClientTLSConfig(),
	})
	result, err := client.UpdateFlags(context.Background(), transport.AuthRequest{
		Username: "owner", Password: []byte("synthetic"),
	}, transport.FlagMutationRequest{
		Mailbox: "INBOX", ExpectedUIDValidity: 77, UID: 1,
		ExpectedModSequence: 10, Flag: `\Seen`, Add: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Present || result.ModSequence != 12 || slices.Contains(result.Flags, `\Seen`) ||
		!slices.Contains(result.Flags, `\Flagged`) || !slices.Contains(result.Flags, `$Remote`) {
		t.Fatalf("conditional conflict state = %+v", result)
	}
	commands := strings.Join(server.Commands(), ",")
	if strings.Contains(commands, "SEARCH") || !strings.Contains(commands, "UID") {
		t.Fatalf("conditional conflict commands = %q", commands)
	}
}

type literalReader struct {
	io.Reader
	size int64
}

func (reader *literalReader) Size() int64 { return reader.size }

func serverTLSConfig(certificate testCertificate) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{certificate.certificate}, MinVersion: tls.VersionTLS12}
}

func rawSyncFixture(messageID, subject string) string {
	return strings.Join([]string{
		"From: Fixture Sender <sender@example.test>",
		"To: fixture-user@example.test",
		"Date: Wed, 05 Aug 2026 20:00:00 +0000",
		"Message-ID: <" + messageID + ">",
		"Subject: " + subject,
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Synthetic memory-server body for " + messageID,
	}, "\r\n")
}
