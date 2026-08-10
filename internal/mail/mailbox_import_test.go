package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestDecodeMboxRDRecordRoundTripsWriterAndResumesByOffset(t *testing.T) {
	rawMessages := [][]byte{
		[]byte("From: a@example.test\r\nSubject: one\r\n\r\nFrom bare\r\n>From quoted\r\n"),
		[]byte("From: b@example.test\nSubject: two 日本語\n\n" + strings.Repeat(">", 1<<20) + "From huge\nbody\n"),
		[]byte("From: c@example.test\r\nSubject: three\r\n\r\nlast\r\n"),
	}
	var archive bytes.Buffer
	for index, raw := range rawMessages {
		if _, err := WriteMboxRDMessage(context.Background(), &archive, "sender@example.test", time.Date(2026, 8, 10, 20, index, 0, 0, time.UTC), bytes.NewReader(raw)); err != nil {
			t.Fatal(err)
		}
	}
	reader := bytes.NewReader(archive.Bytes())
	offset := int64(0)
	for index, want := range rawMessages {
		var decoded bytes.Buffer
		record, err := DecodeMboxRDRecord(context.Background(), reader, offset, &decoded)
		if err != nil {
			t.Fatalf("record %d: %v", index, err)
		}
		if !bytes.Equal(decoded.Bytes(), want) {
			t.Fatalf("record %d mismatch got=%d want=%d", index, decoded.Len(), len(want))
		}
		sum := sha256.Sum256(want)
		if record.StartOffset != offset || record.RawByteSize != int64(len(want)) || record.RawContentHash != hex.EncodeToString(sum[:]) {
			t.Fatalf("record %d metadata=%+v", index, record)
		}
		wantEnvelopeDate := time.Date(2026, 8, 10, 20, index, 0, 0, time.UTC)
		if record.EnvelopeDate == nil || !record.EnvelopeDate.Equal(wantEnvelopeDate) {
			t.Fatalf("record %d envelope_date=%v want=%v", index, record.EnvelopeDate, wantEnvelopeDate)
		}
		if index < len(rawMessages)-1 && record.EOF {
			t.Fatalf("record %d unexpectedly EOF", index)
		}
		if index == len(rawMessages)-1 && !record.EOF {
			t.Fatalf("last record not EOF: %+v", record)
		}
		offset = record.NextOffset
	}
	if offset != int64(archive.Len()) {
		t.Fatalf("final offset=%d archive=%d", offset, archive.Len())
	}
}

func TestDecodeMboxRDRecordNormalizesMissingFinalNewlineOnlyAsArchiveRequires(t *testing.T) {
	raw := []byte("From: a@example.test\nSubject: x\n\nlast")
	var archive bytes.Buffer
	if _, err := WriteMboxRDMessage(context.Background(), &archive, "sender@example.test", time.Unix(0, 0), bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	var decoded bytes.Buffer
	record, err := DecodeMboxRDRecord(context.Background(), bytes.NewReader(archive.Bytes()), 0, &decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !record.EOF || decoded.String() != string(raw)+"\n" {
		t.Fatalf("decoded=%q record=%+v", decoded.String(), record)
	}
}

func TestDecodeMboxRDRecordRejectsMalformedSeparatorAndHonorsCancellation(t *testing.T) {
	for _, value := range []string{"", "Subject: not-mbox\n\nbody\n", "From unterminated"} {
		var output bytes.Buffer
		_, err := DecodeMboxRDRecord(context.Background(), bytes.NewReader([]byte(value)), 0, &output)
		if err == nil {
			t.Fatalf("expected malformed input error for %q", value)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	_, err := DecodeMboxRDRecord(ctx, bytes.NewReader([]byte("From sender Mon Aug 10 20:00:00 2026\nSubject: x\n\nbody\n\n")), 0, &output)
	if err == nil {
		t.Fatal("expected cancellation")
	}
}
