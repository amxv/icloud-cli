---
title: "S/MIME"
description: "Inspect signed mail, import public certificates, configure decryption identities, and sign or encrypt outbound messages."
order: 25
category: Mail
summary: "Optional end-to-end Mail protection for users who already work with S/MIME certificates and keys."
---

S/MIME is an advanced, optional feature. You can ignore this page entirely if you do not use certificate-based email signing or encryption.

## Inspect signed mail

Normal message output can report S/MIME status when available. For certificate and signature detail:

```bash
icloud mail message show msg_… --verbose
```

The CLI keeps signature validity, trust, and sender identity matching distinct so a cryptographically valid but untrusted certificate is not presented as fully trusted mail.

## Import a public certificate

```bash
icloud mail smime certificates import signer.pem \
  --email sender@example.com
```

List imported certificates:

```bash
icloud mail smime certificates list
```

Trust is explicit. To import a certificate as an account/email-scoped trust root:

```bash
icloud mail smime certificates import private-root.pem \
  --email sender@example.com \
  --trust
```

Do not use `--trust` merely because a certificate exists. It represents an explicit trust decision.

## Configure your own S/MIME identity

First make sure the corresponding Mail sender identity exists:

```bash
icloud mail identities list
```

Then bind it to a public certificate and a private-key source.

Store a key from stdin in the native keyring:

```bash
cat private-key.pem | \
  icloud mail smime identities configure mid_… \
    --certificate smc_… \
    --private-key-stdin
```

Or reference a protected file:

```bash
icloud mail smime identities configure owner@icloud.com \
  --certificate smc_… \
  --private-key-file ~/.config/icloud-cli/smime-key.pem
```

A file key must be protected appropriately, such as mode `0600`.

You can also reference an environment variable with `--private-key-env`.

Inspect configured identities without revealing private key material:

```bash
icloud mail smime identities list
icloud mail smime identities show mid_…
```

## Decrypt a message

```bash
icloud mail message show msg_… --decrypt --full
```

Decryption is explicit. It does not replace the stored encrypted message or silently add decrypted content to local search.

## Sign outbound mail

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Signed update' \
  --body-file update.txt \
  --smime sign \
  --dry-run
```

## Encrypt outbound mail

Encryption requires suitable certificate coverage for the recipients:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Private update' \
  --body-file update.txt \
  --smime encrypt \
  --dry-run
```

Sign and encrypt together:

```bash
icloud mail send ... --smime sign-encrypt --dry-run
```

The same S/MIME modes are available on replies, forwards, and draft send intent.

## Remove S/MIME configuration

```bash
icloud mail smime identities remove smi_… --yes
icloud mail smime certificates remove smc_…
```

Removing a configuration does not rewrite previously synchronized mail.
