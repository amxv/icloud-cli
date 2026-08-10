package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type mailSelectionData struct {
	Selection     maildomain.MailSelection `json:"selection"`
	Expired       bool                     `json:"expired"`
	LocalOnly     bool                     `json:"local_only"`
	SchemaVersion int64                    `json:"schema_version"`
}

type mailSelectionDeleteData struct {
	SelectionID   string `json:"selection_id"`
	Deleted       bool   `json:"deleted"`
	SchemaVersion int64  `json:"schema_version"`
}

type mailSelectionReadData struct {
	SelectionID   string          `json:"selection_id"`
	SelectionHash string          `json:"selection_hash"`
	Messages      []publicMessage `json:"messages"`
	Count         int             `json:"count"`
	SchemaVersion int64           `json:"schema_version"`
}

type mailSelectionApplyPreviewData struct {
	SelectionID       string                            `json:"selection_id"`
	SelectionHash     string                            `json:"selection_hash"`
	Action            string                            `json:"action"`
	Mode              string                            `json:"mode"`
	RequestHash       string                            `json:"request_hash"`
	Count             int                               `json:"count"`
	Folders           []string                          `json:"folders"`
	ExpiresAt         time.Time                         `json:"expires_at"`
	LocationPlans     []maildomain.LocationMutationPlan `json:"location_plans,omitempty"`
	ProviderContacted bool                              `json:"provider_contacted"`
	SchemaVersion     int64                             `json:"schema_version"`
}

func newMailSelectionCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "selection",
		Short:   "Create and apply short-lived immutable reviewed mail target sets",
		Args:    cobra.NoArgs,
		Example: "  icloud mail selection create msg_… msg_… --scope-folder inbox\n  icloud mail selection create --search --unread --folder inbox --scope-folder inbox\n  icloud mail selection show sel_…\n  icloud mail selection apply sel_… --action archive --dry-run",
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(newMailSelectionCreateCommand(state))
	command.AddCommand(newMailSelectionShowCommand(state))
	command.AddCommand(newMailSelectionDeleteCommand(state))
	command.AddCommand(newMailSelectionApplyCommand(state))
	return command
}

