---
title: "Local data and backups"
description: "Understand where synchronized Mail is stored, choose an isolated data directory, back it up safely, and keep account secrets separate."
order: 62
category: Reference
summary: "The few local-storage details users actually need for portability, privacy, backup, and recovery."
---

`icloud` keeps synchronized Mail and its working state on your machine. You normally do not need to inspect those files directly.

## Choose a custom data directory

For a disposable test or an agent-owned workspace:

```bash
ICLOUD_HOME=./icloud-state icloud account list
```

Or use the global flag:

```bash
icloud --home ./icloud-state account list
```

`--home` wins over `ICLOUD_HOME` for that command.

## Default locations

When neither override is used, the CLI follows the platform's application-data convention:

- macOS: `~/Library/Application Support/icloud-cli`
- Linux/BSD: `${XDG_DATA_HOME:-~/.local/share}/icloud-cli`
- Windows: the user's application-data directory under `icloud-cli`

Keep this directory private and out of source repositories.

## What the local data contains

It can contain:

- account metadata and health;
- synchronized Mail content and attachments;
- search and conversation indexes;
- sync progress;
- drafts;
- signatures and local Mail preferences;
- outbound and mutation recovery records;
- public S/MIME material and references to private keys.

It does **not** store your app-specific password as plaintext. Keyring secrets remain in the operating-system credential store; environment/file credential sources remain where you configured them.

## Back up the local Mail copy

Before a file-level backup:

1. stop `icloud mail watch`;
2. let any sync, import, send, draft sync, repair, or mail-changing command finish;
3. copy or snapshot the **entire** selected data directory as one unit.

Do not copy only one database file or only the content directory and expect a complete restore.

## Restore

While no `icloud` process is using the target directory, restore the complete saved directory to the same location (or point `ICLOUD_HOME` / `--home` at it).

Then check it locally:

```bash
icloud account list
icloud doctor
icloud mail sync repair --check
```

Run a live account test when you are ready to validate the external credential again:

```bash
icloud account test
```

## External credentials and S/MIME keys

Back up any external credential file or S/MIME private-key file separately using your normal secret-management process. Native keyring items follow the backup/recovery behavior of your operating system.

## Large-message limits

The default inbound ceiling is 64 MiB per raw message and per decoded attachment. Advanced deployments can override the byte limits:

```bash
export ICLOUD_MAX_RAW_MESSAGE_BYTES=67108864
export ICLOUD_MAX_ATTACHMENT_BYTES=67108864
```

Check the effective values with:

```bash
icloud capabilities --json
```

Values must be between 1 byte and 1 GiB. Most users should leave the defaults unchanged.

## Do not edit the state by hand

If synchronized content or indexes appear inconsistent, use:

```bash
icloud mail sync repair --check
```

and, when the check recommends deterministic fixes:

```bash
icloud mail sync repair --apply
```

Manual edits to the database or stored message files can make otherwise recoverable state ambiguous.
