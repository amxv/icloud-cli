---
title: Account validation and doctor
description: Prove iCloud IMAP and SMTP compatibility safely, understand account health, diagnose failures by stage, and inspect stored results without sending mail.
order: 4
category: Guides
summary: Read-only account tests, username fallback, health persistence, doctor diagnostics, timeouts, errors, and privacy.
---

## What account validation proves

After adding an account, run a live test before expecting later mail workflows to work:

```bash
icloud account test Personal
```

The test resolves the selected credential and validates both Apple mail protocols:

- IMAP connects with direct TLS, reads capabilities, authenticates, lists mailboxes, reads `INBOX` status, and logs out;
- SMTP connects, requires STARTTLS, authenticates, sends `NOOP`, and quits.

The SMTP test never issues `MAIL`, `RCPT`, or `DATA`. The IMAP test does not append, move, copy, delete, flag, or expunge anything. It proves authentication and read-only compatibility, not message delivery.

Successful compact output has this shape:

```text
account-test acct_… status=connected imap=ok smtp=ok imap_username=owner mailboxes=6 inbox_messages=42 inbox_unseen=3 checked_at=2026-08-06T03:00:00Z
account-tests count=1 healthy=1
```

An account becomes `connected` only when both protocol checks succeed.

## Username fallback

iCloud accounts may authenticate to IMAP with either the full email address or its short local part. The CLI tries the configured username first, then the other documented forms only after an authentication rejection. It does not retry a TLS, DNS, connection, or timeout failure under a different username.

When a fallback succeeds, that exact username is persisted for later operations. The SMTP username remains the full account email address.

## Validate the selected account or all accounts

With no selector, `account test` uses the configured default, then a sole account:

```bash
icloud account test
icloud --account Personal account test
icloud account test owner@icloud.com
```

Validate every configured profile with a bounded operation:

```bash
icloud account test --all
```

`--all` cannot be combined with a positional selector or global `--account`. The operation is limited to 100 accounts; select one account explicitly for larger inventories.

Use `--verbose` only when capability names are needed:

```bash
icloud account test --verbose
```

Normal compact output omits capability lists. JSON remains explicit:

```bash
icloud --request-id validation-01 account test --json
```

## Stored health state

Every test records a bounded redacted health summary:

- current status: `connected` or `degraded`;
- successful IMAP username;
- sanitized IMAP and SMTP capabilities;
- last tested time;
- last healthy time;
- the stage code for the latest failure.

A failed later test does not erase the previous last-healthy time or last-known capabilities for the protocol that could not be checked. Credential replacement or deletion returns the account to `pending_validation` and clears stale validation evidence.

## Diagnose without contacting Apple

Run local diagnostics:

```bash
icloud doctor
icloud doctor Personal
```

`doctor` reads stored account, credential, and health metadata only. It does not resolve a credential or make a network connection unless `--live` is supplied.

Example local output:

```text
doctor acct_… status=connected credential=env last_tested=2026-08-06T03:00:00Z last_healthy=2026-08-06T03:00:00Z
doctor accounts=1 status=healthy
```

For a new empty state:

```text
doctor status=configuration_required
```

Run the same read-only protocol validation and include its result in diagnostics:

```bash
icloud doctor Personal --live
icloud doctor --all --live --json
```

## Failure stages and exits

Failures identify the stage without exposing a credential, stored environment name, file path, raw server response, or provider capability text. Common stages include:

| Stage | Meaning | Typical exit |
| --- | --- | ---: |
| `credential` | The credential reference is absent, unreadable, or unresolved. | `3` or `4` |
| `dns` | The server hostname could not be resolved. | `5` |
| `connect` | A TCP connection could not be established. | `5` |
| `tls` | Direct IMAP TLS negotiation or certificate validation failed. | `5` |
| `imap_auth` | IMAP rejected every eligible username form. | `4` |
| `imap_list` / `imap_status` | Authenticated read-only compatibility failed. | `5` |
| `smtp_starttls` | SMTP did not offer or complete required STARTTLS. | `5` |
| `smtp_auth` | SMTP rejected the app-specific password. | `4` |
| `timeout` | The command or a protocol stage exceeded its deadline. | `5` |

Plain errors remain one compact stderr line:

```text
error code=authentication message="account validation failed at smtp_auth"
```

With `--json`, the error envelope includes redacted structured reports and the stage so automation can choose a next action.

## Timeouts and cancellation

The global timeout bounds connection establishment and every established protocol stage:

```bash
icloud --timeout 15s account test Personal
```

Cancellation interrupts a stalled greeting, TLS handshake, command response, or authentication exchange. A timeout never changes an authentication error into success and never triggers a send.

## Live integration testing for contributors

The deterministic test suite uses local TLS IMAP and STARTTLS SMTP servers. An additional live harness is opt-in and must use a disposable iCloud account:

```bash
ICLOUD_LIVE_TEST=1 \
ICLOUD_LIVE_TEST_EMAIL=disposable@icloud.com \
ICLOUD_LIVE_TEST_PASSWORD="$DISPOSABLE_APP_PASSWORD" \
go test -run TestLiveICloudCompatibility ./internal/transport/icloud
```

`ICLOUD_LIVE_TEST_IMAP_USERNAME` is optional. The harness skips cleanly when the explicit opt-in or required values are absent. Never use a primary Apple Account password or commit the environment values, protocol output, or generated local state.

Continue with [Troubleshooting](/docs/troubleshooting) for stage-specific next actions, or [Output and automation](/docs/output-and-automation) for JSON and exit contracts.
