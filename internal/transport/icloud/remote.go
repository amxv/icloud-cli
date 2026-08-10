package icloud

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/quotedprintable"
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
	remoteSearchUIDWindow      = uint32(256)
	defaultRemoteCandidateUIDs = 2048
	maxRemoteCandidateUIDs     = 8192
	remoteSnippetBytes         = uint32(4096)
)

type remoteMailboxSession struct {
	conn      net.Conn
	client    *imapclient.Client
	selected  *imap.SelectData
	condStore bool
}

func (client *Client) SearchRemote(ctx context.Context, auth transport.AuthRequest, request transport.RemoteSearchRequest) (transport.RemoteSearchPage, error) {
	if request.Limit < 1 || request.Limit > 100 {
		return transport.RemoteSearchPage{}, transport.NewFailure(transport.StageIMAPSearch, transport.FailureConfiguration, false, errors.New("remote search limit is outside the supported range"))
	}
	if request.MaxCandidateUIDs == 0 {
		request.MaxCandidateUIDs = defaultRemoteCandidateUIDs
	}
	if request.MaxCandidateUIDs < 1 || request.MaxCandidateUIDs > maxRemoteCandidateUIDs {
		return transport.RemoteSearchPage{}, transport.NewFailure(transport.StageIMAPSearch, transport.FailureConfiguration, false, errors.New("remote candidate UID bound is outside the supported range"))
	}
	session, err := client.openRemoteMailbox(ctx, auth, request.Mailbox, request.ExpectedUIDValidity)
	if err != nil {
		return transport.RemoteSearchPage{}, err
	}
	defer session.close()

	base := buildRemoteSearchCriteria(request.Criteria)
	summaries, more, partial, candidates, nextUID, pendingUIDs, err := searchRemoteSummaries(
		ctx, session.conn, session.client, session.selected, base,
		request.Criteria.After, request.Criteria.Before,
		request.Limit, request.MaxCandidateUIDs, request.StartUID, request.PendingUIDs, client.config.StageTimeout,
	)
	if err != nil {
		return transport.RemoteSearchPage{}, err
	}
	if err := runWithDeadline(ctx, session.conn, client.config.StageTimeout, func() error {
		return session.client.Logout().Wait()
	}); err != nil {
		return transport.RemoteSearchPage{}, classifyFailure(transport.StageIMAPLogout, err)
	}
	return transport.RemoteSearchPage{
		Mailbox: strings.TrimSpace(request.Mailbox), UIDValidity: session.selected.UIDValidity,
		Messages: summaries, MoreAvailable: more, Partial: partial, CandidateUIDs: candidates,
		NextUID: nextUID, PendingUIDs: pendingUIDs,
	}, nil
}

func (client *Client) FetchRemoteMessage(ctx context.Context, auth transport.AuthRequest, request transport.RemoteFetchRequest, stager blob.Stager) (transport.RemoteFetchResult, error) {
	if request.UID == 0 || stager == nil {
		return transport.RemoteFetchResult{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureConfiguration, false, errors.New("remote message UID and blob stager are required"))
	}
	session, err := client.openRemoteMailbox(ctx, auth, request.Mailbox, request.ExpectedUIDValidity)
	if err != nil {
		return transport.RemoteFetchResult{}, err
	}
	defer session.close()
	messages, err := fetchInboxMessages(
		ctx, session.conn, session.client, []imap.UID{imap.UID(request.UID)},
		stager, session.condStore, client.config.StageTimeout,
	)
	if err != nil {
		return transport.RemoteFetchResult{}, err
	}
	if len(messages) != 1 || messages[0].UID != request.UID {
		abortFetched(messages)
		return transport.RemoteFetchResult{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, false, errors.New("remote message is no longer available"))
	}
	if err := runWithDeadline(ctx, session.conn, client.config.StageTimeout, func() error {
		return session.client.Logout().Wait()
	}); err != nil {
		abortFetched(messages)
		return transport.RemoteFetchResult{}, classifyFailure(transport.StageIMAPLogout, err)
	}
	return transport.RemoteFetchResult{
		Mailbox: strings.TrimSpace(request.Mailbox), UIDValidity: session.selected.UIDValidity,
		Message: messages[0],
	}, nil
}

