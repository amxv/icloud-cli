package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type mailLatestData struct {
	AccountID     string                     `json:"account_id"`
	Threads       []maildomain.Thread        `json:"threads"`
	Count         int                        `json:"count"`
	More          bool                       `json:"more,omitempty"`
	NextCursor    string                     `json:"next_cursor,omitempty"`
	Query         maildomain.SearchQueryMeta `json:"query"`
	SchemaVersion int64                      `json:"schema_version"`
}

type publicThread struct {
	ID                    string                                    `json:"id"`
	AccountID             string                                    `json:"account_id"`
	RootRFCMessageID      string                                    `json:"root_rfc_message_id,omitempty"`
	Subject               string                                    `json:"subject,omitempty"`
	Snippet               string                                    `json:"snippet,omitempty"`
	Participants          []string                                  `json:"participants,omitempty"`
	FolderIDs             []string                                  `json:"folder_ids,omitempty"`
	MessageCount          int                                       `json:"message_count"`
	UnreadCount           int                                       `json:"unread_count"`
	Replied               bool                                      `json:"replied"`
	Forwarded             bool                                      `json:"forwarded"`
	AttachmentCount       int                                       `json:"attachment_count"`
	FirstMessageAt        *time.Time                                `json:"first_message_at,omitempty"`
	LastMessageAt         *time.Time                                `json:"last_message_at,omitempty"`
	Status                string                                    `json:"status"`
	Reconstruction        string                                    `json:"reconstruction"`
	RedirectedFrom        string                                    `json:"redirected_from,omitempty"`
	ProjectionState       string                                    `json:"projection_state"`
	ProjectionReason      string                                    `json:"projection_reason,omitempty"`
	ProjectionPending     int                                       `json:"projection_pending,omitempty"`
	ProjectionUpdatedAt   *time.Time                                `json:"projection_updated_at,omitempty"`
	SenderPreferences     *maildomain.ThreadSenderPreferenceSummary `json:"sender_preferences,omitempty"`
	EdgeEvidenceTruncated bool                                      `json:"edge_evidence_truncated,omitempty"`
	Messages              []publicThreadMessage                     `json:"messages"`
	Edges                 []publicThreadEdge                        `json:"edges,omitempty"`
}

type publicThreadEdge struct {
	ChildMessageID  string `json:"child_message_id"`
	Relation        string `json:"relation"`
	Ordinal         int    `json:"ordinal"`
	Token           string `json:"token"`
	ParentMessageID string `json:"parent_message_id,omitempty"`
	Status          string `json:"status"`
}

type publicThreadMessage struct {
	Message publicMessage      `json:"message"`
	Content messageContentData `json:"content"`
}

type mailThreadData struct {
	AccountID     string       `json:"account_id"`
	Thread        publicThread `json:"thread"`
	Count         int          `json:"count"`
	More          bool         `json:"more,omitempty"`
	NextCursor    string       `json:"next_cursor,omitempty"`
	SchemaVersion int64        `json:"schema_version"`
}

type threadMutationMeta struct {
	ThreadID                 string
	RedirectedFrom           string
	MessageCount             int
	More                     bool
	NextCursor               string
	SelectionID              string
	SelectionHash            string
	ApplyRequestHash         string
	PurgeID                  string
	PurgeHash                string
	PurgeScope               maildomain.PurgeScopeKind
	PurgeFolderID            string
	PurgeMore                bool
	PurgeBlockedUnresolved   int
	RetentionWarning         string
	RequireConfirmedLocation bool
}

func newMailThreadsCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "threads",
		Short:   "List, read, and change locally reconstructed conversations",
		Args:    cobra.NoArgs,
		Example: "  icloud mail threads list\n  icloud mail threads show thr_…\n  icloud mail threads mark-read thr_… --folder inbox\n  icloud mail threads archive thr_… --from inbox --dry-run",
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(newMailThreadsListCommand(state))
	command.AddCommand(newMailThreadsShowCommand(state))
	command.AddCommand(newMailThreadFlagMutationCommand(state, "mark-read", "Mark selected locations for a canonical thread page as read", maildomain.FlagMutationMarkRead))
	command.AddCommand(newMailThreadFlagMutationCommand(state, "mark-unread", "Mark selected locations for a canonical thread page as unread", maildomain.FlagMutationMarkUnread))
	command.AddCommand(newMailThreadFlagMutationCommand(state, "flag", "Flag selected locations for a canonical thread page", maildomain.FlagMutationFlag))
	command.AddCommand(newMailThreadFlagMutationCommand(state, "unflag", "Remove flags from selected locations for a canonical thread page", maildomain.FlagMutationUnflag))
	for _, action := range []maildomain.LocationMutationAction{
		maildomain.LocationActionMove,
		maildomain.LocationActionCopy,
		maildomain.LocationActionArchive,
		maildomain.LocationActionUnarchive,
		maildomain.LocationActionTrash,
		maildomain.LocationActionRestore,
		maildomain.LocationActionJunk,
		maildomain.LocationActionNotJunk,
		maildomain.LocationActionDelete,
	} {
		command.AddCommand(newMailThreadLocationMutationCommand(state, action))
	}
	return command
}

func newMailThreadFlagMutationCommand(state *executionState, name, short string, mutation maildomain.FlagMutation) *cobra.Command {
	var target, folderSelector, cursor, mode string
	var limit int
	var page, jsonOutput, jsonlOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     name + " <thread-id|message-id>",
		Short:   short,
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: fmt.Sprintf("  icloud mail threads %s thr_… --folder inbox\n  icloud mail threads %s thr_… --folder inbox --page --jsonl", name, name),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, JSONL: jsonlOutput}, OutputSupport{JSON: true, JSONL: true}); err != nil {
				return err
			}
			if len(args) == 1 {
				target = strings.TrimSpace(args[0])
			}
			request := threadFlagMutationJSONRequest{Target: target, Folder: folderSelector, Cursor: cursor, Limit: limit, Page: page, Mode: mode}
			if used, err := readJSONInput(cmd, state, input, &request, "folder", "cursor", "limit", "page", "mode"); err != nil {
				return err
			} else if used {
				target, folderSelector, cursor, limit, page, mode = request.Target, request.Folder, request.Cursor, request.Limit, request.Page, request.Mode
			}
			mode = strings.ToLower(strings.TrimSpace(mode))
			if mode == "" {
				mode = "atomic"
			}
			if mode != "atomic" && mode != "continue-on-error" {
				return NewAppError(ErrorUsage, "--mode must be atomic or continue-on-error", false)
			}
			if strings.TrimSpace(folderSelector) == "" {
				return NewAppError(ErrorUsage, "thread state mutation requires --folder to define the exact provider-copy scope", false)
			}
			pageValue, meta, err := resolveThreadMutationPage(cmd, state, target, cursor, limit, page, folderSelector, false)
			if err != nil {
				return err
			}
			if len(pageValue.MessageIDs) == 0 {
				return renderEmptyThreadMutationPage(state, meta, mode, jsonOutput, jsonlOutput, "state")
			}
			items := make([]flagMutationItem, 0, len(pageValue.MessageIDs))
			for _, messageID := range pageValue.MessageIDs {
				items = append(items, flagMutationItem{MessageID: messageID, Folder: strings.TrimSpace(folderSelector)})
			}
			if err := validateFlagMutationItems(items); err != nil {
				return err
			}
			return executeFlagMutationItems(cmd, state, mutation, items, mode, jsonOutput, jsonlOutput, true, meta)
		},
	}
	command.Flags().StringVar(&folderSelector, "folder", "", "Select one exact folder ID, name, wire name, or kind for every canonical thread member")
	command.Flags().StringVar(&cursor, "cursor", "", "Continue a previously paged thread mutation from its next_cursor")
	command.Flags().IntVar(&limit, "limit", MaximumBulkItems, fmt.Sprintf("Maximum chronological thread members to mutate in this page (1-%d)", MaximumBulkItems))
	command.Flags().BoolVar(&page, "page", false, "Explicitly allow a partial first page when the canonical thread exceeds the bounded limit")
	command.Flags().StringVar(&mode, "mode", "atomic", "Failure mode for this bounded thread page: fail fast or continue-on-error")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&jsonlOutput, "jsonl", false, "Emit ordered per-message and completed JSONL events")
	bindJSONInput(command, &input)
	return command
}

