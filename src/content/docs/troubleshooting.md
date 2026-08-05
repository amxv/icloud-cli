---
title: Troubleshooting
description: Resolve installation, command, output, completion, and man-generation problems in the current icloud build.
order: 5
category: Guides
summary: Common errors, likely causes, and the next command to run.
---

## The native binary is missing

Symptom:

```text
icloud binary is not installed. Re-run: npm rebuild -g @amxv/icloud-cli
```

Run:

```bash
npm rebuild --global @amxv/icloud-cli
```

The installer first downloads the matching GitHub release asset. When that fails, it can build from source if Go is installed and the package contains `cmd/icloud`.

## The command name is not found

Confirm the global npm binary directory is on `PATH`, then inspect the package:

```bash
npm prefix --global
npm list --global @amxv/icloud-cli
```

The executable is `icloud`, not `icloud-cli`.

## An unknown command fails

Run:

```bash
icloud --help
```

Close misspellings include a suggestion. The current command set includes `account list`, `account show`, `version`, `capabilities`, `completion`, `man`, and `help`.

## JSON appeared on stderr

Structured errors are emitted on stderr when `--json` was requested. Successful JSON remains on stdout. Check the process exit code before parsing stdout as a success envelope.

## A timeout flag is rejected

Use a positive duration:

```bash
icloud --timeout 30s capabilities
icloud --timeout 2m man > icloud.1
```

`0s`, negative values, and malformed durations are usage errors.

## A request ID is rejected

Use 1–128 characters from letters, numbers, dots, underscores, and hyphens:

```bash
icloud --request-id sync-check-01 capabilities --json
```

Spaces and other punctuation are rejected.


## Account list created files

That is expected. The first state-bearing command creates or upgrades the selected application root. Use `--home <path>` or `ICLOUD_HOME` when you want a disposable or isolated state directory:

```bash
ICLOUD_HOME=./icloud-state icloud account list
```

Version, capabilities, completion, man, and help commands do not create state merely because `--home` is present.

## An account is not found

A new state contains no accounts, and this build does not yet provide account creation. Verify the selected root, then list it:

```bash
icloud account list
```

`account show` accepts an exact friendly name, email address, or stable account ID. A miss returns exit code `7`.

## Local state is busy

Another `icloud` process is applying or checking a local-state upgrade. The command returns exit code `6` with `retryable=true` after its timeout. Let the other process finish and retry. Do not remove the lock file to bypass a running process.

For a shorter wait in automation:

```bash
icloud --timeout 5s account list
```

## The selected state directory is not writable

Choose a directory owned by the current user:

```bash
icloud --home ./icloud-state account list
```

The CLI reports configuration failures without printing the private absolute path. Do not use a shared or published directory for mail state.

## Completion output is not active

Generating a script does not automatically install it. Write the output to the location your shell loads, then restart or reload the shell. Generate fresh output after upgrading because completion comes from the installed command tree.

```bash
icloud completion bash > /tmp/icloud.bash
icloud completion zsh > /tmp/_icloud
icloud completion fish > /tmp/icloud.fish
icloud completion powershell > /tmp/icloud.ps1
```

## Man generation fails

The default command writes the root page to stdout:

```bash
icloud man > icloud.1
```

For a complete tree, choose a writable directory:

```bash
icloud man --output-dir ./man
```

The summary reports the output directory after generation.

## The command is not contacting iCloud

That is expected in the current build. Local account-state inspection is present, but account creation, credential resolution, and network commands are not. `icloud capabilities` reports only the behavior actually shipped in the installed binary.
