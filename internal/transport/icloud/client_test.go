package icloud

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/textproto"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-imap/v2"
)

func TestIMAPValidationUsesTLSAndReadOnlyCompatibilityCommands(t *testing.T) {
	server := newIMAPServer(t, imapServerOptions{AuthOK: true})
	defer server.Close()

	client := New(Config{
		IMAPHost:     "127.0.0.1",
		IMAPPort:     server.Port(),
		StageTimeout: time.Second,
		TLSConfig:    server.ClientTLSConfig(),
	})
	result, err := client.ValidateIMAP(context.Background(), transport.AuthRequest{
		Username: "owner@icloud.com",
		Password: []byte("synthetic-secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mailboxes != 1 || result.Inbox.Messages != 12 || result.Inbox.Unseen != 3 || result.Inbox.UIDNext != 14 || result.Inbox.UIDValidity != 77 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !contains(result.Capabilities, "IMAP4REV1") || !contains(result.Capabilities, "SPECIAL-USE") {
		t.Fatalf("capabilities = %v", result.Capabilities)
	}
	if usernames := server.Usernames(); len(usernames) != 1 || usernames[0] != "owner@icloud.com" {
		t.Fatalf("usernames = %v", usernames)
	}
	for _, command := range server.Commands() {
		for _, forbidden := range []string{"STORE", "COPY", "MOVE", "APPEND", "EXPUNGE", "DELETE", "RENAME", "CREATE"} {
			if command == forbidden {
				t.Fatalf("validation issued mutating command %q", command)
			}
		}
	}
}

func TestIMAPValidationClassifiesAuthenticationTLSDNSAndCancellation(t *testing.T) {
	t.Run("authentication", func(t *testing.T) {
		server := newIMAPServer(t, imapServerOptions{AuthOK: false})
		defer server.Close()
		client := New(Config{IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: time.Second, TLSConfig: server.ClientTLSConfig()})
		_, err := client.ValidateIMAP(context.Background(), transport.AuthRequest{Username: "bad", Password: []byte("bad")})
		assertFailure(t, err, transport.StageIMAPAuth, transport.FailureAuthentication)
	})

	t.Run("tls", func(t *testing.T) {
		server := newIMAPServer(t, imapServerOptions{AuthOK: true})
		defer server.Close()
		client := New(Config{IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: time.Second})
		_, err := client.ValidateIMAP(context.Background(), transport.AuthRequest{Username: "user", Password: []byte("secret")})
		assertFailure(t, err, transport.StageTLS, transport.FailureUnavailable)
	})

	t.Run("dns", func(t *testing.T) {
		client := New(Config{DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, &net.DNSError{Err: "not found", Name: "redacted.invalid", IsNotFound: true}
		}})
		_, err := client.ValidateIMAP(context.Background(), transport.AuthRequest{Username: "user", Password: []byte("secret")})
		assertFailure(t, err, transport.StageDNS, transport.FailureUnavailable)
	})

	t.Run("cancellation", func(t *testing.T) {
		server := newIMAPServer(t, imapServerOptions{AuthOK: true, StallGreeting: true})
		defer server.Close()
		client := New(Config{IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: 5 * time.Second, TLSConfig: server.ClientTLSConfig()})
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		started := time.Now()
		_, err := client.ValidateIMAP(ctx, transport.AuthRequest{Username: "user", Password: []byte("secret")})
		assertFailure(t, err, transport.StageTimeout, transport.FailureUnavailable)
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("cancellation took %s", elapsed)
		}
	})
}