func newMailThreadLocationMutationCommand(state *executionState, action maildomain.LocationMutationAction) *cobra.Command {
	var target, cursor, mode string
	var limit int
	var page bool
	var flags locationMutationFlags
	var input jsonInputOptions
	name, _, _ := locationMutationCommandMetadata(action)
	command := &cobra.Command{
		Use:     name + " <thread-id|message-id>",
		Short:   threadLocationMutationShort(action),
		Args:    exactArgsOrJSONInput(&input, 1),
		Example: threadLocationMutationExample(action),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: flags.JSON, JSONL: flags.JSONL}, OutputSupport{JSON: true, JSONL: true}); err != nil {
				return err
			}
			if flags.DryRun && flags.Yes {
				return NewAppError(ErrorUsage, "--dry-run and --yes cannot be combined", false)
			}
			if len(args) == 1 {
				target = strings.TrimSpace(args[0])
			}
			request := threadLocationMutationJSONRequest{
				Target: target, From: flags.From, To: flags.To, IdempotencyKey: flags.IdempotencyKey,
				Cursor: cursor, Limit: limit, Page: page, Mode: mode,
			}
			if used, err := readJSONInput(cmd, state, input, &request, "from", "to", "idempotency-key", "cursor", "limit", "page", "mode"); err != nil {
				return err
			} else if used {
				target, flags.From, flags.To, flags.IdempotencyKey = request.Target, request.From, request.To, request.IdempotencyKey
				cursor, limit, page, mode = request.Cursor, request.Limit, request.Page, request.Mode
			}
			mode = strings.ToLower(strings.TrimSpace(mode))
			if mode == "" {
				mode = "atomic"
			}
			if mode != "atomic" && mode != "continue-on-error" {
				return NewAppError(ErrorUsage, "--mode must be atomic or continue-on-error", false)
			}
			if strings.TrimSpace(flags.From) == "" {
				return NewAppError(ErrorUsage, "thread location mutation requires --from to define the exact provider-copy scope", false)
			}
			pageValue, meta, err := resolveThreadMutationPage(cmd, state, target, cursor, limit, page, flags.From, action == maildomain.LocationActionDelete)
			if err != nil {
				return err
			}
			if len(pageValue.MessageIDs) == 0 {
				return renderEmptyThreadMutationPage(state, meta, mode, flags.JSON, flags.JSONL, "location")
			}
			items := make([]locationMutationItem, 0, len(pageValue.MessageIDs))
			for _, messageID := range pageValue.MessageIDs {
				items = append(items, locationMutationItem{MessageID: messageID, From: flags.From, To: flags.To})
			}
			items = applyThreadLocationMutationDefaults(items, flags.IdempotencyKey, pageValue.ThreadID)
			if err := validateLocationMutationItems(action, items); err != nil {
				return err
			}
			return executeLocationMutationItems(cmd, state, action, items, flags, mode, true, meta)
		},
	}
	command.Flags().StringVar(&flags.From, "from", "", "Select one exact current source folder for every canonical thread member")
	command.Flags().StringVar(&flags.To, "to", "", "Select one current destination folder for every canonical thread member")
	command.Flags().StringVar(&flags.IdempotencyKey, "idempotency-key", "", idempotencyKeyHelp)
	command.Flags().StringVar(&cursor, "cursor", "", "Continue a previously paged thread mutation from its next_cursor")
	command.Flags().IntVar(&limit, "limit", MaximumBulkItems, fmt.Sprintf("Maximum chronological thread members to mutate in this page (1-%d)", MaximumBulkItems))
	command.Flags().BoolVar(&page, "page", false, "Explicitly allow a partial first page when the canonical thread exceeds the bounded limit")
	command.Flags().StringVar(&mode, "mode", "atomic", "Failure mode for this bounded thread page: fail fast or continue-on-error")
	command.Flags().BoolVar(&flags.DryRun, "dry-run", false, "Preview every exact per-message location change without creating intents or contacting IMAP")
	command.Flags().BoolVar(&flags.Yes, "yes", false, "Confirm an intentional non-interactive thread location change")
	command.Flags().BoolVar(&flags.JSON, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&flags.JSONL, "jsonl", false, "Emit ordered per-message and completed JSONL events")
	bindJSONInput(command, &input)
	return command
}

