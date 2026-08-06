---
title: Command reference
description: A complete reference for account lifecycle, iCloud validation, safe send/reply/forward workflows, exact provider-location changes, folder discovery, synchronization, search, message and attachment reads, diagnostics, output modes, completion, and man generation.
order: 11
category: Reference
summary: Every account, credential, validation, outbound, location-mutation, folder, sync/read/search, message, attachment, doctor, capability, completion, man, help, and global control available now.
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

## `icloud mail folders create` / `rename`

```bash
icloud mail folders create 'Projects/Client-日本語' --dry-run
icloud mail folders create 'Projects/Client-日本語' --yes
icloud mail folders rename Receipts 'Projects/Receipts' --dry-run
icloud mail folders rename Receipts 'Projects/Receipts' --yes
```

Creates or renames one custom provider folder. `--dry-run` resolves local safety without contacting IMAP; a real non-interactive change requires `--yes`. Names use the provider's discovered hierarchy and may contain Unicode. Rename preserves the existing stable folder ID, local sync policy, checkpoint, and locations. Inbox, provider-derived system roles, and folders with current child mailboxes are protected from rename.

An interrupted rename is not safe to repeat automatically: iCloud may have accepted it before the response was lost. Run `icloud mail folders status`; the durable rename recovery reconciles the old and new provider names back to the original stable folder ID when the fresh LIST evidence is decisive.

## `icloud mail folders subscribe` / `unsubscribe`

```bash
icloud mail folders subscribe Receipts --dry-run
icloud mail folders subscribe Receipts --yes
icloud mail folders unsubscribe Receipts --yes
```

Changes the provider subscription state for one current folder. This is independent from the CLI's local `enable` / `disable` sync policy. `subscription=unknown` remains distinct from subscribed and unsubscribed when iCloud does not report subscription state during normal discovery.

## `icloud mail folders delete`

```bash
icloud mail folders delete fld_… --dry-run
icloud mail folders delete fld_… --yes
icloud mail folders delete fld_… --allow-non-empty --yes
```

Deletes one exact custom provider folder after local safety checks and an immediate provider message-count recheck. Inbox, provider-derived system/special-use folders, non-selectable containers, and folders with current children are protected. Non-empty deletion additionally requires `--allow-non-empty`; it means the folder's provider contents may be deleted. A confirmed delete marks the retained local folder identity missing/stale and disables its sync policy.

## `icloud mail sync`

```bash
icloud mail sync
icloud mail sync --folder sent
icloud mail sync --folder fld_…
icloud mail sync --all-folders --limit 20
icloud mail sync --reconcile-outbound
icloud mail sync --hydrate rem_…
icloud mail sync --hydrate rem_…,rem_… --json
icloud mail sync --limit 50
icloud --account Personal mail sync --json
```

Fetches and atomically commits one bounded page for each enabled, current, selectable folder. Inbox and Sent begin enabled. The default page is 20 messages per folder and the maximum is 100. Use `--folder` with a role, stable ID, decoded name, or exact wire name to select one enabled folder. `--all-folders` is an explicit override that selects every current selectable folder, including locally disabled folders, while still applying the same per-folder bound.

Inbox first setup downloads the newest bounded page. Sent first setup records the current remote tail and downloads no historical Sent messages; later runs ingest only new Sent copies. Another enabled or explicitly all-folder-selected mailbox starts from its newest bounded page. Synchronization never walks backward into older history automatically. `more=true` can also reflect a sparse UID tail; subsequent runs continue only above the saved scan frontier.

Each selected folder also refreshes up to 100 least-recently checked current locations using metadata-only UID reads. The rotating sweep detects server-side Seen, Flagged, Answered, Draft, Deleted, unknown flags, disappearance, and mod-sequences when available without downloading bodies again. Sync never changes remote flags, moves mail, or sends a message.

When a new folder copy has the same strong message identity as an existing local message, it becomes another location on the stable `msg_…` record. Each location retains its own folder, UID identity, flags, and presence. Similar content alone does not collapse two provider-backed messages; ambiguous evidence preserves both records and emits dedupe conflict telemetry.

