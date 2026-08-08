package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

type fakeRemoteClient struct {
	searchMessages  []transport.RemoteMessageSummary
	searchMore      bool
	searchPartial   bool
	searchRequests  []transport.RemoteSearchRequest
	summaryRequests []transport.RemoteFetchRequest
	fetchRequests   []transport.RemoteFetchRequest
	rawByUID        map[uint32]string
	uidValidity     uint32
}

func (client *fakeRemoteClient) SearchRemote(_ context.Context, _ transport.AuthRequest, request transport.RemoteSearchRequest) (transport.RemoteSearchPage, error) {
	client.searchRequests = append(client.searchRequests, request)
	uidValidity := client.uidValidity
	if uidValidity == 0 {
		uidValidity = 100
	}
	if request.ExpectedUIDValidity != 0 && request.ExpectedUIDValidity != uidValidity {
		return transport.RemoteSearchPage{}, &transport.UIDValidityError{Expected: request.ExpectedUIDValidity, Observed: uidValidity}
	}
	limit := request.Limit
	values := append([]transport.RemoteMessageSummary(nil), client.searchMessages...)
	more := client.searchMore || len(values) > limit
	if len(values) > limit {
		values = values[:limit]
	}
	return transport.RemoteSearchPage{
		Mailbox: request.Mailbox, UIDValidity: uidValidity, Messages: values,
		MoreAvailable: more, Partial: client.searchPartial, CandidateUIDs: 17,
	}, nil
}

func (client *fakeRemoteClient) FetchRemoteSummary(_ context.Context, _ transport.AuthRequest, request transport.RemoteFetchRequest) (transport.RemoteSummaryResult, error) {
	client.summaryRequests = append(client.summaryRequests, request)
	uidValidity := client.uidValidity
	if uidValidity == 0 {
		uidValidity = 100
	}
	if request.ExpectedUIDValidity != uidValidity {
		return transport.RemoteSummaryResult{}, &transport.UIDValidityError{Expected: request.ExpectedUIDValidity, Observed: uidValidity}
	}
	for _, value := range client.searchMessages {
		if value.UID == request.UID {
			return transport.RemoteSummaryResult{Mailbox: request.Mailbox, UIDValidity: uidValidity, Message: value}, nil
		}
	}
	return transport.RemoteSummaryResult{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, false, errors.New("missing remote fixture"))
}

func (client *fakeRemoteClient) FetchRemoteMessage(ctx context.Context, _ transport.AuthRequest, request transport.RemoteFetchRequest, stager blob.Stager) (transport.RemoteFetchResult, error) {
	client.fetchRequests = append(client.fetchRequests, request)
	uidValidity := client.uidValidity
	if uidValidity == 0 {
		uidValidity = 100
	}
	if request.ExpectedUIDValidity != uidValidity {
		return transport.RemoteFetchResult{}, &transport.UIDValidityError{Expected: request.ExpectedUIDValidity, Observed: uidValidity}
	}
	raw := client.rawByUID[request.UID]
	if raw == "" {
		return transport.RemoteFetchResult{}, transport.NewFailure(transport.StageIMAPFetch, transport.FailureUnavailable, false, errors.New("missing remote fixture"))
	}
	staged, err := stager.Stage(ctx, strings.NewReader(raw), int64(len(raw)))
	if err != nil {
		return transport.RemoteFetchResult{}, err
	}
	return transport.RemoteFetchResult{
		Mailbox: request.Mailbox, UIDValidity: uidValidity,
		Message: transport.FetchedMessage{
			UID: request.UID, InternalDate: time.Date(2026, 8, 6, 11, 0, 0, 0, time.UTC),
			RFC822Size: int64(len(raw)), Raw: staged,
		},
	}, nil
}