func TestFolderDiscoveryPreservesProviderMetadataAndStatus(t *testing.T) {
	unicodeWire := encodeMailboxWireName("Unicode-日本語")
	server := newIMAPServer(t, imapServerOptions{
		AuthOK: true,
		ListLines: []string{
			`* LIST (\Noinferiors) "/" "INBOX"`,
			`* LIST (\HasNoChildren) "/" "Sent"`,
			`* LIST (\HasNoChildren \Sent) "/" "Sent Messages"`,
			`* LIST (\Noselect \HasChildren) "/" "Projects"`,
			`* LIST (\HasNoChildren \Subscribed) "/" "Projects/Alpha"`,
			`* LIST (\HasNoChildren \Drafts) "/" "Brouillons"`,
			fmt.Sprintf(`* LIST (\HasNoChildren) "/" %s`, strconv.Quote(unicodeWire)),
			`* LIST (\HasNoChildren) "/" "Renamed" (OLDNAME ("Old Name"))`,
		},
		Statuses: map[string]transport.MailboxStatus{
			"INBOX":          {Messages: 12, Unseen: 3, UIDNext: 14, UIDValidity: 77},
			"Sent":           {Messages: 1, UIDNext: 2, UIDValidity: 78},
			"Sent Messages":  {Messages: 2, UIDNext: 3, UIDValidity: 79},
			"Projects/Alpha": {Messages: 4, Unseen: 1, UIDNext: 8, UIDValidity: 80},
			"Brouillons":     {Messages: 1, UIDNext: 2, UIDValidity: 81},
			"Unicode-日本語":    {Messages: 5, Unseen: 2, UIDNext: 9, UIDValidity: 82},
			"Renamed":        {Messages: 6, UIDNext: 10, UIDValidity: 83},
		},
	})
	defer server.Close()
	client := New(Config{IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: time.Second, TLSConfig: server.ClientTLSConfig()})
	result, err := client.DiscoverFolders(context.Background(), transport.AuthRequest{Username: "owner", Password: []byte("synthetic-secret")})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Folders) != 8 {
		t.Fatalf("folder count = %d", len(result.Folders))
	}
	byName := make(map[string]transport.RemoteFolder, len(result.Folders))
	for _, value := range result.Folders {
		byName[value.Name] = value
	}
	if value := byName["INBOX"]; value.Kind != "inbox" || value.KindSource != "inbox" || !value.Selectable || value.Status == nil || value.Status.Unseen != 3 {
		t.Fatalf("inbox = %+v", value)
	}
	if value := byName["Sent Messages"]; value.Kind != "sent" || value.KindSource != "attribute" {
		t.Fatalf("sent special-use = %+v", value)
	}
	if value := byName["Sent"]; value.Kind != "other" {
		t.Fatalf("fallback overrode special-use: %+v", value)
	}
	if value := byName["Projects"]; value.Selectable || value.Status != nil {
		t.Fatalf("non-selectable container = %+v", value)
	}
	if value := byName["Projects/Alpha"]; value.ParentName != "Projects" || value.Subscription != "subscribed" || value.Status == nil || value.Status.Messages != 4 {
		t.Fatalf("nested folder = %+v", value)
	}
	if value := byName["Brouillons"]; value.Kind != "drafts" || value.KindSource != "attribute" {
		t.Fatalf("localized special-use = %+v", value)
	}
	if value := byName["Unicode-日本語"]; value.WireName != unicodeWire || value.Status == nil || value.Status.UIDValidity != 82 {
		t.Fatalf("unicode folder = %+v", value)
	}
	if value := byName["Renamed"]; value.OldName != "Old Name" || value.OldWireName != "Old Name" {
		t.Fatalf("old-name metadata = %+v", value)
	}
	for _, command := range server.Commands() {
		for _, forbidden := range []string{"STORE", "COPY", "MOVE", "APPEND", "EXPUNGE", "DELETE", "RENAME", "CREATE"} {
			if command == forbidden {
				t.Fatalf("discovery issued mutating command %q", command)
			}
		}
	}
}

func TestFolderDiscoveryUsesSentFallbackOnlyWithoutSpecialUse(t *testing.T) {
	folders := []transport.RemoteFolder{{Name: "Sent Mail", Kind: "other"}, {Name: "Other", Kind: "other"}}
	applySentFallback(folders)
	if folders[0].Kind != "sent" || folders[0].KindSource != "fallback" {
		t.Fatalf("fallback folders = %+v", folders)
	}
	folders = []transport.RemoteFolder{{Name: "Localized", Kind: "sent", KindSource: "attribute"}, {Name: "Sent", Kind: "other"}}
	applySentFallback(folders)
	if folders[1].Kind != "other" {
		t.Fatalf("fallback applied despite special-use: %+v", folders)
	}
}

