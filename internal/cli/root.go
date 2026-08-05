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

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

const commandName = "icloud"

type Config struct {
	Version      string
	Stdout       io.Writer
	Stderr       io.Writer
	RequestIDGen func() string
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
	version   string
	stdout    io.Writer
	stderr    io.Writer
	requestID string
	global    globalOptions
	cancel    context.CancelFunc
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
	Command    string           `json:"command"`
	Version    string           `json:"version"`
	Output     capabilityOutput `json:"output"`
	Completion []string         `json:"completion"`
	Man        bool             `json:"man"`
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

	requestID := requestedID(args)
	if !validRequestID(requestID) {
		requestID = config.RequestIDGen()
	}
	state := &executionState{
		version:   strings.TrimSpace(config.Version),
		stdout:    config.Stdout,
		stderr:    config.Stderr,
		requestID: requestID,
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
		Long:          "icloud controls iCloud services from the command line. This build provides command discovery, version and capability inspection, shell completions, and man-page generation.",
		Example:       "  icloud version\n  icloud capabilities\n  icloud completion zsh > _icloud",
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

	root.PersistentFlags().StringVar(&state.global.Home, "home", "", "Override the iCloud application data directory")
	root.PersistentFlags().StringVar(&state.global.Account, "account", "", "Select an account by name, email, or stable ID")
	root.PersistentFlags().DurationVar(&state.global.Timeout, "timeout", 30*time.Second, "Maximum command duration")
	root.PersistentFlags().BoolVar(&state.global.NoInput, "no-input", false, "Never prompt for interactive input")
	root.PersistentFlags().StringVar(&state.global.RequestID, "request-id", "", "Attach a stable request ID to structured output")

	root.AddGroup(
		&cobra.Group{ID: "core", Title: "Core commands:"},
		&cobra.Group{ID: "docs", Title: "Documentation commands:"},
	)
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
				Command: commandName,
				Version: state.version,
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
