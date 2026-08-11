package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

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
	NextCursor    string          `json:"next_cursor,omitempty"`
	SchemaVersion int64           `json:"schema_version"`
}

type folderListPager interface {
	FolderListPage(context.Context, string, string, int, bool, string) (account.Account, []folder.Folder, bool, string, int64, error)
}

type folderStatusPager interface {
	FolderStatusPage(context.Context, string, string, int, string) (account.Account, []folder.Folder, bool, string, int, int64, error)
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

type folderAdminPreviewData struct {
	Mode          string           `json:"mode"`
	Plan          folder.AdminPlan `json:"plan"`
	Safety        string           `json:"safety"`
	SchemaVersion int64            `json:"schema_version"`
}

type folderAdminData struct {
	AccountID     string              `json:"account_id"`
	Outcome       folder.AdminOutcome `json:"outcome"`
	SchemaVersion int64               `json:"schema_version"`
}

type folderRoleListData struct {
	AccountID     string                     `json:"account_id"`
	Bindings      []folder.FolderRoleBinding `json:"bindings"`
	Count         int                        `json:"count"`
	SchemaVersion int64                      `json:"schema_version"`
}

type folderRolePreviewData struct {
	Mode          string                `json:"mode"`
	Plan          folder.FolderRolePlan `json:"plan"`
	SchemaVersion int64                 `json:"schema_version"`
}

type folderRoleData struct {
	Action        folder.RoleAction        `json:"action"`
	AccountID     string                   `json:"account_id"`
	Binding       folder.FolderRoleBinding `json:"binding"`
	Folder        *folder.Folder           `json:"folder,omitempty"`
	SchemaVersion int64                    `json:"schema_version"`
}

func newMailCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "mail",
		Short:   "Send, reply, forward, inspect, synchronize, and change exact iCloud Mail locations",
		GroupID: "mail",
		Args:    cobra.NoArgs,
		Example: "  icloud mail send --to client@example.com --subject Update --body-file message.txt --dry-run\n  icloud mail reply msg_… --body-file reply.txt --dry-run\n  icloud mail forward msg_… --to teammate@example.com --dry-run\n  icloud mail move msg_… --from inbox --to Projects --dry-run\n  icloud mail sync\n  icloud mail messages list\n  icloud mail message show msg_…\n  icloud mail folders refresh",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	command.AddCommand(newMailFoldersCommand(state))
	command.AddCommand(newMailIdentitiesCommand(state))
	command.AddCommand(newMailSettingsCommand(state))
	command.AddCommand(newMailSyncCommand(state))
	command.AddCommand(newMailWatchCommand(state))
	command.AddCommand(newMailDraftCommand(state))
	command.AddCommand(newMailMessagesCommand(state))
	command.AddCommand(newMailInboxCommand(state))
	command.AddCommand(newMailSearchCommand(state))
	command.AddCommand(newMailSendersCommand(state))
	command.AddCommand(newMailSignaturesCommand(state))
	command.AddCommand(newMailMessageCommand(state))
	command.AddCommand(newMailMailboxCommand(state))
	command.AddCommand(newMailThreadsCommand(state))
	command.AddCommand(newMailSelectionCommand(state))
	command.AddCommand(newMailEmptyCommand(state))
	command.AddCommand(newMailThreadCommand(state))
	command.AddCommand(newMailLatestCommand(state))
	command.AddCommand(newMailAttachmentsCommand(state))
	command.AddCommand(newMailSendCommand(state))
	command.AddCommand(newMailUnsubscribeCommand(state))
	command.AddCommand(newMailReplyCommand(state, false))
	command.AddCommand(newMailReplyCommand(state, true))
	command.AddCommand(newMailForwardCommand(state))
	command.AddCommand(newMailOperationsCommand(state))
	command.AddCommand(newMailStatusCommand(state))
	command.AddCommand(newMailFlagMutationCommand(state, "mark-read", "Mark one selected message location as read", maildomain.FlagMutationMarkRead))
	command.AddCommand(newMailFlagMutationCommand(state, "mark-unread", "Mark one selected message location as unread", maildomain.FlagMutationMarkUnread))
	command.AddCommand(newMailFlagMutationCommand(state, "flag", "Flag one selected message location", maildomain.FlagMutationFlag))
	command.AddCommand(newMailFlagMutationCommand(state, "unflag", "Remove the flag from one selected message location", maildomain.FlagMutationUnflag))
	command.AddCommand(newMailLocationMutationCommand(state, maildomain.LocationActionMove))
	command.AddCommand(newMailLocationMutationCommand(state, maildomain.LocationActionCopy))
	command.AddCommand(newMailLocationMutationCommand(state, maildomain.LocationActionArchive))
	command.AddCommand(newMailLocationMutationCommand(state, maildomain.LocationActionUnarchive))
	command.AddCommand(newMailLocationMutationCommand(state, maildomain.LocationActionTrash))
	command.AddCommand(newMailLocationMutationCommand(state, maildomain.LocationActionRestore))
	command.AddCommand(newMailLocationMutationCommand(state, maildomain.LocationActionJunk))
	command.AddCommand(newMailLocationMutationCommand(state, maildomain.LocationActionNotJunk))
	command.AddCommand(newMailLocationMutationCommand(state, maildomain.LocationActionDelete))
	return command
}

func newMailFoldersCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "folders",
		Short:   "Discover folders, inspect provider status, and choose sync policy",
		Args:    cobra.NoArgs,
		Example: "  icloud mail folders refresh\n  icloud mail folders enable Receipts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	command.AddCommand(newFolderListCommand(state))
	command.AddCommand(newFolderShowCommand(state))
	command.AddCommand(newFolderRefreshCommand(state))
	command.AddCommand(newFolderStatusCommand(state))
	command.AddCommand(newFolderPolicyCommand(state, true))
	command.AddCommand(newFolderPolicyCommand(state, false))
	command.AddCommand(newFolderRoleCommand(state))
	command.AddCommand(newFolderAdminCommand(state, folder.AdminCreate))
	command.AddCommand(newFolderAdminCommand(state, folder.AdminRename))
	command.AddCommand(newFolderAdminCommand(state, folder.AdminDelete))
	command.AddCommand(newFolderAdminCommand(state, folder.AdminSubscribe))
	command.AddCommand(newFolderAdminCommand(state, folder.AdminUnsubscribe))
	return command
}

func newFolderListCommand(state *executionState) *cobra.Command {
	var jsonOutput, verbose, includeMissing bool
	var limit int
	var cursor string
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "list",
		Short:   "List locally discovered folders without contacting iCloud",
		Args:    cobra.NoArgs,
		Example: "  icloud mail folders list\n  icloud mail folders list --include-missing --verbose\n  icloud mail folders list --input-json request.json --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			request := folderListReadJSONRequest{Limit: DefaultPageSize}
			if used, err := readJSONInput(cmd, state, input, &request, "limit", "cursor", "include-missing", "verbose"); err != nil {
				return err
			} else if used {
				limit = request.Limit
				cursor = request.Cursor
				includeMissing = request.IncludeMissing
				verbose = request.Verbose
			}
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, Verbose: verbose}, OutputSupport{JSON: true, Verbose: true}); err != nil {
				return err
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			var accountValue account.Account
			var values []folder.Folder
			var more bool
			var nextCursor string
			var schemaVersion int64
			var err error
			if pager, ok := state.folders.(folderListPager); ok {
				accountValue, values, more, nextCursor, schemaVersion, err = pager.FolderListPage(cmd.Context(), state.global.Home, state.global.Account, limit, includeMissing, cursor)
			} else {
				if strings.TrimSpace(cursor) != "" {
					return NewAppError(ErrorConfiguration, "folder continuation is unavailable", false)
				}
				accountValue, values, more, schemaVersion, err = state.folders.FolderList(cmd.Context(), state.global.Home, state.global.Account, limit, includeMissing)
			}
			if err != nil {
				return err
			}
			data := folderListData{AccountID: accountValue.ID, Folders: values, Count: len(values), More: more, NextCursor: nextCursor, SchemaVersion: schemaVersion}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, value := range values {
				if err := renderFolder(renderer, value, verbose); err != nil {
					return err
				}
			}
			return renderer.Summary("folders", Field("account", accountValue.ID), Field("count", fmt.Sprintf("%d", len(values))), Field("more", more), Field("next_cursor", nextCursor))
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum folders to return (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&includeMissing, "include-missing", false, "Include folders missing from the latest provider discovery")
	command.Flags().BoolVar(&verbose, "verbose", false, "Include wire names, attributes, UID metadata, and timestamps")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	command.Flags().StringVar(&cursor, "cursor", "", "Continue from a next_cursor returned by an earlier folder page")
	bindReadJSONInput(command, &input)
	return command
}