func TestRemoteSearchLookupHydrationAndCheckpointIsolation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_REMOTE_PASSWORD", "synthetic-remote-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {{Folders: []transport.RemoteFolder{
			{Name: "INBOX", WireName: "INBOX", Kind: "inbox", KindSource: "inbox", Selectable: true, Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: "INBOX", Messages: 1, UIDNext: 402, UIDValidity: 100}},
			{Name: "Archive", WireName: "Archive", Kind: "archive", KindSource: "attribute", Selectable: true, Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: "Archive", Messages: 1, UIDNext: 402, UIDValidity: 100}},
		}}},
	}}
	raw := syntheticMailFixture(401, "remote-401@example.test", "Remote-only invoice", "remote-hydration-token", "hidden-remote-bcc@example.test")
	remoteClient := &fakeRemoteClient{
		uidValidity: 100,
		searchMessages: []transport.RemoteMessageSummary{
			{
				UID: 401, Flags: []string{`\Flagged`}, InternalDate: time.Date(2026, 8, 6, 11, 0, 0, 0, time.UTC),
				RFC822Size: int64(len(raw)), Snippet: "remote-hydration-token bounded provider preview",
				Envelope: transport.MessageEnvelope{
					Subject: "Remote-only invoice", MessageID: "remote-401@example.test",
					From: []transport.EnvelopeAddress{{Name: "Remote Sender", Email: "remote@example.test"}},
					To:   []transport.EnvelopeAddress{{Email: "owner@icloud.com"}},
					CC:   []transport.EnvelopeAddress{{Email: "visible-cc@example.test"}},
					BCC:  []transport.EnvelopeAddress{{Email: "hidden-remote-bcc@example.test"}},
				},
			},
		},
		rawByUID: map[uint32]string{401: raw},
	}
	commands.remote = remoteClient
	addAndDiscoverInbox(t, commands, home, "ICLOUD_REMOTE_PASSWORD")

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "invoice", "--remote", "--limit", "100", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("remote search code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if len(remoteClient.searchRequests) != 1 || remoteClient.searchRequests[0].Limit != 100 || remoteClient.searchRequests[0].Mailbox != "INBOX" || remoteClient.searchRequests[0].ExpectedUIDValidity != 100 {
		t.Fatalf("remote search request = %+v", remoteClient.searchRequests)
	}
	var searchEnvelope struct {
		Data struct {
			Hits []struct {
				Reference       string `json:"reference"`
				CachedMessageID string `json:"cached_message_id"`
				UID             uint32 `json:"uid"`
			} `json:"hits"`
			Count         int  `json:"count"`
			More          bool `json:"more"`
			Partial       bool `json:"partial"`
			CandidateUIDs int  `json:"candidate_uids"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &searchEnvelope); err != nil || len(searchEnvelope.Data.Hits) != 1 || searchEnvelope.Data.Hits[0].UID != 401 || searchEnvelope.Data.Hits[0].Reference == "" || searchEnvelope.Data.Hits[0].CachedMessageID != "" || searchEnvelope.Data.CandidateUIDs != 17 {
		t.Fatalf("remote search parse err=%v envelope=%+v", err, searchEnvelope)
	}
	for _, forbidden := range []string{"hidden-remote-bcc", "synthetic-remote-secret", `"bcc"`, "raw_storage_key", "raw_content_hash"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("remote search exposed %q: %s", forbidden, stdout)
		}
	}
	reference := searchEnvelope.Data.Hits[0].Reference
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "invoice", "--remote", "--all-folders", "--limit", "2")
	if code != 0 || stderr != "" || compactBlockCount(stdout, "remote-hit") != 2 || !compactContainsAll(stdout, "folders=2", "count=2") || len(remoteClient.searchRequests) != 3 {
		t.Fatalf("all-folder remote search code=%d stdout=%q stderr=%q requests=%+v", code, stdout, stderr, remoteClient.searchRequests)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", reference, "--remote")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "remote-message\n", "uid=401", "subject=\"Remote-only invoice\"") || strings.Contains(stdout, "hidden-remote-bcc") || strings.Contains(stdout, "cached_message=") {
		t.Fatalf("remote summary code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Count(stdout, "\n") > 16 || len(stdout) > 1200 {
		t.Fatalf("remote summary compact budget lines=%d bytes=%d output=%q", strings.Count(stdout, "\n"), len(stdout), stdout)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", reference, "--remote", "--full")
	if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, "requires --cache") {
		t.Fatalf("remote expansion gate code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "--remote", "--attachment-name", "invoice.pdf")
	if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, "cannot filter attachment metadata") || len(remoteClient.searchRequests) != 3 {
		t.Fatalf("unsupported remote filter code=%d stdout=%q stderr=%q requests=%d", code, stdout, stderr, len(remoteClient.searchRequests))
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "invoice", "--remote", "--result", "threads")
	if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, "returns messages only") || len(remoteClient.searchRequests) != 3 {
		t.Fatalf("unsupported remote threads code=%d stdout=%q stderr=%q requests=%d", code, stdout, stderr, len(remoteClient.searchRequests))
	}

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "status")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "committed=0", "scanned=0", "bootstrap=false") {
		t.Fatalf("pre-hydrate checkpoint code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--hydrate", reference, "--json")
	if code != 0 || stderr != "" || len(remoteClient.fetchRequests) != 1 || !strings.Contains(stdout, `"created":true`) || strings.Contains(stdout, reference) {
		t.Fatalf("hydrate code=%d stdout=%q stderr=%q fetches=%d", code, stdout, stderr, len(remoteClient.fetchRequests))
	}
	var hydrateEnvelope struct {
		Data struct {
			Outcomes []struct {
				MessageID  string `json:"message_id"`
				LocationID string `json:"location_id"`
				Created    bool   `json:"created"`
			} `json:"outcomes"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &hydrateEnvelope); err != nil || len(hydrateEnvelope.Data.Outcomes) != 1 || hydrateEnvelope.Data.Outcomes[0].MessageID == "" || hydrateEnvelope.Data.Outcomes[0].LocationID == "" || !hydrateEnvelope.Data.Outcomes[0].Created {
		t.Fatalf("hydrate parse err=%v envelope=%+v", err, hydrateEnvelope)
	}
	messageID := hydrateEnvelope.Data.Outcomes[0].MessageID

	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "search", "remote-hydration-token", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, messageID) || !strings.Contains(stdout, `"index_state":"complete"`) {
		t.Fatalf("local search after hydration code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", reference, "--remote", "--cache", "--full")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "remote-hydration-token") || len(remoteClient.fetchRequests) != 1 {
		t.Fatalf("cached remote full read code=%d stdout=%q stderr=%q fetches=%d", code, stdout, stderr, len(remoteClient.fetchRequests))
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "status")
	if code != 0 || stderr != "" || !compactContainsAll(stdout, "committed=0", "scanned=0", "bootstrap=false") {
		t.Fatalf("post-hydrate checkpoint code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync", "--hydrate", reference)
	if code != 0 || stderr != "" || len(remoteClient.fetchRequests) != 1 || !compactContainsAll(stdout, "status=cached", "count=1") {
		t.Fatalf("duplicate hydrate code=%d stdout=%q stderr=%q fetches=%d", code, stdout, stderr, len(remoteClient.fetchRequests))
	}

	stale, err := maildomain.EncodeRemoteReference(maildomain.RemoteReference{
		AccountID: searchAccountID(t, home), FolderID: searchFolderID(t, home), UIDValidity: 999, UID: 401,
	})
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "message", "show", stale, "--remote")
	if code != cli.ExitConflict || stdout != "" || !strings.Contains(stderr, "folder identity changed") || len(remoteClient.summaryRequests) != 1 {
		t.Fatalf("stale reference code=%d stdout=%q stderr=%q summaries=%d", code, stdout, stderr, len(remoteClient.summaryRequests))
	}
}

