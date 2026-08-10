package app

import (
	"context"
	"strings"

	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/store"
)

func enabledMailIdentityAddresses(ctx context.Context, state *store.Store, accountID string) ([]string, error) {
	if state == nil {
		return nil, cli.NewAppError(cli.ErrorInternal, "local mail identity state is unavailable", false)
	}
	identities, err := state.Identities().Enabled(ctx, accountID)
	if err != nil {
		return nil, cli.WrapAppError(cli.ErrorInternal, "cannot read enabled mail identities", false, err)
	}
	addresses := make([]string, 0, len(identities))
	for _, identity := range identities {
		if address := strings.ToLower(strings.TrimSpace(identity.Address)); address != "" {
			addresses = append(addresses, address)
		}
	}
	if len(addresses) == 0 {
		return nil, cli.NewAppError(cli.ErrorInternal, "selected account has no enabled mail identity", false)
	}
	return addresses, nil
}

func mailIdentityAddressSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}