Compact success prints one block per selected folder with fetched/created/updated counts, reconciled locations, actual state changes, nonzero identity merges or conflicts, committed UID, `more`, and bootstrap state. `--json` returns every folder summary plus its durable checkpoint. An interrupted fetch, reconciliation, parse, blob write, or local commit leaves that folder's prior frontier unchanged.

`--hydrate <rem_…,...>` switches to targeted remote caching and cannot be combined with `--folder` or `--all-folders`. It accepts 1-100 stale-safe remote references, deduplicates repeated arguments, verifies each account/folder/UIDVALIDITY/UID, and routes the fetched message through the same parser, blob, identity, attachment, search, and thread ingestion path. It does not create or advance a sync checkpoint. Compact output reports stable message/location IDs plus `created`, `updated`, or `cached`; JSON omits the submitted remote references.

`--reconcile-outbound` selects Sent, synchronizes one bounded page, and then checks recent `sent` or `unknown` attempts against exact generated RFC Message-ID evidence. It cannot be combined with `--folder`, `--all-folders`, or `--hydrate`. Unique evidence confirms the existing local message/location; missing or ambiguous evidence remains unresolved and never causes a resend.

## `icloud mail send`

Preview or send one new text/HTML message through the selected iCloud account:

```bash
icloud mail send --to client@example.com --subject Update --body 'Hello' --dry-run
icloud mail send --to client@example.com --subject Update --body-file message.txt --dry-run --headers
icloud mail send --to client@example.com --subject Update --body-file message.txt --attach report.pdf --idempotency-key update-01 --yes
printf '%s\n' 'Hello' | icloud mail send --to client@example.com --subject Update --body-file - --yes --json
```

| Flag | Purpose |
| --- | --- |
| `--to <address>` | Required To recipient; repeat or pass a comma-separated list. |
| `--cc <address>` / `--bcc <address>` | Visible Cc or envelope-only Bcc recipient; repeat as needed. |
| `--reply-to <address>` | Optional Reply-To address with display-name support. |
| `--subject <text>` | Required normalized subject. |
| `--body <text>` / `--body-file <path|->` | Inline, regular-file, or stdin text body. |
| `--html-body <html>` / `--html-body-file <path|->` | Inline, regular-file, or stdin HTML body. |
| `--header 'Name: Value'` | Repeatable safe custom header; routing, identity, MIME, delivery, `Received`, and `Resent-*` fields are reserved. |
| `--attach <path>` | Attach one regular file; repeat up to the bounded count. |
| `--attach-name <path=filename>` | Override the normalized filename for one attached path. |
| `--attach-type <path=type/subtype>` | Override the normalized media type for one attached path. |
| `--idempotency-key <key>` | Return the original attempt for the same normalized request; changed content with the same key conflicts. |
| `--retry-of <send_…>` | Link one deliberate retry to a matching failed or unknown attempt; requires a new idempotency key. |
| `--retry-unknown` | Accept duplicate-delivery risk for an unresolved unknown attempt; requires `--retry-of`, `--yes`, and a real send. |
| `--dry-run` | Normalize and review without opening SMTP or creating an attempt. |
| `--yes` | Authorize a real non-interactive send. It cannot be combined with `--dry-run`. |
| `--headers` / `--verbose` | Expand a dry-run review with normalized safe header fields or HTML/custom-header detail. |
| `--format text|html|raw` | Emit one explicit dry-run body or composed RFC message instead of compact review text. |
| `--json` | Emit a dry-run review envelope or sanitized real-send outcome. |

The configured account name and address become `From`. Text plus HTML produces multipart alternative; HTML-only input receives a simple plain-text alternative. Each body is bounded to 8 MiB. Attachments are hashed and normalized before confirmation, cached before SMTP, and streamed through MIME composition. The complete composed message is capped at 20 MiB including encoding overhead; Mail Drop is not used.

