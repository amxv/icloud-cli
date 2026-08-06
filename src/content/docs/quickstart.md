---
title: Quickstart
description: Install icloud, configure an app-specific password safely, validate the account, discover folders, synchronize Inbox and Sent safely, read or save content, and update message state explicitly.
order: 1
category: Start
summary: The shortest safe path from installation to a validated account, locally readable Inbox and new Sent copies, explicit content access, safe attachment export, and location-aware read or flag updates.
---

## Install and inspect the binary

```bash
npm install --global @amxv/icloud-cli
icloud version
icloud capabilities
```

From a source checkout:

```bash
make build
./dist/icloud version
```

Representative output:

```text
version
0.1.0

capabilities
command=icloud
version=0.1.0
schema=8
account_mode=validated_lifecycle
credentials=keyring,env,file
network_validation=true
diagnostics=true
mail_folders=true
mail_sync=true
mail_content=true
mail_attachments=true
mail_flags=true
default_output=compact
json=true
completion=bash,zsh,fish,powershell
man=true
```

## Choose private local state

The platform default is suitable for ordinary use. For an isolated session or agent job:

```bash
export ICLOUD_HOME="$PWD/icloud-state"
icloud account list
```

A new state returns:

```text
accounts
count=0
```

Keep this directory private. It contains account metadata, the local mail database, raw synchronized messages, and locks.

## Add an iCloud Mail account

Use an app-specific password, never the primary password for your Apple Account.

To store it in the native keyring without placing it in argv:

```bash
read -rsp "App-specific password: " ICLOUD_APP_PASSWORD
printf '\n'
printf '%s\n' "$ICLOUD_APP_PASSWORD" |
  icloud account add \
    --name Personal \
    --email owner@icloud.com \
    --password-stdin
unset ICLOUD_APP_PASSWORD
```

Expected shape:

```text
account-added
acct_… status=pending_validation
default=true
credential=keyring
```

For a headless process, keep the value in an environment variable or protected file and store only its reference:

```bash
icloud account add \
  --name Agent \
  --email agent@icloud.com \
  --credential-env ICLOUD_AGENT_APP_PASSWORD
```

Read [Account setup and credentials](/docs/account-setup-and-credentials) before rotating credentials or using file references.

## Validate IMAP and SMTP safely

```bash
icloud account test Personal
```

```text
account-test
acct_… status=connected
imap=ok
smtp=ok
imap_username=owner
mailboxes=6
inbox_messages=42
inbox_unseen=3
checked_at=2026-08-06T03:00:00Z

account-tests
count=1
healthy=1
```

Validation authenticates to both mail protocols but does not send, fetch, move, or mark a message. It also remembers the working full-email or short iCloud IMAP username.

Inspect saved health later without another network request:

```bash
icloud doctor
```

## Discover folders

```bash
icloud mail folders status
```

This saves the current mailbox tree and counts. It is required before message sync so the CLI can use confirmed provider folder identities rather than guessing.

Read the saved inventory offline:

```bash
icloud mail folders list
```

See [Folder discovery and sync policy](/docs/folder-discovery-and-sync-policy) for nested and Unicode names, missing folders, provider roles, and optional sync eligibility.

## Synchronize enabled mail folders

```bash
icloud mail sync
```

The command processes Inbox and Sent by default because both begin enabled. Inbox downloads one bounded current-tail page into private local state:

```text
mail-sync
fld_… account=acct_…
folder=INBOX
fetched=20
created=20
updated=0
last_uid=184
more=true
bootstrap=true
```

Sent setup prints a second `mail-sync` block with `fetched=0` and its current `last_uid`. That is intentional: the CLI does not crawl old Sent history. Later runs ingest Sent copies created after that tail.

Repeat a folder while `more=true`. Later runs fetch only newer messages. A failed page leaves that folder's previous frontier intact and is safe to retry.

Select one folder explicitly when useful:

```bash
icloud mail sync --folder inbox
icloud mail sync --folder sent
icloud mail sync --folder fld_…
```

