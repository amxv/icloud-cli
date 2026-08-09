package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type publicAttachment struct {
	ID           string                            `json:"id"`
	MessageID    string                            `json:"message_id"`
	PartID       string                            `json:"part_id"`
	Ordinal      int                               `json:"ordinal"`
	PartPath     string                            `json:"part_path"`
	Filename     string                            `json:"filename,omitempty"`
	MediaType    string                            `json:"media_type,omitempty"`
	MediaParams  map[string]string                 `json:"media_params,omitempty"`
	Disposition  string                            `json:"disposition,omitempty"`
	ContentID    string                            `json:"content_id,omitempty"`
	Inline       bool                              `json:"inline"`
	ContentHash  string                            `json:"content_hash,omitempty"`
	ByteSize     int64                             `json:"byte_size"`
	Availability maildomain.AttachmentAvailability `json:"availability"`
	ErrorCode    string                            `json:"error_code,omitempty"`
}

type mailAttachmentsData struct {
	AccountID     string                      `json:"account_id"`
	Target        maildomain.AttachmentTarget `json:"target"`
	MessageID     string                      `json:"message_id,omitempty"`
	ThreadID      string                      `json:"thread_id,omitempty"`
	Attachments   []publicAttachment          `json:"attachments"`
	Count         int                         `json:"count"`
	More          bool                        `json:"more,omitempty"`
	NextCursor    string                      `json:"next_cursor,omitempty"`
	SchemaVersion int64                       `json:"schema_version"`
}

type mailAttachmentSaveData struct {
	AccountID     string           `json:"account_id"`
	Attachment    publicAttachment `json:"attachment"`
	Saved         bool             `json:"saved"`
	SchemaVersion int64            `json:"schema_version"`
}

const maximumAttachmentSetSize = 2048

func newMailAttachmentsCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "attachments",
		Short:   "List and safely export decoded local attachments",
		Args:    cobra.NoArgs,
		Example: "  icloud mail attachments list msg_…\n  icloud mail attachments save att_… --output document.pdf\n  icloud mail attachments save-all msg_… --dir ./attachments",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	command.AddCommand(newMailAttachmentsListCommand(state))
	command.AddCommand(newMailAttachmentSaveCommand(state))
	command.AddCommand(newMailAttachmentsSaveAllCommand(state))
	return command
}

func newMailAttachmentsListCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var limit int
	var cursor string
	command := &cobra.Command{
		Use:     "list <message-or-thread-id>",
		Short:   "List bounded attachment metadata without printing bytes",
		Args:    cobra.ExactArgs(1),
		Example: "  icloud mail attachments list msg_…\n  icloud mail attachments list thr_… --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			accountValue, target, attachments, more, nextCursor, schemaVersion, err := state.mail.MailAttachmentsPage(
				cmd.Context(), state.global.Home, state.global.Account, args[0], cursor, limit,
			)
			if err != nil {
				return err
			}
			public := sanitizeAttachments(attachments)
			messageID, threadID := "", ""
			if target.Kind == "message" {
				messageID = target.ID
			} else if target.Kind == "thread" {
				threadID = target.ID
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailAttachmentsData{
					AccountID: accountValue.ID, Target: target, MessageID: messageID, ThreadID: threadID, Attachments: public,
					Count: len(public), More: more, NextCursor: nextCursor, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, attachment := range attachments {
				if err := renderAttachment(renderer, attachment); err != nil {
					return err
				}
			}
			return renderer.Summary("attachments",
				Field("target", target.ID),
				Field("target_kind", target.Kind),
				Field("count", len(attachments)),
				Field("more", fmt.Sprintf("%t", more)),
				Field("next_cursor", nextCursor),
			)
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum attachments to list (1-%d)", MaximumPageSize))
	command.Flags().StringVar(&cursor, "cursor", "", "Continue from a next_cursor returned for the same attachment target")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func newMailAttachmentSaveCommand(state *executionState) *cobra.Command {
	var output string
	var force, binary, jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "save <attachment-id>",
		Short:   "Verify and stream one decoded attachment",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail attachments save att_… --output ./document.pdf\n  icloud mail attachments save att_… --output - --binary",
		RunE: func(cmd *cobra.Command, args []string) error {
			attachmentID := ""
			if len(args) == 1 {
				attachmentID = args[0]
			}
			var jsonRequest attachmentSaveJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest, "output", "force", "binary"); err != nil {
				return err
			} else if used {
				attachmentID, output, force, binary = jsonRequest.AttachmentID, jsonRequest.Output, jsonRequest.Force, jsonRequest.Binary
			}
			if strings.TrimSpace(output) == "" {
				return NewAppError(ErrorUsage, "--output is required", false)
			}
			if output == "-" && !binary {
				return NewAppError(ErrorSafety, "binary stdout requires --binary", false)
			}
			if output == "-" && jsonOutput {
				return NewAppError(ErrorUsage, "--json cannot be combined with --output -", false)
			}
			accountValue, attachment, source, schemaVersion, err := state.mail.MailAttachment(
				cmd.Context(), state.global.Home, state.global.Account, attachmentID,
			)
			if err != nil {
				return err
			}
			if output == "-" {
				if err := source.Write(cmd.Context(), state.stdout); err != nil {
					return messageContentError(err)
				}
				return nil
			}
			if err := exportAttachment(cmd.Context(), source, output, force); err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailAttachmentSaveData{
					AccountID: accountValue.ID, Attachment: sanitizeAttachment(attachment),
					Saved: true, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("attachment-save", attachment.ID,
				Field("message", attachment.MessageID),
				Field("bytes", attachment.ByteSize),
			)
		},
	}
	command.Flags().StringVar(&output, "output", "", "Destination path, or - for stdout")
	command.Flags().BoolVar(&force, "force", false, "Replace an existing regular file")
	command.Flags().BoolVar(&binary, "binary", false, "Acknowledge binary bytes on stdout")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope after file export")
	bindJSONInput(command, &input)
	return command
}

func newMailAttachmentsSaveAllCommand(state *executionState) *cobra.Command {
	var directory string
	var force, jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "save-all <message-or-thread-id>",
		Short:   "Export a bounded attachment set with safe deterministic names",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail attachments save-all msg_… --dir ./attachments\n  icloud mail attachments save-all thr_… --dir ./thread-files",
		RunE: func(cmd *cobra.Command, args []string) error {
			targetID := ""
			if len(args) == 1 {
				targetID = args[0]
			}
			var jsonRequest attachmentsSaveAllJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest, "dir", "force"); err != nil {
				return err
			} else if used {
				if strings.TrimSpace(jsonRequest.TargetID) != "" && strings.TrimSpace(jsonRequest.MessageID) != "" && strings.TrimSpace(jsonRequest.TargetID) != strings.TrimSpace(jsonRequest.MessageID) {
					return NewAppError(ErrorUsage, "target_id and legacy message_id cannot select different attachment targets", false)
				}
				targetID = strings.TrimSpace(jsonRequest.TargetID)
				if targetID == "" {
					targetID = strings.TrimSpace(jsonRequest.MessageID)
				}
				directory, force = jsonRequest.Directory, jsonRequest.Force
			}
			if strings.TrimSpace(directory) == "" {
				return NewAppError(ErrorUsage, "--dir is required", false)
			}
			accountValue, target, attachments, more, schemaVersion, err := state.mail.MailAttachments(
				cmd.Context(), state.global.Home, state.global.Account, targetID, maximumAttachmentSetSize,
			)
			if err != nil {
				return err
			}
			if more {
				return NewAppError(ErrorSafety, "attachment set exceeds the supported save-all bound", false)
			}
			outputs, err := saveAllAttachments(cmd.Context(), state, attachments, directory, force)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: map[string]any{
					"account_id": accountValue.ID, "target": target,
					"attachments": sanitizeAttachments(attachments), "filenames": outputBasenames(outputs),
					"count": len(outputs), "schema_version": schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("attachments-saved", target.ID,
				Field("target_kind", target.Kind),
				Field("count", len(outputs)),
			)
		},
	}
	command.Flags().StringVar(&directory, "dir", "", "Destination directory")
	command.Flags().BoolVar(&force, "force", false, "Replace existing regular files")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func outputBasenames(paths []string) []string {
	result := make([]string, len(paths))
	for index, path := range paths {
		result[index] = filepath.Base(path)
	}
	return result
}

func renderAttachment(renderer *CompactTextRenderer, value maildomain.Attachment) error {
	return renderer.Record("attachment", value.ID,
		Field("message", value.MessageID),
		Field("part", value.PartID),
		Field("filename", Excerpt(value.Filename, DefaultExcerptRunes)),
		Field("type", Excerpt(value.MediaType, 120)),
		Field("bytes", value.ByteSize),
		Field("inline", fmt.Sprintf("%t", value.Inline)),
		Field("disposition", Excerpt(value.Disposition, 80)),
		Field("content_id", Excerpt(value.ContentID, DefaultExcerptRunes)),
		Field("availability", value.Availability),
		Field("error", Excerpt(value.ErrorCode, 120)),
	)
}

func sanitizeAttachments(values []maildomain.Attachment) []publicAttachment {
	result := make([]publicAttachment, 0, len(values))
	for _, value := range values {
		result = append(result, sanitizeAttachment(value))
	}
	return result
}

func sanitizeAttachment(value maildomain.Attachment) publicAttachment {
	return publicAttachment{
		ID: value.ID, MessageID: value.MessageID, PartID: value.PartID,
		Ordinal: value.Ordinal, PartPath: value.PartPath, Filename: value.Filename,
		MediaType: value.MediaType, MediaParams: value.MediaParams,
		Disposition: value.Disposition, ContentID: value.ContentID, Inline: value.Inline,
		ContentHash: value.ContentHash, ByteSize: value.ByteSize,
		Availability: value.Availability, ErrorCode: value.ErrorCode,
	}
}

func exportAttachment(ctx context.Context, source maildomain.AttachmentSource, output string, force bool) error {
	output = filepath.Clean(strings.TrimSpace(output))
	if output == "" || output == "." {
		return NewAppError(ErrorUsage, "invalid attachment destination", false)
	}
	if err := preflightOutput(output, force); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(output), ".icloud-attachment-*.tmp")
	if err != nil {
		return WrapAppError(ErrorConfiguration, "cannot create the attachment file", false, err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o600); err != nil {
		cleanup()
		return WrapAppError(ErrorConfiguration, "cannot secure the attachment file", false, err)
	}
	if err := source.Write(ctx, temporary); err != nil {
		cleanup()
		return messageContentError(err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return WrapAppError(ErrorInternal, "cannot flush the attachment file", true, err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return WrapAppError(ErrorInternal, "cannot close the attachment file", true, err)
	}
	if err := installExportFile(temporaryPath, output, force); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

func preflightOutput(output string, force bool) error {
	info, err := os.Lstat(output)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return NewAppError(ErrorSafety, "attachment destination is not a regular file", false)
		}
		if !force {
			return NewAppError(ErrorConflict, "attachment destination already exists; use --force to replace it", false)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return WrapAppError(ErrorConfiguration, "cannot inspect attachment destination", false, err)
	}
	return nil
}

type preparedAttachment struct {
	attachment maildomain.Attachment
	temporary  string
	target     string
	backup     string
}

type attachmentBatchFS struct {
	lstat      func(string) (os.FileInfo, error)
	createTemp func(string, string) (*os.File, error)
	rename     func(string, string) error
	link       func(string, string) error
	remove     func(string) error
}

var realAttachmentBatchFS = attachmentBatchFS{
	lstat:      os.Lstat,
	createTemp: os.CreateTemp,
	rename:     os.Rename,
	link:       os.Link,
	remove:     os.Remove,
}

func saveAllAttachments(ctx context.Context, state *executionState, attachments []maildomain.Attachment, directory string, force bool) ([]string, error) {
	directory = filepath.Clean(strings.TrimSpace(directory))
	if directory == "" {
		return nil, NewAppError(ErrorUsage, "invalid attachment directory", false)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, WrapAppError(ErrorConfiguration, "cannot create the attachment directory", false, err)
	}
	info, err := os.Lstat(directory)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, NewAppError(ErrorSafety, "attachment destination is not a real directory", false)
	}
	names := uniqueAttachmentNames(attachments)
	prepared := make([]preparedAttachment, 0, len(attachments))
	cleanup := func() {
		for _, item := range prepared {
			_ = os.Remove(item.temporary)
		}
	}
	for index, attachment := range attachments {
		target := filepath.Join(directory, names[index])
		if err := preflightOutput(target, force); err != nil {
			cleanup()
			return nil, err
		}
		_, _, source, _, err := state.mail.MailAttachment(ctx, state.global.Home, state.global.Account, attachment.ID)
		if err != nil {
			cleanup()
			return nil, err
		}
		temporary, err := os.CreateTemp(directory, ".icloud-attachment-batch-*.tmp")
		if err != nil {
			cleanup()
			return nil, WrapAppError(ErrorConfiguration, "cannot create a staged attachment file", false, err)
		}
		path := temporary.Name()
		if err := temporary.Chmod(0o600); err == nil {
			err = source.Write(ctx, temporary)
		}
		if err == nil {
			err = temporary.Sync()
		}
		closeErr := temporary.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(path)
			cleanup()
			return nil, messageContentError(err)
		}
		prepared = append(prepared, preparedAttachment{attachment: attachment, temporary: path, target: target})
	}
	if err := installAttachmentBatch(prepared, force); err != nil {
		cleanup()
		return nil, err
	}
	outputs := make([]string, len(prepared))
	for index, item := range prepared {
		outputs[index] = item.target
	}
	return outputs, nil
}

func installAttachmentBatch(items []preparedAttachment, force bool) error {
	return installAttachmentBatchWithFS(items, force, realAttachmentBatchFS)
}

func installAttachmentBatchWithFS(items []preparedAttachment, force bool, fs attachmentBatchFS) error {
	installed := make([]string, 0, len(items))
	rollback := func() error {
		var rollbackErr error
		for index := len(installed) - 1; index >= 0; index-- {
			if err := fs.remove(installed[index]); err != nil && !errors.Is(err, os.ErrNotExist) {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove installed attachment %q: %w", installed[index], err))
			}
		}
		for index := len(items) - 1; index >= 0; index-- {
			item := items[index]
			if item.backup != "" {
				if err := fs.rename(item.backup, item.target); err != nil {
					rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore attachment backup %q: %w", item.target, err))
				}
			}
		}
		return rollbackErr
	}
	if force {
		for index := range items {
			info, err := fs.lstat(items[index].target)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				rollbackErr := rollback()
				if rollbackErr != nil {
					return WrapAppError(ErrorInternal, "attachment replacement preflight failed and rollback was incomplete", false, errors.Join(err, rollbackErr))
				}
				return NewAppError(ErrorSafety, "attachment destination changed or is not a regular file", false)
			}
			backup, err := fs.createTemp(filepath.Dir(items[index].target), ".icloud-attachment-backup-*.tmp")
			if err != nil {
				rollbackErr := rollback()
				if rollbackErr != nil {
					err = errors.Join(err, rollbackErr)
				}
				return WrapAppError(ErrorConfiguration, "cannot prepare attachment replacement", false, err)
			}
			backupPath := backup.Name()
			_ = backup.Close()
			if err := fs.remove(backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				rollbackErr := rollback()
				if rollbackErr != nil {
					err = errors.Join(err, rollbackErr)
				}
				return WrapAppError(ErrorConfiguration, "cannot prepare attachment replacement", false, err)
			}
			if err := fs.rename(items[index].target, backupPath); err != nil {
				rollbackErr := rollback()
				if rollbackErr != nil {
					err = errors.Join(err, rollbackErr)
				}
				return WrapAppError(ErrorConfiguration, "cannot preserve an existing attachment file", false, err)
			}
			items[index].backup = backupPath
		}
	}
	for _, item := range items {
		var err error
		if force {
			err = fs.rename(item.temporary, item.target)
			if err == nil {
				// Rename makes the final path visible immediately. Record it before
				// any later filesystem step so rollback can never miss it.
				installed = append(installed, item.target)
			}
		} else {
			err = fs.link(item.temporary, item.target)
			if err == nil {
				// Link has already installed the final file. Bookkeep before
				// removing the staging name; a failed unlink must roll it back.
				installed = append(installed, item.target)
				err = fs.remove(item.temporary)
			}
		}
		if err != nil {
			rollbackErr := rollback()
			if rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
				return WrapAppError(ErrorInternal, "attachment batch failed and rollback was incomplete; inspect the destination before retrying", false, err)
			}
			return WrapAppError(ErrorConflict, "cannot install the complete attachment set; no final batch files were retained", false, err)
		}
	}
	var cleanupErr error
	for _, item := range items {
		if item.backup != "" {
			if err := fs.remove(item.backup); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove attachment backup %q: %w", item.backup, err))
			}
		}
	}
	if cleanupErr != nil {
		return WrapAppError(ErrorInternal, "complete attachment set was installed, but obsolete backup cleanup failed; exported final files are present", false, cleanupErr)
	}
	return nil
}

