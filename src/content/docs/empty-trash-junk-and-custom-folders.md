---
title: Empty Trash, Junk, or a custom folder safely
description: Review and permanently delete exact bounded provider locations without mailbox-wide EXPUNGE, then continue page by page until the folder is empty.
order: 10
category: Guides
summary: Short-lived exact-location purge reviews for semantic Trash/Junk and deliberately named custom folders, with hash-bound apply, resumable pages, and ambiguity-safe replay.
---

`mail empty` is the bounded permanent-deletion workflow for a whole folder. It never translates “empty this mailbox” into a blind mailbox-wide EXPUNGE. Instead, each review freezes at most 100 exact current provider locations and later deletes only those exact UIDs through the same durable location-mutation engine used by `mail delete`.

The review is deliberately two-step because synchronized folder contents can change between “show me what would be removed” and “remove it.” A `pur_…` review ID freezes one exact page so newly arriving mail cannot silently enter the already reviewed action.

## Review one bounded page first

Trash and Junk resolve through the account's semantic folder roles:

```bash
icloud mail empty trash --limit 50 --dry-run
icloud mail empty junk --limit 50 --dry-run
```

If iCloud does not advertise the role reliably, configure the stable folder binding first with `mail folders role set trash …` or `mail folders role set junk …`. A valid explicit binding takes precedence over provider labels.

The custom-folder form requires a deliberately named ordinary folder:

```bash
icloud mail empty folder 'Temporary Import' --limit 50 --dry-run
```

It refuses Inbox, Sent, Drafts, Archive, Trash, Junk, or a folder currently bound to one of those semantic roles. Use the corresponding semantic workflow instead of smuggling a protected mailbox through the custom-folder form.

The dry run contacts no mail provider. It creates a short-lived local `pur_…` review scope containing only stable message/location IDs plus UIDVALIDITY, UID, and mod-sequence evidence. The output reports the reviewed count, exact folder, `more`, expiry, a content-independent purge hash, and the **apply request hash** required for execution. Trash/Junk reviews also report `retention_warning=provider_auto_delete_possible` because provider retention may remove old content independently of the CLI.

Already-`\Deleted` provider locations are not admitted to a new purge page. Pending or unresolved exact-delete intents are also excluded, so a new purge review cannot accidentally turn an ambiguous earlier provider outcome into an automatic retry. The review reports them separately as `blocked_unresolved`; a page with `count=0` and `blocked_unresolved>0` is **not** reported as an empty folder.

## Apply exactly the reviewed page

Copy the `pur_…` ID and apply hash from the matching dry run:

```bash
icloud mail empty trash \
  --purge-id pur_… \
  --request-hash <reviewed-hash> \
  --yes \
  --jsonl

icloud mail empty folder 'Temporary Import' \
  --purge-id pur_… \
  --request-hash <reviewed-hash> \
  --yes \
  --jsonl
```

Execution revalidates every frozen exact provider copy before the first new write. Changing the failure mode, changing the custom folder, rebinding Trash/Junk to another folder, expiry, account mismatch, or changed location evidence invalidates the reviewed request before new provider contact.

Each item receives its own deterministic idempotency identity derived from the purge review, apply hash, and stable `loc_…` ID. Replaying the same accepted page therefore returns its existing durable `mut_…` outcomes rather than issuing the exact delete again. A confirmed item is safe to reuse even though its old location is now absent.

An `unresolved` or still-pending exact delete is different: the provider may already have observed the STORE/UID-EXPUNGE sequence. The purge command reports that outcome and refuses to retry it automatically under the same review. Synchronize/reconcile provider state first, then create a new purge page. A provider failure recorded before any mutation was observed can re-enter a later freshly reviewed page with a new idempotency identity.

## Continue until no eligible or unresolved locations remain

One purge review is one immutable page. When the review or apply result says `more=true`, finish that page, then review the same scope again:

```bash
icloud mail empty folder 'Temporary Import' --limit 50 --dry-run
```

The next review enumerates the **current remaining exact locations**, not the original query result. Confirmed deletions from the previous page are gone, later eligible locations can now enter the page, and unresolved exact deletes remain excluded until reconciled. This makes stopping or cancellation between pages safe: already confirmed work stays truthful, while unreviewed later mail is untouched.

`more=false` means there was no additional eligible location behind that reviewed page. Treat the folder as clear only when a fresh review also reports `count=0`, `more=false`, and `blocked_unresolved=0`. If `blocked_unresolved` is non-zero, synchronize and reconcile those ambiguous durable mutation outcomes before reviewing again.

`--mode atomic` means validate the entire reviewed page before starting and then stop on the first provider/item failure. It does not claim rollback of earlier accepted deletes. `--mode continue-on-error` attempts independent remaining items and reports ordered per-item truth plus final requested/attempted/completed/failed/skipped/partial accounting.

## Why an unrelated `\Deleted` message is not swept away

Permanent deletion uses the existing exact location transport path. For the reviewed UID, the CLI verifies the selected mailbox UIDVALIDITY and current UID state, adds `\Deleted` to that exact UID, then uses **UID EXPUNGE for that exact UID set** when the provider supports the safe capability. It never follows the purge page with generic mailbox EXPUNGE.

That distinction matters when another message in the same mailbox was already marked `\Deleted` by some other client. A generic EXPUNGE could remove both. Exact UID EXPUNGE removes only the reviewed UID and leaves the unrelated pre-deleted message alone.

## Logical messages survive other provider copies

Purge removes provider **locations**, not the canonical logical message by identity. If the same logical message also has a current Inbox, Sent, Archive, or other-folder copy, deleting the reviewed location leaves that surviving copy and the canonical message/thread intact. The normal location-mutation reconciliation code recomputes aggregate message state after each confirmed exact deletion.

For one-message permanent delete, move/copy semantics, and mutation-status recovery, see [Move, copy, archive, and delete mail](/docs/manage-mail-locations). For query-derived immutable logical selections, see [Review and apply immutable mail selections](/docs/review-and-apply-mail-selections). For strict JSON/JSONL contracts, see [Output and automation](/docs/output-and-automation).