func (client *Client) FetchRemoteSummary(ctx context.Context, auth transport.AuthRequest, request transport.RemoteFetchRequest) (transport.RemoteSummaryResult, error) {
	if request.UID == 0 {
		return transport.RemoteSummaryResult{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureConfiguration, false, errors.New("remote message UID is required"))
	}
	session, err := client.openRemoteMailbox(ctx, auth, request.Mailbox, request.ExpectedUIDValidity)
	if err != nil {
		return transport.RemoteSummaryResult{}, err
	}
	defer session.close()
	summaries, err := fetchRemoteSummaries(ctx, session.conn, session.client, []imap.UID{imap.UID(request.UID)}, client.config.StageTimeout)
	if err != nil {
		return transport.RemoteSummaryResult{}, err
	}
	if len(summaries) != 1 || summaries[0].UID != request.UID {
		return transport.RemoteSummaryResult{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, false, errors.New("remote message is no longer available"))
	}
	if err := runWithDeadline(ctx, session.conn, client.config.StageTimeout, func() error {
		return session.client.Logout().Wait()
	}); err != nil {
		return transport.RemoteSummaryResult{}, classifyFailure(transport.StageIMAPLogout, err)
	}
	return transport.RemoteSummaryResult{
		Mailbox: strings.TrimSpace(request.Mailbox), UIDValidity: session.selected.UIDValidity, Message: summaries[0],
	}, nil
}

func (client *Client) openRemoteMailbox(ctx context.Context, auth transport.AuthRequest, mailbox string, expectedUIDValidity uint32) (*remoteMailboxSession, error) {
	mailbox = strings.TrimSpace(mailbox)
	if mailbox == "" {
		mailbox = "INBOX"
	}
	address := net.JoinHostPort(client.config.IMAPHost, client.config.IMAPPort)
	plainConn, err := client.dial(ctx, "tcp", address)
	if err != nil {
		return nil, classifyConnectError(err)
	}
	fail := func(err error) (*remoteMailboxSession, error) {
		_ = plainConn.Close()
		return nil, err
	}
	tlsConn := tls.Client(plainConn, client.tlsConfig(client.config.IMAPHost))
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, tlsConn.Handshake); err != nil {
		return fail(classifyFailure(transport.StageTLS, err))
	}
	imapClient := imapclient.New(tlsConn, &imapclient.Options{WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader}})
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, imapClient.WaitGreeting); err != nil {
		closeIMAPClient(plainConn, imapClient)
		return nil, classifyFailure(transport.StageIMAPGreeting, err)
	}
	preAuthCaps, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		closeIMAPClient(plainConn, imapClient)
		return nil, classifyFailure(transport.StageIMAPCapability, err)
	}
	if !preAuthCaps.Has(imap.CapIMAP4rev1) && !preAuthCaps.Has(imap.CapIMAP4rev2) {
		closeIMAPClient(plainConn, imapClient)
		return nil, transport.NewFailure(transport.StageIMAPCapability, transport.FailureUnavailable, false, errors.New("required IMAP capability missing"))
	}
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Login(auth.Username, string(auth.Password)).Wait()
	}); err != nil {
		closeIMAPClient(plainConn, imapClient)
		return nil, classifyAuthenticationFailure(transport.StageIMAPAuth, err)
	}
	postAuthCaps, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		closeIMAPClient(plainConn, imapClient)
		return nil, classifyFailure(transport.StageIMAPCapability, err)
	}
	condStore := postAuthCaps.Has(imap.CapCondStore)
	selected, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (*imap.SelectData, error) {
		return imapClient.Select(mailbox, &imap.SelectOptions{ReadOnly: true, CondStore: condStore}).Wait()
	})
	if err != nil {
		closeIMAPClient(plainConn, imapClient)
		return nil, classifyFailure(transport.StageIMAPSelect, err)
	}
	if expectedUIDValidity != 0 && expectedUIDValidity != selected.UIDValidity {
		closeIMAPClient(plainConn, imapClient)
		return nil, &transport.UIDValidityError{Expected: expectedUIDValidity, Observed: selected.UIDValidity}
	}
	return &remoteMailboxSession{conn: tlsConn, client: imapClient, selected: selected, condStore: condStore}, nil
}

