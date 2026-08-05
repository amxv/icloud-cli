---
title: Account setup and credentials
description: Add multiple iCloud Mail profiles safely, choose keyring, environment, or file credentials, select a default account, and rotate or remove credentials.
order: 3
category: Guides
summary: Offline account setup, app-specific-password handling, credential references, defaults, rotation, and removal.
---

## Before you add an account

Use an **app-specific password**, not the primary password for your Apple Account. Account setup in this build is deliberately offline: it records the profile and credential source, marks the account `pending_validation`, and makes no IMAP or SMTP connection. A later `account test` workflow will perform live validation.

For experiments or agent-owned runs, choose an isolated state root first:

```bash
export ICLOUD_HOME="$PWD/icloud-state"
```

## Store an app-specific password in the native keyring

The native operating-system keyring is the default for interactive setup. Run the command in a terminal and omit credential-source flags:

```bash
icloud account add --name Personal --email owner@icloud.com
```

The prompt does not echo the password. The value is written to the native keyring; the local database stores only a keyring reference.

For a script, password manager, or agent, send the value through stdin explicitly:

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

`--password-stdin` never prints the value and no `--password` argument exists. Avoid putting a password directly in a command line, shell history, JSON request, or environment exported to unrelated processes.

On a headless Linux session without a Secret Service keyring, use an environment or file reference instead.

## Persist an environment reference

```bash
icloud account add \
  --name Agent \
  --email agent@icloud.com \
  --credential-env ICLOUD_AGENT_APP_PASSWORD
```

The CLI stores the variable name, not its value. It does not require the variable to be set during offline setup. Export it only in the process environment that will run a future network command:

```bash
read -rsp "App-specific password: " ICLOUD_AGENT_APP_PASSWORD
printf '\n'
export ICLOUD_AGENT_APP_PASSWORD
```

Account and credential output never reveals the stored variable name.

## Persist a file reference

Create a private file owned by the current user:

```bash
install -m 600 /dev/null "$HOME/.config/icloud-cli/personal-password"
read -rsp "App-specific password: " ICLOUD_FILE_PASSWORD
printf '\n'
printf '%s\n' "$ICLOUD_FILE_PASSWORD" > "$HOME/.config/icloud-cli/personal-password"
unset ICLOUD_FILE_PASSWORD

icloud account add \
  --name Personal \
  --email owner@icloud.com \
  --credential-file "$HOME/.config/icloud-cli/personal-password"
```

The CLI records an absolute path. It does not copy, print, or delete the external file. Keep the file outside repositories, shared directories, backups that expose plaintext, and the CLI state root.

## Add more than one account

The first account becomes the default automatically. Add another profile with a different friendly name and email:

```bash
icloud account add \
  --name Work \
  --email work@icloud.com \
  --credential-env ICLOUD_WORK_APP_PASSWORD
```

Use a non-email IMAP username only when you already know it is required:

```bash
icloud account add \
  --name Legacy \
  --email legacy@icloud.com \
  --imap-username legacy \
  --credential-env ICLOUD_LEGACY_APP_PASSWORD
```

Without `--imap-username`, the email address is stored initially. Live account testing will later be responsible for any documented short/full username fallback and for persisting the form that succeeds.

## Inspect and select accounts

```bash
icloud account list
icloud account show Personal
icloud --account work@icloud.com account show
icloud account show
```

`account show` without a selector uses the configured default. When no default exists, a sole account is selected; multiple accounts without a default return a conflict rather than guessing.

Set or change the default explicitly:

```bash
icloud account default Work
```

Rename a friendly selector without changing the stable account ID:

```bash
icloud account rename Work Client-Mail
```

## Inspect credential metadata

```bash
icloud account credential show Personal
icloud account credential show Personal --json
```

The result contains the account ID, credential scheme, configured state, and update time. It never includes a keyring key, environment-variable name, file path, or credential value. Showing metadata does not resolve the credential or contact iCloud.

## Rotate or change the credential source

Replace a keyring value through stdin:

```bash
printf '%s\n' "$NEW_APP_PASSWORD" |
  icloud account credential replace Personal --password-stdin
```

Switch to an environment reference:

```bash
icloud account credential replace Personal \
  --credential-env ICLOUD_PERSONAL_APP_PASSWORD
```

Switch to a file reference:

```bash
icloud account credential replace Personal \
  --credential-file "$HOME/.config/icloud-cli/personal-password"
```

A replacement returns the account to `pending_validation`. When changing away from keyring storage, the old native keyring entry is removed. If the local transaction fails, the CLI attempts to restore the prior keyring value before returning an error.

## Delete a credential or account

Credential deletion requires explicit authorization and leaves the account profile in place:

```bash
icloud account credential delete Personal --yes
```

Remove an account profile and its credential reference:

```bash
icloud account remove Personal --yes
```

A native keyring entry is removed with the account. An environment variable or external credential file is left untouched. When several accounts exist, the current default cannot be removed until another account is selected:

```bash
icloud account default Work
icloud account remove Personal --yes
```

Both destructive commands return a safety error without `--yes`; they never prompt implicitly in redirected or agent execution.

## What setup does not do yet

Every account added or changed in this build remains `pending_validation`. Setup does not authenticate to Apple, discover folders, sync messages, or prove SMTP sending. Do not interpret a successful `account add` as proof that the email, IMAP username, app-specific password, or external reference is valid.

Continue with [Local state and account inspection](/docs/local-state-and-accounts) for data paths and locking, or [Output and automation](/docs/output-and-automation) for JSON and exit contracts.