Dry-run compact output includes the normalized recipient lists, including BCC, subject, body counts, text body once, request hash, and a no-network/no-attempt safety summary. `--headers` never prints a `Bcc` header. `--format raw` composes a complete preview message with a generated preview Message-ID and date, but BCC remains absent because it is only an SMTP-envelope recipient.

Without `--dry-run`, a terminal receives the same compact preview and a `Send this message? [y/N]` prompt unless `--yes` is supplied. Non-TTY execution and `--no-input` require `--yes`. When stdin supplied a body, interactive confirmation is unavailable; review first, then rerun with `--yes`.

A pending attempt and all outbound attachment metadata are durable before SMTP. Final status is `sent`, `failed`, or `unknown`; normal successful acceptance creates a searchable local `msg_…`, reconstructed `thr_…`, complete local attachments, and reconciliation state in the same local transaction. `unknown` means acceptance could not be proved either way and is always non-retryable. If SMTP acceptance is proven but local completion or Sent-copy creation fails, the attempt remains `sent` and must not be resubmitted. Repeating the exact normalized request with its idempotency key returns the original attempt without another SMTP transaction.

Real compact/JSON success returns only stable account, attempt, message, and thread identity plus status, reconciliation, reuse state, and schema metadata. It omits recipients, BCC, subject, bodies, custom headers, idempotency key, and raw-storage coordinates. Dry-run JSON intentionally contains the full normalized review request and must be treated as private message content.

## `icloud mail reply` / `reply-all`

Reply to one exact locally synchronized message:

```bash
icloud mail reply msg_… --body-file reply.txt --dry-run --headers
icloud mail reply msg_… --body 'Thanks' --idempotency-key reply-01 --yes
icloud mail reply-all msg_… --body-file reply.txt --attach report.pdf --dry-run
```

The positional `msg_…` is mandatory and is always the exact reply parent. The command never selects the latest message in a thread implicitly. `reply` derives one primary recipient from `Reply-To`, then `From`. `reply-all` also derives visible original `To` and `Cc` participants, excludes the selected account identity, deduplicates addresses, and never infers prior BCC recipients.

| Flag | Purpose |
| --- | --- |
| `--to <address>` / `--cc <address>` | Add recipients to the derived To or Cc list. Repeat or pass a comma-separated list. |
| `--bcc <address>` | Add a new envelope-only Bcc recipient to this reply. The parent BCC list is never reused. |
| `--replace-to` / `--replace-cc` | Replace the corresponding derived list with addresses explicitly supplied through `--to` or `--cc`. `--replace-to` requires at least one supplied To address. |
| `--body`, `--body-file`, `--html-body`, `--html-body-file` | Supply the reply body using the same bounds and stdin rules as `mail send`. |
| `--attach`, `--attach-name`, `--attach-type` | Attach reviewed regular files through the same streamed attachment path as `mail send`. |
| `--header 'Name: Value'` | Add a safe custom header. `In-Reply-To` and `References` are reserved for the resolved parent. |
| `--idempotency-key`, `--retry-of`, `--retry-unknown` | Use the same durable attempt and explicit retry controls as `mail send`. |
| `--dry-run` / `--yes` | Review without SMTP, or authorize a real non-interactive reply. |
| `--headers`, `--verbose`, `--format text|html|raw`, `--json` | Expand only a dry-run preview, except `--json`, which also supports sanitized real outcomes. |

The compact dry run shows the exact parent `msg_…`, reconstructed `thr_…`, parent RFC Message-ID when usable, `threading=true|false`, derived versus added recipient lists, final To/Cc/Bcc lists, normalized subject, body/attachment counts, request hash, optional threading headers, and a no-network/no-attempt safety summary.

Repeated `Re:` prefixes collapse to one. When the exact parent has a valid Message-ID, the composed reply uses it in `In-Reply-To` and appends it to a bounded, deduplicated `References` chain. Missing or malformed parent identity produces `threading=false`; the CLI sends no invented relationship headers and does not force the local child into the parent thread.

