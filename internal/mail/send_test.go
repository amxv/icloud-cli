package mail

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeSendRequestPreservesNamesAndDeduplicatesEnvelope(t *testing.T) {
	request, err := NormalizeSendRequest(SendRequest{
		From: Address{DisplayName: "Ashray Sender", Email: "OWNER@ICLOUD.COM"},
		To: []Address{
			{DisplayName: "Primary Person", Email: "PRIMARY@example.com"},
			{DisplayName: "Duplicate", Email: "primary@example.com"},
		},
		CC: []Address{
			{DisplayName: "Primary again", Email: "primary@example.com"},
			{DisplayName: "Visible Copy", Email: "copy@example.com"},
		},
		BCC: []Address{
			{DisplayName: "Hidden Copy", Email: "hidden@example.com"},
			{DisplayName: "Visible duplicate", Email: "copy@example.com"},
		},
		ReplyTo:  &Address{DisplayName: "Replies", Email: "reply@example.com"},
		Subject:  "  Project   update\n",
		TextBody: "Hello\r\nworld\r\n",
		HTMLBody: "<p>Hello world</p>\r\n",
		Headers: []CustomHeader{
			{Key: "X-Trace-ID", Value: " synthetic-01 "},
		},
		IdempotencyKey: " send-project-01 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.From.DisplayName != "Ashray Sender" || request.From.Email != "owner@icloud.com" {
		t.Fatalf("from = %+v", request.From)
	}
	if len(request.To) != 1 || request.To[0].Email != "primary@example.com" || request.To[0].DisplayName != "Primary Person" {
		t.Fatalf("to = %+v", request.To)
	}
	if len(request.CC) != 1 || request.CC[0].Email != "copy@example.com" || len(request.BCC) != 1 || request.BCC[0].Email != "hidden@example.com" {
		t.Fatalf("cc=%+v bcc=%+v", request.CC, request.BCC)
	}
	if request.Subject != "Project update" || request.TextBody != "Hello\nworld" || request.HTMLBody != "<p>Hello world</p>" {
		t.Fatalf("normalized content subject=%q text=%q html=%q", request.Subject, request.TextBody, request.HTMLBody)
	}
	if request.Headers[0].Key != "X-Trace-Id" || request.Headers[0].Value != "synthetic-01" || request.IdempotencyKey != "send-project-01" {
		t.Fatalf("headers/key = %+v %q", request.Headers, request.IdempotencyKey)
	}
	if got := request.EnvelopeRecipients(); strings.Join(got, ",") != "primary@example.com,copy@example.com,hidden@example.com" {
		t.Fatalf("envelope = %v", got)
	}
	firstHash, err := SendRequestHash(request)
	if err != nil {
		t.Fatal(err)
	}
	request.IdempotencyKey = "another-key"
	secondHash, err := SendRequestHash(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstHash) != 64 || firstHash != secondHash {
		t.Fatalf("hashes first=%q second=%q", firstHash, secondHash)
	}
}

func TestNormalizeSendRequestRejectsUnsafeHeadersAndMissingContent(t *testing.T) {
	base := SendRequest{
		From:    Address{Email: "owner@icloud.com"},
		To:      []Address{{Email: "recipient@example.com"}},
		Subject: "Subject", TextBody: "Body",
	}
	for _, header := range []CustomHeader{
		{Key: "Bcc", Value: "hidden@example.com"},
		{Key: "Received", Value: "synthetic"},
		{Key: "X-Test", Value: "safe\r\nBcc: injected@example.com"},
	} {
		request := base
		request.Headers = []CustomHeader{header}
		if _, err := NormalizeSendRequest(request); !errors.Is(err, ErrInvalidSendRequest) {
			t.Fatalf("unsafe header %+v error = %v", header, err)
		}
	}
	missing := base
	missing.TextBody = ""
	if _, err := NormalizeSendRequest(missing); !errors.Is(err, ErrInvalidSendRequest) {
		t.Fatalf("missing body error = %v", err)
	}
}

func TestComposeOutboundMessageOmitsBccHeaderAndParsesTextHTML(t *testing.T) {
	request := SendRequest{
		From:     Address{DisplayName: "Ashray Sender", Email: "owner@icloud.com"},
		To:       []Address{{DisplayName: "Primary", Email: "primary@example.com"}},
		CC:       []Address{{DisplayName: "Visible", Email: "copy@example.com"}},
		BCC:      []Address{{DisplayName: "Hidden", Email: "hidden@example.com"}},
		ReplyTo:  &Address{DisplayName: "Replies", Email: "reply@example.com"},
		Subject:  "Project update",
		TextBody: "Plain body café.\nSecond line.",
		HTMLBody: "<p>HTML body café.</p>",
		Headers:  []CustomHeader{{Key: "X-Synthetic", Value: "phase14"}},
	}
	var raw bytes.Buffer
	sentAt := time.Date(2026, 8, 6, 13, 0, 0, 0, time.UTC)
	if err := ComposeOutboundMessage(&raw, request, "phase14@example.test", sentAt); err != nil {
		t.Fatal(err)
	}
	text := raw.String()
	for _, value := range []string{
		"From: \"Ashray Sender\" <owner@icloud.com>",
		"To: \"Primary\" <primary@example.com>",
		"Cc: \"Visible\" <copy@example.com>",
		"Reply-To: \"Replies\" <reply@example.com>",
		"Message-Id: <phase14@example.test>",
		"X-Synthetic: phase14",
		"multipart/alternative",
	} {
		if !strings.Contains(text, value) {
			t.Fatalf("raw message missing %q:\n%s", value, text)
		}
	}
	if strings.Contains(strings.ToLower(text), "\nbcc:") || strings.Contains(text, "hidden@example.com") {
		t.Fatalf("raw message exposed Bcc:\n%s", text)
	}
	content, err := ParseNormalized(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	if content.Preview.RFCMessageID != "phase14@example.test" || content.Preview.From.DisplayName != "Ashray Sender" || content.Preview.From.Email != "owner@icloud.com" {
		t.Fatalf("parsed preview = %+v", content.Preview)
	}
	if len(content.Preview.To) != 1 || len(content.Preview.CC) != 1 || len(content.Preview.BCC) != 0 || len(content.TextPaths) != 1 || len(content.HTMLPaths) != 1 {
		t.Fatalf("parsed content = %+v", content)
	}
}
