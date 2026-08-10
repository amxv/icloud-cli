package app

import (
	"context"
	"errors"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	"github.com/amxv/icloud-cli/internal/store"
)

func (commands accountCommands) FolderRoleList(
	ctx context.Context,
	home, accountSelector string,
) (account.Account, []folder.FolderRoleBinding, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, nil, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, nil, state.SchemaVersion(), mapAccountError(err)
	}
	bindings, err := state.FolderRoles().List(ctx, accountValue.ID)
	if err != nil {
		return account.Account{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read local folder role bindings", false, err)
	}
	return accountValue, bindings, state.SchemaVersion(), nil
}

func (commands accountCommands) FolderRoleSetPreview(
	ctx context.Context,
	home, accountSelector, roleValue, folderSelector string,
) (account.Account, folder.FolderRolePlan, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, folder.FolderRolePlan{}, 0, err
	}
	defer state.Close()
	accountValue, plan, err := buildFolderRoleSetPlan(ctx, state, accountSelector, roleValue, folderSelector)
	if err != nil {
		return account.Account{}, folder.FolderRolePlan{}, state.SchemaVersion(), err
	}
	return accountValue, plan, state.SchemaVersion(), nil
}

func (commands accountCommands) FolderRoleSet(
	ctx context.Context,
	home, accountSelector, roleValue, folderSelector string,
) (account.Account, folder.FolderRoleBinding, folder.Folder, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.FolderRoleBinding{}, folder.Folder{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, plan, err := buildFolderRoleSetPlan(ctx, state, accountSelector, roleValue, folderSelector)
	if err != nil {
		return account.Account{}, folder.FolderRoleBinding{}, folder.Folder{}, state.SchemaVersion(), err
	}
	binding, err := state.FolderRoles().Set(ctx, accountValue.ID, plan.Role, plan.FolderID, commands.timeNow())
	if err != nil {
		return account.Account{}, folder.FolderRoleBinding{}, folder.Folder{}, state.SchemaVersion(), mapFolderRoleError(err)
	}
	folderValue, err := state.Folders().Resolve(ctx, accountValue.ID, binding.FolderID)
	if err != nil {
		return account.Account{}, folder.FolderRoleBinding{}, folder.Folder{}, state.SchemaVersion(), mapFolderRoleError(err)
	}
	return accountValue, binding, folderValue, state.SchemaVersion(), nil
}

func (commands accountCommands) FolderRoleClearPreview(
	ctx context.Context,
	home, accountSelector, roleValue string,
) (account.Account, folder.FolderRolePlan, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, folder.FolderRolePlan{}, 0, err
	}
	defer state.Close()
	accountValue, plan, err := buildFolderRoleClearPlan(ctx, state, accountSelector, roleValue)
	if err != nil {
		return account.Account{}, folder.FolderRolePlan{}, state.SchemaVersion(), err
	}
	return accountValue, plan, state.SchemaVersion(), nil
}

func (commands accountCommands) FolderRoleClear(
	ctx context.Context,
	home, accountSelector, roleValue string,
) (account.Account, folder.FolderRoleBinding, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.FolderRoleBinding{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, plan, err := buildFolderRoleClearPlan(ctx, state, accountSelector, roleValue)
	if err != nil {
		return account.Account{}, folder.FolderRoleBinding{}, state.SchemaVersion(), err
	}
	binding, err := state.FolderRoles().Clear(ctx, accountValue.ID, plan.Role)
	if err != nil {
		return account.Account{}, folder.FolderRoleBinding{}, state.SchemaVersion(), mapFolderRoleError(err)
	}
	return accountValue, binding, state.SchemaVersion(), nil
}

func buildFolderRoleSetPlan(
	ctx context.Context,
	state *store.Store,
	accountSelector, roleValue, folderSelector string,
) (account.Account, folder.FolderRolePlan, error) {
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.FolderRolePlan{}, mapAccountError(err)
	}
	role, err := folder.NormalizeRole(roleValue)
	if err != nil || !role.Bindable() {
		return account.Account{}, folder.FolderRolePlan{}, mapFolderRoleError(folder.ErrInvalidRole)
	}
	selected, err := state.Folders().Resolve(ctx, accountValue.ID, strings.TrimSpace(folderSelector))
	if err != nil {
		return account.Account{}, folder.FolderRolePlan{}, mapFolderError(err)
	}
	if !selected.Present || selected.Stale || !selected.Selectable {
		return account.Account{}, folder.FolderRolePlan{}, mapFolderRoleError(&folder.InvalidRoleBindingError{
			Binding: folder.FolderRoleBinding{AccountID: accountValue.ID, Role: role, FolderID: selected.ID},
			Reason:  "selected folder is not current and selectable",
		})
	}
	plan := folder.FolderRolePlan{
		Action:           folder.RoleActionSet,
		Role:             role,
		FolderID:         selected.ID,
		FolderName:       selected.Name,
		FolderKind:       selected.Kind,
		FolderKindSource: selected.KindSource,
	}
	if previous, readErr := state.FolderRoles().Binding(ctx, accountValue.ID, role); readErr == nil {
		plan.PreviousBinding = &previous
	} else if !errors.Is(readErr, folder.ErrRoleBindingMissing) {
		return account.Account{}, folder.FolderRolePlan{}, mapFolderRoleError(readErr)
	}
	bindings, err := state.FolderRoles().List(ctx, accountValue.ID)
	if err != nil {
		return account.Account{}, folder.FolderRolePlan{}, cli.WrapAppError(cli.ErrorInternal, "cannot inspect existing folder role bindings", false, err)
	}
	for _, binding := range bindings {
		if binding.FolderID == selected.ID && binding.Role != role {
			return account.Account{}, folder.FolderRolePlan{}, mapFolderRoleError(folder.ErrRoleAlreadyBound)
		}
	}
	return accountValue, plan, nil
}

