package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestAllFolderRemoteSearchTraversesArbitraryFoldersInGlobalOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_MULTI_FOLDER_REMOTE_SEARCH_PASSWORD", "synthetic-multiFolderRemoteSearch-secret")
	commands, _ := newTestAccountCommands()
	folders := multiFolderRemoteSearchRemoteFolders(12, 100)
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {{Folders: folders}},
	}}
	addAndDiscoverInbox(t, commands, home, "ICLOUD_MULTI_FOLDER_REMOTE_SEARCH_PASSWORD")

	folderIDs := multiFolderRemoteSearchFolderIDsByName(t, home)
	base := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	remote := &fakeRemoteClient{
		uidValidity:          100,
		searchPagesByMailbox: make(map[string][]transport.RemoteSearchPage),
		summariesByMailbox:   make(map[string]map[uint32]transport.RemoteMessageSummary),
	}
	type expectedHit struct {
		folderID string
		uid      uint32
		date     time.Time
	}
	expected := make([]expectedHit, 0, 13)
	for index, remoteFolder := range folders {
		date := base.Add(-time.Duration(index) * time.Hour)
		if index == 4 {
			date = base.Add(-3 * time.Hour) // deterministic equal-time folder-ID tie with index 3.
		}
		uid := uint32(900 - index*10)
		summary := multiFolderRemoteSearchRemoteSummary(uid, date, remoteFolder.Name, index)
		remote.summariesByMailbox[remoteFolder.Name] = map[uint32]transport.RemoteMessageSummary{uid: summary}
		remote.searchPagesByMailbox[remoteFolder.Name] = []transport.RemoteSearchPage{{
			Messages: []transport.RemoteMessageSummary{summary}, CandidateUIDs: 16,
		}}
		expected = append(expected, expectedHit{folderID: folderIDs[remoteFolder.Name], uid: uid, date: date})
	}

	// One folder needs a second bounded UID scan before its first exact hit.
	sparseName := folders[5].Name
	sparseSummary := remote.summariesByMailbox[sparseName][uint32(850)]
	remote.searchPagesByMailbox[sparseName] = []transport.RemoteSearchPage{
		{Partial: true, CandidateUIDs: 128, NextUID: 50},
		{Messages: []transport.RemoteMessageSummary{sparseSummary}, CandidateUIDs: 50},
	}

	// One folder contains an older second hit. Consuming its merge head must
	// replenish that folder before another folder can safely win the merge.
	multiName := folders[2].Name
	first := remote.summariesByMailbox[multiName][uint32(880)]
	second := multiFolderRemoteSearchRemoteSummary(840, base.Add(-8*time.Hour-30*time.Minute), multiName, 20)
	remote.summariesByMailbox[multiName][second.UID] = second
	remote.searchPagesByMailbox[multiName] = []transport.RemoteSearchPage{
		{Messages: []transport.RemoteMessageSummary{first}, MoreAvailable: true, CandidateUIDs: 64, NextUID: 50},
		{Messages: []transport.RemoteMessageSummary{second}, CandidateUIDs: 50},
	}
	expected = append(expected, expectedHit{folderID: folderIDs[multiName], uid: second.UID, date: second.InternalDate})
	commands.remote = remote

	sort.Slice(expected, func(left, right int) bool {
		if !expected[left].date.Equal(expected[right].date) {
			return expected[left].date.After(expected[right].date)
		}
		if expected[left].folderID != expected[right].folderID {
			return expected[left].folderID < expected[right].folderID
		}
		return expected[left].uid > expected[right].uid
	})

	type pageEnvelope struct {
		Data struct {
			Hits []struct {
				Reference    string    `json:"reference"`
				FolderID     string    `json:"folder_id"`
				UID          uint32    `json:"uid"`
				InternalDate time.Time `json:"internal_date"`
			} `json:"hits"`
			Count         int    `json:"count"`
			More          bool   `json:"more"`
			Partial       bool   `json:"partial"`
			CandidateUIDs int    `json:"candidate_uids"`
			NextCursor    string `json:"next_cursor"`
		} `json:"data"`
	}
	var got []expectedHit
	seen := make(map[string]struct{})
	cursor := ""
	for pageNumber := 0; pageNumber < 20; pageNumber++ {
		beforeSearch := len(remote.searchRequests)
		beforeSummary := len(remote.summaryRequests)
		args := []string{"--home", home, "mail", "search", "multiFolderRemoteSearch", "--remote", "--all-folders", "--limit", "3", "--json"}
		if cursor != "" {
			args = append(args, "--cursor", cursor)
		}
		code, stdout, stderr := runWithAccountCommands(t, commands, "", args...)
		if code != 0 || stderr != "" {
			t.Fatalf("page %d code=%d stdout=%q stderr=%q", pageNumber, code, stdout, stderr)
		}
		var page pageEnvelope
		if err := json.Unmarshal([]byte(stdout), &page); err != nil {
			t.Fatalf("page %d json: %v raw=%q", pageNumber, err, stdout)
		}
		searchCalls := len(remote.searchRequests) - beforeSearch
		summaryCalls := len(remote.summaryRequests) - beforeSummary
		if searchCalls+summaryCalls > maxRemoteProviderCallsPerInvocation || page.Data.CandidateUIDs > maxRemoteProviderCallsPerInvocation*2048 {
			t.Fatalf("page %d exceeded bounded provider work: searches=%d summaries=%d candidates=%d", pageNumber, searchCalls, summaryCalls, page.Data.CandidateUIDs)
		}
		if pageNumber == 0 {
			if page.Data.Count != 0 || !page.Data.Partial || page.Data.NextCursor == "" || searchCalls != maxRemoteProviderCallsPerInvocation {
				t.Fatalf("first inventory page=%+v search_calls=%d", page.Data, searchCalls)
			}
			decoded, err := maildomain.DecodeRemoteSearchCursor(page.Data.NextCursor)
			if err != nil || decoded.Version != 2 || !decoded.AllFolders || decoded.FolderCount != 12 || len(decoded.Folders) != maxRemoteProviderCallsPerInvocation || decoded.InventoryComplete || decoded.FolderAfterID == "" {
				t.Fatalf("first multi-folder cursor=%+v err=%v", decoded, err)
			}
		}
		for _, hit := range page.Data.Hits {
			if _, duplicate := seen[hit.Reference]; duplicate {
				t.Fatalf("duplicate remote reference on page %d: %q", pageNumber, hit.Reference)
			}
			seen[hit.Reference] = struct{}{}
			got = append(got, expectedHit{folderID: hit.FolderID, uid: hit.UID, date: hit.InternalDate})
		}
		cursor = page.Data.NextCursor
		if cursor == "" {
			if page.Data.More || page.Data.Partial {
				t.Fatalf("terminal page still reports continuation state: %+v", page.Data)
			}
			break
		}
		if !page.Data.More && !page.Data.Partial {
			t.Fatalf("page %d has continuation without more/partial: %+v", pageNumber, page.Data)
		}
	}
	if cursor != "" {
		t.Fatal("all-folder traversal did not terminate within 20 bounded pages")
	}
	if len(got) != len(expected) {
		t.Fatalf("got %d hits, want %d: %+v", len(got), len(expected), got)
	}
	for index := range expected {
		if got[index].folderID != expected[index].folderID || got[index].uid != expected[index].uid || !got[index].date.Equal(expected[index].date) {
			t.Fatalf("global order[%d]=%+v want %+v", index, got[index], expected[index])
		}
	}
}

