package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
)

const (
	defaultMailPurgeTTL = 15 * time.Minute
	maximumMailPurgeTTL = 24 * time.Hour
	maximumPurgeItems   = 100
)

func (commands accountCommands) MailPurgeCreate(ctx context.Context, home, accountSelector string, request maildomain.PurgeCreateRequest) (account.Account, folder.Folder, maildomain.MailPurge, int64, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), mapAccountError(err)
	}
	limit := request.Limit
	if limit == 0 {
		limit = maximumPurgeItems
	}
	if limit < 1 || limit > maximumPurgeItems {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorUsage, "mail purge limit must be between 1 and 100", false)
	}
	ttl := request.TTL
	if ttl == 0 {
		ttl = defaultMailPurgeTTL
	}
	if ttl < time.Minute || ttl > maximumMailPurgeTTL {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorUsage, "mail purge TTL must be between 1m and 24h", false)
	}
	folderValue, err := resolvePurgeFolder(ctx, state, accountValue.ID, request)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), err
	}
	locations, more, blockedUnresolved, err := state.Mail().PurgeLocationsPage(ctx, accountValue.ID, folderValue.ID, limit)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot enumerate exact current purge locations", false, err)
	}
	if len(locations) == 0 {
		return accountValue, folderValue, maildomain.MailPurge{
			AccountID: accountValue.ID, FolderID: folderValue.ID, ScopeKind: request.ScopeKind,
			ItemCount: 0, More: false, BlockedUnresolved: blockedUnresolved,
		}, state.SchemaVersion(), nil
	}
	items := make([]maildomain.MailPurgeItem, 0, len(locations))
	for index, location := range locations {
		items = append(items, maildomain.MailPurgeItem{
			Ordinal: index, MessageID: location.MessageID, LocationID: location.ID,
			UIDValidity: location.UIDValidity, UID: location.UID, ModSequence: location.ModSequence,
		})
	}
	now := commands.timeNow()
	purge := maildomain.MailPurge{
		AccountID: accountValue.ID, FolderID: folderValue.ID, ScopeKind: request.ScopeKind,
		ItemCount: len(items), More: more, BlockedUnresolved: blockedUnresolved,
		CreatedAt: now, ExpiresAt: now.Add(ttl), Items: items,
	}
	purge.RequestHash, err = mailPurgeHash(purge)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot hash mail purge review", false, err)
	}
	purge.ID, err = commands.generateID("pur")
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate mail purge ID", false, err)
	}
	purge, err = state.Purges().Create(ctx, purge)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot persist mail purge review", false, err)
	}
	return accountValue, folderValue, purge, state.SchemaVersion(), nil
}

func (commands accountCommands) MailPurge(ctx context.Context, home, accountSelector, purgeID string, request maildomain.PurgeCreateRequest) (account.Account, folder.Folder, maildomain.MailPurge, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), mapAccountError(err)
	}
	purge, err := state.Purges().ByID(ctx, strings.TrimSpace(purgeID))
	if err != nil {
		if errors.Is(err, maildomain.ErrPurgeNotFound) {
			return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "mail purge review not found", false, err)
		}
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read mail purge review", false, err)
	}
	if purge.AccountID != accountValue.ID {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorConflict, "mail purge review belongs to another account", false)
	}
	if purge.Expired(commands.timeNow()) {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "mail purge review expired; create and review a new bounded purge page", false, maildomain.ErrPurgeExpired)
	}
	if purge.ScopeKind != request.ScopeKind {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorConflict, "mail purge review belongs to a different purge scope", false)
	}
	folderValue, err := resolvePurgeFolder(ctx, state, accountValue.ID, request)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), err
	}
	if folderValue.ID != purge.FolderID {
		return account.Account{}, folder.Folder{}, maildomain.MailPurge{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorConflict, "mail purge source folder no longer matches the reviewed semantic/custom-folder scope", false)
	}
	return accountValue, folderValue, purge, state.SchemaVersion(), nil
}

func resolvePurgeFolder(ctx context.Context, state *store.Store, accountID string, request maildomain.PurgeCreateRequest) (folder.Folder, error) {
	switch request.ScopeKind {
	case maildomain.PurgeScopeTrash:
		if strings.TrimSpace(request.FolderSelector) != "" {
			return folder.Folder{}, cli.NewAppError(cli.ErrorUsage, "empty trash does not accept an explicit folder; configure the Trash role when needed", false)
		}
		resolution, err := resolveSemanticFolder(ctx, state, accountID, folder.RoleTrash)
		return resolution.Folder, err
	case maildomain.PurgeScopeJunk:
		if strings.TrimSpace(request.FolderSelector) != "" {
			return folder.Folder{}, cli.NewAppError(cli.ErrorUsage, "empty junk does not accept an explicit folder; configure the Junk role when needed", false)
		}
		resolution, err := resolveSemanticFolder(ctx, state, accountID, folder.RoleJunk)
		return resolution.Folder, err
	case maildomain.PurgeScopeFolder:
		selector := strings.TrimSpace(request.FolderSelector)
		if selector == "" {
			return folder.Folder{}, cli.NewAppError(cli.ErrorUsage, "empty folder requires one deliberately named custom folder", false)
		}
		folderValue, err := state.Folders().Resolve(ctx, accountID, selector)
		if err != nil {
			return folder.Folder{}, mapFolderError(err)
		}
		if !folderValue.Present || folderValue.Stale || !folderValue.Selectable {
			return folder.Folder{}, cli.NewAppError(cli.ErrorConflict, "selected purge folder is not current and selectable", false)
		}
		kind, err := semanticFolderKind(ctx, state, accountID, folderValue)
		if err != nil {
			return folder.Folder{}, err
		}
		if kind != folder.KindOther {
			return folder.Folder{}, cli.NewAppError(cli.ErrorSafety, "empty folder only accepts an explicitly named ordinary custom folder; use empty trash or empty junk for those semantic roles", false)
		}
		return folderValue, nil
	default:
		return folder.Folder{}, cli.NewAppError(cli.ErrorUsage, "mail purge scope must be trash, junk, or folder", false)
	}
}

func mailPurgeHash(purge maildomain.MailPurge) (string, error) {
	return hashSelectionValue(struct {
		AccountID string                     `json:"account_id"`
		FolderID  string                     `json:"folder_id"`
		ScopeKind maildomain.PurgeScopeKind  `json:"scope_kind"`
		Items     []maildomain.MailPurgeItem `json:"items"`
	}{AccountID: purge.AccountID, FolderID: purge.FolderID, ScopeKind: purge.ScopeKind, Items: purge.Items})
}

func mailPurgeApplyHash(purge maildomain.MailPurge, mode string, planHashes []string) (string, error) {
	return hashSelectionValue(struct {
		PurgeID    string   `json:"purge_id"`
		PurgeHash  string   `json:"purge_hash"`
		Mode       string   `json:"mode"`
		PlanHashes []string `json:"plan_hashes"`
	}{PurgeID: purge.ID, PurgeHash: purge.RequestHash, Mode: strings.TrimSpace(mode), PlanHashes: planHashes})
}

func (commands accountCommands) MailPurgeApplyHash(_ context.Context, purge maildomain.MailPurge, mode string, planHashes []string) (string, error) {
	return mailPurgeApplyHash(purge, mode, planHashes)
}
