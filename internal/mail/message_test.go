package mail

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParsePreviewMalformedDateNeverUsesWallClock(t *testing.T) {
	raw := strings.Join([]string{
		"From: Sender <sender@example.test>",
		"To: receiver@icloud.com",
		"Date: definitely-not-a-date",
		"Message-ID: <fixture-1@example.test>",
		"Subject: Stable fixture",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"A stable body preview.",
	}, "\r\n")
	first, err := ParsePreview(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	second, err := ParsePreview(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if first.HeaderDate != nil || second.HeaderDate != nil {
		t.Fatalf("malformed dates became timestamps: first=%v second=%v", first.HeaderDate, second.HeaderDate)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("parse changed across time: first=%+v second=%+v", first, second)
	}
	internal := time.Date(2026, 8, 6, 1, 2, 3, 0, time.UTC)
	storedInternal, sortAt, source := MessageTimes(first, internal)
	if storedInternal == nil || sortAt == nil || !sortAt.Equal(internal) || source != DateSourceInternalDate {
		t.Fatalf("fallback internal=%v sort=%v source=%q", storedInternal, sortAt, source)
	}
}

func TestParsePreviewDecodesCharsetAndCountsAttachmentsWithoutKeepingBodies(t *testing.T) {
	raw := "From: =?ISO-8859-1?Q?Andr=E9?= <andre@example.test>\r\n" +
		"To: Receiver <receiver@icloud.com>\r\n" +
		"Message-ID: <fixture-2@example.test>\r\n" +
		"Date: Wed, 05 Aug 2026 20:00:00 +0000\r\n" +
		"Subject: =?ISO-8859-1?Q?R=E9sum=E9?=\r\n" +
		"Content-Type: multipart/mixed; boundary=x\r\n\r\n" +
		"--x\r\nContent-Type: text/plain; charset=iso-8859-1\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nOl=E1 preview\r\n" +
		"--x\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=fixture.pdf\r\nContent-Transfer-Encoding: base64\r\n\r\nUERG\r\n--x--\r\n"
	parsed, err := ParsePreview(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Subject != "Résumé" || parsed.From.DisplayName != "André" || !strings.Contains(parsed.Snippet, "Olá preview") || parsed.AttachmentCount != 1 {
		t.Fatalf("parsed = %+v", parsed)
	}
}
