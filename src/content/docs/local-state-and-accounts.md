---
title: Local state and account inspection
description: Choose an isolated data directory, inspect bounded account inventories, understand selection rules, and handle concurrent access safely.
order: 6
category: Guides
summary: State roots, account list and show, selector/default behavior, privacy, locks, and cleanup.
---

## Start with an isolated state directory

For a disposable or agent-owned session, choose the data root explicitly:

```bash
ICLOUD_HOME=./icloud-state icloud account list
```

A new installation returns:

```text
accounts
count=0
```

The command creates or upgrades local state, reads the account inventory, and exits. It does not prompt, resolve a credential, or contact iCloud.

## Data-directory precedence

The CLI resolves its application root in this order:

1. `--home <path>` on the current command;
2. `ICLOUD_HOME`;
3. the platform data directory.

Platform defaults are:

- macOS: `~/Library/Application Support/icloud-cli`;
- Linux and BSD: `${XDG_DATA_HOME:-~/.local/share}/icloud-cli`;
- Windows: the current user's application-data directory under `icloud-cli`.

An explicit flag wins over the environment:

```bash
icloud --home ./work-state account list
```

The root contains `mail.db`, `blobs/`, and `locks/`. Keep it out of repositories and shared or published directories. The CLI creates private directory and database permissions where the operating system supports them.

## List accounts with a bound

```bash
icloud account list
icloud account list --limit 50
```

The default returns at most 20 account records; `--limit` accepts `1` through `100`. A populated state has this shape:

```text
account
acct_0198d704-6c00-7000-8000-000000000001 name=Personal
email=owner@icloud.com
imap_username=owner@icloud.com
status=pending_validation
default=true
credential=keyring

accounts
count=1
```

When more records exist, the summary includes `more=true`. The output exposes only the credential scheme. It never prints a keyring reference, environment-variable name, file path, or secret value.

## Show and select one account

Use a friendly name, email, or stable ID:

```bash
icloud account show Personal
icloud account show owner@icloud.com
icloud account show acct_0198d704-6c00-7000-8000-000000000001
```

The global selector is equivalent for commands that support account selection:

```bash
icloud --account Personal account show
```

With no selector, `account show` chooses the configured default. If no default exists, it chooses a sole account. Multiple accounts without a default return a conflict instead of guessing.

A missing selector returns exit code `7`:

```text
error
code=not_found
message="account not found"
```

## Understand account status

Offline setup creates accounts in `pending_validation`. Credential replacement and deletion also return the profile to that state and clear stale validation evidence. `account test` marks the account `connected` only after both read-only IMAP and SMTP checks succeed. A later failure marks it `degraded` while retaining the last healthy timestamp.

## Request structured output

```bash
icloud --request-id inventory-01 account list --json
```

```json
{"ok":true,"data":{"accounts":[],"count":0,"schema_version":12},"meta":{"request_id":"inventory-01"}}
```

JSON remains explicit. Redirection and non-TTY execution do not change the output mode. Structured account and credential output applies the same reference/value redaction as compact text.

## Handle concurrent access

State upgrades and account mutations use separate cross-process locks. A timed-out upgrade returns:

```text
error
code=conflict
message="local state is busy while another icloud process migrates it"
retryable=true
```

A timed-out account mutation returns:

```text
error
code=conflict
message="local account state is busy in another icloud process"
retryable=true
```

Let the other process finish and retry. Do not remove lock files to bypass a running operation. Reads remain independently bounded and the SQLite store uses transaction boundaries for each mutation.

## Privacy and cleanup

The database stores account metadata, credential references, and durable folder observations/policy, never plaintext app-specific passwords. Folder records retain decoded/wire names, provider attributes and counts, presence/stale state, and local sync enablement. A keyring password lives in the operating-system credential store. Environment values and file contents remain external to the CLI state root.

Removing an account deletes its native keyring entry when present. It does not delete an environment variable or external credential file. Uninstalling the npm package does not remove the selected state root or external credentials.

Continue with [Account setup and credentials](/docs/account-setup-and-credentials) for lifecycle commands, [Account validation and doctor](/docs/account-validation-and-doctor) for persisted health, [Folder discovery and sync policy](/docs/folder-discovery-and-sync-policy) for the mailbox tree, or [Output and automation](/docs/output-and-automation) for envelopes and exit classes.
