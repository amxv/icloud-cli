package cli

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

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
	JSONL          bool
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

type mailLocationBulkData struct {
	Mode               string                     `json:"mode"`
	Results            []mailLocationMutationData `json:"results"`
	Requested          int                        `json:"requested"`
	Attempted          int                        `json:"attempted"`
	Completed          int                        `json:"completed"`
	Failed             int                        `json:"failed"`
	Skipped            int                        `json:"skipped"`
	Partial            bool                       `json:"partial"`
	SchemaVersion      int64                      `json:"schema_version,omitempty"`
	ThreadID           string                     `json:"thread_id,omitempty"`
	RedirectedFrom     string                     `json:"redirected_from,omitempty"`
	ThreadMessageCount int                        `json:"thread_message_count,omitempty"`
	More               bool                       `json:"more,omitempty"`
	NextCursor         string                     `json:"next_cursor,omitempty"`
}

type mailLocationBulkPreviewData struct {
	Mode               string                            `json:"mode"`
	Plans              []maildomain.LocationMutationPlan `json:"plans"`
	Requested          int                               `json:"requested"`
	Count              int                               `json:"count"`
	SchemaVersion      int64                             `json:"schema_version"`
	ThreadID           string                            `json:"thread_id,omitempty"`
	RedirectedFrom     string                            `json:"redirected_from,omitempty"`
	ThreadMessageCount int                               `json:"thread_message_count,omitempty"`
	More               bool                              `json:"more,omitempty"`
	NextCursor         string                            `json:"next_cursor,omitempty"`
}

type locationMutationItem struct {
	MessageID      string
	From           string
	To             string
	IdempotencyKey string
}

func newMailLocationMutationCommand(state *executionState, action maildomain.LocationMutationAction) *cobra.Command {
	var flags locationMutationFlags
	var input jsonInputOptions
	var inputJSONL string
	var mode string
	name, short, example := locationMutationCommandMetadata(action)
	command := &cobra.Command{
		Use:     name + " <message-id> [message-id...]",
		Short:   short,
		Example: example,
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
			if err := ValidateOutputOptions(OutputOptions{JSON: flags.JSON, JSONL: flags.JSONL}, OutputSupport{JSON: true, JSONL: true}); err != nil {
				return err
			}
			if flags.DryRun && flags.Yes {
				return NewAppError(ErrorUsage, "--dry-run and --yes cannot be combined", false)
			}
			mode = strings.ToLower(strings.TrimSpace(mode))
			if mode != "atomic" && mode != "continue-on-error" {
				return NewAppError(ErrorUsage, "--mode must be atomic or continue-on-error", false)
			}
			if strings.TrimSpace(input.Path) != "" && strings.TrimSpace(inputJSONL) != "" {
				return NewAppError(ErrorUsage, "--input-json and --input-jsonl cannot be combined", false)
			}

			items := make([]locationMutationItem, 0, max(1, len(args)))
			if strings.TrimSpace(inputJSONL) != "" {
				if err := readJSONLines(state, inputJSONL, func(line int, value []byte) error {
					var item locationJSONItem
					if err := decodeStrictJSONLine(line, value, &item); err != nil {
						return err
					}
					items = append(items, locationMutationItem{MessageID: item.MessageID, From: item.From, To: item.To, IdempotencyKey: item.IdempotencyKey})
					return nil
				}); err != nil {
					return err
				}
				items = applyLocationMutationDefaults(items, flags.From, flags.To, flags.IdempotencyKey)
			} else {
				var jsonRequest locationJSONRequest
				if used, err := readJSONInput(cmd, state, input, &jsonRequest, "from", "to", "idempotency-key"); err != nil {
					return err
				} else if used {
					items, err = locationMutationItemsFromJSON(jsonRequest)
					if err != nil {
						return err
					}
				} else {
					for _, messageID := range args {
						items = append(items, locationMutationItem{MessageID: messageID})
					}
					items = applyLocationMutationDefaults(items, flags.From, flags.To, flags.IdempotencyKey)
				}
			}
			if err := validateLocationMutationItems(action, items); err != nil {
				return err
			}
			return executeLocationMutationItems(cmd, state, action, items, flags, mode, len(items) > 1 || strings.TrimSpace(inputJSONL) != "", nil)
		},
	}
	command.Flags().StringVar(&flags.From, "from", "", "Select one exact current source folder by ID, name, wire name, or kind")
	command.Flags().StringVar(&flags.To, "to", "", "Select one current destination folder by ID, name, wire name, or kind")
	command.Flags().StringVar(&flags.IdempotencyKey, "idempotency-key", "", idempotencyKeyHelp)
	command.Flags().StringVar(&inputJSONL, "input-jsonl", "", "Read up to 100 strict JSONL location items from a file, or - for stdin")
	command.Flags().StringVar(&mode, "mode", "atomic", "Bulk failure mode: validate the bounded target set then fail fast, or continue-on-error")
	command.Flags().BoolVar(&flags.DryRun, "dry-run", false, "Preview the exact location change without creating intent or contacting IMAP")
	command.Flags().BoolVar(&flags.Yes, "yes", false, "Confirm an intentional non-interactive location change")
	command.Flags().BoolVar(&flags.JSON, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&flags.JSONL, "jsonl", false, "Emit typed per-item and completed JSONL events")
	bindJSONInput(command, &input)
	return command
}

