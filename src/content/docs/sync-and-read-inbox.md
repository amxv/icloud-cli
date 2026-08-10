---
title: Sync and read mail
description: Synchronize enabled iCloud Mail folders, backfill older history deliberately, read cached mail offline, and recover synchronization state safely.
order: 6
category: Guides
summary: Bounded multi-folder sync, resumable historical backfill, deliberate current-tail rebaseline, check-first repair, compact local reads, and safe retries.
---

## Synchronize enabled folders

After validating the account and discovering folders, run:

```bash
icloud mail sync
```

The command processes each enabled, selectable folder independently. Inbox and Sent are enabled when first discovered; other folders join the workflow only after you enable them.

First setup is deliberately different by folder:

- Inbox downloads the newest bounded page instead of walking the whole mailbox.
- Sent records the current remote tail and downloads no old Sent history.
- Another enabled folder starts from its newest bounded page.

A fresh account therefore usually prints an Inbox result followed by a zero-fetch Sent result:

```text
mail-sync
fld_… account=acct_…
folder=INBOX
fetched=20
created=20
updated=0
reconciled=20
state_changes=2
last_uid=184
more=true
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
```

Sent setup does not claim the older messages are synchronized. It establishes a safe starting point so a later run can ingest only Sent copies created afterward.

Choose one folder by confirmed role, stable ID, decoded name, or exact provider name:

```bash
icloud mail sync --folder inbox
icloud mail sync --folder sent
icloud mail sync --folder fld_…
```

`--limit` applies separately to each selected folder and accepts 1–100 messages:

```bash
icloud mail sync --limit 50
```

`more=true` means that folder has another bounded page available. Repeat the same selection until it reports `more=false`. A failed folder page leaves its previous frontier unchanged and is safe to retry.

When you deliberately want the CLI to repeat those same bounded page transactions for you, use the drain helper with explicit global ceilings:

```bash
icloud mail sync --folder inbox --until-current --max-pages 20 --max-messages 1000
```

`--until-current` never replaces the folder checkpoint: every page is committed independently through the ordinary sync path, and the persisted checkpoint remains the only resume authority. `--max-pages` and `--max-messages` bound the whole invocation, while `--limit` still bounds each provider page. The global `--timeout` is the overall command deadline. Compact and JSONL output report every committed page plus a final `stop_reason` of `complete`, `max_pages`, or `max_messages`; when a deadline or provider failure interrupts the drain, rerunning the command resumes from the last committed checkpoint.

When several folders are selected, earlier folders can already be durably committed before a later folder fails. The CLI reports those completed folder summaries instead of hiding them behind the error. Use `--continue-on-error` to attempt the remaining independent folders, and add `--jsonl` for one typed success/error event per folder plus a final partial-completion event:

```bash
icloud mail sync --all-folders --continue-on-error --jsonl
```

`reconciled` counts existing locations whose remote flags were checked without downloading message bodies again. `state_changes` counts real differences. The bounded sweep rotates over time instead of turning one command into a whole-mailbox scan.

## Keep the local copy fresh in the foreground

After the first successful sync, keep new mail flowing into the same local cache with:

```bash
icloud mail watch
```

The command stays in the foreground until you press Ctrl-C. When iCloud advertises IMAP IDLE, the CLI waits efficiently on enabled Inbox and Sent, exits IDLE cleanly when a change arrives, runs the ordinary bounded sync path for that folder, then resumes waiting. If IDLE is unavailable, the startup stream says `mode=polling` and uses the explicit polling interval instead of pretending push notifications are active.

Default compact output reports lifecycle changes and useful sync results while suppressing repeated idle and no-change chatter:

```text
watch-started
acct_… poll=1m0s
reconcile=15m0s

watch-mode
acct_… mode=idle
reason=idle_supported
capabilities=IDLE,IMAP4REV1

watch-change
acct_… folder=INBOX
folder_id=fld_…
reason=change

watch-sync
acct_… folder=INBOX
folder_id=fld_…
fetched=1
created=1
updated=0
reconciled=20
state_changes=1
more=false
```

Use `--verbose` when you also want idle and unchanged-sync lifecycle detail. For an automation stream, use `--jsonl`; every line is one typed event with a per-account sequence number. The stream includes startup, mode, idle, change, sync result, reconnect, degraded, stopped, and completed events.

