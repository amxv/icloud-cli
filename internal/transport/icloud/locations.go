package icloud

import (
	"context"
	"crypto/tls"
	"errors"
	"mime"
	"net"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
)

func (client *Client) MutateLocation(
	ctx context.Context,
	auth transport.AuthRequest,
	request transport.LocationMutationRequest,
) (transport.LocationMutationResult, error) {
	sourceMailbox := strings.TrimSpace(request.SourceMailbox)
	destinationMailbox := strings.TrimSpace(request.DestinationMailbox)
	result := transport.LocationMutationResult{
		Operation:          request.Operation,
		SourceMailbox:      sourceMailbox,
		SourceUIDValidity:  request.ExpectedSourceUIDValidity,
		SourceUID:          request.SourceUID,
		DestinationMailbox: destinationMailbox,
		SourcePresent:      true,
	}
	if sourceMailbox == "" || request.ExpectedSourceUIDValidity == 0 || request.SourceUID == 0 {
		return result, transport.NewFailure(transport.StageIMAPSelect, transport.FailureConfiguration, false, errors.New("source mailbox identity is required"))
	}
	switch request.Operation {
	case transport.LocationMutationCopy, transport.LocationMutationMove:
		if destinationMailbox == "" || strings.EqualFold(sourceMailbox, destinationMailbox) {
			return result, transport.NewFailure(transport.StageIMAPCopy, transport.FailureConfiguration, false, errors.New("a distinct destination mailbox is required"))
		}
	case transport.LocationMutationDelete:
		if destinationMailbox != "" {
			return result, transport.NewFailure(transport.StageIMAPExpunge, transport.FailureConfiguration, false, errors.New("permanent delete cannot name a destination mailbox"))
		}
	default:
		return result, transport.NewFailure(transport.StageIMAPSelect, transport.FailureConfiguration, false, errors.New("unsupported location mutation"))
	}

	address := net.JoinHostPort(client.config.IMAPHost, client.config.IMAPPort)
	plainConn, err := client.dial(ctx, "tcp", address)
	if err != nil {
		return result, classifyConnectError(err)
	}
	defer plainConn.Close()
	tlsConn := tls.Client(plainConn, client.tlsConfig(client.config.IMAPHost))
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, tlsConn.Handshake); err != nil {
		return result, classifyFailure(transport.StageTLS, err)
	}
	imapClient := imapclient.New(tlsConn, &imapclient.Options{WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader}})
	defer closeIMAPClient(plainConn, imapClient)
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, imapClient.WaitGreeting); err != nil {
		return result, classifyFailure(transport.StageIMAPGreeting, err)
	}
	preAuthCaps, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		return result, classifyFailure(transport.StageIMAPCapability, err)
	}
	if !preAuthCaps.Has(imap.CapIMAP4rev1) && !preAuthCaps.Has(imap.CapIMAP4rev2) {
		return result, transport.NewFailure(transport.StageIMAPCapability, transport.FailureUnavailable, false, errors.New("required IMAP capability missing"))
	}
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Login(auth.Username, string(auth.Password)).Wait()
	}); err != nil {
		return result, classifyAuthenticationFailure(transport.StageIMAPAuth, err)
	}
	postAuthCaps, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		return result, classifyFailure(transport.StageIMAPCapability, err)
	}
	condStore := postAuthCaps.Has(imap.CapCondStore)
	uidPlus := postAuthCaps.Has(imap.CapUIDPlus) || postAuthCaps.Has(imap.CapIMAP4rev2)
	moveSupported := postAuthCaps.Has(imap.CapMove) || postAuthCaps.Has(imap.CapIMAP4rev2)
	selected, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (*imap.SelectData, error) {
		return imapClient.Select(sourceMailbox, &imap.SelectOptions{CondStore: condStore}).Wait()
	})
	if err != nil {
		return result, classifyFailure(transport.StageIMAPSelect, err)
	}
	if selected.UIDValidity != request.ExpectedSourceUIDValidity {
		return result, &transport.UIDValidityError{Expected: request.ExpectedSourceUIDValidity, Observed: selected.UIDValidity}
	}
	sourceStates, err := fetchLocationStates(ctx, tlsConn, imapClient, []imap.UID{imap.UID(request.SourceUID)}, condStore, client.config.StageTimeout)
	if err != nil {
		return result, err
	}
	sourceState := sourceStates[0]
	result.SourceFlags = append([]string(nil), sourceState.Flags...)
	result.SourceModSequence = sourceState.ModSequence
	result.SourcePresent = sourceState.Present
	if !sourceState.Present || (request.ExpectedSourceModSequence != 0 && sourceState.ModSequence != 0 && sourceState.ModSequence != request.ExpectedSourceModSequence) {
		return result, &transport.LocationStateConflictError{
			ExpectedModSequence: request.ExpectedSourceModSequence,
			ObservedModSequence: sourceState.ModSequence,
			Present:             sourceState.Present,
		}
	}

	uidSet := imap.UIDSetNum(imap.UID(request.SourceUID))
	var operationErr error
	switch request.Operation {
	case transport.LocationMutationCopy:
		result.ProviderMethod = "copy"
		copyData, copyErr := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (*imap.CopyData, error) {
			return imapClient.Copy(uidSet, destinationMailbox).Wait()
		})
		if copyErr != nil {
			result.ProviderMutationObserved = true
			result.ReconciliationRequired = true
			operationErr = classifyFailure(transport.StageIMAPCopy, copyErr)
			break
		}
		result.ProviderMutationObserved = true
		applyCopyMapping(&result, request.SourceUID, copyData)
		if !result.DestinationKnown {
			result.ReconciliationRequired = true
		}
	case transport.LocationMutationMove:
		if moveSupported {
			result.ProviderMethod = "move"
			moveData, moveErr := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (*imapclient.MoveData, error) {
				return imapClient.Move(uidSet, destinationMailbox).Wait()
			})
			if moveErr != nil {
				result.ProviderMutationObserved = true
				result.ReconciliationRequired = true
				operationErr = classifyFailure(transport.StageIMAPMove, moveErr)
				break
			}
			result.ProviderMutationObserved = true
			result.SourcePresent = false
			applyMoveMapping(&result, request.SourceUID, moveData)
			if !result.DestinationKnown {
				result.ReconciliationRequired = true
			}
			break
		}
		result.ProviderMethod = "copy_only"
		copyData, copyErr := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (*imap.CopyData, error) {
			return imapClient.Copy(uidSet, destinationMailbox).Wait()
		})
		if copyErr != nil {
			result.ProviderMutationObserved = true
			result.ReconciliationRequired = true
			operationErr = classifyFailure(transport.StageIMAPCopy, copyErr)
			break
		}
		result.ProviderMutationObserved = true
		applyCopyMapping(&result, request.SourceUID, copyData)
		result.ReconciliationRequired = true
		if !uidPlus {
			operationErr = transport.NewFailure(transport.StageIMAPExpunge, transport.FailureUnavailable, false, errors.New("safe exact expunge requires UIDPLUS or IMAP4rev2"))
			break
		}
		result.ProviderMethod = "copy_uid_expunge"
		result.ProviderMutationObserved = true
		if err := addDeletedFlag(ctx, tlsConn, imapClient, uidSet, client.config.StageTimeout); err != nil {
			operationErr = classifyFailure(transport.StageIMAPStore, err)
			refreshSourceState(ctx, tlsConn, imapClient, request.SourceUID, condStore, client.config.StageTimeout, &result)
			break
		}
		if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
			return imapClient.UIDExpunge(uidSet).Close()
		}); err != nil {
			operationErr = classifyFailure(transport.StageIMAPExpunge, err)
			refreshSourceState(ctx, tlsConn, imapClient, request.SourceUID, condStore, client.config.StageTimeout, &result)
			break
		}
		result.SourcePresent = false
		result.ReconciliationRequired = !result.DestinationKnown
	case transport.LocationMutationDelete:
		result.ProviderMethod = "uid_expunge"
		if !uidPlus {
			operationErr = transport.NewFailure(transport.StageIMAPExpunge, transport.FailureUnavailable, false, errors.New("safe permanent delete requires UIDPLUS or IMAP4rev2"))
			break
		}
		result.ProviderMutationObserved = true
		if err := addDeletedFlag(ctx, tlsConn, imapClient, uidSet, client.config.StageTimeout); err != nil {
			operationErr = classifyFailure(transport.StageIMAPStore, err)
			break
		}
		if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
			return imapClient.UIDExpunge(uidSet).Close()
		}); err != nil {
			operationErr = classifyFailure(transport.StageIMAPExpunge, err)
			refreshSourceState(ctx, tlsConn, imapClient, request.SourceUID, condStore, client.config.StageTimeout, &result)
			result.ReconciliationRequired = true
			break
		}
		result.SourcePresent = false
	}

	if result.DestinationKnown {
		if observeErr := observeMutationDestination(ctx, tlsConn, imapClient, condStore, client.config.StageTimeout, &result); observeErr != nil {
			result.ReconciliationRequired = true
			if operationErr == nil {
				operationErr = observeErr
			}
		}
	}
	if logoutErr := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Logout().Wait()
	}); logoutErr != nil && operationErr == nil {
		if result.ProviderMutationObserved {
			result.ReconciliationRequired = true
		}
		operationErr = classifyFailure(transport.StageIMAPLogout, logoutErr)
	}
	return result, operationErr
}

