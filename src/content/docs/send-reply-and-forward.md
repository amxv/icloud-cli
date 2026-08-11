---
title: "Send, reply, and forward"
description: "Compose new mail, reply to an exact message, forward selected content, and use dry runs and idempotency for safer sending."
order: 15
category: Mail
summary: "Everyday outbound Mail: preview first, send deliberately, and avoid accidental duplicate sends."
---

`icloud` uses the same compose model for new messages, replies, forwards, and drafts. You can provide text, HTML, files, CC/BCC recipients, a sender identity, a signature, and optional S/MIME protection.

## Send a new message

Start with a preview:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project update' \
  --body-file update.txt \
  --dry-run
```

Then send the reviewed request:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project update' \
  --body-file update.txt \
  --idempotency-key project-update-01 \
  --yes
```

In an interactive terminal you may omit `--yes` and answer the confirmation prompt. Scripts and agents should use `--dry-run` for review and `--yes` only when a real send is intended.

## Why use an idempotency key?

An idempotency key makes retries much safer. Repeating the same normalized send with the same key returns the original operation rather than submitting another copy.

```bash
--idempotency-key project-update-01
```

If you change the message, use a new key. The CLI refuses to silently reuse one key for different content.

## Provide the message body

Inline text:

```bash
icloud mail send \
  --to client@example.com \
  --subject Hello \
  --body 'Thanks for the update.' \
  --dry-run
```

From a file:

```bash
icloud mail send \
  --to client@example.com \
  --subject Hello \
  --body-file message.txt \
  --dry-run
```

From stdin:

```bash
printf '%s\n' 'Thanks for the update.' | \
  icloud mail send \
    --to client@example.com \
    --subject Hello \
    --body-file - \
    --dry-run
```

HTML uses `--html-body` or `--html-body-file`. You can provide both text and HTML when you want a multipart message.

## Add recipients

Repeat recipient flags or provide comma-separated addresses:

```bash
icloud mail send \
  --to primary@example.com \
  --cc reviewer@example.com \
  --bcc private-copy@example.com \
  --reply-to replies@example.com \
  --subject 'Project update' \
  --body-file update.txt \
  --dry-run
```

BCC is shown in an explicit dry-run review, but is not added as a normal Bcc header to the delivered message.

## Attach files

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project files' \
  --body 'See attached.' \
  --attach report.pdf \
  --attach data.csv \
  --dry-run
```

The preview inspects the selected files before a real send. The files are checked again when you actually send so a changed attachment is not silently substituted after review.

For inline images and attachment metadata overrides, see the exact flags in [`icloud mail send`](/docs/command-reference#icloud-mail-send).

## Choose a sender identity

If you have configured aliases or custom-domain addresses:

```bash
icloud mail send \
  --from alias@icloud.com \
  --to client@example.com \
  --subject 'Project update' \
  --body-file update.txt \
  --dry-run
```

See [Sender identities](/docs/sender-identities).

## Use a signature

Once signatures are configured, normal compose commands can use their defaults automatically. Override one message with:

```bash
icloud mail send ... --signature sig_… --dry-run
```

Or suppress the configured signature:

```bash
icloud mail send ... --signature none --dry-run
```

See [Signatures](/docs/signatures).

## Reply to an exact message

Start from a local `msg_…` ID:

```bash
icloud mail reply msg_… --body-file reply.txt --dry-run
```

Then send:

```bash
icloud mail reply msg_… \
  --body-file reply.txt \
  --idempotency-key client-reply-01 \
  --yes
```

If the same logical message has copies in several folders, use `--folder` when the CLI asks you to choose the exact copy:

```bash
icloud mail reply msg_… \
  --folder inbox \
  --body-file reply.txt \
  --dry-run
```

## Reply all

```bash
icloud mail reply-all msg_… \
  --body 'Thanks, everyone.' \
  --dry-run
```

Review the resolved recipient list before sending. The command avoids re-adding your configured sender identities as recipients.

## Forward a message

```bash
icloud mail forward msg_… \
  --to teammate@example.com \
  --body 'For context:' \
  --dry-run
```

The normal forward includes a readable quoted representation. If you need the original message as an exact EML attachment:

```bash
icloud mail forward msg_… \
  --to teammate@example.com \
  --original eml \
  --dry-run
```

Original attachments are opt-in. List them first, then choose the ones to include:

```bash
icloud mail attachments list msg_…

icloud mail forward msg_… \
  --to teammate@example.com \
  --include-attachment att_… \
  --dry-run
```

Use ordinary `--attach` for new local files you want to add to the forward.

## Sign or encrypt with S/MIME

If S/MIME is configured:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Protected update' \
  --body-file update.txt \
  --smime sign-encrypt \
  --dry-run
```

See [S/MIME](/docs/s-mime) for certificate and private-key setup.

## If the send result is uncertain

Do not immediately create a new send. Keep the returned `send_…` ID and inspect the outbound operation:

```bash
icloud mail operations outbound show send_…
icloud mail sync --reconcile-outbound
```

A connection failure can occur after iCloud may already have accepted the message. [Outbox and send recovery](/docs/outbox-and-send-recovery) explains the difference between a safe retry and a duplicate-risk retry.

## Draft first instead

For mail you want to revise before sending, use [Drafts](/docs/drafts).
