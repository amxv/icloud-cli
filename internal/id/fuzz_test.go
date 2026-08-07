package id

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func FuzzNewAcceptsOnlyCanonicalPrefixesAndReturnsUUIDv7(f *testing.F) {
	for _, seed := range []string{"acct", "msg", "thr", "a1", "A", "a", "account_id", strings.Repeat("x", 16)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, prefix string) {
		if len(prefix) > 1<<10 {
			t.Skip()
		}
		value, err := New(prefix)
		trimmed := strings.TrimSpace(prefix)
		valid := prefixPattern.MatchString(trimmed)
		if !valid {
			if err == nil {
				t.Fatalf("invalid prefix %q produced ID %q", prefix, value)
			}
			return
		}
		if err != nil {
			t.Fatalf("valid prefix %q rejected: %v", prefix, err)
		}
		marker := trimmed + "_"
		if !strings.HasPrefix(value, marker) {
			t.Fatalf("ID %q does not retain canonical prefix %q", value, trimmed)
		}
		parsed, parseErr := uuid.Parse(strings.TrimPrefix(value, marker))
		if parseErr != nil || parsed.Version() != 7 {
			t.Fatalf("ID %q is not UUIDv7: parsed=%v err=%v", value, parsed, parseErr)
		}
	})
}
