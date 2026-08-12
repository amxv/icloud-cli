package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
)

func TestCanonicalReadAliasesShareBehavior(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_CANONICAL_COMMAND_PASSWORD", "synthetic-canonicalCommand-secret")
	commands, _ := newTestAccountCommands()
	discovery := transport.FolderDiscoveryResult{Folders: []transport.RemoteFolder{{
		Name: "INBOX", WireName: "INBOX", Kind: "inbox", KindSource: "inbox",
		Selectable: true, Subscription: "subscribed",
		Status: &transport.MailboxStatus{Mailbox: "INBOX", Messages: 2, Unseen: 2, UIDNext: 103, UIDValidity: 100},
	}, {
		Name: "Projects", WireName: "Projects", Kind: "other", KindSource: "name",
		Selectable: true, Subscription: "subscribed",
		Status: &transport.MailboxStatus{Mailbox: "Projects", Messages: 1, Unseen: 0, UIDNext: 2, UIDValidity: 200},
	}}}
	commands.folders = &fakeFolderClient{results: map[string][]transport.FolderDiscoveryResult{
		"owner@icloud.com": {discovery, discovery},
	}}
	code, stdout, stderr := runWithAccountCommands(t, commands, "",
		"--home", home, "account", "add", "--name", "Canonical commands", "--email", "owner@icloud.com", "--credential-env", "ICLOUD_CANONICAL_COMMAND_PASSWORD")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "account-add") {
		t.Fatalf("add account code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, legacyFolders, legacyFolderErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "status", "--limit", "1")
	if code != 0 || legacyFolderErr != "" {
		t.Fatalf("legacy folder refresh code=%d stdout=%q stderr=%q", code, legacyFolders, legacyFolderErr)
	}
	code, canonicalFolders, canonicalFolderErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "refresh", "--limit", "1")
	if code != 0 || canonicalFolderErr != "" || canonicalFolders != legacyFolders {
		t.Fatalf("folder alias mismatch code=%d legacy=%q canonical=%q stderr=%q", code, legacyFolders, canonicalFolders, canonicalFolderErr)
	}
	folderCursor := canonicalCommandCompactField(canonicalFolders, "next_cursor")
	if !strings.HasPrefix(folderCursor, "fcur_") {
		t.Fatalf("folder cursor=%q output=%q", folderCursor, canonicalFolders)
	}
	code, legacyFoldersPage2, legacyFoldersPage2Err := runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "status", "--limit", "1", "--cursor", folderCursor)
	if code != 0 || legacyFoldersPage2Err != "" {
		t.Fatalf("legacy folder page 2 code=%d stdout=%q stderr=%q", code, legacyFoldersPage2, legacyFoldersPage2Err)
	}
	code, canonicalFoldersPage2, canonicalFoldersPage2Err := runWithAccountCommands(t, commands, "", "--home", home, "mail", "folders", "refresh", "--limit", "1", "--cursor", folderCursor)
	if code != 0 || canonicalFoldersPage2Err != "" || canonicalFoldersPage2 != legacyFoldersPage2 {
		t.Fatalf("folder page 2 mismatch code=%d legacy=%q canonical=%q stderr=%q", code, legacyFoldersPage2, canonicalFoldersPage2, canonicalFoldersPage2Err)
	}

	firstRaw := syntheticMailFixture(101, "canonicalCommand-first@example.test", "Canonical commands first", "First canonical commands body", "")
	secondRaw := syntheticMailFixture(102, "canonicalCommand-second@example.test", "Canonical commands second", "Second canonical commands body", "")
	commands.mail = &fakeInboxClient{results: []fakeInboxPageResult{{
		page: transport.InboxPage{
			Mailbox: "INBOX", UIDValidity: 100, LastCommittedUID: 102, LastScannedUID: 102, Bootstrap: true,
			Messages: []transport.FetchedMessage{
				{UID: 101, InternalDate: time.Date(2026, 8, 10, 1, 1, 0, 0, time.UTC)},
				{UID: 102, InternalDate: time.Date(2026, 8, 10, 1, 2, 0, 0, time.UTC)},
			},
		},
		raw: map[uint32]string{101: firstRaw, 102: secondRaw},
	}}}
	code, stdout, stderr = runWithAccountCommands(t, commands, "", "--home", home, "mail", "sync")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "fetched=2") {
		t.Fatalf("sync code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, legacyMessages, legacyMessagesErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "inbox", "--limit", "1")
	if code != 0 || legacyMessagesErr != "" {
		t.Fatalf("legacy messages code=%d stdout=%q stderr=%q", code, legacyMessages, legacyMessagesErr)
	}
	code, canonicalMessages, canonicalMessagesErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "messages", "list", "--limit", "1")
	if code != 0 || canonicalMessagesErr != "" || canonicalMessages != legacyMessages {
		t.Fatalf("message alias mismatch code=%d legacy=%q canonical=%q stderr=%q", code, legacyMessages, canonicalMessages, canonicalMessagesErr)
	}
	messageCursor := canonicalCommandCompactField(canonicalMessages, "next_cursor")
	if !strings.HasPrefix(messageCursor, "cur_") {
		t.Fatalf("message cursor=%q output=%q", messageCursor, canonicalMessages)
	}
	code, legacyMessagesPage2, legacyMessagesPage2Err := runWithAccountCommands(t, commands, "", "--home", home, "mail", "inbox", "--limit", "1", "--cursor", messageCursor)
	if code != 0 || legacyMessagesPage2Err != "" {
		t.Fatalf("legacy message page 2 code=%d stdout=%q stderr=%q", code, legacyMessagesPage2, legacyMessagesPage2Err)
	}
	code, canonicalMessagesPage2, canonicalMessagesPage2Err := runWithAccountCommands(t, commands, "", "--home", home, "mail", "messages", "list", "--limit", "1", "--cursor", messageCursor)
	if code != 0 || canonicalMessagesPage2Err != "" || canonicalMessagesPage2 != legacyMessagesPage2 {
		t.Fatalf("message page 2 mismatch code=%d legacy=%q canonical=%q stderr=%q", code, legacyMessagesPage2, canonicalMessagesPage2, canonicalMessagesPage2Err)
	}
	for _, args := range [][]string{
		{"mail", "inbox", "--limit", "1", "--cursor", messageCursor, "--query", "changed"},
		{"mail", "messages", "list", "--limit", "1", "--cursor", messageCursor, "--query", "changed"},
	} {
		code, _, stderr = runWithAccountCommands(t, commands, "", append([]string{"--home", home}, args...)...)
		if code != cli.ExitUsage || !strings.Contains(stderr, "belongs to another query") {
			t.Fatalf("message cursor binding args=%v code=%d stderr=%q", args, code, stderr)
		}
	}

	code, legacyThreads, legacyThreadsErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "latest", "--limit", "1")
	if code != 0 || legacyThreadsErr != "" {
		t.Fatalf("legacy threads code=%d stdout=%q stderr=%q", code, legacyThreads, legacyThreadsErr)
	}
	code, canonicalThreads, canonicalThreadsErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "threads", "list", "--limit", "1")
	if code != 0 || canonicalThreadsErr != "" || canonicalThreads != legacyThreads {
		t.Fatalf("thread alias mismatch code=%d legacy=%q canonical=%q stderr=%q", code, legacyThreads, canonicalThreads, canonicalThreadsErr)
	}
	threadCursor := canonicalCommandCompactField(canonicalThreads, "next_cursor")
	if !strings.HasPrefix(threadCursor, "cur_") {
		t.Fatalf("thread cursor=%q output=%q", threadCursor, canonicalThreads)
	}
	code, legacyThreadsPage2, legacyThreadsPage2Err := runWithAccountCommands(t, commands, "", "--home", home, "mail", "latest", "--limit", "1", "--cursor", threadCursor)
	if code != 0 || legacyThreadsPage2Err != "" {
		t.Fatalf("legacy thread page 2 code=%d stdout=%q stderr=%q", code, legacyThreadsPage2, legacyThreadsPage2Err)
	}
	code, canonicalThreadsPage2, canonicalThreadsPage2Err := runWithAccountCommands(t, commands, "", "--home", home, "mail", "threads", "list", "--limit", "1", "--cursor", threadCursor)
	if code != 0 || canonicalThreadsPage2Err != "" || canonicalThreadsPage2 != legacyThreadsPage2 {
		t.Fatalf("thread page 2 mismatch code=%d legacy=%q canonical=%q stderr=%q", code, legacyThreadsPage2, canonicalThreadsPage2, canonicalThreadsPage2Err)
	}
	for _, args := range [][]string{
		{"mail", "latest", "--limit", "1", "--cursor", threadCursor, "--query", "changed"},
		{"mail", "threads", "list", "--limit", "1", "--cursor", threadCursor, "--query", "changed"},
	} {
		code, _, stderr = runWithAccountCommands(t, commands, "", append([]string{"--home", home}, args...)...)
		if code != cli.ExitUsage || !strings.Contains(stderr, "belongs to another query") {
			t.Fatalf("thread cursor binding args=%v code=%d stderr=%q", args, code, stderr)
		}
	}

	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	threads, _, err := state.Threads().Latest(context.Background(), accountValue.ID, 20)
	if err != nil || len(threads) != 2 {
		state.Close()
		t.Fatalf("threads=%+v err=%v", threads, err)
	}
	threadID := threads[0].ID
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	code, legacyThreadShow, legacyThreadShowErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "thread", "show", threadID, "--metadata-only")
	if code != 0 || legacyThreadShowErr != "" {
		t.Fatalf("legacy thread show code=%d stdout=%q stderr=%q", code, legacyThreadShow, legacyThreadShowErr)
	}
	code, canonicalThreadShow, canonicalThreadShowErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "threads", "show", threadID, "--metadata-only")
	if code != 0 || canonicalThreadShowErr != "" || canonicalThreadShow != legacyThreadShow {
		t.Fatalf("thread show alias mismatch code=%d legacy=%q canonical=%q stderr=%q", code, legacyThreadShow, canonicalThreadShow, canonicalThreadShowErr)
	}
}

