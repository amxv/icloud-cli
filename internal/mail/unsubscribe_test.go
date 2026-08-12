package mail

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestParseUnsubscribeCapabilityOneClickAndMailtoRedactsTargets(t *testing.T) {
	capability := ParseUnsubscribeCapability([]HeaderField{
		{Key: "List-Unsubscribe", Value: "<https://lists.example.test/u/token-SECRET>, <mailto:leave@example.test?subject=unsubscribe&body=leave%20me>"},
		{Key: "List-Unsubscribe-Post", Value: "List-Unsubscribe=One-Click"},
	})
	if !capability.Available || !capability.OneClickDeclared || capability.SelectionRequired || len(capability.Methods) != 2 {
		t.Fatalf("capability=%+v", capability)
	}
	oneClick, err := SelectUnsubscribeMethod(capability, "")
	if err != nil || oneClick.Kind != UnsubscribeMethodOneClick || oneClick.Host != "lists.example.test" || oneClick.URI == "" || !oneClick.DKIMRequired {
		t.Fatalf("one-click=%+v err=%v", oneClick, err)
	}
	encoded := strings.ToLower(string(mustJSON(t, capability)))
	if strings.Contains(encoded, "secret") || strings.Contains(encoded, "leave@example.test") || strings.Contains(encoded, "leave%20me") {
		t.Fatalf("public capability leaked target: %s", encoded)
	}
}

func TestParseUnsubscribeCapabilityRequiresExplicitMailtoSelection(t *testing.T) {
	capability := ParseUnsubscribeCapability([]HeaderField{{Key: "List-Unsubscribe", Value: "<mailto:a@example.test>, <mailto:b@example.test?subject=remove>"}})
	if !capability.Available || !capability.SelectionRequired || len(capability.Methods) != 2 {
		t.Fatalf("capability=%+v", capability)
	}
	if _, err := SelectUnsubscribeMethod(capability, ""); err != ErrUnsubscribeSelection {
		t.Fatalf("selection err=%v", err)
	}
	method, err := SelectUnsubscribeMethod(capability, "mailto:2")
	if err != nil || method.Kind != UnsubscribeMethodMailto || method.Host != "example.test" || method.MailtoSubject != "remove" {
		t.Fatalf("method=%+v err=%v", method, err)
	}
}

func TestParseUnsubscribeCapabilityRejectsUnsafeOrNoncompliantAlternatives(t *testing.T) {
	cases := [][]HeaderField{
		{{Key: "List-Unsubscribe", Value: "<http://example.test/remove>"}, {Key: "List-Unsubscribe-Post", Value: "List-Unsubscribe=One-Click"}},
		{{Key: "List-Unsubscribe", Value: "<https://a.example/u>, <https://b.example/u>"}, {Key: "List-Unsubscribe-Post", Value: "List-Unsubscribe=One-Click"}},
		{{Key: "List-Unsubscribe", Value: "<mailto:list@example.test?bcc=victim@example.net>"}},
		{{Key: "List-Unsubscribe", Value: "<mailto:list@example.test?subject=ok%0d%0aBcc:evil@example.net>"}},
		{{Key: "List-Unsubscribe", Value: "<mailto:list@example.test?subject=one&Subject=two>"}},
		{{Key: "List-Unsubscribe", Value: "<https://user:pass@example.test/u>"}, {Key: "List-Unsubscribe-Post", Value: "List-Unsubscribe=One-Click"}},
	}
	for index, headers := range cases {
		capability := ParseUnsubscribeCapability(headers)
		if capability.Available {
			t.Fatalf("case %d unexpectedly available: %+v", index, capability)
		}
	}
}

func TestParseUnsubscribeCapabilityFailsClosedOnTooManyMethods(t *testing.T) {
	values := make([]string, 0, maxUnsubscribeMethods+1)
	for index := 0; index < maxUnsubscribeMethods+1; index++ {
		values = append(values, fmt.Sprintf("<mailto:list-%d@example.test>", index))
	}
	capability := ParseUnsubscribeCapability([]HeaderField{{Key: "List-Unsubscribe", Value: strings.Join(values, ", ")}})
	if capability.Available || len(capability.Methods) != 0 {
		t.Fatalf("oversized unsubscribe method set was not rejected: %+v", capability)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