func resolveThreadMutationPage(cmd *cobra.Command, state *executionState, target, cursor string, limit int, allowPartialFirstPage bool, sourceFolder string, includeDeleted bool) (maildomain.ThreadMutationTargetPage, *threadMutationMeta, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return maildomain.ThreadMutationTargetPage{}, nil, NewAppError(ErrorUsage, "thread or message target is required", false)
	}
	if limit < 1 || limit > MaximumBulkItems {
		return maildomain.ThreadMutationTargetPage{}, nil, NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumBulkItems), false)
	}
	_, page, _, err := state.mail.MailThreadMutationTargets(cmd.Context(), state.global.Home, state.global.Account, target, cursor, limit, sourceFolder, includeDeleted)
	if err != nil {
		return maildomain.ThreadMutationTargetPage{}, nil, err
	}
	meta := &threadMutationMeta{ThreadID: page.ThreadID, RedirectedFrom: page.RedirectedFrom, MessageCount: page.MessageCount, More: page.More, NextCursor: page.NextCursor}
	if page.More && strings.TrimSpace(cursor) == "" && !allowPartialFirstPage {
		err := NewAppError(ErrorSafety, fmt.Sprintf("canonical thread has more than %d members; refuse an implicit partial whole-thread mutation", limit), false)
		err.Details = map[string]any{
			"thread_id": page.ThreadID, "thread_message_count": page.MessageCount, "page_count": len(page.MessageIDs),
			"next": "review the thread, then rerun with --page to mutate the first bounded page and follow the returned next_cursor",
		}
		return maildomain.ThreadMutationTargetPage{}, nil, err
	}
	if len(page.MessageIDs) == 0 && !page.More {
		err := NewAppError(ErrorNotFound, "canonical thread has no eligible current location in the selected provider-copy scope", false)
		err.Details = map[string]any{"thread_id": page.ThreadID, "folder": sourceFolder, "thread_message_count": page.MessageCount}
		return maildomain.ThreadMutationTargetPage{}, nil, err
	}
	return page, meta, nil
}

func renderEmptyThreadMutationPage(state *executionState, meta *threadMutationMeta, mode string, jsonOutput, jsonlOutput bool, kind string) error {
	if meta == nil {
		return NewAppError(ErrorInternal, "thread mutation page metadata is unavailable", false)
	}
	payload := map[string]any{"mode": mode, "requested": 0, "attempted": 0, "failed": 0, "skipped": 0, "partial": false, "eligible": 0}
	if kind == "state" {
		payload["count"] = 0
	} else {
		payload["completed"] = 0
	}
	addThreadMutationPayload(payload, meta)
	if jsonlOutput {
		return WriteJSONLEvent(state.stdout, JSONLEvent{Event: "completed", Sequence: 1, Timestamp: time.Now().UTC(), Payload: payload})
	}
	if jsonOutput {
		return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: payload, Meta: EnvelopeMeta{RequestID: state.requestID}})
	}
	fields := []CompactField{
		Field("mode", mode), Field("eligible", 0), Field("attempted", 0), Field("failed", 0), Field("partial", false),
	}
	fields = appendThreadMutationCompactFields(fields, meta)
	return NewCompactTextRenderer(state.stdout).Summary("mail-thread-page", fields...)
}

func applyThreadLocationMutationDefaults(items []locationMutationItem, baseIdempotencyKey, canonicalThreadID string) []locationMutationItem {
	baseIdempotencyKey = strings.TrimSpace(baseIdempotencyKey)
	canonicalThreadID = strings.TrimSpace(canonicalThreadID)
	if baseIdempotencyKey == "" {
		return items
	}
	for index := range items {
		if strings.TrimSpace(items[index].IdempotencyKey) != "" {
			continue
		}
		sum := sha256.Sum256([]byte(baseIdempotencyKey + "\x00" + canonicalThreadID + "\x00" + strings.TrimSpace(items[index].MessageID)))
		items[index].IdempotencyKey = "thread:" + hex.EncodeToString(sum[:])
	}
	return items
}

