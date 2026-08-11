---
title: "Agent guide"
description: "A practical operating guide for agents using icloud Mail: discover capabilities, keep reads small, review writes, and recover without guessing."
order: 40
category: Agents & automation
summary: "The conventions an agent should follow to use the CLI accurately, safely, and with minimal output."
---

`icloud` is designed so an agent can work with Mail through normal commands instead of knowing how iCloud works underneath them.

## Start by discovering the installed surface

Do not assume a feature exists because another version had it:

```bash
icloud --help
icloud capabilities --json
```

For a command you plan to use:

```bash
icloud mail search --help
```

`capabilities --json` also publishes the structured-input and output schemas supported by the installed build.

## Prefer compact text for exploration

Normal output is intentionally concise:

```bash
icloud mail messages list
icloud mail search invoice
icloud mail threads list
```

This is usually the best mode when an agent is deciding what to do next. Switch to JSON when a programmatic parser needs exact fields rather than reflexively requesting JSON for every read.

## Synchronize before assuming the local view is fresh

A reliable sequence is:

```bash
icloud mail sync
icloud mail search invoice
```

If the task specifically asks for the freshest possible provider search, use:

```bash
icloud mail search invoice --remote
```

A remote `rem_…` result is a discovery reference. Cache it before using it as a durable local object:

```bash
icloud mail message show rem_… --remote --cache --full
```

## Expand content only when needed

List and search commands intentionally avoid dumping complete mail bodies. Follow stable IDs to the exact content you need:

```bash
icloud mail message show msg_…
icloud mail message show msg_… --full
icloud mail attachments list msg_…
```

For conversations:

```bash
icloud mail threads show thr_…
icloud mail threads show thr_… --full
```

Use raw MIME or binary attachment output only when the task genuinely needs exact bytes.

## Use stable IDs between commands

Prefer returned IDs over re-identifying mail by mutable display text:

```text
acct_…  account
msg_…   message
thr_…   conversation
att_…   attachment
drf_…   draft
sel_…   reviewed selection
send_…  outbound attempt
```

A common agent flow is:

```text
search → inspect msg_… → inspect attachments → dry-run action → apply action
```

## Review outbound and destructive actions

For send/reply/forward and location-changing actions, use `--dry-run` first:

```bash
icloud mail reply msg_… --body-file reply.txt --dry-run
icloud mail archive msg_… --from inbox --dry-run
```

Only after reviewing the resolved action should a non-interactive agent use `--yes`.

For sends and location changes that support it, provide a stable idempotency key:

```bash
icloud mail reply msg_… \
  --body-file reply.txt \
  --idempotency-key customer-reply-01 \
  --yes
```

Never invent a new key just because the command returned a network error. Inspect the recorded operation first.

## Freeze search results before bulk writes

Do not run a search, wait, rerun it, and assume the membership is unchanged. Use a short-lived selection:

```bash
icloud mail selection create \
  --search \
  --unread \
  --folder inbox \
  --scope-folder inbox \
  --limit 50
```

Then review and apply exactly `sel_…`:

```bash
icloud mail selection apply sel_… --action archive --dry-run
```

Use the returned review hash for the real apply. See [Reviewed bulk actions](/docs/reviewed-bulk-actions).

## Avoid accidental prompts

Agent invocations should normally include:

```bash
--no-input
```

For example:

```bash
icloud --no-input --account Personal mail search invoice
```

If setup needs a credential, provide one of the explicit non-interactive sources from [Accounts and credentials](/docs/accounts-and-credentials).

## Use request IDs for multi-step jobs

```bash
icloud --request-id ticket-482 mail search invoice --json
```

A request ID makes logs easier to correlate without changing the Mail operation itself.

## Treat `retryable` as guidance, not permission to duplicate a write

Structured errors include a code and retryability signal. A retryable connectivity failure can still occur around an outbound operation whose final provider state is uncertain.

For sends, keep the `send_…` ID and follow [Outbox and send recovery](/docs/outbox-and-send-recovery). For moves/deletes, inspect mutation history and current mail state before repeating a different request.

## Prefer one clear command over shell scraping

When a task needs several filters or structured input, use `--input-json` rather than building brittle quoting:

```json
{
  "query": "invoice",
  "folder": "inbox",
  "unread": true,
  "limit": 20
}
```

```bash
icloud mail search --input-json search.json --json
```

See [Output and automation](/docs/output-and-automation) and [Automation recipes](/docs/automation-recipes).