A real compact outcome is payload-free:

```text
mail-reply
send_… kind=reply
parent_message_id=msg_…
status=sent
message_id=msg_…
thread_id=thr_…
reconciliation=pending
reused=false
```

`kind` is `reply_all` for reply-all. The stored outbound attempt preserves the exact parent ID so status and audit output can distinguish a new send from a reply without exposing submitted content.

## `icloud mail forward`

Forward one exact locally synchronized message as a new conversation:

```bash
icloud mail forward msg_… --to teammate@example.com --body 'For context' --dry-run --headers
icloud mail forward msg_… --to teammate@example.com --original eml --idempotency-key forward-01 --yes
icloud mail forward msg_… --to teammate@example.com --include-attachment att_… --attach new-note.txt --dry-run
```

The positional `msg_…` is mandatory and records source provenance. A forward never emits `In-Reply-To` or `References`, never joins the source thread, and never mutates the source message. Repeated `Fwd:` and `Fw:` prefixes normalize to one `Fwd:` subject.

| Flag | Purpose |
| --- | --- |
| `--to`, `--cc`, `--bcc`, `--reply-to` | Supply the new message recipients. BCC remains envelope-only. |
| `--original quoted|eml|none` | Choose the source representation. `quoted` is the default decoded-text form; `eml` attaches exact verified RFC822 bytes; `none` requires your own text or HTML body. |
| `--include-attachment <att_…>` | Include one verified decoded attachment belonging to the selected source message. Repeat to select more; no original attachment is implicit. |
| `--body`, `--body-file`, `--html-body`, `--html-body-file` | Add introductory content, or provide the complete body with `--original none`. |
| `--attach`, `--attach-name`, `--attach-type` | Add new reviewed filesystem attachments. |
| `--header 'Name: Value'` | Add a safe custom header. Reply relationship fields remain reserved and absent. |
| `--idempotency-key`, `--retry-of`, `--retry-unknown` | Use the same durable attempt and explicit retry controls as send/reply. |
| `--dry-run` / `--yes` | Review without SMTP, or authorize a real non-interactive forward. |
| `--headers`, `--verbose`, `--format text|html|raw`, `--json` | Expand only a dry-run preview, except `--json`, which also supports sanitized real outcomes. |

The compact dry run reports the source message/thread IDs, selected original representation, EML size when applicable, counts for selected original and new attachments, recipients, normalized subject, body/attachment counts, a bounded text excerpt, request hash, and explicit no-network/no-attempt/source-unchanged safety fields. `--format text` emits the complete quoted representation; `--format raw` emits the composed new message. Exact EML bytes appear only as the explicitly selected attachment.

A real compact outcome is payload-free:

```text
mail-forward
send_… kind=forward
source_message_id=msg_…
status=sent
message_id=msg_…
thread_id=thr_…
reconciliation=pending
reused=false
```

The stored outbound attempt links the new message to the source `msg_…` for audit and status without treating that source as an RFC reply parent.

## `icloud mail status --outbound`

```bash
icloud mail status --outbound
icloud mail status --outbound --limit 50
icloud mail status --outbound --json
```

Lists the most recent bounded outbound attempts for the selected account without contacting iCloud. Compact records include stable attempt/message/thread IDs, status, attachment counts/bytes, reconciliation and error codes, retry linkage, update time, and one next action. JSON adds accepted/created timestamps and a reconciled location ID when known. Both forms omit submitted recipients, BCC, subject, bodies, custom headers, idempotency keys, attachment paths, hashes, and private blob coordinates.

Each attempt also reports `kind=send|reply|reply_all|forward`. Reply attempts include only the stable `parent_message_id`; forward attempts include only the stable `source_message_id`. Neither exposes recipient or body payloads.

Use `icloud mail sync --reconcile-outbound` to fetch current Sent evidence. Exact unique generated Message-ID evidence confirms a pending sent attempt or resolves an unknown attempt. Missing evidence stays unmatched; conflicting exact evidence stays unknown with `ambiguous_sent_evidence`. Failed attempts can be retried only with `--retry-of` and a new idempotency key. Unknown attempts additionally require `--retry-unknown --yes` after checking Sent and the recipient.