Polling and periodic reconciliation are configurable without changing the normal sync page bound:

```bash
icloud mail watch --poll-interval 2m --reconcile-interval 15m --limit 50
```

Periodic reconciliation refreshes folder state and all enabled folders, so a long-running watch does not rely only on notifications. `--timeout` is different for this foreground command: it bounds each provider/sync attempt rather than ending the watch process after that duration.

Watch every configured account with one foreground process when there are at most 100:

```bash
icloud mail watch --all-accounts
```

The account inventory is paged locally, so accounts beyond the first page are not silently omitted. More than 100 configured accounts returns a bounded-operation error with guidance to select one account or split the work across foreground processes.

Each account keeps its own ordered event sequence, credential reread/reconnect lifecycle, provider session, and watch ownership. A second watch for the same account fails with a retryable conflict instead of creating duplicate workers. Ordinary synchronization remains the only path that commits messages and checkpoints. When several account workers request a durable sync together, queued workers take turns on the local write path; one repeatedly changing account cannot continually jump ahead of accounts already waiting, and Ctrl-C also cancels workers that are waiting for their turn.

If the stream reports `degraded`, use the error code and stage rather than restarting it in a tight loop. Authentication failures use bounded reconnect backoff so you can replace the credential; a UIDVALIDITY change still requires the existing explicit repair/rebaseline decision rather than an automatic reset.

## Backfill older mail deliberately

Normal synchronization only moves forward. When you actually need older history, start a separate bounded backfill for one folder:

```bash
icloud mail sync backfill --folder inbox --limit 50
```

```text
mail-backfill
bf_… account=acct_…
folder=INBOX
folder_id=fld_…
fetched=50
created=45
updated=5
before_uid=165
next_before_uid=112
complete=no
```

The historical cursor is independent from normal new-mail synchronization. A successful backfill page can add older messages without moving the forward checkpoint, and ordinary `mail sync` can keep ingesting new mail while history is still incomplete. A canceled or failed page leaves the previous historical cursor unchanged; repeat the same command until `complete=yes`.

For an explicit bounded drain of several historical pages, use:

```bash
icloud mail sync backfill --folder inbox --until-complete --max-pages 20 --max-messages 1000
```

This is only a convenience loop over ordinary backfill pages. Each page advances the same durable historical frontier, `--limit` still bounds one provider page, and the global ceilings plus `--timeout` bound the invocation. Compact and JSONL modes keep each committed historical page visible before the final drain summary, so a ceiling or interruption never hides how far the durable frontier advanced.

Limit the historical window with UTC calendar dates when useful:

```bash
icloud mail sync backfill --folder Receipts --after 2025-01-01 --before 2026-01-01 --limit 50
```

`--after` is inclusive and `--before` is exclusive. Each date-bounded selection resumes independently, so changing the date window does not silently reuse a narrower cursor. Backfill may revisit an already cached provider location at its starting boundary; that is idempotent and does not create a duplicate logical message.

## Confirm outbound attempts from Sent evidence

A successful SMTP submission creates the local message immediately, then Sent synchronization confirms the provider copy. Run the focused workflow when an attempt is still pending or its SMTP outcome was unknown:

```bash
icloud mail operations outbound list
icloud mail sync --reconcile-outbound
```

The command synchronizes one bounded Sent page and compares recent `sent` or `unknown` attempts with exact generated RFC Message-ID evidence. A single match confirms the attempt and adds the Sent location to the existing `msg_…`; it does not create a second logical message. Subject, body, timestamp, recipients, or attachment resemblance alone never resolves an attempt.

Compact sync output adds nonzero `outbound_confirmed`, `outbound_resolved_unknown`, `outbound_ambiguous`, or `outbound_unmatched` counts. An unmatched attempt can be checked again after a later Sent page. Ambiguous evidence stays unknown and requires inspection; reconciliation never guesses or resends.

## Read Inbox or Sent locally

The canonical local message list defaults to Inbox:

```bash
icloud mail messages list
icloud mail messages list --limit 50
```

Read another synchronized folder explicitly:

```bash
icloud mail messages list --folder sent
icloud mail messages list --folder fld_…
```

Filter the local folder without contacting iCloud:

```bash
icloud mail messages list --query invoice --unread
icloud mail messages list --from billing@example.com --after 2026-08-01
```

`icloud mail inbox` remains a compatibility alias for the same local request, cursor, and rendering path. New scripts and interactive use should prefer `mail messages list`.

The same indexed filters are available through `icloud mail search` across synchronized folders. See [Search synchronized mail](/docs/search-synchronized-mail) for message or thread results, attachment filters, and cursors.

## Cache a selected remote message without advancing sync

When explicit remote search returns a `rem_…` reference, hydrate only that provider location:

```bash
icloud mail sync --hydrate rem_…
icloud mail sync --hydrate rem_…,rem_… --json
icloud mail sync --hydrate rem_…,rem_… --continue-on-error --jsonl
```

This is not a normal folder sync. It verifies the remote account/folder/UID identity, caches the exact message through the standard local parser and dedupe path, and returns stable message/location IDs. The existing folder checkpoint remains unchanged, including committed/scanned UIDs and bootstrap state. If one reference fails after earlier references committed, those stable completed IDs remain visible in text/JSON error details or JSONL item events; `--continue-on-error` attempts the rest.

Use `icloud mail message show rem_… --remote --cache --full` when you want to cache and immediately read complete content. See [Search synchronized mail](/docs/search-synchronized-mail) for supported remote filters, `partial`, stale-reference recovery, and privacy behavior.

Each compact record contains a stable message ID, sender, subject, best available date, true unread or flagged state, nonzero attachment count, and a bounded decoded-text preview:

```text
message
msg_… from=sender@example.com
subject="Project update"
at=2026-08-06T08:30:00Z
unread=true
attachments=1
snippet="Here is the latest update…"

inbox
account=acct_…
count=1
more=false
```

These reads use local state only. They work offline, never mark a message read, and never contact Apple.

## Understand messages with several locations

One logical message may have Inbox, Sent, Archive, or other synchronized locations. Each location keeps its own stable location ID, folder ID, UID validity, UID, flags, and presence state.

When a new Sent copy has the same strong message identity as a stored Inbox copy, the CLI adds the Sent location to the existing `msg_…` record instead of replacing the Inbox location. An unseen Sent copy does not make an outbound message unread. Flagged state still reflects any eligible current location.

The CLI does not merge messages merely because their subject, body, attachments, and time look alike. When the available evidence conflicts or could match several messages, it preserves the separate records and exposes the conflict in message JSON for later repair.

## Read one message by stable ID

```bash
icloud mail message show msg_…
```

The default remains bounded. It shows routing, best timestamp, state, current locations, attachment count, available content kinds, raw and MIME-part availability, a decoded-text excerpt, and the next explicit content choices. It excludes BCC, private storage coordinates, complete bodies, HTML, headers, and MIME bytes.

Return complete decoded text or choose another representation explicitly:

```bash
icloud mail message show msg_… --full
icloud mail message show msg_… --format text --body-only
icloud mail message show msg_… --format html
icloud mail message show msg_… --format html --body-only
icloud mail message show msg_… --headers
icloud mail message show msg_… --verbose
icloud mail message show msg_… --metadata-only
```

`--verbose` adds ordered MIME-part metadata and parse diagnostics. Use the attachment workflow when you need decoded files rather than message-body output.

## Unsubscribe from a mailing list deliberately

When a complete synchronized message contains supported `List-Unsubscribe` metadata, the normal message projection derives a semantic unsubscribe capability from the stored decoded headers. Compact and JSON reads expose only safe method IDs/kinds and host names; opaque HTTPS path/query tokens and `mailto:` recipient/query payloads remain private even though `--headers` can still reveal the original header when you explicitly ask for all decoded headers.

Review without DNS, HTTP, SMTP, or any provider write:

```bash
icloud mail unsubscribe msg_… --dry-run
icloud mail unsubscribe --input-json unsubscribe.json --dry-run --json
```

The default selection prefers one RFC 8058 one-click method when the message correctly declares exactly one HTTPS URI plus `List-Unsubscribe-Post: List-Unsubscribe=One-Click`. Otherwise one `mailto:` alternative may be selected automatically. Several mailto alternatives require an explicit safe ID from dry-run, for example:

```bash
icloud mail unsubscribe msg_… --method mailto:2 --yes
```

One-click execution rereads the exact stored raw message, verifies at least one valid DKIM signature whose `h=` set covers both `List-Unsubscribe` and `List-Unsubscribe-Post`, then sends `List-Unsubscribe=One-Click` as an HTTPS POST body. The HTTP client carries no cookies, HTTP authorization, Apple Account credentials, browser session, Referer, or message-defined custom headers. It does not follow redirects, never connects to loopback/private/link-local/multicast-only targets, bounds the response body, and honors the command context/deadline. A redirect, HTTP endpoint, private-network target, malformed method, missing required DKIM coverage, or oversized response fails closed.

`mailto:` execution does not invent a new delivery path. It validates the URI recipient plus optional `subject` and `body`, rejects other query-driven mail headers such as BCC, and delegates the resulting message to the existing outbound send state machine using a deterministic private idempotency key derived from the source message and selected method. Repeating the same mailto unsubscribe therefore reuses the prior outbound attempt instead of sending another message.

No unsubscribe method runs while reading mail. Real non-interactive execution requires `--yes`; an interactive cancellation exits before DKIM DNS lookup, HTTP, or SMTP.

## Inspect and save attachments

```bash
icloud mail attachments list msg_…
icloud mail attachments save att_… --output document.pdf
icloud mail attachments save-all msg_… --dir ./message-attachments
icloud mail attachments remove msg_… --attachment att_… --dry-run
```

Attachment listing is bounded and metadata-only. Saving verifies locally stored bytes, refuses accidental overwrites, sanitizes untrusted filenames, and prepares a complete batch before installing it. Removal is deliberately different: it rewrites one exact provider copy through append-before-delete recovery, requires a reviewed attachment ID selection, and never changes only the local cache.

Binary stdout is available only with an explicit acknowledgement:

```bash
icloud mail attachments save att_… --output - --binary > document.pdf
```

See [Save mail attachments](/docs/save-mail-attachments) for collisions, inline files, unnamed attachments, provider attachment removal, JSON, and recovery.

## Change one exact location

```bash
icloud mail mark-read msg_…
icloud mail mark-unread msg_…
icloud mail flag msg_…
icloud mail unflag msg_…
```

Without `--folder`, the message must have exactly one eligible current location. A message with several copies returns conflict rather than guessing.

Choose a location by folder when needed:

```bash
icloud mail mark-read msg_… --folder INBOX
icloud mail flag msg_… --folder sent
icloud mail unflag msg_… --folder fld_…
```

The command updates that exact provider UID, stores the flags returned by iCloud, and recomputes the logical message summary across eligible current locations. Deleted, absent, or stale locations are not eligible. There is no implicit all-locations mode.

Compact success omits subjects, recipients, and provider flag details:

```text
mail-state
msg_… action=mark_read
location=loc_…
folder=fld_…
unread=false
flagged=false
```

Use `--json` when automation needs the selected UID identity, canonical and unknown flags, provider mod-sequence when available, and recomputed summaries.

### Colored flags are not exposed as provider state

Apple Mail offers multiple flag colors, but this CLI has not proven a stable, writable color representation through iCloud's documented IMAP path. `icloud capabilities` therefore reports `colored_flags=unsupported`. `mail flag` and `mail unflag` remain the portable provider `\Flagged` operation; they do not select or rename an Apple Mail color.

Unknown provider flags are still preserved when iCloud returns them, including across ordinary read/flag state updates. They remain opaque metadata: the CLI does not infer a color from an undocumented keyword or write guessed keywords back to the provider.

## Stream or export exact raw MIME

```bash
icloud mail message raw msg_… > message.eml
icloud mail message export msg_… --output message.eml
icloud mail message export msg_… --output message.eml --force
icloud mail message export msg_… --output - > message.eml
```

Raw output is byte-for-byte the synchronized RFC822 message. The CLI verifies its stored size and SHA-256 content address before streaming. File export uses a private temporary file, mode `0600`, and refuses an existing regular file unless `--force` is supplied.

## Export one synchronized mailbox as mboxrd

