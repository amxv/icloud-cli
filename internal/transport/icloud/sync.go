package icloud

import (
	"context"
	"crypto/tls"
	"errors"
	"mime"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
)

const (
	incrementalUIDWindow = uint32(256)
	maxUIDScanWindows    = 32
)

func (client *Client) FetchInboxPage(ctx context.Context, auth transport.AuthRequest, request transport.InboxFetchRequest, stager blob.Stager) (transport.InboxPage, error) {
	if stager == nil {
		return transport.InboxPage{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureConfiguration, false, errors.New("blob stager is required"))
	}
	if request.Limit < 1 || request.Limit > 100 {
		return transport.InboxPage{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureConfiguration, false, errors.New("message page limit is outside the supported range"))
	}
	mailbox := strings.TrimSpace(request.Mailbox)
	if mailbox == "" {
		mailbox = "INBOX"
	}
	address := net.JoinHostPort(client.config.IMAPHost, client.config.IMAPPort)
	plainConn, err := client.dial(ctx, "tcp", address)
	if err != nil {
		return transport.InboxPage{}, classifyConnectError(err)
	}
	defer plainConn.Close()

	tlsConn := tls.Client(plainConn, client.tlsConfig(client.config.IMAPHost))
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, tlsConn.Handshake); err != nil {
		return transport.InboxPage{}, classifyFailure(transport.StageTLS, err)
	}
	imapClient := imapclient.New(tlsConn, &imapclient.Options{WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader}})
	defer imapClient.Close()
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, imapClient.WaitGreeting); err != nil {
		return transport.InboxPage{}, classifyFailure(transport.StageIMAPGreeting, err)
	}
	preAuthCaps, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		return transport.InboxPage{}, classifyFailure(transport.StageIMAPCapability, err)
	}
	if !preAuthCaps.Has(imap.CapIMAP4rev1) && !preAuthCaps.Has(imap.CapIMAP4rev2) {
		return transport.InboxPage{}, transport.NewFailure(transport.StageIMAPCapability, transport.FailureUnavailable, false, errors.New("required IMAP capability missing"))
	}
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Login(auth.Username, string(auth.Password)).Wait()
	}); err != nil {
		return transport.InboxPage{}, classifyAuthenticationFailure(transport.StageIMAPAuth, err)
	}

	selected, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (*imap.SelectData, error) {
		return imapClient.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait()
	})
	if err != nil {
		return transport.InboxPage{}, classifyFailure(transport.StageIMAPSelect, err)
	}
	if request.ExpectedUIDValidity != 0 && request.ExpectedUIDValidity != selected.UIDValidity {
		return transport.InboxPage{}, &transport.UIDValidityError{Expected: request.ExpectedUIDValidity, Observed: selected.UIDValidity}
	}

	selection, err := selectInboxUIDs(ctx, tlsConn, imapClient, selected, request, client.config.StageTimeout)
	if err != nil {
		return transport.InboxPage{}, err
	}
	messages, err := fetchInboxMessages(ctx, tlsConn, imapClient, selection.UIDs, stager, client.config.StageTimeout)
	if err != nil {
		return transport.InboxPage{}, err
	}
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Logout().Wait()
	}); err != nil {
		abortFetched(messages)
		return transport.InboxPage{}, classifyFailure(transport.StageIMAPLogout, err)
	}
	return transport.InboxPage{
		Mailbox:          mailbox,
		UIDValidity:      selected.UIDValidity,
		LastCommittedUID: selection.LastCommittedUID,
		LastScannedUID:   selection.LastScannedUID,
		MoreAvailable:    selection.MoreAvailable,
		Bootstrap:        request.Bootstrap,
		Messages:         messages,
	}, nil
}

type inboxUIDSelection struct {
	UIDs             []imap.UID
	LastCommittedUID uint32
	LastScannedUID   uint32
	MoreAvailable    bool
}

