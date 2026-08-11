---
title: Send mail safely
description: Preview, draft, send, reply, and forward through iCloud Mail with explicit message selection, confirmation, idempotency, BCC privacy, streamed attachments, and honest delivery outcomes.
order: 8
category: Guides
summary: Dry-run review, recoverable drafts, exact-message replies and forwards, attachments, privacy, idempotency, Sent reconciliation, and unknown-outcome recovery.
---

## Preview the exact request first

Start with `--dry-run`. It resolves the account's selected sender identity, normalizes addresses, subject, bodies, Reply-To, and custom headers, and does not open SMTP or create an outbound attempt:

```bash
icloud mail send \
  --to 'Client <client@example.com>' \
  --subject 'Project update' \
  --body-file message.txt \
  --dry-run
```

The compact preview shows the configured account name and address used for `From`, each recipient list, the subject, body-part and attachment counts, the text body once, and an explicit safety summary. A dry run never sends mail.

Choose a configured sender explicitly when needed:

```bash
icloud mail send \
  --from alias@icloud.com \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt \
  --dry-run
```

`--from` accepts either a stable `mid_…` identity ID or a configured identity address. If omitted, the account's enabled local default identity is used. The selected identity controls the RFC `From`, SMTP envelope sender, and Message-ID domain; SMTP authentication still uses the selected account's existing login and credential. A disabled identity is rejected before SMTP is contacted.

## Configure local signatures by identity and operation

Signatures are account-local composition state. Create one reusable text signature, optionally with a matching HTML representation and optionally bound to one stable sender identity:

```bash
icloud mail signatures create \
  --identity mid_… \
  --text-file signature.txt \
  --html-file signature.html
```

`mail signatures list` is intentionally metadata-only: it reports stable `sig_…` IDs, identity binding, byte counts, and timestamps without dumping reusable signature bodies. Use the explicit content read when you need to inspect one:

```bash
icloud mail signatures show sig_…
```

Set defaults independently for new mail, replies, reply-all, and forwards. Defaults may be account-wide or identity-specific:

```bash
icloud mail signatures default new sig_…
icloud mail signatures default reply sig_… --identity mid_…
icloud mail signatures default reply-all none --identity mid_…
icloud mail signatures default forward sig_…
```

An identity-specific default wins over the account-wide default. An explicit `none` is also a real setting and prevents fallback. A signature bound to one `mid_…` identity cannot be selected for another sender identity; an unbound signature can be shared. These settings remain local to this CLI installation—they do not read or change Apple Mail/iCloud signature preferences.

Override the selected default for one compose with `--signature sig_…`, or suppress it with `--signature none`. The same override is available on `mail send`, `reply`, `reply-all`, `forward`, and `draft create/update`:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt \
  --signature sig_… \
  --dry-run
```

Signature expansion happens **before** the dry-run preview, body-size checks, MIME composition, and idempotency request hash. The bytes you review are therefore the bytes whose identity is protected by the idempotency key. If a configured signature later changes, reusing an existing key for what would now be different expanded content returns a conflict before SMTP rather than silently changing the message.

For a text body, the signature is appended after one blank line. For an HTML body, the configured signature HTML is appended after `<br><br>`; if that signature has no HTML form, its escaped text form is used with newlines converted to `<br>`. Text-only messages remain text-only. An otherwise empty compose may use the signature as its text body. The 8 MiB per-body ceiling is enforced on the final expanded body, not only on the caller-supplied prefix.

Use the account selector when more than one profile is configured:

```bash
icloud --account Personal mail send \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt \
  --dry-run
```

## Send interactively or authorize automation

In a terminal, omit `--yes` to review the compact preview and answer the confirmation prompt:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt
```

A redirected or agent-run command does not prompt. It must use `--yes`:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt \
  --idempotency-key project-update-2026-08-06 \
  --yes
```

Use a stable idempotency key for automation. Repeating the same normalized request with the same key returns the original attempt without contacting SMTP again. Reusing that key after changing the sender identity, recipient, subject, body, Reply-To, header, or account returns a conflict instead of guessing.

Successful compact output contains only the attempt outcome and stable local IDs:

```text
mail-send
send_… sender_identity=mid_…
status=sent
message_id=msg_…
thread_id=thr_…
reconciliation=pending
reused=false
```

The submitted subject, body, recipients, BCC list, and custom headers are not echoed after a real send. The accepted message is immediately readable and searchable through its local `msg_…` and `thr_…` IDs.

## Keep an unfinished message as a recoverable draft

Draft creation is local-only, so you can assemble recipients, text/HTML, safe custom headers, ordinary attachments, and inline CID resources without opening IMAP or SMTP:

```bash
icloud mail draft create \
  --from alias@icloud.com \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt \
  --attach report.pdf
