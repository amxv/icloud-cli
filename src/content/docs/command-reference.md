---
title: Command reference
description: Generated usage, examples, flags, and subcommands for the complete icloud command tree.
order: 11
category: Reference
summary: Exhaustive syntax generated from the same Cobra tree as the installed icloud executable.
---

This reference is generated from the same command tree as `icloud --help`, so syntax and flags stay aligned with the executable. For task-focused guidance, start with [Quickstart](/docs/quickstart) or choose a guide from the docs index.

## `icloud`

```text
icloud is a local-first iCloud Mail CLI for account setup, validation, folder and synchronization workflows, offline and bounded remote search, complete message and attachment reads, drafts, deliberate send/reply/forward operations, message-state and location changes, recovery diagnostics, automation-safe JSON/JSONL, shell completion, and man-page generation.

Usage:
  icloud [flags]
  icloud [command]

Examples:
  icloud account add --name Personal --email owner@icloud.com --password-stdin
  icloud account test
  icloud mail send --to client@example.com --subject Update --body-file message.txt --dry-run
  icloud mail reply msg_… --body-file reply.txt --dry-run
  icloud mail forward msg_… --to teammate@example.com --dry-run
  icloud mail move msg_… --from inbox --to Projects --dry-run
  icloud mail sync
  icloud mail message show msg_…
  icloud doctor

Core commands:
  capabilities Describe capabilities in this installed build
  doctor       Diagnose local account configuration and optional live connectivity
  version      Print the installed CLI version

Account commands:
  account      Configure and inspect iCloud Mail accounts

Mail commands:
  mail         Send, reply, forward, inspect, synchronize, and change exact iCloud Mail locations

Documentation commands:
  completion   Generate shell completion scripts
  help         Help about any command
  man          Generate man pages from the current command tree

Flags:
      --account string      Select an account by name, email, or stable ID
  -h, --help                help for icloud
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
  -v, --version             version for icloud

Use "icloud [command] --help" for more information about a command.
```

## `icloud account`

```text
Configure and inspect iCloud Mail accounts

Usage:
  icloud account [flags]
  icloud account [command]

Examples:
  icloud account add --name Personal --email owner@icloud.com --password-stdin
  icloud account list
  icloud account show personal

Available Commands:
  add         Add an offline iCloud Mail account profile
  credential  Inspect or change account credential metadata
  default     Set the default account
  list        List accounts from the local state database
  remove      Remove an account profile and its native keyring entry
  rename      Rename an account profile
  show        Show one account from the local state database
  test        Validate credentials against iCloud IMAP and SMTP without sending mail

Flags:
  -h, --help   help for account

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud account [command] --help" for more information about a command.
```

## `icloud account add`

```text
Add an offline iCloud Mail account profile

Usage:
  icloud account add [flags]

Examples:
  printf '%s\n' "$APP_PASSWORD" | icloud account add --name Personal --email owner@icloud.com --password-stdin
  icloud account add --name Agent --email agent@icloud.com --credential-env ICLOUD_AGENT_PASSWORD

Flags:
      --credential-env string    Persist an environment-variable credential reference
      --credential-file string   Persist a credential-file reference
      --default                  Make this the default account
      --email string             iCloud Mail address
  -h, --help                     help for add
      --imap-username string     IMAP username (defaults to the email address)
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                     Emit one stable JSON success envelope
      --name string              Friendly account name
      --password-stdin           Read an app-specific password from stdin and store it in the native keyring

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud account credential`

```text
Inspect or change account credential metadata

Usage:
  icloud account credential [flags]
  icloud account credential [command]

Examples:
  icloud account credential show Personal
  icloud account credential replace Personal --password-stdin

Available Commands:
  delete      Delete an account credential reference and native keyring entry
  replace     Replace an account credential and return it to pending validation
  show        Show credential metadata without resolving or revealing the value

Flags:
  -h, --help   help for credential

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud account credential [command] --help" for more information about a command.
```

## `icloud account credential delete`

```text
Delete an account credential reference and native keyring entry

Usage:
  icloud account credential delete <selector> [flags]

Examples:
  icloud account credential delete Personal --yes

Flags:
  -h, --help                help for delete
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --yes                 Authorize credential deletion without prompting

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud account credential replace`

```text
Replace an account credential and return it to pending validation

Usage:
  icloud account credential replace <selector> [flags]

Examples:
  printf '%s\n' "$NEW_PASSWORD" | icloud account credential replace Personal --password-stdin

Flags:
      --credential-env string    Persist an environment-variable credential reference
      --credential-file string   Persist a credential-file reference
  -h, --help                     help for replace
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                     Emit one stable JSON success envelope
      --password-stdin           Read an app-specific password from stdin and store it in the native keyring

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud account credential show`

```text
Show credential metadata without resolving or revealing the value

Usage:
  icloud account credential show <selector> [flags]

Examples:
  icloud account credential show Personal

Flags:
  -h, --help   help for show
      --json   Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud account default`

```text
Set the default account

Usage:
  icloud account default <selector> [flags]

Examples:
  icloud account default Work

Flags:
  -h, --help                help for default
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud account list`

```text
List accounts from the local state database

Usage:
  icloud account list [flags]

Examples:
  icloud account list
  ICLOUD_HOME=./icloud-state icloud account list --json
  icloud account list --input-json request.json --json

Flags:
      --cursor string       Continue from a next_cursor returned by account list
  -h, --help                help for list
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum accounts to return (1-100) (default 20)

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud account remove`

```text
Remove an account profile and its native keyring entry

Usage:
  icloud account remove <selector> [flags]

Examples:
  icloud account remove Old --yes

Flags:
  -h, --help                help for remove
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --yes                 Authorize removal without prompting

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud account rename`

```text
Rename an account profile

Usage:
  icloud account rename <selector> <new-name> [flags]

Examples:
  icloud account rename Personal Primary

Flags:
  -h, --help                help for rename
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud account show`

```text
Show one account from the local state database

Usage:
  icloud account show [name|email|account-id] [flags]

Examples:
  icloud account show
  icloud account show personal
  icloud --account owner@icloud.com account show --json

Flags:
  -h, --help   help for show
      --json   Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud account test`

```text
Validate credentials against iCloud IMAP and SMTP without sending mail

Usage:
  icloud account test [selector] [flags]

Examples:
  icloud account test
  icloud account test Personal
  icloud account test --all --json

Flags:
      --all       Validate every configured account (maximum 100)
  -h, --help      help for test
      --json      Emit one stable JSON success envelope
      --verbose   Include bounded protocol capability summaries

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud capabilities`

```text
Describe capabilities in this installed build

Usage:
  icloud capabilities [flags]

Examples:
  icloud capabilities
  icloud capabilities --json

Flags:
  -h, --help   help for capabilities
      --json   Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud completion`

```text
Generate shell completion scripts

Usage:
  icloud completion [flags]
  icloud completion [command]

Examples:
  icloud completion bash > icloud.bash
  icloud completion zsh > _icloud

Available Commands:
  bash        Generate bash completion
  fish        Generate fish completion
  powershell  Generate powershell completion
  zsh         Generate zsh completion

Flags:
  -h, --help   help for completion

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud completion [command] --help" for more information about a command.
```

## `icloud completion bash`

```text
Generate bash completion

Usage:
  icloud completion bash

Examples:
  icloud completion bash > icloud.bash

Flags:
  -h, --help   help for bash

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud completion fish`

```text
Generate fish completion

Usage:
  icloud completion fish

Examples:
  icloud completion fish > icloud.fish

Flags:
  -h, --help   help for fish

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud completion powershell`

```text
Generate powershell completion

Usage:
  icloud completion powershell

Examples:
  icloud completion powershell > icloud.powershell

Flags:
  -h, --help   help for powershell

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud completion zsh`

```text
Generate zsh completion

Usage:
  icloud completion zsh

Examples:
  icloud completion zsh > icloud.zsh

Flags:
  -h, --help   help for zsh

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud doctor`

```text
Diagnose local account configuration and optional live connectivity

Usage:
  icloud doctor [selector] [flags]

Examples:
  icloud doctor
  icloud doctor Personal
  icloud doctor --live --json

Flags:
      --all       Diagnose every configured account (maximum 100)
  -h, --help      help for doctor
      --json      Emit one stable JSON success envelope
      --live      Resolve credentials and run read-only IMAP/SMTP validation
      --verbose   Include bounded protocol capability summaries

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud help`

```text
Help provides help for any command in the application.
Simply type icloud help [path to command] for full details.

Usage:
  icloud help [command] [flags]

Flags:
  -h, --help   help for help

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail`

```text
Send, reply, forward, inspect, synchronize, and change exact iCloud Mail locations

Usage:
  icloud mail [flags]
  icloud mail [command]

Examples:
  icloud mail send --to client@example.com --subject Update --body-file message.txt --dry-run
  icloud mail reply msg_… --body-file reply.txt --dry-run
  icloud mail forward msg_… --to teammate@example.com --dry-run
  icloud mail move msg_… --from inbox --to Projects --dry-run
  icloud mail sync
  icloud mail messages list
  icloud mail message show msg_…
  icloud mail folders refresh

Available Commands:
  archive     Move eligible non-Sent locations to the semantic Archive folder
  attachments List and safely export decoded local attachments
  copy        Copy exact current message locations
  delete      Permanently delete exact current provider locations
  draft       Compose, synchronize, inspect, send, and delete iCloud-backed drafts
  empty       Permanently delete reviewed bounded pages from Trash, Junk, or one named custom folder
  flag        Flag one selected message location
  folders     Discover folders, inspect provider status, and choose sync policy
  forward     Forward one explicitly selected local message as a new conversation
  identities  Manage local Send From identities for the selected mail account
  inbox       Compatibility alias for `mail messages list`
  junk        Move exact current message locations to the semantic Junk folder
  latest      Compatibility alias for `mail threads list`
  mailbox     Import or export complete mailbox archives
  mark-read   Mark one selected message location as read
  mark-unread Mark one selected message location as unread
  message     Read, export, or deliberately import complete RFC messages
  messages    Read synchronized messages through the canonical message-list surface
  move        Move exact current message locations
  not-junk    Move current Junk locations back to Inbox
  operations  Inspect local mail operation history by stable ID
  outbox      Inspect and manually retry sends proven safe before SMTP DATA
  reply       Reply to one explicitly selected local message
  reply-all   Reply to all visible participants of one explicitly selected local message
  restore     Move current Trash locations to deliberate destinations
  search      Search synchronized mail locally or query bounded provider metadata explicitly
  selection   Create and apply short-lived immutable reviewed mail target sets
  send        Preview or send text, HTML, and attachments safely and idempotently
  senders     Manage local VIP and blocked sender preferences
  settings    Inspect local Mail settings and truthful provider compatibility
  signatures  Manage local per-identity compose signatures
  status      Compatibility alias for `mail operations … list`
  sync        Fetch and atomically commit bounded pages from enabled mail folders
  thread      Compatibility namespace for `mail threads show`
  threads     List, read, and change locally reconstructed conversations
  trash       Move exact current locations to the semantic Trash folder
  unarchive   Move current Archive locations to Inbox
  unflag      Remove the flag from one selected message location
  unsubscribe Safely execute a standards-based mailing-list unsubscribe action
  watch       Keep enabled mail folders fresh with IDLE or explicit polling fallback

Flags:
  -h, --help   help for mail

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail [command] --help" for more information about a command.
```

## `icloud mail archive`

```text
Move eligible non-Sent locations to the semantic Archive folder

Usage:
  icloud mail archive <message-id> [message-id...] [flags]

Examples:
  icloud mail archive msg_… --dry-run
  icloud mail archive msg_… --from inbox --idempotency-key archive-01 --yes

Flags:
      --dry-run                  Preview the exact location change without creating intent or contacting IMAP
      --from string              Select one exact current source folder by ID, name, wire name, or kind
  -h, --help                     help for archive
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --input-jsonl string       Read up to 100 strict JSONL location items from a file, or - for stdin
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit typed per-item and completed JSONL events
      --mode string              Bulk failure mode: validate the bounded target set then fail fast, or continue-on-error (default "atomic")
      --to string                Select one current destination folder by ID, name, wire name, or kind
      --yes                      Confirm an intentional non-interactive location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail attachments`

```text
List and safely export decoded local attachments

Usage:
  icloud mail attachments [flags]
  icloud mail attachments [command]

Examples:
  icloud mail attachments list msg_…
  icloud mail attachments save att_… --output document.pdf
  icloud mail attachments save-all msg_… --dir ./attachments

Available Commands:
  list        List bounded attachment metadata without printing bytes
  remove      Replace one exact provider copy after removing reviewed MIME attachments
  save        Verify and stream one decoded attachment
  save-all    Export a bounded attachment set with safe deterministic names

Flags:
  -h, --help   help for attachments

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail attachments [command] --help" for more information about a command.
```

## `icloud mail attachments list`

```text
List bounded attachment metadata without printing bytes

Usage:
  icloud mail attachments list <message-or-thread-id> [flags]

Examples:
  icloud mail attachments list msg_…
  icloud mail attachments list thr_… --json
  icloud mail attachments list --input-json request.json --json

Flags:
      --cursor string       Continue from a next_cursor returned for the same attachment target
  -h, --help                help for list
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum attachments to list (1-100) (default 20)

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail attachments remove`

