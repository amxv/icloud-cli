package icloud

import (
	"context"
	"crypto/tls"
	"errors"
	"mime"
	"net"
	"strings"

	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
)

func (client *Client) UpdateFlags(ctx context.Context, auth transport.AuthRequest, request transport.FlagMutationRequest) (transport.FlagMutationResult, error) {
	mailbox := strings.TrimSpace(request.Mailbox)
	if mailbox == "" || request.ExpectedUIDValidity == 0 || request.UID == 0 {
		return transport.FlagMutationResult{}, transport.NewFailure(transport.StageIMAPStore, transport.FailureConfiguration, false, errors.New("mailbox, UID validity, and UID are required"))
	}
	flag, ok := mutableFlag(request.Flag)
	if !ok {
		return transport.FlagMutationResult{}, transport.NewFailure(transport.StageIMAPStore, transport.FailureConfiguration, false, errors.New("unsupported mutable flag"))
	}
	address := net.JoinHostPort(client.config.IMAPHost, client.config.IMAPPort)
	plainConn, err := client.dial(ctx, "tcp", address)
	if err != nil {
		return transport.FlagMutationResult{}, classifyConnectError(err)
	}
	defer plainConn.Close()
	tlsConn := tls.Client(plainConn, client.tlsConfig(client.config.IMAPHost))
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, tlsConn.Handshake); err != nil {
		return transport.FlagMutationResult{}, classifyFailure(transport.StageTLS, err)
	}
	imapClient := imapclient.New(tlsConn, &imapclient.Options{WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader}})
	defer closeIMAPClient(plainConn, imapClient)
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, imapClient.WaitGreeting); err != nil {
		return transport.FlagMutationResult{}, classifyFailure(transport.StageIMAPGreeting, err)
	}
	preAuthCaps, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		return transport.FlagMutationResult{}, classifyFailure(transport.StageIMAPCapability, err)
	}
	if !preAuthCaps.Has(imap.CapIMAP4rev1) && !preAuthCaps.Has(imap.CapIMAP4rev2) {
		return transport.FlagMutationResult{}, transport.NewFailure(transport.StageIMAPCapability, transport.FailureUnavailable, false, errors.New("required IMAP capability missing"))
	}
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Login(auth.Username, string(auth.Password)).Wait()
	}); err != nil {
		return transport.FlagMutationResult{}, classifyAuthenticationFailure(transport.StageIMAPAuth, err)
	}
	postAuthCaps, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		return transport.FlagMutationResult{}, classifyFailure(transport.StageIMAPCapability, err)
	}
	condStore := postAuthCaps.Has(imap.CapCondStore)
	selected, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (*imap.SelectData, error) {
		return imapClient.Select(mailbox, &imap.SelectOptions{CondStore: condStore}).Wait()
	})
	if err != nil {
		return transport.FlagMutationResult{}, classifyFailure(transport.StageIMAPSelect, err)
	}
	if selected.UIDValidity != request.ExpectedUIDValidity {
		return transport.FlagMutationResult{}, &transport.UIDValidityError{Expected: request.ExpectedUIDValidity, Observed: selected.UIDValidity}
	}
	operation := imap.StoreFlagsDel
	if request.Add {
		operation = imap.StoreFlagsAdd
	}
	var storeOptions *imap.StoreOptions
	if condStore && request.ExpectedModSequence != 0 {
		storeOptions = &imap.StoreOptions{UnchangedSince: request.ExpectedModSequence}
	}
	uidSet := imap.UIDSetNum(imap.UID(request.UID))
	buffers, storeErr := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() ([]*imapclient.FetchMessageBuffer, error) {
		return imapClient.Store(uidSet, &imap.StoreFlags{Op: operation, Flags: []imap.Flag{flag}}, storeOptions).Collect()
	})
	var state transport.LocationState
	if storeErr != nil {
		if condStore && request.ExpectedModSequence != 0 && ctx.Err() == nil {
			states, fetchErr := fetchLocationStates(ctx, tlsConn, imapClient, []imap.UID{imap.UID(request.UID)}, true, client.config.StageTimeout)
			if fetchErr == nil {
				state = states[0]
				storeErr = nil
			}
		}
		if storeErr != nil {
			return transport.FlagMutationResult{}, classifyFailure(transport.StageIMAPStore, storeErr)
		}
	}
	if storeErr == nil && state.UID == 0 {
		var ok bool
		state, ok, err = stateFromStoreBuffers(request.UID, buffers)
		if err != nil {
			return transport.FlagMutationResult{}, transport.NewFailure(transport.StageIMAPStore, transport.FailureUnavailable, false, err)
		}
		if !ok || (condStore && state.ModSequence == 0) {
			states, fetchErr := fetchLocationStates(ctx, tlsConn, imapClient, []imap.UID{imap.UID(request.UID)}, condStore, client.config.StageTimeout)
			if fetchErr != nil {
				return transport.FlagMutationResult{}, fetchErr
			}
			state = states[0]
		}
	}
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Logout().Wait()
	}); err != nil {
		return transport.FlagMutationResult{}, classifyFailure(transport.StageIMAPLogout, err)
	}
	return transport.FlagMutationResult{
		Mailbox: mailbox, UIDValidity: selected.UIDValidity, UID: request.UID,
		Flags: state.Flags, ModSequence: state.ModSequence, Present: state.Present,
	}, nil
}

func mutableFlag(value string) (imap.Flag, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case `\seen`:
		return imap.FlagSeen, true
	case `\flagged`:
		return imap.FlagFlagged, true
	case `\answered`:
		return imap.FlagAnswered, true
	default:
		return "", false
	}
}

func stateFromStoreBuffers(uid uint32, buffers []*imapclient.FetchMessageBuffer) (transport.LocationState, bool, error) {
	if len(buffers) == 0 {
		return transport.LocationState{}, false, nil
	}
	if len(buffers) != 1 || buffers[0] == nil {
		return transport.LocationState{}, false, errors.New("provider returned an ambiguous STORE response")
	}
	buffer := buffers[0]
	if buffer.UID != 0 && uint32(buffer.UID) != uid {
		return transport.LocationState{}, false, errors.New("provider returned STORE flags for a different UID")
	}
	flags := make([]string, 0, len(buffer.Flags))
	for _, value := range buffer.Flags {
		flags = append(flags, string(value))
	}
	return transport.LocationState{UID: uid, Flags: flags, ModSequence: buffer.ModSeq, Present: true}, true, nil
}

var _ transport.IMAPFlagClient = (*Client)(nil)
