package cli

import (
	"fmt"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/spf13/cobra"
)

type mailIdentityListData struct {
	AccountID     string                 `json:"account_id"`
	Identities    []account.MailIdentity `json:"identities"`
	Count         int                    `json:"count"`
	More          bool                   `json:"more,omitempty"`
	NextCursor    string                 `json:"next_cursor,omitempty"`
	SchemaVersion int64                  `json:"schema_version"`
}

type mailIdentityShowData struct {
	AccountID     string               `json:"account_id"`
	Identity      account.MailIdentity `json:"identity"`
	SchemaVersion int64                `json:"schema_version"`
}

type mailIdentityMutationData struct {
	Outcome       string               `json:"outcome"`
	AccountID     string               `json:"account_id"`
	Identity      account.MailIdentity `json:"identity"`
	SchemaVersion int64                `json:"schema_version"`
}

type mailIdentityReclassifyData struct {
	Summary       account.DirectionReclassificationSummary `json:"summary"`
	SchemaVersion int64                                    `json:"schema_version"`
}

func newMailIdentitiesCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "identities",
		Short:   "Manage local Send From identities for the selected mail account",
		Args:    cobra.NoArgs,
		Example: "  icloud mail identities list\n  icloud mail identities add --address alias@icloud.com --kind icloud_alias\n  icloud mail identities default mid_…",
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(newMailIdentityListCommand(state))
	command.AddCommand(newMailIdentityShowCommand(state))
	command.AddCommand(newMailIdentityAddCommand(state))
	command.AddCommand(newMailIdentityUpdateCommand(state))
	command.AddCommand(newMailIdentityEnabledCommand(state, true))
	command.AddCommand(newMailIdentityEnabledCommand(state, false))
	command.AddCommand(newMailIdentityDefaultCommand(state))
	command.AddCommand(newMailIdentityRemoveCommand(state))
	command.AddCommand(newMailIdentityReclassifyCommand(state))
	return command
}

