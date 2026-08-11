package cli

import (
	"strings"
	"time"
)

type accountAddJSONRequest struct {
	Name           string `json:"name"`
	Email          string `json:"email"`
	IMAPUsername   string `json:"imap_username,omitempty"`
	Default        bool   `json:"default,omitempty"`
	PasswordStdin  bool   `json:"password_stdin,omitempty"`
	CredentialEnv  string `json:"credential_env,omitempty"`
	CredentialFile string `json:"credential_file,omitempty"`
}

type accountSelectorJSONRequest struct {
	Selector string `json:"selector"`
}

type accountRenameJSONRequest struct {
	Selector string `json:"selector"`
	NewName  string `json:"new_name"`
}

type mailIdentityAddJSONRequest struct {
	Address     string `json:"address"`
	DisplayName string `json:"display_name,omitempty"`
	Label       string `json:"label,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

type mailIdentityUpdateJSONRequest struct {
	Identity    string  `json:"identity"`
	Address     *string `json:"address,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Label       *string `json:"label,omitempty"`
	Kind        *string `json:"kind,omitempty"`
}

type mailIdentitySelectorJSONRequest struct {
	Identity string `json:"identity"`
}

type mailIdentityReclassifyJSONRequest struct {
	Limit int `json:"limit,omitempty"`
}

type credentialReplaceJSONRequest struct {
	Selector       string `json:"selector"`
	PasswordStdin  bool   `json:"password_stdin,omitempty"`
	CredentialEnv  string `json:"credential_env,omitempty"`
	CredentialFile string `json:"credential_file,omitempty"`
}

type folderPolicyJSONRequest struct {
	Folder string `json:"folder"`
}

type folderCreateJSONRequest struct {
	Name string `json:"name"`
}

type folderRenameJSONRequest struct {
	Folder string `json:"folder"`
	Name   string `json:"name"`
}

type folderSelectorJSONRequest struct {
	Folder string `json:"folder"`
}

type folderDeleteJSONRequest struct {
	Folder        string `json:"folder"`
	AllowNonEmpty bool   `json:"allow_non_empty,omitempty"`
}

type folderRoleSetJSONRequest struct {
	Role   string `json:"role"`
	Folder string `json:"folder"`
}

type folderRoleJSONRequest struct {
	Role string `json:"role"`
}

type mailSyncJSONRequest struct {
	Folder            string   `json:"folder,omitempty"`
	AllFolders        bool     `json:"all_folders,omitempty"`
	Hydrate           []string `json:"hydrate,omitempty"`
	ReconcileOutbound bool     `json:"reconcile_outbound,omitempty"`
	ContinueOnError   bool     `json:"continue_on_error,omitempty"`
	UntilCurrent      bool     `json:"until_current,omitempty"`
	MaxPages          *int     `json:"max_pages,omitempty"`
	MaxMessages       *int     `json:"max_messages,omitempty"`
	Limit             *int     `json:"limit,omitempty"`
}

type mailBackfillJSONRequest struct {
	Folder        string `json:"folder,omitempty"`
	After         string `json:"after,omitempty"`
	Before        string `json:"before,omitempty"`
	UntilComplete bool   `json:"until_complete,omitempty"`
	MaxPages      *int   `json:"max_pages,omitempty"`
	MaxMessages   *int   `json:"max_messages,omitempty"`
	Limit         *int   `json:"limit,omitempty"`
}

type mailRebaselineJSONRequest struct {
	Folder      string `json:"folder,omitempty"`
	CurrentTail bool   `json:"current_tail"`
}

type mailRepairJSONRequest struct {
	Mode           string `json:"mode,omitempty"`
	CleanupOrphans bool   `json:"cleanup_orphans,omitempty"`
	Limit          *int   `json:"limit,omitempty"`
}

type mailSearchRepairJSONRequest struct {
	Limit *int `json:"limit,omitempty"`
}

type messageExportJSONRequest struct {
	MessageID string `json:"message_id"`
	Output    string `json:"output"`
	Force     bool   `json:"force,omitempty"`
}

type messageImportJSONRequest struct {
	File           string     `json:"file"`
	To             string     `json:"to"`
	InternalDate   *time.Time `json:"internal_date,omitempty"`
	Flags          []string   `json:"flags,omitempty"`
	IdempotencyKey string     `json:"idempotency_key,omitempty"`
}

type mailboxExportJSONRequest struct {
	Folder string `json:"folder"`
	Format string `json:"format,omitempty"`
	Output string `json:"output"`
	Force  bool   `json:"force,omitempty"`
}

