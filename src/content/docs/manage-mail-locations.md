---
title: Move, copy, archive, and delete mail
description: Change exact synchronized provider locations safely, run bounded reviewed batches, recover from partial provider outcomes, and preserve logical messages when other copies remain.
order: 9
category: Guides
summary: Exact source and destination selection, bounded batch review, truthful partial results, MOVE and safe fallback behavior, durable mutation status, archive/trash shortcuts, restore, and permanent deletion.
---

## Start from current synchronized locations

Location-changing commands act on one exact provider copy, not every copy of a logical message and never an entire reconstructed thread. Inspect the message first:

```bash
icloud mail message show msg_… --metadata-only
icloud mail folders list
```

The message may have independent Inbox, Sent, Archive, Trash, or ordinary-folder locations. Each location keeps its own stable `loc_…` ID, folder, UID validity, UID, flags, and presence. Folder selectors accept a stable folder ID, decoded name, exact wire name, or an unambiguous discovered kind such as `inbox`.

Use a dry run before every new operation:

```bash
icloud mail move msg_… --from inbox --to Projects --dry-run
```

The preview resolves the exact source location and destination folder from current local state. It proves that IMAP was not contacted, no durable mutation intent was created, and no logical message was deleted.

## Move or copy one provider location

Both general commands require explicit source and destination folders:

```bash
icloud mail move msg_… \
  --from inbox \
  --to Projects \
  --idempotency-key project-move-01 \
  --yes

icloud mail copy msg_… \
  --from inbox \
  --to Receipts \
  --idempotency-key receipt-copy-01 \
  --yes
```

`move` prefers the provider's atomic UID MOVE capability. When MOVE is unavailable, the CLI copies first and removes only the exact source UID through safe UID EXPUNGE. It never calls mailbox-wide EXPUNGE. If safe exact removal is unavailable, the source stays present and the mutation becomes `unresolved` instead of pretending the move completed.

`copy` keeps the source location. When iCloud returns a destination UID mapping, the CLI creates or updates the exact local destination location immediately. When the provider omits mapping evidence, the copy remains visible as `unresolved` until synchronization observes the destination.

A confirmed move reports the stable mutation, message, source and destination location/folder IDs, provider method, UID mapping, aggregate unread/flagged state, and next action:

```text
mail-location
mut_… action=move
message_id=msg_…
source_location_id=loc_…
source_folder_id=fld_…
destination_location_id=loc_…
destination_folder_id=fld_…
destination_uid_validity=…
destination_uid=…
status=confirmed
provider_method=move
source_present=false
mapping_known=true
reconciliation_required=false
unread=false
flagged=false
reused=false
next=none
```

The compact and JSON outcomes omit subject, participants, body, attachment names, raw storage coordinates, credentials, and submitted folder names.

## Change up to 100 exact message targets in one bounded batch

Every location command accepts one to 100 direct `msg_…` targets. Positional targets share the command's folder selectors:

```bash
icloud mail move msg_… msg_… msg_… \
  --from inbox \
  --to Projects \
  --idempotency-key project-move-batch-01 \
  --mode atomic \
  --yes
```

Before the first provider write, the CLI resolves and validates the complete bounded target set against current local state. A bad message ID, ambiguous source, missing semantic role, stale folder, changed idempotent request, or unsafe source therefore stops the batch without contacting iCloud. `--dry-run` performs the same whole-set review and creates no mutation intents.

After validation, provider writes remain deliberately sequential. The default `--mode atomic` means **validate the whole batch, then fail fast**: execution stops on the first target/provider failure, but any earlier provider writes remain committed and are reported truthfully. It never claims that IMAP rolled them back. Use `--mode continue-on-error` when independent later targets should still be attempted after a failure.

For ordered automation results, request JSONL:

```bash
icloud mail archive msg_… msg_… msg_… \
  --from inbox \
  --mode continue-on-error \
  --idempotency-key archive-batch-01 \
  --yes \
  --jsonl
```

The stream emits one `item` event for each attempted target in input order, followed by one `completed` event with `requested`, `attempted`, `completed`, `failed`, `skipped`, and `partial`. A failed batch exits non-zero even when earlier or later items succeeded.

Strict JSON input can use one `message_id`, a `message_ids` array, or an `items` array. Choose exactly one target form. Global `from`, `to`, and `idempotency_key` values act as defaults; an `items` entry can override them when exact copies need different folders or keys:

```json
{
  "items": [
    {"message_id": "msg_…", "from": "inbox", "to": "Projects"},
    {"message_id": "msg_…", "from": "Receipts", "to": "Archive"}
  ],
  "idempotency_key": "location-batch-01"
}
```

For streaming input, each `--input-jsonl` line is one strict object with `message_id` and optional `from`, `to`, and `idempotency_key`. Command-line `--from`, `--to`, and `--idempotency-key` remain defaults for fields omitted from each line.

When one idempotency key is supplied for a multi-message request, the CLI deterministically derives a separate private per-item key. Replaying the same reviewed batch therefore returns each existing durable `mut_…` outcome without issuing the provider write again. Supplying an explicit item key overrides the batch default for that item. One-message commands keep their existing output and idempotency behavior unchanged.

## Use archive and trash shortcuts with resolved semantic roles

Convenience commands resolve the account's semantic role rather than guessing English mailbox names:

```bash
icloud mail archive msg_… --dry-run
icloud mail trash msg_… --dry-run
```

