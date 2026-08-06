package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/folder"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

const commandName = "icloud"

type Config struct {
	Version       string
	SchemaVersion int64
	Stdin         io.Reader
	Stdout        io.Writer
	Stderr        io.Writer
	RequestIDGen  func() string
	Accounts      AccountService
	Folders       FolderService
	Mail          MailService
}

type FolderService interface {
	FolderList(context.Context, string, string, int, bool) (account.Account, []folder.Folder, bool, int64, error)
	FolderShow(context.Context, string, string, string) (account.Account, folder.Folder, int64, error)
	FolderStatus(context.Context, string, string) (account.Account, []folder.Folder, int64, error)
	FolderSetSync(context.Context, string, string, string, bool) (account.Account, folder.Folder, int64, error)
}

// AccountService is the application-owned boundary used by account commands.
// Implementations may initialize durable state lazily from the selected home.
type AccountService interface {
	List(context.Context, string, int) ([]account.Account, bool, int64, error)
	Show(context.Context, string, string) (account.Account, int64, error)
	Add(context.Context, string, account.AddInput) (account.Account, int64, error)
	Rename(context.Context, string, string, string) (account.Account, int64, error)
	SetDefault(context.Context, string, string) (account.Account, int64, error)
	Remove(context.Context, string, string) (account.Account, bool, int64, error)
	CredentialShow(context.Context, string, string) (account.Credential, int64, error)
	CredentialReplace(context.Context, string, string, account.CredentialSetup) (account.Credential, int64, error)
	CredentialDelete(context.Context, string, string) (account.Credential, int64, error)
	Test(context.Context, string, string, bool) ([]account.HealthReport, int64, error)
	Doctor(context.Context, string, string, bool, bool) ([]account.DoctorReport, int64, error)
}

type Result struct {
	ExitCode int
}

type globalOptions struct {
	Home      string
	Account   string
	Timeout   time.Duration
	NoInput   bool
	RequestID string
}

type executionState struct {
	version       string
	schemaVersion int64
	stdin         io.Reader
	stdout        io.Writer
	stderr        io.Writer
	requestID     string
	global        globalOptions
	accounts      AccountService
	folders       FolderService
	mail          MailService
	cancel        context.CancelFunc
}

type versionData struct {
	Command string `json:"command"`
	Version string `json:"version"`
}

type capabilityOutput struct {
	Default string `json:"default"`
	JSON    bool   `json:"json"`
	JSONL   bool   `json:"jsonl"`
}

type capabilitiesData struct {
	Command           string           `json:"command"`
	Version           string           `json:"version"`
	DataSchemaVersion int64            `json:"data_schema_version"`
	AccountMode       string           `json:"account_mode"`
	CredentialSchemes []string         `json:"credential_schemes"`
	NetworkValidation bool             `json:"network_validation"`
	Diagnostics       bool             `json:"diagnostics"`
	MailFolders       bool             `json:"mail_folders"`
	MailSync          bool             `json:"mail_sync"`
	MailContent       bool             `json:"mail_content"`
	MailAttachments   bool             `json:"mail_attachments"`
	MailFlags         bool             `json:"mail_flags"`
	MailThreads       bool             `json:"mail_threads"`
	MailSearch        bool             `json:"mail_search"`
	MailRemoteSearch  bool             `json:"mail_remote_search"`
	MailHydration     bool             `json:"mail_hydration"`
	MailSend          bool             `json:"mail_send"`
	MailReply         bool             `json:"mail_reply"`
	Output            capabilityOutput `json:"output"`
	Completion        []string         `json:"completion"`
	Man               bool             `json:"man"`
}

type accountListData struct {
	Accounts      []account.Account `json:"accounts"`
	Count         int               `json:"count"`
	More          bool              `json:"more,omitempty"`
	SchemaVersion int64             `json:"schema_version"`
}

type accountShowData struct {
	Account       account.Account `json:"account"`
	SchemaVersion int64           `json:"schema_version"`
}

