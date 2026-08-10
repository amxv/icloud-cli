package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type mailPurgePreviewData struct {
	AccountID         string                            `json:"account_id"`
	Purge             maildomain.MailPurge              `json:"purge"`
	FolderID          string                            `json:"folder_id"`
	FolderName        string                            `json:"folder_name"`
	RetentionWarning  string                            `json:"retention_warning,omitempty"`
	ApplyRequestHash  string                            `json:"apply_request_hash,omitempty"`
	LocationPlans     []maildomain.LocationMutationPlan `json:"location_plans,omitempty"`
	Count             int                               `json:"count"`
	More              bool                              `json:"more"`
	BlockedUnresolved int                               `json:"blocked_unresolved"`
	Empty             bool                              `json:"empty"`
	ProviderContacted bool                              `json:"provider_contacted"`
	SchemaVersion     int64                             `json:"schema_version"`
}

type mailPurgeService interface {
	MailPurgeCreate(context.Context, string, string, maildomain.PurgeCreateRequest) (account.Account, folder.Folder, maildomain.MailPurge, int64, error)
	MailPurge(context.Context, string, string, string, maildomain.PurgeCreateRequest) (account.Account, folder.Folder, maildomain.MailPurge, int64, error)
	MailPurgeApplyHash(context.Context, maildomain.MailPurge, string, []string) (string, error)
}

func newMailEmptyCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "empty",
		Short:   "Permanently delete reviewed bounded pages from Trash, Junk, or one named custom folder",
		Args:    cobra.NoArgs,
		Example: "  icloud mail empty trash --dry-run\n  icloud mail empty junk --dry-run\n  icloud mail empty folder 'Temporary Import' --dry-run",
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(newMailPurgeCommand(state, maildomain.PurgeScopeTrash))
	command.AddCommand(newMailPurgeCommand(state, maildomain.PurgeScopeJunk))
	command.AddCommand(newMailPurgeCommand(state, maildomain.PurgeScopeFolder))
	return command
}