func newMailSelectionCreateCommand(state *executionState) *cobra.Command {
	var searchFlags mailSearchFlags
	var searchMode, jsonOutput bool
	var resultKind string
	var limit int
	var scopeFolder string
	var ttl time.Duration
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "create [message-id|thread-id...]",
		Short:   "Freeze explicit stable targets or one bounded current local search page",
		Example: "  icloud mail selection create msg_… thr_… --ttl 15m\n  icloud mail selection create --search --query invoice --unread --folder inbox --scope-folder inbox --limit 50 --json",
		Args: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(input.Path) != "" {
				if len(args) != 0 {
					return NewAppError(ErrorUsage, "positional selection targets cannot be combined with --input-json", false)
				}
				return nil
			}
			if len(args) > MaximumBulkItems {
				return NewAppError(ErrorSafety, fmt.Sprintf("MailSelection accepts at most %d explicit targets", MaximumBulkItems), false)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			request := maildomain.SelectionCreateRequest{Targets: append([]string(nil), args...), ScopeFolder: scopeFolder, TTL: ttl, MaximumMembership: MaximumBulkItems}
			var jsonRequest selectionCreateJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest,
				"search", "query", "folder", "from", "participant", "subject", "rfc-id", "attachment-name", "attachment-type",
				"after", "before", "cursor", "unread", "read", "flagged", "unflagged", "result", "limit", "scope-folder", "ttl"); err != nil {
				return err
			} else if used {
				if len(jsonRequest.Targets) > 0 && jsonRequest.Search != nil {
					return NewAppError(ErrorUsage, "selection create JSON requires exactly one of targets or search", false)
				}
				request.Targets = append([]string(nil), jsonRequest.Targets...)
				request.ScopeFolder = jsonRequest.ScopeFolder
				if strings.TrimSpace(jsonRequest.TTL) != "" {
					parsedTTL, parseErr := time.ParseDuration(strings.TrimSpace(jsonRequest.TTL))
					if parseErr != nil {
						return NewAppError(ErrorUsage, "selection ttl must be a Go duration such as 15m or 2h", false)
					}
					request.TTL = parsedTTL
				}
				if jsonRequest.Search != nil {
					if jsonRequest.Search.Remote || jsonRequest.Search.AllFolders {
						return NewAppError(ErrorUsage, "MailSelection supports current local search pages only", false)
					}
					kind := maildomain.SearchResultKind(strings.ToLower(strings.TrimSpace(jsonRequest.Search.Result)))
					if kind == "" {
						kind = maildomain.SearchMessages
					}
					searchOptions, optionsErr := jsonRequest.Search.searchFlags().options(jsonRequest.Search.Query, kind, jsonRequest.Search.Limit)
					if optionsErr != nil {
						return optionsErr
					}
					request.Search = &searchOptions
					request.SearchFolder = jsonRequest.Search.Folder
				}
			} else {
				searchRequested := selectionSearchRequested(cmd, searchMode)
				if len(args) > 0 && searchRequested {
					return NewAppError(ErrorUsage, "explicit selection targets cannot be combined with local search flags", false)
				}
				if searchRequested {
					kind := maildomain.SearchResultKind(strings.ToLower(strings.TrimSpace(resultKind)))
					if kind != maildomain.SearchMessages && kind != maildomain.SearchThreads {
						return NewAppError(ErrorUsage, "--result must be messages or threads", false)
					}
					if limit < 1 || limit > MaximumBulkItems {
						return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumBulkItems), false)
					}
					options, optionsErr := searchFlags.options(searchFlags.Query, kind, limit)
					if optionsErr != nil {
						return optionsErr
					}
					request.Targets = nil
					request.Search = &options
					request.SearchFolder = searchFlags.Folder
				}
			}
			accountValue, selection, schemaVersion, err := state.mail.MailSelectionCreate(cmd.Context(), state.global.Home, state.global.Account, request)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSelectionData{Selection: selection, Expired: false, LocalOnly: true, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderMailSelection(NewCompactTextRenderer(state.stdout), accountValue.ID, selection, false, true)
		},
	}
	command.Flags().BoolVar(&searchMode, "search", false, "Create from one bounded current local search page instead of explicit IDs")
	addMailSearchFlags(command, &searchFlags, true, true)
	command.Flags().StringVar(&resultKind, "result", string(maildomain.SearchMessages), "Search source result kind: messages or threads")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum local search hits before thread expansion (1-%d)", MaximumBulkItems))
	command.Flags().StringVar(&scopeFolder, "scope-folder", "", "Freeze exact eligible current provider copies in one folder for later writes")
	command.Flags().DurationVar(&ttl, "ttl", 15*time.Minute, "Selection lifetime from creation (1m-24h)")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit the immutable selection and stable target evidence as JSON")
	bindJSONInput(command, &input)
	return command
}

