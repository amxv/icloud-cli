package cli

import (
	"fmt"
	"strings"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type mailOutboundStatusData struct {
	AccountID     string                  `json:"account_id"`
	Attempts      []publicOutboundAttempt `json:"attempts"`
	Count         int                     `json:"count"`
	More          bool                    `json:"more,omitempty"`
	NextCursor    string                  `json:"next_cursor,omitempty"`
	SchemaVersion int64                   `json:"schema_version"`
}

type mailLocationMutationStatusData struct {
	AccountID     string                        `json:"account_id"`
	Mutations     []maildomain.LocationMutation `json:"mutations"`
	Count         int                           `json:"count"`
	More          bool                          `json:"more,omitempty"`
	NextCursor    string                        `json:"next_cursor,omitempty"`
	SchemaVersion int64                         `json:"schema_version"`
}

func newMailStatusCommand(state *executionState) *cobra.Command {
	var outbound bool
	var mutations bool
	var jsonOutput bool
	var limit int
	var cursor string
	command := &cobra.Command{
		Use:     "status",
		Short:   "Show bounded local mail operation status",
		Args:    cobra.NoArgs,
		Example: "  icloud mail status --outbound\n  icloud mail status --mutations\n  icloud mail status --mutations --limit 50 --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if outbound == mutations {
				return NewAppError(ErrorUsage, "mail status requires exactly one of --outbound or --mutations", false)
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			if mutations {
				accountValue, values, more, nextCursor, schemaVersion, err := state.mail.MailLocationMutationStatusPage(cmd.Context(), state.global.Home, state.global.Account, cursor, limit)
				if err != nil {
					return err
				}
				if jsonOutput {
					return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailLocationMutationStatusData{
						AccountID: accountValue.ID, Mutations: values, Count: len(values), More: more, NextCursor: nextCursor, SchemaVersion: schemaVersion,
					}, Meta: EnvelopeMeta{RequestID: state.requestID}})
				}
				renderer := NewCompactTextRenderer(state.stdout)
				for _, mutation := range values {
					if err := renderer.Record("mutation", mutation.ID,
						Field("action", mutation.Action),
						Field("message_id", mutation.MessageID),
						Field("source_location_id", mutation.SourceLocationID),
						Field("source_folder_id", mutation.SourceFolderID),
						Field("destination_location_id", mutation.DestinationLocationID),
						Field("destination_folder_id", mutation.DestinationFolderID),
						Field("status", mutation.Status),
						Field("provider_method", mutation.ProviderMethod),
						Field("source_present", fmt.Sprintf("%t", mutation.SourcePresent)),
						Field("mapping_known", fmt.Sprintf("%t", mutation.MappingKnown)),
						Field("reconciliation_required", fmt.Sprintf("%t", mutation.ReconciliationRequired)),
						Field("error_code", mutation.ErrorCode),
						Field("updated_at", mutation.UpdatedAt),
						Field("next", locationMutationNext(mutation)),
					); err != nil {
						return err
					}
				}
				return renderer.Summary("mutation-status", Field("account", accountValue.ID), Field("count", len(values)), Field("more", more), Field("next_cursor", nextCursor))
			}
			accountValue, attempts, more, nextCursor, schemaVersion, err := state.mail.MailOutboundStatusPage(cmd.Context(), state.global.Home, state.global.Account, cursor, limit)
			if err != nil {
				return err
			}
			if jsonOutput {
				public := make([]publicOutboundAttempt, 0, len(attempts))
				for _, attempt := range attempts {
					public = append(public, sanitizeOutboundAttempt(attempt))
				}
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailOutboundStatusData{
					AccountID: accountValue.ID, Attempts: public, Count: len(public), More: more, NextCursor: nextCursor, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, attempt := range attempts {
				if err := renderer.Record("outbound", attempt.ID,
					Field("kind", attempt.Kind),
					Field("parent_message_id", attempt.ParentMessageID),
					Field("source_message_id", attempt.SourceMessageID),
					Field("status", attempt.Status),
					Field("message_id", attempt.MessageID),
					Field("thread_id", attempt.ThreadID),
					Field("attachments", attempt.AttachmentCount),
					Field("attachment_bytes", attempt.AttachmentBytes),
					Field("reconciliation", attempt.Reconciliation),
					Field("reconciliation_error", attempt.ReconciliationErrorCode),
					Field("retry_of", attempt.RetryOfAttemptID),
					Field("error_code", attempt.ErrorCode),
					Field("updated_at", attempt.UpdatedAt),
					Field("next", outboundAttemptNext(attempt)),
				); err != nil {
					return err
				}
			}
			return renderer.Summary("outbound-status",
				Field("account", accountValue.ID),
				Field("count", len(attempts)),
				Field("more", more),
				Field("next_cursor", nextCursor),
			)
		},
	}
	command.Flags().BoolVar(&outbound, "outbound", false, "List recent outbound send attempts")
	command.Flags().BoolVar(&mutations, "mutations", false, "List recent message location mutation intents and outcomes")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum attempts to return (1-%d)", MaximumPageSize))
	command.Flags().StringVar(&cursor, "cursor", "", "Continue from a next_cursor returned by the same status mode")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func outboundAttemptNext(attempt maildomain.OutboundAttempt) string {
	switch attempt.Status {
	case maildomain.OutboundFailed:
		return "fix the reported failure, then retry with --retry-of and a new idempotency key"
	case maildomain.OutboundUnknown:
		if attempt.ReconciliationErrorCode != "" {
			return "inspect Sent and the recipient; retry only with --retry-unknown after resolving duplicate risk"
		}
		return "run mail sync --reconcile-outbound; do not retry automatically"
	case maildomain.OutboundSent:
		if attempt.Reconciliation == maildomain.ReconciliationConfirmed {
			return "none"
		}
		if attempt.ReconciliationErrorCode != "" {
			return "message accepted; check Sent and run mail sync --reconcile-outbound; do not resend"
		}
		if strings.TrimSpace(attempt.ErrorCode) == "local_completion" {
			return "run mail sync --reconcile-outbound to restore local linkage"
		}
		return "run mail sync --reconcile-outbound"
	default:
		return "inspect the attempt before retrying"
	}
}