func uniqueAttachmentNames(values []maildomain.Attachment) []string {
	result := make([]string, len(values))
	used := map[string]struct{}{}
	for index, value := range values {
		base := safeAttachmentFilename(value)
		candidate := base
		for number := 2; ; number++ {
			if _, exists := used[strings.ToLower(candidate)]; !exists {
				break
			}
			candidate = numberedFilename(base, number)
		}
		used[strings.ToLower(candidate)] = struct{}{}
		result[index] = candidate
	}
	return result
}

func numberedFilename(name string, number int) string {
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	return fmt.Sprintf("%s-%d%s", stem, number, extension)
}

func safeAttachmentFilename(value maildomain.Attachment) string {
	name := strings.TrimSpace(value.Filename)
	name = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*':
			return '_'
		case r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			return '_'
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, strings.ToValidUTF8(name, ""))
	name = strings.Trim(name, ". ")
	if name == "" {
		name = "attachment-" + shortStableID(value.ID) + attachmentExtension(value.MediaType)
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if isReservedFilename(stem) {
		name = "_" + name
	}
	extension := filepath.Ext(name)
	stem = strings.TrimSuffix(name, extension)
	for len([]byte(stem+extension)) > 180 && len([]rune(stem)) > 1 {
		runes := []rune(stem)
		stem = string(runes[:len(runes)-1])
	}
	name = stem + extension
	for len([]byte(name)) > 180 && len([]rune(name)) > 1 {
		runes := []rune(name)
		name = string(runes[:len(runes)-1])
	}
	return name
}

func shortStableID(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.LastIndex(value, "-"); index >= 0 && index+1 < len(value) {
		value = value[index+1:]
	}
	if utf8.RuneCountInString(value) > 12 {
		value = string([]rune(value)[:12])
	}
	if value == "" {
		return "unnamed"
	}
	return value
}

func attachmentExtension(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "application/pdf":
		return ".pdf"
	case "message/rfc822":
		return ".eml"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "text/plain":
		return ".txt"
	case "text/html":
		return ".html"
	default:
		return ".bin"
	}
}

func isReservedFilename(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	reserved := []string{"CON", "PRN", "AUX", "NUL", "CLOCK$"}
	for index := 1; index <= 9; index++ {
		reserved = append(reserved, fmt.Sprintf("COM%d", index), fmt.Sprintf("LPT%d", index))
	}
	sort.Strings(reserved)
	position := sort.SearchStrings(reserved, value)
	return position < len(reserved) && reserved[position] == value
}

var _ io.Writer = (*boundedContentBuffer)(nil)
