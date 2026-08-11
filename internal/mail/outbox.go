package mail

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrOutboxNotFound      = errors.New("outbox item not found")
	ErrOutboxNotRetryable  = errors.New("outbox item is not retryable")
	ErrOutboxInvalid       = errors.New("invalid outbox request")
	ErrOutboxInvalidCursor = errors.New("invalid outbox cursor")
)

type OutboxStatus string

const (
	OutboxRetryable OutboxStatus = "retryable"
	OutboxRetrying  OutboxStatus = "retrying"
	OutboxSent      OutboxStatus = "sent"
	OutboxFailed    OutboxStatus = "failed"
	OutboxUnknown   OutboxStatus = "unknown"
	OutboxCanceled  OutboxStatus = "canceled"
)

type OutboxEnvelope struct {
	From    Address   `json:"from"`
	To      []Address `json:"to"`
	CC      []Address `json:"cc,omitempty"`
	BCC     []Address `json:"bcc,omitempty"`
	ReplyTo *Address  `json:"reply_to,omitempty"`
}

func OutboxEnvelopeFromRequest(request SendRequest) OutboxEnvelope {
	return OutboxEnvelope{
		From:    request.From,
		To:      append([]Address(nil), request.To...),
		CC:      append([]Address(nil), request.CC...),
		BCC:     append([]Address(nil), request.BCC...),
		ReplyTo: request.ReplyTo,
	}
}

func (value OutboxEnvelope) EnvelopeRecipients() []string {
	request := SendRequest{To: value.To, CC: value.CC, BCC: value.BCC}
	return request.EnvelopeRecipients()
}

func (value OutboxEnvelope) Validate() error {
	if strings.TrimSpace(value.From.Email) == "" || len(value.EnvelopeRecipients()) == 0 {
		return ErrOutboxInvalid
	}
	return nil
}

type OutboxItem struct {
	ID               string       `json:"id"`
	AccountID        string       `json:"account_id"`
	SourceAttemptID  string       `json:"source_attempt_id"`
	LatestAttemptID  string       `json:"latest_attempt_id,omitempty"`
	SenderIdentityID string       `json:"sender_identity_id"`
	Status           OutboxStatus `json:"status"`
	FailureStage     string       `json:"failure_stage,omitempty"`
	FailureCode      string       `json:"failure_code,omitempty"`
	Kind             OutboundKind `json:"kind"`
	ToCount          int          `json:"to_count"`
	CCCount          int          `json:"cc_count"`
	BCCCount         int          `json:"bcc_count"`
	BodyPartCount    int          `json:"body_part_count"`
	AttachmentCount  int          `json:"attachment_count"`
	AttachmentBytes  int64        `json:"attachment_bytes"`
	RawByteSize      int64        `json:"raw_byte_size"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`

	RequestHash      string         `json:"-"`
	RFCMessageID     string         `json:"-"`
	RawContentHash   string         `json:"-"`
	RawStorageKey    string         `json:"-"`
	ParentMessageID  string         `json:"-"`
	ParentLocationID string         `json:"-"`
	SourceMessageID  string         `json:"-"`
	Envelope         OutboxEnvelope `json:"-"`
}

func (value OutboxItem) Retryable() bool { return value.Status == OutboxRetryable }

type EnqueueOutboxItem struct {
	OutboxID     string
	AttemptID    string
	FailureStage string
	FailureCode  string
	Envelope     OutboxEnvelope
	Now          time.Time
}
