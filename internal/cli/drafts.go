package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type publicDraft struct {
	ID                 string                    `json:"id"`
	AccountID          string                    `json:"account_id"`
	MessageID          string                    `json:"message_id"`
	Revision           int                       `json:"revision"`
	SyncState          maildomain.DraftSyncState `json:"sync_state"`
	RemoteLocationID   string                    `json:"remote_location_id,omitempty"`
	PreviousLocationID string                    `json:"previous_location_id,omitempty"`
	OutboundAttemptID  string                    `json:"outbound_attempt_id,omitempty"`
	LastErrorCode      string                    `json:"last_error_code,omitempty"`
	BCCCount           int                       `json:"bcc_count,omitempty"`
	CreatedAt          time.Time                 `json:"created_at"`
	UpdatedAt          time.Time                 `json:"updated_at"`
	Message            publicMessage             `json:"message"`
}

type draftData struct {
	AccountID     string      `json:"account_id"`
	Draft         publicDraft `json:"draft"`
	SchemaVersion int64       `json:"schema_version"`
}

type draftListData struct {
	AccountID     string        `json:"account_id"`
	Drafts        []publicDraft `json:"drafts"`
	Count         int           `json:"count"`
	More          bool          `json:"more,omitempty"`
	SchemaVersion int64         `json:"schema_version"`
}

type draftShowData struct {
	AccountID     string             `json:"account_id"`
	Draft         publicDraft        `json:"draft"`
	Content       messageContentData `json:"content"`
	SchemaVersion int64              `json:"schema_version"`
}

type draftSyncData struct {
	AccountID     string                      `json:"account_id"`
	Summary       maildomain.DraftSyncSummary `json:"summary"`
	Drafts        []publicDraft               `json:"drafts"`
	Count         int                         `json:"count"`
	SchemaVersion int64                       `json:"schema_version"`
}

type draftSendData struct {
	AccountID     string                `json:"account_id"`
	DraftID       string                `json:"draft_id"`
	Attempt       publicOutboundAttempt `json:"attempt"`
	Reused        bool                  `json:"reused"`
	SchemaVersion int64                 `json:"schema_version"`
}

type draftDeleteData struct {
	AccountID      string `json:"account_id"`
	DraftID        string `json:"draft_id"`
	ProviderCopies int    `json:"provider_copies"`
	Deleted        bool   `json:"deleted"`
	SchemaVersion  int64  `json:"schema_version"`
}

func newMailDraftCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use: "draft", Short: "Compose, synchronize, inspect, send, and delete iCloud-backed drafts", Args: cobra.NoArgs,
		Example: "  icloud mail draft create --to teammate@example.com --subject 'Working note' --body-file draft.txt\n  icloud mail draft sync\n  icloud mail draft list\n  icloud mail draft send drf_… --dry-run",
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(newDraftSaveCommand(state, false))
	command.AddCommand(newDraftSaveCommand(state, true))
	command.AddCommand(newDraftListCommand(state))
	command.AddCommand(newDraftShowCommand(state))
	command.AddCommand(newDraftSyncCommand(state))
	command.AddCommand(newDraftSendCommand(state))
	command.AddCommand(newDraftDeleteCommand(state))
	return command
}