type mailboxImportJSONRequest struct {
	File        string `json:"file"`
	To          string `json:"to"`
	Mode        string `json:"mode,omitempty"`
	MaxMessages int    `json:"max_messages,omitempty"`
}

type unsubscribeJSONRequest struct {
	MessageID string `json:"message_id"`
	Method    string `json:"method,omitempty"`
}

type outboxTargetJSONRequest struct {
	OutboxID string `json:"outbox_id"`
}

type attachmentSaveJSONRequest struct {
	AttachmentID string `json:"attachment_id"`
	Output       string `json:"output"`
	Force        bool   `json:"force,omitempty"`
	Binary       bool   `json:"binary,omitempty"`
}

type attachmentRemoveJSONRequest struct {
	MessageID      string   `json:"message_id"`
	AttachmentIDs  []string `json:"attachment_ids"`
	Folder         string   `json:"folder,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
}

type attachmentsSaveAllJSONRequest struct {
	TargetID  string `json:"target_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Directory string `json:"directory"`
	Force     bool   `json:"force,omitempty"`
}

type attachmentJSONInput struct {
	Path        string `json:"path"`
	Filename    string `json:"filename,omitempty"`
	MediaType   string `json:"media_type,omitempty"`
	Disposition string `json:"disposition,omitempty"`
	ContentID   string `json:"content_id,omitempty"`
}

type composeJSONRequest struct {
	From           string                `json:"from,omitempty"`
	Signature      string                `json:"signature,omitempty"`
	SMIME          string                `json:"smime,omitempty"`
	To             []string              `json:"to,omitempty"`
	CC             []string              `json:"cc,omitempty"`
	BCC            []string              `json:"bcc,omitempty"`
	ReplyTo        string                `json:"reply_to,omitempty"`
	Subject        string                `json:"subject,omitempty"`
	Body           string                `json:"body,omitempty"`
	BodyFile       string                `json:"body_file,omitempty"`
	HTMLBody       string                `json:"html_body,omitempty"`
	HTMLBodyFile   string                `json:"html_body_file,omitempty"`
	Headers        []string              `json:"headers,omitempty"`
	Attachments    []attachmentJSONInput `json:"attachments,omitempty"`
	IdempotencyKey string                `json:"idempotency_key,omitempty"`
	RetryOf        string                `json:"retry_of,omitempty"`
	RetryUnknown   bool                  `json:"retry_unknown,omitempty"`
}

type replyComposeJSONRequest struct {
	From           string                `json:"from,omitempty"`
	Signature      string                `json:"signature,omitempty"`
	SMIME          string                `json:"smime,omitempty"`
	To             []string              `json:"to,omitempty"`
	CC             []string              `json:"cc,omitempty"`
	BCC            []string              `json:"bcc,omitempty"`
	Body           string                `json:"body,omitempty"`
	BodyFile       string                `json:"body_file,omitempty"`
	HTMLBody       string                `json:"html_body,omitempty"`
	HTMLBodyFile   string                `json:"html_body_file,omitempty"`
	Headers        []string              `json:"headers,omitempty"`
	Attachments    []attachmentJSONInput `json:"attachments,omitempty"`
	IdempotencyKey string                `json:"idempotency_key,omitempty"`
	RetryOf        string                `json:"retry_of,omitempty"`
	RetryUnknown   bool                  `json:"retry_unknown,omitempty"`
}

type replyJSONRequest struct {
	MessageID string                  `json:"message_id"`
	Folder    string                  `json:"folder,omitempty"`
	Compose   replyComposeJSONRequest `json:"message"`
	ReplaceTo bool                    `json:"replace_to,omitempty"`
	ReplaceCC bool                    `json:"replace_cc,omitempty"`
}

type forwardComposeJSONRequest struct {
	From           string                `json:"from,omitempty"`
	Signature      string                `json:"signature,omitempty"`
	SMIME          string                `json:"smime,omitempty"`
	To             []string              `json:"to,omitempty"`
	CC             []string              `json:"cc,omitempty"`
	BCC            []string              `json:"bcc,omitempty"`
	ReplyTo        string                `json:"reply_to,omitempty"`
	Body           string                `json:"body,omitempty"`
	BodyFile       string                `json:"body_file,omitempty"`
	HTMLBody       string                `json:"html_body,omitempty"`
	HTMLBodyFile   string                `json:"html_body_file,omitempty"`
	Headers        []string              `json:"headers,omitempty"`
	Attachments    []attachmentJSONInput `json:"attachments,omitempty"`
	IdempotencyKey string                `json:"idempotency_key,omitempty"`
	RetryOf        string                `json:"retry_of,omitempty"`
	RetryUnknown   bool                  `json:"retry_unknown,omitempty"`
}

