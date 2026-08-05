package id

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNewReturnsPrefixedUUIDv7(t *testing.T) {
	t.Parallel()
	first, err := New("acct")
	if err != nil {
		t.Fatal(err)
	}
	second, err := New("acct")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("IDs must be unique")
	}
	for _, value := range []string{first, second} {
		if !strings.HasPrefix(value, "acct_") {
			t.Fatalf("unexpected prefix: %q", value)
		}
		parsed, err := uuid.Parse(strings.TrimPrefix(value, "acct_"))
		if err != nil {
			t.Fatalf("parse %q: %v", value, err)
		}
		if parsed.Version() != 7 {
			t.Fatalf("version = %d", parsed.Version())
		}
	}
}

func TestNewRejectsInvalidPrefix(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"", "A", "account_id", "a", strings.Repeat("a", 17)} {
		if _, err := New(prefix); err == nil {
			t.Fatalf("expected error for %q", prefix)
		}
	}
}
