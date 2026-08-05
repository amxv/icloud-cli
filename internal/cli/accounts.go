package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type credentialSourceOptions struct {
	passwordStdin bool
	environment   string
	file          string
}

type accountMutationData struct {
	Outcome          string                   `json:"outcome"`
	AccountID        string                   `json:"account_id"`
	Status           account.Status           `json:"status,omitempty"`
	Default          bool                     `json:"default,omitempty"`
	CredentialScheme account.CredentialScheme `json:"credential_scheme,omitempty"`
	KeyringDeleted   bool                     `json:"keyring_deleted,omitempty"`
	SchemaVersion    int64                    `json:"schema_version"`
}

type credentialData struct {
	Outcome       string             `json:"outcome"`
	Credential    account.Credential `json:"credential"`
	SchemaVersion int64              `json:"schema_version"`
}

func newAccountAddCommand(state *executionState) *cobra.Command {
	var (
		name         string
		email        string
		imapUsername string
		makeDefault  bool
		jsonOutput   bool
		source       credentialSourceOptions
	)
	command := &cobra.Command{
		Use:   "add",
		Short: "Add an offline iCloud Mail account profile",
		Args:  cobra.NoArgs,
		Example: "  printf '%s\\n' \"$APP_PASSWORD\" | icloud account add --name Personal --email owner@icloud.com --password-stdin\n" +
			"  icloud account add --name Agent --email agent@icloud.com --credential-env ICLOUD_AGENT_PASSWORD",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			setup, err := credentialSetupFromOptions(state, source)
			if err != nil {
				return err
			}
			defer wipeSecret(setup.Secret)
			value, schemaVersion, err := state.accounts.Add(cmd.Context(), state.global.Home, account.AddInput{
				Name:         name,
				Email:        email,
				IMAPUsername: imapUsername,
				MakeDefault:  makeDefault,
				Credential:   setup,
			})
			if err != nil {
				return err
			}
			data := accountMutationData{
				Outcome:          "added",
				AccountID:        value.ID,
				Status:           value.Status,
				Default:          value.Default,
				CredentialScheme: account.CredentialScheme(value.CredentialScheme),
				SchemaVersion:    schemaVersion,
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("account-added", value.ID,
				Field("status", value.Status),
				Field("default", value.Default),
				Field("credential", value.CredentialScheme),
			)
		},
	}
	command.Flags().StringVar(&name, "name", "", "Friendly account name")
	command.Flags().StringVar(&email, "email", "", "iCloud Mail address")
	command.Flags().StringVar(&imapUsername, "imap-username", "", "IMAP username (defaults to the email address)")
	command.Flags().BoolVar(&makeDefault, "default", false, "Make this the default account")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindCredentialSourceFlags(command, &source)
	_ = command.MarkFlagRequired("name")
	_ = command.MarkFlagRequired("email")
	return command
}

func newAccountRenameCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:     "rename <selector> <new-name>",
		Short:   "Rename an account profile",
		Args:    cobra.ExactArgs(2),
		Example: "  icloud account rename Personal Primary",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			value, schemaVersion, err := state.accounts.Rename(cmd.Context(), state.global.Home, args[0], args[1])
			if err != nil {
				return err
			}
			data := accountMutationData{Outcome: "renamed", AccountID: value.ID, Status: value.Status, Default: value.Default, CredentialScheme: account.CredentialScheme(value.CredentialScheme), SchemaVersion: schemaVersion}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("account-renamed", value.ID)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func newAccountDefaultCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:     "default <selector>",
		Short:   "Set the default account",
		Args:    cobra.ExactArgs(1),
		Example: "  icloud account default Work",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			value, schemaVersion, err := state.accounts.SetDefault(cmd.Context(), state.global.Home, args[0])
			if err != nil {
				return err
			}
			data := accountMutationData{Outcome: "defaulted", AccountID: value.ID, Status: value.Status, Default: true, CredentialScheme: account.CredentialScheme(value.CredentialScheme), SchemaVersion: schemaVersion}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("account-default", value.ID)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func newAccountRemoveCommand(state *executionState) *cobra.Command {
	var yes, jsonOutput bool
	command := &cobra.Command{
		Use:     "remove <selector>",
		Short:   "Remove an account profile and its native keyring entry",
		Args:    cobra.ExactArgs(1),
		Example: "  icloud account remove Old --yes",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			if !yes {
				return NewAppError(ErrorSafety, "account removal requires --yes", false)
			}
			value, keyringDeleted, schemaVersion, err := state.accounts.Remove(cmd.Context(), state.global.Home, args[0])
			if err != nil {
				return err
			}
			data := accountMutationData{Outcome: "removed", AccountID: value.ID, KeyringDeleted: keyringDeleted, SchemaVersion: schemaVersion}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("account-removed", value.ID, Field("keyring_deleted", keyringDeleted))
		},
	}
	command.Flags().BoolVar(&yes, "yes", false, "Authorize removal without prompting")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func newAccountCredentialCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "credential",
		Short:   "Inspect or change account credential metadata",
		Args:    cobra.NoArgs,
		Example: "  icloud account credential show Personal\n  icloud account credential replace Personal --password-stdin",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	command.AddCommand(newAccountCredentialShowCommand(state))
	command.AddCommand(newAccountCredentialReplaceCommand(state))
	command.AddCommand(newAccountCredentialDeleteCommand(state))
	return command
}

func newAccountCredentialShowCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:     "show <selector>",
		Short:   "Show credential metadata without resolving or revealing the value",
		Args:    cobra.ExactArgs(1),
		Example: "  icloud account credential show Personal",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			credential, schemaVersion, err := state.accounts.CredentialShow(cmd.Context(), state.global.Home, args[0])
			if err != nil {
				return err
			}
			data := credentialData{Outcome: "shown", Credential: credential, SchemaVersion: schemaVersion}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return renderCredential(NewCompactTextRenderer(state.stdout), credential)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func newAccountCredentialReplaceCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var source credentialSourceOptions
	command := &cobra.Command{
		Use:     "replace <selector>",
		Short:   "Replace an account credential and return it to pending validation",
		Args:    cobra.ExactArgs(1),
		Example: "  printf '%s\\n' \"$NEW_PASSWORD\" | icloud account credential replace Personal --password-stdin",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			setup, err := credentialSetupFromOptions(state, source)
			if err != nil {
				return err
			}
			defer wipeSecret(setup.Secret)
			credential, schemaVersion, err := state.accounts.CredentialReplace(cmd.Context(), state.global.Home, args[0], setup)
			if err != nil {
				return err
			}
			data := credentialData{Outcome: "replaced", Credential: credential, SchemaVersion: schemaVersion}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("credential-replaced", credential.AccountID, Field("scheme", credential.Scheme))
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	bindCredentialSourceFlags(command, &source)
	return command
}

func newAccountCredentialDeleteCommand(state *executionState) *cobra.Command {
	var yes, jsonOutput bool
	command := &cobra.Command{
		Use:     "delete <selector>",
		Short:   "Delete an account credential reference and native keyring entry",
		Args:    cobra.ExactArgs(1),
		Example: "  icloud account credential delete Personal --yes",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			if !yes {
				return NewAppError(ErrorSafety, "credential deletion requires --yes", false)
			}
			credential, schemaVersion, err := state.accounts.CredentialDelete(cmd.Context(), state.global.Home, args[0])
			if err != nil {
				return err
			}
			data := credentialData{Outcome: "deleted", Credential: credential, SchemaVersion: schemaVersion}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{OK: true, Data: data, Meta: EnvelopeMeta{RequestID: state.requestID}})
			}
			return NewCompactTextRenderer(state.stdout).Record("credential-deleted", credential.AccountID, Field("scheme", credential.Scheme))
		},
	}
	command.Flags().BoolVar(&yes, "yes", false, "Authorize credential deletion without prompting")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func bindCredentialSourceFlags(command *cobra.Command, options *credentialSourceOptions) {
	command.Flags().BoolVar(&options.passwordStdin, "password-stdin", false, "Read an app-specific password from stdin and store it in the native keyring")
	command.Flags().StringVar(&options.environment, "credential-env", "", "Persist an environment-variable credential reference")
	command.Flags().StringVar(&options.file, "credential-file", "", "Persist a credential-file reference")
}

