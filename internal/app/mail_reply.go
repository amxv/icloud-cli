package app

import (
	"context"
	"errors"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
)

func (commands accountCommands) MailReplyPreview(
	ctx context.Context,
	home, accountSelector string,
	request maildomain.ReplyRequest,
) (account.Account, maildomain.ReplyPreview, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.ReplyPreview{}, 0, err
	}
	accountValue, plan, schemaVersion, err := resolveReplyPlan(ctx, state, accountSelector, request)
	closeErr := state.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return account.Account{}, maildomain.ReplyPreview{}, schemaVersion, err
	}
	_, preview, schemaVersion, err := commands.MailSendPreview(ctx, home, accountValue.ID, plan.Send)
	if err != nil {
		return account.Account{}, maildomain.ReplyPreview{}, schemaVersion, err
	}
	return accountValue, replyPreview(plan, preview), schemaVersion, nil
}

func (commands accountCommands) MailReply(
	ctx context.Context,
	home, accountSelector string,
	request maildomain.ReplyRequest,
) (account.Account, maildomain.OutboundAttempt, bool, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, 0, err
	}
	accountValue, plan, schemaVersion, err := resolveReplyPlan(ctx, state, accountSelector, request)
	closeErr := state.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return account.Account{}, maildomain.OutboundAttempt{}, false, schemaVersion, err
	}
	return commands.MailSend(ctx, home, accountValue.ID, plan.Send)
}

func resolveReplyPlan(
	ctx context.Context,
	state *store.Store,
	accountSelector string,
	request maildomain.ReplyRequest,
) (account.Account, maildomain.ReplyPlan, int64, error) {
	if state == nil {
		return account.Account{}, maildomain.ReplyPlan{}, 0, cli.NewAppError(cli.ErrorInternal, "local mail state is unavailable", false)
	}
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.ReplyPlan{}, state.SchemaVersion(), mapAccountError(err)
	}
	parentID := strings.TrimSpace(request.ParentMessageID)
	parent, err := state.Mail().Message(ctx, accountValue.ID, parentID)
	if err != nil {
		if errors.Is(err, maildomain.ErrMessageNotFound) {
			return account.Account{}, maildomain.ReplyPlan{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "reply parent message not found", false, err)
		}
		return account.Account{}, maildomain.ReplyPlan{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read the reply parent message", false, err)
	}
	threadPage, err := state.Threads().Show(ctx, accountValue.ID, parent.ID, "", 1)
	if err != nil {
		if errors.Is(err, maildomain.ErrThreadNotFound) {
			return account.Account{}, maildomain.ReplyPlan{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "reply parent thread not found", false, err)
		}
		return account.Account{}, maildomain.ReplyPlan{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot resolve the reply parent thread", false, err)
	}
	self := []string{accountValue.Email}
	if strings.Contains(accountValue.IMAPUsername, "@") {
		self = append(self, accountValue.IMAPUsername)
	}
	plan, err := maildomain.BuildReplyPlan(parent, threadPage.Thread.ID, self, request)
	if err != nil {
		if errors.Is(err, maildomain.ErrInvalidReplyRequest) {
			return account.Account{}, maildomain.ReplyPlan{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorSafety, "cannot construct a safe reply from the selected message", false, err)
		}
		return account.Account{}, maildomain.ReplyPlan{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot construct the reply", false, err)
	}
	return accountValue, plan, state.SchemaVersion(), nil
}

func replyPreview(plan maildomain.ReplyPlan, preview maildomain.SendPreview) maildomain.ReplyPreview {
	return maildomain.ReplyPreview{
		ParentMessageID: plan.ParentMessageID, ParentRFCID: plan.ParentRFCID,
		ThreadID: plan.ThreadID, Kind: plan.Kind, Threading: plan.Threading,
		DerivedTo: plan.DerivedTo, DerivedCC: plan.DerivedCC,
		AddedTo: plan.AddedTo, AddedCC: plan.AddedCC, AddedBCC: plan.AddedBCC,
		ReplaceTo: plan.ReplaceTo, ReplaceCC: plan.ReplaceCC, Send: preview,
	}
}
