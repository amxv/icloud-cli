package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

func (commands accountCommands) MailLocationPreview(
	ctx context.Context,
	home, accountSelector string,
	request maildomain.LocationMutationRequest,
) (account.Account, maildomain.LocationMutationPlan, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.LocationMutationPlan{}, 0, err
	}
	defer state.Close()
	accountValue, plan, err := resolveLocationMutationPlan(ctx, state, accountSelector, request)
	if err != nil {
		return account.Account{}, maildomain.LocationMutationPlan{}, state.SchemaVersion(), err
	}
	return accountValue, plan, state.SchemaVersion(), nil
}

func (commands accountCommands) MailMutateLocation(
	ctx context.Context,
	home, accountSelector string,
	request maildomain.LocationMutationRequest,
) (account.Account, maildomain.LocationMutationPlan, maildomain.Message, maildomain.LocationMutation, bool, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.LocationMutationPlan{}, maildomain.Message{}, maildomain.LocationMutation{}, false, 0, err
	}
	defer state.Close()
	defer unlock()
	if strings.TrimSpace(request.IdempotencyKey) != "" {
		accountValue, resolveErr := state.Accounts().ResolveSelected(ctx, accountSelector)
		if resolveErr != nil {
			return account.Account{}, maildomain.LocationMutationPlan{}, maildomain.Message{}, maildomain.LocationMutation{}, false, state.SchemaVersion(), mapAccountError(resolveErr)
		}
		existing, readErr := state.LocationMutations().ByIdempotencyKey(ctx, accountValue.ID, request.IdempotencyKey)
		if readErr == nil {
			plan, planErr := planForExistingLocationMutation(ctx, state, accountValue.ID, request, existing)
			if planErr != nil {
				return account.Account{}, maildomain.LocationMutationPlan{}, maildomain.Message{}, maildomain.LocationMutation{}, false, state.SchemaVersion(), planErr
			}
			message, messageErr := state.Mail().Message(ctx, accountValue.ID, existing.MessageID)
			if messageErr != nil {
				return account.Account{}, plan, maildomain.Message{}, existing, true, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read the idempotent location mutation result", false, messageErr)
			}
			return accountValue, plan, message, existing, true, state.SchemaVersion(), nil
		}
		if !errors.Is(readErr, maildomain.ErrMutationNotFound) {
			return account.Account{}, maildomain.LocationMutationPlan{}, maildomain.Message{}, maildomain.LocationMutation{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot resolve location mutation idempotency", false, readErr)
		}
	}
	accountValue, plan, err := resolveLocationMutationPlan(ctx, state, accountSelector, request)
	if err != nil {
		return account.Account{}, maildomain.LocationMutationPlan{}, maildomain.Message{}, maildomain.LocationMutation{}, false, state.SchemaVersion(), err
	}
	mutationID, err := commands.generateID("mut")
	if err != nil {
		return account.Account{}, plan, maildomain.Message{}, maildomain.LocationMutation{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate a location mutation ID", false, err)
	}
	proposedLocationID := ""
	if plan.DestinationFolderID != "" {
		proposedLocationID, err = commands.generateID("loc")
		if err != nil {
			return account.Account{}, plan, maildomain.Message{}, maildomain.LocationMutation{}, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate a destination location ID", false, err)
		}
	}
	mutation, reused, err := state.LocationMutations().Begin(ctx, maildomain.BeginLocationMutation{
		MutationID: mutationID, AccountID: accountValue.ID, MessageID: plan.MessageID,
		Action: plan.Action, SourceLocationID: plan.SourceLocation.ID,
		SourceFolderID: plan.SourceFolderID, DestinationFolderID: plan.DestinationFolderID,
		ProposedLocationID: proposedLocationID, RequestHash: plan.RequestHash,
		IdempotencyKey: request.IdempotencyKey, Now: commands.timeNow(),
	})
	if err != nil {
		return account.Account{}, plan, maildomain.Message{}, maildomain.LocationMutation{}, false, state.SchemaVersion(), mapLocationMutationStoreError(err)
	}
	if reused {
		message, readErr := state.Mail().Message(ctx, accountValue.ID, plan.MessageID)
		if readErr != nil {
			return account.Account{}, plan, maildomain.Message{}, mutation, true, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read the idempotent location mutation result", false, readErr)
		}
		return accountValue, plan, message, mutation, true, state.SchemaVersion(), nil
	}
	_, credential, err := state.Accounts().Credential(ctx, accountValue.ID)
	if err != nil {
		mutation, _ = state.LocationMutations().MarkFailed(ctx, mutation.ID, "credential", commands.timeNow())
		return account.Account{}, plan, maildomain.Message{}, mutation, false, state.SchemaVersion(), mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		mutation, _ = state.LocationMutations().MarkFailed(ctx, mutation.ID, "credential", commands.timeNow())
		return account.Account{}, plan, maildomain.Message{}, mutation, false, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)

	transportRequest := transport.LocationMutationRequest{
		Operation:                 transport.LocationMutationOperation(plan.Operation),
		SourceMailbox:             plan.SourceFolderName,
		ExpectedSourceUIDValidity: plan.SourceLocation.UIDValidity,
		SourceUID:                 plan.SourceLocation.UID,
		ExpectedSourceModSequence: plan.SourceLocation.ModSequence,
		DestinationMailbox:        plan.DestinationFolderName,
	}
	var (
		providerResult transport.LocationMutationResult
		usedUsername   string
		providerErr    error
	)
	for _, username := range imapUsernameCandidates(accountValue) {
		providerResult, providerErr = commands.locationClient().MutateLocation(ctx, transport.AuthRequest{
			Username: username, Password: password,
		}, transportRequest)
		if providerErr == nil || providerResult.ProviderMutationObserved {
			usedUsername = username
			break
		}
		failure, ok := transport.AsFailure(providerErr)
		if !ok || failure.Kind != transport.FailureAuthentication {
			break
		}
	}
	if usedUsername != "" && usedUsername != accountValue.IMAPUsername {
		accountValue, err = state.Accounts().UpdateIMAPUsername(ctx, accountValue.ID, usedUsername, commands.timeNow())
		if err != nil {
			return account.Account{}, plan, maildomain.Message{}, mutation, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "provider changed mail but the working IMAP username could not be saved; do not retry automatically", false, err)
		}
	}
	if providerErr != nil && !providerResult.ProviderMutationObserved {
		errorCode := locationMutationErrorCode(providerErr)
		mutation, markErr := state.LocationMutations().MarkFailed(ctx, mutation.ID, errorCode, commands.timeNow())
		if markErr != nil {
			return account.Account{}, plan, maildomain.Message{}, mutation, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "location mutation failed but its intent outcome could not be saved", false, errors.Join(providerErr, markErr))
		}
		return accountValue, plan, maildomain.Message{}, mutation, false, state.SchemaVersion(), withLocationMutationDetails(mapLocationMutationTransportError(providerErr), mutation)
	}
	destinationFlags := maildomain.ParseLocationFlags(providerResult.DestinationFlags)
	if providerResult.DestinationKnown && len(providerResult.DestinationFlags) == 0 {
		destinationFlags = maildomain.ParseLocationFlags(providerResult.SourceFlags)
	}
	status := maildomain.LocationMutationConfirmed
	if providerErr != nil || providerResult.ReconciliationRequired {
		status = maildomain.LocationMutationUnresolved
	}
	mutation, applyErr := state.LocationMutations().Apply(ctx, maildomain.ApplyLocationMutation{
		MutationID: mutation.ID, ProviderMethod: providerResult.ProviderMethod,
		SourceFlags:            maildomain.ParseLocationFlags(providerResult.SourceFlags),
		SourceModSequence:      providerResult.SourceModSequence,
		SourcePresent:          providerResult.SourcePresent,
		DestinationKnown:       providerResult.DestinationKnown,
		DestinationUIDValidity: providerResult.DestinationUIDValidity,
		DestinationUID:         providerResult.DestinationUID,
		DestinationFlags:       destinationFlags,
		DestinationModSequence: providerResult.DestinationModSequence,
		Status:                 status, ReconciliationRequired: providerResult.ReconciliationRequired || providerErr != nil,
		ErrorCode: locationMutationErrorCode(providerErr), Now: commands.timeNow(),
	})
	if applyErr != nil {
		return accountValue, plan, maildomain.Message{}, mutation, false, state.SchemaVersion(), withLocationMutationDetails(
			cli.WrapAppError(cli.ErrorInternal, "provider changed mail but local location completion is incomplete; do not retry automatically", false, errors.Join(providerErr, applyErr)), mutation,
		)
	}
	message, err := state.Mail().Message(ctx, accountValue.ID, plan.MessageID)
	if err != nil {
		return accountValue, plan, maildomain.Message{}, mutation, false, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "location mutation completed but the local message cannot be read", false, err)
	}
	if providerErr != nil {
		return accountValue, plan, message, mutation, false, state.SchemaVersion(), withLocationMutationDetails(
			mapObservedLocationMutationError(providerErr, providerResult), mutation,
		)
	}
	return accountValue, plan, message, mutation, false, state.SchemaVersion(), nil
}