func executeLocationMutationItems(cmd *cobra.Command, state *executionState, action maildomain.LocationMutationAction, items []locationMutationItem, flags locationMutationFlags, mode string, bulkInvocation bool, threadMeta *threadMutationMeta) error {
	accountID, plans, schemaVersion, err := validateLocationMutationPlans(cmd, state, action, items)
	if err != nil {
		normalized := NormalizeError(err)
		addThreadMutationErrorDetails(normalized, threadMeta)
		return normalized
	}
	if flags.DryRun {
		return renderLocationMutationPreviews(state, accountID, plans, schemaVersion, mode, flags.JSON, flags.JSONL, bulkInvocation, threadMeta)
	}
	interactive := isTerminalReader(state.stdin) && !state.global.NoInput
	if !flags.Yes && !interactive {
		return NewAppError(ErrorSafety, "non-interactive mail location mutation requires --yes", false)
	}
	if !flags.Yes {
		if err := renderInteractiveLocationMutationReview(state.stdout, plans, bulkInvocation, threadMeta); err != nil {
			return err
		}
		confirmed, err := confirmMailLocationMutations(state.stdin, state.stderr, action, len(items))
		if err != nil {
			return err
		}
		if !confirmed {
			return NewAppError(ErrorSafety, "mail location mutation canceled before contacting IMAP", false)
		}
	}

	results := make([]mailLocationMutationData, 0, len(items))
	failed := 0
	var firstFailure *AppError
	sequence := uint64(0)
	renderer := NewCompactTextRenderer(state.stdout)
	for index, item := range items {
		request := locationMutationRequest(action, item)
		accountValue, _, message, mutation, reused, version, itemErr := state.mail.MailMutateLocation(
			cmd.Context(), state.global.Home, state.global.Account, request,
		)
		sequence++
		if itemErr != nil {
			failed++
			normalized := NormalizeError(itemErr)
			if firstFailure == nil {
				firstFailure = normalized
			}
			if flags.JSONL {
				payload := map[string]any{"index": index, "message_id": item.MessageID, "action": action, "status": "error", "mode": mode}
				if mutation.ID != "" {
					payload["mutation"] = mutation
				}
				addThreadMutationPayload(payload, threadMeta)
				if writeErr := WriteJSONLEvent(state.stdout, JSONLEvent{Event: "item", Sequence: sequence, AccountID: accountID, Timestamp: time.Now().UTC(), Payload: payload, Error: normalized}); writeErr != nil {
					return writeErr
				}
			} else if !flags.JSON && bulkInvocation {
				if writeErr := renderer.Record("mail-location-error", item.MessageID, Field("action", action), Field("code", normalized.Code), Field("retryable", normalized.Retryable)); writeErr != nil {
					return writeErr
				}
			}
			if !bulkInvocation && !flags.JSONL {
				return itemErr
			}
			if mode == "atomic" {
				break
			}
			continue
		}
		accountID, schemaVersion = accountValue.ID, version
		data := mailLocationMutationData{
			AccountID: accountValue.ID, MessageID: message.ID,
			Mutation: mutation, Reused: reused,
			Unread: message.Unread, Flagged: message.Flagged,
			SchemaVersion: version,
		}
		results = append(results, data)
		if flags.JSONL {
			payload := map[string]any{"index": index, "message_id": item.MessageID, "status": "completed", "mode": mode, "result": data}
			addThreadMutationPayload(payload, threadMeta)
			if writeErr := WriteJSONLEvent(state.stdout, JSONLEvent{Event: "item", Sequence: sequence, AccountID: accountID, Timestamp: time.Now().UTC(), Payload: payload}); writeErr != nil {
				return writeErr
			}
		} else if !flags.JSON {
			if bulkInvocation {
				if writeErr := renderLocationMutationBulkItem(renderer, data); writeErr != nil {
					return writeErr
				}
			} else if writeErr := renderLocationMutationOutcome(renderer, mutation, reused, message.Unread, message.Flagged); writeErr != nil {
				return writeErr
			}
		}
	}

	attempted := len(results) + failed
	skipped := len(items) - attempted
	partial := failed > 0 && len(results) > 0
	if flags.JSONL {
		sequence++
		payload := map[string]any{"mode": mode, "requested": len(items), "attempted": attempted, "completed": len(results), "failed": failed, "skipped": skipped, "partial": partial}
		addThreadMutationPayload(payload, threadMeta)
		if writeErr := WriteJSONLEvent(state.stdout, JSONLEvent{Event: "completed", Sequence: sequence, AccountID: accountID, Timestamp: time.Now().UTC(), Payload: payload}); writeErr != nil {
			return writeErr
		}
	} else if flags.JSON {
		if len(items) == 1 && failed == 0 && threadMeta == nil {
			return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: results[0], Meta: EnvelopeMeta{RequestID: state.requestID}})
		}
		if failed == 0 {
			data := mailLocationBulkData{Mode: mode, Results: results, Requested: len(items), Attempted: attempted, Completed: len(results), Failed: failed, Skipped: skipped, Partial: partial, SchemaVersion: schemaVersion}
			applyThreadMutationLocationData(&data, threadMeta)
			return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
		}
	} else if bulkInvocation {
		fields := []CompactField{Field("mode", mode), Field("requested", len(items)), Field("attempted", attempted), Field("completed", len(results)), Field("failed", failed), Field("skipped", skipped), Field("partial", partial)}
		fields = appendThreadMutationCompactFields(fields, threadMeta)
		if writeErr := renderer.Summary("mail-location-bulk", fields...); writeErr != nil {
			return writeErr
		}
	}
	if failed > 0 {
		err := locationBulkMutationFailure(firstFailure, mode, len(items), len(results), failed, partial)
		addThreadMutationErrorDetails(err, threadMeta)
		return err
	}
	return nil
}