func newMailPurgeCommand(state *executionState, scope maildomain.PurgeScopeKind) *cobra.Command {
	var folderSelector, purgeID, requestHash, mode string
	var limit int
	var dryRun, yes, jsonOutput, jsonlOutput bool
	var input jsonInputOptions
	use, short, example := purgeCommandMetadata(scope)
	command := &cobra.Command{
		Use:     use,
		Short:   short,
		Example: example,
		Args: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(input.Path) != "" {
				if len(args) != 0 {
					return NewAppError(ErrorUsage, "positional purge arguments cannot be combined with JSON input", false)
				}
				return nil
			}
			if scope == maildomain.PurgeScopeFolder {
				if len(args) != 1 {
					return NewAppError(ErrorUsage, "empty folder requires exactly one deliberately named custom folder", false)
				}
				folderSelector = strings.TrimSpace(args[0])
				return nil
			}
			if len(args) != 0 {
				return NewAppError(ErrorUsage, fmt.Sprintf("empty %s does not accept positional arguments", scope), false)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			purgeService, ok := state.mail.(mailPurgeService)
			if !ok {
				return NewAppError(ErrorConfiguration, "mail purge is unavailable", false)
			}
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, JSONL: jsonlOutput}, OutputSupport{JSON: true, JSONL: true}); err != nil {
				return err
			}
			if dryRun && yes {
				return NewAppError(ErrorUsage, "--dry-run and --yes cannot be combined", false)
			}
			if scope == maildomain.PurgeScopeFolder {
				request := mailPurgeJSONRequest{Folder: folderSelector, PurgeID: purgeID, Limit: &limit, Mode: mode, RequestHash: requestHash}
				if used, err := readJSONInput(cmd, state, input, &request, "folder", "purge-id", "limit", "mode", "request-hash"); err != nil {
					return err
				} else if used {
					folderSelector, purgeID, mode, requestHash = request.Folder, request.PurgeID, request.Mode, request.RequestHash
					if request.Limit != nil {
						limit = *request.Limit
					}
				}
			} else {
				request := mailRolePurgeJSONRequest{PurgeID: purgeID, Limit: &limit, Mode: mode, RequestHash: requestHash}
				if used, err := readJSONInput(cmd, state, input, &request, "purge-id", "limit", "mode", "request-hash"); err != nil {
					return err
				} else if used {
					purgeID, mode, requestHash = request.PurgeID, request.Mode, request.RequestHash
					if request.Limit != nil {
						limit = *request.Limit
					}
				}
			}
			folderSelector = strings.TrimSpace(folderSelector)
			purgeID = strings.TrimSpace(purgeID)
			requestHash = strings.TrimSpace(requestHash)
			mode = strings.ToLower(strings.TrimSpace(mode))
			if mode == "" {
				mode = "atomic"
			}
			if mode != "atomic" && mode != "continue-on-error" {
				return NewAppError(ErrorUsage, "--mode must be atomic or continue-on-error", false)
			}
			if limit < 1 || limit > MaximumBulkItems {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumBulkItems), false)
			}
			if scope == maildomain.PurgeScopeFolder && folderSelector == "" {
				return NewAppError(ErrorUsage, "empty folder requires one deliberately named custom folder", false)
			}
			if scope != maildomain.PurgeScopeFolder && folderSelector != "" {
				return NewAppError(ErrorUsage, fmt.Sprintf("empty %s does not accept a folder selector", scope), false)
			}
			if dryRun && requestHash != "" {
				return NewAppError(ErrorUsage, "--request-hash is only used when applying an already reviewed purge", false)
			}
			if !dryRun && purgeID == "" {
				return NewAppError(ErrorSafety, "mail purge execution requires --purge-id from a prior --dry-run review", false)
			}
			if !dryRun && requestHash == "" {
				return NewAppError(ErrorSafety, "mail purge execution requires --request-hash from a matching --dry-run review", false)
			}

			scopeRequest := maildomain.PurgeCreateRequest{ScopeKind: scope, FolderSelector: folderSelector, Limit: limit}
			var (
				accountValue  account.Account
				folderValue   folder.Folder
				purge         maildomain.MailPurge
				schemaVersion int64
				err           error
			)
			if purgeID == "" {
				accountValue, folderValue, purge, schemaVersion, err = purgeService.MailPurgeCreate(cmd.Context(), state.global.Home, state.global.Account, scopeRequest)
			} else {
				accountValue, folderValue, purge, schemaVersion, err = purgeService.MailPurge(cmd.Context(), state.global.Home, state.global.Account, purgeID, scopeRequest)
			}
			if err != nil {
				return err
			}
			retentionWarning := purgeRetentionWarning(scope)
			if purge.ItemCount == 0 {
				return renderEmptyMailPurgePreview(state, accountValue, folderValue, purge, retentionWarning, schemaVersion, jsonOutput, jsonlOutput)
			}

			items := make([]locationMutationItem, 0, len(purge.Items))
			for index := range purge.Items {
				item := purge.Items[index]
				snapshot := item.SelectionItem(purge.FolderID)
				items = append(items, locationMutationItem{
					MessageID: item.MessageID, From: purge.FolderID, Snapshot: &snapshot,
				})
			}
			if !dryRun && requestHash != "" {
				for index := range items {
					items[index].IdempotencyKey = purgeItemIdempotencyKey(purge.ID, requestHash, items[index].Snapshot.LocationID)
				}
			}
			if err := validateLocationMutationItems(maildomain.LocationActionDelete, items); err != nil {
				return err
			}
			_, plans, _, err := validateLocationMutationPlans(cmd, state, maildomain.LocationActionDelete, items)
			if err != nil {
				return err
			}
			planHashes := make([]string, 0, len(plans))
			for _, plan := range plans {
				planHashes = append(planHashes, plan.RequestHash)
			}
			applyHash, err := purgeService.MailPurgeApplyHash(cmd.Context(), purge, mode, planHashes)
			if err != nil {
				return WrapAppError(ErrorInternal, "cannot hash mail purge apply request", false, err)
			}
			if dryRun {
				return renderMailPurgePreview(state, accountValue, folderValue, purge, applyHash, retentionWarning, plans, schemaVersion, jsonOutput, jsonlOutput)
			}
			if requestHash != applyHash {
				return mailPurgeApplyHashMismatch(purge, applyHash, requestHash)
			}
			for index := range items {
				items[index].IdempotencyKey = purgeItemIdempotencyKey(purge.ID, applyHash, items[index].Snapshot.LocationID)
			}
			meta := &threadMutationMeta{
				PurgeID: purge.ID, PurgeHash: purge.RequestHash, PurgeScope: purge.ScopeKind,
				PurgeFolderID: purge.FolderID, PurgeMore: purge.More,
				PurgeBlockedUnresolved: purge.BlockedUnresolved,
				RetentionWarning:       retentionWarning, ApplyRequestHash: applyHash,
				RequireConfirmedLocation: true,
			}
			flags := locationMutationFlags{Yes: yes, JSON: jsonOutput, JSONL: jsonlOutput}
			return executeLocationMutationItems(cmd, state, maildomain.LocationActionDelete, items, flags, mode, true, meta)
		},
	}
	command.Flags().StringVar(&purgeID, "purge-id", "", "Opaque bounded purge review ID emitted by --dry-run")
	command.Flags().IntVar(&limit, "limit", MaximumBulkItems, "Maximum exact provider locations in this reviewed purge page")
	command.Flags().StringVar(&mode, "mode", "atomic", "Failure mode: validate then fail fast, or continue-on-error")
	command.Flags().StringVar(&requestHash, "request-hash", "", "Exact apply hash emitted by the matching --dry-run review")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Freeze and review one exact bounded purge page without provider writes")
	command.Flags().BoolVar(&yes, "yes", false, "Confirm intentional non-interactive permanent deletion")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&jsonlOutput, "jsonl", false, "Emit exact reviewed/apply item events and completion accounting")
	bindJSONInput(command, &input)
	return command
}

