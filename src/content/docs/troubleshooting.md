---
title: Troubleshooting
description: Resolve installation, account setup, credential, state, output, completion, and man-generation problems.
order: 6
category: Guides
summary: Common errors, privacy-safe diagnosis, and the next command to run.
---

## The native binary is missing

```text
icloud binary is not installed. Re-run: npm rebuild -g @amxv/icloud-cli
```

Run:

```bash
npm rebuild --global @amxv/icloud-cli
```

The installer first tries the matching native release asset, then a local Go build when source and Go are available.

## The command name is not found

Confirm the global npm binary directory is on `PATH`:

```bash
npm prefix --global
npm list --global @amxv/icloud-cli
```

The executable is `icloud`, not `icloud-cli`.

## An unknown command fails

```bash
icloud --help
icloud account --help
icloud account credential --help
```

Close misspellings include a compact suggestion. Help is generated from the live command tree.

## Credential input is required

A non-interactive account add or credential replacement never waits for an implicit prompt. Choose exactly one source:

```bash
printf '%s\n' "$ICLOUD_APP_PASSWORD" |
  icloud account add --name Personal --email owner@icloud.com --password-stdin

icloud account add --name Personal --email owner@icloud.com \
  --credential-env ICLOUD_APP_PASSWORD

icloud account add --name Personal --email owner@icloud.com \
  --credential-file "$HOME/.config/icloud-cli/personal-password"
```

Do not put the password in an argv flag. No `--password` flag is supported.

## The native keyring is unavailable

Headless Linux sessions may not have an unlocked Secret Service session. The command returns a configuration error without printing the attempted keyring key or password. Use an environment or file reference, or run the command in a desktop session with a working native keyring.

## A credential value is missing

Offline setup can persist an environment or file reference without resolving it. Before a future live command, verify that the selected process can read the external source:

```bash
read -rsp "App-specific password: " ICLOUD_APP_PASSWORD
printf '\n'
export ICLOUD_APP_PASSWORD
ls -l "$HOME/.config/icloud-cli/personal-password"
```

The CLI intentionally keeps the exact stored variable name or path out of normal output and errors. Use your setup record rather than expecting `credential show` to reveal it.

## An account already exists

Friendly names and email addresses are unique, case-insensitively, within one state root:

```bash
icloud account list
```

Rename the existing profile or choose a different name/email. A failed duplicate keyring setup removes the newly attempted keyring entry before returning the conflict.

## Account selection is ambiguous

Set a default or pass a selector:

```bash
icloud account default Personal
icloud --account owner@icloud.com account show
```

The CLI never guesses between multiple accounts without a default.

## The default account cannot be removed

Choose another default first:

```bash
icloud account default Work
icloud account remove Personal --yes
```

This prevents a multi-account state from losing its deterministic selection target.

## Removal or deletion requires authorization

Account removal and credential deletion require `--yes`:

```bash
icloud account remove Old --yes
icloud account credential delete Personal --yes
```

Without it, the command exits with the safety class before mutating state.

## Account commands created files

That is expected. The first state-bearing command creates or upgrades the selected application root. Use an isolated location when appropriate:

```bash
ICLOUD_HOME=./icloud-state icloud account list
```

Version, capabilities, completion, man, and help do not create state merely because `--home` is present.

## Local state is busy

Another process is upgrading state or mutating accounts. The command returns exit `6` with `retryable=true`. Let the other process finish and retry; do not delete lock files to bypass it.

```bash
icloud --timeout 5s account list
```

## The selected state directory is not writable

Choose a directory owned by the current user:

```bash
icloud --home ./icloud-state account list
```

Errors avoid printing private absolute paths. Do not place mail state in a shared or published directory.

## JSON appeared on stderr

Structured errors use stderr when `--json` was requested. Check the process exit code before parsing stdout as a success envelope.

## A timeout or request ID is rejected

Use a positive Go-style duration and a request ID containing only letters, numbers, dots, underscores, or hyphens:

```bash
icloud --timeout 30s --request-id account-check-01 account list --json
```

## Completion or man output is not active

Generate fresh assets from the installed command tree:

```bash
icloud completion zsh > /tmp/_icloud
icloud man > icloud.1
icloud man --output-dir ./man
```

Generating completion output does not install it automatically.

## The command is not contacting iCloud

That is expected for the current account lifecycle. `account add`, credential replacement, and local reads are offline and leave profiles `pending_validation`. Live IMAP and SMTP validation arrives with the later account-test phase.
