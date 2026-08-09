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

Watch every configured account with one foreground process when there are at most 10:

```bash
icloud mail watch --all-accounts
```

Each account keeps its own ordered event sequence and watch ownership. A second watch for the same account fails with a retryable conflict instead of creating duplicate workers. Ordinary synchronization remains the only path that commits messages and checkpoints.

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

Limit the historical window with UTC calendar dates when useful:

```bash
icloud mail sync backfill --folder Receipts --after 2025-01-01 --before 2026-01-01 --limit 50
```

`--after` is inclusive and `--before` is exclusive. Each date-bounded selection resumes independently, so changing the date window does not silently reuse a narrower cursor. Backfill may revisit an already cached provider location at its starting boundary; that is idempotent and does not create a duplicate logical message.

## Confirm outbound attempts from Sent evidence

A successful SMTP submission creates the local message immediately, then Sent synchronization confirms the provider copy. Run the focused workflow when an attempt is still pending or its SMTP outcome was unknown:

```bash
icloud mail status --outbound
icloud mail sync --reconcile-outbound
```

The command synchronizes one bounded Sent page and compares recent `sent` or `unknown` attempts with exact generated RFC Message-ID evidence. A single match confirms the attempt and adds the Sent location to the existing `msg_…`; it does not create a second logical message. Subject, body, timestamp, recipients, or attachment resemblance alone never resolves an attempt.

Compact sync output adds nonzero `outbound_confirmed`, `outbound_resolved_unknown`, `outbound_ambiguous`, or `outbound_unmatched` counts. An unmatched attempt can be checked again after a later Sent page. Ambiguous evidence stays unknown and requires inspection; reconciliation never guesses or resends.

## Read Inbox or Sent locally

The existing local list command defaults to Inbox:

```bash
icloud mail inbox
icloud mail inbox --limit 50
```

Read another synchronized folder explicitly:

```bash
icloud mail inbox --folder sent
icloud mail inbox --folder fld_…
```

Filter the local folder without contacting iCloud:

```bash
icloud mail inbox --query invoice --unread
icloud mail inbox --from billing@example.com --after 2026-08-01
```

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

## Inspect and save attachments

```bash
icloud mail attachments list msg_…
icloud mail attachments save att_… --output document.pdf
icloud mail attachments save-all msg_… --dir ./message-attachments
```

Attachment listing is bounded and metadata-only. Saving verifies locally stored bytes, refuses accidental overwrites, sanitizes untrusted filenames, and prepares a complete batch before installing it.

Binary stdout is available only with an explicit acknowledgement:

```bash
icloud mail attachments save att_… --output - --binary > document.pdf
```

See [Save mail attachments](/docs/save-mail-attachments) for collisions, inline files, unnamed attachments, JSON, and recovery.

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

## Stream or export exact raw MIME

```bash
icloud mail message raw msg_… > message.eml
icloud mail message export msg_… --output message.eml
icloud mail message export msg_… --output message.eml --force
icloud mail message export msg_… --output - > message.eml
```

Raw output is byte-for-byte the synchronized RFC822 message. The CLI verifies its stored size and SHA-256 content address before streaming. File export uses a private temporary file, mode `0600`, and refuses an existing regular file unless `--force` is supplied.

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
icloud mail folders status
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
icloud mail folders status
icloud mail sync --reconcile-outbound
icloud mail status --outbound
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