func newDraftSaveCommand(state *executionState, update bool) *cobra.Command {
	var flags sendFlags
	use, short, args := "create", "Create one local draft without contacting iCloud", cobra.NoArgs
	if update {
		use, short, args = "update <draft-id>", "Replace one draft revision locally before its next provider sync", cobra.ExactArgs(1)
	}
	command := &cobra.Command{Use: use, Short: short, Args: args,
		Example: "  icloud mail draft create --subject 'Idea' --body 'Incomplete is okay'\n  icloud mail draft update drf_… --to client@example.com --subject 'Ready soon' --body-file draft.txt",
		RunE: func(cmd *cobra.Command, args []string) error {
			request, _, err := sendRequestFromFlags(state, flags)
			if err != nil {
				return err
			}
			var accountValue account.Account
			var value maildomain.Draft
			var schemaVersion int64
			if update {
				accountValue, value, schemaVersion, err = state.mail.MailDraftUpdate(cmd.Context(), state.global.Home, state.global.Account, args[0], request)
			} else {
				accountValue, value, schemaVersion, err = state.mail.MailDraftCreate(cmd.Context(), state.global.Home, state.global.Account, request)
			}
			if err != nil {
				return err
			}
			if flags.JSON {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: draftData{AccountID: accountValue.ID, Draft: sanitizeDraft(value), SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderDraft(NewCompactTextRenderer(state.stdout), value, false)
		},
	}
	bindDraftComposeFlags(command, &flags)
	command.Flags().BoolVar(&flags.JSON, "json", false, "Emit one stable JSON success envelope")
	return command
}

func bindDraftComposeFlags(command *cobra.Command, flags *sendFlags) {
	command.Flags().StringArrayVar(&flags.To, "to", nil, "Recipient address; repeat or provide a comma-separated address list")
	command.Flags().StringArrayVar(&flags.CC, "cc", nil, "Cc recipient address; repeat as needed")
	command.Flags().StringArrayVar(&flags.BCC, "bcc", nil, "Bcc recipient preserved in the saved draft; hidden from default output")
	command.Flags().StringVar(&flags.ReplyTo, "reply-to", "", "Optional Reply-To address")
	command.Flags().StringVar(&flags.Subject, "subject", "", "Draft subject; may be empty")
	command.Flags().StringVar(&flags.Body, "body", "", "Inline text body; may be empty")
	command.Flags().StringVar(&flags.BodyFile, "body-file", "", "Text body file path, or - for stdin")
	command.Flags().StringVar(&flags.HTMLBody, "html-body", "", "Inline HTML body")
	command.Flags().StringVar(&flags.HTMLBodyFile, "html-body-file", "", "HTML body file path, or - for stdin")
	command.Flags().StringArrayVar(&flags.Headers, "header", nil, "Safe custom header as Name: Value; repeat as needed")
	command.Flags().StringArrayVar(&flags.Attachments, "attach", nil, "Regular file path to attach; repeat as needed")
	command.Flags().StringArrayVar(&flags.AttachmentNames, "attach-name", nil, "Override one attachment filename as path=filename")
	command.Flags().StringArrayVar(&flags.AttachmentTypes, "attach-type", nil, "Override one attachment media type as path=type/subtype")
}

func newDraftListCommand(state *executionState) *cobra.Command {
	var limit int
	var jsonOutput, verbose bool
	command := &cobra.Command{Use: "list", Short: "List local and provider-synchronized drafts", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, Verbose: verbose}, OutputSupport{JSON: true, Verbose: true}); err != nil {
				return err
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, "--limit must be between 1 and 100", false)
			}
			accountValue, values, more, schemaVersion, err := state.mail.MailDraftList(cmd.Context(), state.global.Home, state.global.Account, limit)
			if err != nil {
				return err
			}
			if jsonOutput {
				public := make([]publicDraft, 0, len(values))
				for _, value := range values {
					public = append(public, sanitizeDraft(value))
				}
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: draftListData{AccountID: accountValue.ID, Drafts: public, Count: len(public), More: more, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, value := range values {
				if err := renderDraft(renderer, value, verbose); err != nil {
					return err
				}
			}
			if more {
				return renderer.Record("drafts-more", accountValue.ID, Field("limit", limit))
			}
			return nil
		},
	}
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, "Maximum drafts to return (1-100)")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&verbose, "verbose", false, "Include provider recovery IDs and timestamps")
	return command
}

