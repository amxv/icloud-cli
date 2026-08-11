---
title: "Folders"
description: "Discover iCloud Mail folders, choose which ones sync routinely, and create, rename, subscribe to, or delete custom folders."
order: 19
category: Mail
summary: "Manage the mailbox tree without needing to think about provider protocol details."
---

## Refresh the folder list

```bash
icloud mail folders refresh
```

This is the normal first step after adding an account and whenever the mailbox structure has changed elsewhere.

Read the saved list later with:

```bash
icloud mail folders list
```

Inspect one folder:

```bash
icloud mail folders show Receipts
```

A folder can also be selected by its stable `fld_…` ID.

## Choose what normal sync includes

Inbox and Sent are the normal starting set. Enable a custom folder when you want routine `icloud mail sync` and `icloud mail watch` workflows to include it:

```bash
icloud mail folders enable Receipts
```

Disable it later:

```bash
icloud mail folders disable Receipts
```

Disabling routine sync does not delete the folder or already synchronized mail.

## Create a folder

Review:

```bash
icloud mail folders create 'Projects/Client' --dry-run
```

Then create it:

```bash
icloud mail folders create 'Projects/Client' --yes
```

Nested and Unicode names are supported when the provider accepts them.

## Rename a custom folder

```bash
icloud mail folders rename Receipts 'Projects/Receipts' --dry-run
icloud mail folders rename Receipts 'Projects/Receipts' --yes
```

After an interrupted rename, run `icloud mail folders refresh` before trying another change so the CLI can observe the current mailbox tree.

## Subscribe or unsubscribe

Provider subscription and the CLI's routine-sync preference are separate controls:

```bash
icloud mail folders subscribe Receipts
icloud mail folders unsubscribe Receipts
```

Use `folders enable` / `disable` for whether the CLI normally synchronizes a folder. Use `subscribe` / `unsubscribe` for the provider subscription itself.

## Delete a custom folder

Always preview first:

```bash
icloud mail folders delete Receipts --dry-run
```

Then:

```bash
icloud mail folders delete Receipts --yes
```

If the folder contains messages, deletion requires an additional deliberate opt-in:

```bash
icloud mail folders delete Receipts --allow-non-empty --yes
```

System folders and unsafe hierarchy changes are protected rather than treated like ordinary custom folders.

## Archive, Trash, Junk, Drafts, and Sent roles

Most accounts advertise their special folders clearly. If one does not, you can inspect local role bindings:

```bash
icloud mail folders role list
```

Bind a discovered folder to a semantic role when you have reviewed that it is the intended folder:

```bash
icloud mail folders role set archive fld_… --dry-run
icloud mail folders role set archive fld_…
```

Available roles are `sent`, `drafts`, `archive`, `trash`, and `junk`.

Clear a local binding with:

```bash
icloud mail folders role clear archive
```

These bindings help commands such as `mail archive`, `mail trash`, and draft synchronization find the intended folder. They do not rename the folder in iCloud.

## Sync a folder immediately

A folder enabled for routine sync can be selected directly:

```bash
icloud mail sync --folder Receipts
```

To make one deliberate bounded pass across every selectable folder regardless of routine policy:

```bash
icloud mail sync --all-folders
```

See [Sync mail](/docs/sync-and-read-inbox) for current mail, historical backfill, and watch.