func (session *remoteMailboxSession) close() {
	if session == nil || session.conn == nil || session.client == nil {
		return
	}
	closeIMAPClient(session.conn, session.client)
}

func buildRemoteSearchCriteria(request transport.RemoteSearchCriteria) imap.SearchCriteria {
	criteria := imap.SearchCriteria{}
	for _, term := range request.Text {
		if term = strings.TrimSpace(term); term != "" {
			criteria.Text = append(criteria.Text, term)
		}
	}
	if sender := strings.TrimSpace(request.Sender); sender != "" {
		other := orSearchCriteria([]imap.SearchCriteria{
			{Header: searchHeader("From", sender)},
			{Header: searchHeader("Reply-To", sender)},
		})
		criteria.And(&other)
	}
	if participant := strings.TrimSpace(request.Participant); participant != "" {
		other := orSearchCriteria([]imap.SearchCriteria{
			{Header: searchHeader("From", participant)},
			{Header: searchHeader("Reply-To", participant)},
			{Header: searchHeader("To", participant)},
			{Header: searchHeader("Cc", participant)},
		})
		criteria.And(&other)
	}
	if subject := strings.TrimSpace(request.Subject); subject != "" {
		criteria.Header = append(criteria.Header, searchHeader("Subject", subject)...)
	}
	if messageID := strings.Trim(strings.TrimSpace(request.RFCMessageID), "<>"); messageID != "" {
		criteria.Header = append(criteria.Header, searchHeader("Message-ID", messageID)...)
	}
	if request.Unread != nil {
		if *request.Unread {
			criteria.NotFlag = append(criteria.NotFlag, imap.FlagSeen)
		} else {
			criteria.Flag = append(criteria.Flag, imap.FlagSeen)
		}
	}
	if request.Flagged != nil {
		if *request.Flagged {
			criteria.Flag = append(criteria.Flag, imap.FlagFlagged)
		} else {
			criteria.NotFlag = append(criteria.NotFlag, imap.FlagFlagged)
		}
	}
	if request.After != nil {
		criteria.Since = request.After.UTC()
	}
	if request.Before != nil {
		// IMAP BEFORE has day granularity. Include the requested UTC day and
		// apply the exact instant after fetching bounded summaries.
		criteria.Before = request.Before.UTC().AddDate(0, 0, 1)
	}
	return criteria
}

func searchHeader(key, value string) []imap.SearchCriteriaHeaderField {
	return []imap.SearchCriteriaHeaderField{{Key: key, Value: value}}
}

func orSearchCriteria(values []imap.SearchCriteria) imap.SearchCriteria {
	if len(values) == 0 {
		return imap.SearchCriteria{}
	}
	if len(values) == 1 {
		return values[0]
	}
	return imap.SearchCriteria{Or: [][2]imap.SearchCriteria{{values[0], orSearchCriteria(values[1:])}}}
}

