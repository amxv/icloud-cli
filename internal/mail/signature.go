package mail

import (
	"errors"
	"fmt"
	"html"
	"strings"
	"time"
)

var (
	ErrSignatureNotFound      = errors.New("mail signature not found")
	ErrSignatureInvalid       = errors.New("invalid mail signature")
	ErrSignatureInvalidCursor = errors.New("invalid mail signature cursor")
	ErrSignatureIdentity      = errors.New("mail signature identity does not match sender")
)

type Signature struct {
	ID         string    `json:"id"`
	AccountID  string    `json:"account_id"`
	IdentityID string    `json:"identity_id,omitempty"`
	TextBody   string    `json:"text_body"`
	HTMLBody   string    `json:"html_body,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type SignatureInput struct {
	IdentityID string `json:"identity_id,omitempty"`
	TextBody   string `json:"text_body"`
	HTMLBody   string `json:"html_body,omitempty"`
}

type SignatureUpdate struct {
	IdentityID *string `json:"identity_id,omitempty"`
	TextBody   *string `json:"text_body,omitempty"`
	HTMLBody   *string `json:"html_body,omitempty"`
}

type SignaturePage struct {
	Signatures []Signature `json:"signatures"`
	More       bool        `json:"more"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

type SignatureDefault struct {
	AccountID   string       `json:"account_id"`
	IdentityID  string       `json:"identity_id,omitempty"`
	Operation   OutboundKind `json:"operation"`
	SignatureID string       `json:"signature_id,omitempty"`
	None        bool         `json:"none"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

func NormalizeSignatureInput(value SignatureInput) (SignatureInput, error) {
	value.IdentityID = strings.TrimSpace(value.IdentityID)
	value.TextBody = normalizeBody(value.TextBody)
	value.HTMLBody = normalizeBody(value.HTMLBody)
	if value.TextBody == "" {
		return SignatureInput{}, fmt.Errorf("%w: text body is required", ErrSignatureInvalid)
	}
	if len(value.TextBody) > MaxSendBodyBytes || len(value.HTMLBody) > MaxSendBodyBytes {
		return SignatureInput{}, fmt.Errorf("%w: signature body exceeds %d bytes", ErrSignatureInvalid, MaxSendBodyBytes)
	}
	if value.IdentityID != "" && !strings.HasPrefix(value.IdentityID, "mid_") {
		return SignatureInput{}, fmt.Errorf("%w: identity must be a stable mid_… ID", ErrSignatureInvalid)
	}
	return value, nil
}

func NormalizeSignatureOperation(value OutboundKind) (OutboundKind, error) {
	normalized := strings.ToLower(strings.TrimSpace(string(value)))
	switch normalized {
	case "new", "send", "message":
		return OutboundKindSend, nil
	case "reply":
		return OutboundKindReply, nil
	case "reply-all", "reply_all", "replyall":
		return OutboundKindReplyAll, nil
	case "forward", "fwd":
		return OutboundKindForward, nil
	default:
		return "", fmt.Errorf("%w: operation must be new, reply, reply-all, or forward", ErrSignatureInvalid)
	}
}

// ApplySignature appends a selected local signature deterministically to the
// final compose body before normalization, preview, size validation, request
// hashing, or MIME generation. HTML is only created when the caller already
// supplied an HTML representation; a text-only compose remains text-only.
func ApplySignature(request SendRequest, signature Signature) SendRequest {
	text := normalizeBody(signature.TextBody)
	htmlBody := normalizeBody(signature.HTMLBody)
	textBody := request.TextBody
	// Preserve the pre-signature HTML-only compose behavior: MIME composition
	// would normally synthesize a plain-text alternative from the HTML body.
	// Once a text signature is appended we would otherwise suppress that
	// fallback and leave text clients seeing only the signature.
	if normalizeBody(textBody) == "" && normalizeBody(request.HTMLBody) != "" {
		textBody = stripSendHTML(request.HTMLBody)
	}
	request.TextBody = appendSignatureText(textBody, text)
	if request.HTMLBody != "" {
		if htmlBody == "" {
			htmlBody = signatureTextHTML(text)
		}
		request.HTMLBody = appendSignatureHTML(request.HTMLBody, htmlBody)
	}
	request.SignatureExpanded = true
	request.AppliedSignatureID = strings.TrimSpace(signature.ID)
	request.SignatureSelector = ""
	return request
}

func MarkSignatureExpanded(request SendRequest) SendRequest {
	request.SignatureExpanded = true
	request.SignatureSelector = ""
	return request
}

func appendSignatureText(body, signature string) string {
	body = normalizeBody(body)
	signature = normalizeBody(signature)
	if signature == "" {
		return body
	}
	if body == "" {
		return signature
	}
	return body + "\n\n" + signature
}

func appendSignatureHTML(body, signature string) string {
	body = normalizeBody(body)
	signature = normalizeBody(signature)
	if signature == "" {
		return body
	}
	if body == "" {
		return signature
	}
	return body + "<br><br>" + signature
}

func signatureTextHTML(value string) string {
	escaped := html.EscapeString(normalizeBody(value))
	return strings.ReplaceAll(escaped, "\n", "<br>")
}
