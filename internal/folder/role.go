package folder

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidRole        = errors.New("folder role is invalid")
	ErrRoleBindingMissing = errors.New("folder role binding not found")
	ErrRoleBindingInvalid = errors.New("folder role binding is not current and selectable")
	ErrRoleAlreadyBound   = errors.New("folder is already bound to another semantic role")
)

type Role string

const (
	RoleInbox   Role = "inbox"
	RoleSent    Role = "sent"
	RoleDrafts  Role = "drafts"
	RoleArchive Role = "archive"
	RoleTrash   Role = "trash"
	RoleJunk    Role = "junk"
)

var bindableRoles = []Role{RoleSent, RoleDrafts, RoleArchive, RoleTrash, RoleJunk}

func BindableRoles() []Role {
	values := make([]Role, len(bindableRoles))
	copy(values, bindableRoles)
	return values
}

func NormalizeRole(value string) (Role, error) {
	role := Role(strings.ToLower(strings.TrimSpace(value)))
	switch role {
	case RoleInbox, RoleSent, RoleDrafts, RoleArchive, RoleTrash, RoleJunk:
		return role, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidRole, value)
	}
}

func (role Role) Bindable() bool {
	switch role {
	case RoleSent, RoleDrafts, RoleArchive, RoleTrash, RoleJunk:
		return true
	default:
		return false
	}
}

func (role Role) Kind() Kind {
	switch role {
	case RoleInbox:
		return KindInbox
	case RoleSent:
		return KindSent
	case RoleDrafts:
		return KindDrafts
	case RoleArchive:
		return KindArchive
	case RoleTrash:
		return KindTrash
	case RoleJunk:
		return KindJunk
	default:
		return KindOther
	}
}

type FolderRoleBinding struct {
	AccountID string    `json:"account_id"`
	Role      Role      `json:"role"`
	FolderID  string    `json:"folder_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type RoleAction string

const (
	RoleActionSet   RoleAction = "set"
	RoleActionClear RoleAction = "clear"
)

type FolderRolePlan struct {
	Action           RoleAction         `json:"action"`
	Role             Role               `json:"role"`
	FolderID         string             `json:"folder_id,omitempty"`
	FolderName       string             `json:"folder_name,omitempty"`
	FolderKind       Kind               `json:"folder_kind,omitempty"`
	FolderKindSource string             `json:"folder_kind_source,omitempty"`
	PreviousBinding  *FolderRoleBinding `json:"previous_binding,omitempty"`
}

type RoleResolutionSource string

const (
	RoleResolutionBinding  RoleResolutionSource = "binding"
	RoleResolutionProvider RoleResolutionSource = "provider"
	RoleResolutionFallback RoleResolutionSource = "fallback"
)

type FolderRoleResolution struct {
	Role    Role                 `json:"role"`
	Folder  Folder               `json:"folder"`
	Source  RoleResolutionSource `json:"source"`
	Binding *FolderRoleBinding   `json:"binding,omitempty"`
}

type InvalidRoleBindingError struct {
	Binding FolderRoleBinding
	Reason  string
}

func (err *InvalidRoleBindingError) Error() string {
	if err == nil {
		return ErrRoleBindingInvalid.Error()
	}
	reason := strings.TrimSpace(err.Reason)
	if reason == "" {
		reason = "bound folder is not current and selectable"
	}
	return fmt.Sprintf("%s: role=%s folder=%s: %s", ErrRoleBindingInvalid, err.Binding.Role, err.Binding.FolderID, reason)
}

func (err *InvalidRoleBindingError) Unwrap() error { return ErrRoleBindingInvalid }
