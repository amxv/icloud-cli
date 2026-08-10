package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

type mailSignaturesService interface {
	MailSignaturesList(context.Context, string, string, string, int) (account.Account, maildomain.SignaturePage, int64, error)
	MailSignatureShow(context.Context, string, string, string) (account.Account, maildomain.Signature, int64, error)
	MailSignatureCreate(context.Context, string, string, maildomain.SignatureInput) (account.Account, maildomain.Signature, int64, error)
	MailSignatureUpdate(context.Context, string, string, string, maildomain.SignatureUpdate) (account.Account, maildomain.Signature, int64, error)
	MailSignatureRemove(context.Context, string, string, string) (account.Account, maildomain.Signature, int64, error)
	MailSignatureDefault(context.Context, string, string, string, maildomain.OutboundKind) (account.Account, maildomain.SignatureDefault, int64, error)
	MailSignatureSetDefault(context.Context, string, string, string, maildomain.OutboundKind, string) (account.Account, maildomain.SignatureDefault, int64, error)
}

type signatureListJSONRequest struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type signatureTargetJSONRequest struct {
	SignatureID string `json:"signature_id"`
}

type signatureCreateJSONRequest struct {
	IdentityID string `json:"identity_id,omitempty"`
	TextBody   string `json:"text_body,omitempty"`
	TextFile   string `json:"text_file,omitempty"`
	HTMLBody   string `json:"html_body,omitempty"`
	HTMLFile   string `json:"html_file,omitempty"`
}

type signatureUpdateJSONRequest struct {
	SignatureID string  `json:"signature_id"`
	IdentityID  *string `json:"identity_id,omitempty"`
	TextBody    *string `json:"text_body,omitempty"`
	TextFile    *string `json:"text_file,omitempty"`
	HTMLBody    *string `json:"html_body,omitempty"`
	HTMLFile    *string `json:"html_file,omitempty"`
}

type signatureDefaultJSONRequest struct {
	Operation   string  `json:"operation"`
	IdentityID  string  `json:"identity_id,omitempty"`
	SignatureID *string `json:"signature_id,omitempty"`
}

type signatureComposeFlags struct {
	IdentityID string
	TextBody   string
	TextFile   string
	HTMLBody   string
	HTMLFile   string
}