```text
Replace one exact provider copy after removing reviewed MIME attachments

Usage:
  icloud mail attachments remove <message-id> [flags]

Examples:
  icloud mail attachments remove msg_… --attachment att_… --dry-run
  icloud mail attachments remove msg_… --attachment att_… --idempotency-key rewrite-01 --yes
  icloud mail attachments remove --input-json request.json --dry-run --json

Flags:
      --attachment stringArray   Stable att_… attachment ID to remove; repeat as needed
      --dry-run                  Reconstruct and review the replacement locally without creating rewrite intent or contacting IMAP
      --folder string            Select the exact current provider source folder when the message has more than one eligible copy
  -h, --help                     help for remove
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                     Emit one stable JSON success envelope
      --yes                      Confirm an intentional non-interactive provider content replacement

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail attachments save`

```text
Verify and stream one decoded attachment

Usage:
  icloud mail attachments save <attachment-id> [flags]

Examples:
  icloud mail attachments save att_… --output ./document.pdf
  icloud mail attachments save att_… --output - --binary

Flags:
      --binary              Acknowledge binary bytes on stdout
      --force               Replace an existing regular file
  -h, --help                help for save
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope after file export
      --output string       Destination path, or - for stdout

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail attachments save-all`

```text
Export a bounded attachment set with safe deterministic names

Usage:
  icloud mail attachments save-all <message-or-thread-id> [flags]

Examples:
  icloud mail attachments save-all msg_… --dir ./attachments
  icloud mail attachments save-all thr_… --dir ./thread-files

Flags:
      --dir string          Destination directory
      --force               Replace existing regular files
  -h, --help                help for save-all
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail copy`

```text
Copy exact current message locations

Usage:
  icloud mail copy <message-id> [message-id...] [flags]

Examples:
  icloud mail copy msg_… --from inbox --to Projects --dry-run
  icloud mail copy msg_… --from inbox --to Projects --idempotency-key copy-01 --yes

Flags:
      --dry-run                  Preview the exact location change without creating intent or contacting IMAP
      --from string              Select one exact current source folder by ID, name, wire name, or kind
  -h, --help                     help for copy
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --input-jsonl string       Read up to 100 strict JSONL location items from a file, or - for stdin
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit typed per-item and completed JSONL events
      --mode string              Bulk failure mode: validate the bounded target set then fail fast, or continue-on-error (default "atomic")
      --to string                Select one current destination folder by ID, name, wire name, or kind
      --yes                      Confirm an intentional non-interactive location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail delete`

```text
Permanently delete exact current provider locations

Usage:
  icloud mail delete <message-id> [message-id...] [flags]

Examples:
  icloud mail delete msg_… --from trash --dry-run
  icloud mail delete msg_… --from trash --idempotency-key delete-01 --yes

Flags:
      --dry-run                  Preview the exact location change without creating intent or contacting IMAP
      --from string              Select one exact current source folder by ID, name, wire name, or kind
  -h, --help                     help for delete
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --input-jsonl string       Read up to 100 strict JSONL location items from a file, or - for stdin
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit typed per-item and completed JSONL events
      --mode string              Bulk failure mode: validate the bounded target set then fail fast, or continue-on-error (default "atomic")
      --to string                Select one current destination folder by ID, name, wire name, or kind
      --yes                      Confirm an intentional non-interactive location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail draft`

```text
Compose, synchronize, inspect, send, and delete iCloud-backed drafts

Usage:
  icloud mail draft [flags]
  icloud mail draft [command]

Examples:
  icloud mail draft create --to teammate@example.com --subject 'Working note' --body-file draft.txt
  icloud mail draft sync
  icloud mail draft list
  icloud mail draft send drf_… --dry-run

Available Commands:
  create      Create one local draft without contacting iCloud
  delete      Delete one local draft and every known provider Drafts version
  list        List local and provider-synchronized drafts
  send        Preview or send one draft through the normal idempotent outbound path
  show        Show a draft with bounded default content or an explicit body/raw expansion
  sync        Synchronize provider Drafts and safely upload local revisions
  update      Replace one draft revision locally before its next provider sync

Flags:
  -h, --help   help for draft

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail draft [command] --help" for more information about a command.
```

## `icloud mail draft create`

```text
Create one local draft without contacting iCloud

Usage:
  icloud mail draft create [flags]

Examples:
  icloud mail draft create --subject 'Idea' --body 'Incomplete is okay'
  icloud mail draft update drf_… --to client@example.com --subject 'Ready soon' --body-file draft.txt

Flags:
      --attach stringArray               Regular file path to attach; repeat as needed
      --attach-cid stringArray           Set one attachment Content-ID as path=local@domain
      --attach-disposition stringArray   Set one attachment disposition as path=attachment or path=inline
      --attach-name stringArray          Override one attachment filename as path=filename
      --attach-type stringArray          Override one attachment media type as path=type/subtype
      --bcc stringArray                  Bcc recipient preserved in the saved draft; hidden from default output
      --body string                      Inline text body; may be empty
      --body-file string                 Text body file path, or - for stdin
      --cc stringArray                   Cc recipient address; repeat as needed
      --from string                      Sender identity by stable mid_… ID or configured address (defaults to the account's local default identity)
      --header stringArray               Safe custom header as Name: Value; repeat as needed
  -h, --help                             help for create
      --html-body string                 Inline HTML body
      --html-body-file string            HTML body file path, or - for stdin
      --input-json string                Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                             Emit one stable JSON success envelope
      --reply-to string                  Optional Reply-To address
      --signature string                 Override the configured new-message signature with one sig_… ID or none
      --subject string                   Draft subject; may be empty
      --to stringArray                   Recipient address; repeat or provide a comma-separated address list

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail draft delete`

```text
Delete one local draft and every known provider Drafts version

Usage:
  icloud mail draft delete <draft-id> [flags]

Examples:
  icloud mail draft delete drf_… --dry-run
  icloud mail draft delete drf_… --yes

Flags:
      --dry-run             Show the exact deletion scope without changing local or provider state
  -h, --help                help for delete
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --yes                 Confirm intentional non-interactive draft deletion

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail draft list`

```text
List local and provider-synchronized drafts

Usage:
  icloud mail draft list [flags]

Examples:
  icloud mail draft list
  icloud mail draft list --limit 50 --json
  icloud mail draft list --cursor '<opaque-cursor>'

Flags:
      --cursor string   Opaque cursor from a previous draft list page
  -h, --help            help for list
      --json            Emit one stable JSON success envelope
      --limit int       Maximum drafts to return (1-100) (default 20)
      --verbose         Include provider recovery IDs and timestamps

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail draft send`

```text
Preview or send one draft through the normal idempotent outbound path

Usage:
  icloud mail draft send <draft-id> [flags]

Examples:
  icloud mail draft send drf_… --dry-run
  icloud mail draft send drf_… --yes

Flags:
      --dry-run             Preview the normalized send without contacting SMTP or deleting the draft
      --format string       With --dry-run, output text, html, or raw preview content
      --from string         Sender identity by stable mid_… ID or configured address (defaults to the account's local default identity)
      --headers             With --dry-run, print normalized safe headers
  -h, --help                help for send
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --verbose             With --dry-run, include normalized header detail
      --yes                 Confirm an intentional non-interactive send

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail draft show`

```text
Show a draft with bounded default content or an explicit body/raw expansion

Usage:
  icloud mail draft show <draft-id> [flags]

Examples:
  icloud mail draft show drf_…
  icloud mail draft show drf_… --full

Flags:
      --body-only       Write only the selected body representation
      --format string   Content format: compact, text, html, or raw (default "compact")
      --full            Include the complete decoded text body
      --headers         Include decoded headers (BCC remains omitted)
  -h, --help            help for show
      --json            Emit one stable JSON success envelope
      --verbose         Include provider recovery IDs and MIME detail

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail draft sync`

```text
Synchronize provider Drafts and safely upload local revisions

Usage:
  icloud mail draft sync [draft-id] [flags]

Examples:
  icloud mail draft sync
  icloud mail draft sync drf_…
  icloud mail draft sync --folder Drafts

Flags:
      --folder string       Explicitly select the provider Drafts mailbox by ID, name, or wire name when iCloud advertises no Drafts role
  -h, --help                help for sync
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum provider messages committed per Drafts sync page (1-100) (default 20)
      --verbose             Include resulting draft recovery state

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail draft update`

```text
Replace one draft revision locally before its next provider sync

Usage:
  icloud mail draft update <draft-id> [flags]

Examples:
  icloud mail draft create --subject 'Idea' --body 'Incomplete is okay'
  icloud mail draft update drf_… --to client@example.com --subject 'Ready soon' --body-file draft.txt

Flags:
      --attach stringArray               Regular file path to attach; repeat as needed
      --attach-cid stringArray           Set one attachment Content-ID as path=local@domain
      --attach-disposition stringArray   Set one attachment disposition as path=attachment or path=inline
      --attach-name stringArray          Override one attachment filename as path=filename
      --attach-type stringArray          Override one attachment media type as path=type/subtype
      --bcc stringArray                  Bcc recipient preserved in the saved draft; hidden from default output
      --body string                      Inline text body; may be empty
      --body-file string                 Text body file path, or - for stdin
      --cc stringArray                   Cc recipient address; repeat as needed
      --from string                      Sender identity by stable mid_… ID or configured address (defaults to the account's local default identity)
      --header stringArray               Safe custom header as Name: Value; repeat as needed
  -h, --help                             help for update
      --html-body string                 Inline HTML body
      --html-body-file string            HTML body file path, or - for stdin
      --input-json string                Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                             Emit one stable JSON success envelope
      --reply-to string                  Optional Reply-To address
      --signature string                 Override the configured new-message signature with one sig_… ID or none
      --subject string                   Draft subject; may be empty
      --to stringArray                   Recipient address; repeat or provide a comma-separated address list

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail empty`

```text
Permanently delete reviewed bounded pages from Trash, Junk, or one named custom folder

Usage:
  icloud mail empty [flags]
  icloud mail empty [command]

Examples:
  icloud mail empty trash --dry-run
  icloud mail empty junk --dry-run
  icloud mail empty folder 'Temporary Import' --dry-run

Available Commands:
  folder      Permanently delete one reviewed bounded page from one deliberately named ordinary custom folder
  junk        Permanently delete one reviewed bounded page from the semantic Junk folder
  trash       Permanently delete one reviewed bounded page from the semantic Trash folder

Flags:
  -h, --help   help for empty

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail empty [command] --help" for more information about a command.
```

## `icloud mail empty folder`

```text
Permanently delete one reviewed bounded page from one deliberately named ordinary custom folder

Usage:
  icloud mail empty folder <folder> [flags]

Examples:
  icloud mail empty folder 'Temporary Import' --limit 50 --dry-run
  icloud mail empty folder 'Temporary Import' --purge-id pur_… --request-hash <hash> --yes --jsonl

Flags:
      --dry-run               Freeze and review one exact bounded purge page without provider writes
  -h, --help                  help for folder
      --input-json string     Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                  Emit one stable JSON success envelope
      --jsonl                 Emit exact reviewed/apply item events and completion accounting
      --limit int             Maximum exact provider locations in this reviewed purge page (default 100)
      --mode string           Failure mode: validate then fail fast, or continue-on-error (default "atomic")
      --purge-id string       Opaque bounded purge review ID emitted by --dry-run
      --request-hash string   Exact apply hash emitted by the matching --dry-run review
      --yes                   Confirm intentional non-interactive permanent deletion

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail empty junk`

```text
Permanently delete one reviewed bounded page from the semantic Junk folder

Usage:
  icloud mail empty junk [flags]

Examples:
  icloud mail empty junk --limit 50 --dry-run
  icloud mail empty junk --purge-id pur_… --request-hash <hash> --yes --jsonl

Flags:
      --dry-run               Freeze and review one exact bounded purge page without provider writes
  -h, --help                  help for junk
      --input-json string     Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                  Emit one stable JSON success envelope
      --jsonl                 Emit exact reviewed/apply item events and completion accounting
      --limit int             Maximum exact provider locations in this reviewed purge page (default 100)
      --mode string           Failure mode: validate then fail fast, or continue-on-error (default "atomic")
      --purge-id string       Opaque bounded purge review ID emitted by --dry-run
      --request-hash string   Exact apply hash emitted by the matching --dry-run review
      --yes                   Confirm intentional non-interactive permanent deletion

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail empty trash`

```text
Permanently delete one reviewed bounded page from the semantic Trash folder

Usage:
  icloud mail empty trash [flags]

Examples:
  icloud mail empty trash --limit 50 --dry-run
  icloud mail empty trash --purge-id pur_… --request-hash <hash> --yes --jsonl

Flags:
      --dry-run               Freeze and review one exact bounded purge page without provider writes
  -h, --help                  help for trash
      --input-json string     Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                  Emit one stable JSON success envelope
      --jsonl                 Emit exact reviewed/apply item events and completion accounting
      --limit int             Maximum exact provider locations in this reviewed purge page (default 100)
      --mode string           Failure mode: validate then fail fast, or continue-on-error (default "atomic")
      --purge-id string       Opaque bounded purge review ID emitted by --dry-run
      --request-hash string   Exact apply hash emitted by the matching --dry-run review
      --yes                   Confirm intentional non-interactive permanent deletion

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail flag`

```text
Flag one selected message location

Usage:
  icloud mail flag <message-id> [message-id...] [flags]

Examples:
  icloud mail flag msg_…
  icloud mail flag msg_… --folder inbox

Flags:
      --folder string        Select one exact folder ID, name, wire name, or kind for every positional/JSON item
  -h, --help                 help for flag
      --input-json string    Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --input-jsonl string   Read up to 100 strict JSONL items with message_id and optional folder from a file, or - for stdin
      --json                 Emit one stable JSON success envelope
      --jsonl                Emit typed per-item and completed JSONL events
      --mode string          Bulk failure mode: validate the bounded input then fail fast on target/provider errors, or continue-on-error (default "atomic")

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail folders`

