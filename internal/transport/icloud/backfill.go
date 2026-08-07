package icloud

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

func (client *Client) FetchBackfillPage(ctx context.Context, auth transport.AuthRequest, request transport.BackfillFetchRequest, stager blob.Stager) (transport.BackfillPage, error) {
	if stager == nil {
		return transport.BackfillPage{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureConfiguration, false, errors.New("blob stager is required"))
	}
	if request.Limit < 1 || request.Limit > 100 {
		return transport.BackfillPage{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureConfiguration, false, errors.New("backfill page limit is outside the supported range"))
	}
	if request.After != nil && request.Before != nil && !request.After.Before(*request.Before) {
		return transport.BackfillPage{}, transport.NewFailure(transport.StageIMAPSearch, transport.FailureConfiguration, false, errors.New("backfill after date must precede before date"))
	}
	session, err := client.openRemoteMailbox(ctx, auth, request.Mailbox, request.ExpectedUIDValidity)
	if err != nil {
		return transport.BackfillPage{}, err
	}
	defer session.close()

	selection, err := selectBackfillUIDs(ctx, session.conn, session.client, session.selected, request, client.config.StageTimeout)
	if err != nil {
		return transport.BackfillPage{}, err
	}
	messages, err := fetchInboxMessages(ctx, session.conn, session.client, selection.UIDs, stager, session.condStore, client.config.StageTimeout)
	if err != nil {
		return transport.BackfillPage{}, err
	}
	if err := runWithDeadline(ctx, session.conn, client.config.StageTimeout, func() error {
		return session.client.Logout().Wait()
	}); err != nil {
		abortFetched(messages)
		return transport.BackfillPage{}, classifyFailure(transport.StageIMAPLogout, err)
	}
	return transport.BackfillPage{
		Mailbox:       normalizedMailbox(request.Mailbox),
		UIDValidity:   session.selected.UIDValidity,
		BeforeUID:     selection.StartBeforeUID,
		NextBeforeUID: selection.NextBeforeUID,
		Complete:      selection.Complete,
		Messages:      messages,
	}, nil
}

func (client *Client) MailboxStatus(ctx context.Context, auth transport.AuthRequest, mailbox string) (transport.MailboxStatus, error) {
	session, err := client.openRemoteMailbox(ctx, auth, mailbox, 0)
	if err != nil {
		return transport.MailboxStatus{}, err
	}
	defer session.close()
	mailbox = normalizedMailbox(mailbox)
	status, err := runValueWithDeadline(ctx, session.conn, client.config.StageTimeout, func() (*imap.StatusData, error) {
		return session.client.Status(mailbox, &imap.StatusOptions{NumMessages: true, NumUnseen: true, UIDNext: true, UIDValidity: true}).Wait()
	})
	if err != nil {
		return transport.MailboxStatus{}, classifyFailure(transport.StageIMAPStatus, err)
	}
	result := transport.MailboxStatus{Mailbox: mailbox, UIDNext: uint32(status.UIDNext), UIDValidity: status.UIDValidity}
	if status.NumMessages != nil {
		result.Messages = *status.NumMessages
	}
	if status.NumUnseen != nil {
		result.Unseen = *status.NumUnseen
	}
	if err := runWithDeadline(ctx, session.conn, client.config.StageTimeout, func() error { return session.client.Logout().Wait() }); err != nil {
		return transport.MailboxStatus{}, classifyFailure(transport.StageIMAPLogout, err)
	}
	return result, nil
}

type backfillUIDSelection struct {
	UIDs           []imap.UID
	StartBeforeUID uint32
	NextBeforeUID  uint32
	Complete       bool
}

func selectBackfillUIDs(ctx context.Context, conn net.Conn, client *imapclient.Client, selected *imap.SelectData, request transport.BackfillFetchRequest, timeout time.Duration) (backfillUIDSelection, error) {
	remoteExclusive := uint32(selected.UIDNext)
	if remoteExclusive == 0 {
		remoteExclusive = 1
	}
	startBefore := request.BeforeUID
	if startBefore == 0 || startBefore > remoteExclusive {
		startBefore = remoteExclusive
	}
	if startBefore <= 1 || selected.NumMessages == 0 {
		return backfillUIDSelection{StartBeforeUID: startBefore, Complete: true}, nil
	}

	criteria := imap.SearchCriteria{}
	if request.After != nil {
		criteria.Since = request.After.UTC()
	}
	if request.Before != nil {
		criteria.Before = request.Before.UTC()
	}

	end := startBefore - 1
	found := make([]imap.UID, 0, request.Limit)
	nextBefore := startBefore
	windows := 0
	for end > 0 && len(found) < request.Limit && windows < maxUIDScanWindows {
		start := uint32(1)
		if end >= incrementalUIDWindow {
			start = end - incrementalUIDWindow + 1
		}
		uidSet := imap.UIDSet{}
		uidSet.AddRange(imap.UID(start), imap.UID(end))
		windowCriteria := criteria
		windowCriteria.UID = []imap.UIDSet{uidSet}
		result, err := runValueWithDeadline(ctx, conn, timeout, func() (*imap.SearchData, error) {
			return client.UIDSearch(&windowCriteria, nil).Wait()
		})
		if err != nil {
			return backfillUIDSelection{}, classifyFailure(transport.StageIMAPSearch, err)
		}
		uids := result.AllUIDs()
		for _, uid := range uids {
			numeric := uint32(uid)
			if numeric < start || numeric > end {
				return backfillUIDSelection{}, transport.NewFailure(transport.StageIMAPSearch, transport.FailureUnavailable, false, errors.New("provider returned a UID outside the bounded backfill window"))
			}
		}
		sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
		remaining := request.Limit - len(found)
		if len(uids) > remaining {
			uids = uids[:remaining]
		}
		found = append(found, uids...)
		if len(found) == request.Limit {
			nextBefore = uint32(found[len(found)-1])
			break
		}
		nextBefore = start
		if start == 1 {
			end = 0
		} else {
			end = start - 1
		}
		windows++
	}
	return backfillUIDSelection{
		UIDs:           found,
		StartBeforeUID: startBefore,
		NextBeforeUID:  nextBefore,
		Complete:       end == 0 && len(found) < request.Limit,
	}, nil
}

func normalizedMailbox(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "INBOX"
	}
	return value
}
