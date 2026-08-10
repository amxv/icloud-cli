package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type mailboxExportService interface {
	MailMailboxExport(context.Context, string, string, string, io.Writer) (account.Account, folder.Folder, maildomain.MailboxExportSummary, int64, error)
}

type mailMailboxExportData struct {
	AccountID     string                          `json:"account_id"`
	Folder        folder.Folder                   `json:"folder"`
	Summary       maildomain.MailboxExportSummary `json:"summary"`
	SchemaVersion int64                           `json:"schema_version"`
}

func newMailMailboxCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:   "mailbox",
		Short: "Import or export complete mailbox archives",
		Args:  cobra.NoArgs,
	}
	command.AddCommand(newMailMailboxImportCommand(state))
	command.AddCommand(newMailMailboxExportCommand(state))
	return command
}

func newMailMailboxExportCommand(state *executionState) *cobra.Command {
	var format, output string
	var force, jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "export <folder>",
		Short:   "Stream one synchronized mailbox as mboxrd without contacting iCloud",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail mailbox export inbox --format mboxrd --output inbox.mbox\n  icloud mail mailbox export Archive --output - > archive.mbox\n  icloud mail mailbox export --input-json request.json --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			service, ok := state.mail.(mailboxExportService)
			if !ok {
				return NewAppError(ErrorConfiguration, "mailbox export is unavailable", false)
			}
			folderSelector := ""
			if len(args) == 1 {
				folderSelector = args[0]
			}
			var jsonRequest mailboxExportJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest, "format", "output", "force"); err != nil {
				return err
			} else if used {
				folderSelector = jsonRequest.Folder
				format = jsonRequest.Format
				output = jsonRequest.Output
				force = jsonRequest.Force
			}
			folderSelector = strings.TrimSpace(folderSelector)
			if folderSelector == "" {
				return NewAppError(ErrorUsage, "one synchronized mailbox folder is required", false)
			}
			format = strings.ToLower(strings.TrimSpace(format))
			if format == "" {
				format = "mboxrd"
			}
			if format != "mboxrd" {
				return NewAppError(ErrorUsage, "mailbox export currently supports only --format mboxrd", false)
			}
			output = strings.TrimSpace(output)
			if output == "" {
				return NewAppError(ErrorUsage, "mailbox export requires --output <file|->", false)
			}
			if output == "-" && force {
				return NewAppError(ErrorUsage, "--force is not valid with --output -", false)
			}
			if output == "-" && jsonOutput {
				return NewAppError(ErrorUsage, "--json cannot share stdout with a native mboxrd stream; write the archive to a file for JSON summary output", false)
			}

			var (
				accountValue account.Account
				folderValue  folder.Folder
				summary      maildomain.MailboxExportSummary
				schema       int64
				err          error
			)
			if output == "-" {
				accountValue, folderValue, summary, schema, err = service.MailMailboxExport(cmd.Context(), state.global.Home, state.global.Account, folderSelector, state.stdout)
				_ = accountValue
				_ = schema
				if err != nil {
					return err
				}
				return renderMailboxExportSummary(state.stderr, folderValue, summary)
			}

			err = writeMailboxExportFile(output, force, func(writer io.Writer) error {
				var exportErr error
				accountValue, folderValue, summary, schema, exportErr = service.MailMailboxExport(cmd.Context(), state.global.Home, state.global.Account, folderSelector, writer)
				return exportErr
			})
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailMailboxExportData{
					AccountID: accountValue.ID, Folder: folderValue, Summary: summary, SchemaVersion: schema,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderMailboxExportSummary(state.stdout, folderValue, summary)
		},
	}
	command.Flags().StringVar(&format, "format", "mboxrd", "Mailbox archive format; currently mboxrd")
	command.Flags().StringVar(&output, "output", "", "Archive destination path, or - for native stdout")
	command.Flags().BoolVar(&force, "force", false, "Replace an existing regular archive file")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON summary when output is a file")
	bindJSONInput(command, &input)
	return command
}

func renderMailboxExportSummary(writer io.Writer, folderValue folder.Folder, summary maildomain.MailboxExportSummary) error {
	cursor := ""
	if summary.LastCursor != nil {
		cursor = fmt.Sprintf("%d:%d:%s", summary.LastCursor.UIDValidity, summary.LastCursor.UID, summary.LastCursor.LocationID)
	}
	return NewCompactTextRenderer(writer).Summary("mailbox-export",
		Field("folder", folderValue.Name), Field("folder_id", folderValue.ID), Field("format", summary.Format),
		Field("count", summary.Count), Field("bytes", summary.Bytes), Field("complete", summary.Complete), Field("cursor", cursor),
	)
}

func writeMailboxExportFile(output string, force bool, export func(io.Writer) error) error {
	if export == nil {
		return NewAppError(ErrorInternal, "mailbox export writer is unavailable", false)
	}
	output = filepath.Clean(strings.TrimSpace(output))
	if output == "" || output == "." {
		return NewAppError(ErrorUsage, "invalid mailbox export destination", false)
	}
	info, err := os.Lstat(output)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return NewAppError(ErrorSafety, "mailbox export destination is not a regular file", false)
		}
		if !force {
			return NewAppError(ErrorConflict, "mailbox export destination already exists; use --force to replace it", false)
		}
	case !errors.Is(err, os.ErrNotExist):
		return WrapAppError(ErrorConfiguration, "cannot inspect mailbox export destination", false, err)
	}
	directory := filepath.Dir(output)
	temporary, err := os.CreateTemp(directory, ".icloud-mailbox-*.tmp")
	if err != nil {
		return WrapAppError(ErrorConfiguration, "cannot create the mailbox export file", false, err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o600); err != nil {
		cleanup()
		return WrapAppError(ErrorConfiguration, "cannot secure the mailbox export file", false, err)
	}
	if err := export(temporary); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return WrapAppError(ErrorInternal, "cannot flush the mailbox export file", true, err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return WrapAppError(ErrorInternal, "cannot close the mailbox export file", true, err)
	}
	if err := installExportFile(temporaryPath, output, force); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}