func addDeletedFlag(ctx context.Context, conn net.Conn, client *imapclient.Client, uidSet imap.UIDSet, timeout time.Duration) error {
	command := client.Store(uidSet, &imap.StoreFlags{
		Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted},
	}, nil)
	return runWithDeadline(ctx, conn, timeout, command.Close)
}

func applyCopyMapping(result *transport.LocationMutationResult, sourceUID uint32, data *imap.CopyData) {
	if data == nil {
		return
	}
	applyMappedUIDs(result, sourceUID, data.UIDValidity, data.SourceUIDs, data.DestUIDs)
}

func applyMoveMapping(result *transport.LocationMutationResult, sourceUID uint32, data *imapclient.MoveData) {
	if data == nil {
		return
	}
	source, sourceOK := data.SourceUIDs.(imap.UIDSet)
	destination, destinationOK := data.DestUIDs.(imap.UIDSet)
	if !sourceOK || !destinationOK {
		return
	}
	applyMappedUIDs(result, sourceUID, data.UIDValidity, source, destination)
}

func applyMappedUIDs(result *transport.LocationMutationResult, sourceUID uint32, uidValidity uint32, source, destination imap.UIDSet) {
	sourceValues, sourceOK := source.Nums()
	destinationValues, destinationOK := destination.Nums()
	if !sourceOK || !destinationOK || len(sourceValues) != 1 || len(destinationValues) != 1 ||
		uint32(sourceValues[0]) != sourceUID || uidValidity == 0 || destinationValues[0] == 0 {
		return
	}
	result.DestinationUIDValidity = uidValidity
	result.DestinationUID = uint32(destinationValues[0])
	result.DestinationKnown = true
}

