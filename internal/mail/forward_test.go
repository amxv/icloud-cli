package mail

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestBuildForwardPlanQuotesSafeOriginalAndStartsNewThread(t *testing.T) {
	headerDate := time.Date(2026, 8, 7, 1, 2, 3, 0, time.UTC)
	source := Message{
		ID: "msg_forward_source", RFCMessageID: "source@example.test",
		InReplyTo: "root@example.test", References: []string{"root@example.test"},
		From:    Address{DisplayName: "Original Sender", Email: "sender@example.test"},
		To:      []Address{{DisplayName: "Visible Recipient", Email: "visible@example.test"}},
		CC:      []Address{{Email: "copy@example.test"}},
		BCC:     []Address{{Email: "hidden@example.test"}},
		Subject: "Fwd: FW: Project update", HeaderDate: &headerDate,
	}
	plan, err := BuildForwardPlan(source, "thr_source", ForwardRequest{
		SourceMessageID: source.ID, OriginalMode: ForwardOriginalQuoted,
		To: []Address{{Email: "target@example.test"}}, TextBody: "Please review.",
	}, "Line one\nLine two", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Send.Kind != OutboundKindForward || plan.Send.SourceMessageID != source.ID ||
		plan.Send.ParentMessageID != "" || plan.Send.InReplyTo != "" || len(plan.Send.References) != 0 {
		t.Fatalf("forward relationship metadata = %+v", plan.Send)
	}
	if plan.Send.Subject != "Fwd: Project update" {
		t.Fatalf("subject = %q", plan.Send.Subject)
	}
	for _, want := range []string{
		"Please review.", "---------- Forwarded message ----------",
		"From: Original Sender <sender@example.test>",
		"Date: Fri, 07 Aug 2026 01:02:03 +0000",
		"Subject: Fwd: FW: Project update", "To: Visible Recipient <visible@example.test>",
		"Cc: copy@example.test", "> Line one", "> Line two",
	} {
		if !strings.Contains(plan.Send.TextBody, want) {
			t.Fatalf("quoted body missing %q: %q", want, plan.Send.TextBody)
		}
	}
	if strings.Contains(plan.Send.TextBody, "hidden@example.test") {
		t.Fatalf("quoted body exposed BCC: %q", plan.Send.TextBody)
	}
}

func TestBuildForwardPlanSupportsExplicitEMLAndSelectedOriginalAttachments(t *testing.T) {
	eml := []byte("From: sender@example.test\r\nTo: owner@example.test\r\n\r\noriginal\r\n")
	emlHash := sha256.Sum256(eml)
	original := []byte("selected original attachment")
	originalHash := sha256.Sum256(original)
	emlAttachment := OutboundAttachment{
		Filename: "forwarded-message.eml", MediaType: "message/rfc822",
		ContentHash: hex.EncodeToString(emlHash[:]), ByteSize: int64(len(eml)),
		Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(eml)), nil },
	}
	originalAttachment := OutboundAttachment{
		Filename: "selected.txt", MediaType: "text/plain",
		ContentHash: hex.EncodeToString(originalHash[:]), ByteSize: int64(len(original)),
		Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(original)), nil },
	}
	plan, err := BuildForwardPlan(Message{ID: "msg_forward_eml", Subject: "Topic"}, "thr_old", ForwardRequest{
		SourceMessageID: "msg_forward_eml", OriginalMode: ForwardOriginalEML,
		To: []Address{{Email: "target@example.test"}},
	}, "", []OutboundAttachment{originalAttachment}, []ForwardOriginalAttachment{{
		ID: "att_selected", Filename: "selected.txt", MediaType: "text/plain", ByteSize: int64(len(original)),
	}}, &emlAttachment)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.AttachedOriginalEML || plan.OriginalEMLByteSize != int64(len(eml)) ||
		len(plan.Send.Attachments) != 2 || plan.Send.TextBody != "Forwarded message attached." {
		t.Fatalf("EML forward plan = %+v", plan)
	}
	if plan.Send.Attachments[0].MediaType != "message/rfc822" || plan.Send.Attachments[1].Filename != "selected.txt" {
		t.Fatalf("attachment order = %+v", plan.Send.Attachments)
	}
	if len(plan.OriginalAttachments) != 1 || plan.OriginalAttachments[0].ID != "att_selected" {
		t.Fatalf("selected metadata = %+v", plan.OriginalAttachments)
	}
}

