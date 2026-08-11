package cli

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// ReferenceAssets are deterministic documentation artifacts derived from the
// same Cobra tree used by the public executable.
type ReferenceAssets struct {
	CommandReference []byte
	Completions      map[string][]byte
	ManPages         map[string][]byte
}

// GenerateReferenceAssets renders the public command reference, shell
// completions, and complete man-page tree without opening local state or
// contacting a provider.
func GenerateReferenceAssets(version string, schemaVersion int64) (ReferenceAssets, error) {
	state := &executionState{
		version:       strings.TrimSpace(version),
		schemaVersion: schemaVersion,
		stdin:         strings.NewReader(""),
		stdout:        io.Discard,
		stderr:        io.Discard,
		requestID:     "req_reference",
		accounts:      unavailableAccountService{},
		folders:       unavailableFolderService{},
		mail:          unavailableMailService{},
	}
	if state.version == "" {
		state.version = "dev"
	}
	root := newRootCommand(state)

	reference, err := renderCommandReference(root)
	if err != nil {
		return ReferenceAssets{}, err
	}
	completions, err := renderCompletions(root)
	if err != nil {
		return ReferenceAssets{}, err
	}
	manPages, err := renderManPages(root, state.version)
	if err != nil {
		return ReferenceAssets{}, err
	}
	return ReferenceAssets{CommandReference: reference, Completions: completions, ManPages: manPages}, nil
}

func renderCommandReference(root *cobra.Command) ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("---\n")
	output.WriteString("title: Command reference\n")
	output.WriteString("description: Generated usage, examples, flags, and subcommands for the complete icloud command tree.\n")
	output.WriteString("order: 90\n")
	output.WriteString("category: Reference\n")
	output.WriteString("summary: Exhaustive syntax generated from the same Cobra tree as the installed icloud executable.\n")
	output.WriteString("---\n\n")
	output.WriteString("This reference is generated from the same command tree as `icloud --help`, so syntax and flags stay aligned with the executable. For task-focused guidance, start with [Quickstart](/docs/quickstart) or choose a guide from the docs index.\n\n")

	commands := flattenVisibleCommands(root)
	for _, command := range commands {
		var help bytes.Buffer
		command.SetOut(&help)
		command.SetErr(&help)
		if err := command.Help(); err != nil {
			return nil, fmt.Errorf("render help for %s: %w", command.CommandPath(), err)
		}
		output.WriteString("## `")
		output.WriteString(command.CommandPath())
		output.WriteString("`\n\n")
		output.WriteString("```text\n")
		output.WriteString(strings.TrimSpace(help.String()))
		output.WriteString("\n```\n\n")
	}
	return append(bytes.TrimRight(output.Bytes(), "\n"), '\n'), nil
}

func flattenVisibleCommands(root *cobra.Command) []*cobra.Command {
	result := make([]*cobra.Command, 0, 64)
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		if command.Hidden {
			return
		}
		result = append(result, command)
		children := append([]*cobra.Command(nil), command.Commands()...)
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, child := range children {
			walk(child)
		}
	}
	walk(root)
	return result
}

func renderCompletions(root *cobra.Command) (map[string][]byte, error) {
	renderers := map[string]func(io.Writer) error{
		"_icloud":     root.GenZshCompletion,
		"icloud.bash": root.GenBashCompletion,
		"icloud.fish": func(writer io.Writer) error { return root.GenFishCompletion(writer, true) },
		"icloud.ps1":  root.GenPowerShellCompletion,
	}
	result := make(map[string][]byte, len(renderers))
	for name, render := range renderers {
		var output bytes.Buffer
		if err := render(&output); err != nil {
			return nil, fmt.Errorf("generate %s completion: %w", name, err)
		}
		result[name] = append([]byte(nil), output.Bytes()...)
	}
	return result, nil
}

func renderManPages(root *cobra.Command, version string) (map[string][]byte, error) {
	date := time.Unix(0, 0).UTC()
	header := &doc.GenManHeader{
		Title:   "ICLOUD",
		Section: "1",
		Date:    &date,
		Source:  "icloud " + version,
		Manual:  "iCloud Mail CLI Manual",
	}
	result := make(map[string][]byte)
	for _, command := range flattenVisibleCommands(root) {
		if command != root && (!command.IsAvailableCommand() || command.IsAdditionalHelpTopicCommand()) {
			continue
		}
		var output bytes.Buffer
		if err := doc.GenMan(command, header, &output); err != nil {
			return nil, fmt.Errorf("generate man page for %s: %w", command.CommandPath(), err)
		}
		name := strings.ReplaceAll(command.CommandPath(), " ", "-") + ".1"
		result[name] = append([]byte(nil), output.Bytes()...)
	}
	return result, nil
}
