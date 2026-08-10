package mail

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRewriteRawWithoutPartsRemovesExactDuplicateAndNestedPaths(t *testing.T) {
	raw := strings.Join([]string{
		"From: sender@example.test",
		"To: owner@icloud.com",
		"Message-ID: <rewrite@example.test>",
		"Date: Mon, 10 Aug 2026 16:00:00 +0000",
		"Subject: Rewrite fixture",
		"Received: by transit.example.test",
		"DKIM-Signature: invalid-after-rewrite",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="outer"`,
		"",
		"--outer",
		`Content-Type: text/plain; charset="utf-8"`,
		"",
		"keep body",
		"--outer",
		`Content-Type: application/octet-stream; name="duplicate.bin"`,
		`Content-Disposition: attachment; filename="duplicate.bin"`,
		"Content-Transfer-Encoding: base64",
		"",
		"S0VFUC1C",
		"--outer",
		`Content-Type: application/octet-stream; name="duplicate.bin"`,
		`Content-Disposition: attachment; filename="duplicate.bin"`,
		"Content-Transfer-Encoding: base64",
		"",
		"S0VFUC1C",
		"--outer",
		`Content-Type: message/rfc822; name="nested.eml"`,
		`Content-Disposition: attachment; filename="nested.eml"`,
		"",
		"From: nested@example.test",
		"To: owner@icloud.com",
		"Message-ID: <nested-rewrite@example.test>",
		"Subject: Nested rewrite",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="inner"`,
		"",
		"--inner",
		`Content-Type: text/plain; charset="utf-8"`,
		"",
		"nested body survives",
		"--inner",
		`Content-Type: image/png; name="inner.png"`,
		`Content-Disposition: inline; filename="inner.png"`,
		"Content-ID: <inner@example.test>",
		"Content-Transfer-Encoding: base64",
		"",
		"UkVNT1ZFLUlOTkVS",
		"--inner--",
		"",
		"--outer--",
		"",
	}, "\r\n")

	before, err := ParseNormalizedContext(context.Background(), strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Attachments) != 4 {
		t.Fatalf("before attachments=%+v", before.Attachments)
	}
	if before.Attachments[0].PartPath != "2" || before.Attachments[1].PartPath != "3" || before.Attachments[2].PartPath != "4" || before.Attachments[3].PartPath != "4.1.2" {
		t.Fatalf("unexpected canonical paths=%+v", before.Attachments)
	}

	var rewritten bytes.Buffer
	if err := RewriteRawWithoutParts(context.Background(), strings.NewReader(raw), map[string]struct{}{"2": {}, "4.1.2": {}}, &rewritten); err != nil {
		t.Fatal(err)
	}
	text := rewritten.String()
	for _, forbidden := range []string{"Received:", "DKIM-Signature:"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("rewritten raw retained %q: %s", forbidden, text)
		}
	}
	for _, want := range []string{"keep body", "Message-ID: <rewrite@example.test>", "Date: Mon, 10 Aug 2026 16:00:00 +0000"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rewritten raw missing %q: %s", want, text)
		}
	}

	after, err := ParseNormalizedContext(context.Background(), bytes.NewReader(rewritten.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if after.Preview.RFCMessageID != "rewrite@example.test" || after.Preview.Subject != "Rewrite fixture" || len(after.Attachments) != 2 {
		t.Fatalf("after preview=%+v attachments=%+v", after.Preview, after.Attachments)
	}
	if after.Attachments[0].Filename != "duplicate.bin" || after.Attachments[1].Filename != "nested.eml" {
		t.Fatalf("remaining attachments=%+v", after.Attachments)
	}
}

func TestRewriteRawWithoutPartsCanRemoveRootAttachment(t *testing.T) {
	raw := "From: sender@example.test\r\nTo: owner@icloud.com\r\nMessage-ID: <root-attachment@example.test>\r\nSubject: Root attachment\r\nContent-Type: application/pdf; name=only.pdf\r\nContent-Disposition: attachment; filename=only.pdf\r\n\r\nPDF bytes"
	var rewritten bytes.Buffer
	if err := RewriteRawWithoutParts(context.Background(), strings.NewReader(raw), map[string]struct{}{"0": {}}, &rewritten); err != nil {
		t.Fatal(err)
	}
	after, err := ParseNormalizedContext(context.Background(), bytes.NewReader(rewritten.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if after.Preview.RFCMessageID != "root-attachment@example.test" || after.Preview.AttachmentCount != 0 || len(after.Attachments) != 0 {
		t.Fatalf("rewritten root attachment=%+v attachments=%+v", after.Preview, after.Attachments)
	}
}
