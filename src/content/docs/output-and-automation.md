---
title: Output and automation
description: Understand compact defaults, strict JSON read and write requests, bounded bulk JSONL, versioned schemas, outbound-review privacy, stdin safety, stderr, request IDs, timeouts, and stable exit classes.
order: 9
category: Guides
summary: The agent-facing contract for bounded text, explicit JSON, strict read and write requests, bulk state changes, public schemas, safe message input, and stable failures.
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

`account list`, `mail messages list`, `mail search`, `mail threads list`, `mail threads show`, attachment lists, checkpoint lists, `mail operations outbound list`, and `mail operations mutations list` return 20 records by default, accept `--limit` through 100, and report `more=true` when truncated. Every bounded local inventory returns an opaque `next_cursor` when another page exists; pass it through `--cursor` to continue without repeating earlier records. Account/target/status-scoped cursors reject reuse outside the inventory that issued them, and terminal pages omit `next_cursor`. Local search cursors additionally bind the complete query, filters, and result kind. `mail inbox`, `mail latest`, `mail thread show`, `mail status --outbound`, and `mail status --mutations` remain compatibility aliases over those same request and rendering paths. Remote search is intentionally different: it has no cursor, reports `partial` plus the bounded provider positions examined, and requires a narrower query when older provider state was not scanned. Normal sync and historical backfill each apply their own 1–100 bound; repair applies the bound per finding/repair category and reports `more=true` when another bounded pass is needed. Mutations, hydration, rebaseline, repair, and export commands return outcomes and stable IDs or counts rather than echoing submitted names, addresses, subjects, paths, passwords, or remote references.

## JSON is explicit

Commands that advertise `--json` return one envelope:

```bash
icloud --request-id account-01 account show --json
```

```json
{"ok":true,"data":{"account":{"id":"acct_…","name":"Personal","email":"owner@icloud.com","imap_username":"owner","status":"connected","default":true,"credential_scheme":"keyring","imap_capabilities":["IDLE","IMAP4REV1"],"smtp_capabilities":["AUTH"],"last_healthy_at":"2026-08-06T03:00:00Z","last_tested_at":"2026-08-06T03:00:00Z","created_at":"2026-08-06T02:30:00Z","updated_at":"2026-08-06T03:00:00Z"},"schema_version":19},"meta":{"request_id":"account-01"}}
```

Account lifecycle, credential, validation, doctor, folder discovery/policy, enabled-folder sync, historical backfill, current-tail rebaseline, bounded repair, local and explicit remote search, targeted hydration, local folder reads, thread reads, message reads/imports and state mutations, attachment list/save/save-all, draft lifecycle commands, operation history, outbound send/reply/forward preview and outcomes, account reads, `version`, and `capabilities` support JSON when stdout is not carrying native bytes. Folder list/show/refresh, thread reads, draft sync, and repair findings also support bounded `--verbose` evidence. Completion and man generation intentionally return native text formats.

Thread JSON remains local reconstruction data. `mail threads list --json` returns aggregates only. `mail threads show --metadata-only --verbose --json` returns chronological messages and bounded RFC graph evidence without excerpts, BCC, raw-storage coordinates, bodies, or internal edge IDs. Local mute state is exposed as `muted` on the current thread and `thread_muted` on message context; it is not a provider flag. Thread state/location writes return the canonical `thread_id`, optional `redirected_from`, full `thread_message_count`, and bounded page continuation separately from the eligible per-message results. `--format raw --json` is explicit and bounded; plain multi-message raw output is rejected.

Credential metadata JSON omits internal IDs, keyring references, environment names, file paths, and values. Outbound operation JSON omits recipients, BCC, subject, bodies, idempotency keys, attachment paths, and private blob coordinates. Error envelopes omit wrapped provider and storage causes.

## Use strict JSON requests for complex reads and writes

Complex bounded reads accept `--input-json <path|->` when a request would otherwise require several selectors or filters. This includes account and folder inventories, folder refresh/show, message and thread lists, local or remote search, thread/message show, attachment metadata, operation history, and synchronization status/checkpoints. Local message/search/thread requests may use `vip` and `blocked` to select account-local exact-sender preferences; those fields are rejected for remote provider search. The JSON request selects the same service path as the equivalent flags; it does not create a second read API.

