package icloud

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"strings"

	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
)

func (client *Client) AppendSent(ctx context.Context, auth transport.AuthRequest, request transport.SentAppendRequest) (transport.SentAppendResult, error) {
	mailbox := strings.TrimSpace(request.Mailbox)
	if mailbox == "" || request.Message == nil || request.Size < 0 {
		return transport.SentAppendResult{}, transport.NewFailure(transport.StageIMAPAppend, transport.FailureConfiguration, false, errors.New("Sent mailbox, message, and size are required"))
	}
	address := net.JoinHostPort(client.config.IMAPHost, client.config.IMAPPort)
	plainConn, err := client.dial(ctx, "tcp", address)
	if err != nil {
		return transport.SentAppendResult{}, classifyConnectError(err)
	}
	defer plainConn.Close()
	tlsConn := tls.Client(plainConn, client.tlsConfig(client.config.IMAPHost))
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, tlsConn.Handshake); err != nil {
		return transport.SentAppendResult{}, classifyFailure(transport.StageTLS, err)
	}
	imapClient := imapclient.New(tlsConn, &imapclient.Options{WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader}})
	defer closeIMAPClient(plainConn, imapClient)
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, imapClient.WaitGreeting); err != nil {
		return transport.SentAppendResult{}, classifyFailure(transport.StageIMAPGreeting, err)
	}
	capabilities, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		return transport.SentAppendResult{}, classifyFailure(transport.StageIMAPCapability, err)
	}
	if !capabilities.Has(imap.CapIMAP4rev1) && !capabilities.Has(imap.CapIMAP4rev2) {
		return transport.SentAppendResult{}, transport.NewFailure(transport.StageIMAPCapability, transport.FailureUnavailable, false, errors.New("required IMAP capability missing"))
	}
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Login(auth.Username, string(auth.Password)).Wait()
	}); err != nil {
		return transport.SentAppendResult{}, classifyAuthenticationFailure(transport.StageIMAPAuth, err)
	}
	var appendData *imap.AppendData
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		command := imapClient.Append(mailbox, request.Size, &imap.AppendOptions{
			Flags: []imap.Flag{imap.FlagSeen}, Time: request.InternalDate,
		})
		written, copyErr := io.CopyN(command, request.Message, request.Size)
		if copyErr != nil {
			if errors.Is(copyErr, io.EOF) {
				copyErr = io.ErrUnexpectedEOF
			}
			_ = command.Close()
			return fmt.Errorf("write Sent literal after %d bytes: %w", written, copyErr)
		}
		var extra [1]byte
		if count, extraErr := request.Message.Read(extra[:]); count != 0 || (extraErr != nil && !errors.Is(extraErr, io.EOF)) {
			_ = command.Close()
			if extraErr != nil {
				return fmt.Errorf("verify Sent literal size: %w", extraErr)
			}
			return errors.New("Sent literal contains more bytes than declared")
		}
		if err := command.Close(); err != nil {
			return err
		}
		var waitErr error
		appendData, waitErr = command.Wait()
		return waitErr
	}); err != nil {
		return transport.SentAppendResult{}, classifyFailure(transport.StageIMAPAppend, err)
	}
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Logout().Wait()
	}); err != nil {
		return transport.SentAppendResult{}, classifyFailure(transport.StageIMAPLogout, err)
	}
	result := transport.SentAppendResult{Mailbox: mailbox}
	if appendData != nil {
		result.UIDValidity = appendData.UIDValidity
		result.UID = uint32(appendData.UID)
	}
	return result, nil
}
