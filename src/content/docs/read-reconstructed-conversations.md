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
icloud mail threads list
```

The command reads local state only. It does not contact Apple, change read state, or hydrate missing mailboxes.

`icloud mail latest` remains a compatibility alias for this same list path. New scripts and interactive use should prefer `mail threads list`.

Each compact record is one reconstructed conversation:

```text
thread
thr_… subject="Project update"
messages=3
unread=1
replied=true
vip_senders=1
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

When synchronized messages in a conversation come from addresses marked with `mail senders vip` or `mail senders block`, the thread summary also reports the nonzero `vip_senders` / `blocked_senders` counts. These are local account preferences used for triage only; they do not change thread membership, synchronization, read state, or provider mailboxes.

Filter conversations with the same local index used by message search:

```bash
icloud mail threads list --query launch --unread
icloud mail threads list --participant teammate@example.com --folder sent
```

When a filtered page reports `more=true`, continue with its query-bound `next_cursor`. Use `icloud mail search <text> --result threads` for the same conversation result shape with an explicit search command. See [Search synchronized mail](/docs/search-synchronized-mail).

## These are local RFC threads

The CLI does not use or claim an Apple-native thread identifier. It reconstructs conversations from synchronized RFC mail evidence:

1. ordered `References` values;
2. `In-Reply-To`;
3. a narrow subject-and-participant fallback only when RFC relationship headers are absent.

Subject similarity never overrides RFC evidence. Two provider messages are not joined merely because their body, subject, sender, or timestamp looks alike.

Malformed, missing, self-referential, or cyclic relationships remain observable instead of being repaired by guesswork. Use verbose or metadata JSON when you need the evidence that produced a grouping.

Thread reconstruction is derived local state and is never allowed to block a canonical message/provider-location commit merely because a conversation is exceptionally large. If bounded reconstruction is deferred, thread reads report `projection_state=pending` or `projection_state=degraded` plus a reason and pending-work count instead of pretending the cached grouping is complete. Resume the persisted work locally with:

```bash
icloud mail sync repair --check --verbose
icloud mail sync repair --apply
```

Repeat bounded apply passes while repair reports remaining projection work. Existing message and provider-location identities remain usable while reconstruction catches up.

## Read one conversation chronologically

Use a stable thread ID from `mail threads list`:

```bash
icloud mail threads show thr_…
```

You can also start with any stable message ID:

```bash
icloud mail threads show msg_…
```

The singular `icloud mail thread show` path remains a compatibility alias for `mail threads show` and returns the same pages, cursors, errors, and content modes.

The compact default prints the thread aggregate once, followed by a bounded chronological entry for each selected message. Each message keeps its immutable `msg_…` identity and independent provider locations.