func Execute(ctx context.Context, args []string, config Config) Result {
	if config.Stdout == nil {
		config.Stdout = io.Discard
	}
	if config.Stdin == nil {
		config.Stdin = strings.NewReader("")
	}
	if config.Stderr == nil {
		config.Stderr = io.Discard
	}
	if config.RequestIDGen == nil {
		config.RequestIDGen = NewRequestID
	}
	if config.Accounts == nil {
		config.Accounts = unavailableAccountService{}
	}
	if config.Folders == nil {
		config.Folders = unavailableFolderService{}
	}
	if config.Mail == nil {
		config.Mail = unavailableMailService{}
	}

	requestID := requestedID(args)
	if !validRequestID(requestID) {
		requestID = config.RequestIDGen()
	}
	state := &executionState{
		version:       strings.TrimSpace(config.Version),
		schemaVersion: config.SchemaVersion,
		stdin:         config.Stdin,
		stdout:        config.Stdout,
		stderr:        config.Stderr,
		requestID:     requestID,
		accounts:      config.Accounts,
		folders:       config.Folders,
		mail:          config.Mail,
	}
	if state.version == "" {
		state.version = "dev"
	}

	root := newRootCommand(state)
	executionArgs := args
	if requestedJSON(args) {
		if token := rootCommandToken(args); token != "" && !isKnownRootCommand(root, token) {
			executionArgs = removeJSONFlag(args)
		}
	}
	root.SetArgs(executionArgs)
	root.SetContext(ctx)
	err := root.Execute()
	if state.cancel != nil {
		state.cancel()
	}
	if err == nil {
		return Result{ExitCode: ExitSuccess}
	}

	appErr := NormalizeError(err)
	if appErr.Code == ErrorUsage && strings.HasPrefix(strings.ToLower(appErr.Message), "unknown command") {
		if suggestions := root.SuggestionsFor(rootCommandToken(args)); len(suggestions) > 0 {
			appErr.Message += "; did you mean " + strconv.Quote(suggestions[0]) + "?"
		}
	}
	if requestedJSON(args) {
		_ = WriteJSON(config.Stderr, OutputEnvelope{
			OK:    false,
			Error: appErr,
			Meta:  EnvelopeMeta{RequestID: state.requestID},
		})
	} else {
		_ = NewCompactTextRenderer(config.Stderr).Error(appErr)
	}
	return Result{ExitCode: ExitCodeFor(appErr)}
}

func newRootCommand(state *executionState) *cobra.Command {
	root := &cobra.Command{
		Use:           commandName,
		Short:         "Control iCloud services from the command line",
		Long:          "icloud controls iCloud services from the command line. This build provides secure account setup, live read-only IMAP and SMTP validation, deliberate text, HTML, and attachment sending plus exact-message reply and reply-all with dry-run review, idempotency, explicit retry linkage, and Sent reconciliation, durable folder discovery, bounded folder synchronization and flag reconciliation, indexed local search, explicit bounded remote search and targeted hydration, complete local message reads, verified attachment export, location-aware read and flag updates, account health diagnostics, version and capability reporting, shell completions, and man-page generation.",
		Example:       "  icloud account add --name Personal --email owner@icloud.com --password-stdin\n  icloud account test\n  icloud mail send --to client@example.com --subject Update --body-file message.txt --dry-run\n  icloud mail reply msg_… --body-file reply.txt --dry-run\n  icloud mail sync\n  icloud mail message show msg_…\n  icloud mail mark-read msg_…\n  icloud doctor",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if state.global.Timeout <= 0 {
				return NewAppError(ErrorUsage, "--timeout must be greater than zero", false)
			}
			if state.global.RequestID != "" {
				if !validRequestID(state.global.RequestID) {
					return NewAppError(ErrorUsage, "--request-id must contain 1-128 letters, numbers, dots, underscores, or hyphens", false)
				}
				state.requestID = state.global.RequestID
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), state.global.Timeout)
			state.cancel = cancel
			cmd.SetContext(ctx)
			return nil
		},
	}
	root.SetOut(state.stdout)
	root.SetErr(state.stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return WrapAppError(ErrorUsage, err.Error(), false, err)
	})
	root.CompletionOptions.DisableDefaultCmd = true
	root.DisableAutoGenTag = true
	root.SuggestionsMinimumDistance = 2
	root.Version = state.version
	root.SetVersionTemplate("version\n{{.Version}}\n")

	root.PersistentFlags().StringVar(&state.global.Home, "home", "", "Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)")
	root.PersistentFlags().StringVar(&state.global.Account, "account", "", "Select an account by name, email, or stable ID")
	root.PersistentFlags().DurationVar(&state.global.Timeout, "timeout", 30*time.Second, "Maximum command duration")
	root.PersistentFlags().BoolVar(&state.global.NoInput, "no-input", false, "Never prompt for interactive input")
	root.PersistentFlags().StringVar(&state.global.RequestID, "request-id", "", "Attach a stable request ID to structured output")

	root.AddGroup(
		&cobra.Group{ID: "core", Title: "Core commands:"},
		&cobra.Group{ID: "account", Title: "Account commands:"},
		&cobra.Group{ID: "mail", Title: "Mail commands:"},
		&cobra.Group{ID: "docs", Title: "Documentation commands:"},
	)
	root.AddCommand(newAccountCommand(state))
	root.AddCommand(newMailCommand(state))
	root.AddCommand(newDoctorCommand(state))
	root.AddCommand(newVersionCommand(state))
	root.AddCommand(newCapabilitiesCommand(state))
	root.AddCommand(newCompletionCommand(root))
	root.AddCommand(newManCommand(root, state))
	root.InitDefaultHelpCmd()
	if helpCommand, _, err := root.Find([]string{"help"}); err == nil && helpCommand != root {
		helpCommand.GroupID = "docs"
	}
	return root
}

func newVersionCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "version",
		Short:   "Print the installed CLI version",
		GroupID: "core",
		Args:    cobra.NoArgs,
		Example: "  icloud version\n  icloud version --json",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			data := versionData{Command: commandName, Version: state.version}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{
					OK:   true,
					Data: data,
					Meta: EnvelopeMeta{RequestID: state.requestID},
				})
			}
			return NewCompactTextRenderer(state.stdout).Record("version", state.version)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return cmd
}

func newCapabilitiesCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "capabilities",
		Short:   "Describe capabilities in this installed build",
		GroupID: "core",
		Args:    cobra.NoArgs,
		Example: "  icloud capabilities\n  icloud capabilities --json",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			data := capabilitiesData{
				Command:           commandName,
				Version:           state.version,
				DataSchemaVersion: state.schemaVersion,
				AccountMode:       "validated_lifecycle",
				CredentialSchemes: []string{"keyring", "env", "file"},
				NetworkValidation: true,
				Diagnostics:       true,
				MailFolders:       true,
				MailSync:          true,
				MailContent:       true,
				MailAttachments:   true,
				MailFlags:         true,
				MailThreads:       true,
				MailSearch:        true,
				MailRemoteSearch:  true,
				MailHydration:     true,
				MailSend:          true,
				MailReply:         true,
				Output: capabilityOutput{
					Default: "compact",
					JSON:    true,
					JSONL:   false,
				},
				Completion: []string{"bash", "zsh", "fish", "powershell"},
				Man:        true,
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{
					OK:   true,
					Data: data,
					Meta: EnvelopeMeta{RequestID: state.requestID},
				})
			}
			return NewCompactTextRenderer(state.stdout).Record("capabilities", "",
				Field("command", data.Command),
				Field("version", data.Version),
				Field("schema", data.DataSchemaVersion),
				Field("account_mode", data.AccountMode),
				Field("credentials", data.CredentialSchemes),
				Field("network_validation", data.NetworkValidation),
				Field("diagnostics", data.Diagnostics),
				Field("mail_folders", data.MailFolders),
				Field("mail_sync", data.MailSync),
				Field("mail_content", data.MailContent),
				Field("mail_attachments", data.MailAttachments),
				Field("mail_flags", data.MailFlags),
				Field("mail_threads", data.MailThreads),
				Field("mail_search", data.MailSearch),
				Field("mail_remote_search", data.MailRemoteSearch),
				Field("mail_hydration", data.MailHydration),
				Field("mail_send", data.MailSend),
				Field("mail_reply", data.MailReply),
				Field("default_output", data.Output.Default),
				Field("json", data.Output.JSON),
				Field("completion", data.Completion),
				Field("man", data.Man),
			)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return cmd
}

func newAccountCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{
		Use:     "account",
		Short:   "Configure and inspect iCloud Mail accounts",
		GroupID: "account",
		Args:    cobra.NoArgs,
		Example: "  icloud account add --name Personal --email owner@icloud.com --password-stdin\n  icloud account list\n  icloud account show personal",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	command.AddCommand(newAccountAddCommand(state))
	command.AddCommand(newAccountListCommand(state))
	command.AddCommand(newAccountShowCommand(state))
	command.AddCommand(newAccountRenameCommand(state))
	command.AddCommand(newAccountDefaultCommand(state))
	command.AddCommand(newAccountRemoveCommand(state))
	command.AddCommand(newAccountCredentialCommand(state))
	command.AddCommand(newAccountTestCommand(state))
	return command
}

func newAccountListCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	var limit int
	command := &cobra.Command{
		Use:     "list",
		Short:   "List accounts from the local state database",
		Args:    cobra.NoArgs,
		Example: "  icloud account list\n  ICLOUD_HOME=./icloud-state icloud account list --json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			if limit < 1 || limit > MaximumPageSize {
				return NewAppError(ErrorUsage, fmt.Sprintf("--limit must be between 1 and %d", MaximumPageSize), false)
			}
			accounts, more, schemaVersion, err := state.accounts.List(cmd.Context(), state.global.Home, limit)
			if err != nil {
				return err
			}
			data := accountListData{Accounts: accounts, Count: len(accounts), More: more, SchemaVersion: schemaVersion}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{
					OK:   true,
					Data: data,
					Meta: EnvelopeMeta{RequestID: state.requestID},
				})
			}
			renderer := NewCompactTextRenderer(state.stdout)
			for _, value := range accounts {
				if err := renderAccount(renderer, value); err != nil {
					return err
				}
			}
			return renderer.Summary("accounts",
				Field("count", fmt.Sprintf("%d", len(accounts))),
				Field("more", more),
			)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	command.Flags().IntVar(&limit, "limit", DefaultPageSize, fmt.Sprintf("Maximum accounts to return (1-%d)", MaximumPageSize))
	return command
}

func newAccountShowCommand(state *executionState) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:     "show [name|email|account-id]",
		Short:   "Show one account from the local state database",
		Args:    cobra.MaximumNArgs(1),
		Example: "  icloud account show\n  icloud account show personal\n  icloud --account owner@icloud.com account show --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			selector, err := selectedAccountSelector(state, args)
			if err != nil {
				return err
			}
			value, schemaVersion, err := state.accounts.Show(cmd.Context(), state.global.Home, selector)
			if err != nil {
				return err
			}
			if jsonOutput {
				return WriteJSON(state.stdout, OutputEnvelope{
					OK:   true,
					Data: accountShowData{Account: value, SchemaVersion: schemaVersion},
					Meta: EnvelopeMeta{RequestID: state.requestID},
				})
			}
			return renderAccount(NewCompactTextRenderer(state.stdout), value)
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Emit one stable JSON success envelope")
	return command
}

func renderAccount(renderer *CompactTextRenderer, value account.Account) error {
	return renderer.Record("account", value.ID,
		Field("name", value.Name),
		Field("email", value.Email),
		Field("imap_username", value.IMAPUsername),
		Field("status", value.Status),
		Field("default", value.Default),
		Field("credential", value.CredentialScheme),
	)
}

type unavailableAccountService struct{}

func (unavailableAccountService) List(context.Context, string, int) ([]account.Account, bool, int64, error) {
	return nil, false, 0, NewAppError(ErrorConfiguration, "local account state is unavailable", false)
}

func (unavailableAccountService) Show(context.Context, string, string) (account.Account, int64, error) {
	return account.Account{}, 0, NewAppError(ErrorConfiguration, "local account state is unavailable", false)
}

func (unavailableAccountService) Add(context.Context, string, account.AddInput) (account.Account, int64, error) {
	return account.Account{}, 0, NewAppError(ErrorConfiguration, "local account state is unavailable", false)
}

func (unavailableAccountService) Rename(context.Context, string, string, string) (account.Account, int64, error) {
	return account.Account{}, 0, NewAppError(ErrorConfiguration, "local account state is unavailable", false)
}

func (unavailableAccountService) SetDefault(context.Context, string, string) (account.Account, int64, error) {
	return account.Account{}, 0, NewAppError(ErrorConfiguration, "local account state is unavailable", false)
}

func (unavailableAccountService) Remove(context.Context, string, string) (account.Account, bool, int64, error) {
	return account.Account{}, false, 0, NewAppError(ErrorConfiguration, "local account state is unavailable", false)
}

func (unavailableAccountService) CredentialShow(context.Context, string, string) (account.Credential, int64, error) {
	return account.Credential{}, 0, NewAppError(ErrorConfiguration, "local account state is unavailable", false)
}

func (unavailableAccountService) CredentialReplace(context.Context, string, string, account.CredentialSetup) (account.Credential, int64, error) {
	return account.Credential{}, 0, NewAppError(ErrorConfiguration, "local account state is unavailable", false)
}

