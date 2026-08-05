---
title: Quickstart
description: Install the icloud command, verify the build, and inspect its current capabilities without configuring credentials.
order: 1
category: Start
summary: The shortest safe path from installation to a verified compact CLI session.
---

## What this build does

The current `icloud` build establishes the public command and automation contract. It can report its version and capabilities, generate shell completions, and generate man pages.

It does **not** request an Apple Account password, read a keychain, create local mail state, or contact iCloud. No credential setup is required for this quickstart.

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
capabilities command=icloud version=0.1.0 default_output=compact json=true completion=bash,zsh,fish,powershell man=true
```

The installed version replaces `0.1.0` with the version embedded in that binary.

## Use structured output deliberately

`version` and `capabilities` support one JSON envelope when a script needs named fields:

```bash
icloud capabilities --json
```

```json
{"ok":true,"data":{"command":"icloud","version":"0.1.0","output":{"default":"compact","json":true,"jsonl":false},"completion":["bash","zsh","fish","powershell"],"man":true},"meta":{"request_id":"req_example"}}
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
- **Invalid timeout** — use a positive Go-style duration such as `--timeout 30s` or `--timeout 2m`.

Next, read the [command reference](/docs/command-reference) for every command and global flag available in this build.
