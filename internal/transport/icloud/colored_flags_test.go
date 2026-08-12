package icloud

import "testing"

func TestDoesNotGuessColoredFlagKeywords(t *testing.T) {
	if flag, ok := mutableFlag(`\Flagged`); !ok || string(flag) != `\Flagged` {
		t.Fatalf("portable flagged mutation=%q ok=%t", flag, ok)
	}
	for _, candidate := range []string{"$ColoredFlagOpaque", "red", "orange"} {
		if flag, ok := mutableFlag(candidate); ok {
			t.Fatalf("unproven provider color %q became writable as %q", candidate, flag)
		}
	}
}