```text
Discover folders, inspect provider status, and choose sync policy

Usage:
  icloud mail folders [flags]
  icloud mail folders [command]

Examples:
  icloud mail folders refresh
  icloud mail folders enable Receipts

Available Commands:
  create      Create one custom iCloud Mail folder
  delete      Delete one custom iCloud Mail folder under explicit safety policy
  disable     Disable local synchronization policy for one folder
  enable      Enable local synchronization policy for one folder
  list        List locally discovered folders without contacting iCloud
  refresh     Refresh every remote folder and its provider counts without fetching messages
  rename      Rename one custom iCloud Mail folder without changing its stable local ID
  role        Manage local semantic folder-role bindings without changing provider folder metadata
  show        Show one locally discovered folder
  status      Compatibility alias for `mail folders refresh`
  subscribe   Subscribe one current iCloud Mail folder
  unsubscribe Unsubscribe one current iCloud Mail folder

Flags:
  -h, --help   help for folders

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail folders [command] --help" for more information about a command.
```

## `icloud mail folders create`

```text
Create one custom iCloud Mail folder

Usage:
  icloud mail folders create <name> [flags]

Examples:
  icloud mail folders create 'Projects/Client' --dry-run
  icloud mail folders create 'Projects/Client' --yes

Flags:
      --dry-run             Preview the exact folder change without contacting IMAP
  -h, --help                help for create
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --yes                 Confirm an intentional non-interactive folder change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail folders delete`

```text
Delete one custom iCloud Mail folder under explicit safety policy

Usage:
  icloud mail folders delete <folder> [flags]

Examples:
  icloud mail folders delete fld_… --dry-run
  icloud mail folders delete fld_… --yes

Flags:
      --allow-non-empty     Allow provider deletion only after iCloud rechecks that the folder contains messages
      --dry-run             Preview the exact folder change without contacting IMAP
  -h, --help                help for delete
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --yes                 Confirm an intentional non-interactive folder change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail folders disable`

```text
Disable local synchronization policy for one folder

Usage:
  icloud mail folders disable <folder-id|name|wire-name|kind> [flags]

Examples:
  icloud mail folders disable Receipts

Flags:
  -h, --help                help for disable
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail folders enable`

```text
Enable local synchronization policy for one folder

Usage:
  icloud mail folders enable <folder-id|name|wire-name|kind> [flags]

Examples:
  icloud mail folders enable Receipts

Flags:
  -h, --help                help for enable
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail folders list`

```text
List locally discovered folders without contacting iCloud

Usage:
  icloud mail folders list [flags]

Examples:
  icloud mail folders list
  icloud mail folders list --include-missing --verbose
  icloud mail folders list --input-json request.json --json

Flags:
      --cursor string       Continue from a next_cursor returned by an earlier folder page
  -h, --help                help for list
      --include-missing     Include folders missing from the latest provider discovery
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum folders to return (1-100) (default 20)
      --verbose             Include wire names, attributes, UID metadata, and timestamps

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail folders refresh`

```text
Refresh every remote folder and its provider counts without fetching messages

Usage:
  icloud mail folders refresh [flags]

Examples:
  icloud mail folders refresh
  icloud --account Personal mail folders refresh --json
  icloud mail folders refresh --input-json request.json --json

Flags:
      --cursor string       Continue from a next_cursor returned by an earlier refreshed folder page
  -h, --help                help for refresh
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum folder records to print after refreshing all remote folders (1-100) (default 20)
      --verbose             Include wire names, attributes, UID metadata, and timestamps

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail folders rename`

```text
Rename one custom iCloud Mail folder without changing its stable local ID

Usage:
  icloud mail folders rename <folder> <new-name> [flags]

Examples:
  icloud mail folders rename Receipts 'Projects/Receipts' --dry-run
  icloud mail folders rename Receipts 'Projects/Receipts' --yes

Flags:
      --dry-run             Preview the exact folder change without contacting IMAP
  -h, --help                help for rename
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --yes                 Confirm an intentional non-interactive folder change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail folders role`

```text
Manage local semantic folder-role bindings without changing provider folder metadata

Usage:
  icloud mail folders role [flags]
  icloud mail folders role [command]

Available Commands:
  clear       Remove one local semantic folder-role binding
  list        List account-owned semantic folder-role bindings from local state
  set         Bind one semantic role to one stable local folder ID

Flags:
  -h, --help   help for role

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail folders role [command] --help" for more information about a command.
```

## `icloud mail folders role clear`

```text
Remove one local semantic folder-role binding

Usage:
  icloud mail folders role clear <sent|drafts|archive|trash|junk> [flags]

Examples:
  icloud mail folders role clear archive --dry-run
  icloud mail folders role clear archive

Flags:
      --dry-run             Preview the local binding removal without writing SQLite or contacting iCloud
  -h, --help                help for clear
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail folders role list`

```text
List account-owned semantic folder-role bindings from local state

Usage:
  icloud mail folders role list [flags]

Examples:
  icloud mail folders role list
  icloud --account Personal mail folders role list --json

Flags:
  -h, --help   help for list
      --json   Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail folders role set`

```text
Bind one semantic role to one stable local folder ID

Usage:
  icloud mail folders role set <sent|drafts|archive|trash|junk> <folder-id|name|wire-name> [flags]

Examples:
  icloud mail folders role set archive fld_… --dry-run
  icloud mail folders role set drafts 'Drafts Local'

Flags:
      --dry-run             Preview the local binding change without writing SQLite or contacting iCloud
  -h, --help                help for set
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail folders show`

```text
Show one locally discovered folder

Usage:
  icloud mail folders show <folder-id|name|wire-name|kind> [flags]

Examples:
  icloud mail folders show INBOX
  icloud mail folders show fld_… --verbose
  icloud mail folders show --input-json request.json --json

Flags:
  -h, --help                help for show
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --verbose             Include wire names, attributes, UID metadata, and timestamps

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail folders status`

```text
Compatibility alias for `mail folders refresh`

Usage:
  icloud mail folders status [flags]

Examples:
  icloud mail folders refresh
  icloud --account Personal mail folders refresh --json

Flags:
      --cursor string       Continue from a next_cursor returned by an earlier refreshed folder page
  -h, --help                help for status
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum folder records to print after refreshing all remote folders (1-100) (default 20)
      --verbose             Include wire names, attributes, UID metadata, and timestamps

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail folders subscribe`

```text
Subscribe one current iCloud Mail folder

Usage:
  icloud mail folders subscribe <folder> [flags]

Examples:
  icloud mail folders subscribe Receipts --dry-run
  icloud mail folders subscribe Receipts --yes

Flags:
      --dry-run             Preview the exact folder change without contacting IMAP
  -h, --help                help for subscribe
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --yes                 Confirm an intentional non-interactive folder change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail folders unsubscribe`

```text
Unsubscribe one current iCloud Mail folder

Usage:
  icloud mail folders unsubscribe <folder> [flags]

Examples:
  icloud mail folders unsubscribe Receipts --dry-run
  icloud mail folders unsubscribe Receipts --yes

Flags:
      --dry-run             Preview the exact folder change without contacting IMAP
  -h, --help                help for unsubscribe
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --yes                 Confirm an intentional non-interactive folder change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail forward`

```text
Forward one explicitly selected local message as a new conversation

Usage:
  icloud mail forward <msg_…> [flags]

Examples:
  icloud mail forward msg_… --to teammate@example.com --body 'For context' --dry-run
  icloud mail forward msg_… --from mid_… --to teammate@example.com --original eml --include-attachment att_… --idempotency-key forward-01 --yes

Flags:
      --attach stringArray               New regular file path to attach; repeat as needed
      --attach-cid stringArray           Set one new attachment Content-ID as path=local@domain; repeat as needed
      --attach-disposition stringArray   Set one new attachment disposition as path=attachment or path=inline; repeat as needed
      --attach-name stringArray          Override one new attachment filename as path=filename; repeat as needed
      --attach-type stringArray          Override one new attachment media type as path=type/subtype; repeat as needed
      --bcc stringArray                  Bcc envelope recipient; omitted from message headers
      --body string                      Optional introductory text body
      --body-file string                 Introductory text body file path, or - for stdin
      --cc stringArray                   Cc recipient address; repeat as needed
      --dry-run                          Preview the resolved forward without creating an attempt or contacting SMTP
      --format string                    With --dry-run, output text, html, or raw preview content
      --from string                      Sender identity by stable mid_… ID or configured address (defaults to the account's local default identity)
      --header stringArray               Safe custom header as Name: Value; repeat as needed
      --headers                          With --dry-run, print normalized headers; forward reply headers remain absent
  -h, --help                             help for forward
      --html-body string                 Optional introductory HTML body
      --html-body-file string            Introductory HTML body file path, or - for stdin
      --idempotency-key string           Reuse the original operation when the same normalized request and idempotency key are repeated
      --include-attachment stringArray   Include one original att_… attachment by stable ID; repeat as needed
      --input-json string                Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                             Emit one stable JSON success envelope
      --original string                  Original representation: quoted, eml, or none (default "quoted")
      --reply-to string                  Optional Reply-To address
      --retry-of string                  Link an explicit retry to one failed or unknown send_… attempt
      --retry-unknown                    Authorize one duplicate-risk retry of an unknown attempt; requires --retry-of and --yes
      --signature string                 Override the configured forward signature with one sig_… ID or none
      --to stringArray                   Recipient address; repeat or provide a comma-separated address list
      --verbose                          With --dry-run, include custom-header detail and HTML byte count
      --yes                              Confirm an intentional non-interactive forward

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail identities`

```text
Manage local Send From identities for the selected mail account

Usage:
  icloud mail identities [flags]
  icloud mail identities [command]

Examples:
  icloud mail identities list
  icloud mail identities add --address alias@icloud.com --kind icloud_alias
  icloud mail identities default mid_…

Available Commands:
  add         Add one local sender identity without claiming provider acceptance
  default     Choose the local default enabled sender identity
  disable     Disable one non-primary local sender identity
  enable      Enable one local sender identity
  list        List bounded local sender identities without contacting iCloud
  reclassify  Advance bounded local historical direction repair for newly recognized self addresses
  remove      Remove one non-primary local sender identity
  show        Show one local sender identity for the selected account
  update      Update local address, display-name, label, or kind metadata

Flags:
  -h, --help   help for identities

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail identities [command] --help" for more information about a command.
```

## `icloud mail identities add`

```text
Add one local sender identity without claiming provider acceptance

Usage:
  icloud mail identities add [flags]

Examples:
  icloud mail identities add --address alias@icloud.com --kind icloud_alias
  icloud mail identities add --address me@example.com --display-name 'Work Me' --kind custom_domain --default

Flags:
      --address string        RFC email address for this identity
      --default               Make this enabled identity the local default sender
      --display-name string   Local display name (defaults to the account name)
  -h, --help                  help for add
      --input-json string     Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                  Emit one stable JSON success envelope
      --kind string           Identity kind: icloud_alias, custom_domain, hide_my_email, or other (default "other")
      --label string          Optional local label; for Hide My Email this never exposes or manages Apple's forwarding destination

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail identities default`

```text
Choose the local default enabled sender identity

Usage:
  icloud mail identities default <identity-id|address> [flags]

Examples:
  icloud mail identities default mid_…

Flags:
  -h, --help                help for default
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail identities disable`

```text
Disable one non-primary local sender identity

Usage:
  icloud mail identities disable <identity-id|address> [flags]

Examples:
  icloud mail identities disable mid_…

Flags:
  -h, --help                help for disable
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail identities enable`

```text
Enable one local sender identity

Usage:
  icloud mail identities enable <identity-id|address> [flags]

Examples:
  icloud mail identities enable mid_…

Flags:
  -h, --help                help for enable
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail identities list`

```text
List bounded local sender identities without contacting iCloud

Usage:
  icloud mail identities list [flags]

Examples:
  icloud mail identities list
  icloud --account Personal mail identities list --json
  icloud mail identities list --input-json request.json --json

Flags:
      --cursor string       Continue from a next_cursor returned for the same account
  -h, --help                help for list
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum identities to return (1-100) (default 20)

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail identities reclassify`

```text
Advance bounded local historical direction repair for newly recognized self addresses

Usage:
  icloud mail identities reclassify [flags]

Examples:
  icloud mail identities reclassify
  icloud mail identities reclassify --limit 100 --json

Flags:
  -h, --help                help for reclassify
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum historical messages to promote in this pass (1-100) (default 20)

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail identities remove`

```text
Remove one non-primary local sender identity

Usage:
  icloud mail identities remove <identity-id|address> [flags]

Examples:
  icloud mail identities remove mid_… --yes

Flags:
  -h, --help                help for remove
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --yes                 Authorize local identity removal without prompting

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail identities show`

```text
Show one local sender identity for the selected account

Usage:
  icloud mail identities show <identity-id|address> [flags]

Examples:
  icloud mail identities show mid_…
  icloud mail identities show alias@icloud.com --json
  icloud mail identities show --input-json request.json --json

Flags:
  -h, --help                help for show
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail identities update`

```text
Update local address, display-name, label, or kind metadata

Usage:
  icloud mail identities update <identity-id|address> [flags]

Examples:
  icloud mail identities update mid_… --display-name 'Work Me'
  icloud mail identities update alias@icloud.com --kind custom_domain

Flags:
      --address string        Replace the RFC email address
      --display-name string   Replace the local display name
  -h, --help                  help for update
      --input-json string     Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                  Emit one stable JSON success envelope
      --kind string           Replace the identity kind
      --label string          Replace or clear the optional local label

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail inbox`

