package cli

import (
	"context"
	"fmt"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type folderListData struct {
	AccountID     string          `json:"account_id"`
	Folders       []folder.Folder `json:"folders"`
	Count         int             `json:"count"`
	Total         int             `json:"total,omitempty"`
	More          bool            `json:"more,omitempty"`
	SchemaVersion int64           `json:"schema_version"`
}

type folderShowData struct {
	AccountID     string        `json:"account_id"`
	Folder        folder.Folder `json:"folder"`
	SchemaVersion int64         `json:"schema_version"`
}

type folderPolicyData struct {
	Outcome       string        `json:"outcome"`
	AccountID     string        `json:"account_id"`
	Folder        folder.Folder `json:"folder"`
	SchemaVersion int64         `json:"schema_version"`
}

func newMailCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "mail",
		Short:   "Inspect and synchronize iCloud Mail data",
		GroupID: "mail",
		Args:    cobra.NoArgs,
		Example: "  icloud mail sync\n  icloud mail inbox\n  icloud mail message show msg_…\n  icloud mail folders status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	command.AddCommand(newMailFoldersCommand(state))
	command.AddCommand(newMailSyncCommand(state))
	command.AddCommand(newMailInboxCommand(state))
	command.AddCommand(newMailMessageCommand(state))
	command.AddCommand(newMailThreadCommand(state))
	command.AddCommand(newMailLatestCommand(state))
	command.AddCommand(newMailAttachmentsCommand(state))
	command.AddCommand(newMailFlagMutationCommand(state, "mark-read", "Mark one selected message location as read", maildomain.FlagMutationMarkRead))
	command.AddCommand(newMailFlagMutationCommand(state, "mark-unread", "Mark one selected message location as unread", maildomain.FlagMutationMarkUnread))
	command.AddCommand(newMailFlagMutationCommand(state, "flag", "Flag one selected message location", maildomain.FlagMutationFlag))
	command.AddCommand(newMailFlagMutationCommand(state, "unflag", "Remove the flag from one selected message location", maildomain.FlagMutationUnflag))
	return command
}

func newMailFoldersCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "folders",
		Short:   "Discover folders, inspect provider status, and choose sync policy",
		Args:    cobra.NoArgs,
		Example: "  icloud mail folders status\n  icloud mail folders enable Receipts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	command.AddCommand(newFolderListCommand(state))
	command.AddCommand(newFolderShowCommand(state))
	command.AddCommand(newFolderStatusCommand(state))
	command.AddCommand(newFolderPolicyCommand(state, true))
	command.AddCommand(newFolderPolicyCommand(state, false))
	return command
}

func newFolderListCommand(state *executionState) *cobra.Command {
	var jsonOutput, verbose, includeMissing bool
	var limit int
	command := &cobra.Command{
		Use:     "list",
		Short:   "List locally discovered folders without contacting iCloud",
		Args:    cobra.NoArgs,
		Example: "  icloud mail folders list\n  icloud mail folders list --include-missing --verbose",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, Verbose: verbose}, OutputSupport{JSON: true, Verbose: true}); err != nil {
				return err
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			accountValue, values, more, schemaVersion, err := state.folders.FolderList(cmd.Context(), state.global.Home, state.global.Account, limit, includeMissing)
			if err != nil {
				return err
			}
			data := folderListData{AccountID: accountValue.ID, Folders: values, Count: len(values), More: more, SchemaVersion: schemaVersion}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, value := range values {
				if err := renderFolder(renderer, value, verbose); err != nil {
					return err
				}
			}
			return renderer.Summary("folders", Field("account", accountValue.ID), Field("count", fmt.Sprintf("%d", len(values))), Field("more", more))
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum folders to return (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&includeMissing, "include-missing", false, "Include folders missing from the latest provider discovery")
	command.Flags().BoolVar(&verbose, "verbose", false, "Include wire names, attributes, UID metadata, and timestamps")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func newFolderShowCommand(state *executionState) *cobra.Command {
	var jsonOutput, verbose bool
	command := &cobra.Command{
		Use:     "show <folder-id|name|wire-name|kind>",
		Short:   "Show one locally discovered folder",
		Args:    cobra.ExactArgs(1),
		Example: "  icloud mail folders show INBOX\n  icloud mail folders show fld_… --verbose",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, Verbose: verbose}, OutputSupport{JSON: true, Verbose: true}); err != nil {
				return err
			}
			accountValue, value, schemaVersion, err := state.folders.FolderShow(cmd.Context(), state.global.Home, state.global.Account, args[0])
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: folderShowData{AccountID: accountValue.ID, Folder: value, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderFolder(NewCompactTextRenderer(state.stdout), value, verbose)
		},
	}
	command.Flags().BoolVar(&verbose, "verbose", false, "Include wire names, attributes, UID metadata, and timestamps")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func newFolderStatusCommand(state *executionState) *cobra.Command {
	var jsonOutput, verbose bool
	var limit int
	command := &cobra.Command{
		Use:     "status",
		Short:   "Refresh every remote folder and its provider counts without fetching messages",
		Args:    cobra.NoArgs,
		Example: "  icloud mail folders status\n  icloud --account Personal mail folders status --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, Verbose: verbose}, OutputSupport{JSON: true, Verbose: true}); err != nil {
				return err
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			accountValue, values, schemaVersion, err := state.folders.FolderStatus(cmd.Context(), state.global.Home, state.global.Account)
			if err != nil {
				return err
			}
			shown := values
			more := len(values) > limit
			if more {
				shown = values[:limit]
			}
			data := folderListData{AccountID: accountValue.ID, Folders: shown, Count: len(shown), More: more, SchemaVersion: schemaVersion}
			if more {
				data.Total = len(values)
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			present, stale, enabled := 0, 0, 0
			for _, value := range values {
				if value.Present {
					present++
				}
				if value.Stale {
					stale++
				}
				if value.SyncEnabled {
					enabled++
				}
			}
			for _, value := range shown {
				if err := renderFolder(renderer, value, verbose); err != nil {
					return err
				}
			}
			var total any
			if more {
				total = len(values)
			}
			return renderer.Summary("folder-status",
				Field("account", accountValue.ID),
				Field("count", fmt.Sprintf("%d", len(shown))),
				Field("total", total),
				Field("more", more),
				Field("present", fmt.Sprintf("%d", present)),
				Field("stale", fmt.Sprintf("%d", stale)),
				Field("enabled", fmt.Sprintf("%d", enabled)),
			)
		},
	}
	command.Flags().BoolVar(&verbose, "verbose", false, "Include wire names, attributes, UID metadata, and timestamps")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum folder records to print after refreshing all remote folders (1-%d)", MaximumPageSize))
	return command
}