func applyThreadMutationLocationData(data *mailLocationBulkData, meta *threadMutationMeta) {
	if data == nil || meta == nil {
		return
	}
	data.ThreadID = meta.ThreadID
	data.RedirectedFrom = meta.RedirectedFrom
	data.ThreadMessageCount = meta.MessageCount
	data.More = meta.More
	data.NextCursor = meta.NextCursor
}

func applyThreadMutationLocationPreviewData(data *mailLocationBulkPreviewData, meta *threadMutationMeta) {
	if data == nil || meta == nil {
		return
	}
	data.ThreadID = meta.ThreadID
	data.RedirectedFrom = meta.RedirectedFrom
	data.ThreadMessageCount = meta.MessageCount
	data.More = meta.More
	data.NextCursor = meta.NextCursor
}

func locationMutationItemsFromJSON(request locationJSONRequest) ([]locationMutationItem, error) {
	forms := 0
	if strings.TrimSpace(request.MessageID) != "" {
		forms++
	}
	if len(request.MessageIDs) > 0 {
		forms++
	}
	if len(request.Items) > 0 {
		forms++
	}
	if forms != 1 {
		return nil, NewAppError(ErrorUsage, "location JSON input requires exactly one of message_id, message_ids, or items", false)
	}
	items := make([]locationMutationItem, 0, max(1, len(request.MessageIDs)+len(request.Items)))
	switch {
	case strings.TrimSpace(request.MessageID) != "":
		items = append(items, locationMutationItem{MessageID: request.MessageID})
	case len(request.MessageIDs) > 0:
		for _, messageID := range request.MessageIDs {
			items = append(items, locationMutationItem{MessageID: messageID})
		}
	default:
		for _, item := range request.Items {
			items = append(items, locationMutationItem{MessageID: item.MessageID, From: item.From, To: item.To, IdempotencyKey: item.IdempotencyKey})
		}
	}
	return applyLocationMutationDefaults(items, request.From, request.To, request.IdempotencyKey), nil
}

