package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type mailSendersService interface {
	MailSendersList(context.Context, string, string, string, int) (account.Account, maildomain.SenderPreferencePage, int64, error)
	MailSenderShow(context.Context, string, string, string) (account.Account, maildomain.SenderPreference, int64, error)
	MailSenderSetVIP(context.Context, string, string, string, bool) (account.Account, maildomain.SenderPreference, int64, error)
	MailSenderSetBlocked(context.Context, string, string, string, bool) (account.Account, maildomain.SenderPreference, int64, error)
}

type senderListJSONRequest struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type senderPreferenceJSONRequest struct {
	Email string `json:"email"`
}

type mailSendersListData struct {
	AccountID     string                        `json:"account_id"`
	Preferences   []maildomain.SenderPreference `json:"preferences"`
	Count         int                           `json:"count"`
	More          bool                          `json:"more"`
	NextCursor    string                        `json:"next_cursor,omitempty"`
	LocalOnly     bool                          `json:"local_only"`
	SchemaVersion int64                         `json:"schema_version"`
}

type mailSenderData struct {
	AccountID     string                      `json:"account_id"`
	Preference    maildomain.SenderPreference `json:"preference"`
	LocalOnly     bool                        `json:"local_only"`
	SchemaVersion int64                       `json:"schema_version"`
}

func newMailSendersCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{Use: "senders", Short: "Manage local VIP and blocked sender preferences", Args: cobra.NoArgs}
	command.AddCommand(newMailSendersListCommand(state))
	command.AddCommand(newMailSenderShowCommand(state))
	command.AddCommand(newMailSenderToggleCommand(state, "vip", true))
	command.AddCommand(newMailSenderToggleCommand(state, "unvip", false))
	command.AddCommand(newMailSenderToggleCommand(state, "block", true))
	command.AddCommand(newMailSenderToggleCommand(state, "unblock", false))
	return command
}

func newMailSendersListCommand(state *executionState) *cobra.Command {
	var cursor string
	var limit int
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "list",
		Short:   "List account-local sender preferences",
		Args:    exactArgsOrJSONInput(&input, 0),
		Example: "  icloud mail senders list\n  icloud mail senders list --limit 50 --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, ok := state.mail.(mailSendersService)
			if !ok {
				return NewAppError(ErrorConfiguration, "sender preferences are unavailable", false)
			}
			request := senderListJSONRequest{Cursor: cursor, Limit: limit}
			if used, err := readJSONInput(cmd, state, input, &request, "cursor", "limit"); err != nil {
				return err
			} else if used {
				cursor, limit = request.Cursor, request.Limit
			}
			if limit == 0 {
				limit = DefaultPageSize
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			accountValue, page, schemaVersion, err := service.MailSendersList(cmd.Context(), state.global.Home, state.global.Account, cursor, limit)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSendersListData{
					AccountID: accountValue.ID, Preferences: page.Preferences, Count: len(page.Preferences), More: page.More,
					NextCursor: page.NextCursor, LocalOnly: true, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, preference := range page.Preferences {
				if err := renderSenderPreference(renderer, preference); err != nil {
					return err
				}
			}
			return renderer.Summary("senders", Field("account", accountValue.ID), Field("scope", "local"), Field("count", len(page.Preferences)), Field("more", page.More), Field("next_cursor", page.NextCursor))
		},
	}
	command.Flags().StringVar(&cursor, "cursor", "", "Continue from a next_cursor returned by the same account")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum sender preferences to return (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailSenderShowCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "show <email>",
		Short:   "Show one exact local sender preference",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail senders show alerts@example.com\n  icloud mail senders show alerts@example.com --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			service, ok := state.mail.(mailSendersService)
			if !ok {
				return NewAppError(ErrorConfiguration, "sender preferences are unavailable", false)
			}
			email := ""
			if len(args) == 1 {
				email = args[0]
			}
			request := senderPreferenceJSONRequest{Email: email}
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				email = request.Email
			}
			accountValue, preference, schemaVersion, err := service.MailSenderShow(cmd.Context(), state.global.Home, state.global.Account, email)
			if err != nil {
				return err
			}
			return renderMailSenderResult(state, accountValue.ID, preference, schemaVersion, jsonOutput)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailSenderToggleCommand(state *executionState, name string, value bool) *cobra.Command {
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     name + " <email>",
		Short:   senderToggleShort(name),
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: senderToggleExample(name),
		RunE: func(cmd *cobra.Command, args []string) error {
			service, ok := state.mail.(mailSendersService)
			if !ok {
				return NewAppError(ErrorConfiguration, "sender preferences are unavailable", false)
			}
			email := ""
			if len(args) == 1 {
				email = args[0]
			}
			request := senderPreferenceJSONRequest{Email: email}
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				email = request.Email
			}
			var accountValue account.Account
			var preference maildomain.SenderPreference
			var schemaVersion int64
			var err error
			switch name {
			case "vip", "unvip":
				accountValue, preference, schemaVersion, err = service.MailSenderSetVIP(cmd.Context(), state.global.Home, state.global.Account, email, value)
			case "block", "unblock":
				accountValue, preference, schemaVersion, err = service.MailSenderSetBlocked(cmd.Context(), state.global.Home, state.global.Account, email, value)
			}
			if err != nil {
				return err
			}
			return renderMailSenderResult(state, accountValue.ID, preference, schemaVersion, jsonOutput)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func senderToggleShort(name string) string {
	switch name {
	case "vip":
		return "Mark one exact sender address as a local VIP"
	case "unvip":
		return "Clear the local VIP flag for one exact sender address"
	case "block":
		return "Mark one exact sender address as locally blocked"
	default:
		return "Clear the local blocked flag for one exact sender address"
	}
}

func senderToggleExample(name string) string {
	return "  icloud mail senders " + name + " alerts@example.com\n" +
		"  icloud mail senders " + name + " alerts@example.com --json"
}

func renderMailSenderResult(state *executionState, accountID string, preference maildomain.SenderPreference, schemaVersion int64, jsonOutput bool) error {
	if jsonOutput {
		return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSenderData{AccountID: accountID, Preference: preference, LocalOnly: true, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
	}
	return renderSenderPreference(NewCompactTextRenderer(state.stdout), preference)
}

func renderSenderPreference(renderer *CompactTextRenderer, preference maildomain.SenderPreference) error {
	return renderer.Record("sender-preference", strings.ToLower(preference.Email),
		Field("email", preference.Email), Field("scope", "local"), Field("vip", preference.VIP), Field("blocked", preference.Blocked), Field("updated_at", preference.UpdatedAt))
}
