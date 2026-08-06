---
title: Manage folders and sync policy
description: Discover, create, rename, subscribe, delete, and synchronize iCloud Mail folders without guessing provider roles or crawling mailbox history.
order: 5
category: Guides
summary: Safe folder administration, Unicode and nested names, protected system roles, interrupted-rename recovery, and explicit bounded synchronization.
---

## Start with a fresh mailbox tree

Run a live, read-only refresh after the account passes validation:

```bash
icloud mail folders status
```

The command lists every mailbox iCloud currently exposes and reads counts for selectable folders. It does not download messages or change mail. Run it before administration so names, hierarchy, roles, and counts come from current provider evidence.

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

## Create nested or Unicode folders deliberately

Preview first:

```bash
icloud mail folders create 'Projects/Client-日本語' --dry-run
```

The preview is local and reports that IMAP was not contacted. Apply the reviewed change explicitly:

```bash
icloud mail folders create 'Projects/Client-日本語' --yes
icloud mail folders status
```

New custom folders begin with local synchronization disabled. Enable one only when you want it included in normal future sync:

```bash
icloud mail folders enable 'Projects/Client-日本語'
icloud mail sync --folder 'Projects/Client-日本語'
```

Use decoded names in ordinary commands. The CLI respects iCloud's discovered hierarchy delimiter and namespace, while the IMAP transport handles the provider wire encoding exactly once.

```text
name=Unicode-日本語
name=Projects/Alpha parent=Projects
```

The provider wire form shown by `--verbose` is identity metadata, not a local path and not the form you need to type for Unicode mailboxes.

## Rename without losing the folder identity

```bash
icloud mail folders rename Receipts 'Projects/Receipts' --dry-run
icloud mail folders rename Receipts 'Projects/Receipts' --yes
```

A successful rename keeps the same stable `fld_…` identity, local sync choice, message locations, and synchronization checkpoint. The CLI refuses to rename a protected system folder or a folder that still has current child mailboxes; change the children first so a provider hierarchy cascade cannot silently broaden the operation.

If a rename times out, **do not immediately repeat it**. iCloud may have accepted the command even when the final response was lost. Refresh instead:

```bash
icloud mail folders status
```

The CLI records enough recovery information before the rename to reconnect the provider's new name to the original stable folder even when the next LIST response omits rename metadata. If the old name is still present, the interrupted attempt is closed as not applied; if the new name is present and the old one is gone, the original folder identity is repaired.

## Keep provider subscription separate from sync policy

```bash
icloud mail folders subscribe Receipts --dry-run
icloud mail folders subscribe Receipts --yes
icloud mail folders unsubscribe Receipts --yes
```

Subscription changes iCloud's mailbox subscription state. `folders enable` / `disable` changes only the CLI's local synchronization policy. They are intentionally separate decisions: an unsubscribed folder can still be explicitly selected, and an unknown provider subscription does not mean unsubscribed.

## Delete a custom folder with current provider evidence

Preview the exact target first:

```bash
icloud mail folders delete fld_… --dry-run
```

Inbox and provider-discovered system or special-use folders are protected. Non-selectable hierarchy containers and folders with current children are also refused rather than deleted recursively.

For an empty custom folder:

```bash
icloud mail folders delete fld_… --yes
```

The provider count is checked again immediately before deletion. If the folder contains messages, deletion stops unless you deliberately add both safeguards:

```bash
icloud mail folders delete fld_… --allow-non-empty --yes
```

That flag means the provider folder and its contents may be removed. It is not a shortcut for moving mail to Trash. After a confirmed deletion, the historical local folder identity remains inspectable as `present=no stale=true`, while locations that existed only in that folder become absent.

## Understand folder roles

`INBOX` is always recognized. Other system roles are taken from provider metadata when available, including Sent, Drafts, Archive, Trash, and Junk.

When iCloud exposes no Sent role, the CLI can use a conservative compatibility fallback for common Sent names. It does not guess Archive, Drafts, Trash, or Junk merely from an English display name. A folder without trustworthy role metadata remains `kind=other` and can still be selected by name or stable ID.

Trash and Junk also show `retention_risk=provider_auto_delete_possible`, while Drafts shows `retention_risk=provider_mutable_draft`. These warnings stay attached to the provider-derived semantic kind so an explicit all-folder sync does not make a risky mailbox look like ordinary long-term storage.

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

The sync command processes every enabled, current, selectable folder:

```bash
icloud mail sync
```

Select one folder when you want a narrower run:

```bash
icloud mail sync --folder sent
icloud mail sync --folder Receipts
icloud mail sync --folder fld_…
```

When you intentionally want every current selectable folder once, including folders that are locally disabled, opt in explicitly:

```bash
icloud mail sync --all-folders --limit 20
```

This is still bounded synchronization, not an all-history crawl. A previously unsynchronized custom folder starts from its newest bounded page; later runs move only forward from the saved UID frontier. Missing, stale, and non-selectable folders are excluded. `--all-folders` cannot be combined with `--folder`, targeted hydration, or outbound reconciliation.

Inbox starts with its newest bounded page. Sent starts differently: the first run records the current tail without downloading old Sent history, then later runs ingest only new Sent copies. Other enabled folders start from their newest bounded page. See [Sync and read mail](/docs/sync-and-read-inbox) for the complete workflow.

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

Continue with [Sync and read mail](/docs/sync-and-read-inbox) after discovery, or use the [command reference](/docs/command-reference) for exact flags.
