---
title: Quickstart
description: Install icloud, configure an app-specific password safely, validate the account, preview and send mail deliberately, synchronize folders, search or read offline, save content, and update message state explicitly.
order: 1
category: Start
summary: The shortest safe path from installation to a validated account, deliberate outbound mail, synchronized Inbox and new Sent copies, local or explicit remote search, targeted hydration, content access, safe attachment export, and location-aware state updates.
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
schema=19
account_mode=validated_lifecycle
credentials=keyring,env,file
network_validation=true
diagnostics=true
mail_folders=true
mail_sync=true
mail_content=true
mail_attachments=true
mail_flags=true
mail_threads=true
mail_search=true
mail_remote_search=true
mail_hydration=true
mail_send=true
mail_reply=true
mail_forward=true
mail_location_mutations=true
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

## Record alternate sender identities locally

Every account has one protected `MailIdentity` matching its configured email address. Inspect it without contacting iCloud:

```bash
icloud mail identities list
```

Record an alias or custom-domain address you already control:

```bash
icloud mail identities add --address alias@icloud.com --kind icloud_alias
```

User-entered identities are labeled `verification=unverified` until a sending workflow proves provider acceptance. They reuse the account's existing credential rather than creating another mailbox profile. See [Manage sender identities](/docs/manage-sender-identities) for kinds, local defaults, enabled state, pagination, and protected-primary rules.

## Preview and send a message deliberately

Review the normalized request without contacting SMTP or creating an attempt:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt \
  --attach report.pdf \
  --dry-run
```

In a terminal, rerun without `--dry-run` and confirm the prompt. An agent or redirected process must authorize the real send explicitly and should use an idempotency key:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt \
  --attach report.pdf \
  --idempotency-key project-update-2026-08-06 \
  --yes
```

Success returns only stable attempt, message, and thread IDs plus attachment counts and reconciliation state. BCC addresses remain in the SMTP envelope and are omitted from the composed message. An `unknown` result means acceptance could not be proved either way and must not be retried automatically. Run `icloud mail sync --reconcile-outbound` before considering an explicit linked retry. Read [Send mail safely](/docs/send-mail-safely) for text/HTML input, attachments, display names, Reply-To, custom headers, dry-run expansions, idempotency, Sent confirmation, and recovery.

## Save work as a draft before sending

Create a draft locally first when the message is not ready to leave the machine:

```bash
icloud mail draft create \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt
```

`draft create` and `draft update` do not contact iCloud. Upload the current revision only when you want it available in iCloud Mail:

```bash
icloud mail draft sync drf_…
```

If `icloud mail folders refresh` shows the real provider Drafts mailbox as `kind=other`, iCloud did not advertise a Drafts role. Keep that classification truthful. You can select the mailbox explicitly for one workflow, or bind its stable ID once so ordinary Drafts commands can resolve it without changing provider metadata:

```bash
icloud mail draft sync drf_… --folder Drafts
icloud mail folders role set drafts fld_… --dry-run
icloud mail folders role set drafts fld_…
```

Update locally, sync the replacement, then preview the exact saved revision before sending:

```bash
icloud mail draft update drf_… --to client@example.com --subject 'Project update' --body-file revised.txt
icloud mail draft sync drf_…
icloud mail draft send drf_… --dry-run
icloud mail draft send drf_… --yes
```

Replacement uploads keep the previous recoverable provider copy until the new revision is confirmed. A proven send removes known provider draft copies; an unknown SMTP outcome keeps the draft instead of guessing that it is safe to delete or resend. The [Send mail safely](/docs/send-mail-safely) guide covers listing, full-content review, attachments, BCC privacy, cleanup recovery, and deletion.

Reply or forward only after selecting the exact local message ID you intend:

```bash
icloud mail reply msg_… --body-file reply.txt --dry-run
icloud mail forward msg_… --to teammate@example.com --body 'For context' --dry-run
```

Reply preserves RFC relationship headers when the selected parent has usable identity. Forward always starts a new conversation: quoted decoded text is the default, exact EML requires `--original eml`, and original attachments are included only through explicit `--include-attachment att_…` selections. Both workflows reuse the same confirmation, idempotency, attachment, and Sent-reconciliation safeguards.

