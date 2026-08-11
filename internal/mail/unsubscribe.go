package mail

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrUnsubscribeUnavailable = errors.New("mailing-list unsubscribe is unavailable")
	ErrInvalidUnsubscribe     = errors.New("invalid mailing-list unsubscribe metadata")
	ErrUnsubscribeSelection   = errors.New("mailing-list unsubscribe method selection is required")
)

const (
	UnsubscribeMethodOneClick UnsubscribeMethodKind = "one_click"
	UnsubscribeMethodMailto   UnsubscribeMethodKind = "mailto"

	maxUnsubscribeHeaderBytes = 32 << 10
	maxUnsubscribeMailtoText  = 8 << 10
	maxUnsubscribeRecipients  = 10
	maxUnsubscribeMethods     = 10
)

type UnsubscribeMethodKind string

type UnsubscribeMethod struct {
	ID            string                `json:"id"`
	Kind          UnsubscribeMethodKind `json:"kind"`
	Host          string                `json:"host,omitempty"`
	DKIMRequired  bool                  `json:"dkim_required,omitempty"`
	TargetHash    string                `json:"-"`
	URI           string                `json:"-"`
	MailtoTo      []Address             `json:"-"`
	MailtoSubject string                `json:"-"`
	MailtoBody    string                `json:"-"`
}

type UnsubscribeCapability struct {
	Available         bool                `json:"available"`
	OneClickDeclared  bool                `json:"one_click_declared,omitempty"`
	SelectionRequired bool                `json:"selection_required,omitempty"`
	Methods           []UnsubscribeMethod `json:"methods,omitempty"`
}

type UnsubscribePlan struct {
	MessageID         string              `json:"message_id"`
	Available         bool                `json:"available"`
	OneClickDeclared  bool                `json:"one_click_declared,omitempty"`
	SelectionRequired bool                `json:"selection_required,omitempty"`
	Selected          *UnsubscribeMethod  `json:"selected,omitempty"`
	Alternatives      []UnsubscribeMethod `json:"alternatives,omitempty"`
	DryRun            bool                `json:"dry_run"`
}

type UnsubscribeResult struct {
	MessageID  string                `json:"message_id"`
	Method     UnsubscribeMethodKind `json:"method"`
	Host       string                `json:"host,omitempty"`
	Status     string                `json:"status"`
	Reused     bool                  `json:"reused,omitempty"`
	AttemptID  string                `json:"attempt_id,omitempty"`
	DKIMDomain string                `json:"dkim_domain,omitempty"`
}

// ParseUnsubscribeCapability converts stored, decoded RFC 2369 / RFC 8058
// headers into a privacy-safe semantic capability. Raw endpoint URIs and
// mailto recipient/query material remain private fields excluded from JSON.
func ParseUnsubscribeCapability(headers []HeaderField) UnsubscribeCapability {
	unsubscribeValues := headerValues(headers, "list-unsubscribe")
	postValues := headerValues(headers, "list-unsubscribe-post")
	if len(unsubscribeValues) != 1 || len(unsubscribeValues[0]) == 0 || len(unsubscribeValues[0]) > maxUnsubscribeHeaderBytes {
		return UnsubscribeCapability{}
	}

	uris := parseListUnsubscribeURIs(unsubscribeValues[0])
	oneClickDeclared := len(postValues) == 1 && strings.EqualFold(strings.TrimSpace(postValues[0]), "List-Unsubscribe=One-Click")
	methods := make([]UnsubscribeMethod, 0, len(uris))
	httpsMethods := make([]UnsubscribeMethod, 0, 1)
	mailtoMethods := make([]UnsubscribeMethod, 0, len(uris))
	for _, raw := range uris {
		parsed, err := url.Parse(raw)
		if err != nil || parsed == nil {
			continue
		}
		switch strings.ToLower(parsed.Scheme) {
		case "https":
			if !oneClickDeclared {
				continue
			}
			method, ok := normalizeHTTPSUnsubscribe(parsed)
			if ok {
				httpsMethods = append(httpsMethods, method)
			}
		case "mailto":
			method, ok := normalizeMailtoUnsubscribe(parsed)
			if ok {
				mailtoMethods = append(mailtoMethods, method)
			}
		}
	}

	// RFC 8058 requires exactly one HTTPS URI. If a message advertises more
	// than one HTTPS endpoint, do not guess which tokenized endpoint to POST.
	if oneClickDeclared && len(httpsMethods) == 1 {
		method := httpsMethods[0]
		method.ID = "one-click"
		method.DKIMRequired = true
		methods = append(methods, method)
	}
	for index, method := range mailtoMethods {
		method.ID = fmt.Sprintf("mailto:%d", index+1)
		methods = append(methods, method)
	}
	if len(methods) == 0 {
		return UnsubscribeCapability{OneClickDeclared: oneClickDeclared}
	}

	selectionRequired := false
	if len(methods) > 1 && methods[0].Kind != UnsubscribeMethodOneClick {
		selectionRequired = true
	}
	return UnsubscribeCapability{
		Available: true, OneClickDeclared: oneClickDeclared,
		SelectionRequired: selectionRequired, Methods: methods,
	}
}

