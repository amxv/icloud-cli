---
title: Search synchronized mail
description: Find messages or conversations from private local mail state with indexed text, precise filters, stable cursors, and visible recovery.
order: 8
category: Guides
summary: Offline full-text search, structured filters, message or thread results, bounded pages, query-bound cursors, and local index repair.
---

## Find mail without contacting iCloud

Synchronize the relevant folders first, then search the private local copy:

```bash
icloud mail search invoice
```

Search matches synchronized subject, sender, To and CC participants, decoded message text, attachment names and media types, and RFC Message-ID. BCC is never part of normal search.

The compact default returns bounded message hits with the matched field and a short excerpt:

```text
search-hit
msg_… kind=message
from="Billing <billing@example.com>"
participants=billing@example.com,owner@example.com
subject="August invoice"
at=2026-08-06T09:30:00Z
state=unread
match=body
excerpt="Your [invoice] is ready…"

search-results
account=acct_…
kind=messages
count=1
more=false
sort=relevance
index=complete
```

Search is offline and does not change read state. Run `icloud mail sync` when you expect newer remote mail.

## Narrow the result deliberately

Use one or several filters:

```bash
icloud mail search --from billing@example.com --unread
icloud mail search --participant teammate@example.com --subject launch
icloud mail search --folder sent --after 2026-08-01
icloud mail search --attachment-name invoice --attachment-type application/pdf
icloud mail search --rfc-id '<message@example.com>'
```

Useful choices:

- `--unread` or `--read` selects the logical read state across current eligible locations.
- `--flagged` or `--unflagged` selects the logical flagged state.
- `--folder` requires a current synchronized location in the selected folder.
- `--after` includes the boundary; `--before` excludes it. Use RFC3339 or a `YYYY-MM-DD` UTC date.
- `--from` checks From and Reply-To. `--participant` also checks To and CC.

Free text and filters combine with AND. For example:

```bash
icloud mail search invoice --from billing@example.com --unread --after 2026-08-01
```

## Search conversations instead of individual messages

```bash
icloud mail search launch --result threads
```

The CLI returns each matching reconstructed conversation once. A thread hit identifies the best matching message and keeps the aggregate bounded:

```text
search-hit
thr_… kind=thread
participants=owner@example.com,teammate@example.com
subject="Launch plan"
last=2026-08-06T10:15:00Z
messages=4
unread=1
match=body
matched_message=msg_…
excerpt="The revised [launch] plan…"
```

You can apply the same filters directly while browsing normal local views:

```bash
icloud mail inbox --query invoice --unread
icloud mail latest --query launch --participant teammate@example.com
```

Without filters, `mail inbox` and `mail latest` keep their familiar compact records.

## Continue a bounded result page

The default limit is 20 and the maximum is 100:

```bash
icloud mail search invoice --limit 50
```

When the summary reports `more=true`, pass its `next_cursor` back with the exact same query, filters, result kind, and account:

```bash
icloud mail search invoice --limit 50 --cursor cur_…
```

A cursor belongs to one complete search. Changing a filter, account, or result kind returns a usage error rather than skipping or repeating records silently.

Use `--json` when automation needs the cursor, numeric rank, matched field, bounded excerpt, query summary, and sanitized message or thread object:

```bash
icloud --request-id search-01 mail search invoice --limit 50 --json
```

## Understand index status

Every result summary reports one local index state:

- `index=complete` — all synchronized messages have decoded body text indexed.
- `index=pending` — metadata and previews remain searchable, but some older or interrupted records still need body indexing.
- `index=error` — at least one record could not be rebuilt from its saved local message.

Repair a bounded local batch without contacting iCloud:

```bash
icloud mail search repair
icloud mail search repair --limit 100 --json
```

Repeat while `remaining` or `errors` is nonzero. Repair verifies and reparses already synchronized local message bytes; it does not redownload a mailbox or change stable message, location, attachment, or thread IDs.

If errors remain, inspect the affected local content workflow and sync frontier before changing state manually. Continue with [Sync and read mail](/docs/sync-and-read-inbox), [Read reconstructed conversations](/docs/read-reconstructed-conversations), or [Troubleshooting](/docs/troubleshooting).
