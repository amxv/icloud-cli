package mail

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestWriteMboxRDMessageQuotesAllMboxRDFromVariantsAndPreservesOtherBytes(t *testing.T) {
	raw := strings.Join([]string{
		"Subject: 日本語 café",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"From bare",
		">From once",
		">>>From many",
		">not-from",
		"ordinary",
		"",
	}, "\r\n")
	var output bytes.Buffer
	written, err := WriteMboxRDMessage(context.Background(), &output, "sender@example.test", time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC), strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if written != int64(output.Len()) {
		t.Fatalf("written=%d len=%d", written, output.Len())
	}
	want := "From sender@example.test Mon Aug 10 18:00:00 2026\n" + strings.Join([]string{
		"Subject: 日本語 café",
		"Content-Type: text/plain; charset=utf-8",
		"",
		">From bare",
		">>From once",
		">>>>From many",
		">not-from",
		"ordinary",
		"",
	}, "\r\n") + "\n"
	if output.String() != want {
		t.Fatalf("mboxrd output mismatch\nwant=%q\n got=%q", want, output.String())
	}
}

func TestWriteMboxRDMessageHandlesMissingFinalNewlineAndHugeQuotedPrefixBoundedly(t *testing.T) {
	raw := "Subject: x\n\n" + strings.Repeat(">", 1<<20) + "From huge\nlast"
	var output bytes.Buffer
	if _, err := WriteMboxRDMessage(context.Background(), &output, "bad sender", time.Time{}, strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "From MAILER-DAEMON Thu Jan  1 00:00:00 1970\nSubject: x\n\n") {
		t.Fatalf("unexpected separator: %.120q", output.String())
	}
	needle := "\n>" + strings.Repeat(">", 1<<20) + "From huge\nlast\n\n"
	if !strings.Contains(output.String(), needle) {
		t.Fatal("long mboxrd prefix was not reversibly quoted")
	}
}

func TestWriteMboxRDMessageHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	if _, err := WriteMboxRDMessage(ctx, &output, "sender@example.test", time.Now(), strings.NewReader("Subject: x\n\nbody\n")); err == nil {
		t.Fatal("expected cancellation")
	}
}