## `icloud mail inbox`

```bash
icloud mail inbox
icloud mail inbox --folder sent
icloud mail inbox --folder fld_…
icloud mail inbox --query invoice --unread
icloud mail inbox --cursor cur_…
icloud mail inbox --limit 50
icloud mail inbox --json
```

Reads a bounded page from local state without contacting iCloud. The command defaults to Inbox; `--folder` accepts the same selectors as sync. Without filters, compact records include stable ID, sender, subject, best available date, true unread/flagged states, nonzero attachment count, and a bounded decoded-text preview.

`--query`, sender/participant/subject/RFC ID, attachment, date, read, and flag filters use the same indexed local path as `mail search`. Filtered compact records identify the matched field and bounded excerpt. `--cursor` continues only the exact same account, folder, text, and filters. JSON adds `next_cursor` and query/index metadata while preserving the existing sanitized message objects. An unseen Sent location does not make an outbound message unread.

## `icloud mail search`

```bash
icloud mail search invoice
icloud mail search --from billing@example.com --unread
icloud mail search launch --result threads
icloud mail search --attachment-type application/pdf --after 2026-08-01
icloud mail search invoice --limit 50 --cursor cur_…
icloud mail search invoice --remote
icloud mail search --remote --folder archive --from billing@example.com --unread
icloud mail search invoice --remote --all-folders
icloud mail search invoice --json
```

Searches synchronized local mail without contacting iCloud or changing read state. A positional query matches subject, From and Reply-To, To and CC participants, decoded message text, attachment filenames and media types, and RFC Message-ID. BCC is excluded. Plain query terms are combined with AND.

`--result messages|threads` selects individual message hits or one aggregate per reconstructed conversation. Text search sorts by local relevance, then newest stable message or thread identity; filter-only search sorts newest first. Compact output includes the matched field and a bounded excerpt. The default limit is 20 and the maximum is 100.

Common filters:

| Flag | Behavior |
| --- | --- |
| `--folder <selector>` | Require a current synchronized location in one folder. |
| `--from <text>` | Match From or Reply-To name/address. |
| `--participant <text>` | Match From, Reply-To, To, or CC name/address. |
| `--subject <text>` | Match subject text. |
| `--rfc-id <id>` | Match one RFC Message-ID, with or without angle brackets. |
| `--attachment-name <text>` | Match attachment filename text. |
| `--attachment-type <text>` | Match attachment media type text. |
| `--unread` / `--read` | Select logical unread or read state. |
| `--flagged` / `--unflagged` | Select logical flagged or unflagged state. |
| `--after <time>` | Include messages at or after RFC3339 or a UTC `YYYY-MM-DD` date. |
| `--before <time>` | Include messages before RFC3339 or a UTC `YYYY-MM-DD` date. |
| `--cursor <cursor>` | Continue the exact account, query, filters, and result kind. |

Free text and structured filters combine with AND. A cursor used with a changed query, filter, account, or result kind exits with usage rather than silently changing the result boundary.

JSON returns sanitized message or thread hits, rank, matched field, bounded excerpt, page metadata, and index status. It omits credentials, BCC, private raw-storage coordinates, complete bodies, HTML, raw MIME, and attachment bytes.

`--remote` contacts iCloud and returns bounded uncached provider-message summaries with `rem_…` references. It defaults to Inbox; `--folder` selects one current selectable folder, while `--all-folders` explicitly fans out to at most 10 current selectable folders. Remote results are newest-first and report folder/UID identity, envelope sender/To/CC, subject, internal date, read/flag state, RFC822 size, a bounded decoded provider preview, matched field, and a stable local message ID only when the provider location is already cached.

