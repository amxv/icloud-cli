---
title: Output and automation
description: Understand compact defaults, JSON envelopes, stderr, request IDs, timeouts, and stable exit classes.
order: 3
category: Guides
summary: The agent-facing contract for small default outputs and explicit structured expansion.
---

## Compact text is always the default

The CLI does not switch to JSON because stdout is redirected or because a caller is non-interactive. Routine commands return bounded, undecorated text with stable field order:

```text
version 0.1.0
capabilities command=icloud version=0.1.0 default_output=compact json=true completion=bash,zsh,fish,powershell man=true
```

Default output omits empty values, ANSI color, box-drawing tables, JSON punctuation, and repeated labels. Successful output goes to stdout.

## JSON is an explicit opt-in

Commands that advertise `--json` return exactly one envelope:

```bash
icloud version --json
```

```json
{"ok":true,"data":{"command":"icloud","version":"0.1.0"},"meta":{"request_id":"req_example"}}
```

The current `version` and `capabilities` commands support JSON. Completion and man generation intentionally return their native text formats and do not advertise JSON.

## Request IDs

Pass a stable request ID when a calling system wants to correlate a JSON result:

```bash
icloud --request-id job-42 capabilities --json
```

Request IDs accept 1–128 letters, numbers, dots, underscores, or hyphens. When omitted, the CLI generates one for structured output.

## Errors and stderr

Plain errors are one compact line on stderr:

```text
error code=usage message="unknown command \"versoin\" for \"icloud\"; did you mean \"version\"?"
```

When `--json` is requested, the error is also one envelope on stderr:

```json
{"ok":false,"error":{"code":"usage","message":"unknown command ...","retryable":false},"meta":{"request_id":"job-42"}}
```

Help remains on stdout. A command error never writes a success payload to stdout.

## Exit classes

The command reserves stable process exit classes:

| Exit | Class | Meaning |
| ---: | --- | --- |
| `0` | success | The command completed. |
| `1` | internal | An unexpected internal failure occurred. |
| `2` | usage | Arguments, flags, or command names were invalid. |
| `3` | configuration | Required local configuration was invalid or unavailable. |
| `4` | authentication | Credentials were rejected or unavailable. |
| `5` | unavailable | A dependency or network service was unavailable. |
| `6` | conflict | A lock, concurrent operation, or state conflict blocked the command. |
| `7` | not found | The selected object does not exist. |
| `8` | safety | Explicit authorization is required for the requested operation. |

Only success and usage are exercised by the current inspection commands; the remaining classes are part of the established public boundary.

## Global controls

These flags are accepted at the root:

```text
--home <path>         override the future application data root
--account <selector>  select an account when a command uses one
--timeout <duration>  bound command execution; default 30s
--no-input            prohibit interactive prompts
--request-id <id>     correlate structured output
```

The current inspection and documentation commands do not read account state or prompt. They still validate timeout and request-ID syntax so automation can adopt one stable root invocation pattern.

## Unsupported modes are not silently ignored

Command help exposes only representations supported by that command. The current build does not advertise JSONL, full mail bodies, HTML, raw MIME, headers, or body-only views because no mail-reading or streaming command exists yet. Supplying an unknown output flag produces a usage error rather than changing or discarding output silently.