```

The returned `drf_…` remains the stable draft ID across revisions. `draft update` replaces the saved revision locally; it does not silently edit the provider copy first:

```bash
icloud mail draft update drf_… \
  --from mid_… \
  --to client@example.com \
  --subject 'Project update — revised' \
  --body-file revised.txt \
  --attach report.pdf
```

Use `draft list` for a bounded overview and `draft show drf_…` for one saved draft. Default compact and JSON views hide BCC addresses. Complete decoded text, HTML, headers, body-only output, or raw MIME requires the corresponding explicit `draft show` expansion.

Synchronize only when you want the revision in iCloud Mail:

```bash
icloud mail draft sync drf_…
```

The CLI normally selects the provider-advertised Drafts role. Some iCloud accounts expose a selectable Drafts mailbox without advertising that role; `folders status` then truthfully shows `kind=other`. In that case, select the discovered mailbox explicitly instead of teaching the CLI to guess from its English name:

```bash
icloud mail draft sync drf_… --folder Drafts
```

An update uploads the replacement before deleting the previous confirmed provider version. If APPEND succeeds but its UID is unavailable, sync reconciles the exact generated Message-ID and current raw content before removing an older copy. If that proof is interrupted, the previous version remains recoverable and the draft reports a recovery state rather than deleting first.

When the draft is ready, preview the exact stored revision, then send it through the same outbound attempt path as `mail send`:

```bash
icloud mail draft send drf_… --dry-run
icloud mail draft send drf_… --from alias@icloud.com --yes
```

Draft create/update persist the stable `mid_…` identity used to compose that revision. Draft send also accepts `--from`; if omitted, it resolves the account's current enabled default sender before the outbound request is hashed. A proven send removes known `\Draft` provider copies before removing the local draft. If SMTP acceptance is unknown, the draft and provider copy stay in place. If the message is proven sent but cleanup is incomplete, do not resend; run `icloud mail draft sync drf_…` (adding the explicit `--folder` selector when needed) so cleanup can resume from the saved attempt state.

Signature expansion is part of the saved draft revision. `draft create` and every explicit `draft update` apply the signature/default selected at that moment before storing the revision. Editing or replacing that signature later does **not** mutate an already-saved draft or change a later `draft send` implicitly. To apply the current signature to changed draft content, perform an explicit `draft update`; that creates a new revision whose expanded body can be previewed before sending.

Likewise, a draft in `reconcile`, `cleanup_pending`, or `send_unknown` recovery state cannot be directly discarded by an ordinary send/delete path when doing so would lose the only provider-recovery evidence. Run `draft sync` first. A retry of a draft whose outbound attempt is already proven `sent` remains idempotent: it reuses that attempt and may resume cleanup without another SMTP transmission.

Delete without sending only after reviewing the scope:

```bash
icloud mail draft delete drf_… --dry-run
icloud mail draft delete drf_… --yes
```

Deletion removes the known provider draft versions first and then the local draft. It refuses to claim success when provider deletion is unresolved.

## Reply to the exact message you intend

Replies always start from a stable local `msg_…` ID. The CLI never guesses that the newest message in a conversation is the one you meant:

```bash
icloud mail reply msg_… \
  --from alias@icloud.com \
  --body-file reply.txt \
  --dry-run --headers
```

The preview identifies the exact parent and reconstructed thread, shows the resolved sender identity, then separates recipients derived from the parent from addresses you added. A normal reply prefers the parent's `Reply-To` address and otherwise uses `From`. It normalizes the subject to one `Re:` prefix and adds RFC `In-Reply-To` and `References` headers when the parent has a usable Message-ID. `--from` accepts the same enabled `mid_…`/address selector as `mail send`; omission uses the account's current local default identity.

The reply also selects one exact current provider location for the parent so a proven send can update iCloud's standard Answered state safely. Most messages have only one eligible location. If the same logical message is present in multiple folders, choose the intended copy explicitly:

```bash
icloud mail reply msg_… --folder INBOX --body-file reply.txt --dry-run
```

The preview reports both `parent_location_id` and `parent_folder_id`; an ambiguous parent is rejected before SMTP is contacted.

After review, send with the same safety controls as a new message:

```bash
icloud mail reply msg_… \
  --body-file reply.txt \
  --idempotency-key client-reply-2026-08-06 \
  --yes