## Read compact previews offline

```bash
icloud mail inbox
```

```text
message
msg_… from=sender@example.com
subject="Project update"
at=2026-08-06T08:30:00Z
unread=true
attachments=1
snippet="Here is the latest update…"

inbox
account=acct_…
count=1
more=false
```

The command reads local state only and never marks messages read. It intentionally returns only bounded previews.

Read locally synchronized Sent copies with:

```bash
icloud mail inbox --folder sent
```

The same logical `msg_…` record may contain separate Inbox and Sent locations. Each location retains its own UID and flags; an unseen Sent copy does not make outbound mail unread.

## Read one complete message explicitly

Copy a stable `msg_…` ID from `mail inbox`, then inspect a bounded message block:

```bash
icloud mail message show msg_…
```

The default includes routing, time, state, locations, attachment count, available content kinds, a bounded excerpt, and a hint for explicit expansion. It never dumps a complete body or raw MIME accidentally.

Choose the content you need:

```bash
icloud mail message show msg_… --full
icloud mail message show msg_… --format html --body-only
icloud mail message show msg_… --headers
icloud mail message raw msg_… > message.eml
icloud mail message export msg_… --output message.eml
```

`--full` and `--format text` stream complete decoded text. HTML, headers, metadata-only, MIME-part detail, and raw RFC822 are separate opt-ins. Raw and exported bytes are verified against the local content address before they are returned.

## Save attachments explicitly

List metadata first, then save by stable attachment ID:

```bash
icloud mail attachments list msg_…
icloud mail attachments save att_… --output document.pdf
icloud mail attachments save-all msg_… --dir ./message-attachments
```

Attachment filenames are treated as untrusted metadata. Existing files are not replaced without `--force`, binary stdout requires `--binary`, and a batch is prepared before installation so failures do not leave an intentional partial set.

## Change read or flag state deliberately

Use the stable message ID from `mail inbox`:

```bash
icloud mail mark-read msg_…
icloud mail flag msg_…
icloud mail mark-unread msg_…
icloud mail unflag msg_…
```

The command updates one exact current provider location and then stores the state returned by iCloud. When a message has several eligible locations, choose one explicitly:

```bash
icloud mail mark-read msg_… --folder INBOX
```

These commands do not search every mailbox or mutate every copy of a message. Choose `--folder sent` only when that exact synchronized Sent location should change. A later `mail sync` refreshes bounded location state without downloading message bodies again.

Inspect progress:

```bash
icloud mail sync status
icloud mail sync status --folder sent
icloud mail sync checkpoints
```

Read [Sync and read mail](/docs/sync-and-read-inbox) for enabled-folder selection, history-free Sent setup, incremental state reconciliation, location selection, and recovery, or [Save mail attachments](/docs/save-mail-attachments) for filename, collision, batch, and binary-output rules.

## Select another account

```bash
icloud --account Work mail sync
icloud --account owner@icloud.com mail inbox
```

Without a selector, commands use the default account, then a sole account. The CLI never guesses between multiple non-default profiles.

## Use JSON deliberately

```bash
icloud --request-id inbox-01 mail inbox --json
```

JSON is opt-in. Redirecting stdout does not change the compact default. Passwords, credential references, BCC recipients, raw storage paths, and full bodies stay out of Inbox and default message JSON. A message body or raw payload appears in JSON only when the same explicit content mode is requested.

## Common next actions

- Credential or validation trouble: [Account validation and doctor](/docs/account-validation-and-doctor)
- Empty or unexpected folder inventory: [Folder discovery and sync policy](/docs/folder-discovery-and-sync-policy)
- Interrupted Inbox, Sent, or other enabled-folder sync: [Sync and read mail](/docs/sync-and-read-inbox)
- Attachment listing, export, or collision handling: [Save mail attachments](/docs/save-mail-attachments)
- Stable stdout, stderr, JSON, and exits: [Output and automation](/docs/output-and-automation)
- Every command and flag: [Command reference](/docs/command-reference)
