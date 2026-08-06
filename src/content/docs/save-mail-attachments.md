---
title: Save mail attachments
description: List attachments from a synchronized message, save one file or a complete set safely, and recover from missing, corrupt, or conflicting local content.
order: 7
category: Guides
summary: Offline attachment inspection, verified streaming, safe filenames, no-clobber exports, batch saves, and recovery.
---

## Start with a synchronized message

Copy a stable message ID from the local Inbox:

```bash
icloud mail inbox
icloud mail attachments list msg_…
```

The list command works entirely from local state. It prints bounded metadata such as the attachment ID, filename, media type, decoded size, inline status, and availability. It never prints attachment bytes or trusts the filename as a destination path.

```text
attachment
att_… message=msg_…
part=prt_…
filename="invoice.pdf"
type=application/pdf
bytes=18422
inline=false
disposition=attachment
availability=available

attachments
message=msg_…
count=1
more=false
```

Inline images and attached messages appear in the same list with `inline=true` or `type=message/rfc822`. Use the stable `att_…` ID rather than matching by filename.

## Save one attachment

```bash
icloud mail attachments save att_… --output invoice.pdf
```

The CLI verifies the locally stored decoded bytes before installing the file. It writes a private temporary file first and refuses to replace an existing destination by default.

Replace an existing regular file deliberately:

```bash
icloud mail attachments save att_… --output invoice.pdf --force
```

Directories, symbolic links, and other special files are rejected even with `--force`. Changing or deleting an exported copy never changes the canonical local attachment.

## Stream bytes to stdout explicitly

Binary stdout requires an acknowledgement:

```bash
icloud mail attachments save att_… --output - --binary > invoice.pdf
```

Without `--binary`, the command exits with a safety error before writing bytes. Native attachment output has no compact wrapper, making it safe for redirection and pipelines.

## Save every attachment from a message

```bash
icloud mail attachments save-all msg_… --dir ./message-attachments
```

The CLI derives safe filenames from untrusted message metadata. Path separators and control characters are removed, unnamed files receive deterministic names, Unicode names are preserved, and duplicate names receive stable numeric suffixes. Every file is prepared before the set is installed, so a conflict or verification failure does not leave a partial batch.

Use `--force` only when every conflicting destination is an existing regular file that may be replaced:

```bash
icloud mail attachments save-all msg_… --dir ./message-attachments --force
```

Compact success reports only the message ID and count. It does not echo the destination directory or every saved path.

## Use JSON for metadata and outcomes

```bash
icloud mail attachments list msg_… --json
icloud mail attachments save att_… --output invoice.pdf --json
icloud mail attachments save-all msg_… --dir ./message-attachments --json
```

List JSON includes stable attachment and MIME-part IDs, decoded size, content hash, media parameters, inline state, and availability. It omits private storage coordinates. Save JSON reports the verified outcome without returning the destination path; batch JSON returns only safe output basenames.

## Recover from common failures

### The attachment is unavailable or corrupt

The message metadata can remain readable even when trusted decoded bytes are missing. The CLI fails before creating a successful output file. Keep the local state intact, inspect:

```bash
icloud mail sync status
icloud mail message show msg_… --verbose
```

Do not replace local cached content manually. A later repair workflow can reconcile missing content without changing stable message or attachment IDs.

### The destination already exists

Choose another path or use `--force` for a regular file you intend to replace. For `save-all`, resolve unexpected files first; the command does not guess which subset should be overwritten.

### The filename looks unusual

That is expected when the original filename contained traversal components, unsupported separators, reserved device names, or no usable name. The sanitized filename is chosen for safe export; the original metadata remains visible through `attachments list --json`.

Continue with [Sync and read the Inbox](/docs/sync-and-read-inbox) for message acquisition, [Output and automation](/docs/output-and-automation) for stream contracts, or the [command reference](/docs/command-reference) for exact flags.
