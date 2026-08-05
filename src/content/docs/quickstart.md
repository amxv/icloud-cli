---
title: Quickstart
description: Install icloud, verify the binary, create an isolated state root, and add an offline iCloud Mail account without exposing its app-specific password.
order: 1
category: Start
summary: The shortest safe path from installation to a configured pending-validation account.
---

## What this build does

The current `icloud` build can:

- create or upgrade a private local state directory;
- add multiple iCloud Mail profiles with stable IDs and a deterministic default;
- store an app-specific password in the native keyring or persist an environment/file reference;
- list, show, rename, default, remove, and manage credential metadata;
- report its version and local-state schema, generate shell completions, and generate man pages.

Account setup is offline. It does **not** authenticate to Apple, read mail, discover folders, or send mail yet. A newly added account is `pending_validation`.

## Install

```bash
npm install --global @amxv/icloud-cli
icloud version
icloud capabilities
```

From a source checkout:

```bash
make build
./dist/icloud version
```

Representative compact output:

```text
version 0.1.0
capabilities command=icloud version=0.1.0 schema=1 account_mode=offline_lifecycle credentials=keyring,env,file default_output=compact json=true completion=bash,zsh,fish,powershell man=true
```

## Choose an isolated state root

```bash
export ICLOUD_HOME="$PWD/icloud-state"
icloud account list
```

A new state returns:

```text
accounts count=0
```

The command creates `mail.db`, `blobs/`, and `locks/` under the selected root. It performs no network request.

## Add an account safely

Use an app-specific password rather than the primary password for your Apple Account. To place the value in the native operating-system keyring without an argv flag or echoed input:

```bash
read -rsp "App-specific password: " ICLOUD_APP_PASSWORD
printf '\n'
printf '%s\n' "$ICLOUD_APP_PASSWORD" |
  icloud account add \
    --name Personal \
    --email owner@icloud.com \
    --password-stdin
unset ICLOUD_APP_PASSWORD
```

Expected shape:

```text
account-added acct_… status=pending_validation default=true credential=keyring
```

The first account becomes the default. The output does not repeat the email or password. The database contains a credential reference, not the password value.

For a headless process, persist an environment reference instead:

```bash
icloud account add \
  --name Agent \
  --email agent@icloud.com \
  --credential-env ICLOUD_AGENT_APP_PASSWORD
```

Read [Account setup and credentials](/docs/account-setup-and-credentials) before using file references, rotating credentials, or removing profiles.

## Inspect the configured profile

```bash
icloud account list
icloud account show
icloud account credential show Personal
```

A compact account record includes the stable ID, friendly name, email, initial IMAP username, `pending_validation` status, default state, and credential scheme. It never includes the credential reference or value.

## Use JSON deliberately

```bash
icloud --request-id account-setup-01 account show --json
```

JSON is opt-in and uses one success envelope. Redirecting stdout does not change the compact default. Credential references and values remain omitted from JSON.

## Generate local help assets

```bash
icloud completion zsh > ~/.zsh/completions/_icloud
icloud man > icloud.1
icloud man --output-dir ./man
```

## Common errors

- **Native keyring unavailable** — use `--credential-env` or `--credential-file`, especially in headless Linux sessions.
- **Credential input required** — non-interactive setup must choose `--password-stdin`, `--credential-env`, or `--credential-file`.
- **Account already exists** — friendly names and email addresses are unique within one state root.
- **Cannot remove the default** — set another default account first.
- **Local state is busy** — let the other `icloud` process finish or retry with an appropriate timeout.

Next, use the [command reference](/docs/command-reference) for every available command and flag.
