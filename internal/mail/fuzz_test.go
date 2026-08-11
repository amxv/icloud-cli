package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/mail"
	"reflect"
	"strings"
	"testing"
	"time"
)

func FuzzParseNormalizedDeterministicAndBounded(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte("Message-ID: <plain@example.test>\r\nFrom: Sender <sender@example.test>\r\nTo: Receiver <receiver@example.test>\r\nSubject: Plain\r\n\r\nhello"),
		[]byte("Content-Type: multipart/mixed; boundary=x\r\nMessage-ID: <mixed@example.test>\r\n\r\n--x\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nbody\r\n--x\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=note.bin\r\nContent-Transfer-Encoding: base64\r\n\r\nAAEC\r\n--x--\r\n"),
		[]byte("Date: definitely-not-a-date\r\nSubject: malformed\x00header\r\n\r\nbody"),
		[]byte("Subject: =?UTF-8?Q?broken_=F0=28=8C=28?=\r\nContent-Type: text/plain\r\n\r\nmalformed\xffutf8"),
		[]byte("Content-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename*=UTF-8''caf%C3%A9.txt\r\n\r\npayload\r\n--x--\r\n"),
		[]byte("List-Unsubscribe: <https://lists.example.test/u/token>, <mailto:leave@example.test?subject=unsubscribe>\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click\r\nDate: Fri, 07 Aug 2026 10:00:00 +0000\r\n\r\nlist body"),
		[]byte(""),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		// Fuzz the MIME/header/parser boundary without turning the corpus into a
		// large-message performance test; those limits have dedicated fixtures.
		if len(raw) > 256<<10 {
			t.Skip()
		}
		first, firstErr := ParseNormalized(bytes.NewReader(raw))
		second, secondErr := ParseNormalized(bytes.NewReader(raw))
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("parse success changed across identical input: first=%v second=%v", firstErr, secondErr)
		}
		if firstErr == nil && !reflect.DeepEqual(first, second) {
			t.Fatal("normalized MIME parse is not deterministic")
		}
		if len(first.Parts) > maxMIMEParts || len(second.Parts) > maxMIMEParts {
			t.Fatalf("MIME part bound exceeded: first=%d second=%d", len(first.Parts), len(second.Parts))
		}
	})
}

func FuzzUnsubscribeMetadataRejectsUnsafeURLsAndStaysBounded(f *testing.F) {
	for _, seed := range []string{
		"<https://lists.example.test/u/token>",
		"<mailto:leave@example.test?subject=unsubscribe&body=bye>",
		"<https://user:pass@example.test/u>",
		"<http://example.test/u>",
		"<mailto:list@example.test?subject=ok%0d%0aBcc:evil@example.test>",
		"<https://example.test/\xff>",
		strings.Repeat("<mailto:a@example.test>,", maxUnsubscribeRecipients+5),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if len(value) > maxUnsubscribeHeaderBytes+1024 {
			t.Skip()
		}
		headers := []HeaderField{
			{Key: "List-Unsubscribe", Value: value},
			{Key: "List-Unsubscribe-Post", Value: "List-Unsubscribe=One-Click"},
		}
		first := ParseUnsubscribeCapability(headers)
		second := ParseUnsubscribeCapability(headers)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("unsubscribe parsing is not deterministic: first=%+v second=%+v", first, second)
		}
		if len(first.Methods) > maxUnsubscribeMethods {
			t.Fatalf("unsubscribe method bound exceeded: %d", len(first.Methods))
		}
		for _, method := range first.Methods {
			if method.ID == "" || method.TargetHash == "" {
				t.Fatalf("accepted unsubscribe method lacks stable public/private identity: %+v", method)
			}
			if method.Kind == UnsubscribeMethodOneClick && (!method.DKIMRequired || method.Host == "") {
				t.Fatalf("unsafe one-click method accepted: %+v", method)
			}
		}
	})
}