Export the provider copies already present in a cached mailbox without contacting iCloud:

```bash
icloud mail mailbox export inbox --format mboxrd --output inbox.mbox
icloud mail mailbox export 'Archive' --output - > archive.mbox
```

This is a mailbox-copy export, not a logical-message deduplication pass. Every `present=true` provider location in that cached folder is emitted once. Records are traversed with bounded keyset pages in deterministic `(UIDVALIDITY ascending, UID descending)` order, so a logical message that genuinely has two provider copies in one or more mailboxes is represented according to the selected mailbox's stored copies rather than silently collapsed.

Each record uses that exact provider location's verified raw content. The exporter does not rebuild MIME from normalized fields, so Bcc and transport/authentication headers are included when they exist in the canonical raw source. The mbox envelope separator is archive framing derived from stored sender/time metadata; it is separate from the RFC message. For mboxrd reversibility, every body/header line already matching zero or more `>` characters followed by `From ` receives one additional leading `>` in the archive. Other RFC bytes, including existing CRLF or LF line endings, pass through unchanged; a raw message that lacks its own final newline necessarily receives archive framing before the next separator.

File output is written to a private `0600` temporary file in the destination directory, flushed, and installed only after every record succeeds. Existing regular files require `--force`; symlinks and special files are rejected. If any canonical raw blob is missing or fails its size/SHA-256 verification, the whole file export fails and a new target is not installed or an existing forced target is left untouched.

`--output -` writes only native mboxrd bytes to stdout and sends the compact completion summary to stderr. `--json` is intentionally incompatible with stdout archives; write to a file when automation needs the structured summary. The summary reports exported record count, archive byte count, `complete=true`, and the final provider cursor. A downstream stdout write failure can naturally leave the caller with a partial stream, whereas file output retains the atomic install guarantee.

## Import a mailbox archive with a durable frontier

Use mailbox import for an mboxrd archive that may contain many RFC messages. Dry-run fingerprints the complete regular file, resolves the selectable destination, and validates the record at the current durable frontier without contacting iCloud or creating an import job:

```bash
icloud mail mailbox import archive.mbox --to inbox --dry-run
```

Confirmed import is intentionally bounded:

```bash
icloud mail mailbox import archive.mbox --to inbox --max-messages 100 --yes
```

The job is scoped to the selected account, stable folder ID, and exact archive fingerprint/size. It stores the earliest unconfirmed byte offset and record index plus immutable evidence for every attempted record. Only one decoded EML record is staged at a time; the whole archive and multiple records' attachment payloads are never buffered together. Each record delegates APPEND fencing, UID reconciliation, exact-provider-byte verification, and canonical ingestion to the single-message import state machine. When a standard mbox separator carries a parseable envelope date, that date is supplied as the provider `INTERNALDATE`; this preserves the date emitted by `mail mailbox export` during an export/import migration. Third-party separator dates that cannot be parsed are not treated as malformed message content, and the single-message importer falls back to the RFC `Date:` behavior. Normal sync and historical-backfill checkpoints are independent and are not advanced by mailbox import.

If the process exits after APPEND but before the batch frontier advances, rerun the same archive. The record keeps the same single-message recovery key and reconciles the existing provider copy before any new APPEND. A provider failure proven to occur before APPEND is different: the record advances to a new private per-attempt key on retry because the earlier key is durably failed and cannot represent a new deliberate provider attempt.

`--mode atomic` is the default: stop at the first record failure without pretending earlier confirmed APPENDs were rolled back. `--mode continue-on-error` attempts later records in the same bounded pass and reports them individually, but the durable resume frontier remains at the earliest unconfirmed record. This means later confirmed records are safely recognized and skipped on the next traversal while the gap is retried. Any incomplete job refuses continuation if the archive at that canonical path has changed.

## Import one exact EML message

Use import when you already have one RFC822/EML file and want one deliberate provider copy in a current selectable mailbox. Review is local-only:

```bash
icloud mail message import message.eml --to inbox --dry-run
```

The preview reports the destination, exact source SHA-256 and byte size, decoded subject/Message-ID when present, attachment count, flags, and effective internal date. It creates no durable import intent and does not contact IMAP.

