package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestOutboundAttemptIdempotencyAndUnknownOutcome(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	input := maildomain.BeginOutboundAttempt{
		AttemptID:         "send_0198d704-6c00-7000-8000-000000000001",
		AccountID:         accountID,
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
		RequestHash: strings.Repeat("f", 64), IdempotencyKey: "accepted-send-01",
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

func TestOutboundAcceptedTransactionRollbackLeavesPendingAttemptAndNoMessage(t *testing.T) {
	state, accountID, _ := newMailStoreFixture(t)
	defer state.Close()
	now := time.Date(2026, 8, 6, 14, 30, 0, 0, time.UTC)
	request, content, raw := outboundStoreFixture(t, accountID, now)
	hash := strings.Repeat("1", 64)
	attempt, _, err := state.Outbound().Begin(context.Background(), maildomain.BeginOutboundAttempt{
		AttemptID: "send_0198d704-6c00-7000-8000-000000000020",
		AccountID: accountID, ProposedMessageID: "msg_0198d704-6c00-7000-8000-000000000620",
		RequestHash: strings.Repeat("2", 64), RFCMessageID: content.Preview.RFCMessageID,
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

func outboundStoreFixture(t *testing.T, accountID string, now time.Time) (maildomain.SendRequest, maildomain.NormalizedContent, []byte) {
	t.Helper()
	request, err := maildomain.NormalizeSendRequest(maildomain.SendRequest{
		From:    maildomain.Address{DisplayName: "Ashray Sender", Email: "owner@icloud.com"},
		To:      []maildomain.Address{{DisplayName: "Primary", Email: "primary@example.com"}},
		CC:      []maildomain.Address{{DisplayName: "Visible", Email: "visible@example.com"}},
		BCC:     []maildomain.Address{{DisplayName: "Hidden", Email: "hidden@example.com"}},
		ReplyTo: &maildomain.Address{DisplayName: "Replies", Email: "reply@example.com"},
		Subject: "Phase 14 accepted", TextBody: "outbound-search-token plain body",
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