## Discover folders

```bash
icloud mail folders refresh
```

This saves the current mailbox tree and counts. It is required before message sync so the CLI can use confirmed provider folder identities rather than guessing.

Read the saved inventory offline:

```bash
icloud mail folders list
```

See [Manage folders and sync policy](/docs/folder-discovery-and-sync-policy) for safe create/rename/delete, nested and Unicode names, provider roles, interrupted-rename recovery, and optional sync eligibility.

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

Custom folders begin disabled. Create and opt one into normal sync deliberately:

```bash
icloud mail folders create 'Projects/Client' --dry-run
icloud mail folders create 'Projects/Client' --yes
icloud mail folders enable 'Projects/Client'
icloud mail sync --folder 'Projects/Client'
```

Use `icloud mail sync --all-folders --limit 20` only when you explicitly want one bounded pass across every current selectable folder, including locally disabled ones. It starts unsynchronized folders at their newest bounded page; it does not crawl their older history.

## Keep new mail synchronized while you work

Once the first sync is healthy, leave a foreground watcher running:

```bash
icloud mail watch
```

The watcher uses IMAP IDLE for enabled Inbox and Sent when available, reports an explicit polling fallback otherwise, and periodically refreshes all enabled folders. Press Ctrl-C to stop cleanly. Use `icloud mail watch --all-accounts` for up to 10 configured accounts, or `--jsonl` when another process needs a typed event stream.

## Read compact previews offline

```bash
icloud mail messages list
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
icloud mail messages list --folder sent
```

The same logical `msg_…` record may contain separate Inbox and Sent locations. Each location retains its own UID and flags; an unseen Sent copy does not make outbound mail unread.

## Search the synchronized copy

Find text in locally synchronized subject, sender, participants, decoded message text, attachment metadata, or RFC Message-ID:

```bash
icloud mail search invoice
icloud mail search --from billing@example.com --unread
```

Search is offline, bounded, and does not mark mail read. Use `--result threads` when you want one hit per reconstructed conversation:

```bash
icloud mail search launch --result threads
```

When `more=true`, continue with the returned `next_cursor` and the same search. See [Search synchronized mail](/docs/search-synchronized-mail) for filters, JSON, cursor rules, and local index repair.

When you need a fresh iCloud check without moving the normal sync frontier, search explicitly:

```bash
icloud mail search invoice --remote --folder inbox
```

If provider search reports `more=true` or `partial=true`, continue from its `next_cursor` with the same scope. One-folder continuation resumes the next unscanned UID boundary without repeating newer windows:

```bash
icloud mail search invoice --remote --folder inbox --cursor rcur_…
```

All-folder provider search is resumable too. It pages the current selectable-folder inventory and maintains a bounded merge frontier across folders, with at most two provider mailbox operations per invocation. An early page can contain zero hits with `partial=true` while the CLI is still establishing globally ordered folder heads:

```bash
icloud mail search invoice --remote --all-folders --limit 20
icloud mail search invoice --remote --all-folders --limit 20 --cursor rcur_…
```

The result uses a stale-safe `rem_…` reference until you choose to cache it:

```bash
icloud mail message show rem_… --remote
icloud mail message show rem_… --remote --cache --full
```

Targeted caching returns stable local IDs and leaves the ordinary folder checkpoint unchanged. See [Search synchronized mail](/docs/search-synchronized-mail) for provider filter limits, remote cursor scope, bounded all-folder merge behavior, `partial`, and batch hydration.

## Browse reconstructed conversations

List the newest local conversations without contacting Apple:

```bash
icloud mail threads list
```

Open one by stable thread ID, or start from any message ID:

```bash
icloud mail threads show thr_…
icloud mail threads show msg_…
```

Threads are reconstructed locally from ordered RFC `References`, then `In-Reply-To`, with a narrow subject-and-participant fallback only when relationship headers are absent. Late roots can merge provisional conversations while retired thread IDs remain usable aliases. The compact default shows one aggregate plus chronological bounded message excerpts.

Expand content only when needed:

```bash
icloud mail threads show thr_… --full
icloud mail threads show thr_… --metadata-only --verbose --json
```