```text
Compatibility alias for `mail messages list`

Usage:
  icloud mail inbox [flags]

Examples:
  icloud mail messages list
  icloud mail messages list --unread --from billing@example.com
  icloud mail messages list --folder sent --json

Flags:
      --after string             Include messages at or after an RFC3339 timestamp or YYYY-MM-DD UTC date
      --attachment-name string   Restrict attachment filename text
      --attachment-type string   Restrict attachment media type
      --before string            Include messages before an RFC3339 timestamp or YYYY-MM-DD UTC date
      --blocked                  Include only messages from exact local blocked sender addresses
      --cursor string            Continue from a next_cursor returned by the same query
      --flagged                  Include only messages with a flagged eligible location
      --folder string            Folder kind, ID, name, or wire name (default "inbox")
      --from string              Restrict sender or Reply-To text
  -h, --help                     help for inbox
      --input-json string        Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                     Emit one stable JSON success envelope
      --limit int                Maximum local folder messages to return (1-100) (default 20)
      --participant string       Restrict sender, Reply-To, To, or Cc participant text
      --query string             Match indexed subject, sender, participants, decoded text, attachments, or RFC Message-ID
      --read                     Include only messages without an unread eligible location
      --rfc-id string            Match one RFC Message-ID
      --subject string           Restrict subject text
      --unflagged                Include only messages without a flagged eligible location
      --unread                   Include only messages with an unread eligible location
      --vip                      Include only messages from exact local VIP sender addresses

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail junk`

```text
Move exact current message locations to the semantic Junk folder

Usage:
  icloud mail junk <message-id> [message-id...] [flags]

Examples:
  icloud mail junk msg_… --dry-run
  icloud mail junk msg_… --from inbox --idempotency-key junk-01 --yes

Flags:
      --dry-run                  Preview the exact location change without creating intent or contacting IMAP
      --from string              Select one exact current source folder by ID, name, wire name, or kind
  -h, --help                     help for junk
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --input-jsonl string       Read up to 100 strict JSONL location items from a file, or - for stdin
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit typed per-item and completed JSONL events
      --mode string              Bulk failure mode: validate the bounded target set then fail fast, or continue-on-error (default "atomic")
      --to string                Select one current destination folder by ID, name, wire name, or kind
      --yes                      Confirm an intentional non-interactive location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail latest`

```text
Compatibility alias for `mail threads list`

Usage:
  icloud mail latest [flags]

Examples:
  icloud mail threads list
  icloud mail threads list --query launch --unread
  icloud mail threads list --cursor cur_… --json

Flags:
      --after string             Include messages at or after an RFC3339 timestamp or YYYY-MM-DD UTC date
      --attachment-name string   Restrict attachment filename text
      --attachment-type string   Restrict attachment media type
      --before string            Include messages before an RFC3339 timestamp or YYYY-MM-DD UTC date
      --blocked                  Include only messages from exact local blocked sender addresses
      --cursor string            Continue from a next_cursor returned by the same query
      --flagged                  Include only messages with a flagged eligible location
      --folder string            Restrict matches to one folder kind, ID, name, or wire name
      --from string              Restrict sender or Reply-To text
  -h, --help                     help for latest
      --input-json string        Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                     Emit one stable JSON success envelope
      --limit int                Maximum reconstructed conversations to return (1-100) (default 20)
      --participant string       Restrict sender, Reply-To, To, or Cc participant text
      --query string             Match indexed subject, sender, participants, decoded text, attachments, or RFC Message-ID
      --read                     Include only messages without an unread eligible location
      --rfc-id string            Match one RFC Message-ID
      --subject string           Restrict subject text
      --unflagged                Include only messages without a flagged eligible location
      --unread                   Include only messages with an unread eligible location
      --vip                      Include only messages from exact local VIP sender addresses

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail mailbox`

```text
Import or export complete mailbox archives

Usage:
  icloud mail mailbox [command]

Available Commands:
  export      Stream one synchronized mailbox as mboxrd without contacting iCloud
  import      Resume a bounded mboxrd import through exact single-message APPEND recovery

Flags:
  -h, --help   help for mailbox

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail mailbox [command] --help" for more information about a command.
```

## `icloud mail mailbox export`

```text
Stream one synchronized mailbox as mboxrd without contacting iCloud

Usage:
  icloud mail mailbox export <folder> [flags]

Examples:
  icloud mail mailbox export inbox --format mboxrd --output inbox.mbox
  icloud mail mailbox export Archive --output - > archive.mbox
  icloud mail mailbox export --input-json request.json --json

Flags:
      --force               Replace an existing regular archive file
      --format string       Mailbox archive format; currently mboxrd (default "mboxrd")
  -h, --help                help for export
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON summary when output is a file
      --output string       Archive destination path, or - for native stdout

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail mailbox import`

```text
Resume a bounded mboxrd import through exact single-message APPEND recovery

Usage:
  icloud mail mailbox import <file.mbox> [flags]

Examples:
  icloud mail mailbox import archive.mbox --to inbox --dry-run
  icloud mail mailbox import archive.mbox --to Archive --max-messages 100 --yes
  icloud mail mailbox import archive.mbox --to inbox --mode continue-on-error --yes --jsonl
  icloud mail mailbox import --input-json request.json --yes --json

Flags:
      --dry-run             Fingerprint and validate the next archive record locally without creating a job or contacting IMAP
  -h, --help                help for import
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --jsonl               Emit ordered record results plus one terminal completed event
      --max-messages int    Maximum archive records to attempt in this invocation (default 100)
      --mode string         Failure mode for this bounded pass: fail fast or continue-on-error (default "atomic")
      --to string           Current selectable destination folder kind, ID, name, or wire name
      --yes                 Confirm an intentional non-interactive provider import

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail mark-read`

```text
Mark one selected message location as read

Usage:
  icloud mail mark-read <message-id> [message-id...] [flags]

Examples:
  icloud mail mark-read msg_…
  icloud mail mark-read msg_… --folder inbox

Flags:
      --folder string        Select one exact folder ID, name, wire name, or kind for every positional/JSON item
  -h, --help                 help for mark-read
      --input-json string    Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --input-jsonl string   Read up to 100 strict JSONL items with message_id and optional folder from a file, or - for stdin
      --json                 Emit one stable JSON success envelope
      --jsonl                Emit typed per-item and completed JSONL events
      --mode string          Bulk failure mode: validate the bounded input then fail fast on target/provider errors, or continue-on-error (default "atomic")

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail mark-unread`

```text
Mark one selected message location as unread

Usage:
  icloud mail mark-unread <message-id> [message-id...] [flags]

Examples:
  icloud mail mark-unread msg_…
  icloud mail mark-unread msg_… --folder inbox

Flags:
      --folder string        Select one exact folder ID, name, wire name, or kind for every positional/JSON item
  -h, --help                 help for mark-unread
      --input-json string    Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --input-jsonl string   Read up to 100 strict JSONL items with message_id and optional folder from a file, or - for stdin
      --json                 Emit one stable JSON success envelope
      --jsonl                Emit typed per-item and completed JSONL events
      --mode string          Bulk failure mode: validate the bounded input then fail fast on target/provider errors, or continue-on-error (default "atomic")

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail message`

```text
Read, export, or deliberately import complete RFC messages

Usage:
  icloud mail message [flags]
  icloud mail message [command]

Examples:
  icloud mail message show msg_…
  icloud mail message raw msg_…
  icloud mail message export msg_… --output message.eml
  icloud mail message import message.eml --to inbox --dry-run

Available Commands:
  export      Export the exact locally stored RFC822 message atomically
  import      Append one validated RFC message and ingest the exact provider copy
  raw         Stream the exact locally stored RFC822 message to stdout
  show        Show bounded metadata or explicitly requested complete content

Flags:
  -h, --help   help for message

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail message [command] --help" for more information about a command.
```

## `icloud mail message export`

```text
Export the exact locally stored RFC822 message atomically

Usage:
  icloud mail message export <message-id> [flags]

Examples:
  icloud mail message export msg_… --output message.eml
  icloud mail message export msg_… --output - > message.eml

Flags:
      --force               Replace an existing regular file
  -h, --help                help for export
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope after file export
      --output string       Destination path, or - for stdout

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail message import`

```text
Append one validated RFC message and ingest the exact provider copy

Usage:
  icloud mail message import <file.eml> [flags]

Examples:
  icloud mail message import message.eml --to inbox --dry-run
  icloud mail message import message.eml --to Archive --flag seen --idempotency-key import-01 --yes
  icloud mail message import --input-json request.json --dry-run --json

Flags:
      --dry-run                  Validate and review the exact source locally without creating import intent or contacting IMAP
      --flag stringArray         Initial provider flag: seen, flagged, or answered; repeat as needed
  -h, --help                     help for import
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --internal-date string     Provider INTERNALDATE as RFC3339; defaults to the message Date header or current time
      --json                     Emit one stable JSON success envelope
      --to string                Current selectable destination folder kind, ID, name, or wire name
      --yes                      Confirm an intentional non-interactive provider APPEND

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail message raw`

```text
Stream the exact locally stored RFC822 message to stdout

Usage:
  icloud mail message raw <message-id> [flags]

Examples:
  icloud mail message raw msg_… > message.eml

Flags:
  -h, --help   help for raw

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail message show`

```text
Show bounded metadata or explicitly requested complete content

Usage:
  icloud mail message show <message-id> [flags]

Examples:
  icloud mail message show msg_…
  icloud mail message show rem_… --remote
  icloud mail message show rem_… --remote --cache --full
  icloud mail message show msg_… --format html --body-only
  icloud mail message show msg_… --headers --json
  icloud mail message show --input-json request.json --json

Flags:
      --body-only           Write only the selected body representation
      --cache               Persist the referenced remote message through the normal local ingestion path
      --format string       Content format: compact, text, html, or raw (default "compact")
      --full                Include the complete decoded text body
      --headers             Include decoded headers (BCC remains omitted)
  -h, --help                help for show
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --metadata-only       Show metadata without a decoded excerpt or content
      --remote              Treat the argument as a stale-safe rem_… provider reference
      --verbose             Include MIME part metadata and parse diagnostics

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail messages`

```text
Read synchronized messages through the canonical message-list surface

Usage:
  icloud mail messages [flags]
  icloud mail messages [command]

Examples:
  icloud mail messages list
  icloud mail messages list --folder sent --json

Available Commands:
  list        List and filter a bounded page from a synchronized mail folder

Flags:
  -h, --help   help for messages

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail messages [command] --help" for more information about a command.
```

## `icloud mail messages list`

```text
List and filter a bounded page from a synchronized mail folder

Usage:
  icloud mail messages list [flags]

Examples:
  icloud mail messages list
  icloud mail messages list --unread --from billing@example.com
  icloud mail messages list --query invoice --cursor cur_…
  icloud mail messages list --folder sent --json
  icloud mail messages list --input-json request.json --json

Flags:
      --after string             Include messages at or after an RFC3339 timestamp or YYYY-MM-DD UTC date
      --attachment-name string   Restrict attachment filename text
      --attachment-type string   Restrict attachment media type
      --before string            Include messages before an RFC3339 timestamp or YYYY-MM-DD UTC date
      --blocked                  Include only messages from exact local blocked sender addresses
      --cursor string            Continue from a next_cursor returned by the same query
      --flagged                  Include only messages with a flagged eligible location
      --folder string            Folder kind, ID, name, or wire name (default "inbox")
      --from string              Restrict sender or Reply-To text
  -h, --help                     help for list
      --input-json string        Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                     Emit one stable JSON success envelope
      --limit int                Maximum local folder messages to return (1-100) (default 20)
      --participant string       Restrict sender, Reply-To, To, or Cc participant text
      --query string             Match indexed subject, sender, participants, decoded text, attachments, or RFC Message-ID
      --read                     Include only messages without an unread eligible location
      --rfc-id string            Match one RFC Message-ID
      --subject string           Restrict subject text
      --unflagged                Include only messages without a flagged eligible location
      --unread                   Include only messages with an unread eligible location
      --vip                      Include only messages from exact local VIP sender addresses

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail move`

```text
Move exact current message locations

Usage:
  icloud mail move <message-id> [message-id...] [flags]

Examples:
  icloud mail move msg_… --from inbox --to Projects --dry-run
  icloud mail move msg_… --from inbox --to Projects --idempotency-key move-01 --yes
  icloud mail move msg_… msg_… --from inbox --to Projects --idempotency-key move-batch-01 --yes --jsonl

Flags:
      --dry-run                  Preview the exact location change without creating intent or contacting IMAP
      --from string              Select one exact current source folder by ID, name, wire name, or kind
  -h, --help                     help for move
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --input-jsonl string       Read up to 100 strict JSONL location items from a file, or - for stdin
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit typed per-item and completed JSONL events
      --mode string              Bulk failure mode: validate the bounded target set then fail fast, or continue-on-error (default "atomic")
      --to string                Select one current destination folder by ID, name, wire name, or kind
      --yes                      Confirm an intentional non-interactive location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail not-junk`

```text
Move current Junk locations back to Inbox

Usage:
  icloud mail not-junk <message-id> [message-id...] [flags]

Examples:
  icloud mail not-junk msg_… --dry-run
  icloud mail not-junk msg_… --idempotency-key not-junk-01 --yes

Flags:
      --dry-run                  Preview the exact location change without creating intent or contacting IMAP
      --from string              Select one exact current source folder by ID, name, wire name, or kind
  -h, --help                     help for not-junk
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --input-jsonl string       Read up to 100 strict JSONL location items from a file, or - for stdin
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit typed per-item and completed JSONL events
      --mode string              Bulk failure mode: validate the bounded target set then fail fast, or continue-on-error (default "atomic")
      --to string                Select one current destination folder by ID, name, wire name, or kind
      --yes                      Confirm an intentional non-interactive location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail operations`

