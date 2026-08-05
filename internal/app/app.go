package app

import (
	"context"
	"io"

	"github.com/amxv/icloud-cli/internal/buildinfo"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/store"
)

// Run composes the application command tree and returns a process exit code.
// Process startup and os.Exit remain owned by cmd/icloud.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	result := cli.Execute(ctx, args, cli.Config{
		Version:       buildinfo.CurrentVersion(),
		SchemaVersion: store.SchemaVersion,
		Stdout:        stdout,
		Stderr:        stderr,
		Accounts:      accountCommands{},
	})
	return result.ExitCode
}
