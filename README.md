# icloud

`icloud` is a local-first command-line client for iCloud services. The project is being built for agent workflows without making the normal terminal experience noisy: compact plain text is the default, and structured or expanded output is always explicit.

The current implementation provides secure multi-account and credential lifecycle, read-only live iCloud compatibility testing, deliberate text/HTML and filesystem-attachment sending, exact-message reply/reply-all, exact-message forwarding, durable all-folder discovery, bounded forward synchronization, foreground IDLE/polling watch, resumable historical backfill, explicit current-tail rebaseline, check-first repair, indexed offline mail search, explicit bounded remote search and targeted hydration, local RFC conversation reconstruction, complete local message reads, safe attachment export, explicit read/flag updates, and exact provider-location move/copy/archive/trash/restore/delete workflows. It can validate IMAP/SMTP authentication, preview outbound or location-changing requests without network side effects, require interactive confirmation or non-TTY `--yes`, stream standards-compliant MIME, keep historical and forward sync progress independent, preserve cached mail during rebaseline, repair only deterministic consistency problems, report ambiguous identity or canonical-blob problems without guessing, persist idempotent outbound and location-mutation intents, distinguish proven outcomes from unresolved provider state, preserve one logical message across independent folder locations, prefer atomic UID MOVE, fall back only to safe COPY plus UID EXPUNGE, record destination UID mappings, avoid archiving Sent copies, delete one exact provider location without purging another copy, and expose bounded recovery status when a provider write may have partially completed.

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
icloud mail folders create <name> [--dry-run|--yes] [--json]
icloud mail folders rename <selector> <new-name> [--dry-run|--yes] [--json]
icloud mail folders delete <selector> [--allow-non-empty] [--dry-run|--yes] [--json]
icloud mail folders subscribe <selector> [--dry-run|--yes] [--json]
icloud mail folders unsubscribe <selector> [--dry-run|--yes] [--json]
icloud mail sync [--folder <selector>|--all-folders] [--limit <n>] [--json]
icloud mail watch [--all-accounts] [--poll-interval <duration>] [--reconcile-interval <duration>] [--limit <n>] [--verbose] [--jsonl]
icloud mail sync --reconcile-outbound [--limit <n>] [--json]
icloud mail sync --hydrate <rem_…[,rem_…]> [--json]
icloud mail sync backfill --folder <selector> [--after YYYY-MM-DD] [--before YYYY-MM-DD] [--limit <n>] [--json]
icloud mail sync rebaseline --folder <selector> --current-tail [--json]
icloud mail sync repair [--check|--apply] [--cleanup-orphans --yes] [--limit <n>] [--verbose] [--json]
icloud mail inbox [--folder <selector>] [--query <text>] [filters] [--cursor <cursor>] [--limit <n>] [--json]
icloud mail search [query] [filters] [--result messages|threads] [--cursor <cursor>] [--limit <n>] [--json]
icloud mail search [query] --remote [--folder <selector>|--all-folders] [provider-filters] [--limit <n>] [--json]
icloud mail search repair [--limit <n>] [--json]
icloud mail sync status [--folder <selector>] [--json]
icloud mail sync checkpoints [--limit <n>] [--json]
icloud mail latest [--query <text>] [filters] [--cursor <cursor>] [--limit <n>] [--json]
icloud mail thread show <thread-id|message-id> [--limit <n>] [--cursor <cursor>] [--full] [--format compact|text|html|raw] [--headers] [--body-only] [--metadata-only] [--verbose] [--json]
icloud mail message show <message-id> [--full] [--format compact|text|html|raw] [--headers] [--body-only] [--metadata-only] [--verbose] [--json]
icloud mail message show <rem_…> --remote [--cache] [content flags] [--json]
icloud mail message raw <message-id>
icloud mail message export <message-id> --output <path|-> [--force]
icloud mail attachments list <message-id> [--limit <n>] [--json]
icloud mail attachments save <attachment-id> --output <path|-> [--binary] [--force] [--json]
icloud mail attachments save-all <message-id> --dir <directory> [--force] [--json]
icloud mail send --to <address> --subject <subject> [--body <text>|--body-file <path|->] [--html-body <html>|--html-body-file <path|->] [--cc <address>] [--bcc <address>] [--reply-to <address>] [--header 'Name: Value'] [--attach <path>] [--attach-name <path=filename>] [--attach-type <path=type/subtype>] [--idempotency-key <key>] [--retry-of <send-id>] [--retry-unknown] [--dry-run|--yes] [--json]
icloud mail reply <message-id> [--to <address>] [--cc <address>] [--bcc <address>] [--replace-to] [--replace-cc] [--body <text>|--body-file <path|->] [--html-body <html>|--html-body-file <path|->] [--attach <path>] [--idempotency-key <key>] [--dry-run|--yes] [--json]
icloud mail reply-all <message-id> [reply flags]
icloud mail forward <message-id> --to <address> [--original quoted|eml|none] [--include-attachment <attachment-id>] [--body <text>|--body-file <path|->] [--attach <path>] [--idempotency-key <key>] [--dry-run|--yes] [--json]
icloud mail status --outbound [--limit <n>] [--json]
icloud mail status --mutations [--limit <n>] [--json]
icloud mail mark-read <message-id> [--folder <selector>] [--json]
icloud mail mark-unread <message-id> [--folder <selector>] [--json]
icloud mail flag <message-id> [--folder <selector>] [--json]
icloud mail unflag <message-id> [--folder <selector>] [--json]
icloud mail move <message-id> --from <folder> --to <folder> [--idempotency-key <key>] [--dry-run|--yes] [--json]
icloud mail copy <message-id> --from <folder> --to <folder> [--idempotency-key <key>] [--dry-run|--yes] [--json]
icloud mail archive <message-id> [--from <folder>] [--idempotency-key <key>] [--dry-run|--yes] [--json]
icloud mail unarchive <message-id> [--idempotency-key <key>] [--dry-run|--yes] [--json]
icloud mail trash <message-id> [--from <folder>] [--idempotency-key <key>] [--dry-run|--yes] [--json]
icloud mail restore <message-id> --to <folder> [--idempotency-key <key>] [--dry-run|--yes] [--json]
icloud mail delete <message-id> --from <folder> [--idempotency-key <key>] [--dry-run|--yes] [--json]
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
reconciled=20
state_changes=2
last_uid=184
more=false
bootstrap=true

