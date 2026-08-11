---
title: "Mail"
description: "A task map for the iCloud Mail command family and the shortest path to the guide you need."
order: 9
category: Mail
summary: "Start here when you know you want to work with Mail but are not sure which command or guide covers the task."
---

Mail is the comprehensive service in `icloud` today. You can use it as an everyday terminal mail client, as a toolbox inside scripts, or as a stable interface for an agent.

## Pick a task

| I want to… | Start here |
| --- | --- |
| Get recent mail onto this machine | [Sync mail](/docs/sync-mail) |
| Browse Inbox or another folder | [Read mail](/docs/read-mail) |
| Find a message or conversation | [Search mail](/docs/search-mail) |
| Read a whole conversation | [Conversations and threads](/docs/conversations-and-threads) |
| Save files from mail | [Attachments](/docs/attachments) |
| Send, reply, or forward | [Send, reply, and forward](/docs/send-reply-and-forward) |
| Prepare mail without sending | [Drafts](/docs/drafts) |
| Send from an alias | [Sender identities](/docs/sender-identities) |
| Add reusable signatures | [Signatures](/docs/signatures) |
| Create or choose folders | [Folders](/docs/folders) |
| Archive, move, trash, or flag mail | [Organize mail](/docs/organize-mail) |
| Act on a reviewed search result set | [Reviewed bulk actions](/docs/reviewed-bulk-actions) |
| Empty Trash or Junk | [Empty Trash, Junk, or a folder](/docs/empty-trash-junk-or-a-folder) |
| Import or export mail | [Import and export mail](/docs/import-and-export-mail) |
| Unsubscribe or triage senders | [Mailing lists and sender preferences](/docs/mailing-lists-and-sender-preferences) |
| Sign, encrypt, or decrypt mail | [S/MIME](/docs/s-mime) |
| Recover after a send problem | [Outbox and send recovery](/docs/outbox-and-send-recovery) |

## The everyday loop

Most Mail work follows a simple pattern:

```bash
icloud mail sync
icloud mail search invoice
icloud mail message show msg_… --full
```

Then take the action you actually want:

```bash
icloud mail reply msg_… --body-file reply.txt --dry-run
icloud mail archive msg_… --from inbox --dry-run
```

## Local when it helps, fresh when you ask

Normal reading and search use the Mail you have synchronized, which keeps repeated inspection fast and predictable.

When you specifically need a fresh provider-side search:

```bash
icloud mail search invoice --remote
```

You can cache a chosen result into the normal local Mail view afterward.

## Small output by default

Lists and search results give you identifiers and enough context to choose the next step rather than dumping every full body or attachment.

```bash
icloud mail messages list
icloud mail message show msg_…
icloud mail message show msg_… --full
```

That makes the same commands comfortable for a person and efficient for an agent.

## Preview important writes

For outbound mail, moves, deletion, and other location-changing actions, preview the resolved request with `--dry-run` before a non-interactive `--yes` execution.

```bash
icloud mail send \
  --to teammate@example.com \
  --subject 'Update' \
  --body-file update.txt \
  --dry-run
```

For automation, stable idempotency keys make retries safer when a supported write is repeated.

## Need exact syntax?

Every Mail command has built-in help:

```bash
icloud mail --help
icloud mail search --help
icloud mail send --help
```

The [Command reference](/docs/command-reference) contains the complete generated command tree.
