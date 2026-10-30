---
title: "Reviewed bulk actions"
description: "Freeze a small set of messages before an agent or script reads, marks, archives, moves, trashes, or deletes them."
order: 21
category: Mail
summary: "Turn a search or list of IDs into a short-lived reviewed selection, then apply exactly that selection."
---

Selections are useful when the set you found should stay fixed between **review** and **action**.

A selection contains at most 100 messages and expires automatically. New matching mail does not silently join it after creation.

## Select explicit messages or a conversation

```bash
icloud mail selection create msg_… msg_… --ttl 15m
icloud mail selection create thr_… --ttl 15m
```

If you intend to change provider state, include the folder scope you reviewed:

```bash
icloud mail selection create \
  msg_… msg_… \
  --scope-folder inbox \
  --ttl 15m
```

## Create a selection from search

```bash
icloud mail selection create \
  --search \
  --query invoice \
  --unread \
  --folder inbox \
  --scope-folder inbox \
  --limit 50
```

This freezes the current bounded local search page. It does not keep rerunning the query later.

## Review it

```bash
icloud mail selection show sel_…
icloud mail selection show sel_… --json
```

## Read the frozen messages

```bash
icloud mail selection apply sel_… --action read
```

This is local-only and uses exactly the selected message IDs.

## Review a write

For example:

```bash
icloud mail selection apply sel_… \
  --action mark-read \
  --dry-run
```

Or:

```bash
icloud mail selection apply sel_… \
  --action archive \
  --dry-run
```

The dry run returns a `request_hash`. That hash binds the action you reviewed to the frozen selection.

## Apply the reviewed action

```bash
icloud mail selection apply sel_… \
  --action archive \
  --request-hash <reviewed-hash> \
  --yes \
  --jsonl
```

If the reviewed target state changed in a way that makes the selection stale, the CLI refuses to silently switch to a different provider copy. Create and review a fresh selection instead.

Supported write actions include read/unread, flag/unflag, move, copy, archive/unarchive, trash/restore, junk/not-junk, and delete.

## Move or copy a selection

```bash
icloud mail selection apply sel_… \
  --action copy \
  --to Projects \
  --dry-run
```

Use the returned hash with the same `--to` destination when you apply it.

## Delete selection metadata

```bash
icloud mail selection delete sel_…
```

This only removes the local selection record. It does not delete mail.