Remote search supports positional provider text, `--from`, `--participant`, `--subject`, `--rfc-id`, read/flag state, and date filters. It returns messages only. `--result threads`, attachment filename/type filters, and `--cursor` return usage errors because those require the local indexed/reconstructed model. Remote `more=true` means matching provider results were truncated by `--limit`; `partial=true` means the bounded recent provider range ended before the folder's older UID space and the caller should narrow the query. JSON retains the same privacy exclusions as local search.

## `icloud mail search repair`

```bash
icloud mail search repair
icloud mail search repair --limit 100
icloud mail search repair --json
```

Rebuilds a bounded batch of incomplete decoded-body search entries from already synchronized local raw messages. It does not contact iCloud, redownload a mailbox, or change stable message, location, attachment, or thread IDs. The default limit is 20 and the maximum is 100. Compact and JSON results report selected, repaired, failed, remaining pending, and error counts.

## `icloud mail latest`

```bash
icloud mail latest
icloud mail latest --query launch --unread
icloud mail latest --participant teammate@example.com --folder sent
icloud mail latest --cursor cur_…
icloud mail latest --limit 50
icloud mail latest --json
```

Lists the newest locally reconstructed conversations without contacting Apple. The default returns 20 threads and the maximum is 100. Without filters, compact output reports stable thread ID, canonical subject, logical message count, unread count, normalized non-BCC participants, current folder IDs, attachment count, latest message time, and `reconstruction=local_rfc_graph`.

`--query` and the same structured flags as `mail search` restrict threads by matching member messages. Each conversation appears once with its best matched message, field, and bounded excerpt. `--cursor` is bound to the exact account and filters. JSON preserves aggregate thread objects and adds cursor and query/index metadata.

Thread identity is local. The CLI uses ordered normalized `References`, then `In-Reply-To`, and only a conservative subject-and-participant fallback when RFC relationship headers are absent. It does not claim an Apple-native thread ID.

## `icloud mail thread show`

```bash
icloud mail thread show thr_…
icloud mail thread show msg_…
icloud mail thread show thr_… --limit 50
icloud mail thread show thr_… --cursor cur_…
icloud mail thread show thr_… --full
icloud mail thread show thr_… --body-only
icloud mail thread show thr_… --format html
icloud mail thread show thr_… --headers
icloud mail thread show thr_… --metadata-only --verbose --json
icloud mail thread show thr_… --format raw --json
```

Resolves a canonical thread ID, retired alias, or stable message ID and returns a bounded chronological page. Compact output prints the aggregate once, then one bounded message record per logical message. When `more=true`, pass the reported `next_cursor` back through `--cursor`; cursors are bound to the selected thread and the final page omits one. `--full` expands complete decoded text for every selected message, while `--body-only` writes chronological bodies without metadata wrappers. HTML, decoded public headers, MIME metadata, and JSON remain explicit choices.

Compact reads and plain `--metadata-only` do not open raw message files. Explicit body, header-decoding, HTML, raw, or not-yet-indexed verbose MIME modes fail visibly when their synchronized source content is unavailable. Verbose graph evidence exposes child, relationship, token, parent, and status fields but not internal evidence-row IDs.

Plain multi-message raw output is rejected because concatenated RFC822 streams are ambiguous. Use raw JSON for bounded per-message payloads or `mail message raw <message-id>` for exact streaming.

`--metadata-only --verbose --json` includes bounded edge evidence with child message, relationship source, ordered reference position, token, resolved parent, and status such as `resolved`, `unresolved`, `malformed`, `self`, `cycle`, `grouped`, or `isolated`. Metadata-only removes thread and message excerpts. BCC and private blob coordinates remain excluded.

Late roots or bridge messages may merge provisional threads transactionally. One existing ID remains canonical and retired IDs become durable aliases. Message, location, MIME-part, attachment, and blob IDs are never rewritten by thread reconstruction.

## `icloud mail mark-read` / `mark-unread`

```bash
icloud mail mark-read msg_…
icloud mail mark-read msg_… --folder INBOX
icloud mail mark-unread msg_… --json
```