func observeMutationDestination(
	ctx context.Context,
	conn net.Conn,
	client *imapclient.Client,
	condStore bool,
	timeout time.Duration,
	result *transport.LocationMutationResult,
) error {
	selected, err := runValueWithDeadline(ctx, conn, timeout, func() (*imap.SelectData, error) {
		return client.Select(result.DestinationMailbox, &imap.SelectOptions{CondStore: condStore}).Wait()
	})
	if err != nil {
		return classifyFailure(transport.StageIMAPSelect, err)
	}
	if selected.UIDValidity != result.DestinationUIDValidity {
		return &transport.UIDValidityError{Expected: result.DestinationUIDValidity, Observed: selected.UIDValidity}
	}
	states, err := fetchLocationStates(ctx, conn, client, []imap.UID{imap.UID(result.DestinationUID)}, condStore, timeout)
	if err != nil {
		return err
	}
	state := states[0]
	if !state.Present {
		return transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, false, errors.New("mapped destination UID is not present"))
	}
	result.DestinationFlags = append([]string(nil), state.Flags...)
	result.DestinationModSequence = state.ModSequence
	return nil
}

func refreshSourceState(
	ctx context.Context,
	conn net.Conn,
	client *imapclient.Client,
	uid uint32,
	condStore bool,
	timeout time.Duration,
	result *transport.LocationMutationResult,
) {
	states, err := fetchLocationStates(ctx, conn, client, []imap.UID{imap.UID(uid)}, condStore, timeout)
	if err != nil || len(states) != 1 {
		result.ReconciliationRequired = true
		return
	}
	result.SourceFlags = append([]string(nil), states[0].Flags...)
	result.SourceModSequence = states[0].ModSequence
	result.SourcePresent = states[0].Present
	result.ReconciliationRequired = true
}

var _ transport.IMAPLocationClient = (*Client)(nil)
