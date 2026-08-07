package store

import (
	"fmt"
	"strings"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

// locationCountsUnread is the canonical local eligibility rule used by every
// message/thread/search read model. Draft state is provider evidence, so an
// explicitly selected ordinary folder containing a \Draft location is still
// excluded even when the provider did not advertise the Drafts special-use
// role for that mailbox.
func locationCountsUnread(value maildomain.Location) bool {
	if !value.Present || !value.Flags.Eligible() || value.Flags.Seen || value.Flags.Draft {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(value.FolderKind)) {
	case "sent", "drafts":
		return false
	default:
		return true
	}
}

// unreadLocationSQL is the SQL form of locationCountsUnread. Keep all indexed
// aggregate/search predicates routed through this helper so changes to unread
// semantics cannot silently diverge between read surfaces.
func unreadLocationSQL(locationAlias, folderAlias string) string {
	return fmt.Sprintf("%s.present = 1 AND %s.deleted = 0 AND %s.seen = 0 AND %s.draft = 0 AND %s.kind NOT IN ('sent', 'drafts')",
		locationAlias, locationAlias, locationAlias, locationAlias, folderAlias)
}
