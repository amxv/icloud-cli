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

The installer first downloads the matching native release asset and verifies it against the tagged release checksum. If the download or verification fails, it can use a local Go build when source and Go are available.

If the warning reports a checksum mismatch, do not bypass it or copy the downloaded file into place manually. Re-run the install after confirming you are requesting the intended package version; if the release is incomplete, use the documented source-build path instead.

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

## A move, copy, or delete is unresolved

Do not repeat the provider operation automatically. An unresolved result means iCloud may have accepted COPY, MOVE, STORE, or EXPUNGE before the CLI received final confirmation.

```bash
icloud mail status --mutations
icloud mail message show msg_… --metadata-only
icloud mail sync --folder <source-folder>
icloud mail sync --folder <destination-folder>
```

After synchronization, inspect the current locations and decide whether a new deliberate action is needed. Reusing the original idempotency key returns the existing `mut_…` record without contacting iCloud. A `failed` mutation is different: no provider change was observed, so fix the authentication, stale UIDVALIDITY, missing folder, or location conflict before issuing a new request.

## Archive or Trash cannot be resolved

The shortcut commands rely on provider-advertised special-use roles. They do not guess that an English folder named `Archive` or `Trash` has that role.

```bash
icloud mail folders status --verbose
```

When the role is absent or stale, refresh folder discovery. Use explicit `move --from … --to …` only when the selected destination is intentional. Archive never moves a Sent location; a message with several eligible source locations requires `--from`.

## Permanent delete is unavailable

Permanent deletion requires safe exact UID EXPUNGE support before it changes the provider. The CLI never falls back to mailbox-wide EXPUNGE. Synchronize current capabilities and location state, then retry only as a new deliberate operation. Deleting one provider location does not purge the logical message while another location remains.

## Mail send reports an idempotency conflict

The same account and idempotency key were already used for a different normalized request. Do not change recipients, subject, body, Reply-To, headers, or the account identity while reusing a key.

Run the original command unchanged to retrieve its attempt without another SMTP submission. Use a new key only for an intentionally new message.

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

The provider returned final acceptance, but the CLI could not finish the local searchable message/thread update. The durable attempt remains `sent` with `error_code=local_completion` so an idempotent replay cannot submit it again.

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

## Mail watch is degraded or reconnecting

`icloud mail watch` reports provider/session failures as lifecycle events and uses bounded reconnect backoff instead of retrying in a tight loop. For automation, use `--jsonl` to read the error code and stage directly.

If the stage is `imap_auth`, replace or restore the selected account credential, then let the watcher reconnect. If the stream reports polling mode, IDLE is unavailable for that session; adjust `--poll-interval` only if you intentionally want a different fallback cadence. If synchronization reaches `repair_required` after a UIDVALIDITY change, keep the watcher from making identity decisions on your behalf and use the existing check-first recovery:

```bash
icloud mail sync repair --check --verbose
icloud mail sync repair --apply
```

A second foreground watch for the same account returns a retryable conflict. Stop the existing watcher rather than deleting its lock file. Ctrl-C stops active IDLE sessions cleanly and emits stopped/completed lifecycle events.

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

Structured errors use stderr when `--json` was requested. `mail watch --jsonl` likewise keeps initial or terminal failures as one typed JSONL error event on stderr. Check the process exit code before parsing stdout as a success envelope or event stream.

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

The npm package also contains generated `completions/` files and a complete `man/` tree. If those packaged assets are missing or disagree with `icloud --help`, reinstall the package rather than editing them in place.

## A local command is not contacting iCloud

That is expected for `account add`, credential replacement, account reads, local folder `list`/`show`, folder policy changes, outbound or mutation status, folder-administration dry-runs, outbound dry-runs, location-mutation dry-runs, and `doctor` without `--live`. Use `icloud account test`, `icloud doctor --live`, `icloud mail folders status`, a real `mail folders create|rename|delete|subscribe|unsubscribe`, `icloud mail sync`, a real outbound operation, or a real location mutation for explicit network work. Folder sync and status commands never resend or move a message.

## Folder list is empty

`icloud mail folders list` reads local state only. Run one live discovery first:

```bash
icloud mail folders status
```

If that fails, use the returned credential, authentication, timeout, TLS, LIST, or STATUS stage to choose the next action. The command does not fetch messages.

## A folder says subscription unknown

That is truthful when iCloud does not expose subscription metadata through the LIST command it accepts. Unknown does not mean unsubscribed and does not prevent local future-sync policy from being enabled.

Provider subscription and local synchronization policy are separate. Use `mail folders subscribe` / `unsubscribe` for the provider state and `mail folders enable` / `disable` for future automatic CLI sync selection.

## A folder rename timed out