func selectionSearchRequested(command *cobra.Command, explicit bool) bool {
	if explicit {
		return true
	}
	for _, name := range []string{"query", "folder", "from", "participant", "subject", "rfc-id", "attachment-name", "attachment-type", "after", "before", "cursor", "unread", "read", "flagged", "unflagged", "result", "limit"} {
		if command.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func newMailSelectionShowCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "show <selection-id>",
		Short:   "Show immutable reviewed MailSelection membership without message content",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail selection show sel_…\n  icloud mail selection show sel_… --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			selectionID := ""
			if len(args) == 1 {
				selectionID = strings.TrimSpace(args[0])
			}
			var request selectionTargetJSONRequest
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				selectionID = strings.TrimSpace(request.SelectionID)
			}
			accountValue, selection, schemaVersion, err := state.mail.MailSelection(cmd.Context(), state.global.Home, state.global.Account, selectionID)
			if err != nil {
				return err
			}
			expired := selection.Expired(time.Now().UTC())
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSelectionData{Selection: selection, Expired: expired, LocalOnly: true, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderMailSelection(NewCompactTextRenderer(state.stdout), accountValue.ID, selection, expired, false)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit selection metadata and stable target evidence as JSON")
	bindReadJSONInput(command, &input)
	return command
}

func newMailSelectionDeleteCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "delete <selection-id>",
		Short:   "Delete one local MailSelection without contacting iCloud",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail selection delete sel_…\n  icloud mail selection delete --input-json selection.json --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			selectionID := ""
			if len(args) == 1 {
				selectionID = strings.TrimSpace(args[0])
			}
			var request selectionTargetJSONRequest
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				selectionID = strings.TrimSpace(request.SelectionID)
			}
			_, selection, schemaVersion, err := state.mail.MailSelectionDelete(cmd.Context(), state.global.Home, state.global.Account, selectionID)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSelectionDeleteData{SelectionID: selection.ID, Deleted: true, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("mail-selection-delete", selection.ID, Field("deleted", true), Field("provider_contacted", false))
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailSelectionApplyCommand(state *executionState) *cobra.Command {
	var selectionID, action, to, mode, requestHash string
	var dryRun, yes, jsonOutput, jsonlOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "apply <selection-id>",
		Short:   "Read or mutate exactly the frozen MailSelection target set",
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail selection apply sel_… --action read --json\n  icloud mail selection apply sel_… --action mark-read --dry-run\n  icloud mail selection apply sel_… --action archive --dry-run\n  icloud mail selection apply sel_… --action archive --request-hash <hash> --yes --jsonl",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, JSONL: jsonlOutput}, OutputSupport{JSON: true, JSONL: true}); err != nil {
				return err
			}
			if len(args) == 1 {
				selectionID = strings.TrimSpace(args[0])
			}
			var request selectionApplyJSONRequest
			if used, err := readJSONInput(cmd, state, input, &request, "action", "to", "mode", "request-hash"); err != nil {
				return err
			} else if used {
				selectionID, action, to, mode, requestHash = request.SelectionID, request.Action, request.To, request.Mode, request.RequestHash
			}
			action = strings.ToLower(strings.TrimSpace(action))
			mode = strings.ToLower(strings.TrimSpace(mode))
			if mode == "" {
				mode = "atomic"
			}
			if mode != "atomic" && mode != "continue-on-error" {
				return NewAppError(ErrorUsage, "--mode must be atomic or continue-on-error", false)
			}
			if action == "read" {
				if dryRun || yes || strings.TrimSpace(requestHash) != "" || strings.TrimSpace(to) != "" || jsonlOutput {
					return NewAppError(ErrorUsage, "selection read does not accept write review/confirmation flags or JSONL", false)
				}
				_, selection, messages, schemaVersion, err := state.mail.MailSelectionForApply(cmd.Context(), state.global.Home, state.global.Account, selectionID, false)
				if err != nil {
					return err
				}
				public := make([]publicMessage, 0, len(messages))
				for _, message := range messages {
					public = append(public, sanitizeMessage(message))
				}
				if jsonOutput {
					return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSelectionReadData{SelectionID: selection.ID, SelectionHash: selection.RequestHash, Messages: public, Count: len(public), SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
				}
				renderer := NewCompactTextRenderer(state.stdout)
				for _, message := range public {
					if err := renderSelectionReadMessage(renderer, message); err != nil {
						return err
					}
				}
				return renderer.Summary("mail-selection-read", Field("selection", selection.ID), Field("selection_hash", selection.RequestHash), Field("count", len(public)), Field("provider_contacted", false))
			}
			if mutation, ok := selectionFlagAction(action); ok {
				return applySelectionFlagMutation(cmd, state, selectionID, action, mode, requestHash, dryRun, jsonOutput, jsonlOutput, mutation)
			}
			locationAction, ok := selectionLocationAction(action)
			if !ok {
				return NewAppError(ErrorUsage, "--action must be read, mark-read, mark-unread, flag, unflag, move, copy, archive, unarchive, trash, restore, junk, not-junk, or delete", false)
			}
			return applySelectionLocationMutation(cmd, state, selectionID, action, to, mode, requestHash, dryRun, yes, jsonOutput, jsonlOutput, locationAction)
		},
	}
	command.Flags().StringVar(&action, "action", "", "Reviewed action to apply to the frozen target set")
	command.Flags().StringVar(&to, "to", "", "Destination folder for move, copy, or restore; semantic actions resolve their normal destination")
	command.Flags().StringVar(&mode, "mode", "atomic", "Failure mode: validate then fail fast, or continue-on-error")
	command.Flags().StringVar(&requestHash, "request-hash", "", "Exact hash emitted by the matching --dry-run review")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Review exact frozen targets/action and emit the required request hash without provider contact")
	command.Flags().BoolVar(&yes, "yes", false, "Confirm an intentional non-interactive location-changing selection apply")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&jsonlOutput, "jsonl", false, "Emit ordered per-item and completed JSONL events for writes")
	bindJSONInput(command, &input)
	return command
}