func TestFolderDiscoveryClassifiesStatusFailureAndRejectsUnsafeInventory(t *testing.T) {
	t.Run("status failure", func(t *testing.T) {
		server := newIMAPServer(t, imapServerOptions{
			AuthOK:    true,
			ListLines: []string{`* LIST (\HasNoChildren) "/" "INBOX"`, `* LIST (\HasNoChildren) "/" "Receipts"`},
			Statuses: map[string]transport.MailboxStatus{
				"INBOX": {Messages: 1, UIDNext: 2, UIDValidity: 7},
			},
		})
		defer server.Close()
		client := New(Config{IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: time.Second, TLSConfig: server.ClientTLSConfig()})
		_, err := client.DiscoverFolders(context.Background(), transport.AuthRequest{Username: "owner", Password: []byte("secret")})
		assertFailure(t, err, transport.StageIMAPStatus, transport.FailureUnavailable)
	})

	t.Run("folder count bound", func(t *testing.T) {
		lines := make([]string, 0, maxDiscoveredFolders+1)
		for index := 0; index <= maxDiscoveredFolders; index++ {
			lines = append(lines, fmt.Sprintf(`* LIST (\Noselect) "/" "Container-%04d"`, index))
		}
		server := newIMAPServer(t, imapServerOptions{AuthOK: true, ListLines: lines})
		defer server.Close()
		client := New(Config{IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: time.Second, TLSConfig: server.ClientTLSConfig()})
		_, err := client.DiscoverFolders(context.Background(), transport.AuthRequest{Username: "owner", Password: []byte("secret")})
		assertFailure(t, err, transport.StageIMAPList, transport.FailureUnavailable)
	})

	t.Run("invalid decoded name", func(t *testing.T) {
		if _, err := remoteFolderFromList(&imap.ListData{Mailbox: string([]byte{0xff}), Delim: '/'}); err == nil {
			t.Fatal("expected invalid UTF-8 mailbox to fail")
		}
	})
}

