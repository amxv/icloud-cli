---
title: Command reference
description: A complete reference for account lifecycle, iCloud validation, folder discovery, Inbox synchronization, message and attachment reads, doctor diagnostics, global controls, output modes, completion, and man generation.
order: 10
category: Reference
summary: Every account, credential, validation, folder, Inbox sync/read, message, attachment, doctor, capability, completion, man, help, and global control available now.
---

## Root command

```bash
icloud [global flags] <command> [command flags]
```

Running `icloud` without a command prints root help to stdout.

### Global flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--home <path>` | `ICLOUD_HOME`, then platform default | Override the application data directory for state-bearing commands. |
| `--account <selector>` | none | Select by friendly name, email, or stable ID when supported. |
| `--timeout <duration>` | `30s` | Bound execution and lock acquisition. |
| `--no-input` | false | Prevent implicit terminal prompts. Explicit stdin remains allowed. |
| `--request-id <id>` | generated for structured output | Correlate JSON success and error envelopes. |
| `--version` | — | Print the compact installed version and exit. |
| `-h`, `--help` | — | Print help to stdout. |

## `icloud account add`

Create an offline account profile. `--name` and `--email` are required.

```bash
icloud account add --name Personal --email owner@icloud.com
printf '%s\n' "$APP_PASSWORD" |
  icloud account add --name Personal --email owner@icloud.com --password-stdin
icloud account add --name Agent --email agent@icloud.com \
  --credential-env ICLOUD_AGENT_APP_PASSWORD
icloud account add --name Work --email work@icloud.com \
  --credential-file "$HOME/.config/icloud-cli/work-password"
```

Credential-source flags are mutually exclusive:

| Flag | Behavior |
| --- | --- |
| `--password-stdin` | Read a bounded value from stdin and store it in the native keyring. |
| `--credential-env <name>` | Persist an environment-variable reference without resolving it. |
| `--credential-file <path>` | Persist an absolute file reference without reading or copying it. |

In an interactive terminal, omitting all three sources opens a secure non-echoing keyring prompt. Non-TTY execution requires an explicit source. Optional `--imap-username` overrides the initial full-email username. `--default` makes the new profile the default; otherwise the first profile defaults automatically.

Success returns an outcome, stable ID, `pending_validation`, default state when true, and credential scheme. It does not echo the submitted name, email, path, variable, or password. `--json` returns the same bounded mutation metadata.

## `icloud account list`

```bash
icloud account list
icloud account list --limit 50
icloud account list --json
```

The default bound is 20 and the maximum is 100. A final summary reports count and `more=true` when truncated. Account records include the credential scheme only.

## `icloud account show`

```bash
icloud account show Personal
icloud --account owner@icloud.com account show
icloud account show
icloud account show --json
```

Without a selector, the command chooses the default, then a sole account. A missing account exits `7`; ambiguous selection exits `6`.

## `icloud account rename`

```bash
icloud account rename Personal Primary
icloud account rename Personal Primary --json
```

The stable account ID and email do not change. Friendly-name conflicts exit `6`.

## `icloud account default`

```bash
icloud account default Work
icloud account default Work --json
```

Exactly one account is default after success.

## `icloud account remove`

```bash
icloud account remove Old --yes
icloud account remove Old --yes --json
```

`--yes` is required. Removing a native-keyring profile clears its keyring entry. Environment variables and external files are not deleted. A default account cannot be removed while another profile exists; set another default first.

## `icloud account credential show`

```bash
icloud account credential show Personal
icloud account credential show Personal --json
```

Returns account ID, scheme, configured state, and update time. The command does not resolve or disclose the reference or value.

## `icloud account credential replace`

```bash
printf '%s\n' "$NEW_APP_PASSWORD" |
  icloud account credential replace Personal --password-stdin
icloud account credential replace Personal \
  --credential-env ICLOUD_PERSONAL_APP_PASSWORD
icloud account credential replace Personal \
  --credential-file "$HOME/.config/icloud-cli/personal-password"
```

Uses the same mutually exclusive source flags as `account add`. A replacement marks the account `pending_validation`. `--json` returns credential metadata without the reference or value.

## `icloud account credential delete`

```bash
icloud account credential delete Personal --yes
icloud account credential delete Personal --yes --json
```

`--yes` is required. The account remains, but its credential reference is removed and its status is `pending_validation`. A native keyring entry is cleared when present.

## `icloud account test`

```bash
icloud account test
icloud account test Personal
icloud --account owner@icloud.com account test
icloud account test --all
icloud account test --verbose
icloud account test --json
```

Resolves the selected credential and validates direct-TLS IMAP plus mandatory-STARTTLS SMTP. IMAP reads capabilities, mailbox names, and `INBOX` status. SMTP authenticates, sends `NOOP`, and quits. The command never sends a message or mutates a mailbox.

`--all` is bounded to 100 profiles and conflicts with a selector. `--verbose` adds sanitized capability names; normal compact output includes protocol outcomes, the successful IMAP username, mailbox count, bounded `INBOX` counts, and the test time. Both protocols must pass for `connected` status.

