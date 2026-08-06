package mail

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBuildReplyPlanUsesExactParentReplyToAndNeverResurrectsBCC(t *testing.T) {
	parent := Message{
		ID: "msg_parent", RFCMessageID: "<Parent@Example.Test>",
		From:    Address{DisplayName: "Sender", Email: "sender@example.test"},
		ReplyTo: Address{DisplayName: "Replies", Email: "replies@example.test"},
		To:      []Address{{DisplayName: "Owner", Email: "OWNER@ICLOUD.COM"}},
		CC:      []Address{{DisplayName: "Visible", Email: "visible@example.test"}},
		BCC:     []Address{{DisplayName: "Hidden", Email: "hidden@example.test"}},
		Subject: "Re: Re: Status", References: []string{"<root@example.test>", "ROOT@example.test"},
	}
	plan, err := BuildReplyPlan(parent, "thr_parent", []string{"owner@icloud.com"}, ReplyRequest{
		ParentMessageID: parent.ID,
		To:              []Address{{DisplayName: "Added", Email: "added@example.test"}},
		TextBody:        "Reply body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != OutboundKindReply || !plan.Threading || plan.ParentRFCID != "parent@example.test" {
		t.Fatalf("reply plan metadata = %+v", plan)
	}
	if got := addressEmails(plan.Send.To); strings.Join(got, ",") != "replies@example.test,added@example.test" {
		t.Fatalf("reply To = %v", got)
	}
	if len(plan.Send.CC) != 0 || len(plan.Send.BCC) != 0 {
		t.Fatalf("simple reply copied prior recipients: cc=%+v bcc=%+v", plan.Send.CC, plan.Send.BCC)
	}
	if plan.Send.Subject != "Re: Status" || plan.Send.InReplyTo != "parent@example.test" ||
		strings.Join(plan.Send.References, ",") != "root@example.test,parent@example.test" {
		t.Fatalf("reply subject/threading = %+v", plan.Send)
	}
}

func TestBuildReplyPlanFallsBackToFromWhenReplyToIsAbsent(t *testing.T) {
	parent := Message{
		ID: "msg_parent", RFCMessageID: "parent@example.test",
		From:    Address{DisplayName: "Original Sender", Email: "sender@example.test"},
		To:      []Address{{Email: "owner@icloud.com"}},
		Subject: "Fallback recipient",
	}
	plan, err := BuildReplyPlan(parent, "thr_parent", []string{"owner@icloud.com"}, ReplyRequest{
		ParentMessageID: parent.ID, TextBody: "Reply body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(addressEmails(plan.Send.To), ","); got != "sender@example.test" {
		t.Fatalf("reply From fallback To = %s", got)
	}
	if len(plan.Send.CC) != 0 || len(plan.Send.BCC) != 0 {
		t.Fatalf("reply From fallback copied other lists: %+v", plan.Send)
	}
}

func TestBuildReplyAllExcludesSelfDeduplicatesAndHandlesOwnSentParent(t *testing.T) {
	parent := Message{
		ID: "msg_parent", RFCMessageID: "sent-parent@example.test",
		From: Address{DisplayName: "Owner", Email: "owner@icloud.com"},
		To: []Address{
			{DisplayName: "Primary", Email: "primary@example.test"},
			{DisplayName: "Owner duplicate", Email: "OWNER@ICLOUD.COM"},
		},
		CC: []Address{
			{DisplayName: "Primary duplicate", Email: "PRIMARY@example.test"},
			{DisplayName: "Copy", Email: "copy@example.test"},
			{DisplayName: "Configured Alias", Email: "alias@icloud.com"},
		},
		BCC:     []Address{{Email: "prior-hidden@example.test"}},
		Subject: "Topic",
	}
	plan, err := BuildReplyPlan(parent, "thr_parent", []string{"owner@icloud.com", "alias@icloud.com"}, ReplyRequest{
		ParentMessageID: parent.ID, ReplyAll: true,
		CC:       []Address{{DisplayName: "Added", Email: "added@example.test"}, {Email: "copy@example.test"}},
		BCC:      []Address{{Email: "new-hidden@example.test"}, {Email: "owner@icloud.com"}},
		TextBody: "Reply all body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(addressEmails(plan.Send.To), ","); got != "primary@example.test" {
		t.Fatalf("reply-all To = %s", got)
	}
	if got := strings.Join(addressEmails(plan.Send.CC), ","); got != "copy@example.test,added@example.test" {
		t.Fatalf("reply-all Cc = %s", got)
	}
	if got := strings.Join(addressEmails(plan.Send.BCC), ","); got != "new-hidden@example.test" {
		t.Fatalf("reply-all Bcc = %s", got)
	}
	for _, forbidden := range []string{"owner@icloud.com", "alias@icloud.com", "prior-hidden@example.test"} {
		if strings.Contains(fmt.Sprint(plan.Send), forbidden) {
			t.Fatalf("reply-all retained forbidden recipient %q: %+v", forbidden, plan.Send)
		}
	}
}

func TestBuildReplyPlanSupportsExplicitRecipientReplacement(t *testing.T) {
	parent := Message{
		ID: "msg_parent", RFCMessageID: "parent@example.test",
		From: Address{Email: "sender@example.test"},
		To:   []Address{{Email: "owner@icloud.com"}},
		CC:   []Address{{Email: "old-copy@example.test"}}, Subject: "Topic",
	}
	plan, err := BuildReplyPlan(parent, "thr_parent", []string{"owner@icloud.com"}, ReplyRequest{
		ParentMessageID: parent.ID, ReplyAll: true,
		To: []Address{{Email: "override@example.test"}}, CC: []Address{{Email: "new-copy@example.test"}},
		ReplaceTo: true, ReplaceCC: true, TextBody: "Override",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(addressEmails(plan.Send.To), ","); got != "override@example.test" {
		t.Fatalf("replacement To = %s", got)
	}
	if got := strings.Join(addressEmails(plan.Send.CC), ","); got != "new-copy@example.test" {
		t.Fatalf("replacement Cc = %s", got)
	}
}

func TestReplyThreadingRejectsMalformedParentAndBoundsReferences(t *testing.T) {
	values := []string{"not a message id", "<root@example.test>"}
	for index := 0; index < 240; index++ {
		values = append(values, fmt.Sprintf("node-%03d@references.example.test", index))
	}
	parent := Message{
		ID: "msg_parent", RFCMessageID: "malformed parent",
		From: Address{Email: "sender@example.test"}, Subject: "Topic", References: values,
	}
	plan, err := BuildReplyPlan(parent, "thr_parent", []string{"owner@icloud.com"}, ReplyRequest{
		ParentMessageID: parent.ID, TextBody: "No fabricated headers",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Threading || plan.Send.InReplyTo != "" || len(plan.Send.References) != 0 {
		t.Fatalf("malformed parent produced threading headers: %+v", plan)
	}

	chain := NormalizeReferenceChain(values, "parent@example.test")
	if len(chain) > MaxReplyReferences || referenceHeaderBytes(chain) > MaxReplyReferencesBytes || chain[len(chain)-1] != "parent@example.test" {
		t.Fatalf("bounded references count=%d bytes=%d tail=%q", len(chain), referenceHeaderBytes(chain), chain[len(chain)-1])
	}
}

func TestComposeOutboundReplyWritesCanonicalThreadingHeadersAndNoBCC(t *testing.T) {
	request := SendRequest{
		Kind: OutboundKindReply, ParentMessageID: "msg_parent",
		From: Address{Email: "owner@icloud.com"}, To: []Address{{Email: "recipient@example.test"}},
		BCC: []Address{{Email: "hidden@example.test"}}, Subject: "Re: Topic", TextBody: "Reply body",
		InReplyTo: "parent@example.test", References: []string{"root@example.test", "parent@example.test"},
	}
	var raw bytes.Buffer
	if err := ComposeOutboundMessage(&raw, request, "child@example.test", time.Date(2026, 8, 6, 18, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	text := raw.String()
	for _, want := range []string{
		"In-Reply-To: <parent@example.test>",
		"References: <root@example.test> <parent@example.test>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("raw reply missing %q: %q", want, text)
		}
	}
	if strings.Contains(text, "hidden@example.test") || strings.Contains(strings.ToLower(text), "\nbcc:") {
		t.Fatalf("raw reply exposed BCC: %q", text)
	}
}

func addressEmails(values []Address) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.Email)
	}
	return result
}
