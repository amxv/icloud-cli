---
title: Troubleshooting
description: Resolve installation, account setup, credential, validation, outbound-send, state, message and attachment output, completion, and man-generation problems.
order: 10
category: Guides
summary: Common errors, privacy-safe diagnosis, and the next command to run.
---

## The native binary is missing

```text
icloud binary is not installed. Re-run: npm rebuild -g @amxv/icloud-cli
```

Run:

```bash
npm rebuild --global @amxv/icloud-cli
```

The installer first tries the matching native release asset, then a local Go build when source and Go are available.

## The command name is not found

Confirm the global npm binary directory is on `PATH`:

```bash
npm prefix --global
npm list --global @amxv/icloud-cli
```

The executable is `icloud`, not `icloud-cli`.

## An unknown command fails

```bash
icloud --help
icloud account --help
icloud account credential --help
```

Close misspellings include a compact suggestion. Help is generated from the live command tree.

## Credential input is required

A non-interactive account add or credential replacement never waits for an implicit prompt. Choose exactly one source:

```bash
printf '%s\n' "$ICLOUD_APP_PASSWORD" |
  icloud account add --name Personal --email owner@icloud.com --password-stdin

icloud account add --name Personal --email owner@icloud.com \
  --credential-env ICLOUD_APP_PASSWORD

icloud account add --name Personal --email owner@icloud.com \
  --credential-file "$HOME/.config/icloud-cli/personal-password"
```

Do not put the password in an argv flag. No `--password` flag is supported.

## The native keyring is unavailable

Headless Linux sessions may not have an unlocked Secret Service session. The command returns a configuration error without printing the attempted keyring key or password. Use an environment or file reference, or run the command in a desktop session with a working native keyring.

## A credential value is missing

Offline setup can persist an environment or file reference without resolving it. Before `account test` or `doctor --live`, verify that the selected process can read the external source:

```bash
read -rsp "App-specific password: " ICLOUD_APP_PASSWORD
printf '\n'
export ICLOUD_APP_PASSWORD
ls -l "$HOME/.config/icloud-cli/personal-password"
```

The CLI intentionally keeps the exact stored variable name or path out of normal output and errors. Use your setup record rather than expecting `credential show` to reveal it.

`account test` reports this as the redacted `credential` stage and stops before opening IMAP or SMTP.

## Live validation failed

First inspect stored health without making another network request:

```bash
icloud doctor Personal
```

Then use the stage from the error:

- `dns` or `connect` — verify connectivity, DNS, firewall policy, and the command timeout;
- `tls` — verify the system clock and trusted certificate roots; do not bypass verification;
- `imap_auth` — confirm the app-specific password and allow the CLI to try the persisted/full/short username forms;
- `imap_list` or `imap_status` — authentication worked but the read-only compatibility commands failed;
- `smtp_starttls` — the server did not provide the required encrypted submission path; authentication was not attempted in plaintext;
- `smtp_auth` — confirm the app-specific password and full iCloud email address;
- `timeout` — retry with a deliberate larger `--timeout` after checking for a stalled network path.

Run `icloud doctor Personal --live --json` when an agent needs a fresh structured report. Validation never sends a message, so a successful test does not prove delivery.

## Mail send requires authorization

`mail send --dry-run` never contacts SMTP and needs no confirmation. A real non-interactive or `--no-input` send requires `--yes`:

```bash
icloud mail send \
  --to client@example.com \
  --subject Update \
  --body-file message.txt \
  --dry-run

icloud mail send \
  --to client@example.com \
  --subject Update \
  --body-file message.txt \
  --idempotency-key update-01 \
  --yes
```

In a terminal, omitting `--yes` prints the normalized compact preview and asks `Send this message? [y/N]`. When `--body-file -` or `--html-body-file -` consumed stdin, the same stream cannot also answer the prompt. Review with `--dry-run`, then rerun with `--yes`.

Reply and forward use the same authorization boundary. A non-interactive `mail reply`, `reply-all`, or `forward` requires `--yes` after an exact-message dry run.

## A forward has the wrong original content or attachments

Forward never copies everything implicitly. Review the source representation and selection first:

```bash
icloud mail attachments list msg_…
icloud mail forward msg_… --to teammate@example.com --dry-run
```

The default is `--original quoted`. Use `--original eml` only for exact verified RFC822 bytes, or `--original none` with your own body. Add original files one at a time with `--include-attachment att_…`; each ID must belong to the selected source message. Use ordinary `--attach` for new local files.

If decoded quoted text is unavailable or exceeds the body bound, choose EML or omit the original. If an original attachment is unavailable or corrupt, resynchronize or repair that source before forwarding it. Forward does not mutate the source and does not join its reconstructed thread.

## Mail send reports an idempotency conflict

The same account and idempotency key were already used for a different normalized request. Do not change recipients, subject, body, Reply-To, headers, or the account identity while reusing a key.

Run the original command unchanged to retrieve its attempt without another SMTP transaction. Use a new key only for an intentionally new message.

## Mail send outcome is unknown

The CLI lost certainty after message data may have reached iCloud, so it cannot prove whether iCloud accepted the message. This error is deliberately `retryable=false` even when the underlying network problem was temporary.

Do not rerun with a new idempotency key immediately. Keep the returned `send_…` attempt ID, rerun the exact same command with the same key only to retrieve the original attempt, and verify the receiving mailbox or iCloud Sent mailbox before deciding whether another message is appropriate.

Run the bounded reconciliation workflow first:

```bash
icloud mail status --outbound
icloud mail sync --reconcile-outbound
icloud mail status --outbound
```

Only after checking both Sent and the recipient may you deliberately create a linked duplicate-risk retry with a new idempotency key, `--retry-of send_…`, `--retry-unknown`, and `--yes`.

## SMTP accepted the message but local completion is incomplete

The provider returned final acceptance, but the CLI could not finish the local searchable message/thread transaction. The durable attempt remains `sent` with `error_code=local_completion` so an idempotent replay cannot submit it again.

Do not resend. Preserve the `send_…` ID and run `icloud mail sync --reconcile-outbound` to restore the missing local linkage from exact Sent evidence.

## SMTP accepted the message but its Sent copy was not confirmed

The recipient submission succeeded, but the follow-up Sent copy or its local status update failed. The attempt remains `sent`; repeating the same idempotency key returns it without another SMTP submission.

Do not resend. Refresh folders if necessary, then reconcile:

```bash
icloud mail folders status
icloud mail sync --reconcile-outbound
icloud mail status --outbound
```

## Mail send failed before acceptance

Use the reported stage:

- `smtp_starttls` — the encrypted submission path was unavailable; authentication and message transfer did not proceed;
- `smtp_auth` — correct the app-specific password, then run `icloud account test`;
- `smtp_mail` or `smtp_recipient` — iCloud rejected the envelope before message data;
- `smtp_data` or a final explicit SMTP rejection — the server proved that the attempt was not accepted;
- `timeout` before DATA — check connectivity and choose a deliberate larger global timeout when justified.

A `failed` attempt is not automatically retried. Correct the cause, preserve its `send_…` ID, review the same normalized message again, and use `--retry-of send_…` with a new idempotency key for the deliberate new attempt.

## An outbound attachment was rejected before SMTP

The CLI accepts only readable regular files and rechecks reviewed bytes before sending. Missing paths, directories, symbolic or other special files, changed files, invalid filename/media-type overrides, cache failures, and per-file/count/total bounds stop before SMTP.

The complete MIME message must also fit the 20 MiB iCloud Mail limit after base64 and multipart overhead. Reduce the files or body when the source files appear smaller than the reported composed-message limit. The CLI does not invoke Mail Drop.

## An account already exists

Friendly names and email addresses are unique, case-insensitively, within one state root:

```bash
icloud account list
```

Rename the existing profile or choose a different name/email. A failed duplicate keyring setup removes the newly attempted keyring entry before returning the conflict.

## Account selection is ambiguous

Set a default or pass a selector:

```bash
icloud account default Personal
icloud --account owner@icloud.com account show
```

The CLI never guesses between multiple accounts without a default.

## The default account cannot be removed

Choose another default first:

```bash
icloud account default Work
icloud account remove Personal --yes
```

This prevents a multi-account state from losing its deterministic selection target.

