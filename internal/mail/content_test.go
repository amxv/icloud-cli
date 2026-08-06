package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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

func TestParseNormalizedStreamsAttachmentsAndReopensEmbeddedMessage(t *testing.T) {
	raw := strings.ReplaceAll(`Message-ID: <root@example.test>
Content-Type: multipart/mixed; boundary=mixed

--mixed
Content-Type: text/plain; charset=utf-8

Body.
--mixed
Content-Type: application/octet-stream; name="../unsafe.bin"
Content-Disposition: attachment; filename="../unsafe.bin"
Content-Transfer-Encoding: base64

AAEC
--mixed
Content-Type: message/rfc822; name=forwarded.eml
Content-Disposition: attachment; filename=forwarded.eml

Message-ID: <nested@example.test>
Content-Type: text/plain; charset=utf-8

Nested body.
--mixed--
`, "\n", "\r\n")
	stored := map[string][]byte{}
	content, err := ParseNormalizedWithAttachmentsContext(context.Background(), strings.NewReader(raw), func(_ context.Context, part MIMEPart, reader io.Reader) (AttachmentBlob, error) {
		value, err := io.ReadAll(reader)
		if err != nil {
			return AttachmentBlob{}, err
		}
		stored[part.Path] = append([]byte(nil), value...)
		hash := sha256.Sum256(value)
		return AttachmentBlob{
			ContentHash: hex.EncodeToString(hash[:]),
			StorageKey:  "attachments/" + part.Path,
			ByteSize:    int64(len(value)),
			Open: func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(value)), nil
			},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !content.AttachmentsIndexed || len(content.Attachments) != 2 {
		t.Fatalf("indexed=%v attachments=%+v", content.AttachmentsIndexed, content.Attachments)
	}
	if got := stored["2"]; !bytes.Equal(got, []byte{0, 1, 2}) {
		t.Fatalf("binary attachment = %v", got)
	}
	if got := string(stored["3"]); !strings.Contains(got, "Nested body.") {
		t.Fatalf("embedded attachment = %q", got)
	}
	foundNestedBody := false
	for _, part := range content.Parts {
		if part.Path == "3.1" && part.EmbeddedMessage && part.DecodedSize > 0 {
			foundNestedBody = true
		}
	}
	if !foundNestedBody {
		t.Fatalf("embedded MIME tree = %+v", content.Parts)
	}
	if content.Attachments[0].Filename != "../unsafe.bin" || content.Attachments[0].Availability != AttachmentAvailable {
		t.Fatalf("attachment metadata = %+v", content.Attachments[0])
	}
}

func TestParseNormalizedPropagatesAttachmentSinkFailure(t *testing.T) {
	raw := "Content-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=x.bin\r\n\r\ndata"
	want := errors.New("disk full")
	_, err := ParseNormalizedWithAttachmentsContext(context.Background(), strings.NewReader(raw), func(context.Context, MIMEPart, io.Reader) (AttachmentBlob, error) {
		return AttachmentBlob{}, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("attachment sink error = %v", err)
	}
}

func TestParseNormalizedStreamsZeroAndLargeAttachments(t *testing.T) {
	large := bytes.Repeat([]byte("0123456789abcdef"), 131072)
	raw := strings.Join([]string{
		"Message-ID: <large-attachments@example.test>",
		"Content-Type: multipart/mixed; boundary=x", "",
		"--x", "Content-Type: application/octet-stream", "Content-Disposition: attachment; filename=empty.bin", "", "",
		"--x", "Content-Type: application/octet-stream", "Content-Disposition: attachment; filename=large.bin", "Content-Transfer-Encoding: base64", "",
		base64.StdEncoding.EncodeToString(large),
		"--x--", "",
	}, "\r\n")
	sizes := map[string]int64{}
	content, err := ParseNormalizedWithAttachmentsContext(context.Background(), strings.NewReader(raw), func(_ context.Context, part MIMEPart, reader io.Reader) (AttachmentBlob, error) {
		hash := sha256.New()
		size, err := io.Copy(hash, reader)
		if err != nil {
			return AttachmentBlob{}, err
		}
		sizes[part.Filename] = size
		return AttachmentBlob{ContentHash: hex.EncodeToString(hash.Sum(nil)), StorageKey: "attachments/" + part.Filename, ByteSize: size}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if sizes["empty.bin"] != 0 || sizes["large.bin"] != int64(len(large)) || len(content.Attachments) != 2 {
		t.Fatalf("sizes=%v attachments=%+v", sizes, content.Attachments)
	}
	for _, attachment := range content.Attachments {
		if attachment.Availability != AttachmentAvailable || len(attachment.ContentHash) != 64 {
			t.Fatalf("attachment availability = %+v", attachment)
		}
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
