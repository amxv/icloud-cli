---
title: "Drafts"
description: "Create and edit drafts locally, sync them to iCloud when you choose, then preview and send the saved revision."
order: 16
category: Mail
summary: "Compose incrementally with stable `drf_…` draft IDs instead of sending immediately."
---

Drafts are useful when a message needs more than one editing step or an agent should prepare mail for later review.

## Create a draft

```bash
icloud mail draft create \
  --to client@example.com \
  --subject 'Project update' \
  --body-file draft.txt
```

Draft creation is local. The returned `drf_…` ID stays with the draft as you revise it.

A draft can be incomplete:

```bash
icloud mail draft create --subject 'Idea' --body 'Notes so far'
```

## List and read drafts

```bash
icloud mail draft list
icloud mail draft show drf_…
```

Ask for the full body only when needed:

```bash
icloud mail draft show drf_… --full
```

## Update a draft

```bash
icloud mail draft update drf_… \
  --to client@example.com \
  --subject 'Project update — revised' \
  --body-file revised.txt
```

You can use the same compose options as new mail, including attachments, sender identities, signatures, and S/MIME intent.

## Make the draft available in iCloud Mail

```bash
icloud mail draft sync drf_…
```

To synchronize the provider Drafts area generally:

```bash
icloud mail draft sync
```

If your account does not expose a recognizable Drafts role, choose the discovered folder explicitly:

```bash
icloud mail draft sync drf_… --folder Drafts
```

See [Folders](/docs/folders) if Drafts is not resolved automatically.

## Preview and send

```bash
icloud mail draft send drf_… --dry-run
```

Then:

```bash
icloud mail draft send drf_… --yes
```

The send uses the same outbound safeguards as `icloud mail send`. If a send becomes uncertain, keep the draft and follow [Outbox and send recovery](/docs/outbox-and-send-recovery) rather than assuming it is safe to resend.

## Delete a draft without sending

Review:

```bash
icloud mail draft delete drf_… --dry-run
```

Then delete:

```bash
icloud mail draft delete drf_… --yes
```

## Agent workflow

A useful pattern is:

```text
create → show/review → update → sync if desired → send --dry-run → send
```

This gives an agent a stable object to work on without turning every compose step into an outbound action.
