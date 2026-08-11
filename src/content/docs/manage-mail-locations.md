---
title: "Organize mail"
description: "Mark, flag, move, copy, archive, trash, restore, junk, or permanently delete the exact mail you intend."
order: 20
category: Mail
summary: "Familiar mailbox actions with dry-run previews for location-changing operations."
---

## Mark read or unread

```bash
icloud mail mark-read msg_…
icloud mail mark-unread msg_…
```

Flag or unflag:

```bash
icloud mail flag msg_…
icloud mail unflag msg_…
```

If the same message has eligible copies in several folders, choose the one you mean:

```bash
icloud mail mark-read msg_… --folder inbox
```

## Move a message

Preview the move:

```bash
icloud mail move msg_… --from inbox --to Projects --dry-run
```

Then run it with a stable idempotency key:

```bash
icloud mail move msg_… \
  --from inbox \
  --to Projects \
  --idempotency-key move-project-01 \
  --yes
```

## Copy a message

```bash
icloud mail copy msg_… --from inbox --to Projects --dry-run
```

Copy keeps the source location. Move changes it.

## Archive

```bash
icloud mail archive msg_… --dry-run
icloud mail archive msg_… \
  --from inbox \
  --idempotency-key archive-01 \
  --yes
```

The Archive destination comes from the account's recognized or configured Archive role. See [Folders](/docs/folder-discovery-and-sync-policy) if that role cannot be resolved.

## Trash and restore

```bash
icloud mail trash msg_… --dry-run
```

Restore a trashed message:

```bash
icloud mail restore msg_… --from trash --to inbox --dry-run
```

For a real location change, add the idempotency key and `--yes` shown by the command's help.

## Mark as junk or not junk

```bash
icloud mail junk msg_… --dry-run
icloud mail not-junk msg_… --dry-run
```

These use the recognized Junk and Inbox locations rather than guessing from arbitrary folder names.

## Permanently delete one location

Permanent deletion is separate from moving to Trash:

```bash
icloud mail delete msg_… --from trash --dry-run
```

Then:

```bash
icloud mail delete msg_… \
  --from trash \
  --idempotency-key delete-01 \
  --yes
```

Use [Empty Trash, Junk, or a folder](/docs/empty-trash-junk-and-custom-folders) when you want to permanently delete a reviewed page of messages rather than one exact target.

## Change several explicit messages

Most state and location actions accept up to 100 explicit message IDs in one bounded request:

```bash
icloud mail move msg_… msg_… \
  --from inbox \
  --to Projects \
  --idempotency-key move-batch-01 \
  --yes \
  --jsonl
```

For a search-derived set that you want to freeze and review before acting, use [Reviewed bulk actions](/docs/review-and-apply-mail-selections).

## Apply an action to a conversation

Thread commands use the same familiar actions:

```bash
icloud mail threads mark-read thr_… --folder inbox
icloud mail threads archive thr_… --from inbox --dry-run
```

See [Conversations and threads](/docs/read-reconstructed-conversations).

## If an operation becomes uncertain

Do not blindly repeat a location-changing command with a new key. Inspect the operation and current message state:

```bash
icloud mail operations mutations list
icloud mail message show msg_… --metadata-only
```

Then synchronize the source and destination folders. Reusing the original request with the same idempotency key retrieves the recorded operation instead of creating a new one.
