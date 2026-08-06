---
title: Sync and read the Inbox
description: Download a bounded iCloud Inbox page into private local state, read compact previews offline, inspect progress, and recover safely from interrupted syncs.
order: 6
category: Guides
summary: Bounded Inbox sync, compact local reading, incremental checkpoints, safe retries, and identity-change recovery.
---

## Sync the current Inbox

After validating the account and discovering folders, run:

```bash
icloud mail sync
```

The first run downloads the newest Inbox page into the selected private state root. It does not walk the entire mailbox, change read state, move mail, or contact the Sent folder.

Expected shape:

```text
mail-sync fld_… account=acct_… folder=INBOX fetched=20 created=20 updated=0 last_uid=184 more=true bootstrap=true
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
message msg_… from=sender@example.com subject="Project update" at=2026-08-06T08:30:00Z unread=true attachments=1 snippet="Here is the latest update…"

inbox account=acct_… count=1 more=false
```

`mail inbox` reads only local state. It works offline and never marks a message read. Full bodies, HTML, raw MIME, headers, and attachment downloads arrive through later explicit commands; the compact Inbox intentionally stays small.

## Understand incremental sync

After the first successful page, later runs fetch only newer iCloud Inbox messages:

```bash
icloud mail sync
```

A no-change run is still a successful result:

```text
mail-sync fld_… account=acct_… folder=INBOX fetched=0 created=0 updated=0 last_uid=184 more=false bootstrap=false
```

Interrupted downloads are safe to retry. The CLI advances its durable frontier only after the whole bounded page and its local metadata commit together, so a failed message cannot be silently skipped.

## Inspect the synchronization frontier

```bash
icloud mail sync status
```

```text
sync-status chk_… account=acct_… folder=INBOX folder_id=fld_… state=idle uid_validity=1 committed=184 scanned=184 bootstrap=true more=false last_success=2026-08-06T08:31:00Z
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
```

JSON returns stable message, location, and checkpoint identifiers plus the complete locally stored preview metadata. It does not expose credential references, raw storage paths, raw content hashes, BCC recipients, or full message bodies.

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

Continue with [Output and automation](/docs/output-and-automation) for stable stream rules or the [command reference](/docs/command-reference) for exact flags.