func SelectUnsubscribeMethod(capability UnsubscribeCapability, selector string) (UnsubscribeMethod, error) {
	if !capability.Available || len(capability.Methods) == 0 {
		return UnsubscribeMethod{}, ErrUnsubscribeUnavailable
	}
	selector = strings.ToLower(strings.TrimSpace(selector))
	if selector != "" {
		for _, method := range capability.Methods {
			if strings.EqualFold(method.ID, selector) {
				return method, nil
			}
		}
		return UnsubscribeMethod{}, ErrInvalidUnsubscribe
	}
	for _, method := range capability.Methods {
		if method.Kind == UnsubscribeMethodOneClick {
			return method, nil
		}
	}
	if len(capability.Methods) == 1 {
		return capability.Methods[0], nil
	}
	return UnsubscribeMethod{}, ErrUnsubscribeSelection
}

func UnsubscribeIdempotencyKey(messageID string, method UnsubscribeMethod) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(messageID) + "\x00" + string(method.Kind) + "\x00" + method.TargetHash))
	return "unsubscribe:" + hex.EncodeToString(sum[:])
}

func headerValues(headers []HeaderField, key string) []string {
	result := make([]string, 0, 1)
	for _, field := range headers {
		if strings.EqualFold(strings.TrimSpace(field.Key), key) {
			value := strings.TrimSpace(field.Value)
			if value != "" {
				result = append(result, value)
			}
		}
	}
	return result
}

func parseListUnsubscribeURIs(value string) []string {
	if len(value) == 0 || len(value) > maxUnsubscribeHeaderBytes || strings.ContainsFunc(value, invalidUnsubscribeRune) {
		return nil
	}
	result := make([]string, 0, 2)
	for index := 0; index < len(value); {
		open := strings.IndexByte(value[index:], '<')
		if open < 0 {
			break
		}
		open += index
		close := strings.IndexByte(value[open+1:], '>')
		if close < 0 {
			return nil
		}
		close += open + 1
		raw := strings.TrimSpace(value[open+1 : close])
		if raw != "" && !strings.ContainsFunc(raw, unicode.IsSpace) && !strings.ContainsFunc(raw, invalidUnsubscribeRune) {
			result = append(result, raw)
			if len(result) > maxUnsubscribeMethods {
				return nil
			}
		}
		index = close + 1
	}
	return result
}

