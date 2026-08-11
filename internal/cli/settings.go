package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type mailSettingsService interface {
	MailSettingsShow(context.Context, string, string, bool) (account.Account, maildomain.MailSettings, int64, error)
}

type mailSettingsData struct {
	Settings      maildomain.MailSettings `json:"settings"`
	SchemaVersion int64                   `json:"schema_version"`
}

func newMailSettingsCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:   "settings",
		Short: "Inspect local Mail settings and truthful provider compatibility",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	command.AddCommand(newMailSettingsShowCommand(state))
	return command
}

func newMailSettingsShowCommand(state *executionState) *cobra.Command {
	var verbose bool
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "show",
		Short:   "Show local defaults, bindings, preferences, and provider compatibility without network access",
		Args:    exactArgsOrJSONInput(&input, 0),
		Example: "  icloud mail settings show\n  icloud --account Personal mail settings show --verbose\n  icloud mail settings show --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, ok := state.mail.(mailSettingsService)
			if !ok {
				return NewAppError(ErrorConfiguration, "mail settings are unavailable", false)
			}
			request := mailSettingsShowReadJSONRequest{Verbose: verbose}
			if used, err := readJSONInput(cmd, state, input, &request, "verbose"); err != nil {
				return err
			} else if used {
				verbose = request.Verbose
			}
			_, settings, schemaVersion, err := service.MailSettingsShow(cmd.Context(), state.global.Home, state.global.Account, verbose)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSettingsData{Settings: settings, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderMailSettings(NewCompactTextRenderer(state.stdout), settings, verbose)
		},
	}
	command.Flags().BoolVar(&verbose, "verbose", false, "Include short next actions for unsupported or unverified provider features")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindReadJSONInput(command, &input)
	return command
}

func renderMailSettings(renderer *CompactTextRenderer, value maildomain.MailSettings, verbose bool) error {
	signatures := make([]string, 0, len(value.Local.SignatureDefaults))
	for _, setting := range value.Local.SignatureDefaults {
		selected := setting.SignatureID
		if setting.None || strings.TrimSpace(selected) == "" {
			selected = "none"
		}
		signatures = append(signatures, fmt.Sprintf("%s:%s", setting.Operation, selected))
	}
	roles := make([]string, 0, len(value.Local.FolderRoles))
	for _, role := range value.Local.FolderRoles {
		if role.Resolved {
			roles = append(roles, fmt.Sprintf("%s:%s(%s)", role.Role, role.FolderID, role.Source))
		} else {
			roles = append(roles, role.Role+":unresolved")
		}
	}
	if err := renderer.Record("mail-settings", value.AccountID,
		Field("scope", "local"),
		Field("provider_contact", "none"),
		Field("default_from", value.Local.DefaultIdentity.Address),
		Field("default_identity", value.Local.DefaultIdentity.ID),
		Field("default_kind", value.Local.DefaultIdentity.Kind),
		Field("identities", value.Local.IdentityCount),
		Field("enabled_identities", value.Local.EnabledIdentities),
		Field("signatures", value.Local.SignatureCount),
		Field("signature_defaults", signatures),
		Field("folder_roles", roles),
		Field("sender_preferences", value.Local.Preferences.SenderRecords),
		Field("vip_senders", value.Local.Preferences.VIPSenders),
		Field("blocked_senders", value.Local.Preferences.BlockedSenders),
		Field("thread_preferences", value.Local.Preferences.ThreadRecords),
		Field("muted_threads", value.Local.Preferences.MutedThreads),
		Field("alternate_from", value.Compatibility.AlternateFrom.Status),
		Field("reply_answered", value.Compatibility.ReplyAnswered.Status),
		Field("forwarded_state", value.Compatibility.ForwardedState.Status),
		Field("colored_flags", value.Compatibility.ColoredFlags.Status),
		Field("hide_my_email_outbound", value.Compatibility.HideMyEmailOutbound.Status),
		Field("smime_inspection", value.Compatibility.SMIMEInspection.Status),
		Field("smime", value.Compatibility.SMIME.Status),
		Field("provider_settings", maildomain.SettingsUnsupportedCurrentAuth),
	); err != nil {
		return err
	}
	if !verbose {
		return nil
	}
	for _, item := range []struct {
		name string
		cap  maildomain.SettingsCapability
	}{
		{"automatic_forwarding", value.ProviderSettings.AutomaticForwarding},
		{"automatic_reply", value.ProviderSettings.AutomaticReply},
		{"server_rules", value.ProviderSettings.ServerRules},
		{"alias_provisioning", value.ProviderSettings.AliasProvisioning},
		{"custom_domain_provisioning", value.ProviderSettings.CustomDomainProvisioning},
		{"hide_my_email_management", value.ProviderSettings.HideMyEmailManagement},
		{"category_views", value.ProviderSettings.CategoryViews},
		{"provider_default_send_address", value.ProviderSettings.ProviderDefaultSendAddress},
	} {
		if err := renderer.Record("provider-setting", item.name, Field("status", item.cap.Status), Field("next_action", item.cap.NextAction)); err != nil {
			return err
		}
	}
	return nil
}
