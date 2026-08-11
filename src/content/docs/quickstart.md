---
title: "Quickstart"
description: "Go from a fresh install to reading, searching, replying to, and sending iCloud Mail."
order: 3
category: Start
summary: "The shortest useful path through account setup, sync, reading, search, and safe sending."
---

This guide covers the everyday path. Advanced features such as historical backfill, imports, S/MIME, bulk changes, and recovery each have their own guide.

## 1. Install the CLI

```bash
npm install --global @amxv/icloud-cli@latest
icloud version
```

## 2. Create an Apple app-specific password

Use an **app-specific password** for your Apple Account. Do not give the CLI your normal Apple Account password.

When you have one, add the account interactively:

```bash
icloud account add --name Personal --email you@icloud.com
```

The interactive path stores the password in your system keyring when available.

For a script or agent, pass it through stdin instead of placing it in the command line:

```bash
printf '%s\n' "$ICLOUD_APP_PASSWORD" | \
  icloud account add \
    --name Personal \
    --email you@icloud.com \
    --password-stdin
```

See [Accounts and credentials](/docs/account-setup-and-credentials) for environment variables, credential files, multiple accounts, and rotation.

## 3. Check the account

```bash
icloud account test Personal
```

This validates that the account can connect to iCloud Mail. It does not send a message.

If it fails, use [Account checks and doctor](/docs/account-validation-and-doctor).

## 4. Discover your folders

```bash
icloud mail folders refresh
```

Then inspect the saved list:

```bash
icloud mail folders list
```

Inbox and Sent are the normal starting folders. You can opt custom folders into routine synchronization later.

## 5. Sync recent mail

```bash
icloud mail sync
```

To pull additional recent pages until you are current:

```bash
icloud mail sync --folder inbox --until-current
```

Normal sync is designed for current mail. If you later want older history, use [Sync mail](/docs/sync-and-read-inbox) and its backfill workflow.

## 6. Read your inbox

```bash
icloud mail messages list
```

Open a message using the returned `msg_…` ID:

```bash
icloud mail message show msg_…
icloud mail message show msg_… --full
```

The first command gives you a concise view. `--full` asks for the complete readable body.

For HTML or exact message data:

```bash
icloud mail message show msg_… --format html
icloud mail message raw msg_… > message.eml
```

See [Read mail](/docs/read-mail) for the rest of the content options.

## 7. Search

Search your synchronized mail locally:

```bash
icloud mail search invoice
icloud mail search --from billing@example.com --unread
```

If you need to check iCloud directly for something that is not in your local copy yet:

```bash
icloud mail search invoice --remote
```

See [Search mail](/docs/search-synchronized-mail) for filters, thread results, and caching selected remote hits.

## 8. Read conversations

```bash
icloud mail threads list
icloud mail threads show thr_…
```

A message ID works too:

```bash
icloud mail threads show msg_…
```

See [Conversations and threads](/docs/read-reconstructed-conversations).

## 9. Save an attachment

```bash
icloud mail attachments list msg_…
icloud mail attachments save att_… --output invoice.pdf
```

Or save all attachments from a message:

```bash
icloud mail attachments save-all msg_… --dir ./attachments
```

See [Attachments](/docs/save-mail-attachments).

## 10. Reply safely

Preview a reply first:

```bash
icloud mail reply msg_… --body-file reply.txt --dry-run
```

Then send it:

```bash
icloud mail reply msg_… \
  --body-file reply.txt \
  --idempotency-key reply-2026-08-11-01 \
  --yes
```

`--idempotency-key` is especially useful in automation because repeating the same request with the same key will not create an accidental duplicate send.

## 11. Send a new message

```bash
icloud mail send \
  --to teammate@example.com \
  --subject 'Project update' \
  --body-file update.txt \
  --attach report.pdf \
  --dry-run
```

After review:

```bash
icloud mail send \
  --to teammate@example.com \
  --subject 'Project update' \
  --body-file update.txt \
  --attach report.pdf \
  --idempotency-key project-update-01 \
  --yes
```

See [Send, reply, and forward](/docs/send-mail-safely).

## 12. Keep mail fresh while you work

For a foreground watcher:

```bash
icloud mail watch
```

Press Ctrl-C to stop it. For an agent or another program that wants events:

```bash
icloud mail watch --jsonl
```

## Multiple accounts

Use `--account` when you have more than one:

```bash
icloud --account Work mail sync
icloud --account Work mail messages list
```

You can also set a default:

```bash
icloud account default Work
```

## A useful mental model

For everyday use:

```text
folders refresh → sync → list/search → inspect → act
```

You do not need to understand how the CLI talks to iCloud to use it. The task guides explain the user-facing behavior and safety rules; the [command reference](/docs/command-reference) contains every flag when you need something exact.
