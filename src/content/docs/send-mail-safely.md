---
title: Send mail safely
description: Preview and send a new iCloud Mail message with explicit confirmation, idempotency, BCC privacy, text or HTML bodies, and honest delivery outcomes.
order: 8
category: Guides
summary: Dry-run review, interactive or non-TTY confirmation, body inputs, recipient privacy, idempotent retries, stable results, and unknown-outcome recovery.
---

## Preview the exact request first

Start with `--dry-run`. It normalizes the account identity, addresses, subject, bodies, Reply-To, and custom headers without opening SMTP or creating an outbound attempt:

```bash
icloud mail send \
  --to 'Client <client@example.com>' \
  --subject 'Project update' \
  --body-file message.txt \
  --dry-run
```

The compact preview shows the configured account name and address used for `From`, each recipient list, the subject, body-part and attachment counts, the text body once, and an explicit safety summary. A dry run never sends mail.

Use the account selector when more than one profile is configured:

```bash
icloud --account Personal mail send \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt \
  --dry-run
```

## Send interactively or authorize automation

In a terminal, omit `--yes` to review the compact preview and answer the confirmation prompt:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt
```

A redirected or agent-run command does not prompt. It must use `--yes`:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt \
  --idempotency-key project-update-2026-08-06 \
  --yes
```

Use a stable idempotency key for automation. Repeating the same normalized request with the same key returns the original attempt without contacting SMTP again. Reusing that key after changing a recipient, subject, body, Reply-To, header, or account identity returns a conflict instead of guessing.

Successful compact output contains only the attempt outcome and stable local IDs:

```text
mail-send
send_… status=sent
message_id=msg_…
thread_id=thr_…
reconciliation=pending
reused=false
```

The submitted subject, body, recipients, BCC list, and custom headers are not echoed after a real send. The accepted message is immediately readable and searchable through its local `msg_…` and `thr_…` IDs.

## Choose one safe body source

Provide text inline, from a regular file, or from stdin:

```bash
icloud mail send --to client@example.com --subject Update --body 'Hello' --dry-run
icloud mail send --to client@example.com --subject Update --body-file message.txt --dry-run
printf '%s\n' 'Hello' | icloud mail send --to client@example.com --subject Update --body-file - --dry-run
```

HTML uses the matching `--html-body` or `--html-body-file` flags. Text and HTML together produce one multipart alternative message. HTML without an explicit text body receives a simple plain-text alternative.

Each body is bounded to 8 MiB before composition. One stdin stream cannot supply both text and HTML. A real interactive send cannot consume its body and confirmation from the same stdin; review with `--dry-run`, then rerun with `--yes`.

Filesystem attachments are not accepted by `mail send` in the current command set. The attachment count remains zero, and unsupported attachment input is never silently ignored.

## Review addresses and headers without exposing BCC on the wire

Repeat `--to`, `--cc`, or `--bcc`, or pass a comma-separated address list. Display names are preserved:

```bash
icloud mail send \
  --to 'Primary <primary@example.com>' \
  --cc 'Visible Copy <copy@example.com>' \
  --bcc 'Private Copy <hidden@example.com>' \
  --reply-to 'Replies <reply@example.com>' \
  --subject 'Project update' \
  --body-file message.txt \
  --dry-run --headers
```

The dry-run review includes BCC because you explicitly asked to inspect the outbound request. The composed RFC message never contains a `Bcc` header or BCC address; those addresses exist only in the SMTP envelope.

`--headers` shows normalized safe header fields and custom headers. Use `--format raw` during a dry run when you need the composed RFC message itself:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt \
  --header 'X-Workflow: client-update' \
  --dry-run --format raw > preview.eml
```

Custom headers cannot replace routing, identity, MIME, or delivery headers. Header names and values containing control characters, duplicate names, `Bcc`, `Received`, `Resent-*`, or other reserved fields are rejected before any network connection.

## Interpret the outcome before retrying

Every real send persists a pending attempt before the SMTP transaction. The final result is one of:

| Status | Meaning | Next action |
| --- | --- | --- |
| `sent` | iCloud SMTP returned final acceptance. Normally the local message and attempt commit together and return `msg_…` and `thr_…`. | Use the returned IDs. When an error reports `error_code=local_completion`, iCloud still accepted the message but the local projection is incomplete; do not resend it. |
| `failed` | The CLI proved the message was not accepted, such as authentication failure or an explicit SMTP rejection. | Fix the reported cause. A deliberate new attempt should use a new idempotency key. |
| `unknown` | The connection failed after message data may have reached iCloud, so acceptance could not be proved either way. | Do not retry automatically. Verify delivery first. |
| `pending` | An earlier process stopped before a final durable outcome was recorded. | Treat it as unresolved; do not create an automatic duplicate. |

An `unknown` error is always reported as non-retryable even when the underlying network failure was temporary. Run the exact same command with the same idempotency key to retrieve the original attempt without resending, then check the receiving mailbox or iCloud Sent mailbox before deciding whether a new attempt is appropriate.

A local-completion error is different: the final SMTP acceptance was proven, so the durable attempt remains `sent` and the same idempotency key cannot resubmit it. Preserve the attempt ID and let Sent reconciliation restore the missing local location/message evidence.

## Use JSON only when its privacy tradeoff is intentional

A real `--json` success contains the account ID, sanitized attempt fields, reuse state, schema version, and request ID. It omits recipients, BCC, subject, bodies, raw storage coordinates, and the idempotency key.

A dry-run `--json` response is different: it is an explicit review mode and therefore includes the normalized recipients, BCC, subject, bodies, Reply-To, and safe custom headers. Treat that output as message content and do not write it to broad logs.

## Common recovery

- `non-interactive mail send requires --yes`: review with `--dry-run`, then add `--yes` to the deliberate send.
- `mail body consumed stdin`: stdin cannot also answer an interactive prompt; rerun with `--yes` after review.
- `idempotency key was already used`: keep the original request or choose a new key for an intentionally different message.
- `mail send outcome is unknown`: preserve the attempt ID, do not auto-retry, and verify delivery externally.
- `SMTP accepted the message but local completion is incomplete`: the provider accepted it; preserve the `send_…` ID and do not resend.
- SMTP authentication or STARTTLS failure: run `icloud account test` and correct the account credential or network path before another deliberate attempt.