func planForExistingLocationMutation(
	ctx context.Context,
	state *store.Store,
	accountID string,
	request maildomain.LocationMutationRequest,
	existing maildomain.LocationMutation,
) (maildomain.LocationMutationPlan, error) {
	if request.Action != existing.Action || strings.TrimSpace(request.MessageID) != existing.MessageID {
		return maildomain.LocationMutationPlan{}, cli.NewAppError(cli.ErrorConflict, "location mutation idempotency key conflicts with another request", false)
	}
	switch existing.Action {
	case maildomain.LocationActionMove, maildomain.LocationActionCopy:
		if strings.TrimSpace(request.FromFolder) == "" || strings.TrimSpace(request.ToFolder) == "" {
			return maildomain.LocationMutationPlan{}, cli.NewAppError(cli.ErrorUsage, "idempotent move/copy replay requires the original --from and --to selections", false)
		}
	case maildomain.LocationActionDelete:
		if strings.TrimSpace(request.FromFolder) == "" {
			return maildomain.LocationMutationPlan{}, cli.NewAppError(cli.ErrorUsage, "idempotent delete replay requires the original --from selection", false)
		}
	case maildomain.LocationActionRestore:
		if strings.TrimSpace(request.ToFolder) == "" {
			return maildomain.LocationMutationPlan{}, cli.NewAppError(cli.ErrorUsage, "idempotent restore replay requires the original --to selection", false)
		}
	}
	sourceLocation, err := state.Mail().Location(ctx, accountID, existing.SourceLocationID)
	if err != nil {
		return maildomain.LocationMutationPlan{}, cli.WrapAppError(cli.ErrorInternal, "cannot read the idempotent mutation source", false, err)
	}
	sourceFolder, err := state.Folders().Resolve(ctx, accountID, existing.SourceFolderID)
	if err != nil {
		return maildomain.LocationMutationPlan{}, mapFolderError(err)
	}
	if selector := strings.TrimSpace(request.FromFolder); selector != "" {
		resolved, resolveErr := state.Folders().Resolve(ctx, accountID, selector)
		if resolveErr != nil || resolved.ID != existing.SourceFolderID {
			return maildomain.LocationMutationPlan{}, cli.NewAppError(cli.ErrorConflict, "location mutation idempotency key conflicts with another source folder", false)
		}
	}
	var destinationFolder folder.Folder
	if existing.DestinationFolderID != "" {
		destinationFolder, err = state.Folders().Resolve(ctx, accountID, existing.DestinationFolderID)
		if err != nil {
			return maildomain.LocationMutationPlan{}, mapFolderError(err)
		}
		if selector := strings.TrimSpace(request.ToFolder); selector != "" {
			resolved, resolveErr := state.Folders().Resolve(ctx, accountID, selector)
			if resolveErr != nil || resolved.ID != existing.DestinationFolderID {
				return maildomain.LocationMutationPlan{}, cli.NewAppError(cli.ErrorConflict, "location mutation idempotency key conflicts with another destination folder", false)
			}
		}
	} else if strings.TrimSpace(request.ToFolder) != "" {
		return maildomain.LocationMutationPlan{}, cli.NewAppError(cli.ErrorConflict, "location mutation idempotency key conflicts with a destination folder", false)
	}
	return maildomain.LocationMutationPlan{
		Action: existing.Action, Operation: existing.Action.TransportOperation(), MessageID: existing.MessageID,
		SourceLocation: sourceLocation, SourceFolderID: sourceFolder.ID, SourceFolderName: sourceFolder.Name,
		DestinationFolderID: destinationFolder.ID, DestinationFolderName: destinationFolder.Name,
		IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), RequestHash: existing.RequestHash,
	}, nil
}