type forwardJSONRequest struct {
	MessageID          string                    `json:"message_id"`
	Compose            forwardComposeJSONRequest `json:"message"`
	Original           string                    `json:"original,omitempty"`
	IncludeAttachments []string                  `json:"include_attachments,omitempty"`
}

type draftComposeJSONRequest struct {
	From         string                `json:"from,omitempty"`
	Signature    string                `json:"signature,omitempty"`
	SMIME        string                `json:"smime,omitempty"`
	To           []string              `json:"to,omitempty"`
	CC           []string              `json:"cc,omitempty"`
	BCC          []string              `json:"bcc,omitempty"`
	ReplyTo      string                `json:"reply_to,omitempty"`
	Subject      string                `json:"subject,omitempty"`
	Body         string                `json:"body,omitempty"`
	BodyFile     string                `json:"body_file,omitempty"`
	HTMLBody     string                `json:"html_body,omitempty"`
	HTMLBodyFile string                `json:"html_body_file,omitempty"`
	Headers      []string              `json:"headers,omitempty"`
	Attachments  []attachmentJSONInput `json:"attachments,omitempty"`
}

type draftSaveJSONRequest struct {
	DraftID string                  `json:"draft_id,omitempty"`
	Message draftComposeJSONRequest `json:"message"`
}

type draftSyncJSONRequest struct {
	DraftID string `json:"draft_id,omitempty"`
	Folder  string `json:"folder,omitempty"`
	Limit   *int   `json:"limit,omitempty"`
}

type draftTargetJSONRequest struct {
	DraftID string `json:"draft_id"`
}

type draftSendJSONRequest struct {
	DraftID string `json:"draft_id"`
	From    string `json:"from,omitempty"`
}

type locationJSONRequest struct {
	MessageID      string             `json:"message_id,omitempty"`
	MessageIDs     []string           `json:"message_ids,omitempty"`
	Items          []locationJSONItem `json:"items,omitempty"`
	From           string             `json:"from,omitempty"`
	To             string             `json:"to,omitempty"`
	IdempotencyKey string             `json:"idempotency_key,omitempty"`
}

type locationJSONItem struct {
	MessageID      string `json:"message_id"`
	From           string `json:"from,omitempty"`
	To             string `json:"to,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type threadFlagMutationJSONRequest struct {
	Target string `json:"target"`
	Folder string `json:"folder,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Page   bool   `json:"page,omitempty"`
	Mode   string `json:"mode,omitempty"`
}

type threadPreferenceJSONRequest struct {
	Target string `json:"target"`
}

type threadLocationMutationJSONRequest struct {
	Target         string `json:"target"`
	From           string `json:"from,omitempty"`
	To             string `json:"to,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	Cursor         string `json:"cursor,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	Page           bool   `json:"page,omitempty"`
	Mode           string `json:"mode,omitempty"`
}

type selectionCreateJSONRequest struct {
	Targets     []string                   `json:"targets,omitempty"`
	Search      *mailSearchReadJSONRequest `json:"search,omitempty"`
	ScopeFolder string                     `json:"scope_folder,omitempty"`
	TTL         string                     `json:"ttl,omitempty"`
}

type selectionTargetJSONRequest struct {
	SelectionID string `json:"selection_id"`
}

type selectionApplyJSONRequest struct {
	SelectionID string `json:"selection_id"`
	Action      string `json:"action"`
	To          string `json:"to,omitempty"`
	Mode        string `json:"mode,omitempty"`
	RequestHash string `json:"request_hash,omitempty"`
}

type mailPurgeJSONRequest struct {
	Folder      string `json:"folder,omitempty"`
	PurgeID     string `json:"purge_id,omitempty"`
	Limit       *int   `json:"limit,omitempty"`
	Mode        string `json:"mode,omitempty"`
	RequestHash string `json:"request_hash,omitempty"`
}

type mailRolePurgeJSONRequest struct {
	PurgeID     string `json:"purge_id,omitempty"`
	Limit       *int   `json:"limit,omitempty"`
	Mode        string `json:"mode,omitempty"`
	RequestHash string `json:"request_hash,omitempty"`
}

type flagBulkJSONRequest struct {
	MessageIDs []string `json:"message_ids"`
	Folder     string   `json:"folder,omitempty"`
}

type flagBulkJSONLItem struct {
	MessageID string `json:"message_id"`
	Folder    string `json:"folder,omitempty"`
}

