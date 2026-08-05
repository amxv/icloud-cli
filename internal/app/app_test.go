package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), args, &stdout, &stderr)
	return exitCode, stdout.String(), stderr.String()
}

func TestRootHelpUsesICloudIdentityAndGroups(t *testing.T) {
	exitCode, stdout, stderr := run(t, "--help")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	for _, want := range []string{
		"icloud controls iCloud services from the command line.",
		"Core commands:",
		"Documentation commands:",
		"capabilities",
		"completion",
		"man",
		"version",
		"--no-input",
		"--timeout",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("help missing %q:\n%s", want, stdout)
		}
	}
	for _, unwanted := range []string{"icloud-cli", "hello", "template"} {
		if strings.Contains(stdout, unwanted) {
			t.Fatalf("help unexpectedly contains %q:\n%s", unwanted, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("help wrote stderr: %q", stderr)
	}
}

func TestVersionCompactDefault(t *testing.T) {
	exitCode, stdout, stderr := run(t, "version")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	if stdout != "version dev\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestVersionJSONEnvelope(t *testing.T) {
	exitCode, stdout, stderr := run(t, "--request-id", "req_test", "version", "--json")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Command string `json:"command"`
			Version string `json:"version"`
		} `json:"data"`
		Meta struct {
			RequestID string `json:"request_id"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if !envelope.OK || envelope.Data.Command != "icloud" || envelope.Data.Version != "dev" {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	if envelope.Meta.RequestID != "req_test" {
		t.Fatalf("request id = %q", envelope.Meta.RequestID)
	}
}

func TestCapabilitiesCompactGoldenBudget(t *testing.T) {
	exitCode, stdout, stderr := run(t, "capabilities")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	const want = "capabilities command=icloud version=dev default_output=compact json=true completion=bash,zsh,fish,powershell man=true\n"
	if stdout != want {
		t.Fatalf("stdout mismatch\nwant: %q\n got: %q", want, stdout)
	}
	if lines := strings.Count(stdout, "\n"); lines > 1 {
		t.Fatalf("line budget exceeded: %d", lines)
	}
	if len(stdout) > 160 {
		t.Fatalf("byte budget exceeded: %d", len(stdout))
	}
	for _, unwanted := range []string{"{", "}", "\x1b[", "│", "Command:", "Version:"} {
		if strings.Contains(stdout, unwanted) {
			t.Fatalf("compact output contains %q: %q", unwanted, stdout)
		}
	}
}

func TestCapabilitiesJSONEnvelope(t *testing.T) {
	exitCode, stdout, stderr := run(t, "--request-id=req_caps", "capabilities", "--json")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if envelope["ok"] != true {
		t.Fatalf("ok = %#v", envelope["ok"])
	}
	if strings.Contains(stdout, "\x1b[") {
		t.Fatalf("JSON contains ANSI: %q", stdout)
	}
}

func TestUnknownCommandIsCompactUsageErrorWithSuggestion(t *testing.T) {
	exitCode, stdout, stderr := run(t, "versoin")
	if exitCode != 2 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	for _, want := range []string{"error", "code=usage", "versoin", "version"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q: %q", want, stderr)
		}
	}
	if strings.Count(stderr, "\n") != 1 {
		t.Fatalf("error line budget exceeded: %q", stderr)
	}
}

func TestUnknownCommandJSONErrorEnvelope(t *testing.T) {
	exitCode, stdout, stderr := run(t, "--request-id", "req_error", "versoin", "--json")
	if exitCode != 2 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
		Meta struct {
			RequestID string `json:"request_id"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(stderr), &envelope); err != nil {
		t.Fatalf("invalid JSON error: %v\n%s", err, stderr)
	}
	if envelope.OK || envelope.Error.Code != "usage" || envelope.Error.Retryable {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	if !strings.Contains(stderr, "did you mean") || !strings.Contains(stderr, "version") {
		t.Fatalf("JSON error lost the command suggestion: %q", stderr)
	}
	if envelope.Meta.RequestID != "req_error" {
		t.Fatalf("request id = %q", envelope.Meta.RequestID)
	}
}

func TestHelpWritesStdoutAndFlagErrorsWriteStderr(t *testing.T) {
	exitCode, stdout, stderr := run(t, "version", "--help")
	if exitCode != 0 || !strings.Contains(stdout, "Usage:") || stderr != "" {
		t.Fatalf("help result code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = run(t, "version", "--unsupported")
	if exitCode != 2 || stdout != "" || !strings.Contains(stderr, "code=usage") {
		t.Fatalf("flag result code=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestNoColorDoesNotChangeCompactOutput(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	exitCode, stdout, stderr := run(t, "capabilities")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", exitCode, stderr)
	}
	if strings.Contains(stdout, "\x1b[") {
		t.Fatalf("output contains ANSI: %q", stdout)
	}
}
