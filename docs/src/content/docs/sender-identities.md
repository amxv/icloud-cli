---
title: "Sender identities"
description: "Send from iCloud aliases, custom-domain addresses, or other addresses already available to the selected account."
order: 17
category: Mail
summary: "Configure the addresses that `--from` can use and choose the default sender for an account."
---

A sender identity is an address the CLI may use in the `From` field for the selected Mail account.

Every account starts with its primary email address. Add aliases or other addresses you already control when you want to compose from them.

## List identities

```bash
icloud mail identities list
```

## Add an alias

```bash
icloud mail identities add \
  --address alias@icloud.com \
  --kind icloud_alias
```

You can also add a display name or label; check `icloud mail identities add --help` for the available fields.

Adding an identity tells the CLI about an address. It does not create the alias in your Apple Account.

## Choose the default sender

```bash
icloud mail identities default alias@icloud.com
```

You can also use the stable `mid_…` ID:

```bash
icloud mail identities default mid_…
```

## Use a sender for one message

```bash
icloud mail send \
  --from alias@icloud.com \
  --to client@example.com \
  --subject Hello \
  --body 'Hi there' \
  --dry-run
```

The same `--from` option is available on replies, forwards, and drafts.

## Enable or disable an identity

```bash
icloud mail identities disable alias@icloud.com
icloud mail identities enable alias@icloud.com
```

Disabled identities cannot be selected for new outbound mail.

## Update metadata

```bash
icloud mail identities update alias@icloud.com \
  --display-name 'Ashray'
```

For all editable fields:

```bash
icloud mail identities update --help
```

## Remove an identity

```bash
icloud mail identities remove alias@icloud.com
```

The account's protected primary identity cannot be removed as though it were an optional alias.

## Existing mail after adding a new self address

If you add an address that already appears in older synchronized mail, the CLI can update historical direction classification in bounded passes:

```bash
icloud mail identities reclassify
```

This is useful when older messages sent from the newly configured alias should be recognized as your outbound mail.

## Signatures and S/MIME

Signatures can be tied to a particular sender identity. See [Signatures](/docs/signatures).

S/MIME signing/decryption identities are configured separately on top of Mail identities. See [S/MIME](/docs/s-mime).
