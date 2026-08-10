package mail

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode"
)

const (
	MaxReplyReferences      = 100
	MaxReplyReferencesBytes = 8 << 10
)

var ErrInvalidReplyRequest = errors.New("invalid reply request")

type ReplyRequest struct {
	ParentMessageID       string
	ParentFolder          string
	SenderIdentityID      string
	ReplyAll              bool
	To                    []Address
	CC                    []Address
	BCC                   []Address
	ReplaceTo             bool
	ReplaceCC             bool
	TextBody              string
	HTMLBody              string
	Headers               []CustomHeader
	Attachments           []OutboundAttachment
	IdempotencyKey        string
	RetryOfAttemptID      string
	AuthorizeUnknownRetry bool
}

type ReplyPlan struct {
	ParentMessageID  string       `json:"parent_message_id"`
	ParentLocationID string       `json:"parent_location_id"`
	ParentFolderID   string       `json:"parent_folder_id"`
	ParentRFCID      string       `json:"parent_rfc_message_id,omitempty"`
	ThreadID         string       `json:"thread_id"`
	Kind             OutboundKind `json:"kind"`
	Threading        bool         `json:"threading"`
	DerivedTo        []Address    `json:"derived_to,omitempty"`
	DerivedCC        []Address    `json:"derived_cc,omitempty"`
	AddedTo          []Address    `json:"added_to,omitempty"`
	AddedCC          []Address    `json:"added_cc,omitempty"`
	AddedBCC         []Address    `json:"added_bcc,omitempty"`
	ReplaceTo        bool         `json:"replace_to"`
	ReplaceCC        bool         `json:"replace_cc"`
	Send             SendRequest  `json:"-"`
}

type ReplyPreview struct {
	ParentMessageID  string       `json:"parent_message_id"`
	ParentLocationID string       `json:"parent_location_id"`
	ParentFolderID   string       `json:"parent_folder_id"`
	ParentRFCID      string       `json:"parent_rfc_message_id,omitempty"`
	ThreadID         string       `json:"thread_id"`
	Kind             OutboundKind `json:"kind"`
	Threading        bool         `json:"threading"`
	DerivedTo        []Address    `json:"derived_to,omitempty"`
	DerivedCC        []Address    `json:"derived_cc,omitempty"`
	AddedTo          []Address    `json:"added_to,omitempty"`
	AddedCC          []Address    `json:"added_cc,omitempty"`
	AddedBCC         []Address    `json:"added_bcc,omitempty"`
	ReplaceTo        bool         `json:"replace_to"`
	ReplaceCC        bool         `json:"replace_cc"`
	Send             SendPreview  `json:"send"`
}

func BuildReplyPlan(parent Message, threadID string, selfAddresses []string, input ReplyRequest) (ReplyPlan, error) {
	parentID := strings.TrimSpace(input.ParentMessageID)
	if parentID == "" {
		parentID = strings.TrimSpace(parent.ID)
	}
	if !strings.HasPrefix(parentID, "msg_") || parent.ID != parentID || strings.TrimSpace(threadID) == "" {
		return ReplyPlan{}, fmt.Errorf("%w: an exact local parent message and thread are required", ErrInvalidReplyRequest)
	}
	addedTo, err := normalizeAddressList(input.To)
	if err != nil {
		return ReplyPlan{}, fmt.Errorf("%w: invalid added To recipient", ErrInvalidReplyRequest)
	}
	addedCC, err := normalizeAddressList(input.CC)
	if err != nil {
		return ReplyPlan{}, fmt.Errorf("%w: invalid added Cc recipient", ErrInvalidReplyRequest)
	}
	addedBCC, err := normalizeAddressList(input.BCC)
	if err != nil {
		return ReplyPlan{}, fmt.Errorf("%w: invalid Bcc recipient", ErrInvalidReplyRequest)
	}

	self := canonicalAddressSet(selfAddresses)
	addedTo = removeAddresses(addedTo, self)
	addedCC = removeAddresses(addedCC, self)
	addedBCC = removeAddresses(addedBCC, self)

	primary := firstSafeAddress(parent.ReplyTo, parent.From)
	if _, exists := self[canonicalAddress(primary.Email)]; exists {
		primary = Address{}
	}
	derivedTo := []Address{}
	if primary.Email != "" {
		derivedTo = append(derivedTo, primary)
	}
	derivedCC := []Address{}
	if input.ReplyAll {
		participants := append(append([]Address(nil), parent.To...), parent.CC...)
		participants = removeAddresses(cleanStoredAddresses(participants), self)
		participants = removeAddresses(participants, addressSet(derivedTo))
		if len(derivedTo) == 0 && len(participants) > 0 {
			derivedTo = append(derivedTo, participants[0])
			participants = participants[1:]
		}
		derivedCC = participants
	}

	finalTo := append([]Address(nil), derivedTo...)
	if input.ReplaceTo {
		finalTo = nil
	}
	finalTo = append(finalTo, addedTo...)
	finalCC := append([]Address(nil), derivedCC...)
	if input.ReplaceCC {
		finalCC = nil
	}
	finalCC = append(finalCC, addedCC...)
	finalTo = dedupeAddresses(finalTo)
	finalCC = removeAddresses(dedupeAddresses(finalCC), addressSet(finalTo))
	addedBCC = removeAddresses(dedupeAddresses(addedBCC), mergeAddressSets(addressSet(finalTo), addressSet(finalCC)))
	if len(finalTo) == 0 {
		return ReplyPlan{}, fmt.Errorf("%w: reply has no non-self To recipient", ErrInvalidReplyRequest)
	}

	parentRFCID, threading := NormalizeRFCMessageID(parent.RFCMessageID)
	inReplyTo := ""
	references := []string(nil)
	if threading {
		inReplyTo = parentRFCID
		references = NormalizeReferenceChain(parent.References, parentRFCID)
	}
	kind := OutboundKindReply
	if input.ReplyAll {
		kind = OutboundKindReplyAll
	}
	send := SendRequest{
		Kind: kind, ParentMessageID: parentID, SenderIdentityID: strings.TrimSpace(input.SenderIdentityID),
		To: finalTo, CC: finalCC, BCC: addedBCC,
		Subject: ReplySubject(parent.Subject), TextBody: input.TextBody, HTMLBody: input.HTMLBody,
		Headers: input.Headers, InReplyTo: inReplyTo, References: references,
		Attachments: input.Attachments, IdempotencyKey: input.IdempotencyKey,
		RetryOfAttemptID: input.RetryOfAttemptID, AuthorizeUnknownRetry: input.AuthorizeUnknownRetry,
	}
	return ReplyPlan{
		ParentMessageID: parentID, ParentRFCID: parentRFCID, ThreadID: threadID,
		Kind: kind, Threading: threading,
		DerivedTo: derivedTo, DerivedCC: derivedCC,
		AddedTo: addedTo, AddedCC: addedCC, AddedBCC: addedBCC,
		ReplaceTo: input.ReplaceTo, ReplaceCC: input.ReplaceCC, Send: send,
	}, nil
}

