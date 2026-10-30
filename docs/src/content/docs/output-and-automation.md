---
title: "Output and automation"
description: "Use compact text, JSON, JSONL, strict request files, request IDs, timeouts, and stable exit classes in scripts and agents."
order: 41
category: Agents & automation
summary: "Choose the smallest useful output, opt into structured contracts, and handle failures by stable class instead of parsing prose."
---

## Compact text is the default

```bash
icloud account list
icloud mail messages list
icloud mail search invoice
```

Redirecting output does not silently change formats. This keeps ordinary terminal use and agent use predictable.

## JSON is explicit

```bash
icloud account list --json
icloud mail search invoice --json
```

A successful response uses a stable envelope with `ok`, `data`, and `meta`. Errors use an error envelope on stderr.

Use a request ID when correlating a larger workflow:

```bash
icloud --request-id job-42 mail search invoice --json
```

## JSONL is for streams and per-item work

Long-running watch:

```bash
icloud mail watch --jsonl
```

Bounded multi-item action:

```bash
icloud mail move msg_… msg_… \
  --from inbox \
  --to Projects \
  --idempotency-key move-batch-01 \
  --yes \
  --jsonl
```

JSONL emits typed item/lifecycle events and a final completion event so a caller can process progress without waiting for one large JSON document.

## Put complex requests in JSON

For a search:

```json
{
  "query": "invoice",
  "folder": "inbox",
  "from": "billing@example.com",
  "unread": true,
  "limit": 20
}
```

```bash
icloud mail search --input-json search.json --json
```

For a send:

```json
{
  "to": ["client@example.com"],
  "subject": "Project update",
  "body_file": "message.txt",
  "idempotency_key": "project-update-07"
}
```

```bash
icloud mail send --input-json request.json --dry-run --json
icloud mail send --input-json request.json --yes --json
```

Do not mix equivalent positional/request flags with `--input-json`. Keep process controls such as `--yes`, `--dry-run`, global `--account`, and output selection outside the JSON request.

## Discover schemas from the installed build

```bash
icloud capabilities --json
```

The automation section lists commands that accept structured input and links each one to its versioned request/response schema. Use this rather than copying an old request shape into a long-lived integration.

## Keep message content opt-in

Structured output does not imply full message content:

```bash
icloud mail message show msg_… --json
```

Ask for content explicitly:

```bash
icloud mail message show msg_… --full --json
icloud mail message show msg_… --format html --json
```

Exact raw MIME is a separate explicit mode. Attachment bytes require an explicit output destination.

## Global controls

These work across the command tree where applicable:

```text
--account <selector>  choose an account
--home <path>         choose a local data root
--timeout <duration>  bound normal command work
--no-input            never prompt implicitly
--request-id <id>     correlate structured output
```

For `mail watch`, `--timeout` applies to each provider/sync attempt rather than ending the foreground watcher itself.

## Errors go to stderr

Compact example:

```text
error
code=safety
message="account removal requires --yes"
```

JSON example:

```json
{"ok":false,"error":{"code":"safety","message":"account removal requires --yes","retryable":false},"meta":{"request_id":"job-42"}}
```

A failed command does not emit a normal success payload to stdout.

## Exit classes

| Exit | Class | Meaning |
| ---: | --- | --- |
| `0` | success | The command completed. |
| `1` | internal | An unexpected CLI failure occurred. |
| `2` | usage | Arguments, flags, or request shape were invalid. |
| `3` | configuration | Required local configuration or state is unavailable. |
| `4` | authentication | A credential is missing or was rejected. |
| `5` | unavailable | Connectivity, TLS, provider availability, or timeout prevented the operation. |
| `6` | conflict | Existing state makes the requested action ambiguous or conflicting. |
| `7` | not found | The requested local object does not exist. |
| `8` | safety | Explicit review, input, or authorization is required. |

Branch on these classes or the JSON `error.code`; do not parse human error prose.

## Partial work is reported

Multi-item provider operations cannot pretend to be transactional when earlier items have already succeeded remotely. Structured completion output therefore reports attempted/succeeded/failed counts and whether work was partial.

The default bulk mode stops on the first item failure after request validation. Where supported, use:

```bash
--mode continue-on-error
```

when remaining independent items should still be attempted.

## Secrets stay outside ordinary request JSON

Account setup supports explicit secret sources such as `--password-stdin`, `--credential-env`, and `--credential-file`. There is no plaintext `--password` flag, and credential request JSON does not accept a password value.

See [Agent guide](/docs/agent-guide) for operating conventions and [Automation recipes](/docs/automation-recipes) for complete examples.