```text
Inspect local mail operation history by stable ID

Usage:
  icloud mail operations [flags]
  icloud mail operations [command]

Examples:
  icloud mail operations outbound list
  icloud mail operations mutations show mut_…

Available Commands:
  mutations   Inspect message location mutation history
  outbound    Inspect outbound send attempts

Flags:
  -h, --help   help for operations

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail operations [command] --help" for more information about a command.
```

## `icloud mail operations mutations`

```text
Inspect message location mutation history

Usage:
  icloud mail operations mutations [flags]
  icloud mail operations mutations [command]

Examples:
  icloud mail operations mutations list
  icloud mail operations mutations show mut_…

Available Commands:
  list        List bounded local message location mutations
  show        Show one message location mutation by stable ID

Flags:
  -h, --help   help for mutations

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail operations mutations [command] --help" for more information about a command.
```

## `icloud mail operations mutations list`

```text
List bounded local message location mutations

Usage:
  icloud mail operations mutations list [flags]

Examples:
  icloud mail operations mutations list
  icloud mail operations mutations list --limit 50 --cursor mcur_… --json
  icloud mail operations mutations list --input-json request.json --json

Flags:
      --cursor string       Continue from a next_cursor returned by this operation list
  -h, --help                help for list
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum attempts to return (1-100) (default 20)

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail operations mutations show`

```text
Show one message location mutation by stable ID

Usage:
  icloud mail operations mutations show <mutation-id> [flags]

Examples:
  icloud mail operations mutations show mut_…
  icloud mail operations mutations show mut_… --json

Flags:
  -h, --help   help for show
      --json   Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail operations outbound`

```text
Inspect outbound send attempts

Usage:
  icloud mail operations outbound [flags]
  icloud mail operations outbound [command]

Examples:
  icloud mail operations outbound list
  icloud mail operations outbound show send_…

Available Commands:
  list             List bounded local outbound send attempts
  reconcile-answer Retry only the provider Answered-state write for one proven sent reply
  show             Show one outbound send attempt by stable ID

Flags:
  -h, --help   help for outbound

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail operations outbound [command] --help" for more information about a command.
```

## `icloud mail operations outbound list`

```text
List bounded local outbound send attempts

Usage:
  icloud mail operations outbound list [flags]

Examples:
  icloud mail operations outbound list
  icloud mail operations outbound list --limit 50 --cursor ocur_… --json
  icloud mail operations outbound list --input-json request.json --json

Flags:
      --cursor string       Continue from a next_cursor returned by this operation list
  -h, --help                help for list
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum attempts to return (1-100) (default 20)

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail operations outbound reconcile-answer`

```text
Retry only the provider Answered-state write for one proven sent reply

Usage:
  icloud mail operations outbound reconcile-answer <attempt-id> [flags]

Examples:
  icloud mail operations outbound reconcile-answer send_…
  icloud mail operations outbound reconcile-answer send_… --json

Flags:
  -h, --help   help for reconcile-answer
      --json   Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail operations outbound show`

```text
Show one outbound send attempt by stable ID

Usage:
  icloud mail operations outbound show <attempt-id> [flags]

Examples:
  icloud mail operations outbound show send_…
  icloud mail operations outbound show send_… --json

Flags:
  -h, --help   help for show
      --json   Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail outbox`

```text
Inspect and manually retry sends proven safe before SMTP DATA

Usage:
  icloud mail outbox [flags]
  icloud mail outbox [command]

Examples:
  icloud mail outbox list
  icloud mail outbox show out_…
  icloud mail outbox retry out_… --dry-run
  icloud mail outbox retry out_… --yes
  icloud mail outbox cancel out_…

Available Commands:
  cancel      Cancel one retryable local Outbox item without contacting SMTP
  list        List bounded local safe-retry Outbox items without exposing message payloads or recipients
  retry       Explicitly replay the exact reviewed raw message and private SMTP envelope
  show        Show one safe-retry Outbox item's lifecycle metadata

Flags:
  -h, --help   help for outbox

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail outbox [command] --help" for more information about a command.
```

## `icloud mail outbox cancel`

```text
Cancel one retryable local Outbox item without contacting SMTP

Usage:
  icloud mail outbox cancel <outbox-id> [flags]

Examples:
  icloud mail outbox cancel out_…
  icloud mail outbox cancel --input-json request.json --json

Flags:
  -h, --help                help for cancel
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail outbox list`

```text
List bounded local safe-retry Outbox items without exposing message payloads or recipients

Usage:
  icloud mail outbox list [flags]

Examples:
  icloud mail outbox list
  icloud mail outbox list --limit 50 --cursor obcur_… --json
  icloud mail outbox list --input-json request.json --json

Flags:
      --cursor string       Continue from a next_cursor returned for the same account
  -h, --help                help for list
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum Outbox items to return (1-100) (default 20)

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail outbox retry`

```text
Explicitly replay the exact reviewed raw message and private SMTP envelope

Usage:
  icloud mail outbox retry <outbox-id> [flags]

Examples:
  icloud mail outbox retry out_… --dry-run
  icloud mail outbox retry out_… --yes --json

Flags:
      --dry-run             Review retry lifecycle metadata without contacting SMTP
  -h, --help                help for retry
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --yes                 Confirm an intentional non-interactive exact-message replay

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail outbox show`

```text
Show one safe-retry Outbox item's lifecycle metadata

Usage:
  icloud mail outbox show <outbox-id> [flags]

Examples:
  icloud mail outbox show out_…
  icloud mail outbox show --input-json request.json --json

Flags:
  -h, --help                help for show
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail reply`

```text
Reply to one explicitly selected local message

Usage:
  icloud mail reply <msg_…> [flags]

Examples:
  icloud mail reply msg_… --body-file reply.txt --dry-run
  icloud mail reply msg_… --from mid_… --body 'Thanks' --idempotency-key reply-01 --yes

Flags:
      --attach stringArray               Regular file path to attach; repeat as needed
      --attach-cid stringArray           Set one attachment Content-ID as path=local@domain; repeat as needed
      --attach-disposition stringArray   Set one attachment disposition as path=attachment or path=inline; repeat as needed
      --attach-name stringArray          Override one attachment filename as path=filename; repeat as needed
      --attach-type stringArray          Override one attachment media type as path=type/subtype; repeat as needed
      --bcc stringArray                  Add a new Bcc envelope recipient; prior Bcc recipients are never inferred
      --body string                      Inline text body
      --body-file string                 Text body file path, or - for stdin
      --cc stringArray                   Add a Cc recipient; repeat as needed
      --dry-run                          Preview the resolved reply without creating an attempt or contacting SMTP
      --folder string                    Select the exact current provider parent location when the message has multiple locations
      --format string                    With --dry-run, output text, html, or raw preview content
      --from string                      Sender identity by stable mid_… ID or configured address (defaults to the account's local default identity)
      --header stringArray               Safe custom header as Name: Value; repeat as needed
      --headers                          With --dry-run, print normalized headers including RFC threading fields
  -h, --help                             help for reply
      --html-body string                 Inline HTML body
      --html-body-file string            HTML body file path, or - for stdin
      --idempotency-key string           Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string                Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                             Emit one stable JSON success envelope
      --replace-cc                       Replace derived Cc recipients with the addresses supplied by --cc
      --replace-to                       Replace derived To recipients with the addresses supplied by --to
      --retry-of string                  Link an explicit retry to one failed or unknown send_… attempt
      --retry-unknown                    Authorize one duplicate-risk retry of an unknown attempt; requires --retry-of and --yes
      --signature string                 Override the configured reply signature with one sig_… ID or none
      --to stringArray                   Add a To recipient; repeat or provide a comma-separated address list
      --verbose                          With --dry-run, include custom-header detail and HTML byte count
      --yes                              Confirm an intentional non-interactive reply

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail reply-all`

```text
Reply to all visible participants of one explicitly selected local message

Usage:
  icloud mail reply-all <msg_…> [flags]

Examples:
  icloud mail reply-all msg_… --body-file reply.txt --dry-run
  icloud mail reply-all msg_… --from mid_… --body 'Thanks' --idempotency-key reply-01 --yes

Flags:
      --attach stringArray               Regular file path to attach; repeat as needed
      --attach-cid stringArray           Set one attachment Content-ID as path=local@domain; repeat as needed
      --attach-disposition stringArray   Set one attachment disposition as path=attachment or path=inline; repeat as needed
      --attach-name stringArray          Override one attachment filename as path=filename; repeat as needed
      --attach-type stringArray          Override one attachment media type as path=type/subtype; repeat as needed
      --bcc stringArray                  Add a new Bcc envelope recipient; prior Bcc recipients are never inferred
      --body string                      Inline text body
      --body-file string                 Text body file path, or - for stdin
      --cc stringArray                   Add a Cc recipient; repeat as needed
      --dry-run                          Preview the resolved reply without creating an attempt or contacting SMTP
      --folder string                    Select the exact current provider parent location when the message has multiple locations
      --format string                    With --dry-run, output text, html, or raw preview content
      --from string                      Sender identity by stable mid_… ID or configured address (defaults to the account's local default identity)
      --header stringArray               Safe custom header as Name: Value; repeat as needed
      --headers                          With --dry-run, print normalized headers including RFC threading fields
  -h, --help                             help for reply-all
      --html-body string                 Inline HTML body
      --html-body-file string            HTML body file path, or - for stdin
      --idempotency-key string           Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string                Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                             Emit one stable JSON success envelope
      --replace-cc                       Replace derived Cc recipients with the addresses supplied by --cc
      --replace-to                       Replace derived To recipients with the addresses supplied by --to
      --retry-of string                  Link an explicit retry to one failed or unknown send_… attempt
      --retry-unknown                    Authorize one duplicate-risk retry of an unknown attempt; requires --retry-of and --yes
      --signature string                 Override the configured reply signature with one sig_… ID or none
      --to stringArray                   Add a To recipient; repeat or provide a comma-separated address list
      --verbose                          With --dry-run, include custom-header detail and HTML byte count
      --yes                              Confirm an intentional non-interactive reply

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail restore`

```text
Move current Trash locations to deliberate destinations

Usage:
  icloud mail restore <message-id> [message-id...] [flags]

Examples:
  icloud mail restore msg_… --to inbox --dry-run
  icloud mail restore msg_… --to inbox --idempotency-key restore-01 --yes

Flags:
      --dry-run                  Preview the exact location change without creating intent or contacting IMAP
      --from string              Select one exact current source folder by ID, name, wire name, or kind
  -h, --help                     help for restore
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --input-jsonl string       Read up to 100 strict JSONL location items from a file, or - for stdin
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit typed per-item and completed JSONL events
      --mode string              Bulk failure mode: validate the bounded target set then fail fast, or continue-on-error (default "atomic")
      --to string                Select one current destination folder by ID, name, wire name, or kind
      --yes                      Confirm an intentional non-interactive location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail search`

```text
Search synchronized mail locally or query bounded provider metadata explicitly

Usage:
  icloud mail search [query] [flags]
  icloud mail search [command]

Examples:
  icloud mail search invoice
  icloud mail search --from billing@example.com --unread
  icloud mail search launch --result threads --limit 20
  icloud mail search invoice --remote --folder inbox
  icloud mail search invoice --remote --folder inbox --cursor rcur_…
  icloud mail search invoice --remote --all-folders --cursor rcur_…
  icloud mail search --attachment-type application/pdf --json
  icloud mail search --input-json request.json --json

Available Commands:
  repair      Rebuild a bounded batch of incomplete local body-search entries

Flags:
      --after string             Include messages at or after an RFC3339 timestamp or YYYY-MM-DD UTC date
      --all-folders              Search every current selectable folder through bounded resumable fan-out
      --attachment-name string   Restrict attachment filename text
      --attachment-type string   Restrict attachment media type
      --before string            Include messages before an RFC3339 timestamp or YYYY-MM-DD UTC date
      --blocked                  Include only messages from exact local blocked sender addresses
      --cursor string            Continue from a next_cursor returned by the same query
      --flagged                  Include only messages with a flagged eligible location
      --folder string            Restrict matches to one folder kind, ID, name, or wire name
      --from string              Restrict sender or Reply-To text
  -h, --help                     help for search
      --input-json string        Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                     Emit one stable JSON success envelope
      --limit int                Maximum hits to return (1-100) (default 20)
      --participant string       Restrict sender, Reply-To, To, or Cc participant text
      --read                     Include only messages without an unread eligible location
      --remote                   Query bounded provider metadata instead of local synchronized state
      --result string            Result kind: messages or threads (default "messages")
      --rfc-id string            Match one RFC Message-ID
      --subject string           Restrict subject text
      --unflagged                Include only messages without a flagged eligible location
      --unread                   Include only messages with an unread eligible location
      --vip                      Include only messages from exact local VIP sender addresses

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail search [command] --help" for more information about a command.
```

## `icloud mail search repair`

```text
Rebuild a bounded batch of incomplete local body-search entries

Usage:
  icloud mail search repair [flags]

Examples:
  icloud mail search repair
  icloud mail search repair --limit 100 --json

Flags:
  -h, --help                help for repair
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum incomplete messages to repair (1-100) (default 20)

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail selection`

