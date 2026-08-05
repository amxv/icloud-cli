package icloud

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

// encodeMailboxWireName returns the canonical modified UTF-7 mailbox form
// used by IMAP4rev1. go-imap decodes LIST names before exposing them, so the
// transport re-encodes the decoded provider value for durable reconciliation.
func encodeMailboxWireName(value string) string {
	var result strings.Builder
	var encoded []rune
	flush := func() {
		if len(encoded) == 0 {
			return
		}
		units := utf16.Encode(encoded)
		raw := make([]byte, len(units)*2)
		for index, unit := range units {
			binary.BigEndian.PutUint16(raw[index*2:], unit)
		}
		text := base64.StdEncoding.EncodeToString(raw)
		text = strings.TrimRight(text, "=")
		text = strings.ReplaceAll(text, "/", ",")
		result.WriteByte('&')
		result.WriteString(text)
		result.WriteByte('-')
		encoded = encoded[:0]
	}

	for _, character := range value {
		if character >= 0x20 && character <= 0x7e && character != '&' {
			flush()
			result.WriteRune(character)
			continue
		}
		if character == '&' {
			flush()
			result.WriteString("&-")
			continue
		}
		encoded = append(encoded, character)
	}
	flush()
	return result.String()
}
