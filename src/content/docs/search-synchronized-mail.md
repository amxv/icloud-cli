---
title: Search synchronized mail
description: Search private synchronized state quickly, or query bounded iCloud metadata explicitly and cache only the remote messages you choose.
order: 8
category: Guides
summary: Offline full-text search, structured filters, message or thread results, bounded iCloud search, stale-safe remote references, targeted hydration, cursors, and local index repair.
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

## Search iCloud explicitly when freshness matters

Use `--remote` when you need to check iCloud without first advancing the normal synchronization frontier:

```bash
icloud mail search invoice --remote
icloud mail search --remote --folder archive --from billing@example.com --unread
```

Remote search defaults to Inbox and contacts iCloud. It returns bounded provider locations marked with `rem_…` references rather than pretending an uncached result already has a stable local message ID:

```text
remote-hit
rem_… folder=INBOX
folder_id=fld_…
uid_validity=1
uid=184
from="Billing <billing@example.com>"
subject="August invoice"
at=2026-08-06T09:30:00Z
state=unread
match=provider_text
excerpt="Your invoice is ready…"

remote-results
account=acct_…
count=1
more=false
partial=false
folders=1
candidate_uids=184
```

The provider path supports text, sender, participant, subject, RFC Message-ID, read/flag state, and date filters. It does not provide local FTS ranking, reconstructed-thread results, attachment filename/type filters, or local cursors. Those choices return a usage error rather than silently changing meaning.

Precise RFC3339 `--after` and `--before` filters are enforced after iCloud's day-granularity IMAP match, but same-day candidates rejected by the exact timestamp do not consume the requested result quota. The CLI continues through bounded UID windows until it has enough exact matches or reaches its candidate-position bound; `partial=true` means older matching state may still exist beyond that bound.

Use `--all-folders` only when you deliberately want every current selectable folder. The fan-out is capped at 10 folders. Each folder examines a bounded recent provider range; `partial=true` means older provider positions were not examined, so narrow the folder, dates, sender, subject, or Message-ID. `more=true` means additional matches were truncated by the result limit.

Remote text previews are bounded and decoded from the provider response when possible. They are useful for selection, not a substitute for the verified local MIME parser.

## Inspect or cache one remote result

Read the current bounded provider summary without caching:

```bash
icloud mail message show rem_… --remote
```

Complete text, HTML, headers, raw MIME, MIME details, and body-only output require caching first:

```bash
icloud mail message show rem_… --remote --cache --full
icloud mail message show rem_… --remote --cache --format html
```

Cache several selected references without performing an ordinary forward sync:

```bash
icloud mail sync --hydrate rem_…
icloud mail sync --hydrate rem_…,rem_… --json
```

Hydration verifies that the account, folder, UIDVALIDITY, and UID still identify the same provider location, then sends the raw message through the normal parser, blob, identity, search, attachment, and thread path. It returns stable `msg_…` and `loc_…` IDs without echoing the remote reference. Repeating the same hydration reports `cached` and performs no duplicate ingestion.

For bulk `--hydrate … --jsonl`, each attempted reference produces one item record in original input order. The record carries its original index/reference identity, result or error, and committed state; the terminal counts are derived from those item records. A handled item failure is not followed by a contradictory second stderr completion/error stream.

Targeted hydration does not create, advance, reset, or repair the ordinary folder checkpoint. A later normal sync recognizes the cached provider location and remains idempotent.

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

## Continue a bounded local result page

The default limit is 20 and the maximum is 100:

```bash
icloud mail search invoice --limit 50
```

When the summary reports `more=true`, pass its `next_cursor` back with the exact same query, filters, result kind, and account:

```bash
icloud mail search invoice --limit 50 --cursor cur_…
```

A cursor belongs to one complete local search. Changing a filter, account, or result kind returns a usage error rather than skipping or repeating records silently. Remote search does not accept `--cursor`; use a narrower remote query when `partial=true` or `more=true`.

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
