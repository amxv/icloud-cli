package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type mailboxImportService interface {
	MailMailboxImportPreview(context.Context, string, string, string, maildomain.MailboxImportRequest) (account.Account, folder.Folder, maildomain.MailboxImportPlan, int64, error)
	MailMailboxImport(context.Context, string, string, string, maildomain.MailboxImportRequest) (account.Account, folder.Folder, maildomain.MailboxImportRun, int64, error)
}

type mailMailboxImportPreviewData struct {
	AccountID     string                       `json:"account_id"`
	Plan          maildomain.MailboxImportPlan `json:"plan"`
	SchemaVersion int64                        `json:"schema_version"`
}

type mailMailboxImportData struct {
	AccountID     string                      `json:"account_id"`
	Folder        folder.Folder               `json:"folder"`
	Run           maildomain.MailboxImportRun `json:"run"`
	SchemaVersion int64                       `json:"schema_version"`
}

func newMailMailboxImportCommand(state *executionState) *cobra.Command {
	var destination, mode string
	var maxMessages int
	var dryRun, yes, jsonOutput, jsonlOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:   "import <file.mbox>",
		Short: "Resume a bounded mboxrd import through exact single-message APPEND recovery",
		Args:  exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail mailbox import archive.mbox --to inbox --dry-run\n" +
			"  icloud mail mailbox import archive.mbox --to Archive --max-messages 100 --yes\n" +
			"  icloud mail mailbox import archive.mbox --to inbox --mode continue-on-error --yes --jsonl\n" +
			"  icloud mail mailbox import --input-json request.json --yes --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, JSONL: jsonlOutput}, OutputSupport{JSON: true, JSONL: true}); err != nil {
				return err
			}
			if dryRun && yes {
				return NewAppError(ErrorUsage, "--dry-run and --yes cannot be combined", false)
			}
			service, ok := state.mail.(mailboxImportService)
			if !ok {
				return NewAppError(ErrorConfiguration, "mailbox import is unavailable", false)
			}
			sourcePath := ""
			if len(args) == 1 {
				sourcePath = args[0]
			}
			request := maildomain.MailboxImportRequest{Folder: destination, Mode: mode, MaxMessages: maxMessages}
			var jsonRequest mailboxImportJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest, "to", "mode", "max-messages"); err != nil {
				return err
			} else if used {
				sourcePath = jsonRequest.File
				request = maildomain.MailboxImportRequest{Folder: jsonRequest.To, Mode: jsonRequest.Mode, MaxMessages: jsonRequest.MaxMessages}
			}
			if strings.TrimSpace(sourcePath) == "" {
				return NewAppError(ErrorUsage, "one .mbox source file is required", false)
			}

			if dryRun {
				accountValue, _, plan, schemaVersion, err := service.MailMailboxImportPreview(cmd.Context(), state.global.Home, state.global.Account, sourcePath, request)
				if err != nil {
					return err
				}
				if jsonOutput {
					return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailMailboxImportPreviewData{
						AccountID: accountValue.ID, Plan: plan, SchemaVersion: schemaVersion,
					}, Meta: EnvelopeMeta{RequestID: state.requestID}})
				}
				if jsonlOutput {
					return WriteJSONLEvent(state.stdout, JSONLEvent{Event: "review", Sequence: 1, AccountID: accountValue.ID, Timestamp: time.Now().UTC(), Payload: map[string]any{"plan": plan, "schema_version": schemaVersion}})
				}
				return renderMailboxImportPreview(state.stdout, plan, true)
			}

			interactive := isTerminalReader(state.stdin) && !state.global.NoInput
			if !yes && !interactive {
				return NewAppError(ErrorSafety, "non-interactive mailbox import requires --yes", false)
			}
			if !yes {
				_, _, plan, _, err := service.MailMailboxImportPreview(cmd.Context(), state.global.Home, state.global.Account, sourcePath, request)
				if err != nil {
					return err
				}
				if err := renderMailboxImportPreview(state.stdout, plan, false); err != nil {
					return err
				}
				confirmed, err := confirmMailboxImport(state.stdin, state.stderr, plan.FolderName, plan.MaxMessages)
				if err != nil {
					return err
				}
				if !confirmed {
					return NewAppError(ErrorSafety, "mailbox import canceled before creating a job or contacting IMAP", false)
				}
			}

			accountValue, folderValue, run, schemaVersion, importErr := service.MailMailboxImport(cmd.Context(), state.global.Home, state.global.Account, sourcePath, request)
			if jsonlOutput {
				if err := renderMailboxImportJSONL(state.stdout, accountValue.ID, run, schemaVersion); err != nil {
					return err
				}
				if importErr != nil {
					return markJSONLHandled(importErr)
				}
				return nil
			}
			if importErr != nil {
				if !jsonOutput && run.Job.ID != "" {
					if err := renderMailboxImportRun(state.stdout, run); err != nil {
						return err
					}
				}
				return importErr
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailMailboxImportData{
					AccountID: accountValue.ID, Folder: folderValue, Run: run, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderMailboxImportRun(state.stdout, run)
		},
	}
	command.Flags().StringVar(&destination, "to", "", "Current selectable destination folder kind, ID, name, or wire name")
	command.Flags().StringVar(&mode, "mode", "atomic", "Failure mode for this bounded pass: fail fast or continue-on-error")
	command.Flags().IntVar(&maxMessages, "max-messages", MaximumBulkItems, "Maximum archive records to attempt in this invocation")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Fingerprint and validate the next archive record locally without creating a job or contacting IMAP")
	command.Flags().BoolVar(&yes, "yes", false, "Confirm an intentional non-interactive provider import")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&jsonlOutput, "jsonl", false, "Emit ordered record results plus one terminal completed event")
	bindJSONInput(command, &input)
	return command
}