func newFolderPolicyCommand(state *executionState, enabled bool) *cobra.Command {
	name, outcome, short := "disable", "disabled", "Disable local synchronization policy for one folder"
	if enabled {
		name, outcome, short = "enable", "enabled", "Enable local synchronization policy for one folder"
	}
	var jsonOutput bool
	command := &cobra.Command{
		Use:     name + " <folder-id|name|wire-name|kind>",
		Short:   short,
		Args:    cobra.ExactArgs(1),
		Example: "  icloud mail folders " + name + " Receipts",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			accountValue, value, schemaVersion, err := state.folders.FolderSetSync(cmd.Context(), state.global.Home, state.global.Account, args[0], enabled)
			if err != nil {
				return err
			}
			data := folderPolicyData{Outcome: outcome, AccountID: accountValue.ID, Folder: value, SchemaVersion: schemaVersion}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("folder-policy", value.ID, Field("sync", outcome), Field("present", yesNo(value.Present)))
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func renderFolder(renderer *CompactTextRenderer, value folder.Folder, verbose bool) error {
	fields := []CompactField{
		Field("name", value.Name),
		Field("kind", value.Kind),
		Field("parent", value.ParentName),
		Field("selectable", yesNo(value.Selectable)),
		Field("subscription", value.Subscription),
		Field("present", yesNo(value.Present)),
		Field("stale", value.Stale),
		Field("sync", enabledDisabled(value.SyncEnabled)),
	}
	if value.StatusAt != nil {
		fields = append(fields,
			Field("messages", value.Messages),
			Field("unseen", value.Unseen),
		)
	}
	if verbose {
		fields = append(fields,
			Field("wire_name", value.WireName),
			Field("parent_wire", value.ParentWireName),
			Field("delimiter", value.Delimiter),
			Field("attributes", value.Attributes),
			Field("kind_source", value.KindSource),
			Field("uid_next", value.UIDNext),
			Field("uid_validity", value.UIDValidity),
			Field("discovered_at", value.DiscoveredAt),
			Field("status_at", value.StatusAt),
			Field("missing_since", value.MissingSince),
		)
	}
	return renderer.Record("folder", value.ID, fields...)
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func enabledDisabled(value bool) string {
	if value {
		return "enabled"
	}
	return "disabled"
}

type unavailableFolderService struct{}

func (unavailableFolderService) FolderList(context.Context, string, string, int, bool) (account.Account, []folder.Folder, bool, int64, error) {
	return account.Account{}, nil, false, 0, NewAppError(ErrorConfiguration, "folder state is unavailable", false)
}

func (unavailableFolderService) FolderShow(context.Context, string, string, string) (account.Account, folder.Folder, int64, error) {
	return account.Account{}, folder.Folder{}, 0, NewAppError(ErrorConfiguration, "folder state is unavailable", false)
}

func (unavailableFolderService) FolderStatus(context.Context, string, string) (account.Account, []folder.Folder, int64, error) {
	return account.Account{}, nil, 0, NewAppError(ErrorConfiguration, "folder discovery is unavailable", false)
}

func (unavailableFolderService) FolderSetSync(context.Context, string, string, string, bool) (account.Account, folder.Folder, int64, error) {
	return account.Account{}, folder.Folder{}, 0, NewAppError(ErrorConfiguration, "folder state is unavailable", false)
}
