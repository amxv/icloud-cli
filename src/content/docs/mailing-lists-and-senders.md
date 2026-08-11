---
title: "Mailing lists and sender preferences"
description: "Unsubscribe from supported mailing lists and mark exact sender addresses as local VIPs or blocked for easier triage."
order: 24
category: Mail
summary: "Keep inbox triage useful without turning local preferences into hidden provider-side actions."
---

## Unsubscribe from a mailing list

When a synchronized message advertises a supported unsubscribe method, review it first:

```bash
icloud mail unsubscribe msg_… --dry-run
```

Then execute the reviewed method:

```bash
icloud mail unsubscribe msg_… --yes
```

If the preview offers more than one safe choice, select the method ID it returned:

```bash
icloud mail unsubscribe msg_… \
  --method mailto:2 \
  --yes
```

The CLI supports standards-based unsubscribe flows rather than opening arbitrary message links on your behalf.

## Mark a sender as VIP

```bash
icloud mail senders vip teammate@example.com
```

Use the preference in local filters:

```bash
icloud mail search --vip
icloud mail messages list --vip
```

Clear it with:

```bash
icloud mail senders unvip teammate@example.com
```

## Mark a sender as blocked

```bash
icloud mail senders block noisy-list@example.com
```

Then filter locally:

```bash
icloud mail search --blocked
```

Clear the preference:

```bash
icloud mail senders unblock noisy-list@example.com
```

## Inspect sender preferences

```bash
icloud mail senders list
icloud mail senders show teammate@example.com
```

VIP and blocked are local CLI preferences. They do not create Apple Mail rules, change Contacts, or automatically move/delete mail. They simply add useful context and filters for you or an agent.
