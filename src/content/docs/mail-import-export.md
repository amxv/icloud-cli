---
title: "Import and export mail"
description: "Export exact EML files or whole synchronized mailboxes, and import EML or mboxrd archives deliberately."
order: 23
category: Mail
summary: "Move mail in and out of the CLI using interoperable message and mailbox formats."
---

## Export one message as EML

```bash
icloud mail message export msg_… --output message.eml
```

Or stream it:

```bash
icloud mail message raw msg_… > message.eml
```

Use `--force` only when you intend to replace an existing regular file.

## Export a whole synchronized mailbox

```bash
icloud mail mailbox export inbox \
  --format mboxrd \
  --output inbox.mbox
```

Another folder works the same way:

```bash
icloud mail mailbox export Archive --output archive.mbox
```

Or stream the archive:

```bash
icloud mail mailbox export Archive --output - > archive.mbox
```

Mailbox export uses the mail already synchronized locally. If you want the newest provider mail included, run `icloud mail sync --folder <folder>` first.

## Import one EML message

Review the exact file and destination first:

```bash
icloud mail message import message.eml \
  --to inbox \
  --dry-run
```

Then import it:

```bash
icloud mail message import message.eml \
  --to inbox \
  --idempotency-key import-message-01 \
  --yes
```

You can set initial supported flags:

```bash
icloud mail message import message.eml \
  --to Archive \
  --flag seen \
  --flag flagged \
  --idempotency-key import-archive-01 \
  --yes
```

## Import an mboxrd archive

Preview:

```bash
icloud mail mailbox import archive.mbox \
  --to Archive \
  --dry-run
```

Then import a bounded batch:

```bash
icloud mail mailbox import archive.mbox \
  --to Archive \
  --max-messages 100 \
  --yes
```

Large imports are resumable. Re-run the same archive and destination to continue from the saved progress.

For per-record automation output:

```bash
icloud mail mailbox import archive.mbox \
  --to Archive \
  --mode continue-on-error \
  --yes \
  --jsonl
```

## If an import is interrupted

Do not edit the archive in place and assume the old import can continue. Resume the same source when possible. For a single EML import with an uncertain provider result, repeat the same normalized command with the same idempotency key so the CLI can reconcile the original attempt rather than blindly creating another copy.

See [Troubleshooting](/docs/troubleshooting) if an import reports an unresolved state.