func (request composeJSONRequest) sendFlags() sendFlags {
	flags := sendFlags{
		From: request.From, Signature: request.Signature, SMIME: request.SMIME,
		To: request.To, CC: request.CC, BCC: request.BCC, ReplyTo: request.ReplyTo,
		Subject: request.Subject, Body: request.Body, BodyFile: request.BodyFile,
		HTMLBody: request.HTMLBody, HTMLBodyFile: request.HTMLBodyFile,
		Headers: request.Headers, IdempotencyKey: request.IdempotencyKey,
		RetryOf: request.RetryOf, RetryUnknown: request.RetryUnknown,
	}
	for _, attachment := range request.Attachments {
		path := strings.TrimSpace(attachment.Path)
		flags.Attachments = append(flags.Attachments, path)
		if strings.TrimSpace(attachment.Filename) != "" {
			flags.AttachmentNames = append(flags.AttachmentNames, path+"="+attachment.Filename)
		}
		if strings.TrimSpace(attachment.MediaType) != "" {
			flags.AttachmentTypes = append(flags.AttachmentTypes, path+"="+attachment.MediaType)
		}
		if strings.TrimSpace(attachment.Disposition) != "" {
			flags.AttachmentDispositions = append(flags.AttachmentDispositions, path+"="+attachment.Disposition)
		}
		if strings.TrimSpace(attachment.ContentID) != "" {
			flags.AttachmentContentIDs = append(flags.AttachmentContentIDs, path+"="+attachment.ContentID)
		}
	}
	return flags
}

func (request replyComposeJSONRequest) sendFlags() sendFlags {
	return composeJSONRequest{
		From: request.From, Signature: request.Signature, SMIME: request.SMIME,
		To: request.To, CC: request.CC, BCC: request.BCC,
		Body: request.Body, BodyFile: request.BodyFile, HTMLBody: request.HTMLBody, HTMLBodyFile: request.HTMLBodyFile,
		Headers: request.Headers, Attachments: request.Attachments,
		IdempotencyKey: request.IdempotencyKey, RetryOf: request.RetryOf, RetryUnknown: request.RetryUnknown,
	}.sendFlags()
}

func (request forwardComposeJSONRequest) sendFlags() sendFlags {
	return composeJSONRequest{
		From: request.From, Signature: request.Signature, SMIME: request.SMIME,
		To: request.To, CC: request.CC, BCC: request.BCC, ReplyTo: request.ReplyTo,
		Body: request.Body, BodyFile: request.BodyFile, HTMLBody: request.HTMLBody, HTMLBodyFile: request.HTMLBodyFile,
		Headers: request.Headers, Attachments: request.Attachments,
		IdempotencyKey: request.IdempotencyKey, RetryOf: request.RetryOf, RetryUnknown: request.RetryUnknown,
	}.sendFlags()
}

func (request draftComposeJSONRequest) sendFlags() sendFlags {
	return composeJSONRequest{
		From: request.From, Signature: request.Signature, SMIME: request.SMIME,
		To: request.To, CC: request.CC, BCC: request.BCC, ReplyTo: request.ReplyTo, Subject: request.Subject,
		Body: request.Body, BodyFile: request.BodyFile, HTMLBody: request.HTMLBody, HTMLBodyFile: request.HTMLBodyFile,
		Headers: request.Headers, Attachments: request.Attachments,
	}.sendFlags()
}

func credentialSourceFromJSON(passwordStdin bool, environment, file string) credentialSourceOptions {
	return credentialSourceOptions{passwordStdin: passwordStdin, environment: environment, file: file}
}

func jsonInputConsumesStdin(options jsonInputOptions) bool {
	return strings.TrimSpace(options.Path) == "-"
}

func composeConsumesStdin(request composeJSONRequest) bool {
	return strings.TrimSpace(request.BodyFile) == "-" || strings.TrimSpace(request.HTMLBodyFile) == "-"
}

func replyComposeConsumesStdin(request replyComposeJSONRequest) bool {
	return strings.TrimSpace(request.BodyFile) == "-" || strings.TrimSpace(request.HTMLBodyFile) == "-"
}

func forwardComposeConsumesStdin(request forwardComposeJSONRequest) bool {
	return strings.TrimSpace(request.BodyFile) == "-" || strings.TrimSpace(request.HTMLBodyFile) == "-"
}

func draftComposeConsumesStdin(request draftComposeJSONRequest) bool {
	return strings.TrimSpace(request.BodyFile) == "-" || strings.TrimSpace(request.HTMLBodyFile) == "-"
}