func TestAllFolderCursorRejectsFolderSetAndUIDValidityDrift(t *testing.T) {
	for _, drift := range []string{"membership", "uidvalidity"} {
		t.Run(drift, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("ICLOUD_MULTI_FOLDER_REMOTE_SEARCH_DRIFT_PASSWORD", "synthetic-multiFolderRemoteSearch-drift-secret")
			commands, _ := newTestAccountCommands()
			folders := multiFolderRemoteSearchRemoteFolders(12, 100)
			folderClient := &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
				"owner@icloud.com": {{Folders: folders}},
			}}
			commands.folders = folderClient
			remote := &fakeRemoteClient{uidValidity: 100}
			remote.searchMessages = []transport.RemoteMessageSummary{multiFolderRemoteSearchRemoteSummary(9, time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC), "drift", 1)}
			commands.remote = remote
			addAndDiscoverInbox(t, commands, home, "ICLOUD_MULTI_FOLDER_REMOTE_SEARCH_DRIFT_PASSWORD")

			code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "multiFolderRemoteSearch drift", "--remote", "--all-folders", "--limit", "1", "--json")
			if code != 0 || stderr != "" {
				t.Fatalf("initial cursor code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			var first struct {
				Data struct {
					NextCursor string `json:"next_cursor"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(stdout), &first); err != nil || first.Data.NextCursor == "" {
				t.Fatalf("initial cursor parse err=%v raw=%q", err, stdout)
			}

			changed := append([]transport.RemoteFolder(nil), folders...)
			if drift == "membership" {
				changed = changed[:len(changed)-1]
			} else {
				copy := *changed[0].Status
				copy.UIDValidity = 101
				changed[0].Status = &copy
			}
			folderClient.results["owner@icloud.com"] = []transport.FolderDiscoveryResult{{Folders: changed}}
			code, _, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "status")
			if code != 0 || stderr != "" {
				t.Fatalf("folder drift refresh code=%d stderr=%q", code, stderr)
			}
			providerCalls := len(remote.searchRequests)
			code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "multiFolderRemoteSearch drift", "--remote", "--all-folders", "--limit", "1", "--cursor", first.Data.NextCursor, "--json")
			if code != cli.ExitConflict || stdout != "" || len(remote.searchRequests) != providerCalls {
				t.Fatalf("%s drift code=%d stdout=%q stderr=%q provider_calls=%d->%d", drift, code, stdout, stderr, providerCalls, len(remote.searchRequests))
			}
		})
	}
}

func multiFolderRemoteSearchRemoteFolders(count int, uidValidity uint32) []transport.RemoteFolder {
	values := make([]transport.RemoteFolder, 0, count)
	for index := 0; index < count; index++ {
		name := fmt.Sprintf("MultiFolderRemoteSearch-%02d", index)
		kind := "other"
		kindSource := "none"
		if index == 0 {
			name = "INBOX"
			kind = "inbox"
			kindSource = "inbox"
		}
		values = append(values, transport.RemoteFolder{
			Name: name, WireName: name, Kind: kind, KindSource: kindSource, Selectable: true, Subscription: "subscribed",
			Status: &transport.MailboxStatus{Mailbox: name, Messages: 20, UIDNext: uint32(1000 + index), UIDValidity: uidValidity},
		})
	}
	return values
}

func multiFolderRemoteSearchFolderIDsByName(t *testing.T, home string) map[string]string {
	t.Helper()
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	values, more, _, err := state.Folders().ListSelectablePage(context.Background(), accountValue.ID, 100, "")
	if err != nil || more {
		t.Fatalf("selectable folders more=%v err=%v", more, err)
	}
	result := make(map[string]string, len(values))
	for _, value := range values {
		result[value.Name] = value.ID
	}
	return result
}

func multiFolderRemoteSearchRemoteSummary(uid uint32, date time.Time, mailbox string, index int) transport.RemoteMessageSummary {
	return transport.RemoteMessageSummary{
		UID: uid, InternalDate: date, RFC822Size: 200,
		Snippet: fmt.Sprintf("multiFolderRemoteSearch-%d", index),
		Envelope: transport.MessageEnvelope{
			Subject:   fmt.Sprintf("Multi-folder remote search %s %d", mailbox, index),
			MessageID: fmt.Sprintf("multiFolderRemoteSearch-%s-%d@example.test", mailbox, index),
			From:      []transport.EnvelopeAddress{{Email: "sender@example.test"}},
			To:        []transport.EnvelopeAddress{{Email: "owner@icloud.com"}},
		},
	}
}
