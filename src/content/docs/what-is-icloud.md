---
title: "What is icloud?"
description: "A practical introduction to the icloud CLI, what it can do today, and how the project is growing beyond Mail."
order: 1
category: Start
summary: "Understand the project in two minutes: one CLI for iCloud, with a comprehensive Mail experience available today."
---

`icloud` brings iCloud workflows to the command line. The goal is simple: give people, scripts, and agents a clean interface to iCloud without making them learn the plumbing underneath it.

Today, **Mail is the comprehensive part of the CLI**. You can read and search your inbox, synchronize mail for offline use, send and reply, work with drafts and attachments, organize folders, archive or trash messages, import and export mail, use S/MIME, and automate common workflows.

Over time, the same top-level CLI can grow to cover more of the Apple iCloud ecosystem—such as Contacts, Reminders, Calendars, and other useful services. See [The iCloud ecosystem](/docs/the-icloud-ecosystem) for the project direction and the current support boundary.

## A CLI that reads like the task

The command structure is intentionally predictable:

```bash
icloud account add ...
icloud mail sync
icloud mail messages list
icloud mail search invoice
icloud mail reply msg_… ...
```

You should usually be able to discover what you need with `--help`:

```bash
icloud --help
icloud mail --help
icloud mail search --help
```

For exact flags on every command, use the [command reference](/docs/command-reference).

## Designed for people and agents

Normal output is compact text that is easy to scan in a terminal and inexpensive for an agent to consume. Structured output is opt-in:

```bash
icloud mail messages list
icloud mail messages list --json
icloud mail watch --jsonl
```

Actions that send, move, delete, or otherwise change remote mail are intentionally explicit. Most important writes support a review step with `--dry-run`, and non-interactive workflows use `--yes` when a real action is intended.

```bash
icloud mail send \
  --to teammate@example.com \
  --subject 'Project update' \
  --body-file update.txt \
  --dry-run
```

## Local-first Mail

Mail that you synchronize is stored locally so reading, searching, and many inspection workflows remain useful without another network request. This also makes the CLI pleasant for agents: fetch once, then inspect and reason over stable message and thread IDs.

```bash
icloud mail sync
icloud mail search invoice
icloud mail threads list
icloud mail message show msg_… --full
```

You decide when you want a fresh provider-side search:

```bash
icloud mail search invoice --remote
```

## Stable IDs make workflows composable

Commands return stable IDs such as:

- `acct_…` for accounts
- `msg_…` for messages
- `thr_…` for conversations
- `att_…` for attachments
- `drf_…` for drafts
- `send_…` for outbound attempts

That means one command naturally feeds the next:

```bash
icloud mail search invoice
icloud mail message show msg_… --full
icloud mail attachments list msg_…
icloud mail reply msg_… --body-file reply.txt --dry-run
```

## Where to go next

If this is your first time here, follow the [Quickstart](/docs/quickstart). It gets you from installation to reading and sending mail without covering every advanced option.

If you already know what you want to do, jump directly to the Mail guides from the [documentation index](/docs).
