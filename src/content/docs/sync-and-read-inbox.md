---
title: Sync and read mail
description: Synchronize enabled iCloud Mail folders into private local state, read Inbox or Sent copies offline, update one exact location, and recover safely.
order: 6
category: Guides
summary: Bounded multi-folder sync, history-free Sent setup, compact local reads, explicit content access, location-aware state changes, safe retries, and recovery.
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

`reconciled` counts existing locations whose remote flags were checked without downloading message bodies again. `state_changes` counts real differences. The bounded sweep rotates over time instead of turning one command into a whole-mailbox scan.

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
```

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

### Sync failed at an IMAP stage

Inspect the affected folder frontier, then retry only after checking connectivity:

```bash
icloud mail sync status --folder sent
icloud --timeout 90s mail sync --folder sent
```

A failed run leaves `committed` unchanged.

### Mailbox identity changed

`state=repair_required` means iCloud returned a different UIDVALIDITY for the selected folder. The CLI stops before resetting or combining identities. Keep the local state intact for the dedicated repair workflow.

### Inbox looks older than expected

Inbox starts from its newest bounded page. Repeat `mail sync --folder inbox` while `more=true`. Sent is intentionally different: its first setup does not import earlier history.

### A message body or attachment is unavailable

Local content commands never contact iCloud to repair data implicitly. Inspect the relevant sync status first. Missing or corrupt bytes fail visibly while stable message, location, MIME-part, and attachment IDs remain intact.

### A state command says the location is ambiguous

Inspect the current locations, then choose one:

```bash
icloud mail message show msg_… --metadata-only --json
icloud mail mark-read msg_… --folder INBOX
```

The CLI does not guess which copy should change.

Continue with [Search synchronized mail](/docs/search-synchronized-mail), [Save mail attachments](/docs/save-mail-attachments), [Output and automation](/docs/output-and-automation) for stable stream rules, or the [command reference](/docs/command-reference) for exact flags.