```text
thread
thr_… subject="Project update"
messages=2
unread=1
replied=true
reconstruction=local_rfc_graph

message
msg_… at=2026-08-06T09:20:00Z
from=sender@example.com
direction=inbound
state=unread
replied=true
reply_state=answered
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

`replied=true` is aggregate proven reply history. On a message, `reply_state=answered` means a current eligible provider location carries standard `\Answered`; `provider_pending` means SMTP acceptance is proven but the exact provider-state write still needs reconciliation; and `replied` means durable local reply provenance exists even when no current provider Answered location is available. A thread's `replied=true` means at least one member has provider Answered state or a proven sent reply. It does not expose the reply's private recipients.

`forwarded=true` is separate durable provenance. A message becomes forwarded only after a forward attempt for that exact `msg_…` has a proven sent outcome; failed and unknown SMTP attempts do not count. A thread is forwarded when at least one member has that proven history. iCloud currently exposes no verified provider forwarded marker through this integration, so this field is intentionally local and does not reinterpret unknown IMAP keywords.

`direction` is canonical cached message state, not a folder-name guess. Any newly ingested message whose `From` matches an enabled `MailIdentity` is classified `outbound`, even when that copy is outside Sent. When a new self address is configured later, `mail identities reclassify` can promote older cached inbound rows in bounded resumable passes. Promotion does not change stable `msg_…`/`thr_…` IDs, and disabling an identity does not demote previously proven outbound history.

When a page ends with `more=true`, continue from the returned cursor instead of rereading earlier messages:

```bash
icloud mail threads show thr_… --limit 20 --cursor cur_…
```

Compact output reports the value as `next_cursor`; JSON uses the same field. A cursor belongs to one reconstructed thread, so passing it to another thread fails instead of silently skipping messages. The final page has no next cursor.

## Expand content deliberately

Use the same content choices as a single-message read:

```bash
icloud mail threads show thr_… --full
icloud mail threads show thr_… --format text
icloud mail threads show thr_… --format html
icloud mail threads show thr_… --body-only
icloud mail threads show thr_… --headers
icloud mail threads show thr_… --metadata-only
icloud mail threads show thr_… --verbose
icloud mail threads show thr_… --json
```

`--full` returns complete decoded text for each selected message in chronological order. `--body-only` removes thread and message wrappers and writes only the selected bodies, separated by a blank line, which is useful for redirection. HTML, decoded public headers, MIME-part detail, and JSON remain separate opt-ins.

Verbose RFC edge evidence is also bounded. If more than the displayed evidence limit exists, JSON sets `edge_evidence_truncated=true` and compact verbose output prints an explicit truncation record rather than silently implying the evidence list is complete.

Compact reads and plain `--metadata-only` use stored summaries and do not require raw message files to be opened. Complete text, HTML, headers that still need decoding, verbose MIME detail that has not yet been indexed, and raw MIME require the corresponding synchronized raw content to remain available locally.

Plain multi-message raw output is rejected because concatenated RFC822 streams are ambiguous. Use one of these explicit forms instead:

```bash
icloud mail threads show thr_… --format raw --json
icloud mail message raw msg_… > message.eml
```

Raw JSON remains subject to the existing per-message size bound. Exact raw streaming by message ID is the better choice for large mail.

## Inspect graph evidence

```bash
icloud mail threads show thr_… --metadata-only --verbose --json
```

The response includes bounded edge evidence for the selected messages. Internal evidence-row identifiers are not part of the public response. Each edge identifies:

- the child message;
- whether the source was `reference`, `in_reply_to`, or conservative `fallback`;
- the ordered reference position;
- the normalized or original malformed token;
- the resolved parent message when one exists;
- status such as `resolved`, `unresolved`, `malformed`, `self`, `cycle`, `grouped`, or `isolated`.

Metadata-only output removes thread and message excerpts. BCC, private raw-storage coordinates, and message bodies remain excluded.

## Understand late merges and aliases

A reply can arrive before its root, or a later message can bridge two provisional conversations. Newly synchronized evidence can merge or split the affected local conversations while preserving every stable message ID.

When two provisional threads merge, one existing `thr_…` ID remains canonical. Retired IDs become durable aliases:

```bash
icloud mail threads show thr_old…
```

The command resolves the alias and reports `redirected_from=thr_old…`. Message, location, MIME-part, attachment, and blob IDs never change during a thread merge.

## Change one canonical thread without guessing provider copies

Thread actions are built on the same exact per-message state and location engines as direct `msg_…` commands. Start from a reviewed canonical thread or any durable retired alias, then always name the provider-copy scope explicitly:

```bash
icloud mail threads mark-read thr_… --folder inbox --jsonl
icloud mail threads flag thr_… --folder inbox --jsonl

icloud mail threads copy thr_… \
  --from inbox \
  --to Projects \
  --idempotency-key thread-copy-01 \
  --dry-run
