---
title: "Sync mail"
description: "Keep a useful local copy of iCloud Mail current, watch for new mail, and deliberately fetch older history when you need it."
order: 10
category: Mail
summary: "Sync current mail, watch in the foreground, backfill history, inspect status, and repair sync state."
---

Synchronization gives `icloud` a local copy it can read and search quickly. For normal use, the important command is simply:

```bash
icloud mail sync
```

## First sync

Refresh folders once before the first sync:

```bash
icloud mail folders refresh
icloud mail sync
```

Inbox and Sent are the normal starting folders. Additional folders can be enabled from the [Folders](/docs/folders) guide.

## Bring Inbox fully current

One sync invocation is intentionally bounded. If the output says more current mail remains, you can let the CLI continue through bounded pages:

```bash
icloud mail sync --folder inbox --until-current
```

You can place ceilings on the work:

```bash
icloud mail sync \
  --folder inbox \
  --until-current \
  --max-pages 10 \
  --max-messages 500
```

If the command is interrupted, run it again. Completed pages stay completed.

## Sync a specific folder

```bash
icloud mail sync --folder inbox
icloud mail sync --folder sent
icloud mail sync --folder Receipts
```

To deliberately make one pass across every current selectable folder, including folders not in your normal sync set:

```bash
icloud mail sync --all-folders
```

## Keep mail fresh in the foreground

```bash
icloud mail watch
```

The watcher keeps enabled folders fresh until you stop it with Ctrl-C. It uses the best live behavior available and falls back to polling when needed.

Useful variants:

```bash
icloud mail watch --all-accounts
icloud mail watch --poll-interval 2m
icloud mail watch --jsonl
```

`--jsonl` is the right choice when another program wants a stream of events.

## Fetch older mail

Normal sync focuses on current mail. Historical mail is a separate, explicit workflow:

```bash
icloud mail sync backfill --folder inbox
```

Repeat until the result says the requested history is complete, or ask the CLI to continue within limits:

```bash
icloud mail sync backfill \
  --folder inbox \
  --until-complete \
  --max-pages 20 \
  --max-messages 1000
```

You can also limit the historical window:

```bash
icloud mail sync backfill \
  --folder Receipts \
  --after 2025-01-01 \
  --before 2026-01-01
```

Backfill does not interfere with the normal forward sync position, so you can keep receiving new mail while older history is being filled in.

## Inspect sync status

```bash
icloud mail sync status
icloud mail sync status --folder sent
icloud mail sync checkpoints
```

These are useful when a folder appears stale or a previous command was interrupted.

## Start future sync from “now”

Sometimes you do not want normal sync to fill a gap. After reviewing the current status, you can deliberately move a folder's forward starting point to the current iCloud tail:

```bash
icloud mail sync status --folder inbox
icloud mail sync rebaseline --folder inbox --current-tail
```

This keeps already cached mail. It simply tells future normal sync to start from the current point. Use backfill later if you still want older messages.

## Repair sync state

Start with a check:

```bash
icloud mail sync repair --check
```

For more detail:

```bash
icloud mail sync repair --check --verbose
```

If the findings are repairable automatically:

```bash
icloud mail sync repair --apply
```

The repair workflow is intentionally check-first. If it reports missing or ambiguous data that cannot be repaired safely, keep the local state intact and use [Troubleshooting](/docs/troubleshooting).

## Sync and outbound mail

If a send has an uncertain or not-yet-observed Sent copy, use:

```bash
icloud mail sync --reconcile-outbound
```

Then inspect the attempt:

```bash
icloud mail operations outbound list
```

See [Outbox and send recovery](/docs/outbox-and-send-recovery) before retrying an uncertain send.

Next: [Read mail](/docs/read-mail) or [Search mail](/docs/search-mail).