func newFolderShowCommand(state *executionState) *cobra.Command {
	var jsonOutput, verbose bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "show <folder-id|name|wire-name|kind>",
		Short:   "Show one locally discovered folder",
		Args:    exactReadArgsOrJSONInput(&input, 1),
		Example: "  icloud mail folders show INBOX\n  icloud mail folders show fld_… --verbose\n  icloud mail folders show --input-json request.json --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := ""
			if len(args) == 1 {
				selector = args[0]
			}
			request := folderShowReadJSONRequest{Folder: selector}
			if used, err := readJSONInput(cmd, state, input, &request, "verbose"); err != nil {
				return err
			} else if used {
				selector = request.Folder
				verbose = request.Verbose
			}
			if strings.TrimSpace(selector) == "" {
				return NewAppError(ErrorUsage, "folder is required", false)
			}
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, Verbose: verbose}, OutputSupport{JSON: true, Verbose: true}); err != nil {
				return err
			}
			accountValue, value, schemaVersion, err := state.folders.FolderShow(cmd.Context(), state.global.Home, state.global.Account, selector)
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
	bindReadJSONInput(command, &input)
	return command
}

func newFolderRefreshCommand(state *executionState) *cobra.Command {
	return newFolderRefreshLeafCommand(
		state,
		"refresh",
		"Refresh every remote folder and its provider counts without fetching messages",
		"  icloud mail folders refresh\n  icloud --account Personal mail folders refresh --json\n  icloud mail folders refresh --input-json request.json --json",
	)
}

func newFolderStatusCommand(state *executionState) *cobra.Command {
	return newFolderRefreshLeafCommand(
		state,
		"status",
		"Compatibility alias for `mail folders refresh`",
		"  icloud mail folders refresh\n  icloud --account Personal mail folders refresh --json",
	)
}

func newFolderRefreshLeafCommand(state *executionState, use, short, example string) *cobra.Command {
	var jsonOutput, verbose bool
	var limit int
	var cursor string
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     use,
		Short:   short,
		Args:    cobra.NoArgs,
		Example: example,
		RunE: func(cmd *cobra.Command, _ []string) error {
			request := folderRefreshReadJSONRequest{Limit: DefaultPageSize}
			if used, err := readJSONInput(cmd, state, input, &request, "limit", "cursor", "verbose"); err != nil {
				return err
			} else if used {
				limit = request.Limit
				cursor = request.Cursor
				verbose = request.Verbose
			}
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, Verbose: verbose}, OutputSupport{JSON: true, Verbose: true}); err != nil {
				return err
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			var accountValue account.Account
			var shown []folder.Folder
			var more bool
			var nextCursor string
			var total int
			var schemaVersion int64
			var err error
			if pager, ok := state.folders.(folderStatusPager); ok {
				accountValue, shown, more, nextCursor, total, schemaVersion, err = pager.FolderStatusPage(cmd.Context(), state.global.Home, state.global.Account, limit, cursor)
			} else {
				if strings.TrimSpace(cursor) != "" {
					return NewAppError(ErrorConfiguration, "folder status continuation is unavailable", false)
				}
				var values []folder.Folder
				accountValue, values, schemaVersion, err = state.folders.FolderStatus(cmd.Context(), state.global.Home, state.global.Account)
				if err == nil {
					total = len(values)
					shown = values
					more = len(shown) > limit
					if more {
						shown = shown[:limit]
					}
				}
			}
			if err != nil {
				return err
			}
			data := folderListData{AccountID: accountValue.ID, Folders: shown, Count: len(shown), More: more, NextCursor: nextCursor, SchemaVersion: schemaVersion}
			if more || total != len(shown) {
				data.Total = total
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			present, stale, enabled := 0, 0, 0
			for _, value := range shown {
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
			var totalValue any
			if more {
				totalValue = total
			}
			return renderer.Summary("folder-status",
				Field("account", accountValue.ID),
				Field("count", fmt.Sprintf("%d", len(shown))),
				Field("total", totalValue),
				Field("more", more),
				Field("next_cursor", nextCursor),
				Field("present", fmt.Sprintf("%d", present)),
				Field("stale", fmt.Sprintf("%d", stale)),
				Field("enabled", fmt.Sprintf("%d", enabled)),
			)
		},
	}
	command.Flags().BoolVar(&verbose, "verbose", false, "Include wire names, attributes, UID metadata, and timestamps")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum folder records to print after refreshing all remote folders (1-%d)", MaximumPageSize))
	command.Flags().StringVar(&cursor, "cursor", "", "Continue from a next_cursor returned by an earlier refreshed folder page")
	bindReadJSONInput(command, &input)
	return command
}

