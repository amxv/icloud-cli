package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type MailService interface {
	MailDraftCreate(context.Context, string, string, maildomain.SendRequest) (account.Account, maildomain.Draft, int64, error)
	MailDraftUpdate(context.Context, string, string, string, maildomain.SendRequest) (account.Account, maildomain.Draft, int64, error)
	MailDraftList(context.Context, string, string, int) (account.Account, []maildomain.Draft, bool, int64, error)
	MailDraftListPage(context.Context, string, string, string, int) (account.Account, []maildomain.Draft, bool, string, int64, error)
	MailDraftShow(context.Context, string, string, string) (account.Account, maildomain.Draft, maildomain.ContentSource, int64, error)
	MailDraftSync(context.Context, string, string, string, string, int) (account.Account, maildomain.DraftSyncSummary, []maildomain.Draft, int64, error)
	MailDraftSendPreview(context.Context, string, string, string) (account.Account, maildomain.Draft, maildomain.SendPreview, int64, error)
	MailDraftSend(context.Context, string, string, string) (account.Account, maildomain.Draft, maildomain.OutboundAttempt, bool, int64, error)
	MailDraftDelete(context.Context, string, string, string) (account.Account, maildomain.Draft, int, int64, error)
	MailWatch(context.Context, string, string, maildomain.WatchOptions, func(maildomain.WatchEvent) error) error
	MailSync(context.Context, string, string, string, int) (account.Account, []folder.Folder, []maildomain.SyncSummary, int64, error)
	MailSyncMode(context.Context, string, string, string, int, bool) (account.Account, []folder.Folder, []maildomain.SyncSummary, int64, error)
	MailSyncAll(context.Context, string, string, int) (account.Account, []folder.Folder, []maildomain.SyncSummary, int64, error)
	MailSyncAllMode(context.Context, string, string, int, bool) (account.Account, []folder.Folder, []maildomain.SyncSummary, int64, error)
	MailBackfill(context.Context, string, string, string, *time.Time, *time.Time, int) (account.Account, folder.Folder, maildomain.BackfillSummary, int64, error)
	MailRebaseline(context.Context, string, string, string, bool) (account.Account, folder.Folder, maildomain.RebaselineSummary, int64, error)
	MailRepair(context.Context, string, string, maildomain.RepairOptions) (account.Account, maildomain.RepairSummary, int64, error)
	MailInbox(context.Context, string, string, string, int) (account.Account, folder.Folder, []maildomain.Message, bool, int64, error)
	MailSyncStatus(context.Context, string, string, string) (account.Account, folder.Folder, maildomain.Checkpoint, int64, error)
	MailSyncCheckpoints(context.Context, string, string, int) (account.Account, []maildomain.Checkpoint, bool, int64, error)
	MailMessage(context.Context, string, string, string) (account.Account, maildomain.Message, maildomain.ContentSource, int64, error)
	MailLatest(context.Context, string, string, int) (account.Account, []maildomain.Thread, bool, int64, error)
	MailThread(context.Context, string, string, string, maildomain.ThreadReadOptions) (account.Account, maildomain.ThreadPage, []maildomain.ContentSource, int64, error)
	MailAttachments(context.Context, string, string, string, int) (account.Account, maildomain.AttachmentTarget, []maildomain.Attachment, bool, int64, error)
	MailAttachment(context.Context, string, string, string) (account.Account, maildomain.Attachment, maildomain.AttachmentSource, int64, error)
	MailUpdateFlag(context.Context, string, string, string, string, maildomain.FlagMutation) (account.Account, folder.Folder, maildomain.Message, maildomain.Location, int64, error)
	MailSearch(context.Context, string, string, string, maildomain.SearchOptions) (account.Account, folder.Folder, maildomain.SearchPage, int64, error)
	MailSearchRepair(context.Context, string, string, int) (account.Account, maildomain.SearchRepairSummary, int64, error)
	MailRemoteSearch(context.Context, string, string, string, bool, maildomain.SearchOptions) (account.Account, maildomain.RemoteSearchPage, int64, error)
	MailRemoteSummary(context.Context, string, string, string) (account.Account, maildomain.RemoteSearchHit, int64, error)
	MailHydrate(context.Context, string, string, []string) (account.Account, []maildomain.HydrateOutcome, int64, error)
	MailSendPreview(context.Context, string, string, maildomain.SendRequest) (account.Account, maildomain.SendPreview, int64, error)
	MailSend(context.Context, string, string, maildomain.SendRequest) (account.Account, maildomain.OutboundAttempt, bool, int64, error)
	MailReplyPreview(context.Context, string, string, maildomain.ReplyRequest) (account.Account, maildomain.ReplyPreview, int64, error)
	MailReply(context.Context, string, string, maildomain.ReplyRequest) (account.Account, maildomain.OutboundAttempt, bool, int64, error)
	MailForwardPreview(context.Context, string, string, maildomain.ForwardRequest) (account.Account, maildomain.ForwardPreview, int64, error)
	MailForward(context.Context, string, string, maildomain.ForwardRequest) (account.Account, maildomain.OutboundAttempt, bool, int64, error)
	MailOutboundStatus(context.Context, string, string, int) (account.Account, []maildomain.OutboundAttempt, bool, int64, error)
	MailLocationPreview(context.Context, string, string, maildomain.LocationMutationRequest) (account.Account, maildomain.LocationMutationPlan, int64, error)
	MailMutateLocation(context.Context, string, string, maildomain.LocationMutationRequest) (account.Account, maildomain.LocationMutationPlan, maildomain.Message, maildomain.LocationMutation, bool, int64, error)
	MailLocationMutationStatus(context.Context, string, string, int) (account.Account, []maildomain.LocationMutation, bool, int64, error)
}

// HydrateItemResult is the ordered truth record for one attempted remote
// hydration input. Item-level failures live beside their input identity and
// durable canonical state instead of being separated into success/failure
// slices that lose ordering and accounting information.
type HydrateItemResult struct {
	Index          int
	Reference      string
	Outcome        *maildomain.HydrateOutcome
	Error          *AppError
	CommittedState string
}

type orderedHydrateService interface {
	MailHydrateItems(context.Context, string, string, []string, bool) (account.Account, []HydrateItemResult, int64, error)
}

type mailSyncData struct {
	AccountID     string                   `json:"account_id"`
	Summary       maildomain.SyncSummary   `json:"summary"`
	Summaries     []maildomain.SyncSummary `json:"summaries"`
	Count         int                      `json:"count"`
	SchemaVersion int64                    `json:"schema_version"`
}

type mailHydrateData struct {
	AccountID     string                      `json:"account_id"`
	Outcomes      []maildomain.HydrateOutcome `json:"outcomes"`
	Count         int                         `json:"count"`
	SchemaVersion int64                       `json:"schema_version"`
}

type mailInboxData struct {
	AccountID     string                     `json:"account_id"`
	FolderID      string                     `json:"folder_id"`
	Messages      []publicMessage            `json:"messages"`
	Count         int                        `json:"count"`
	More          bool                       `json:"more,omitempty"`
	NextCursor    string                     `json:"next_cursor,omitempty"`
	Query         maildomain.SearchQueryMeta `json:"query"`
	SchemaVersion int64                      `json:"schema_version"`
}

