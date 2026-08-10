package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type locationMutationFlags struct {
	From           string
	To             string
	IdempotencyKey string
	DryRun         bool
	Yes            bool
	JSON           bool
}

type mailLocationPreviewData struct {
	Mode          string                          `json:"mode"`
	Plan          maildomain.LocationMutationPlan `json:"plan"`
	Safety        string                          `json:"safety"`
	SchemaVersion int64                           `json:"schema_version"`
}

type mailLocationMutationData struct {
	AccountID     string                      `json:"account_id"`
	MessageID     string                      `json:"message_id"`
	Mutation      maildomain.LocationMutation `json:"mutation"`
	Reused        bool                        `json:"reused"`
	Unread        bool                        `json:"unread"`
	Flagged       bool                        `json:"flagged"`
	SchemaVersion int64                       `json:"schema_version"`
}

func newMailLocationMutationCommand(state *executionState, action maildomain.LocationMutationAction) *cobra.Command {
	var flags locationMutationFlags
	var input jsonInputOptions
	name, short, example := locationMutationCommandMetadata(action)
	command := &cobra.Command{
		Use:     name + " <message-id>",
		Short:   short,
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: example,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: flags.JSON}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			if flags.DryRun && flags.Yes {
				return NewAppError(ErrorUsage, "--dry-run and --yes cannot be combined", false)
			}
			messageID := ""
			if len(args) == 1 {
				messageID = strings.TrimSpace(args[0])
			}
			var jsonRequest locationJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest, "from", "to", "idempotency-key"); err != nil {
				return err
			} else if used {
				messageID, flags.From, flags.To, flags.IdempotencyKey = strings.TrimSpace(jsonRequest.MessageID), jsonRequest.From, jsonRequest.To, jsonRequest.IdempotencyKey
			}
			request := maildomain.LocationMutationRequest{
				Action: action, MessageID: messageID,
				FromFolder: strings.TrimSpace(flags.From), ToFolder: strings.TrimSpace(flags.To),
				IdempotencyKey: strings.TrimSpace(flags.IdempotencyKey),
			}
			if flags.DryRun {
				_, plan, schemaVersion, err := state.mail.MailLocationPreview(cmd.Context(), state.global.Home, state.global.Account, request)
				if err != nil {
					return err
				}
				return renderLocationMutationPreview(state, plan, schemaVersion, flags.JSON)
			}
			interactive := isTerminalReader(state.stdin) && !state.global.NoInput
			if !flags.Yes && !interactive {
				return NewAppError(ErrorSafety, "non-interactive mail location mutation requires --yes", false)
			}
			if !flags.Yes {
				_, plan, _, err := state.mail.MailLocationPreview(cmd.Context(), state.global.Home, state.global.Account, request)
				if err != nil {
					return err
				}
				if err := renderCompactLocationMutationPreview(state.stdout, plan); err != nil {
					return err
				}
				confirmed, err := confirmMailLocationMutation(state.stdin, state.stderr, action)
				if err != nil {
					return err
				}
				if !confirmed {
					return NewAppError(ErrorSafety, "mail location mutation canceled before contacting IMAP", false)
				}
			}
			accountValue, _, message, mutation, reused, schemaVersion, err := state.mail.MailMutateLocation(
				cmd.Context(), state.global.Home, state.global.Account, request,
			)
			if err != nil {
				return err
			}
			data := mailLocationMutationData{
				AccountID: accountValue.ID, MessageID: message.ID,
				Mutation: mutation, Reused: reused,
				Unread: message.Unread, Flagged: message.Flagged,
				SchemaVersion: schemaVersion,
			}
			if flags.JSON {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderLocationMutationOutcome(NewCompactTextRenderer(state.stdout), mutation, reused, message.Unread, message.Flagged)
		},
	}
	command.Flags().StringVar(&flags.From, "from", "", "Select one exact current source folder by ID, name, wire name, or kind")
	command.Flags().StringVar(&flags.To, "to", "", "Select one current destination folder by ID, name, wire name, or kind")
	command.Flags().StringVar(&flags.IdempotencyKey, "idempotency-key", "", idempotencyKeyHelp)
	command.Flags().BoolVar(&flags.DryRun, "dry-run", false, "Preview the exact location change without creating intent or contacting IMAP")
	command.Flags().BoolVar(&flags.Yes, "yes", false, "Confirm an intentional non-interactive location change")
	command.Flags().BoolVar(&flags.JSON, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func locationMutationCommandMetadata(action maildomain.LocationMutationAction) (string, string, string) {
	switch action {
	case maildomain.LocationActionMove:
		return "move", "Move one exact current message location", "  icloud mail move msg_… --from inbox --to Projects --dry-run\n  icloud mail move msg_… --from inbox --to Projects --idempotency-key move-01 --yes"
	case maildomain.LocationActionCopy:
		return "copy", "Copy one exact current message location", "  icloud mail copy msg_… --from inbox --to Projects --dry-run\n  icloud mail copy msg_… --from inbox --to Projects --idempotency-key copy-01 --yes"
	case maildomain.LocationActionArchive:
		return "archive", "Move one eligible non-Sent location to the discovered Archive folder", "  icloud mail archive msg_… --dry-run\n  icloud mail archive msg_… --from inbox --idempotency-key archive-01 --yes"
	case maildomain.LocationActionUnarchive:
		return "unarchive", "Move one current Archive location to Inbox", "  icloud mail unarchive msg_… --dry-run\n  icloud mail unarchive msg_… --idempotency-key unarchive-01 --yes"
	case maildomain.LocationActionTrash:
		return "trash", "Move one exact current location to the discovered Trash folder", "  icloud mail trash msg_… --dry-run\n  icloud mail trash msg_… --from inbox --idempotency-key trash-01 --yes"
	case maildomain.LocationActionRestore:
		return "restore", "Move one current Trash location to a deliberate destination", "  icloud mail restore msg_… --to inbox --dry-run\n  icloud mail restore msg_… --to inbox --idempotency-key restore-01 --yes"
	case maildomain.LocationActionJunk:
		return "junk", "Move one exact current message location to the semantic Junk folder", "  icloud mail junk msg_… --dry-run\n  icloud mail junk msg_… --from inbox --idempotency-key junk-01 --yes"
	case maildomain.LocationActionNotJunk:
		return "not-junk", "Move one current Junk location back to Inbox", "  icloud mail not-junk msg_… --dry-run\n  icloud mail not-junk msg_… --idempotency-key not-junk-01 --yes"
	case maildomain.LocationActionDelete:
		return "delete", "Permanently delete one exact current provider location", "  icloud mail delete msg_… --from trash --dry-run\n  icloud mail delete msg_… --from trash --idempotency-key delete-01 --yes"
	default:
		return "mutate", "Change one exact current message location", ""
	}
}

func renderLocationMutationPreview(state *executionState, plan maildomain.LocationMutationPlan, schemaVersion int64, jsonOutput bool) error {
	if jsonOutput {
		return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailLocationPreviewData{
			Mode: "dry-run", Plan: plan,
			Safety:        "no IMAP mutation or durable mutation intent was created",
			SchemaVersion: schemaVersion,
		}, Meta: EnvelopeMeta{RequestID: state.requestID}})
	}
	return renderCompactLocationMutationPreview(state.stdout, plan)
}

