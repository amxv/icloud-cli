package app

import (
	"context"
	"errors"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
)

func (commands accountCommands) MailSettingsShow(ctx context.Context, home, accountSelector string, verbose bool) (account.Account, maildomain.MailSettings, int64, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.MailSettings{}, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.MailSettings{}, state.SchemaVersion(), mapAccountError(err)
	}

	identities, err := listAllMailIdentities(ctx, state, accountValue.ID)
	if err != nil {
		return accountValue, maildomain.MailSettings{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read local mail identities for settings", false, err)
	}
	defaultIdentity, err := state.Identities().Default(ctx, accountValue.ID)
	if err != nil {
		return accountValue, maildomain.MailSettings{}, state.SchemaVersion(), mapMailIdentityError(err)
	}

	signatureCount, err := state.Signatures().Count(ctx, accountValue.ID)
	if err != nil {
		return accountValue, maildomain.MailSettings{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot count local signatures for settings", false, err)
	}
	signatureDefaults := make([]maildomain.SettingsSignatureDefault, 0, 4)
	for _, operation := range []maildomain.OutboundKind{maildomain.OutboundKindSend, maildomain.OutboundKindReply, maildomain.OutboundKindReplyAll, maildomain.OutboundKindForward} {
		value, defaultErr := state.Signatures().Default(ctx, accountValue.ID, defaultIdentity.ID, operation)
		if defaultErr != nil {
			return accountValue, maildomain.MailSettings{}, state.SchemaVersion(), mapSignatureError(defaultErr)
		}
		signatureDefaults = append(signatureDefaults, maildomain.SettingsSignatureDefault{
			Operation: operation, IdentityID: value.IdentityID, SignatureID: value.SignatureID, None: value.None,
		})
	}

	folderRoles, err := mailSettingsFolderRoles(ctx, state.FolderRoles(), accountValue.ID)
	if err != nil {
		return accountValue, maildomain.MailSettings{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read semantic folder settings", false, err)
	}
	senderTotal, vipSenders, blockedSenders, err := state.SenderPreferences().Counts(ctx, accountValue.ID)
	if err != nil {
		return accountValue, maildomain.MailSettings{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot count sender preferences for settings", false, err)
	}
	threadTotal, mutedThreads, err := state.Threads().PreferenceCounts(ctx, accountValue.ID)
	if err != nil {
		return accountValue, maildomain.MailSettings{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot count thread preferences for settings", false, err)
	}

	enabled := 0
	for _, identity := range identities {
		if identity.Enabled {
			enabled++
		}
	}
	settings := maildomain.MailSettings{
		AccountID: accountValue.ID,
		Local: maildomain.LocalMailSettings{
			DefaultIdentity: maildomain.SettingsIdentity{
				ID: defaultIdentity.ID, Address: defaultIdentity.Address, DisplayName: defaultIdentity.DisplayName, Label: defaultIdentity.Label,
				Kind: string(defaultIdentity.Kind), Verification: string(defaultIdentity.Verification),
			},
			IdentityCount: len(identities), EnabledIdentities: enabled, SignatureCount: signatureCount,
			SignatureDefaults: signatureDefaults, FolderRoles: folderRoles,
			Preferences: maildomain.SettingsPreferenceCounts{
				SenderRecords: senderTotal, VIPSenders: vipSenders, BlockedSenders: blockedSenders,
				ThreadRecords: threadTotal, MutedThreads: mutedThreads,
			},
		},
		Compatibility:    mailSettingsCompatibility(identities, verbose),
		ProviderSettings: mailProviderSettingsAvailability(verbose),
		LocalOnlyRead:    true,
	}
	return accountValue, settings, state.SchemaVersion(), nil
}

func listAllMailIdentities(ctx context.Context, state *store.Store, accountID string) ([]account.MailIdentity, error) {
	return listAllIdentityPages(ctx, state.Identities(), accountID)
}

type mailIdentityPager interface {
	ListPage(context.Context, string, int, string) ([]account.MailIdentity, bool, string, error)
}

func listAllIdentityPages(ctx context.Context, repository mailIdentityPager, accountID string) ([]account.MailIdentity, error) {
	values := make([]account.MailIdentity, 0, 8)
	cursor := ""
	for {
		page, more, next, err := repository.ListPage(ctx, accountID, cli.MaximumPageSize, cursor)
		if err != nil {
			return nil, err
		}
		values = append(values, page...)
		if !more {
			return values, nil
		}
		if next == "" {
			return nil, account.ErrInvalidInput
		}
		cursor = next
	}
}

type folderRoleReader interface {
	List(context.Context, string) ([]folder.FolderRoleBinding, error)
	Resolve(context.Context, string, folder.Role) (folder.FolderRoleResolution, error)
}

func mailSettingsFolderRoles(ctx context.Context, repository folderRoleReader, accountID string) ([]maildomain.SettingsFolderRole, error) {
	bindings, err := repository.List(ctx, accountID)
	if err != nil {
		return nil, err
	}
	configured := make(map[folder.Role]bool, len(bindings))
	for _, binding := range bindings {
		configured[binding.Role] = true
	}
	roles := []folder.Role{folder.RoleInbox, folder.RoleSent, folder.RoleDrafts, folder.RoleArchive, folder.RoleTrash, folder.RoleJunk}
	result := make([]maildomain.SettingsFolderRole, 0, len(roles))
	for _, role := range roles {
		value := maildomain.SettingsFolderRole{Role: string(role), Configured: configured[role]}
		resolution, resolveErr := repository.Resolve(ctx, accountID, role)
		switch {
		case resolveErr == nil:
			value.FolderID = resolution.Folder.ID
			value.FolderName = resolution.Folder.Name
			value.Source = string(resolution.Source)
			value.Resolved = true
		case errors.Is(resolveErr, folder.ErrNotFound), errors.Is(resolveErr, folder.ErrRoleBindingInvalid):
			// Keep unresolved semantic roles visible without turning an ordinary
			// missing/stale local binding into a failed settings read.
		default:
			return nil, resolveErr
		}
		result = append(result, value)
	}
	return result, nil
}

func mailSettingsCompatibility(identities []account.MailIdentity, verbose bool) maildomain.ProviderCompatibility {
	alternate := maildomain.SettingsUnconfigured
	hme := maildomain.SettingsUnavailable
	for _, identity := range identities {
		if identity.Kind == account.MailIdentityPrimary {
			continue
		}
		status := verificationSettingsStatus(identity.Verification)
		if identity.Kind == account.MailIdentityHideMyEmail {
			hme = strongerSettingsStatus(hme, status)
			continue
		}
		alternate = strongerSettingsStatus(alternate, status)
	}
	capability := func(status maildomain.SettingsStatus, action string) maildomain.SettingsCapability {
		value := maildomain.SettingsCapability{Status: status}
		if verbose {
			value.NextAction = action
		}
		return value
	}
	return maildomain.ProviderCompatibility{
		AlternateFrom:       capability(alternate, "Verify an exact alternate sender with iCloud SMTP before relying on it."),
		ReplyAnswered:       capability(maildomain.SettingsSupported, "No action required; replied state is reconciled from provider \\Answered metadata."),
		ForwardedState:      capability(maildomain.SettingsUnsupported, "Use local outbound history; iCloud has not exposed a proven forwarded-state mapping to this client."),
		ColoredFlags:        capability(maildomain.SettingsUnsupported, "Use portable \\Flagged; manage Apple Mail flag colors in Apple Mail until a provider mapping is proven."),
		HideMyEmailOutbound: capability(hme, "Configure only a pre-existing HME identity; outbound use requires durable accepted-SMTP evidence for that exact address."),
		SMIMEInspection:     capability(maildomain.SettingsSupported, "Inbound S/MIME signature inspection and public certificate trust are available locally."),
		SMIMEDecryption:     capability(maildomain.SettingsSupported, "Configure an S/MIME identity with an external private-key reference, then use message show --decrypt."),
		SMIME:               capability(maildomain.SettingsSupported, "Outbound send/reply/forward and draft-send support explicit S/MIME sign, encrypt, and sign-then-encrypt modes using configured identities and trusted recipient certificates."),
	}
}

func verificationSettingsStatus(value account.MailIdentityVerification) maildomain.SettingsStatus {
	switch value {
	case account.MailIdentityVerified:
		return maildomain.SettingsSupported
	case account.MailIdentityUnverified:
		return maildomain.SettingsUnverified
	default:
		return maildomain.SettingsUnavailable
	}
}

func strongerSettingsStatus(current, candidate maildomain.SettingsStatus) maildomain.SettingsStatus {
	rank := func(value maildomain.SettingsStatus) int {
		switch value {
		case maildomain.SettingsSupported:
			return 4
		case maildomain.SettingsUnverified:
			return 3
		case maildomain.SettingsUnavailable:
			return 2
		case maildomain.SettingsUnconfigured:
			return 1
		default:
			return 0
		}
	}
	if rank(candidate) > rank(current) {
		return candidate
	}
	return current
}

func mailProviderSettingsAvailability(verbose bool) maildomain.ProviderSettingsAvailability {
	unsupported := func(action string) maildomain.SettingsCapability {
		value := maildomain.SettingsCapability{Status: maildomain.SettingsUnsupportedCurrentAuth}
		if verbose {
			value.NextAction = action
		}
		return value
	}
	return maildomain.ProviderSettingsAvailability{
		AutomaticForwarding:        unsupported("Manage Mail Forwarding in Mail settings on iCloud.com."),
		AutomaticReply:             unsupported("Manage Auto-Reply in Mail settings on iCloud.com."),
		ServerRules:                unsupported("Manage iCloud Mail rules in signed-in Apple/iCloud settings."),
		AliasProvisioning:          unsupported("Create, enable, disable, or delete iCloud Mail aliases in Mail settings on iCloud.com."),
		CustomDomainProvisioning:   unsupported("Manage Custom Email Domain addresses in iCloud+ settings."),
		HideMyEmailManagement:      unsupported("Create and manage Hide My Email addresses and forwarding in iCloud+ settings."),
		CategoryViews:              unsupported("Manage Mail Categories and sender categorization in Mail settings on iCloud.com."),
		ProviderDefaultSendAddress: unsupported("Manage Apple's Default Send Address in Mail settings on iCloud.com; the CLI default identity is local client state."),
	}
}
