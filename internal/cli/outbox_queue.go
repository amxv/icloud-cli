package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type publicOutboxItem struct {
	ID               string                  `json:"id"`
	SourceAttemptID  string                  `json:"source_attempt_id"`
	LatestAttemptID  string                  `json:"latest_attempt_id,omitempty"`
	SenderIdentityID string                  `json:"sender_identity_id"`
	Status           maildomain.OutboxStatus `json:"status"`
	FailureStage     string                  `json:"failure_stage,omitempty"`
	FailureCode      string                  `json:"failure_code,omitempty"`
	Kind             maildomain.OutboundKind `json:"kind"`
	ToCount          int                     `json:"to_count"`
	CCCount          int                     `json:"cc_count"`
	BCCCount         int                     `json:"bcc_count"`
	BodyPartCount    int                     `json:"body_part_count"`
	AttachmentCount  int                     `json:"attachment_count"`
	AttachmentBytes  int64                   `json:"attachment_bytes"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
}

type mailOutboxListData struct {
	AccountID     string             `json:"account_id"`
	Items         []publicOutboxItem `json:"items"`
	Count         int                `json:"count"`
	More          bool               `json:"more,omitempty"`
	NextCursor    string             `json:"next_cursor,omitempty"`
	SchemaVersion int64              `json:"schema_version"`
}

type mailOutboxItemData struct {
	AccountID     string           `json:"account_id"`
	Item          publicOutboxItem `json:"item"`
	SchemaVersion int64            `json:"schema_version"`
}

type mailOutboxRetryData struct {
	AccountID     string                `json:"account_id"`
	Item          publicOutboxItem      `json:"item"`
	Attempt       publicOutboundAttempt `json:"attempt"`
	SchemaVersion int64                 `json:"schema_version"`
}

func newMailOutboxCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "outbox",
		Short:   "Inspect and manually retry sends proven safe before SMTP DATA",
		Args:    cobra.NoArgs,
		Example: "  icloud mail outbox list\n  icloud mail outbox show out_…\n  icloud mail outbox retry out_… --dry-run\n  icloud mail outbox retry out_… --yes\n  icloud mail outbox cancel out_…",
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(newMailOutboxListCommand(state))
	command.AddCommand(newMailOutboxShowCommand(state))
	command.AddCommand(newMailOutboxRetryCommand(state))
	command.AddCommand(newMailOutboxCancelCommand(state))
	return command
}

func newMailOutboxListCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var limit int
	var cursor string
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "list",
		Short:   "List bounded local safe-retry Outbox items without exposing message payloads or recipients",
		Args:    cobra.NoArgs,
		Example: "  icloud mail outbox list\n  icloud mail outbox list --limit 50 --cursor obcur_… --json\n  icloud mail outbox list --input-json request.json --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			request := outboxListReadJSONRequest{Limit: DefaultPageSize}
			if used, err := readJSONInput(cmd, state, input, &request, "limit", "cursor"); err != nil {
				return err
			} else if used {
				limit, cursor = request.Limit, request.Cursor
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			accountValue, values, more, next, schemaVersion, err := state.mail.MailOutboxListPage(cmd.Context(), state.global.Home, state.global.Account, cursor, limit)
			if err != nil {
				return err
			}
			public := make([]publicOutboxItem, 0, len(values))
			for _, value := range values {
				public = append(public, sanitizeOutboxItem(value))
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailOutboxListData{AccountID: accountValue.ID, Items: public, Count: len(public), More: more, NextCursor: next, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, value := range values {
				if err := renderOutboxItem(renderer, value); err != nil {
					return err
				}
			}
			return renderer.Summary("outbox", Field("account", accountValue.ID), Field("count", len(values)), Field("more", more), Field("next_cursor", next))
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum Outbox items to return (1-%d)", MaximumPageSize))
	command.Flags().StringVar(&cursor, "cursor", "", "Continue from a next_cursor returned for the same account")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindReadJSONInput(command, &input)
	return command
}

func newMailOutboxShowCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "show <outbox-id>",
		Short:   "Show one safe-retry Outbox item's lifecycle metadata",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail outbox show out_…\n  icloud mail outbox show --input-json request.json --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			outboxID := ""
			if len(args) == 1 {
				outboxID = strings.TrimSpace(args[0])
			}
			var request outboxTargetJSONRequest
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				outboxID = strings.TrimSpace(request.OutboxID)
			}
			accountValue, item, schemaVersion, err := state.mail.MailOutboxItem(cmd.Context(), state.global.Home, state.global.Account, outboxID)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailOutboxItemData{AccountID: accountValue.ID, Item: sanitizeOutboxItem(item), SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderOutboxItem(NewCompactTextRenderer(state.stdout), item)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindReadJSONInput(command, &input)
	return command
}

func newMailOutboxRetryCommand(state *executionState) *cobra.Command {
	var dryRun, yes, jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "retry <outbox-id>",
		Short:   "Explicitly replay the exact reviewed raw message and private SMTP envelope",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail outbox retry out_… --dry-run\n  icloud mail outbox retry out_… --yes --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			outboxID := ""
			if len(args) == 1 {
				outboxID = strings.TrimSpace(args[0])
			}
			var request outboxTargetJSONRequest
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				outboxID = strings.TrimSpace(request.OutboxID)
			}
			if dryRun && yes {
				return NewAppError(ErrorUsage, "--dry-run and --yes cannot be combined", false)
			}
			accountValue, item, schemaVersion, err := state.mail.MailOutboxItem(cmd.Context(), state.global.Home, state.global.Account, outboxID)
			if err != nil {
				return err
			}
			if !item.Retryable() {
				return NewAppError(ErrorConflict, "Outbox item is not retryable", false)
			}
			if dryRun {
				if jsonOutput {
					return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailOutboxItemData{AccountID: accountValue.ID, Item: sanitizeOutboxItem(item), SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
				}
				return NewCompactTextRenderer(state.stdout).Record("outbox-retry-preview", item.ID,
					Field("status", item.Status), Field("source_attempt", item.SourceAttemptID), Field("attachments", item.AttachmentCount),
					Field("safety", "exact reviewed raw bytes and private SMTP envelope will be replayed only on explicit retry"))
			}
			interactive := isTerminalReader(state.stdin) && !state.global.NoInput
			if !yes && !interactive {
				return NewAppError(ErrorSafety, "non-interactive Outbox retry requires --yes", false)
			}
			if !yes {
				if err := renderOutboxItem(NewCompactTextRenderer(state.stdout), item); err != nil {
					return err
				}
				confirmed, err := confirmOutboxRetry(state.stdin, state.stderr)
				if err != nil {
					return err
				}
				if !confirmed {
					return NewAppError(ErrorSafety, "Outbox retry canceled before contacting SMTP", false)
				}
			}
			accountValue, item, attempt, schemaVersion, err := state.mail.MailOutboxRetry(cmd.Context(), state.global.Home, state.global.Account, outboxID)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailOutboxRetryData{AccountID: accountValue.ID, Item: sanitizeOutboxItem(item), Attempt: sanitizeOutboundAttempt(attempt), SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			if err := renderSendOutcome(NewCompactTextRenderer(state.stdout), attempt, false); err != nil {
				return err
			}
			return renderOutboxItem(NewCompactTextRenderer(state.stdout), item)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Review retry lifecycle metadata without contacting SMTP")
	command.Flags().BoolVar(&yes, "yes", false, "Confirm an intentional non-interactive exact-message replay")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailOutboxCancelCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "cancel <outbox-id>",
		Short:   "Cancel one retryable local Outbox item without contacting SMTP",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail outbox cancel out_…\n  icloud mail outbox cancel --input-json request.json --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			outboxID := ""
			if len(args) == 1 {
				outboxID = strings.TrimSpace(args[0])
			}
			var request outboxTargetJSONRequest
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				outboxID = strings.TrimSpace(request.OutboxID)
			}
			accountValue, item, schemaVersion, err := state.mail.MailOutboxCancel(cmd.Context(), state.global.Home, state.global.Account, outboxID)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailOutboxItemData{AccountID: accountValue.ID, Item: sanitizeOutboxItem(item), SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderOutboxItem(NewCompactTextRenderer(state.stdout), item)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func sanitizeOutboxItem(value maildomain.OutboxItem) publicOutboxItem {
	return publicOutboxItem{
		ID: value.ID, SourceAttemptID: value.SourceAttemptID, LatestAttemptID: value.LatestAttemptID,
		SenderIdentityID: value.SenderIdentityID, Status: value.Status, FailureStage: value.FailureStage, FailureCode: value.FailureCode,
		Kind: value.Kind, ToCount: value.ToCount, CCCount: value.CCCount, BCCCount: value.BCCCount,
		BodyPartCount: value.BodyPartCount, AttachmentCount: value.AttachmentCount, AttachmentBytes: value.AttachmentBytes,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func renderOutboxItem(renderer *CompactTextRenderer, item maildomain.OutboxItem) error {
	return renderer.Record("outbox", item.ID,
		Field("status", item.Status), Field("kind", item.Kind), Field("source_attempt", item.SourceAttemptID),
		Field("latest_attempt", item.LatestAttemptID), Field("sender_identity", item.SenderIdentityID),
		Field("to", item.ToCount), Field("cc", item.CCCount), Field("bcc", item.BCCCount),
		Field("attachments", item.AttachmentCount), Field("attachment_bytes", item.AttachmentBytes),
		Field("failure_stage", item.FailureStage), Field("failure_code", item.FailureCode), Field("updated_at", item.UpdatedAt),
		Field("next", outboxItemNext(item)),
	)
}

func outboxItemNext(item maildomain.OutboxItem) string {
	switch item.Status {
	case maildomain.OutboxRetryable:
		return "review with mail outbox show, then explicitly retry or cancel"
	case maildomain.OutboxRetrying:
		return "retry outcome is being resolved; reopen the CLI before taking another action"
	case maildomain.OutboxUnknown:
		return "inspect Sent and the recipient; do not retry automatically"
	case maildomain.OutboxFailed:
		return "linked retry is terminal for the pre-DATA Outbox contract; inspect the outbound attempt before any explicit retry"
	default:
		return "none"
	}
}

func confirmOutboxRetry(reader io.Reader, stderr io.Writer) (bool, error) {
	if _, err := fmt.Fprint(stderr, "Replay this exact Outbox message through SMTP? [y/N]: "); err != nil {
		return false, WrapAppError(ErrorInternal, "cannot write the Outbox retry confirmation prompt", false, err)
	}
	line, err := bufio.NewReader(io.LimitReader(reader, 64)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, WrapAppError(ErrorConfiguration, "cannot read the Outbox retry confirmation", false, err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