```

The success result includes `kind=reply`, `parent_message_id=msg_…`, `parent_location_id=loc_…`, the accepted child `message_id`, the reconstructed `thread_id`, and `answer_reconciliation`. After SMTP acceptance is proven, the CLI conditionally adds iCloud's standard `\Answered` flag to that exact parent location using its current UIDVALIDITY and modification sequence. The provider-returned flags become the new local state, including any unknown keywords already present.

SMTP remains authoritative. A failed or unknown SMTP outcome never marks the parent answered. If SMTP acceptance is proven but the Answered-state write cannot be completed, the reply remains `status=sent` and reports `answer_reconciliation=pending` rather than pretending the send failed. Repair only that provider state—without retransmitting the message—with:

```bash
icloud mail operations outbound reconcile-answer send_…
```

Repeating the same normalized reply with the same idempotency key also returns the original sent attempt without contacting SMTP again; a confirmed Answered reconciliation is not repeated.

Use `reply-all` only after inspecting the resolved participant lists:

```bash
icloud mail reply-all msg_… \
  --from mid_… \
  --body 'Thanks, everyone.' \
  --dry-run
```

Reply-all starts with the parent's visible `To` and `Cc` participants, removes **every enabled identity address for the selected account** case-insensitively, deduplicates addresses, and keeps the primary reply address in `To`. This includes enabled aliases and custom-domain identities, not only the login/primary address. It never resurrects BCC recipients from the parent. A new `--bcc` address is included only because you supplied it for this reply, and it remains envelope-only on the wire.

`--to` and `--cc` add recipients to the derived lists. Use `--replace-to` or `--replace-cc` when you deliberately want the supplied list to replace the corresponding derived list:

```bash
icloud mail reply-all msg_… \
  --to new-owner@example.com --replace-to \
  --cc reviewer@example.com --replace-cc \
  --body-file reply.txt \
  --dry-run
```

If the parent's Message-ID is absent or malformed, the preview reports `threading=false` and sends without inventing relationship headers. The local child may then remain a separate reconstructed conversation; use the preview rather than assuming a thread merge.

## Forward one exact message as a new conversation

Forwarding starts from one stable local `msg_…` ID, but unlike reply it always creates a new conversation. The source thread is recorded as provenance only; the outgoing message contains no `In-Reply-To` or `References` headers:

```bash
icloud mail forward msg_… \
  --from alias@icloud.com \
  --to teammate@example.com \
  --body 'For context:' \
  --dry-run --headers
```

The default `--original quoted` representation adds a bounded decoded-text copy below a safe forwarded-message header containing From, Date, Subject, To, and Cc. It never quotes the source BCC list. Repeated `Fwd:` or `Fw:` prefixes collapse to one normalized `Fwd:` subject. Forward accepts the same `--from <mid_…|address>` selector as send/reply, but source-thread identity remains provenance only: changing the sender does not mutate or merge the source conversation.

Use an exact attached RFC message only when the recipient needs the original wire representation:

```bash
icloud mail forward msg_… \
  --to teammate@example.com \
  --original eml \
  --dry-run
```

`--original eml` attaches the verified source bytes as `forwarded-message.eml`. Because exact raw mail can contain sensitive headers that are intentionally absent from ordinary previews, EML forwarding is never implicit. `--original none` omits the original content, requires your own text or HTML body, and does not require opening the source raw MIME at all.

Original attachments are also explicit. List the source message first, then include only the stable attachment IDs you intend to resend:

```bash
icloud mail attachments list msg_…

icloud mail forward msg_… \
  --to teammate@example.com \
  --include-attachment att_… \
  --attach new-note.txt \
  --idempotency-key teammate-forward-2026-08-07 \
  --yes
