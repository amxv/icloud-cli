package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestParseNormalizedHandlesCharsetsAlternativesInlineAndEmbeddedMessages(t *testing.T) {
	raw := strings.ReplaceAll(`Message-ID: <root@example.test>
Date: Thu, 06 Aug 2026 02:00:00 +0000
From: =?ISO-8859-1?Q?Jos=E9?= <jose@example.test>
To: receiver@icloud.com
Bcc: hidden@example.test
Subject: =?ISO-8859-1?Q?Ol=E1?=
Content-Type: multipart/mixed; boundary=mixed

--mixed
Content-Type: multipart/alternative; boundary=alt

--alt
Content-Type: text/plain; charset=iso-8859-1
Content-Transfer-Encoding: quoted-printable

Ol=E1, caf=E9.
--alt
Content-Type: text/html; charset=utf-8

<p>Olá, <strong>café</strong>.</p>
--alt--
--mixed
Content-Type: image/png; name=pixel.png
Content-Disposition: inline; filename=pixel.png
Content-ID: <pixel@example.test>
Content-Transfer-Encoding: base64

aW1hZ2U=
--mixed
Content-Type: message/rfc822
Content-Disposition: attachment; filename=forwarded.eml

Message-ID: <nested@example.test>
From: nested@example.test
To: receiver@icloud.com
Subject: Nested
Content-Type: text/plain; charset=utf-8

Nested body.
--mixed--
`, "\n", "\r\n")

	content, err := ParseNormalized(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if content.Preview.Subject != "Olá" || content.Preview.From.DisplayName != "José" {
		t.Fatalf("decoded header preview = %+v", content.Preview)
	}
	if !strings.Contains(content.Preview.Snippet, "Olá, café") {
		t.Fatalf("snippet = %q", content.Preview.Snippet)
	}
	if content.Preview.AttachmentCount != 2 {
		t.Fatalf("attachment count = %d", content.Preview.AttachmentCount)
	}
	if len(content.TextPaths) != 1 || len(content.HTMLPaths) != 1 {
		t.Fatalf("body paths text=%v html=%v", content.TextPaths, content.HTMLPaths)
	}
	var inline, embedded bool
	for _, part := range content.Parts {
		if part.ContentID == "pixel@example.test" && part.Inline && part.Attachment && part.DecodedSize == 5 {
			inline = true
		}
		if part.MediaType == "message/rfc822" && part.EmbeddedMessage && part.Attachment {
			embedded = true
		}
	}
	if !inline || !embedded {
		t.Fatalf("parts missing inline=%v embedded=%v: %+v", inline, embedded, content.Parts)
	}
	if len(content.Headers) == 0 || content.Headers[0].Key != "Message-Id" {
		t.Fatalf("canonical headers = %+v", content.Headers)
	}
}

func TestParseNormalizedMalformedDateIsStableAndUnknownEncodingIsPartial(t *testing.T) {
	raw := strings.ReplaceAll(`Message-ID: <partial@example.test>
Date: not-a-date
From: sender@example.test
To: receiver@icloud.com
Subject: Partial
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: x-unknown

Preserved undecoded body.
`, "\n", "\r\n")
	first, err := ParseNormalized(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseNormalized(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if first.Preview.HeaderDate != nil || second.Preview.HeaderDate != nil {
		t.Fatalf("malformed Date gained a wall clock: first=%v second=%v", first.Preview.HeaderDate, second.Preview.HeaderDate)
	}
	if first.Preview.Snippet != second.Preview.Snippet {
		t.Fatalf("parse changed across reads: %q != %q", first.Preview.Snippet, second.Preview.Snippet)
	}
	found := false
	for _, diagnostic := range first.Diagnostics {
		if diagnostic.Code == "unknown_encoding" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unknown encoding diagnostics = %+v", first.Diagnostics)
	}
}

func TestWriteSelectedBodyStreamsLargeTextAndHTMLFallback(t *testing.T) {
	large := strings.Repeat("0123456789abcdef", 131072)
	rawText := fmt.Sprintf("Message-ID: <large@example.test>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s", large)
	content, err := ParseNormalized(strings.NewReader(rawText))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := WriteSelectedBody(context.Background(), strings.NewReader(rawText), ContentText, content.TextPaths, &output); err != nil {
		t.Fatal(err)
	}
	if output.Len() != len(large) || sha256.Sum256(output.Bytes()) != sha256.Sum256([]byte(large)) {
		t.Fatalf("large streamed body len=%d", output.Len())
	}

	rawHTML := "Message-ID: <html@example.test>\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>Hello <strong>world</strong> &amp; friends.</p>"
	htmlContent, err := ParseNormalized(strings.NewReader(rawHTML))
	if err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := WriteSelectedBody(context.Background(), strings.NewReader(rawHTML), ContentText, htmlContent.HTMLPaths, &output); err != nil {
		t.Fatal(err)
	}
	if value := strings.Join(strings.Fields(output.String()), " "); value != "Hello world & friends." {
		t.Fatalf("HTML text fallback = %q", value)
	}
}

func TestParseNormalizedHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ParseNormalizedContext(ctx, strings.NewReader("Message-ID: <cancel@example.test>\r\n\r\nbody"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled parse = %v", err)
	}
}
