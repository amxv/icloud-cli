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

type mailOutboundAttemptData struct {
	AccountID     string                `json:"account_id"`
	Attempt       publicOutboundAttempt `json:"attempt"`
	SchemaVersion int64                 `json:"schema_version"`
}

type mailLocationMutationOperationData struct {
	AccountID     string                      `json:"account_id"`
	Mutation      maildomain.LocationMutation `json:"mutation"`
	SchemaVersion int64                       `json:"schema_version"`
}

type mailOperationKind string

const (
	mailOperationOutbound  mailOperationKind = "outbound"
	mailOperationMutations mailOperationKind = "mutations"
)

func newMailOperationsCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "operations",
		Short:   "Inspect local mail operation history by stable ID",
		Args:    cobra.NoArgs,
		Example: "  icloud mail operations outbound list\n  icloud mail operations mutations show mut_…",
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(newMailOutboundOperationsCommand(state))
	command.AddCommand(newMailMutationOperationsCommand(state))
	return command
}

func newMailOutboundOperationsCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "outbound",
		Short:   "Inspect outbound send attempts",
		Args:    cobra.NoArgs,
		Example: "  icloud mail operations outbound list\n  icloud mail operations outbound show send_…",
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(newMailOperationListCommand(state, mailOperationOutbound))
	command.AddCommand(newMailOutboundOperationShowCommand(state))
	command.AddCommand(newMailReplyAnswerReconcileCommand(state))
	return command
}

func newMailReplyAnswerReconcileCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:     "reconcile-answer <attempt-id>",
		Short:   "Retry only the provider Answered-state write for one proven sent reply",
		Args:    cobra.ExactArgs(1),
		Example: "  icloud mail operations outbound reconcile-answer send_…\n  icloud mail operations outbound reconcile-answer send_… --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			accountValue, attempt, schemaVersion, err := state.mail.MailReconcileReplyAnswer(cmd.Context(), state.global.Home, state.global.Account, args[0])
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailOutboundAttemptData{
					AccountID: accountValue.ID, Attempt: sanitizeOutboundAttempt(attempt), SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderReplyAnswerReconciliation(NewCompactTextRenderer(state.stdout), attempt)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func renderReplyAnswerReconciliation(renderer *CompactTextRenderer, attempt maildomain.OutboundAttempt) error {
	return renderer.Record("reply-answer", attempt.ID,
		Field("status", attempt.Status),
		Field("parent_message_id", attempt.ParentMessageID),
		Field("parent_location_id", attempt.ParentLocationID),
		Field("answer_reconciliation", attempt.AnswerReconciliation),
		Field("answer_error", attempt.AnswerErrorCode),
		Field("smtp_retried", "false"),
	)
}

func newMailMutationOperationsCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "mutations",
		Short:   "Inspect message location mutation history",
		Args:    cobra.NoArgs,
		Example: "  icloud mail operations mutations list\n  icloud mail operations mutations show mut_…",
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(newMailOperationListCommand(state, mailOperationMutations))
	command.AddCommand(newMailLocationMutationShowCommand(state))
	return command
}

