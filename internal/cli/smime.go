package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/spf13/cobra"
)

const maxSMIMECertificateFileBytes = 64 << 10

type mailSMIMEService interface {
	MailSMIMECertificatesList(context.Context, string, string, string, string, int) (account.Account, maildomain.SMIMECertificatePage, int64, error)
	MailSMIMECertificateImport(context.Context, string, string, string, []byte, bool) (account.Account, maildomain.SMIMECertificate, int64, error)
	MailSMIMECertificateRemove(context.Context, string, string, string) (account.Account, maildomain.SMIMECertificate, int64, error)
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
	command := &cobra.Command{Use: "smime", Short: "Inspect inbound S/MIME and manage public certificate trust material", Args: cobra.NoArgs}
	command.AddCommand(newMailSMIMECertificatesCommand(state))
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
