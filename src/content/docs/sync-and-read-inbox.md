---
title: Sync and read the Inbox
description: Download a bounded iCloud Inbox page into private local state, read compact previews or complete messages offline, inspect progress, and recover safely from interrupted syncs.
order: 6
category: Guides
summary: Bounded Inbox sync, compact previews, explicit full-message reads, incremental checkpoints, safe retries, and recovery.
---

## Sync the current Inbox

After validating the account and discovering folders, run:

```bash
icloud mail sync
```

The first run downloads the newest Inbox page into the selected private state root. It does not walk the entire mailbox, change read state, move mail, or contact the Sent folder.

Expected shape:

```text
mail-sync
fld_… account=acct_…
folder=INBOX
fetched=20
created=20
updated=0
last_uid=184
more=true
bootstrap=true
```

`more=true` means another bounded page is available. Repeat the command until it reports `more=false`:

```bash
while icloud mail sync | tee /dev/stderr | grep -q 'more=true'; do :; done
```

For a smaller or larger page, choose 1–100 messages:

```bash
icloud mail sync --limit 50
```

## Read locally without contacting iCloud

```bash
icloud mail inbox
icloud mail inbox --limit 50
```

Each compact record contains the stable message ID, sender, subject, best available date, unread or flagged state when true, attachment count when nonzero, and a bounded decoded-text preview:

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

`mail inbox` reads only local state. It works offline and never marks a message read. The compact Inbox intentionally stays small; use its stable ID for explicit content access.

## Read one message by stable ID

```bash
icloud mail message show msg_…
```

Default output is still bounded. It shows the message and account IDs, From/To/CC when present, subject and best timestamp, state and folder locations, attachment count, available content kinds, raw availability, MIME-part count, a decoded-text excerpt, and the next content flags. It excludes BCC, raw storage coordinates, complete bodies, HTML, headers, and MIME bytes.

Return complete decoded text:

```bash
icloud mail message show msg_… --full
icloud mail message show msg_… --format text --body-only
```

Choose HTML or decoded headers explicitly:

```bash
icloud mail message show msg_… --format html
icloud mail message show msg_… --format html --body-only
icloud mail message show msg_… --headers
icloud mail message show msg_… --verbose
icloud mail message show msg_… --metadata-only
```

`--verbose` adds ordered MIME-part blocks with type, charset, encoding, disposition, filename, inline/attachment state, decoded size, and partial-parse diagnostics. It does not extract attachment bytes; attachment saving is a separate workflow.

## Stream or export exact raw MIME

```bash
icloud mail message raw msg_… > message.eml
icloud mail message export msg_… --output message.eml
icloud mail message export msg_… --output message.eml --force
icloud mail message export msg_… --output - > message.eml
```

Raw output is byte-for-byte the synchronized RFC822 message. The CLI verifies the stored size and SHA-256 content address before streaming. File export uses a private temporary file, mode `0600`, and refuses to replace an existing regular file unless `--force` is supplied. A missing or corrupt blob fails visibly rather than returning partial content.

## Understand incremental sync

After the first successful page, later runs fetch only newer iCloud Inbox messages:

```bash
icloud mail sync
```

A no-change run is still a successful result:

```text
mail-sync
fld_… account=acct_…
folder=INBOX
fetched=0
created=0
updated=0
last_uid=184
more=false
bootstrap=false
```

Interrupted downloads are safe to retry. The CLI advances its durable frontier only after the whole bounded page and its local metadata commit together, so a failed message cannot be silently skipped.

## Inspect the synchronization frontier

```bash
icloud mail sync status
```

```text
sync-status
chk_… account=acct_…
folder=INBOX
folder_id=fld_…
state=idle
uid_validity=1
committed=184
scanned=184
bootstrap=true
more=false
last_success=2026-08-06T08:31:00Z
```

Useful fields:

- `state=idle` — the latest page committed successfully.
- `state=failed` — the frontier did not advance; fix the reported stage and retry.
- `state=repair_required` — iCloud replaced the mailbox identity, so ordinary sync stops rather than guessing.
- `committed` — highest message UID safely stored.
- `scanned` — highest UID range safely checked, including empty gaps.
- `more=true` — another bounded page is available.

List all checkpoints for the selected account with:

```bash
icloud mail sync checkpoints
```

Inbox is the only message checkpoint in this build, but the command is already account-scoped and bounded for later folder support.

## Use JSON deliberately

```bash
icloud --request-id inbox-01 mail sync --json
icloud --request-id inbox-02 mail inbox --json
icloud mail sync status --json
icloud mail message show msg_… --json
icloud mail message show msg_… --full --json
```

Inbox JSON returns stable message, location, and checkpoint identifiers plus locally stored preview metadata. Default message JSON adds content-availability and parse-status fields without including headers, MIME parts, complete bodies, or raw bytes. Use the same explicit flags to include text, HTML, headers, MIME-part metadata, or base64 raw MIME. Raw JSON is limited to 8 MiB; plain raw/export streaming remains available up to the synchronized message-size limit.

Credential references, raw storage paths, raw content hashes, and BCC recipients remain excluded from JSON. Exact raw MIME can contain a BCC header only when raw output was explicitly requested.

## Recover from common problems

### Inbox has not been discovered

```bash
icloud mail folders status
icloud mail sync
```

Folder discovery is explicit so the CLI never guesses at provider identity.

### Sync failed at an IMAP stage

Inspect the local frontier:

```bash
icloud mail sync status
```

Then retry with a longer command timeout only after checking connectivity:

```bash
icloud --timeout 90s mail sync
```

A failed run leaves `committed` unchanged.

### Mailbox identity changed

`state=repair_required` means iCloud reported a different UIDVALIDITY for the Inbox. The CLI stops before resetting or merging identities. Keep the local state intact; the dedicated repair workflow is introduced in a later command slice.

### The Inbox looks older than expected

The first run intentionally starts from the newest bounded page. Repeat `mail sync` while `more=true`. New messages arriving afterward are picked up incrementally.

### A message body is unavailable or corrupt

`mail message` never contacts iCloud to repair local content implicitly. Run `mail sync status` first. A missing or corrupt raw blob returns an unavailable error and leaves message/location IDs untouched. Keep the state directory intact for the later repair workflow; do not replace a blob manually.

Continue with [Output and automation](/docs/output-and-automation) for stable stream rules or the [command reference](/docs/command-reference) for exact flags.