func TestEncodeMailboxWireNameRFC3501(t *testing.T) {
	tests := map[string]string{
		"":                   "",
		"a&b":                "a&-b",
		"日本語":                "&ZeVnLIqe-",
		"~peter/mail/台北/日本語": "~peter/mail/&U,BTFw-/&ZeVnLIqe-",
		"Unicode-日本語":        "Unicode-&ZeVnLIqe-",
		"Hi Mom -☺-!":        "Hi Mom -&Jjo--!",
	}
	keys := make([]string, 0, len(tests))
	for key := range tests {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, input := range keys {
		if got := encodeMailboxWireName(input); got != tests[input] {
			t.Fatalf("encode %q = %q want %q", input, got, tests[input])
		}
	}
}

func TestSMTPValidationRequiresSTARTTLSAuthenticatesAndNeverSends(t *testing.T) {
	server := newSMTPServer(t, smtpServerOptions{AdvertiseSTARTTLS: true, AuthOK: true})
	defer server.Close()
	client := New(Config{
		SMTPHost:     "127.0.0.1",
		SMTPPort:     server.Port(),
		StageTimeout: time.Second,
		TLSConfig:    server.ClientTLSConfig(),
	})
	result, err := client.ValidateSMTP(context.Background(), transport.AuthRequest{
		Username: "owner@icloud.com",
		Password: []byte("synthetic-secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(result.Capabilities, "AUTH") || !contains(result.Capabilities, "SMTPUTF8") {
		t.Fatalf("capabilities = %v", result.Capabilities)
	}
	if !server.SawSTARTTLS() || !server.SawAuth() {
		t.Fatalf("starttls=%v auth=%v", server.SawSTARTTLS(), server.SawAuth())
	}
	if server.SawMutation() {
		t.Fatal("account validation issued MAIL/RCPT/DATA")
	}
}

func TestSMTPValidationClassifiesSTARTTLSAndAuthenticationFailures(t *testing.T) {
	t.Run("starttls required", func(t *testing.T) {
		server := newSMTPServer(t, smtpServerOptions{AdvertiseSTARTTLS: false})
		defer server.Close()
		client := New(Config{SMTPHost: "127.0.0.1", SMTPPort: server.Port(), StageTimeout: time.Second, TLSConfig: server.ClientTLSConfig()})
		_, err := client.ValidateSMTP(context.Background(), transport.AuthRequest{Username: "user", Password: []byte("secret")})
		assertFailure(t, err, transport.StageSMTPStartTLS, transport.FailureUnavailable)
		if server.SawAuth() || server.SawMutation() {
			t.Fatal("plaintext SMTP server received auth or send commands")
		}
	})

	t.Run("authentication", func(t *testing.T) {
		server := newSMTPServer(t, smtpServerOptions{AdvertiseSTARTTLS: true, AuthOK: false})
		defer server.Close()
		client := New(Config{SMTPHost: "127.0.0.1", SMTPPort: server.Port(), StageTimeout: time.Second, TLSConfig: server.ClientTLSConfig()})
		_, err := client.ValidateSMTP(context.Background(), transport.AuthRequest{Username: "user", Password: []byte("secret")})
		assertFailure(t, err, transport.StageSMTPAuth, transport.FailureAuthentication)
		if server.SawMutation() {
			t.Fatal("failed authentication proceeded to a send command")
		}
	})
}

func TestLiveICloudCompatibility(t *testing.T) {
	if strings.TrimSpace(os.Getenv("ICLOUD_LIVE_TEST")) != "1" {
		t.Skip("set ICLOUD_LIVE_TEST=1 with disposable-account variables")
	}
	email := strings.TrimSpace(os.Getenv("ICLOUD_LIVE_TEST_EMAIL"))
	password := os.Getenv("ICLOUD_LIVE_TEST_PASSWORD")
	if email == "" || password == "" {
		t.Skip("live disposable-account variables are absent")
	}
	username := strings.TrimSpace(os.Getenv("ICLOUD_LIVE_TEST_IMAP_USERNAME"))
	if username == "" {
		username = email
	}
	client := New(Config{StageTimeout: 20 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := client.ValidateIMAP(ctx, transport.AuthRequest{Username: username, Password: []byte(password)}); err != nil {
		t.Fatalf("live IMAP compatibility failed: %v", err)
	}
	if _, err := client.ValidateSMTP(ctx, transport.AuthRequest{Username: email, Password: []byte(password)}); err != nil {
		t.Fatalf("live SMTP compatibility failed: %v", err)
	}
}

func assertFailure(t *testing.T, err error, stage transport.Stage, kind transport.FailureKind) {
	t.Helper()
	failure, ok := transport.AsFailure(err)
	if !ok {
		t.Fatalf("error is not a transport failure: %T %v", err, err)
	}
	if failure.Stage != stage || failure.Kind != kind {
		t.Fatalf("failure = %+v, want stage=%s kind=%s", failure, stage, kind)
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type testCertificate struct {
	certificate tls.Certificate
	pool        *x509.CertPool
}

func newTestCertificate(t *testing.T) testCertificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(&template)
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool = x509.NewCertPool()
	pool.AddCert(parsed)
	return testCertificate{certificate: certificate, pool: pool}
}

type imapServerOptions struct {
	AuthOK                   bool
	StallGreeting            bool
	StallUIDStore            bool
	StallUIDSearch           bool
	CondStore                bool
	ConditionalStoreConflict bool
	UIDValidity              uint32
	ListLines                []string
	Statuses                 map[string]transport.MailboxStatus
}

type imapTestServer struct {
	t           *testing.T
	listener    net.Listener
	certificate testCertificate
	mu          sync.Mutex
	usernames   []string
	commands    []string
}

func newIMAPServer(t *testing.T, options imapServerOptions) *imapTestServer {
	t.Helper()
	certificate := newTestCertificate(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate.certificate}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	server := &imapTestServer{t: t, listener: listener, certificate: certificate}
	go server.serve(options)
	return server
}

func (server *imapTestServer) serve(options imapServerOptions) {
	for {
		conn, err := server.listener.Accept()
		if err != nil {
			return
		}
		go server.handle(conn, options)
	}
}

func (server *imapTestServer) handle(conn net.Conn, options imapServerOptions) {
	defer conn.Close()
	if options.StallGreeting {
		<-time.After(2 * time.Second)
		return
	}
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	capabilities := "IMAP4rev1 SPECIAL-USE IDLE UIDPLUS"
	if options.CondStore {
		capabilities += " CONDSTORE"
	}
	_, _ = writer.WriteString("* OK [CAPABILITY " + capabilities + "] ready\r\n")
	_ = writer.Flush()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		tag := fields[0]
		command := strings.ToUpper(fields[1])
		server.mu.Lock()
		server.commands = append(server.commands, command)
		server.mu.Unlock()
		switch command {
		case "CAPABILITY":
			_, _ = writer.WriteString("* CAPABILITY " + capabilities + "\r\n" + tag + " OK capability\r\n")
		case "LOGIN":
			username := ""
			if len(fields) > 2 {
				username = strings.Trim(fields[2], "\"")
			}
			server.mu.Lock()
			server.usernames = append(server.usernames, username)
			server.mu.Unlock()
			if options.AuthOK {
				_, _ = writer.WriteString(tag + " OK logged in\r\n")
			} else {
				_, _ = writer.WriteString(tag + " NO [AUTHENTICATIONFAILED] rejected\r\n")
			}
		case "LIST":
			lines := options.ListLines
			if len(lines) == 0 {
				lines = []string{`* LIST (\HasNoChildren) "/" "INBOX"`}
			}
			for _, responseLine := range lines {
				_, _ = writer.WriteString(responseLine + "\r\n")
			}
			_, _ = writer.WriteString(tag + " OK list\r\n")
		case "STATUS":
			mailbox := parseIMAPMailboxArgument(line, "STATUS")
			status, ok := options.Statuses[mailbox]
			if len(options.Statuses) == 0 && mailbox == "INBOX" {
				status = transport.MailboxStatus{Messages: 12, Unseen: 3, UIDNext: 14, UIDValidity: 77}
				ok = true
			}
			if !ok {
				_, _ = writer.WriteString(tag + " NO status unavailable\r\n")
				break
			}
			wireName := encodeMailboxWireName(mailbox)
			_, _ = writer.WriteString(fmt.Sprintf("* STATUS %s (MESSAGES %d UNSEEN %d UIDNEXT %d UIDVALIDITY %d)\r\n%s OK status\r\n",
				strconv.Quote(wireName), status.Messages, status.Unseen, status.UIDNext, status.UIDValidity, tag))
		case "SELECT", "EXAMINE":
			uidValidity := options.UIDValidity
			if uidValidity == 0 {
				uidValidity = 77
			}
			response := fmt.Sprintf("* FLAGS (\\Seen \\Flagged)\r\n* OK [PERMANENTFLAGS (\\Seen \\Flagged \\*)] flags\r\n* 1 EXISTS\r\n* OK [UIDVALIDITY %d] validity\r\n* OK [UIDNEXT 2] next\r\n", uidValidity)
			if options.CondStore {
				response += "* OK [HIGHESTMODSEQ 12] modseq\r\n"
			}
			response += tag + " OK [READ-WRITE] selected\r\n"
			_, _ = writer.WriteString(response)
		case "UID":
			if len(fields) > 2 && strings.EqualFold(fields[2], "SEARCH") && options.StallUIDSearch {
				continue
			}
			if len(fields) > 2 && strings.EqualFold(fields[2], "STORE") {
				if options.StallUIDStore {
					continue
				}
				if options.ConditionalStoreConflict {
					_, _ = writer.WriteString(tag + " NO [MODIFIED 1] conditional store lost a race\r\n")
					break
				}
			}
			if len(fields) > 2 && strings.EqualFold(fields[2], "FETCH") && options.ConditionalStoreConflict {
				_, _ = writer.WriteString("* 1 FETCH (UID 1 FLAGS (\\Flagged $Remote) MODSEQ (12))\r\n" + tag + " OK fetch\r\n")
				break
			}
			_, _ = writer.WriteString(tag + " BAD unsupported UID command\r\n")
		case "LOGOUT":
			_, _ = writer.WriteString("* BYE logging out\r\n" + tag + " OK logout\r\n")
			_ = writer.Flush()
			return
		default:
			_, _ = writer.WriteString(tag + " BAD unsupported\r\n")
		}
		_ = writer.Flush()
	}
}

func (server *imapTestServer) Port() string {
	_, port, _ := net.SplitHostPort(server.listener.Addr().String())
	return port
}

func (server *imapTestServer) ClientTLSConfig() *tls.Config {
	return &tls.Config{RootCAs: server.certificate.pool, ServerName: "localhost", MinVersion: tls.VersionTLS12}
}

func (server *imapTestServer) Usernames() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]string(nil), server.usernames...)
}

func (server *imapTestServer) Commands() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]string(nil), server.commands...)
}