Search continuation uses two deliberately distinct opaque cursor contracts. Local synchronized search emits `cur_…`; provider search emits `rcur_…` whenever another bounded invocation can continue the same remote traversal. A one-folder remote cursor binds account, stable folder ID, UIDVALIDITY, normalized provider query/filter signature, result limit, and the next unscanned UID frontier. An all-folder remote cursor additionally binds the complete current selectable-folder membership/known UIDVALIDITY signature and carries bounded per-folder UID frontiers plus one merge head per seeded folder. Supplying either cursor with changed scope is rejected before provider search. `rem_…` remains a separate targeted-message reference used for remote summary/hydration, never a paging cursor.

All-folder remote search deliberately bounds each invocation to at most two provider mailbox operations total. Those operations may be bounded search scans or exact buffered-head summary fetches; search scans can examine at most 4,096 candidate UID positions in one invocation. Establishing the merge frontier across more than two folders, or advancing a very sparse folder until its next exact hit is known, can therefore return `count=0`, `partial=true`, and a usable `next_cursor`; continue that cursor rather than interpreting the empty page as “no matches.”

For example, a search request can move shell-sensitive filters into one strict object while keeping output selection explicit:

```json
{
  "query": "invoice",
  "folder": "inbox",
  "from": "billing@example.com",
  "unread": true,
  "limit": 20
}
```

```bash
icloud mail search --input-json search.json --json
```

Message and thread content expansion follows the same rule. Request fields such as `full`, `format`, `headers`, `body_only`, `metadata_only`, and `verbose` map to the normal content validators. Omitting them stays bounded and preview-only; structured input never implies full text, HTML, raw MIME, or decoded headers. `--json` remains an output flag rather than an input-request field.

Every command in the explicit automation write contract—account/folder configuration, bounded sync/repair, local sender/thread preferences, provider message mutations, drafts, outbound mail, and caller-selected exports—also accepts `--input-json <path|->`. `icloud capabilities --json` is the source of truth for both read and write structured-input commands. JSON enters the same validators and safety gates as flags; the contract remains command-scoped rather than becoming a generic RPC layer, and foreground watch/native byte streams keep their existing interfaces.

For example, preview a send request from stdin:

```bash
cat >request.json <<'JSON'
{
  "to": ["client@example.com"],
  "subject": "Project update",
  "body_file": "message.txt",
  "idempotency_key": "project-update-07"
}
JSON

icloud mail send --input-json request.json --dry-run --json
icloud mail send --input-json request.json --yes --json
```

Equivalent request flags and positional selectors cannot be mixed with `--input-json`; a mixed request exits `2` before a read service or provider mutation runs. For writes, process controls remain flags: `--yes` and `--dry-run` preserve the existing confirmation model. Global `--home`, `--account`, `--timeout`, `--no-input`, and `--request-id` keep their normal precedence, and supported output flags such as `--json` remain separate from the request document.

