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
	SchemaVersion int64                   `json:"schema_version"`
}

func newMailStatusCommand(state *executionState) *cobra.Command {
	var outbound bool
	var jsonOutput bool
	var limit int
	command := &cobra.Command{
		Use:     "status",
		Short:   "Show bounded local mail operation status",
		Args:    cobra.NoArgs,
		Example: "  icloud mail status --outbound\n  icloud mail status --outbound --limit 50 --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !outbound {
				return NewAppError(ErrorUsage, "mail status currently requires --outbound", false)
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			accountValue, attempts, more, schemaVersion, err := state.mail.MailOutboundStatus(cmd.Context(), state.global.Home, state.global.Account, limit)
			if err != nil {
				return err
			}
			if jsonOutput {
				public := make([]publicOutboundAttempt, 0, len(attempts))
				for _, attempt := range attempts {
					public = append(public, sanitizeOutboundAttempt(attempt))
				}
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailOutboundStatusData{
					AccountID: accountValue.ID, Attempts: public, Count: len(public), More: more, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, attempt := range attempts {
				if err := renderer.Record("outbound", attempt.ID,
					Field("kind", attempt.Kind),
					Field("parent_message_id", attempt.ParentMessageID),
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
			)
		},
	}
	command.Flags().BoolVar(&outbound, "outbound", false, "List recent outbound send attempts")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum attempts to return (1-%d)", MaximumPageSize))
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
