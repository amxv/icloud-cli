package mail

import (
	"errors"
	stdmail "net/mail"
	"strings"
	"time"
)

var (
	ErrSenderPreferenceNotFound = errors.New("sender preference not found")
	ErrInvalidSenderPreference  = errors.New("invalid sender preference")
)

type SenderPreference struct {
	AccountID string    `json:"account_id"`
	Email     string    `json:"email"`
	VIP       bool      `json:"vip"`
	Blocked   bool      `json:"blocked"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SenderPreferenceState struct {
	Email    string `json:"email"`
	Explicit bool   `json:"explicit"`
	VIP      bool   `json:"vip"`
	Blocked  bool   `json:"blocked"`
}

type ThreadSenderPreferenceSummary struct {
	DistinctSenders       int `json:"distinct_senders"`
	ExplicitSenders       int `json:"explicit_senders"`
	VIPSenders            int `json:"vip_senders"`
	BlockedSenders        int `json:"blocked_senders"`
	UnpreferredSenders    int `json:"unpreferred_senders"`
	MissingSenderMessages int `json:"missing_sender_messages"`
}

type SenderPreferencePage struct {
	Preferences []SenderPreference `json:"preferences"`
	More        bool               `json:"more"`
	NextCursor  string             `json:"next_cursor,omitempty"`
}

// NormalizeSenderEmail intentionally accepts only one bare exact mailbox
// address. Display-name heuristics, domains, patterns, and address lists do not
// define sender preference identity.
func NormalizeSenderEmail(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", ErrInvalidSenderPreference
	}
	parsed, err := stdmail.ParseAddress(value)
	if err != nil || parsed == nil || parsed.Name != "" {
		return "", ErrInvalidSenderPreference
	}
	email := strings.ToLower(strings.TrimSpace(parsed.Address))
	if email == "" || !strings.EqualFold(value, parsed.Address) || strings.ContainsAny(email, "\r\n\x00") {
		return "", ErrInvalidSenderPreference
	}
	return email, nil
}