func searchRemoteSummaries(
	ctx context.Context,
	conn net.Conn,
	client *imapclient.Client,
	selected *imap.SelectData,
	base imap.SearchCriteria,
	after, before *time.Time,
	limit, maxCandidates int,
	startUID uint32,
	pendingUIDs []uint32,
	timeout time.Duration,
) ([]transport.RemoteMessageSummary, bool, bool, int, uint32, []uint32, error) {
	tail := uidTail(selected.UIDNext)
	found := make([]transport.RemoteMessageSummary, 0, limit)
	candidates := 0

	remainingPending := append([]uint32(nil), pendingUIDs...)
	if len(remainingPending) > 0 {
		imapUIDs := make([]imap.UID, 0, len(remainingPending))
		for _, uid := range remainingPending {
			if uid == 0 || uid > tail {
				return nil, false, false, 0, startUID, nil, transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, false, errors.New("remote continuation contains a UID outside the current mailbox frontier"))
			}
			imapUIDs = append(imapUIDs, imap.UID(uid))
		}
		summaries, fetchErr := fetchRemoteSummaries(ctx, conn, client, imapUIDs, timeout)
		if fetchErr != nil {
			return nil, false, false, 0, startUID, nil, fetchErr
		}
		remainingPending = remainingPending[:0]
		for _, summary := range summaries {
			if !remoteSummaryDateMatches(summary.InternalDate, after, before) {
				continue
			}
			if len(found) < limit {
				found = append(found, summary)
				continue
			}
			remainingPending = append(remainingPending, summary.UID)
		}
		if len(found) == limit {
			more := len(remainingPending) > 0 || startUID > 0
			return found, more, false, 0, startUID, remainingPending, nil
		}
	}

	if tail == 0 || startUID == 0 && len(pendingUIDs) > 0 {
		return found, false, false, candidates, 0, nil, nil
	}
	end := tail
	if startUID != 0 && startUID < end {
		end = startUID
	}
	for end > 0 && candidates < maxCandidates && len(found) < limit {
		remaining := maxCandidates - candidates
		window := int(remoteSearchUIDWindow)
		if remaining < window {
			window = remaining
		}
		start := uint32(1)
		if end >= uint32(window) {
			start = end - uint32(window) + 1
		}
		windowEnd := end
		uidSet := imap.UIDSet{}
		uidSet.AddRange(imap.UID(start), imap.UID(windowEnd))
		criteria := base
		criteria.UID = append(append([]imap.UIDSet(nil), base.UID...), uidSet)
		result, err := runValueWithDeadline(ctx, conn, timeout, func() (*imap.SearchData, error) {
			return client.UIDSearch(&criteria, nil).Wait()
		})
		if err != nil {
			return nil, false, false, candidates, end, nil, classifyFailure(transport.StageIMAPSearch, err)
		}
		uids := result.AllUIDs()
		for _, uid := range uids {
			numeric := uint32(uid)
			if numeric < start || numeric > windowEnd {
				return nil, false, false, candidates, end, nil, transport.NewFailure(transport.StageIMAPSearch, transport.FailureUnavailable, false, errors.New("provider returned a UID outside the bounded search window"))
			}
		}
		sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
		if len(uids) > 0 {
			summaries, fetchErr := fetchRemoteSummaries(ctx, conn, client, uids, timeout)
			if fetchErr != nil {
				return nil, false, false, candidates, end, nil, fetchErr
			}
			for _, summary := range summaries {
				if !remoteSummaryDateMatches(summary.InternalDate, after, before) {
					continue
				}
				if len(found) < limit {
					found = append(found, summary)
					continue
				}
				remainingPending = append(remainingPending, summary.UID)
			}
		}
		candidates += int(windowEnd-start) + 1
		if start == 1 {
			end = 0
		} else {
			end = start - 1
		}
		if len(found) == limit {
			break
		}
	}
	more := len(found) == limit && (len(remainingPending) > 0 || end > 0)
	partial := len(found) < limit && end > 0
	return found, more, partial, candidates, end, remainingPending, nil
}

func remoteSummaryDateMatches(value time.Time, after, before *time.Time) bool {
	if after != nil && value.Before(after.UTC()) {
		return false
	}
	if before != nil && !value.Before(before.UTC()) {
		return false
	}
	return true
}

