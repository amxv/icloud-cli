package icloud

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/transport"
)

func TestMailboxWatchIdleChangeStopsIDLEBeforeReturn(t *testing.T) {
	server := newIMAPServer(t, imapServerOptions{AuthOK: true, IdleNotify: true})
	defer server.Close()
	client := New(Config{IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: time.Second, TLSConfig: server.ClientTLSConfig()})
	session, err := client.OpenMailboxWatch(context.Background(), transport.AuthRequest{Username: "fixture", Password: []byte("secret")}, "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	if info := session.Info(); info.Mode != transport.MailboxWatchIdle || !slices.Contains(info.Capabilities, "IDLE") {
		t.Fatalf("watch info = %+v", info)
	}
	reason, err := session.Wait(context.Background(), time.Minute)
	if err != nil || reason != transport.MailboxWatchChanged {
		t.Fatalf("wait reason=%q err=%v", reason, err)
	}
	commands := server.Commands()
	idle := slices.Index(commands, "IDLE")
	done := slices.Index(commands, "DONE")
	if idle < 0 || done <= idle {
		t.Fatalf("commands = %v", commands)
	}
}

func TestMailboxWatchFallsBackWhenIdleUnsupported(t *testing.T) {
	server := newIMAPServer(t, imapServerOptions{AuthOK: true, DisableIdle: true})
	defer server.Close()
	client := New(Config{IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: time.Second, TLSConfig: server.ClientTLSConfig()})
	session, err := client.OpenMailboxWatch(context.Background(), transport.AuthRequest{Username: "fixture", Password: []byte("secret")}, "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if info := session.Info(); info.Mode != transport.MailboxWatchPolling || slices.Contains(info.Capabilities, "IDLE") {
		t.Fatalf("watch info = %+v", info)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := session.Wait(ctx, time.Minute); err == nil {
		t.Fatal("polling wait ignored cancellation")
	}
}

func TestMailboxWatchReportsDroppedIdleConnection(t *testing.T) {
	server := newIMAPServer(t, imapServerOptions{AuthOK: true, DropIdle: true})
	defer server.Close()
	client := New(Config{IMAPHost: "127.0.0.1", IMAPPort: server.Port(), StageTimeout: time.Second, TLSConfig: server.ClientTLSConfig()})
	session, err := client.OpenMailboxWatch(context.Background(), transport.AuthRequest{Username: "fixture", Password: []byte("secret")}, "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Wait(context.Background(), time.Minute); err == nil {
		t.Fatal("dropped IDLE connection returned success")
	}
}
