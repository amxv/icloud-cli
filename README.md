# icloud

`icloud` is a local-first command-line client for iCloud services. The project is being built for agent workflows without making the normal terminal experience noisy: compact plain text is the default, and structured or expanded output is always explicit.

The current implementation provides secure multi-account and credential lifecycle, read-only live iCloud compatibility testing, durable all-folder discovery, bounded local Inbox synchronization, complete local message reads, and safe inbound attachment export. It can validate IMAP/SMTP authentication, discover nested and Unicode mailboxes, stream raw Inbox messages into private local state, show compact decoded-text excerpts, explicitly return complete text or HTML, inspect decoded headers and MIME metadata, export exact RFC822 bytes, and save verified decoded attachments without trusting message filenames as paths. It does not send mail or mutate remote mailbox state.

## Install

```bash
npm install --global @amxv/icloud-cli
icloud version
icloud capabilities
```

Build from source:

```bash
make check
make build
./dist/icloud --help
```

## Current commands

```bash
icloud account add --name <name> --email <address> [--password-stdin|--credential-env <name>|--credential-file <path>]
icloud account list [--json]
icloud account show [name|email|account-id] [--json]
icloud account rename <selector> <new-name> [--json]
icloud account default <selector> [--json]
icloud account remove <selector> --yes [--json]
icloud account credential show <selector> [--json]
icloud account credential replace <selector> [--password-stdin|--credential-env <name>|--credential-file <path>] [--json]
icloud account credential delete <selector> --yes [--json]
icloud account test [selector] [--all] [--verbose] [--json]
icloud mail folders list [--include-missing] [--verbose] [--json]
icloud mail folders show <selector> [--verbose] [--json]
icloud mail folders status [--verbose] [--json]
icloud mail folders enable <selector> [--json]
icloud mail folders disable <selector> [--json]
icloud mail sync [--limit <n>] [--json]
icloud mail inbox [--limit <n>] [--json]
icloud mail sync status [--json]
icloud mail sync checkpoints [--limit <n>] [--json]
icloud mail message show <message-id> [--full] [--format compact|text|html|raw] [--headers] [--body-only] [--metadata-only] [--verbose] [--json]
icloud mail message raw <message-id>
icloud mail message export <message-id> --output <path|-> [--force]
icloud mail attachments list <message-id> [--limit <n>] [--json]
icloud mail attachments save <attachment-id> --output <path|-> [--binary] [--force] [--json]
icloud mail attachments save-all <message-id> --dir <directory> [--force] [--json]
icloud doctor [selector] [--all] [--live] [--verbose] [--json]
icloud version [--json]
icloud capabilities [--json]
icloud completion bash|zsh|fish|powershell
icloud man [--output-dir <directory>]
icloud help [command]
```

Representative default output:

```text
account-test
acct_… status=connected
imap=ok
smtp=ok
imap_username=owner
mailboxes=6
inbox_messages=42
inbox_unseen=3

folder-status
account=acct_…
count=8
present=8
stale=0
enabled=2

mail-sync
fld_… account=acct_…
folder=INBOX
fetched=20
created=20
updated=0
last_uid=184
more=false
bootstrap=true

inbox
account=acct_…
count=20
more=false

doctor
accounts=1
status=healthy

version
0.1.0

capabilities
command=icloud
version=0.1.0
schema=6
account_mode=validated_lifecycle
credentials=keyring,env,file
network_validation=true
diagnostics=true
mail_folders=true
mail_sync=true
mail_content=true
mail_attachments=true
default_output=compact
json=true
completion=bash,zsh,fish,powershell
man=true
```

Redirected and non-interactive execution uses the same compact default. Use `--json` only on commands that advertise it. For isolated state, set `ICLOUD_HOME=./icloud-state` or pass `--home ./icloud-state`. Use an app-specific password, never the primary Apple Account password; pass it through a secure prompt or `--password-stdin`, not argv.

## Development

Prerequisites: Go 1.26+, Node.js 18+, and Bun 1+ for the docs site.

```bash
make fmt
make test
make vet
make lint
make check
make build-all
bun run docs:check
bun run docs:build
```

Run the docs commands serially.

## Project layout

- `cmd/icloud`: process startup and final exit behavior
- `internal/app`: dependency composition and execution entry
- `internal/account`, `internal/folder`, `internal/config`, `internal/id`, `internal/secret`, and `internal/store`: account/folder vocabulary, paths, identifiers, credential boundaries, and persistence
- `internal/transport`: narrow redacted protocol contracts and the isolated iCloud IMAP/SMTP compatibility client
- `internal/cli`: Cobra tree, compact rendering, JSON/JSONL primitives, help, errors, completion, and man generation
- `internal/buildinfo`: linker-injected version metadata
- `bin/icloud.js`: npm executable shim
- `scripts/postinstall.js`: release-asset installer with local Go fallback
- `.github/workflows/release.yml`: cross-platform release and npm publication
- `src/content/docs`: user-facing guides
- `gg`: implementation plan, sweep, and progress ledger

## Documentation

The hosted documentation is at `https://icloud.ashray.xyz`. Local docs development:

```bash
bun install
bun run docs:dev
```

## License

Apache 2.0
