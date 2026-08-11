---
title: "Automation recipes"
description: "Copyable patterns for agents and scripts that search, read, sync, draft, send, and organize iCloud Mail without hidden prompts."
order: 42
category: Agents & automation
summary: "Small end-to-end examples built from the CLI's normal commands and safety controls."
---

These recipes assume the account is already configured. Add `--account <name>` when you have more than one account.

## Sync, then list unread mail

```bash
icloud --no-input mail sync
icloud --no-input mail messages list --unread --limit 20 --json
```

## Find unread invoices

```bash
icloud --no-input mail search invoice \
  --folder inbox \
  --unread \
  --json
```

## Get the full text of one chosen message

```bash
icloud --no-input mail message show msg_… \
  --full \
  --body-only
```

This keeps the earlier search small and expands only the message the workflow actually needs.

## Save every attachment from a message

```bash
icloud --no-input mail attachments list msg_… --json
icloud --no-input mail attachments save-all msg_… --dir ./downloads --json
```

## Prepare a draft for human review

```bash
icloud --no-input mail draft create \
  --to client@example.com \
  --subject 'Project update' \
  --body-file response.txt \
  --json
```

Inspect it later:

```bash
icloud --no-input mail draft show drf_… --full
```

## Preview a reply without sending

```bash
icloud --no-input mail reply msg_… \
  --body-file response.txt \
  --dry-run \
  --json
```

The dry run is appropriate even when the agent is authorized to send; it lets the workflow inspect recipients and composed content before the outbound side effect.

## Send an already reviewed reply

```bash
icloud --no-input mail reply msg_… \
  --body-file response.txt \
  --idempotency-key ticket-482-reply-01 \
  --yes \
  --json
```

Persist the idempotency key with the job so a process restart does not invent a duplicate request.

## Archive a reviewed search page

Create a frozen selection:

```bash
icloud --no-input mail selection create \
  --search \
  --query newsletter \
  --folder inbox \
  --scope-folder inbox \
  --limit 50 \
  --json
```

Review the exact action:

```bash
icloud --no-input mail selection apply sel_… \
  --action archive \
  --dry-run \
  --json
```

Then use the returned `request_hash`:

```bash
icloud --no-input mail selection apply sel_… \
  --action archive \
  --request-hash <reviewed-hash> \
  --yes \
  --jsonl
```

## Keep a process informed about new mail

```bash
icloud --no-input mail watch --jsonl
```

Consume one JSON object per line. The process can stop the watcher normally when its own job ends.

## Search iCloud directly, then cache one result

```bash
icloud --no-input mail search invoice --remote --folder inbox --json
```

For a chosen `rem_…` result:

```bash
icloud --no-input mail message show rem_… \
  --remote \
  --cache \
  --full \
  --json
```

The cached result now has normal local message identity for later steps.

## Use a request file for shell-sensitive input

`search.json`:

```json
{
  "query": "Q3 contract",
  "participant": "legal@example.com",
  "unread": true,
  "limit": 20
}
```

```bash
icloud --no-input --request-id legal-review-07 \
  mail search --input-json search.json --json
```

## Recover from a send error

Do not immediately rerun a fresh send. Keep the returned `send_…` ID:

```bash
icloud --no-input mail operations outbound show send_… --json
icloud --no-input mail sync --reconcile-outbound --json
```

If the item appears in the safe-retry Outbox, review and retry that exact `out_…` item. If the send is still `unknown`, follow [Outbox and send recovery](/docs/outbox-and-recovery) rather than guessing.