func (server *imapTestServer) Close() { _ = server.listener.Close() }

func parseIMAPMailboxArgument(line, command string) string {
	rest := strings.TrimSpace(line)
	fields := strings.Fields(rest)
	if len(fields) < 3 || !strings.EqualFold(fields[1], command) {
		return ""
	}
	rest = strings.TrimSpace(strings.TrimPrefix(rest, fields[0]))
	rest = strings.TrimSpace(strings.TrimPrefix(rest, fields[1]))
	if !strings.HasPrefix(rest, `"`) {
		return strings.Fields(rest)[0]
	}
	var escaped bool
	for index := 1; index < len(rest); index++ {
		switch {
		case escaped:
			escaped = false
		case rest[index] == '\\':
			escaped = true
		case rest[index] == '"':
			quoted := rest[:index+1]
			value, err := strconv.Unquote(quoted)
			if err != nil {
				return ""
			}
			// The client writes modified UTF-7. Tests use the canonical encoder,
			// so map back to a decoded fixture key when one is known.
			for _, candidate := range []string{"Unicode-日本語"} {
				if value == encodeMailboxWireName(candidate) {
					return candidate
				}
			}
			return value
		}
	}
	return ""
}

type smtpServerOptions struct {
	AdvertiseSTARTTLS   bool
	AuthOK              bool
	AcceptSend          bool
	RejectAfterData     bool
	DisconnectAfterData bool
}

