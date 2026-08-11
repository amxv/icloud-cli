---
title: "Read mail"
description: "Browse synchronized messages, open complete bodies only when needed, and export exact EML when a workflow needs it."
order: 11
category: Mail
summary: "List messages, inspect concise previews, read full text or HTML, and export exact message files."
---

After [synchronizing mail](/docs/sync-and-read-inbox), your everyday reading workflow is local and fast.

## List Inbox messages

```bash
icloud mail messages list
```

Common filters work directly on the list:

```bash
icloud mail messages list --unread
icloud mail messages list --from billing@example.com
icloud mail messages list --query invoice
```

Read another synchronized folder:

```bash
icloud mail messages list --folder sent
icloud mail messages list --folder Receipts
```

The older `icloud mail inbox` command is kept as a compatibility alias; new usage should prefer `mail messages list`.

## Open one message

Use the returned `msg_…` ID:

```bash
icloud mail message show msg_…
```

The default is intentionally concise. It gives you enough context to decide whether you need more.

## Read the full body

```bash
icloud mail message show msg_… --full
```

Or ask for a particular representation:

```bash
icloud mail message show msg_… --format text
icloud mail message show msg_… --format html
```

Use `--body-only` when the body itself should be the only output:

```bash
icloud mail message show msg_… --full --body-only > message.txt
```

## Inspect headers or metadata

```bash
icloud mail message show msg_… --headers
icloud mail message show msg_… --metadata-only
icloud mail message show msg_… --verbose
```

`--verbose` is useful for diagnostics and advanced message details; it is not intended as the normal reading mode.

## Export exact EML

Stream the exact stored message:

```bash
icloud mail message raw msg_… > message.eml
```

Or use the file-oriented export command:

```bash
icloud mail message export msg_… --output message.eml
```

An existing output file is not overwritten unless you explicitly add `--force`.

## Pagination

Message lists are bounded. If a result includes a `next_cursor`, continue the same query with:

```bash
icloud mail messages list --limit 50 --cursor cur_…
```

Keep the rest of the query the same. A cursor belongs to the query that created it.

## JSON

```bash
icloud mail messages list --json
icloud mail message show msg_… --json
icloud mail message show msg_… --full --json
```

Default JSON is still intentionally bounded; full bodies are included only when you explicitly request the matching content mode.

## Attachments and conversations

For files inside a message, continue with [Attachments](/docs/save-mail-attachments).

For a conversation view across related messages, use [Conversations and threads](/docs/read-reconstructed-conversations).
