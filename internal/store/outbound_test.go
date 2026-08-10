package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestOutboundAttemptIdempotencyAndUnknownOutcome(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	input := maildomain.BeginOutboundAttempt{
		AttemptID:         "send_0198d704-6c00-7000-8000-000000000001",
		AccountID:         accountID,
		SenderIdentityID:  primaryMailIdentityID(accountID),
		ProposedMessageID: "msg_0198d704-6c00-7000-8000-000000000601",
		RequestHash:       strings.Repeat("a", 64), IdempotencyKey: "send-fixture-01",
		RFCMessageID:   "outbound@example.test",
		RawContentHash: strings.Repeat("b", 64),
		RawStorageKey:  "raw/sha256/bb/" + strings.Repeat("b", 64) + ".eml",
		RawByteSize:    123, ToCount: 1, CCCount: 1, BCCCount: 1,
		BodyPartCount: 2, Now: now,
	}
	attempt, existing, err := state.Outbound().Begin(context.Background(), input)
	if err != nil || existing || attempt.Status != maildomain.OutboundPending || attempt.ID != input.AttemptID {
		t.Fatalf("begin attempt=%+v existing=%v err=%v", attempt, existing, err)
	}
	retry := input
	retry.AttemptID = "send_0198d704-6c00-7000-8000-000000000002"
	retry.ProposedMessageID = "msg_0198d704-6c00-7000-8000-000000000602"
	retry.RawContentHash = strings.Repeat("c", 64)
	retry.RawStorageKey = "raw/sha256/cc/" + strings.Repeat("c", 64) + ".eml"
	reused, existing, err := state.Outbound().Begin(context.Background(), retry)
	if err != nil || !existing || reused.ID != input.AttemptID {
		t.Fatalf("reuse attempt=%+v existing=%v err=%v", reused, existing, err)
	}
	conflict := retry
	conflict.RequestHash = strings.Repeat("d", 64)
	if _, _, err := state.Outbound().Begin(context.Background(), conflict); !errors.Is(err, maildomain.ErrIdempotencyConflict) {
		t.Fatalf("conflicting begin error = %v", err)
	}
	resolved, found, err := state.Outbound().ResolveIdempotency(context.Background(), accountID, input.IdempotencyKey, input.RequestHash)
	if err != nil || !found || resolved.ID != input.AttemptID {
		t.Fatalf("resolve attempt=%+v found=%v err=%v", resolved, found, err)
	}
	unknown, err := state.Outbound().MarkOutcome(context.Background(), attempt.ID, maildomain.OutboundUnknown, "smtp_accept", now.Add(time.Minute))
	if err != nil || unknown.Status != maildomain.OutboundUnknown || unknown.Reconciliation != maildomain.ReconciliationUnknown || unknown.ErrorCode != "smtp_accept" {
		t.Fatalf("unknown attempt=%+v err=%v", unknown, err)
	}
	resolved, found, err = state.Outbound().ResolveIdempotency(context.Background(), accountID, input.IdempotencyKey, input.RequestHash)
	if err != nil || !found || resolved.Status != maildomain.OutboundUnknown {
		t.Fatalf("resolved unknown=%+v found=%v err=%v", resolved, found, err)
	}
}