type smtpTestServer struct {
	t           *testing.T
	listener    net.Listener
	certificate testCertificate
	mu          sync.Mutex
	startTLS    bool
	auth        bool
	mutation    bool
	mailFrom    string
	recipients  []string
	message     []byte
}

func newSMTPServer(t *testing.T, options smtpServerOptions) *smtpTestServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &smtpTestServer{t: t, listener: listener, certificate: newTestCertificate(t)}
	go server.serve(options)
	return server
}

func (server *smtpTestServer) serve(options smtpServerOptions) {
	for {
		conn, err := server.listener.Accept()
		if err != nil {
			return
		}
		go server.handle(conn, options)
	}
}

func (server *smtpTestServer) handle(conn net.Conn, options smtpServerOptions) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeSMTP(writer, "220 localhost ready")
	tlsActive := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		trimmed := strings.TrimSpace(line)
		command := strings.ToUpper(trimmed)
		switch {
		case strings.HasPrefix(command, "EHLO "):
			if !tlsActive && options.AdvertiseSTARTTLS {
				_, _ = writer.WriteString("250-localhost\r\n250-STARTTLS\r\n250 AUTH PLAIN\r\n")
			} else if !tlsActive {
				_, _ = writer.WriteString("250-localhost\r\n250 SIZE 1024\r\n")
			} else {
				_, _ = writer.WriteString("250-localhost\r\n250-AUTH PLAIN\r\n250-SMTPUTF8\r\n250 SIZE 1024\r\n")
			}
			_ = writer.Flush()
		case command == "STARTTLS":
			server.mu.Lock()
			server.startTLS = true
			server.mu.Unlock()
			writeSMTP(writer, "220 begin tls")
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{server.certificate.certificate}, MinVersion: tls.VersionTLS12})
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			reader = bufio.NewReader(conn)
			writer = bufio.NewWriter(conn)
			tlsActive = true
		case strings.HasPrefix(command, "AUTH "):
			server.mu.Lock()
			server.auth = true
			server.mu.Unlock()
			if options.AuthOK {
				writeSMTP(writer, "235 2.7.0 authenticated")
			} else {
				writeSMTP(writer, "535 5.7.8 rejected")
			}
		case command == "NOOP":
			writeSMTP(writer, "250 ok")
		case command == "QUIT":
			writeSMTP(writer, "221 bye")
			return
		case strings.HasPrefix(command, "MAIL"):
			server.mu.Lock()
			server.mutation = true
			server.mailFrom = trimmed
			server.mu.Unlock()
			if options.AcceptSend {
				writeSMTP(writer, "250 sender ok")
			} else {
				writeSMTP(writer, "550 validation cannot send")
			}
		case strings.HasPrefix(command, "RCPT"):
			server.mu.Lock()
			server.mutation = true
			server.recipients = append(server.recipients, trimmed)
			server.mu.Unlock()
			if options.AcceptSend {
				writeSMTP(writer, "250 recipient ok")
			} else {
				writeSMTP(writer, "550 validation cannot send")
			}
		case command == "DATA":
			server.mu.Lock()
			server.mutation = true
			server.mu.Unlock()
			if !options.AcceptSend {
				writeSMTP(writer, "550 validation cannot send")
				continue
			}
			writeSMTP(writer, "354 continue")
			data, readErr := io.ReadAll(textproto.NewReader(reader).DotReader())
			if readErr != nil {
				return
			}
			server.mu.Lock()
			server.message = append([]byte(nil), data...)
			server.mu.Unlock()
			if options.DisconnectAfterData {
				return
			}
			if options.RejectAfterData {
				writeSMTP(writer, "550 message rejected")
			} else {
				writeSMTP(writer, "250 accepted")
			}
		default:
			writeSMTP(writer, "500 unsupported")
		}
	}
}