Confirm with a stable idempotency key:

```bash
icloud mail message import message.eml \
  --to 'Unicode-日本語' \
  --flag seen \
  --flag flagged \
  --idempotency-key import-archive-2026-01 \
  --yes
```

`--flag` accepts only `seen`, `flagged`, and `answered`; deletion and Draft state are not import options. `--internal-date <RFC3339>` can override provider INTERNALDATE. Without it, the decoded message `Date` is used when available, otherwise the first durable import attempt chooses the current time and reuses that same value on recovery.

Import accepts only one regular file, rejects symlinks/special files and oversize input, stages an immutable content-addressed copy, and reparses it before provider mutation. After APPEND, the exact provider UID is fetched and committed through canonical ingestion. iCloud has been observed to append one terminal CRLF to an otherwise byte-identical APPEND literal. Import accepts only the source bytes exactly or exactly `source + "\r\n"`; the success result identifies that proof as `provider_raw_variant=exact` or `provider_raw_variant=terminal_crlf`. Headers, bodies, MIME boundaries, attachment bytes, or any other change still fail closed. Canonical raw reads/exports always reflect the exact bytes fetched back from the provider. **Targeted import does not create or advance the ordinary synchronization checkpoint**; a later normal sync remains independently responsible for its own frontier and recognizes the already-cached provider location idempotently.

If APPEND succeeds without APPENDUID, or the connection drops after iCloud may have accepted the literal, the CLI does not blindly upload the file again. The durable import remembers the pre-APPEND UIDVALIDITY/UIDNEXT fence and exact source hash, inspects only a bounded post-fence UID range, and proceeds only when exactly one provider copy is proven as either byte-identical or the exact one-CRLF provider form above. Retry the same normalized request with the same idempotency key. Multiple matching candidates remain an explicit conflict; a definite failure before APPEND is marked failed and a new deliberate attempt needs a new key.

## Inspect each synchronization frontier

Status defaults to Inbox. Select Sent or another folder explicitly:

```bash
icloud mail sync status
icloud mail sync status --folder sent
icloud mail sync status --folder fld_…
```

```text
sync-status
chk_… account=acct_…
folder="Sent Messages"
folder_id=fld_…
state=idle
uid_validity=1
committed=53
scanned=53
bootstrap=true
more=false
last_success=2026-08-06T08:31:00Z
```

Useful fields:

- `state=idle` — the latest selected-folder page committed successfully.
- `state=failed` — the frontier did not advance; fix the reported stage and retry.
- `state=repair_required` — iCloud replaced that mailbox identity, so ordinary sync stops rather than guessing.
- `committed` — highest message UID safely stored, or the initial Sent tail before the first new Sent copy.
- `scanned` — highest UID range safely checked, including empty gaps.
- `more=true` — another bounded page is available for that folder.

List bounded checkpoints for every synchronized folder in the selected account:

```bash
icloud mail sync checkpoints
icloud mail sync checkpoints --limit 100 --cursor 'ccur_…'
```

Checkpoint history keeps repair-required and failed folders prominent while remaining fully traversable. When `more=true`, continue with the returned opaque `next_cursor` for the same selected account. The final page omits `next_cursor`.

## Rebaseline one forward frontier deliberately

If you intentionally want normal synchronization to continue from the provider's current tail rather than filling the gap in front of the saved checkpoint, make that choice explicit:

```bash
icloud mail sync rebaseline --folder inbox --current-tail
```

The command reads current iCloud UID identity for only the selected folder, moves only that folder's forward checkpoint to `UIDNEXT-1`, and keeps every cached message intact. Compact output shows the previous and new committed/scanned values plus `cached_kept` and `deleted=no` so the effect is visible before you continue.

Rebaseline does **not** mean older mail is synchronized. It deliberately skips that range for normal forward sync. Use `mail sync backfill` afterward when you still want some or all of the older provider history.

## Audit and repair synchronization state

Start with the bounded, local check:

```bash
icloud mail sync repair --check
icloud mail sync repair --check --verbose
```

`--check` is also the default when neither mode flag is supplied. It does not contact iCloud or change state. The compact summary reports searchable-content drift, temporary blob leftovers, UID/frontier problems, unresolved dedupe or outbound evidence, canonical blob problems, whether more findings remain, and one `next` action. Use `--verbose` or `--json` for bounded per-finding detail.

