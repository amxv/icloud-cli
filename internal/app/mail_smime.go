package app

import (
	"context"
	"crypto/x509"
	"fmt"

	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
)

func (commands accountCommands) inboundSMIMEPolicy(ctx context.Context, state *store.Store, accountID string) (*maildomain.SMIMEPolicy, error) {
	revision, err := state.SMIMECertificates().PolicyRevision(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("read account S/MIME policy revision: %w", err)
	}
	materials, err := state.SMIMECertificates().Materials(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("read account S/MIME certificates: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		// System roots are an optional trust source. A platform without a usable
		// root pool can still evaluate cryptographic validity and explicitly
		// trusted imported roots without making ordinary message parsing fail.
		roots = x509.NewCertPool()
	}
	return &maildomain.SMIMEPolicy{Roots: roots, Certificates: materials, Now: commands.timeNow(), Revision: revision}, nil
}
