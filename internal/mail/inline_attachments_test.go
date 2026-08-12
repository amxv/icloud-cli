package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
	"time"
)

func TestComposeRelatedInlineCIDWithOrdinaryAttachment(t *testing.T) {
	inlineBytes := bytes.Repeat([]byte{0x89, 'P', 'N', 'G', '\n'}, 40000)
	ordinaryBytes := bytes.Repeat([]byte("ordinary-attachment-token\n"), 60000)
	inlineSum := sha256.Sum256(inlineBytes)
	ordinarySum := sha256.Sum256(ordinaryBytes)
	inlineReader := &boundedAttachmentReader{reader: bytes.NewReader(inlineBytes), maximumRead: 64 << 10}
	ordinaryReader := &boundedAttachmentReader{reader: bytes.NewReader(ordinaryBytes), maximumRead: 64 << 10}

	request := SendRequest{
		From: Address{Email: "owner@icloud.com"}, To: []Address{{Email: "recipient@example.test"}},
		Subject: "Inline related structure", TextBody: "Plain fallback.",
		HTMLBody: `<html><body><p>Inline image:</p><img src="cid:hero@example.test"></body></html>`,
		Attachments: []OutboundAttachment{
			{
				Filename: "report.bin", MediaType: "application/octet-stream",
				Disposition: AttachmentDispositionAttachment, ContentHash: hex.EncodeToString(ordinarySum[:]), ByteSize: int64(len(ordinaryBytes)),
				Open: func() (io.ReadCloser, error) { return io.NopCloser(ordinaryReader), nil },
			},
			{
				Filename: "hero.png", MediaType: "image/png", Disposition: AttachmentDispositionInline,
				ContentID: "hero@example.test", ContentHash: hex.EncodeToString(inlineSum[:]), ByteSize: int64(len(inlineBytes)),
				Open: func() (io.ReadCloser, error) { return io.NopCloser(inlineReader), nil },
			},
		},
	}
	var raw bytes.Buffer
	if err := ComposeOutboundMessage(&raw, request, "inlineAttachment-structure@example.test", time.Date(2026, 8, 10, 16, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if inlineReader.largestRead > inlineReader.maximumRead || ordinaryReader.largestRead > ordinaryReader.maximumRead {
		t.Fatalf("attachment streaming buffers inline=%d ordinary=%d", inlineReader.largestRead, ordinaryReader.largestRead)
	}
	text := raw.String()
	for _, want := range []string{"multipart/mixed", "multipart/related", "multipart/alternative", "Content-Id: <hero@example.test>", "Content-Disposition: inline", "Content-Disposition: attachment"} {
		if !strings.Contains(text, want) {
			t.Fatalf("composed MIME missing %q: %s", want, text[:min(len(text), 8192)])
		}
	}
	content, err := ParseNormalizedWithAttachmentsContext(context.Background(), bytes.NewReader(raw.Bytes()), func(_ context.Context, _ MIMEPart, body io.Reader) (AttachmentBlob, error) {
		hash := sha256.New()
		size, copyErr := io.Copy(hash, body)
		return AttachmentBlob{ContentHash: hex.EncodeToString(hash.Sum(nil)), StorageKey: "inlineAttachment/test", ByteSize: size}, copyErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(content.TextPaths) != 1 || len(content.HTMLPaths) != 1 || len(content.Attachments) != 2 {
		t.Fatalf("parsed content paths text=%v html=%v attachments=%+v", content.TextPaths, content.HTMLPaths, content.Attachments)
	}
	inline := content.Attachments[0]
	ordinary := content.Attachments[1]
	if inline.Filename != "hero.png" || inline.Disposition != "inline" || !inline.Inline || inline.ContentID != "hero@example.test" || inline.ContentHash != hex.EncodeToString(inlineSum[:]) || inline.ByteSize != int64(len(inlineBytes)) {
		t.Fatalf("parsed inline attachment=%+v", inline)
	}
	if ordinary.Filename != "report.bin" || ordinary.Disposition != "attachment" || ordinary.Inline || ordinary.ContentID != "" || ordinary.ContentHash != hex.EncodeToString(ordinarySum[:]) || ordinary.ByteSize != int64(len(ordinaryBytes)) {
		t.Fatalf("parsed ordinary attachment=%+v", ordinary)
	}
}

func TestInlineCIDNormalizationAndRequestHash(t *testing.T) {
	base := SendRequest{
		From: Address{Email: "owner@icloud.com"}, To: []Address{{Email: "recipient@example.test"}},
		Subject: "CID hash", HTMLBody: `<img src="cid:inline">`,
		Attachments: []OutboundAttachment{{
			Filename: "inline.png", MediaType: "image/png", Disposition: AttachmentDispositionInline,
			ContentHash: strings.Repeat("ab", sha256.Size), ByteSize: 12,
		}},
	}
	generated, err := NormalizeSendRequest(base)
	if err != nil {
		t.Fatal(err)
	}
	wantCID := "inline-01-" + strings.Repeat("ab", 12) + "@icloud-cli.local"
	if generated.Attachments[0].Disposition != AttachmentDispositionInline || generated.Attachments[0].ContentID != wantCID {
		t.Fatalf("generated inline metadata=%+v want_cid=%q", generated.Attachments[0], wantCID)
	}
	generatedAgain, err := NormalizeSendRequest(base)
	if err != nil || generatedAgain.Attachments[0].ContentID != wantCID {
		t.Fatalf("generated CID is not deterministic: %+v err=%v", generatedAgain.Attachments, err)
	}
	firstHash, err := SendRequestHash(generated)
	if err != nil {
		t.Fatal(err)
	}
	changed := base
	changed.Attachments = append([]OutboundAttachment(nil), base.Attachments...)
	changed.Attachments[0].ContentID = "changed@example.test"
	changed, err = NormalizeSendRequest(changed)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := SendRequestHash(changed)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash == secondHash {
		t.Fatal("changing Content-ID did not change normalized request identity")
	}

	for _, mutate := range []func(*SendRequest){
		func(value *SendRequest) { value.Attachments[0].Disposition = "sideways" },
		func(value *SendRequest) { value.Attachments[0].ContentID = "bad content id@example.test" },
		func(value *SendRequest) {
			value.Attachments = append(value.Attachments, value.Attachments[0])
			value.Attachments[0].ContentID = "duplicate@example.test"
			value.Attachments[1].ContentID = "DUPLICATE@example.test"
		},
	} {
		candidate := base
		candidate.Attachments = append([]OutboundAttachment(nil), base.Attachments...)
		mutate(&candidate)
		if _, err := NormalizeSendRequest(candidate); err == nil {
			t.Fatalf("unsafe inline metadata unexpectedly normalized: %+v", candidate.Attachments)
		}
	}
}