## `icloud doctor`

```bash
icloud doctor
icloud doctor Personal
icloud doctor --all
icloud doctor Personal --live
icloud doctor --json
```

Without `--live`, reads stored account, credential, last-error, last-tested, and last-healthy metadata without resolving a secret or contacting Apple. With `--live`, runs the same read-only validation as `account test` first. An empty state returns `doctor status=configuration_required`.

## `icloud mail folders status`

```bash
icloud mail folders status
icloud mail folders status --limit 50
icloud --account Personal mail folders status --verbose
icloud mail folders status --json
```

Runs live IMAP LIST plus STATUS for every selectable folder, then transactionally reconciles the complete account-scoped inventory. It stores decoded and modified-UTF-7 wire names, delimiter, attributes, special-use kind/source, selectable/subscription state, counts, UID metadata, discovery time, presence/stale state, and local sync policy. It never fetches messages or mutates a mailbox.

The refresh persists every discovered folder while output is bounded to 20 records by default and 100 with `--limit`. The result includes retained missing folders and reports `more=true` when not every record was printed. `--verbose` adds provider metadata; `--json` returns the same bounded records.

## `icloud mail folders list`

```bash
icloud mail folders list
icloud mail folders list --limit 50
icloud mail folders list --include-missing --verbose
icloud mail folders list --json
```

Reads the persisted folder inventory without contacting iCloud. The default bound is 20 and the maximum is 100. Missing/stale folders are hidden unless `--include-missing` is supplied.

## `icloud mail folders show`

```bash
icloud mail folders show INBOX
icloud mail folders show 'Unicode-日本語' --verbose
icloud mail folders show fld_… --json
```

Selects by stable folder ID, decoded name, exact wire name, or unambiguous kind. Duplicate display names and non-unique kinds return a conflict rather than guessing.

## `icloud mail folders enable` / `disable`

```bash
icloud mail folders enable Receipts
icloud mail folders disable Receipts
```

Changes local future-sync policy only; no message is fetched or changed. Newly discovered selectable Inbox and Sent default enabled, while every other folder defaults disabled. Policy survives rediscovery, rename, missing state, and reappearance. Enabling a non-selectable container returns conflict.

## `icloud mail sync`

```bash
icloud mail sync
icloud mail sync --limit 50
icloud --account Personal mail sync --json
```

Fetches and atomically commits one bounded Inbox page. The default page is 20 messages and the maximum is 100. The first run starts at the current Inbox tail; later runs fetch only newer UIDs. `more=true` means another bounded page is available.

The command requires a previously discovered, present, selectable, sync-enabled Inbox. It never changes remote flags, reads Sent, moves mail, or sends a message. An interrupted fetch, parse, blob write, or local commit leaves the prior frontier unchanged.

Compact success reports the account/folder IDs, fetched/created/updated counts, committed UID, `more`, and whether the page was the initial bootstrap. `--json` returns the same outcome plus the durable checkpoint.

## `icloud mail inbox`

```bash
icloud mail inbox
icloud mail inbox --limit 50
icloud mail inbox --json
```

Reads a bounded page from local state without contacting iCloud. Compact records include stable ID, sender, subject, best available date, true unread/flagged states, nonzero attachment count, and a bounded decoded-text preview.

JSON adds complete locally stored preview metadata and provider location IDs while omitting credentials, BCC, raw storage paths/hashes, and full message bodies.

## `icloud mail message show`

```bash
icloud mail message show msg_…
icloud mail message show msg_… --full
icloud mail message show msg_… --format text --body-only
icloud mail message show msg_… --format html
icloud mail message show msg_… --headers
icloud mail message show msg_… --metadata-only
icloud mail message show msg_… --verbose
icloud mail message show msg_… --json
```

Reads one locally synchronized message by stable ID without contacting iCloud or changing read state. The compact default is a bounded vertical block with account/message identity, From/To/CC when present, subject, best time and date source, current state/locations, attachment count, available content kinds, raw/part availability, a decoded-text excerpt, and an expansion hint.

Content is opt-in:

| Option | Result |
| --- | --- |
| `--full` | Complete decoded text, equivalent to explicit text content plus metadata. |
| `--format text` | Complete decoded text. HTML-only messages are converted to readable text. |
| `--format html` | Complete decoded HTML when available. |
| `--format raw` | Exact RFC822 bytes; plain output contains no compact wrapper. |
| `--headers` | Ordered decoded headers after the metadata block; BCC is omitted. |
| `--body-only` | Only the selected text or HTML body, suitable for redirection. |
| `--metadata-only` | Metadata without excerpt or content; conflicts with content flags. |
| `--verbose` | Ordered MIME-part metadata and parse diagnostics. |
| `--json` | One envelope; content appears only when the corresponding content flag is also present. |

Default JSON reports text/HTML/raw/header/part availability, part count, and whether parsing was complete. `--full --json` adds decoded text; `--format html --json` adds HTML; `--headers --json` adds headers; `--verbose --json` adds MIME parts; `--format raw --json` adds base64 raw MIME up to 8 MiB. Use plain streaming modes for larger content.

