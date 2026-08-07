---
title: Output and automation
description: Understand compact defaults, JSON envelopes, outbound-review privacy, stdin safety, stderr, request IDs, timeouts, and stable exit classes.
order: 9
category: Guides
summary: The agent-facing contract for bounded text, explicit JSON, safe credential and message input, deliberate send/reply/forward operations, and stable failures.
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

`account list`, `mail inbox`, `mail search`, `mail latest`, `mail thread show`, attachment lists, and checkpoint lists return 20 records by default, accept `--limit` through 100, and report `more=true` when truncated. Local search and thread pages also return `next_cursor`; pass it through `--cursor` to continue without repeating earlier records. Local search cursors are bound to the complete account, query, filters, and result kind. Remote search is intentionally different: it has no cursor, reports `partial` plus the bounded provider positions examined, and requires a narrower query when older provider state was not scanned. Normal sync and historical backfill each apply their own 1–100 bound; repair applies the bound per finding/repair category and reports `more=true` when another bounded pass is needed. Mutations, hydration, rebaseline, repair, and export commands return outcomes and stable IDs or counts rather than echoing submitted names, addresses, subjects, paths, passwords, or remote references.

## JSON is explicit

Commands that advertise `--json` return one envelope:

```bash
icloud --request-id account-01 account show --json
```

```json
{"ok":true,"data":{"account":{"id":"acct_…","name":"Personal","email":"owner@icloud.com","imap_username":"owner","status":"connected","default":true,"credential_scheme":"keyring","imap_capabilities":["IDLE","IMAP4REV1"],"smtp_capabilities":["AUTH"],"last_healthy_at":"2026-08-06T03:00:00Z","last_tested_at":"2026-08-06T03:00:00Z","created_at":"2026-08-06T02:30:00Z","updated_at":"2026-08-06T03:00:00Z"},"schema_version":18},"meta":{"request_id":"account-01"}}
```

Account lifecycle, credential, validation, doctor, folder discovery/policy, enabled-folder sync, historical backfill, current-tail rebaseline, bounded repair, local and explicit remote search, targeted hydration, local folder reads, latest/thread reads, message reads and state mutations, attachment list/save/save-all, draft lifecycle commands, outbound send/reply/forward preview and outcomes, account reads, `version`, and `capabilities` support JSON when stdout is not carrying native bytes. Folder list/show/status, thread reads, draft sync, and repair findings also support bounded `--verbose` evidence. Completion and man generation intentionally return native text formats.

Thread JSON remains local reconstruction data. `mail latest --json` returns aggregates only. `mail thread show --metadata-only --verbose --json` returns chronological messages and bounded RFC graph evidence without excerpts, BCC, raw-storage coordinates, bodies, or internal edge IDs. `--format raw --json` is explicit and bounded; plain multi-message raw output is rejected.

Credential metadata JSON omits internal IDs, keyring references, environment names, file paths, and values. Outbound status JSON omits recipients, BCC, subject, bodies, idempotency keys, attachment paths, and private blob coordinates. Error envelopes omit wrapped provider and storage causes.

## Outbound review and success have different privacy contracts

`mail send --dry-run` is an explicit message-review mode. Its compact output includes the normalized recipient lists, including BCC, the subject, the text body once, body-part and attachment counts, attachment filenames/media types/sizes/hashes, and a safety summary proving that SMTP was not contacted and no attempt was created. Reply dry runs add the exact parent and derived recipients. Forward dry runs add the exact source, quoted/EML/none representation, selected original attachments, and a bounded text excerpt. `--headers`, `--verbose`, `--format text|html|raw`, and `--json` deliberately expand those reviews. Dry-run JSON contains normalized recipients, BCC, subject, bodies, Reply-To, safe custom headers, and reviewed attachment metadata; treat it as private message content rather than routine telemetry.

A real outbound operation behaves differently. Non-interactive send, reply, or forward requires `--yes`, and automation should supply a stable `--idempotency-key`. Compact or JSON success returns the account ID, stable attempt/message/thread IDs, status, reconciliation state, attachment count/bytes, reuse state, schema version, and request ID. Reply adds only its stable parent ID; forward adds only its stable source ID. It does not echo recipients, BCC, subject, bodies, custom headers, attachment paths, attachment names, content hashes, the idempotency key, or raw-storage coordinates.

Draft lifecycle output follows the same progressive-disclosure rule without pretending a draft is a sent attempt. `draft create`, `update`, `list`, `show`, and `sync` keep BCC addresses out of ordinary compact and JSON output; `draft show` exposes complete decoded content, headers, HTML, or raw MIME only when those modes are explicitly requested. `draft send --dry-run` is an intentional outbound review and may expose the saved recipients and content just like `mail send --dry-run`. A real non-interactive `draft send` or `draft delete` requires `--yes`; successful send output is limited to stable draft/attempt/message/thread identity and status, while deletion reports the stable draft ID and provider-copy count rather than echoing message payloads.