func writeSMTP(writer *bufio.Writer, line string) {
	_, _ = writer.WriteString(line + "\r\n")
	_ = writer.Flush()
}

func (server *smtpTestServer) Port() string {
	_, port, _ := net.SplitHostPort(server.listener.Addr().String())
	return port
}

func (server *smtpTestServer) ClientTLSConfig() *tls.Config {
	return &tls.Config{RootCAs: server.certificate.pool, ServerName: "localhost", MinVersion: tls.VersionTLS12}
}

func (server *smtpTestServer) SawSTARTTLS() bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.startTLS
}

func (server *smtpTestServer) SawAuth() bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.auth
}

func (server *smtpTestServer) SawMutation() bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.mutation
}

func (server *smtpTestServer) Envelope() (string, []string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.mailFrom, append([]string(nil), server.recipients...)
}

func (server *smtpTestServer) Message() []byte {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]byte(nil), server.message...)
}

func (server *smtpTestServer) Close() { _ = server.listener.Close() }

func TestFailureErrorsAreRedacted(t *testing.T) {
	failure := transport.NewFailure(transport.StageIMAPAuth, transport.FailureAuthentication, false, errors.New("secret tail and server text"))
	if got := failure.Error(); got != "imap_auth failed" || strings.Contains(got, "secret") || strings.Contains(got, "server") {
		t.Fatalf("failure text = %q", got)
	}
}