## Removal or deletion requires authorization

Account removal and credential deletion require `--yes`:

```bash
icloud account remove Old --yes
icloud account credential delete Personal --yes
```

Without it, the command exits with the safety class before mutating state.

## Account commands created files

That is expected. The first state-bearing command creates or upgrades the selected application root. Use an isolated location when appropriate:

```bash
ICLOUD_HOME=./icloud-state icloud account list
```

Version, capabilities, completion, man, and help do not create state merely because `--home` is present.

## Local state is busy

Another process is upgrading state or mutating accounts. The command returns exit `6` with `retryable=true`. Let the other process finish and retry; do not delete lock files to bypass it.

```bash
icloud --timeout 5s account list
```

## The selected state directory is not writable

Choose a directory owned by the current user:

```bash
icloud --home ./icloud-state account list
```

Errors avoid printing private absolute paths. Do not place mail state in a shared or published directory.

## JSON appeared on stderr

Structured errors use stderr when `--json` was requested. Check the process exit code before parsing stdout as a success envelope.

## A timeout or request ID is rejected

Use a positive Go-style duration and a request ID containing only letters, numbers, dots, underscores, or hyphens:

```bash
icloud --timeout 30s --request-id account-check-01 account list --json
```

## Completion or man output is not active

Generate fresh assets from the installed command tree:

```bash
icloud completion zsh > /tmp/_icloud
icloud man > icloud.1
icloud man --output-dir ./man
```

Generating completion output does not install it automatically.

## A local command is not contacting iCloud

That is expected for `account add`, credential replacement, account reads, local folder `list`/`show`, folder policy changes, outbound status, and `doctor` without `--live`. Use `icloud account test`, `icloud doctor --live`, `icloud mail folders status`, `icloud mail sync`, or a real `icloud mail send`, `reply`, or `forward` for explicit network work. Folder sync and outbound status never resend a message; move, copy, archive, trash, and delete are not implemented yet.

## Folder list is empty

`icloud mail folders list` reads local state only. Run one live discovery first:

```bash
icloud mail folders status
```

If that fails, use the returned credential, authentication, timeout, TLS, LIST, or STATUS stage to choose the next action. The command does not fetch messages.

## A folder says subscription unknown

That is truthful when iCloud does not expose subscription metadata through the LIST command it accepts. Unknown does not mean unsubscribed and does not prevent local future-sync policy from being enabled.

## Archive or Drafts says kind other

The provider did not advertise a corresponding special-use attribute. The CLI deliberately does not infer Archive, Drafts, Trash, or Junk roles from an English display name. Select the folder by stable ID or name and enable it explicitly when future synchronization should include it.

## A removed folder still appears

Live folder status retains absent provider folders as `present=no stale=true` so identity and policy are not silently lost. Normal `folders list` hides retained missing records; add `--include-missing` to inspect them.


## Sync says the folder is not discovered

Run a folder refresh, then retry:

```bash
icloud mail folders status
icloud mail sync --folder sent
```

The CLI requires a confirmed, selectable provider identity before fetching messages. The selected folder must also be enabled.

## Sync failed without advancing

Inspect the saved frontier:

```bash
icloud mail sync status --folder sent
```

`state=failed` means the page was not committed and `committed` remains safe to retry. Use a larger `--timeout` only after checking the reported IMAP stage and connectivity.

## A folder sync requires repair

`state=repair_required` means iCloud returned a different mailbox identity. Ordinary sync stops before resetting or combining message locations. Keep the state directory intact; a dedicated repair command is introduced in a later workflow.

## The first Sent sync fetched zero messages

That is expected and safe. Sent setup records the current remote tail without downloading old Sent history:

```bash
icloud mail sync status --folder sent
```

After a new Sent copy is created, run `icloud mail sync --folder sent` again. Only copies newer than the recorded tail are ingested.

## The Inbox command does not contact iCloud

That is intentional. `icloud mail inbox` reads the latest locally committed page. Run `icloud mail sync` first when new remote mail is expected.

Use `icloud mail inbox --folder sent` to read locally synchronized Sent copies. It also stays offline.

## Search does not show a new remote message

Search reads synchronized local state only. Synchronize the relevant folder, then repeat the same query:

```bash
icloud mail sync --folder inbox
icloud mail search invoice
```

Use `--folder sent` or another confirmed folder selector when the expected copy is not in Inbox.

For a fresh provider check without advancing sync, use explicit remote search:

```bash
icloud mail search invoice --remote --folder inbox
```

Remote search is bounded and is not local FTS parity. If it reports `partial=true`, narrow the folder, date range, sender, subject, or RFC Message-ID. Attachment metadata and reconstructed-thread filters require synchronization or targeted hydration first.

## A remote reference is stale

A `rem_…` reference is bound to one account, folder, UIDVALIDITY, and UID. If the mailbox identity changed, the command returns conflict before fetching an unrelated reused UID.

Refresh folder status and repeat the remote search:

```bash
icloud mail folders status
icloud mail search invoice --remote --folder inbox
```

Do not edit or reuse an older remote reference after a UIDVALIDITY change.

## Remote content requires caching

An uncached remote summary intentionally does not fetch or print complete bodies, HTML, headers, raw MIME, or MIME details. Cache through the normal local ingestion path:

```bash
icloud mail message show rem_… --remote --cache --full
```

For several selected results:

```bash
icloud mail sync --hydrate rem_…,rem_…
```

Hydration does not advance the ordinary folder checkpoint. Repeating the same reference reports `cached`.

## Search reports a pending or error index

Metadata and previews remain searchable while some decoded bodies are incomplete. Rebuild a bounded local batch:

```bash
icloud mail search repair
icloud mail search repair --limit 100 --json
```

The repair command uses already synchronized local message bytes and does not contact iCloud. Repeat while `remaining` is nonzero. When `errors` remains nonzero, inspect local message availability and the relevant sync frontier before attempting manual cleanup:

```bash
icloud mail sync status --folder inbox
icloud mail message show msg_… --metadata-only --verbose --json
```

Keep the state directory intact so stable IDs and repair evidence remain available.

## A search cursor is rejected

A cursor belongs to the exact account, text, filters, result kind, and sort of the page that returned it. Repeat the original command unchanged:

```bash
icloud mail search invoice --limit 50 --cursor cur_…
```

Changing `--folder`, `--result`, dates, state filters, or the selected account requires a fresh first page.

## Message show returns not found

Use a stable ID from the same selected account:

```bash
icloud mail inbox
icloud --account Personal mail message show msg_…
```

Message IDs are account-scoped. The CLI does not search another account or contact iCloud when an ID is missing.

## Complete text or HTML is unavailable

Inspect the bounded metadata and MIME-part view:

```bash
icloud mail message show msg_…
icloud mail message show msg_… --verbose
```

Some messages contain only text or only HTML. `--format text` falls back to readable text derived from HTML; `--format html` requires a stored HTML body. Attachments and embedded messages are not treated as the parent message body.

## Attachment list returns not found

Use a stable message ID from the same selected account:

```bash
icloud mail inbox
icloud --account Personal mail attachments list msg_…
```

Attachment IDs are also account-scoped. The CLI does not search another account or contact iCloud when a message or attachment ID is missing.

## Binary attachment stdout was rejected

Native attachment bytes require an explicit acknowledgement:

```bash
icloud mail attachments save att_… --output - --binary > attachment.bin
```

Without `--binary`, the command exits before writing stdout. Do not combine native stdout with `--json`.

## Attachment export refuses a destination

An existing regular file requires `--force`:

```bash
icloud mail attachments save att_… --output attachment.bin --force
```

Directories, symbolic links, and other special files are always rejected. For `save-all`, resolve unexpected destination files or use `--force` only when every conflicting path is a regular file that may be replaced. The batch is prepared before installation, so a failure does not intentionally leave a partial set.

## An attachment is unavailable or corrupt

Inspect its metadata without requesting bytes:

```bash
icloud mail attachments list msg_…
icloud mail message show msg_… --verbose
icloud mail sync status
```

A missing or corrupt decoded attachment fails before a successful output file is installed. Keep the local state intact and do not replace cached content manually. Stable message, MIME-part, and attachment IDs remain available for the later repair workflow.

## A read or flag command says the location is ambiguous