Use this precedence consistently in automation: global selection/lifetime flags choose local state, account, timeout, prompting policy, and request correlation first; then choose exactly one command-input form (ordinary positional/request flags, `--input-json`, or a command's documented JSONL input); then apply write safety controls such as `--dry-run` and `--yes`; finally choose one structured output mode. `--json` and `--jsonl` are mutually exclusive. Where a read exposes content-expansion fields in its request schema, those fields stay subject to the same explicit privacy rules as their ordinary flags. Where a write supports an idempotency key, it is part of that write request: provide it either as `--idempotency-key` with ordinary flags or as `idempotency_key` in JSON, never both.

JSON decoding is strict: unknown fields, trailing values, empty input, and requests larger than 1 MiB are rejected. A write request read from stdin cannot also request a body or password from stdin; use an input file for one of those streams instead. Credential JSON never accepts a plaintext password property. It can select `password_stdin`, `credential_env`, or `credential_file`, preserving the same secret-handling boundary as the human flags.

The exact JSON shape for each structured read or write is published in `icloud capabilities --json`. Its automation command list links each command to a versioned request schema and response schema. Canonical read commands and their compatibility aliases point to the same request schema. The installed npm package ships the same files under `schemas/v1/`, and the docs site serves them from `/schemas/v1/`.

## Bulk message state is bounded and explicit about partial success

Read/flag state commands accept up to 100 stable message IDs at once:

```bash
icloud mail mark-read msg_… msg_… msg_…
```

For structured pipelines, either send one JSON object with `message_ids` through `--input-json`, or use line-delimited items:

```bash
cat changes.jsonl | \
  icloud mail flag --input-jsonl - --mode continue-on-error --jsonl
```

Each JSONL input line contains one `message_id` and an optional exact `folder`. `--mode atomic` is the default: the complete bounded input shape and stable IDs are validated before provider writes begin, then target resolution and provider mutation proceed item by item and execution stops on the first failure. iCloud cannot roll back provider writes that already succeeded earlier in the batch, so the command explicitly reports `partial=true`, requested/attempted/updated/failed/skipped counts, and a non-zero exit instead of pretending those writes were undone. `--mode continue-on-error` attempts every syntactically valid item and returns a non-zero exit if any target or provider operation failed.

`--jsonl` emits one typed `item` event per attempted operation followed by a typed `completed` event containing mode, requested/attempted/updated/failed/skipped counts, and `partial`. Compact output uses one small result block per attempted item plus one bounded summary.

The same truthful partial-work rule applies to repeated targeted hydration and multi-folder sync. By default, `mail sync --hydrate ...` and multi-folder `mail sync` stop after the first failing item/folder, but any earlier committed message or folder remains committed and is reported in the structured error details. Add `--continue-on-error` when independent remaining hydration references or folders should still be attempted. With `--jsonl`, every attempted item keeps its original index and input identity, reports its result/error plus `committed_state`, and the stream ends with one `completed` event whose requested/attempted/completed/failed/committed/partial counts are derived from those records. A handled item failure returns a non-zero exit without appending an unrelated second stderr error event; JSONL never pretends earlier durable work was rolled back.

Mailbox archive import follows the same truthful partial-work rule, with an additional durable byte/message frontier because an mboxrd source can span many invocations. `mail mailbox import <file.mbox> --to <folder>` attempts at most 100 records per invocation by default. `--jsonl` emits one ordered `item` event for every archive record attempted in that pass and one terminal `completed` event containing the durable job summary. A failed record preserves a non-zero exit even if `--mode continue-on-error` later confirms independent records.

The terminal mailbox-import summary separates this invocation's `attempted`, `confirmed`, `failed`, and `reused` counts from job-wide `imported_count`, `next_index`, and `next_offset`. `partial=true` means unresolved durable work remains. `more=true` means the contiguous frontier has not reached the immutable source byte size. `complete=true` is emitted only after every record through EOF is confirmed. Later records confirmed beyond a failed gap increase `imported_count`, but they never advance the durable frontier across that gap.

Automation may submit `file`, `to`, optional `mode`, and optional `max_messages` through the published `mailbox-import-input.schema.json`. Source paths and private per-record idempotency keys are excluded from normal job/item output. Dry-run may report the source SHA-256 and byte size as review evidence, matching the single-message import review model.

The explicit drain helpers keep the same rule while repeating several bounded page transactions. `mail sync --until-current` and `mail sync backfill --until-complete` use `--max-pages`, `--max-messages`, and the global `--timeout` as invocation-wide ceilings while `--limit` still bounds each provider page. JSONL emits every committed page before its final drain event with the stop reason, so automation resumes from the normal persisted checkpoint/backfill frontier rather than maintaining a second client-side cursor.

## Schemas and capabilities are public contracts

```bash
icloud capabilities --json
```

The capability envelope includes the CLI version, local data-schema version, automation-contract version, supported bulk modes/bounds, stable schema identifiers, and per-command read/write request and response schema IDs. When you explicitly select an account with `--account`, capabilities may also include the IMAP/SMTP capability strings already cached for that account; this lookup is local-only and never resolves a credential or opens a network connection.

`capabilities --json` also reports the effective inbound raw-message and decoded-attachment byte limits. These are application configuration, not provider claims, and are independent from the iCloud composed-message send limit.

The versioned JSON Schemas cover accounts, folders, messages, threads, attachments, checkpoints, outbound attempts, drafts, JSONL events, errors, the common output envelope, and every documented structured read/write input shape. Compose attachment objects may include `disposition` (`attachment` or `inline`) and `content_id` alongside the reviewed path, filename, and media type; the same shape is shared by send, reply, forward, and draft create/update. Mailing-list unsubscribe automation uses `unsubscribe-input.schema.json` with only `message_id` plus an optional safe method ID; tokenized endpoints and private mailto payloads are never request fields. Local sender-preference automation uses `sender-list-input.schema.json` and `sender-preference-input.schema.json`; local thread mute/unmute uses `thread-preference-input.schema.json` with one reviewed `target`. Returned preference records are explicitly marked local-only at the command envelope. Read schemas contain selectors, filters, pagination, and explicit content choices only; they do not expose credentials, BCC compose payloads, private blob coordinates, or idempotency material. Contributors regenerate schemas together with the Cobra command reference, man pages, and completion assets using `make references`; checks fail if committed generated output drifts.

Shell completion follows the same local-only rule. Account and folder suggestions use cached stable IDs with friendly descriptions and do not run folder status, authenticate, or contact iCloud.

## Outbound review and success have different privacy contracts

`mail send --dry-run` is an explicit message-review mode. Its compact output includes the normalized recipient lists, including BCC, the subject, the text body once, body-part and attachment counts, attachment filenames/media types/sizes/hashes, and a safety summary proving that SMTP was not contacted and no attempt was created. Reply dry runs add the exact parent and derived recipients. Forward dry runs add the exact source, quoted/EML/none representation, selected original attachments, and a bounded text excerpt. `--headers`, `--verbose`, `--format text|html|raw`, and `--json` deliberately expand those reviews. Dry-run JSON contains normalized recipients, BCC, subject, bodies, Reply-To, safe custom headers, and reviewed attachment metadata; treat it as private message content rather than routine telemetry.

A real outbound operation behaves differently. Non-interactive send, reply, or forward requires `--yes`, and automation should supply a stable `--idempotency-key`. Compact or JSON success returns the account ID, stable attempt/message/thread IDs, status, reconciliation state, attachment count/bytes, reuse state, schema version, and request ID. Reply adds only its stable parent ID; forward adds only its stable source ID. It does not echo recipients, BCC, subject, bodies, custom headers, attachment paths, attachment names, content hashes, the idempotency key, or raw-storage coordinates.

`mail unsubscribe` uses the same review/authorization split without turning list headers into a generic web client. Dry-run is local-only and returns safe alternatives (`one-click`, `mailto:1`, and so on), method kind, host, selection state, and `network_contacted=false`; it does not verify DKIM or make a network request. Real non-interactive execution requires `--yes`. One-click success reports only the source message ID, `method=one_click`, host, confirmation status, and signing DKIM domain; the opaque HTTPS endpoint is never returned. Mailto success reports the safe method/host plus the durable outbound attempt ID/reuse state while still omitting its recipient, subject, body, and private deterministic idempotency key. Several mailto choices require `--method`; automation must not infer one from raw URL order.

`mail send` and draft create/update/send may select a configured sender with the request field `from`, using either a stable `mid_…` ID or configured address. Normalized draft and outbound records keep only the safe stable `sender_identity_id`; credentials and SMTP authentication material remain account-owned and never enter those public schemas. The sender identity participates in normalized outbound request identity, so reusing an idempotency key with another sender returns a conflict instead of silently changing From.

Draft lifecycle output follows the same progressive-disclosure rule without pretending a draft is a sent attempt. `draft create`, `update`, `list`, `show`, and `sync` keep BCC addresses out of ordinary compact and JSON output; `draft show` exposes complete decoded content, headers, HTML, or raw MIME only when those modes are explicitly requested. `draft send --dry-run` is an intentional outbound review and may expose the saved recipients and content just like `mail send --dry-run`. A real non-interactive `draft send` or `draft delete` requires `--yes`; successful send output is limited to stable draft/attempt/message/thread identity and status, while deletion reports the stable draft ID and provider-copy count rather than echoing message payloads.

Location-changing dry runs follow the same explicit-review rule. `mail move`, `copy`, `archive`, `unarchive`, `trash`, `restore`, `junk`, `not-junk`, and `delete` resolve exact current sources and destinations locally, prove that IMAP was not contacted, and create no mutation intent. Each accepts up to 100 direct message targets. A multi-target request validates the complete bounded target set before provider contact; `--mode atomic` then fails fast without pretending earlier accepted writes can be rolled back, while `--mode continue-on-error` attempts independent remaining items. `--jsonl` preserves input order and ends with requested/attempted/completed/failed/skipped/partial accounting. Real non-interactive execution requires `--yes`; automation should use a stable idempotency key. One batch key deterministically produces separate per-item replay identities, while structured `items`/JSONL may provide per-item folder and idempotency overrides. Single-item output and behavior remain unchanged. Success returns stable mutation/message/location/folder IDs, provider method, UID mapping, status, reconciliation state, aggregate unread/flagged values, and reuse state without echoing message payloads or submitted folder names.

`mail threads mute|unmute` is different from provider mutation: it resolves the current canonical local RFC conversation, stores only anchored client-local preference state, and makes no provider call. `mail threads mark-read|mark-unread|flag|unflag` and `mail threads move|copy|archive|unarchive|trash|restore|junk|not-junk|delete` reuse those same per-message mutation engines after resolving one canonical local RFC conversation. Thread state writes require `--folder`; thread location writes require `--from`. Only canonical members with an eligible current copy in that exact scope are attempted, so a conversation's Sent-only member is not changed by an Inbox-scoped command. Up to 100 chronological members are examined per provider-mutation page. If the canonical thread exceeds the selected bound, the first provider write is rejected unless `--page` explicitly opts into a reviewed partial page; subsequent invocations use the returned thread-bound `next_cursor`. JSON/JSONL include the full `thread_message_count`, canonical ID/alias redirect, and `more`/`next_cursor` separately from eligible requested/attempted counts. A page with no eligible copy makes no provider call and can still advance the cursor. Pending/degraded reconstruction rejects provider thread mutation rather than silently treating an incomplete projection as full scope.

`mail selection` is the immutable review boundary for query-derived or otherwise curated writes. `selection create` freezes at most 100 stable logical targets from explicit `msg_…`/`thr_…` IDs or one bounded **local** search page; `--scope-folder` also freezes the exact provider-location ID, UIDVALIDITY, UID, and mod-sequence used by later writes. Selection records are local-only and contain source/request hashes plus stable IDs and timestamps, never the source query text, message subjects/participants/bodies/excerpts, attachment names, or raw MIME. `selection show` exposes only that bounded structural evidence. `selection apply --action read` reads exactly the frozen logical IDs without rerunning the source query or contacting iCloud.

Provider-changing selection actions use a two-step review hash. `selection apply … --dry-run` validates the unexpired account-bound snapshot and returns a content-independent `request_hash`; location actions also resolve the same exact per-message plans used for execution, so changing a destination or semantic-role resolution changes the hash. Real apply requires that exact `--request-hash` and rejects expiry, account mismatch, stale location evidence, or changed reviewed requests before new provider contact. JSON/JSONL write results include `selection_id`, the immutable `selection_hash`, and the reviewed `apply_request_hash`. Location replay derives private per-message idempotency identities from that reviewed selection request, so an already accepted write is reused rather than duplicated even after a move changed the original source location.

`mail empty trash`, `mail empty junk`, and `mail empty folder <folder>` use a separate short-lived exact-location purge review because one logical message can legitimately have multiple provider copies in the same folder. A dry run freezes at most 100 stable `loc_…` targets with UIDVALIDITY/UID/mod-sequence evidence and returns `purge_id`, `purge_hash`, `more`, `blocked_unresolved`, and a hash-bound apply request without provider writes. Real execution requires the matching `--purge-id` and `--request-hash`; JSONL then carries the purge identity/scope/folder and the normal ordered per-location mutation accounting. New purge pages exclude locations already marked Deleted plus pending/unresolved delete intents, but count those ambiguous durable outcomes separately so `count=0` cannot falsely imply the folder is clear. Every actual deletion goes through exact UID-targeted permanent delete rather than generic mailbox EXPUNGE. Trash/Junk reviews also expose `retention_warning=provider_auto_delete_possible`.

An `unresolved` location mutation is not safe to retry automatically. The provider may have accepted COPY, MOVE, STORE, or EXPUNGE before the connection failed. Read `mail operations mutations list` or `show mut_…`, synchronize the exact source and destination folders, and inspect the message's current locations. Repeating the same request with the same idempotency key returns the original `mut_…` record without another provider write. Treat an interrupted folder rename similarly: run `mail folders refresh` rather than repeating the provider command so rename recovery can settle the new name against fresh LIST evidence.

`mail operations outbound list` is the bounded operational view for recent attempts, while `mail operations outbound show send_…` retrieves one exact stable attempt without paging. Their compact/JSON records contain stable IDs, status, attachment counts/bytes, reconciliation/error codes, retry linkage, update time, and one next action. They never repeat submitted recipients, subject, bodies, idempotency keys, or private blob coordinates. `mail sync --reconcile-outbound` returns only bounded reconciliation counts in addition to the normal Sent sync summary.

An indeterminate post-DATA result uses status `unknown`, returns the stable attempt ID on stderr, and is always `retryable=false`. The caller must not infer that a temporary network cause makes the send safe to repeat. Reusing the same normalized request and idempotency key retrieves the original attempt without another SMTP submission.

An `account test --json` failure writes one error envelope to stderr. Its `details.stage` and bounded `details.reports` identify the failing protocol without including the credential reference, password, raw server response, or debug transcript.

## Use JSONL for the watch stream

`mail watch` is a stream rather than a one-shot result, so structured automation uses JSONL instead of a single JSON envelope:

```bash
icloud mail watch --jsonl
icloud mail watch --all-accounts --jsonl
```

Every output line is one complete JSON object with `event`, `sequence`, `account_id`, and `timestamp`. Payloads identify only the lifecycle data needed for startup, mode changes, IDLE, provider changes, bounded sync results, reconnects, degraded operation, stopping, and completion. Sequences are strictly increasing within each account even when several account workers share one foreground process.

`--all-accounts` pages the local configured-account inventory and starts up to 100 account workers; it never treats the first account page as the complete set. If more than 100 accounts are configured, the command fails before starting workers and asks the caller to narrow with `--account` or split the work across bounded foreground processes.

Compact watch output suppresses repeated idle and unchanged-sync chatter. It also suppresses changed-mail attention only after that provider change has already synchronized and every committed changed logical message can be resolved confidently to a locally muted reconstructed conversation. This is presentation suppression, not sync suppression: `--verbose` and JSONL deliberately include the muted change and sync events so a consumer can maintain exact lifecycle state, and the mail remains indexed/readable normally. If exact post-sync thread identity is unavailable or uncertain, compact output remains noisy rather than guessing. Initial or terminal command failures requested with `--jsonl` stay JSONL on stderr rather than mixing a compact error into the structured stream.

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

Version, capabilities, completion, man, and help remain disk-free. Account reads, `mail messages list` with any folder selector, local `mail search`, `mail search repair`, `mail threads list`, `mail threads show`, local `mail message`, `mail mailbox export`, attachment reads, sync status, checkpoint reads, exact operation `show` reads, `mail sync repair --check`, local `doctor`, outbound dry runs, message-import dry runs, mailbox-import dry runs, unsubscribe dry runs, folder-administration dry runs, and location-mutation dry runs never prompt or contact Apple. Message-import dry runs may stage and parse the selected local EML under the configured raw limit, but create no import intent. Mailbox-import dry runs fingerprint the regular archive and decode only the current frontier record; they create no import job and make no IMAP request. Unsubscribe dry-run uses only stored decoded headers: it performs no DKIM DNS lookup, HTTP POST, or SMTP send. Mailbox export keyset-pages cached provider locations and verifies local raw blobs only; it never refreshes a folder or advances synchronization. Compact thread reads and plain local `--metadata-only` use stored summaries without opening raw message files. Search repair may verify and parse already synchronized raw bytes, but it never contacts iCloud. `mail search --remote`, `mail message show rem_… --remote`, targeted hydration, confirmed message import, confirmed mailbox import, one-click unsubscribe, mailto unsubscribe, normal sync, historical backfill, current-tail rebaseline, `mail folders refresh`, real folder administration, read/flag mutations, real outbound mail, real location mutations, and live validation contact Apple and use the same global timeout. `mail sync repair --apply` stays local unless an exact affected mailbox needs fresh UID identity; in that case it reads status only for that folder. Streaming message, mailbox archive, attachment, EML import, mboxrd import, unsubscribe DKIM verification/HTTPS response handling, outbound MIME, COPY/MOVE, and reconciliation paths also honor the command timeout. `mail watch` is the deliberate long-running exception to a whole-command deadline: it runs until cancellation while applying the same timeout to each provider and synchronization attempt.

Repair is check-first for automation. `--check` returns bounded counts and one `next` action without changing local/provider state. `--apply` fixes only deterministic evidence; ambiguous dedupe identity and missing/corrupt canonical blobs remain findings. Deleting orphan files from the private staging directory is never implicit and requires `--apply --cleanup-orphans --yes`.

Remote compact/JSON results expose only the stale-safe `rem_…` reference, current folder/UID identity, bounded envelope fields, flags, size, provider preview, and an optional cached stable message ID. BCC, credentials, raw storage coordinates, raw MIME, full bodies, HTML, attachment bytes, and private local paths remain excluded. Hydration output omits the submitted remote reference and returns only stable local IDs and created/updated/cached status.

## Message content is explicit

`mail messages list` stays preview-only. `mail message show <id>` adds orthogonal content choices:

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

`mail threads show` supports the same content choices plus `--cursor`. Plain `--body-only` writes each selected body chronologically with a blank line between messages. Because a body-only stream intentionally contains no pagination metadata, automation should read `next_cursor` from a compact or JSON page first when a thread may exceed the chosen limit.

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
