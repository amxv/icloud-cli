---
title: Command reference
description: A complete reference for the current icloud command tree, global controls, output modes, and generated help assets.
order: 5
category: Reference
summary: Every command and flag available at the Phase 1 public boundary.
---

## Root command

```bash
icloud [global flags] <command> [command flags]
```

Running `icloud` without a command prints root help to stdout.

### Global flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--home <path>` | platform default later | Override the application data directory for commands that use local state. |
| `--account <selector>` | none | Select an account by friendly name, email, or stable ID when required. |
| `--timeout <duration>` | `30s` | Bound command execution with a positive Go-style duration. |
| `--no-input` | false | Prevent interactive prompting. |
| `--request-id <id>` | generated for structured output | Correlate JSON success and error envelopes. |
| `--version` | — | Print the compact installed version and exit. |
| `-h`, `--help` | — | Print help to stdout. |

The current inspection and documentation commands do not access account state. The shared flags are accepted now so scripts use one stable root contract.

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
capabilities command=icloud version=0.1.0 default_output=compact json=true completion=bash,zsh,fish,powershell man=true
```

The JSON form also reports that no JSONL command is present in this build.

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

Only `version` and `capabilities` expose `--json` in this build. No command silently accepts unsupported JSONL, full-body, HTML, raw, header, body-only, or verbose modes. See [Output and automation](/docs/output-and-automation) for the stable envelope and exit contracts.