```

`--folder` and `--from` are required on thread writes. The CLI resolves the supplied `thr_…` alias or `msg_…` selector to one current canonical conversation, walks its members chronologically, and includes only members with an eligible current location in that exact selected folder. A Sent-only member is therefore skipped by an Inbox-scoped action, and another copy of the same logical message in Archive or a custom folder is never changed implicitly.

State actions are `mark-read`, `mark-unread`, `flag`, and `unflag`. Location actions are `move`, `copy`, `archive`, `unarchive`, `trash`, `restore`, `junk`, `not-junk`, and `delete`; each still applies the normal per-message role and destination checks. Location dry runs resolve every eligible exact source/destination before provider contact, create no mutation intents, and preserve the existing fail-fast/continue and unknown-provider-outcome rules.

Thread writes are bounded to at most 100 chronological thread members per page. A conversation that fits in one page is treated as one complete reviewed target set. If the canonical conversation has more members than the selected `--limit`, the first invocation refuses to mutate an implicit partial thread. Review the conversation, then opt into bounded paging deliberately:

```bash
icloud mail threads mark-read thr_… --folder inbox --limit 100 --page --jsonl
icloud mail threads mark-read thr_… --folder inbox --limit 100 --cursor cur_… --jsonl
```

Each page reports the canonical `thread_id`, total `thread_message_count`, `more`, and `next_cursor`. A page can contain zero eligible locations when the selected folder has no copy among those chronological members; in that case no provider call occurs and the cursor still advances when another thread page exists. The cursor remains thread-bound and cannot be reused on another conversation.

For location writes, one supplied idempotency key derives stable private per-message replay identities from the canonical thread ID and each `msg_…` ID. That means a retry through a retired alias, canonical ID, or later page returns already recorded `mut_…` outcomes without repeating accepted provider writes. `atomic` means validate the bounded location page then stop on the first provider failure; it never claims earlier accepted IMAP writes were rolled back. `continue-on-error` attempts independent remaining eligible members.

Thread mutation is intentionally unavailable while local reconstruction reports `projection_state=pending` or `projection_state=degraded`. The CLI refuses to treat an incomplete projection as destructive scope; run the bounded repair workflow above, review the repaired thread, and only then retry. This does not block direct exact-message operations while reconstruction catches up.

## Interpret aggregates

Thread counts derive from current local messages and locations:

- `messages` counts immutable logical messages, not mailbox copies;
- `unread` counts messages with at least one eligible unread non-Sent location;
- `attachments` sums logical message attachment counts;
- `folders` lists current non-deleted provider locations across the thread;
- `participants` uses From, Reply-To, To, and CC addresses, never BCC;
- `vip_senders` / `blocked_senders` count distinct message From addresses carrying those local sender preferences when the count is nonzero;
- `first` and `last` use the best stored message times;
- `snippet` follows the latest message with a usable bounded preview.

A read or flag update refreshes the affected conversation summaries. A message with both Inbox and Sent locations still counts once.

## Recover from unexpected grouping

### A thread is missing a parent

Synchronize the folder that may contain the root, then inspect the graph again:

```bash
icloud mail sync --folder inbox
icloud mail threads show thr_… --metadata-only --verbose --json
```

An unresolved token is preserved. If the matching root arrives later, it joins the existing conversation without replacing immutable message IDs.

### Similar subjects stayed separate

That is the conservative default. Subject-and-participant fallback is used only when relationship headers are absent, participants match exactly, a reply-like subject exists, and messages are close enough in time. Use graph evidence to confirm why the records stayed independent.

### A cycle or malformed relationship appears

The CLI records the evidence and keeps the messages readable. It does not invent a parent, delete a message, or replace RFC headers. Keep local state intact for the later repair workflow.

Continue with [Sync and read mail](/docs/sync-and-read-inbox) for acquisition and state, [Search synchronized mail](/docs/search-synchronized-mail) for indexed filters, [Output and automation](/docs/output-and-automation) for stream contracts, or the [command reference](/docs/command-reference) for exact flags.
