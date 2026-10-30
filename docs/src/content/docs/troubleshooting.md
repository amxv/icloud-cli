---
title: "Troubleshooting"
description: "Symptom-based fixes for installation, account setup, synchronization, search, reading, sending, folders, imports, and local-state problems."
order: 63
category: Reference
summary: "Start with the command that failed, then use these short checks instead of inspecting implementation details."
---

Start with the smallest diagnostic that matches the problem. Add `--json` only when structured error details help; compact errors are usually enough for interactive debugging.

## `icloud` command not found

Verify the package is installed globally:

```bash
npm install --global @amxv/icloud-cli@latest
npm prefix --global
```

Then open a new shell or make sure the npm global binary directory is on `PATH`.

## The native executable is missing or will not run

Reinstall the current package:

```bash
npm install --global @amxv/icloud-cli@latest
icloud version
```

If you built from source, rebuild with:

```bash
make build
./dist/icloud version
```

## Account setup asks for a password in automation

Use an explicit non-interactive source:

```bash
printf '%s\n' "$ICLOUD_APP_PASSWORD" | \
  icloud account add \
    --name Personal \
    --email you@icloud.com \
    --password-stdin
```

Or configure `--credential-env` / `--credential-file`. There is no plaintext `--password` flag.

## Authentication fails

Use an Apple **app-specific password**, not your normal Apple Account password.

```bash
icloud account credential show Personal
icloud account test Personal
```

If you rotated the password:

```bash
printf '%s\n' "$NEW_PASSWORD" | \
  icloud account credential replace Personal --password-stdin

icloud account test Personal
```

## The keyring is unavailable

For a headless environment, use an environment or protected-file credential reference instead of requiring a desktop keyring:

```bash
icloud account credential replace Personal \
  --credential-env ICLOUD_APP_PASSWORD
```

## I have several accounts and the CLI will not choose one

List them:

```bash
icloud account list
```

Select one explicitly:

```bash
icloud --account Work mail messages list
```

Or set a default:

```bash
icloud account default Work
```

## Folder list is empty or outdated

```bash
icloud mail folders refresh
icloud mail folders list
```

`folders list` shows the saved view; `folders refresh` is the command that checks the current mailbox tree.

## Archive, Trash, Junk, Drafts, or Sent cannot be resolved

Inspect folders and role bindings:

```bash
icloud mail folders refresh
icloud mail folders role list
```

If the intended special folder is known, bind it locally:

```bash
icloud mail folders role set archive fld_… --dry-run
icloud mail folders role set archive fld_…
```

See [Folders](/docs/folders).

## Inbox looks stale

Run a sync:

```bash
icloud mail sync --folder inbox
```

If several bounded pages remain:

```bash
icloud mail sync --folder inbox --until-current
```

Check status when progress looks unexpected:

```bash
icloud mail sync status --folder inbox
```

## Older mail is missing

Normal sync focuses on current mail. Fetch older history deliberately:

```bash
icloud mail sync backfill --folder inbox --until-complete
```

Add `--after` / `--before` if you only need a time range.

## Search does not find a newly arrived message

First sync, then search locally:

```bash
icloud mail sync
icloud mail search 'expected words'
```

Or make a fresh provider search:

```bash
icloud mail search 'expected words' --remote
```

If local indexing reports incomplete content:

```bash
icloud mail search repair
```

## A remote search result cannot be read later

`rem_…` is a temporary provider reference. Search again if it is stale, then cache the chosen result:

```bash
icloud mail message show rem_… --remote --cache --full
```

Use the returned `msg_…` ID for durable later steps.

## Complete message text is unavailable

Inspect what is stored:

```bash
icloud mail message show msg_… --verbose
```

Then try normal synchronization/repair:

```bash
icloud mail sync
icloud mail sync repair --check
```

Do not fabricate missing local message files.

## Attachment save says the destination exists

Choose another path, or deliberately overwrite a regular file:

```bash
icloud mail attachments save att_… --output invoice.pdf --force
```

## A message action says the location is ambiguous

The same logical message may exist in several folders. Choose the exact source:

```bash
icloud mail move msg_… --from inbox --to Projects --dry-run
icloud mail mark-read msg_… --folder inbox
```

Do not guess which copy the user intended.

## Archive or move failed after a network problem

Inspect the recorded mutation and current mailbox state before repeating a different request:

```bash
icloud mail operations mutations list
icloud mail message show msg_… --metadata-only
icloud mail sync --folder inbox
```

If you repeat the original operation, reuse its original idempotency key.

## Sending requires confirmation

Interactive sends ask for confirmation. Non-interactive sends require explicit authorization:

```bash
icloud mail send ... --dry-run
icloud mail send ... --yes
```

For automation, use a stable idempotency key as well.

## A send failed or the result is unknown

Do **not** immediately issue a new send.

```bash
icloud mail operations outbound show send_…
icloud mail sync --reconcile-outbound
```

Then inspect again. If an item is in the safe-retry Outbox:

```bash
icloud mail outbox list
icloud mail outbox retry out_… --dry-run
```

See [Outbox and send recovery](/docs/outbox-and-send-recovery) for the difference between failed, safe-retry, and unknown sends.

## A reply was sent but does not look Answered

Do not resend the reply. Reconcile only the original message state:

```bash
icloud mail operations outbound reconcile-answer send_…
```

## A folder rename or delete was interrupted

Refresh before issuing another structural change:

```bash
icloud mail folders refresh
icloud mail folders show <folder>
```

Then decide from the current provider-visible folder tree rather than the earlier command's error alone.

## A draft is not visible in Apple Mail

Local draft creation does not automatically upload the draft. Sync it deliberately:

```bash
icloud mail draft sync drf_…
```

If Drafts is not recognized, check [Folders](/docs/folders).

## An import stopped partway through

For mboxrd imports, rerun the same archive and destination; the import keeps resumable progress:

```bash
icloud mail mailbox import archive.mbox --to Archive --max-messages 100 --yes
```

For an uncertain single EML import, reuse the same normalized request and idempotency key rather than creating a fresh import identity.

## Local state is busy

Another `icloud` process may be updating shared state. Let it finish and retry. Do not delete lock files to bypass an active process.

If a long-running watcher is using the same state and you need a clean backup, stop it with Ctrl-C first.

## The local state directory is not writable

Check the selected root:

```bash
printf '%s\n' "$ICLOUD_HOME"
icloud --home ./test-state account list
```

Use a directory owned by the current user. Do not place Mail state inside a read-only checkout or shared public directory.

## I want to check local consistency

```bash
icloud mail sync repair --check
```

For detail:

```bash
icloud mail sync repair --check --verbose
```

Apply only the deterministic repairs it offers:

```bash
icloud mail sync repair --apply
```

## I need more diagnostic detail

Useful escalation commands are:

```bash
icloud doctor --verbose
icloud doctor --live --verbose
icloud mail settings show --verbose
icloud mail message show msg_… --verbose
icloud mail sync repair --check --verbose
```

For exact flags or a stable machine-readable error, use the [Command reference](/docs/command-reference) and [Output and automation](/docs/output-and-automation).
