---
title: "Attachments"
description: "List attachments from a message or conversation, save one file, save a complete set, or deliberately remove selected attachments."
order: 14
category: Mail
summary: "Inspect attachment metadata first, then export exact files by stable `att_…` ID."
---

## List attachments

For one message:

```bash
icloud mail attachments list msg_…
```

For a whole conversation:

```bash
icloud mail attachments list thr_…
```

The list shows metadata only. It does not dump binary content into your terminal.

## Save one attachment

Use its `att_…` ID:

```bash
icloud mail attachments save att_… --output invoice.pdf
```

The CLI refuses to overwrite an existing file by default. If replacement is deliberate:

```bash
icloud mail attachments save att_… --output invoice.pdf --force
```

## Save all attachments

From one message:

```bash
icloud mail attachments save-all msg_… --dir ./attachments
```

Or from a conversation:

```bash
icloud mail attachments save-all thr_… --dir ./thread-files
```

The CLI chooses safe filenames and prepares the set before installing it, so odd filenames from email metadata are not treated as trusted filesystem paths.

## Stream an attachment

Binary stdout requires an explicit opt-in:

```bash
icloud mail attachments save att_… --output - --binary > invoice.pdf
```

That makes it hard to accidentally print binary data into a normal terminal session.

## JSON metadata

```bash
icloud mail attachments list msg_… --json
```

Use `--cursor` when a metadata list is paginated.

## Remove attachments from a provider copy

This is a destructive Mail action, so review it first:

```bash
icloud mail attachments remove \
  msg_… \
  --attachment att_… \
  --dry-run
```

Then run the same reviewed selection with an idempotency key and confirmation:

```bash
icloud mail attachments remove \
  msg_… \
  --attachment att_… \
  --idempotency-key remove-old-report-01 \
  --yes
```

Repeat `--attachment` to remove several selected files in one operation.

If the message exists in more than one folder, add `--folder` to choose the copy you intend to replace:

```bash
icloud mail attachments remove \
  msg_… \
  --folder inbox \
  --attachment att_… \
  --idempotency-key remove-inbox-attachment-01 \
  --yes
```

Cryptographically protected messages may refuse attachment removal rather than weakening or breaking the protected content.

## When a file is unavailable

Keep the local mail state intact and inspect:

```bash
icloud mail message show msg_… --verbose
icloud mail sync repair --check
```

See [Troubleshooting](/docs/troubleshooting) if synchronized content is missing or corrupt.
