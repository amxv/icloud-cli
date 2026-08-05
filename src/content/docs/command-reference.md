---
title: Command reference
description: A complete reference for account lifecycle, read-only iCloud validation, doctor diagnostics, global controls, output modes, completion, and man generation.
order: 8
category: Reference
summary: Every account, credential, validation, doctor, capability, completion, man, help, and global control available now.
---

## Root command

```bash
icloud [global flags] <command> [command flags]
```

Running `icloud` without a command prints root help to stdout.

### Global flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--home <path>` | `ICLOUD_HOME`, then platform default | Override the application data directory for state-bearing commands. |
| `--account <selector>` | none | Select by friendly name, email, or stable ID when supported. |
| `--timeout <duration>` | `30s` | Bound execution and lock acquisition. |
| `--no-input` | false | Prevent implicit terminal prompts. Explicit stdin remains allowed. |
| `--request-id <id>` | generated for structured output | Correlate JSON success and error envelopes. |
| `--version` | — | Print the compact installed version and exit. |
| `-h`, `--help` | — | Print help to stdout. |

## `icloud account add`

Create an offline account profile. `--name` and `--email` are required.

```bash
icloud account add --name Personal --email owner@icloud.com
printf '%s\n' "$APP_PASSWORD" |
  icloud account add --name Personal --email owner@icloud.com --password-stdin
icloud account add --name Agent --email agent@icloud.com \
  --credential-env ICLOUD_AGENT_APP_PASSWORD
icloud account add --name Work --email work@icloud.com \
  --credential-file "$HOME/.config/icloud-cli/work-password"
```

Credential-source flags are mutually exclusive:

| Flag | Behavior |
| --- | --- |
| `--password-stdin` | Read a bounded value from stdin and store it in the native keyring. |
| `--credential-env <name>` | Persist an environment-variable reference without resolving it. |
| `--credential-file <path>` | Persist an absolute file reference without reading or copying it. |

In an interactive terminal, omitting all three sources opens a secure non-echoing keyring prompt. Non-TTY execution requires an explicit source. Optional `--imap-username` overrides the initial full-email username. `--default` makes the new profile the default; otherwise the first profile defaults automatically.

Success returns an outcome, stable ID, `pending_validation`, default state when true, and credential scheme. It does not echo the submitted name, email, path, variable, or password. `--json` returns the same bounded mutation metadata.

## `icloud account list`

```bash
icloud account list
icloud account list --limit 50
icloud account list --json
```

The default bound is 20 and the maximum is 100. A final summary reports count and `more=true` when truncated. Account records include the credential scheme only.

## `icloud account show`

```bash
icloud account show Personal
icloud --account owner@icloud.com account show
icloud account show
icloud account show --json
```

Without a selector, the command chooses the default, then a sole account. A missing account exits `7`; ambiguous selection exits `6`.

## `icloud account rename`

```bash
icloud account rename Personal Primary
icloud account rename Personal Primary --json
```

The stable account ID and email do not change. Friendly-name conflicts exit `6`.

## `icloud account default`

```bash
icloud account default Work
icloud account default Work --json
```

Exactly one account is default after success.

## `icloud account remove`

```bash
icloud account remove Old --yes
icloud account remove Old --yes --json
```

`--yes` is required. Removing a native-keyring profile clears its keyring entry. Environment variables and external files are not deleted. A default account cannot be removed while another profile exists; set another default first.

## `icloud account credential show`

```bash
icloud account credential show Personal
icloud account credential show Personal --json
```

Returns account ID, scheme, configured state, and update time. The command does not resolve or disclose the reference or value.

## `icloud account credential replace`

```bash
printf '%s\n' "$NEW_APP_PASSWORD" |
  icloud account credential replace Personal --password-stdin
icloud account credential replace Personal \
  --credential-env ICLOUD_PERSONAL_APP_PASSWORD
icloud account credential replace Personal \
  --credential-file "$HOME/.config/icloud-cli/personal-password"
```

Uses the same mutually exclusive source flags as `account add`. A replacement marks the account `pending_validation`. `--json` returns credential metadata without the reference or value.

## `icloud account credential delete`

```bash
icloud account credential delete Personal --yes
icloud account credential delete Personal --yes --json
```

`--yes` is required. The account remains, but its credential reference is removed and its status is `pending_validation`. A native keyring entry is cleared when present.

## `icloud account test`

```bash
icloud account test
icloud account test Personal
icloud --account owner@icloud.com account test
icloud account test --all
icloud account test --verbose
icloud account test --json
```

Resolves the selected credential and validates direct-TLS IMAP plus mandatory-STARTTLS SMTP. IMAP reads capabilities, mailbox names, and `INBOX` status. SMTP authenticates, sends `NOOP`, and quits. The command never sends a message or mutates a mailbox.

`--all` is bounded to 100 profiles and conflicts with a selector. `--verbose` adds sanitized capability names; normal compact output includes protocol outcomes, the successful IMAP username, mailbox count, bounded `INBOX` counts, and the test time. Both protocols must pass for `connected` status.

## `icloud doctor`

```bash
icloud doctor
icloud doctor Personal
icloud doctor --all
icloud doctor Personal --live
icloud doctor --json
```

Without `--live`, reads stored account, credential, last-error, last-tested, and last-healthy metadata without resolving a secret or contacting Apple. With `--live`, runs the same read-only validation as `account test` first. An empty state returns `doctor status=configuration_required`.

## `icloud version`

```bash
icloud version
icloud version --json
```

Compact output:

```text
version 0.1.0
```

## `icloud capabilities`

```bash
icloud capabilities
icloud capabilities --json
```

Compact output:

```text
capabilities command=icloud version=0.1.0 schema=2 account_mode=validated_lifecycle credentials=keyring,env,file network_validation=true diagnostics=true default_output=compact json=true completion=bash,zsh,fish,powershell man=true
```

The schema field reports the installed local-state migration version without opening state.

## `icloud completion`

```bash
icloud completion bash
icloud completion zsh
icloud completion fish
icloud completion powershell
```

Output is the native shell script, not compact text or JSON.

## `icloud man`

```bash
icloud man > icloud.1
icloud man --output-dir ./man
```

Directory mode returns:

```text
man generated dir=man
```

## `icloud help`

```bash
icloud help
icloud help account
icloud account credential replace --help
```

Help goes to stdout. Usage failures go to stderr and exit `2`.

## Output availability

Every account and credential lifecycle command above, `account test`, `doctor`, `version`, and `capabilities` supports `--json`. Validation and doctor also support bounded `--verbose` capability detail. Completion and man generation retain native text. No command silently accepts unsupported JSONL, full-body, HTML, raw, header, or body-only modes.
