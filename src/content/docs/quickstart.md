---
title: Quickstart
description: Install icloud, create an isolated account safely, validate iCloud IMAP and SMTP without sending mail, and inspect health.
order: 1
category: Start
summary: The shortest safe path from installation to a configured and read-only validated iCloud Mail account.
---

## What this build does

The current `icloud` build can:

- create or upgrade a private local state directory;
- add multiple iCloud Mail profiles with stable IDs and a deterministic default;
- store an app-specific password in the native keyring or persist an environment/file reference;
- list, show, rename, default, remove, and manage credential metadata;
- validate direct-TLS IMAP and mandatory-STARTTLS SMTP without sending mail;
- persist account health and inspect local or live diagnostics with `icloud doctor`;
- discover every remote folder and store provider counts without downloading messages;
- choose local future-sync policy independently from discovery;
- report its version and local-state schema, generate shell completions, and generate man pages.

Account setup itself is offline and a newly added account is `pending_validation`. `account test` then performs a read-only authentication and compatibility check. Folder status can subsequently discover the complete mailbox tree and counts. The current build does **not** synchronize message content, export content, mutate mailbox state, or send mail.

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
capabilities command=icloud version=0.1.0 schema=3 account_mode=validated_lifecycle credentials=keyring,env,file network_validation=true diagnostics=true mail_folders=true default_output=compact json=true completion=bash,zsh,fish,powershell man=true
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

## Validate both mail protocols

Ensure an environment/file credential value is available to the current process, then run:

```bash
icloud account test Personal
```

Expected compact shape:

```text
account-test acct_… status=connected imap=ok smtp=ok imap_username=owner mailboxes=6 inbox_messages=42 inbox_unseen=3 checked_at=2026-08-06T03:00:00Z
account-tests count=1 healthy=1
```

The IMAP check reads capabilities, mailbox names, and `INBOX` status. The SMTP check requires STARTTLS, authenticates, sends only `NOOP`, and quits. No message or mailbox is changed.

Inspect the persisted result without another network request:

```bash
icloud doctor
```

Read [Account validation and doctor](/docs/account-validation-and-doctor) for username fallback, all-account checks, live diagnostics, stage codes, and timeouts.

## Discover folders without syncing messages

```bash
icloud mail folders status
icloud mail folders list
```

The live status command lists every mailbox and reads remote counts, while local list reuses the persisted result without contacting Apple. Inbox and Sent default to future sync enabled; every other folder defaults disabled.

Enable an additional folder explicitly:

```bash
icloud mail folders enable Receipts
```

This changes local policy only. It does not fetch or change any message. Read [Folder discovery and sync policy](/docs/folder-discovery-and-sync-policy) for nested and Unicode names, special-use roles, missing folders, and verbose provider metadata.

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
- **Validation failed at a stage** — run `icloud doctor`, then use the stage in [Account validation and doctor](/docs/account-validation-and-doctor) to choose the next action.
- **Folder list is empty** — run `icloud mail folders status` once; local list never performs implicit network discovery.

Next, use the [command reference](/docs/command-reference) for every available command and flag.
