package mail

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidRemoteReference    = errors.New("invalid remote message reference")
	ErrStaleRemoteReference      = errors.New("remote message reference is stale")
	ErrInvalidRemoteSearchCursor = errors.New("invalid remote search cursor")
	ErrStaleRemoteSearchCursor   = errors.New("remote search cursor is stale")
	ErrUnsupportedRemoteFilter   = errors.New("search filter is not supported remotely")
)

type RemoteSearchCursor struct {
	Version     int      `json:"v"`
	AccountID   string   `json:"account"`
	FolderID    string   `json:"folder"`
	UIDValidity uint32   `json:"uid_validity"`
	QueryHash   string   `json:"query_hash"`
	Limit       int      `json:"limit"`
	NextUID     uint32   `json:"next_uid"`
	PendingUIDs []uint32 `json:"pending_uids,omitempty"`
}

func EncodeRemoteSearchCursor(value RemoteSearchCursor) (string, error) {
	value.AccountID = strings.TrimSpace(value.AccountID)
	value.FolderID = strings.TrimSpace(value.FolderID)
	value.QueryHash = strings.TrimSpace(value.QueryHash)
	if value.Version == 0 {
		value.Version = 1
	}
	if value.Version != 1 || value.AccountID == "" || value.FolderID == "" || value.UIDValidity == 0 || len(value.QueryHash) != 64 || value.Limit < 1 || value.Limit > 100 || !validRemoteSearchContinuation(value.NextUID, value.PendingUIDs) {
		return "", ErrInvalidRemoteSearchCursor
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", ErrInvalidRemoteSearchCursor
	}
	return "rcur_" + base64.RawURLEncoding.EncodeToString(payload), nil
}

func DecodeRemoteSearchCursor(value string) (RemoteSearchCursor, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "rcur_") {
		return RemoteSearchCursor{}, ErrInvalidRemoteSearchCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "rcur_"))
	if err != nil {
		return RemoteSearchCursor{}, ErrInvalidRemoteSearchCursor
	}
	var cursor RemoteSearchCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return RemoteSearchCursor{}, ErrInvalidRemoteSearchCursor
	}
	if cursor.Version != 1 || strings.TrimSpace(cursor.AccountID) == "" || strings.TrimSpace(cursor.FolderID) == "" || cursor.UIDValidity == 0 || len(strings.TrimSpace(cursor.QueryHash)) != 64 || cursor.Limit < 1 || cursor.Limit > 100 || !validRemoteSearchContinuation(cursor.NextUID, cursor.PendingUIDs) {
		return RemoteSearchCursor{}, ErrInvalidRemoteSearchCursor
	}
	return cursor, nil
}

func validRemoteSearchContinuation(nextUID uint32, pendingUIDs []uint32) bool {
	if nextUID == 0 && len(pendingUIDs) == 0 {
		return false
	}
	if len(pendingUIDs) > 256 {
		return false
	}
	previous := ^uint32(0)
	for _, uid := range pendingUIDs {
		if uid == 0 || uid >= previous || (nextUID != 0 && uid <= nextUID) {
			return false
		}
		previous = uid
	}
	return true
}

type RemoteReference struct {
	Version     int    `json:"v"`
	AccountID   string `json:"account"`
	FolderID    string `json:"folder"`
	UIDValidity uint32 `json:"uid_validity"`
	UID         uint32 `json:"uid"`
}

func EncodeRemoteReference(value RemoteReference) (string, error) {
	value.AccountID = strings.TrimSpace(value.AccountID)
	value.FolderID = strings.TrimSpace(value.FolderID)
	if value.Version == 0 {
		value.Version = 1
	}
	if value.Version != 1 || value.AccountID == "" || value.FolderID == "" || value.UIDValidity == 0 || value.UID == 0 {
		return "", ErrInvalidRemoteReference
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", ErrInvalidRemoteReference
	}
	return "rem_" + base64.RawURLEncoding.EncodeToString(payload), nil
}

func DecodeRemoteReference(value string) (RemoteReference, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "rem_") {
		return RemoteReference{}, ErrInvalidRemoteReference
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "rem_"))
	if err != nil {
		return RemoteReference{}, ErrInvalidRemoteReference
	}
	var reference RemoteReference
	if err := json.Unmarshal(payload, &reference); err != nil {
		return RemoteReference{}, ErrInvalidRemoteReference
	}
	if reference.Version != 1 || strings.TrimSpace(reference.AccountID) == "" || strings.TrimSpace(reference.FolderID) == "" || reference.UIDValidity == 0 || reference.UID == 0 {
		return RemoteReference{}, ErrInvalidRemoteReference
	}
	return reference, nil
}

type RemoteSearchHit struct {
	Reference       string        `json:"reference"`
	AccountID       string        `json:"account_id"`
	FolderID        string        `json:"folder_id"`
	FolderName      string        `json:"folder_name"`
	UIDValidity     uint32        `json:"uid_validity"`
	UID             uint32        `json:"uid"`
	InternalDate    time.Time     `json:"internal_date,omitempty"`
	From            Address       `json:"from"`
	To              []Address     `json:"to,omitempty"`
	CC              []Address     `json:"cc,omitempty"`
	Subject         string        `json:"subject,omitempty"`
	RFCMessageID    string        `json:"rfc_message_id,omitempty"`
	Flags           LocationFlags `json:"flags"`
	Snippet         string        `json:"snippet,omitempty"`
	RFC822Size      int64         `json:"rfc822_size,omitempty"`
	CachedMessageID string        `json:"cached_message_id,omitempty"`
	MatchedField    string        `json:"matched_field,omitempty"`
}

type RemoteSearchPage struct {
	Hits          []RemoteSearchHit `json:"hits"`
	Count         int               `json:"count"`
	More          bool              `json:"more"`
	Partial       bool              `json:"partial"`
	Folders       int               `json:"folders"`
	CandidateUIDs int               `json:"candidate_uids"`
	NextCursor    string            `json:"next_cursor,omitempty"`
}

type HydratePage struct {
	AccountID   string
	FolderID    string
	UIDValidity uint32
	AttemptedAt time.Time
	Records     []IngestRecord
}

type HydrateResult struct {
	Created         int `json:"created"`
	Updated         int `json:"updated"`
	IdentityMerges  int `json:"identity_merges"`
	DedupeConflicts int `json:"dedupe_conflicts"`
}

type HydrateOutcome struct {
	Reference  string `json:"-"`
	MessageID  string `json:"message_id"`
	LocationID string `json:"location_id"`
	Created    bool   `json:"created"`
	Updated    bool   `json:"updated"`
}