func TestOutboundAcceptedPersistsLocationlessSearchableMessageAtomically(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 14, 15, 0, 0, time.UTC)
	request, content, raw := outboundStoreFixture(t, accountID, now)
	hash := strings.Repeat("e", 64)
	key := "raw/sha256/ee/" + hash + ".eml"
	attempt, _, err := state.Outbound().Begin(context.Background(), maildomain.BeginOutboundAttempt{
		AttemptID: "send_0198d704-6c00-7000-8000-000000000010",
		AccountID: accountID, ProposedMessageID: "msg_0198d704-6c00-7000-8000-000000000610",
		SenderIdentityID: primaryMailIdentityID(accountID),
		RequestHash:      strings.Repeat("f", 64), IdempotencyKey: "accepted-send-01",
		RFCMessageID:   content.Preview.RFCMessageID,
		RawContentHash: hash, RawStorageKey: key, RawByteSize: int64(len(raw)),
		ToCount: len(request.To), CCCount: len(request.CC), BCCCount: len(request.BCC),
		BodyPartCount: request.BodyPartCount(), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := state.Outbound().CompleteSent(context.Background(), maildomain.CompleteOutboundSend{
		AttemptID: attempt.ID, Request: request, Content: content, AcceptedAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status != maildomain.OutboundSent || accepted.MessageID != attempt.ProposedMessageID || !strings.HasPrefix(accepted.ThreadID, "thr_") || accepted.Reconciliation != maildomain.ReconciliationPending || accepted.AcceptedAt == nil {
		t.Fatalf("accepted attempt = %+v", accepted)
	}
	message, err := state.Mail().Message(context.Background(), accountID, accepted.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.Direction != maildomain.DirectionOutbound || len(message.Locations) != 0 || message.LocalRaw == nil || message.LocalRaw.ContentHash != hash || message.LocalRaw.StorageKey != key || message.LocalRaw.ByteSize != int64(len(raw)) {
		t.Fatalf("outbound message source = %+v", message)
	}
	if message.From.DisplayName != "Ashray Sender" || message.From.Email != "owner@icloud.com" || len(message.To) != 1 || len(message.CC) != 1 || len(message.BCC) != 1 || message.BCC[0].Email != "hidden@example.com" {
		t.Fatalf("outbound message recipients = %+v", message)
	}
	if message.Content == nil || !message.Content.AttachmentsIndexed || len(message.Content.TextPaths) != 1 || len(message.Content.HTMLPaths) != 1 {
		t.Fatalf("outbound content = %+v", message.Content)
	}
	page, err := state.Search().Search(context.Background(), accountID, maildomain.SearchOptions{
		Query: "outbound-search-token", ResultKind: maildomain.SearchMessages, Limit: 20,
	})
	if err != nil || len(page.Hits) != 1 || page.Hits[0].Message.ID != message.ID || page.Query.IndexState != "complete" {
		t.Fatalf("outbound search page=%+v err=%v", page, err)
	}
	threadPage, err := state.Threads().Show(context.Background(), accountID, accepted.ThreadID, "", 20)
	if err != nil || threadPage.Thread.MessageCount != 1 || len(threadPage.Thread.Messages) != 1 || threadPage.Thread.Messages[0].ID != message.ID {
		t.Fatalf("outbound thread=%+v err=%v", threadPage, err)
	}

	acceptedAgain, err := state.Outbound().CompleteSent(context.Background(), maildomain.CompleteOutboundSend{
		AttemptID: attempt.ID, Request: request, Content: content, AcceptedAt: now.Add(2 * time.Minute),
	})
	if err != nil || acceptedAgain.ID != accepted.ID || acceptedAgain.Status != maildomain.OutboundSent {
		t.Fatalf("repeat complete=%+v err=%v", acceptedAgain, err)
	}
}

func TestOutboundReplyPersistsExactParentAndJoinsExistingThread(t *testing.T) {
	state, accountID, inboxID := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 6, 14, 25, 0, 0, time.UTC)
	parent := mailRecordFixture(accountID, inboxID, 100, 1, "reply-parent@example.test")
	parent.Parsed.InReplyTo = ""
	parent.Parsed.References = nil
	parent.Parsed.Subject = "Reply persistence"
	parent.Parsed.ReplyTo = maildomain.Address{Email: "reply-target@example.test"}
	commitSentRecord(t, state, parent, "chk_0198d704-6c00-7000-8000-000000000611", base)
	parentPage, err := state.Threads().Show(ctx, accountID, parent.MessageID, "", 20)
	if err != nil {
		t.Fatal(err)
	}

	request, err := maildomain.NormalizeSendRequest(maildomain.SendRequest{
		Kind:             maildomain.OutboundKindReply,
		ParentMessageID:  parent.MessageID,
		SenderIdentityID: primaryMailIdentityID(accountID),
		From:             maildomain.Address{DisplayName: "Mail Test", Email: "mail-test@icloud.com"},
		To:               []maildomain.Address{{Email: "reply-target@example.test"}},
		Subject:          "Re: Reply persistence",
		TextBody:         "persisted reply body",
		InReplyTo:        parent.Parsed.RFCMessageID,
		References:       []string{parent.Parsed.RFCMessageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	if err := maildomain.ComposeOutboundMessage(&raw, request, "reply-child@example.test", base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	content, err := maildomain.ParseNormalized(bytes.NewReader(raw.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	content.AttachmentsIndexed = true
	for index := range content.Parts {
		content.Parts[index].ID = fmt.Sprintf("prt_0198d704-6c00-7000-8000-%012d", index+750)
	}
	rawHash := strings.Repeat("9", 64)
	attempt, existing, err := state.Outbound().Begin(ctx, maildomain.BeginOutboundAttempt{
		AttemptID: "send_0198d704-6c00-7000-8000-000000000011",
		AccountID: accountID, ProposedMessageID: "msg_0198d704-6c00-7000-8000-000000000611",
		SenderIdentityID: primaryMailIdentityID(accountID),
		RequestHash:      strings.Repeat("8", 64), IdempotencyKey: "reply-persistence-01",
		RFCMessageID:   content.Preview.RFCMessageID,
		RawContentHash: rawHash, RawStorageKey: "raw/sha256/99/" + rawHash + ".eml", RawByteSize: int64(raw.Len()),
		ToCount: len(request.To), BodyPartCount: request.BodyPartCount(),
		Kind: request.Operation(), ParentMessageID: request.ParentMessageID, Now: base.Add(time.Minute),
	})
	if err != nil || existing {
		t.Fatalf("begin reply attempt=%+v existing=%v err=%v", attempt, existing, err)
	}
	accepted, err := state.Outbound().CompleteSent(ctx, maildomain.CompleteOutboundSend{
		AttemptID: attempt.ID, Request: request, Content: content, AcceptedAt: base.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Kind != maildomain.OutboundKindReply || accepted.ParentMessageID != parent.MessageID || accepted.ThreadID != parentPage.Thread.ID {
		t.Fatalf("accepted reply metadata=%+v parent_thread=%s", accepted, parentPage.Thread.ID)
	}
	message, err := state.Mail().Message(ctx, accountID, accepted.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.InReplyTo != "reply-parent@example.test" || strings.Join(message.References, ",") != "reply-parent@example.test" {
		t.Fatalf("stored reply threading=%+v", message)
	}
	threadPage, err := state.Threads().Show(ctx, accountID, parentPage.Thread.ID, "", 20)
	if err != nil || threadPage.Thread.MessageCount != 2 || len(threadPage.Thread.Messages) != 2 {
		t.Fatalf("reply thread page=%+v err=%v", threadPage, err)
	}
}

func TestOutboundForwardPersistsSourceAndCreatesIndependentThread(t *testing.T) {
	state, accountID, inboxID := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 7, 0, 15, 0, 0, time.UTC)
	source := mailRecordFixture(accountID, inboxID, 100, 5, "forward-source@example.test")
	source.Parsed.InReplyTo = ""
	source.Parsed.References = nil
	source.Parsed.From = maildomain.Address{Email: "target@example.test"}
	source.Parsed.ReplyTo = maildomain.Address{}
	source.Parsed.To = []maildomain.Address{{Email: "mail-test@icloud.com"}}
	source.Parsed.Subject = "Forward source"
	source.Parsed.BCC = []maildomain.Address{{Email: "hidden@example.test"}}
	commitSentRecord(t, state, source, "chk_0198d704-6c00-7000-8000-000000000612", base)
	sourcePage, err := state.Threads().Show(ctx, accountID, source.MessageID, "", 20)
	if err != nil {
		t.Fatal(err)
	}

	request, err := maildomain.NormalizeSendRequest(maildomain.SendRequest{
		Kind: maildomain.OutboundKindForward, SourceMessageID: source.MessageID,
		SenderIdentityID: primaryMailIdentityID(accountID),
		From:             maildomain.Address{DisplayName: "Mail Test", Email: "mail-test@icloud.com"},
		To:               []maildomain.Address{{Email: "target@example.test"}},
		Subject:          "Fwd: Forward source", TextBody: "quoted forward body",
	})
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	if err := maildomain.ComposeOutboundMessage(&raw, request, "forward-child@example.test", base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	content, err := maildomain.ParseNormalized(bytes.NewReader(raw.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	content.AttachmentsIndexed = true
	for index := range content.Parts {
		content.Parts[index].ID = fmt.Sprintf("prt_0198d704-6c00-7000-8000-%012d", index+780)
	}
	rawHash := strings.Repeat("d", 64)
	attempt, existing, err := state.Outbound().Begin(ctx, maildomain.BeginOutboundAttempt{
		AttemptID: "send_0198d704-6c00-7000-8000-000000000012",
		AccountID: accountID, ProposedMessageID: "msg_0198d704-6c00-7000-8000-000000000612",
		SenderIdentityID: primaryMailIdentityID(accountID),
		RequestHash:      strings.Repeat("c", 64), IdempotencyKey: "forward-persistence-01",
		RFCMessageID:   content.Preview.RFCMessageID,
		RawContentHash: rawHash, RawStorageKey: "raw/sha256/dd/" + rawHash + ".eml", RawByteSize: int64(raw.Len()),
		ToCount: len(request.To), BodyPartCount: request.BodyPartCount(),
		Kind: request.Operation(), SourceMessageID: request.SourceMessageID, Now: base.Add(time.Minute),
	})
	if err != nil || existing {
		t.Fatalf("begin forward attempt=%+v existing=%v err=%v", attempt, existing, err)
	}
	accepted, err := state.Outbound().CompleteSent(ctx, maildomain.CompleteOutboundSend{
		AttemptID: attempt.ID, Request: request, Content: content, AcceptedAt: base.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Kind != maildomain.OutboundKindForward || accepted.SourceMessageID != source.MessageID ||
		accepted.ParentMessageID != "" || accepted.ThreadID == "" || accepted.ThreadID == sourcePage.Thread.ID {
		t.Fatalf("accepted forward metadata=%+v source_thread=%s", accepted, sourcePage.Thread.ID)
	}
	message, err := state.Mail().Message(ctx, accountID, accepted.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.InReplyTo != "" || len(message.References) != 0 || message.Subject != "Fwd: Forward source" {
		t.Fatalf("stored forward relationship=%+v", message)
	}
	sourceAfter, err := state.Mail().Message(ctx, accountID, source.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if sourceAfter.Subject != source.Parsed.Subject || len(sourceAfter.Locations) != 1 || !sourceAfter.Locations[0].Present {
		t.Fatalf("source mutated by forward: %+v", sourceAfter)
	}
	sourceThreadAfter, err := state.Threads().Show(ctx, accountID, source.MessageID, "", 20)
	if err != nil || sourceThreadAfter.Thread.ID != sourcePage.Thread.ID || sourceThreadAfter.Thread.MessageCount != 1 {
		t.Fatalf("source thread changed=%+v err=%v", sourceThreadAfter, err)
	}
}

func TestOutboundAcceptedTransactionRollbackLeavesPendingAttemptAndNoMessage(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 14, 30, 0, 0, time.UTC)
	request, content, raw := outboundStoreFixture(t, accountID, now)
	hash := strings.Repeat("1", 64)
	attempt, _, err := state.Outbound().Begin(context.Background(), maildomain.BeginOutboundAttempt{
		AttemptID: "send_0198d704-6c00-7000-8000-000000000020",
		AccountID: accountID, ProposedMessageID: "msg_0198d704-6c00-7000-8000-000000000620",
		SenderIdentityID: primaryMailIdentityID(accountID),
		RequestHash:      strings.Repeat("2", 64), RFCMessageID: content.Preview.RFCMessageID,
		RawContentHash: hash, RawStorageKey: "raw/sha256/11/" + hash + ".eml", RawByteSize: int64(len(raw)),
		ToCount: 1, BodyPartCount: 2, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.Exec(`
CREATE TRIGGER synthetic_outbound_complete_failure
BEFORE INSERT ON message_search_documents
WHEN NEW.subject = 'Phase 14 accepted'
BEGIN
    SELECT RAISE(ABORT, 'synthetic outbound completion failure');
END;`); err != nil {
		t.Fatal(err)
	}
	_, err = state.Outbound().CompleteSent(context.Background(), maildomain.CompleteOutboundSend{
		AttemptID: attempt.ID, Request: request, Content: content, AcceptedAt: now.Add(time.Minute),
	})
	if err == nil || !strings.Contains(err.Error(), "synthetic outbound completion failure") {
		t.Fatalf("completion error = %v", err)
	}
	stored, err := state.Outbound().Attempt(context.Background(), attempt.ID)
	if err != nil || stored.Status != maildomain.OutboundPending || stored.MessageID != "" || stored.ThreadID != "" {
		t.Fatalf("rolled-back attempt=%+v err=%v", stored, err)
	}
	if _, err := state.Mail().Message(context.Background(), accountID, attempt.ProposedMessageID); !errors.Is(err, maildomain.ErrMessageNotFound) {
		t.Fatalf("rolled-back message error = %v", err)
	}
	acceptedIncomplete, err := state.Outbound().MarkAcceptedIncomplete(
		context.Background(), attempt.ID, "local_completion", now.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if acceptedIncomplete.Status != maildomain.OutboundSent ||
		acceptedIncomplete.Reconciliation != maildomain.ReconciliationPending ||
		acceptedIncomplete.ErrorCode != "local_completion" ||
		acceptedIncomplete.AcceptedAt == nil || acceptedIncomplete.MessageID != "" || acceptedIncomplete.ThreadID != "" {
		t.Fatalf("accepted incomplete attempt = %+v", acceptedIncomplete)
	}
}

func TestOutboundReconcileConfirmsSentLocationAndResolvesUnknownOnlyByExactMessageID(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 6, 16, 30, 0, 0, time.UTC)
	sentID := insertMailFolderFixture(t, state, accountID, folder.KindSent, "Sent Messages", 210, base)

	request, content, raw := outboundStoreFixture(t, accountID, base)
	accepted, _, err := state.Outbound().Begin(ctx, maildomain.BeginOutboundAttempt{
		AttemptID: "send_0198d704-6c00-7000-8000-000000000030",
		AccountID: accountID, ProposedMessageID: "msg_0198d704-6c00-7000-8000-000000000630",
		SenderIdentityID: primaryMailIdentityID(accountID),
		RequestHash:      strings.Repeat("3", 64), IdempotencyKey: "reconcile-sent-01",
		RFCMessageID:   content.Preview.RFCMessageID,
		RawContentHash: strings.Repeat("4", 64), RawStorageKey: "raw/sha256/44/" + strings.Repeat("4", 64) + ".eml", RawByteSize: int64(len(raw)),
		ToCount: 1, BodyPartCount: 2, Now: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err = state.Outbound().CompleteSent(ctx, maildomain.CompleteOutboundSend{
		AttemptID: accepted.ID, Request: request, Content: content, AcceptedAt: base.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	acceptedRecord := mailRecordFixture(accountID, sentID, 210, 31, content.Preview.RFCMessageID)
	acceptedRecord.Direction = maildomain.DirectionOutbound
	commitSentRecord(t, state, acceptedRecord, "chk_0198d704-6c00-7000-8000-000000000630", base.Add(2*time.Minute))

	summary, err := state.Outbound().Reconcile(ctx, accountID, 100, base.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Confirmed != 1 || summary.ResolvedUnknown != 0 || summary.Ambiguous != 0 || summary.Unmatched != 0 {
		t.Fatalf("accepted reconciliation summary = %+v", summary)
	}
	confirmed, err := state.Outbound().Attempt(ctx, accepted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Status != maildomain.OutboundSent || confirmed.Reconciliation != maildomain.ReconciliationConfirmed ||
		confirmed.MessageID != accepted.MessageID || confirmed.ReconciledLocationID != acceptedRecord.LocationID || confirmed.ReconciliationErrorCode != "" {
		t.Fatalf("confirmed accepted attempt = %+v", confirmed)
	}

	unknownID := "send_0198d704-6c00-7000-8000-000000000031"
	unknownRFC := "unknown-reconcile@example.test"
	unknown, _, err := state.Outbound().Begin(ctx, maildomain.BeginOutboundAttempt{
		AttemptID: unknownID, AccountID: accountID,
		SenderIdentityID:  primaryMailIdentityID(accountID),
		ProposedMessageID: "msg_0198d704-6c00-7000-8000-000000000631",
		RequestHash:       strings.Repeat("5", 64), IdempotencyKey: "reconcile-unknown-01",
		RFCMessageID: unknownRFC, RawContentHash: strings.Repeat("6", 64),
		RawStorageKey: "raw/sha256/66/" + strings.Repeat("6", 64) + ".eml", RawByteSize: 80,
		ToCount: 1, BodyPartCount: 1, Now: base.Add(4 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	unknown, err = state.Outbound().MarkOutcome(ctx, unknown.ID, maildomain.OutboundUnknown, "smtp_accept", base.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	unknownRecord := mailRecordFixture(accountID, sentID, 210, 32, unknownRFC)
	unknownRecord.Direction = maildomain.DirectionOutbound
	commitSentRecord(t, state, unknownRecord, "chk_0198d704-6c00-7000-8000-000000000630", base.Add(6*time.Minute))

	summary, err = state.Outbound().Reconcile(ctx, accountID, 100, base.Add(7*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if summary.ResolvedUnknown != 1 || summary.Confirmed != 0 || summary.Ambiguous != 0 {
		t.Fatalf("unknown reconciliation summary = %+v", summary)
	}
	resolved, err := state.Outbound().Attempt(ctx, unknown.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != maildomain.OutboundSent || resolved.Reconciliation != maildomain.ReconciliationConfirmed ||
		resolved.MessageID != unknownRecord.MessageID || resolved.ReconciledLocationID != unknownRecord.LocationID || resolved.ErrorCode != "" {
		t.Fatalf("resolved unknown attempt = %+v", resolved)
	}
}

func TestOutboundReconcileKeepsAmbiguousAndSubjectOnlyEvidenceUnknown(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	ctx := context.Background()
	base := time.Date(2026, 8, 6, 17, 0, 0, 0, time.UTC)
	sentOne := insertMailFolderFixture(t, state, accountID, folder.KindSent, "Sent Messages", 220, base)
	sentTwo := insertMailFolderFixture(t, state, accountID, folder.KindSent, "Sent Archive", 221, base)

	ambiguousRFC := "ambiguous-reconcile@example.test"
	ambiguous, _, err := state.Outbound().Begin(ctx, maildomain.BeginOutboundAttempt{
		AttemptID: "send_0198d704-6c00-7000-8000-000000000040", AccountID: accountID,
		SenderIdentityID:  primaryMailIdentityID(accountID),
		ProposedMessageID: "msg_0198d704-6c00-7000-8000-000000000640",
		RequestHash:       strings.Repeat("7", 64), IdempotencyKey: "ambiguous-unknown-01",
		RFCMessageID: ambiguousRFC, RawContentHash: strings.Repeat("8", 64),
		RawStorageKey: "raw/sha256/88/" + strings.Repeat("8", 64) + ".eml", RawByteSize: 90,
		ToCount: 1, BodyPartCount: 1, Now: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	ambiguous, err = state.Outbound().MarkOutcome(ctx, ambiguous.ID, maildomain.OutboundUnknown, "smtp_accept", base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	first := mailRecordFixture(accountID, sentOne, 220, 41, ambiguousRFC)
	first.Direction = maildomain.DirectionOutbound
	second := mailRecordFixture(accountID, sentTwo, 221, 42, ambiguousRFC)
	second.Direction = maildomain.DirectionOutbound
	commitSentRecord(t, state, first, "chk_0198d704-6c00-7000-8000-000000000640", base.Add(2*time.Minute))
	commitSentRecord(t, state, second, "chk_0198d704-6c00-7000-8000-000000000641", base.Add(3*time.Minute))

	summary, err := state.Outbound().Reconcile(ctx, accountID, 100, base.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Ambiguous != 1 || summary.ResolvedUnknown != 0 {
		t.Fatalf("ambiguous reconciliation summary = %+v", summary)
	}
	stored, err := state.Outbound().Attempt(ctx, ambiguous.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != maildomain.OutboundUnknown || stored.Reconciliation != maildomain.ReconciliationUnknown || stored.ReconciliationErrorCode != "ambiguous_sent_evidence" {
		t.Fatalf("ambiguous attempt = %+v", stored)
	}

	unmatchedRFC := "unmatched-reconcile@example.test"
	unmatched, _, err := state.Outbound().Begin(ctx, maildomain.BeginOutboundAttempt{
		AttemptID: "send_0198d704-6c00-7000-8000-000000000041", AccountID: accountID,
		SenderIdentityID:  primaryMailIdentityID(accountID),
		ProposedMessageID: "msg_0198d704-6c00-7000-8000-000000000641",
		RequestHash:       strings.Repeat("9", 64), IdempotencyKey: "unmatched-unknown-01",
		RFCMessageID: unmatchedRFC, RawContentHash: strings.Repeat("a", 64),
		RawStorageKey: "raw/sha256/aa/" + strings.Repeat("a", 64) + ".eml", RawByteSize: 100,
		ToCount: 1, BodyPartCount: 1, Now: base.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	unmatched, err = state.Outbound().MarkOutcome(ctx, unmatched.ID, maildomain.OutboundUnknown, "smtp_accept", base.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	subjectOnly := mailRecordFixture(accountID, sentOne, 220, 43, "different-message-id@example.test")
	subjectOnly.Direction = maildomain.DirectionOutbound
	subjectOnly.Parsed.Subject = "Fixture message"
	subjectOnly.Parsed.Snippet = "Synthetic durable Inbox message."
	commitSentRecord(t, state, subjectOnly, "chk_0198d704-6c00-7000-8000-000000000640", base.Add(7*time.Minute))

	summary, err = state.Outbound().Reconcile(ctx, accountID, 100, base.Add(8*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Unmatched != 1 || summary.ResolvedUnknown != 0 {
		t.Fatalf("subject-only reconciliation summary = %+v", summary)
	}
	stored, err = state.Outbound().Attempt(ctx, unmatched.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != maildomain.OutboundUnknown || stored.MessageID != "" || stored.ReconciledLocationID != "" {
		t.Fatalf("subject-only attempt merged unexpectedly = %+v", stored)
	}
}

func commitSentRecord(t *testing.T, state *Store, record maildomain.IngestRecord, checkpointID string, attemptedAt time.Time) {
	t.Helper()
	if _, err := state.Mail().CommitInboxPage(context.Background(), maildomain.CommitPage{
		CheckpointID: checkpointID, AccountID: record.AccountID, FolderID: record.FolderID,
		UIDValidity: record.UIDValidity, Bootstrap: true,
		LastCommittedUID: record.UID, LastScannedUID: record.UID,
		AttemptedAt: attemptedAt, Records: []maildomain.IngestRecord{record},
	}); err != nil {
		t.Fatal(err)
	}
}

func outboundStoreFixture(t *testing.T, accountID string, now time.Time) (maildomain.SendRequest, maildomain.NormalizedContent, []byte) {
	t.Helper()
	request, err := maildomain.NormalizeSendRequest(maildomain.SendRequest{
		SenderIdentityID: primaryMailIdentityID(accountID),
		From:             maildomain.Address{DisplayName: "Ashray Sender", Email: "owner@icloud.com"},
		To:               []maildomain.Address{{DisplayName: "Primary", Email: "primary@example.com"}},
		CC:               []maildomain.Address{{DisplayName: "Visible", Email: "visible@example.com"}},
		BCC:              []maildomain.Address{{DisplayName: "Hidden", Email: "hidden@example.com"}},
		ReplyTo:          &maildomain.Address{DisplayName: "Replies", Email: "reply@example.com"},
		Subject:          "Phase 14 accepted", TextBody: "outbound-search-token plain body",
		HTMLBody: "<p>outbound-search-token HTML body</p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	if err := maildomain.ComposeOutboundMessage(&raw, request, "phase14-store@example.test", now); err != nil {
		t.Fatal(err)
	}
	content, err := maildomain.ParseNormalized(strings.NewReader(raw.String()))
	if err != nil {
		t.Fatal(err)
	}
	content.AttachmentsIndexed = true
	for index := range content.Parts {
		content.Parts[index].ID = fmt.Sprintf("prt_0198d704-6c00-7000-8000-%012d", index+700)
	}
	if content.Preview.RFCMessageID == "" || content.Preview.Subject != request.Subject {
		t.Fatalf("composed content preview = %+v", content.Preview)
	}
	_ = accountID
	return request, content, raw.Bytes()
}
