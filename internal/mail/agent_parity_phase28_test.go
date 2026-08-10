package mail

import (
	"reflect"
	"testing"
)

func TestAgentParityPhase28KeepsUnprovenProviderFlagMetadataOpaque(t *testing.T) {
	flags := ParseLocationFlags([]string{`\Flagged`, "$Phase28Opaque", "$phase28opaque"})
	if !flags.Flagged {
		t.Fatal("portable \\Flagged state was not preserved")
	}
	if !reflect.DeepEqual(flags.Unknown, []string{"$Phase28Opaque"}) {
		t.Fatalf("opaque provider flags=%v", flags.Unknown)
	}
	if got := flags.All(); !reflect.DeepEqual(got, []string{`\Flagged`, "$Phase28Opaque"}) {
		t.Fatalf("provider flags=%v", got)
	}
}
