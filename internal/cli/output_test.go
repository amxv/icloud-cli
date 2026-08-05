package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCompactRendererOmitsDefaultsAndBoundsExcerpt(t *testing.T) {
	var out bytes.Buffer
	renderer := NewCompactTextRenderer(&out)
	longBody := strings.Repeat("word ", 100)
	err := renderer.Record("msg", "msg_01",
		Field("unread", true),
		Field("attachments", 0),
		Field("subject", "Launch notes"),
		Field("snippet", Excerpt(longBody, 24)),
		Field("empty", ""),
	)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "attachments=") || strings.Contains(got, "empty=") {
		t.Fatalf("default fields were not omitted: %q", got)
	}
	if strings.Count(got, "snippet=") != 1 || !strings.Contains(got, "…") {
		t.Fatalf("excerpt was not bounded: %q", got)
	}
	for _, unwanted := range []string{"{", "}", "\x1b[", "│"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("compact output contains %q: %q", unwanted, got)
		}
	}
}

func TestCompactSummaryAndEventPrimitives(t *testing.T) {
	var out bytes.Buffer
	renderer := NewCompactTextRenderer(&out)
	if err := renderer.Summary("sync", Field("messages", 3), Field("next_cursor", "cur_02")); err != nil {
		t.Fatal(err)
	}
	if err := renderer.Event("started", 1, Field("account", "acct_01")); err != nil {
		t.Fatal(err)
	}
	const want = "sync messages=3 next_cursor=cur_02\nevent started sequence=1 account=acct_01\n"
	if out.String() != want {
		t.Fatalf("compact primitives mismatch\nwant: %q\n got: %q", want, out.String())
	}
}

func TestValidateOutputOptionsRejectsUnsupportedAndAmbiguousModes(t *testing.T) {
	tests := []OutputOptions{
		{JSON: true, JSONL: true},
		{Full: true},
		{Format: "html"},
		{Headers: true, BodyOnly: true},
		{Format: "raw", Headers: true},
	}
	for _, options := range tests {
		if err := ValidateOutputOptions(options, OutputSupport{JSON: true}); err == nil {
			t.Fatalf("expected error for %+v", options)
		}
	}
	if err := ValidateOutputOptions(
		OutputOptions{JSON: true, Full: true, Format: "text"},
		OutputSupport{JSON: true, Full: true, Text: true},
	); err != nil {
		t.Fatalf("valid orthogonal modes rejected: %v", err)
	}
}

func TestJSONEnvelopeAndJSONLPrimitives(t *testing.T) {
	var envelope bytes.Buffer
	err := WriteJSON(&envelope, OutputEnvelope{
		OK:   true,
		Data: map[string]any{"version": "dev"},
		Meta: EnvelopeMeta{RequestID: "req_test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(envelope.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid envelope: %v", err)
	}

	var stream bytes.Buffer
	err = WriteJSONLEvent(&stream, JSONLEvent{
		Event:     "started",
		Sequence:  1,
		AccountID: "acct_01",
		Timestamp: time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC),
		Payload:   map[string]any{"mode": "idle"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(stream.String(), "\n") != 1 {
		t.Fatalf("JSONL must be one object per line: %q", stream.String())
	}
	if err := json.Unmarshal(bytes.TrimSpace(stream.Bytes()), &decoded); err != nil {
		t.Fatalf("invalid JSONL event: %v", err)
	}
}
