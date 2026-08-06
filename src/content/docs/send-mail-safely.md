---
title: Send mail safely
description: Preview and send a new iCloud Mail message with explicit confirmation, idempotency, BCC privacy, streamed attachments, and honest delivery outcomes.
order: 8
category: Guides
summary: Dry-run review, confirmation, body and attachment inputs, recipient privacy, idempotency, Sent reconciliation, explicit retries, and unknown-outcome recovery.
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

## Attach reviewed files without buffering the whole message

Repeat `--attach` for regular files. A dry run reads each file, detects its media type, computes its SHA-256 hash, and shows the normalized filename, type, size, and hash before any network connection:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project files' \
  --body 'See attached.' \
  --attach report.pdf \
  --attach data.csv \
  --dry-run
```

Override untrusted or unhelpful local metadata by referring to the same attached path:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project files' \
  --body 'See attached.' \
  --attach ./build/output.bin \
  --attach-name './build/output.bin=report.bin' \
  --attach-type './build/output.bin=application/octet-stream' \
  --idempotency-key project-files-2026-08-06 \
  --yes
```

The real send rechecks the reviewed bytes before composing the message. A missing file, directory, symbolic or other special file, changed file, unreadable file, unsupported filename, invalid media type, cache failure, or size violation stops before SMTP. The composed RFC message is capped at 20 MiB, including MIME/base64 overhead; the CLI does not invoke Mail Drop.

Accepted attachment bytes are immediately available through the normal local message and attachment commands, even before a later Sent sync confirms the provider location:

```bash
icloud mail attachments list msg_…
icloud mail attachments save att_… --output report.pdf
```

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
| `sent` | iCloud SMTP returned final acceptance. Normally the local message and attempt commit together and return `msg_…` and `thr_…`. | Use the returned IDs. `reconciliation=pending` means the provider Sent location has not been observed yet; it does not make the send safe to repeat. |
| `failed` | The CLI proved the message was not accepted, such as authentication failure or an explicit SMTP rejection. | Fix the cause, preserve the original `send_…` ID, then create one linked retry with a new idempotency key. |
| `unknown` | The connection failed after message data may have reached iCloud, so acceptance could not be proved either way. | Reconcile Sent and check the recipient. Retry only after explicitly accepting duplicate risk. |
| `pending` | An earlier process stopped before a final durable outcome was recorded. | Treat it as unresolved; do not create an automatic duplicate. |

Inspect bounded payload-free status, then synchronize Sent and reconcile exact generated Message-ID evidence:

```bash
icloud mail status --outbound
icloud mail sync --reconcile-outbound
icloud mail status --outbound --json
```

A unique Sent observation confirms the attempt and associates the provider location with the existing local message. Similar subjects, bodies, times, or attachments are never enough. No match remains pending/unknown, and conflicting exact evidence remains visibly ambiguous.

An `unknown` error is always reported as non-retryable even when the underlying network failure was temporary. Run the exact same command with the same idempotency key to retrieve the original attempt without resending, then reconcile Sent and check the recipient.

After a proven failed attempt, repeat the same normalized message with a new key and link it explicitly:

```bash
icloud mail send ... \
  --retry-of send_… \
  --idempotency-key update-retry-02 \
  --yes
```

An unresolved `unknown` attempt requires the additional `--retry-unknown` acknowledgement. This is deliberately noisy because both copies may be delivered:

```bash
icloud mail send ... \
  --retry-of send_… \
  --retry-unknown \
  --idempotency-key update-duplicate-risk-02 \
  --yes
```

A local-completion error is different: the final SMTP acceptance was proven, so the durable attempt remains `sent` and the same idempotency key cannot resubmit it. Preserve the attempt ID and let Sent reconciliation restore the missing local location/message evidence.

## Use JSON only when its privacy tradeoff is intentional

A real `--json` success contains the account ID, sanitized attempt fields, reuse state, schema version, and request ID. It omits recipients, BCC, subject, bodies, raw storage coordinates, and the idempotency key.

A dry-run `--json` response is different: it is an explicit review mode and therefore includes the normalized recipients, BCC, subject, bodies, Reply-To, and safe custom headers. Treat that output as message content and do not write it to broad logs.

## Common recovery

- `non-interactive mail send requires --yes`: review with `--dry-run`, then add `--yes` to the deliberate send.
- `mail body consumed stdin`: stdin cannot also answer an interactive prompt; rerun with `--yes` after review.
- `idempotency key was already used`: keep the original request or choose a new key for an intentionally different message.
- `mail send outcome is unknown`: preserve the attempt ID, run `mail sync --reconcile-outbound`, inspect `mail status --outbound`, and verify the recipient before considering `--retry-unknown`.
- `SMTP accepted the message but local completion is incomplete`: the provider accepted it; preserve the `send_…` ID and do not resend.
- `SMTP accepted the message but its Sent copy could not be confirmed`: the message was accepted; do not resend. Refresh folder discovery if needed, then synchronize and reconcile Sent.
- `composed message exceeds iCloud Mail's 20 MiB message limit`: reduce attachment/body size; MIME encoding overhead counts toward the limit.
- SMTP authentication or STARTTLS failure: run `icloud account test` and correct the account credential or network path before another deliberate attempt.