The message has more than one eligible current provider location. Inspect the local metadata and choose one exact folder:

```bash
icloud mail message show msg_… --metadata-only --json
icloud mail mark-read msg_… --folder INBOX
icloud mail flag msg_… --folder fld_…
```

Folder selectors can be a stable ID, decoded name, exact provider wire name, or unambiguous kind. The CLI never guesses and has no implicit all-locations mode.

## A message has no eligible current location

The synchronized copies are absent, deleted, stale, or in a folder that is no longer current and selectable. Refresh discovery and the relevant folder state first:

```bash
icloud mail folders status
icloud mail sync --folder sent
icloud mail message show msg_… --metadata-only --json
```

Do not use an old UID from logs or JSON directly. State mutations resolve only from current local location identity.

## A flag update reports a concurrent change

iCloud changed the selected location after the local state was read. The CLI persists the current server-returned flags and returns conflict instead of claiming the requested state was applied. Read the message again and retry only when the new state still needs changing:

```bash
icloud mail message show msg_… --metadata-only --json
icloud mail mark-unread msg_… --folder INBOX
```

When the server exposes a mod-sequence, the CLI uses it for conditional updates. Otherwise it verifies the final state with a targeted UID read. Neither path searches another mailbox; it updates only the selected synchronized location.

## Sync reports reconciliation but no new messages

That is expected. `fetched=0` means the selected folder's new-message frontier did not move; `reconciled` counts existing current locations whose flags were checked, while `state_changes` counts real remote differences. The bounded sweep rotates by oldest check time, so repeated syncs eventually cover older local locations without re-downloading bodies.

## Two similar messages were not merged

The CLI requires strong identity or a uniquely safe local fallback before it joins locations under one `msg_…` ID. Matching subject, body, attachments, and time are not enough to collapse two provider-backed messages. Inspect `mail inbox --folder <folder> --json` or `mail message show <id> --metadata-only --json`; unresolved identity conflicts remain visible instead of deleting one record.

## A conversation is missing its root

The reply may have arrived before the root, or the root may be in another folder that has not been synchronized. Inspect local RFC graph evidence:

```bash
icloud mail thread show msg_… --metadata-only --verbose --json
```

An `unresolved` token is preserved. Synchronize the likely folder and retry. If the matching root arrives later, it joins the existing conversation without changing message, location, part, attachment, or blob IDs.

## An old thread ID redirects

Late RFC evidence can bridge provisional conversations. One existing thread ID remains canonical and retired IDs become durable aliases:

```bash
icloud mail thread show thr_old…
```

The resolved result reports `redirected_from`. Store the canonical ID for new work, but existing references to the alias remain valid.

## Similar subjects stayed in separate conversations

That is intentionally conservative. Subject-and-participant fallback is used only when RFC relationship headers are absent, normalized participants match, a reply-like subject exists, and the messages are close enough in time. Duplicate subjects alone do not establish a conversation.

## Thread evidence reports malformed or cycle

The CLI keeps the messages readable and records the relationship as `malformed`, `self`, or `cycle`. It does not invent a parent or rewrite RFC headers. Use `mail thread show <id> --metadata-only --verbose --json` to inspect the evidence and keep local state intact for the later repair workflow.

## Raw message content is missing or corrupt

`mail message raw`, raw format, and export verify the local blob's recorded size and SHA-256 address before returning bytes. An unavailable/corrupt error means the metadata remains present but the exact local RFC822 bytes cannot be trusted.

Do not replace the blob manually or resynchronize the whole mailbox blindly. Keep the state directory intact, inspect `icloud mail sync status`, and wait for the targeted repair workflow. Stable message and location IDs remain unchanged.

## Export refuses an existing file

This is the safe default:

```bash
icloud mail message export msg_… --output message.eml --force
```

`--force` is required to replace an existing regular file. Directories and special files are rejected. The command writes a private temporary file first and does not echo the destination path in normal output.

## Raw JSON is too large

Body/raw content in JSON is deliberately bounded. Use a streaming mode instead:

```bash
icloud mail message raw msg_… > message.eml
icloud mail message show msg_… --full > message.txt
```

Default message JSON remains available regardless of body size because it contains availability and bounded metadata rather than the payload.