func applyLocationMutationDefaults(items []locationMutationItem, from, to, idempotencyKey string) []locationMutationItem {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	for index := range items {
		if strings.TrimSpace(items[index].From) == "" {
			items[index].From = from
		}
		if strings.TrimSpace(items[index].To) == "" {
			items[index].To = to
		}
		if strings.TrimSpace(items[index].IdempotencyKey) == "" && idempotencyKey != "" {
			if len(items) == 1 {
				items[index].IdempotencyKey = idempotencyKey
			} else {
				items[index].IdempotencyKey = derivedLocationBulkIdempotencyKey(idempotencyKey, index)
			}
		}
	}
	return items
}

func derivedLocationBulkIdempotencyKey(base string, index int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", strings.TrimSpace(base), index)))
	return "bulk:" + hex.EncodeToString(sum[:])
}

func validateLocationMutationItems(action maildomain.LocationMutationAction, items []locationMutationItem) error {
	if len(items) < 1 {
		return NewAppError(ErrorUsage, "bulk location request contains no message IDs", false)
	}
	if len(items) > MaximumBulkItems {
		return NewAppError(ErrorSafety, fmt.Sprintf("bulk location request exceeds %d items", MaximumBulkItems), false)
	}
	seenTargets := make(map[string]struct{}, len(items))
	seenKeys := make(map[string]struct{}, len(items))
	for index := range items {
		items[index].MessageID = strings.TrimSpace(items[index].MessageID)
		items[index].From = strings.TrimSpace(items[index].From)
		items[index].To = strings.TrimSpace(items[index].To)
		items[index].IdempotencyKey = strings.TrimSpace(items[index].IdempotencyKey)
		if !strings.HasPrefix(items[index].MessageID, "msg_") || len(items[index].MessageID) <= len("msg_") {
			return NewAppError(ErrorUsage, fmt.Sprintf("bulk item %d requires a stable msg_… message_id", index), false)
		}
		if len(items[index].IdempotencyKey) > 256 {
			return NewAppError(ErrorUsage, fmt.Sprintf("bulk item %d idempotency key is too long", index), false)
		}
		key := items[index].MessageID + "\x00" + items[index].From
		if action == maildomain.LocationActionMove || action == maildomain.LocationActionCopy || action == maildomain.LocationActionRestore {
			key += "\x00" + items[index].To
		}
		if _, ok := seenTargets[key]; ok {
			return NewAppError(ErrorUsage, fmt.Sprintf("bulk item %d duplicates an earlier exact location target", index), false)
		}
		seenTargets[key] = struct{}{}
		if items[index].IdempotencyKey != "" {
			if _, ok := seenKeys[items[index].IdempotencyKey]; ok {
				return NewAppError(ErrorUsage, fmt.Sprintf("bulk item %d duplicates an earlier idempotency key", index), false)
			}
			seenKeys[items[index].IdempotencyKey] = struct{}{}
		}
	}
	return nil
}

func locationMutationRequest(action maildomain.LocationMutationAction, item locationMutationItem) maildomain.LocationMutationRequest {
	return maildomain.LocationMutationRequest{
		Action: action, MessageID: item.MessageID,
		FromFolder: item.From, ToFolder: item.To, IdempotencyKey: item.IdempotencyKey,
	}
}

func validateLocationMutationPlans(cmd *cobra.Command, state *executionState, action maildomain.LocationMutationAction, items []locationMutationItem) (string, []maildomain.LocationMutationPlan, int64, error) {
	plans := make([]maildomain.LocationMutationPlan, 0, len(items))
	accountID := ""
	var schemaVersion int64
	for index, item := range items {
		accountValue, plan, version, err := state.mail.MailLocationPreview(cmd.Context(), state.global.Home, state.global.Account, locationMutationRequest(action, item))
		if err != nil {
			if len(items) == 1 {
				return "", nil, version, err
			}
			normalized := NormalizeError(err)
			copy := *normalized
			copy.Message = fmt.Sprintf("bulk location validation failed at item %d before provider contact: %s", index, normalized.Message)
			copy.Details = map[string]any{"index": index, "message_id": item.MessageID, "phase": "validation", "provider_contacted": false}
			return "", nil, version, &copy
		}
		if accountID == "" {
			accountID = accountValue.ID
		} else if accountValue.ID != accountID {
			return "", nil, version, NewAppError(ErrorConflict, "bulk location targets resolved across different accounts", false)
		}
		schemaVersion = version
		plans = append(plans, plan)
	}
	return accountID, plans, schemaVersion, nil
}