func TestMailHydrateReportsCommittedPartialResultsAndJSONLItemFailures(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_REMOTE_PASSWORD", "synthetic-remote-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {{Folders: []transport.RemoteFolder{{
			Name: "INBOX", WireName: "INBOX", Kind: "inbox", KindSource: "inbox", Selectable: true,
			Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: "INBOX", Messages: 2, UIDNext: 503, UIDValidity: 100},
		}}}},
	}}
	raw := syntheticMailFixture(501, "hydrate-501@example.test", "Hydrate first", "hydrate-partial-first-token", "")
	remoteClient := &fakeRemoteClient{uidValidity: 100, rawByUID: map[uint32]string{501: raw}}
	commands.remote = remoteClient
	addAndDiscoverInbox(t, commands, home, "ICLOUD_REMOTE_PASSWORD")
	accountID := searchAccountID(t, home)
	folderID := searchFolderID(t, home)
	firstRef, err := maildomain.EncodeRemoteReference(maildomain.RemoteReference{AccountID: accountID, FolderID: folderID, UIDValidity: 100, UID: 501})
	if err != nil {
		t.Fatal(err)
	}
	secondRef, err := maildomain.EncodeRemoteReference(maildomain.RemoteReference{AccountID: accountID, FolderID: folderID, UIDValidity: 100, UID: 502})
	if err != nil {
		t.Fatal(err)
	}

	accountValue, outcomes, _, hydrateErr := commands.MailHydrate(context.Background(), home, "", []string{firstRef, secondRef})
	if hydrateErr == nil || accountValue.ID != accountID || len(outcomes) != 1 || outcomes[0].MessageID == "" {
		t.Fatalf("partial hydrate account=%+v outcomes=%+v err=%v", accountValue, outcomes, hydrateErr)
	}
	normalized := cli.NormalizeError(hydrateErr)
	if normalized.Details["partial"] != true || normalized.Details["completed_count"] != 1 || normalized.Details["failed_reference"] != secondRef {
		t.Fatalf("partial hydrate details=%+v", normalized.Details)
	}
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Mail().Message(context.Background(), accountID, outcomes[0].MessageID); err != nil {
		state.Close()
		t.Fatalf("committed hydration result was hidden or rolled back: %v", err)
	}
	state.Close()

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync",
		"--hydrate", firstRef+","+secondRef, "--continue-on-error", "--jsonl")
	if code == 0 {
		t.Fatalf("partial JSONL hydrate unexpectedly succeeded stdout=%q stderr=%q", stdout, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], `"event":"item"`) || !strings.Contains(lines[0], `"status":"cached"`) ||
		!strings.Contains(lines[1], `"event":"item"`) || !strings.Contains(lines[1], `"status":"error"`) || !strings.Contains(lines[1], secondRef) ||
		!strings.Contains(lines[2], `"event":"completed"`) || !strings.Contains(lines[2], `"failed":1`) || !strings.Contains(lines[2], `"partial":true`) {
		t.Fatalf("partial JSONL hydrate stdout=%q", stdout)
	}
	if stderr != "" {
		t.Fatalf("partial JSONL hydrate emitted contradictory terminal stderr=%q", stderr)
	}
}

