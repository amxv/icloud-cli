---
title: "Accounts and credentials"
description: "Add one or more iCloud Mail accounts, choose a default, and manage app-specific passwords without putting secrets on the command line."
order: 4
category: Start
summary: "Configure accounts safely with the system keyring, stdin, environment variables, or protected files."
---

## Add your first account

Use an Apple **app-specific password**, not your normal Apple Account password.

The interactive command is:

```bash
icloud account add --name Personal --email you@icloud.com
```

When the system keyring is available, this is the simplest setup.

## Headless and agent-friendly credential sources

For non-interactive use, choose one explicit source.

### Read the password from stdin

```bash
printf '%s\n' "$ICLOUD_APP_PASSWORD" | \
  icloud account add \
    --name Personal \
    --email you@icloud.com \
    --password-stdin
```

### Reference an environment variable

```bash
icloud account add \
  --name Personal \
  --email you@icloud.com \
  --credential-env ICLOUD_APP_PASSWORD
```

The CLI remembers the reference; the environment variable must exist when a network operation needs the credential.

### Reference a protected file

```bash
icloud account add \
  --name Personal \
  --email you@icloud.com \
  --credential-file ~/.config/icloud-cli/personal-password
```

Use filesystem permissions appropriate for a secret.

There is intentionally no plaintext `--password ...` flag.

## Validate the account

After setup:

```bash
icloud account test Personal
```

This checks Mail connectivity without sending a message.

## Add multiple accounts

```bash
icloud account add --name Personal --email you@icloud.com
icloud account add --name Work --email you@company.example
```

List them:

```bash
icloud account list
```

Choose a default:

```bash
icloud account default Personal
```

Or select one for a single command:

```bash
icloud --account Work mail messages list
```

Selectors can use the account's friendly name, email address, or stable `acct_…` ID.

## Rename an account

```bash
icloud account rename Personal Home
```

The friendly name changes; your Mail data stays associated with the same account.

## Inspect credential metadata

```bash
icloud account credential show Personal
```

This describes how the credential is sourced without printing the password itself.

## Replace a credential

For example, to rotate into a new keyring value:

```bash
printf '%s\n' "$NEW_ICLOUD_APP_PASSWORD" | \
  icloud account credential replace Personal --password-stdin
```

Then re-check the account:

```bash
icloud account test Personal
```

## Remove an account

Review your local data first if you may want to keep it. Then:

```bash
icloud account remove Personal --yes
```

Removing an account is different from uninstalling the CLI. See [Local data and backups](/docs/local-state-and-accounts) if you want to preserve or delete synchronized mail deliberately.

## Troubleshooting

If a credential works in the Apple website but `account test` fails, make sure it is an **app-specific password** and not the primary Apple Account password. For connection diagnostics, continue with [Account checks and doctor](/docs/account-validation-and-doctor).
