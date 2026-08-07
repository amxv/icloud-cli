package icloud

import (
	"context"
	"crypto/tls"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

func TestFetchBackfillPageResumesNewestToOldest(t *testing.T) {
	client, auth, cleanup := newBackfillMemoryClient(t, []time.Time{
		time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC),
	})
	defer cleanup()
	first, err := client.FetchBackfillPage(context.Background(), auth, transport.BackfillFetchRequest{Mailbox: "INBOX", Limit: 2}, blob.New(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer abortFetched(first.Messages)
	if got := fetchedUIDs(first.Messages); !reflect.DeepEqual(got, []uint32{5, 4}) || first.BeforeUID != 6 || first.NextBeforeUID != 4 || first.Complete {
		t.Fatalf("first page uids=%v page=%+v", got, first)
	}
	second, err := client.FetchBackfillPage(context.Background(), auth, transport.BackfillFetchRequest{Mailbox: "INBOX", ExpectedUIDValidity: first.UIDValidity, BeforeUID: first.NextBeforeUID, Limit: 2}, blob.New(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer abortFetched(second.Messages)
	if got := fetchedUIDs(second.Messages); !reflect.DeepEqual(got, []uint32{3, 2}) || second.BeforeUID != 4 || second.NextBeforeUID != 2 || second.Complete {
		t.Fatalf("second page uids=%v page=%+v", got, second)
	}
	third, err := client.FetchBackfillPage(context.Background(), auth, transport.BackfillFetchRequest{Mailbox: "INBOX", ExpectedUIDValidity: first.UIDValidity, BeforeUID: second.NextBeforeUID, Limit: 2}, blob.New(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer abortFetched(third.Messages)
	if got := fetchedUIDs(third.Messages); !reflect.DeepEqual(got, []uint32{1}) || third.BeforeUID != 2 || third.NextBeforeUID != 1 || !third.Complete {
		t.Fatalf("third page uids=%v page=%+v", got, third)
	}
}

func TestFetchBackfillPageAppliesDateBoundsInsideBoundedUIDSearch(t *testing.T) {
	client, auth, cleanup := newBackfillMemoryClient(t, []time.Time{
		time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC),
	})
	defer cleanup()
	after := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	page, err := client.FetchBackfillPage(context.Background(), auth, transport.BackfillFetchRequest{Mailbox: "INBOX", After: &after, Before: &before, Limit: 10}, blob.New(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer abortFetched(page.Messages)
	if got := fetchedUIDs(page.Messages); !reflect.DeepEqual(got, []uint32{4, 3}) || !page.Complete {
		t.Fatalf("date-bounded page uids=%v page=%+v", got, page)
	}
}

func TestMailboxStatusReadsExactCurrentTail(t *testing.T) {
	client, auth, cleanup := newBackfillMemoryClient(t, []time.Time{
		time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
	})
	defer cleanup()
	status, err := client.MailboxStatus(context.Background(), auth, "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if status.Mailbox != "INBOX" || status.Messages != 2 || status.UIDNext != 3 || status.UIDValidity == 0 {
		t.Fatalf("status = %+v", status)
	}
}

func newBackfillMemoryClient(t *testing.T, dates []time.Time) (*Client, transport.AuthRequest, func()) {
	t.Helper()
	const username, password = "backfill-user", "backfill-password"
	memServer := imapmemserver.New()
	user := imapmemserver.NewUser(username, password)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	for index, date := range dates {
		raw := rawSyncFixture(string(rune('a'+index))+"-backfill@example.test", "Backfill fixture")
		if _, err := user.Append("INBOX", &literalReader{Reader: strings.NewReader(raw), size: int64(len(raw))}, &imap.AppendOptions{Time: date}); err != nil {
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
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	client := New(Config{IMAPHost: "127.0.0.1", IMAPPort: port, StageTimeout: 10 * time.Second, TLSConfig: &tls.Config{RootCAs: certificate.pool, ServerName: "localhost", MinVersion: tls.VersionTLS12}})
	return client, transport.AuthRequest{Username: username, Password: []byte(password)}, func() { _ = server.Close() }
}

func fetchedUIDs(messages []transport.FetchedMessage) []uint32 {
	values := make([]uint32, 0, len(messages))
	for _, message := range messages {
		values = append(values, message.UID)
	}
	return values
}
