package mail

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidLocationMutation = errors.New("invalid location mutation")
	ErrMutationNotFound        = errors.New("location mutation not found")
	ErrMutationConflict        = errors.New("location mutation conflicts with existing state")
)

type LocationMutationAction string

const (
	LocationActionMove      LocationMutationAction = "move"
	LocationActionCopy      LocationMutationAction = "copy"
	LocationActionArchive   LocationMutationAction = "archive"
	LocationActionUnarchive LocationMutationAction = "unarchive"
	LocationActionTrash     LocationMutationAction = "trash"
	LocationActionRestore   LocationMutationAction = "restore"
	LocationActionJunk      LocationMutationAction = "junk"
	LocationActionNotJunk   LocationMutationAction = "not_junk"
	LocationActionDelete    LocationMutationAction = "delete"
)

func (action LocationMutationAction) TransportOperation() LocationTransportOperation {
	switch action {
	case LocationActionCopy:
		return LocationTransportCopy
	case LocationActionDelete:
		return LocationTransportDelete
	case LocationActionMove, LocationActionArchive, LocationActionUnarchive, LocationActionTrash, LocationActionRestore, LocationActionJunk, LocationActionNotJunk:
		return LocationTransportMove
	default:
		return ""
	}
}

type LocationTransportOperation string

const (
	LocationTransportMove   LocationTransportOperation = "move"
	LocationTransportCopy   LocationTransportOperation = "copy"
	LocationTransportDelete LocationTransportOperation = "delete"
)

type LocationMutationStatus string

const (
	LocationMutationPending    LocationMutationStatus = "pending"
	LocationMutationConfirmed  LocationMutationStatus = "confirmed"
	LocationMutationUnresolved LocationMutationStatus = "unresolved"
	LocationMutationFailed     LocationMutationStatus = "failed"
)

type LocationMutationRequest struct {
	Action         LocationMutationAction `json:"action"`
	MessageID      string                 `json:"message_id"`
	FromFolder     string                 `json:"from_folder,omitempty"`
	ToFolder       string                 `json:"to_folder,omitempty"`
	IdempotencyKey string                 `json:"idempotency_key,omitempty"`
}

type LocationMutationPlan struct {
	Action                LocationMutationAction     `json:"action"`
	Operation             LocationTransportOperation `json:"operation"`
	MessageID             string                     `json:"message_id"`
	SourceLocation        Location                   `json:"source_location"`
	SourceFolderID        string                     `json:"source_folder_id"`
	SourceFolderName      string                     `json:"source_folder_name"`
	DestinationFolderID   string                     `json:"destination_folder_id,omitempty"`
	DestinationFolderName string                     `json:"destination_folder_name,omitempty"`
	IdempotencyKey        string                     `json:"-"`
	RequestHash           string                     `json:"request_hash"`
}

type LocationMutation struct {
	ID                     string                 `json:"id"`
	AccountID              string                 `json:"account_id"`
	MessageID              string                 `json:"message_id"`
	Action                 LocationMutationAction `json:"action"`
	SourceLocationID       string                 `json:"source_location_id"`
	SourceFolderID         string                 `json:"source_folder_id"`
	DestinationFolderID    string                 `json:"destination_folder_id,omitempty"`
	ProposedLocationID     string                 `json:"-"`
	DestinationLocationID  string                 `json:"destination_location_id,omitempty"`
	RequestHash            string                 `json:"-"`
	IdempotencyKey         string                 `json:"-"`
	Status                 LocationMutationStatus `json:"status"`
	ProviderMethod         string                 `json:"provider_method,omitempty"`
	SourcePresent          bool                   `json:"source_present"`
	DestinationUIDValidity uint32                 `json:"destination_uid_validity,omitempty"`
	DestinationUID         uint32                 `json:"destination_uid,omitempty"`
	MappingKnown           bool                   `json:"mapping_known"`
	ReconciliationRequired bool                   `json:"reconciliation_required"`
	ErrorCode              string                 `json:"error_code,omitempty"`
	CreatedAt              time.Time              `json:"created_at"`
	UpdatedAt              time.Time              `json:"updated_at"`
}

type BeginLocationMutation struct {
	MutationID          string
	AccountID           string
	MessageID           string
	Action              LocationMutationAction
	SourceLocationID    string
	SourceFolderID      string
	DestinationFolderID string
	ProposedLocationID  string
	RequestHash         string
	IdempotencyKey      string
	Now                 time.Time
}

type ApplyLocationMutation struct {
	MutationID             string
	ProviderMethod         string
	SourceFlags            LocationFlags
	SourceModSequence      uint64
	SourcePresent          bool
	DestinationKnown       bool
	DestinationUIDValidity uint32
	DestinationUID         uint32
	DestinationFlags       LocationFlags
	DestinationModSequence uint64
	Status                 LocationMutationStatus
	ReconciliationRequired bool
	ErrorCode              string
	Now                    time.Time
}

func LocationMutationHash(plan LocationMutationPlan) (string, error) {
	payload, err := json.Marshal(struct {
		Action              LocationMutationAction `json:"action"`
		MessageID           string                 `json:"message_id"`
		SourceLocationID    string                 `json:"source_location_id"`
		SourceFolderID      string                 `json:"source_folder_id"`
		DestinationFolderID string                 `json:"destination_folder_id,omitempty"`
	}{
		Action: plan.Action, MessageID: strings.TrimSpace(plan.MessageID),
		SourceLocationID:    strings.TrimSpace(plan.SourceLocation.ID),
		SourceFolderID:      strings.TrimSpace(plan.SourceFolderID),
		DestinationFolderID: strings.TrimSpace(plan.DestinationFolderID),
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