```text
Create and apply short-lived immutable reviewed mail target sets

Usage:
  icloud mail selection [flags]
  icloud mail selection [command]

Examples:
  icloud mail selection create msg_… msg_… --scope-folder inbox
  icloud mail selection create --search --unread --folder inbox --scope-folder inbox
  icloud mail selection show sel_…
  icloud mail selection apply sel_… --action archive --dry-run

Available Commands:
  apply       Read or mutate exactly the frozen MailSelection target set
  create      Freeze explicit stable targets or one bounded current local search page
  delete      Delete one local MailSelection without contacting iCloud
  show        Show immutable reviewed MailSelection membership without message content

Flags:
  -h, --help   help for selection

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail selection [command] --help" for more information about a command.
```

## `icloud mail selection apply`

```text
Read or mutate exactly the frozen MailSelection target set

Usage:
  icloud mail selection apply <selection-id> [flags]

Examples:
  icloud mail selection apply sel_… --action read --json
  icloud mail selection apply sel_… --action mark-read --dry-run
  icloud mail selection apply sel_… --action archive --dry-run
  icloud mail selection apply sel_… --action archive --request-hash <hash> --yes --jsonl

Flags:
      --action string         Reviewed action to apply to the frozen target set
      --dry-run               Review exact frozen targets/action and emit the required request hash without provider contact
  -h, --help                  help for apply
      --input-json string     Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                  Emit one stable JSON success envelope
      --jsonl                 Emit ordered per-item and completed JSONL events for writes
      --mode string           Failure mode: validate then fail fast, or continue-on-error (default "atomic")
      --request-hash string   Exact hash emitted by the matching --dry-run review
      --to string             Destination folder for move, copy, or restore; semantic actions resolve their normal destination
      --yes                   Confirm an intentional non-interactive location-changing selection apply

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail selection create`

```text
Freeze explicit stable targets or one bounded current local search page

Usage:
  icloud mail selection create [message-id|thread-id...] [flags]

Examples:
  icloud mail selection create msg_… thr_… --ttl 15m
  icloud mail selection create --search --query invoice --unread --folder inbox --scope-folder inbox --limit 50 --json

Flags:
      --after string             Include messages at or after an RFC3339 timestamp or YYYY-MM-DD UTC date
      --attachment-name string   Restrict attachment filename text
      --attachment-type string   Restrict attachment media type
      --before string            Include messages before an RFC3339 timestamp or YYYY-MM-DD UTC date
      --blocked                  Include only messages from exact local blocked sender addresses
      --cursor string            Continue from a next_cursor returned by the same query
      --flagged                  Include only messages with a flagged eligible location
      --folder string            Restrict matches to one folder kind, ID, name, or wire name
      --from string              Restrict sender or Reply-To text
  -h, --help                     help for create
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                     Emit the immutable selection and stable target evidence as JSON
      --limit int                Maximum local search hits before thread expansion (1-100) (default 20)
      --participant string       Restrict sender, Reply-To, To, or Cc participant text
      --query string             Match indexed subject, sender, participants, decoded text, attachments, or RFC Message-ID
      --read                     Include only messages without an unread eligible location
      --result string            Search source result kind: messages or threads (default "messages")
      --rfc-id string            Match one RFC Message-ID
      --scope-folder string      Freeze exact eligible current provider copies in one folder for later writes
      --search                   Create from one bounded current local search page instead of explicit IDs
      --subject string           Restrict subject text
      --ttl duration             Selection lifetime from creation (1m-24h) (default 15m0s)
      --unflagged                Include only messages without a flagged eligible location
      --unread                   Include only messages with an unread eligible location
      --vip                      Include only messages from exact local VIP sender addresses

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail selection delete`

```text
Delete one local MailSelection without contacting iCloud

Usage:
  icloud mail selection delete <selection-id> [flags]

Examples:
  icloud mail selection delete sel_…
  icloud mail selection delete --input-json selection.json --json

Flags:
  -h, --help                help for delete
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail selection show`

```text
Show immutable reviewed MailSelection membership without message content

Usage:
  icloud mail selection show <selection-id> [flags]

Examples:
  icloud mail selection show sel_…
  icloud mail selection show sel_… --json

Flags:
  -h, --help                help for show
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit selection metadata and stable target evidence as JSON

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail send`

```text
Preview or send text, HTML, and attachments safely and idempotently

Usage:
  icloud mail send [flags]

Examples:
  icloud mail send --to client@example.com --subject 'Update' --body-file message.txt --dry-run
  icloud mail send --to 'Client <client@example.com>' --subject 'Update' --body 'Hello' --attach report.pdf --yes --idempotency-key send-01
  icloud mail send --to client@example.com --subject 'Update' --body-file - --yes --json

Flags:
      --attach stringArray               Regular file path to attach; repeat as needed
      --attach-cid stringArray           Set one attachment Content-ID as path=local@domain; repeat as needed
      --attach-disposition stringArray   Set one attachment disposition as path=attachment or path=inline; repeat as needed
      --attach-name stringArray          Override one attachment filename as path=filename; repeat as needed
      --attach-type stringArray          Override one attachment media type as path=type/subtype; repeat as needed
      --bcc stringArray                  Bcc envelope recipient; omitted from message headers
      --body string                      Inline text body
      --body-file string                 Text body file path, or - for stdin
      --cc stringArray                   Cc recipient address; repeat as needed
      --dry-run                          Preview the normalized message without creating an attempt or contacting SMTP
      --format string                    With --dry-run, output text, html, or raw preview content
      --from string                      Sender identity by stable mid_… ID or configured address (defaults to the account's local default identity)
      --header stringArray               Safe custom header as Name: Value; repeat as needed
      --headers                          With --dry-run, print normalized safe header fields (Bcc remains omitted)
  -h, --help                             help for send
      --html-body string                 Inline HTML body
      --html-body-file string            HTML body file path, or - for stdin
      --idempotency-key string           Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string                Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                             Emit one stable JSON success envelope
      --reply-to string                  Optional Reply-To address
      --retry-of string                  Link an explicit retry to one failed or unknown send_… attempt
      --retry-unknown                    Authorize one duplicate-risk retry of an unknown attempt; requires --retry-of and --yes
      --signature string                 Override the configured signature with one sig_… ID or none
      --subject string                   Message subject
      --to stringArray                   Recipient address; repeat or provide a comma-separated address list
      --verbose                          With --dry-run, include normalized custom-header detail and HTML byte count
      --yes                              Confirm an intentional non-interactive send

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail senders`

```text
Manage local VIP and blocked sender preferences

Usage:
  icloud mail senders [command]

Available Commands:
  block       Mark one exact sender address as locally blocked
  list        List account-local sender preferences
  show        Show one exact local sender preference
  unblock     Clear the local blocked flag for one exact sender address
  unvip       Clear the local VIP flag for one exact sender address
  vip         Mark one exact sender address as a local VIP

Flags:
  -h, --help   help for senders

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail senders [command] --help" for more information about a command.
```

## `icloud mail senders block`

```text
Mark one exact sender address as locally blocked

Usage:
  icloud mail senders block <email> [flags]

Examples:
  icloud mail senders block alerts@example.com
  icloud mail senders block alerts@example.com --json

Flags:
  -h, --help                help for block
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail senders list`

```text
List account-local sender preferences

Usage:
  icloud mail senders list [flags]

Examples:
  icloud mail senders list
  icloud mail senders list --limit 50 --json

Flags:
      --cursor string       Continue from a next_cursor returned by the same account
  -h, --help                help for list
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum sender preferences to return (1-100) (default 20)

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail senders show`

```text
Show one exact local sender preference

Usage:
  icloud mail senders show <email> [flags]

Examples:
  icloud mail senders show alerts@example.com
  icloud mail senders show alerts@example.com --json

Flags:
  -h, --help                help for show
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail senders unblock`

```text
Clear the local blocked flag for one exact sender address

Usage:
  icloud mail senders unblock <email> [flags]

Examples:
  icloud mail senders unblock alerts@example.com
  icloud mail senders unblock alerts@example.com --json

Flags:
  -h, --help                help for unblock
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail senders unvip`

```text
Clear the local VIP flag for one exact sender address

Usage:
  icloud mail senders unvip <email> [flags]

Examples:
  icloud mail senders unvip alerts@example.com
  icloud mail senders unvip alerts@example.com --json

Flags:
  -h, --help                help for unvip
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail senders vip`

```text
Mark one exact sender address as a local VIP

Usage:
  icloud mail senders vip <email> [flags]

Examples:
  icloud mail senders vip alerts@example.com
  icloud mail senders vip alerts@example.com --json

Flags:
  -h, --help                help for vip
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail settings`

```text
Inspect local Mail settings and truthful provider compatibility

Usage:
  icloud mail settings [flags]
  icloud mail settings [command]

Available Commands:
  show        Show local defaults, bindings, preferences, and provider compatibility without network access

Flags:
  -h, --help   help for settings

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail settings [command] --help" for more information about a command.
```

## `icloud mail settings show`

```text
Show local defaults, bindings, preferences, and provider compatibility without network access

Usage:
  icloud mail settings show [flags]

Examples:
  icloud mail settings show
  icloud --account Personal mail settings show --verbose
  icloud mail settings show --json

Flags:
  -h, --help                help for show
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --verbose             Include short next actions for unsupported or unverified provider features

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail signatures`

```text
Manage local per-identity compose signatures

Usage:
  icloud mail signatures [command]

Available Commands:
  create      Create one account-local signature
  default     Show or set one operation-family signature default
  delete      Delete one local signature and defaults that reference it
  list        List local signature metadata without dumping signature bodies
  show        Show one local signature including its text and optional HTML body
  update      Update selected fields of one local signature

Flags:
  -h, --help   help for signatures

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail signatures [command] --help" for more information about a command.
```

## `icloud mail signatures create`

```text
Create one account-local signature

Usage:
  icloud mail signatures create [flags]

Examples:
  icloud mail signatures create --text-file signature.txt
  icloud mail signatures create --identity mid_… --text 'Thanks,
Alex' --json

Flags:
  -h, --help                help for create
      --html string         Optional inline HTML signature body
      --html-file string    Optional HTML signature file path, or - for stdin
      --identity string     Optionally bind this signature to one mail identity (use none on update to clear the binding)
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --text string         Inline plain-text signature body
      --text-file string    Plain-text signature file path, or - for stdin

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail signatures default`

```text
Show or set one operation-family signature default

Usage:
  icloud mail signatures default <new|reply|reply-all|forward> [signature-id|none] [flags]

Examples:
  icloud mail signatures default new sig_…
  icloud mail signatures default reply none --identity mid_…
  icloud mail signatures default forward --json

Flags:
  -h, --help                help for default
      --identity string     Optional identity scope by stable mid_… ID or configured address; omit for account-wide default
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail signatures delete`

```text
Delete one local signature and defaults that reference it

Usage:
  icloud mail signatures delete <signature-id> [flags]

Examples:
  icloud mail signatures delete sig_…
  icloud mail signatures delete sig_… --json

Flags:
  -h, --help                help for delete
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail signatures list`

```text
List local signature metadata without dumping signature bodies

Usage:
  icloud mail signatures list [flags]

Examples:
  icloud mail signatures list
  icloud mail signatures list --limit 50 --json

Flags:
      --cursor string       Continue from a next_cursor returned by the same account
  -h, --help                help for list
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum signatures to return (1-100) (default 20)

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail signatures show`

```text
Show one local signature including its text and optional HTML body

Usage:
  icloud mail signatures show <signature-id> [flags]

Examples:
  icloud mail signatures show sig_…
  icloud mail signatures show sig_… --json

Flags:
  -h, --help                help for show
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail signatures update`

```text
Update selected fields of one local signature

Usage:
  icloud mail signatures update <signature-id> [flags]

Examples:
  icloud mail signatures update sig_… --text-file signature.txt
  icloud mail signatures update sig_… --identity none --json

Flags:
  -h, --help                help for update
      --html string         Optional inline HTML signature body
      --html-file string    Optional HTML signature file path, or - for stdin
      --identity string     Optionally bind this signature to one mail identity (use none on update to clear the binding)
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --text string         Inline plain-text signature body
      --text-file string    Plain-text signature file path, or - for stdin

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail status`

```text
Compatibility alias for `mail operations … list`

Usage:
  icloud mail status [flags]

Examples:
  icloud mail operations outbound list
  icloud mail operations mutations list
  icloud mail operations mutations list --limit 50 --json
  icloud mail status --input-json request.json --json

Flags:
      --cursor string       Continue from a next_cursor returned by the same status mode
  -h, --help                help for status
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum attempts to return (1-100) (default 20)
      --mutations           List recent message location mutation intents and outcomes
      --outbound            List recent outbound send attempts

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail sync`

```text
Fetch and atomically commit bounded pages from enabled mail folders

Usage:
  icloud mail sync [flags]
  icloud mail sync [command]

Examples:
  icloud mail sync
  icloud mail sync --folder sent
  icloud mail sync --hydrate rem_…
  icloud mail sync --all-folders --continue-on-error --jsonl
  icloud mail sync --limit 50 --json
  icloud mail sync status --folder sent

Available Commands:
  backfill    Resume one bounded historical page without moving the new-mail frontier
  checkpoints List bounded local synchronization checkpoints
  rebaseline  Move one forward sync frontier to the verified current iCloud tail without deleting cache
  repair      Audit bounded local mail consistency and apply only deterministic repairs
  status      Show one local folder synchronization frontier

Flags:
      --all-folders          Explicitly sync every current selectable folder, including locally disabled folders, with bounded recent bootstrap
      --continue-on-error    Continue independent hydration items or folders after an error; committed earlier work is never rolled back
      --folder string        Sync one enabled folder by kind, ID, name, or wire name instead of all enabled folders
  -h, --help                 help for sync
      --hydrate strings      Cache one or more comma-separated rem_… references without advancing folder checkpoints
      --input-json string    Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                 Emit one stable JSON success envelope
      --jsonl                Emit typed per-folder or per-hydration-item JSONL events plus a completed event
      --limit int            Maximum messages to fetch and commit per folder (1-100) (default 20)
      --max-messages int     Maximum fetched messages for --until-current (1-10000) (default 1000)
      --max-pages int        Maximum committed sync pages for --until-current (1-1000) (default 20)
      --reconcile-outbound   Sync Sent and reconcile recent sent/unknown attempts by exact Message-ID evidence
      --until-current        Repeat bounded sync pages until current or a global drain ceiling is reached

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail sync [command] --help" for more information about a command.
```