type mailSyncStatusData struct {
	AccountID     string                `json:"account_id"`
	Folder        folder.Folder         `json:"folder"`
	Checkpoint    maildomain.Checkpoint `json:"checkpoint"`
	SchemaVersion int64                 `json:"schema_version"`
}

type mailCheckpointData struct {
	AccountID     string                  `json:"account_id"`
	Checkpoints   []maildomain.Checkpoint `json:"checkpoints"`
	Count         int                     `json:"count"`
	More          bool                    `json:"more,omitempty"`
	SchemaVersion int64                   `json:"schema_version"`
}

type mailBackfillData struct {
	AccountID     string                     `json:"account_id"`
	Folder        folder.Folder              `json:"folder"`
	Summary       maildomain.BackfillSummary `json:"summary"`
	SchemaVersion int64                      `json:"schema_version"`
}

type mailRebaselineData struct {
	AccountID     string                       `json:"account_id"`
	Folder        folder.Folder                `json:"folder"`
	Summary       maildomain.RebaselineSummary `json:"summary"`
	SchemaVersion int64                        `json:"schema_version"`
}

type mailRepairData struct {
	Summary       maildomain.RepairSummary `json:"summary"`
	SchemaVersion int64                    `json:"schema_version"`
}

type publicMessage struct {
	ID                string                        `json:"id"`
	AccountID         string                        `json:"account_id"`
	RFCMessageID      string                        `json:"rfc_message_id,omitempty"`
	InReplyTo         string                        `json:"in_reply_to,omitempty"`
	References        []string                      `json:"references,omitempty"`
	Direction         maildomain.Direction          `json:"direction"`
	From              maildomain.Address            `json:"from"`
	ReplyTo           *maildomain.Address           `json:"reply_to,omitempty"`
	To                []maildomain.Address          `json:"to,omitempty"`
	CC                []maildomain.Address          `json:"cc,omitempty"`
	Subject           string                        `json:"subject,omitempty"`
	Snippet           string                        `json:"snippet,omitempty"`
	HeaderDate        *time.Time                    `json:"header_date,omitempty"`
	InternalDate      *time.Time                    `json:"internal_date,omitempty"`
	SortAt            *time.Time                    `json:"sort_at,omitempty"`
	DateSource        maildomain.DateSource         `json:"date_source"`
	AttachmentCount   int                           `json:"attachment_count,omitempty"`
	Unread            bool                          `json:"unread"`
	Flagged           bool                          `json:"flagged"`
	CreatedAt         time.Time                     `json:"created_at"`
	UpdatedAt         time.Time                     `json:"updated_at"`
	Locations         []publicLocation              `json:"locations,omitempty"`
	ContentLocationID string                        `json:"content_location_id,omitempty"`
	Identity          []maildomain.IdentityEvidence `json:"identity,omitempty"`
	DedupeConflicts   []maildomain.DedupeConflict   `json:"dedupe_conflicts,omitempty"`
}

type publicLocation struct {
	ID             string                   `json:"id"`
	FolderID       string                   `json:"folder_id"`
	FolderKind     string                   `json:"folder_kind,omitempty"`
	UIDValidity    uint32                   `json:"uid_validity"`
	UID            uint32                   `json:"uid"`
	Flags          maildomain.LocationFlags `json:"flags"`
	ModSequence    uint64                   `json:"mod_sequence,omitempty"`
	FlagsCheckedAt *time.Time               `json:"flags_checked_at,omitempty"`
	InternalDate   *time.Time               `json:"internal_date,omitempty"`
	RFC822Size     int64                    `json:"rfc822_size,omitempty"`
	Present        bool                     `json:"present"`
}