Do not immediately repeat the rename. The provider may have accepted the command before the response was lost.

```bash
icloud mail folders status
```

Rename recovery is recorded before provider contact. A fresh status refresh can reconnect the new provider name to the original `fld_…` even when iCloud omits rename metadata. If both old and new names remain or neither is visible, keep the state directory intact and avoid another rename until the provider view is unambiguous.

## A custom folder cannot be renamed or deleted

Inbox and provider-discovered system or special-use folders are protected. A folder with current child mailboxes is also protected from automatic rename/delete so an implicit provider hierarchy cascade cannot broaden the operation; change the children first. Non-selectable containers are not deleted automatically.

For deletion, refresh counts with `icloud mail folders status`. A non-empty custom folder additionally requires `--allow-non-empty --yes`, which means its provider contents may be removed.

## Archive or Drafts says kind other

The provider did not advertise a corresponding special-use attribute. The CLI deliberately does not infer Archive, Drafts, Trash, or Junk roles from an English display name.

For ordinary folder synchronization, select the discovered folder by stable ID or name and enable it explicitly when you want it included. For the draft lifecycle, keep the provider classification unchanged and explicitly select the discovered mailbox for that operation:

```bash
icloud mail draft sync drf_… --folder Drafts
```

The selector does not relabel the mailbox as a trusted system folder. If iCloud later advertises a real Drafts role, the CLI uses that provider role and rejects an override to a different ordinary folder.

## A draft was sent but cleanup is pending

Do not send the draft again. The outbound attempt is already proven sent; only provider/local draft cleanup remains.

```bash
icloud mail draft sync drf_…
```

If this account exposes Drafts as `kind=other`, add the same explicit `--folder <selector>` you used for upload. Sync retries cleanup only from saved provider locations and proven send state. If the error instead says the SMTP outcome is unknown, keep the draft intact and verify Sent plus the recipient before taking any deliberate retry action.

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

`state=repair_required` means iCloud returned a different mailbox identity. Ordinary sync stops before resetting or combining message locations. Keep the state directory intact and inspect the bounded repair plan first:

```bash
icloud mail sync repair --check --verbose
```

If the finding is an affected UID frontier, apply the deterministic repair:

```bash
icloud mail sync repair --apply
icloud mail sync status --folder <folder>
```

The apply path reads fresh status for only the affected mailbox, moves its forward frontier to the verified current tail, and preserves cached messages. A historical frontier tied to the old UID namespace is reset to the same current provider boundary rather than combining reused UIDs.

## Historical backfill stopped or was canceled

Repeat the same bounded request:

```bash
icloud mail sync backfill --folder inbox --limit 50
```

A failed page does not advance the historical cursor. Normal `mail sync` remains independent and can continue to fetch new mail while backfill resumes. If the backfill reports a mailbox identity change, run `mail sync repair --check` before another historical fetch.

## I want future sync to start from now

Inspect the existing frontier first, then make the skip explicit:

```bash
icloud mail sync status --folder inbox
icloud mail sync rebaseline --folder inbox --current-tail
```

Rebaseline does not delete cached messages, but ordinary forward sync will no longer fill the skipped range. Use `mail sync backfill` if some of that earlier provider history should still be cached.

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

Remote search is bounded and is not local FTS parity. `partial=true` or `more=true` includes an `rcur_…` `next_cursor`; resume the exact same remote scope. One-folder search continues older unscanned UID windows. All-folder search uses at most two provider mailbox operations per invocation, so it may return an empty partial page while it pages the selectable-folder inventory or advances a sparse folder far enough to establish its next merge head. Continue the cursor instead of treating that partial empty page as a terminal “no matches” result. Attachment metadata and reconstructed-thread filters require synchronization or targeted hydration first.

If a remote cursor is rejected because the query, folder, account, result limit, selectable-folder membership, or UIDVALIDITY changed, restart the search from current provider state instead of editing or reusing the old cursor. Local `cur_…` cursors and remote `rcur_…` cursors are distinct contracts.

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

Do not replace the blob manually or resynchronize the whole mailbox blindly. Keep the state directory intact and inspect the bounded repair evidence:

```bash
icloud mail sync repair --check --verbose
```

Missing or corrupt canonical bytes are reported with a targeted-recovery/backup next action. `--apply` does not fabricate replacement content or collapse identities to hide the problem. Stable message and location IDs remain unchanged.

## Export refuses an existing file

This is the safe default:

```bash
icloud mail message export msg_… --output message.eml --force
```

`--force` is required to replace an existing regular file. Directories and special files are rejected. The command writes a private temporary file first and does not echo the destination path in normal output.

