package cli

import (
	"fmt"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type mailFlagMutationData struct {
	Outcome        string                   `json:"outcome"`
	Action         maildomain.FlagMutation  `json:"action"`
	AccountID      string                   `json:"account_id"`
	MessageID      string                   `json:"message_id"`
	LocationID     string                   `json:"location_id"`
	FolderID       string                   `json:"folder_id"`
	UIDValidity    uint32                   `json:"uid_validity"`
	UID            uint32                   `json:"uid"`
	Flags          maildomain.LocationFlags `json:"flags"`
	ModSequence    uint64                   `json:"mod_sequence,omitempty"`
	FlagsCheckedAt *time.Time               `json:"flags_checked_at,omitempty"`
	Unread         bool                     `json:"unread"`
	Flagged        bool                     `json:"flagged"`
	SchemaVersion  int64                    `json:"schema_version"`
}

func newMailFlagMutationCommand(state *executionState, name, short string, mutation maildomain.FlagMutation) *cobra.Command {
	var folderSelector string
	var jsonOutput bool
	command := &cobra.Command{
		Use:   name + " <message-id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			accountValue, folderValue, message, location, schemaVersion, err := state.mail.MailUpdateFlag(
				cmd.Context(), state.global.Home, state.global.Account, args[0], folderSelector, mutation,
			)
			if err != nil {
				return err
			}
			data := mailFlagMutationData{
				Outcome: "updated", Action: mutation, AccountID: accountValue.ID,
				MessageID: message.ID, LocationID: location.ID, FolderID: folderValue.ID,
				UIDValidity: location.UIDValidity, UID: location.UID, Flags: location.Flags,
				ModSequence: location.ModSequence, FlagsCheckedAt: location.FlagsCheckedAt,
				Unread: message.Unread, Flagged: message.Flagged,
				SchemaVersion: schemaVersion,
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("mail-state", message.ID,
				Field("action", mutation),
				Field("location", location.ID),
				Field("folder", folderValue.ID),
				Field("unread", fmt.Sprintf("%t", message.Unread)),
				Field("flagged", fmt.Sprintf("%t", message.Flagged)),
			)
		},
	}
	command.Flags().StringVar(&folderSelector, "folder", "", "Select one exact folder ID, name, wire name, or kind")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}
