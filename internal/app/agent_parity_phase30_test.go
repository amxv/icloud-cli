package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/config"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
)

func TestAgentParityPhase30MailSettingsShowAggregatesLocalTruthWithoutProviderAccess(t *testing.T) {
	home := t.TempDir()
	commands, _ := newTestAccountCommands()
	addSendAccount(t, commands, home)
	primary := phase5PrimaryIdentity(t, commands, home)

	_, alias, _, err := commands.IdentityAdd(context.Background(), home, "", account.MailIdentityAddInput{
		Address: "verified-alias@icloud.com", Kind: account.MailIdentityICloudAlias,
	})
	if err != nil {
		t.Fatal(err)
	}
	phase29HideMyEmailFixture(t, commands, home, "local-hme@icloud.com", "Private", account.MailIdentityUnverified)
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = state.Identities().SetVerification(context.Background(), alias.AccountID, alias.ID, account.MailIdentityVerified, time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)); err != nil {
		state.Close()
		t.Fatal(err)
	}
	state.Close()

	_, signature, _, err := commands.MailSignatureCreate(context.Background(), home, "", maildomain.SignatureInput{TextBody: "Local signature"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = commands.MailSignatureSetDefault(context.Background(), home, "", primary.ID, maildomain.OutboundKindSend, signature.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = commands.MailSenderSetVIP(context.Background(), home, "", "vip@example.test", true); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = commands.MailSenderSetBlocked(context.Background(), home, "", "blocked@example.test", true); err != nil {
		t.Fatal(err)
	}
	seedReplyMessage(t, home, maildomain.IngestRecord{
		MessageID: "msg_phase30_muted",
		Parsed:    maildomain.ParsedMessage{RFCMessageID: "phase30-muted@example.test", From: maildomain.Address{Email: "sender@example.test"}, To: []maildomain.Address{{Email: primary.Address}}, Subject: "Muted settings fixture"},
		Direction: maildomain.DirectionInbound, InternalDate: time.Date(2026, 8, 11, 2, 5, 0, 0, time.UTC),
	})
	if _, preference, _, err := commands.MailThreadSetMuted(context.Background(), home, "", "msg_phase30_muted", true); err != nil || !preference.Muted {
		t.Fatalf("mute preference=%+v err=%v", preference, err)
	}

	_, settings, schemaVersion, err := commands.MailSettingsShow(context.Background(), home, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if schemaVersion != store.SchemaVersion || !settings.LocalOnlyRead || settings.AccountID == "" {
		t.Fatalf("settings header=%+v schema=%d", settings, schemaVersion)
	}
	if settings.Local.DefaultIdentity.ID != primary.ID || settings.Local.IdentityCount != 3 || settings.Local.EnabledIdentities != 3 || settings.Local.SignatureCount != 1 {
		t.Fatalf("local settings=%+v", settings.Local)
	}
	if len(settings.Local.SignatureDefaults) != 4 || settings.Local.SignatureDefaults[0].SignatureID != signature.ID || settings.Local.SignatureDefaults[0].Operation != maildomain.OutboundKindSend {
		t.Fatalf("signature defaults=%+v", settings.Local.SignatureDefaults)
	}
	if settings.Local.Preferences.SenderRecords != 2 || settings.Local.Preferences.VIPSenders != 1 || settings.Local.Preferences.BlockedSenders != 1 || settings.Local.Preferences.ThreadRecords != 1 || settings.Local.Preferences.MutedThreads != 1 {
		t.Fatalf("preference counts=%+v", settings.Local.Preferences)
	}
	if settings.Compatibility.AlternateFrom.Status != maildomain.SettingsSupported || settings.Compatibility.HideMyEmailOutbound.Status != maildomain.SettingsUnverified || settings.Compatibility.ReplyAnswered.Status != maildomain.SettingsSupported || settings.Compatibility.ForwardedState.Status != maildomain.SettingsUnsupported || settings.Compatibility.ColoredFlags.Status != maildomain.SettingsUnsupported || settings.Compatibility.SMIMEInspection.Status != maildomain.SettingsSupported || settings.Compatibility.SMIMEDecryption.Status != maildomain.SettingsSupported || settings.Compatibility.SMIME.Status != maildomain.SettingsSupported {
		t.Fatalf("compatibility=%+v", settings.Compatibility)
	}
	if settings.ProviderSettings.AutomaticForwarding.Status != maildomain.SettingsUnsupportedCurrentAuth || settings.ProviderSettings.AutomaticReply.Status != maildomain.SettingsUnsupportedCurrentAuth || settings.ProviderSettings.ServerRules.Status != maildomain.SettingsUnsupportedCurrentAuth || settings.ProviderSettings.AliasProvisioning.Status != maildomain.SettingsUnsupportedCurrentAuth || settings.ProviderSettings.CustomDomainProvisioning.Status != maildomain.SettingsUnsupportedCurrentAuth || settings.ProviderSettings.HideMyEmailManagement.Status != maildomain.SettingsUnsupportedCurrentAuth || settings.ProviderSettings.CategoryViews.Status != maildomain.SettingsUnsupportedCurrentAuth || settings.ProviderSettings.ProviderDefaultSendAddress.Status != maildomain.SettingsUnsupportedCurrentAuth {
		t.Fatalf("provider settings=%+v", settings.ProviderSettings)
	}
	if settings.ProviderSettings.AutomaticForwarding.NextAction != "" || settings.Compatibility.ColoredFlags.NextAction != "" {
		t.Fatalf("non-verbose settings leaked next actions: %+v %+v", settings.ProviderSettings, settings.Compatibility)
	}

	_, verboseSettings, _, err := commands.MailSettingsShow(context.Background(), home, "", true)
	if err != nil || verboseSettings.ProviderSettings.AutomaticForwarding.NextAction == "" || verboseSettings.Compatibility.ColoredFlags.NextAction == "" {
		t.Fatalf("verbose settings=%+v err=%v", verboseSettings, err)
	}

	code, compact, stderr := runWithAccountCommands(t, commands, "", "--home", home, "mail", "settings", "show")
	if code != 0 || stderr != "" || !strings.Contains(compact, "provider_contact=none") || !strings.Contains(compact, "provider_settings=unsupported_current_auth") || strings.Contains(compact, "next_action") {
		t.Fatalf("compact code=%d stdout=%q stderr=%q", code, compact, stderr)
	}
	if lines := strings.Count(compact, "\n"); lines > 30 {
		t.Fatalf("settings compact budget lines=%d output=%q", lines, compact)
	}

	code, stdout, stderr := runWithAccountCommands(t, commands, `{"verbose":true}`, "--home", home, "mail", "settings", "show", "--input-json", "-", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("json code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope struct {
		Data struct {
			Settings maildomain.MailSettings `json:"settings"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Data.Settings.LocalOnlyRead || envelope.Data.Settings.ProviderSettings.AutomaticForwarding.NextAction == "" {
		t.Fatalf("json settings=%+v", envelope.Data.Settings)
	}
}