Apply only deterministic repairs after reviewing the check:

```bash
icloud mail sync repair --apply
```

This can refresh stale local search data, reconcile exact local Sent evidence, and repair a changed UID namespace only after reading fresh status for the exact affected folder. It does not crawl every mailbox, invent dedupe identity, or fabricate missing/corrupt canonical message bytes. Those ambiguous cases remain findings for review.

Orphan files in the private temporary-blob staging area are reported by default and are never removed implicitly. Cleaning them is a separate destructive choice:

```bash
icloud mail sync repair --apply --cleanup-orphans --yes
```

Run `--check` again after any repair if the final `next` field is not `none`.

## Use JSON deliberately

```bash
icloud --request-id mail-01 mail sync --json
icloud --request-id mail-02 mail inbox --folder sent --json
icloud mail sync status --folder sent --json
icloud mail message show msg_… --json
icloud mail message show msg_… --full --json
```

Sync JSON returns one summary per selected folder. Local list JSON returns stable message and location identities plus bounded preview metadata. When a message has several locations, each location remains separate. Identity provenance and unresolved dedupe conflicts are included without exposing credentials, BCC, private raw-storage paths, or complete bodies.

Complete text, HTML, headers, MIME-part metadata, attachment bytes, and raw MIME remain explicit expansions. Exact raw MIME can contain a BCC header only when raw output was requested.

## Recover from common problems

### A folder has not been discovered

```bash
icloud mail folders refresh
icloud mail sync --folder sent
```

Discovery is explicit so the CLI never guesses provider folder identity.

### Sent setup fetched zero messages

That is the safe first-run behavior. Check the recorded tail:

```bash
icloud mail sync status --folder sent
```

Send or append a new test message through the normal mail workflow, then run `icloud mail sync --folder sent` again. The CLI does not crawl old Sent history implicitly.

### A sent or unknown attempt is not confirmed

```bash
icloud mail folders refresh
icloud mail sync --reconcile-outbound
icloud mail operations outbound list
```

`unmatched` means the bounded Sent synchronization has not observed one exact generated Message-ID yet. Repeat after the provider copy appears. `ambiguous_sent_evidence` means more than one current Sent location matches; keep the attempt unresolved and inspect the mailbox before any duplicate-risk retry.

### Sync failed at an IMAP stage

Inspect the affected folder frontier, then retry only after checking connectivity:

```bash
icloud mail sync status --folder sent
icloud --timeout 90s mail sync --folder sent
```

A failed run leaves `committed` unchanged.

### Mailbox identity changed

`state=repair_required` means iCloud returned a different UIDVALIDITY for the selected folder. The CLI stops before resetting or combining identities. Keep the local state intact, inspect the plan, then apply only the targeted repair:

```bash
icloud mail sync repair --check --verbose
icloud mail sync repair --apply
icloud mail sync status --folder <folder>
```

The repair reads fresh provider status only for an affected folder, moves its forward frontier to the verified current tail, and leaves cached messages intact. Any historical backfill frontier tied to the old UID namespace is reset to that same verified provider boundary rather than combined with reused UIDs.

### Inbox looks older than expected

Inbox starts from its newest bounded page. Repeat `mail sync --folder inbox` while `more=true`. Sent is intentionally different: its first setup does not import earlier history.

### A message body or attachment is unavailable

Local content commands never contact iCloud to repair data implicitly. Inspect the relevant sync status, then run `icloud mail sync repair --check --verbose`. Missing or corrupt canonical bytes remain a reported finding rather than being guessed or replaced, while stable message, location, MIME-part, and attachment IDs stay intact.

### A state command says the location is ambiguous

Inspect the current locations, then choose one:

```bash
icloud mail message show msg_… --metadata-only --json
icloud mail mark-read msg_… --folder INBOX
```

The CLI does not guess which copy should change.

Continue with [Search synchronized mail](/docs/search-synchronized-mail), [Save mail attachments](/docs/save-mail-attachments), [Output and automation](/docs/output-and-automation) for stable stream rules, or the [command reference](/docs/command-reference) for exact flags.
