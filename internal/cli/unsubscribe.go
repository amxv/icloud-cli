package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type mailUnsubscribeService interface {
	MailUnsubscribePreview(context.Context, string, string, string, string) (account.Account, maildomain.UnsubscribePlan, int64, error)
	MailUnsubscribe(context.Context, string, string, string, string) (account.Account, maildomain.UnsubscribePlan, maildomain.UnsubscribeResult, int64, error)
}

type mailUnsubscribePreviewData struct {
	AccountID     string                     `json:"account_id"`
	Plan          maildomain.UnsubscribePlan `json:"plan"`
	SchemaVersion int64                      `json:"schema_version"`
}

type mailUnsubscribeData struct {
	AccountID     string                       `json:"account_id"`
	Plan          maildomain.UnsubscribePlan   `json:"plan"`
	Result        maildomain.UnsubscribeResult `json:"result"`
	SchemaVersion int64                        `json:"schema_version"`
}

func newMailUnsubscribeCommand(state *executionState) *cobra.Command {
	var method string
	var dryRun, yes, jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:   "unsubscribe <msg_...>",
		Short: "Safely execute a standards-based mailing-list unsubscribe action",
		Args:  exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail unsubscribe msg_… --dry-run\n" +
			"  icloud mail unsubscribe msg_… --yes\n" +
			"  icloud mail unsubscribe msg_… --method mailto:2 --yes --json\n" +
			"  icloud mail unsubscribe --input-json request.json --dry-run --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			service, ok := state.mail.(mailUnsubscribeService)
			if !ok {
				return NewAppError(ErrorConfiguration, "mail unsubscribe is unavailable", false)
			}
			if dryRun && yes {
				return NewAppError(ErrorUsage, "--dry-run and --yes cannot be combined", false)
			}
			messageID := ""
			if len(args) == 1 {
				messageID = strings.TrimSpace(args[0])
			}
			var jsonRequest unsubscribeJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest, "method"); err != nil {
				return err
			} else if used {
				messageID = strings.TrimSpace(jsonRequest.MessageID)
				method = strings.TrimSpace(jsonRequest.Method)
			}
			if messageID == "" {
				return NewAppError(ErrorUsage, "one message_id is required", false)
			}
			if dryRun {
				accountValue, plan, schemaVersion, err := service.MailUnsubscribePreview(cmd.Context(), state.global.Home, state.global.Account, messageID, method)
				if err != nil {
					return err
				}
				if jsonOutput {
					return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailUnsubscribePreviewData{AccountID: accountValue.ID, Plan: plan, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
				}
				return renderMailUnsubscribePlan(state.stdout, plan)
			}

			interactive := isTerminalReader(state.stdin) && !state.global.NoInput
			if !yes && !interactive {
				return NewAppError(ErrorSafety, "non-interactive mailing-list unsubscribe requires --yes", false)
			}
			if !yes {
				_, plan, _, err := service.MailUnsubscribePreview(cmd.Context(), state.global.Home, state.global.Account, messageID, method)
				if err != nil {
					return err
				}
				if err := renderMailUnsubscribePlan(state.stdout, plan); err != nil {
					return err
				}
				if plan.SelectionRequired || plan.Selected == nil {
					return NewAppError(ErrorUsage, "multiple unsubscribe methods are available; rerun with --method using one of the safe IDs shown above", false)
				}
				confirmed, err := confirmMailUnsubscribe(state.stdin, state.stderr, *plan.Selected)
				if err != nil {
					return err
				}
				if !confirmed {
					return NewAppError(ErrorSafety, "mailing-list unsubscribe canceled before any network or provider action", false)
				}
			}

			accountValue, plan, result, schemaVersion, err := service.MailUnsubscribe(cmd.Context(), state.global.Home, state.global.Account, messageID, method)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailUnsubscribeData{AccountID: accountValue.ID, Plan: plan, Result: result, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderMailUnsubscribeResult(state.stdout, result)
		},
	}
	command.Flags().StringVar(&method, "method", "", "Safe method ID from dry-run, for example one-click or mailto:2")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Review only local sanitized unsubscribe capability; do not verify DKIM, POST, or send mail")
	command.Flags().BoolVar(&yes, "yes", false, "Confirm an intentional non-interactive unsubscribe action")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON envelope")
	bindJSONInput(command, &input)
	return command
}

func renderMailUnsubscribePlan(writer io.Writer, plan maildomain.UnsubscribePlan) error {
	renderer := NewCompactTextRenderer(writer)
	for _, method := range plan.Alternatives {
		if err := renderer.Record("unsubscribe-method", method.ID,
			Field("id", method.ID), Field("method", method.Kind), Field("host", method.Host), Field("dkim_required", method.DKIMRequired)); err != nil {
			return err
		}
	}
	selectedID, selectedMethod, selectedHost := "", "", ""
	if plan.Selected != nil {
		selectedID = plan.Selected.ID
		selectedMethod = string(plan.Selected.Kind)
		selectedHost = plan.Selected.Host
	}
	return renderer.Summary("mail-unsubscribe-review",
		Field("message", plan.MessageID), Field("available", plan.Available), Field("one_click_declared", plan.OneClickDeclared),
		Field("selection_required", plan.SelectionRequired), Field("selected_id", selectedID), Field("method", selectedMethod),
		Field("host", selectedHost), Field("dry_run", plan.DryRun), Field("network_contacted", false),
	)
}

func renderMailUnsubscribeResult(writer io.Writer, result maildomain.UnsubscribeResult) error {
	return NewCompactTextRenderer(writer).Summary("mail-unsubscribe",
		Field("message", result.MessageID), Field("method", result.Method), Field("host", result.Host),
		Field("status", result.Status), Field("reused", result.Reused), Field("attempt", result.AttemptID),
		Field("dkim_domain", result.DKIMDomain),
	)
}

func confirmMailUnsubscribe(reader io.Reader, stderr io.Writer, method maildomain.UnsubscribeMethod) (bool, error) {
	if _, err := fmt.Fprintf(stderr, "Unsubscribe this message using %s at %s? [y/N]: ", method.Kind, method.Host); err != nil {
		return false, WrapAppError(ErrorInternal, "cannot write the unsubscribe confirmation prompt", false, err)
	}
	line, err := bufio.NewReader(io.LimitReader(reader, 64)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, WrapAppError(ErrorConfiguration, "cannot read the unsubscribe confirmation", false, err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
