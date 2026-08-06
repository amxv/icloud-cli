package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amxv/icloud-cli/internal/folder"
)

func TestFolderRetentionRiskKeepsRiskySemanticKindsVisible(t *testing.T) {
	for kind, want := range map[folder.Kind]string{
		folder.KindTrash:  "provider_auto_delete_possible",
		folder.KindJunk:   "provider_auto_delete_possible",
		folder.KindDrafts: "provider_mutable_draft",
		folder.KindInbox:  "",
		folder.KindOther:  "",
	} {
		if got := folderRetentionRisk(kind); got != want {
			t.Fatalf("kind %q retention risk = %q, want %q", kind, got, want)
		}
	}
}

func execute(t *testing.T, args ...string) (Result, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	result := Execute(context.Background(), args, Config{
		Version:      "1.2.3",
		Stdout:       &stdout,
		Stderr:       &stderr,
		RequestIDGen: func() string { return "req_generated" },
	})
	return result, stdout.String(), stderr.String()
}

func TestVersionFlagUsesCompactOutput(t *testing.T) {
	result, stdout, stderr := execute(t, "--version")
	if result.ExitCode != 0 || stdout != "version\n1.2.3\n" || stderr != "" {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
}

func TestCompletionGenerationForEveryShell(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		result, stdout, stderr := execute(t, "completion", shell)
		if result.ExitCode != 0 {
			t.Fatalf("%s exit=%d stderr=%q", shell, result.ExitCode, stderr)
		}
		if len(stdout) < 200 || !strings.Contains(strings.ToLower(stdout), "icloud") {
			t.Fatalf("%s completion looks incomplete: %q", shell, stdout)
		}
		if stderr != "" {
			t.Fatalf("%s stderr=%q", shell, stderr)
		}
	}
}

func TestManGenerationToStdoutAndDirectory(t *testing.T) {
	result, stdout, stderr := execute(t, "man")
	if result.ExitCode != 0 || stderr != "" {
		t.Fatalf("result=%+v stderr=%q", result, stderr)
	}
	if !strings.Contains(stdout, ".TH \"ICLOUD\" \"1\"") || !strings.Contains(stdout, "icloud-capabilities(1)") {
		t.Fatalf("unexpected man output: %q", stdout)
	}
	first := stdout
	result, stdout, stderr = execute(t, "man")
	if result.ExitCode != 0 || stderr != "" || stdout != first {
		t.Fatalf("man output is not deterministic: result=%+v stderr=%q", result, stderr)
	}

	directory := t.TempDir()
	result, stdout, stderr = execute(t, "man", "--output-dir", directory)
	if result.ExitCode != 0 || stderr != "" {
		t.Fatalf("result=%+v stderr=%q", result, stderr)
	}
	if !strings.Contains(stdout, "man\ngenerated dir=") {
		t.Fatalf("unexpected summary: %q", stdout)
	}
	matches, err := filepath.Glob(filepath.Join(directory, "*.1"))
	if err != nil || len(matches) < 4 {
		t.Fatalf("generated man pages = %v, err=%v", matches, err)
	}
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			t.Fatalf("invalid man page %s: info=%v err=%v", path, info, err)
		}
	}
}

func TestInvalidTimeoutAndRequestIDAreUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"--timeout", "0s", "version"},
		{"--request-id", "bad id", "version"},
	} {
		result, stdout, stderr := execute(t, args...)
		if result.ExitCode != ExitUsage || stdout != "" || !strings.Contains(stderr, "code=usage") {
			t.Fatalf("args=%v result=%+v stdout=%q stderr=%q", args, result, stdout, stderr)
		}
	}
}

func TestJSONUnknownCommandKeepsSuggestion(t *testing.T) {
	result, stdout, stderr := execute(t, "--request-id", "req_error", "versoin", "--json")
	if result.ExitCode != ExitUsage || stdout != "" {
		t.Fatalf("result=%+v stdout=%q stderr=%q", result, stdout, stderr)
	}
	for _, want := range []string{`"code":"usage"`, "unknown command", "did you mean", "version"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q: %q", want, stderr)
		}
	}
}
