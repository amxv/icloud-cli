package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/spf13/cobra"
)

const maxSMIMECertificateFileBytes = 64 << 10

type mailSMIMEService interface {
	MailSMIMECertificatesList(context.Context, string, string, string, string, int) (account.Account, maildomain.SMIMECertificatePage, int64, error)
	MailSMIMECertificateImport(context.Context, string, string, string, []byte, bool) (account.Account, maildomain.SMIMECertificate, int64, error)
	MailSMIMECertificateRemove(context.Context, string, string, string) (account.Account, maildomain.SMIMECertificate, int64, error)
	MailSMIMEIdentitiesList(context.Context, string, string, string, int) (account.Account, maildomain.SMIMEIdentityPage, int64, error)
	MailSMIMEIdentityShow(context.Context, string, string, string) (account.Account, maildomain.SMIMEIdentity, int64, error)
	MailSMIMEIdentityConfigure(context.Context, string, string, string, string, account.CredentialSetup) (account.Account, maildomain.SMIMEIdentity, int64, error)
	MailSMIMEIdentityRemove(context.Context, string, string, string) (account.Account, maildomain.SMIMEIdentity, int64, error)
}

type smimeCertificateListReadJSONRequest struct {
	Email  string `json:"email,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type smimeCertificateImportJSONRequest struct {
	Email           string `json:"email"`
	CertificateFile string `json:"certificate_file"`
	Trust           bool   `json:"trust,omitempty"`
}

type smimeCertificateTargetJSONRequest struct {
	CertificateID string `json:"certificate_id"`
}

type smimeIdentityListReadJSONRequest struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type smimeIdentityTargetJSONRequest struct {
	Identity string `json:"identity"`
}

type smimeIdentityConfigureJSONRequest struct {
	MailIdentity   string `json:"mail_identity"`
	CertificateID  string `json:"certificate_id"`
	PrivateKeyEnv  string `json:"private_key_env,omitempty"`
	PrivateKeyFile string `json:"private_key_file,omitempty"`
}

type smimeCertificateListData struct {
	AccountID     string                        `json:"account_id"`
	Certificates  []maildomain.SMIMECertificate `json:"certificates"`
	Count         int                           `json:"count"`
	More          bool                          `json:"more,omitempty"`
	NextCursor    string                        `json:"next_cursor,omitempty"`
	SchemaVersion int64                         `json:"schema_version"`
}

type smimeCertificateData struct {
	AccountID     string                      `json:"account_id"`
	Certificate   maildomain.SMIMECertificate `json:"certificate"`
	SchemaVersion int64                       `json:"schema_version"`
}

type smimeCertificateRemoveData struct {
	AccountID     string `json:"account_id"`
	CertificateID string `json:"certificate_id"`
	Removed       bool   `json:"removed"`
	SchemaVersion int64  `json:"schema_version"`
}

func newMailSMIMECommand(state *executionState) *cobra.Command {
	command := &cobra.Command{Use: "smime", Short: "Inspect inbound S/MIME and manage certificates and decryption identities", Args: cobra.NoArgs}
	command.AddCommand(newMailSMIMECertificatesCommand(state))
	command.AddCommand(newMailSMIMEIdentitiesCommand(state))
	return command
}

func newMailSMIMECertificatesCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{Use: "certificates", Short: "Manage bounded account-scoped public S/MIME certificates", Args: cobra.NoArgs}
	command.AddCommand(newMailSMIMECertificatesListCommand(state))
	command.AddCommand(newMailSMIMECertificateImportCommand(state))
	command.AddCommand(newMailSMIMECertificateRemoveCommand(state))
	return command
}

func newMailSMIMECertificatesListCommand(state *executionState) *cobra.Command {
	var email, cursor string
	var limit int
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use: "list", Short: "List public S/MIME certificate metadata without DER", Args: exactArgsOrJSONInput(&input, 0),
		Example: "  icloud mail smime certificates list\n  icloud mail smime certificates list --email sender@example.com --limit 20 --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, ok := state.mail.(mailSMIMEService)
			if !ok {
				return NewAppError(ErrorConfiguration, "mail S/MIME certificate management is unavailable", false)
			}
			request := smimeCertificateListReadJSONRequest{Email: email, Cursor: cursor, Limit: limit}
			if used, err := readJSONInput(cmd, state, input, &request, "email", "cursor", "limit"); err != nil {
				return err
			} else if used {
				email, cursor, limit = request.Email, request.Cursor, request.Limit
			}
			if limit == 0 {
				limit = DefaultPageSize
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			accountValue, page, schemaVersion, err := service.MailSMIMECertificatesList(cmd.Context(), state.global.Home, state.global.Account, email, cursor, limit)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: smimeCertificateListData{AccountID: accountValue.ID, Certificates: page.Certificates, Count: len(page.Certificates), More: page.More, NextCursor: page.NextCursor, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, value := range page.Certificates {
				if err := renderSMIMECertificate(renderer, value); err != nil {
					return err
				}
			}
			return renderer.Summary("smime-certificates", Field("account", accountValue.ID), Field("count", len(page.Certificates)), Field("more", page.More), Field("next_cursor", page.NextCursor))
		},
	}
	command.Flags().StringVar(&email, "email", "", "Filter by exact scoped mailbox email")
	command.Flags().StringVar(&cursor, "cursor", "", "Continue from a next_cursor for the same account and email filter")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum certificates to return (1-%d)", MaximumPageSize))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindReadJSONInput(command, &input)
	return command
}

func newMailSMIMECertificateImportCommand(state *executionState) *cobra.Command {
	var email string
	var trust, jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use: "import <certificate-file>", Short: "Import one public certificate; trust is opt-in", Args: exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail smime certificates import signer.pem --email sender@example.com\n  icloud mail smime certificates import private-root.pem --email sender@example.com --trust --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			service, ok := state.mail.(mailSMIMEService)
			if !ok {
				return NewAppError(ErrorConfiguration, "mail S/MIME certificate management is unavailable", false)
			}
			path := ""
			if len(args) == 1 {
				path = strings.TrimSpace(args[0])
			}
			request := smimeCertificateImportJSONRequest{Email: email, CertificateFile: path, Trust: trust}
			if used, err := readJSONInput(cmd, state, input, &request, "email", "trust"); err != nil {
				return err
			} else if used {
				email, path, trust = request.Email, request.CertificateFile, request.Trust
			}
			if strings.TrimSpace(email) == "" || strings.TrimSpace(path) == "" {
				return NewAppError(ErrorUsage, "--email and a certificate file are required", false)
			}
			data, err := readBoundedSMIMECertificateFile(path)
			if err != nil {
				return err
			}
			accountValue, value, schemaVersion, err := service.MailSMIMECertificateImport(cmd.Context(), state.global.Home, state.global.Account, email, data, trust)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: smimeCertificateData{AccountID: accountValue.ID, Certificate: value, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderSMIMECertificate(NewCompactTextRenderer(state.stdout), value)
		},
	}
	command.Flags().StringVar(&email, "email", "", "Scope this public certificate to one mailbox identity")
	command.Flags().BoolVar(&trust, "trust", false, "Explicitly trust this certificate as an account/email-scoped verification root")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailSMIMECertificateRemoveCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use: "remove <certificate-id>", Short: "Remove one account-scoped public S/MIME certificate", Args: exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail smime certificates remove smc_…\n  icloud mail smime certificates remove --input-json remove-certificate.json --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			service, ok := state.mail.(mailSMIMEService)
			if !ok {
				return NewAppError(ErrorConfiguration, "mail S/MIME certificate management is unavailable", false)
			}
			id := ""
			if len(args) == 1 {
				id = strings.TrimSpace(args[0])
			}
			request := smimeCertificateTargetJSONRequest{CertificateID: id}
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				id = request.CertificateID
			}
			accountValue, value, schemaVersion, err := service.MailSMIMECertificateRemove(cmd.Context(), state.global.Home, state.global.Account, id)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: smimeCertificateRemoveData{AccountID: accountValue.ID, CertificateID: value.ID, Removed: true, SchemaVersion: schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("smime-certificate-remove", value.ID, Field("removed", true), Field("email", value.Email))
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func readBoundedSMIMECertificateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, WrapAppError(ErrorConfiguration, "cannot inspect S/MIME certificate file", false, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, NewAppError(ErrorSafety, "S/MIME certificate input must be a regular non-symlink file", false)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, WrapAppError(ErrorConfiguration, "cannot open S/MIME certificate file", false, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSMIMECertificateFileBytes+1))
	if err != nil {
		return nil, WrapAppError(ErrorConfiguration, "cannot read S/MIME certificate file", false, err)
	}
	if len(data) == 0 || len(data) > maxSMIMECertificateFileBytes {
		return nil, NewAppError(ErrorUsage, "S/MIME certificate file must be between 1 and 65536 bytes", false)
	}
	return data, nil
}

func renderSMIMECertificate(renderer *CompactTextRenderer, value maildomain.SMIMECertificate) error {
	return renderer.Record("smime-certificate", value.ID, Field("email", value.Email), Field("fingerprint_sha256", value.FingerprintSHA256), Field("trusted", value.Trusted), Field("subject", value.Subject), Field("issuer", value.Issuer), Field("not_before", value.NotBefore), Field("not_after", value.NotAfter), Field("is_ca", value.IsCA), Field("key_usage", value.KeyUsage), Field("extended_key_usage", value.ExtendedKeyUsage))
}

func newMailSMIMEIdentitiesCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{Use: "identities", Short: "Manage mail-identity S/MIME certificates and external private-key references", Args: cobra.NoArgs}
	command.AddCommand(newMailSMIMEIdentitiesListCommand(state))
	command.AddCommand(newMailSMIMEIdentityShowCommand(state))
	command.AddCommand(newMailSMIMEIdentityConfigureCommand(state))
	command.AddCommand(newMailSMIMEIdentityRemoveCommand(state))
	return command
}

func newMailSMIMEIdentitiesListCommand(state *executionState) *cobra.Command {
	var cursor string
	var limit int
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use: "list", Short: "List S/MIME identity metadata without private-key references", Args: exactArgsOrJSONInput(&input, 0),
		Example: "  icloud mail smime identities list\n  icloud mail smime identities list --limit 20 --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, ok := state.mail.(mailSMIMEService)
			if !ok {
				return NewAppError(ErrorConfiguration, "S/MIME identity management is unavailable", false)
			}
			request := smimeIdentityListReadJSONRequest{Cursor: cursor, Limit: limit}
			if used, err := readJSONInput(cmd, state, input, &request, "cursor", "limit"); err != nil {
				return err
			} else if used {
				cursor, limit = request.Cursor, request.Limit
			}
			if limit == 0 {
				limit = DefaultPageSize
			}
			if limit < 1 || limit > maildomain.MaxSMIMEIdentitiesPerAccount {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", maildomain.MaxSMIMEIdentitiesPerAccount), false)
			}
			accountValue, page, schemaVersion, err := service.MailSMIMEIdentitiesList(cmd.Context(), state.global.Home, state.global.Account, cursor, limit)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: map[string]any{"account_id": accountValue.ID, "identities": page.Identities, "count": len(page.Identities), "more": page.More, "next_cursor": page.NextCursor, "schema_version": schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, value := range page.Identities {
				if err := renderSMIMEIdentity(renderer, value); err != nil {
					return err
				}
			}
			return renderer.Summary("smime-identities", Field("account", accountValue.ID), Field("count", len(page.Identities)), Field("more", page.More), Field("next_cursor", page.NextCursor))
		},
	}
	command.Flags().StringVar(&cursor, "cursor", "", "Continue from a next_cursor for the same account")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum identities to return (1-%d)", maildomain.MaxSMIMEIdentitiesPerAccount))
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindReadJSONInput(command, &input)
	return command
}

func newMailSMIMEIdentityShowCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use: "show <identity>", Short: "Show one S/MIME identity without its private-key reference", Args: exactReadArgsOrJSONInput(&input, 1),
		Example: "  icloud mail smime identities show smi_…\n  icloud mail smime identities show owner@icloud.com --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			selector := ""
			if len(args) == 1 {
				selector = args[0]
			}
			request := smimeIdentityTargetJSONRequest{Identity: selector}
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				selector = request.Identity
			}
			service, ok := state.mail.(mailSMIMEService)
			if !ok {
				return NewAppError(ErrorConfiguration, "S/MIME identity management is unavailable", false)
			}
			accountValue, value, schemaVersion, err := service.MailSMIMEIdentityShow(cmd.Context(), state.global.Home, state.global.Account, selector)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: map[string]any{"account_id": accountValue.ID, "identity": value, "schema_version": schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderSMIMEIdentity(NewCompactTextRenderer(state.stdout), value)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindReadJSONInput(command, &input)
	return command
}

func newMailSMIMEIdentityConfigureCommand(state *executionState) *cobra.Command {
	var certificateID, keyEnv, keyFile string
	var keyStdin, jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use: "configure <mail-identity>", Short: "Bind one mail identity to a public certificate and external private key", Args: exactArgsOrJSONInput(&input, 1),
		Example: "  cat private-key.pem | icloud mail smime identities configure mid_… --certificate smc_… --private-key-stdin\n  icloud mail smime identities configure owner@icloud.com --certificate smc_… --private-key-file ~/.config/icloud-cli/smime-key.pem",
		RunE: func(cmd *cobra.Command, args []string) error {
			mailIdentity := ""
			if len(args) == 1 {
				mailIdentity = args[0]
			}
			request := smimeIdentityConfigureJSONRequest{MailIdentity: mailIdentity, CertificateID: certificateID, PrivateKeyEnv: keyEnv, PrivateKeyFile: keyFile}
			if used, err := readJSONInput(cmd, state, input, &request, "certificate", "private-key-env", "private-key-file"); err != nil {
				return err
			} else if used {
				if keyStdin {
					return NewAppError(ErrorUsage, "--private-key-stdin cannot be combined with --input-json", false)
				}
				mailIdentity, certificateID, keyEnv, keyFile = request.MailIdentity, request.CertificateID, request.PrivateKeyEnv, request.PrivateKeyFile
			}
			if strings.TrimSpace(mailIdentity) == "" || strings.TrimSpace(certificateID) == "" {
				return NewAppError(ErrorUsage, "mail identity and --certificate are required", false)
			}
			sources := 0
			if keyStdin {
				sources++
			}
			if strings.TrimSpace(keyEnv) != "" {
				sources++
			}
			if strings.TrimSpace(keyFile) != "" {
				sources++
			}
			if sources != 1 {
				return NewAppError(ErrorUsage, "choose exactly one of --private-key-stdin, --private-key-env, or --private-key-file", false)
			}
			setup := account.CredentialSetup{}
			if keyStdin {
				if isTerminalReader(state.stdin) {
					return NewAppError(ErrorSafety, "--private-key-stdin requires redirected stdin; private key material is never accepted on argv or echoed at a terminal", false)
				}
				value, err := io.ReadAll(io.LimitReader(state.stdin, secret.MaxSecretBytes+1))
				if err != nil {
					return WrapAppError(ErrorConfiguration, "cannot read S/MIME private key from stdin", false, err)
				}
				if len(value) == 0 || len(value) > secret.MaxSecretBytes {
					wipeSecret(value)
					return NewAppError(ErrorUsage, "S/MIME private key input is empty or exceeds the size limit", false)
				}
				defer wipeSecret(value)
				setup = account.CredentialSetup{Scheme: account.CredentialKeyring, Secret: value}
			} else if strings.TrimSpace(keyEnv) != "" {
				setup = account.CredentialSetup{Scheme: account.CredentialEnv, Reference: keyEnv}
			} else {
				setup = account.CredentialSetup{Scheme: account.CredentialFile, Reference: keyFile}
			}
			service, ok := state.mail.(mailSMIMEService)
			if !ok {
				return NewAppError(ErrorConfiguration, "S/MIME identity management is unavailable", false)
			}
			accountValue, value, schemaVersion, err := service.MailSMIMEIdentityConfigure(cmd.Context(), state.global.Home, state.global.Account, mailIdentity, certificateID, setup)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: map[string]any{"account_id": accountValue.ID, "identity": value, "schema_version": schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderSMIMEIdentity(NewCompactTextRenderer(state.stdout), value)
		},
	}
	command.Flags().StringVar(&certificateID, "certificate", "", "Account-scoped end-entity public certificate ID")
	command.Flags().BoolVar(&keyStdin, "private-key-stdin", false, "Read private key bytes from redirected stdin and store them in the native keyring")
	command.Flags().StringVar(&keyEnv, "private-key-env", "", "Persist an environment-variable private-key reference")
	command.Flags().StringVar(&keyFile, "private-key-file", "", "Persist a private 0600 regular-file private-key reference")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func newMailSMIMEIdentityRemoveCommand(state *executionState) *cobra.Command {
	var yes, jsonOutput bool
	var input jsonInputOptions
	command := &cobra.Command{
		Use: "remove <identity>", Short: "Remove one S/MIME identity and its owned keyring secret", Args: exactArgsOrJSONInput(&input, 1),
		Example: "  icloud mail smime identities remove smi_… --yes\n  icloud mail smime identities remove --input-json remove.json --yes --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return NewAppError(ErrorSafety, "S/MIME identity removal requires --yes because an owned native-keyring private key may be deleted", false)
			}
			selector := ""
			if len(args) == 1 {
				selector = args[0]
			}
			request := smimeIdentityTargetJSONRequest{Identity: selector}
			if used, err := readJSONInput(cmd, state, input, &request); err != nil {
				return err
			} else if used {
				selector = request.Identity
			}
			service, ok := state.mail.(mailSMIMEService)
			if !ok {
				return NewAppError(ErrorConfiguration, "S/MIME identity management is unavailable", false)
			}
			accountValue, value, schemaVersion, err := service.MailSMIMEIdentityRemove(cmd.Context(), state.global.Home, state.global.Account, selector)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: map[string]any{"account_id": accountValue.ID, "identity_id": value.ID, "removed": true, "schema_version": schemaVersion}, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("smime-identity-remove", value.ID, Field("removed", true), Field("mail_identity", value.MailIdentityID), Field("email", value.Email))
		},
	}
	command.Flags().BoolVar(&yes, "yes", false, "Authorize S/MIME identity removal and owned native-keyring secret deletion without prompting")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindJSONInput(command, &input)
	return command
}

func renderSMIMEIdentity(renderer *CompactTextRenderer, value maildomain.SMIMEIdentity) error {
	return renderer.Record("smime-identity", value.ID,
		Field("mail_identity", value.MailIdentityID), Field("email", value.Email), Field("certificate", value.CertificateID),
		Field("fingerprint_sha256", value.FingerprintSHA256), Field("subject", value.Subject),
		Field("not_before", value.NotBefore), Field("not_after", value.NotAfter), Field("key_usage", value.KeyUsage),
		Field("extended_key_usage", value.ExtendedKeyUsage), Field("private_key_scheme", value.PrivateKeyScheme), Field("private_key_configured", value.PrivateKeyConfigured),
	)
}
