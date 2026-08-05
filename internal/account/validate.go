package account

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxFriendlyNameRunes = 100
	maxAddressRunes      = 254
)

func NormalizeName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > maxFriendlyNameRunes || containsControl(value) {
		return "", fmt.Errorf("%w: friendly name must contain 1-%d printable characters", ErrInvalidInput, maxFriendlyNameRunes)
	}
	return value, nil
}

func NormalizeEmail(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address == "" || utf8.RuneCountInString(parsed.Address) > maxAddressRunes || containsControl(parsed.Address) {
		return "", fmt.Errorf("%w: email address is invalid", ErrInvalidInput)
	}
	local, domain, ok := strings.Cut(parsed.Address, "@")
	if !ok || strings.TrimSpace(local) == "" || strings.TrimSpace(domain) == "" {
		return "", fmt.Errorf("%w: email address is invalid", ErrInvalidInput)
	}
	return strings.ToLower(local + "@" + domain), nil
}

func NormalizeIMAPUsername(value, email string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = email
	}
	if value == "" || utf8.RuneCountInString(value) > maxAddressRunes || containsControl(value) {
		return "", fmt.Errorf("%w: IMAP username is invalid", ErrInvalidInput)
	}
	return value, nil
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}
