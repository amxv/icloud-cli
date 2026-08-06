---
title: Output and automation
description: Understand compact defaults, JSON envelopes, stdin credential safety, stderr, request IDs, timeouts, and stable exit classes.
order: 8
category: Guides
summary: The agent-facing contract for bounded text, explicit JSON, safe input, and stable failures.
---

## Compact text is always the default

The CLI does not switch to JSON because stdout is redirected or a caller is non-interactive. Routine commands return bounded, undecorated text with stable field order:

```text
version
0.1.0

account-added
acct_… status=pending_validation
default=true
credential=keyring

accounts
count=1

account-test
acct_… status=connected
imap=ok
smtp=ok
imap_username=owner
mailboxes=6
inbox_messages=42
inbox_unseen=3
checked_at=2026-08-06T03:00:00Z
```

Each compact result is a small vertical block. The first line names the result, the next line carries its stable identity plus the first field when present, and every remaining field appears on its own line. A blank line separates consecutive blocks. Default output still omits empty values, ANSI color, box-drawing tables, JSON punctuation, and repeated prose. Successful output goes to stdout.

`account list`, `mail inbox`, attachment lists, and checkpoint lists return 20 records by default, accept `--limit` through 100, and report `more=true` when truncated. Sync also reconciles at most 100 least-recently checked current locations per run. Mutation and export commands return an outcome and stable IDs rather than echoing submitted names, email addresses, subjects, paths, or passwords.

## JSON is explicit

Commands that advertise `--json` return one envelope:

```bash
icloud --request-id account-01 account show --json
```

```json
{"ok":true,"data":{"account":{"id":"acct_…","name":"Personal","email":"owner@icloud.com","imap_username":"owner","status":"connected","default":true,"credential_scheme":"keyring","imap_capabilities":["IDLE","IMAP4REV1"],"smtp_capabilities":["AUTH"],"last_healthy_at":"2026-08-06T03:00:00Z","last_tested_at":"2026-08-06T03:00:00Z","created_at":"2026-08-06T02:30:00Z","updated_at":"2026-08-06T03:00:00Z"},"schema_version":7},"meta":{"request_id":"account-01"}}
```

Account lifecycle, credential, validation, doctor, folder discovery/policy, Inbox sync and reads, message reads and state mutations, attachment list/save/save-all, account reads, `version`, and `capabilities` support JSON when stdout is not carrying native bytes. Folder list/show/status also support bounded `--verbose` provider metadata. Completion and man generation intentionally return native text formats.

Credential metadata JSON omits internal IDs, keyring references, environment names, file paths, and values. Error envelopes omit wrapped provider and storage causes.

An `account test --json` failure writes one error envelope to stderr. Its `details.stage` and bounded `details.reports` identify the failing protocol without including the credential reference, password, raw server response, or debug transcript.

## Supply credentials without prompting

An agent or redirected process must choose one source explicitly:

```text
--password-stdin
--credential-env <name>
--credential-file <path>
```

No `--password` flag exists. Without a source, non-TTY setup returns a safety error and does not prompt. `--no-input` also prohibits an implicit terminal prompt. Password stdin is bounded and stored in the native keyring; it is never copied into JSON or SQLite.

## Request IDs

```bash
icloud --request-id job-42 account list --json
```

Request IDs accept 1–128 letters, numbers, dots, underscores, or hyphens. When omitted, the CLI generates one for structured output.

## Errors and stderr

Plain errors use the same compact block shape on stderr:

```text
error
code=safety
message="account removal requires --yes"
```

With `--json`, the error is one envelope on stderr:

```json
{"ok":false,"error":{"code":"safety","message":"account removal requires --yes","retryable":false},"meta":{"request_id":"job-42"}}
```

Help remains on stdout. A failed command never writes a success payload to stdout.

## Exit classes

| Exit | Class | Meaning |
| ---: | --- | --- |
| `0` | success | The command completed. |
| `1` | internal | An unexpected internal failure occurred. |
| `2` | usage | Arguments, flags, selectors, or references were invalid. |
| `3` | configuration | Local state or the native keyring was unavailable. |
| `4` | authentication | A credential value was missing or IMAP/SMTP rejected it. |
| `5` | unavailable | DNS, connectivity, TLS, protocol compatibility, or timeout prevented validation. |
| `6` | conflict | A lock, duplicate account, ambiguous selection, or default-removal rule blocked the command. |
| `7` | not found | The selected account or credential metadata does not exist. |
| `8` | safety | Explicit input or `--yes` authorization is required. |

## Global controls

```text
--home <path>         override the application data root
--account <selector>  select an account when a command supports it
--timeout <duration>  bound command execution; default 30s
--no-input            prohibit implicit terminal prompts
--request-id <id>     correlate structured output
```

Version, capabilities, completion, man, and help remain disk-free. Account reads, `mail inbox`, `mail message`, attachment reads, sync status, checkpoint reads, and local `doctor` never prompt or contact Apple. Sync, read/flag mutations, and live validation contact Apple and use the same global timeout; streaming message and attachment reads also honor the command timeout.

## Message content is explicit

`mail inbox` stays preview-only. `mail message show <id>` adds orthogonal content choices:

```text
--full
--format compact|text|html|raw
--headers
--body-only
--metadata-only
--verbose
--json
```

Default message JSON reports whether text, HTML, headers, raw MIME, and parts are locally available, but omits their content. Body-bearing JSON requires `--full` or `--format text|html`; raw-bearing JSON requires `--format raw` and is capped at 8 MiB. Larger bodies and raw messages remain available through plain streaming output. BCC is excluded from compact output, message JSON, and decoded-header output; exact raw MIME includes it only because raw was explicitly requested.

Unsupported or contradictory combinations fail visibly. For example, `--headers --body-only`, content flags with `--metadata-only`, and plain `--format raw --verbose` return usage errors rather than guessing.

## Attachment bytes require an explicit destination

Attachment metadata is compact and bounded by default:

```bash
icloud mail attachments list msg_…
```

Saving one file requires `--output`; writing native bytes to stdout additionally requires `--binary`:

```bash
icloud mail attachments save att_… --output document.pdf
icloud mail attachments save att_… --output - --binary > document.pdf
```

The native stdout mode contains only attachment bytes. File and batch success output remains concise and omits the destination path. `save-all` stages the complete supported set before installing it, so automation does not need to infer whether a partial batch succeeded.

## Read and flag mutations are location-scoped

```bash
icloud mail mark-read msg_… --json
icloud mail flag msg_… --folder INBOX --json
```

Success JSON identifies the exact stable message, location, and folder plus UID validity, UID, structured canonical and unknown flags, provider mod-sequence when available, last flag-check time, and recomputed message summaries. Compact output intentionally omits provider flags and subjects.

Several eligible locations return a conflict until `--folder` selects one. A conditional provider race also returns conflict, but the response details contain the current server-returned location state that was persisted locally. Automation should read fresh metadata and decide whether to retry; it should not treat conflict as partial success or broaden the operation to other locations.