func applySelectionFlagMutation(cmd *cobra.Command, state *executionState, selectionID, action, mode, providedHash string, dryRun, jsonOutput, jsonlOutput bool, mutation maildomain.FlagMutation) error {
	_, selection, _, schemaVersion, err := state.mail.MailSelectionForApply(cmd.Context(), state.global.Home, state.global.Account, selectionID, true)
	if err != nil {
		return err
	}
	if selection.ScopeFolderID == "" {
		return NewAppError(ErrorSafety, "MailSelection has no frozen provider-copy scope; create it with --scope-folder before a provider write", false)
	}
	applyHash, err := state.mail.MailSelectionApplyHash(cmd.Context(), selection, action, mode, "", nil)
	if err != nil {
		return WrapAppError(ErrorInternal, "cannot hash MailSelection apply request", false, err)
	}
	folders := selectionFolders(selection)
	if dryRun {
		return renderSelectionApplyPreview(state, mailSelectionApplyPreviewData{SelectionID: selection.ID, SelectionHash: selection.RequestHash, Action: action, Mode: mode, RequestHash: applyHash, Count: len(selection.Items), Folders: folders, ExpiresAt: selection.ExpiresAt, ProviderContacted: false, SchemaVersion: schemaVersion}, jsonOutput, jsonlOutput)
	}
	if strings.TrimSpace(providedHash) == "" {
		return NewAppError(ErrorSafety, "selection apply requires --request-hash from a matching --dry-run review", false)
	}
	if strings.TrimSpace(providedHash) != applyHash {
		return selectionApplyHashMismatch(selection, action, applyHash, providedHash)
	}
	items := make([]flagMutationItem, 0, len(selection.Items))
	for index := range selection.Items {
		item := selection.Items[index]
		items = append(items, flagMutationItem{MessageID: item.MessageID, Folder: item.FolderID, Snapshot: &item})
	}
	meta := &threadMutationMeta{SelectionID: selection.ID, SelectionHash: selection.RequestHash, ApplyRequestHash: applyHash}
	return executeFlagMutationItems(cmd, state, mutation, items, mode, jsonOutput, jsonlOutput, true, meta)
}