func FuzzSMIMEEnvelopeParserFailsClosedAndDeterministically(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte("Content-Type: application/pkcs7-mime; smime-type=enveloped-data\r\nContent-Transfer-Encoding: base64\r\n\r\nnot-cms"),
		[]byte("Content-Type: application/x-pkcs7-mime\r\n\r\n\x00\x01\x02\xff"),
		[]byte("Content-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; boundary=x\r\n\r\n--x--\r\n"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 256<<10 {
			t.Skip()
		}
		first, firstErr := ParseSMIMEEnvelopeContext(context.Background(), bytes.NewReader(raw))
		second, secondErr := ParseSMIMEEnvelopeContext(context.Background(), bytes.NewReader(raw))
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("S/MIME envelope parse success changed: first=%v second=%v", firstErr, secondErr)
		}
		if firstErr == nil {
			if first == nil || second == nil || first.p7 == nil || second.p7 == nil {
				t.Fatalf("S/MIME envelope parse changed across identical input: first=%v second=%v", first, second)
			}
			return
		}
		if first != nil || second != nil {
			t.Fatalf("failed S/MIME parse returned usable envelope: first=%v second=%v errors=%v/%v", first, second, firstErr, secondErr)
		}
	})
}

func FuzzRemoteReferenceRoundTripAndRejectsMalformed(f *testing.F) {
	f.Add([]byte("account"), []byte("folder"), uint32(1), uint32(1), "rem_bad")
	f.Add([]byte("日本語"), []byte("Archive/2026"), uint32(77), uint32(42), "")
	f.Fuzz(func(t *testing.T, accountSeed, folderSeed []byte, uidValidity, uid uint32, malformed string) {
		if len(accountSeed)+len(folderSeed)+len(malformed) > 64<<10 {
			t.Skip()
		}
		accountHash := sha256.Sum256(accountSeed)
		folderHash := sha256.Sum256(folderSeed)
		accountID := "acct_" + hex.EncodeToString(accountHash[:8])
		folderID := "fld_" + hex.EncodeToString(folderHash[:8])
		if uidValidity == 0 {
			uidValidity = 1
		}
		if uid == 0 {
			uid = 1
		}
		want := RemoteReference{Version: 1, AccountID: accountID, FolderID: folderID, UIDValidity: uidValidity, UID: uid}
		encoded, err := EncodeRemoteReference(want)
		if err != nil {
			t.Fatalf("encode valid reference: %v", err)
		}
		got, err := DecodeRemoteReference(encoded)
		if err != nil || got != want {
			t.Fatalf("round trip got=%+v err=%v want=%+v", got, err, want)
		}
		// Arbitrary remote input must be safely rejectable; the assertion here
		// is panic-freedom plus the stricter property that accepted references
		// canonicalize back to the exact same token.
		if decoded, err := DecodeRemoteReference(malformed); err == nil {
			reencoded, encodeErr := EncodeRemoteReference(decoded)
			if encodeErr != nil || reencoded != strings.TrimSpace(malformed) {
				t.Fatalf("accepted non-canonical remote reference %q -> %q (%v)", malformed, reencoded, encodeErr)
			}
		}
	})
}

func FuzzReplyReferenceChainStaysCanonicalAndBounded(f *testing.F) {
	f.Add("root@example.test", "child@example.test|ROOT@example.test|bad value")
	f.Add(strings.Repeat("x", MaxReplyReferencesBytes+1)+"@example.test", "")
	f.Fuzz(func(t *testing.T, parent, encoded string) {
		if len(parent)+len(encoded) > 128<<10 {
			t.Skip()
		}
		values := strings.Split(encoded, "|")
		chain := NormalizeReferenceChain(values, parent)
		if len(chain) > MaxReplyReferences {
			t.Fatalf("reference count = %d", len(chain))
		}
		if referenceHeaderBytes(chain) > MaxReplyReferencesBytes {
			t.Fatalf("reference header bytes = %d", referenceHeaderBytes(chain))
		}
		seen := map[string]struct{}{}
		for _, value := range chain {
			canonical, ok := NormalizeRFCMessageID(value)
			if !ok || canonical != value {
				t.Fatalf("non-canonical reference %q", value)
			}
			if _, duplicate := seen[value]; duplicate {
				t.Fatalf("duplicate reference %q", value)
			}
			seen[value] = struct{}{}
		}
	})
}

