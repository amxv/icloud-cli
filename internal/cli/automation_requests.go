package cli

import (
	"strings"
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
	Kind        string `json:"kind,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

type mailIdentityUpdateJSONRequest struct {
	Identity    string  `json:"identity"`
	Address     *string `json:"address,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Kind        *string `json:"kind,omitempty"`
}

type mailIdentitySelectorJSONRequest struct {
	Identity string `json:"identity"`
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

type mailSyncJSONRequest struct {
	Folder            string   `json:"folder,omitempty"`
	AllFolders        bool     `json:"all_folders,omitempty"`
	Hydrate           []string `json:"hydrate,omitempty"`
	ReconcileOutbound bool     `json:"reconcile_outbound,omitempty"`
	ContinueOnError   bool     `json:"continue_on_error,omitempty"`
	Limit             *int     `json:"limit,omitempty"`
}

type mailBackfillJSONRequest struct {
	Folder string `json:"folder,omitempty"`
	After  string `json:"after,omitempty"`
	Before string `json:"before,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
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

type attachmentSaveJSONRequest struct {
	AttachmentID string `json:"attachment_id"`
	Output       string `json:"output"`
	Force        bool   `json:"force,omitempty"`
	Binary       bool   `json:"binary,omitempty"`
}

type attachmentsSaveAllJSONRequest struct {
	TargetID  string `json:"target_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Directory string `json:"directory"`
	Force     bool   `json:"force,omitempty"`
}

type attachmentJSONInput struct {
	Path      string `json:"path"`
	Filename  string `json:"filename,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

type composeJSONRequest struct {
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
	Compose   replyComposeJSONRequest `json:"message"`
	ReplaceTo bool                    `json:"replace_to,omitempty"`
	ReplaceCC bool                    `json:"replace_cc,omitempty"`
}

type forwardComposeJSONRequest struct {
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

type locationJSONRequest struct {
	MessageID      string `json:"message_id"`
	From           string `json:"from,omitempty"`
	To             string `json:"to,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
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
	}
	return flags
}

func (request replyComposeJSONRequest) sendFlags() sendFlags {
	return composeJSONRequest{
		To: request.To, CC: request.CC, BCC: request.BCC,
		Body: request.Body, BodyFile: request.BodyFile, HTMLBody: request.HTMLBody, HTMLBodyFile: request.HTMLBodyFile,
		Headers: request.Headers, Attachments: request.Attachments,
		IdempotencyKey: request.IdempotencyKey, RetryOf: request.RetryOf, RetryUnknown: request.RetryUnknown,
	}.sendFlags()
}

func (request forwardComposeJSONRequest) sendFlags() sendFlags {
	return composeJSONRequest{
		To: request.To, CC: request.CC, BCC: request.BCC, ReplyTo: request.ReplyTo,
		Body: request.Body, BodyFile: request.BodyFile, HTMLBody: request.HTMLBody, HTMLBodyFile: request.HTMLBodyFile,
		Headers: request.Headers, Attachments: request.Attachments,
		IdempotencyKey: request.IdempotencyKey, RetryOf: request.RetryOf, RetryUnknown: request.RetryUnknown,
	}.sendFlags()
}

func (request draftComposeJSONRequest) sendFlags() sendFlags {
	return composeJSONRequest{
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
