---
title: Quickstart
description: Install the icloud command, verify the build, and create an isolated local account-state inventory without configuring credentials.
order: 1
category: Start
summary: The shortest safe path from installation to a verified local zero-account session.
---

## What this build does

The current `icloud` build provides the public command and automation contract plus durable local account-state inspection. It can:

- create or upgrade an isolated application data directory;
- list stored accounts and show one by name, email, or stable ID;
- report its version, capabilities, and local-state schema version;
- generate shell completions and man pages.

It does **not** request an Apple Account password, add or validate an account, resolve a credential, read mail, or contact iCloud. No credential setup is required for this quickstart.

## Install

Install the npm package globally:

```bash
npm install --global @amxv/icloud-cli
```

The package downloads the matching native release binary. When a release asset is unavailable and Go is installed, the installer can build the same `icloud` binary from the packaged source.

From a source checkout, build locally instead:

```bash
make build
./dist/icloud version
```

## Verify the command

Run:

```bash
icloud version
icloud capabilities
icloud --help
```

Representative compact output:

```text
version 0.1.0
capabilities command=icloud version=0.1.0 schema=1 default_output=compact json=true completion=bash,zsh,fish,powershell man=true
```

The installed version replaces `0.1.0` with the version embedded in that binary.

## Create an isolated zero-account state

Use an explicit location for a disposable first session:

```bash
ICLOUD_HOME=./icloud-state icloud account list
```

Expected compact output:

```text
accounts count=0
```

The command creates `mail.db`, `blobs/`, and `locks/` under `./icloud-state`, then reads the empty account inventory. It performs no network request and stores no credential value.

Run it again to verify an idempotent reopen:

```bash
ICLOUD_HOME=./icloud-state icloud account list
```

For platform defaults, precedence, privacy, conflicts, and cleanup, read [Local state and account inspection](/docs/local-state-and-accounts).

## Use structured output deliberately

Account reads, `version`, and `capabilities` support one JSON envelope when a script needs named fields:

```bash
ICLOUD_HOME=./icloud-state icloud --request-id inventory-01 account list --json
```

```json
{"ok":true,"data":{"accounts":[],"count":0,"schema_version":1},"meta":{"request_id":"inventory-01"}}
```

Plain text remains the default in terminals and redirected non-interactive runs. See [Output and automation](/docs/output-and-automation) for errors, request IDs, and exit classes.

## Generate local help assets

Generate a completion script for the shell you use:

```bash
icloud completion zsh > ~/.zsh/completions/_icloud
```

Generate a root man page:

```bash
icloud man > icloud.1
```

Generate the complete man-page tree:

```bash
icloud man --output-dir ./man
```

## Common errors

- **`icloud binary is not installed`** — run `npm rebuild --global @amxv/icloud-cli`, or install Go and reinstall so the local-build fallback can run.
- **Unknown command** — run `icloud --help`. Close misspellings include a compact suggestion.
- **`account not found`** — the selected state has no matching account; a new state is empty by design.
- **Local state is busy** — let the other `icloud` process finish its upgrade, then retry.
- **Invalid timeout** — use a positive Go-style duration such as `--timeout 30s` or `--timeout 2m`.

Next, read the [command reference](/docs/command-reference) for every command and global flag available in this build.