func newMailIdentityReclassifyCommand(state *executionState) *cobra.Command {
	var limit int
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "reclassify",
		Short:   "Advance bounded local historical direction repair for newly recognized self addresses",
		Args:    cobra.NoArgs,
		Example: "  icloud mail identities reclassify\n  icloud mail identities reclassify --limit 100 --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			request := mailIdentityReclassifyJSONRequest{Limit: DefaultPageSize}
			if used, err := readJSONInput(cmd, state, input, &request, "limit"); err != nil {
				return err
			} else if used {
				limit = request.Limit
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			_, summary, schemaVersion, err := state.accounts.IdentityReclassify(cmd.Context(), state.global.Home, state.global.Account, limit)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailIdentityReclassifyData{Summary: summary, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("identity-reclassification", summary.AccountID,
				Field("selected", summary.Selected),
				Field("promoted", summary.Promoted),
				Field("more", summary.More),
				Field("pending", summary.Pending),
			)
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum historical messages to promote in this pass (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailIdentityListCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var limit int
	var cursor string
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "list",
		Short:   "List bounded local sender identities without contacting iCloud",
		Args:    cobra.NoArgs,
		Example: "  icloud mail identities list\n  icloud --account Personal mail identities list --json\n  icloud mail identities list --input-json request.json --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			request := mailIdentityListReadJSONRequest{Limit: DefaultPageSize}
			if used, err := readJSONInput(cmd, state, input, &request, "limit", "cursor"); err != nil {
				return err
			} else if used {
				limit, cursor = request.Limit, request.Cursor
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			accountValue, values, more, nextCursor, schemaVersion, err := state.accounts.IdentityListPage(cmd.Context(), state.global.Home, state.global.Account, limit, cursor)
			if err != nil {
				return err
			}
			data := mailIdentityListData{AccountID: accountValue.ID, Identities: values, Count: len(values), More: more, NextCursor: nextCursor, SchemaVersion: schemaVersion}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, value := range values {
				if err := renderMailIdentity(renderer, value); err != nil {
					return err
				}
			}
			return renderer.Summary("identities", Field("account", accountValue.ID), Field("count", len(values)), Field("more", more), Field("next_cursor", nextCursor))
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum identities to return (1-%d)", MaximumPageSize))
	command.Flags().StringVar(&cursor, "cursor", "", "Continue from a next_cursor returned for the same account")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindReadJSONInput(command, &input)
	return command
}

func newMailIdentityShowCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "show <identity-id|address>",
		Short:   "Show one local sender identity for the selected account",
		Args:    exactReadArgsOrJSONInput(&input, 1),
		Example: "  icloud mail identities show mid_…\n  icloud mail identities show alias@icloud.com --json\n  icloud mail identities show --input-json request.json --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := ""
			if len(args) == 1 {
				selector = args[0]
			}
			request := mailIdentityShowReadJSONRequest{Identity: selector}
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				selector = request.Identity
			}
			if strings.TrimSpace(selector) == "" {
				return NewAppError(ErrorUsage, "mail identity is required", false)
			}
			accountValue, identity, schemaVersion, err := state.accounts.IdentityShow(cmd.Context(), state.global.Home, state.global.Account, selector)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailIdentityShowData{AccountID: accountValue.ID, Identity: identity, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderMailIdentity(NewCompactTextRenderer(state.stdout), identity)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindReadJSONInput(command, &input)
	return command
}

func newMailIdentityAddCommand(state *executionState) *cobra.Command {
	var address, displayName, label, kind string
	var makeDefault, jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "add",
		Short:   "Add one local sender identity without claiming provider acceptance",
		Args:    cobra.NoArgs,
		Example: "  icloud mail identities add --address alias@icloud.com --kind icloud_alias\n  icloud mail identities add --address me@example.com --display-name 'Work Me' --kind custom_domain --default",
		RunE: func(cmd *cobra.Command, _ []string) error {
			request := mailIdentityAddJSONRequest{Address: address, DisplayName: displayName, Label: label, Kind: kind, Default: makeDefault}
			if used, err := readJSONInput(cmd, state, input, &request, "address", "display-name", "label", "kind", "default"); err != nil {
				return err
			} else if used {
				address, displayName, label, kind, makeDefault = request.Address, request.DisplayName, request.Label, request.Kind, request.Default
			}
			if strings.TrimSpace(address) == "" {
				return NewAppError(ErrorUsage, "mail identity add requires --address", false)
			}
			accountValue, identity, schemaVersion, err := state.accounts.IdentityAdd(cmd.Context(), state.global.Home, state.global.Account, account.MailIdentityAddInput{
				Address: address, DisplayName: displayName, Label: label, Kind: account.MailIdentityKind(kind), MakeDefault: makeDefault,
			})
			if err != nil {
				return err
			}
			return renderMailIdentityMutation(state, jsonOutput, "added", accountValue.ID, identity, schemaVersion)
		},
	}
	command.Flags().StringVar(&address, "address", "", "RFC email address for this identity")
	command.Flags().StringVar(&displayName, "display-name", "", "Local display name (defaults to the account name)")
	command.Flags().StringVar(&label, "label", "", "Optional local label; for Hide My Email this never exposes or manages Apple's forwarding destination")
	command.Flags().StringVar(&kind, "kind", string(account.MailIdentityOther), "Identity kind: icloud_alias, custom_domain, hide_my_email, or other")
	command.Flags().BoolVar(&makeDefault, "default", false, "Make this enabled identity the local default sender")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailIdentityUpdateCommand(state *executionState) *cobra.Command {
	var address, displayName, label, kind string
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "update <identity-id|address>",
		Short:   "Update local address, display-name, label, or kind metadata",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail identities update mid_… --display-name 'Work Me'\n  icloud mail identities update alias@icloud.com --kind custom_domain",
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := ""
			if len(args) == 1 {
				selector = args[0]
			}
			var request mailIdentityUpdateJSONRequest
			if used, err := readJSONInput(cmd, state, input, &request, "address", "display-name", "label", "kind"); err != nil {
				return err
			} else if used {
				selector = request.Identity
				addressPtr, displayPtr, kindPtr := request.Address, request.DisplayName, request.Kind
				update := account.MailIdentityUpdateInput{Address: addressPtr, DisplayName: displayPtr, Label: request.Label}
				if kindPtr != nil {
					value := account.MailIdentityKind(*kindPtr)
					update.Kind = &value
				}
				return runMailIdentityUpdate(cmd, state, jsonOutput, selector, update)
			}
			update := account.MailIdentityUpdateInput{}
			if cmd.Flags().Changed("address") {
				update.Address = &address
			}
			if cmd.Flags().Changed("display-name") {
				update.DisplayName = &displayName
			}
			if cmd.Flags().Changed("label") {
				update.Label = &label
			}
			if cmd.Flags().Changed("kind") {
				value := account.MailIdentityKind(kind)
				update.Kind = &value
			}
			return runMailIdentityUpdate(cmd, state, jsonOutput, selector, update)
		},
	}
	command.Flags().StringVar(&address, "address", "", "Replace the RFC email address")
	command.Flags().StringVar(&displayName, "display-name", "", "Replace the local display name")
	command.Flags().StringVar(&label, "label", "", "Replace or clear the optional local label")
	command.Flags().StringVar(&kind, "kind", "", "Replace the identity kind")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func runMailIdentityUpdate(cmd *cobra.Command, state *executionState, jsonOutput bool, selector string, update account.MailIdentityUpdateInput) error {
	if strings.TrimSpace(selector) == "" {
		return NewAppError(ErrorUsage, "mail identity is required", false)
	}
	if update.Address == nil && update.DisplayName == nil && update.Label == nil && update.Kind == nil {
		return NewAppError(ErrorUsage, "mail identity update requires at least one of --address, --display-name, --label, or --kind", false)
	}
	accountValue, identity, schemaVersion, err := state.accounts.IdentityUpdate(cmd.Context(), state.global.Home, state.global.Account, selector, update)
	if err != nil {
		return err
	}
	return renderMailIdentityMutation(state, jsonOutput, "updated", accountValue.ID, identity, schemaVersion)
}

func newMailIdentityEnabledCommand(state *executionState, enabled bool) *cobra.Command {
	name, outcome, short := "disable", "disabled", "Disable one non-primary local sender identity"
	if enabled {
		name, outcome, short = "enable", "enabled", "Enable one local sender identity"
	}
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     name + " <identity-id|address>",
		Short:   short,
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail identities " + name + " mid_…",
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := ""
			if len(args) == 1 {
				selector = args[0]
			}
			var request mailIdentitySelectorJSONRequest
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				selector = request.Identity
			}
			if strings.TrimSpace(selector) == "" {
				return NewAppError(ErrorUsage, "mail identity is required", false)
			}
			accountValue, identity, schemaVersion, err := state.accounts.IdentitySetEnabled(cmd.Context(), state.global.Home, state.global.Account, selector, enabled)
			if err != nil {
				return err
			}
			return renderMailIdentityMutation(state, jsonOutput, outcome, accountValue.ID, identity, schemaVersion)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailIdentityDefaultCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "default <identity-id|address>",
		Short:   "Choose the local default enabled sender identity",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail identities default mid_…",
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := ""
			if len(args) == 1 {
				selector = args[0]
			}
			var request mailIdentitySelectorJSONRequest
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				selector = request.Identity
			}
			if strings.TrimSpace(selector) == "" {
				return NewAppError(ErrorUsage, "mail identity is required", false)
			}
			accountValue, identity, schemaVersion, err := state.accounts.IdentitySetDefault(cmd.Context(), state.global.Home, state.global.Account, selector)
			if err != nil {
				return err
			}
			return renderMailIdentityMutation(state, jsonOutput, "defaulted", accountValue.ID, identity, schemaVersion)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailIdentityRemoveCommand(state *executionState) *cobra.Command {
	var yes, jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "remove <identity-id|address>",
		Short:   "Remove one non-primary local sender identity",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail identities remove mid_… --yes",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return NewAppError(ErrorSafety, "mail identity removal requires --yes", false)
			}
			selector := ""
			if len(args) == 1 {
				selector = args[0]
			}
			var request mailIdentitySelectorJSONRequest
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				selector = request.Identity
			}
			if strings.TrimSpace(selector) == "" {
				return NewAppError(ErrorUsage, "mail identity is required", false)
			}
			accountValue, identity, schemaVersion, err := state.accounts.IdentityRemove(cmd.Context(), state.global.Home, state.global.Account, selector)
			if err != nil {
				return err
			}
			return renderMailIdentityMutation(state, jsonOutput, "removed", accountValue.ID, identity, schemaVersion)
		},
	}
	command.Flags().BoolVar(&yes, "yes", false, "Authorize local identity removal without prompting")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func renderMailIdentityMutation(state *executionState, jsonOutput bool, outcome, accountID string, identity account.MailIdentity, schemaVersion int64) error {
	data := mailIdentityMutationData{Outcome: outcome, AccountID: accountID, Identity: identity, SchemaVersion: schemaVersion}
	if jsonOutput {
		return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
	}
	renderer := NewCompactTextRenderer(state.stdout)
	if err := renderMailIdentity(renderer, identity); err != nil {
		return err
	}
	return renderer.Summary("identity-"+outcome, Field("account", accountID), Field("identity", identity.ID))
}

func renderMailIdentity(renderer *CompactTextRenderer, value account.MailIdentity) error {
	return renderer.Record("identity", value.ID,
		Field("address", value.Address),
		Field("display_name", value.DisplayName),
		Field("label", value.Label),
		Field("kind", value.Kind),
		Field("provenance", value.Provenance),
		Field("verification", value.Verification),
		Field("enabled", value.Enabled),
		Field("default", value.Default),
	)
}
