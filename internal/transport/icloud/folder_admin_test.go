package icloud

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/transport"
)

func TestFolderAdminCreateUsesNamespaceHierarchyAndModifiedUTF7(t *testing.T) {
	server := newIMAPServer(t, imapServerOptions{
		AuthOK:             true,
		Namespace:          true,
		NamespacePrefix:    "Root/",
		NamespaceDelimiter: "/",
		ListLines:          []string{`* LIST (\HasNoChildren) "/" "INBOX"`},
	})
	defer server.Close()
	client := New(Config{IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: time.Second, TLSConfig: server.ClientTLSConfig()})
	result, err := client.AdministerFolder(context.Background(), transport.AuthRequest{Username: "owner", Password: []byte("secret")}, transport.FolderAdminRequest{
		Action: transport.FolderAdminCreate, NewMailbox: "Projects/日本語",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Folder.Name != "Root/Projects/日本語" || result.Folder.ParentName != "Root/Projects" || result.Folder.Delimiter != "/" {
		t.Fatalf("created folder = %+v", result.Folder)
	}
	if result.Folder.WireName != encodeMailboxWireName(result.Folder.Name) {
		t.Fatalf("wire name = %q", result.Folder.WireName)
	}
	joined := strings.Join(server.CommandLines(), "\n")
	if !strings.Contains(joined, "NAMESPACE") || !strings.Contains(joined, `CREATE "Root/Projects/&ZeVnLIqe-"`) {
		t.Fatalf("commands = %s", joined)
	}
}

func TestFolderAdminProtectsSystemFoldersAndRejectsEmptyHierarchySegments(t *testing.T) {
	server := newIMAPServer(t, imapServerOptions{
		AuthOK: true,
		ListLines: []string{
			`* LIST (\HasNoChildren) "/" "INBOX"`,
			`* LIST (\HasNoChildren \Sent) "/" "Sent Messages"`,
		},
	})
	defer server.Close()
	client := New(Config{IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: time.Second, TLSConfig: server.ClientTLSConfig()})
	_, err := client.AdministerFolder(context.Background(), transport.AuthRequest{Username: "owner", Password: []byte("secret")}, transport.FolderAdminRequest{
		Action: transport.FolderAdminRename, Mailbox: "Sent Messages", NewMailbox: "Old Sent",
	})
	assertFailure(t, err, transport.StageIMAPRename, transport.FailureConfiguration)
	_, err = client.AdministerFolder(context.Background(), transport.AuthRequest{Username: "owner", Password: []byte("secret")}, transport.FolderAdminRequest{
		Action: transport.FolderAdminCreate, NewMailbox: "Projects//Broken",
	})
	assertFailure(t, err, transport.StageIMAPCreate, transport.FailureConfiguration)
	for _, command := range server.Commands() {
		if command == "RENAME" || command == "CREATE" {
			t.Fatalf("unsafe provider command issued: %s", command)
		}
	}
}

func TestFolderAdminDeleteRechecksNonEmptyMailboxAndSubscriptionCommands(t *testing.T) {
	server := newIMAPServer(t, imapServerOptions{
		AuthOK:    true,
		ListLines: []string{`* LIST (\HasNoChildren) "/" "INBOX"`, `* LIST (\HasNoChildren) "/" "Receipts"`},
		Statuses: map[string]transport.MailboxStatus{
			"Receipts": {Messages: 2, UIDNext: 3, UIDValidity: 9},
		},
	})
	defer server.Close()
	client := New(Config{IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: time.Second, TLSConfig: server.ClientTLSConfig()})
	auth := transport.AuthRequest{Username: "owner", Password: []byte("secret")}
	_, err := client.AdministerFolder(context.Background(), auth, transport.FolderAdminRequest{Action: transport.FolderAdminDelete, Mailbox: "Receipts"})
	var nonEmpty *transport.FolderNonEmptyError
	if !errors.As(err, &nonEmpty) || nonEmpty.Messages != 2 {
		t.Fatalf("non-empty error = %T %v", err, err)
	}
	for _, command := range server.Commands() {
		if command == "DELETE" {
			t.Fatal("delete issued before explicit non-empty authorization")
		}
	}
	result, err := client.AdministerFolder(context.Background(), auth, transport.FolderAdminRequest{Action: transport.FolderAdminDelete, Mailbox: "Receipts", AllowNonEmpty: true})
	if err != nil || !result.Deleted || result.SourceMessages != 2 {
		t.Fatalf("authorized delete = %+v err=%v", result, err)
	}
	if _, err := client.AdministerFolder(context.Background(), auth, transport.FolderAdminRequest{Action: transport.FolderAdminSubscribe, Mailbox: "Receipts"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.AdministerFolder(context.Background(), auth, transport.FolderAdminRequest{Action: transport.FolderAdminUnsubscribe, Mailbox: "Receipts"}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(server.Commands(), " ")
	for _, command := range []string{"STATUS", "DELETE", "SUBSCRIBE", "UNSUBSCRIBE"} {
		if !strings.Contains(joined, command) {
			t.Fatalf("missing %s in %s", command, joined)
		}
	}
}