func newDraftShowCommand(state *executionState) *cobra.Command {
	var options OutputOptions
	command := &cobra.Command{Use: "show <draft-id>", Short: "Show a draft with bounded default content or an explicit body/raw expansion", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := effectiveMessageFormat(options)
			if err != nil {
				return err
			}
			accountValue, value, source, schemaVersion, err := state.mail.MailDraftShow(cmd.Context(), state.global.Home, state.global.Account, args[0])
			if err != nil {
				return err
			}
			if options.JSON {
				content, err := buildJSONMessageContent(cmd.Context(), value.Message, source, options, format)
				if err != nil {
					return err
				}
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: draftShowData{AccountID: accountValue.ID, Draft: sanitizeDraft(value), Content: content, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			if format == maildomain.ContentRaw {
				if source == nil {
					return NewAppError(ErrorUnavailable, "draft raw content is unavailable locally", false)
				}
				return messageContentError(source.WriteRaw(cmd.Context(), state.stdout))
			}
			wroteMetadata := false
			if !options.BodyOnly {
				if err := renderDraft(NewCompactTextRenderer(state.stdout), value, options.Verbose); err != nil {
					return err
				}
				wroteMetadata = true
			}
			if options.Headers {
				if err := writeHeaderSection(state.stdout, publicHeaders(value.Message.Content), wroteMetadata); err != nil {
					return err
				}
				wroteMetadata = true
			}
			if format == maildomain.ContentText || format == maildomain.ContentHTML {
				if source == nil {
					return NewAppError(ErrorUnavailable, "draft body content is unavailable locally", false)
				}
				if !options.BodyOnly {
					if wroteMetadata {
						_, _ = io.WriteString(state.stdout, "\n")
					}
					_, _ = io.WriteString(state.stdout, "body\n")
				}
				return messageContentError(source.WriteBody(cmd.Context(), format, state.stdout))
			}
			return nil
		},
	}
	command.Flags().BoolVar(&options.JSON, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&options.Full, "full", false, "Include the complete decoded text body")
	command.Flags().BoolVar(&options.Verbose, "verbose", false, "Include provider recovery IDs and MIME detail")
	command.Flags().StringVar(&options.Format, "format", "compact", "Content format: compact, text, html, or raw")
	command.Flags().BoolVar(&options.Headers, "headers", false, "Include decoded headers (BCC remains omitted)")
	command.Flags().BoolVar(&options.BodyOnly, "body-only", false, "Write only the selected body representation")
	return command
}

func newDraftSyncCommand(state *executionState) *cobra.Command {
	var limit int
	var folderSelector string
	var jsonOutput, verbose bool
	command := &cobra.Command{Use: "sync [draft-id]", Short: "Synchronize provider Drafts and safely upload local revisions", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput, Verbose: verbose}, OutputSupport{JSON: true, Verbose: true}); err != nil {
				return err
			}
			draftID := ""
			if len(args) == 1 {
				draftID = args[0]
			}
			accountValue, summary, values, schemaVersion, err := state.mail.MailDraftSync(cmd.Context(), state.global.Home, state.global.Account, folderSelector, draftID, limit)
			if err != nil {
				return err
			}
			if jsonOutput {
				public := make([]publicDraft, 0, len(values))
				for _, value := range values {
					public = append(public, sanitizeDraft(value))
				}
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: draftSyncData{AccountID: accountValue.ID, Summary: summary, Drafts: public, Count: len(public), SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			if err := renderer.Record("draft-sync", accountValue.ID, Field("folder", summary.FolderName), Field("observed", summary.Observed), Field("appended", summary.Appended), Field("reconciled", summary.Reconciled), Field("cleaned", summary.Cleaned), Field("pending", summary.Pending), Field("more", summary.MoreAvailable)); err != nil {
				return err
			}
			if verbose {
				for _, value := range values {
					if err := renderDraft(renderer, value, true); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&folderSelector, "folder", "", "Explicitly select the provider Drafts mailbox by ID, name, or wire name when iCloud advertises no Drafts role")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, "Maximum provider messages committed per Drafts sync page (1-100)")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&verbose, "verbose", false, "Include resulting draft recovery state")
	return command
}

func newDraftSendCommand(state *executionState) *cobra.Command {
	var dryRun, yes, jsonOutput, verbose, showHeaders bool
	var format string
	command := &cobra.Command{Use: "send <draft-id>", Short: "Preview or send one draft through the normal idempotent outbound path", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun && yes {
				return NewAppError(ErrorUsage, "--dry-run and --yes cannot be combined", false)
			}
			if !dryRun && (verbose || showHeaders || strings.TrimSpace(format) != "") {
				return NewAppError(ErrorUsage, "--verbose, --headers, and --format are dry-run preview expansions", false)
			}
			if dryRun {
				accountValue, value, preview, schemaVersion, err := state.mail.MailDraftSendPreview(cmd.Context(), state.global.Home, state.global.Account, args[0])
				if err != nil {
					return err
				}
				if jsonOutput {
					return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: map[string]any{"account_id": accountValue.ID, "draft": sanitizeDraft(value), "preview": preview, "schema_version": schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
				}
				return renderSendPreview(cmd.Context(), state, preview, sendFlags{DryRun: true, Verbose: verbose, ShowHeaders: showHeaders, Format: format}, schemaVersion)
			}
			interactive := isTerminalReader(state.stdin) && !state.global.NoInput
			if !yes && !interactive {
				return NewAppError(ErrorSafety, "non-interactive draft send requires --yes", false)
			}
			if !yes {
				_, _, preview, _, err := state.mail.MailDraftSendPreview(cmd.Context(), state.global.Home, state.global.Account, args[0])
				if err != nil {
					return err
				}
				if err := renderCompactSendPreview(state.stdout, preview, false, false); err != nil {
					return err
				}
				confirmed, err := confirmMailSend(state.stdin, state.stderr)
				if err != nil {
					return err
				}
				if !confirmed {
					return NewAppError(ErrorSafety, "draft send canceled before contacting SMTP", false)
				}
			}
			accountValue, value, attempt, reused, schemaVersion, err := state.mail.MailDraftSend(cmd.Context(), state.global.Home, state.global.Account, args[0])
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: draftSendData{AccountID: accountValue.ID, DraftID: value.ID, Attempt: sanitizeOutboundAttempt(attempt), Reused: reused, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderSendOutcome(NewCompactTextRenderer(state.stdout), attempt, reused)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Preview the normalized send without contacting SMTP or deleting the draft")
	command.Flags().BoolVar(&yes, "yes", false, "Confirm an intentional non-interactive send")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	command.Flags().BoolVar(&verbose, "verbose", false, "With --dry-run, include normalized header detail")
	command.Flags().BoolVar(&showHeaders, "headers", false, "With --dry-run, print normalized safe headers")
	command.Flags().StringVar(&format, "format", "", "With --dry-run, output text, html, or raw preview content")
	return command
}

func newDraftDeleteCommand(state *executionState) *cobra.Command {
	var dryRun, yes, jsonOutput bool
	command := &cobra.Command{Use: "delete <draft-id>", Short: "Delete one local draft and every known provider Drafts version", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun && yes {
				return NewAppError(ErrorUsage, "--dry-run and --yes cannot be combined", false)
			}
			accountValue, value, _, schemaVersion, err := state.mail.MailDraftShow(cmd.Context(), state.global.Home, state.global.Account, args[0])
			if err != nil {
				return err
			}
			providerCopies := draftProviderCopies(value)
			if dryRun {
				if jsonOutput {
					return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: draftDeleteData{AccountID: accountValue.ID, DraftID: value.ID, ProviderCopies: providerCopies, Deleted: false, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
				}
				return NewCompactTextRenderer(state.stdout).Record("draft-delete-preview", value.ID, Field("provider_copies", providerCopies), Field("safety", "no local or provider draft was deleted"))
			}
			interactive := isTerminalReader(state.stdin) && !state.global.NoInput
			if !yes && !interactive {
				return NewAppError(ErrorSafety, "non-interactive draft delete requires --yes", false)
			}
			if !yes {
				confirmed, err := confirmDraftDelete(state.stdin, state.stderr, value.ID, providerCopies)
				if err != nil {
					return err
				}
				if !confirmed {
					return NewAppError(ErrorSafety, "draft delete canceled before contacting IMAP", false)
				}
			}
			accountValue, value, providerCopies, schemaVersion, err = state.mail.MailDraftDelete(cmd.Context(), state.global.Home, state.global.Account, args[0])
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: draftDeleteData{AccountID: accountValue.ID, DraftID: value.ID, ProviderCopies: providerCopies, Deleted: true, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("draft-deleted", value.ID, Field("provider_copies", providerCopies))
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Show the exact deletion scope without changing local or provider state")
	command.Flags().BoolVar(&yes, "yes", false, "Confirm intentional non-interactive draft deletion")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func sanitizeDraft(value maildomain.Draft) publicDraft {
	return publicDraft{ID: value.ID, AccountID: value.AccountID, MessageID: value.MessageID, Revision: value.Revision, SyncState: value.SyncState,
		RemoteLocationID: value.RemoteLocationID, PreviousLocationID: value.PreviousLocationID, OutboundAttemptID: value.OutboundAttemptID,
		LastErrorCode: value.LastErrorCode, BCCCount: len(value.Message.BCC), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, Message: sanitizeMessage(value.Message)}
}

func renderDraft(renderer *CompactTextRenderer, value maildomain.Draft, verbose bool) error {
	fields := []CompactField{Field("message", value.MessageID), Field("revision", value.Revision), Field("state", value.SyncState), Field("to", formatAddresses(value.Message.To)), Field("cc", formatAddresses(value.Message.CC)), Field("bcc", len(value.Message.BCC)), Field("subject", value.Message.Subject), Field("attachments", value.Message.AttachmentCount), Field("excerpt", Excerpt(value.Message.Snippet, DefaultExcerptRunes))}
	if verbose {
		fields = append(fields, Field("remote_location", value.RemoteLocationID), Field("previous_location", value.PreviousLocationID), Field("outbound_attempt", value.OutboundAttemptID), Field("last_error", value.LastErrorCode), Field("updated", value.UpdatedAt))
	}
	return renderer.Record("draft", value.ID, fields...)
}

func draftProviderCopies(value maildomain.Draft) int {
	count := 0
	for _, location := range value.Message.Locations {
		if location.Present && location.FolderKind == "drafts" {
			count++
		}
	}
	return count
}

func confirmDraftDelete(reader io.Reader, stderr io.Writer, draftID string, providerCopies int) (bool, error) {
	if _, err := fmt.Fprintf(stderr, "Delete draft %s and %d provider copy/copies? [y/N] ", draftID, providerCopies); err != nil {
		return false, WrapAppError(ErrorInternal, "cannot write the draft deletion confirmation prompt", false, err)
	}
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, WrapAppError(ErrorConfiguration, "cannot read draft deletion confirmation", false, err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
