package app

import (
	"context"
	"errors"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

func (commands accountCommands) MailReconcileReplyAnswer(
	ctx context.Context,
	home, accountSelector, attemptID string,
) (account.Account, maildomain.OutboundAttempt, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, 0, err
	}
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	schemaVersion := state.SchemaVersion()
	_ = state.Close()
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, schemaVersion, mapAccountError(err)
	}
	attempt, err := commands.reconcileReplyAnswer(ctx, home, accountValue.ID, attemptID)
	return accountValue, attempt, schemaVersion, err
}

func (commands accountCommands) reconcileReplyAnswer(
	ctx context.Context,
	home, accountSelector, attemptID string,
) (maildomain.OutboundAttempt, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return maildomain.OutboundAttempt{}, err
	}
	defer state.Close()
	defer unlock()

	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return maildomain.OutboundAttempt{}, mapAccountError(err)
	}
	attempt, err := state.Outbound().Attempt(ctx, strings.TrimSpace(attemptID))
	if err != nil {
		if errors.Is(err, maildomain.ErrOutboundAttemptNotFound) {
			return maildomain.OutboundAttempt{}, cli.WrapAppError(cli.ErrorNotFound, "outbound attempt not found", false, err)
		}
		return maildomain.OutboundAttempt{}, cli.WrapAppError(cli.ErrorInternal, "cannot read outbound attempt", false, err)
	}
	if attempt.AccountID != accountValue.ID {
		return maildomain.OutboundAttempt{}, cli.NewAppError(cli.ErrorNotFound, "outbound attempt not found", false)
	}
	if attempt.Kind != maildomain.OutboundKindReply && attempt.Kind != maildomain.OutboundKindReplyAll {
		return attempt, cli.NewAppError(cli.ErrorUsage, "answer reconciliation requires a reply attempt", false)
	}
	if attempt.Status != maildomain.OutboundSent {
		return attempt, cli.NewAppError(cli.ErrorConflict, "reply was not proven sent; provider answer state will not be changed", false)
	}
	if attempt.AnswerReconciliation == maildomain.AnswerReconciliationConfirmed || attempt.AnswerReconciliation == maildomain.AnswerReconciliationUnsupported {
		return attempt, nil
	}
	if !strings.HasPrefix(attempt.ParentLocationID, "loc_") {
		// Historical replies created before exact parent-location provenance are
		// still proven replies, but there is no safe provider target to mutate.
		return attempt, nil
	}

	location, err := state.Mail().Location(ctx, accountValue.ID, attempt.ParentLocationID)
	if err != nil {
		return commands.markReplyAnswerPending(ctx, state.Outbound(), attempt, answerReconciliationErrorCode(err), err)
	}
	if location.MessageID != attempt.ParentMessageID {
		mismatch := errors.New("persisted reply parent location no longer identifies the reply parent")
		return commands.markReplyAnswerPending(ctx, state.Outbound(), attempt, "parent_location_mismatch", mismatch)
	}
	folderValue, err := state.Folders().Resolve(ctx, accountValue.ID, location.FolderID)
	if err != nil {
		return commands.markReplyAnswerPending(ctx, state.Outbound(), attempt, answerReconciliationErrorCode(err), err)
	}
	if !location.Present || !location.Flags.Eligible() || !folderValue.Present || folderValue.Stale || !folderValue.Selectable {
		stale := errors.New("reply parent provider location is no longer current and selectable")
		return commands.markReplyAnswerPending(ctx, state.Outbound(), attempt, "parent_location_stale", stale)
	}
	if location.Flags.Answered {
		outcomeCtx, cancel := durableOutcomeContext(ctx)
		defer cancel()
		confirmed, confirmErr := state.Outbound().MarkAnswerConfirmed(outcomeCtx, attempt.ID, commands.timeNow())
		if confirmErr != nil {
			return attempt, cli.WrapAppError(cli.ErrorInternal, "provider answer state is present but reconciliation could not be recorded", true, confirmErr)
		}
		return confirmed, nil
	}

	_, _, _, _, _, mutationErr := commands.updateExactLocationFlag(
		ctx, state, accountValue, folderValue, location, attempt.ParentMessageID, maildomain.FlagMutationAnswer,
	)
	if mutationErr != nil {
		return commands.markReplyAnswerPending(ctx, state.Outbound(), attempt, answerReconciliationErrorCode(mutationErr), mutationErr)
	}
	outcomeCtx, cancel := durableOutcomeContext(ctx)
	defer cancel()
	confirmed, err := state.Outbound().MarkAnswerConfirmed(outcomeCtx, attempt.ID, commands.timeNow())
	if err != nil {
		return attempt, cli.WrapAppError(cli.ErrorInternal, "provider answer state was applied but reconciliation could not be recorded", true, err)
	}
	return confirmed, nil
}

func (commands accountCommands) markReplyAnswerPending(
	ctx context.Context,
	repository *store.OutboundRepository,
	attempt maildomain.OutboundAttempt,
	code string,
	cause error,
) (maildomain.OutboundAttempt, error) {
	outcomeCtx, cancel := durableOutcomeContext(ctx)
	defer cancel()
	updated, err := repository.MarkAnswerPending(outcomeCtx, attempt.ID, code, commands.timeNow())
	if err != nil {
		return attempt, errors.Join(cause, cli.WrapAppError(cli.ErrorInternal, "reply was sent but answer reconciliation state could not be recorded", false, err))
	}
	return updated, cause
}

func answerReconciliationErrorCode(err error) string {
	var validity *transport.UIDValidityError
	if errors.As(err, &validity) {
		return "uidvalidity"
	}
	if failure, ok := transport.AsFailure(err); ok && failure.Stage != "" {
		return string(failure.Stage)
	}
	if errors.Is(err, maildomain.ErrLocationConflict) {
		return "location_conflict"
	}
	if errors.Is(err, maildomain.ErrLocationNotFound) {
		return "location_missing"
	}
	return "answer_reconciliation"
}