func fetchRemoteSummaries(ctx context.Context, conn net.Conn, client *imapclient.Client, uids []imap.UID, timeout time.Duration) ([]transport.RemoteMessageSummary, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	section := &imap.FetchItemBodySection{
		Specifier: imap.PartSpecifierText,
		Peek:      true,
		Partial:   &imap.SectionPartial{Offset: 0, Size: int64(remoteSnippetBytes)},
	}
	buffers, err := runValueWithDeadline(ctx, conn, timeout, func() ([]*imapclient.FetchMessageBuffer, error) {
		return client.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
			UID: true, Envelope: true, Flags: true, InternalDate: true, RFC822Size: true,
			BodySection: []*imap.FetchItemBodySection{section},
		}).Collect()
	})
	if err != nil {
		return nil, classifyFailure(transport.StageIMAPFetch, err)
	}
	byUID := make(map[uint32]transport.RemoteMessageSummary, len(buffers))
	for _, buffer := range buffers {
		if buffer == nil || buffer.UID == 0 {
			return nil, transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, false, errors.New("provider omitted a remote search UID"))
		}
		uid := uint32(buffer.UID)
		if _, duplicate := byUID[uid]; duplicate {
			return nil, transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, false, errors.New("provider returned a duplicate remote search UID"))
		}
		flags := make([]string, 0, len(buffer.Flags))
		for _, flag := range buffer.Flags {
			flags = append(flags, string(flag))
		}
		byUID[uid] = transport.RemoteMessageSummary{
			UID: uid, Flags: flags, InternalDate: buffer.InternalDate.UTC(), RFC822Size: buffer.RFC822Size,
			Envelope: convertEnvelope(buffer.Envelope), Snippet: normalizeRemoteSnippet(buffer.FindBodySection(section)),
		}
	}
	result := make([]transport.RemoteMessageSummary, 0, len(uids))
	for _, uid := range uids {
		value, ok := byUID[uint32(uid)]
		if !ok {
			return nil, transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, true, errors.New("provider omitted a selected remote search UID"))
		}
		result = append(result, value)
	}
	return result, nil
}

func normalizeRemoteSnippet(value []byte) string {
	text := strings.ToValidUTF8(string(value), "�")
	text = extractRemoteSnippetPayload(text)
	text = strings.Join(strings.Fields(text), " ")
	const maxRunes = 320
	runes := []rune(text)
	if len(runes) > maxRunes {
		text = strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
	}
	return text
}

func extractRemoteSnippetPayload(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	trimmed := strings.TrimLeft(value, "\n")
	firstLineEnd := strings.IndexByte(trimmed, '\n')
	if firstLineEnd < 0 {
		return trimmed
	}
	firstLine := strings.TrimSpace(trimmed[:firstLineEnd])
	boundary := strings.HasPrefix(firstLine, "--")
	lowerFirst := strings.ToLower(firstLine)
	headerFirst := strings.HasPrefix(lowerFirst, "content-") || strings.HasPrefix(lowerFirst, "mime-version:")
	if !boundary && !headerFirst {
		return trimmed
	}
	remainder := trimmed[firstLineEnd+1:]
	if headerFirst {
		remainder = trimmed
	}
	headerEnd := strings.Index(remainder, "\n\n")
	if headerEnd < 0 {
		return trimmed
	}
	headers := remainder[:headerEnd]
	body := remainder[headerEnd+2:]
	if boundary := strings.Index(body, "\n--"); boundary >= 0 {
		body = body[:boundary]
	}
	encoding := ""
	mediaType := ""
	for _, line := range strings.Split(headers, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "content-transfer-encoding":
			encoding = strings.ToLower(strings.TrimSpace(value))
		case "content-type":
			mediaType = strings.ToLower(strings.TrimSpace(value))
		}
	}
	decoded := []byte(body)
	switch encoding {
	case "quoted-printable":
		if value, err := io.ReadAll(io.LimitReader(quotedprintable.NewReader(strings.NewReader(body)), int64(remoteSnippetBytes))); err == nil {
			decoded = value
		}
	case "base64":
		compact := strings.Map(func(character rune) rune {
			if character == ' ' || character == '\t' || character == '\n' || character == '\r' {
				return -1
			}
			return character
		}, body)
		if value, err := base64.StdEncoding.DecodeString(compact); err == nil {
			decoded = value
		}
	}
	text := strings.ToValidUTF8(string(decoded), "�")
	if strings.Contains(mediaType, "text/html") {
		text = stripRemoteHTML(text)
	}
	return text
}

func stripRemoteHTML(value string) string {
	var builder strings.Builder
	inside := false
	for _, character := range value {
		switch character {
		case '<':
			inside = true
		case '>':
			inside = false
			builder.WriteByte(' ')
		default:
			if !inside {
				builder.WriteRune(character)
			}
		}
	}
	return builder.String()
}
