---
title: Manage sender identities
description: Record the primary iCloud address, aliases, custom-domain addresses, and other Send From identities locally without duplicating account credentials or claiming provider acceptance.
order: 5
category: Guides
summary: Account-scoped sender identities, stable IDs, local defaults, enabled state, provider-verification boundaries, pagination, and safe removal rules.
---

## Inspect the account's primary identity

Every configured account has one protected primary identity matching the account email address:

```bash
icloud mail identities list
```

The compact record shows the stable `mid_…` ID, normalized address, local display name, kind, provenance, provider-verification state, enabled state, and whether it is the local default sender. Identity reads use local SQLite state only; they do not authenticate or contact iCloud.

Use `--account` when more than one account is configured:

```bash
icloud --account Personal mail identities list
icloud --account Work mail identities show mid_…
```

Identity IDs and addresses are account-scoped. A stable identity ID from one account cannot be opened or changed through another account.

## Add an alternate address locally

Record an address you already control or intend to test as a sender:

```bash
icloud mail identities add \
  --address alias@icloud.com \
  --display-name 'Ashray' \
  --kind icloud_alias
```

Supported kinds are `icloud_alias`, `custom_domain`, `hide_my_email`, and `other`. The protected `primary` kind is created only from the account itself.

User-entered aliases and provider-specific address types are recorded with `provenance=user_configured` and `verification=unverified`. That is deliberate: storing an address locally does not prove that iCloud SMTP accepts it, that Hide My Email can send from it, or that Apple has synchronized the setting. Account authentication remains owned by the existing account credential and IMAP username.

`mail send` and draft create/update/send accept `--from <mid_…|address>`. For ordinary aliases and custom-domain identities, when SMTP accepts that envelope sender, the CLI records `verification=verified`. If iCloud permanently rejects an ordinary alternate at `MAIL FROM`, the CLI records `verification=rejected`, disables that identity, and does not retry with the primary address. Re-enabling it resets verification to `unverified` for another deliberate test after provider-side configuration changes.

## Record a pre-existing Hide My Email address safely

Hide My Email is deliberately narrower. Record only an address that already exists in your Apple account:

```bash
icloud mail identities add \
  --address random-address@icloud.com \
  --kind hide_my_email \
  --label 'Shopping receipts'
```

`--label` is optional local CLI metadata. It does not read, update, or claim to synchronize the label stored by Apple. The identity record has no forwarding-destination field, and ordinary output never exposes an Apple forwarding address. This CLI also does not create, deactivate, delete, or enumerate Hide My Email addresses through Apple Account web surfaces.

If you omit `--display-name` for a Hide My Email identity, the CLI leaves the RFC From display name empty instead of inheriting the account name. This avoids accidentally pairing a privacy address with a real-name local default. Set `--display-name` only when you deliberately want that text to accompany future outbound mail.

Hide My Email outbound use is gated more strictly than ordinary aliases. An HME identity may become the default or be used for an actual SMTP send only after this local identity already has `verification=verified`, meaning the CLI has durable evidence that iCloud accepted that exact sender. `unverified` and `rejected` HME identities remain useful self/delivery identities but actual outbound use is refused before SMTP. A provider rejection records `verification=rejected` without disabling the HME identity, so reply-all self exclusion and delivery recognition remain intact.

Inspect the account-scoped compatibility result with:

```bash
icloud --account Personal capabilities --json
```

`hide_my_email_outbound` is `unavailable` when the selected account has no usable evidence, `unverified` when it has a configured but unproven HME address, and `supported` only when at least one HME identity has accepted-SMTP verification. Without `--account`, the value is `account_scoped` rather than a provider claim about an unknown mailbox.

Replies are privacy-safe by default. When an inbound message was delivered to exactly one enabled, verified HME identity, reply/reply-all automatically prefer that identity. If the delivered HME identity is unverified/rejected, or multiple HME identities appear in the recipient headers, the reply fails with a safety error instead of silently falling back to the primary address. Use an explicit `--from` identity only when you intentionally choose a different sender.

Local compose signatures may optionally bind to one stable identity ID. Use `mail signatures create --identity mid_…` when an alias or custom-domain address needs its own signature, then choose operation-specific defaults with `mail signatures default`. Identity-specific defaults override account-wide signature defaults; explicit `none` prevents fallback. Removing an identity also removes signatures/default rows owned by that identity, while unbound account-wide signatures remain. These are local CLI preferences and do not claim synchronization with Apple Mail signature settings.

Addresses are normalized and unique case-insensitively within one account. The same address may exist under another configured account because each mailbox profile owns its own identity set.

## Choose the local default identity

An account always has one enabled local default sender. Select another enabled identity with:

```bash
icloud mail identities default mid_…
```

You can also make an identity the default when adding it:

```bash
icloud mail identities add \
  --address me@example.com \
  --kind custom_domain \
  --default
```

The primary identity remains enabled and protected even when another address is the local default. This keeps the account from entering a state with no usable sender identity.

An unverified or rejected `hide_my_email` identity cannot become the default. If an older local database had such an HME identity as the default before this compatibility boundary existed, schema migration restores the protected primary identity as default while preserving the HME record for recognition.

## Enable, disable, update, or remove an identity

Update local metadata without changing the stable identity ID:

```bash
icloud mail identities update mid_… --display-name 'Work Ashray'
icloud mail identities update mid_… --kind custom_domain
```

Enable or disable a non-primary identity explicitly:

```bash
icloud mail identities disable mid_…
icloud mail identities enable mid_…
```

If the disabled identity was the default, the CLI reassigns the default to another enabled identity, preferring the protected primary identity. A disabled identity cannot become the default until it is enabled again.

Enabled identities are also the account's canonical self-address set for reply-all and message direction. Enabling or adding an address queues a bounded local historical direction repair; it does not scan the mailbox synchronously. Advance that work explicitly:

```bash
icloud mail identities reclassify --limit 100
```

When `more=true`, run the same command again. The persisted frontier resumes after interruption or process restart. Only cached messages still classified `inbound` whose `From` matches a currently enabled identity are promoted to `outbound`; direction is monotonic, so disabling or removing an identity never demotes previously proven outbound mail. Stable message/thread IDs and search membership are preserved.

Removing local identity configuration is explicit:

```bash
icloud mail identities remove mid_… --yes
```

The primary identity cannot be disabled, removed, or changed away from the account's primary address. Removing an alternate identity never deletes the account, credential, messages, folders, or attachments.

## Continue large identity lists

Identity listing is bounded like other local inventories:

```bash
icloud mail identities list --limit 20
icloud mail identities list --cursor icur_…
```

When `more=true`, the result also includes `next_cursor`. The cursor is opaque and bound to the selected account; reusing it with another account returns a usage error.

## Use strict JSON requests in automation

Read and write request schemas are published by:

```bash
icloud capabilities --json
```

For example:

```json
{
  "address": "alias@icloud.com",
  "display_name": "Ashray",
  "kind": "icloud_alias"
}
```

```bash
icloud mail identities add --input-json identity.json --json
```

Unknown JSON fields and mixed equivalent flags are rejected. Credential references, passwords, BCC data, message bodies, and private blob coordinates are not part of identity request schemas.

Identity configuration remains local account metadata, while provider acceptance is learned only from SMTP evidence produced by a deliberate send. The CLI never treats the `kind` label alone as proof of an Apple capability. `verified` therefore means this CLI observed the provider accept that envelope sender; it does not mean Apple synchronized the identity to every client or exposed an identity-management API.
