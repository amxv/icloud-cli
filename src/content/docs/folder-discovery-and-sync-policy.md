---
title: Folder discovery and sync policy
description: Discover iCloud Mail folders, understand their roles and names, inspect counts, and choose which folders are eligible for synchronization.
order: 5
category: Guides
summary: Live folder discovery, Unicode and nested names, system roles, missing folders, and explicit sync eligibility.
---

## Discover the mailbox tree

Run a live, read-only refresh after the account passes validation:

```bash
icloud mail folders status
```

The command lists every mailbox iCloud currently exposes and reads counts for selectable folders. It does not download messages or change mail.

Use the global account selector when several profiles exist:

```bash
icloud --account Personal mail folders status
```

Compact output is bounded to 20 records by default:

```text
folder
fld_… name=INBOX
kind=inbox
selectable=yes
subscription=unknown
present=yes
sync=enabled
messages=15
unseen=2

folder
fld_… name=Projects/Alpha
kind=other
parent=Projects
selectable=yes
subscription=unknown
present=yes
sync=disabled
messages=1

folder-status
account=acct_…
count=8
present=8
stale=0
enabled=2
```

Increase the display bound without changing what gets discovered:

```bash
icloud mail folders status --limit 50
```

## Read the saved inventory offline

```bash
icloud mail folders list
icloud mail folders show INBOX
icloud mail folders show 'Unicode-日本語'
```

These commands use local state only. A selector may be a stable folder ID, decoded name, exact provider wire name, or an unambiguous role such as `inbox`. Ambiguous matches fail instead of guessing.

Use `--verbose` only when troubleshooting provider identity:

```bash
icloud mail folders show 'Unicode-日本語' --verbose
```

It adds details such as the wire name, delimiter, provider attributes, UID metadata, and discovery timestamps.

## Unicode and nested names work normally

Use decoded names in ordinary commands:

```text
name=Unicode-日本語
name=Projects/Alpha parent=Projects
```

The provider wire form shown by `--verbose` is identity metadata, not a local path.

## Understand folder roles

`INBOX` is always recognized. Other system roles are taken from provider metadata when available, including Sent, Drafts, Archive, Trash, and Junk.

When iCloud exposes no Sent role, the CLI can use a conservative compatibility fallback for common Sent names. It does not guess Archive, Drafts, Trash, or Junk merely from an English display name. A folder without trustworthy role metadata remains `kind=other` and can still be selected by name or stable ID.

## Subscription may be unknown

```text
subscription=unknown
```

This means iCloud did not provide subscription metadata in the accepted folder response. It does not mean unsubscribed and does not prevent the folder from being used.

## Choose synchronization eligibility

Inbox and Sent start enabled. Other folders start disabled:

```bash
icloud mail folders enable Receipts
icloud mail folders disable Receipts
```

These commands change local policy only. They do not immediately fetch, move, or modify mail. Non-selectable container folders cannot be enabled.

The current message sync command reads Inbox only:

```bash
icloud mail sync
```

Enabling another folder preserves the choice for later phases; it does not cause the current Inbox command to broaden its scope. See [Sync and read the Inbox](/docs/sync-and-read-inbox) for the active workflow.

## Missing folders remain visible

If a previously known mailbox disappears from a successful refresh, the CLI retains it as missing rather than silently deleting its identity and policy:

```text
present=no stale=true
```

Normal local lists hide missing entries. Inspect them explicitly with:

```bash
icloud mail folders list --include-missing
```

When the same folder returns and can be matched safely, its stable ID and sync choice are restored.

## Request JSON

```bash
icloud --request-id folders-01 mail folders status --json
icloud mail folders list --include-missing --json
```

JSON includes the complete folder record and local schema version. Credential references and values remain private.

Continue with [Sync and read the Inbox](/docs/sync-and-read-inbox) after discovery, or use the [command reference](/docs/command-reference) for exact flags.