func renderCompactLocationMutationPreview(writer io.Writer, plan maildomain.LocationMutationPlan) error {
	renderer := NewCompactTextRenderer(writer)
	if err := renderer.Record("mail-location-preview", plan.MessageID,
		Field("action", plan.Action),
		Field("operation", plan.Operation),
		Field("source_location", plan.SourceLocation.ID),
		Field("source_folder", plan.SourceFolderID),
		Field("source_uid_validity", plan.SourceLocation.UIDValidity),
		Field("source_uid", plan.SourceLocation.UID),
		Field("destination_folder", plan.DestinationFolderID),
		Field("request_hash", plan.RequestHash),
	); err != nil {
		return err
	}
	return renderer.Summary("mail-location-safety",
		Field("dry_run", "true"),
		Field("imap_contacted", "false"),
		Field("intent_created", "false"),
		Field("logical_message_deleted", "false"),
		Field("next", "review the exact source and destination, then rerun without --dry-run and use --yes when non-interactive"),
	)
}

func renderLocationMutationOutcome(renderer *CompactTextRenderer, mutation maildomain.LocationMutation, reused, unread, flagged bool) error {
	return renderer.Record("mail-location", mutation.ID,
		Field("action", mutation.Action),
		Field("message_id", mutation.MessageID),
		Field("source_location_id", mutation.SourceLocationID),
		Field("source_folder_id", mutation.SourceFolderID),
		Field("destination_location_id", mutation.DestinationLocationID),
		Field("destination_folder_id", mutation.DestinationFolderID),
		Field("destination_uid_validity", mutation.DestinationUIDValidity),
		Field("destination_uid", mutation.DestinationUID),
		Field("status", mutation.Status),
		Field("provider_method", mutation.ProviderMethod),
		Field("source_present", fmt.Sprintf("%t", mutation.SourcePresent)),
		Field("mapping_known", fmt.Sprintf("%t", mutation.MappingKnown)),
		Field("reconciliation_required", fmt.Sprintf("%t", mutation.ReconciliationRequired)),
		Field("error_code", mutation.ErrorCode),
		Field("unread", fmt.Sprintf("%t", unread)),
		Field("flagged", fmt.Sprintf("%t", flagged)),
		Field("reused", fmt.Sprintf("%t", reused)),
		Field("next", locationMutationNext(mutation)),
	)
}

func locationMutationNext(mutation maildomain.LocationMutation) string {
	switch mutation.Status {
	case maildomain.LocationMutationConfirmed:
		return "none"
	case maildomain.LocationMutationUnresolved:
		if mutation.DestinationFolderID != "" {
			return "synchronize the source and destination folders; do not repeat the provider mutation automatically"
		}
		return "synchronize the source folder; do not repeat permanent deletion automatically"
	case maildomain.LocationMutationFailed:
		return "fix the reported failure and issue a new deliberate request"
	default:
		return "inspect mutation status before another provider write"
	}
}

func confirmMailLocationMutation(reader io.Reader, stderr io.Writer, action maildomain.LocationMutationAction) (bool, error) {
	if _, err := fmt.Fprintf(stderr, "Apply this %s location change? [y/N]: ", action); err != nil {
		return false, WrapAppError(ErrorInternal, "cannot write the location mutation confirmation prompt", false, err)
	}
	line, err := bufio.NewReader(io.LimitReader(reader, 64)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, WrapAppError(ErrorConfiguration, "cannot read location mutation confirmation", false, err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
