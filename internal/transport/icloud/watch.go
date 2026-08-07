package icloud

import (
	"context"
	"crypto/tls"
	"errors"
	"mime"
	"net"
	"sync"
	"time"

	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
)

type mailboxWatchSession struct {
	info         transport.MailboxWatchInfo
	conn         net.Conn
	client       *imapclient.Client
	changes      chan struct{}
	stageTimeout time.Duration
	closeOnce    sync.Once
	closeErr     error
}

func (client *Client) OpenMailboxWatch(ctx context.Context, request transport.AuthRequest, mailbox string) (transport.IMAPMailboxWatchSession, error) {
	address := net.JoinHostPort(client.config.IMAPHost, client.config.IMAPPort)
	plainConn, err := client.dial(ctx, "tcp", address)
	if err != nil {
		return nil, classifyConnectError(err)
	}
	tlsConn := tls.Client(plainConn, client.tlsConfig(client.config.IMAPHost))
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, tlsConn.Handshake); err != nil {
		_ = plainConn.Close()
		return nil, classifyFailure(transport.StageTLS, err)
	}

	changes := make(chan struct{}, 1)
	notify := func() {
		select {
		case changes <- struct{}{}:
		default:
		}
	}
	imapClient := imapclient.New(tlsConn, &imapclient.Options{
		WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader},
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Expunge: func(uint32) { notify() },
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				if data != nil && (data.NumMessages != nil || len(data.Flags) > 0 || len(data.PermanentFlags) > 0) {
					notify()
				}
			},
			Fetch: func(*imapclient.FetchMessageData) { notify() },
		},
	})
	cleanup := func() { closeIMAPClient(tlsConn, imapClient) }
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, imapClient.WaitGreeting); err != nil {
		cleanup()
		return nil, classifyFailure(transport.StageIMAPGreeting, err)
	}
	preAuthCaps, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		cleanup()
		return nil, classifyFailure(transport.StageIMAPCapability, err)
	}
	if !preAuthCaps.Has(imap.CapIMAP4rev1) && !preAuthCaps.Has(imap.CapIMAP4rev2) {
		cleanup()
		return nil, transport.NewFailure(transport.StageIMAPCapability, transport.FailureUnavailable, false, errors.New("required IMAP capability missing"))
	}
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Login(request.Username, string(request.Password)).Wait()
	}); err != nil {
		cleanup()
		return nil, classifyAuthenticationFailure(transport.StageIMAPAuth, err)
	}
	caps, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		cleanup()
		return nil, classifyFailure(transport.StageIMAPCapability, err)
	}
	info := transport.MailboxWatchInfo{Mailbox: mailbox, Capabilities: capabilityStrings(caps)}
	if !caps.Has(imap.CapIdle) && !caps.Has(imap.CapIMAP4rev2) {
		info.Mode = transport.MailboxWatchPolling
		_ = runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error { return imapClient.Logout().Wait() })
		cleanup()
		return &mailboxWatchSession{info: info, stageTimeout: client.config.StageTimeout}, nil
	}
	if _, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (*imap.SelectData, error) {
		return imapClient.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
	}); err != nil {
		cleanup()
		return nil, classifyFailure(transport.StageIMAPSelect, err)
	}
	info.Mode = transport.MailboxWatchIdle
	return &mailboxWatchSession{info: info, conn: tlsConn, client: imapClient, changes: changes, stageTimeout: client.config.StageTimeout}, nil
}

func (session *mailboxWatchSession) Info() transport.MailboxWatchInfo { return session.info }

func (session *mailboxWatchSession) Wait(ctx context.Context, interval time.Duration) (transport.MailboxWatchReason, error) {
	if session.info.Mode == transport.MailboxWatchPolling {
		timer := time.NewTimer(interval)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return transport.MailboxWatchPoll, nil
		}
	}
	if session.client == nil || session.conn == nil {
		return "", transport.NewFailure(transport.StageIMAPIdle, transport.FailureConfiguration, false, errors.New("IDLE session is unavailable"))
	}
	idleCmd, err := runValueWithDeadline(ctx, session.conn, session.stageTimeout, session.client.Idle)
	if err != nil {
		return "", classifyFailure(transport.StageIMAPIdle, err)
	}
	idleDone := make(chan error, 1)
	go func() { idleDone <- idleCmd.Wait() }()
	timer := time.NewTimer(interval)
	defer timer.Stop()

	var reason transport.MailboxWatchReason
	select {
	case <-session.changes:
		reason = transport.MailboxWatchChanged
	case <-timer.C:
		reason = transport.MailboxWatchReconcile
	case err := <-idleDone:
		if err != nil {
			return "", classifyFailure(transport.StageIMAPIdle, err)
		}
		return "", transport.NewFailure(transport.StageIMAPIdle, transport.FailureUnavailable, true, errors.New("IDLE ended before a change or reconciliation boundary"))
	case <-ctx.Done():
		stopCtx, cancel := context.WithTimeout(context.Background(), session.stageTimeout)
		_ = stopIdle(stopCtx, session.conn, idleCmd, idleDone, session.stageTimeout)
		cancel()
		return "", ctx.Err()
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), session.stageTimeout)
	err = stopIdle(stopCtx, session.conn, idleCmd, idleDone, session.stageTimeout)
	cancel()
	if err != nil {
		return "", classifyFailure(transport.StageIMAPIdle, err)
	}
	return reason, nil
}

func stopIdle(ctx context.Context, conn net.Conn, idleCmd *imapclient.IdleCommand, idleDone <-chan error, timeout time.Duration) error {
	if err := runWithDeadline(ctx, conn, timeout, idleCmd.Close); err != nil {
		return err
	}
	return runWithDeadline(ctx, conn, timeout, func() error { return <-idleDone })
}

func (session *mailboxWatchSession) Close(ctx context.Context) error {
	session.closeOnce.Do(func() {
		if session.client == nil || session.conn == nil {
			return
		}
		session.closeErr = runWithDeadline(ctx, session.conn, session.stageTimeout, func() error {
			return session.client.Logout().Wait()
		})
		closeIMAPClient(session.conn, session.client)
	})
	return session.closeErr
}
