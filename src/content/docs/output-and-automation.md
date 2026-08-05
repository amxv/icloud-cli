---
title: Output and automation
description: Understand compact defaults, JSON envelopes, stdin credential safety, stderr, request IDs, timeouts, and stable exit classes.
order: 6
category: Guides
summary: The agent-facing contract for bounded text, explicit JSON, safe input, and stable failures.
---

## Compact text is always the default

The CLI does not switch to JSON because stdout is redirected or a caller is non-interactive. Routine commands return bounded, undecorated text with stable field order:

```text
version 0.1.0
account-added acct_… status=pending_validation default=true credential=keyring
accounts count=1
account-test acct_… status=connected imap=ok smtp=ok imap_username=owner mailboxes=6 inbox_messages=42 inbox_unseen=3 checked_at=2026-08-06T03:00:00Z
```

Default output omits empty values, ANSI color, box-drawing tables, JSON punctuation, and repeated labels. Successful output goes to stdout.

`account list` returns 20 records by default, accepts `--limit` through 100, and reports `more=true` when truncated. Mutation commands return an outcome and stable account ID rather than echoing submitted names, email addresses, paths, or passwords.

## JSON is explicit

Commands that advertise `--json` return one envelope:

```bash
icloud --request-id account-01 account show --json
```

```json
{"ok":true,"data":{"account":{"id":"acct_…","name":"Personal","email":"owner@icloud.com","imap_username":"owner","status":"connected","default":true,"credential_scheme":"keyring","imap_capabilities":["IDLE","IMAP4REV1"],"smtp_capabilities":["AUTH"],"last_healthy_at":"2026-08-06T03:00:00Z","last_tested_at":"2026-08-06T03:00:00Z","created_at":"2026-08-06T02:30:00Z","updated_at":"2026-08-06T03:00:00Z"},"schema_version":2},"meta":{"request_id":"account-01"}}
```

Account lifecycle, credential, validation, doctor, account reads, `version`, and `capabilities` support JSON. Completion and man generation intentionally return native text formats.

Credential metadata JSON omits internal IDs, keyring references, environment names, file paths, and values. Error envelopes omit wrapped provider and storage causes.

An `account test --json` failure writes one error envelope to stderr. Its `details.stage` and bounded `details.reports` identify the failing protocol without including the credential reference, password, raw server response, or debug transcript.

## Supply credentials without prompting

An agent or redirected process must choose one source explicitly:

```text
--password-stdin
--credential-env <name>
--credential-file <path>
```

No `--password` flag exists. Without a source, non-TTY setup returns a safety error and does not prompt. `--no-input` also prohibits an implicit terminal prompt. Password stdin is bounded and stored in the native keyring; it is never copied into JSON or SQLite.

## Request IDs

```bash
icloud --request-id job-42 account list --json
```

Request IDs accept 1–128 letters, numbers, dots, underscores, or hyphens. When omitted, the CLI generates one for structured output.

## Errors and stderr

Plain errors are one compact line on stderr:

```text
error code=safety message="account removal requires --yes"
```

With `--json`, the error is one envelope on stderr:

```json
{"ok":false,"error":{"code":"safety","message":"account removal requires --yes","retryable":false},"meta":{"request_id":"job-42"}}
```

Help remains on stdout. A failed command never writes a success payload to stdout.

## Exit classes

| Exit | Class | Meaning |
| ---: | --- | --- |
| `0` | success | The command completed. |
| `1` | internal | An unexpected internal failure occurred. |
| `2` | usage | Arguments, flags, selectors, or references were invalid. |
| `3` | configuration | Local state or the native keyring was unavailable. |
| `4` | authentication | A credential value was missing or IMAP/SMTP rejected it. |
| `5` | unavailable | DNS, connectivity, TLS, protocol compatibility, or timeout prevented validation. |
| `6` | conflict | A lock, duplicate account, ambiguous selection, or default-removal rule blocked the command. |
| `7` | not found | The selected account or credential metadata does not exist. |
| `8` | safety | Explicit input or `--yes` authorization is required. |

## Global controls

```text
--home <path>         override the application data root
--account <selector>  select an account when a command supports it
--timeout <duration>  bound command execution; default 30s
--no-input            prohibit implicit terminal prompts
--request-id <id>     correlate structured output
```

Version, capabilities, completion, man, and help remain disk-free. Account reads and local `doctor` never prompt or contact Apple. Mutations and live validation use the same global timeout; transport stages also apply deadlines after a connection is established.

## Unsupported expansions fail visibly

No current command advertises JSONL, full mail bodies, HTML, raw MIME, headers, or body-only views because mail reading and streaming are not present yet. Unknown output flags produce usage errors rather than being silently ignored.
