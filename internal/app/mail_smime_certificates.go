package app

import (
	"context"
	"errors"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func (commands accountCommands) MailSMIMECertificatesList(ctx context.Context, home, accountSelector, email, cursor string, limit int) (account.Account, maildomain.SMIMECertificatePage, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.SMIMECertificatePage{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.SMIMECertificatePage{}, state.SchemaVersion(), mapAccountError(err)
	}
	page, err := state.SMIMECertificates().ListPage(ctx, accountValue.ID, strings.TrimSpace(email), cursor, limit)
	if err != nil {
		return account.Account{}, maildomain.SMIMECertificatePage{}, state.SchemaVersion(), mapSMIMECertificateError(err)
	}
	return accountValue, page, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSMIMECertificateImport(ctx context.Context, home, accountSelector, email string, certificateData []byte, trusted bool) (account.Account, maildomain.SMIMECertificate, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.SMIMECertificate{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.SMIMECertificate{}, state.SchemaVersion(), mapAccountError(err)
	}
	cert, err := maildomain.ParseSMIMECertificate(certificateData)
	if err != nil {
		return account.Account{}, maildomain.SMIMECertificate{}, state.SchemaVersion(), mapSMIMECertificateError(err)
	}
	certificateID, err := commands.generateID("smc")
	if err != nil {
		return account.Account{}, maildomain.SMIMECertificate{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate an S/MIME certificate ID", false, err)
	}
	value, err := state.SMIMECertificates().Import(ctx, accountValue.ID, certificateID, email, cert, trusted, commands.timeNow())
	if err != nil {
		return account.Account{}, maildomain.SMIMECertificate{}, state.SchemaVersion(), mapSMIMECertificateError(err)
	}
	return accountValue, value, state.SchemaVersion(), nil
}

func (commands accountCommands) MailSMIMECertificateRemove(ctx context.Context, home, accountSelector, certificateID string) (account.Account, maildomain.SMIMECertificate, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.SMIMECertificate{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.SMIMECertificate{}, state.SchemaVersion(), mapAccountError(err)
	}
	value, err := state.SMIMECertificates().Remove(ctx, accountValue.ID, certificateID, commands.timeNow())
	if err != nil {
		return account.Account{}, maildomain.SMIMECertificate{}, state.SchemaVersion(), mapSMIMECertificateError(err)
	}
	return accountValue, value, state.SchemaVersion(), nil
}

func mapSMIMECertificateError(err error) error {
	switch {
	case errors.Is(err, maildomain.ErrSMIMECertificateNotFound):
		return cli.WrapAppError(cli.ErrorNotFound, "S/MIME certificate was not found", false, err)
	case errors.Is(err, maildomain.ErrSMIMECertificateLimit):
		return cli.WrapAppError(cli.ErrorConflict, "S/MIME certificate inventory reached its bounded account limit; remove an old certificate first", false, err)
	case errors.Is(err, maildomain.ErrSMIMEIdentityConflict):
		return cli.WrapAppError(cli.ErrorConflict, "remove the S/MIME identity configuration before removing this certificate", false, err)
	case errors.Is(err, maildomain.ErrSMIMECertificateInvalid), errors.Is(err, maildomain.ErrSMIMEInvalidCursor):
		return cli.WrapAppError(cli.ErrorUsage, "S/MIME certificate request is invalid", false, err)
	default:
		return cli.WrapAppError(cli.ErrorInternal, "cannot resolve local S/MIME certificate state", false, err)
	}
}