func newMailSyncCommand(state *executionState) *cobra.Command {
	var jsonOutput, jsonlOutput, continueOnError bool
	var limit int
	var folderSelector string
	var allFolders bool
	var hydrate []string
	var reconcileOutbound bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "sync",
		Short:   "Fetch and atomically commit bounded pages from enabled mail folders",
		Args:    cobra.NoArgs,
		Example: "  icloud mail sync\n  icloud mail sync --folder sent\n  icloud mail sync --hydrate rem_…\n  icloud mail sync --all-folders --continue-on-error --jsonl\n  icloud mail sync --limit 50 --json\n  icloud mail sync status --folder sent",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, JSONL: jsonlOutput}, OutputSupport{JSON: true, JSONL: true}); err != nil {
				return err
			}
			var jsonRequest mailSyncJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest, "folder", "all-folders", "hydrate", "reconcile-outbound", "continue-on-error", "limit"); err != nil {
				return err
			} else if used {
				folderSelector, allFolders, hydrate, reconcileOutbound, continueOnError = jsonRequest.Folder, jsonRequest.AllFolders, append([]string(nil), jsonRequest.Hydrate...), jsonRequest.ReconcileOutbound, jsonRequest.ContinueOnError
				if jsonRequest.Limit != nil {
					limit = *jsonRequest.Limit
				}
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			if reconcileOutbound {
				if len(hydrate) > 0 || strings.TrimSpace(folderSelector) != "" || allFolders {
					return NewAppError(ErrorUsage, "--reconcile-outbound cannot be combined with --folder, --all-folders, or --hydrate", false)
				}
				folderSelector = string(folder.KindSent)
			}
			if len(hydrate) > 0 {
				if strings.TrimSpace(folderSelector) != "" || allFolders {
					return NewAppError(ErrorUsage, "--hydrate cannot be combined with --folder or --all-folders", false)
				}
				if jsonlOutput {
					return runHydrateJSONL(cmd, state, hydrate, continueOnError)
				}
				var accountValue account.Account
				var outcomes []maildomain.HydrateOutcome
				var schemaVersion int64
				var operationErr error
				hydrateFailures := make([]bulkItemFailure, 0)
				if continueOnError {
					seen := map[string]struct{}{}
					failures := make([]map[string]any, 0)
					var firstFailure *AppError
					for _, reference := range hydrate {
						reference = strings.TrimSpace(reference)
						if reference == "" {
							continue
						}
						if _, duplicate := seen[reference]; duplicate {
							continue
						}
						seen[reference] = struct{}{}
						value, itemOutcomes, version, itemErr := state.mail.MailHydrate(cmd.Context(), state.global.Home, state.global.Account, []string{reference})
						if value.ID != "" {
							accountValue = value
						}
						if version != 0 {
							schemaVersion = version
						}
						outcomes = append(outcomes, itemOutcomes...)
						if itemErr != nil {
							normalized := NormalizeError(itemErr)
							if firstFailure == nil {
								firstFailure = normalized
							}
							failures = append(failures, map[string]any{"reference": reference, "error": normalized})
							hydrateFailures = append(hydrateFailures, bulkItemFailure{Reference: reference, Error: normalized})
						}
					}
					if firstFailure != nil {
						copyErr := *firstFailure
						copyErr.Message = "remote hydration completed with per-item failures; committed results are reported in error details"
						copyErr.Details = map[string]any{"requested": len(seen), "completed": outcomes, "completed_count": len(outcomes), "failures": failures, "partial": len(outcomes) > 0, "mode": "continue-on-error"}
						operationErr = &copyErr
					}
				} else {
					accountValue, outcomes, schemaVersion, operationErr = state.mail.MailHydrate(cmd.Context(), state.global.Home, state.global.Account, hydrate)
					if operationErr != nil {
						normalized := NormalizeError(operationErr)
						reference, _ := normalized.Details["failed_reference"].(string)
						hydrateFailures = append(hydrateFailures, bulkItemFailure{Reference: reference, Error: normalized})
					}
				}
				if jsonOutput {
					if operationErr != nil {
						return operationErr
					}
					return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailHydrateData{
						AccountID: accountValue.ID, Outcomes: outcomes, Count: len(outcomes), SchemaVersion: schemaVersion,
					}, Meta: EnvelopeMeta{RequestID: state.requestID}})
				}
				renderer := NewCompactTextRenderer(state.stdout)
				for _, outcome := range outcomes {
					if err := renderer.Record("mail-hydrate", outcome.MessageID,
						Field("location_id", outcome.LocationID),
						Field("status", hydrateOutcomeStatus(outcome)),
					); err != nil {
						return err
					}
				}
				if err := renderer.Summary("hydrate-results",
					Field("account", accountValue.ID),
					Field("count", fmt.Sprintf("%d", len(outcomes))),
					Field("partial", operationErr != nil && len(outcomes) > 0),
				); err != nil {
					return err
				}
				return operationErr
			}
			if allFolders && strings.TrimSpace(folderSelector) != "" {
				return NewAppError(ErrorUsage, "--all-folders cannot be combined with --folder", false)
			}
			var (
				accountValue  account.Account
				summaries     []maildomain.SyncSummary
				schemaVersion int64
				err           error
			)
			if allFolders {
				accountValue, _, summaries, schemaVersion, err = state.mail.MailSyncAllMode(cmd.Context(), state.global.Home, state.global.Account, limit, continueOnError)
			} else {
				accountValue, _, summaries, schemaVersion, err = state.mail.MailSyncMode(cmd.Context(), state.global.Home, state.global.Account, folderSelector, limit, continueOnError)
			}
			if len(summaries) == 0 && err == nil {
				return NewAppError(ErrorInternal, "mail synchronization returned no folder summaries", false)
			}
			if jsonOutput {
				if err != nil {
					return err
				}
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSyncData{
					AccountID: accountValue.ID, Summary: summaries[0], Summaries: summaries,
					Count: len(summaries), SchemaVersion: schemaVersion,
				}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			if jsonlOutput {
				return renderSyncJSONL(state, accountValue.ID, summaries, err, continueOnError)
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, summary := range summaries {
				if err := renderer.Record("mail-sync", summary.FolderID,
					Field("account", summary.AccountID),
					Field("folder", summary.FolderName),
					Field("fetched", fmt.Sprintf("%d", summary.Fetched)),
					Field("created", fmt.Sprintf("%d", summary.Created)),
					Field("updated", fmt.Sprintf("%d", summary.Updated)),
					Field("reconciled", fmt.Sprintf("%d", summary.Reconciled)),
					Field("state_changes", fmt.Sprintf("%d", summary.StateChanges)),
					Field("identity_merges", summary.IdentityMerges),
					Field("dedupe_conflicts", summary.DedupeConflicts),
					Field("last_uid", summary.LastUID),
					Field("more", fmt.Sprintf("%t", summary.MoreAvailable)),
					Field("bootstrap", fmt.Sprintf("%t", summary.Bootstrap)),
					Field("outbound_confirmed", outboundReconcileValue(summary.OutboundReconcile, "confirmed")),
					Field("outbound_resolved_unknown", outboundReconcileValue(summary.OutboundReconcile, "resolved_unknown")),
					Field("outbound_ambiguous", outboundReconcileValue(summary.OutboundReconcile, "ambiguous")),
					Field("outbound_unmatched", outboundReconcileValue(summary.OutboundReconcile, "unmatched")),
				); err != nil {
					return err
				}
			}
			return err
		},
	}
	command.Flags().StringVar(&folderSelector, "folder", "", "Sync one enabled folder by kind, ID, name, or wire name instead of all enabled folders")
	command.Flags().BoolVar(&allFolders, "all-folders", false, "Explicitly sync every current selectable folder, including locally disabled folders, with bounded recent bootstrap")
	command.Flags().StringSliceVar(&hydrate, "hydrate", nil, "Cache one or more comma-separated rem_… references without advancing folder checkpoints")
	command.Flags().BoolVar(&reconcileOutbound, "reconcile-outbound", false, "Sync Sent and reconcile recent sent/unknown attempts by exact Message-ID evidence")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum messages to fetch and commit per folder (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&jsonlOutput, "jsonl", false, "Emit typed per-folder or per-hydration-item JSONL events plus a completed event")
	command.Flags().BoolVar(&continueOnError, "continue-on-error", false, "Continue independent hydration items or folders after an error; committed earlier work is never rolled back")
	bindJSONInput(command, &input)
	command.AddCommand(newMailSyncStatusCommand(state))
	command.AddCommand(newMailSyncCheckpointsCommand(state))
	command.AddCommand(newMailSyncBackfillCommand(state))
	command.AddCommand(newMailSyncRebaselineCommand(state))
	command.AddCommand(newMailSyncRepairCommand(state))
	return command
}

func hydrateOutcomeStatus(outcome maildomain.HydrateOutcome) string {
	if outcome.Created {
		return "created"
	}
	if outcome.Updated {
		return "updated"
	}
	return "cached"
}

func bulkFailureMode(continueOnError bool) string {
	if continueOnError {
		return "continue-on-error"
	}
	return "stop-on-error"
}

type bulkItemFailure struct {
	Reference  string
	FolderID   string
	FolderName string
	Error      *AppError
}

type bulkItemRecord struct {
	Index       int
	Reference   string
	FolderID    string
	FolderName  string
	Status      string
	CommitState string
	Result      any
	Error       *AppError
}

func runHydrateJSONL(cmd *cobra.Command, state *executionState, references []string, continueOnError bool) error {
	if service, ok := state.mail.(orderedHydrateService); ok {
		accountValue, items, _, err := service.MailHydrateItems(cmd.Context(), state.global.Home, state.global.Account, references, continueOnError)
		if err != nil {
			return err
		}
		records := make([]bulkItemRecord, 0, len(items))
		var terminal error
		for _, item := range items {
			record := bulkItemRecord{
				Index: item.Index, Reference: item.Reference, Status: "error",
				CommitState: item.CommittedState, Error: item.Error,
			}
			if item.Outcome != nil {
				record.Status = hydrateOutcomeStatus(*item.Outcome)
				record.Result = *item.Outcome
			}
			if item.Error != nil {
				record.Status = "error"
				if terminal == nil {
					terminal = item.Error
				}
			}
			records = append(records, record)
		}
		if err := writeBulkJSONLRecords(state, accountValue.ID, records, len(references), bulkFailureMode(continueOnError)); err != nil {
			return err
		}
		return markJSONLHandled(terminal)
	}

	records := make([]bulkItemRecord, 0, len(references))
	seen := make(map[string]struct{}, len(references))
	accountID := ""
	var terminal error
	for index, raw := range references {
		reference := strings.TrimSpace(raw)
		if reference == "" {
			continue
		}
		if _, duplicate := seen[reference]; duplicate {
			continue
		}
		seen[reference] = struct{}{}
		accountValue, outcomes, _, itemErr := state.mail.MailHydrate(cmd.Context(), state.global.Home, state.global.Account, []string{reference})
		if accountValue.ID != "" {
			accountID = accountValue.ID
		}
		record := bulkItemRecord{Index: index, Reference: reference, Status: "error", CommitState: "unknown"}
		if len(outcomes) == 1 {
			record.Status = hydrateOutcomeStatus(outcomes[0])
			record.Result = outcomes[0]
			if outcomes[0].Created || outcomes[0].Updated {
				record.CommitState = "committed"
			} else {
				record.CommitState = "already_committed"
			}
		}
		if itemErr != nil {
			record.Error = NormalizeError(itemErr)
			if len(outcomes) == 0 {
				record.Status = "error"
			}
			if terminal == nil {
				terminal = itemErr
			}
		}
		records = append(records, record)
		if itemErr != nil && !continueOnError {
			break
		}
	}
	if err := writeBulkJSONLRecords(state, accountID, records, len(references), bulkFailureMode(continueOnError)); err != nil {
		return err
	}
	return markJSONLHandled(terminal)
}