func purgeCommandMetadata(scope maildomain.PurgeScopeKind) (string, string, string) {
	switch scope {
	case maildomain.PurgeScopeTrash:
		return "trash", "Permanently delete one reviewed bounded page from the semantic Trash folder", "  icloud mail empty trash --limit 50 --dry-run\n  icloud mail empty trash --purge-id pur_… --request-hash <hash> --yes --jsonl"
	case maildomain.PurgeScopeJunk:
		return "junk", "Permanently delete one reviewed bounded page from the semantic Junk folder", "  icloud mail empty junk --limit 50 --dry-run\n  icloud mail empty junk --purge-id pur_… --request-hash <hash> --yes --jsonl"
	default:
		return "folder <folder>", "Permanently delete one reviewed bounded page from one deliberately named ordinary custom folder", "  icloud mail empty folder 'Temporary Import' --limit 50 --dry-run\n  icloud mail empty folder 'Temporary Import' --purge-id pur_… --request-hash <hash> --yes --jsonl"
	}
}

func purgeRetentionWarning(scope maildomain.PurgeScopeKind) string {
	if scope == maildomain.PurgeScopeTrash || scope == maildomain.PurgeScopeJunk {
		return "provider_auto_delete_possible"
	}
	return ""
}

func purgeItemIdempotencyKey(purgeID, applyHash, locationID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(purgeID) + "\x00" + strings.TrimSpace(applyHash) + "\x00" + strings.TrimSpace(locationID)))
	return "purge:" + hex.EncodeToString(sum[:])
}

func mailPurgeApplyHashMismatch(purge maildomain.MailPurge, expected, provided string) *AppError {
	err := NewAppError(ErrorConflict, "mail purge apply request no longer matches the reviewed request hash", false)
	err.Details = map[string]any{
		"purge_id": purge.ID, "purge_hash": purge.RequestHash,
		"expected_request_hash": expected, "provided_request_hash": strings.TrimSpace(provided),
		"next": "rerun the same purge review with --dry-run and review the new hash before provider contact",
	}
	return err
}

