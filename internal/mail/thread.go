package mail

import (
	"errors"
	"time"
)

var ErrThreadNotFound = errors.New("mail thread not found")
var ErrInvalidThreadCursor = errors.New("invalid mail thread cursor")

type Thread struct {
	ID               string       `json:"id"`
	AccountID        string       `json:"account_id"`
	RootRFCMessageID string       `json:"root_rfc_message_id,omitempty"`
	CanonicalSubject string       `json:"subject,omitempty"`
	Snippet          string       `json:"snippet,omitempty"`
	Participants     []string     `json:"participants,omitempty"`
	FolderIDs        []string     `json:"folder_ids,omitempty"`
	MessageCount     int          `json:"message_count"`
	UnreadCount      int          `json:"unread_count"`
	AttachmentCount  int          `json:"attachment_count"`
	FirstMessageAt   time.Time    `json:"first_message_at,omitempty"`
	LastMessageAt    time.Time    `json:"last_message_at,omitempty"`
	Status           string       `json:"status"`
	Reconstruction   string       `json:"reconstruction"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	RedirectedFrom   string       `json:"redirected_from,omitempty"`
	Messages         []Message    `json:"messages,omitempty"`
	Edges            []ThreadEdge `json:"edges,omitempty"`
}

type ThreadEdge struct {
	ID              string `json:"id"`
	AccountID       string `json:"account_id"`
	ChildMessageID  string `json:"child_message_id"`
	Relation        string `json:"relation"`
	Ordinal         int    `json:"ordinal"`
	Token           string `json:"token"`
	ParentMessageID string `json:"parent_message_id,omitempty"`
	Status          string `json:"status"`
}

type ThreadPage struct {
	Thread     Thread       `json:"thread"`
	More       bool         `json:"more"`
	NextCursor string       `json:"next_cursor,omitempty"`
	Edges      []ThreadEdge `json:"edges,omitempty"`
}

type ThreadReadOptions struct {
	Cursor         string
	Limit          int
	RequireSource  bool
	HydrateContent bool
}