The same no-clobber rule applies to `mail mailbox export`. A mailbox archive is installed only after the complete local mboxrd stream succeeds, so a missing/corrupt canonical raw blob does not replace an existing `--force` target or leave a new archive path behind. The error details identify the stable message/location and provider UID that could not be exported. Run the bounded repair check instead of forcing a reconstructed archive:

```bash
icloud mail sync repair --check --verbose
```

Mailbox export is intentionally local-only. If the archive is missing newer provider mail, synchronize that folder explicitly first and then rerun the export; the export command itself never contacts iCloud to make the local cache look current.

## EML import is unresolved

An unresolved import means the provider may have accepted the APPEND but the CLI does not yet have exactly one safe provider identity to commit. Do **not** change the file, destination, flags, internal date, or idempotency key and then retry as though it were a new upload.

Re-run the same command with the same normalized request and idempotency key. The CLI uses the durable pre-APPEND UID fence and exact raw hash to inspect the bounded candidate range without issuing another APPEND. A provider candidate is accepted only when it is byte-identical to the reviewed source or exactly that source plus one terminal CRLF, an iCloud APPEND normalization reported as `provider_raw_variant=terminal_crlf`. If one such copy is proven, canonical ingestion resumes. If none is visible yet, retry later; if several matching copies exist, the command reports a conflict instead of guessing.

A definite authentication/connect/select failure before APPEND is different: that import is durably failed. Correct the underlying problem, then make a new deliberate request with a new idempotency key. Import dry-run is always safe for re-review because it does not create recovery state or contact iCloud.

## Mailing-list unsubscribe is unavailable or refused

First inspect the local semantic capability without causing any external action:

```bash
icloud mail unsubscribe msg_… --dry-run
```

If no supported method is shown, the message either has no usable `List-Unsubscribe` metadata or advertises a form the CLI deliberately does not execute. Generic `http:` URLs, HTTPS userinfo, malformed/control-bearing URIs, and mailto query fields other than one `subject` and one `body` are rejected. Two or more RFC 8058 HTTPS endpoints are not guessed. Several mailto alternatives remain available but require `--method mailto:N` from the dry-run output.

One-click may be visible in dry-run yet fail at execution if the raw message does not have a valid DKIM signature covering both `List-Unsubscribe` and `List-Unsubscribe-Post`. That check happens only after explicit confirmation because it may require DNS. Do not work around it by manually POSTing the tokenized endpoint through a browser session: RFC 8058 specifically defines a context-free POST, and the CLI refuses cookies, authorization, redirects, HTTP downgrade, loopback/private/link-local/multicast-only destinations, and oversized responses for that reason.

An HTTP timeout or 5xx outcome can be ambiguous from the client's perspective: the sender may have processed the unsubscribe even though the response was lost. Inspect later mail/list behavior before repeatedly issuing one-click requests. A mailto action instead reuses the existing durable outbound-send journal, so repeating the same source message + method selection reuses the original attempt rather than sending a duplicate message.

If the command shows multiple safe method IDs, choose one explicitly. Ordinary `mail message show` output intentionally reports only method/host capability and never the full opaque HTTPS URI or mailto payload; `--headers` is the explicit private-content expansion if you truly need to inspect the source header.

## Mailbox import is partial or will not resume

Mailbox import deliberately resumes from the **earliest unconfirmed archive record**, not the last record that happened to succeed. This matters when `--mode continue-on-error` confirms later records after one earlier record fails: those later records are remembered and will not be APPENDed again, but `next_index` and `next_offset` remain at the gap until it is proven.

Re-run the same archive path and destination. A record whose APPEND outcome was ambiguous keeps the same private single-message recovery key so the CLI reconciles the fenced provider candidates before any new APPEND. A provider failure proven to have occurred before APPEND is safe to retry with a new private per-attempt key; the mailbox job makes that transition automatically. Malformed source records remain failed because changing or silently skipping the archived bytes would make the durable frontier dishonest.

If the command reports that the source file changed, do not force continuation. An incomplete job is bound to the canonical source path plus the exact archive SHA-256 and byte size. Restore the exact original archive if that is what you intended to resume, or treat a corrected/rebuilt archive as a separate deliberate migration source rather than splicing new bytes into the old job.

`--dry-run` is safe while diagnosing an archive: it fingerprints the file and validates only the record at the current frontier without creating a mailbox-import job or contacting IMAP. `--jsonl` is useful for a partial pass because each attempted record and the terminal durable summary remain visible even when the command exits non-zero.

## Raw JSON is too large

Body/raw content in JSON is deliberately bounded. Use a streaming mode instead:

```bash
icloud mail message raw msg_… > message.eml
icloud mail message show msg_… --full > message.txt
```

Default message JSON remains available regardless of body size because it contains availability and bounded metadata rather than the payload.