func renderMailPurgePreview(state *executionState, accountValue account.Account, folderValue folder.Folder, purge maildomain.MailPurge, applyHash, retentionWarning string, plans []maildomain.LocationMutationPlan, schemaVersion int64, jsonOutput, jsonlOutput bool) error {
	data := mailPurgePreviewData{
		AccountID: accountValue.ID, Purge: purge, FolderID: folderValue.ID, FolderName: folderValue.Name,
		RetentionWarning: retentionWarning, ApplyRequestHash: applyHash, LocationPlans: plans,
		Count: len(plans), More: purge.More, BlockedUnresolved: purge.BlockedUnresolved,
		Empty: false, ProviderContacted: false, SchemaVersion: schemaVersion,
	}
	if jsonlOutput {
		sequence := uint64(0)
		for index, plan := range plans {
			sequence++
			payload := map[string]any{
				"index": index, "status": "preview", "purge_id": purge.ID, "purge_hash": purge.RequestHash,
				"purge_scope": purge.ScopeKind, "folder_id": folderValue.ID, "more": purge.More,
				"blocked_unresolved": purge.BlockedUnresolved,
				"retention_warning":  retentionWarning, "apply_request_hash": applyHash, "plan": plan,
			}
			if err := WriteJSONLEvent(state.stdout, JSONLEvent{Event: "item", Sequence: sequence, AccountID: accountValue.ID, Timestamp: time.Now().UTC(), Payload: payload}); err != nil {
				return err
			}
		}
		sequence++
		return WriteJSONLEvent(state.stdout, JSONLEvent{Event: "completed", Sequence: sequence, AccountID: accountValue.ID, Timestamp: time.Now().UTC(), Payload: map[string]any{
			"purge_id": purge.ID, "purge_hash": purge.RequestHash, "purge_scope": purge.ScopeKind,
			"folder_id": folderValue.ID, "reviewed": len(plans), "more": purge.More,
			"blocked_unresolved": purge.BlockedUnresolved, "empty": false,
			"retention_warning": retentionWarning, "apply_request_hash": applyHash,
			"provider_contacted": false, "intent_created": false,
		}})
	}
	if jsonOutput {
		return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
	}
	renderer := NewCompactTextRenderer(state.stdout)
	for index, item := range purge.Items {
		if err := renderer.Record("mail-purge-item", item.LocationID,
			Field("index", index), Field("message", item.MessageID), Field("uid_validity", item.UIDValidity), Field("uid", item.UID), Field("mod_sequence", item.ModSequence),
		); err != nil {
			return err
		}
	}
	return renderer.Summary("mail-purge-review",
		Field("purge", purge.ID), Field("purge_hash", purge.RequestHash), Field("scope", purge.ScopeKind),
		Field("folder", folderValue.ID), Field("folder_name", folderValue.Name), Field("count", len(plans)),
		Field("more", purge.More), Field("blocked_unresolved", purge.BlockedUnresolved), Field("empty", false),
		Field("expires_at", purge.ExpiresAt), Field("retention_warning", retentionWarning),
		Field("apply_request_hash", applyHash), Field("provider_contacted", false),
		Field("next", "rerun this scope with --purge-id, --request-hash, and confirmation; when more=true create a new --dry-run page after this page completes"),
	)
}

func renderEmptyMailPurgePreview(state *executionState, accountValue account.Account, folderValue folder.Folder, purge maildomain.MailPurge, retentionWarning string, schemaVersion int64, jsonOutput, jsonlOutput bool) error {
	empty := purge.BlockedUnresolved == 0
	next := "nothing to purge from this current synchronized folder state"
	if !empty {
		next = "synchronize and reconcile unresolved exact-delete outcomes, then create a new bounded purge review"
	}
	data := mailPurgePreviewData{
		AccountID: accountValue.ID, Purge: purge, FolderID: folderValue.ID, FolderName: folderValue.Name,
		RetentionWarning: retentionWarning, Count: 0, More: false, BlockedUnresolved: purge.BlockedUnresolved,
		Empty: empty, ProviderContacted: false, SchemaVersion: schemaVersion,
	}
	if jsonlOutput {
		return WriteJSONLEvent(state.stdout, JSONLEvent{Event: "completed", Sequence: 1, AccountID: accountValue.ID, Timestamp: time.Now().UTC(), Payload: map[string]any{
			"purge_scope": purge.ScopeKind, "folder_id": folderValue.ID, "reviewed": 0, "more": false,
			"blocked_unresolved": purge.BlockedUnresolved, "empty": empty,
			"retention_warning": retentionWarning, "provider_contacted": false, "intent_created": false, "next": next,
		}})
	}
	if jsonOutput {
		return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
	}
	return NewCompactTextRenderer(state.stdout).Summary("mail-purge-review",
		Field("scope", purge.ScopeKind), Field("folder", folderValue.ID), Field("folder_name", folderValue.Name),
		Field("count", 0), Field("more", false), Field("blocked_unresolved", purge.BlockedUnresolved), Field("empty", empty),
		Field("retention_warning", retentionWarning), Field("provider_contacted", false), Field("next", next),
	)
}