## `icloud mail sync backfill`

```text
Resume one bounded historical page without moving the new-mail frontier

Usage:
  icloud mail sync backfill [flags]

Examples:
  icloud mail sync backfill --folder inbox
  icloud mail sync backfill --folder Receipts --after 2025-01-01 --before 2026-01-01 --limit 50
  icloud mail sync backfill --folder fld_… --json

Flags:
      --after string        Include messages on or after this UTC date (YYYY-MM-DD)
      --before string       Include messages before this UTC date (YYYY-MM-DD)
      --folder string       Folder kind, ID, name, or wire name (default "inbox")
  -h, --help                help for backfill
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --jsonl               Emit one event per committed historical page plus a completed event
      --limit int           Maximum historical messages to fetch and commit in this page (1-100) (default 20)
      --max-messages int    Maximum fetched historical messages for --until-complete (1-10000) (default 1000)
      --max-pages int       Maximum committed backfill pages for --until-complete (1-1000) (default 20)
      --until-complete      Repeat bounded historical pages until complete or a global drain ceiling is reached

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail sync checkpoints`

```text
List bounded local synchronization checkpoints

Usage:
  icloud mail sync checkpoints [flags]

Examples:
  icloud mail sync checkpoints
  icloud mail sync checkpoints --json
  icloud mail sync checkpoints --input-json request.json --json

Flags:
      --cursor string       Continue from a next_cursor returned for the same account
  -h, --help                help for checkpoints
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum checkpoints to return (1-100) (default 20)

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail sync rebaseline`

```text
Move one forward sync frontier to the verified current iCloud tail without deleting cache

Usage:
  icloud mail sync rebaseline [flags]

Examples:
  icloud mail sync rebaseline --folder inbox --current-tail
  icloud mail sync rebaseline --folder fld_… --current-tail --json

Flags:
      --current-tail        Acknowledge moving only this forward frontier to UIDNEXT-1 observed now
      --folder string       Folder kind, ID, name, or wire name (default "inbox")
  -h, --help                help for rebaseline
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail sync repair`

```text
Audit bounded local mail consistency and apply only deterministic repairs

Usage:
  icloud mail sync repair [flags]

Examples:
  icloud mail sync repair --check
  icloud mail sync repair --check --verbose
  icloud mail sync repair --apply
  icloud mail sync repair --apply --cleanup-orphans --yes

Flags:
      --apply               Apply only deterministic repairs described by the audit
      --check               Audit only (the default when neither mode flag is supplied)
      --cleanup-orphans     With --apply --yes, delete bounded orphan files from the private staging directory
  -h, --help                help for repair
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope with bounded findings
      --limit int           Maximum findings or repair candidates per bounded category (1-100) (default 20)
      --verbose             Print bounded per-finding detail before the compact summary
      --yes                 Authorize destructive orphan staging-file cleanup

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail sync status`

```text
Show one local folder synchronization frontier

Usage:
  icloud mail sync status [flags]

Examples:
  icloud mail sync status
  icloud mail sync status --folder sent --json
  icloud mail sync status --input-json request.json --json

Flags:
      --folder string       Folder kind, ID, name, or wire name (default "inbox")
  -h, --help                help for status
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail thread`

```text
Compatibility namespace for `mail threads show`

Usage:
  icloud mail thread [flags]
  icloud mail thread [command]

Examples:
  icloud mail threads show thr_…
  icloud mail threads show msg_… --full

Available Commands:
  show        Compatibility alias for `mail threads show`

Flags:
  -h, --help   help for thread

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail thread [command] --help" for more information about a command.
```

## `icloud mail thread show`

```text
Compatibility alias for `mail threads show`

Usage:
  icloud mail thread show <thread-id|message-id> [flags]

Examples:
  icloud mail threads show thr_…
  icloud mail threads show msg_… --full
  icloud mail threads show thr_… --limit 20 --cursor cur_…
  icloud mail threads show thr_… --format html --json
  icloud mail threads show thr_… --headers --verbose

Flags:
      --body-only           Write only chronological decoded message bodies
      --cursor string       Continue from a next_cursor returned by an earlier page
      --format string       Content format: compact, text, html, or raw (raw requires --json) (default "compact")
      --full                Include complete decoded text for every selected message
      --headers             Include decoded public headers for every selected message
  -h, --help                help for show
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum chronological messages to return (1-100) (default 20)
      --metadata-only       Show thread and message metadata without excerpts or bodies
      --verbose             Include MIME part metadata and RFC graph edge evidence

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads`

```text
List, read, and change locally reconstructed conversations

Usage:
  icloud mail threads [flags]
  icloud mail threads [command]

Examples:
  icloud mail threads list
  icloud mail threads show thr_…
  icloud mail threads mark-read thr_… --folder inbox
  icloud mail threads archive thr_… --from inbox --dry-run

Available Commands:
  archive     Apply archive to exact locations for one canonical thread page
  copy        Apply copy to exact locations for one canonical thread page
  delete      Apply delete to exact locations for one canonical thread page
  flag        Flag selected locations for a canonical thread page
  junk        Apply junk to exact locations for one canonical thread page
  list        List and filter the newest reconstructed conversations
  mark-read   Mark selected locations for a canonical thread page as read
  mark-unread Mark selected locations for a canonical thread page as unread
  move        Apply move to exact locations for one canonical thread page
  mute        Mute one reconstructed conversation locally
  not-junk    Apply not-junk to exact locations for one canonical thread page
  restore     Apply restore to exact locations for one canonical thread page
  show        Show a bounded chronological page from a reconstructed conversation
  trash       Apply trash to exact locations for one canonical thread page
  unarchive   Apply unarchive to exact locations for one canonical thread page
  unflag      Remove flags from selected locations for a canonical thread page
  unmute      Clear local mute state for one reconstructed conversation

Flags:
  -h, --help   help for threads

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)

Use "icloud mail threads [command] --help" for more information about a command.
```

## `icloud mail threads archive`

```text
Apply archive to exact locations for one canonical thread page

Usage:
  icloud mail threads archive <thread-id|message-id> [flags]

Examples:
  icloud mail threads archive thr_… --dry-run
  icloud mail threads archive thr_… --idempotency-key thread-archive-01 --yes

Flags:
      --cursor string            Continue a previously paged thread mutation from its next_cursor
      --dry-run                  Preview every exact per-message location change without creating intents or contacting IMAP
      --from string              Select one exact current source folder for every canonical thread member
  -h, --help                     help for archive
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit ordered per-message and completed JSONL events
      --limit int                Maximum chronological thread members to mutate in this page (1-100) (default 100)
      --mode string              Failure mode for this bounded thread page: fail fast or continue-on-error (default "atomic")
      --page                     Explicitly allow a partial first page when the canonical thread exceeds the bounded limit
      --to string                Select one current destination folder for every canonical thread member
      --yes                      Confirm an intentional non-interactive thread location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads copy`

```text
Apply copy to exact locations for one canonical thread page

Usage:
  icloud mail threads copy <thread-id|message-id> [flags]

Examples:
  icloud mail threads copy thr_… --from inbox --to Projects --dry-run
  icloud mail threads copy thr_… --from inbox --to Projects --idempotency-key thread-copy-01 --yes

Flags:
      --cursor string            Continue a previously paged thread mutation from its next_cursor
      --dry-run                  Preview every exact per-message location change without creating intents or contacting IMAP
      --from string              Select one exact current source folder for every canonical thread member
  -h, --help                     help for copy
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit ordered per-message and completed JSONL events
      --limit int                Maximum chronological thread members to mutate in this page (1-100) (default 100)
      --mode string              Failure mode for this bounded thread page: fail fast or continue-on-error (default "atomic")
      --page                     Explicitly allow a partial first page when the canonical thread exceeds the bounded limit
      --to string                Select one current destination folder for every canonical thread member
      --yes                      Confirm an intentional non-interactive thread location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads delete`

```text
Apply delete to exact locations for one canonical thread page

Usage:
  icloud mail threads delete <thread-id|message-id> [flags]

Examples:
  icloud mail threads delete thr_… --from trash --dry-run
  icloud mail threads delete thr_… --from trash --idempotency-key thread-delete-01 --yes

Flags:
      --cursor string            Continue a previously paged thread mutation from its next_cursor
      --dry-run                  Preview every exact per-message location change without creating intents or contacting IMAP
      --from string              Select one exact current source folder for every canonical thread member
  -h, --help                     help for delete
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit ordered per-message and completed JSONL events
      --limit int                Maximum chronological thread members to mutate in this page (1-100) (default 100)
      --mode string              Failure mode for this bounded thread page: fail fast or continue-on-error (default "atomic")
      --page                     Explicitly allow a partial first page when the canonical thread exceeds the bounded limit
      --to string                Select one current destination folder for every canonical thread member
      --yes                      Confirm an intentional non-interactive thread location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads flag`

```text
Flag selected locations for a canonical thread page

Usage:
  icloud mail threads flag <thread-id|message-id> [flags]

Examples:
  icloud mail threads flag thr_… --folder inbox
  icloud mail threads flag thr_… --folder inbox --page --jsonl

Flags:
      --cursor string       Continue a previously paged thread mutation from its next_cursor
      --folder string       Select one exact folder ID, name, wire name, or kind for every canonical thread member
  -h, --help                help for flag
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --jsonl               Emit ordered per-message and completed JSONL events
      --limit int           Maximum chronological thread members to mutate in this page (1-100) (default 100)
      --mode string         Failure mode for this bounded thread page: fail fast or continue-on-error (default "atomic")
      --page                Explicitly allow a partial first page when the canonical thread exceeds the bounded limit

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads junk`

```text
Apply junk to exact locations for one canonical thread page

Usage:
  icloud mail threads junk <thread-id|message-id> [flags]

Examples:
  icloud mail threads junk thr_… --dry-run
  icloud mail threads junk thr_… --idempotency-key thread-junk-01 --yes

Flags:
      --cursor string            Continue a previously paged thread mutation from its next_cursor
      --dry-run                  Preview every exact per-message location change without creating intents or contacting IMAP
      --from string              Select one exact current source folder for every canonical thread member
  -h, --help                     help for junk
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit ordered per-message and completed JSONL events
      --limit int                Maximum chronological thread members to mutate in this page (1-100) (default 100)
      --mode string              Failure mode for this bounded thread page: fail fast or continue-on-error (default "atomic")
      --page                     Explicitly allow a partial first page when the canonical thread exceeds the bounded limit
      --to string                Select one current destination folder for every canonical thread member
      --yes                      Confirm an intentional non-interactive thread location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads list`

```text
List and filter the newest reconstructed conversations

Usage:
  icloud mail threads list [flags]

Examples:
  icloud mail threads list
  icloud mail threads list --query launch --unread
  icloud mail threads list --participant teammate@example.com
  icloud mail threads list --cursor cur_… --json
  icloud mail threads list --input-json request.json --json

Flags:
      --after string             Include messages at or after an RFC3339 timestamp or YYYY-MM-DD UTC date
      --attachment-name string   Restrict attachment filename text
      --attachment-type string   Restrict attachment media type
      --before string            Include messages before an RFC3339 timestamp or YYYY-MM-DD UTC date
      --blocked                  Include only messages from exact local blocked sender addresses
      --cursor string            Continue from a next_cursor returned by the same query
      --flagged                  Include only messages with a flagged eligible location
      --folder string            Restrict matches to one folder kind, ID, name, or wire name
      --from string              Restrict sender or Reply-To text
  -h, --help                     help for list
      --input-json string        Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                     Emit one stable JSON success envelope
      --limit int                Maximum reconstructed conversations to return (1-100) (default 20)
      --participant string       Restrict sender, Reply-To, To, or Cc participant text
      --query string             Match indexed subject, sender, participants, decoded text, attachments, or RFC Message-ID
      --read                     Include only messages without an unread eligible location
      --rfc-id string            Match one RFC Message-ID
      --subject string           Restrict subject text
      --unflagged                Include only messages without a flagged eligible location
      --unread                   Include only messages with an unread eligible location
      --vip                      Include only messages from exact local VIP sender addresses

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads mark-read`

```text
Mark selected locations for a canonical thread page as read

Usage:
  icloud mail threads mark-read <thread-id|message-id> [flags]

Examples:
  icloud mail threads mark-read thr_… --folder inbox
  icloud mail threads mark-read thr_… --folder inbox --page --jsonl

Flags:
      --cursor string       Continue a previously paged thread mutation from its next_cursor
      --folder string       Select one exact folder ID, name, wire name, or kind for every canonical thread member
  -h, --help                help for mark-read
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --jsonl               Emit ordered per-message and completed JSONL events
      --limit int           Maximum chronological thread members to mutate in this page (1-100) (default 100)
      --mode string         Failure mode for this bounded thread page: fail fast or continue-on-error (default "atomic")
      --page                Explicitly allow a partial first page when the canonical thread exceeds the bounded limit

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads mark-unread`

