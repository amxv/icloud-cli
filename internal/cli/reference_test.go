package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestGeneratedReferenceAssetsAreDeterministicAndComplete(t *testing.T) {
	first, err := GenerateReferenceAssets("1.2.3", 18)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateReferenceAssets("1.2.3", 18)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.CommandReference, second.CommandReference) {
		t.Fatal("generated command reference is not deterministic")
	}
	if len(first.Completions) != 4 || len(first.ManPages) < 60 {
		t.Fatalf("completions=%d man-pages=%d", len(first.Completions), len(first.ManPages))
	}
	if _, exists := first.ManPages["icloud-help.1"]; exists {
		t.Fatal("generated man tree should match Cobra GenManTree and omit the synthetic help command")
	}
	for _, want := range []string{
		"## `icloud mail draft send`",
		"## `icloud mail sync repair`",
		"## `icloud mail messages list`",
		"## `icloud mail threads list`",
		"## `icloud mail threads show`",
		"## `icloud mail folders refresh`",
		"## `icloud mail operations outbound list`",
		"## `icloud mail operations outbound show`",
		"## `icloud mail operations mutations list`",
		"## `icloud mail operations mutations show`",
		"## `icloud mail inbox`",
		"## `icloud mail latest`",
		"## `icloud mail thread show`",
		"## `icloud mail folders status`",
		"## `icloud mail status`",
		"--input-json",
		"--jsonl",
		"--body-only",
		"--verbose",
	} {
		if !strings.Contains(string(first.CommandReference), want) {
			t.Fatalf("generated command reference missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(string(first.CommandReference)), "icloud services") {
		t.Fatal("generated command reference overclaims non-Mail iCloud services")
	}
	rootMan := string(first.ManPages["icloud.1"])
	if !strings.Contains(rootMan, "icloud 1.2.3") || !strings.Contains(rootMan, "iCloud Mail CLI Manual") {
		t.Fatalf("root man page lacks version/product metadata: %q", rootMan[:min(len(rootMan), 1000)])
	}
}

func TestEveryRunnableVisibleLeafCommandHasCommandLocalExample(t *testing.T) {
	state := &executionState{version: "test", schemaVersion: 18, stdin: strings.NewReader(""), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accounts: unavailableAccountService{}, folders: unavailableFolderService{}, mail: unavailableMailService{}}
	root := newRootCommand(state)
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		if command.Hidden {
			return
		}
		visibleChildren := 0
		for _, child := range command.Commands() {
			if child.Hidden {
				continue
			}
			visibleChildren++
			walk(child)
		}
		if command.Name() != "help" && visibleChildren == 0 && (command.Run != nil || command.RunE != nil) && strings.TrimSpace(command.Example) == "" {
			t.Errorf("runnable leaf %q has no command-local example", command.CommandPath())
		}
	}
	walk(root)
}
