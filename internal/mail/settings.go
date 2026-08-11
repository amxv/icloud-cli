package mail

type SettingsStatus string

const (
	SettingsSupported              SettingsStatus = "supported"
	SettingsUnverified             SettingsStatus = "unverified"
	SettingsUnavailable            SettingsStatus = "unavailable"
	SettingsUnconfigured           SettingsStatus = "unconfigured"
	SettingsUnsupported            SettingsStatus = "unsupported"
	SettingsUnsupportedCurrentAuth SettingsStatus = "unsupported_current_auth"
)

type SettingsCapability struct {
	Status     SettingsStatus `json:"status"`
	NextAction string         `json:"next_action,omitempty"`
}

type SettingsIdentity struct {
	ID           string `json:"id"`
	Address      string `json:"address"`
	DisplayName  string `json:"display_name,omitempty"`
	Label        string `json:"label,omitempty"`
	Kind         string `json:"kind"`
	Verification string `json:"verification"`
}

type SettingsSignatureDefault struct {
	Operation   OutboundKind `json:"operation"`
	IdentityID  string       `json:"identity_id,omitempty"`
	SignatureID string       `json:"signature_id,omitempty"`
	None        bool         `json:"none"`
}

type SettingsFolderRole struct {
	Role       string `json:"role"`
	FolderID   string `json:"folder_id,omitempty"`
	FolderName string `json:"folder_name,omitempty"`
	Source     string `json:"source,omitempty"`
	Configured bool   `json:"configured"`
	Resolved   bool   `json:"resolved"`
}

type SettingsPreferenceCounts struct {
	SenderRecords  int `json:"sender_records"`
	VIPSenders     int `json:"vip_senders"`
	BlockedSenders int `json:"blocked_senders"`
	ThreadRecords  int `json:"thread_records"`
	MutedThreads   int `json:"muted_threads"`
}

type LocalMailSettings struct {
	DefaultIdentity   SettingsIdentity           `json:"default_identity"`
	IdentityCount     int                        `json:"identity_count"`
	EnabledIdentities int                        `json:"enabled_identities"`
	SignatureCount    int                        `json:"signature_count"`
	SignatureDefaults []SettingsSignatureDefault `json:"signature_defaults"`
	FolderRoles       []SettingsFolderRole       `json:"folder_roles"`
	Preferences       SettingsPreferenceCounts   `json:"preferences"`
}

type ProviderCompatibility struct {
	AlternateFrom       SettingsCapability `json:"alternate_from"`
	ReplyAnswered       SettingsCapability `json:"reply_answered"`
	ForwardedState      SettingsCapability `json:"forwarded_state"`
	ColoredFlags        SettingsCapability `json:"colored_flags"`
	HideMyEmailOutbound SettingsCapability `json:"hide_my_email_outbound"`
	SMIME               SettingsCapability `json:"smime"`
}

type ProviderSettingsAvailability struct {
	AutomaticForwarding        SettingsCapability `json:"automatic_forwarding"`
	AutomaticReply             SettingsCapability `json:"automatic_reply"`
	ServerRules                SettingsCapability `json:"server_rules"`
	AliasProvisioning          SettingsCapability `json:"alias_provisioning"`
	CustomDomainProvisioning   SettingsCapability `json:"custom_domain_provisioning"`
	HideMyEmailManagement      SettingsCapability `json:"hide_my_email_management"`
	CategoryViews              SettingsCapability `json:"category_views"`
	ProviderDefaultSendAddress SettingsCapability `json:"provider_default_send_address"`
}

type MailSettings struct {
	AccountID        string                       `json:"account_id"`
	Local            LocalMailSettings            `json:"local"`
	Compatibility    ProviderCompatibility        `json:"compatibility"`
	ProviderSettings ProviderSettingsAvailability `json:"provider_settings"`
	LocalOnlyRead    bool                         `json:"local_only_read"`
}