mail-sync
fld_… account=acct_…
folder="Sent Messages"
fetched=0
created=0
updated=0
last_uid=52
more=false
bootstrap=true

inbox
account=acct_…
count=20
more=false

thread
thr_… subject="Project update"
messages=3
unread=1
participants=owner@example.com,sender@example.com
folders=fld_…
last=2026-08-06T09:21:00Z
reconstruction=local_rfc_graph

threads
account=acct_…
count=1
more=false

doctor
accounts=1
status=healthy

mail-send
send_… status=sent
message_id=msg_…
thread_id=thr_…
reconciliation=pending
reused=false

mail-reply
send_… kind=reply
parent_message_id=msg_…
status=sent
message_id=msg_…
thread_id=thr_…
reconciliation=pending
reused=false

mail-forward
send_… kind=forward
source_message_id=msg_…
status=sent
message_id=msg_…
thread_id=thr_…
reconciliation=pending
reused=false

mail-location
mut_… action=move
message_id=msg_…
source_location_id=loc_…
source_folder_id=fld_…
destination_location_id=loc_…
destination_folder_id=fld_…
destination_uid_validity=…
destination_uid=…
status=confirmed
provider_method=move
source_present=false
mapping_known=true
reconciliation_required=false
unread=false
flagged=false
reused=false
next=none

version
0.1.0

capabilities
command=icloud
version=0.1.0
schema=17
account_mode=validated_lifecycle
credentials=keyring,env,file
network_validation=true
diagnostics=true
mail_folders=true
mail_sync=true
mail_content=true
mail_attachments=true
mail_flags=true
mail_threads=true
mail_search=true
mail_remote_search=true
mail_hydration=true
mail_send=true
mail_reply=true
mail_forward=true
mail_location_mutations=true
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