func FuzzContentFingerprintAndProviderIdentityStayDeterministicAndScoped(f *testing.F) {
	f.Add("acct_one", "subject", "sender@example.test", "recipient@example.test", "folder_one", uint32(7), uint32(9))
	f.Add("acct_two", "日本語", "Sender <sender@example.test>", "", "Archive/2026", uint32(1), uint32(1))
	f.Fuzz(func(t *testing.T, accountID, subject, from, to, folderID string, uidValidity, uid uint32) {
		if len(accountID)+len(subject)+len(from)+len(to)+len(folderID) > 64<<10 {
			t.Skip()
		}
		parsed := ParsedMessage{
			Subject: subject,
			From:    Address{Email: from},
			To:      []Address{{Email: to}},
		}
		moment := time.Unix(1_786_000_000, 0).UTC()
		first := BuildContentFingerprint(accountID, parsed, moment, nil)
		second := BuildContentFingerprint(accountID, parsed, moment, nil)
		if first != second {
			t.Fatalf("content fingerprint changed across identical input: %q != %q", first, second)
		}
		if first != "" && strings.TrimSpace(accountID) != "" {
			other := BuildContentFingerprint(accountID+"-other", parsed, moment, nil)
			if other == first {
				t.Fatalf("content fingerprint is not account-scoped: %q", first)
			}
		}

		if uidValidity == 0 {
			uidValidity = 1
		}
		if uid == 0 {
			uid = 1
		}
		if strings.TrimSpace(folderID) == "" {
			folderID = "folder"
		}
		identity := ProviderLocationIdentity(folderID, uidValidity, uid)
		if identity == "" || identity != ProviderLocationIdentity(folderID, uidValidity, uid) {
			t.Fatalf("provider location identity is empty or unstable: %q", identity)
		}
		if identity == ProviderLocationIdentity(folderID, uidValidity, uid+1) && uid != ^uint32(0) {
			t.Fatalf("provider location identity did not distinguish UID %d", uid)
		}
	})
}

func FuzzParseSendAddressCanonicalizesAcceptedInput(f *testing.F) {
	for _, seed := range []string{
		"person@example.test",
		"Person Name <PERSON@example.test>",
		`"Quoted, Name" <quoted@example.test>`,
		"日本語 <unicode@example.test>",
		"bad\r\nBcc: hidden@example.test",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > MaxSendHeaderValue {
			t.Skip()
		}
		first, firstErr := ParseSendAddress(input)
		second, secondErr := ParseSendAddress(input)
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("address parse success changed across identical input: first=%v second=%v", firstErr, secondErr)
		}
		if firstErr != nil {
			return
		}
		if first != second {
			t.Fatalf("address normalization changed across identical input: first=%+v second=%+v", first, second)
		}
		if first.Email == "" || first.Email != strings.ToLower(first.Email) || strings.ContainsAny(first.Email, "\r\n") {
			t.Fatalf("accepted address is not canonical/safe: %+v", first)
		}
		if strings.ContainsAny(first.DisplayName, "\r\n") {
			t.Fatalf("accepted display name contains header injection: %q", first.DisplayName)
		}
		// Formatting the normalized value and reparsing it must preserve the
		// canonical mailbox identity even when the display name needs quoting.
		formatted := (&mail.Address{Name: first.DisplayName, Address: first.Email}).String()
		roundTrip, err := ParseSendAddress(formatted)
		if err != nil || roundTrip != first {
			t.Fatalf("address round trip got=%+v err=%v want=%+v formatted=%q", roundTrip, err, first, formatted)
		}
	})
}