func normalizeHTTPSUnsubscribe(parsed *url.URL) (UnsubscribeMethod, bool) {
	if parsed == nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return UnsubscribeMethod{}, false
	}
	if strings.ContainsFunc(parsed.String(), invalidUnsubscribeRune) {
		return UnsubscribeMethod{}, false
	}
	host := strings.ToLower(parsed.Hostname())
	if !validPublicHostLabel(host) {
		return UnsubscribeMethod{}, false
	}
	canonical := parsed.String()
	return UnsubscribeMethod{Kind: UnsubscribeMethodOneClick, Host: host, URI: canonical, TargetHash: shortTargetHash(canonical)}, true
}

func normalizeMailtoUnsubscribe(parsed *url.URL) (UnsubscribeMethod, bool) {
	if parsed == nil || !strings.EqualFold(parsed.Scheme, "mailto") || parsed.Fragment != "" || parsed.User != nil {
		return UnsubscribeMethod{}, false
	}
	recipientText, err := url.PathUnescape(parsed.Opaque)
	if err != nil || recipientText == "" || strings.ContainsFunc(recipientText, invalidUnsubscribeRune) {
		return UnsubscribeMethod{}, false
	}
	parsedAddresses, err := mail.ParseAddressList(recipientText)
	if err != nil || len(parsedAddresses) == 0 || len(parsedAddresses) > maxUnsubscribeRecipients {
		return UnsubscribeMethod{}, false
	}
	to := make([]Address, 0, len(parsedAddresses))
	hosts := make([]string, 0, len(parsedAddresses))
	for _, address := range parsedAddresses {
		emailAddress := strings.ToLower(strings.TrimSpace(address.Address))
		at := strings.LastIndexByte(emailAddress, '@')
		if at <= 0 || at+1 >= len(emailAddress) || strings.ContainsFunc(emailAddress, invalidUnsubscribeRune) {
			return UnsubscribeMethod{}, false
		}
		host := strings.ToLower(strings.TrimSpace(emailAddress[at+1:]))
		if !validPublicHostLabel(host) {
			return UnsubscribeMethod{}, false
		}
		to = append(to, Address{DisplayName: cleanHeaderText(address.Name), Email: emailAddress})
		hosts = append(hosts, host)
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return UnsubscribeMethod{}, false
	}
	subject := ""
	body := ""
	seenSubject := false
	seenBody := false
	for key, values := range query {
		lower := strings.ToLower(strings.TrimSpace(key))
		if (lower != "subject" && lower != "body") || len(values) != 1 {
			return UnsubscribeMethod{}, false
		}
		switch lower {
		case "subject":
			if seenSubject {
				return UnsubscribeMethod{}, false
			}
			seenSubject = true
			subject = values[0]
		case "body":
			if seenBody {
				return UnsubscribeMethod{}, false
			}
			seenBody = true
			body = values[0]
		}
	}
	if !safeMailtoText(subject) || !safeMailtoText(body) {
		return UnsubscribeMethod{}, false
	}
	if subject == "" {
		subject = "unsubscribe"
	}
	if body == "" {
		body = "unsubscribe"
	}
	sort.Strings(hosts)
	canonical := strings.Join(unsubscribeAddressEmails(to), ",") + "\x00" + subject + "\x00" + body
	return UnsubscribeMethod{
		Kind: UnsubscribeMethodMailto, Host: hosts[0], TargetHash: shortTargetHash(canonical),
		MailtoTo: to, MailtoSubject: subject, MailtoBody: body,
	}, true
}

func unsubscribeAddressEmails(values []Address) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.Email)
	}
	return result
}

func safeMailtoText(value string) bool {
	return utf8.ValidString(value) && len(value) <= maxUnsubscribeMailtoText && !strings.ContainsAny(value, "\x00\r\n") && !strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsControl(r) && r != '\t'
	})
}

func invalidUnsubscribeRune(r rune) bool {
	return r == '\r' || r == '\n' || r == '\x00' || (unicode.IsControl(r) && r != '\t')
}

func validPublicHostLabel(host string) bool {
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "\r\n\t /@") {
		return false
	}
	return utf8.ValidString(host)
}

func shortTargetHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}
