---
title: Read reconstructed conversations
description: Browse locally reconstructed RFC conversations, understand stable thread aliases, and expand chronological message content deliberately.
order: 7
category: Guides
summary: Offline latest-thread summaries, chronological thread reads, RFC evidence, conservative fallback, durable aliases, and explicit content expansion.
---

## Browse the newest conversations offline

After synchronizing mail, run:

```bash
icloud mail latest
```

The command reads local state only. It does not contact Apple, change read state, or hydrate missing mailboxes.

Each compact record is one reconstructed conversation:

```text
thread
thr_… subject="Project update"
messages=3
unread=1
participants=owner@example.com,sender@example.com
folders=fld_…
attachments=2
last=2026-08-06T09:21:00Z
reconstruction=local_rfc_graph

threads
account=acct_…
count=1
more=false
```

The default returns 20 conversations and accepts `--limit` through 100. `more=true` means additional local threads were not printed.

## These are local RFC threads

The CLI does not use or claim an Apple-native thread identifier. It reconstructs conversations from synchronized RFC mail evidence:

1. ordered `References` values;
2. `In-Reply-To`;
3. a narrow subject-and-participant fallback only when RFC relationship headers are absent.

Subject similarity never overrides RFC evidence. Two provider messages are not joined merely because their body, subject, sender, or timestamp looks alike.

Malformed, missing, self-referential, or cyclic relationships remain observable instead of being repaired by guesswork. Use verbose or metadata JSON when you need the evidence that produced a grouping.

## Read one conversation chronologically

Use a stable thread ID from `mail latest`:

```bash
icloud mail thread show thr_…
```

You can also start with any stable message ID:

```bash
icloud mail thread show msg_…
```

The compact default prints the thread aggregate once, followed by a bounded chronological entry for each selected message. Each message keeps its immutable `msg_…` identity and independent provider locations.

```text
thread
thr_… subject="Project update"
messages=2
unread=1
reconstruction=local_rfc_graph

message
msg_… at=2026-08-06T09:20:00Z
from=sender@example.com
direction=inbound
state=unread
excerpt="Initial update…"

message
msg_… at=2026-08-06T09:21:00Z
from=owner@example.com
direction=outbound
state=read
excerpt="Thanks, I will review it…"

thread-page
thread=thr_…
count=2
more=false
```

The default is bounded and preview-only. It never prints BCC, private blob paths, complete bodies, HTML, raw MIME, or decoded attachments.

## Expand content deliberately

Use the same content choices as a single-message read:

```bash
icloud mail thread show thr_… --full
icloud mail thread show thr_… --format text
icloud mail thread show thr_… --format html
icloud mail thread show thr_… --headers
icloud mail thread show thr_… --metadata-only
icloud mail thread show thr_… --verbose
icloud mail thread show thr_… --json
```

`--full` returns complete decoded text for each selected message in chronological order. HTML, decoded public headers, MIME-part detail, and JSON remain separate opt-ins.

Plain multi-message raw output is rejected because concatenated RFC822 streams are ambiguous. Use one of these explicit forms instead:

```bash
icloud mail thread show thr_… --format raw --json
icloud mail message raw msg_… > message.eml
```

Raw JSON remains subject to the existing per-message size bound. Exact raw streaming by message ID is the better choice for large mail.

## Inspect graph evidence

```bash
icloud mail thread show thr_… --metadata-only --verbose --json
```

The response includes bounded edge evidence for the selected messages. Each edge identifies:

- the child message;
- whether the source was `reference`, `in_reply_to`, or conservative `fallback`;
- the ordered reference position;
- the normalized or original malformed token;
- the resolved parent message when one exists;
- status such as `resolved`, `unresolved`, `malformed`, `self`, `cycle`, `grouped`, or `isolated`.

Metadata-only output removes thread and message excerpts. BCC, private raw-storage coordinates, and message bodies remain excluded.

## Understand late merges and aliases

A reply can arrive before its root, or a later message can bridge two provisional conversations. The CLI recomputes the account-local graph in the same transaction as synchronized message and flag changes.

When two provisional threads merge, one existing `thr_…` ID remains canonical. Retired IDs become durable aliases:

```bash
icloud mail thread show thr_old…
```

The command resolves the alias and reports `redirected_from=thr_old…`. Message, location, MIME-part, attachment, and blob IDs never change during a thread merge.

## Interpret aggregates

Thread counts derive from current local messages and locations:

- `messages` counts immutable logical messages, not mailbox copies;
- `unread` counts messages with at least one eligible unread non-Sent location;
- `attachments` sums logical message attachment counts;
- `folders` lists current non-deleted provider locations across the thread;
- `participants` uses From, Reply-To, To, and CC addresses, never BCC;
- `first` and `last` use the best stored message times;
- `snippet` follows the latest message with a usable bounded preview.

A read or flag update refreshes affected thread aggregates transactionally. A message with both Inbox and Sent locations still counts once.

## Recover from unexpected grouping

### A thread is missing a parent

Synchronize the folder that may contain the root, then inspect the graph again:

```bash
icloud mail sync --folder inbox
icloud mail thread show thr_… --metadata-only --verbose --json
```

An unresolved token is preserved. If the matching root arrives later, it joins the existing conversation without replacing immutable message IDs.

### Similar subjects stayed separate

That is the conservative default. Subject-and-participant fallback is used only when relationship headers are absent, participants match exactly, a reply-like subject exists, and messages are close enough in time. Use graph evidence to confirm why the records stayed independent.

### A cycle or malformed relationship appears

The CLI records the evidence and keeps the messages readable. It does not invent a parent, delete a message, or replace RFC headers. Keep local state intact for the later repair workflow.

Continue with [Sync and read mail](/docs/sync-and-read-inbox) for acquisition and state, [Output and automation](/docs/output-and-automation) for stream contracts, or the [command reference](/docs/command-reference) for exact flags.