Location-changing dry runs follow the same explicit-review rule. `mail move`, `copy`, `archive`, `unarchive`, `trash`, `restore`, and `delete` resolve the exact current source and destination locally, prove that IMAP was not contacted, and create no mutation intent. Real non-interactive execution requires `--yes`; automation should use a stable idempotency key. Success returns stable mutation/message/location/folder IDs, provider method, UID mapping, status, reconciliation state, aggregate unread/flagged values, and reuse state without echoing message payloads or submitted folder names.

An `unresolved` location mutation is not safe to retry automatically. The provider may have accepted COPY, MOVE, STORE, or EXPUNGE before the connection failed. Read `mail status --mutations`, synchronize the exact source and destination folders, and inspect the message's current locations. Repeating the same request with the same idempotency key returns the original `mut_…` record without another provider write. Treat an interrupted folder rename similarly: run `mail folders status` rather than repeating the provider command so rename recovery can settle the new name against fresh LIST evidence.

`mail status --outbound` is the bounded operational view for recent attempts. Its compact records contain stable IDs, status, attachment counts/bytes, reconciliation/error codes, retry linkage, update time, and one next action. It never repeats submitted message payloads. `mail sync --reconcile-outbound` returns only bounded reconciliation counts in addition to the normal Sent sync summary.

An indeterminate post-DATA result uses status `unknown`, returns the stable attempt ID on stderr, and is always `retryable=false`. The caller must not infer that a temporary network cause makes the send safe to repeat. Reusing the same normalized request and idempotency key retrieves the original attempt without another SMTP transaction.

An `account test --json` failure writes one error envelope to stderr. Its `details.stage` and bounded `details.reports` identify the failing protocol without including the credential reference, password, raw server response, or debug transcript.

## Use JSONL for the watch stream

`mail watch` is a stream rather than a one-shot result, so structured automation uses JSONL instead of a single JSON envelope:

```bash
icloud mail watch --jsonl
icloud mail watch --all-accounts --jsonl
```

Every output line is one complete JSON object with `event`, `sequence`, `account_id`, and `timestamp`. Payloads identify only the lifecycle data needed for startup, mode changes, IDLE, provider changes, bounded sync results, reconnects, degraded operation, stopping, and completion. Sequences are strictly increasing within each account even when several account workers share one foreground process.

Compact watch output suppresses repeated idle and unchanged-sync chatter; JSONL deliberately includes those typed events so a consumer can maintain exact lifecycle state. Initial or terminal command failures requested with `--jsonl` stay JSONL on stderr rather than mixing a compact error into the structured stream.

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
--timeout <duration>  bound command execution; for mail watch, bound each provider/sync attempt
--no-input            prohibit implicit terminal prompts
--request-id <id>     correlate structured output
```

Version, capabilities, completion, man, and help remain disk-free. Account reads, `mail inbox` with any folder selector, local `mail search`, `mail search repair`, `mail latest`, `mail thread show`, local `mail message`, attachment reads, sync status, checkpoint reads, `mail sync repair --check`, local `doctor`, outbound dry runs, folder-administration dry runs, and location-mutation dry runs never prompt or contact Apple. Compact thread reads and plain local `--metadata-only` use stored summaries without opening raw message files. Search repair may verify and parse already synchronized raw bytes, but it never contacts iCloud. `mail search --remote`, `mail message show rem_… --remote`, targeted hydration, normal sync, historical backfill, current-tail rebaseline, real folder administration, read/flag mutations, real outbound mail, real location mutations, and live validation contact Apple and use the same global timeout. `mail sync repair --apply` stays local unless an exact affected mailbox needs fresh UID identity; in that case it reads status only for that folder. Streaming message, attachment, outbound MIME, COPY/MOVE, and reconciliation paths also honor the command timeout. `mail watch` is the deliberate long-running exception to a whole-command deadline: it runs until cancellation while applying the same timeout to each provider and synchronization attempt.

Repair is check-first for automation. `--check` returns bounded counts and one `next` action without changing local/provider state. `--apply` fixes only deterministic evidence; ambiguous dedupe identity and missing/corrupt canonical blobs remain findings. Deleting orphan files from the private staging directory is never implicit and requires `--apply --cleanup-orphans --yes`.

Remote compact/JSON results expose only the stale-safe `rem_…` reference, current folder/UID identity, bounded envelope fields, flags, size, provider preview, and an optional cached stable message ID. BCC, credentials, raw storage coordinates, raw MIME, full bodies, HTML, attachment bytes, and private local paths remain excluded. Hydration output omits the submitted remote reference and returns only stable local IDs and created/updated/cached status.

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

`mail thread show` supports the same content choices plus `--cursor`. Plain `--body-only` writes each selected body chronologically with a blank line between messages. Because a body-only stream intentionally contains no pagination metadata, automation should read `next_cursor` from a compact or JSON page first when a thread may exceed the chosen limit.

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
