---
title: Local state and account inspection
description: Create an isolated local state directory, inspect the empty account inventory, understand data paths, and handle lock conflicts safely.
order: 3
category: Guides
summary: First-run local state, account list and show, path overrides, privacy, and concurrent access.
---

## Start with an isolated state directory

The first account command creates the local state needed by later iCloud Mail workflows. For a disposable or agent-owned session, choose the directory explicitly:

```bash
ICLOUD_HOME=./icloud-state icloud account list
```

A new installation returns a valid empty inventory:

```text
accounts count=0
```

This command does not prompt, resolve a credential, or contact iCloud. It creates or upgrades local state, reads the account inventory, and exits.

Running the same command again reopens the existing state safely:

```bash
ICLOUD_HOME=./icloud-state icloud account list
```

## Choose the application data location

The CLI resolves its data directory in this order:

1. `--home <path>` on the current command;
2. the `ICLOUD_HOME` environment variable;
3. the platform data directory.

Platform defaults are:

- macOS: `~/Library/Application Support/icloud-cli`;
- Linux and BSD: `${XDG_DATA_HOME:-~/.local/share}/icloud-cli`;
- Windows: the current user's application-data directory under `icloud-cli`.

An explicit command flag wins over the environment:

```bash
icloud --home ./work-state account list
```

The chosen root contains `mail.db`, `blobs/`, and `locks/`. Do not place it inside a shared or published directory. The CLI creates the root, blob directory, and lock directory with private permissions where the operating system supports them.

## List accounts

```bash
icloud account list
icloud account list --limit 50
```

The compact default returns at most 20 records. `--limit` accepts `1` through `100`. Output contains one bounded record per returned account, followed by one count summary. A populated state has this shape:

```text
account acct_0198d704-6c00-7000-8000-000000000001 name=Personal email=owner@example.com imap_username=owner status=pending_validation default=true credential=env
accounts count=1
```

When additional records exist beyond the selected bound, the summary includes `more=true`. Increase `--limit` deliberately rather than receiving an unbounded inventory.

The record includes the stable account ID and only the credential **scheme**. It never prints the credential reference or secret value.

This build does not yet provide account creation or credential setup, so a normal new installation remains at `accounts count=0`.

## Show one account

`account show` accepts a friendly name, email address, or stable account ID:

```bash
icloud account show Personal
icloud account show owner@example.com
icloud account show acct_0198d704-6c00-7000-8000-000000000001
```

A missing selector returns exit code `7` and one compact error on stderr:

```text
error code=not_found message="account not found"
```

## Request structured output

Both account reads support one JSON success envelope:

```bash
ICLOUD_HOME=./icloud-state icloud --request-id inventory-01 account list --json
```

```json
{"ok":true,"data":{"accounts":[],"count":0,"schema_version":1},"meta":{"request_id":"inventory-01"}}
```

For a bounded partial page, JSON includes `"more":true`.

JSON is opt-in. Redirecting stdout does not change the compact default.

## Handle concurrent initialization

State upgrades are serialized across processes. When another `icloud` process holds the migration lock until your command timeout expires, the command returns exit code `6`:

```text
error code=conflict message="local state is busy while another icloud process migrates it" retryable=true
```

Let the other process finish, then retry the same read. Do not delete the lock file to bypass a running operation.

## Privacy and cleanup

The local database stores account metadata and credential references, not plaintext app-specific passwords. Account reads do not reveal a reference's environment-variable name, file path, keyring key, or value.

Uninstalling the npm package does not delete application state. Before removing a state root manually, confirm that you selected the intended directory and preserve any data you need. The current build has no public account-removal or state-cleanup command.

Next, read [Output and automation](/docs/output-and-automation) for stable envelopes and exit classes, or use the [Command reference](/docs/command-reference) for exact account syntax.
