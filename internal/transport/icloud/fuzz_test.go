package icloud

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/emersion/go-imap/v2"
)

func FuzzMailboxNamesRemainBoundedCanonicalAndSafe(f *testing.F) {
	for _, seed := range []string{
		"INBOX",
		"Projects/日本語",
		"R&D",
		"Archive/2026/Q3",
		"\x00invalid",
		strings.Repeat("x", 1025),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if len(name) > 4<<10 {
			t.Skip()
		}
		data := &imap.ListData{Mailbox: name, Delim: '/'}
		folder, err := remoteFolderFromList(data)
		if invalidMailboxName(name) {
			if err == nil {
				t.Fatalf("invalid mailbox name was accepted: %q", name)
			}
			return
		}
		if err != nil {
			t.Fatalf("valid mailbox name was rejected: %q: %v", name, err)
		}
		if folder.Name != name || folder.WireName != encodeMailboxWireName(name) {
			t.Fatalf("mailbox canonicalization changed: folder=%+v", folder)
		}
		if !utf8.ValidString(folder.Name) || len(folder.Name) > 1024 {
			t.Fatalf("accepted mailbox violates decoded-name bounds: %q", folder.Name)
		}
		if folder.WireName != encodeMailboxWireName(folder.Name) {
			t.Fatalf("wire mailbox encoding is not deterministic: %q", folder.WireName)
		}
		for _, b := range []byte(folder.WireName) {
			if b < 0x20 || b > 0x7e {
				t.Fatalf("modified UTF-7 wire name contains non-printable/non-ASCII byte %#x", b)
			}
		}
	})
}