func TestMailHydrateJSONLPreservesOriginalOrderWhenFirstItemFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_REMOTE_PASSWORD", "synthetic-remote-secret")
	commands, _ := newTestAccountCommands()
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {{Folders: []transport.RemoteFolder{{
			Name: "INBOX", WireName: "INBOX", Kind: "inbox", KindSource: "inbox", Selectable: true,
			Subscription: "subscribed", Status: &transport.MailboxStatus{Mailbox: "INBOX", Messages: 2, UIDNext: 503, UIDValidity: 100},
		}}}},
	}}
	raw := syntheticMailFixture(502, "hydrate-502@example.test", "Hydrate second", "hydrate-second-token", "")
	commands.remote = &fakeRemoteClient{uidValidity: 100, rawByUID: map[uint32]string{502: raw}}
	addAndDiscoverInbox(t, commands, home, "ICLOUD_REMOTE_PASSWORD")
	accountID := searchAccountID(t, home)
	folderID := searchFolderID(t, home)
	firstRef, err := maildomain.EncodeRemoteReference(maildomain.RemoteReference{AccountID: accountID, FolderID: folderID, UIDValidity: 100, UID: 501})
	if err != nil {
		t.Fatal(err)
	}
	secondRef, err := maildomain.EncodeRemoteReference(maildomain.RemoteReference{AccountID: accountID, FolderID: folderID, UIDValidity: 100, UID: 502})
	if err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync",
		"--hydrate", firstRef+","+secondRef, "--continue-on-error", "--jsonl")
	if code == 0 || stderr != "" {
		t.Fatalf("ordered hydrate exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 ||
		!strings.Contains(lines[0], `"index":0`) || !strings.Contains(lines[0], `"status":"error"`) || !strings.Contains(lines[0], firstRef) ||
		!strings.Contains(lines[1], `"index":1`) || !strings.Contains(lines[1], secondRef) || !strings.Contains(lines[1], `"committed_state":"committed"`) ||
		!strings.Contains(lines[2], `"attempted":2`) || !strings.Contains(lines[2], `"completed":1`) || !strings.Contains(lines[2], `"failed":1`) {
		t.Fatalf("ordered hydrate JSONL=%q", stdout)
	}
}

func searchAccountID(t *testing.T, home string) string {
	t.Helper()
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	value, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	return value.ID
}

func searchFolderID(t *testing.T, home string) string {
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
	value, err := state.Folders().Resolve(context.Background(), accountValue.ID, "inbox")
	if err != nil {
		t.Fatal(err)
	}
	return value.ID
}
