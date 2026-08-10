package app

import (
	"context"
	"errors"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func (commands accountCommands) MailSendersList(ctx context.Context, home, accountSelector, cursor string, limit int) (account.Account, maildomain.SenderPreferencePage, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.SenderPreferencePage{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.SenderPreferencePage{}, state.SchemaVersion(), mapAccountError(err)
	}
	page, err := state.SenderPreferences().ListPage(ctx, accountValue.ID, cursor, limit)
	if err != nil {
		return accountValue, maildomain.SenderPreferencePage{}, state.SchemaVersion(), mapSenderPreferenceError(err)
	}
	return accountValue, page, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSenderShow(ctx context.Context, home, accountSelector, email string) (account.Account, maildomain.SenderPreference, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.SenderPreference{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.SenderPreference{}, state.SchemaVersion(), mapAccountError(err)
	}
	value, err := state.SenderPreferences().Get(ctx, accountValue.ID, email)
	if err != nil {
		return accountValue, maildomain.SenderPreference{}, state.SchemaVersion(), mapSenderPreferenceError(err)
	}
	return accountValue, value, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSenderSetVIP(ctx context.Context, home, accountSelector, email string, value bool) (account.Account, maildomain.SenderPreference, int64, error) {
	return commands.mailSenderSetFlag(ctx, home, accountSelector, email, "vip", value)
}

func (commands accountCommands) MailSenderSetBlocked(ctx context.Context, home, accountSelector, email string, value bool) (account.Account, maildomain.SenderPreference, int64, error) {
	return commands.mailSenderSetFlag(ctx, home, accountSelector, email, "blocked", value)
}

func (commands accountCommands) mailSenderSetFlag(ctx context.Context, home, accountSelector, email, flag string, value bool) (account.Account, maildomain.SenderPreference, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.SenderPreference{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.SenderPreference{}, state.SchemaVersion(), mapAccountError(err)
	}
	var preference maildomain.SenderPreference
	switch flag {
	case "vip":
		preference, err = state.SenderPreferences().SetVIP(ctx, accountValue.ID, email, value, commands.timeNow())
	case "blocked":
		preference, err = state.SenderPreferences().SetBlocked(ctx, accountValue.ID, email, value, commands.timeNow())
	default:
		err = maildomain.ErrInvalidSenderPreference
	}
	if err != nil {
		return accountValue, maildomain.SenderPreference{}, state.SchemaVersion(), mapSenderPreferenceError(err)
	}
	return accountValue, preference, state.SchemaVersion(), nil
}

func mapSenderPreferenceError(err error) error {
	switch {
	case errors.Is(err, maildomain.ErrSenderPreferenceNotFound):
		return cli.WrapAppError(cli.ErrorNotFound, "sender preference was not found", false, err)
	case errors.Is(err, maildomain.ErrInvalidSenderPreference), errors.Is(err, maildomain.ErrInvalidListCursor):
		return cli.WrapAppError(cli.ErrorUsage, "sender preference email, cursor, or page request is invalid", false, err)
	default:
		return cli.WrapAppError(cli.ErrorInternal, "cannot read or update sender preference", false, err)
	}
}