`archive` moves one eligible non-Sent location to the discovered Archive folder. It never moves a Sent copy. `trash` moves one eligible location to the discovered Trash folder. If several eligible locations exist, add `--from` rather than letting the CLI guess:

```bash
icloud mail archive msg_… --from inbox --idempotency-key archive-01 --yes
icloud mail trash msg_… --from Projects --idempotency-key trash-01 --yes
```

When iCloud does not advertise an Archive or Trash role, you can keep provider metadata truthful and explicitly bind a current stable folder ID:

```bash
icloud mail folders role set archive fld_… --dry-run
icloud mail folders role set archive fld_…
```

The folder can remain `kind=other`; the binding is separate local intent. If no binding or trustworthy provider role exists, the shortcut still fails honestly. If an explicit binding later becomes stale or missing, the shortcut stops and asks you to clear or rebind it instead of silently choosing a different folder.

## Move one exact message to Junk or back to Inbox

Junk handling is a location move, not a local classifier. The CLI does not score spam, infer suspicious senders, add mail rules, or create its own category state. It resolves the semantic Junk folder from a valid local role binding or trustworthy provider metadata and leaves mailbox classification to iCloud.

Preview the exact source and destination first:

```bash
icloud mail junk msg_… --dry-run
icloud mail not-junk msg_… --dry-run
```

Then apply the reviewed move:

```bash
icloud mail junk msg_… --idempotency-key junk-2026-08-10-01 --yes
icloud mail not-junk msg_… --idempotency-key not-junk-2026-08-10-01 --yes
```

`mail junk` moves one eligible current source location to the semantic Junk folder. It never moves a Sent location. If the same logical message has more than one eligible source, select the exact copy with `--from <folder>`.

`mail not-junk` requires the source location to be the current semantic Junk folder and moves that exact provider copy to Inbox. An explicit `--from` cannot bypass the Junk-source requirement.

Both commands use the same durable location-mutation journal, UIDVALIDITY checks, idempotency rules, provider MOVE support, and safe COPY plus UID-scoped EXPUNGE fallback as the other location operations. They never use mailbox-wide EXPUNGE. If iCloud accepts a provider mutation but the final mapping is uncertain, the normal unresolved/reconciliation state is preserved instead of guessing success.

If iCloud does not advertise a Junk role but you deliberately use a current selectable folder for Junk, bind its stable ID without changing its provider metadata:

```bash
icloud mail folders role set junk fld_… --dry-run
icloud mail folders role set junk fld_…
```

Clear the binding to return to provider role discovery.

## Unarchive and restore deliberately

`unarchive` accepts only a current Archive source and moves it to the discovered Inbox:

```bash
icloud mail unarchive msg_… --idempotency-key unarchive-01 --yes
```

`restore` accepts only a current Trash source and requires the destination explicitly:

```bash
icloud mail restore msg_… --to inbox --idempotency-key restore-01 --yes
```

The explicit destination prevents the CLI from inventing a previous folder when no reliable provider evidence exists.

## Permanently delete one exact location

Permanent deletion always requires `--from` and non-interactive `--yes`:

```bash
icloud mail delete msg_… --from trash --dry-run
icloud mail delete msg_… --from trash --idempotency-key delete-trash-01 --yes
```

The provider operation targets one exact UID and uses safe UID EXPUNGE. It never runs mailbox-wide EXPUNGE. Deleting one location does not delete the logical `msg_…`, decoded content, attachments, search record, or reconstructed thread while another provider location remains. The deleted provider location is retained locally as absent evidence so later synchronization can converge truthfully.

## Recover from unresolved provider state

A provider may accept COPY, MOVE, STORE, or EXPUNGE and then disconnect before the CLI receives final confirmation. The mutation is recorded before the network write, and any observed or ambiguous provider change becomes `unresolved` with `reconciliation_required=true`.

Inspect recent operations:

```bash
icloud mail operations mutations list
icloud mail operations mutations list --json
icloud mail operations mutations list --limit 100 --cursor 'mcur_…' --json
icloud mail operations mutations show mut_… --json
```

Operation history is newest-first. If the page reports `more=true`, continue with its opaque `next_cursor`; cursors are bound to the selected account and mutation-status inventory, and the final page omits the cursor.
Use `show mut_…` to inspect any exact stable mutation directly, including one reached on an older page. `icloud mail status --mutations` remains a compatibility alias for `mail operations mutations list`.

Then synchronize the exact source and destination folders:

```bash
icloud mail sync --folder Projects
icloud mail sync --folder Archive
icloud mail message show msg_… --metadata-only
```

Do not issue the provider mutation again automatically. Repeating the same request with the same idempotency key returns the existing `mut_…` record without contacting iCloud, even when the original source is now absent.

`failed` means the provider write did not begin or no provider-side change was observed. Fix the reported authentication, configuration, stale UIDVALIDITY, or location conflict and issue a new deliberate request. `unresolved` means a provider change may exist and requires synchronization rather than retry.

## Keep location state and logical identity separate

Move, copy, archive, trash, Junk/Not Junk, restore, and delete change provider locations only. They do not rewrite stable message, MIME-part, attachment, blob, search, or thread IDs. A copy can add another independently flagged location; a move can replace one location; a permanent delete can make one location absent. Message unread and flagged summaries are recomputed from every eligible current location after each confirmed or partial outcome.

Read [Sync and read mail](/docs/sync-and-read-inbox) for folder synchronization and reconciliation, [Folder discovery and sync policy](/docs/folder-discovery-and-sync-policy) for role metadata, and [Troubleshooting](/docs/troubleshooting) for stale or unresolved operations.