func threadLocationMutationShort(action maildomain.LocationMutationAction) string {
	name, _, _ := locationMutationCommandMetadata(action)
	return fmt.Sprintf("Apply %s to exact locations for one canonical thread page", name)
}

func threadLocationMutationExample(action maildomain.LocationMutationAction) string {
	name, _, _ := locationMutationCommandMetadata(action)
	switch action {
	case maildomain.LocationActionMove, maildomain.LocationActionCopy:
		return fmt.Sprintf("  icloud mail threads %s thr_… --from inbox --to Projects --dry-run\n  icloud mail threads %s thr_… --from inbox --to Projects --idempotency-key thread-%s-01 --yes", name, name, name)
	case maildomain.LocationActionDelete:
		return "  icloud mail threads delete thr_… --from trash --dry-run\n  icloud mail threads delete thr_… --from trash --idempotency-key thread-delete-01 --yes"
	default:
		return fmt.Sprintf("  icloud mail threads %s thr_… --dry-run\n  icloud mail threads %s thr_… --idempotency-key thread-%s-01 --yes", name, name, name)
	}
}

func newMailThreadsListCommand(state *executionState) *cobra.Command {
	return newMailThreadListCommand(
		state,
		"list",
		"List and filter the newest reconstructed conversations",
		"  icloud mail threads list\n  icloud mail threads list --query launch --unread\n  icloud mail threads list --participant teammate@example.com\n  icloud mail threads list --cursor cur_… --json\n  icloud mail threads list --input-json request.json --json",
	)
}

func newMailLatestCommand(state *executionState) *cobra.Command {
	return newMailThreadListCommand(
		state,
		"latest",
		"Compatibility alias for `mail threads list`",
		"  icloud mail threads list\n  icloud mail threads list --query launch --unread\n  icloud mail threads list --cursor cur_… --json",
	)
}

