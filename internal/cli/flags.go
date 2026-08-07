package cli

import (
	"fmt"
	"strings"
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

type mailFlagBulkData struct {
	Mode          string                 `json:"mode"`
	Results       []mailFlagMutationData `json:"results"`
	Requested     int                    `json:"requested"`
	Attempted     int                    `json:"attempted"`
	Count         int                    `json:"count"`
	Failed        int                    `json:"failed"`
	Skipped       int                    `json:"skipped"`
	Partial       bool                   `json:"partial"`
	SchemaVersion int64                  `json:"schema_version,omitempty"`
}

type flagMutationItem struct {
	MessageID string
	Folder    string
}

func newMailFlagMutationCommand(state *executionState, name, short string, mutation maildomain.FlagMutation) *cobra.Command {
	var folderSelector string
	var jsonOutput, jsonlOutput bool
	var input jsonInputOptions
	var inputJSONL string
	var mode string
	command := &cobra.Command{
		Use:   name + " <message-id> [message-id...]",
		Short: short,
		Args: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(input.Path) != "" || strings.TrimSpace(inputJSONL) != "" {
				if len(args) != 0 {
					return NewAppError(ErrorUsage, "positional write arguments cannot be combined with JSON/JSONL input", false)
				}
				return nil
			}
			if len(args) < 1 || len(args) > MaximumBulkItems {
				return NewAppError(ErrorUsage, fmt.Sprintf("requires between 1 and %d message IDs", MaximumBulkItems), false)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, JSONL: jsonlOutput}, OutputSupport{JSON: true, JSONL: true}); err != nil {
				return err
			}
			mode = strings.ToLower(strings.TrimSpace(mode))
			if mode != "atomic" && mode != "continue-on-error" {
				return NewAppError(ErrorUsage, "--mode must be atomic or continue-on-error", false)
			}
			if strings.TrimSpace(input.Path) != "" && strings.TrimSpace(inputJSONL) != "" {
				return NewAppError(ErrorUsage, "--input-json and --input-jsonl cannot be combined", false)
			}
			items := make([]flagMutationItem, 0, max(1, len(args)))
			if strings.TrimSpace(inputJSONL) != "" {
				if cmd.Flags().Changed("folder") {
					return NewAppError(ErrorUsage, "--folder cannot be combined with --input-jsonl; put folder in each JSONL item", false)
				}
				if err := readJSONLines(state, inputJSONL, func(line int, value []byte) error {
					var item flagBulkJSONLItem
					if err := decodeStrictJSONLine(line, value, &item); err != nil {
						return err
					}
					items = append(items, flagMutationItem{MessageID: item.MessageID, Folder: item.Folder})
					return nil
				}); err != nil {
					return err
				}
			} else {
				var request flagBulkJSONRequest
				if used, err := readJSONInput(cmd, state, input, &request, "folder"); err != nil {
					return err
				} else if used {
					for _, messageID := range request.MessageIDs {
						items = append(items, flagMutationItem{MessageID: messageID, Folder: request.Folder})
					}
				} else {
					for _, messageID := range args {
						items = append(items, flagMutationItem{MessageID: messageID, Folder: folderSelector})
					}
				}
			}
			if err := validateFlagMutationItems(items); err != nil {
				return err
			}
			bulkInvocation := len(items) > 1 || strings.TrimSpace(inputJSONL) != ""

			results := make([]mailFlagMutationData, 0, len(items))
			failed := 0
			var firstFailure *AppError
			var accountID string
			var schemaVersion int64
			sequence := uint64(0)
			renderer := NewCompactTextRenderer(state.stdout)
			for index, item := range items {
				accountValue, folderValue, message, location, version, err := state.mail.MailUpdateFlag(
					cmd.Context(), state.global.Home, state.global.Account, item.MessageID, item.Folder, mutation,
				)
				sequence++
				if err != nil {
					failed++
					normalized := NormalizeError(err)
					if firstFailure == nil {
						firstFailure = normalized
					}
					if jsonlOutput {
						if writeErr := WriteJSONLEvent(state.stdout, JSONLEvent{Event: "item", Sequence: sequence, AccountID: accountID, Timestamp: time.Now().UTC(), Payload: map[string]any{"index": index, "message_id": item.MessageID, "action": mutation, "status": "error", "mode": mode}, Error: normalized}); writeErr != nil {
							return writeErr
						}
					} else if !jsonOutput && bulkInvocation {
						if writeErr := renderer.Record("mail-state-error", item.MessageID, Field("action", mutation), Field("code", normalized.Code), Field("retryable", normalized.Retryable)); writeErr != nil {
							return writeErr
						}
					}
					if mode == "atomic" {
						break
					}
					continue
				}
				accountID, schemaVersion = accountValue.ID, version
				data := mailFlagMutationData{
					Outcome: "updated", Action: mutation, AccountID: accountValue.ID,
					MessageID: message.ID, LocationID: location.ID, FolderID: folderValue.ID,
					UIDValidity: location.UIDValidity, UID: location.UID, Flags: location.Flags,
					ModSequence: location.ModSequence, FlagsCheckedAt: location.FlagsCheckedAt,
					Unread: message.Unread, Flagged: message.Flagged,
					SchemaVersion: schemaVersion,
				}
				results = append(results, data)
				if jsonlOutput {
					if err := WriteJSONLEvent(state.stdout, JSONLEvent{Event: "item", Sequence: sequence, AccountID: accountID, Timestamp: time.Now().UTC(), Payload: map[string]any{"index": index, "status": "updated", "mode": mode, "result": data}}); err != nil {
						return err
					}
				} else if !jsonOutput {
					if err := renderFlagMutation(renderer, data); err != nil {
						return err
					}
				}
			}

			attempted := len(results) + failed
			skipped := len(items) - attempted
			partial := failed > 0 && len(results) > 0
			if jsonlOutput {
				sequence++
				if err := WriteJSONLEvent(state.stdout, JSONLEvent{Event: "completed", Sequence: sequence, AccountID: accountID, Timestamp: time.Now().UTC(), Payload: map[string]any{"mode": mode, "requested": len(items), "attempted": attempted, "count": len(results), "failed": failed, "skipped": skipped, "partial": partial}}); err != nil {
					return err
				}
			} else if jsonOutput {
				if len(items) == 1 && failed == 0 {
					return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: results[0], Meta: EnvelopeMeta{RequestID: state.requestID}})
				}
				if failed == 0 {
					return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailFlagBulkData{Mode: mode, Results: results, Requested: len(items), Attempted: attempted, Count: len(results), Failed: failed, Skipped: skipped, Partial: partial, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
				}
			} else if bulkInvocation {
				if err := renderer.Summary("mail-state-bulk", Field("mode", mode), Field("requested", len(items)), Field("attempted", attempted), Field("updated", len(results)), Field("failed", failed), Field("skipped", skipped), Field("partial", partial)); err != nil {
					return err
				}
			}
			if failed > 0 {
				if !bulkInvocation && !jsonlOutput {
					return firstFailure
				}
				return bulkMutationFailure(firstFailure, mode, len(items), len(results), failed, partial)
			}
			return nil
		},
	}
	command.Flags().StringVar(&folderSelector, "folder", "", "Select one exact folder ID, name, wire name, or kind for every positional/JSON item")
	command.Flags().StringVar(&inputJSONL, "input-jsonl", "", "Read up to 100 strict JSONL items with message_id and optional folder from a file, or - for stdin")
	command.Flags().StringVar(&mode, "mode", "atomic", "Bulk failure mode: validate the bounded input then fail fast on target/provider errors, or continue-on-error")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&jsonlOutput, "jsonl", false, "Emit typed per-item and completed JSONL events")
	bindJSONInput(command, &input)
	return command
}

