---
title: "Account checks and doctor"
description: "Confirm that an account can use iCloud Mail, inspect saved health, and diagnose common setup or connectivity problems without sending mail."
order: 61
category: Reference
summary: "Use `account test` for a live Mail check and `doctor` for readable diagnostics."
---

## Test one account

```bash
icloud account test Personal
```

Or test the selected/default account:

```bash
icloud account test
```

The check validates that the credential can authenticate to the incoming and outgoing iCloud Mail services. It does **not** send a message or modify your mailbox.

For more detail:

```bash
icloud account test Personal --verbose
```

For automation:

```bash
icloud account test Personal --json
```

## Test every configured account

```bash
icloud account test --all
```

This is bounded to avoid turning one command into an unexpectedly large network operation.

## Run local diagnostics

```bash
icloud doctor
```

By default, doctor inspects the saved configuration and health information without contacting iCloud or resolving the account secret.

For one account:

```bash
icloud doctor Personal
```

## Include a live connectivity check

```bash
icloud doctor Personal --live
```

Or:

```bash
icloud doctor --all --live --json
```

## Understand account status

You will commonly see:

- `pending_validation` — the account or credential was added/changed and has not passed a current live check;
- `connected` — the last full Mail validation succeeded;
- `degraded` — a later validation failed after the account had previously been healthy.

Changing or deleting a credential intentionally returns the account to a state that needs validation again.

## Common failure classes

### Authentication

The credential is missing or iCloud rejected it. Confirm that you are using an Apple **app-specific password**, then replace or test the credential again.

```bash
icloud account credential show Personal
icloud account test Personal
```

### Configuration

A referenced environment variable, file, keyring entry, or local state location is unavailable. Fix the local source first; repeated live tests will not repair it.

### Unavailable

DNS, connectivity, TLS negotiation, provider availability, or a timeout prevented the check. Retry when connectivity is restored.

### Timeout

Choose a different command deadline when appropriate:

```bash
icloud --timeout 60s account test Personal
```

## JSON and exit status

Structured failures include a stable `error.code`, a `retryable` hint, and a bounded diagnostic stage without exposing the secret value.

See [Output and automation](/docs/output-and-automation) for exit classes and [Troubleshooting](/docs/troubleshooting) for symptom-based next steps.