func newMailOperationListCommand(state *executionState, kind mailOperationKind) *cobra.Command {
	var jsonOutput bool
	var limit int
	var cursor string
	var input jsonInputOptions
	example := "  icloud mail operations outbound list\n  icloud mail operations outbound list --limit 50 --cursor ocur_… --json\n  icloud mail operations outbound list --input-json request.json --json"
	short := "List bounded local outbound send attempts"
	if kind == mailOperationMutations {
		example = "  icloud mail operations mutations list\n  icloud mail operations mutations list --limit 50 --cursor mcur_… --json\n  icloud mail operations mutations list --input-json request.json --json"
		short = "List bounded local message location mutations"
	}
	command := &cobra.Command{
		Use:     "list",
		Short:   short,
		Args:    cobra.NoArgs,
		Example: example,
		RunE: func(cmd *cobra.Command, _ []string) error {
			request := operationListReadJSONRequest{Operation: string(kind), Limit: DefaultPageSize}
			if used, err := readJSONInput(cmd, state, input, &request, "limit", "cursor"); err != nil {
				return err
			} else if used {
				operation := mailOperationKind(strings.ToLower(strings.TrimSpace(request.Operation)))
				if operation != kind {
					return NewAppError(ErrorUsage, fmt.Sprintf("operation must match %s for this command", kind), false)
				}
				limit = request.Limit
				cursor = request.Cursor
			}
			return runMailOperationList(cmd, state, kind, jsonOutput, limit, cursor)
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum attempts to return (1-%d)", MaximumPageSize))
	command.Flags().StringVar(&cursor, "cursor", "", "Continue from a next_cursor returned by this operation list")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindReadJSONInput(command, &input)
	return command
}

func newMailOutboundOperationShowCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:     "show <attempt-id>",
		Short:   "Show one outbound send attempt by stable ID",
		Args:    cobra.ExactArgs(1),
		Example: "  icloud mail operations outbound show send_…\n  icloud mail operations outbound show send_… --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			accountValue, attempt, schemaVersion, err := state.mail.MailOutboundAttempt(cmd.Context(), state.global.Home, state.global.Account, args[0])
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailOutboundAttemptData{
					AccountID: accountValue.ID, Attempt: sanitizeOutboundAttempt(attempt), SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderOutboundAttempt(NewCompactTextRenderer(state.stdout), attempt)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func newMailLocationMutationShowCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:     "show <mutation-id>",
		Short:   "Show one message location mutation by stable ID",
		Args:    cobra.ExactArgs(1),
		Example: "  icloud mail operations mutations show mut_…\n  icloud mail operations mutations show mut_… --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			accountValue, mutation, schemaVersion, err := state.mail.MailLocationMutation(cmd.Context(), state.global.Home, state.global.Account, args[0])
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailLocationMutationOperationData{
					AccountID: accountValue.ID, Mutation: mutation, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderLocationMutation(NewCompactTextRenderer(state.stdout), mutation)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func newMailStatusCommand(state *executionState) *cobra.Command {
	var outbound bool
	var mutations bool
	var jsonOutput bool
	var limit int
	var cursor string
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "status",
		Short:   "Compatibility alias for `mail operations … list`",
		Args:    cobra.NoArgs,
		Example: "  icloud mail operations outbound list\n  icloud mail operations mutations list\n  icloud mail operations mutations list --limit 50 --json\n  icloud mail status --input-json request.json --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			request := operationListReadJSONRequest{Limit: DefaultPageSize}
			if used, err := readJSONInput(cmd, state, input, &request, "outbound", "mutations", "limit", "cursor"); err != nil {
				return err
			} else if used {
				switch mailOperationKind(strings.ToLower(strings.TrimSpace(request.Operation))) {
				case mailOperationOutbound:
					outbound, mutations = true, false
				case mailOperationMutations:
					outbound, mutations = false, true
				default:
					return NewAppError(ErrorUsage, "operation must be outbound or mutations", false)
				}
				limit = request.Limit
				cursor = request.Cursor
			}
			if outbound == mutations {
				return NewAppError(ErrorUsage, "mail status requires exactly one of --outbound or --mutations", false)
			}
			if mutations {
				return runMailOperationList(cmd, state, mailOperationMutations, jsonOutput, limit, cursor)
			}
			return runMailOperationList(cmd, state, mailOperationOutbound, jsonOutput, limit, cursor)
		},
	}
	command.Flags().BoolVar(&outbound, "outbound", false, "List recent outbound send attempts")
	command.Flags().BoolVar(&mutations, "mutations", false, "List recent message location mutation intents and outcomes")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum attempts to return (1-%d)", MaximumPageSize))
	command.Flags().StringVar(&cursor, "cursor", "", "Continue from a next_cursor returned by the same status mode")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindReadJSONInput(command, &input)
	return command
}

func runMailOperationList(cmd *cobra.Command, state *executionState, kind mailOperationKind, jsonOutput bool, limit int, cursor string) error {
	if limit < 1 || limit > MaximumPageSize {
		return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
	}
	if kind == mailOperationMutations {
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
			if err := renderLocationMutation(renderer, mutation); err != nil {
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
		if err := renderOutboundAttempt(renderer, attempt); err != nil {
			return err
		}
	}
	return renderer.Summary("outbound-status",
		Field("account", accountValue.ID),
		Field("count", len(attempts)),
		Field("more", more),
		Field("next_cursor", nextCursor),
	)
}

func renderOutboundAttempt(renderer *CompactTextRenderer, attempt maildomain.OutboundAttempt) error {
	return renderer.Record("outbound", attempt.ID,
		Field("kind", attempt.Kind),
		Field("parent_message_id", attempt.ParentMessageID),
		Field("parent_location_id", attempt.ParentLocationID),
		Field("source_message_id", attempt.SourceMessageID),
		Field("status", attempt.Status),
		Field("message_id", attempt.MessageID),
		Field("thread_id", attempt.ThreadID),
		Field("attachments", attempt.AttachmentCount),
		Field("attachment_bytes", attempt.AttachmentBytes),
		Field("reconciliation", attempt.Reconciliation),
		Field("reconciliation_error", attempt.ReconciliationErrorCode),
		Field("answer_reconciliation", attempt.AnswerReconciliation),
		Field("answer_error", attempt.AnswerErrorCode),
		Field("retry_of", attempt.RetryOfAttemptID),
		Field("error_code", attempt.ErrorCode),
		Field("updated_at", attempt.UpdatedAt),
		Field("next", outboundAttemptNext(attempt)),
	)
}

func renderLocationMutation(renderer *CompactTextRenderer, mutation maildomain.LocationMutation) error {
	return renderer.Record("mutation", mutation.ID,
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
	)
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
