package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestInboxCompactTwentyRecordBudgetAndPrivacy(t *testing.T) {
	var output bytes.Buffer
	renderer := NewCompactTextRenderer(&output)
	for index := 0; index < 20; index++ {
		when := time.Date(2026, 8, 6, 1, index, 0, 0, time.UTC)
		message := maildomain.Message{
			ID:              fmt.Sprintf("msg_%032d", index),
			From:            maildomain.Address{Email: "sender@example.test"},
			To:              []maildomain.Address{{Email: "receiver@icloud.com"}},
			BCC:             []maildomain.Address{{Email: "hidden-bcc@example.test"}},
			Subject:         strings.Repeat("subject ", 20),
			Snippet:         strings.Repeat("bounded preview text ", 80),
			SortAt:          &when,
			Unread:          index%2 == 0,
			Flagged:         index%3 == 0,
			AttachmentCount: index % 4,
		}
		if err := renderInboxMessage(renderer, message); err != nil {
			t.Fatal(err)
		}
	}
	if err := renderer.Summary("inbox", Field("account", "acct_fixture"), Field("count", "20"), Field("more", "true")); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if lines := strings.Count(text, "\n"); lines != 21 {
		t.Fatalf("lines = %d", lines)
	}
	if len(text) > 13000 {
		t.Fatalf("compact Inbox bytes = %d", len(text))
	}
	if strings.Contains(text, "hidden-bcc") || strings.Contains(text, "raw_storage") || strings.Contains(text, "raw_content") {
		t.Fatalf("compact Inbox exposed private/internal metadata: %q", text)
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if len(line) > 620 {
			t.Fatalf("line exceeds byte budget: %d", len(line))
		}
	}
}

func TestSanitizeMessageOmitsBCCAndRawBlobCoordinates(t *testing.T) {
	value := sanitizeMessage(maildomain.Message{
		ID:   "msg_fixture",
		From: maildomain.Address{Email: "sender@example.test"},
		BCC:  []maildomain.Address{{Email: "hidden-bcc@example.test"}},
		Locations: []maildomain.Location{{
			ID: "loc_fixture", FolderID: "fld_fixture", UIDValidity: 1, UID: 2,
			RawContentHash: strings.Repeat("a", 64), RawStorageKey: "raw/sha256/aa/private.eml", Present: true,
		}},
	})
	if len(value.Locations) != 1 || value.Locations[0].UID != 2 {
		t.Fatalf("sanitized locations = %+v", value.Locations)
	}
	encoded := fmt.Sprintf("%+v", value)
	if strings.Contains(encoded, "hidden-bcc") || strings.Contains(encoded, "private.eml") || strings.Contains(encoded, strings.Repeat("a", 64)) {
		t.Fatalf("sanitized message leaked private/internal values: %s", encoded)
	}
}
