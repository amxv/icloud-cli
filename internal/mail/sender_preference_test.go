package mail

import "testing"

func TestNormalizeSenderEmailExactAddressOnly(t *testing.T) {
	for input, want := range map[string]string{
		" Alice@Example.COM ":  "alice@example.com",
		"x.y+tag@example.test": "x.y+tag@example.test",
	} {
		got, err := NormalizeSenderEmail(input)
		if err != nil || got != want {
			t.Fatalf("NormalizeSenderEmail(%q)=%q err=%v want=%q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "Alice <alice@example.com>", "example.com", "a@example.com,b@example.com", "a@example.com\r\nBcc: x@example.com"} {
		if got, err := NormalizeSenderEmail(input); err == nil {
			t.Fatalf("NormalizeSenderEmail(%q)=%q unexpectedly succeeded", input, got)
		}
	}
}
