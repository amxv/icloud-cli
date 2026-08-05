package app

import (
	"context"
	"errors"
	"os"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/store"
)

type accountCommands struct{}

func (accountCommands) List(ctx context.Context, home string, limit int) ([]account.Account, bool, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return nil, false, 0, err
	}
	defer state.Close()

	accounts, more, err := state.Accounts().List(ctx, limit)
	if err != nil {
		return nil, false, 0, cli.WrapAppError(cli.ErrorInternal, "cannot read local accounts", false, err)
	}
	return accounts, more, state.SchemaVersion(), nil
}

func (accountCommands) Show(ctx context.Context, home, selector string) (account.Account, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, 0, err
	}
	defer state.Close()

	value, err := state.Accounts().Resolve(ctx, selector)
	if err != nil {
		return account.Account{}, state.SchemaVersion(), err
	}
	return value, state.SchemaVersion(), nil
}

func openState(ctx context.Context, home string) (*store.Store, error) {
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		return nil, cli.WrapAppError(cli.ErrorConfiguration, "cannot resolve the iCloud application data directory", false, err)
	}
	state, err := store.Open(ctx, paths)
	if err == nil {
		return state, nil
	}
	if errors.Is(err, store.ErrMigrationLock) {
		return nil, cli.WrapAppError(cli.ErrorConflict, "local state is busy while another icloud process migrates it", true, err)
	}
	if errors.Is(err, os.ErrPermission) {
		return nil, cli.WrapAppError(cli.ErrorConfiguration, "cannot access the iCloud application data directory", false, err)
	}
	return nil, cli.WrapAppError(cli.ErrorInternal, "cannot open local iCloud state", false, err)
}
