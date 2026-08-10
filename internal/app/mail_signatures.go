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

func (commands accountCommands) MailSignaturesList(ctx context.Context, home, accountSelector, cursor string, limit int) (account.Account, maildomain.SignaturePage, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.SignaturePage{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.SignaturePage{}, state.SchemaVersion(), mapAccountError(err)
	}
	page, err := state.Signatures().ListPage(ctx, accountValue.ID, cursor, limit)
	if err != nil {
		return account.Account{}, maildomain.SignaturePage{}, state.SchemaVersion(), mapSignatureError(err)
	}
	return accountValue, page, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSignatureShow(ctx context.Context, home, accountSelector, signatureID string) (account.Account, maildomain.Signature, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.Signature{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.Signature{}, state.SchemaVersion(), mapAccountError(err)
	}
	value, err := state.Signatures().Resolve(ctx, accountValue.ID, signatureID)
	if err != nil {
		return account.Account{}, maildomain.Signature{}, state.SchemaVersion(), mapSignatureError(err)
	}
	return accountValue, value, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSignatureCreate(ctx context.Context, home, accountSelector string, input maildomain.SignatureInput) (account.Account, maildomain.Signature, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.Signature{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.Signature{}, state.SchemaVersion(), mapAccountError(err)
	}
	input.IdentityID, err = resolveSignatureIdentitySelector(ctx, state, accountValue.ID, input.IdentityID, true)
	if err != nil {
		return account.Account{}, maildomain.Signature{}, state.SchemaVersion(), err
	}
	signatureID, err := commands.generateID("sig")
	if err != nil {
		return account.Account{}, maildomain.Signature{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate a mail signature ID", false, err)
	}
	value, err := state.Signatures().Create(ctx, accountValue.ID, signatureID, input, commands.timeNow())
	if err != nil {
		return account.Account{}, maildomain.Signature{}, state.SchemaVersion(), mapSignatureError(err)
	}
	return accountValue, value, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSignatureUpdate(ctx context.Context, home, accountSelector, signatureID string, update maildomain.SignatureUpdate) (account.Account, maildomain.Signature, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.Signature{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.Signature{}, state.SchemaVersion(), mapAccountError(err)
	}
	if update.IdentityID != nil {
		resolved, resolveErr := resolveSignatureIdentitySelector(ctx, state, accountValue.ID, *update.IdentityID, true)
		if resolveErr != nil {
			return account.Account{}, maildomain.Signature{}, state.SchemaVersion(), resolveErr
		}
		update.IdentityID = &resolved
	}
	value, err := state.Signatures().Update(ctx, accountValue.ID, signatureID, update, commands.timeNow())
	if err != nil {
		return account.Account{}, maildomain.Signature{}, state.SchemaVersion(), mapSignatureError(err)
	}
	return accountValue, value, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSignatureRemove(ctx context.Context, home, accountSelector, signatureID string) (account.Account, maildomain.Signature, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.Signature{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.Signature{}, state.SchemaVersion(), mapAccountError(err)
	}
	value, err := state.Signatures().Remove(ctx, accountValue.ID, signatureID)
	if err != nil {
		return account.Account{}, maildomain.Signature{}, state.SchemaVersion(), mapSignatureError(err)
	}
	return accountValue, value, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSignatureDefault(ctx context.Context, home, accountSelector, identitySelector string, operation maildomain.OutboundKind) (account.Account, maildomain.SignatureDefault, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.SignatureDefault{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.SignatureDefault{}, state.SchemaVersion(), mapAccountError(err)
	}
	identityID, err := resolveSignatureIdentitySelector(ctx, state, accountValue.ID, identitySelector, false)
	if err != nil {
		return account.Account{}, maildomain.SignatureDefault{}, state.SchemaVersion(), err
	}
	value, err := state.Signatures().Default(ctx, accountValue.ID, identityID, operation)
	if err != nil {
		return account.Account{}, maildomain.SignatureDefault{}, state.SchemaVersion(), mapSignatureError(err)
	}
	return accountValue, value, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSignatureSetDefault(ctx context.Context, home, accountSelector, identitySelector string, operation maildomain.OutboundKind, signatureSelector string) (account.Account, maildomain.SignatureDefault, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.SignatureDefault{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.SignatureDefault{}, state.SchemaVersion(), mapAccountError(err)
	}
	identityID, err := resolveSignatureIdentitySelector(ctx, state, accountValue.ID, identitySelector, false)
	if err != nil {
		return account.Account{}, maildomain.SignatureDefault{}, state.SchemaVersion(), err
	}
	value, err := state.Signatures().SetDefault(ctx, accountValue.ID, identityID, operation, signatureSelector, commands.timeNow())
	if err != nil {
		return account.Account{}, maildomain.SignatureDefault{}, state.SchemaVersion(), mapSignatureError(err)
	}
	return accountValue, value, state.SchemaVersion(), nil
}

func resolveSignatureIdentitySelector(ctx context.Context, state *store.Store, accountID, selector string, allowNone bool) (string, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" || (allowNone && strings.EqualFold(selector, "none")) {
		return "", nil
	}
	value, err := state.Identities().Resolve(ctx, accountID, selector)
	if err != nil {
		if errors.Is(err, account.ErrIdentityNotFound) {
			return "", cli.WrapAppError(cli.ErrorNotFound, "mail identity was not found", false, err)
		}
		return "", mapMailIdentityError(err)
	}
	return value.ID, nil
}
