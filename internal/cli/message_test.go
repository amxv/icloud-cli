package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestMessageCompactBlockIsBoundedAndHintsAtExplicitContent(t *testing.T) {
	when := time.Date(2026, 8, 6, 5, 45, 0, 0, time.UTC)
	message := maildomain.Message{
		ID:              "msg_0198d704-6c00-7000-8000-000000000701",
		From:            maildomain.Address{DisplayName: "Fixture Sender", Email: "sender@example.test"},
		To:              []maildomain.Address{{Email: "receiver@icloud.com"}},
		CC:              []maildomain.Address{{Email: "copy@example.test"}},
		Subject:         strings.Repeat("Long subject ", 20),
		Snippet:         strings.Repeat("large decoded body preview ", 200),
		SortAt:          &when,
		DateSource:      maildomain.DateSourceHeader,
		Unread:          true,
		AttachmentCount: 2,
		Locations: []maildomain.Location{{
			ID: "loc_0198d704-6c00-7000-8000-000000000702", FolderID: "fld_fixture", Present: true,
		}},
		Content: &maildomain.NormalizedContent{
			TextPaths: []string{"1"}, HTMLPaths: []string{"2"},
			Parts: []maildomain.MIMEPart{{ID: "prt_fixture", Path: "1", MediaType: "text/plain"}},
		},
	}
	var output strings.Builder
	if err := renderMessageShow(NewCompactTextRenderer(&output), "acct_fixture", message, false); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.HasPrefix(text, "message\nmsg_") || !strings.Contains(text, "content=text,html") || !strings.Contains(text, "hint=\"use --full") || !strings.Contains(text, "…") {
		t.Fatalf("message block = %q", text)
	}
	if !strings.Contains(text, "state=unread") {
		t.Fatalf("message block omitted explicit read state: %q", text)
	}
	if strings.Count(text, "\n") > 22 || len(text) > 1500 {
		t.Fatalf("message compact budget lines=%d bytes=%d", strings.Count(text, "\n"), len(text))
	}
	for _, forbidden := range []string{"raw/sha256", "hidden-bcc", "<html>"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("compact message exposed %q", forbidden)
		}
	}
}

func TestInstallExportFileWithoutForceNeverReplacesExistingDestination(t *testing.T) {
	directory := t.TempDir()
	temporary := filepath.Join(directory, "temporary.eml")
	output := filepath.Join(directory, "message.eml")
	if err := os.WriteFile(temporary, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := installExportFile(temporary, output, false)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrorConflict {
		t.Fatalf("install error = %#v", err)
	}
	content, err := os.ReadFile(output)
	if err != nil || string(content) != "existing" {
		t.Fatalf("existing destination changed: %q err=%v", content, err)
	}
	content, err = os.ReadFile(temporary)
	if err != nil || string(content) != "new" {
		t.Fatalf("temporary source changed: %q err=%v", content, err)
	}
}

func TestPublicHeadersAlwaysOmitBCC(t *testing.T) {
	headers := publicHeaders(&maildomain.NormalizedContent{Headers: []maildomain.HeaderField{
		{Key: "From", Value: "sender@example.test"},
		{Key: "Bcc", Value: "hidden@example.test"},
		{Key: "X-BCC-Note", Value: "ordinary custom field"},
	}})
	if len(headers) != 2 || headers[0].Key != "From" || headers[1].Key != "X-BCC-Note" {
		t.Fatalf("public headers = %+v", headers)
	}
}

func TestBoundedJSONContentBufferRejectsOversizeWithoutPartialSuccess(t *testing.T) {
	buffer := &boundedContentBuffer{max: 4}
	if _, err := buffer.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Write([]byte("5")); !errors.Is(err, errJSONContentTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
	if buffer.String() != "1234" {
		t.Fatalf("buffer changed after rejected write: %q", buffer.String())
	}
}

func TestJSONRawContentLimitReturnsSafetyError(t *testing.T) {
	source := oversizedContentSource{}
	_, err := buildJSONMessageContent(context.Background(), maildomain.Message{}, source, OutputOptions{}, maildomain.ContentRaw)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrorSafety || !strings.Contains(appErr.Message, "plain streaming output") {
		t.Fatalf("JSON oversize error = %#v", err)
	}
}

type oversizedContentSource struct{}

func (oversizedContentSource) WriteRaw(_ context.Context, writer io.Writer) error {
	_, err := io.WriteString(writer, strings.Repeat("x", maxJSONMessageContentBytes+1))
	return err
}

func (oversizedContentSource) WriteBody(context.Context, maildomain.ContentFormat, io.Writer) error {
	return nil
}