```

`--include-attachment` verifies the saved decoded bytes and confirms that each `att_…` belongs to the selected source message. It does not copy every original attachment automatically. New filesystem attachments continue to use `--attach` and the same changed-file checks as `mail send`.

The compact result is payload-free and preserves source provenance without implying RFC thread identity:

```text
mail-forward
send_… kind=forward
source_message_id=msg_…
status=sent
message_id=msg_…
thread_id=thr_…
reconciliation=pending
reused=false
```

Forward uses the same confirmation, idempotency, retry, size, SMTP, local acceptance, Sent append, and reconciliation behavior as send and reply. Repeating an identical forward with the same key returns the existing attempt without contacting SMTP.

After SMTP acceptance is proven, the source message and its reconstructed thread expose `forwarded=true` from the durable forward attempt. Failed or unknown SMTP outcomes do not create that history. This is a local provenance aggregate: the source MIME, provider locations, provider flags, attachments, and thread membership are not rewritten.

The current iCloud IMAP compatibility probe did not expose a stable forwarded flag or keyword before versus after a real forward, so `icloud capabilities --json` reports `provider_forwarded_state="unsupported"`. The CLI does not guess `$Forwarded` or write an undocumented keyword merely to imitate an Apple Mail UI marker.

## Choose one safe body source

Provide text inline, from a regular file, or from stdin:

```bash
icloud mail send --to client@example.com --subject Update --body 'Hello' --dry-run
icloud mail send --to client@example.com --subject Update --body-file message.txt --dry-run
printf '%s\n' 'Hello' | icloud mail send --to client@example.com --subject Update --body-file - --dry-run
```

HTML uses the matching `--html-body` or `--html-body-file` flags. Text and HTML together produce one multipart alternative message. HTML without an explicit text body receives a simple plain-text alternative.

Each final body is bounded to 8 MiB after local signature expansion and normalization, before MIME composition. One stdin stream cannot supply both text and HTML. A real interactive send cannot consume its body and confirmation from the same stdin; review with `--dry-run`, then rerun with `--yes`.

## Attach reviewed files without buffering the whole message

Repeat `--attach` for regular files. A dry run reads each file, detects its media type, computes its SHA-256 hash, and shows the normalized filename, type, size, and hash before any network connection:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project files' \
  --body 'See attached.' \
  --attach report.pdf \
  --attach data.csv \
  --dry-run
```

Override untrusted or unhelpful local metadata by referring to the same attached path:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project files' \
  --body 'See attached.' \
  --attach ./build/output.bin \
  --attach-name './build/output.bin=report.bin' \
  --attach-type './build/output.bin=application/octet-stream' \
  --idempotency-key project-files-2026-08-06 \
  --yes
```

The real send rechecks the reviewed bytes before composing the message. A missing file, directory, symbolic or other special file, changed file, unreadable file, unsupported filename, invalid media type, cache failure, or size violation stops before SMTP. The composed RFC message is capped at 20 MiB, including MIME/base64 overhead; the CLI does not invoke Mail Drop.

### Embed an inline CID resource

Use the same reviewed attachment path with `--attach-disposition ...=inline`. Supply a Content-ID with `--attach-cid` when the HTML already references a specific `cid:` value:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Inline preview' \
  --body 'Open this message in an HTML-capable client.' \
  --html-body '<p>Logo:</p><img src="cid:project-logo@example.test">' \
  --attach ./logo.png \
  --attach-disposition './logo.png=inline' \
  --attach-cid './logo.png=project-logo@example.test' \
  --dry-run
```

Inline resources use `Content-Disposition: inline` and a canonical `Content-ID`; HTML/text alternatives and their related resources are nested together before ordinary attachments. If an inline attachment omits `--attach-cid`, the CLI derives a deterministic Content-ID from its reviewed content hash and attachment position. That generated value is visible in the dry-run attachment review so HTML automation can use it deliberately on a subsequent request.

Disposition and Content-ID are part of the normalized request identity. Reusing an idempotency key after changing either value is a conflict rather than a silent resend with different MIME semantics. Content-IDs must use a single `local@domain` token with no whitespace or header-control characters, and one request cannot reuse the same Content-ID for two resources.

The same `--attach-disposition` and `--attach-cid` controls are available on `mail reply`, `mail reply-all`, `mail forward`, and `mail draft create/update`. Draft synchronization retains the metadata through the provider copy, and sending a synchronized draft reconstructs the same inline relationship. Explicitly included original forward attachments also retain their observed inline disposition and Content-ID rather than being flattened into ordinary attachments.

Accepted attachment bytes are immediately available through the normal local message and attachment commands, even before a later Sent sync confirms the provider location:

```bash
icloud mail attachments list msg_…
icloud mail attachments save att_… --output report.pdf
```

## Review addresses and headers without exposing BCC on the wire

Repeat `--to`, `--cc`, or `--bcc`, or pass a comma-separated address list. Display names are preserved:

```bash
icloud mail send \
  --to 'Primary <primary@example.com>' \
  --cc 'Visible Copy <copy@example.com>' \
  --bcc 'Private Copy <hidden@example.com>' \
  --reply-to 'Replies <reply@example.com>' \
  --subject 'Project update' \
  --body-file message.txt \
  --dry-run --headers
```

The dry-run review includes BCC because you explicitly asked to inspect the outbound request. The composed RFC message never contains a `Bcc` header or BCC address; those addresses exist only in the SMTP envelope.

`--headers` shows normalized safe header fields and custom headers. Use `--format raw` during a dry run when you need the composed RFC message itself:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt \
  --header 'X-Workflow: client-update' \
  --dry-run --format raw > preview.eml
```

Custom headers cannot replace routing, identity, MIME, delivery, or reply-threading headers. Header names and values containing control characters, duplicate names, `Bcc`, `In-Reply-To`, `References`, `Received`, `Resent-*`, or other reserved fields are rejected before any network connection.

## Interpret the outcome before retrying

Alternate sender acceptance is recorded from SMTP protocol evidence, not inferred from the identity kind. A successful send—or a later recipient/DATA-stage failure after `MAIL FROM` already succeeded—sets that local identity to `verification=verified`. A permanent `MAIL FROM` rejection marks a non-primary identity `verification=rejected`, disables it, and returns a configuration error without falling back to the primary address. Re-enable it only after fixing provider-side configuration; doing so resets its local verification state to `unverified` for another deliberate test.

Every real send persists a pending attempt before SMTP submission. The final result is one of:

| Status | Meaning | Next action |
| --- | --- | --- |
| `sent` | iCloud SMTP returned final acceptance. Normally the local message and attempt commit together and return `msg_…` and `thr_…`. | Use the returned IDs. `reconciliation=pending` means the provider Sent location has not been observed yet; it does not make the send safe to repeat. |
| `failed` | The CLI proved the message was not accepted, such as authentication failure or an explicit SMTP rejection. | If the transport proved the failure happened before SMTP DATA began, the attempt may also have a private `out_…` recovery item. Otherwise fix the cause, preserve the original `send_…` ID, and use an explicit linked retry when appropriate. |
| `unknown` | The connection failed after message data may have reached iCloud, so acceptance could not be proved either way. | Reconcile Sent and check the recipient. Retry only after explicitly accepting duplicate risk. |
| `pending` | An earlier process stopped before a final durable outcome was recorded. | Treat it as unresolved; do not create an automatic duplicate. |

### Use the Outbox only for transport-proven pre-DATA failures

The local Outbox is a narrow recovery queue, not Send Later and not a background scheduler. It is created only when the SMTP transport proves that the failed attempt stopped **before DATA could have started carrying the message**. DNS/connect/TLS/authentication/MAIL/RCPT/DATA-command failures can qualify when that boundary is proven. Unknown outcomes, failures after DATA began, accepted sends, and Sent-reconciliation failures never create a retryable Outbox item.

List or inspect recovery metadata without exposing recipients, BCC addresses, body content, Message-ID, request hashes, or private blob coordinates:

```bash
icloud mail outbox list
icloud mail outbox show out_…
```

An Outbox item privately retains the exact reviewed raw MIME identity and the exact SMTP envelope that went with it, including envelope-only BCC recipients. A retry never reconstructs the message from public attempt fields. Review the item first, then explicitly replay those exact bytes and that private envelope:

```bash
icloud mail outbox retry out_… --dry-run
icloud mail outbox retry out_… --yes
```

`--dry-run` shows lifecycle/count metadata only and does not contact SMTP. A redirected or agent-run retry requires `--yes`; an interactive terminal can confirm at the prompt. Every actual replay receives a new linked `send_…` attempt whose `retry_of_attempt_id` points to the previous attempt in the chain.

If that replay again fails before DATA, the same item becomes `retryable` again. If the replay becomes `unknown`, the Outbox becomes `unknown` and cannot be replayed again; use the ordinary Sent/recipient duplicate-risk recovery path. A post-DATA failed replay is also terminal for the Outbox even when SMTP returned an explicit rejection—the Outbox contract intentionally covers only the stricter pre-DATA boundary. Cancel a still-retryable local item without provider contact when you no longer want it sent:

```bash
icloud mail outbox cancel out_…
```

There is no daemon, timer, scheduled-send mode, or automatic resend loop. Restart recovery also fails closed: if a process stopped after a linked retry attempt was durably created but before its provider outcome was recorded, that attempt and Outbox item become `unknown` rather than being transmitted again automatically.

Inspect bounded payload-free operation history, then synchronize Sent and reconcile exact generated Message-ID evidence:

```bash
icloud mail operations outbound list
icloud mail sync --reconcile-outbound
icloud mail operations outbound list --json
icloud mail operations outbound list --limit 100 --cursor 'ocur_…' --json
icloud mail operations outbound show send_… --json
```

Outbound history is newest-first. When a bounded page reports `more=true`, pass its opaque `next_cursor` back with `--cursor`; the cursor belongs to that account and outbound-status inventory, and the terminal page omits it.
Use `show send_…` to inspect any exact stable attempt directly, including one reached on an older page. Exact inspection keeps the same payload-free privacy boundary as the list. `icloud mail status --outbound` remains a compatibility alias for `mail operations outbound list`.

A unique Sent observation confirms the attempt and associates the provider location with the existing local message. Similar subjects, bodies, times, or attachments are never enough. No match remains pending/unknown, and conflicting exact evidence remains visibly ambiguous.

An `unknown` error is always reported as non-retryable even when the underlying network failure was temporary. Run the exact same command with the same idempotency key to retrieve the original attempt without resending, then reconcile Sent and check the recipient.

After a proven failed attempt, repeat the same normalized message with a new key and link it explicitly:

```bash
icloud mail send ... \
  --retry-of send_… \
  --idempotency-key update-retry-02 \
  --yes