func renderSyncJSONL(state *executionState, accountID string, summaries []maildomain.SyncSummary, operationErr error, continueOnError bool) error {
	records := syncBulkItemRecords(summaries, operationErr)
	if operationErr != nil && len(records) == 0 {
		// No folder attempt exists to account for this error (for example account,
		// credential, or local-state resolution failed before scheduling). Let the
		// root emit the ordinary JSONL error event instead of fabricating a
		// successful-looking completed bulk stream with failed=0.
		return operationErr
	}
	requested := len(records)
	if operationErr != nil {
		if value, ok := NormalizeError(operationErr).Details["requested"].(int); ok {
			requested = value
		}
	}
	if err := writeBulkJSONLRecords(state, accountID, records, requested, bulkFailureMode(continueOnError)); err != nil {
		return err
	}
	return markJSONLHandled(operationErr)
}

func syncBulkItemRecords(summaries []maildomain.SyncSummary, operationErr error) []bulkItemRecord {
	if operationErr == nil {
		values := make([]bulkItemRecord, 0, len(summaries))
		for index, summary := range summaries {
			values = append(values, bulkItemRecord{Index: index, FolderID: summary.FolderID, FolderName: summary.FolderName, Status: "committed", CommitState: "committed", Result: summary})
		}
		return values
	}
	raw, ok := NormalizeError(operationErr).Details["items"].([]map[string]any)
	if !ok {
		return nil
	}
	values := make([]bulkItemRecord, 0, len(raw))
	for _, item := range raw {
		record := bulkItemRecord{Status: "error", CommitState: "not_committed"}
		record.Index, _ = item["index"].(int)
		record.FolderID, _ = item["folder_id"].(string)
		record.FolderName, _ = item["folder_name"].(string)
		committed, _ := item["committed"].(bool)
		if committed {
			record.CommitState = "committed"
		}
		if summary, ok := item["summary"].(maildomain.SyncSummary); ok {
			record.Result = summary
			if item["error"] == nil {
				record.Status = "committed"
			}
		}
		if appErr, ok := item["error"].(*AppError); ok {
			record.Error = appErr
		} else if nested, ok := item["error"].(error); ok {
			record.Error = NormalizeError(nested)
		}
		values = append(values, record)
	}
	return values
}

func writeBulkJSONLRecords(state *executionState, accountID string, records []bulkItemRecord, requested int, mode string) error {
	sequence := uint64(0)
	completed, failed, committed := 0, 0, 0
	for _, record := range records {
		sequence++
		payload := map[string]any{"index": record.Index, "status": record.Status, "committed_state": record.CommitState}
		if record.Reference != "" {
			payload["reference"] = record.Reference
		}
		if record.FolderID != "" {
			payload["folder_id"] = record.FolderID
		}
		if record.FolderName != "" {
			payload["folder_name"] = record.FolderName
		}
		if record.Result != nil {
			payload["result"] = record.Result
		}
		if record.CommitState == "committed" || record.CommitState == "already_committed" {
			committed++
		}
		if record.Error != nil {
			failed++
		} else {
			completed++
		}
		if err := WriteJSONLEvent(state.stdout, JSONLEvent{Event: "item", Sequence: sequence, AccountID: accountID, Timestamp: time.Now().UTC(), Payload: payload, Error: record.Error}); err != nil {
			return err
		}
	}
	sequence++
	return WriteJSONLEvent(state.stdout, JSONLEvent{Event: "completed", Sequence: sequence, AccountID: accountID, Timestamp: time.Now().UTC(), Payload: map[string]any{
		"requested": requested, "attempted": len(records), "completed": completed, "failed": failed, "committed": committed,
		"partial": completed > 0 && failed > 0, "mode": mode,
	}})
}

func bulkFailuresFromError(err error) []bulkItemFailure {
	if err == nil {
		return nil
	}
	normalized := NormalizeError(err)
	raw, ok := normalized.Details["failures"].([]map[string]any)
	if !ok {
		return nil
	}
	values := make([]bulkItemFailure, 0, len(raw))
	for _, item := range raw {
		failure := bulkItemFailure{}
		failure.FolderID, _ = item["folder_id"].(string)
		failure.FolderName, _ = item["folder_name"].(string)
		failure.Reference, _ = item["reference"].(string)
		if appErr, ok := item["error"].(*AppError); ok {
			failure.Error = appErr
		} else if nestedErr, ok := item["error"].(error); ok {
			failure.Error = NormalizeError(nestedErr)
		} else {
			failure.Error = normalized
		}
		values = append(values, failure)
	}
	return values
}

func outboundReconcileValue(summary *maildomain.OutboundReconcileSummary, field string) any {
	if summary == nil {
		return nil
	}
	switch field {
	case "confirmed":
		return summary.Confirmed
	case "resolved_unknown":
		return summary.ResolvedUnknown
	case "ambiguous":
		return summary.Ambiguous
	case "unmatched":
		return summary.Unmatched
	default:
		return nil
	}
}

func newMailInboxCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var limit int
	var folderSelector string
	var filters mailSearchFlags
	command := &cobra.Command{
		Use:     "inbox",
		Short:   "Read and filter a bounded page from a synchronized mail folder",
		Args:    cobra.NoArgs,
		Example: "  icloud mail inbox\n  icloud mail inbox --unread --from billing@example.com\n  icloud mail inbox --query invoice --cursor cur_…\n  icloud mail inbox --folder sent --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			options, err := filters.options(filters.Query, maildomain.SearchMessages, limit)
			if err != nil {
				return err
			}
			accountValue, folderValue, page, schemaVersion, err := state.mail.MailSearch(cmd.Context(), state.global.Home, state.global.Account, folderSelector, options)
			if err != nil {
				return err
			}
			messages := make([]maildomain.Message, 0, len(page.Hits))
			for _, hit := range page.Hits {
				if hit.Message != nil {
					messages = append(messages, *hit.Message)
				}
			}
			if jsonOutput {
				public := make([]publicMessage, 0, len(messages))
				for _, message := range messages {
					public = append(public, sanitizeMessage(message))
				}
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailInboxData{AccountID: accountValue.ID, FolderID: folderValue.ID, Messages: public, Count: len(public), More: page.More, NextCursor: page.NextCursor, Query: page.Query, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			filtered := mailSearchFlagsActive(filters)
			for index, message := range messages {
				var renderErr error
				if filtered {
					renderErr = renderSearchHit(renderer, page.Hits[index])
				} else {
					renderErr = renderInboxMessage(renderer, message)
				}
				if renderErr != nil {
					return renderErr
				}
			}
			summaryKind := "mailbox"
			if folderValue.Kind == folder.KindInbox {
				return renderer.Summary("inbox", Field("account", accountValue.ID), Field("count", fmt.Sprintf("%d", len(messages))), Field("more", fmt.Sprintf("%t", page.More)), Field("next_cursor", page.NextCursor))
			}
			return renderer.Summary(summaryKind, Field("account", accountValue.ID), Field("folder", folderValue.Name), Field("folder_id", folderValue.ID), Field("count", fmt.Sprintf("%d", len(messages))), Field("more", fmt.Sprintf("%t", page.More)), Field("next_cursor", page.NextCursor))
		},
	}
	command.Flags().StringVar(&folderSelector, "folder", string(folder.KindInbox), "Folder kind, ID, name, or wire name")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum local folder messages to return (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	addMailSearchFlags(command, &filters, false, true)
	return command
}

func newMailSyncStatusCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var folderSelector string
	command := &cobra.Command{
		Use:     "status",
		Short:   "Show one local folder synchronization frontier",
		Args:    cobra.NoArgs,
		Example: "  icloud mail sync status\n  icloud mail sync status --folder sent --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			accountValue, folderValue, checkpoint, schemaVersion, err := state.mail.MailSyncStatus(cmd.Context(), state.global.Home, state.global.Account, folderSelector)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailSyncStatusData{AccountID: accountValue.ID, Folder: folderValue, Checkpoint: checkpoint, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderCheckpoint(NewCompactTextRenderer(state.stdout), "sync-status", accountValue.ID, folderValue.Name, checkpoint)
		},
	}
	command.Flags().StringVar(&folderSelector, "folder", string(folder.KindInbox), "Folder kind, ID, name, or wire name")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func newMailSyncCheckpointsCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var limit int
	command := &cobra.Command{
		Use:     "checkpoints",
		Short:   "List bounded local synchronization checkpoints",
		Args:    cobra.NoArgs,
		Example: "  icloud mail sync checkpoints\n  icloud mail sync checkpoints --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			accountValue, values, more, schemaVersion, err := state.mail.MailSyncCheckpoints(cmd.Context(), state.global.Home, state.global.Account, limit)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailCheckpointData{AccountID: accountValue.ID, Checkpoints: values, Count: len(values), More: more, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, checkpoint := range values {
				if err := renderCheckpoint(renderer, "checkpoint", accountValue.ID, "", checkpoint); err != nil {
					return err
				}
			}
			return renderer.Summary("checkpoints", Field("account", accountValue.ID), Field("count", fmt.Sprintf("%d", len(values))), Field("more", fmt.Sprintf("%t", more)))
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum checkpoints to return (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func newMailSyncBackfillCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var folderSelector string
	var afterValue string
	var beforeValue string
	var limit int
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "backfill",
		Short:   "Resume one bounded historical page without moving the new-mail frontier",
		Args:    cobra.NoArgs,
		Example: "  icloud mail sync backfill --folder inbox\n  icloud mail sync backfill --folder Receipts --after 2025-01-01 --before 2026-01-01 --limit 50\n  icloud mail sync backfill --folder fld_… --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			var jsonRequest mailBackfillJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest, "folder", "after", "before", "limit"); err != nil {
				return err
			} else if used {
				folderSelector, afterValue, beforeValue = jsonRequest.Folder, jsonRequest.After, jsonRequest.Before
				if jsonRequest.Limit != nil {
					limit = *jsonRequest.Limit
				}
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			after, err := parseBackfillDate(afterValue)
			if err != nil {
				return NewAppError(ErrorUsage, "--after must be a UTC date in YYYY-MM-DD form", false)
			}
			before, err := parseBackfillDate(beforeValue)
			if err != nil {
				return NewAppError(ErrorUsage, "--before must be a UTC date in YYYY-MM-DD form", false)
			}
			if after != nil && before != nil && !after.Before(*before) {
				return NewAppError(ErrorUsage, "--after must precede --before", false)
			}
			accountValue, folderValue, summary, schemaVersion, err := state.mail.MailBackfill(cmd.Context(), state.global.Home, state.global.Account, folderSelector, after, before, limit)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailBackfillData{AccountID: accountValue.ID, Folder: folderValue, Summary: summary, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("mail-backfill", summary.Frontier.ID,
				Field("account", accountValue.ID), Field("folder", folderValue.Name), Field("folder_id", folderValue.ID),
				Field("fetched", summary.Fetched), Field("created", summary.Created), Field("updated", summary.Updated),
				Field("before_uid", summary.StartBeforeUID), Field("next_before_uid", summary.NextBeforeUID),
				Field("complete", yesNo(summary.Complete)), Field("after", after), Field("before", before),
			)
		},
	}
	command.Flags().StringVar(&folderSelector, "folder", string(folder.KindInbox), "Folder kind, ID, name, or wire name")
	command.Flags().StringVar(&afterValue, "after", "", "Include messages on or after this UTC date (YYYY-MM-DD)")
	command.Flags().StringVar(&beforeValue, "before", "", "Include messages before this UTC date (YYYY-MM-DD)")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum historical messages to fetch and commit in this page (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailSyncRebaselineCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var folderSelector string
	var currentTail bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "rebaseline",
		Short:   "Move one forward sync frontier to the verified current iCloud tail without deleting cache",
		Args:    cobra.NoArgs,
		Example: "  icloud mail sync rebaseline --folder inbox --current-tail\n  icloud mail sync rebaseline --folder fld_… --current-tail --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			var jsonRequest mailRebaselineJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest, "folder", "current-tail"); err != nil {
				return err
			} else if used {
				folderSelector, currentTail = jsonRequest.Folder, jsonRequest.CurrentTail
			}
			if !currentTail {
				return NewAppError(ErrorSafety, "rebaseline requires --current-tail", false)
			}
			accountValue, folderValue, summary, schemaVersion, err := state.mail.MailRebaseline(cmd.Context(), state.global.Home, state.global.Account, folderSelector, currentTail)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailRebaselineData{AccountID: accountValue.ID, Folder: folderValue, Summary: summary, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("mail-rebaseline", summary.After.ID,
				Field("account", accountValue.ID), Field("folder", folderValue.Name), Field("folder_id", folderValue.ID),
				Field("uid_validity", summary.UIDValidity), Field("before_committed", summary.PreviousCommitted),
				Field("before_scanned", summary.PreviousScanned), Field("before_state", summary.PreviousState),
				Field("after_committed", summary.After.LastCommittedUID), Field("after_scanned", summary.After.LastScannedUID),
				Field("cached_kept", summary.CachedMessagesKept), Field("deleted", yesNo(false)),
			)
		},
	}
	command.Flags().StringVar(&folderSelector, "folder", string(folder.KindInbox), "Folder kind, ID, name, or wire name")
	command.Flags().BoolVar(&currentTail, "current-tail", false, "Acknowledge moving only this forward frontier to UIDNEXT-1 observed now")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailSyncRepairCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var verbose bool
	var check bool
	var apply bool
	var cleanupOrphans bool
	var yes bool
	var limit int
	var input jsonInputOptions
	command := &cobra.Command{
		Use:     "repair",
		Short:   "Audit bounded local mail consistency and apply only deterministic repairs",
		Args:    cobra.NoArgs,
		Example: "  icloud mail sync repair --check\n  icloud mail sync repair --check --verbose\n  icloud mail sync repair --apply\n  icloud mail sync repair --apply --cleanup-orphans --yes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, Verbose: verbose}, OutputSupport{JSON: true, Verbose: true}); err != nil {
				return err
			}
			var jsonRequest mailRepairJSONRequest
			if used, err := readJSONInput(cmd, state, input, &jsonRequest, "check", "apply", "cleanup-orphans", "limit"); err != nil {
				return err
			} else if used {
				cleanupOrphans = jsonRequest.CleanupOrphans
				if jsonRequest.Limit != nil {
					limit = *jsonRequest.Limit
				}
				switch strings.ToLower(strings.TrimSpace(jsonRequest.Mode)) {
				case "", "check":
					check, apply = true, false
				case "apply":
					check, apply = false, true
				default:
					return NewAppError(ErrorUsage, "repair JSON mode must be check or apply", false)
				}
			}
			if check && apply {
				return NewAppError(ErrorUsage, "--check and --apply cannot be combined", false)
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			if cleanupOrphans && !apply {
				return NewAppError(ErrorUsage, "--cleanup-orphans requires --apply", false)
			}
			if cleanupOrphans && !yes {
				return NewAppError(ErrorSafety, "temporary blob cleanup requires --yes", false)
			}
			mode := maildomain.RepairCheck
			if apply {
				mode = maildomain.RepairApply
			}
			_, summary, schemaVersion, err := state.mail.MailRepair(cmd.Context(), state.global.Home, state.global.Account, maildomain.RepairOptions{Mode: mode, Limit: limit, CleanupOrphans: cleanupOrphans})
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: mailRepairData{Summary: summary, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			if verbose {
				for _, finding := range summary.Findings {
					if err := renderer.Record("repair-finding", finding.ID,
						Field("kind", finding.Kind), Field("folder_id", finding.FolderID), Field("action", finding.Action),
						Field("applicable", yesNo(finding.Applicable)), Field("applied", yesNo(finding.Applied)), Field("detail", Excerpt(finding.Detail, DefaultExcerptRunes)),
					); err != nil {
						return err
					}
				}
			}
			var projectionState, projectionPending, projectionAdvanced, projectionRepaired any
			if summary.ThreadProjectionState != "" && summary.ThreadProjectionState != "ready" {
				projectionState = summary.ThreadProjectionState
				if summary.ThreadProjectionPending > 0 {
					projectionPending = summary.ThreadProjectionPending
				}
			}
			if summary.ThreadProjectionAdvanced > 0 {
				projectionAdvanced = summary.ThreadProjectionAdvanced
			}
			if summary.ThreadProjectionRepaired > 0 {
				projectionRepaired = summary.ThreadProjectionRepaired
			}
			return renderer.Summary("mail-repair",
				Field("account", summary.AccountID), Field("mode", summary.Mode), Field("checked", fmt.Sprint(summary.Checked)), Field("changed", fmt.Sprint(summary.Changed)),
				Field("pending_search", fmt.Sprint(summary.PendingSearch)), Field("search_errors", fmt.Sprint(summary.FailedSearch)), Field("stale_search", fmt.Sprint(summary.StaleFTSRows)),
				Field("temp_orphans", fmt.Sprint(summary.OrphanTempFiles)), Field("blob_issues", fmt.Sprint(summary.CanonicalBlobIssues)), Field("dedupe_conflicts", fmt.Sprint(summary.DedupeConflicts)),
				Field("uid_repairs", fmt.Sprint(summary.RepairCheckpoints)), Field("frontier_problems", fmt.Sprint(summary.FrontierProblems)), Field("outbound_unknown", fmt.Sprint(summary.OutboundUnknown)),
				Field("thread_projection", projectionState), Field("thread_projection_pending", projectionPending),
				Field("thread_projection_advanced", projectionAdvanced), Field("thread_projection_repaired", projectionRepaired),
				Field("more", yesNo(summary.More)), Field("next", summary.NextAction),
			)
		},
	}
	command.Flags().BoolVar(&check, "check", false, "Audit only (the default when neither mode flag is supplied)")
	command.Flags().BoolVar(&apply, "apply", false, "Apply only deterministic repairs described by the audit")
	command.Flags().BoolVar(&cleanupOrphans, "cleanup-orphans", false, "With --apply --yes, delete bounded orphan files from the private staging directory")
	command.Flags().BoolVar(&yes, "yes", false, "Authorize destructive orphan staging-file cleanup")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum findings or repair candidates per bounded category (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&verbose, "verbose", false, "Print bounded per-finding detail before the compact summary")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope with bounded findings")
	bindJSONInput(command, &input)
	return command
}