func validateFlagMutationItems(items []flagMutationItem) error {
	if len(items) < 1 {
		return NewAppError(ErrorUsage, "bulk state request contains no message IDs", false)
	}
	if len(items) > MaximumBulkItems {
		return NewAppError(ErrorSafety, fmt.Sprintf("bulk state request exceeds %d items", MaximumBulkItems), false)
	}
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		messageID := strings.TrimSpace(item.MessageID)
		if !strings.HasPrefix(messageID, "msg_") || len(messageID) <= len("msg_") {
			return NewAppError(ErrorUsage, fmt.Sprintf("bulk item %d requires a stable msg_… message_id", index), false)
		}
		key := messageID + "\x00" + strings.TrimSpace(item.Folder)
		if _, ok := seen[key]; ok {
			return NewAppError(ErrorUsage, fmt.Sprintf("bulk item %d duplicates message_id/folder", index), false)
		}
		seen[key] = struct{}{}
		items[index].MessageID = messageID
		items[index].Folder = strings.TrimSpace(item.Folder)
	}
	return nil
}

func renderFlagMutation(renderer *CompactTextRenderer, data mailFlagMutationData) error {
	return renderer.Record("mail-state", data.MessageID,
		Field("action", data.Action),
		Field("location", data.LocationID),
		Field("folder", data.FolderID),
		Field("unread", fmt.Sprintf("%t", data.Unread)),
		Field("flagged", fmt.Sprintf("%t", data.Flagged)),
	)
}

func bulkMutationFailure(first *AppError, mode string, requested, succeeded, failed int, partial bool) *AppError {
	if first == nil {
		first = NewAppError(ErrorInternal, "bulk state operation failed", false)
	}
	result := *first
	attempted := succeeded + failed
	result.Details = map[string]any{
		"mode": mode, "requested": requested, "attempted": attempted, "succeeded": succeeded, "failed": failed, "skipped": requested - attempted, "partial": partial,
	}
	if partial {
		result.Message = fmt.Sprintf("bulk %s state operation partially succeeded: %d updated, %d failed", mode, succeeded, failed)
	}
	return &result
}