func renderMailboxImportPreview(writer io.Writer, plan maildomain.MailboxImportPlan, dryRun bool) error {
	return NewCompactTextRenderer(writer).Summary("mailbox-import-review",
		Field("folder", plan.FolderName), Field("folder_id", plan.FolderID),
		Field("source_bytes", plan.SourceByteSize), Field("source_sha256", plan.SourceContentHash),
		Field("start_index", plan.StartIndex), Field("start_offset", plan.StartOffset),
		Field("already_imported", plan.AlreadyImported), Field("max_messages", plan.MaxMessages),
		Field("mode", plan.Mode), Field("complete", plan.Complete), Field("dry_run", dryRun),
		Field("imap_contacted", false), Field("job_created", false),
	)
}

func confirmMailboxImport(reader io.Reader, stderr io.Writer, folderName string, maxMessages int) (bool, error) {
	if _, err := fmt.Fprintf(stderr, "Import up to %d reviewed mboxrd records into %s using durable per-message recovery? [y/N]: ", maxMessages, folderName); err != nil {
		return false, WrapAppError(ErrorInternal, "cannot write the mailbox import confirmation prompt", false, err)
	}
	line, err := bufio.NewReader(io.LimitReader(reader, 64)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, WrapAppError(ErrorConfiguration, "cannot read mailbox import confirmation", false, err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func renderMailboxImportRun(writer io.Writer, run maildomain.MailboxImportRun) error {
	renderer := NewCompactTextRenderer(writer)
	for _, item := range run.Items {
		if err := renderer.Record("mailbox-import-item", item.MessageID,
			Field("index", item.Index), Field("status", item.Status), Field("import", item.ImportID),
			Field("message", item.MessageID), Field("location", item.LocationID), Field("reused", item.Reused), Field("error_code", item.ErrorCode)); err != nil {
			return err
		}
	}
	return renderer.Summary("mailbox-import",
		Field("job", run.Summary.JobID), Field("folder_id", run.Summary.FolderID), Field("mode", run.Summary.Mode),
		Field("status", run.Summary.Status), Field("attempted", run.Summary.Attempted), Field("confirmed", run.Summary.Confirmed),
		Field("failed", run.Summary.Failed), Field("reused", run.Summary.Reused), Field("imported", run.Summary.ImportedCount),
		Field("next_index", run.Summary.NextIndex), Field("next_offset", run.Summary.NextOffset),
		Field("source_bytes", run.Summary.SourceByteSize), Field("partial", run.Summary.Partial),
		Field("more", run.Summary.More), Field("complete", run.Summary.Complete),
	)
}

func renderMailboxImportJSONL(writer io.Writer, accountID string, run maildomain.MailboxImportRun, schemaVersion int64) error {
	sequence := uint64(1)
	for _, item := range run.Items {
		payload := map[string]any{"index": item.Index, "status": item.Status, "result": item, "job_id": run.Job.ID}
		if err := WriteJSONLEvent(writer, JSONLEvent{Event: "item", Sequence: sequence, AccountID: accountID, Timestamp: time.Now().UTC(), Payload: payload}); err != nil {
			return err
		}
		sequence++
	}
	return WriteJSONLEvent(writer, JSONLEvent{Event: "completed", Sequence: sequence, AccountID: accountID, Timestamp: time.Now().UTC(), Payload: map[string]any{
		"job_id": run.Job.ID, "summary": run.Summary, "schema_version": schemaVersion,
	}})
}