func selectInboxUIDs(ctx context.Context, conn net.Conn, client *imapclient.Client, selected *imap.SelectData, request transport.InboxFetchRequest, timeout time.Duration) (inboxUIDSelection, error) {
	if request.Bootstrap {
		return selectBootstrapUIDs(ctx, conn, client, selected, request.Limit, timeout)
	}
	remoteTail := uidTail(selected.UIDNext)
	lastCommitted := request.LastCommittedUID
	lastScanned := request.LastScannedUID
	if lastScanned < lastCommitted {
		lastScanned = lastCommitted
	}
	if remoteTail <= lastScanned {
		return inboxUIDSelection{LastCommittedUID: lastCommitted, LastScannedUID: lastScanned}, nil
	}

	cursor := lastScanned + 1
	selectedUIDs := make([]imap.UID, 0, request.Limit)
	windows := 0
	for cursor <= remoteTail && len(selectedUIDs) < request.Limit && windows < maxUIDScanWindows {
		end := cursor + incrementalUIDWindow - 1
		if end < cursor || end > remoteTail {
			end = remoteTail
		}
		uidSet := imap.UIDSet{}
		uidSet.AddRange(imap.UID(cursor), imap.UID(end))
		result, err := runValueWithDeadline(ctx, conn, timeout, func() (*imap.SearchData, error) {
			return client.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{uidSet}}, nil).Wait()
		})
		if err != nil {
			return inboxUIDSelection{}, classifyFailure(transport.StageIMAPSearch, err)
		}
		uids := result.AllUIDs()
		sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
		var full bool
		selectedUIDs, lastCommitted, lastScanned, full, err = advanceIncrementalWindow(selectedUIDs, uids, request.Limit, cursor, end, lastCommitted)
		if err != nil {
			return inboxUIDSelection{}, transport.NewFailure(transport.StageIMAPSearch, transport.FailureUnavailable, false, err)
		}
		if full {
			break
		}
		cursor = end + 1
		windows++
	}
	return inboxUIDSelection{
		UIDs:             selectedUIDs,
		LastCommittedUID: lastCommitted,
		LastScannedUID:   lastScanned,
		MoreAvailable:    lastScanned < remoteTail,
	}, nil
}

func advanceIncrementalWindow(selected, discovered []imap.UID, limit int, start, end, lastCommitted uint32) ([]imap.UID, uint32, uint32, bool, error) {
	if limit < 1 || start == 0 || end < start {
		return nil, lastCommitted, 0, false, errors.New("invalid bounded UID window")
	}
	result := append([]imap.UID(nil), selected...)
	var previous uint32
	for _, uid := range discovered {
		numeric := uint32(uid)
		if numeric < start || numeric > end {
			return nil, lastCommitted, 0, false, errors.New("provider returned a UID outside the requested bounded range")
		}
		if previous != 0 && numeric <= previous {
			return nil, lastCommitted, 0, false, errors.New("provider returned duplicate or unsorted UIDs")
		}
		previous = numeric
		result = append(result, uid)
		lastCommitted = numeric
		if len(result) == limit {
			return result, lastCommitted, numeric, true, nil
		}
	}
	return result, lastCommitted, end, false, nil
}

