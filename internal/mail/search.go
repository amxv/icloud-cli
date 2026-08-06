package mail

import (
	"errors"
	"time"
)

var ErrInvalidSearchCursor = errors.New("invalid mail search cursor")

type SearchResultKind string

const (
	SearchMessages SearchResultKind = "messages"
	SearchThreads  SearchResultKind = "threads"
)

type SearchOptions struct {
	Query              string
	FolderID           string
	Unread             *bool
	Flagged            *bool
	Sender             string
	Participant        string
	Subject            string
	RFCMessageID       string
	AttachmentFilename string
	AttachmentType     string
	After              *time.Time
	Before             *time.Time
	ResultKind         SearchResultKind
	Cursor             string
	Limit              int
}

type SearchHit struct {
	Kind             SearchResultKind `json:"kind"`
	Message          *Message         `json:"message,omitempty"`
	Thread           *Thread          `json:"thread,omitempty"`
	MatchedMessageID string           `json:"matched_message_id,omitempty"`
	MatchedField     string           `json:"matched_field,omitempty"`
	Excerpt          string           `json:"excerpt,omitempty"`
	Rank             float64          `json:"rank"`
}

type SearchQueryMeta struct {
	HasText      bool             `json:"has_text"`
	FilterCount  int              `json:"filter_count"`
	ResultKind   SearchResultKind `json:"result_kind"`
	Sort         string           `json:"sort"`
	IndexState   string           `json:"index_state"`
	PendingCount int              `json:"pending_count,omitempty"`
	ErrorCount   int              `json:"error_count,omitempty"`
}

type SearchPage struct {
	Hits       []SearchHit     `json:"hits"`
	More       bool            `json:"more"`
	NextCursor string          `json:"next_cursor,omitempty"`
	Query      SearchQueryMeta `json:"query"`
}

type SearchRepairItem struct {
	MessageID string
	State     string
	ErrorCode string
}

type SearchRepairSummary struct {
	AccountID string `json:"account_id"`
	Selected  int    `json:"selected"`
	Repaired  int    `json:"repaired"`
	Failed    int    `json:"failed"`
	Remaining int    `json:"remaining"`
	Errors    int    `json:"errors"`
}