func credentialSetupFromOptions(state *executionState, options credentialSourceOptions) (account.CredentialSetup, error) {
	sources := 0
	if options.passwordStdin {
		sources++
	}
	if strings.TrimSpace(options.environment) != "" {
		sources++
	}
	if strings.TrimSpace(options.file) != "" {
		sources++
	}
	if sources > 1 {
		return account.CredentialSetup{}, NewAppError(ErrorUsage, "choose only one of --password-stdin, --credential-env, or --credential-file", false)
	}
	if strings.TrimSpace(options.environment) != "" {
		return account.CredentialSetup{Scheme: account.CredentialEnv, Reference: options.environment}, nil
	}
	if strings.TrimSpace(options.file) != "" {
		return account.CredentialSetup{Scheme: account.CredentialFile, Reference: options.file}, nil
	}

	interactive := isTerminalReader(state.stdin)
	if options.passwordStdin && interactive && state.global.NoInput {
		return account.CredentialSetup{}, NewAppError(ErrorSafety, "--password-stdin requires redirected stdin when --no-input is set", false)
	}
	if !options.passwordStdin && (!interactive || state.global.NoInput) {
		return account.CredentialSetup{}, NewAppError(ErrorSafety, "credential input is required; use --password-stdin, --credential-env, or --credential-file", false)
	}
	value, err := readSecretInput(state.stdin, state.stderr, interactive)
	if err != nil {
		return account.CredentialSetup{}, err
	}
	return account.CredentialSetup{Scheme: account.CredentialKeyring, Secret: value}, nil
}

func readSecretInput(reader io.Reader, stderr io.Writer, interactive bool) ([]byte, error) {
	if interactive {
		file, ok := reader.(*os.File)
		if !ok {
			return nil, NewAppError(ErrorConfiguration, "secure terminal credential input is unavailable", false)
		}
		if _, err := fmt.Fprint(stderr, "App-specific password: "); err != nil {
			return nil, WrapAppError(ErrorInternal, "cannot write credential prompt", false, err)
		}
		value, err := term.ReadPassword(int(file.Fd()))
		_, _ = fmt.Fprintln(stderr)
		if err != nil {
			return nil, WrapAppError(ErrorConfiguration, "cannot read credential from the terminal", false, err)
		}
		if len(value) > secret.MaxSecretBytes {
			wipeSecret(value)
			return nil, NewAppError(ErrorUsage, "credential input exceeds the size limit", false)
		}
		return value, nil
	}
	value, err := io.ReadAll(io.LimitReader(reader, secret.MaxSecretBytes+1))
	if err != nil {
		return nil, WrapAppError(ErrorConfiguration, "cannot read credential from stdin", false, err)
	}
	if len(value) > secret.MaxSecretBytes {
		wipeSecret(value)
		return nil, NewAppError(ErrorUsage, "credential input exceeds the size limit", false)
	}
	return value, nil
}

func isTerminalReader(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func selectedAccountSelector(state *executionState, args []string) (string, error) {
	positional := ""
	if len(args) > 0 {
		positional = strings.TrimSpace(args[0])
	}
	global := strings.TrimSpace(state.global.Account)
	if positional != "" && global != "" && !strings.EqualFold(positional, global) {
		return "", NewAppError(ErrorUsage, "the positional account selector conflicts with --account", false)
	}
	if positional != "" {
		return positional, nil
	}
	return global, nil
}

func renderCredential(renderer *CompactTextRenderer, credential account.Credential) error {
	return renderer.Record("credential", credential.AccountID,
		Field("scheme", credential.Scheme),
		Field("configured", credential.Configured),
		Field("updated_at", credential.UpdatedAt),
	)
}

func wipeSecret(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