func TestBuildForwardPlanRejectsUnsafeModesAndMissingBodies(t *testing.T) {
	source := Message{ID: "msg_forward_none", Subject: "Topic"}
	_, err := BuildForwardPlan(source, "thr", ForwardRequest{
		SourceMessageID: source.ID, OriginalMode: ForwardOriginalNone,
		To: []Address{{Email: "target@example.test"}},
	}, "", nil, nil, nil)
	if !errors.Is(err, ErrInvalidForwardRequest) {
		t.Fatalf("none without body error = %v", err)
	}
	_, err = BuildForwardPlan(source, "thr", ForwardRequest{
		SourceMessageID: source.ID, OriginalMode: ForwardOriginalEML,
		To: []Address{{Email: "target@example.test"}},
	}, "", nil, nil, nil)
	if !errors.Is(err, ErrInvalidForwardRequest) {
		t.Fatalf("EML without raw error = %v", err)
	}
	if _, err := NormalizeForwardOriginalMode("raw"); !errors.Is(err, ErrInvalidForwardRequest) {
		t.Fatalf("invalid original mode error = %v", err)
	}
}

func TestComposeOutboundForwardStreamsLargeEMLWithoutReplyHeaders(t *testing.T) {
	payload := bytes.Repeat([]byte("large-forward-eml-token\n"), 140000)
	sum := sha256.Sum256(payload)
	opened := 0
	request := SendRequest{
		Kind: OutboundKindForward, SourceMessageID: "msg_forward_large",
		From: Address{Email: "owner@icloud.com"}, To: []Address{{Email: "target@example.test"}},
		Subject: "Fwd: Large message", TextBody: "Forwarded message attached.",
		Attachments: []OutboundAttachment{{
			Filename: "forwarded-message.eml", MediaType: "message/rfc822",
			ContentHash: hex.EncodeToString(sum[:]), ByteSize: int64(len(payload)),
			Open: func() (io.ReadCloser, error) {
				opened++
				return &boundedForwardReader{reader: bytes.NewReader(payload), maximum: 64 << 10}, nil
			},
		}},
	}
	var raw bytes.Buffer
	if err := ComposeOutboundMessage(&raw, request, "forward@example.test", time.Date(2026, 8, 7, 2, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if opened != 1 {
		t.Fatalf("attachment opens = %d", opened)
	}
	text := raw.String()
	if strings.Contains(text, "In-Reply-To:") || strings.Contains(text, "References:") || !strings.Contains(text, "forwarded-message.eml") {
		t.Fatalf("forward raw relationship/attachment headers = %q", text[:min(len(text), 2000)])
	}
	content, err := ParseNormalized(bytes.NewReader(raw.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if content.Preview.InReplyTo != "" || len(content.Preview.References) != 0 || content.Preview.Subject != "Fwd: Large message" {
		t.Fatalf("parsed forward preview = %+v", content.Preview)
	}
}

func TestNormalizeForwardRejectsReplyThreadingMetadata(t *testing.T) {
	_, err := NormalizeSendRequest(SendRequest{
		Kind: OutboundKindForward, SourceMessageID: "msg_forward",
		From: Address{Email: "owner@icloud.com"}, To: []Address{{Email: "target@example.test"}},
		Subject: "Fwd: Topic", TextBody: "body", InReplyTo: "parent@example.test",
	})
	if !errors.Is(err, ErrInvalidSendRequest) {
		t.Fatalf("forward threading error = %v", err)
	}
}

type boundedForwardReader struct {
	reader  io.Reader
	maximum int
}

func (reader *boundedForwardReader) Read(buffer []byte) (int, error) {
	if len(buffer) > reader.maximum {
		return 0, errors.New("forward attachment received an unbounded read buffer")
	}
	return reader.reader.Read(buffer)
}

func (reader *boundedForwardReader) Close() error { return nil }
