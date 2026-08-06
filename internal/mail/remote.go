package mail

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidRemoteReference  = errors.New("invalid remote message reference")
	ErrStaleRemoteReference    = errors.New("remote message reference is stale")
	ErrUnsupportedRemoteFilter = errors.New("search filter is not supported remotely")
)

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