func applySelectionLocationMutation(cmd *cobra.Command, state *executionState, selectionID, actionName, to, mode, providedHash string, dryRun, yes, jsonOutput, jsonlOutput bool, action maildomain.LocationMutationAction) error {
	_, selection, _, schemaVersion, err := state.mail.MailSelectionForApply(cmd.Context(), state.global.Home, state.global.Account, selectionID, false)
	if err != nil {
		return err
	}
	if selection.ScopeFolderID == "" {
		return NewAppError(ErrorSafety, "MailSelection has no frozen provider-copy scope; create it with --scope-folder before a provider write", false)
	}
	items := make([]locationMutationItem, 0, len(selection.Items))
	for index := range selection.Items {
		item := selection.Items[index]
		items = append(items, locationMutationItem{MessageID: item.MessageID, From: item.FolderID, To: strings.TrimSpace(to), Snapshot: &item})
	}
	if err := validateLocationMutationItems(action, items); err != nil {
		return err
	}
	// A real retry has the reviewed hash already, so install deterministic
	// per-item idempotency before preview resolution; an already accepted move
	// can then be reconstructed even though its original source is now absent.
	if !dryRun && strings.TrimSpace(providedHash) != "" {
		for index := range items {
			items[index].IdempotencyKey = selectionItemIdempotencyKey(selection.ID, providedHash, items[index].MessageID)
		}
	}
	_, plans, _, err := validateLocationMutationPlans(cmd, state, action, items)
	if err != nil {
		return err
	}
	planHashes := make([]string, 0, len(plans))
	for _, plan := range plans {
		planHashes = append(planHashes, plan.RequestHash)
	}
	applyHash, err := state.mail.MailSelectionApplyHash(cmd.Context(), selection, actionName, mode, "", planHashes)
	if err != nil {
		return WrapAppError(ErrorInternal, "cannot hash MailSelection apply request", false, err)
	}
	if dryRun {
		return renderSelectionApplyPreview(state, mailSelectionApplyPreviewData{SelectionID: selection.ID, SelectionHash: selection.RequestHash, Action: actionName, Mode: mode, RequestHash: applyHash, Count: len(selection.Items), Folders: selectionPlanFolders(plans), ExpiresAt: selection.ExpiresAt, LocationPlans: plans, ProviderContacted: false, SchemaVersion: schemaVersion}, jsonOutput, jsonlOutput)
	}
	if strings.TrimSpace(providedHash) == "" {
		return NewAppError(ErrorSafety, "selection apply requires --request-hash from a matching --dry-run review", false)
	}
	if strings.TrimSpace(providedHash) != applyHash {
		return selectionApplyHashMismatch(selection, actionName, applyHash, providedHash)
	}
	for index := range items {
		items[index].IdempotencyKey = selectionItemIdempotencyKey(selection.ID, applyHash, items[index].MessageID)
	}
	flags := locationMutationFlags{To: strings.TrimSpace(to), Yes: yes, JSON: jsonOutput, JSONL: jsonlOutput}
	meta := &threadMutationMeta{SelectionID: selection.ID, SelectionHash: selection.RequestHash, ApplyRequestHash: applyHash}
	return executeLocationMutationItems(cmd, state, action, items, flags, mode, true, meta)
}

func selectionApplyHashMismatch(selection maildomain.MailSelection, action, expected, provided string) *AppError {
	err := NewAppError(ErrorConflict, "selection apply request no longer matches the reviewed request hash", false)
	err.Details = map[string]any{"selection_id": selection.ID, "action": action, "expected_request_hash": expected, "provided_request_hash": strings.TrimSpace(provided), "next": "rerun the same selection apply with --dry-run and review the new hash before provider contact"}
	return err
}

func selectionFlagAction(action string) (maildomain.FlagMutation, bool) {
	switch action {
	case "mark-read":
		return maildomain.FlagMutationMarkRead, true
	case "mark-unread":
		return maildomain.FlagMutationMarkUnread, true
	case "flag":
		return maildomain.FlagMutationFlag, true
	case "unflag":
		return maildomain.FlagMutationUnflag, true
	default:
		return "", false
	}
}

