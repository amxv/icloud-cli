---
title: "Search mail"
description: "Search synchronized mail locally, use filters, search conversations, or check iCloud directly when freshness matters."
order: 12
category: Mail
summary: "Search locally by default, add useful filters, and opt into remote search only when you need a fresh provider check."
---

## Search your synchronized mail

```bash
icloud mail search invoice
```

Local search is the default. It can match subjects, senders, participants, readable message text, attachment metadata, and message IDs from the mail you have synchronized.

Useful filters include:

```bash
icloud mail search --from billing@example.com --unread
icloud mail search --participant teammate@example.com --subject launch
icloud mail search --attachment-type application/pdf
icloud mail search --after 2026-08-01 --before 2026-09-01
icloud mail search --flagged
```

Filters combine, so you can be specific:

```bash
icloud mail search invoice \
  --from billing@example.com \
  --unread \
  --after 2026-08-01
```

## Search conversations instead of messages

```bash
icloud mail search launch --result threads
```

This returns matching conversations rather than every matching message.

## Search a single folder

```bash
icloud mail search invoice --folder inbox
icloud mail search invoice --folder sent
icloud mail search invoice --folder Receipts
```

## Check iCloud directly

Use `--remote` when you want a fresh provider-side check without first doing a normal sync:

```bash
icloud mail search invoice --remote
icloud mail search invoice --remote --folder inbox
```

Remote search is useful for discovery. A remote hit has a temporary `rem_…` reference until you choose to cache it locally.

Inspect the current summary:

```bash
icloud mail message show rem_… --remote
```

Cache and immediately read the full message:

```bash
icloud mail message show rem_… --remote --cache --full
```

Or cache selected hits without changing the normal sync position:

```bash
icloud mail sync --hydrate rem_…
icloud mail sync --hydrate rem_…,rem_…
```

Once cached, the result has normal stable local IDs such as `msg_…`.

## Search every remote folder

```bash
icloud mail search invoice --remote --all-folders
```

This work is intentionally bounded. If the result says `partial=true` or `more=true`, continue with the returned remote cursor:

```bash
icloud mail search invoice \
  --remote \
  --all-folders \
  --cursor rcur_…
```

An early partial page can contain no hits while the search is still progressing. Continue the cursor until the result is complete.

## Local pagination

```bash
icloud mail search invoice --limit 50
icloud mail search invoice --limit 50 --cursor cur_…
```

Local `cur_…` cursors and remote `rcur_…` cursors are different. Always reuse the cursor with the same search that produced it.

## VIP and blocked sender filters

If you use local sender preferences:

```bash
icloud mail search --vip
icloud mail search --blocked
```

See [Mailing lists and sender preferences](/docs/mailing-lists-and-sender-preferences).

## Repair local search

If search reports that some synchronized content still needs indexing:

```bash
icloud mail search repair
```

This works from already synchronized mail. It does not perform a normal mailbox sync.

For exact flags, see [`icloud mail search`](/docs/command-reference#icloud-mail-search).
