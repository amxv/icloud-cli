---
title: "Empty Trash, Junk, or a folder"
description: "Permanently delete mail in reviewed bounded pages instead of turning an empty-folder request into one unbounded destructive action."
order: 22
category: Mail
summary: "Preview a page, apply exactly that page, then repeat if more mail remains."
---

Permanent deletion is intentionally a review-and-apply workflow.

## Empty Trash

Preview up to 50 exact locations:

```bash
icloud mail empty trash --limit 50 --dry-run
```

The preview returns a `purge_id` and `request_hash`. Apply that exact reviewed page:

```bash
icloud mail empty trash \
  --purge-id pur_… \
  --request-hash <reviewed-hash> \
  --yes \
  --jsonl
```

If more Trash mail remains, create a new dry-run page and repeat.

## Empty Junk

The same pattern applies:

```bash
icloud mail empty junk --limit 50 --dry-run
```

Then apply the returned review identifiers with `--yes`.

## Empty a custom folder

For an ordinary custom folder you deliberately name:

```bash
icloud mail empty folder 'Temporary Import' \
  --limit 50 \
  --dry-run
```

Then use the returned `purge_id` and `request_hash` on the matching apply command.

## Why it is paged

A request to “empty Trash” can affect a large mailbox. The CLI turns it into small reviewed pages so a person or agent can see the scope before permanent deletion and recover cleanly if a page is interrupted.

For deleting one exact message location instead, use:

```bash
icloud mail delete msg_… --from trash --dry-run
```

See [Organize mail](/docs/manage-mail-locations).