Adds or removes `Seen` on one exact current provider location. Without `--folder`, the message must have exactly one eligible present location. A message with several eligible copies returns conflict rather than guessing. `--folder` accepts a stable folder ID, decoded name, exact wire name, or unambiguous kind and still requires exactly one matching location.

The provider UID and UID validity are taken from synchronized local state; the command never searches mailboxes by Message-ID or thread reference. It stores the flags returned by iCloud and recomputes the message's unread and flagged summaries across every eligible current location. Deleted or absent locations are excluded.

## `icloud mail flag` / `unflag`

```bash
icloud mail flag msg_…
icloud mail flag msg_… --folder fld_…
icloud mail unflag msg_… --json
```

Adds or removes `Flagged` at one location with the same resolution and ambiguity rules as read state. Unknown provider flags are preserved. Conditional updates use the synchronized mod-sequence when iCloud exposes one; a concurrent change returns conflict after the current server state has been persisted.

Compact success reports the action plus stable message, location, and folder IDs and the recomputed unread/flagged summaries. JSON additionally includes UID identity, structured flags, mod-sequence, last flag-check time, and schema version. No command in this phase offers implicit all-locations or thread-wide mutation.

## `icloud mail move` / `copy`

```bash
icloud mail move msg_… --from inbox --to Projects --dry-run
icloud mail move msg_… --from inbox --to Projects --idempotency-key move-01 --yes
icloud mail copy msg_… --from inbox --to Receipts --idempotency-key copy-01 --yes
```

Changes one exact current provider location. Both commands require `--from` and `--to`; selectors accept a stable folder ID, decoded name, wire name, or unambiguous discovered kind. The destination must be current, present, selectable, and distinct from the source.

`move` prefers UID MOVE. Without MOVE, it uses COPY followed by exact UID STORE/UID EXPUNGE only when the provider advertises a safe capability. It never runs mailbox-wide EXPUNGE. `copy` retains the source. Provider UID mappings create the exact destination location immediately; missing or ambiguous mapping evidence produces an `unresolved` mutation for later folder synchronization.

| Flag | Purpose |
| --- | --- |
| `--from <folder>` | Select one exact current source location. Required. |
| `--to <folder>` | Select one current destination folder. Required. |
| `--idempotency-key <key>` | Return the existing `mut_…` result for the same exact request without another provider write. |
| `--dry-run` | Resolve and preview the location operation without IMAP or durable intent. |
| `--yes` | Authorize non-interactive provider mutation. |
| `--json` | Emit a stable structured preview or sanitized outcome. |

## `icloud mail archive` / `unarchive`

```bash
icloud mail archive msg_… --dry-run
icloud mail archive msg_… --from inbox --idempotency-key archive-01 --yes
icloud mail unarchive msg_… --idempotency-key unarchive-01 --yes
```

`archive` moves one eligible non-Sent location to the currently discovered Archive role. It never selects a Sent, Archive, or Trash source. When several eligible locations exist, `--from` is required. `unarchive` accepts only one current Archive source and moves it to the discovered Inbox. The commands fail instead of inferring roles from English folder names when iCloud has not advertised the necessary metadata.

## `icloud mail trash` / `restore`

```bash
icloud mail trash msg_… --from inbox --idempotency-key trash-01 --yes
icloud mail restore msg_… --to inbox --idempotency-key restore-01 --yes
```

`trash` moves one eligible location to the discovered Trash role. `restore` accepts only a current Trash source and requires `--to`; the CLI does not invent a previous folder. Both use the same durable intent, MOVE/fallback, UID mapping, idempotency, dry-run, confirmation, and unresolved-recovery contract as general move.

## `icloud mail delete`

```bash
icloud mail delete msg_… --from trash --dry-run
icloud mail delete msg_… --from trash --idempotency-key delete-01 --yes
```

Permanently deletes one exact provider location. `--from` is mandatory. Safe UID EXPUNGE support is required before the provider write begins; ordinary mailbox-wide EXPUNGE is never used. The logical message, content, attachments, search identity, and thread remain available while another current location exists. The selected location becomes absent evidence rather than being silently removed from local history.