func (commands accountCommands) MailLocationMutationStatus(
	ctx context.Context,
	home, accountSelector string,
	limit int,
) (account.Account, []maildomain.LocationMutation, bool, int64, error) {
	accountValue, values, more, _, schemaVersion, err := commands.MailLocationMutationStatusPage(ctx, home, accountSelector, "", limit)
	return accountValue, values, more, schemaVersion, err
}

func (commands accountCommands) MailLocationMutationStatusPage(
	ctx context.Context,
	home, accountSelector, cursor string,
	limit int,
) (account.Account, []maildomain.LocationMutation, bool, string, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, nil, false, "", 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, nil, false, "", state.SchemaVersion(), mapAccountError(err)
	}
	values, more, nextCursor, err := state.LocationMutations().ListPage(ctx, accountValue.ID, limit, cursor)
	if err != nil {
		if errors.Is(err, maildomain.ErrInvalidListCursor) {
			return account.Account{}, nil, false, "", state.SchemaVersion(), cli.WrapAppError(cli.ErrorUsage, "location mutation status cursor is invalid or belongs to another account", false, err)
		}
		return account.Account{}, nil, false, "", state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read location mutation status", false, err)
	}
	return accountValue, values, more, nextCursor, state.SchemaVersion(), nil
}

func resolveLocationMutationPlan(
	ctx context.Context,
	state *store.Store,
	accountSelector string,
	request maildomain.LocationMutationRequest,
) (account.Account, maildomain.LocationMutationPlan, error) {
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.LocationMutationPlan{}, mapAccountError(err)
	}
	message, err := state.Mail().Message(ctx, accountValue.ID, strings.TrimSpace(request.MessageID))
	if err != nil {
		if errors.Is(err, maildomain.ErrMessageNotFound) {
			return account.Account{}, maildomain.LocationMutationPlan{}, cli.WrapAppError(cli.ErrorNotFound, "message not found", false, err)
		}
		return account.Account{}, maildomain.LocationMutationPlan{}, cli.WrapAppError(cli.ErrorInternal, "cannot read the selected message", false, err)
	}
	action := request.Action
	if action.TransportOperation() == "" {
		return account.Account{}, maildomain.LocationMutationPlan{}, cli.NewAppError(cli.ErrorUsage, "location mutation action is invalid", false)
	}
	sourceFolder, sourceLocation, err := resolveLocationMutationSource(ctx, state, accountValue.ID, message, action, request.FromFolder)
	if err != nil {
		return account.Account{}, maildomain.LocationMutationPlan{}, err
	}
	var destinationFolder folder.Folder
	if action != maildomain.LocationActionDelete {
		destinationSelector := strings.TrimSpace(request.ToFolder)
		switch action {
		case maildomain.LocationActionArchive:
			destinationFolder, err = state.Folders().ResolveKind(ctx, accountValue.ID, folder.KindArchive)
		case maildomain.LocationActionUnarchive:
			destinationFolder, err = state.Folders().ResolveKind(ctx, accountValue.ID, folder.KindInbox)
		case maildomain.LocationActionTrash:
			destinationFolder, err = state.Folders().ResolveKind(ctx, accountValue.ID, folder.KindTrash)
		case maildomain.LocationActionRestore:
			if destinationSelector == "" {
				return account.Account{}, maildomain.LocationMutationPlan{}, cli.NewAppError(cli.ErrorUsage, "restore requires a deliberate --to folder", false)
			}
		case maildomain.LocationActionMove, maildomain.LocationActionCopy:
			if destinationSelector == "" {
				return account.Account{}, maildomain.LocationMutationPlan{}, cli.NewAppError(cli.ErrorUsage, "--to is required", false)
			}
		}
		if err == nil && destinationFolder.ID == "" {
			destinationFolder, err = state.Folders().Resolve(ctx, accountValue.ID, destinationSelector)
		}
		if err != nil {
			return account.Account{}, maildomain.LocationMutationPlan{}, mapFolderError(err)
		}
		if !destinationFolder.Present || destinationFolder.Stale || !destinationFolder.Selectable {
			return account.Account{}, maildomain.LocationMutationPlan{}, cli.NewAppError(cli.ErrorConflict, "destination folder is not current and selectable", false)
		}
		if destinationFolder.ID == sourceFolder.ID {
			return account.Account{}, maildomain.LocationMutationPlan{}, cli.NewAppError(cli.ErrorConflict, "source and destination folders must differ", false)
		}
	}
	plan := maildomain.LocationMutationPlan{
		Action: action, Operation: action.TransportOperation(), MessageID: message.ID,
		SourceLocation: sourceLocation, SourceFolderID: sourceFolder.ID, SourceFolderName: sourceFolder.Name,
		DestinationFolderID: destinationFolder.ID, DestinationFolderName: destinationFolder.Name,
		IdempotencyKey: strings.TrimSpace(request.IdempotencyKey),
	}
	plan.RequestHash, err = maildomain.LocationMutationHash(plan)
	if err != nil {
		return account.Account{}, maildomain.LocationMutationPlan{}, cli.WrapAppError(cli.ErrorInternal, "cannot hash the location mutation", false, err)
	}
	return accountValue, plan, nil
}