func selectBootstrapUIDs(ctx context.Context, conn net.Conn, client *imapclient.Client, selected *imap.SelectData, limit int, timeout time.Duration) (inboxUIDSelection, error) {
	remoteTail := uidTail(selected.UIDNext)
	if selected.NumMessages == 0 {
		return inboxUIDSelection{LastScannedUID: remoteTail}, nil
	}
	start := uint32(1)
	if selected.NumMessages > uint32(limit) {
		start = selected.NumMessages - uint32(limit) + 1
	}
	sequenceSet := imap.SeqSet{}
	sequenceSet.AddRange(start, selected.NumMessages)
	buffers, err := runValueWithDeadline(ctx, conn, timeout, func() ([]*imapclient.FetchMessageBuffer, error) {
		return client.Fetch(sequenceSet, &imap.FetchOptions{UID: true}).Collect()
	})
	if err != nil {
		return inboxUIDSelection{}, classifyFailure(transport.StageIMAPFetch, err)
	}
	uids := make([]imap.UID, 0, len(buffers))
	for _, buffer := range buffers {
		if buffer != nil && buffer.UID != 0 {
			uids = append(uids, buffer.UID)
		}
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	if len(uids) == 0 {
		return inboxUIDSelection{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, false, errors.New("provider returned no UIDs for the current Inbox tail"))
	}
	for index := 1; index < len(uids); index++ {
		if uids[index] <= uids[index-1] {
			return inboxUIDSelection{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, false, errors.New("provider returned duplicate bootstrap UIDs"))
		}
	}
	last := uint32(uids[len(uids)-1])
	return inboxUIDSelection{UIDs: uids, LastCommittedUID: last, LastScannedUID: last, MoreAvailable: last < remoteTail}, nil
}

func fetchInboxMessages(ctx context.Context, conn net.Conn, client *imapclient.Client, uids []imap.UID, stager blob.Stager, timeout time.Duration) ([]transport.FetchedMessage, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	uidSet := imap.UIDSetNum(uids...)
	section := &imap.FetchItemBodySection{Peek: true}
	var messages []transport.FetchedMessage
	err := runWithDeadline(ctx, conn, timeout, func() error {
		command := client.Fetch(uidSet, &imap.FetchOptions{
			UID:          true,
			Envelope:     true,
			Flags:        true,
			InternalDate: true,
			RFC822Size:   true,
			BodySection:  []*imap.FetchItemBodySection{section},
		})
		closeCommand := func(cause error) error {
			closeErr := command.Close()
			if cause != nil {
				return cause
			}
			return closeErr
		}
		for data := command.Next(); data != nil; data = command.Next() {
			message := transport.FetchedMessage{}
			for item := data.Next(); item != nil; item = data.Next() {
				switch value := item.(type) {
				case imapclient.FetchItemDataUID:
					message.UID = uint32(value.UID)
				case imapclient.FetchItemDataFlags:
					message.Flags = make([]string, 0, len(value.Flags))
					for _, flag := range value.Flags {
						message.Flags = append(message.Flags, string(flag))
					}
				case imapclient.FetchItemDataInternalDate:
					message.InternalDate = value.Time.UTC()
				case imapclient.FetchItemDataRFC822Size:
					message.RFC822Size = value.Size
				case imapclient.FetchItemDataEnvelope:
					message.Envelope = convertEnvelope(value.Envelope)
				case imapclient.FetchItemDataBodySection:
					if value.Literal == nil {
						return closeCommand(errors.New("provider returned an empty raw message literal"))
					}
					if message.Raw != nil {
						_ = message.Raw.Abort()
						return closeCommand(errors.New("provider returned duplicate raw message data"))
					}
					staged, err := stager.Stage(ctx, value.Literal, message.RFC822Size)
					if err != nil {
						return closeCommand(err)
					}
					message.Raw = staged
				}
			}
			if message.UID == 0 || message.Raw == nil {
				if message.Raw != nil {
					_ = message.Raw.Abort()
				}
				return closeCommand(errors.New("provider omitted required UID or raw message data"))
			}
			messages = append(messages, message)
		}
		return closeCommand(nil)
	})
	if err != nil {
		abortFetched(messages)
		return nil, classifyFailure(transport.StageIMAPFetch, err)
	}
	byUID := make(map[uint32]transport.FetchedMessage, len(messages))
	for _, message := range messages {
		if _, exists := byUID[message.UID]; exists {
			abortFetched(messages)
			return nil, transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, false, errors.New("provider returned a duplicate UID"))
		}
		byUID[message.UID] = message
	}
	ordered := make([]transport.FetchedMessage, 0, len(uids))
	for _, uid := range uids {
		message, exists := byUID[uint32(uid)]
		if !exists {
			abortFetched(messages)
			return nil, transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, true, errors.New("provider omitted a selected UID"))
		}
		ordered = append(ordered, message)
	}
	return ordered, nil
}

func convertEnvelope(value *imap.Envelope) transport.MessageEnvelope {
	if value == nil {
		return transport.MessageEnvelope{}
	}
	return transport.MessageEnvelope{
		Date:      value.Date.UTC(),
		Subject:   value.Subject,
		From:      convertEnvelopeAddresses(value.From),
		ReplyTo:   convertEnvelopeAddresses(value.ReplyTo),
		To:        convertEnvelopeAddresses(value.To),
		CC:        convertEnvelopeAddresses(value.Cc),
		BCC:       convertEnvelopeAddresses(value.Bcc),
		InReplyTo: append([]string(nil), value.InReplyTo...),
		MessageID: value.MessageID,
	}
}

func convertEnvelopeAddresses(values []imap.Address) []transport.EnvelopeAddress {
	result := make([]transport.EnvelopeAddress, 0, len(values))
	for _, value := range values {
		if address := strings.TrimSpace(value.Addr()); address != "" {
			result = append(result, transport.EnvelopeAddress{Name: strings.TrimSpace(value.Name), Email: address})
		}
	}
	return result
}

func abortFetched(messages []transport.FetchedMessage) {
	for _, message := range messages {
		if message.Raw != nil {
			_ = message.Raw.Abort()
		}
	}
}

func uidTail(next imap.UID) uint32 {
	if next <= 1 {
		return 0
	}
	return uint32(next - 1)
}
