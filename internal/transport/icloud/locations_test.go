package icloud

import (
	"context"
	"crypto/tls"
	"errors"
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

func TestMutateLocationDirectMoveCopyAndDelete(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation transport.LocationMutationOperation
		dest      string
		method    string
		source    int
		destCount int
	}{
		{name: "move", operation: transport.LocationMutationMove, dest: "Archive Localized", method: "move", source: 0, destCount: 1},
		{name: "copy", operation: transport.LocationMutationCopy, dest: "Archive Localized", method: "copy", source: 1, destCount: 1},
		{name: "delete", operation: transport.LocationMutationDelete, method: "uid_expunge", source: 0, destCount: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, auth, closeServer := newLocationMemoryServer(t, imap.CapSet{
				imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapMove: {},
			})
			defer closeServer()
			result, err := client.MutateLocation(context.Background(), auth, transport.LocationMutationRequest{
				Operation: test.operation, SourceMailbox: "INBOX",
				DestinationMailbox: test.dest, ExpectedSourceUIDValidity: 1, SourceUID: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.ProviderMethod != test.method || !result.ProviderMutationObserved ||
				result.SourcePresent != (test.source == 1) || result.ReconciliationRequired {
				t.Fatalf("mutation result = %+v", result)
			}
			if test.dest != "" && (!result.DestinationKnown || result.DestinationUIDValidity == 0 || result.DestinationUID != 1) {
				t.Fatalf("destination mapping = %+v", result)
			}
			assertMailboxMessageCount(t, client, auth, "INBOX", test.source)
			if test.dest != "" {
				assertMailboxMessageCount(t, client, auth, test.dest, test.destCount)
			}
		})
	}
}

func TestMutateLocationSafeCopyUIDExpungeFallback(t *testing.T) {
	client, auth, closeServer := newLocationMemoryServer(t, imap.CapSet{
		imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {},
	})
	defer closeServer()
	result, err := client.MutateLocation(context.Background(), auth, transport.LocationMutationRequest{
		Operation: transport.LocationMutationMove, SourceMailbox: "INBOX",
		DestinationMailbox: "Archive Localized", ExpectedSourceUIDValidity: 1, SourceUID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProviderMethod != "copy_uid_expunge" || result.SourcePresent ||
		!result.DestinationKnown || result.DestinationUID != 1 || result.ReconciliationRequired {
		t.Fatalf("fallback result = %+v", result)
	}
	assertMailboxMessageCount(t, client, auth, "INBOX", 0)
	assertMailboxMessageCount(t, client, auth, "Archive Localized", 1)
}

func TestMutateLocationDeleteUIDExpungePreservesUnrelatedAlreadyDeletedNeighbor(t *testing.T) {
	client, auth, closeServer := newLocationMemoryServerWithMessages(t, imap.CapSet{
		imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {},
	}, []locationMemoryMessage{
		{messageID: "purge-target@example.test", flags: []imap.Flag{imap.FlagSeen}},
		{messageID: "unrelated-deleted@example.test", flags: []imap.Flag{imap.FlagDeleted}},
	})
	defer closeServer()
	result, err := client.MutateLocation(context.Background(), auth, transport.LocationMutationRequest{
		Operation: transport.LocationMutationDelete, SourceMailbox: "INBOX",
		ExpectedSourceUIDValidity: 1, SourceUID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProviderMethod != "uid_expunge" || !result.ProviderMutationObserved || result.SourcePresent {
		t.Fatalf("delete result=%+v", result)
	}
	// Generic EXPUNGE would remove both messages after the target is marked
	// Deleted. UID EXPUNGE for UID 1 must leave the unrelated pre-deleted UID 2.
	assertMailboxMessageCount(t, client, auth, "INBOX", 1)
}

func TestMutateLocationRefusesUnsafeFallbackAfterCopyAndPreservesSource(t *testing.T) {
	client, auth, closeServer := newLocationMemoryServer(t, imap.CapSet{imap.CapIMAP4rev1: {}})
	defer closeServer()
	result, err := client.MutateLocation(context.Background(), auth, transport.LocationMutationRequest{
		Operation: transport.LocationMutationMove, SourceMailbox: "INBOX",
		DestinationMailbox: "Archive Localized", ExpectedSourceUIDValidity: 1, SourceUID: 1,
	})
	if err == nil {
		t.Fatal("expected safe fallback refusal")
	}
	failure, ok := transport.AsFailure(err)
	if !ok || failure.Stage != transport.StageIMAPExpunge || result.ProviderMethod != "copy_only" ||
		!result.ProviderMutationObserved || !result.SourcePresent || !result.ReconciliationRequired {
		t.Fatalf("unsafe fallback result=%+v err=%v", result, err)
	}
	assertMailboxMessageCount(t, client, auth, "INBOX", 1)
	assertMailboxMessageCount(t, client, auth, "Archive Localized", 1)
}

func TestMutateLocationRejectsMissingOrChangedSourceBeforeMutation(t *testing.T) {
	client, auth, closeServer := newLocationMemoryServer(t, imap.CapSet{
		imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapMove: {},
	})
	defer closeServer()
	result, err := client.MutateLocation(context.Background(), auth, transport.LocationMutationRequest{
		Operation: transport.LocationMutationMove, SourceMailbox: "INBOX",
		DestinationMailbox: "Archive Localized", ExpectedSourceUIDValidity: 1, SourceUID: 99,
	})
	var conflict *transport.LocationStateConflictError
	if !errors.As(err, &conflict) || conflict.Present || result.ProviderMutationObserved {
		t.Fatalf("missing source result=%+v err=%v", result, err)
	}
	assertMailboxMessageCount(t, client, auth, "INBOX", 1)
	assertMailboxMessageCount(t, client, auth, "Archive Localized", 0)
}

func TestMutateLocationCopyCancellationReturnsWithoutMutation(t *testing.T) {
	server := newIMAPServer(t, imapServerOptions{AuthOK: true, StallUIDCopy: true, UIDValidity: 77})
	defer server.Close()
	client := New(Config{
		IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: 5 * time.Second,
		TLSConfig: server.ClientTLSConfig(),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	started := time.Now()
	result, err := client.MutateLocation(ctx, transport.AuthRequest{Username: "owner", Password: []byte("synthetic")}, transport.LocationMutationRequest{
		Operation: transport.LocationMutationCopy, SourceMailbox: "INBOX",
		DestinationMailbox: "Archive", ExpectedSourceUIDValidity: 77, SourceUID: 1,
	})
	if err == nil || time.Since(started) > time.Second || !result.ProviderMutationObserved || !result.ReconciliationRequired {
		t.Fatalf("canceled copy result=%+v err=%v elapsed=%s", result, err, time.Since(started))
	}
	failure, ok := transport.AsFailure(err)
	if !ok || (failure.Stage != transport.StageIMAPCopy && failure.Stage != transport.StageTimeout) {
		t.Fatalf("canceled copy error = %v", err)
	}
	commands := strings.Join(server.Commands(), ",")
	if !strings.Contains(commands, "UID") || strings.Contains(commands, "EXPUNGE") {
		t.Fatalf("canceled copy commands = %s", commands)
	}
}

func newLocationMemoryServer(t *testing.T, caps imap.CapSet) (*Client, transport.AuthRequest, func()) {
	return newLocationMemoryServerWithMessages(t, caps, []locationMemoryMessage{{
		messageID: "location-source@example.test", flags: []imap.Flag{imap.FlagSeen, imap.FlagFlagged},
	}})
}

type locationMemoryMessage struct {
	messageID string
	flags     []imap.Flag
}

func newLocationMemoryServerWithMessages(t *testing.T, caps imap.CapSet, messages []locationMemoryMessage) (*Client, transport.AuthRequest, func()) {
	t.Helper()
	const username = "location-user"
	const password = "location-password"
	memServer := imapmemserver.New()
	user := imapmemserver.NewUser(username, password)
	for _, mailbox := range []string{"INBOX", "Archive Localized", "Trash Localized"} {
		if err := user.Create(mailbox, nil); err != nil {
			t.Fatal(err)
		}
	}
	for index, message := range messages {
		raw := "From: sender@example.test\r\nTo: owner@example.test\r\nMessage-ID: <" + message.messageID + ">\r\n\r\nlocation mutation source\r\n"
		if _, err := user.Append("INBOX", &literalReader{Reader: strings.NewReader(raw), size: int64(len(raw))}, &imap.AppendOptions{
			Flags: message.flags, Time: time.Date(2026, 8, 7, 2, index, 0, 0, time.UTC),
		}); err != nil {
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
		Caps:      caps,
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
		IMAPHost: "127.0.0.1", IMAPPort: port, StageTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{RootCAs: certificate.pool, ServerName: "localhost", MinVersion: tls.VersionTLS12},
	})
	return client, transport.AuthRequest{Username: username, Password: []byte(password)}, func() {
		_ = server.Close()
		_ = listener.Close()
	}
}

func assertMailboxMessageCount(t *testing.T, client *Client, auth transport.AuthRequest, mailbox string, want int) {
	t.Helper()
	page, err := client.FetchInboxPage(context.Background(), auth, transport.InboxFetchRequest{
		Mailbox: mailbox, Bootstrap: true, Limit: 20,
	}, blob.New(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer abortFetched(page.Messages)
	if len(page.Messages) != want {
		t.Fatalf("mailbox %q messages=%d want=%d page=%+v", mailbox, len(page.Messages), want, page)
	}
}