func TestOperationNavigationIsPagedExactAndPrivate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ICLOUD_SEND_PASSWORD", "synthetic-canonicalCommand-operation-secret")
	commands, _ := newTestAccountCommands()
	sender := &fakeSMTPSender{}
	commands.sender = sender
	addSendAccount(t, commands, home)
	for index, values := range []struct {
		subject string
		body    string
		key     string
	}{
		{subject: "Canonical commands private first", body: "canonicalCommand-private-first-body", key: "canonicalCommand-private-first-key"},
		{subject: "Canonical commands private second", body: "canonicalCommand-private-second-body", key: "canonicalCommand-private-second-key"},
	} {
		code, stdout, stderr := runWithAccountCommands(t, commands, "",
			"--home", home, "mail", "send", "--to", "private-recipient@example.com", "--subject", values.subject,
			"--body", values.body, "--idempotency-key", values.key, "--yes")
		if code != 0 || stderr != "" || sender.calls != index+1 {
			t.Fatalf("send %d code=%d stdout=%q stderr=%q calls=%d", index, code, stdout, stderr, sender.calls)
		}
	}

	code, legacyOutbound, legacyOutboundErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "status", "--outbound", "--limit", "1")
	if code != 0 || legacyOutboundErr != "" {
		t.Fatalf("legacy outbound list code=%d stdout=%q stderr=%q", code, legacyOutbound, legacyOutboundErr)
	}
	code, canonicalOutbound, canonicalOutboundErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "operations", "outbound", "list", "--limit", "1")
	if code != 0 || canonicalOutboundErr != "" || canonicalOutbound != legacyOutbound {
		t.Fatalf("outbound list mismatch code=%d legacy=%q canonical=%q stderr=%q", code, legacyOutbound, canonicalOutbound, canonicalOutboundErr)
	}
	outboundCursor := canonicalCommandCompactField(canonicalOutbound, "next_cursor")
	if !strings.HasPrefix(outboundCursor, "ocur_") {
		t.Fatalf("outbound cursor=%q output=%q", outboundCursor, canonicalOutbound)
	}
	code, olderOutboundPage, olderOutboundErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "operations", "outbound", "list", "--limit", "1", "--cursor", outboundCursor)
	if code != 0 || olderOutboundErr != "" || strings.Contains(olderOutboundPage, "more=true") {
		t.Fatalf("older outbound page code=%d stdout=%q stderr=%q", code, olderOutboundPage, olderOutboundErr)
	}
	olderAttemptID := canonicalCommandRecordID(olderOutboundPage, "outbound")
	if !strings.HasPrefix(olderAttemptID, "send_") {
		t.Fatalf("older outbound id=%q output=%q", olderAttemptID, olderOutboundPage)
	}
	code, outboundShow, outboundShowErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "operations", "outbound", "show", olderAttemptID)
	if code != 0 || outboundShowErr != "" || !strings.Contains(outboundShow, olderAttemptID) {
		t.Fatalf("outbound show code=%d stdout=%q stderr=%q", code, outboundShow, outboundShowErr)
	}
	for _, private := range []string{"private-recipient@example.com", "Canonical commands private first", "canonicalCommand-private-first-body", "canonicalCommand-private-first-key", "synthetic-canonicalCommand-operation-secret"} {
		if strings.Contains(outboundShow, private) {
			t.Fatalf("outbound show exposed %q: %q", private, outboundShow)
		}
	}
	code, outboundShowJSON, outboundShowJSONErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "operations", "outbound", "show", olderAttemptID, "--json")
	if code != 0 || outboundShowJSONErr != "" || !strings.Contains(outboundShowJSON, `"id":"`+olderAttemptID+`"`) {
		t.Fatalf("outbound show JSON code=%d stdout=%q stderr=%q", code, outboundShowJSON, outboundShowJSONErr)
	}
	for _, private := range []string{"private-recipient@example.com", "Canonical commands private first", "canonicalCommand-private-first-body", "canonicalCommand-private-first-key", "request_hash", "raw_storage_key", "raw_content_hash"} {
		if strings.Contains(outboundShowJSON, private) {
			t.Fatalf("outbound show JSON exposed %q: %q", private, outboundShowJSON)
		}
	}
	seedSecondAccount(t, home)
	code, _, crossAccountErr := runWithAccountCommands(t, commands, "", "--home", home, "--account", "Work", "mail", "operations", "outbound", "show", olderAttemptID)
	if code != cli.ExitNotFound || !strings.Contains(crossAccountErr, "not found") {
		t.Fatalf("cross-account outbound show code=%d stderr=%q", code, crossAccountErr)
	}

	fixture := seedLocationMutationFixture(t, home, []locationSeed{{kind: folder.KindInbox, uid: 1, flag: `\Seen`}})
	state, err := openState(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	accountValue, err := state.Accounts().ResolveSelected(context.Background(), "")
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	source := fixture.locations[folder.KindInbox]
	base := time.Date(2026, 8, 10, 2, 0, 0, 0, time.UTC)
	for index, values := range []struct {
		id   string
		hash string
		key  string
	}{
		{id: "mut_canonicalCommand_older", hash: strings.Repeat("a", 64), key: "canonicalCommand-private-mutation-older"},
		{id: "mut_canonicalCommand_newer", hash: strings.Repeat("b", 64), key: "canonicalCommand-private-mutation-newer"},
	} {
		_, reused, beginErr := state.LocationMutations().Begin(context.Background(), maildomain.BeginLocationMutation{
			MutationID: values.id, AccountID: accountValue.ID, MessageID: fixture.messageID,
			Action: maildomain.LocationActionDelete, SourceLocationID: source.ID, SourceFolderID: source.FolderID,
			RequestHash: values.hash, IdempotencyKey: values.key, Now: base.Add(time.Duration(index) * time.Minute),
		})
		if beginErr != nil || reused {
			state.Close()
			t.Fatalf("begin mutation %d reused=%v err=%v", index, reused, beginErr)
		}
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	code, legacyMutations, legacyMutationsErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "status", "--mutations", "--limit", "1")
	if code != 0 || legacyMutationsErr != "" {
		t.Fatalf("legacy mutation list code=%d stdout=%q stderr=%q", code, legacyMutations, legacyMutationsErr)
	}
	code, canonicalMutations, canonicalMutationsErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "operations", "mutations", "list", "--limit", "1")
	if code != 0 || canonicalMutationsErr != "" || canonicalMutations != legacyMutations {
		t.Fatalf("mutation list mismatch code=%d legacy=%q canonical=%q stderr=%q", code, legacyMutations, canonicalMutations, canonicalMutationsErr)
	}
	mutationCursor := canonicalCommandCompactField(canonicalMutations, "next_cursor")
	if !strings.HasPrefix(mutationCursor, "mcur_") {
		t.Fatalf("mutation cursor=%q output=%q", mutationCursor, canonicalMutations)
	}
	code, olderMutationPage, olderMutationErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "operations", "mutations", "list", "--limit", "1", "--cursor", mutationCursor)
	if code != 0 || olderMutationErr != "" || !strings.Contains(olderMutationPage, "mut_canonicalCommand_older") {
		t.Fatalf("older mutation page code=%d stdout=%q stderr=%q", code, olderMutationPage, olderMutationErr)
	}
	code, mutationShow, mutationShowErr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "operations", "mutations", "show", "mut_canonicalCommand_older", "--json")
	if code != 0 || mutationShowErr != "" || !strings.Contains(mutationShow, `"id":"mut_canonicalCommand_older"`) {
		t.Fatalf("mutation show code=%d stdout=%q stderr=%q", code, mutationShow, mutationShowErr)
	}
	for _, private := range []string{"canonicalCommand-private-mutation-older", strings.Repeat("a", 64), "request_hash", "idempotency_key", "proposed_location_id"} {
		if strings.Contains(mutationShow, private) {
			t.Fatalf("mutation show exposed %q: %q", private, mutationShow)
		}
	}
	code, _, crossAccountMutationErr := runWithAccountCommands(t, commands, "", "--home", home, "--account", "Work", "mail", "operations", "mutations", "show", "mut_canonicalCommand_older")
	if code != cli.ExitNotFound || !strings.Contains(crossAccountMutationErr, "not found") {
		t.Fatalf("cross-account mutation show code=%d stderr=%q", code, crossAccountMutationErr)
	}

	for _, args := range [][]string{
		{"mail", "operations", "outbound", "show", "send_missing"},
		{"mail", "operations", "mutations", "show", "mut_missing"},
	} {
		code, _, stderr := runWithAccountCommands(t, commands, "", append([]string{"--home", home}, args...)...)
		if code != cli.ExitNotFound || !strings.Contains(stderr, "not found") {
			t.Fatalf("missing operation args=%v code=%d stderr=%q", args, code, stderr)
		}
	}
}

func canonicalCommandCompactField(output, key string) string {
	prefix := key + "="
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}

func canonicalCommandRecordID(output, kind string) string {
	lines := strings.Split(output, "\n")
	for index := range lines {
		if lines[index] != kind || index+1 >= len(lines) {
			continue
		}
		fields := strings.Fields(lines[index+1])
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}
