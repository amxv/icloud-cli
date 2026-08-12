package mail

import (
	"reflect"
	"testing"
)

func TestKeepsUnprovenProviderFlagMetadataOpaque(t *testing.T) {
	flags := ParseLocationFlags([]string{`\Flagged`, "$ColoredFlagOpaque", "$coloredFlagopaque"})
	if !flags.Flagged {
		t.Fatal("portable \\Flagged state was not preserved")
	}
	if !reflect.DeepEqual(flags.Unknown, []string{"$ColoredFlagOpaque"}) {
		t.Fatalf("opaque provider flags=%v", flags.Unknown)
	}
	if got := flags.All(); !reflect.DeepEqual(got, []string{`\Flagged`, "$ColoredFlagOpaque"}) {
		t.Fatalf("provider flags=%v", got)
	}
}