## `icloud mail status --mutations`

```bash
icloud mail status --mutations
icloud mail status --mutations --limit 50 --json
```

Lists bounded, payload-free location mutation intents and outcomes. Each record includes stable mutation/message/source/destination IDs, action, status, provider method, source presence, mapping and reconciliation state, error code, update time, and next action. Submitted folder names, subject, participants, body, attachment metadata, credentials, idempotency keys, request hashes, and private storage coordinates are omitted.

`confirmed` means local location state follows provider evidence. `failed` means no provider-side change was observed. `unresolved` means the provider may have copied, moved, deleted, or expunged content without complete confirmation; synchronize the affected folders and do not repeat the provider operation automatically. Repeating the same command with its original idempotency key returns the existing result without IMAP.

## `icloud mail message show`

```bash
icloud mail message show msg_…
icloud mail message show msg_… --full
icloud mail message show msg_… --format text --body-only
icloud mail message show msg_… --format html
icloud mail message show msg_… --headers
icloud mail message show msg_… --metadata-only
icloud mail message show msg_… --verbose
icloud mail message show rem_… --remote
icloud mail message show rem_… --remote --cache --full
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
| `--remote` | Treat the selector as a stale-safe `rem_…` provider reference and read a bounded current provider summary. |
| `--cache` | With `--remote`, persist the exact provider message through normal local ingestion before applying content flags. |

Default JSON reports text/HTML/raw/header/part availability, part count, and whether parsing was complete. `--full --json` adds decoded text; `--format html --json` adds HTML; `--headers --json` adds headers; `--verbose --json` adds MIME parts; `--format raw --json` adds base64 raw MIME up to 8 MiB. Use plain streaming modes for larger content.

Contradictory combinations return usage errors rather than choosing silently. Examples include `--headers --body-only`, content flags with `--metadata-only`, `--full --format html`, and plain `--format raw --verbose`.

An uncached remote summary allows compact or JSON metadata only. Complete text, HTML, headers, raw MIME, body-only output, and verbose MIME details require `--remote --cache`. Caching returns to the ordinary stable local `msg_…` read path and does not advance the folder sync checkpoint. A remote reference whose account, folder, or UIDVALIDITY no longer matches exits with usage or conflict before fetching another message at a reused UID.

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
icloud mail sync status --folder sent
icloud mail sync status --json
```

Shows one selected folder checkpoint locally. It defaults to Inbox and accepts a role, stable ID, decoded name, or exact wire name. `idle` means the latest page committed, `failed` means the frontier did not advance, and `repair_required` means iCloud reported a different mailbox identity and ordinary sync stopped.

## `icloud mail sync checkpoints`

```bash
icloud mail sync checkpoints
icloud mail sync checkpoints --limit 50
icloud mail sync checkpoints --json
```

Lists bounded account-scoped checkpoints from local state, including Inbox, Sent, and any other enabled folder that has been synchronized.

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
schema=16
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

Every account and credential lifecycle command above, `account test`, every folder command, enabled-folder sync/read/local-or-remote-search/status/checkpoints, targeted hydration, outbound send/reply/forward preview and outcomes, latest/thread reads, message read and state commands, attachment list/save/save-all, `doctor`, `version`, and `capabilities` supports `--json` where a native byte stream is not requested. Validation, doctor, folder reads/status, thread reads, message reads, and outbound dry-run review support bounded `--verbose` detail where advertised. `mail message raw`, `message export --output -`, `attachments save --output - --binary`, and outbound dry-run `--format text|html|raw` retain their explicit native formats; completion and man generation do likewise. Local folder, search, and thread lists stay preview-only. Remote summaries stay metadata-only until `--cache`; stable local thread/message reads then provide explicit full-text, HTML, raw, headers, metadata-only, verbose, and JSON modes.

Compact output uses vertical blocks: result type first, stable identity plus the first field next when present, then one remaining field per line. A blank line separates consecutive records or a final summary. Errors use the same block shape on stderr.