func newMailThreadListCommand(state *executionState, use, short, example string) *cobra.Command {
	var jsonOutput bool
	var limit int
	var filters mailSearchFlags
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     use,
		Short:   short,
		Args:    cobra.NoArgs,
		Example: example,
		RunE: func(cmd *cobra.Command, _ []string) error {
			request := threadListReadJSONRequest{Limit: DefaultPageSize}
			if used, err := readJSONInput(cmd, state, input, &request,
				"folder", "query", "from", "participant", "subject", "rfc-id", "attachment-name", "attachment-type",
				"after", "before", "cursor", "unread", "read", "flagged", "unflagged", "limit"); err != nil {
				return err
			} else if used {
				limit = request.Limit
				filters = request.searchFlags()
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			options, err := filters.options(filters.Query, maildomain.SearchThreads, limit)
			if err != nil {
				return err
			}
			accountValue, _, page, schemaVersion, err := state.mail.MailSearch(cmd.Context(), state.global.Home, state.global.Account, filters.Folder, options)
			if err != nil {
				return err
			}
			threads := make([]maildomain.Thread, 0, len(page.Hits))
			for _, hit := range page.Hits {
				if hit.Thread != nil {
					threads = append(threads, *hit.Thread)
				}
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailLatestData{
					AccountID: accountValue.ID, Threads: threads, Count: len(threads), More: page.More, NextCursor: page.NextCursor, Query: page.Query, SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			filtered := mailSearchFlagsActive(filters)
			for index, thread := range threads {
				var renderErr error
				if filtered {
					renderErr = renderSearchHit(renderer, page.Hits[index])
				} else {
					renderErr = renderThreadSummary(renderer, thread)
				}
				if renderErr != nil {
					return renderErr
				}
			}
			return renderer.Summary("threads", Field("account", accountValue.ID), Field("count", len(threads)), Field("more", fmt.Sprintf("%t", page.More)), Field("next_cursor", page.NextCursor))
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum reconstructed conversations to return (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	addMailSearchFlags(command, &filters, true, true)
	bindReadJSONInput(command, &input)
	return command
}

func newMailThreadCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "thread",
		Short:   "Compatibility namespace for `mail threads show`",
		Args:    cobra.NoArgs,
		Example: "  icloud mail threads show thr_…\n  icloud mail threads show msg_… --full",
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(newMailThreadShowCommand(state))
	return command
}

func newMailThreadShowCommand(state *executionState) *cobra.Command {
	return newMailThreadShowLeafCommand(
		state,
		"show <thread-id|message-id>",
		"Compatibility alias for `mail threads show`",
		"  icloud mail threads show thr_…\n  icloud mail threads show msg_… --full\n  icloud mail threads show thr_… --limit 20 --cursor cur_…\n  icloud mail threads show thr_… --format html --json\n  icloud mail threads show thr_… --headers --verbose",
	)
}

func newMailThreadsShowCommand(state *executionState) *cobra.Command {
	return newMailThreadShowLeafCommand(
		state,
		"show <thread-id|message-id>",
		"Show a bounded chronological page from a reconstructed conversation",
		"  icloud mail threads show thr_…\n  icloud mail threads show msg_… --full\n  icloud mail threads show thr_… --limit 20 --cursor cur_…\n  icloud mail threads show thr_… --format html --json\n  icloud mail threads show thr_… --headers --verbose\n  icloud mail threads show --input-json request.json --json",
	)
}

func newMailThreadShowLeafCommand(state *executionState, use, short, example string) *cobra.Command {
	var options OutputOptions
	var limit int
	var cursor string
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     use,
		Short:   short,
		Args:    exactReadArgsOrJSONInput(&input, 1),
		Example: example,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			request := threadShowReadJSONRequest{Target: target, Limit: DefaultPageSize, Format: "compact"}
			if used, err := readJSONInput(cmd, state, input, &request,
				"limit", "cursor", "full", "verbose", "format", "headers", "body-only", "metadata-only"); err != nil {
				return err
			} else if used {
				target = request.Target
				limit = request.Limit
				cursor = request.Cursor
				options = request.outputOptions(options.JSON)
			}
			if strings.TrimSpace(target) == "" {
				return NewAppError(ErrorUsage, "thread or message target is required", false)
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			if err := ValidateOutputOptions(options, OutputSupport{
				JSON: true, Full: true, Verbose: true, Text: true, HTML: true, Raw: true,
				Headers: true, BodyOnly: true, MetadataOnly: true,
			}); err != nil {
				return err
			}
			format, err := effectiveMessageFormat(options)
			if err != nil {
				return err
			}
			if format == maildomain.ContentRaw && !options.JSON {
				return NewAppError(ErrorUsage, "plain multi-message raw output is ambiguous; use --format raw --json or `mail message raw <message-id>`", false)
			}
			accountValue, page, sources, schemaVersion, err := state.mail.MailThread(
				cmd.Context(), state.global.Home, state.global.Account, target, maildomain.ThreadReadOptions{
					Cursor: cursor, Limit: limit,
					RequireSource:  format == maildomain.ContentRaw || format == maildomain.ContentText || format == maildomain.ContentHTML,
					HydrateContent: options.Headers || options.Verbose || format == maildomain.ContentText || format == maildomain.ContentHTML,
				},
			)
			if err != nil {
				return err
			}
			if options.JSON {
				public, buildErr := buildPublicThread(cmd.Context(), page.Thread, sources, options, format)
				if buildErr != nil {
					return buildErr
				}
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailThreadData{
					AccountID: accountValue.ID, Thread: public, Count: len(public.Messages), More: page.More, SchemaVersion: schemaVersion,
					NextCursor: page.NextCursor,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			if options.BodyOnly {
				for index := range page.Thread.Messages {
					if sources[index] == nil {
						return NewAppError(ErrorUnavailable, "decoded thread message content is not available locally", false)
					}
					if index > 0 {
						if _, err := io.WriteString(state.stdout, "\n\n"); err != nil {
							return err
						}
					}
					if err := sources[index].WriteBody(cmd.Context(), format, state.stdout); err != nil {
						return messageContentError(err)
					}
				}
				return nil
			}
			renderer := NewCompactTextRenderer(state.stdout)
			if err := renderThreadSummary(renderer, page.Thread); err != nil {
				return err
			}
			for index, message := range page.Thread.Messages {
				if err := renderThreadMessage(renderer, message, options.MetadataOnly); err != nil {
					return err
				}
				if options.Verbose && message.Content != nil {
					for _, part := range message.Content.Parts {
						if err := renderMIMEPart(renderer, part); err != nil {
							return err
						}
					}
				}
				if options.Headers {
					if err := writeThreadHeaders(state.stdout, message); err != nil {
						return err
					}
				}
				if format == maildomain.ContentText || format == maildomain.ContentHTML {
					if sources[index] == nil {
						return NewAppError(ErrorUnavailable, "decoded thread message content is not available locally", false)
					}
					if _, err := fmt.Fprintf(state.stdout, "\nbody\nmessage=%s\n", message.ID); err != nil {
						return err
					}
					if err := sources[index].WriteBody(cmd.Context(), format, state.stdout); err != nil {
						return messageContentError(err)
					}
					if _, err := io.WriteString(state.stdout, "\n"); err != nil {
						return err
					}
				}
			}
			if options.Verbose {
				for _, edge := range page.Thread.Edges {
					if err := renderer.Record("thread-edge", "",
						Field("child", edge.ChildMessageID),
						Field("relation", edge.Relation),
						Field("ordinal", edge.Ordinal),
						Field("token", edge.Token),
						Field("parent", edge.ParentMessageID),
						Field("status", edge.Status),
					); err != nil {
						return err
					}
				}
				if page.Thread.EdgeEvidenceTruncated {
					if err := renderer.Record("thread-edge-evidence", "",
						Field("shown", len(page.Thread.Edges)),
						Field("truncated", true),
					); err != nil {
						return err
					}
				}
			}
			return renderer.Summary("thread-page", Field("thread", page.Thread.ID), Field("count", len(page.Thread.Messages)), Field("more", fmt.Sprintf("%t", page.More)), Field("next_cursor", page.NextCursor))
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum chronological messages to return (1-%d)", MaximumPageSize))
	command.Flags().StringVar(&cursor, "cursor", "", "Continue from a next_cursor returned by an earlier page")
	command.Flags().BoolVar(&options.JSON, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&options.Full, "full", false, "Include complete decoded text for every selected message")
	command.Flags().BoolVar(&options.Verbose, "verbose", false, "Include MIME part metadata and RFC graph edge evidence")
	command.Flags().StringVar(&options.Format, "format", "compact", "Content format: compact, text, html, or raw (raw requires --json)")
	command.Flags().BoolVar(&options.Headers, "headers", false, "Include decoded public headers for every selected message")
	command.Flags().BoolVar(&options.BodyOnly, "body-only", false, "Write only chronological decoded message bodies")
	command.Flags().BoolVar(&options.MetadataOnly, "metadata-only", false, "Show thread and message metadata without excerpts or bodies")
	bindReadJSONInput(command, &input)
	return command
}

func renderThreadSummary(renderer *CompactTextRenderer, thread maildomain.Thread) error {
	var projectionState, projectionReason, projectionPending any
	if thread.ProjectionState != "" && thread.ProjectionState != "ready" {
		projectionState = thread.ProjectionState
		projectionReason = thread.ProjectionReason
		if thread.ProjectionPending > 0 {
			projectionPending = thread.ProjectionPending
		}
	}
	return renderer.Record("thread", thread.ID,
		Field("subject", thread.CanonicalSubject),
		Field("messages", thread.MessageCount),
		Field("unread", thread.UnreadCount),
		Field("replied", thread.Replied),
		Field("forwarded", thread.Forwarded),
		Field("vip_senders", threadSenderPreferenceCount(thread.SenderPreferences, "vip")),
		Field("blocked_senders", threadSenderPreferenceCount(thread.SenderPreferences, "blocked")),
		Field("participants", thread.Participants),
		Field("folders", thread.FolderIDs),
		Field("attachments", thread.AttachmentCount),
		Field("last", thread.LastMessageAt),
		Field("redirected_from", thread.RedirectedFrom),
		Field("reconstruction", thread.Reconstruction),
		Field("projection", projectionState),
		Field("projection_reason", projectionReason),
		Field("projection_pending", projectionPending),
	)
}

func renderThreadMessage(renderer *CompactTextRenderer, message maildomain.Message, metadataOnly bool) error {
	fields := []CompactField{
		Field("at", message.SortAt),
		Field("from", formatAddress(message.From)),
		Field("direction", message.Direction),
		Field("state", messageReadState(message)),
		Field("flagged", message.Flagged),
		Field("replied", message.Replied),
		Field("reply_state", message.ReplyState),
		Field("forwarded", message.Forwarded),
		Field("vip", message.SenderPreference != nil && message.SenderPreference.VIP),
		Field("blocked", message.SenderPreference != nil && message.SenderPreference.Blocked),
		Field("attachments", message.AttachmentCount),
	}
	if !metadataOnly {
		fields = append(fields, Field("excerpt", Excerpt(message.Snippet, DefaultExcerptRunes)))
	}
	return renderer.Record("message", message.ID, fields...)
}

func buildPublicThread(ctx context.Context, thread maildomain.Thread, sources []maildomain.ContentSource, options OutputOptions, format maildomain.ContentFormat) (publicThread, error) {
	messages := make([]publicThreadMessage, 0, len(thread.Messages))
	for index, message := range thread.Messages {
		content, err := buildJSONMessageContent(ctx, message, sources[index], options, format)
		if err != nil {
			return publicThread{}, err
		}
		publicMessage := sanitizeMessage(message)
		if options.MetadataOnly {
			publicMessage.Snippet = ""
		}
		messages = append(messages, publicThreadMessage{Message: publicMessage, Content: content})
	}
	var first, last *time.Time
	if !thread.FirstMessageAt.IsZero() {
		value := thread.FirstMessageAt
		first = &value
	}
	if !thread.LastMessageAt.IsZero() {
		value := thread.LastMessageAt
		last = &value
	}
	edges := []publicThreadEdge(nil)
	if options.Verbose || options.MetadataOnly {
		for _, edge := range thread.Edges {
			edges = append(edges, publicThreadEdge{
				ChildMessageID: edge.ChildMessageID, Relation: edge.Relation, Ordinal: edge.Ordinal,
				Token: edge.Token, ParentMessageID: edge.ParentMessageID, Status: edge.Status,
			})
		}
	}
	snippet := thread.Snippet
	if options.MetadataOnly {
		snippet = ""
	}
	return publicThread{
		ID: thread.ID, AccountID: thread.AccountID, RootRFCMessageID: thread.RootRFCMessageID,
		Subject: thread.CanonicalSubject, Snippet: snippet,
		Participants: append([]string(nil), thread.Participants...), FolderIDs: append([]string(nil), thread.FolderIDs...),
		MessageCount: thread.MessageCount, UnreadCount: thread.UnreadCount, Replied: thread.Replied, Forwarded: thread.Forwarded, AttachmentCount: thread.AttachmentCount,
		FirstMessageAt: first, LastMessageAt: last, Status: thread.Status, Reconstruction: thread.Reconstruction,
		RedirectedFrom:  thread.RedirectedFrom,
		ProjectionState: thread.ProjectionState, ProjectionReason: thread.ProjectionReason,
		ProjectionPending: thread.ProjectionPending, ProjectionUpdatedAt: thread.ProjectionUpdatedAt,
		SenderPreferences:     thread.SenderPreferences,
		EdgeEvidenceTruncated: thread.EdgeEvidenceTruncated,
		Messages:              messages, Edges: edges,
	}, nil
}

func writeThreadHeaders(writer io.Writer, message maildomain.Message) error {
	if _, err := fmt.Fprintf(writer, "\nheaders\nmessage=%s\n", message.ID); err != nil {
		return err
	}
	for _, field := range publicHeaders(message.Content) {
		if _, err := fmt.Fprintf(writer, "%s: %s\n", field.Key, field.Value); err != nil {
			return err
		}
	}
	return nil
}
