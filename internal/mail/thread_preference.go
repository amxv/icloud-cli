package mail

import "time"

// ThreadPreference is local client state projected through the current RFC
// graph. AnchorMessageID is intentionally stable while ThreadID is derived and
// may change as stronger evidence merges or splits reconstructed conversations.
type ThreadPreference struct {
	AccountID       string    `json:"account_id"`
	ThreadID        string    `json:"thread_id"`
	AnchorMessageID string    `json:"anchor_message_id"`
	Muted           bool      `json:"muted"`
	LocalOnly       bool      `json:"local_only"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