type publicSignatureSummary struct {
	ID         string    `json:"id"`
	IdentityID string    `json:"identity_id,omitempty"`
	TextBytes  int       `json:"text_bytes"`
	HTMLBytes  int       `json:"html_bytes"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type signatureListData struct {
	AccountID     string                   `json:"account_id"`
	Signatures    []publicSignatureSummary `json:"signatures"`
	Count         int                      `json:"count"`
	More          bool                     `json:"more"`
	NextCursor    string                   `json:"next_cursor,omitempty"`
	LocalOnly     bool                     `json:"local_only"`
	SchemaVersion int64                    `json:"schema_version"`
}

type signatureData struct {
	AccountID     string               `json:"account_id"`
	Signature     maildomain.Signature `json:"signature"`
	LocalOnly     bool                 `json:"local_only"`
	SchemaVersion int64                `json:"schema_version"`
}

type signatureRemoveData struct {
	AccountID     string `json:"account_id"`
	SignatureID   string `json:"signature_id"`
	Removed       bool   `json:"removed"`
	LocalOnly     bool   `json:"local_only"`
	SchemaVersion int64  `json:"schema_version"`
}

type signatureDefaultData struct {
	AccountID     string                      `json:"account_id"`
	Default       maildomain.SignatureDefault `json:"default"`
	LocalOnly     bool                        `json:"local_only"`
	SchemaVersion int64                       `json:"schema_version"`
}

func newMailSignaturesCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{Use: "signatures", Short: "Manage local per-identity compose signatures", Args: cobra.NoArgs}
	command.AddCommand(newMailSignaturesListCommand(state))
	command.AddCommand(newMailSignatureShowCommand(state))
	command.AddCommand(newMailSignatureCreateCommand(state))
	command.AddCommand(newMailSignatureUpdateCommand(state))
	command.AddCommand(newMailSignatureRemoveCommand(state))
	command.AddCommand(newMailSignatureDefaultCommand(state))
	return command
}

func newMailSignaturesListCommand(state *executionState) *cobra.Command {
	var cursor string
	var limit int
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use: "list", Short: "List local signature metadata without dumping signature bodies", Args: exactArgsOrJSONInput(&input, 0),
		Example: "  icloud mail signatures list\n  icloud mail signatures list --limit 50 --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, ok := state.mail.(mailSignaturesService)
			if !ok {
				return NewAppError(ErrorConfiguration, "mail signatures are unavailable", false)
			}
			request := signatureListJSONRequest{Cursor: cursor, Limit: limit}
			if used, err := readJSONInput(cmd, state, input, &request, "cursor", "limit"); err != nil {
				return err
			} else if used {
				cursor, limit = request.Cursor, request.Limit
			}
			if limit == 0 {
				limit = DefaultPageSize
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			accountValue, page, schemaVersion, err := service.MailSignaturesList(cmd.Context(), state.global.Home, state.global.Account, cursor, limit)
			if err != nil {
				return err
			}
			if jsonOutput {
				summaries := make([]publicSignatureSummary, 0, len(page.Signatures))
				for _, value := range page.Signatures {
					summaries = append(summaries, publicSignatureMetadata(value))
				}
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: signatureListData{AccountID: accountValue.ID, Signatures: summaries, Count: len(summaries), More: page.More, NextCursor: page.NextCursor, LocalOnly: true, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, value := range page.Signatures {
				if err := renderSignatureMetadata(renderer, value); err != nil {
					return err
				}
			}
			return renderer.Summary("signatures", Field("account", accountValue.ID), Field("scope", "local"), Field("count", len(page.Signatures)), Field("more", page.More), Field("next_cursor", page.NextCursor))
		},
	}
	command.Flags().StringVar(&cursor, "cursor", "", "Continue from a next_cursor returned by the same account")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum signatures to return (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailSignatureShowCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use: "show <signature-id>", Short: "Show one local signature including its text and optional HTML body", Args: exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail signatures show sig_…\n  icloud mail signatures show sig_… --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			service, ok := state.mail.(mailSignaturesService)
			if !ok {
				return NewAppError(ErrorConfiguration, "mail signatures are unavailable", false)
			}
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			request := signatureTargetJSONRequest{SignatureID: id}
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				id = request.SignatureID
			}
			accountValue, value, schemaVersion, err := service.MailSignatureShow(cmd.Context(), state.global.Home, state.global.Account, id)
			if err != nil {
				return err
			}
			return renderSignatureResult(state, accountValue.ID, value, schemaVersion, jsonOutput)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailSignatureCreateCommand(state *executionState) *cobra.Command {
	var flags signatureComposeFlags
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use: "create", Short: "Create one account-local signature", Args: exactArgsOrJSONInput(&input, 0),
		Example: "  icloud mail signatures create --text-file signature.txt\n  icloud mail signatures create --identity mid_… --text 'Thanks,\nAlex' --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, ok := state.mail.(mailSignaturesService)
			if !ok {
				return NewAppError(ErrorConfiguration, "mail signatures are unavailable", false)
			}
			if used, err := readSignatureCreateJSON(cmd, state, input, &flags); err != nil {
				return err
			} else if used && jsonInputConsumesStdin(input) && (flags.TextFile == "-" || flags.HTMLFile == "-") {
				return NewAppError(ErrorUsage, "signature body files cannot read stdin when --input-json also reads stdin", false)
			}
			value, err := signatureInputFromFlags(state, flags)
			if err != nil {
				return err
			}
			accountValue, signature, schemaVersion, err := service.MailSignatureCreate(cmd.Context(), state.global.Home, state.global.Account, value)
			if err != nil {
				return err
			}
			return renderSignatureResult(state, accountValue.ID, signature, schemaVersion, jsonOutput)
		},
	}
	bindSignatureComposeFlags(command, &flags)
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailSignatureUpdateCommand(state *executionState) *cobra.Command {
	var flags signatureComposeFlags
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use: "update <signature-id>", Short: "Update selected fields of one local signature", Args: exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail signatures update sig_… --text-file signature.txt\n  icloud mail signatures update sig_… --identity none --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			service, ok := state.mail.(mailSignaturesService)
			if !ok {
				return NewAppError(ErrorConfiguration, "mail signatures are unavailable", false)
			}
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			update := maildomain.SignatureUpdate{}
			if used, err := readSignatureUpdateJSON(cmd, state, input, &id, &update); err != nil {
				return err
			} else if !used {
				var buildErr error
				update, buildErr = signatureUpdateFromFlags(cmd, state, flags)
				if buildErr != nil {
					return buildErr
				}
			}
			if strings.TrimSpace(id) == "" {
				return NewAppError(ErrorUsage, "signature ID is required", false)
			}
			if update.IdentityID == nil && update.TextBody == nil && update.HTMLBody == nil {
				return NewAppError(ErrorUsage, "signature update requires at least one changed field", false)
			}
			accountValue, signature, schemaVersion, err := service.MailSignatureUpdate(cmd.Context(), state.global.Home, state.global.Account, id, update)
			if err != nil {
				return err
			}
			return renderSignatureResult(state, accountValue.ID, signature, schemaVersion, jsonOutput)
		},
	}
	bindSignatureComposeFlags(command, &flags)
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailSignatureRemoveCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use: "delete <signature-id>", Short: "Delete one local signature and defaults that reference it", Args: exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail signatures delete sig_…\n  icloud mail signatures delete sig_… --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			service, ok := state.mail.(mailSignaturesService)
			if !ok {
				return NewAppError(ErrorConfiguration, "mail signatures are unavailable", false)
			}
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			request := signatureTargetJSONRequest{SignatureID: id}
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				id = request.SignatureID
			}
			accountValue, value, schemaVersion, err := service.MailSignatureRemove(cmd.Context(), state.global.Home, state.global.Account, id)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: signatureRemoveData{AccountID: accountValue.ID, SignatureID: value.ID, Removed: true, LocalOnly: true, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("signature-removed", value.ID, Field("scope", "local"), Field("removed", true))
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailSignatureDefaultCommand(state *executionState) *cobra.Command {
	var identityID string
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use: "default <new|reply|reply-all|forward> [signature-id|none]", Short: "Show or set one operation-family signature default", Args: signatureDefaultArgs(&input),
		Example: "  icloud mail signatures default new sig_…\n  icloud mail signatures default reply none --identity mid_…\n  icloud mail signatures default forward --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			service, ok := state.mail.(mailSignaturesService)
			if !ok {
				return NewAppError(ErrorConfiguration, "mail signatures are unavailable", false)
			}
			operation := ""
			var signatureID *string
			if len(args) >= 1 {
				operation = args[0]
			}
			if len(args) == 2 {
				value := args[1]
				signatureID = &value
			}
			request := signatureDefaultJSONRequest{Operation: operation, IdentityID: identityID, SignatureID: signatureID}
			if used, err := readJSONInput(cmd, state, input, &request, "identity"); err != nil {
				return err
			} else if used {
				operation, identityID, signatureID = request.Operation, request.IdentityID, request.SignatureID
			}
			kind, err := maildomain.NormalizeSignatureOperation(maildomain.OutboundKind(operation))
			if err != nil {
				return NewAppError(ErrorUsage, "signature default operation must be new, reply, reply-all, or forward", false)
			}
			var accountValue account.Account
			var value maildomain.SignatureDefault
			var schemaVersion int64
			if signatureID == nil {
				accountValue, value, schemaVersion, err = service.MailSignatureDefault(cmd.Context(), state.global.Home, state.global.Account, identityID, kind)
			} else {
				if strings.TrimSpace(*signatureID) == "" {
					return NewAppError(ErrorUsage, "signature default must be a sig_… ID or none", false)
				}
				accountValue, value, schemaVersion, err = service.MailSignatureSetDefault(cmd.Context(), state.global.Home, state.global.Account, identityID, kind, *signatureID)
			}
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: signatureDefaultData{AccountID: accountValue.ID, Default: value, LocalOnly: true, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderSignatureDefault(NewCompactTextRenderer(state.stdout), value)
		},
	}
	command.Flags().StringVar(&identityID, "identity", "", "Optional identity scope by stable mid_… ID or configured address; omit for account-wide default")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func signatureDefaultArgs(options *jsonInputOptions) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(options.Path) != "" {
			if len(args) != 0 {
				return NewAppError(ErrorUsage, "positional write arguments cannot be combined with --input-json", false)
			}
			return nil
		}
		return cobra.RangeArgs(1, 2)(cmd, args)
	}
}

func bindSignatureComposeFlags(command *cobra.Command, flags *signatureComposeFlags) {
	command.Flags().StringVar(&flags.IdentityID, "identity", "", "Optionally bind this signature to one mail identity (use none on update to clear the binding)")
	command.Flags().StringVar(&flags.TextBody, "text", "", "Inline plain-text signature body")
	command.Flags().StringVar(&flags.TextFile, "text-file", "", "Plain-text signature file path, or - for stdin")
	command.Flags().StringVar(&flags.HTMLBody, "html", "", "Optional inline HTML signature body")
	command.Flags().StringVar(&flags.HTMLFile, "html-file", "", "Optional HTML signature file path, or - for stdin")
}

func signatureInputFromFlags(state *executionState, flags signatureComposeFlags) (maildomain.SignatureInput, error) {
	text, textStdin, err := readSendBody(state.stdin, flags.TextBody, flags.TextFile, "signature text")
	if err != nil {
		return maildomain.SignatureInput{}, err
	}
	html, htmlStdin, err := readSendBody(state.stdin, flags.HTMLBody, flags.HTMLFile, "signature HTML")
	if err != nil {
		return maildomain.SignatureInput{}, err
	}
	if textStdin && htmlStdin {
		return maildomain.SignatureInput{}, NewAppError(ErrorUsage, "text and HTML signatures cannot both read from stdin", false)
	}
	return maildomain.SignatureInput{IdentityID: flags.IdentityID, TextBody: text, HTMLBody: html}, nil
}

func signatureUpdateFromFlags(cmd *cobra.Command, state *executionState, flags signatureComposeFlags) (maildomain.SignatureUpdate, error) {
	update := maildomain.SignatureUpdate{}
	if cmd.Flags().Changed("identity") {
		value := flags.IdentityID
		update.IdentityID = &value
	}
	if cmd.Flags().Changed("text") && cmd.Flags().Changed("text-file") {
		return update, NewAppError(ErrorUsage, "choose either --text or --text-file", false)
	}
	if cmd.Flags().Changed("html") && cmd.Flags().Changed("html-file") {
		return update, NewAppError(ErrorUsage, "choose either --html or --html-file", false)
	}
	if cmd.Flags().Changed("text") || cmd.Flags().Changed("text-file") {
		value, _, err := readSendBody(state.stdin, flags.TextBody, flags.TextFile, "signature text")
		if err != nil {
			return update, err
		}
		update.TextBody = &value
	}
	if cmd.Flags().Changed("html") || cmd.Flags().Changed("html-file") {
		value, _, err := readSendBody(state.stdin, flags.HTMLBody, flags.HTMLFile, "signature HTML")
		if err != nil {
			return update, err
		}
		update.HTMLBody = &value
	}
	return update, nil
}

func readSignatureCreateJSON(cmd *cobra.Command, state *executionState, input jsonInputOptions, flags *signatureComposeFlags) (bool, error) {
	request := signatureCreateJSONRequest{IdentityID: flags.IdentityID, TextBody: flags.TextBody, TextFile: flags.TextFile, HTMLBody: flags.HTMLBody, HTMLFile: flags.HTMLFile}
	used, err := readJSONInput(cmd, state, input, &request, "identity", "text", "text-file", "html", "html-file")
	if err != nil || !used {
		return used, err
	}
	flags.IdentityID, flags.TextBody, flags.TextFile, flags.HTMLBody, flags.HTMLFile = request.IdentityID, request.TextBody, request.TextFile, request.HTMLBody, request.HTMLFile
	return true, nil
}

func readSignatureUpdateJSON(cmd *cobra.Command, state *executionState, input jsonInputOptions, id *string, update *maildomain.SignatureUpdate) (bool, error) {
	request := signatureUpdateJSONRequest{SignatureID: *id}
	used, err := readJSONInput(cmd, state, input, &request, "identity", "text", "text-file", "html", "html-file")
	if err != nil || !used {
		return used, err
	}
	if jsonInputConsumesStdin(input) && ((request.TextFile != nil && *request.TextFile == "-") || (request.HTMLFile != nil && *request.HTMLFile == "-")) {
		return true, NewAppError(ErrorUsage, "signature body files cannot read stdin when --input-json also reads stdin", false)
	}
	if request.TextBody != nil && request.TextFile != nil {
		return true, NewAppError(ErrorUsage, "choose either text_body or text_file", false)
	}
	if request.HTMLBody != nil && request.HTMLFile != nil {
		return true, NewAppError(ErrorUsage, "choose either html_body or html_file", false)
	}
	*id = request.SignatureID
	update.IdentityID = request.IdentityID
	if request.TextBody != nil {
		value := *request.TextBody
		update.TextBody = &value
	} else if request.TextFile != nil {
		value, _, readErr := readSendBody(state.stdin, "", *request.TextFile, "signature text")
		if readErr != nil {
			return true, readErr
		}
		update.TextBody = &value
	}
	if request.HTMLBody != nil {
		value := *request.HTMLBody
		update.HTMLBody = &value
	} else if request.HTMLFile != nil {
		value, _, readErr := readSendBody(state.stdin, "", *request.HTMLFile, "signature HTML")
		if readErr != nil {
			return true, readErr
		}
		update.HTMLBody = &value
	}
	return true, nil
}

func renderSignatureResult(state *executionState, accountID string, value maildomain.Signature, schemaVersion int64, jsonOutput bool) error {
	if jsonOutput {
		return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: signatureData{AccountID: accountID, Signature: value, LocalOnly: true, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
	}
	renderer := NewCompactTextRenderer(state.stdout)
	if err := renderer.Record("signature", value.ID, Field("identity", value.IdentityID), Field("scope", "local"), Field("text_bytes", len(value.TextBody)), Field("html_bytes", len(value.HTMLBody)), Field("updated_at", value.UpdatedAt)); err != nil {
		return err
	}
	if err := renderer.Record("signature-text", value.ID, Field("body", value.TextBody)); err != nil {
		return err
	}
	if value.HTMLBody != "" {
		return renderer.Record("signature-html", value.ID, Field("body", value.HTMLBody))
	}
	return nil
}

func publicSignatureMetadata(value maildomain.Signature) publicSignatureSummary {
	return publicSignatureSummary{ID: value.ID, IdentityID: value.IdentityID, TextBytes: len(value.TextBody), HTMLBytes: len(value.HTMLBody), UpdatedAt: value.UpdatedAt}
}

func renderSignatureMetadata(renderer *CompactTextRenderer, value maildomain.Signature) error {
	return renderer.Record("signature", value.ID, Field("identity", value.IdentityID), Field("text_bytes", len(value.TextBody)), Field("html_bytes", len(value.HTMLBody)), Field("updated_at", value.UpdatedAt))
}

func renderSignatureDefault(renderer *CompactTextRenderer, value maildomain.SignatureDefault) error {
	selected := value.SignatureID
	if value.None {
		selected = "none"
	}
	return renderer.Record("signature-default", string(value.Operation), Field("identity", value.IdentityID), Field("scope", "local"), Field("signature", selected), Field("updated_at", value.UpdatedAt))
}