func resolveLocationMutationSource(
	ctx context.Context,
	state *store.Store,
	accountID string,
	message maildomain.Message,
	action maildomain.LocationMutationAction,
	selector string,
) (folder.Folder, maildomain.Location, error) {
	selector = strings.TrimSpace(selector)
	if selector != "" {
		folderValue, err := state.Folders().Resolve(ctx, accountID, selector)
		if err != nil {
			return folder.Folder{}, maildomain.Location{}, mapFolderError(err)
		}
		for _, location := range message.Locations {
			if location.FolderID == folderValue.ID && location.Present {
				if err := validateLocationMutationSourceRole(action, folderValue.Kind); err != nil {
					return folder.Folder{}, maildomain.Location{}, err
				}
				if action != maildomain.LocationActionDelete && !location.Flags.Eligible() {
					return folder.Folder{}, maildomain.Location{}, cli.NewAppError(cli.ErrorConflict, "selected source location is marked deleted; synchronize before mutating it", false)
				}
				return folderValue, location, nil
			}
		}
		return folder.Folder{}, maildomain.Location{}, cli.NewAppError(cli.ErrorNotFound, "message is not present in the selected source folder", false)
	}
	if action == maildomain.LocationActionMove || action == maildomain.LocationActionCopy || action == maildomain.LocationActionDelete {
		return folder.Folder{}, maildomain.Location{}, cli.NewAppError(cli.ErrorUsage, "--from is required", false)
	}
	candidates := make([]maildomain.Location, 0, len(message.Locations))
	for _, location := range message.Locations {
		if !location.Present || !location.Flags.Eligible() {
			continue
		}
		switch action {
		case maildomain.LocationActionArchive:
			if location.FolderKind == string(folder.KindSent) || location.FolderKind == string(folder.KindArchive) || location.FolderKind == string(folder.KindTrash) {
				continue
			}
		case maildomain.LocationActionUnarchive:
			if location.FolderKind != string(folder.KindArchive) {
				continue
			}
		case maildomain.LocationActionTrash:
			if location.FolderKind == string(folder.KindTrash) {
				continue
			}
		case maildomain.LocationActionRestore:
			if location.FolderKind != string(folder.KindTrash) {
				continue
			}
		}
		candidates = append(candidates, location)
	}
	if len(candidates) == 0 {
		return folder.Folder{}, maildomain.Location{}, cli.NewAppError(cli.ErrorNotFound, "message has no eligible source location for this action", false)
	}
	if len(candidates) > 1 {
		return folder.Folder{}, maildomain.Location{}, cli.NewAppError(cli.ErrorConflict, "message has multiple eligible source locations; use --from", false)
	}
	folderValue, err := state.Folders().Resolve(ctx, accountID, candidates[0].FolderID)
	if err != nil {
		return folder.Folder{}, maildomain.Location{}, mapFolderError(err)
	}
	return folderValue, candidates[0], nil
}