func ReplySubject(value string) string {
	value = cleanSendHeaderText(value)
	for {
		trimmed := strings.TrimSpace(value)
		if len(trimmed) < 3 || !strings.EqualFold(trimmed[:3], "re:") {
			value = trimmed
			break
		}
		value = strings.TrimSpace(trimmed[3:])
	}
	if value == "" {
		return "Re:"
	}
	return "Re: " + value
}

func NormalizeRFCMessageID(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") && len(value) > 2 {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	if value == "" || len(value)+2 > MaxReplyReferencesBytes || strings.ContainsAny(value, "<>") || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return "", false
	}
	at := strings.LastIndexByte(value, '@')
	if at <= 0 || at == len(value)-1 || strings.Contains(value[:at], "@") {
		return "", false
	}
	return strings.ToLower(value), true
}

func NormalizeReferenceChain(values []string, parentID string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values)+1)
	appendID := func(value string) {
		value, ok := NormalizeRFCMessageID(value)
		if !ok {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	for _, value := range values {
		appendID(value)
	}
	appendID(parentID)
	if len(result) > MaxReplyReferences {
		result = result[len(result)-MaxReplyReferences:]
	}
	for len(result) > 1 && referenceHeaderBytes(result) > MaxReplyReferencesBytes {
		result = result[1:]
	}
	return result
}

func referenceHeaderBytes(values []string) int {
	if len(values) == 0 {
		return 0
	}
	size := 0
	for _, value := range values {
		size += len(value) + 2
	}
	return size + len(values) - 1
}

func canonicalAddressSet(values []string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, value := range values {
		if parsed, err := mail.ParseAddress(strings.TrimSpace(value)); err == nil {
			value = parsed.Address
		}
		if value = canonicalAddress(value); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func canonicalAddress(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func firstSafeAddress(values ...Address) Address {
	for _, value := range values {
		normalized, err := normalizeOneAddress(value, true)
		if err == nil {
			return normalized
		}
	}
	return Address{}
}

func cleanStoredAddresses(values []Address) []Address {
	result := make([]Address, 0, len(values))
	for _, value := range values {
		if normalized, err := normalizeOneAddress(value, true); err == nil {
			result = append(result, normalized)
		}
	}
	return dedupeAddresses(result)
}

func dedupeAddresses(values []Address) []Address {
	seen := map[string]struct{}{}
	result := make([]Address, 0, len(values))
	for _, value := range values {
		key := canonicalAddress(value.Email)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func addressSet(values []Address) map[string]struct{} {
	result := map[string]struct{}{}
	for _, value := range values {
		if key := canonicalAddress(value.Email); key != "" {
			result[key] = struct{}{}
		}
	}
	return result
}

func mergeAddressSets(values ...map[string]struct{}) map[string]struct{} {
	result := map[string]struct{}{}
	for _, value := range values {
		for key := range value {
			result[key] = struct{}{}
		}
	}
	return result
}

func removeAddresses(values []Address, blocked map[string]struct{}) []Address {
	result := make([]Address, 0, len(values))
	for _, value := range values {
		if _, exists := blocked[canonicalAddress(value.Email)]; exists {
			continue
		}
		result = append(result, value)
	}
	return result
}