func parseBackfillDate(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func renderInboxMessage(renderer *CompactTextRenderer, value maildomain.Message) error {
	from := value.From.Email
	if strings.TrimSpace(from) == "" {
		from = "unknown"
	}
	return renderer.Record("message", value.ID,
		Field("from", from),
		Field("subject", value.Subject),
		Field("at", value.SortAt),
		Field("unread", value.Unread),
		Field("flagged", value.Flagged),
		Field("attachments", value.AttachmentCount),
		Field("snippet", Excerpt(value.Snippet, DefaultExcerptRunes)),
	)
}

func renderCheckpoint(renderer *CompactTextRenderer, kind, accountID, folderName string, value maildomain.Checkpoint) error {
	state := value.State
	if state == "" {
		state = maildomain.CheckpointIdle
	}
	recordID := value.ID
	if recordID == "" {
		recordID = value.FolderID
	}
	return renderer.Record(kind, recordID,
		Field("account", accountID),
		Field("folder", folderName),
		Field("folder_id", value.FolderID),
		Field("state", state),
		Field("uid_validity", fmt.Sprintf("%d", value.UIDValidity)),
		Field("committed", fmt.Sprintf("%d", value.LastCommittedUID)),
		Field("scanned", fmt.Sprintf("%d", value.LastScannedUID)),
		Field("bootstrap", fmt.Sprintf("%t", value.BootstrapComplete)),
		Field("more", fmt.Sprintf("%t", value.MoreAvailable)),
		Field("last_error", value.LastErrorCode),
		Field("last_success", value.LastSuccessAt),
	)
}

func publicReplyTo(value maildomain.Address) *maildomain.Address {
	if strings.TrimSpace(value.Email) == "" && strings.TrimSpace(value.DisplayName) == "" {
		return nil
	}
	copy := value
	return &copy
}

func sanitizeMessage(value maildomain.Message) publicMessage {
	locations := make([]publicLocation, 0, len(value.Locations))
	for _, location := range value.Locations {
		locations = append(locations, publicLocation{
			ID: location.ID, FolderID: location.FolderID, FolderKind: location.FolderKind, UIDValidity: location.UIDValidity,
			UID: location.UID, Flags: location.Flags, ModSequence: location.ModSequence,
			FlagsCheckedAt: location.FlagsCheckedAt,
			InternalDate:   location.InternalDate, RFC822Size: location.RFC822Size, Present: location.Present,
		})
	}
	return publicMessage{
		ID: value.ID, AccountID: value.AccountID, RFCMessageID: value.RFCMessageID,
		InReplyTo: value.InReplyTo, References: append([]string(nil), value.References...),
		Direction: value.Direction, From: value.From, ReplyTo: publicReplyTo(value.ReplyTo),
		To: append([]maildomain.Address(nil), value.To...), CC: append([]maildomain.Address(nil), value.CC...),
		Subject: value.Subject, Snippet: value.Snippet, HeaderDate: value.HeaderDate,
		InternalDate: value.InternalDate, SortAt: value.SortAt, DateSource: value.DateSource,
		AttachmentCount: value.AttachmentCount, Unread: value.Unread, Flagged: value.Flagged,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, Locations: locations,
		ContentLocationID: value.ContentLocationID,
		Identity:          append([]maildomain.IdentityEvidence(nil), value.Identity...),
		DedupeConflicts:   append([]maildomain.DedupeConflict(nil), value.DedupeConflicts...),
	}
}

type unavailableMailService struct{}

func (unavailableMailService) MailDraftCreate(context.Context, string, string, maildomain.SendRequest) (account.Account, maildomain.Draft, int64, error) {
	return account.Account{}, maildomain.Draft{}, 0, NewAppError(ErrorConfiguration, "mail drafts are unavailable", false)
}
func (unavailableMailService) MailDraftUpdate(context.Context, string, string, string, maildomain.SendRequest) (account.Account, maildomain.Draft, int64, error) {
	return account.Account{}, maildomain.Draft{}, 0, NewAppError(ErrorConfiguration, "mail drafts are unavailable", false)
}
func (unavailableMailService) MailDraftList(context.Context, string, string, int) (account.Account, []maildomain.Draft, bool, int64, error) {
	return account.Account{}, nil, false, 0, NewAppError(ErrorConfiguration, "mail drafts are unavailable", false)
}
func (unavailableMailService) MailDraftListPage(context.Context, string, string, string, int) (account.Account, []maildomain.Draft, bool, string, int64, error) {
	return account.Account{}, nil, false, "", 0, NewAppError(ErrorConfiguration, "mail drafts are unavailable", false)
}
func (unavailableMailService) MailDraftShow(context.Context, string, string, string) (account.Account, maildomain.Draft, maildomain.ContentSource, int64, error) {
	return account.Account{}, maildomain.Draft{}, nil, 0, NewAppError(ErrorConfiguration, "mail drafts are unavailable", false)
}
func (unavailableMailService) MailDraftSync(context.Context, string, string, string, string, int) (account.Account, maildomain.DraftSyncSummary, []maildomain.Draft, int64, error) {
	return account.Account{}, maildomain.DraftSyncSummary{}, nil, 0, NewAppError(ErrorConfiguration, "mail drafts are unavailable", false)
}
func (unavailableMailService) MailDraftSendPreview(context.Context, string, string, string) (account.Account, maildomain.Draft, maildomain.SendPreview, int64, error) {
	return account.Account{}, maildomain.Draft{}, maildomain.SendPreview{}, 0, NewAppError(ErrorConfiguration, "mail drafts are unavailable", false)
}
func (unavailableMailService) MailDraftSend(context.Context, string, string, string) (account.Account, maildomain.Draft, maildomain.OutboundAttempt, bool, int64, error) {
	return account.Account{}, maildomain.Draft{}, maildomain.OutboundAttempt{}, false, 0, NewAppError(ErrorConfiguration, "mail drafts are unavailable", false)
}
func (unavailableMailService) MailDraftDelete(context.Context, string, string, string) (account.Account, maildomain.Draft, int, int64, error) {
	return account.Account{}, maildomain.Draft{}, 0, 0, NewAppError(ErrorConfiguration, "mail drafts are unavailable", false)
}

func (unavailableMailService) MailWatch(context.Context, string, string, maildomain.WatchOptions, func(maildomain.WatchEvent) error) error {
	return NewAppError(ErrorConfiguration, "mail watch is unavailable", false)
}

func (unavailableMailService) MailSync(context.Context, string, string, string, int) (account.Account, []folder.Folder, []maildomain.SyncSummary, int64, error) {
	return account.Account{}, nil, nil, 0, NewAppError(ErrorConfiguration, "mail synchronization is unavailable", false)
}
func (unavailableMailService) MailSyncMode(context.Context, string, string, string, int, bool) (account.Account, []folder.Folder, []maildomain.SyncSummary, int64, error) {
	return account.Account{}, nil, nil, 0, NewAppError(ErrorConfiguration, "mail synchronization is unavailable", false)
}

func (unavailableMailService) MailSyncAll(context.Context, string, string, int) (account.Account, []folder.Folder, []maildomain.SyncSummary, int64, error) {
	return account.Account{}, nil, nil, 0, NewAppError(ErrorConfiguration, "mail synchronization is unavailable", false)
}
func (unavailableMailService) MailSyncAllMode(context.Context, string, string, int, bool) (account.Account, []folder.Folder, []maildomain.SyncSummary, int64, error) {
	return account.Account{}, nil, nil, 0, NewAppError(ErrorConfiguration, "mail synchronization is unavailable", false)
}
func (unavailableMailService) MailBackfill(context.Context, string, string, string, *time.Time, *time.Time, int) (account.Account, folder.Folder, maildomain.BackfillSummary, int64, error) {
	return account.Account{}, folder.Folder{}, maildomain.BackfillSummary{}, 0, NewAppError(ErrorConfiguration, "historical mail backfill is unavailable", false)
}
func (unavailableMailService) MailRebaseline(context.Context, string, string, string, bool) (account.Account, folder.Folder, maildomain.RebaselineSummary, int64, error) {
	return account.Account{}, folder.Folder{}, maildomain.RebaselineSummary{}, 0, NewAppError(ErrorConfiguration, "mail synchronization rebaseline is unavailable", false)
}
func (unavailableMailService) MailRepair(context.Context, string, string, maildomain.RepairOptions) (account.Account, maildomain.RepairSummary, int64, error) {
	return account.Account{}, maildomain.RepairSummary{}, 0, NewAppError(ErrorConfiguration, "mail synchronization repair is unavailable", false)
}
func (unavailableMailService) MailInbox(context.Context, string, string, string, int) (account.Account, folder.Folder, []maildomain.Message, bool, int64, error) {
	return account.Account{}, folder.Folder{}, nil, false, 0, NewAppError(ErrorConfiguration, "local Inbox is unavailable", false)
}
func (unavailableMailService) MailSyncStatus(context.Context, string, string, string) (account.Account, folder.Folder, maildomain.Checkpoint, int64, error) {
	return account.Account{}, folder.Folder{}, maildomain.Checkpoint{}, 0, NewAppError(ErrorConfiguration, "mail synchronization status is unavailable", false)
}
func (unavailableMailService) MailSyncCheckpoints(context.Context, string, string, int) (account.Account, []maildomain.Checkpoint, bool, int64, error) {
	return account.Account{}, nil, false, 0, NewAppError(ErrorConfiguration, "mail synchronization checkpoints are unavailable", false)
}
func (unavailableMailService) MailMessage(context.Context, string, string, string) (account.Account, maildomain.Message, maildomain.ContentSource, int64, error) {
	return account.Account{}, maildomain.Message{}, nil, 0, NewAppError(ErrorConfiguration, "local message content is unavailable", false)
}
func (unavailableMailService) MailLatest(context.Context, string, string, int) (account.Account, []maildomain.Thread, bool, int64, error) {
	return account.Account{}, nil, false, 0, NewAppError(ErrorConfiguration, "local mail threads are unavailable", false)
}
func (unavailableMailService) MailThread(context.Context, string, string, string, maildomain.ThreadReadOptions) (account.Account, maildomain.ThreadPage, []maildomain.ContentSource, int64, error) {
	return account.Account{}, maildomain.ThreadPage{}, nil, 0, NewAppError(ErrorConfiguration, "local mail threads are unavailable", false)
}
func (unavailableMailService) MailAttachments(context.Context, string, string, string, int) (account.Account, maildomain.AttachmentTarget, []maildomain.Attachment, bool, int64, error) {
	return account.Account{}, maildomain.AttachmentTarget{}, nil, false, 0, NewAppError(ErrorConfiguration, "local attachments are unavailable", false)
}
func (unavailableMailService) MailAttachment(context.Context, string, string, string) (account.Account, maildomain.Attachment, maildomain.AttachmentSource, int64, error) {
	return account.Account{}, maildomain.Attachment{}, nil, 0, NewAppError(ErrorConfiguration, "local attachment content is unavailable", false)
}
func (unavailableMailService) MailUpdateFlag(context.Context, string, string, string, string, maildomain.FlagMutation) (account.Account, folder.Folder, maildomain.Message, maildomain.Location, int64, error) {
	return account.Account{}, folder.Folder{}, maildomain.Message{}, maildomain.Location{}, 0, NewAppError(ErrorConfiguration, "message-state mutation is unavailable", false)
}
func (unavailableMailService) MailSearch(context.Context, string, string, string, maildomain.SearchOptions) (account.Account, folder.Folder, maildomain.SearchPage, int64, error) {
	return account.Account{}, folder.Folder{}, maildomain.SearchPage{}, 0, NewAppError(ErrorConfiguration, "local mail search is unavailable", false)
}
func (unavailableMailService) MailSearchRepair(context.Context, string, string, int) (account.Account, maildomain.SearchRepairSummary, int64, error) {
	return account.Account{}, maildomain.SearchRepairSummary{}, 0, NewAppError(ErrorConfiguration, "local mail search repair is unavailable", false)
}
func (unavailableMailService) MailRemoteSearch(context.Context, string, string, string, bool, maildomain.SearchOptions) (account.Account, maildomain.RemoteSearchPage, int64, error) {
	return account.Account{}, maildomain.RemoteSearchPage{}, 0, NewAppError(ErrorConfiguration, "remote mail search is unavailable", false)
}
func (unavailableMailService) MailRemoteSummary(context.Context, string, string, string) (account.Account, maildomain.RemoteSearchHit, int64, error) {
	return account.Account{}, maildomain.RemoteSearchHit{}, 0, NewAppError(ErrorConfiguration, "remote message lookup is unavailable", false)
}
func (unavailableMailService) MailHydrate(context.Context, string, string, []string) (account.Account, []maildomain.HydrateOutcome, int64, error) {
	return account.Account{}, nil, 0, NewAppError(ErrorConfiguration, "remote message hydration is unavailable", false)
}
func (unavailableMailService) MailSendPreview(context.Context, string, string, maildomain.SendRequest) (account.Account, maildomain.SendPreview, int64, error) {
	return account.Account{}, maildomain.SendPreview{}, 0, NewAppError(ErrorConfiguration, "mail send preview is unavailable", false)
}
func (unavailableMailService) MailSend(context.Context, string, string, maildomain.SendRequest) (account.Account, maildomain.OutboundAttempt, bool, int64, error) {
	return account.Account{}, maildomain.OutboundAttempt{}, false, 0, NewAppError(ErrorConfiguration, "mail send is unavailable", false)
}
func (unavailableMailService) MailReplyPreview(context.Context, string, string, maildomain.ReplyRequest) (account.Account, maildomain.ReplyPreview, int64, error) {
	return account.Account{}, maildomain.ReplyPreview{}, 0, NewAppError(ErrorConfiguration, "mail reply preview is unavailable", false)
}
func (unavailableMailService) MailReply(context.Context, string, string, maildomain.ReplyRequest) (account.Account, maildomain.OutboundAttempt, bool, int64, error) {
	return account.Account{}, maildomain.OutboundAttempt{}, false, 0, NewAppError(ErrorConfiguration, "mail reply is unavailable", false)
}
func (unavailableMailService) MailForwardPreview(context.Context, string, string, maildomain.ForwardRequest) (account.Account, maildomain.ForwardPreview, int64, error) {
	return account.Account{}, maildomain.ForwardPreview{}, 0, NewAppError(ErrorConfiguration, "mail forward preview is unavailable", false)
}
func (unavailableMailService) MailForward(context.Context, string, string, maildomain.ForwardRequest) (account.Account, maildomain.OutboundAttempt, bool, int64, error) {
	return account.Account{}, maildomain.OutboundAttempt{}, false, 0, NewAppError(ErrorConfiguration, "mail forward is unavailable", false)
}
func (unavailableMailService) MailOutboundStatus(context.Context, string, string, int) (account.Account, []maildomain.OutboundAttempt, bool, int64, error) {
	return account.Account{}, nil, false, 0, NewAppError(ErrorConfiguration, "outbound mail status is unavailable", false)
}
func (unavailableMailService) MailLocationPreview(context.Context, string, string, maildomain.LocationMutationRequest) (account.Account, maildomain.LocationMutationPlan, int64, error) {
	return account.Account{}, maildomain.LocationMutationPlan{}, 0, NewAppError(ErrorConfiguration, "mail location mutation preview is unavailable", false)
}
func (unavailableMailService) MailMutateLocation(context.Context, string, string, maildomain.LocationMutationRequest) (account.Account, maildomain.LocationMutationPlan, maildomain.Message, maildomain.LocationMutation, bool, int64, error) {
	return account.Account{}, maildomain.LocationMutationPlan{}, maildomain.Message{}, maildomain.LocationMutation{}, false, 0, NewAppError(ErrorConfiguration, "mail location mutation is unavailable", false)
}
func (unavailableMailService) MailLocationMutationStatus(context.Context, string, string, int) (account.Account, []maildomain.LocationMutation, bool, int64, error) {
	return account.Account{}, nil, false, 0, NewAppError(ErrorConfiguration, "mail location mutation status is unavailable", false)
}
