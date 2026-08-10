---
title: Review and apply immutable mail selections
description: Freeze a bounded local result set before an agent changes provider state, review its exact stable targets, and apply only that snapshot.
order: 9
category: Guides
summary: Short-lived local MailSelection snapshots for explicit IDs or one current local search page, with exact provider-copy evidence and hash-bound writes.
---

`MailSelection` is the safety boundary between **finding mail** and **changing mail**. A selection is a short-lived, local, immutable snapshot of at most 100 stable `msg_…` targets. Applying it never reruns the search or thread query that created it.

This is intentionally not a saved search or Smart Mailbox. The CLI stores only stable IDs, a source/query hash, optional exact provider-location evidence, creation/expiry times, and a content-independent selection hash. It does not store the source query text, subjects, participants, bodies, excerpts, attachment names, or raw MIME inside the selection record.

## Freeze explicit messages or threads

Create a selection from stable message and/or thread IDs:

```bash
icloud mail selection create msg_… msg_… --ttl 15m
icloud mail selection create thr_… --scope-folder inbox --ttl 15m
```

A `thr_…` target is canonicalized immediately. If the ID is a durable retired thread alias, the selection freezes the current canonical members in chronological order. A thread whose reconstruction is pending/degraded or whose complete membership exceeds the 100-item selection bound is rejected rather than silently truncated.

`--scope-folder` is optional for local review/read workflows. Add it when you intend to change provider state. It filters the reviewed logical set to members that currently have one eligible exact copy in that folder and freezes the stable location ID plus UIDVALIDITY, UID, and mod-sequence evidence for that copy.

## Freeze one current local search page

Use the normal local search filters with `--search`:

```bash
icloud mail selection create \
  --search \
  --query invoice \
  --unread \
  --folder inbox \
  --scope-folder inbox \
  --limit 50 \
  --ttl 15m
```

The source is **one bounded current local page**. `--result messages` freezes matching messages directly. `--result threads` expands each matched canonical thread, while still enforcing the final 100-message selection bound.

New mail that matches the same search after creation is not part of the selection. Deleting or changing the source query later cannot change the frozen membership because apply never evaluates it again. Remote search is deliberately not a selection source; hydrate/synchronize first, then select from the canonical local store.

## Review the exact snapshot

```bash
icloud mail selection show sel_…
icloud mail selection show sel_… --json
```

Compact output lists only stable message/location evidence and a summary containing the local selection ID, count, scope folder ID, selection request hash, and expiry. JSON exposes the same bounded structural evidence. It does not expand message content.

Selections default to 15 minutes and accept `--ttl` from `1m` through `24h`. Expired selections can still be inspected or deleted, but they cannot be applied; create and review a fresh snapshot instead.

## Read exactly the frozen logical messages

```bash
icloud mail selection apply sel_… --action read
icloud mail selection apply sel_… --action read --json
```

Read returns the current bounded message summaries for exactly the frozen `msg_…` IDs. It does not add newly matching mail and does not contact iCloud.

## Review a provider write and copy its request hash

Provider writes require a selection created with `--scope-folder`. Review the intended action first:

```bash
icloud mail selection apply sel_… --action mark-read --dry-run

icloud mail selection apply sel_… \
  --action copy \
  --to Projects \
  --mode atomic \
  --dry-run
```

The dry run validates the frozen exact source evidence before provider contact and returns an **apply `request_hash`**. For location actions it also resolves the same exact per-message location plans the normal mutation engine will use, so the hash binds the reviewed destination and semantic-folder resolution as well as the immutable selection.

No provider call or location-mutation intent is created by this review.

## Apply only the reviewed request

Use the exact hash from the matching dry run:

```bash
icloud mail selection apply sel_… \
  --action mark-read \
  --request-hash <reviewed-hash> \
  --jsonl

icloud mail selection apply sel_… \
  --action copy \
  --to Projects \
  --mode atomic \
  --request-hash <reviewed-hash> \
  --yes \
  --jsonl
```

If the action, destination, semantic-role resolution, or exact provider source no longer matches the reviewed request, apply fails before the new provider write. Expired or cross-account selections also fail before provider contact.

State actions are `mark-read`, `mark-unread`, `flag`, and `unflag`. Location actions are `move`, `copy`, `archive`, `unarchive`, `trash`, `restore`, `junk`, `not-junk`, and `delete`. Every item still goes through the existing exact provider-copy mutation engine; selections add reviewed scope, not another IMAP implementation.

Location apply derives a private stable per-item idempotency identity from the selection plus reviewed apply hash. Replaying the same accepted selection request therefore returns existing durable `mut_…` outcomes instead of duplicating a provider write, even after the source move changed local location state.

`--mode atomic` retains the bulk meaning used elsewhere: validate the bounded request, then stop at the first provider failure. Earlier accepted provider writes remain committed and are reported truthfully. `--mode continue-on-error` attempts independent remaining frozen targets.

## When the mailbox changed after review

A selection freezes exact provider-location evidence, not message content. If synchronization or another client replaces/deletes the reviewed provider copy, or changes its mod-sequence before a state change, the apply is rejected as stale. Create a new selection, review it, and use its new apply hash.

That fail-closed rule prevents a stale reviewed selection from silently switching to a newly arrived copy in the same folder.

## Delete local review state

```bash
icloud mail selection delete sel_…
```

Deletion is local-only and never contacts iCloud. It does not change any message or provider location.

For how direct and thread location mutations behave after selection review, see [Move, copy, archive, and delete mail](/docs/manage-mail-locations). For local search filters and cursors, see [Search synchronized mail](/docs/search-synchronized-mail). For JSON/JSONL contracts, see [Output and automation](/docs/output-and-automation).
