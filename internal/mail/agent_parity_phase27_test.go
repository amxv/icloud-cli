package mail

import (
	"errors"
	"strings"
	"testing"
)

func TestAgentParityPhase27SignatureExpansionTextHTMLEmptyAndHashTruth(t *testing.T) {
	signature := Signature{ID: "sig_phase27", TextBody: "Thanks,\nAlex", HTMLBody: "<p>Thanks,<br>Alex</p>"}

	textOnly := ApplySignature(SendRequest{TextBody: "Hello"}, signature)
	if textOnly.TextBody != "Hello\n\nThanks,\nAlex" || textOnly.HTMLBody != "" || !textOnly.SignatureExpanded || textOnly.AppliedSignatureID != signature.ID || textOnly.SignatureSelector != "" {
		t.Fatalf("text-only expansion=%+v", textOnly)
	}

	multipart := ApplySignature(SendRequest{TextBody: "Hello", HTMLBody: "<p>Hello</p>"}, signature)
	if multipart.TextBody != "Hello\n\nThanks,\nAlex" || multipart.HTMLBody != "<p>Hello</p><br><br><p>Thanks,<br>Alex</p>" {
		t.Fatalf("multipart expansion text=%q html=%q", multipart.TextBody, multipart.HTMLBody)
	}

	htmlOnly := ApplySignature(SendRequest{HTMLBody: "<p>Hello</p>"}, Signature{ID: "sig_text_fallback", TextBody: "A & B\n<team>"})
	if htmlOnly.TextBody != "Hello\n\nA & B\n<team>" || htmlOnly.HTMLBody != "<p>Hello</p><br><br>A &amp; B<br>&lt;team&gt;" {
		t.Fatalf("HTML-only fallback text=%q html=%q", htmlOnly.TextBody, htmlOnly.HTMLBody)
	}

	empty := ApplySignature(SendRequest{}, signature)
	if empty.TextBody != "Thanks,\nAlex" || empty.HTMLBody != "" {
		t.Fatalf("empty compose expansion=%+v", empty)
	}

	base := SendRequest{
		From: Address{Email: "owner@example.test"}, To: []Address{{Email: "recipient@example.test"}},
		Subject: "Signature hash", TextBody: "Hello", IdempotencyKey: "phase27-same-key",
	}
	first, err := NormalizeSendRequest(ApplySignature(base, Signature{ID: "sig_one", TextBody: "First"}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NormalizeSendRequest(ApplySignature(base, Signature{ID: "sig_one", TextBody: "Second"}))
	if err != nil {
		t.Fatal(err)
	}
	firstHash, err := SendRequestHash(first)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := SendRequestHash(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash == secondHash {
		t.Fatalf("expanded content did not affect request hash: %q", firstHash)
	}

	nearLimit := base
	nearLimit.TextBody = strings.Repeat("x", MaxSendBodyBytes-2)
	_, err = NormalizeSendRequest(ApplySignature(nearLimit, Signature{ID: "sig_overflow", TextBody: "sig"}))
	if !errors.Is(err, ErrInvalidSendRequest) || !strings.Contains(err.Error(), "body exceeds") {
		t.Fatalf("post-expansion body ceiling err=%v", err)
	}
}

func TestAgentParityPhase27SignatureInputAndOperationNormalization(t *testing.T) {
	input, err := NormalizeSignatureInput(SignatureInput{IdentityID: " mid_123 ", TextBody: "Line 1\r\nLine 2\r\n", HTMLBody: "<p>x</p>\r\n"})
	if err != nil || input.IdentityID != "mid_123" || input.TextBody != "Line 1\nLine 2" || input.HTMLBody != "<p>x</p>" {
		t.Fatalf("normalized signature=%+v err=%v", input, err)
	}
	if _, err := NormalizeSignatureInput(SignatureInput{TextBody: ""}); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("empty signature err=%v", err)
	}
	for raw, want := range map[string]OutboundKind{"new": OutboundKindSend, "reply": OutboundKindReply, "reply-all": OutboundKindReplyAll, "forward": OutboundKindForward} {
		got, err := NormalizeSignatureOperation(OutboundKind(raw))
		if err != nil || got != want {
			t.Fatalf("operation %q=%q err=%v", raw, got, err)
		}
	}
}
