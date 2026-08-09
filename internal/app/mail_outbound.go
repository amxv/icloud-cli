package app

import (
	"context"
	"errors"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func (commands accountCommands) MailOutboundStatus(ctx context.Context, home, accountSelector string, limit int) (account.Account, []maildomain.OutboundAttempt, bool, int64, error) {
	accountValue, values, more, _, schemaVersion, err := commands.MailOutboundStatusPage(ctx, home, accountSelector, "", limit)
	return accountValue, values, more, schemaVersion, err
}

func (commands accountCommands) MailOutboundStatusPage(ctx context.Context, home, accountSelector, cursor string, limit int) (account.Account, []maildomain.OutboundAttempt, bool, string, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, nil, false, "", 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, nil, false, "", state.SchemaVersion(), mapAccountError(err)
	}
	values, more, nextCursor, err := state.Outbound().ListPage(ctx, accountValue.ID, limit, cursor)
	if err != nil {
		if errors.Is(err, maildomain.ErrInvalidListCursor) {
			return account.Account{}, nil, false, "", state.SchemaVersion(), cli.WrapAppError(cli.ErrorUsage, "outbound status cursor is invalid or belongs to another account", false, err)
		}
		return account.Account{}, nil, false, "", state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read outbound attempt status", false, err)
	}
	return accountValue, values, more, nextCursor, state.SchemaVersion(), nil
}
