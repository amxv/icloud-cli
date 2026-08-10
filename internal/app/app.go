package app

import (
	"context"
	"io"
	"strings"

	"github.com/amxv/icloud-cli/internal/buildinfo"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
	transporticloud "github.com/amxv/icloud-cli/internal/transport/icloud"
)

// Run composes the application command tree and returns a process exit code.
// Process startup and os.Exit remain owned by cmd/icloud.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return RunWithInput(ctx, args, strings.NewReader(""), stdout, stderr)
}

// RunWithInput composes the command tree with an explicit stdin source. The
// public process uses os.Stdin; tests and embedded callers can provide a
// bounded non-interactive reader.
func RunWithInput(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	protocolClient := transporticloud.New(transporticloud.Config{})
	return runWithAccounts(ctx, args, stdin, stdout, stderr, accountCommands{
		imap:        protocolClient,
		smtp:        protocolClient,
		sender:      protocolClient,
		folders:     protocolClient,
		folderAdmin: protocolClient,
		mail:        protocolClient,
		remote:      protocolClient,
		flags:       protocolClient,
		locations:   protocolClient,
		unsubscribe: transport.NewHTTPUnsubscriber(),
	})
}

func runWithAccounts(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, accounts cli.AccountService) int {
	var folders cli.FolderService
	if candidate, ok := accounts.(cli.FolderService); ok {
		folders = candidate
	}
	var mailService cli.MailService
	if candidate, ok := accounts.(cli.MailService); ok {
		mailService = candidate
	}
	result := cli.Execute(ctx, args, cli.Config{
		Version:       buildinfo.CurrentVersion(),
		SchemaVersion: store.SchemaVersion,
		Stdin:         stdin,
		Stdout:        stdout,
		Stderr:        stderr,
		Accounts:      accounts,
		Folders:       folders,
		Mail:          mailService,
	})
	return result.ExitCode
}
