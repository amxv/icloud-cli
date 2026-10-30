---
title: "Signatures"
description: "Create reusable text or HTML signatures and choose defaults for new mail, replies, reply-all, and forwards."
order: 18
category: Mail
summary: "Configure signatures once, then let normal compose commands apply them automatically."
---

## Create a signature

Text only:

```bash
icloud mail signatures create --text-file signature.txt
```

Text and HTML:

```bash
icloud mail signatures create \
  --text-file signature.txt \
  --html-file signature.html
```

To bind a signature to one sender identity:

```bash
icloud mail signatures create \
  --identity mid_… \
  --text-file signature.txt
```

The command returns a `sig_…` ID.

## List and inspect signatures

```bash
icloud mail signatures list
icloud mail signatures show sig_…
```

The list stays concise; `show` is the explicit content view.

## Set defaults

Choose separate defaults for different compose actions:

```bash
icloud mail signatures default new sig_…
icloud mail signatures default reply sig_…
icloud mail signatures default reply-all sig_…
icloud mail signatures default forward sig_…
```

Identity-specific defaults are supported too:

```bash
icloud mail signatures default reply sig_… --identity mid_…
```

Disable a default explicitly with `none`:

```bash
icloud mail signatures default reply-all none
```

## Override one message

Use a particular signature:

```bash
icloud mail send ... --signature sig_… --dry-run
```

Or suppress signatures for one compose:

```bash
icloud mail send ... --signature none --dry-run
```

This override also works with replies, forwards, and draft create/update.

## Update or delete

```bash
icloud mail signatures update sig_… --text-file new-signature.txt
icloud mail signatures delete sig_…
```

Use `--help` on `update` when changing HTML or identity binding.