func renderLocationMutationPreviews(state *executionState, accountID string, plans []maildomain.LocationMutationPlan, schemaVersion int64, mode string, jsonOutput, jsonlOutput, bulkInvocation bool, threadMeta *threadMutationMeta) error {
	if len(plans) == 1 && !jsonlOutput && !bulkInvocation {
		return renderLocationMutationPreview(state, plans[0], schemaVersion, jsonOutput)
	}
	if jsonlOutput {
		sequence := uint64(0)
		for index, plan := range plans {
			sequence++
			payload := map[string]any{"index": index, "message_id": plan.MessageID, "status": "preview", "mode": mode, "plan": plan}
			addThreadMutationPayload(payload, threadMeta)
			if err := WriteJSONLEvent(state.stdout, JSONLEvent{Event: "item", Sequence: sequence, AccountID: accountID, Timestamp: time.Now().UTC(), Payload: payload}); err != nil {
				return err
			}
		}
		sequence++
		payload := map[string]any{"mode": mode, "requested": len(plans), "reviewed": len(plans), "imap_contacted": false, "intent_created": false}
		addThreadMutationPayload(payload, threadMeta)
		return WriteJSONLEvent(state.stdout, JSONLEvent{Event: "completed", Sequence: sequence, AccountID: accountID, Timestamp: time.Now().UTC(), Payload: payload})
	}
	if jsonOutput {
		data := mailLocationBulkPreviewData{Mode: mode, Plans: plans, Requested: len(plans), Count: len(plans), SchemaVersion: schemaVersion}
		applyThreadMutationLocationPreviewData(&data, threadMeta)
		return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
	}
	return renderCompactLocationMutationPreviews(state.stdout, plans, threadMeta)
}

func renderCompactLocationMutationPreviews(writer io.Writer, plans []maildomain.LocationMutationPlan, threadMeta *threadMutationMeta) error {
	renderer := NewCompactTextRenderer(writer)
	for _, plan := range plans {
		if err := renderer.Record("mail-location-preview", plan.MessageID,
			Field("action", plan.Action),
			Field("operation", plan.Operation),
			Field("source_folder", plan.SourceFolderID),
			Field("destination_folder", plan.DestinationFolderID),
			Field("request_hash", plan.RequestHash),
		); err != nil {
			return err
		}
	}
	fields := []CompactField{
		Field("dry_run", "true"),
		Field("requested", len(plans)),
		Field("imap_contacted", "false"),
		Field("intent_created", "false"),
		Field("logical_message_deleted", "false"),
		Field("next", "review every exact source and destination, then rerun without --dry-run and use --yes when non-interactive"),
	}
	fields = appendThreadMutationCompactFields(fields, threadMeta)
	return renderer.Summary("mail-location-safety", fields...)
}

func renderInteractiveLocationMutationReview(writer io.Writer, plans []maildomain.LocationMutationPlan, bulkInvocation bool, threadMeta *threadMutationMeta) error {
	if len(plans) == 1 && !bulkInvocation {
		return renderCompactLocationMutationPreview(writer, plans[0])
	}
	renderer := NewCompactTextRenderer(writer)
	for _, plan := range plans {
		if err := renderer.Record("mail-location-preview", plan.MessageID,
			Field("action", plan.Action),
			Field("operation", plan.Operation),
			Field("source_folder", plan.SourceFolderID),
			Field("destination_folder", plan.DestinationFolderID),
			Field("request_hash", plan.RequestHash),
		); err != nil {
			return err
		}
	}
	fields := []CompactField{
		Field("review", "true"),
		Field("requested", len(plans)),
		Field("imap_contacted", "false"),
		Field("intent_created", "false"),
		Field("logical_message_deleted", "false"),
		Field("next", "confirm the reviewed target set or answer no to cancel before provider contact"),
	}
	fields = appendThreadMutationCompactFields(fields, threadMeta)
	return renderer.Summary("mail-location-safety", fields...)
}