func selectionLocationAction(action string) (maildomain.LocationMutationAction, bool) {
	switch action {
	case "move":
		return maildomain.LocationActionMove, true
	case "copy":
		return maildomain.LocationActionCopy, true
	case "archive":
		return maildomain.LocationActionArchive, true
	case "unarchive":
		return maildomain.LocationActionUnarchive, true
	case "trash":
		return maildomain.LocationActionTrash, true
	case "restore":
		return maildomain.LocationActionRestore, true
	case "junk":
		return maildomain.LocationActionJunk, true
	case "not-junk":
		return maildomain.LocationActionNotJunk, true
	case "delete":
		return maildomain.LocationActionDelete, true
	default:
		return "", false
	}
}

func selectionItemIdempotencyKey(selectionID, applyHash, messageID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(selectionID) + "\x00" + strings.TrimSpace(applyHash) + "\x00" + strings.TrimSpace(messageID)))
	return "selection:" + hex.EncodeToString(sum[:])
}

func selectionFolders(selection maildomain.MailSelection) []string {
	set := map[string]struct{}{}
	for _, item := range selection.Items {
		if item.FolderID != "" {
			set[item.FolderID] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func selectionPlanFolders(plans []maildomain.LocationMutationPlan) []string {
	set := map[string]struct{}{}
	for _, plan := range plans {
		if plan.SourceFolderID != "" {
			set[plan.SourceFolderID] = struct{}{}
		}
		if plan.DestinationFolderID != "" {
			set[plan.DestinationFolderID] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func renderSelectionApplyPreview(state *executionState, data mailSelectionApplyPreviewData, jsonOutput, jsonlOutput bool) error {
	if jsonlOutput {
		return WriteJSONLEvent(state.stdout, JSONLEvent{Event: "preview", Sequence: 1, Timestamp: time.Now().UTC(), Payload: map[string]any{"selection_id": data.SelectionID, "selection_hash": data.SelectionHash, "action": data.Action, "mode": data.Mode, "request_hash": data.RequestHash, "count": data.Count, "folders": data.Folders, "expires_at": data.ExpiresAt, "provider_contacted": false, "location_plans": data.LocationPlans}})
	}
	if jsonOutput {
		return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
	}
	return NewCompactTextRenderer(state.stdout).Summary("mail-selection-apply-preview",
		Field("selection", data.SelectionID), Field("selection_hash", data.SelectionHash), Field("action", data.Action), Field("mode", data.Mode),
		Field("count", data.Count), Field("folders", strings.Join(data.Folders, ",")), Field("request_hash", data.RequestHash),
		Field("expires_at", data.ExpiresAt.UTC().Format(time.RFC3339)), Field("provider_contacted", false),
		Field("next", "rerun the same apply request with --request-hash and --yes when the action changes provider locations"),
	)
}

func renderMailSelection(renderer *CompactTextRenderer, accountID string, selection maildomain.MailSelection, expired, created bool) error {
	for _, item := range selection.Items {
		if err := renderer.Record("mail-selection-item", item.MessageID,
			Field("ordinal", item.Ordinal), Field("location", item.LocationID), Field("folder", item.FolderID),
			Field("uid_validity", item.UIDValidity), Field("uid", item.UID), Field("mod_sequence", item.ModSequence),
		); err != nil {
			return err
		}
	}
	return renderer.Summary("mail-selection",
		Field("id", selection.ID), Field("account", accountID), Field("source", selection.SourceDescription),
		Field("count", selection.ItemCount), Field("scope_folder", selection.ScopeFolderID), Field("request_hash", selection.RequestHash),
		Field("created_at", selection.CreatedAt.UTC().Format(time.RFC3339)), Field("expires_at", selection.ExpiresAt.UTC().Format(time.RFC3339)),
		Field("expired", expired), Field("local_only", true), Field("created", created),
	)
}

func renderSelectionReadMessage(renderer *CompactTextRenderer, message publicMessage) error {
	return renderer.Record("mail-selection-message", message.ID,
		Field("direction", message.Direction), Field("unread", message.Unread), Field("flagged", message.Flagged),
		Field("answered", message.Answered), Field("forwarded", message.Forwarded),
	)
}