Read [Reconstructed conversations](/docs/read-reconstructed-conversations) for graph evidence, aliases, cycles, fallback boundaries, and multi-message content modes.

## Read one complete message explicitly

Copy a stable `msg_…` ID from `mail messages list`, then inspect a bounded message block:

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

Use the stable message ID from `mail messages list`:

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

## Move or copy one exact message location

Review the current message locations, then dry-run the exact provider change:

```bash
icloud mail message show msg_… --metadata-only
icloud mail move msg_… --from inbox --to Projects --dry-run
```

For a real non-interactive operation, add a stable idempotency key and `--yes`:

```bash
icloud mail move msg_… \
  --from inbox \
  --to Projects \
  --idempotency-key project-move-01 \
  --yes
```

`move` prefers atomic UID MOVE and otherwise uses only a safe exact-UID fallback. `copy` keeps the source. Archive, unarchive, trash, restore, and permanent delete are also location-scoped; they never mutate an entire reconstructed thread. Check `icloud mail operations mutations list` when a provider write becomes unresolved, then synchronize the exact affected folders instead of retrying automatically.

Read [Move, copy, archive, and delete mail](/docs/manage-mail-locations) before permanent deletion or when a message has several provider locations.

Inspect progress:

```bash
icloud mail sync status
icloud mail sync status --folder sent
icloud mail sync checkpoints
```

Normal synchronization never walks backward through old mailbox history. When you deliberately need older mail, use the separate resumable path:

```bash
icloud mail sync backfill --folder inbox --limit 50
```

Repeat while `complete=no`. The historical cursor does not move the normal new-mail checkpoint, so ordinary sync can continue at the same time.

When a saved forward frontier is no longer the starting point you want, inspect it before deliberately moving only that folder to the current provider tail:

```bash
icloud mail sync status --folder inbox
icloud mail sync rebaseline --folder inbox --current-tail
```

Rebaseline keeps cached messages; it does not claim skipped mail is synchronized. Use backfill later if you still want that older range.

For recovery, check before applying changes:

```bash
icloud mail sync repair --check
icloud mail sync repair --apply
```

The check is bounded, local, and non-mutating. Apply fixes only deterministic local/frontier state; ambiguous identity or missing canonical bytes remain visible instead of being guessed. Temporary staging-file cleanup requires the separate `--cleanup-orphans --yes` choice.

Read [Sync and read mail](/docs/sync-and-read-inbox) for enabled-folder selection, resumable history, current-tail rebaseline, repair, location selection, and recovery, [Move, copy, archive, and delete mail](/docs/manage-mail-locations) for provider location changes, or [Save mail attachments](/docs/save-mail-attachments) for filename, collision, batch, and binary-output rules.

## Select another account

```bash
icloud --account Work mail sync
icloud --account owner@icloud.com mail messages list
```

Without a selector, commands use the default account, then a sole account. The CLI never guesses between multiple non-default profiles.

## Use JSON deliberately

```bash
icloud --request-id inbox-01 mail messages list --json
```

JSON is opt-in. Redirecting stdout does not change the compact default. Passwords, credential references, BCC recipients, raw storage paths, and full bodies stay out of Inbox, default message JSON, and successful send JSON. A message body or raw payload appears only when the matching explicit content mode is requested. Dry-run send JSON is an intentional request review and therefore includes normalized recipients, BCC, subject, and bodies.

## Common next actions

- Credential or validation trouble: [Account validation and doctor](/docs/account-validation-and-doctor)
- Outbound preview, confirmation, idempotency, or an unknown result: [Send mail safely](/docs/send-mail-safely)
- Empty or unexpected folder inventory or an interrupted rename: [Manage folders and sync policy](/docs/folder-discovery-and-sync-policy)
- Interrupted Inbox, Sent, or other enabled-folder sync: [Sync and read mail](/docs/sync-and-read-inbox)
- Unexpected conversation grouping or a retired thread ID: [Reconstructed conversations](/docs/read-reconstructed-conversations)
- Attachment listing, export, or collision handling: [Save mail attachments](/docs/save-mail-attachments)
- Stable stdout, stderr, JSON, and exits: [Output and automation](/docs/output-and-automation)
- Every command and flag: [Command reference](/docs/command-reference)
