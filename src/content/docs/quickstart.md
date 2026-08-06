---
title: Quickstart
description: Install icloud, configure an app-specific password safely, validate the account, discover folders, synchronize the Inbox, and read compact local previews.
order: 1
category: Start
summary: The shortest safe path from installation to a validated account and locally readable iCloud Inbox.
---

## Install and inspect the binary

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

Representative output:

```text
version 0.1.0
capabilities command=icloud version=0.1.0 schema=4 account_mode=validated_lifecycle credentials=keyring,env,file network_validation=true diagnostics=true mail_folders=true mail_sync=true default_output=compact json=true completion=bash,zsh,fish,powershell man=true
```

## Choose private local state

The platform default is suitable for ordinary use. For an isolated session or agent job:

```bash
export ICLOUD_HOME="$PWD/icloud-state"
icloud account list
```

A new state returns:

```text
accounts count=0
```

Keep this directory private. It contains account metadata, the local mail database, raw synchronized messages, and locks.

## Add an iCloud Mail account

Use an app-specific password, never the primary password for your Apple Account.

To store it in the native keyring without placing it in argv:

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

For a headless process, keep the value in an environment variable or protected file and store only its reference:

```bash
icloud account add \
  --name Agent \
  --email agent@icloud.com \
  --credential-env ICLOUD_AGENT_APP_PASSWORD
```

Read [Account setup and credentials](/docs/account-setup-and-credentials) before rotating credentials or using file references.

## Validate IMAP and SMTP safely

```bash
icloud account test Personal
```

```text
account-test acct_… status=connected imap=ok smtp=ok imap_username=owner mailboxes=6 inbox_messages=42 inbox_unseen=3 checked_at=2026-08-06T03:00:00Z

account-tests count=1 healthy=1
```

Validation authenticates to both mail protocols but does not send, fetch, move, or mark a message. It also remembers the working full-email or short iCloud IMAP username.

Inspect saved health later without another network request:

```bash
icloud doctor
```

## Discover folders

```bash
icloud mail folders status
```

This saves the current mailbox tree and counts. It is required before message sync so the CLI can use a confirmed Inbox identity rather than guessing.

Read the saved inventory offline:

```bash
icloud mail folders list
```

See [Folder discovery and sync policy](/docs/folder-discovery-and-sync-policy) for nested and Unicode names, missing folders, provider roles, and optional sync eligibility.

## Synchronize the Inbox

```bash
icloud mail sync
```

The first run downloads one bounded current-tail page into private local state:

```text
mail-sync fld_… account=acct_… folder=INBOX fetched=20 created=20 updated=0 last_uid=184 more=true bootstrap=true
```

Repeat while `more=true`. Later runs fetch only newer Inbox messages. A failed page leaves the previous frontier intact and is safe to retry.

## Read compact previews offline

```bash
icloud mail inbox
```

```text
message msg_… from=sender@example.com subject="Project update" at=2026-08-06T08:30:00Z unread=true attachments=1 snippet="Here is the latest update…"

inbox account=acct_… count=1 more=false
```

The command reads local state only and never marks messages read. Full bodies, HTML, raw MIME, headers, and attachment download are explicit later capabilities rather than part of the universal compact default.

Inspect progress:

```bash
icloud mail sync status
icloud mail sync checkpoints
```

Read [Sync and read the Inbox](/docs/sync-and-read-inbox) for incremental behavior, safe retries, JSON, and mailbox-identity recovery.

## Select another account

```bash
icloud --account Work mail sync
icloud --account owner@icloud.com mail inbox
```

Without a selector, commands use the default account, then a sole account. The CLI never guesses between multiple non-default profiles.

## Use JSON deliberately

```bash
icloud --request-id inbox-01 mail inbox --json
```

JSON is opt-in. Redirecting stdout does not change the compact default. Passwords, credential references, BCC recipients, raw storage paths, and full bodies stay out of Inbox JSON.

## Common next actions

- Credential or validation trouble: [Account validation and doctor](/docs/account-validation-and-doctor)
- Empty or unexpected folder inventory: [Folder discovery and sync policy](/docs/folder-discovery-and-sync-policy)
- Interrupted or stale Inbox sync: [Sync and read the Inbox](/docs/sync-and-read-inbox)
- Stable stdout, stderr, JSON, and exits: [Output and automation](/docs/output-and-automation)
- Every command and flag: [Command reference](/docs/command-reference)
