---
title: "Outbox and send recovery"
description: "Understand sent, failed, and uncertain outbound results, safely retry the narrow set of sends that are proven retryable, and avoid duplicates."
order: 26
category: Mail
summary: "When a send goes wrong, inspect the recorded attempt before deciding whether another transmission is safe."
---

Most sends are simple: iCloud accepts the message and the command returns `status=sent`. Recovery matters when the connection fails around the moment a message may have been submitted.

## Inspect outbound attempts

```bash
icloud mail operations outbound list
icloud mail operations outbound show send_…
```

The useful outcomes are:

- **sent** — iCloud accepted the message. Do not resend merely because its Sent copy has not appeared locally yet.
- **failed** — the attempt is known not to have succeeded.
- **unknown** — the CLI cannot prove whether iCloud accepted the message. Treat this as duplicate risk.

## Reconcile with Sent

```bash
icloud mail sync --reconcile-outbound
```

Then inspect the same attempt again:

```bash
icloud mail operations outbound show send_…
```

This is the first thing to try for an unknown or not-yet-confirmed Sent result.

## Use the Outbox for proven safe retries

The Outbox contains only the narrow class of failed sends where the CLI knows the message did not reach the risky message-data stage.

```bash
icloud mail outbox list
icloud mail outbox show out_…
```

Review a retry:

```bash
icloud mail outbox retry out_… --dry-run
```

Then explicitly replay it:

```bash
icloud mail outbox retry out_… --yes
```

Cancel a retryable item without sending it:

```bash
icloud mail outbox cancel out_…
```

The Outbox is not a scheduled-send queue and does not retry messages in the background.

## An unknown send is different

If an attempt is `unknown`, do **not** create a fresh send immediately. The recipient may already have received it.

1. Keep the `send_…` ID.
2. Run `icloud mail sync --reconcile-outbound`.
3. Check the recipient or Sent mailbox if appropriate.
4. Only then decide whether duplicate risk is acceptable.

If you deliberately retry an unresolved unknown attempt, use the explicit linked-retry controls documented by `icloud mail send --help`, including `--retry-of`, `--retry-unknown`, a new idempotency key, and `--yes`.

## Reply Answered-state recovery

A reply can be successfully sent even if updating the original message's Answered state is still pending. Repair only that state without resending the reply:

```bash
icloud mail operations outbound reconcile-answer send_…
```

## General rule

When a write result is uncertain, **inspect and reconcile before repeating it**. Stable operation IDs and idempotency exist so an agent does not need to guess from an error string alone.
