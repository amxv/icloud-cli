package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

const commandName = "icloud"

type Config struct {
	Version       string
	SchemaVersion int64
	Stdout        io.Writer
	Stderr        io.Writer
	RequestIDGen  func() string
	Accounts      AccountService
}

// AccountService is the application-owned boundary used by account commands.
// Implementations may initialize durable state lazily from the selected home.
type AccountService interface {
	List(context.Context, string, int) ([]account.Account, bool, int64, error)
	Show(context.Context, string, string) (account.Account, int64, error)
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
	stdout        io.Writer
	stderr        io.Writer
	requestID     string
	global        globalOptions
	accounts      AccountService
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
	if config.Stderr == nil {
		config.Stderr = io.Discard
	}
	if config.RequestIDGen == nil {
		config.RequestIDGen = NewRequestID
	}
	if config.Accounts == nil {
		config.Accounts = unavailableAccountService{}
	}

	requestID := requestedID(args)
	if !validRequestID(requestID) {
		requestID = config.RequestIDGen()
	}
	state := &executionState{
		version:       strings.TrimSpace(config.Version),
		schemaVersion: config.SchemaVersion,
		stdout:        config.Stdout,
		stderr:        config.Stderr,
		requestID:     requestID,
		accounts:      config.Accounts,
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
		Long:          "icloud controls iCloud services from the command line. This build provides local account-state inspection, version and capability reporting, shell completions, and man-page generation.",
		Example:       "  icloud account list\n  icloud version\n  icloud capabilities\n  icloud completion zsh > _icloud",
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
	root.SetVersionTemplate("version {{.Version}}\n")

	root.PersistentFlags().StringVar(&state.global.Home, "home", "", "Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)")
	root.PersistentFlags().StringVar(&state.global.Account, "account", "", "Select an account by name, email, or stable ID")
	root.PersistentFlags().DurationVar(&state.global.Timeout, "timeout", 30*time.Second, "Maximum command duration")
	root.PersistentFlags().BoolVar(&state.global.NoInput, "no-input", false, "Never prompt for interactive input")
	root.PersistentFlags().StringVar(&state.global.RequestID, "request-id", "", "Attach a stable request ID to structured output")

	root.AddGroup(
		&cobra.Group{ID: "core", Title: "Core commands:"},
		&cobra.Group{ID: "account", Title: "Account commands:"},
		&cobra.Group{ID: "docs", Title: "Documentation commands:"},
	)
	root.AddCommand(newAccountCommand(state))
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
		Short:   "Inspect configured iCloud Mail accounts",
		GroupID: "account",
		Args:    cobra.NoArgs,
		Example: "  icloud account list\n  icloud account show personal",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	command.AddCommand(newAccountListCommand(state))
	command.AddCommand(newAccountShowCommand(state))
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
		Use:     "show <name|email|account-id>",
		Short:   "Show one account from the local state database",
		Args:    cobra.ExactArgs(1),
		Example: "  icloud account show personal\n  icloud account show acct_0198f000-0000-7000-8000-000000000001 --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ValidateOutputOptions(OutputOptions{JSON: jsonOutput}, OutputSupport{JSON: true}); err != nil {
				return err
			}
			value, schemaVersion, err := state.accounts.Show(cmd.Context(), state.global.Home, args[0])
			if err != nil {
				switch {
				case errors.Is(err, account.ErrNotFound):
					return NewAppError(ErrorNotFound, "account not found", false)
				case errors.Is(err, account.ErrAmbiguous):
					return NewAppError(ErrorConflict, "account selector is ambiguous", false)
				default:
					return err
				}
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
