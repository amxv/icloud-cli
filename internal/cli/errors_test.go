package cli

import (
	"errors"
	"testing"
)

func TestExitCodeClasses(t *testing.T) {
	tests := []struct {
		code ErrorCode
		want int
	}{
		{ErrorUsage, ExitUsage},
		{ErrorConfiguration, ExitConfiguration},
		{ErrorAuthentication, ExitAuthentication},
		{ErrorUnavailable, ExitUnavailable},
		{ErrorConflict, ExitConflict},
		{ErrorNotFound, ExitNotFound},
		{ErrorSafety, ExitSafety},
		{ErrorInternal, ExitInternal},
	}
	for _, test := range tests {
		if got := ExitCodeFor(NewAppError(test.code, "test", false)); got != test.want {
			t.Fatalf("code %q: got %d want %d", test.code, got, test.want)
		}
	}
}

func TestNormalizeErrorKeepsTypedDetails(t *testing.T) {
	original := NewAppError(ErrorUnavailable, "service unavailable", true)
	original.Details = map[string]any{"stage": "dial"}
	normalized := NormalizeError(original)
	if normalized.Code != ErrorUnavailable || !normalized.Retryable {
		t.Fatalf("unexpected normalized error: %+v", normalized)
	}
	if normalized.Details["stage"] != "dial" {
		t.Fatalf("details lost: %+v", normalized.Details)
	}
}

func TestNormalizeUnknownErrorRedactsCause(t *testing.T) {
	normalized := NormalizeError(errors.New("provider response contained private content"))
	if normalized.Code != ErrorInternal || normalized.Message != "unexpected internal failure" {
		t.Fatalf("unexpected normalized error: %+v", normalized)
	}
}