func buildFolderRoleClearPlan(
	ctx context.Context,
	state *store.Store,
	accountSelector, roleValue string,
) (account.Account, folder.FolderRolePlan, error) {
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.FolderRolePlan{}, mapAccountError(err)
	}
	role, err := folder.NormalizeRole(roleValue)
	if err != nil || !role.Bindable() {
		return account.Account{}, folder.FolderRolePlan{}, mapFolderRoleError(folder.ErrInvalidRole)
	}
	binding, err := state.FolderRoles().Binding(ctx, accountValue.ID, role)
	if err != nil {
		return account.Account{}, folder.FolderRolePlan{}, mapFolderRoleError(err)
	}
	plan := folder.FolderRolePlan{Action: folder.RoleActionClear, Role: role, FolderID: binding.FolderID, PreviousBinding: &binding}
	if selected, readErr := state.Folders().Resolve(ctx, accountValue.ID, binding.FolderID); readErr == nil {
		plan.FolderName = selected.Name
		plan.FolderKind = selected.Kind
		plan.FolderKindSource = selected.KindSource
	}
	return accountValue, plan, nil
}

func mapFolderRoleError(err error) error {
	switch {
	case errors.Is(err, folder.ErrInvalidRole):
		return cli.WrapAppError(cli.ErrorUsage, "folder role must be one of sent, drafts, archive, trash, or junk", false, err)
	case errors.Is(err, folder.ErrRoleBindingMissing):
		return cli.WrapAppError(cli.ErrorNotFound, "folder role binding not found", false, err)
	case errors.Is(err, folder.ErrRoleAlreadyBound):
		return cli.WrapAppError(cli.ErrorConflict, "a folder can be bound to only one semantic role; clear the existing binding first", false, err)
	case errors.Is(err, folder.ErrRoleBindingInvalid):
		appErr := cli.WrapAppError(cli.ErrorConflict, "folder role binding is stale, missing, or not selectable; clear or rebind the role before retrying", false, err)
		var invalid *folder.InvalidRoleBindingError
		if errors.As(err, &invalid) {
			appErr.Details = map[string]any{"role": invalid.Binding.Role, "folder_id": invalid.Binding.FolderID, "recovery": "mail folders role clear " + string(invalid.Binding.Role) + " or role set to a current folder"}
		}
		return appErr
	case errors.Is(err, folder.ErrNotFound):
		return mapFolderError(err)
	case errors.Is(err, folder.ErrAmbiguous):
		return mapFolderError(err)
	default:
		return cli.WrapAppError(cli.ErrorInternal, "cannot manage local folder role binding", false, err)
	}
}

func resolveSemanticFolder(ctx context.Context, state *store.Store, accountID string, role folder.Role) (folder.FolderRoleResolution, error) {
	resolution, err := state.FolderRoles().Resolve(ctx, accountID, role)
	if err != nil {
		return folder.FolderRoleResolution{}, mapFolderRoleError(err)
	}
	return resolution, nil
}

func resolveOptionalSemanticFolder(ctx context.Context, state *store.Store, accountID string, role folder.Role) (folder.FolderRoleResolution, bool, error) {
	resolution, err := state.FolderRoles().Resolve(ctx, accountID, role)
	if err == nil {
		return resolution, true, nil
	}
	if errors.Is(err, folder.ErrNotFound) {
		return folder.FolderRoleResolution{}, false, nil
	}
	return folder.FolderRoleResolution{}, false, mapFolderRoleError(err)
}

func semanticFolderKind(ctx context.Context, state *store.Store, accountID string, folderValue folder.Folder) (folder.Kind, error) {
	role, bound, err := state.FolderRoles().RoleForFolder(ctx, accountID, folderValue.ID)
	if err != nil {
		return folder.KindOther, cli.WrapAppError(cli.ErrorInternal, "cannot resolve local semantic folder role", false, err)
	}
	if bound {
		return role.Kind(), nil
	}
	return folderValue.Kind, nil
}
