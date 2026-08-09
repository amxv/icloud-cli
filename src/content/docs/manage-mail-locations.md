---
title: Move, copy, archive, and delete mail
description: Change one exact synchronized provider location safely, recover from partial provider outcomes, and preserve the logical message when other copies remain.
order: 9
category: Guides
summary: Exact source and destination selection, dry-run review, MOVE and safe fallback behavior, durable mutation status, archive/trash shortcuts, restore, and permanent deletion.
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

## Use archive and trash shortcuts only when roles are discovered

Convenience commands resolve current special-use roles from folder discovery rather than guessing English mailbox names:

```bash
icloud mail archive msg_… --dry-run
icloud mail trash msg_… --dry-run
```

`archive` moves one eligible non-Sent location to the discovered Archive folder. It never moves a Sent copy. `trash` moves one eligible location to the discovered Trash folder. If several eligible locations exist, add `--from` rather than letting the CLI guess:

```bash
icloud mail archive msg_… --from inbox --idempotency-key archive-01 --yes
icloud mail trash msg_… --from Projects --idempotency-key trash-01 --yes
```

When iCloud does not advertise an Archive or Trash role, the shortcut fails honestly. Use `mail folders list --verbose` to inspect current provider metadata and use explicit `move --from … --to …` only when you know the intended folder.

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
icloud mail status --mutations
icloud mail status --mutations --json
icloud mail status --mutations --limit 100 --cursor 'mcur_…' --json
```

Operation history is newest-first. If the page reports `more=true`, continue with its opaque `next_cursor`; cursors are bound to the selected account and mutation-status inventory, and the final page omits the cursor.

Then synchronize the exact source and destination folders:

```bash
icloud mail sync --folder Projects
icloud mail sync --folder Archive
icloud mail message show msg_… --metadata-only
```

Do not issue the provider mutation again automatically. Repeating the same request with the same idempotency key returns the existing `mut_…` record without contacting iCloud, even when the original source is now absent.

`failed` means the provider write did not begin or no provider-side change was observed. Fix the reported authentication, configuration, stale UIDVALIDITY, or location conflict and issue a new deliberate request. `unresolved` means a provider change may exist and requires synchronization rather than retry.

## Keep location state and logical identity separate

Move, copy, archive, trash, restore, and delete change provider locations only. They do not rewrite stable message, MIME-part, attachment, blob, search, or thread IDs. A copy can add another independently flagged location; a move can replace one location; a permanent delete can make one location absent. Message unread and flagged summaries are recomputed from every eligible current location after each confirmed or partial outcome.

Read [Sync and read mail](/docs/sync-and-read-inbox) for folder synchronization and reconciliation, [Folder discovery and sync policy](/docs/folder-discovery-and-sync-policy) for role metadata, and [Troubleshooting](/docs/troubleshooting) for stale or unresolved operations.