func newFolderPolicyCommand(state *executionState, enabled bool) *cobra.Command {
	name, outcome, short := "disable", "disabled", "Disable local synchronization policy for one folder"
	if enabled {
		name, outcome, short = "enable", "enabled", "Enable local synchronization policy for one folder"
	}
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     name + " <folder-id|name|wire-name|kind>",
		Short:   short,
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail folders " + name + " Receipts",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			selector := ""
			if len(args) == 1 {
				selector = args[0]
			}
			var request folderPolicyJSONRequest
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				selector = request.Folder
			}
			accountValue, value, schemaVersion, err := state.folders.FolderSetSync(cmd.Context(), state.global.Home, state.global.Account, selector, enabled)
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
	bindJSONInput(command, &input)
	return command
}

func newFolderRoleCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:   "role",
		Short: "Manage local semantic folder-role bindings without changing provider folder metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	command.AddCommand(newFolderRoleListCommand(state))
	command.AddCommand(newFolderRoleSetCommand(state))
	command.AddCommand(newFolderRoleClearCommand(state))
	return command
}

func newFolderRoleListCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:     "list",
		Short:   "List account-owned semantic folder-role bindings from local state",
		Args:    cobra.NoArgs,
		Example: "  icloud mail folders role list\n  icloud --account Personal mail folders role list --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			accountValue, bindings, schemaVersion, err := state.folders.FolderRoleList(cmd.Context(), state.global.Home, state.global.Account)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: folderRoleListData{
					AccountID: accountValue.ID, Bindings: bindings, Count: len(bindings), SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, binding := range bindings {
				if err := renderer.Record("folder-role", string(binding.Role), Field("folder_id", binding.FolderID)); err != nil {
					return err
				}
			}
			return renderer.Summary("folder-roles", Field("account", accountValue.ID), Field("count", len(bindings)))
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func newFolderRoleSetCommand(state *executionState) *cobra.Command {
	var dryRun, jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "set <sent|drafts|archive|trash|junk> <folder-id|name|wire-name>",
		Short:   "Bind one semantic role to one stable local folder ID",
		Args:    exactArgsOrJSONInput(&input, 2),
		Example: "  icloud mail folders role set archive fld_… --dry-run\n  icloud mail folders role set drafts 'Drafts Local'",
		RunE: func(cmd *cobra.Command, args []string) error {
			roleValue, folderSelector := "", ""
			if len(args) == 2 {
				roleValue, folderSelector = args[0], args[1]
			}
			var request folderRoleSetJSONRequest
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				roleValue, folderSelector = request.Role, request.Folder
			}
			if dryRun {
				_, plan, schemaVersion, err := state.folders.FolderRoleSetPreview(cmd.Context(), state.global.Home, state.global.Account, roleValue, folderSelector)
				if err != nil {
					return err
				}
				return renderFolderRolePreview(state, plan, schemaVersion, jsonOutput)
			}
			accountValue, binding, folderValue, schemaVersion, err := state.folders.FolderRoleSet(cmd.Context(), state.global.Home, state.global.Account, roleValue, folderSelector)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: folderRoleData{
					Action: folder.RoleActionSet, AccountID: accountValue.ID, Binding: binding, Folder: &folderValue, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("folder-role", string(binding.Role),
				Field("folder_id", binding.FolderID), Field("folder", folderValue.Name),
				Field("provider_kind", folderValue.Kind), Field("provider_kind_source", folderValue.KindSource), Field("action", "set"))
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the local binding change without writing SQLite or contacting iCloud")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newFolderRoleClearCommand(state *executionState) *cobra.Command {
	var dryRun, jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "clear <sent|drafts|archive|trash|junk>",
		Short:   "Remove one local semantic folder-role binding",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail folders role clear archive --dry-run\n  icloud mail folders role clear archive",
		RunE: func(cmd *cobra.Command, args []string) error {
			roleValue := ""
			if len(args) == 1 {
				roleValue = args[0]
			}
			var request folderRoleJSONRequest
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				roleValue = request.Role
			}
			if dryRun {
				_, plan, schemaVersion, err := state.folders.FolderRoleClearPreview(cmd.Context(), state.global.Home, state.global.Account, roleValue)
				if err != nil {
					return err
				}
				return renderFolderRolePreview(state, plan, schemaVersion, jsonOutput)
			}
			accountValue, binding, schemaVersion, err := state.folders.FolderRoleClear(cmd.Context(), state.global.Home, state.global.Account, roleValue)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: folderRoleData{
					Action: folder.RoleActionClear, AccountID: accountValue.ID, Binding: binding, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("folder-role", string(binding.Role), Field("folder_id", binding.FolderID), Field("action", "cleared"))
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the local binding removal without writing SQLite or contacting iCloud")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func renderFolderRolePreview(state *executionState, plan folder.FolderRolePlan, schemaVersion int64, jsonOutput bool) error {
	if jsonOutput {
		return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: folderRolePreviewData{Mode: "dry_run", Plan: plan, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
	}
	return NewCompactTextRenderer(state.stdout).Record("folder-role-preview", string(plan.Role),
		Field("action", plan.Action), Field("folder_id", plan.FolderID), Field("folder", plan.FolderName),
		Field("provider_kind", plan.FolderKind), Field("provider_kind_source", plan.FolderKindSource), Field("provider_contact", "false"))
}

func newFolderAdminCommand(state *executionState, action folder.AdminAction) *cobra.Command {
	use, short, example := folderAdminCommandMetadata(action)
	var dryRun, yes, jsonOutput, allowNonEmpty bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:   use,
		Short: short,
		Args: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(input.Path) != "" {
				if len(args) != 0 {
					return NewAppError(ErrorUsage, "positional write arguments cannot be combined with --input-json", false)
				}
				return nil
			}
			return folderAdminArgs(action)(cmd, args)
		},
		Example: example,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			if dryRun && yes {
				return NewAppError(ErrorUsage, "--dry-run and --yes cannot be combined", false)
			}
			request := folder.AdminRequest{Action: action, AllowNonEmpty: allowNonEmpty}
			if len(args) > 0 {
				switch action {
				case folder.AdminCreate:
					request.Name = args[0]
				case folder.AdminRename:
					request.Folder, request.Name = args[0], args[1]
				default:
					request.Folder = args[0]
				}
			}
			if strings.TrimSpace(input.Path) != "" {
				switch action {
				case folder.AdminCreate:
					var jsonRequest folderCreateJSONRequest
					if _, err := readJSONInput(cmd, state, input, &jsonRequest); err != nil {
						return err
					}
					request.Name = jsonRequest.Name
				case folder.AdminRename:
					var jsonRequest folderRenameJSONRequest
					if _, err := readJSONInput(cmd, state, input, &jsonRequest); err != nil {
						return err
					}
					request.Folder, request.Name = jsonRequest.Folder, jsonRequest.Name
				case folder.AdminDelete:
					var jsonRequest folderDeleteJSONRequest
					if _, err := readJSONInput(cmd, state, input, &jsonRequest, "allow-non-empty"); err != nil {
						return err
					}
					request.Folder, request.AllowNonEmpty = jsonRequest.Folder, jsonRequest.AllowNonEmpty
				default:
					var jsonRequest folderSelectorJSONRequest
					if _, err := readJSONInput(cmd, state, input, &jsonRequest); err != nil {
						return err
					}
					request.Folder = jsonRequest.Folder
				}
			}
			if dryRun {
				_, plan, schemaVersion, err := state.folders.FolderAdminPreview(cmd.Context(), state.global.Home, state.global.Account, request)
				if err != nil {
					return err
				}
				return renderFolderAdminPreview(state, plan, schemaVersion, jsonOutput)
			}
			interactive := isTerminalReader(state.stdin) && !state.global.NoInput
			if !yes && !interactive {
				return NewAppError(ErrorSafety, "non-interactive folder administration requires --yes", false)
			}
			if !yes {
				_, plan, _, err := state.folders.FolderAdminPreview(cmd.Context(), state.global.Home, state.global.Account, request)
				if err != nil {
					return err
				}
				if err := renderCompactFolderAdminPreview(state.stdout, plan); err != nil {
					return err
				}
				confirmed, err := confirmFolderAdmin(state.stdin, state.stderr, action)
				if err != nil {
					return err
				}
				if !confirmed {
					return NewAppError(ErrorSafety, "folder change canceled before contacting IMAP", false)
				}
			}
			accountValue, outcome, schemaVersion, err := state.folders.FolderAdmin(cmd.Context(), state.global.Home, state.global.Account, request)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: folderAdminData{AccountID: accountValue.ID, Outcome: outcome, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderFolderAdminOutcome(NewCompactTextRenderer(state.stdout), outcome)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the exact folder change without contacting IMAP")
	command.Flags().BoolVar(&yes, "yes", false, "Confirm an intentional non-interactive folder change")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	if action == folder.AdminDelete {
		command.Flags().BoolVar(&allowNonEmpty, "allow-non-empty", false, "Allow provider deletion only after iCloud rechecks that the folder contains messages")
	}
	bindJSONInput(command, &input)
	return command
}

func folderAdminCommandMetadata(action folder.AdminAction) (string, string, string) {
	switch action {
	case folder.AdminCreate:
		return "create <name>", "Create one custom iCloud Mail folder", "  icloud mail folders create 'Projects/Client' --dry-run\n  icloud mail folders create 'Projects/Client' --yes"
	case folder.AdminRename:
		return "rename <folder> <new-name>", "Rename one custom iCloud Mail folder without changing its stable local ID", "  icloud mail folders rename Receipts 'Projects/Receipts' --dry-run\n  icloud mail folders rename Receipts 'Projects/Receipts' --yes"
	case folder.AdminDelete:
		return "delete <folder>", "Delete one custom iCloud Mail folder under explicit safety policy", "  icloud mail folders delete fld_… --dry-run\n  icloud mail folders delete fld_… --yes"
	case folder.AdminSubscribe:
		return "subscribe <folder>", "Subscribe one current iCloud Mail folder", "  icloud mail folders subscribe Receipts --dry-run\n  icloud mail folders subscribe Receipts --yes"
	case folder.AdminUnsubscribe:
		return "unsubscribe <folder>", "Unsubscribe one current iCloud Mail folder", "  icloud mail folders unsubscribe Receipts --dry-run\n  icloud mail folders unsubscribe Receipts --yes"
	default:
		return "admin", "Change one iCloud Mail folder", ""
	}
}

func folderAdminArgs(action folder.AdminAction) cobra.PositionalArgs {
	if action == folder.AdminRename {
		return cobra.ExactArgs(2)
	}
	return cobra.ExactArgs(1)
}

func renderFolderAdminPreview(state *executionState, plan folder.AdminPlan, schemaVersion int64, jsonOutput bool) error {
	if jsonOutput {
		return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: folderAdminPreviewData{
			Mode: "dry-run", Plan: plan, Safety: "no IMAP folder mutation was attempted", SchemaVersion: schemaVersion,
		}, Meta: EnvelopeMeta{RequestID: state.requestID}})
	}
	return renderCompactFolderAdminPreview(state.stdout, plan)
}

func renderCompactFolderAdminPreview(writer io.Writer, plan folder.AdminPlan) error {
	renderer := NewCompactTextRenderer(writer)
	recordID := plan.FolderID
	if recordID == "" {
		recordID = "new"
	}
	if err := renderer.Record("folder-admin-preview", recordID,
		Field("action", plan.Action),
		Field("current_name", plan.CurrentName),
		Field("requested_name", plan.RequestedName),
		Field("kind", plan.Kind),
		Field("subscription", plan.Subscription),
		Field("messages", plan.Messages),
		Field("sync", enabledDisabled(plan.SyncEnabled)),
		Field("allow_non_empty", plan.AllowNonEmpty),
		Field("provider_rechecks", plan.ProviderRechecks),
	); err != nil {
		return err
	}
	return renderer.Summary("folder-admin-safety",
		Field("dry_run", "true"),
		Field("imap_contacted", "false"),
		Field("destructive", plan.Destructive),
		Field("next", "review the exact folder and rerun without --dry-run; use --yes when non-interactive"),
	)
}

func renderFolderAdminOutcome(renderer *CompactTextRenderer, outcome folder.AdminOutcome) error {
	return renderer.Record("folder-admin", outcome.Folder.ID,
		Field("action", outcome.Action),
		Field("name", outcome.Folder.Name),
		Field("previous_name", outcome.PreviousName),
		Field("present", yesNo(outcome.Folder.Present)),
		Field("subscription", outcome.Folder.Subscription),
		Field("sync", enabledDisabled(outcome.Folder.SyncEnabled)),
		Field("deleted", outcome.Deleted),
		Field("next", "run mail folders refresh to refresh provider metadata"),
	)
}

func confirmFolderAdmin(reader io.Reader, stderr io.Writer, action folder.AdminAction) (bool, error) {
	if _, err := fmt.Fprintf(stderr, "Apply this %s folder change? [y/N]: ", action); err != nil {
		return false, WrapAppError(ErrorInternal, "cannot write the folder administration confirmation prompt", false, err)
	}
	line, err := bufio.NewReader(io.LimitReader(reader, 64)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, WrapAppError(ErrorConfiguration, "cannot read folder administration confirmation", false, err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
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
	if retentionRisk := folderRetentionRisk(value.Kind); retentionRisk != "" {
		fields = append(fields, Field("retention_risk", retentionRisk))
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

func folderRetentionRisk(kind folder.Kind) string {
	switch kind {
	case folder.KindTrash, folder.KindJunk:
		return "provider_auto_delete_possible"
	case folder.KindDrafts:
		return "provider_mutable_draft"
	default:
		return ""
	}
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

func (unavailableFolderService) FolderRoleList(context.Context, string, string) (account.Account, []folder.FolderRoleBinding, int64, error) {
	return account.Account{}, nil, 0, NewAppError(ErrorConfiguration, "folder role bindings are unavailable", false)
}

func (unavailableFolderService) FolderRoleSetPreview(context.Context, string, string, string, string) (account.Account, folder.FolderRolePlan, int64, error) {
	return account.Account{}, folder.FolderRolePlan{}, 0, NewAppError(ErrorConfiguration, "folder role bindings are unavailable", false)
}

func (unavailableFolderService) FolderRoleSet(context.Context, string, string, string, string) (account.Account, folder.FolderRoleBinding, folder.Folder, int64, error) {
	return account.Account{}, folder.FolderRoleBinding{}, folder.Folder{}, 0, NewAppError(ErrorConfiguration, "folder role bindings are unavailable", false)
}

func (unavailableFolderService) FolderRoleClearPreview(context.Context, string, string, string) (account.Account, folder.FolderRolePlan, int64, error) {
	return account.Account{}, folder.FolderRolePlan{}, 0, NewAppError(ErrorConfiguration, "folder role bindings are unavailable", false)
}

func (unavailableFolderService) FolderRoleClear(context.Context, string, string, string) (account.Account, folder.FolderRoleBinding, int64, error) {
	return account.Account{}, folder.FolderRoleBinding{}, 0, NewAppError(ErrorConfiguration, "folder role bindings are unavailable", false)
}

func (unavailableFolderService) FolderAdminPreview(context.Context, string, string, folder.AdminRequest) (account.Account, folder.AdminPlan, int64, error) {
	return account.Account{}, folder.AdminPlan{}, 0, NewAppError(ErrorConfiguration, "folder administration is unavailable", false)
}

func (unavailableFolderService) FolderAdmin(context.Context, string, string, folder.AdminRequest) (account.Account, folder.AdminOutcome, int64, error) {
	return account.Account{}, folder.AdminOutcome{}, 0, NewAppError(ErrorConfiguration, "folder administration is unavailable", false)
}