```text
Mark selected locations for a canonical thread page as unread

Usage:
  icloud mail threads mark-unread <thread-id|message-id> [flags]

Examples:
  icloud mail threads mark-unread thr_… --folder inbox
  icloud mail threads mark-unread thr_… --folder inbox --page --jsonl

Flags:
      --cursor string       Continue a previously paged thread mutation from its next_cursor
      --folder string       Select one exact folder ID, name, wire name, or kind for every canonical thread member
  -h, --help                help for mark-unread
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --jsonl               Emit ordered per-message and completed JSONL events
      --limit int           Maximum chronological thread members to mutate in this page (1-100) (default 100)
      --mode string         Failure mode for this bounded thread page: fail fast or continue-on-error (default "atomic")
      --page                Explicitly allow a partial first page when the canonical thread exceeds the bounded limit

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads move`

```text
Apply move to exact locations for one canonical thread page

Usage:
  icloud mail threads move <thread-id|message-id> [flags]

Examples:
  icloud mail threads move thr_… --from inbox --to Projects --dry-run
  icloud mail threads move thr_… --from inbox --to Projects --idempotency-key thread-move-01 --yes

Flags:
      --cursor string            Continue a previously paged thread mutation from its next_cursor
      --dry-run                  Preview every exact per-message location change without creating intents or contacting IMAP
      --from string              Select one exact current source folder for every canonical thread member
  -h, --help                     help for move
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit ordered per-message and completed JSONL events
      --limit int                Maximum chronological thread members to mutate in this page (1-100) (default 100)
      --mode string              Failure mode for this bounded thread page: fail fast or continue-on-error (default "atomic")
      --page                     Explicitly allow a partial first page when the canonical thread exceeds the bounded limit
      --to string                Select one current destination folder for every canonical thread member
      --yes                      Confirm an intentional non-interactive thread location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads mute`

```text
Mute one reconstructed conversation locally

Usage:
  icloud mail threads mute <thread-id|message-id> [flags]

Examples:
  icloud mail threads mute thr_…
  icloud mail threads mute msg_… --json

Flags:
  -h, --help                help for mute
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads not-junk`

```text
Apply not-junk to exact locations for one canonical thread page

Usage:
  icloud mail threads not-junk <thread-id|message-id> [flags]

Examples:
  icloud mail threads not-junk thr_… --dry-run
  icloud mail threads not-junk thr_… --idempotency-key thread-not-junk-01 --yes

Flags:
      --cursor string            Continue a previously paged thread mutation from its next_cursor
      --dry-run                  Preview every exact per-message location change without creating intents or contacting IMAP
      --from string              Select one exact current source folder for every canonical thread member
  -h, --help                     help for not-junk
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit ordered per-message and completed JSONL events
      --limit int                Maximum chronological thread members to mutate in this page (1-100) (default 100)
      --mode string              Failure mode for this bounded thread page: fail fast or continue-on-error (default "atomic")
      --page                     Explicitly allow a partial first page when the canonical thread exceeds the bounded limit
      --to string                Select one current destination folder for every canonical thread member
      --yes                      Confirm an intentional non-interactive thread location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads restore`

```text
Apply restore to exact locations for one canonical thread page

Usage:
  icloud mail threads restore <thread-id|message-id> [flags]

Examples:
  icloud mail threads restore thr_… --dry-run
  icloud mail threads restore thr_… --idempotency-key thread-restore-01 --yes

Flags:
      --cursor string            Continue a previously paged thread mutation from its next_cursor
      --dry-run                  Preview every exact per-message location change without creating intents or contacting IMAP
      --from string              Select one exact current source folder for every canonical thread member
  -h, --help                     help for restore
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit ordered per-message and completed JSONL events
      --limit int                Maximum chronological thread members to mutate in this page (1-100) (default 100)
      --mode string              Failure mode for this bounded thread page: fail fast or continue-on-error (default "atomic")
      --page                     Explicitly allow a partial first page when the canonical thread exceeds the bounded limit
      --to string                Select one current destination folder for every canonical thread member
      --yes                      Confirm an intentional non-interactive thread location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads show`

```text
Show a bounded chronological page from a reconstructed conversation

Usage:
  icloud mail threads show <thread-id|message-id> [flags]

Examples:
  icloud mail threads show thr_…
  icloud mail threads show msg_… --full
  icloud mail threads show thr_… --limit 20 --cursor cur_…
  icloud mail threads show thr_… --format html --json
  icloud mail threads show thr_… --headers --verbose
  icloud mail threads show --input-json request.json --json

Flags:
      --body-only           Write only chronological decoded message bodies
      --cursor string       Continue from a next_cursor returned by an earlier page
      --format string       Content format: compact, text, html, or raw (raw requires --json) (default "compact")
      --full                Include complete decoded text for every selected message
      --headers             Include decoded public headers for every selected message
  -h, --help                help for show
      --input-json string   Read one strict JSON read request from a file, or - for stdin; cannot be mixed with equivalent read flags or positional read arguments
      --json                Emit one stable JSON success envelope
      --limit int           Maximum chronological messages to return (1-100) (default 20)
      --metadata-only       Show thread and message metadata without excerpts or bodies
      --verbose             Include MIME part metadata and RFC graph edge evidence

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads trash`

```text
Apply trash to exact locations for one canonical thread page

Usage:
  icloud mail threads trash <thread-id|message-id> [flags]

Examples:
  icloud mail threads trash thr_… --dry-run
  icloud mail threads trash thr_… --idempotency-key thread-trash-01 --yes

Flags:
      --cursor string            Continue a previously paged thread mutation from its next_cursor
      --dry-run                  Preview every exact per-message location change without creating intents or contacting IMAP
      --from string              Select one exact current source folder for every canonical thread member
  -h, --help                     help for trash
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit ordered per-message and completed JSONL events
      --limit int                Maximum chronological thread members to mutate in this page (1-100) (default 100)
      --mode string              Failure mode for this bounded thread page: fail fast or continue-on-error (default "atomic")
      --page                     Explicitly allow a partial first page when the canonical thread exceeds the bounded limit
      --to string                Select one current destination folder for every canonical thread member
      --yes                      Confirm an intentional non-interactive thread location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads unarchive`

```text
Apply unarchive to exact locations for one canonical thread page

Usage:
  icloud mail threads unarchive <thread-id|message-id> [flags]

Examples:
  icloud mail threads unarchive thr_… --dry-run
  icloud mail threads unarchive thr_… --idempotency-key thread-unarchive-01 --yes

Flags:
      --cursor string            Continue a previously paged thread mutation from its next_cursor
      --dry-run                  Preview every exact per-message location change without creating intents or contacting IMAP
      --from string              Select one exact current source folder for every canonical thread member
  -h, --help                     help for unarchive
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit ordered per-message and completed JSONL events
      --limit int                Maximum chronological thread members to mutate in this page (1-100) (default 100)
      --mode string              Failure mode for this bounded thread page: fail fast or continue-on-error (default "atomic")
      --page                     Explicitly allow a partial first page when the canonical thread exceeds the bounded limit
      --to string                Select one current destination folder for every canonical thread member
      --yes                      Confirm an intentional non-interactive thread location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads unflag`

```text
Remove flags from selected locations for a canonical thread page

Usage:
  icloud mail threads unflag <thread-id|message-id> [flags]

Examples:
  icloud mail threads unflag thr_… --folder inbox
  icloud mail threads unflag thr_… --folder inbox --page --jsonl

Flags:
      --cursor string       Continue a previously paged thread mutation from its next_cursor
      --folder string       Select one exact folder ID, name, wire name, or kind for every canonical thread member
  -h, --help                help for unflag
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope
      --jsonl               Emit ordered per-message and completed JSONL events
      --limit int           Maximum chronological thread members to mutate in this page (1-100) (default 100)
      --mode string         Failure mode for this bounded thread page: fail fast or continue-on-error (default "atomic")
      --page                Explicitly allow a partial first page when the canonical thread exceeds the bounded limit

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail threads unmute`

```text
Clear local mute state for one reconstructed conversation

Usage:
  icloud mail threads unmute <thread-id|message-id> [flags]

Examples:
  icloud mail threads unmute thr_…
  icloud mail threads unmute msg_… --json

Flags:
  -h, --help                help for unmute
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail trash`

```text
Move exact current locations to the semantic Trash folder

Usage:
  icloud mail trash <message-id> [message-id...] [flags]

Examples:
  icloud mail trash msg_… --dry-run
  icloud mail trash msg_… --from inbox --idempotency-key trash-01 --yes

Flags:
      --dry-run                  Preview the exact location change without creating intent or contacting IMAP
      --from string              Select one exact current source folder by ID, name, wire name, or kind
  -h, --help                     help for trash
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --input-jsonl string       Read up to 100 strict JSONL location items from a file, or - for stdin
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit typed per-item and completed JSONL events
      --mode string              Bulk failure mode: validate the bounded target set then fail fast, or continue-on-error (default "atomic")
      --to string                Select one current destination folder by ID, name, wire name, or kind
      --yes                      Confirm an intentional non-interactive location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail unarchive`

```text
Move current Archive locations to Inbox

Usage:
  icloud mail unarchive <message-id> [message-id...] [flags]

Examples:
  icloud mail unarchive msg_… --dry-run
  icloud mail unarchive msg_… --idempotency-key unarchive-01 --yes

Flags:
      --dry-run                  Preview the exact location change without creating intent or contacting IMAP
      --from string              Select one exact current source folder by ID, name, wire name, or kind
  -h, --help                     help for unarchive
      --idempotency-key string   Reuse the original operation when the same normalized request and idempotency key are repeated
      --input-json string        Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --input-jsonl string       Read up to 100 strict JSONL location items from a file, or - for stdin
      --json                     Emit one stable JSON success envelope
      --jsonl                    Emit typed per-item and completed JSONL events
      --mode string              Bulk failure mode: validate the bounded target set then fail fast, or continue-on-error (default "atomic")
      --to string                Select one current destination folder by ID, name, wire name, or kind
      --yes                      Confirm an intentional non-interactive location change

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail unflag`

```text
Remove the flag from one selected message location

Usage:
  icloud mail unflag <message-id> [message-id...] [flags]

Examples:
  icloud mail unflag msg_…
  icloud mail unflag msg_… --folder inbox

Flags:
      --folder string        Select one exact folder ID, name, wire name, or kind for every positional/JSON item
  -h, --help                 help for unflag
      --input-json string    Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --input-jsonl string   Read up to 100 strict JSONL items with message_id and optional folder from a file, or - for stdin
      --json                 Emit one stable JSON success envelope
      --jsonl                Emit typed per-item and completed JSONL events
      --mode string          Bulk failure mode: validate the bounded input then fail fast on target/provider errors, or continue-on-error (default "atomic")

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail unsubscribe`

```text
Safely execute a standards-based mailing-list unsubscribe action

Usage:
  icloud mail unsubscribe <msg_...> [flags]

Examples:
  icloud mail unsubscribe msg_… --dry-run
  icloud mail unsubscribe msg_… --yes
  icloud mail unsubscribe msg_… --method mailto:2 --yes --json
  icloud mail unsubscribe --input-json request.json --dry-run --json

Flags:
      --dry-run             Review only local sanitized unsubscribe capability; do not verify DKIM, POST, or send mail
  -h, --help                help for unsubscribe
      --input-json string   Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments
      --json                Emit one stable JSON envelope
      --method string       Safe method ID from dry-run, for example one-click or mailto:2
      --yes                 Confirm an intentional non-interactive unsubscribe action

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud mail watch`

```text
Keep enabled iCloud Mail folders fresh in the foreground. The command uses IMAP IDLE for high-priority enabled folders when supported, falls back explicitly to polling otherwise, periodically refreshes folder state, and stops on Ctrl-C. --timeout bounds each provider/sync attempt rather than the lifetime of this foreground stream.

Usage:
  icloud mail watch [flags]

Examples:
  icloud mail watch
  icloud mail watch --all-accounts
  icloud mail watch --poll-interval 2m --reconcile-interval 15m
  icloud mail watch --jsonl

Flags:
      --all-accounts                  Watch all configured accounts (maximum 100 workers)
  -h, --help                          help for watch
      --jsonl                         Emit one typed JSON object per lifecycle event
      --limit int                     Maximum messages committed per folder sync (1-100) (default 20)
      --poll-interval duration        Polling interval when IDLE is unavailable (default 1m0s)
      --reconcile-interval duration   Interval for full enabled-folder and folder-state reconciliation (default 15m0s)
      --verbose                       Include idle and unchanged synchronization lifecycle detail

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud man`

```text
Generate man pages from the current command tree

Usage:
  icloud man [flags]

Examples:
  icloud man > icloud.1
  icloud man --output-dir ./man

Flags:
  -h, --help                help for man
      --output-dir string   Write the complete man-page tree to this directory

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```

## `icloud version`

```text
Print the installed CLI version

Usage:
  icloud version [flags]

Examples:
  icloud version
  icloud version --json

Flags:
  -h, --help   help for version
      --json   Emit one stable JSON success envelope

Global Flags:
      --account string      Select an account by name, email, or stable ID
      --home string         Override the iCloud application data directory (otherwise ICLOUD_HOME or the platform default)
      --no-input            Never prompt for interactive input
      --request-id string   Attach a stable request ID to structured output
      --timeout duration    Maximum normal command duration; watch applies it per provider/sync attempt (default 30s)
```