func renderLocationMutationBulkItem(renderer *CompactTextRenderer, data mailLocationMutationData) error {
	return renderer.Record("mail-location-item", data.MessageID,
		Field("action", data.Mutation.Action),
		Field("mutation_id", data.Mutation.ID),
		Field("status", data.Mutation.Status),
		Field("provider_method", data.Mutation.ProviderMethod),
		Field("reused", data.Reused),
	)
}

func locationBulkMutationFailure(first *AppError, mode string, requested, completed, failed int, partial bool) *AppError {
	if first == nil {
		first = NewAppError(ErrorInternal, "bulk location operation failed", false)
	}
	result := *first
	attempted := completed + failed
	result.Details = map[string]any{
		"mode": mode, "requested": requested, "attempted": attempted, "completed": completed,
		"failed": failed, "skipped": requested - attempted, "partial": partial,
	}
	if partial {
		result.Message = fmt.Sprintf("bulk %s location operation partially succeeded: %d completed, %d failed; prior provider writes remain committed", mode, completed, failed)
	}
	return &result
}

func confirmMailLocationMutations(reader io.Reader, stderr io.Writer, action maildomain.LocationMutationAction, count int) (bool, error) {
	if count == 1 {
		return confirmMailLocationMutation(reader, stderr, action)
	}
	if _, err := fmt.Fprintf(stderr, "Apply this %s location change to %d reviewed targets? [y/N]: ", action, count); err != nil {
		return false, WrapAppError(ErrorInternal, "cannot write the location mutation confirmation prompt", false, err)
	}
	line, err := bufio.NewReader(io.LimitReader(reader, 64)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, WrapAppError(ErrorConfiguration, "cannot read location mutation confirmation", false, err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func locationMutationCommandMetadata(action maildomain.LocationMutationAction) (string, string, string) {
	switch action {
	case maildomain.LocationActionMove:
		return "move", "Move exact current message locations", "  icloud mail move msg_… --from inbox --to Projects --dry-run\n  icloud mail move msg_… --from inbox --to Projects --idempotency-key move-01 --yes\n  icloud mail move msg_… msg_… --from inbox --to Projects --idempotency-key move-batch-01 --yes --jsonl"
	case maildomain.LocationActionCopy:
		return "copy", "Copy exact current message locations", "  icloud mail copy msg_… --from inbox --to Projects --dry-run\n  icloud mail copy msg_… --from inbox --to Projects --idempotency-key copy-01 --yes"
	case maildomain.LocationActionArchive:
		return "archive", "Move eligible non-Sent locations to the semantic Archive folder", "  icloud mail archive msg_… --dry-run\n  icloud mail archive msg_… --from inbox --idempotency-key archive-01 --yes"
	case maildomain.LocationActionUnarchive:
		return "unarchive", "Move current Archive locations to Inbox", "  icloud mail unarchive msg_… --dry-run\n  icloud mail unarchive msg_… --idempotency-key unarchive-01 --yes"
	case maildomain.LocationActionTrash:
		return "trash", "Move exact current locations to the semantic Trash folder", "  icloud mail trash msg_… --dry-run\n  icloud mail trash msg_… --from inbox --idempotency-key trash-01 --yes"
	case maildomain.LocationActionRestore:
		return "restore", "Move current Trash locations to deliberate destinations", "  icloud mail restore msg_… --to inbox --dry-run\n  icloud mail restore msg_… --to inbox --idempotency-key restore-01 --yes"
	case maildomain.LocationActionJunk:
		return "junk", "Move exact current message locations to the semantic Junk folder", "  icloud mail junk msg_… --dry-run\n  icloud mail junk msg_… --from inbox --idempotency-key junk-01 --yes"
	case maildomain.LocationActionNotJunk:
		return "not-junk", "Move current Junk locations back to Inbox", "  icloud mail not-junk msg_… --dry-run\n  icloud mail not-junk msg_… --idempotency-key not-junk-01 --yes"
	case maildomain.LocationActionDelete:
		return "delete", "Permanently delete exact current provider locations", "  icloud mail delete msg_… --from trash --dry-run\n  icloud mail delete msg_… --from trash --idempotency-key delete-01 --yes"
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
