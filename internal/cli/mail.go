package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type MailService interface {
	MailSync(context.Context, string, string, int) (account.Account, folder.Folder, maildomain.SyncSummary, int64, error)
	MailInbox(context.Context, string, string, int) (account.Account, folder.Folder, []maildomain.Message, bool, int64, error)
	MailSyncStatus(context.Context, string, string) (account.Account, folder.Folder, maildomain.Checkpoint, int64, error)
	MailSyncCheckpoints(context.Context, string, string, int) (account.Account, []maildomain.Checkpoint, bool, int64, error)
	MailMessage(context.Context, string, string, string) (account.Account, maildomain.Message, maildomain.ContentSource, int64, error)
}

type mailSyncData struct {
	Summary       maildomain.SyncSummary `json:"summary"`
	SchemaVersion int64                  `json:"schema_version"`
}

type mailInboxData struct {
	AccountID     string          `json:"account_id"`
	FolderID      string          `json:"folder_id"`
	Messages      []publicMessage `json:"messages"`
	Count         int             `json:"count"`
	More          bool            `json:"more,omitempty"`
	SchemaVersion int64           `json:"schema_version"`
}

type mailSyncStatusData struct {
	AccountID     string                `json:"account_id"`
	Folder        folder.Folder         `json:"folder"`
	Checkpoint    maildomain.Checkpoint `json:"checkpoint"`
	SchemaVersion int64                 `json:"schema_version"`
}

type mailCheckpointData struct {
	AccountID     string                  `json:"account_id"`
	Checkpoints   []maildomain.Checkpoint `json:"checkpoints"`
	Count         int                     `json:"count"`
	More          bool                    `json:"more,omitempty"`
	SchemaVersion int64                   `json:"schema_version"`
}

type publicMessage struct {
	ID              string                `json:"id"`
	AccountID       string                `json:"account_id"`
	RFCMessageID    string                `json:"rfc_message_id,omitempty"`
	InReplyTo       string                `json:"in_reply_to,omitempty"`
	References      []string              `json:"references,omitempty"`
	Direction       maildomain.Direction  `json:"direction"`
	From            maildomain.Address    `json:"from"`
	ReplyTo         *maildomain.Address   `json:"reply_to,omitempty"`
	To              []maildomain.Address  `json:"to,omitempty"`
	CC              []maildomain.Address  `json:"cc,omitempty"`
	Subject         string                `json:"subject,omitempty"`
	Snippet         string                `json:"snippet,omitempty"`
	HeaderDate      *time.Time            `json:"header_date,omitempty"`
	InternalDate    *time.Time            `json:"internal_date,omitempty"`
	SortAt          *time.Time            `json:"sort_at,omitempty"`
	DateSource      maildomain.DateSource `json:"date_source"`
	AttachmentCount int                   `json:"attachment_count,omitempty"`
	Unread          bool                  `json:"unread"`
	Flagged         bool                  `json:"flagged"`
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
	Locations       []publicLocation      `json:"locations,omitempty"`
}

type publicLocation struct {
	ID           string     `json:"id"`
	FolderID     string     `json:"folder_id"`
	UIDValidity  uint32     `json:"uid_validity"`
	UID          uint32     `json:"uid"`
	Flags        []string   `json:"flags,omitempty"`
	InternalDate *time.Time `json:"internal_date,omitempty"`
	RFC822Size   int64      `json:"rfc822_size,omitempty"`
	Present      bool       `json:"present"`
}

func newMailSyncCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var limit int
	command := &cobra.Command{
		Use:     "sync",
		Short:   "Fetch and atomically commit one bounded Inbox page",
		Args:    cobra.NoArgs,
		Example: "  icloud mail sync\n  icloud mail sync --limit 50 --json\n  icloud mail sync status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			_, _, summary, schemaVersion, err := state.mail.MailSync(cmd.Context(), state.global.Home, state.global.Account, limit)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSyncData{Summary: summary, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("mail-sync", summary.FolderID,
				Field("account", summary.AccountID),
				Field("folder", summary.FolderName),
				Field("fetched", fmt.Sprintf("%d", summary.Fetched)),
				Field("created", fmt.Sprintf("%d", summary.Created)),
				Field("updated", fmt.Sprintf("%d", summary.Updated)),
				Field("last_uid", summary.LastUID),
				Field("more", fmt.Sprintf("%t", summary.MoreAvailable)),
				Field("bootstrap", fmt.Sprintf("%t", summary.Bootstrap)),
			)
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum Inbox messages to fetch and commit (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	command.AddCommand(newMailSyncStatusCommand(state))
	command.AddCommand(newMailSyncCheckpointsCommand(state))
	return command
}

func newMailInboxCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var limit int
	command := &cobra.Command{
		Use:     "inbox",
		Short:   "Read a bounded page from the locally synchronized Inbox",
		Args:    cobra.NoArgs,
		Example: "  icloud mail inbox\n  icloud mail inbox --limit 50\n  icloud mail inbox --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			accountValue, inbox, messages, more, schemaVersion, err := state.mail.MailInbox(cmd.Context(), state.global.Home, state.global.Account, limit)
			if err != nil {
				return err
			}
			if jsonOutput {
				public := make([]publicMessage, 0, len(messages))
				for _, message := range messages {
					public = append(public, sanitizeMessage(message))
				}
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailInboxData{AccountID: accountValue.ID, FolderID: inbox.ID, Messages: public, Count: len(public), More: more, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, message := range messages {
				if err := renderInboxMessage(renderer, message); err != nil {
					return err
				}
			}
			return renderer.Summary("inbox", Field("account", accountValue.ID), Field("count", fmt.Sprintf("%d", len(messages))), Field("more", fmt.Sprintf("%t", more)))
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum local Inbox messages to return (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func newMailSyncStatusCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:     "status",
		Short:   "Show the local Inbox synchronization frontier",
		Args:    cobra.NoArgs,
		Example: "  icloud mail sync status\n  icloud mail sync status --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			accountValue, inbox, checkpoint, schemaVersion, err := state.mail.MailSyncStatus(cmd.Context(), state.global.Home, state.global.Account)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSyncStatusData{AccountID: accountValue.ID, Folder: inbox, Checkpoint: checkpoint, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderCheckpoint(NewCompactTextRenderer(state.stdout), "sync-status", accountValue.ID, inbox.Name, checkpoint)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func newMailSyncCheckpointsCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var limit int
	command := &cobra.Command{
		Use:     "checkpoints",
		Short:   "List bounded local synchronization checkpoints",
		Args:    cobra.NoArgs,
		Example: "  icloud mail sync checkpoints\n  icloud mail sync checkpoints --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			accountValue, values, more, schemaVersion, err := state.mail.MailSyncCheckpoints(cmd.Context(), state.global.Home, state.global.Account, limit)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailCheckpointData{AccountID: accountValue.ID, Checkpoints: values, Count: len(values), More: more, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, checkpoint := range values {
				if err := renderCheckpoint(renderer, "checkpoint", accountValue.ID, "", checkpoint); err != nil {
					return err
				}
			}
			return renderer.Summary("checkpoints", Field("account", accountValue.ID), Field("count", fmt.Sprintf("%d", len(values))), Field("more", fmt.Sprintf("%t", more)))
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum checkpoints to return (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func renderInboxMessage(renderer *CompactTextRenderer, value maildomain.Message) error {
	from := value.From.Email
	if strings.TrimSpace(from) == "" {
		from = "unknown"
	}
	return renderer.Record("message", value.ID,
		Field("from", from),
		Field("subject", value.Subject),
		Field("at", value.SortAt),
		Field("unread", value.Unread),
		Field("flagged", value.Flagged),
		Field("attachments", value.AttachmentCount),
		Field("snippet", Excerpt(value.Snippet, DefaultExcerptRunes)),
	)
}

func renderCheckpoint(renderer *CompactTextRenderer, kind, accountID, folderName string, value maildomain.Checkpoint) error {
	state := value.State
	if state == "" {
		state = maildomain.CheckpointIdle
	}
	recordID := value.ID
	if recordID == "" {
		recordID = value.FolderID
	}
	return renderer.Record(kind, recordID,
		Field("account", accountID),
		Field("folder", folderName),
		Field("folder_id", value.FolderID),
		Field("state", state),
		Field("uid_validity", fmt.Sprintf("%d", value.UIDValidity)),
		Field("committed", fmt.Sprintf("%d", value.LastCommittedUID)),
		Field("scanned", fmt.Sprintf("%d", value.LastScannedUID)),
		Field("bootstrap", fmt.Sprintf("%t", value.BootstrapComplete)),
		Field("more", fmt.Sprintf("%t", value.MoreAvailable)),
		Field("last_error", value.LastErrorCode),
		Field("last_success", value.LastSuccessAt),
	)
}

func publicReplyTo(value maildomain.Address) *maildomain.Address {
	if strings.TrimSpace(value.Email) == "" && strings.TrimSpace(value.DisplayName) == "" {
		return nil
	}
	copy := value
	return &copy
}

func sanitizeMessage(value maildomain.Message) publicMessage {
	locations := make([]publicLocation, 0, len(value.Locations))
	for _, location := range value.Locations {
		locations = append(locations, publicLocation{
			ID: location.ID, FolderID: location.FolderID, UIDValidity: location.UIDValidity,
			UID: location.UID, Flags: append([]string(nil), location.Flags...),
			InternalDate: location.InternalDate, RFC822Size: location.RFC822Size, Present: location.Present,
		})
	}
	return publicMessage{
		ID: value.ID, AccountID: value.AccountID, RFCMessageID: value.RFCMessageID,
		InReplyTo: value.InReplyTo, References: append([]string(nil), value.References...),
		Direction: value.Direction, From: value.From, ReplyTo: publicReplyTo(value.ReplyTo),
		To: append([]maildomain.Address(nil), value.To...), CC: append([]maildomain.Address(nil), value.CC...),
		Subject: value.Subject, Snippet: value.Snippet, HeaderDate: value.HeaderDate,
		InternalDate: value.InternalDate, SortAt: value.SortAt, DateSource: value.DateSource,
		AttachmentCount: value.AttachmentCount, Unread: value.Unread, Flagged: value.Flagged,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, Locations: locations,
	}
}

type unavailableMailService struct{}

func (unavailableMailService) MailSync(context.Context, string, string, int) (account.Account, folder.Folder, maildomain.SyncSummary, int64, error) {
	return account.Account{}, folder.Folder{}, maildomain.SyncSummary{}, 0, NewAppError(ErrorConfiguration, "mail synchronization is unavailable", false)
}
func (unavailableMailService) MailInbox(context.Context, string, string, int) (account.Account, folder.Folder, []maildomain.Message, bool, int64, error) {
	return account.Account{}, folder.Folder{}, nil, false, 0, NewAppError(ErrorConfiguration, "local Inbox is unavailable", false)
}
func (unavailableMailService) MailSyncStatus(context.Context, string, string) (account.Account, folder.Folder, maildomain.Checkpoint, int64, error) {
	return account.Account{}, folder.Folder{}, maildomain.Checkpoint{}, 0, NewAppError(ErrorConfiguration, "mail synchronization status is unavailable", false)
}
func (unavailableMailService) MailSyncCheckpoints(context.Context, string, string, int) (account.Account, []maildomain.Checkpoint, bool, int64, error) {
	return account.Account{}, nil, false, 0, NewAppError(ErrorConfiguration, "mail synchronization checkpoints are unavailable", false)
}
func (unavailableMailService) MailMessage(context.Context, string, string, string) (account.Account, maildomain.Message, maildomain.ContentSource, int64, error) {
	return account.Account{}, maildomain.Message{}, nil, 0, NewAppError(ErrorConfiguration, "local message content is unavailable", false)
}
