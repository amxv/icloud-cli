package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestAgentParityPhase15OneFolderRemoteCursorResumesProviderFrontierAndRejectsScopeDrift(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_REMOTE_PASSWORD", "synthetic-phase15-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {{Folders: []transport.RemoteFolder{
			{Name: "INBOX", WireName: "INBOX", Kind: "inbox", KindSource: "inbox", Selectable: true, Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: "INBOX", Messages: 900, UIDNext: 901, UIDValidity: 100}},
			{Name: "Archive", WireName: "Archive", Kind: "archive", KindSource: "attribute", Selectable: true, Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: "Archive", Messages: 700, UIDNext: 701, UIDValidity: 200}},
		}}},
	}}
	remoteClient := &fakeRemoteClient{
		uidValidity: 100,
		searchPages: []transport.RemoteSearchPage{
			{
				UIDValidity: 100, Messages: []transport.RemoteMessageSummary{{
					UID: 701, InternalDate: time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC),
					Envelope: transport.MessageEnvelope{Subject: "Phase 15 newest", MessageID: "phase15-newest@example.test"},
				}},
				MoreAvailable: true, CandidateUIDs: 400, NextUID: 500, PendingUIDs: []uint32{700},
			},
			{
				UIDValidity: 100, Messages: []transport.RemoteMessageSummary{{
					UID: 700, InternalDate: time.Date(2026, 8, 10, 7, 0, 0, 0, time.UTC),
					Envelope: transport.MessageEnvelope{Subject: "Phase 15 carried", MessageID: "phase15-carried@example.test"},
				}},
				MoreAvailable: true, NextUID: 500,
			},
			{
				UIDValidity: 100, Messages: []transport.RemoteMessageSummary{{
					UID: 399, InternalDate: time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC),
					Envelope: transport.MessageEnvelope{Subject: "Phase 15 older", MessageID: "phase15-older@example.test"},
				}},
				CandidateUIDs: 300,
			},
		},
	}
	commands.remote = remoteClient
	addAndDiscoverInbox(t, commands, home, "ICLOUD_REMOTE_PASSWORD")

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "phase15 sparse", "--remote", "--folder", "inbox", "--limit", "1", "--json")
	if code != 0 || stderr != "" || len(remoteClient.searchRequests) != 1 || remoteClient.searchRequests[0].StartUID != 0 {
		t.Fatalf("first page code=%d stdout=%q stderr=%q requests=%+v", code, stdout, stderr, remoteClient.searchRequests)
	}
	var first struct {
		Data struct {
			Hits       []maildomain.RemoteSearchHit `json:"hits"`
			More       bool                         `json:"more"`
			Partial    bool                         `json:"partial"`
			NextCursor string                       `json:"next_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &first); err != nil || len(first.Data.Hits) != 1 || first.Data.Hits[0].UID != 701 || !first.Data.More || first.Data.Partial || first.Data.NextCursor == "" {
		t.Fatalf("first page parse err=%v page=%+v raw=%q", err, first, stdout)
	}
	cursor, err := maildomain.DecodeRemoteSearchCursor(first.Data.NextCursor)
	if err != nil || cursor.AccountID != searchAccountID(t, home) || cursor.FolderID != searchFolderID(t, home) || cursor.UIDValidity != 100 || cursor.Limit != 1 || cursor.NextUID != 500 || len(cursor.PendingUIDs) != 1 || cursor.PendingUIDs[0] != 700 || len(cursor.QueryHash) != 64 {
		t.Fatalf("cursor=%+v err=%v", cursor, err)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "phase15 sparse", "--remote", "--folder", "inbox", "--cursor", first.Data.NextCursor, "--limit", "1", "--json")
	if code != 0 || stderr != "" || len(remoteClient.searchRequests) != 2 || remoteClient.searchRequests[1].StartUID != 500 || len(remoteClient.searchRequests[1].PendingUIDs) != 1 || remoteClient.searchRequests[1].PendingUIDs[0] != 700 || remoteClient.searchRequests[1].ExpectedUIDValidity != 100 {
		t.Fatalf("resumed page code=%d stdout=%q stderr=%q requests=%+v", code, stdout, stderr, remoteClient.searchRequests)
	}
	var second struct {
		Data struct {
			Hits       []maildomain.RemoteSearchHit `json:"hits"`
			More       bool                         `json:"more"`
			Partial    bool                         `json:"partial"`
			NextCursor string                       `json:"next_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &second); err != nil || len(second.Data.Hits) != 1 || second.Data.Hits[0].UID != 700 || !second.Data.More || second.Data.Partial || second.Data.NextCursor == "" {
		t.Fatalf("second page parse err=%v page=%+v raw=%q", err, second, stdout)
	}
	secondCursor, err := maildomain.DecodeRemoteSearchCursor(second.Data.NextCursor)
	if err != nil || secondCursor.NextUID != 500 || len(secondCursor.PendingUIDs) != 0 {
		t.Fatalf("second cursor=%+v err=%v", secondCursor, err)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "phase15 sparse", "--remote", "--folder", "inbox", "--cursor", second.Data.NextCursor, "--limit", "1", "--json")
	if code != 0 || stderr != "" || len(remoteClient.searchRequests) != 3 || remoteClient.searchRequests[2].StartUID != 500 || len(remoteClient.searchRequests[2].PendingUIDs) != 0 || remoteClient.searchRequests[2].ExpectedUIDValidity != 100 {
		t.Fatalf("third page code=%d stdout=%q stderr=%q requests=%+v", code, stdout, stderr, remoteClient.searchRequests)
	}
	var third struct {
		Data struct {
			Hits       []maildomain.RemoteSearchHit `json:"hits"`
			More       bool                         `json:"more"`
			Partial    bool                         `json:"partial"`
			NextCursor string                       `json:"next_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &third); err != nil || len(third.Data.Hits) != 1 || third.Data.Hits[0].UID != 399 || third.Data.More || third.Data.Partial || third.Data.NextCursor != "" {
		t.Fatalf("third page parse err=%v page=%+v raw=%q", err, third, stdout)
	}

	providerCalls := len(remoteClient.searchRequests)
	assertRemoteCursorRejectedWithoutProviderCall := func(name string, args ...string) {
		t.Helper()
		code, stdout, stderr := runWithAccountCommands(t, commands, "", append([]string{"--home", home, "mail", "search"}, args...)...)
		if code == 0 || stdout != "" || len(remoteClient.searchRequests) != providerCalls || stderr == "" {
			t.Fatalf("%s code=%d stdout=%q stderr=%q provider_calls=%d", name, code, stdout, stderr, len(remoteClient.searchRequests))
		}
	}
	assertRemoteCursorRejectedWithoutProviderCall("query drift", "different query", "--remote", "--folder", "inbox", "--cursor", first.Data.NextCursor, "--limit", "1")
	assertRemoteCursorRejectedWithoutProviderCall("filter drift", "phase15 sparse", "--remote", "--folder", "inbox", "--unread", "--cursor", first.Data.NextCursor, "--limit", "1")
	assertRemoteCursorRejectedWithoutProviderCall("limit drift", "phase15 sparse", "--remote", "--folder", "inbox", "--cursor", first.Data.NextCursor, "--limit", "2")
	assertRemoteCursorRejectedWithoutProviderCall("folder drift", "phase15 sparse", "--remote", "--folder", "Archive", "--cursor", first.Data.NextCursor, "--limit", "1")
	assertRemoteCursorRejectedWithoutProviderCall("all folders", "phase15 sparse", "--remote", "--all-folders", "--cursor", first.Data.NextCursor, "--limit", "1")

	wrongAccount := cursor
	wrongAccount.AccountID = "acct_other"
	encodedWrongAccount, err := maildomain.EncodeRemoteSearchCursor(wrongAccount)
	if err != nil {
		t.Fatal(err)
	}
	assertRemoteCursorRejectedWithoutProviderCall("account drift", "phase15 sparse", "--remote", "--folder", "inbox", "--cursor", encodedWrongAccount, "--limit", "1")

	staleValidity := cursor
	staleValidity.UIDValidity = 999
	encodedStaleValidity, err := maildomain.EncodeRemoteSearchCursor(staleValidity)
	if err != nil {
		t.Fatal(err)
	}
	assertRemoteCursorRejectedWithoutProviderCall("uidvalidity drift", "phase15 sparse", "--remote", "--folder", "inbox", "--cursor", encodedStaleValidity, "--limit", "1")

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "phase15 sparse", "--remote", "--folder", "inbox", "--cursor", "cur_local-not-remote", "--limit", "1")
	if code != cli.ExitUsage || stdout != "" || len(remoteClient.searchRequests) != providerCalls || !strings.Contains(stderr, "remote search cursor is invalid") {
		t.Fatalf("local cursor code=%d stdout=%q stderr=%q provider_calls=%d", code, stdout, stderr, len(remoteClient.searchRequests))
	}
}