Contradictory combinations return usage errors rather than choosing silently. Examples include `--headers --body-only`, content flags with `--metadata-only`, `--full --format html`, and plain `--format raw --verbose`.

## `icloud mail message raw`

```bash
icloud mail message raw msg_… > message.eml
```

Streams the exact synchronized RFC822 bytes to stdout after verifying the stored size and SHA-256 content address. The command emits no compact status block because stdout is the requested binary/native payload.

## `icloud mail message export`

```bash
icloud mail message export msg_… --output message.eml
icloud mail message export msg_… --output message.eml --force
icloud mail message export msg_… --output - > message.eml
```

Exports exact RFC822 bytes. File output uses a private temporary file and mode `0600`, refuses existing destinations unless `--force` is supplied, and installs the completed file only after verified streaming succeeds. `--output -` writes raw bytes to stdout and emits no compact wrapper. Successful file output reports the message ID and byte count without echoing the destination path.

## `icloud mail attachments list`

```bash
icloud mail attachments list msg_…
icloud mail attachments list msg_… --limit 50
icloud mail attachments list msg_… --json
```

Reads bounded attachment metadata from one locally synchronized message. Compact records include stable attachment, message, and MIME-part IDs; filename, media type, decoded size, inline state, disposition, content ID when present, and availability. Attachment bytes and private storage coordinates are never printed.

The default bound is 20 and the maximum is 100. `more=true` means additional attachment records were not printed. JSON adds media parameters and the decoded content hash while still omitting private storage paths.

## `icloud mail attachments save`

```bash
icloud mail attachments save att_… --output document.pdf
icloud mail attachments save att_… --output document.pdf --force
icloud mail attachments save att_… --output - --binary > document.pdf
icloud mail attachments save att_… --output document.pdf --json
```

Verifies and streams one decoded local attachment by stable ID. `--output` is required. File output uses a private temporary file, refuses an existing destination unless `--force` is supplied, and rejects directories, symbolic links, and other special files. Successful compact and JSON output do not echo the destination path.

Writing native bytes to stdout requires both `--output -` and `--binary`; the command emits no compact wrapper in that mode. `--json` cannot be combined with binary stdout.

## `icloud mail attachments save-all`

```bash
icloud mail attachments save-all msg_… --dir ./message-attachments
icloud mail attachments save-all msg_… --dir ./message-attachments --force
icloud mail attachments save-all msg_… --dir ./message-attachments --json
```

Saves the complete supported attachment set for one message. Filenames are treated as untrusted metadata: traversal components, separators, controls, and reserved names are made safe; unnamed files receive deterministic names; Unicode is preserved; and duplicate names receive stable numeric suffixes.

Every file is prepared and verified before the batch is installed. A conflict or failure rolls back installed outputs rather than leaving a partial set. Compact success reports the message ID and count. JSON returns attachment metadata and safe output basenames, not the requested directory path.

## `icloud mail sync status`

```bash
icloud mail sync status
icloud mail sync status --json
```

Shows the selected Inbox checkpoint locally. `idle` means the latest page committed, `failed` means the frontier did not advance, and `repair_required` means iCloud reported a different mailbox identity and ordinary sync stopped.

## `icloud mail sync checkpoints`

```bash
icloud mail sync checkpoints
icloud mail sync checkpoints --limit 50
icloud mail sync checkpoints --json
```

Lists bounded account-scoped checkpoints from local state. Inbox is the only message checkpoint in this build.

## `icloud version`

```bash
icloud version
icloud version --json
```

Compact output:

```text
version
0.1.0
```

## `icloud capabilities`

```bash
icloud capabilities
icloud capabilities --json
```

Compact output:

```text
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

The schema field reports the installed local-state migration version without opening state.

## `icloud completion`

```bash
icloud completion bash
icloud completion zsh
icloud completion fish
icloud completion powershell
```

Output is the native shell script, not compact text or JSON.

## `icloud man`

```bash
icloud man > icloud.1
icloud man --output-dir ./man
```

Directory mode returns:

```text
man
generated dir=man
```

## `icloud help`

```bash
icloud help
icloud help account
icloud account credential replace --help
```

Help goes to stdout. Usage failures go to stderr and exit `2`.

## Output availability

Every account and credential lifecycle command above, `account test`, every folder command, Inbox sync/read/status/checkpoints, `mail message show`, attachment list/save/save-all, `doctor`, `version`, and `capabilities` supports `--json` where a native byte stream is not requested. Validation, doctor, folder reads/status, and message reads support bounded `--verbose` detail where advertised. `mail message raw`, `message export --output -`, and `attachments save --output - --binary` retain native bytes; completion and man generation retain their native formats. Inbox stays preview-only, while stable-ID message reads provide explicit full-text, HTML, raw, headers, body-only, metadata-only, verbose, and JSON modes.

Compact output uses vertical blocks: result type first, stable identity plus the first field next when present, then one remaining field per line. A blank line separates consecutive records or a final summary. Errors use the same block shape on stderr.