func (unavailableAccountService) CredentialDelete(context.Context, string, string) (account.Credential, int64, error) {
	return account.Credential{}, 0, NewAppError(ErrorConfiguration, "local account state is unavailable", false)
}

func (unavailableAccountService) Test(context.Context, string, string, bool) ([]account.HealthReport, int64, error) {
	return nil, 0, NewAppError(ErrorConfiguration, "account validation is unavailable", false)
}

func (unavailableAccountService) Doctor(context.Context, string, string, bool, bool) ([]account.DoctorReport, int64, error) {
	return nil, 0, NewAppError(ErrorConfiguration, "account diagnostics are unavailable", false)
}

func newCompletionCommand(root *cobra.Command) *cobra.Command {
	completion := &cobra.Command{
		Use:     "completion",
		Short:   "Generate shell completion scripts",
		GroupID: "docs",
		Args:    cobra.NoArgs,
		Example: "  icloud completion bash > icloud.bash\n  icloud completion zsh > _icloud",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	types := []struct {
		name string
		run  func(io.Writer) error
	}{
		{"bash", root.GenBashCompletion},
		{"zsh", root.GenZshCompletion},
		{"fish", func(w io.Writer) error { return root.GenFishCompletion(w, true) }},
		{"powershell", root.GenPowerShellCompletion},
	}
	for _, shell := range types {
		shell := shell
		completion.AddCommand(&cobra.Command{
			Use:                   shell.name,
			Short:                 fmt.Sprintf("Generate %s completion", shell.name),
			Args:                  cobra.NoArgs,
			DisableFlagsInUseLine: true,
			Example:               fmt.Sprintf("  icloud completion %s > icloud.%s", shell.name, shell.name),
			RunE: func(cmd *cobra.Command, _ []string) error {
				return shell.run(cmd.OutOrStdout())
			},
		})
	}
	return completion
}

func newManCommand(root *cobra.Command, state *executionState) *cobra.Command {
	var outputDir string
	cmd := &cobra.Command{
		Use:     "man",
		Short:   "Generate man pages from the current command tree",
		GroupID: "docs",
		Args:    cobra.NoArgs,
		Example: "  icloud man > icloud.1\n  icloud man --output-dir ./man",
		RunE: func(_ *cobra.Command, _ []string) error {
			date := time.Unix(0, 0).UTC()
			header := &doc.GenManHeader{
				Title:   "ICLOUD",
				Section: "1",
				Date:    &date,
				Source:  "icloud " + state.version,
				Manual:  "iCloud CLI Manual",
			}
			if strings.TrimSpace(outputDir) == "" {
				return doc.GenMan(root, header, state.stdout)
			}
			cleanDir := filepath.Clean(outputDir)
			if err := os.MkdirAll(cleanDir, 0o755); err != nil {
				return WrapAppError(ErrorConfiguration, "cannot create man output directory", false, err)
			}
			if err := doc.GenManTree(root, header, cleanDir); err != nil {
				return WrapAppError(ErrorInternal, "cannot generate man pages", false, err)
			}
			return NewCompactTextRenderer(state.stdout).Record("man", "generated", Field("dir", cleanDir))
		},
	}
	cmd.Flags().StringVar(&outputDir, "output-dir", "", "Write the complete man-page tree to this directory")
	return cmd
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

func validRequestID(value string) bool {
	return value != "" && requestIDPattern.MatchString(value)
}

func requestedID(args []string) string {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if strings.HasPrefix(arg, "--request-id=") {
			return strings.TrimPrefix(arg, "--request-id=")
		}
		if arg == "--request-id" && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func requestedJSON(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--json", "--json=true":
			return true
		case "--json=false":
			return false
		}
	}
	return false
}

func rootCommandToken(args []string) string {
	valueFlags := map[string]struct{}{
		"--account":    {},
		"--home":       {},
		"--request-id": {},
		"--timeout":    {},
	}
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if _, ok := valueFlags[arg]; ok {
			skipNext = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg
	}
	return ""
}

func isKnownRootCommand(root *cobra.Command, token string) bool {
	for _, command := range root.Commands() {
		if command.Name() == token {
			return true
		}
	}
	return false
}

func removeJSONFlag(args []string) []string {
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--json", "--json=true":
			continue
		default:
			filtered = append(filtered, arg)
		}
	}
	return filtered
}
