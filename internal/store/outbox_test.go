package store

import (
	"context"
	"strings"
	"testing"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestOutboxPrivateEnvelopePersistsAndInterruptedRetryFailsClosed(t *testing.T) {
	root := t.TempDir()
	state, accountID, _ := newMailStoreFixtureAt(t, root)
	ctx := context.Background()
	now := time.Date(2026, 8, 11, 1, 0, 0, 0, time.UTC)
	hash := strings.Repeat("a", 64)
	key := "raw/sha256/aa/" + hash + ".eml"
	source, _, err := state.Outbound().Begin(ctx, maildomain.BeginOutboundAttempt{
		AttemptID: "send_0198d704-6c00-7000-8000-000000031001", AccountID: accountID,
		SenderIdentityID: primaryMailIdentityID(accountID), ProposedMessageID: "msg_0198d704-6c00-7000-8000-000000031001",
		RequestHash: strings.Repeat("b", 64), IdempotencyKey: "phase31-source", RFCMessageID: "phase31-outbox@example.test",
		RawContentHash: hash, RawStorageKey: key, RawByteSize: 512, ToCount: 1, CCCount: 0, BCCCount: 1, BodyPartCount: 1,
		Kind: maildomain.OutboundKindSend, Now: now,
	})
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	item, failed, err := state.Outbox().EnqueueSafeFailure(ctx, maildomain.EnqueueOutboxItem{
		OutboxID: "out_0198d704-6c00-7000-8000-000000031001", AttemptID: source.ID,
		FailureStage: "smtp_auth", FailureCode: "smtp_auth",
		Envelope: maildomain.OutboxEnvelope{
			From: maildomain.Address{Email: "owner@icloud.com"}, To: []maildomain.Address{{Email: "visible@example.test"}},
			BCC: []maildomain.Address{{Email: "hidden@example.test"}},
		}, Now: now.Add(time.Minute),
	})
	if err != nil || item.Status != maildomain.OutboxRetryable || failed.Status != maildomain.OutboundFailed {
		state.Close()
		t.Fatalf("enqueue item=%+v attempt=%+v err=%v", item, failed, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	state, err = Open(ctx, testPaths(t, root))
	if err != nil {
		t.Fatal(err)
	}
	item, err = state.Outbox().Item(ctx, accountID, item.ID)
	if err != nil || item.Envelope.From.Email != "owner@icloud.com" || len(item.Envelope.BCC) != 1 || item.Envelope.BCC[0].Email != "hidden@example.test" || item.RawContentHash != hash || item.RawStorageKey != key {
		state.Close()
		t.Fatalf("reopened private item=%+v err=%v", item, err)
	}

	// A crash after PrepareRetry but before the linked attempt exists is
	// provably pre-provider and can safely restore retryability.
	preparedID := "send_0198d704-6c00-7000-8000-000000031002"
	if _, err := state.Outbox().PrepareRetry(ctx, accountID, item.ID, preparedID, now.Add(2*time.Minute)); err != nil {
		state.Close()
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = Open(ctx, testPaths(t, root))
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Outbox().RepairInterrupted(ctx, accountID, now.Add(3*time.Minute)); err != nil {
		state.Close()
		t.Fatal(err)
	}
	item, err = state.Outbox().Item(ctx, accountID, item.ID)
	if err != nil || item.Status != maildomain.OutboxRetryable || item.LatestAttemptID != "" {
		state.Close()
		t.Fatalf("pre-attempt recovery=%+v err=%v", item, err)
	}

	// Once a linked pending attempt exists, restart cannot prove whether DATA
	// was transmitted. Repair therefore marks both records unknown and never
	// restores automatic/manual safe-retry eligibility.
	retryID := "send_0198d704-6c00-7000-8000-000000031003"
	if _, err := state.Outbox().PrepareRetry(ctx, accountID, item.ID, retryID, now.Add(4*time.Minute)); err != nil {
		state.Close()
		t.Fatal(err)
	}
	if _, _, err := state.Outbound().Begin(ctx, maildomain.BeginOutboundAttempt{
		AttemptID: retryID, AccountID: accountID, SenderIdentityID: item.SenderIdentityID,
		ProposedMessageID: "msg_0198d704-6c00-7000-8000-000000031003", RequestHash: item.RequestHash,
		IdempotencyKey: "phase31-retry", RFCMessageID: item.RFCMessageID, RawContentHash: item.RawContentHash,
		RawStorageKey: item.RawStorageKey, RawByteSize: item.RawByteSize, ToCount: item.ToCount, CCCount: item.CCCount,
		BCCCount: item.BCCCount, BodyPartCount: item.BodyPartCount, Kind: item.Kind, RetryOfAttemptID: source.ID, Now: now.Add(4 * time.Minute),
	}); err != nil {
		state.Close()
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = Open(ctx, testPaths(t, root))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.Outbox().RepairInterrupted(ctx, accountID, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	item, err = state.Outbox().Item(ctx, accountID, item.ID)
	if err != nil || item.Status != maildomain.OutboxUnknown || item.LatestAttemptID != retryID {
		t.Fatalf("pending-attempt recovery=%+v err=%v", item, err)
	}
	retryAttempt, err := state.Outbound().Attempt(ctx, retryID)
	if err != nil || retryAttempt.Status != maildomain.OutboundUnknown || retryAttempt.ErrorCode != "retry_interrupted" {
		t.Fatalf("pending-attempt recovery attempt=%+v err=%v", retryAttempt, err)
	}
}
