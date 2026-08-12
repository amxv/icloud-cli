package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestSignatureDefaultsBindingsIsolationAndPagination(t *testing.T) {
	ctx := context.Background()
	state, err := Open(ctx, testPaths(t, filepath.Join(t.TempDir(), "state")))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	first := createIdentityTestAccount(t, state, 71, "signature-one@example.test")
	second := createIdentityTestAccount(t, state, 72, "signature-two@example.test")
	now := time.Date(2026, 8, 11, 6, 0, 0, 0, time.UTC)
	alias, err := state.Identities().Create(ctx, account.MailIdentityCreateRecord{
		IdentityID: "mid_signature_alias", AccountID: first.ID, Address: "alias-signature@example.test", DisplayName: "Signature Alias",
		Kind: account.MailIdentityICloudAlias, Provenance: account.MailIdentityUserConfigured, Verification: account.MailIdentityVerified,
		Enabled: true, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	primary, err := state.Identities().Default(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}

	unbound, err := state.Signatures().Create(ctx, first.ID, "sig_signature_unbound", maildomain.SignatureInput{TextBody: "Account signature"}, now)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := state.Signatures().Create(ctx, first.ID, "sig_signature_bound", maildomain.SignatureInput{IdentityID: alias.ID, TextBody: "Alias signature", HTMLBody: "<p>Alias signature</p>"}, now.Add(time.Second))
	if err != nil || bound.IdentityID != alias.ID {
		t.Fatalf("bound=%+v err=%v", bound, err)
	}
	if _, err := state.Signatures().Create(ctx, first.ID, "sig_signature_wrong_identity", maildomain.SignatureInput{IdentityID: "mid_missing", TextBody: "bad"}, now); !errors.Is(err, maildomain.ErrSignatureIdentity) {
		t.Fatalf("missing identity err=%v", err)
	}
	if _, err := state.Signatures().Resolve(ctx, second.ID, unbound.ID); !errors.Is(err, maildomain.ErrSignatureNotFound) {
		t.Fatalf("cross-account resolve err=%v", err)
	}

	page, err := state.Signatures().ListPage(ctx, first.ID, "", 1)
	if err != nil || len(page.Signatures) != 1 || !page.More || page.NextCursor == "" {
		t.Fatalf("first page=%+v err=%v", page, err)
	}
	if _, err := state.Signatures().ListPage(ctx, second.ID, page.NextCursor, 1); !errors.Is(err, maildomain.ErrSignatureInvalidCursor) {
		t.Fatalf("cross-account cursor err=%v", err)
	}
	next, err := state.Signatures().ListPage(ctx, first.ID, page.NextCursor, 1)
	if err != nil || len(next.Signatures) != 1 || next.Signatures[0].ID == page.Signatures[0].ID {
		t.Fatalf("next page=%+v err=%v", next, err)
	}

	accountDefault, err := state.Signatures().SetDefault(ctx, first.ID, "", maildomain.OutboundKindSend, unbound.ID, now.Add(time.Minute))
	if err != nil || accountDefault.SignatureID != unbound.ID || accountDefault.None {
		t.Fatalf("account default=%+v err=%v", accountDefault, err)
	}
	selected, err := state.Signatures().ResolveForCompose(ctx, first.ID, primary.ID, maildomain.OutboundKindSend, "")
	if err != nil || selected == nil || selected.ID != unbound.ID {
		t.Fatalf("fallback default=%+v err=%v", selected, err)
	}

	identityDefault, err := state.Signatures().SetDefault(ctx, first.ID, alias.ID, maildomain.OutboundKindReply, bound.ID, now.Add(2*time.Minute))
	if err != nil || identityDefault.SignatureID != bound.ID {
		t.Fatalf("identity default=%+v err=%v", identityDefault, err)
	}
	selected, err = state.Signatures().ResolveForCompose(ctx, first.ID, alias.ID, maildomain.OutboundKindReply, "")
	if err != nil || selected == nil || selected.ID != bound.ID {
		t.Fatalf("identity selection=%+v err=%v", selected, err)
	}
	if _, err := state.Signatures().SetDefault(ctx, first.ID, "", maildomain.OutboundKindForward, bound.ID, now); !errors.Is(err, maildomain.ErrSignatureIdentity) {
		t.Fatalf("bound account-wide default err=%v", err)
	}
	if _, err := state.Signatures().ResolveForCompose(ctx, first.ID, primary.ID, maildomain.OutboundKindSend, bound.ID); !errors.Is(err, maildomain.ErrSignatureIdentity) {
		t.Fatalf("wrong identity override err=%v", err)
	}
	none, err := state.Signatures().SetDefault(ctx, first.ID, alias.ID, maildomain.OutboundKindReplyAll, "none", now.Add(3*time.Minute))
	if err != nil || !none.None || none.SignatureID != "" {
		t.Fatalf("explicit none=%+v err=%v", none, err)
	}
	selected, err = state.Signatures().ResolveForCompose(ctx, first.ID, alias.ID, maildomain.OutboundKindReplyAll, "")
	if err != nil || selected != nil {
		t.Fatalf("explicit none resolved=%+v err=%v", selected, err)
	}

	changedText := "Alias signature v2"
	updated, err := state.Signatures().Update(ctx, first.ID, bound.ID, maildomain.SignatureUpdate{TextBody: &changedText}, now.Add(4*time.Minute))
	if err != nil || updated.TextBody != changedText {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	removed, err := state.Signatures().Remove(ctx, first.ID, bound.ID)
	if err != nil || removed.ID != bound.ID {
		t.Fatalf("removed=%+v err=%v", removed, err)
	}
	defaultAfterRemove, err := state.Signatures().Default(ctx, first.ID, alias.ID, maildomain.OutboundKindReply)
	if err != nil || !defaultAfterRemove.None || defaultAfterRemove.SignatureID != "" {
		t.Fatalf("default after cascade=%+v err=%v", defaultAfterRemove, err)
	}
}
