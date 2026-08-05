---
title: Command reference
description: A complete reference for the current icloud command tree, global controls, output modes, and generated help assets.
order: 6
category: Reference
summary: Every account-inspection, capability, completion, man, help, and global control available now.
---

## Root command

```bash
icloud [global flags] <command> [command flags]
```

Running `icloud` without a command prints root help to stdout.

### Global flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--home <path>` | `ICLOUD_HOME`, then the platform default | Override the application data directory for commands that use local state. |
| `--account <selector>` | none | Select an account by friendly name, email, or stable ID when required. |
| `--timeout <duration>` | `30s` | Bound command execution with a positive Go-style duration. |
| `--no-input` | false | Prevent interactive prompting. |
| `--request-id <id>` | generated for structured output | Correlate JSON success and error envelopes. |
| `--version` | — | Print the compact installed version and exit. |
| `-h`, `--help` | — | Print help to stdout. |

`account list` and `account show` initialize and read local state. The inspection and documentation commands remain disk-free. Shared flags keep one root contract across both kinds of command.

## `icloud version`

Print the version injected into the binary at build time.

```bash
icloud version
icloud version --json
```

Compact output:

```text
version 0.1.0
```

`--json` emits one success envelope with `command` and `version` fields.

## `icloud capabilities`

Describe behavior supported by the installed binary.

```bash
icloud capabilities
icloud capabilities --json
```

Compact output:

```text
capabilities command=icloud version=0.1.0 schema=1 default_output=compact json=true completion=bash,zsh,fish,powershell man=true
```

The compact `schema` field and JSON `data_schema_version` field report the installed local-state schema. The JSON form also reports that no JSONL command is present in this build.

## `icloud account list`

Create or upgrade the selected local state and list configured accounts:

```bash
icloud account list
icloud account list --limit 50
icloud --home ./work-state account list --json
```

A new state returns:

```text
accounts count=0
```

A populated state emits at most 20 compact account records by default, followed by one count summary. `--limit` accepts `1` through `100`; the summary and JSON data include `more=true` when additional records exist. `--json` returns `accounts`, `count`, and `schema_version` in one success envelope. No credential reference or secret value is included.

## `icloud account show`

Resolve one stored account by friendly name, email address, or stable account ID:

```bash
icloud account show Personal
icloud account show owner@example.com --json
```

A missing account writes a `not_found` error to stderr and exits `7`.

## `icloud completion`

Generate a completion script to stdout:

```bash
icloud completion bash
icloud completion zsh
icloud completion fish
icloud completion powershell
```

Each shell is a command-local help target:

```bash
icloud completion zsh --help
```

Completion output is intentionally the native shell script, not compact text or JSON.

## `icloud man`

Generate the root man page to stdout:

```bash
icloud man > icloud.1
```

Generate the complete page tree:

```bash
icloud man --output-dir ./man
```

Directory mode prints one compact summary after successful generation:

```text
man generated dir=man
```

The pages are generated from the same Cobra command tree used for help and completions.

## `icloud help`

Print root or command-local help:

```bash
icloud help
icloud help capabilities
icloud capabilities --help
```

Help goes to stdout. Usage failures go to stderr and exit with code `2`.

## Output availability

`version`, `capabilities`, `account list`, and `account show` expose `--json` in this build. No command silently accepts unsupported JSONL, full-body, HTML, raw, header, body-only, or verbose modes. See [Output and automation](/docs/output-and-automation) for the stable envelope and exit contracts.