func validateLocationMutationSourceRole(action maildomain.LocationMutationAction, kind folder.Kind) error {
	switch action {
	case maildomain.LocationActionArchive:
		if kind == folder.KindSent {
			return cli.NewAppError(cli.ErrorSafety, "archive never moves a Sent location", false)
		}
	case maildomain.LocationActionUnarchive:
		if kind != folder.KindArchive {
			return cli.NewAppError(cli.ErrorConflict, "unarchive requires a current Archive source location", false)
		}
	case maildomain.LocationActionRestore:
		if kind != folder.KindTrash {
			return cli.NewAppError(cli.ErrorConflict, "restore requires a current Trash source location", false)
		}
	}
	return nil
}

func (commands accountCommands) locationClient() transport.IMAPLocationClient {
	if commands.locations != nil {
		return commands.locations
	}
	if candidate, ok := commands.imap.(transport.IMAPLocationClient); ok {
		return candidate
	}
	return unavailableLocationClient{}
}

type unavailableLocationClient struct{}

func (unavailableLocationClient) MutateLocation(context.Context, transport.AuthRequest, transport.LocationMutationRequest) (transport.LocationMutationResult, error) {
	return transport.LocationMutationResult{}, transport.NewFailure(transport.StageIMAPMove, transport.FailureConfiguration, false, errors.New("location mutation transport is unavailable"))
}

func locationMutationErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var validity *transport.UIDValidityError
	if errors.As(err, &validity) {
		return "uid_validity"
	}
	var conflict *transport.LocationStateConflictError
	if errors.As(err, &conflict) {
		return "location_changed"
	}
	if failure, ok := transport.AsFailure(err); ok {
		return string(failure.Stage)
	}
	return "provider"
}

func mapLocationMutationTransportError(err error) error {
	var validity *transport.UIDValidityError
	if errors.As(err, &validity) {
		return cli.WrapAppError(cli.ErrorConflict, "source mailbox identity changed; synchronize before retrying", false, err)
	}
	var conflict *transport.LocationStateConflictError
	if errors.As(err, &conflict) {
		return cli.WrapAppError(cli.ErrorConflict, "source message location changed; synchronize before retrying", false, err)
	}
	failure, ok := transport.AsFailure(err)
	if !ok {
		return cli.WrapAppError(cli.ErrorUnavailable, "mail location mutation failed", false, err)
	}
	code := cli.ErrorUnavailable
	switch failure.Kind {
	case transport.FailureAuthentication:
		code = cli.ErrorAuthentication
	case transport.FailureConfiguration:
		code = cli.ErrorConfiguration
	}
	message := "mail location mutation failed at " + string(failure.Stage)
	return cli.WrapAppError(code, message, failure.Retryable, err)
}

func mapObservedLocationMutationError(err error, result transport.LocationMutationResult) error {
	if result.ProviderMutationObserved && result.SourcePresent &&
		(result.ProviderMethod == "copy_only" || result.ProviderMethod == "copy_uid_expunge") {
		return cli.WrapAppError(
			cli.ErrorUnavailable,
			"provider copy may exist but exact source removal did not complete; synchronize both folders and do not retry automatically",
			false,
			err,
		)
	}
	if result.ProviderMutationObserved && !result.SourcePresent {
		return cli.WrapAppError(
			cli.ErrorUnavailable,
			"provider location changed but verification is incomplete; synchronize affected folders and do not retry automatically",
			false,
			err,
		)
	}
	if result.ProviderMutationObserved {
		return cli.WrapAppError(
			cli.ErrorUnavailable,
			"provider may have changed one or more locations but confirmation is incomplete; synchronize affected folders and do not retry automatically",
			false,
			err,
		)
	}
	return mapLocationMutationTransportError(err)
}

func mapLocationMutationStoreError(err error) error {
	switch {
	case errors.Is(err, maildomain.ErrMutationConflict):
		return cli.WrapAppError(cli.ErrorConflict, "location mutation idempotency key conflicts with another request", false, err)
	case errors.Is(err, maildomain.ErrLocationNotFound):
		return cli.WrapAppError(cli.ErrorNotFound, "source message location not found", false, err)
	case errors.Is(err, maildomain.ErrInvalidLocationMutation):
		return cli.WrapAppError(cli.ErrorUsage, "location mutation request is invalid", false, err)
	default:
		return cli.WrapAppError(cli.ErrorInternal, "cannot persist location mutation intent", false, err)
	}
}

func withLocationMutationDetails(err error, mutation maildomain.LocationMutation) error {
	var appErr *cli.AppError
	if !errors.As(err, &appErr) {
		return err
	}
	if appErr.Details == nil {
		appErr.Details = map[string]any{}
	}
	appErr.Details["mutation_id"] = mutation.ID
	appErr.Details["status"] = mutation.Status
	appErr.Details["reconciliation_required"] = mutation.ReconciliationRequired
	return appErr
}

var _ transport.IMAPLocationClient = unavailableLocationClient{}

func mutationPlanSummary(plan maildomain.LocationMutationPlan) string {
	return fmt.Sprintf("%s %s from %s to %s", plan.Action, plan.MessageID, plan.SourceFolderID, plan.DestinationFolderID)
}