```

An unresolved `unknown` attempt requires the additional `--retry-unknown` acknowledgement. This is deliberately noisy because both copies may be delivered:

```bash
icloud mail send ... \
  --retry-of send_… \
  --retry-unknown \
  --idempotency-key update-duplicate-risk-02 \
  --yes
```

A local-completion error is different: the final SMTP acceptance was proven, so the durable attempt remains `sent` and the same idempotency key cannot resubmit it. Preserve the attempt ID and let Sent reconciliation restore the missing local location/message evidence.

## Use JSON only when its privacy tradeoff is intentional

A real `--json` success contains the account ID, sanitized attempt fields, reuse state, schema version, and request ID. It omits recipients, BCC, subject, bodies, raw storage coordinates, and the idempotency key.

A dry-run `--json` response is different: it is an explicit review mode and therefore includes the normalized recipients, BCC, subject, bodies, Reply-To, and safe custom headers. Treat that output as message content and do not write it to broad logs.

## Common recovery

- `non-interactive mail send requires --yes`: review with `--dry-run`, then add `--yes` to the deliberate send.
- `non-interactive mail reply requires --yes`: review the exact parent and resolved recipients with `--dry-run`, then add `--yes`.
- `non-interactive mail forward requires --yes`: review the exact source, original representation, and attachment selection with `--dry-run`, then add `--yes`.
- `reply parent message not found`: synchronize or cache the intended message, then use its stable local `msg_…` ID.
- `forward source message not found`: synchronize or cache the intended message, then use its stable local `msg_…` ID.
- `cannot construct a safe reply from the selected message`: inspect the exact parent. If every derived recipient is one of the selected account's own identities, supply a deliberate `--to` address or use `--replace-to --to ...`.
- `cannot construct a safe forward from the selected message`: supply a recipient, use a supported `--original` mode, and provide a body when using `--original none`.
- `decoded forward source text is unavailable`: use `--original eml` for the verified exact source or `--original none` with your own body.
- `selected attachment does not belong to the forward source message`: list attachments for the exact source `msg_…` and choose IDs from that result.
- `mail body consumed stdin`: stdin cannot also answer an interactive prompt; rerun with `--yes` after review.
- `idempotency key was already used`: keep the original request or choose a new key for an intentionally different message.
- `mail send outcome is unknown`: preserve the attempt ID, run `mail sync --reconcile-outbound`, inspect `mail operations outbound show send_…`, and verify the recipient before considering `--retry-unknown`.
- `SMTP accepted the message but local completion is incomplete`: the provider accepted it; preserve the `send_…` ID and do not resend.
- `SMTP accepted the message but its Sent copy could not be confirmed`: the message was accepted; do not resend. Refresh folder discovery if needed, then synchronize and reconcile Sent.
- `composed message exceeds iCloud Mail's 20 MiB message limit`: reduce attachment/body size; MIME encoding overhead counts toward the limit.
- SMTP authentication or STARTTLS failure: run `icloud account test` and correct the account credential or network path before another deliberate attempt.
