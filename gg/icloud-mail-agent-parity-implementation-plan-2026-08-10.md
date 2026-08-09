# iCloud Mail Agent Parity Expansion — Implementation Plan

> **Current-state sweep:** [`icloud-mail-agent-parity-sweep-2026-08-10.md`](./icloud-mail-agent-parity-sweep-2026-08-10.md)
> **Durable handoff ledger:** [`progress.md`](./progress.md)
> **Implementation-agent prompts:** [`icloud-mail-agent-parity-agent-prompts.md`](./icloud-mail-agent-parity-agent-prompts.md)

## Planning Basis

- **Repository:** `amxv/icloud-cli`, Zodex checkout `/workspace/repos/icloud-cli`.
- **Branch / planning baseline:** `main` at `1c3c9372af516564695e5daed6a766a277062141`, equal to `origin/main` when planning began. The only commit after the completed Round 2 mail-remediation boundary changes the landing page, so the Go mail implementation remains the schema-19 `799b052` lineage.
- **Planning date:** 2026-08-10.
- **Workflow artifacts consumed:** attached `sweep.md`, `blueprint.md`, `plan-doc.md`, and Zodex Go CLI Planning Playbook; the new current-state sweep above; current code/tests/history; and `gg/progress.md` for the completed-track handoff and authorized disposable-harness policy.
- **External planning evidence:** Apple’s current iCloud Mail documentation for enabled/default Send From addresses, aliases/custom domains, Junk/Not Junk, Hide My Email, automatic forwarding/automatic reply, and S/MIME behavior; Apple Developer TN3191 for the supported Mail IMAP-extension boundary; current primary Go package documentation for the selected CMS primitive.
- **Unverified provider facts carried as assumptions:** exact SMTP acceptance semantics for alternate iCloud sender addresses, iCloud’s writable representation of `\\Answered`, forwarded state, colored flags, and Hide My Email sender addresses, plus Apple-Mail interoperability details for the selected pure-Go S/MIME stack. Those are assigned explicit live-compatibility phases rather than guessed.

Authoritative external references used to fix provider boundaries for downstream agents:

- Apple iCloud Mail Send From/default-address guidance: <https://support.apple.com/guide/icloud/mmbffbba6600/icloud>
- Apple iCloud alias guidance: <https://support.apple.com/guide/icloud/what-are-email-aliases-in-icloud-mail-mm074af79454/icloud>
- Apple custom-domain Mail guidance: <https://support.apple.com/guide/icloud/use-a-custom-email-domain-with-mail-mm772b937369/icloud>
- Apple iCloud junk/not-junk guidance: <https://support.apple.com/en-in/102376>
- Apple Developer TN3191 IMAP-extension guidance: <https://developer.apple.com/documentation/technotes/tn3191-imap-extensions-supported-by-mail>
- Apple Hide My Email guidance: <https://support.apple.com/guide/icloud/set-up-hide-my-email-mm9d9012c9e8/icloud>
- Apple S/MIME/personal-certificate guidance: <https://support.apple.com/guide/mail/use-personal-certificates-mlhlp1179/mac>
- `github.com/smallstep/pkcs7` primary Go package documentation: <https://pkg.go.dev/github.com/smallstep/pkcs7>

This is a **new expansion track** layered on the completed iCloud Mail CLI. It does not reopen or renumber the original 25 implementation phases or the two Crucible remediation rounds. The historical artifacts remain evidence for the system that already exists; this plan is the new authoritative contract for the agent-parity expansion.

## State of Current System

The current product is already a mature local-first iCloud Mail client: one `icloud` executable, one versioned SQLite store, one content-addressed blob root, and a narrow IMAP/SMTP transport boundary. It already has correct bounded sync/backfill/watch, full message/raw/attachment reads, local and remote search, reconstructed threads, drafts, safe idempotent send/reply/reply-all/forward, exact-location mutations, mailbox administration, repair/reconciliation, strict write JSON/JSONL contracts, generated schemas/reference assets, and release packaging.

The expansion starts from several deliberate constraints documented in the Sweep:

- `Account.Email` currently conflates login/account identity, sender identity, self-address recognition, and direction classification.
- provider folder `Kind` is observed truth; there is no separate durable user semantic-role binding;
- `\\Answered` is stored but not writable through the public flag mutation path;
- forward provenance already exists locally through `outbound_attempts.source_message_id`, but provider forwarded state is unproven;
- exact location changes have a strong durable intent/reconciliation model, but only one target per command;
- remote provider search is bounded but not resumable, and all-folder search hard-stops at ten folders;
- inbound MIME tracks inline/CID metadata that outbound composition cannot currently express;
- several bounded local list APIs can say `more=true` without returning a continuation cursor;
- there is no local identity/signature/VIP/block/mute/outbox/S-MIME state;
- there is no supported Apple Account/iCloud.com settings authentication boundary in this repository.

Read [`icloud-mail-agent-parity-sweep-2026-08-10.md`](./icloud-mail-agent-parity-sweep-2026-08-10.md) selectively for the exact call paths, tests, negative findings, and landmines. The plan does not duplicate that report.

## State of Ideal System

When this track is complete, an agent can use `icloud` as a broadly Apple-Mail-equivalent **mail-account operator** without the CLI pretending to own Apple-only classification or undocumented web APIs.

### Provider-backed mail behavior

- One account can own multiple `MailIdentity` records: the primary iCloud address plus explicitly configured aliases, custom-domain addresses, and eligible Hide My Email addresses. One identity is the local default Send From address. Authentication remains account-owned and separate from sender identity.
- All enabled account identities form the canonical self-address set used by reply-all, sender selection, outbound/inbound direction, signatures, and S/MIME identity matching.
- Proven replies update the selected source provider location to `\\Answered`; public message/thread state exposes reply history. Forward history is always available from durable local provenance and gains provider state only if iCloud exposes a safe writable representation during compatibility testing.
- User semantic folder-role bindings are durable and distinct from provider `Folder.Kind`. Provider-discovered roles remain visible; an explicit user binding can supply Archive, Trash, Drafts, Junk, or Sent semantics when provider metadata is missing.
- Junk/Not Junk are thin provider-native mailbox operations: move to the bound/discovered Junk role or move Junk to Inbox. The CLI contains no spam classifier.
- Location changes support bounded direct-message batches, thread targets, and reviewed immutable `MailSelection` snapshots while retaining exact per-item provider evidence, idempotency, and truthful partial-work semantics.
- Empty Trash/Junk is a bounded resumable purge built from exact provider-location deletes, never mailbox-wide blind EXPUNGE.
- Remote search is resumable across UID windows and arbitrary current selectable folders without turning one invocation into an unbounded scan.
- EML and mbox import/export use the existing canonical raw/MIME/blob/identity path rather than a second mail representation.
- Attachment removal is a first-class content rewrite with durable old/new provider evidence, not a disguised move operation.
- Outbound MIME supports inline/CID attachments across send, replies, forwards, and drafts.
- Generic Inbox/folder/thread reads have one consistent command vocabulary and strict structured request contracts for agents while existing public names remain shallow aliases to the same code path.

### Local client-state behavior

The following state is intentionally local to this CLI unless Apple later documents a supported synchronization surface:

- VIP and blocked sender preferences;
- thread mute state;
- reusable text/HTML signatures and identity defaults;
- reviewed selection snapshots;
- a conservative Outbox for definitely pre-acceptance retryable sends only;
- S/MIME certificate/trust/private-key references needed by this client.

Local preference state is explicitly labeled as local in output/docs and never misrepresented as Apple Mail/iCloud account state.

### Provider capability boundary

`mail settings` and `capabilities --json` distinguish:

- functionality implemented over current IMAP/SMTP/HTTPS standards;
- local CLI preferences;
- Apple features documented in iCloud.com but unavailable through the current app-specific-password protocol boundary;
- provider behavior that is explicitly `unverified` until the live compatibility phase resolves it.

No implementation phase introduces a hidden Apple Account web session, private iCloud endpoint scraper, custom spam/category classifier, or local rules engine.

### Hard cross-phase vocabulary

These names cross phase boundaries and should not be reinvented later:

- **`MailIdentity`** — an account-owned address that counts as self and may be enabled for outbound sending.
- **`FolderRoleBinding`** — an explicit user-owned semantic folder role, separate from provider `Folder.Kind` evidence.
- **`MailSelection`** — a bounded immutable reviewed set of stable message/location targets with an expiry and content-independent request hash.
- **`RemoteSearchCursor`** — opaque account/query/folder/UID scan continuation state.
- **`SenderPreference`** — local per-address VIP/blocked preference state.
- **`ThreadPreference`** — local per-conversation mute preference state, reconciled across canonical thread redirects.
- **`Signature`** — reusable text/HTML content, optionally defaulted to one `MailIdentity` and operation kind.
- **`OutboxItem`** — private durable replay state for a definitely safe-to-retry pre-acceptance outbound failure.
- **`MessageRewrite`** — durable provider replacement state for content-changing operations such as removing attachments.
- **`SMIMEIdentity`** — one sender identity’s certificate/private-key reference and signing/encryption policy.

### Global traversal contract

For every bounded public list in scope, `more=true` means a valid opaque `next_cursor` is also returned. Cursors are account/scope/query bound and reject changed requests. No public list may acknowledge unreachable older records.

### Global safety/output contract

Every new consequential write follows the existing product contract: compact plain text by default, explicit JSON/JSONL, bounded work, no implicit non-TTY confirmation, dry-run/preview where the action is destructive or externally visible, stable idempotency where a retry could duplicate an external effect, no BCC/private payload in ordinary telemetry, and explicit unknown/unresolved states when provider acceptance cannot be proven.

## Decisions and Assumptions

### Decisions

#### 1. Scope boundary: account parity before Apple-client imitation

**One-way for this track.**

- **A. Build durable mail-account and agent-workflow parity while using Apple-native provider behavior where it is exposed.** — **Recommended**
- **B. Recreate every Apple Mail UI/client feature locally.**
- **C. Reverse-engineer iCloud.com private APIs to imitate Apple-only state.**

**Why A:** the existing product is a safe IMAP/SMTP account client, and the user explicitly rejected custom categorization/rules classification. B adds unrelated client behavior; C would make ordinary mail correctness depend on an unsupported authentication/API surface.

**Selected:** A.

---

#### 2. Sender identity ownership

**One-way schema decision.**

- **A. Add account-owned `MailIdentity` rows and keep account authentication separate.** — **Recommended**
- **B. Free-form From only.** Add only a `--from` flag without durable identity state.
- **C. Separate profiles.** Create one CLI account profile per alias/custom-domain address.

**Why A:** From identity affects reply-all self exclusion, message direction, signatures, Hide My Email, and S/MIME in addition to SMTP compose. B leaves those paths inconsistent; C duplicates credentials/sync state for one mailbox.

**Selected:** A.

---

#### 3. Historical self/direction semantics after identities are added

**One-way data-semantics decision.**

- **A. Treat every enabled identity as self and run a bounded/resumable reclassification of cached messages whose direction is now provably outbound.** — **Recommended**
- **B. Future mail only.** Apply identities only to new mail.
- **C. Read-time derivation.** Stop persisting direction and compute it ad hoc on every read.

**Why A:** current direction is persisted and monotonic and is already a public read contract. B leaves historical behavior wrong; C rewrites a mature identity/read model and makes every consumer re-derive provider evidence.

**Selected:** A.

---

#### 4. Semantic folder overrides

**One-way schema boundary.**

- **A. Persist `FolderRoleBinding` separately from provider `Folder.Kind`/`KindSource`.** — **Recommended**
- **B. Overwrite provider kind.** Replace `Folder.Kind` when the user configures a role.
- **C. Name inference.** Expand English-name guessing.

**Why A:** provider refresh owns `Folder.Kind`, and `ResolveKind` intentionally stopped name guessing after a real safety defect. B creates competing ownership; C reintroduces the defect class.

**Selected:** A.

---

#### 5. Junk semantics

**Reversible product surface, fixed behavior.**

- **A. Use Apple’s existing Junk mailbox behavior only: Junk means exact move to semantic Junk; Not Junk means exact move from Junk to Inbox.** — **Recommended**
- **B. Local classifier.** Add a local spam classifier.
- **C. Rules inference.** Infer junk from sender/rules.

**Why A:** Apple already performs iCloud junk classification and documents folder moves as the compatible behavior for other clients. The user explicitly does not want local classification code.

**Selected:** A.

---

#### 6. Bulk and thread mutations

**Reversible public API, load-bearing semantics.**

- **A. Reuse exact per-location mutation intents and expose bounded multi-target orchestration with the existing validate-first/fail-fast-or-continue contract.** — **Recommended**
- **B. Fictitious global atomicity.** Pretend one database/IMAP transaction can make a batch atomic.
- **C. Shell loops.** Leave agents to shell-loop single-message writes.

**Why A:** the existing mutation journal already captures provider ambiguity and idempotency. IMAP cannot roll back accepted earlier operations, so B is false; C makes common agent workflows less safe and less observable.

**Selected:** A.

---

#### 7. Query-to-action safety

**One-way local data contract.**

- **A. Materialize reviewed targets into an immutable bounded `MailSelection` before query-derived destructive actions.** — **Recommended**
- **B. Live-query execution.** Let destructive commands rerun a query at execution time.
- **C. Huge argv sets.** Encode all selected IDs on argv.

**Why A:** the local index can change between review and mutation. A binds the action to exactly what was reviewed without exposing huge ID sets in shell history; B introduces query drift; C is awkward and unbounded.

**Selected:** A.

---

#### 8. Remote search continuation

**One-way cursor contract.**

- **A. Persist no server-side search state; return an opaque `RemoteSearchCursor` containing signed/bound query/folder traversal and per-folder UID frontiers.** — **Recommended**
- **B. Raise hidden limits.** Increase candidate/folder limits until most users stop seeing partial results.
- **C. Require synchronization.** Force callers to sync/backfill whenever a remote page is partial.

**Why A:** it preserves today’s bounded provider work while making the result space traversable. B converts hidden limits into scalability problems; C defeats the explicit current-provider search feature.

**Selected:** A.

---

#### 9. Content-changing provider operations

**One-way schema decision.**

- **A. Model attachment removal as a durable `MessageRewrite` with append-new-first, exact confirmation, then old-location deletion.** — **Recommended**
- **B. Reuse location intents.** Reuse `LocationMutation` despite its content-stability assumptions.
- **C. Local-only rewrite.** Rewrite only the local cached raw message.

**Why A:** removing an attachment changes raw hash, MIME parts, attachments, search projection, and provider UID evidence. B’s invariant is location-only; C would make local state disagree with iCloud.

**Selected:** A.

---

#### 10. Local client preferences

**Reversible.**

- **A. Store VIP, blocked, mute, and signatures locally and label them as local.** — **Recommended**
- **B. Wait for Apple sync.** Do not implement them unless Apple sync state is available.
- **C. Scrape Apple state.** Scrape Apple Mail/iCloud client databases or web APIs.

**Why A:** these features are useful to the agent even without cross-device synchronization and fit the existing local-first product. C creates unsupported coupling; B leaves straightforward agent workflows missing.

**Selected:** A.

---

#### 11. Outbox semantics

**One-way safety decision.**

- **A. Queue only explicitly eligible failures known to be pre-acceptance; replay the exact reviewed raw bytes/envelope and never queue unknown/post-DATA/accepted outcomes.** — **Recommended**
- **B. Retry every failure.** Automatically retry all failed and unknown attempts.
- **C. Recompose later.** Recompose queued messages later from public attempt metadata.

**Why A:** the existing send architecture correctly treats possible post-DATA delivery as unknown and prevents automatic duplicate delivery. C cannot reconstruct BCC/private envelope state from public attempt telemetry and could change reviewed bytes.

**Selected:** A.

---

#### 12. Provider settings surface

**Reversible product boundary.**

- **A. `mail settings` reports local settings plus provider capability/unsupported status; do not implement iCloud.com-only forwarding, auto-reply, rules, alias creation, or Hide My Email creation without a supported auth/API.** — **Recommended**
- **B. Private web integration.** Reverse-engineer iCloud.com immediately.
- **C. No settings surface.** Omit settings entirely.

**Why A:** Apple documents these account features, but the repository authenticates only through app-specific-password IMAP/SMTP and has no supported settings API. A gives agents truthful discovery without adding brittle hidden web behavior.

**Selected:** A.

---

#### 13. S/MIME implementation boundary

**One-way dependency/security decision.**

- **A. Use the existing `go-message` MIME path plus a maintained pure-Go CMS/PKCS#7 primitive (`github.com/smallstep/pkcs7`), with explicit chain/trust/email/key-usage validation and external private-key references.** — **Recommended**
- **B. OpenSSL runtime.** Shell out to OpenSSL.
- **C. Signature-only trust.** Treat CMS signature verification as trusted identity without chain validation.

**Why A:** the project ships CGO-disabled cross-platform binaries and already has a Go MIME parser. The package’s plain `Verify` helper does not establish trust, so chain validation must be explicit. B adds a runtime dependency; C is a security defect.

**Selected:** A, subject to the interoperability assumption below.

---

#### 14. Public command cleanup

**Reversible naming decision.**

- **A. Introduce canonical `mail messages list`, `mail threads list/show`, and `mail folders refresh`, while retaining existing `inbox`, `latest`, `thread show`, and `folders status` as shallow aliases to the same implementation.** — **Recommended**
- **B. Breaking rename.** Rename and remove the old commands immediately.
- **C. Current nouns only.** Keep only the current inconsistent nouns.

**Why A:** agents get a predictable noun/verb surface without breaking existing users or maintaining two behavior paths. The aliases must call the same command service, not become duplicated implementations.

**Selected:** A.

---

#### 15. Explicit exclusions

**One-way scope decision for this plan.**

- **A. Exclude custom rules/category classification, Primary/Updates/Transactions/Promotions, Send Later, Undo Send, reminders/follow-up, saved searches/Smart Mailboxes, Mail Drop, durable event logging, the provider >1,000 LIST-snapshot guard, and the >100 dynamic-completion limitation.** — **Recommended**

**Why A:** these are the user’s explicit exclusions from the earlier audit, including the follow-up decision not to recreate Apple’s categories or spam classification ourselves. Nearby phases may expose truthful unsupported capability state but must not implement these features incidentally.

**Selected:** A.

### Assumptions

The plan proceeds on these falsifiable assumptions. An implementation agent must amend this plan immediately if a load-bearing assumption fails.

- **Alternate iCloud From addresses authenticate through the existing mailbox account.**
  Assume iCloud SMTP accepts an enabled alias/custom-domain sender in the SMTP envelope/RFC From while authenticating with the account’s existing app-specific-password credentials. **Load-bearing for Phases 5–6 and 29.** Live compatibility must prove it before claiming support.

- **`\\Answered` is safely writable through ordinary UID STORE.**
  Assume iCloud accepts the standard flag on an exact current location with the same CONDSTORE race protections used for Seen/Flagged. **Load-bearing for Phase 7.** If false, public reply history remains local-only and the phase must amend provider claims.

- **Forwarded and colored-flag provider state may not be exposed over documented IMAP.**
  Proceed with local forward provenance regardless; Phase 8/28 may conclude `unsupported` after a deterministic/live compatibility probe rather than inventing state.

- **A pre-existing Hide My Email address may or may not be SMTP-sendable through app-specific-password transport.**
  Phase 29 treats creation/management as unsupported under current auth and gates sender use on live evidence.

- **The disposable live harness may not possess every iCloud+ feature or certificate fixture.**
  Provider-facing phases still require deterministic protocol fixtures and should use the live account where the feature is actually available. Lack of an entitlement is not evidence that the protocol is unsupported.

- **`github.com/smallstep/pkcs7` remains compatible with the project’s supported Go/toolchain targets and can interoperate with representative Apple Mail CMS payloads when MIME framing and trust validation are implemented correctly.**
  **Load-bearing for Phases 32–34.** Interoperability failure requires an Amendment before choosing another CMS library.

- **No supported Apple Account/iCloud Mail settings API is available through the project’s current app-specific-password authentication boundary.**
  This assumption is based on current Apple documentation. A future documented API may expand Phase 30, but implementation agents must not substitute private web endpoints without a new user scope decision.

- **Contacts remain outside this repository.**
  Recipient address books/groups are not added to the mail database. New compose APIs continue to accept explicit RFC addresses; a future Contacts integration can resolve them externally.

## Acceptance Criteria

### Identity and sender correctness

1. Every existing account is migrated with exactly one primary `MailIdentity` matching its current email address, with no credential or stable account ID change.
2. An account can store multiple unique identities with address, display name, kind/provenance, enabled state, and one local default sender.
3. Identity CRUD/list/show/default operations are account-isolated, cursor-bounded where listed, and do not contact SMTP merely to edit local configuration.
4. A disabled identity cannot be selected as an outbound sender.
5. Send, reply, reply-all, forward, draft create/update/send, and dry-run can select an enabled identity explicitly.
6. Omitting sender selection uses the configured default identity and preserves the legacy primary-address behavior after migration.
7. SMTP authentication remains account-owned; selecting another From identity does not duplicate credentials or mailbox state.
8. The SMTP envelope From and RFC `From` header use the selected identity only after provider validation accepts that address.
9. All enabled identities are excluded from reply-all self recipients.
10. Messages sent from any enabled identity are classified outbound on future ingestion.
11. Adding an identity can repair provably misclassified historical messages through bounded/resumable work without scanning the whole mailbox synchronously.
12. Identity changes never rewrite immutable message/location/attachment/thread IDs.

### Replied and forwarded state

13. A proven successful reply/reply-all sets `\\Answered` on the exact selected provider parent location when iCloud supports the standard mutation.
14. Reply state mutation uses UIDVALIDITY/mod-sequence race protections and persists the provider-returned current state.
15. A failed or unknown SMTP reply never marks the source answered merely because a reply attempt exists.
16. If SMTP acceptance is proven but the answer-state provider write fails, the sent attempt remains sent and the parent exposes a recoverable reconciliation state without resending.
17. Message JSON/compact output exposes aggregate answered/replied state without requiring raw location inspection.
18. Thread aggregates can indicate that the conversation contains a replied message without exposing private recipient data.
19. Local forward history derives from proven/durable forward attempts and is available even if iCloud has no forwarded keyword.
20. Provider forwarded-state support is claimed only when compatibility testing discovers and proves a safe representation; otherwise capabilities say unsupported/unverified truthfully.

### Folder roles and Junk

21. Provider `Folder.Kind` and `KindSource` remain provider/discovery evidence and are never overwritten by a user role override.
22. Users can list/set/clear one `FolderRoleBinding` for Sent, Drafts, Archive, Trash, and Junk.
23. A role binding points only to a current selectable folder in the selected account and conflicts visibly with duplicate/invalid assignments.
24. Semantic shortcuts resolve a valid explicit user binding first, then trustworthy provider semantics, then the existing Sent compatibility fallback only where already supported.
25. A stale/missing bound folder fails safely instead of silently choosing a similarly named mailbox.
26. `mail junk` moves exact current locations to the semantic Junk mailbox without performing local content classification.
27. `mail not-junk` moves exact current Junk locations to Inbox.
28. Junk/Not Junk support bounded multi-message input and provider-safe partial-result reporting.
29. The CLI never claims to implement its own spam scoring, Primary/Updates/Transactions/Promotions categories, or filtering rules.

### Bulk, thread, selection, and purge workflows

30. Move/copy/archive/unarchive/trash/restore/delete accept bounded multiple direct message targets through flags/JSON/JSONL without shell-looping provider writes.
31. Every attempted location write retains one durable exact mutation record and the same idempotency/ambiguity semantics as the existing single-message path.
32. Bulk `atomic` mode validates the bounded target set before starting and stops on first target/provider failure without falsely claiming rollback of prior accepted writes.
33. Continue-on-error mode emits ordered per-item result/error records and a final completion summary with requested/attempted/completed/failed/skipped/partial accounting.
34. Thread read/flag/location operations resolve one canonical thread/alias into bounded eligible message locations and never mutate unrelated copies implicitly.
35. Thread-level read/flag actions have the same provider race protections as direct message actions.
36. `MailSelection` can snapshot a bounded reviewed local search/thread/message result into exact stable targets with account/scope binding, expiry, and request hash.
37. A selection does not silently add newly matching mail after creation.
38. Expired, cross-account, or changed-scope selections fail before provider writes.
39. Destructive selection-based actions support a compact dry-run that reports exact count/folders and stable selection ID without echoing message bodies.
40. Empty Trash/Junk enumerates exact current locations in bounded resumable batches, supports dry-run, and never uses a mailbox-wide blind EXPUNGE.
41. Cancellation between purge batches leaves already confirmed deletions truthful and continuation state usable.

### Traversal and command ergonomics

42. `account list` returns `next_cursor` whenever `more=true` and can enumerate every configured account within the supported store.
43. `mail attachments list` returns `next_cursor` whenever `more=true` and can enumerate a set larger than 100.
44. Outbound-attempt status, location-mutation status, and sync-checkpoint listing return usable cursors whenever `more=true`.
45. All new cursors are opaque, account/scope bound, deterministic for unchanged local state, and reject changed request parameters.
46. `mail messages list` is the canonical generic synchronized-folder message listing command; `mail inbox` remains a shallow compatibility alias/default.
47. `mail threads list` and `mail threads show` are canonical conversation commands; `mail latest` and `mail thread show` share the same implementation path as compatibility aliases.
48. `mail folders refresh` is the canonical provider-refresh name; `mail folders status` remains a compatibility alias to the same behavior.
49. Canonical aliases do not create duplicate stores, schemas, output shapes, or maintenance paths.
50. Complex read commands publish strict JSON request schemas with the same unknown-field/trailing-value/size discipline as writes.
51. Read JSON input never weakens explicit content-expansion privacy: full body/HTML/raw/headers remain opt-in.
52. `capabilities --json` enumerates the read and write structured contracts without bloating compact capabilities output.

### Remote search and synchronization ergonomics

53. Remote search can continue from a returned `RemoteSearchCursor` without repeating or skipping the prior scanned UID interval.
54. The remote cursor is bound to account, complete query/filter set, selected folder set/order, UIDVALIDITY, and per-folder scan frontier.
55. UIDVALIDITY change makes an old remote cursor stale with an actionable restart error rather than silently continuing.
56. One remote call retains explicit candidate-UID/folder work ceilings and never becomes an unbounded provider scan.
57. `--all-folders` remote search can traverse more than ten current selectable folders across continuation pages instead of rejecting the account solely for folder count.
58. Results remain globally deterministic newest-first across folders for a stable provider snapshot, with explicit `more`, `partial`, and `next_cursor` semantics.
59. Targeted hydration continues to use `rem_...` references and does not advance normal sync checkpoints.
60. `mail sync --until-current` repeatedly invokes the existing bounded commit path until selected forward frontiers are current or an explicit total-work/time ceiling is reached.
61. `mail sync backfill --until-complete` repeatedly advances the exact historical frontier without changing forward checkpoints.
62. Drain helpers expose every committed page/partial failure and resume from durable frontiers after interruption.
63. Drain helpers do not silently change the semantics of ordinary one-page `mail sync` / `backfill`.
64. `mail watch --all-accounts` can traverse more than ten configured accounts up to a documented process safety maximum without silently omitting later accounts.
65. Watch continues to use account-scoped notification workers, bounded reconnect, the existing write locks, and compact noise suppression.

### MIME, attachments, import, and export

66. Outbound attachment requests can explicitly describe attachment versus inline disposition and a validated Content-ID.
67. HTML messages can reference inline CID assets that arrive with matching MIME disposition/Content-ID in an independent recipient client.
68. Plain attachments preserve existing behavior when no inline metadata is requested.
69. Draft create/update/sync/send preserves inline/CID attachment metadata across revisions.
70. Reply/forward/send share one outbound MIME attachment model rather than separate implementations.
71. Removing attachments from a stored message requires explicit selected attachment IDs and a dry-run that shows which exact provider location/content will be replaced.
72. `MessageRewrite` persists old/new raw identity, provider source/destination evidence, status, and reconciliation state before the original provider copy can be removed.
73. Attachment removal appends and confirms the rewritten replacement before exact deletion/expunge of the old provider location.
74. A partial/disconnected rewrite never leaves local state pretending the provider replacement is complete.
75. Successful rewrite reparses through canonical ingestion so content, parts, attachments, FTS, thread identity, and stable logical-message semantics remain coherent.
76. `mail message import <file.eml> --to <folder>` validates/streams one RFC message, appends it safely, and reconciles it into canonical local identity without requiring full-history sync.
77. EML import has idempotency/recovery behavior that prevents accidental duplicate APPEND on an ambiguous provider outcome.
78. Mailbox export streams deterministic mboxrd without materializing the entire mailbox or all raw MIME in memory.
79. Mailbox export can page through arbitrary synchronized folders and preserves exact stored RFC bytes per message.
80. mbox import parses/streams one message at a time, persists a source fingerprint and resumable offset/frontier, and is safe to restart.
81. A failed mbox message does not advance the import frontier beyond the last proven append/reconciliation.
82. Import/export default output remains compact metadata; raw mailbox bytes always require an explicit destination/stdout intent.

### Mailing-list, VIP/block, mute, and signatures

83. Message inspection can expose sanitized mailing-list unsubscribe capability derived from `List-Unsubscribe` / `List-Unsubscribe-Post` without printing endpoint tokens in ordinary compact output.
84. `mail unsubscribe` previews the exact method/host and requires explicit authorization before an HTTPS or outbound-mail unsubscribe action.
85. Standards-compliant one-click HTTPS unsubscribe uses a bounded TLS request and never follows an arbitrary credential-bearing browser session.
86. Mailto unsubscribe reuses the safe outbound send path and its idempotency/unknown-outcome rules.
87. Local `SenderPreference` supports VIP and blocked flags by normalized exact address, is account-isolated, and is explicitly described as local CLI state.
88. VIP/block preferences do not automatically classify, delete, or move incoming mail.
89. Local search/message/thread outputs can filter/show VIP/blocked status without rewriting provider messages.
90. `ThreadPreference` supports mute/unmute and follows canonical thread redirects/merges without applying mute to an unrelated split component silently.
91. Muted threads continue to synchronize and remain searchable/readable; mute affects only the CLI’s local alert/watch presentation unless another explicit operation is requested.
92. `Signature` supports text plus optional HTML, account ownership, optional identity binding, and operation-specific defaults.
93. Send/reply/reply-all/forward/draft preview shows the final signature-expanded content before any SMTP write.
94. Idempotency hashes the final normalized message including selected identity/signature expansion so changed signature content conflicts rather than silently reusing an old send.
95. Ordinary success telemetry never echoes the signature/body after send.

### Provider-specific compatibility and settings

96. Generic `\\Flagged` behavior remains unchanged even if colored-flag compatibility is added.
97. Colored-flag support is exposed only after deterministic/live evidence identifies a provider representation that round-trips through iCloud; otherwise capabilities explicitly report it unsupported/unverified.
98. A colored-flag implementation preserves unknown flags and exact location race semantics and never fabricates Apple flag labels locally as provider state.
99. A manually configured Hide My Email address can be represented as a `MailIdentity` without exposing its forwarding destination in ordinary output.
100. Hide My Email outbound use is enabled only after provider compatibility proves SMTP acceptance; creation/deactivation/label management remains unsupported unless a supported Apple API is later added by Amendment.
101. `mail settings` clearly separates local settings, supported IMAP/SMTP capabilities, unverified provider behavior, and Apple-web-only features.
102. `mail settings` does not claim to configure iCloud automatic forwarding, automatic reply, web rules, category views, alias creation, custom-domain provisioning, or Hide My Email creation under the current auth boundary.
103. Documentation names the supported path for those Apple-managed settings instead of silently simulating them.

### Outbox

104. An `OutboxItem` can be created only from a send failure classified definitely pre-acceptance/retryable under the existing SMTP outcome model.
105. Unknown, post-DATA, accepted, or reconciliation-only failures can never enter automatic/replayable Outbox state.
106. Outbox storage keeps exact reviewed raw bytes and private SMTP envelope data behind the same private local-state permissions; ordinary status redacts recipients/BCC/body.
107. `mail outbox list/show/retry/cancel` is explicit; this track adds no background scheduler, Send Later, or Undo Send.
108. Retry reuses the exact reviewed bytes/envelope and records ancestry/new attempt identity so duplicate behavior remains auditable.
109. Cancellation removes only a queued local item and never claims to recall mail already accepted by SMTP.

### S/MIME

110. Inbound message parsing identifies signed/encrypted S/MIME containers without breaking ordinary MIME/raw reads when the CMS payload is malformed or unsupported.
111. Signed-message inspection reports cryptographic signature result, signer certificate identity, certificate dates/key usage, email-address binding, and a separate explicit trust-chain result.
112. No code path treats a CMS signature as trusted merely because the signature math verifies.
113. Recipient certificates learned/imported for encryption are account/address scoped and can be listed/removed without exposing private key material.
114. `SMIMEIdentity` stores certificate/public metadata plus an external private-key/keyring/file reference; private key bytes never enter SQLite, logs, schemas, compact output, or Git fixtures.
115. Encrypted inbound messages can be decrypted locally when the selected identity has the matching key and remain explicitly unavailable otherwise.
116. Decrypted content is processed through the canonical MIME parser with the same body/attachment limits and privacy rules as ordinary mail.
117. Send/draft/reply/forward can explicitly sign and/or encrypt using one selected sender identity.
118. Encryption fails before SMTP if any required recipient certificate is missing/untrusted under the chosen policy.
119. Signed/encrypted outbound MIME is independently readable/verifiable by representative Apple Mail-compatible fixtures before support is accepted.
120. S/MIME remains optional; accounts without certificates experience no change in ordinary compose/read behavior or binary packaging portability.

### Whole-track safety, performance, docs, and exclusions

121. Every new table/index/migration upgrades schema-19 state without changing existing stable account/folder/message/location/thread/attachment/draft/outbound IDs.
122. No migration performs an unbounded full-mailbox reconstruction during database open; large derived/backfill work is bounded/resumable.
123. Every new list/search/import/export/bulk operation has an explicit work/page bound and representative large-state test or query-plan evidence where scale is material.
124. Provider I/O remains cancellation/deadline bounded and no new goroutine/socket writer can outlive a closed/reused protocol object.
125. Existing BCC, raw/body privacy, file permissions, blob verification, unknown-send, and exact-location safety regressions remain green after every affected phase.
126. Every substantial public phase updates command-local help/examples and the relevant task-focused docs; generated reference/man/completion/schema assets stay drift-free.
127. Final package validation proves one `icloud` executable, CGO-disabled Darwin/Linux/Windows amd64/arm64 builds, verified npm packaging, and no local database/blob/private fixture in the tarball.
128. The complete expansion has a numbered acceptance matrix mapping these criteria to deterministic and, where safe/available, live provider evidence.
129. No product code implements a custom rules engine, Primary/Updates/Transactions/Promotions categorizer, local spam classifier, saved-search/Smart-Mailbox system, Send Later, Undo Send, reminder/follow-up engine, Mail Drop, or durable watch event log.
130. This track does not change the existing provider LIST snapshot guard above 1,000 folders or the dynamic shell-completion one-page limitation; those remain explicit out-of-scope issues rather than being silently “fixed” in an unrelated phase.
131. Contacts, Calendar, Drive, Notes, Photos, Find My, Apple Intelligence writing, PDF/image markup, printing, Apple Mail GUI preferences, and On My Mac mailbox management remain outside this CLI track.
132. `mail operations outbound list|show` and `mail operations mutations list|show` provide a canonical, cursor-navigable operation-history surface while `mail status --outbound|--mutations` remains a compatibility alias to the same data path.
133. Exact operation `show` resolves one stable attempt/mutation ID within the selected account and returns recovery/reconciliation state without exposing submitted BCC/recipient/body/idempotency/private blob data.
134. Legacy operation-status invocations and canonical operation commands cannot diverge in ordering, cursor behavior, error classes, or privacy semantics.

## Plan Phases

### Execution protocol for every phase

1. Start from current `main`, fetch and pull fast-forward-only, inspect status/recent history, then reconcile this plan and `gg/progress.md` against the actual code before editing.
2. Read only the phase-scoped Sweep sections and source/test/docs regions listed below; do not reload every planning artifact or historical implementation plan by habit.
3. Preserve the completed schema-19 architecture: one executable, one SQLite canonical store, one content-addressed blob root, narrow provider interfaces, explicit locks/transactions, compact default output, and deliberate expansion modes.
4. Provider-facing phases should use the authorized disposable harness described in `gg/progress.md` when the behavior can be exercised safely. Never copy its credentials, private addresses, raw payloads, state paths, or fixture identifiers into source/docs/progress.
5. Deterministic fakes remain mandatory for cancellation, ambiguity, failure injection, malformed MIME/CMS, races, pagination, scale, and any provider condition unsafe or impractical to manufacture live.
6. Every public write added to the automation contract gets strict JSON input/schema coverage; streaming/bulk operations use JSONL only when it materially helps ordered item results.
7. Every substantial user-visible slice updates existing task-focused docs or adds a distinct reader journey when necessary, regenerates public references, and records docs validation in `gg/progress.md`.
8. A phase is complete only after positive evidence that was absent before the phase, relevant regression evidence, help/docs/reference updates, a coherent commit pushed to `origin/main`, and an exact progress entry.
9. If implementation invalidates a load-bearing assumption or selected architecture, append an Amendment immediately and name every later phase affected. Do not weaken an acceptance criterion in the Amendment.
10. Keep the branch usable at every boundary. Do not begin a later phase while an earlier phase is partially implemented unless this plan explicitly marks the dependency independent.

### Phase 1 — Make every in-scope bounded local inventory traversable

#### Files to read before starting

- `gg/icloud-mail-agent-parity-sweep-2026-08-10.md` ("Pagination and bounded-list contract" plus Landmines 16–18) — the exact incomplete list surfaces and existing cursor patterns.
- `internal/store/accounts.go` (`AccountRepository.List`, `Resolve`) — current account ordering and `more` behavior.
- `internal/store/outbound.go` (`OutboundRepository.List`) and `internal/store/location_mutations.go` (`LocationMutationRepository.List`) — operation-history ordering keys.
- `internal/store/mail.go` (`MailRepository.Checkpoints`) — checkpoint list ordering and folder references.
- `internal/cli/root.go` (`newAccountListCommand`), `internal/cli/attachments.go` (`newMailAttachmentsListCommand`), `internal/cli/outbound.go` (`newMailStatusCommand`), and `internal/cli/mail.go` (`newMailSyncCheckpointsCommand`) — public output that currently reports `more` without continuation.
- `internal/store/folders.go` (`ListPage` and folder cursor encode/decode helpers) and `internal/store/drafts.go` (draft cursor paging) — the two closest repository patterns for account-bound keyset cursors.
- Representative tests: `internal/store/folders_test.go` cursor paging, `internal/cli/attachments_test.go`, and operation/checkpoint list tests adjacent to the symbols above.

#### What to do

Make the global traversal contract true for the five existing list families in scope: account list, attachment list, outbound status, mutation status, and sync checkpoints. Use domain-specific opaque keyset cursors bound to the complete selection scope rather than offset paging. The last returned record must provide the continuation sort key; cursor use with a changed account/target/status scope must fail as usage/conflict rather than silently page a different list.

Attachment paging must preserve deterministic message/thread aggregation ordering and distinct attachment IDs. Operation-history paging must preserve newest-first ordering across equal timestamps. The phase may introduce small shared cursor signing/encoding helpers only where the existing implementations genuinely duplicate the same invariant; do not force all established domain cursors through a speculative abstraction.

Public compact and JSON output must include `next_cursor` only when `more=true`. Keep existing command names and semantics otherwise. Dynamic shell completion and the provider LIST >1,000 guard are explicitly out of scope.

#### Validation strategy

Add failing-before/passing-after tests that create more than 100/one-page records in each list family and consume every page without duplication or omission, including equal-sort-key ties and cross-account/cross-target cursor rejection. Prove attachment paging reaches metadata beyond the former first 100 while `save-all` behavior remains unchanged.

Run the affected store/CLI suites plus the existing folder/draft cursor regressions and generated schema/reference drift scope.

#### What must not break

- Existing compact record shapes and default limits.
- Folder, draft, search, and thread cursor semantics.
- BCC/private outbound payload redaction in operation status.
- Round 1 database-row iteration error propagation.

### Phase 2 — Normalize the public read command vocabulary without duplicating behavior

#### Files to read before starting

- Sweep ("Public command tree", "Local search and reconstructed-conversation reads", and Landmine 31) — current command naming and generated-reference ownership.
- `internal/cli/mail.go` (`newMailInboxCommand`) — generic folder-message listing hidden behind the Inbox noun.
- `internal/cli/threads.go` (`newMailLatestCommand`, `newMailThreadCommand`, `newMailThreadShowCommand`) — thread list/show split.
- `internal/cli/folders.go` (`newFolderStatusCommand`) — network refresh currently named status.
- `internal/app/mail_search.go`, `internal/app/mail_threads.go`, and account/folder refresh methods in `internal/app/accounts.go` — service methods the aliases must share rather than wrap with new behavior.
- `internal/cli/reference.go` and `internal/cli/reference_test.go` — command-tree generation/leaf-example invariants.
- `internal/cli/root_test.go` / `internal/app/app_test.go` command-help tests — public-name compatibility expectations.

#### What to do

Introduce canonical nouns:

- `mail messages list` for the existing synchronized folder/search message listing, with Inbox still the default folder when no selector is supplied;
- `mail threads list` and `mail threads show` for reconstructed conversations;
- `mail folders refresh` for the network-backed provider folder discovery/status refresh.
- `mail operations outbound list|show` and `mail operations mutations list|show` for navigable operation history and exact stable-ID inspection.

Retain `mail inbox`, `mail latest`, `mail thread show`, `mail folders status`, and `mail status --outbound|--mutations` as shallow compatibility aliases that route into the same request/service/rendering functions. Do not maintain two command implementations, two schemas, or divergent examples. Help should point new users at the canonical surface while old invocations remain functional. Exact operation `show` must use existing stable `send_...` / mutation IDs and must not expose private outbound payload that list/status intentionally redacts.

This phase changes naming/discoverability only. It does not add new message/thread semantics or delete the old public names.

#### Validation strategy

Tests that run each canonical command and its legacy alias against the same fixture must produce equivalent data/exit behavior, including cursors and errors. Operation-history tests must page to older records and then show one exact attempt/mutation by stable ID without leaking submitted recipients/body/idempotency/private blob coordinates. Generated command reference/man/completion output must contain the canonical paths and preserve compatibility paths.

#### What must not break

- Existing scripts that call `mail inbox`, `latest`, `thread show`, or `folders status`.
- Local-versus-network behavior: `messages list` stays local; `folders refresh` remains explicit network work.
- Compact/JSON privacy and search cursor binding.

### Phase 3 — Add a strict structured request contract for complex reads

#### Files to read before starting

- Sweep ("Automation, schemas, and output") — current write-only structured contract.
- `internal/cli/input.go` (`jsonInputOptions`, strict decoder, stdin collision rules) — canonical input behavior.
- `internal/cli/automation_requests.go` — existing write request shapes and optional-field patterns.
- `internal/cli/schemas.go` (`automationCommandContracts`, schema registry/generation) — public discovery contract.
- Canonical read command constructors from Phases 1–2 plus `internal/cli/search.go` and `internal/cli/message.go` — read flags that need structured equivalents.
- `internal/cli/schemas_test.go` and `internal/cli/automation_test.go` — unknown-field, zero-value, privacy, and command-contract invariants.

#### What to do

Add strict `--input-json <path|->` request support to the complex non-streaming read/list commands where agents currently need extensive shell quoting: account list, folder list/refresh/show, messages list, local/remote search, thread list/show, message show metadata/content selection, attachment list, operation status, and checkpoint/status reads.

Define read-request schemas separately from write schemas but publish them through `capabilities --json` under the same automation contract discovery model. Structured input is an alternative to equivalent flags/positional selectors; it must reject mixed duplicate control exactly like existing writes. Content-expansion fields must map to the same `ValidateOutputOptions` rules and must never cause implicit full/raw/header disclosure merely because JSON input was used.

Do not turn watch or binary streaming output into a request/response RPC abstraction. Preserve ordinary flag syntax as the human surface.

#### Validation strategy

For representative message/search/thread/folder reads, prove a strict JSON request reaches the identical service/output result as flags, unknown/trailing fields fail before network/disk mutation, zero limits receive ordinary validation, and stdin ownership conflicts are rejected. Schema/public capability tests must validate every published read request shape and prove private blob/BCC/credential fields are absent.

#### What must not break

- Existing write automation contract and schema IDs.
- Compact output as the redirected/default mode.
- Explicit `--full`, HTML, raw, headers, body-only, verbose, JSON and binary-safe rules.

### Phase 4 — Introduce the account-owned `MailIdentity` model

#### Files to read before starting

- Sweep ("Persistence and identity shapes", "Account authentication and outbound sender behavior", Landmines 1–4).
- `internal/account/account.go` (`Account`, `AddInput`) — current primary address/auth shape.
- `internal/store/migrations/00001_accounts.sql` and `internal/store/store.go` migration discipline — primary account data to seed without changing stable account IDs.
- `internal/store/accounts.go` (create/read/resolve/remove transactions) — account lifecycle and cascade expectations.
- `internal/cli/accounts.go` / `internal/cli/root.go` account lifecycle commands and strict JSON patterns.
- `internal/id/id.go` — public UUIDv7 ID discipline.
- Account/store migration tests in `internal/store/store_test.go` and `internal/account/validate_test.go`.

#### What to do

Add the minimal durable `MailIdentity` model needed by the first end-to-end slice. Every existing account must gain one seeded primary identity matching `Account.Email` and friendly display name. An identity belongs to exactly one account, has a normalized RFC address, local display name, kind/provenance, enabled state, created/updated timestamps, and default-send designation. Keep credential/IMAP username ownership on `Account`; do not move or duplicate secrets.

Expose local `mail identities list|show|add|update|enable|disable|default|remove` workflows with stable `mid_...` IDs (or another checked-in public prefix selected consistently in this phase). Primary identity removal/disable rules must preserve a valid sender/default state and make illegal “no enabled sender” configurations unrepresentable through schema/transactions.

The phase is deliberately local: adding an alias does not yet promise SMTP acceptance. Support provenance kinds sufficient for primary, iCloud alias, custom domain, Hide My Email, and manual/other, but label user-entered provider claims as unverified until later phases.

#### Validation strategy

Migrate schema-19 fixtures and prove exactly one primary identity is seeded per account with unchanged account/credential IDs. Exercise local identity lifecycle, duplicate case-insensitive address conflicts, default reassignment, account isolation, pagination, and account deletion cascade. The existing account list/show/test/credential workflows must remain byte/privacy compatible where not deliberately expanded.

#### What must not break

- Account selection by existing primary email/name/ID.
- IMAP/SMTP credential resolution and keyring/file/env references.
- No network contact for identity configuration.
- Migration open remains bounded; do not scan messages in this phase.

### Phase 5 — Use selected `MailIdentity` for send and drafts

#### Files to read before starting

- Sweep ("Send", "Drafts", Landmines 1–2, 21–24) and the MailIdentity phase contract.
- `internal/mail/send.go` (`SendRequest`, normalization/hash) and `send_compose.go` (From/Message-ID construction).
- `internal/app/mail_send.go` (`normalizeAccountSendRequest`, SMTP auth/envelope, accepted-message completion).
- `internal/app/mail_drafts.go` (From assignment, draft compose/reconstruction/send).
- `internal/cli/send.go`, `drafts.go`, `automation_requests.go`, `schemas.go` — public flags/JSON/schema expansion.
- `internal/store/outbound.go` — request/idempotency persistence assumptions.
- Existing send/draft app/transport tests, especially STARTTLS/BCC/idempotency/unknown-outcome fixtures.

#### What to do

Resolve one enabled `MailIdentity` before composing send or draft content. Add `--from <identity-id|address>` / structured equivalent to send and draft create/update/send; omission uses the local default identity. The chosen identity’s address/display name becomes RFC From and SMTP envelope From, while authentication continues using the selected account’s existing credential/username.

Include selected identity in normalized idempotency identity and durable outbound/draft provenance so a retry cannot silently switch sender. Generate Message-ID using the selected sender domain as today’s generator does for the primary address.

Provider compatibility is part of this phase: use the disposable account to prove the primary address and any safely available configured alias/custom-domain identity. An identity that iCloud rejects as a sender must surface a typed configuration/provider error and can remain configured but unverified/disabled-for-send; never fall back silently to the primary address.

#### Validation strategy

Deterministic SMTP tests must assert separate auth username versus alternate envelope/header From, BCC envelope behavior, dry-run, draft raw MIME, request hash conflicts, and idempotent replay. Live evidence should prove at least one provider-supported alternate identity if the harness has one; otherwise record the entitlement gap and prove primary behavior plus deterministic alias acceptance/rejection paths.

#### What must not break

- Mandatory STARTTLS and unknown-send classification.
- Exact accepted raw message/Sent append reconciliation.
- Existing primary sender behavior for migrated accounts with no identity selection.
- Draft replacement/recovery fencing and BCC privacy.

### Phase 6 — Make replies, forwards, self detection, and historical direction identity-aware

#### Files to read before starting

- Sweep ("Reply", "Forward", "Message direction usage" and Landmines 3–4).
- `internal/mail/reply.go` (`BuildReplyPlan`, address normalization/removal).
- `internal/app/mail_reply.go` (`resolveReplyPlan` self set) and `mail_forward.go`.
- `internal/app/mail_sync.go` (`messageDirection`) and `internal/store/mail.go` (direction promotion/update).
- `internal/store/search.go` / `threads.go` consumers of message rows; confirm direction is not silently embedded in unrelated query semantics.
- `internal/app/mail_reply_test.go`, `mail_forward_test.go`, direction/identity tests in `internal/store/mail_test.go` and `app_test.go`.

#### What to do

Use the account’s enabled `MailIdentity` set as the sole self-address authority. Reply/reply-all/forward sender selection accepts the same identity selector as send; reply-all removes every enabled self address, including aliases/custom domains/Hide My Email.

Update ingestion direction classification to recognize any enabled self identity. When a new identity is added, historical cached messages that are now provably from self must be repairable through a bounded persisted reclassification frontier; do not scan the mailbox during identity creation or store open. Direction remains monotonic outbound for strong self/Sent/Draft evidence.

Preserve exact-parent/source selection and all current threading/provenance semantics.

#### Validation strategy

Construct a message addressed to primary plus alias and prove reply-all never sends to either self identity. Ingest outbound mail from an alternate identity in a non-Sent provider copy and prove outbound direction. Seed a large pre-identity mailbox and demonstrate reclassification advances in bounded pages, resumes after interruption, and leaves stable IDs/search/thread state intact.

#### What must not break

- No prior BCC resurrection in reply-all.
- Exact parent/source semantics and forward thread isolation.
- Round 2 message identity/dedupe correctness.
- Database open/backfill remains bounded.

### Phase 7 — Reconcile successful replies into provider `\\Answered` state

#### Files to read before starting

- Sweep ("Flags and message-state mutation", "Reply", Landmine 5–6).
- `internal/mail/message.go` (`LocationFlags`, `FlagMutation`, `ProviderFlag`).
- `internal/transport/icloud/flags.go` (`UpdateFlags`, `mutableFlag`, CONDSTORE fallback).
- `internal/app/mail_flags.go` exact-location resolution and provider-state persistence.
- `internal/app/mail_reply.go` / `mail_send.go` proven-send boundary and parent provenance.
- Store aggregate/public read code in `internal/store/mail.go`, `threads.go`, `internal/cli/mail.go`, `threads.go`.
- Existing flag mutation, reply idempotency, and SMTP unknown tests.

#### What to do

Extend the exact flag mutation boundary to support standard `\\Answered` without opening arbitrary keyword writes. After a reply/reply-all is proven sent, mark the exact selected provider parent location answered using UIDVALIDITY/mod-sequence and persist the server-returned flags. Keep the SMTP send outcome authoritative: answer-state failure cannot turn a sent reply into a failed/resent message.

Persist enough reconciliation state to retry only the answer-state update after an accepted reply when provider/local persistence fails. Aggregate message/thread public models gain answered/replied state derived from eligible locations and/or proven reply provenance, with clear distinction when provider state is pending.

If the live provider rejects `\\Answered`, amend this phase to expose local proven-reply state plus provider capability `unsupported`; do not fabricate the IMAP flag.

#### Validation strategy

Deterministic provider tests prove add/read `\\Answered`, conditional race handling, cancellation, and unknown-flag preservation. App tests cover successful reply, SMTP failed/unknown reply, sent-but-answer-write-failed recovery, idempotent resend prevention, and exact parent selection. Live compatibility should observe Apple/iCloud state if safe.

#### What must not break

- Existing Seen/Flagged mutations and bulk contracts.
- A successful reply must never be retransmitted solely to repair parent state.
- Location ambiguity remains explicit.

### Phase 8 — Expose reliable forwarded state without inventing provider semantics

#### Files to read before starting

- Sweep ("Forward", "Flags and message-state mutation", Landmines 6–7 and factual provider gaps).
- `internal/store/outbound.go` forward `source_message_id` reads/indexes and `internal/store/migrations/00014_outbound_forward.sql`.
- `internal/app/mail_forward.go` proven send path.
- `internal/mail/message.go` location unknown flags and public message state.
- `internal/transport/icloud/flags.go` extension point for a verified provider keyword only.
- Forward/provider fixture history/tests around `internal/app/mail_forward_test.go` and `internal/store/outbound_test.go`.

#### What to do

Make “forwarded” a first-class public state derived at minimum from durable proven forward provenance. Add a bounded repository query/index if needed so message/thread reads do not scan outbound history per record.

In the same phase, run a provider compatibility probe using synthetic mail to determine whether Apple/iCloud exposes a stable forwarded flag/keyword that can be safely mutated/read over IMAP. If and only if that representation is observed and regression-testable, add it as provider-backed evidence using the exact flag mutation/reconciliation boundary. Otherwise publish `provider_forwarded_state=unsupported` while keeping local forwarded history fully useful.

#### Validation strategy

A successful forward must make the source locally forwarded; failed/unknown forward must not. Large list/thread reads must avoid N+1 outbound-history queries. Provider compatibility tests either demonstrate a round trip or deterministically lock the unsupported capability without fake state.

#### What must not break

- Existing forward source immutability/thread isolation.
- Unknown provider flags remain preserved.
- No automatic provider keyword guess from Apple UI terminology.

### Phase 9 — Add durable user semantic folder-role bindings

#### Files to read before starting

- Sweep ("Folders, roles, and provider discovery", Landmines 8–10).
- `internal/folder/folder.go` kinds/protection policy.
- `internal/store/folders.go` (`Resolve`, `ResolveKind`, reconciliation/matching, folder paging).
- `internal/transport/icloud/folders.go` (`mailboxKind`, `applySentFallback`) — provider truth/fallback boundary.
- `internal/app/mail_locations.go` role shortcut resolution and `mail_drafts.go` Drafts resolution.
- `internal/cli/folders.go` folder command patterns/JSON output.
- Folder/live role safety tests from `internal/app/mail_locations_test.go`, `folder_admin_test.go`, `internal/store/folders_test.go`.

#### What to do

Add `FolderRoleBinding` as separate account-owned durable state for Sent, Drafts, Archive, Trash, and Junk. Add `mail folders role list|set|clear` with local dry-run where useful. A binding points to stable folder ID, never raw display name, and provider observations remain untouched.

Introduce one semantic-role resolver used by archive/unarchive/trash/restore, Drafts sync/cleanup, Sent append/reconciliation, Junk later, and any future role consumer. Resolution precedence is explicit valid user binding, trustworthy provider role, then the pre-existing narrow Sent compatibility fallback. Stale/missing/non-selectable bindings fail with a recovery action instead of name guessing.

#### Validation strategy

Reproduce the historical ordinary-folder-named-Archive shape and prove no special behavior occurs until an explicit role binding exists. Prove bindings survive provider refresh/rename stable-folder reconciliation, fail safely after deletion/staleness, remain account-isolated, and immediately enable the existing archive/trash/draft/Sent shortcuts without mutating `Kind`.

#### What must not break

- Provider `Kind`/`KindSource` reporting.
- System-folder destructive protections.
- Folder rename recovery and >1,000 persisted scheduler paging.
- No expansion of English-name heuristics.

### Phase 10 — Add provider-native Junk and Not Junk workflows

#### Files to read before starting

- Sweep (folder-role and location-mutation sections, Junk external fact, Landmines 8–12).
- Phase 9 semantic-role resolver.
- `internal/mail/location_mutation.go`, `internal/app/mail_locations.go`, `internal/transport/icloud/locations.go`, `internal/store/location_mutations.go` — exact move safety.
- `internal/cli/locations.go` and `internal/cli/flags.go` — single-location plus bulk item output patterns.
- Junk/special-use folder discovery fixtures in `internal/transport/icloud/client_test.go` (`TestFolderDiscoveryPreservesProviderMetadataAndStatus`, `TestFolderDiscoveryUsesSentFallbackOnlyWithoutSpecialUse`) and role safety tests.

#### What to do

Add explicit `mail junk` and `mail not-junk` semantic actions implemented strictly as provider location moves. Junk resolves the semantic Junk role and moves selected eligible source locations there. Not Junk requires a current Junk source and moves it to Inbox. Both use the exact move/COPY+UID-EXPUNGE mutation journal and are available for bounded direct-message batches using the bulk contract established later in Phase 11 only if Phase 11 has already landed; until then this phase must deliver at least one exact message end to end and define request types so Phase 11 can widen them without a second path.

Do not add spam scores, sender heuristics, rules, or category state. Documentation should state Apple/iCloud owns classification.

#### Validation strategy

Deterministic tests prove source/destination role checks, no-op/ambiguous/stale behavior, idempotent replay, direct MOVE and safe fallback, and no classifier calls/data. Live evidence should move a synthetic message to Junk and back to Inbox and confirm provider convergence if the disposable account advertises/binds Junk.

#### What must not break

- Trash and permanent-delete semantics.
- Folder-role safety and Sent exclusion rules.
- No mailbox-wide EXPUNGE.

### Phase 11 — Generalize exact location changes to bounded multi-message operations

#### Files to read before starting

- Sweep ("Exact-location move/copy/delete", "Automation, schemas, and output", Landmine 12).
- `internal/cli/flags.go` bounded message-state bulk parser/executor/JSONL accounting — canonical public pattern.
- `internal/cli/locations.go` single-operation confirmation/rendering.
- `internal/app/mail_locations.go` preview + durable mutation orchestration.
- `internal/store/location_mutations.go` idempotency/list behavior.
- `internal/mail/location_mutation.go` request/hash/action vocabulary.
- Round 2 partial JSONL tests (`internal/app/mail_sync_partial_test.go`) and existing location tests.

#### What to do

Make move/copy/archive/unarchive/trash/restore/delete and Junk/Not Junk accept up to the published bounded bulk maximum of exact message targets through positional IDs, strict JSON array/object input, and JSONL items. Preserve the single-message behavior and output compatibility for one target.

Bulk validation resolves account/message IDs and syntactic request shape before starting. Provider execution remains sequentially truthful: atomic means fail fast, continue-on-error attempts independent remaining items, and every durable mutation remains individually idempotent. Folder selectors may be global defaults with per-item overrides in structured input where ambiguity requires it.

#### Validation strategy

Prove A succeeds/B fails/C is skipped in fail-fast mode and A succeeds/B fails/C succeeds in continue mode, with correct provider call counts, durable mutation rows, JSONL order, counts, and replay behavior. Include a failure after one provider move has committed to prove no fictitious rollback claim.

#### What must not break

- Single-item exact error/output contract.
- Non-TTY confirmation and dry-run safety.
- Unknown/unresolved provider outcomes must not be auto-retried.

### Phase 12 — Add thread-level state and location operations

#### Files to read before starting

- Sweep ("Threads and local client preferences", location mutation section, Landmine 25).
- `internal/store/threads.go` (`ThreadRepository.Show`, alias resolution, chronological paging, projection state).
- `internal/app/mail_threads.go` message/thread hydration/read boundary.
- Phase 11 bulk location executor and existing flag-bulk executor.
- `internal/cli/threads.go` public canonical/alias command wiring.
- Thread graph/projection/alias tests in `internal/store/threads_test.go` and CLI thread tests.

#### What to do

Add thread-target forms for mark-read/mark-unread/flag/unflag and location-changing actions where semantics are safe. Resolve an alias to the canonical thread, collect bounded chronological/current eligible logical messages, and feed them into the same per-message bulk engines. Never mutate a provider copy outside the explicitly selected folder/source scope merely because another location belongs to the same logical message.

For large threads, require paging/explicit continuation or a bounded selection snapshot rather than materializing >10k messages in one write. A degraded/pending thread projection cannot be treated as complete destructive scope; surface repair/selection guidance.

#### Validation strategy

Use a multi-message thread with multi-location members, an alias redirect, and an unrelated same-subject thread. Prove only canonical members/current eligible source locations change, provider call/result order is stable, and an oversized/degraded projection refuses unsafe “whole thread” mutation rather than silently omitting messages.

#### What must not break

- Thread projection repair/frontier independence from canonical provider writes.
- Stable message/location IDs and thread aliases.
- Existing thread read pagination/content expansion.

### Phase 13 — Add immutable reviewed `MailSelection` snapshots

#### Files to read before starting

- Sweep ("Local search and reconstructed-conversation reads", "Bulk, thread" integration risks, Landmine 12).
- `internal/store/search.go` search query signature/cursor binding and `SearchOptions` normalization.
- `internal/store/threads.go` canonical thread resolution and stable member IDs.
- Phase 11–12 bulk executors and preview request hashing.
- `internal/id/id.go` stable ID generation; `internal/store/store.go` migration/transaction pattern.
- `internal/cli/search.go`, canonical messages/threads commands, and location dry-run rendering.

#### What to do

Introduce `MailSelection` as local durable reviewed scope, not a saved search. A selection stores account ownership, creation/expiry, source description/query signature, exact bounded logical message IDs and the exact provider-location evidence required by the requested action where appropriate. The selection never re-evaluates the source query after creation.

Add `mail selection create` from explicit message/thread IDs or one bounded current local search page, `selection show`, `selection delete`, and `selection apply` integration with the existing bulk read/flag/location actions. Require a preview/request hash for destructive selection applications so a caller can review count/folders/action before confirmation. Keep selections short-lived by default and explicitly local.

Do not implement named/persistent saved searches or Smart Mailboxes; selection is a reviewed immutable result set for action safety only.

#### Validation strategy

Create a selection from a query, add a new matching message afterward, and prove the new message is not mutated. Test expiry, account mismatch, deleted/stale provider locations, canonical thread alias resolution at creation, action request-hash mismatch, and bounded maximum membership. Reopen the database and prove an unexpired selection resumes exactly.

#### What must not break

- Search cursor/query semantics.
- No message body/recipient payload stored merely to preserve a selection.
- Selection state must not become a permanent saved-filter feature.

### Phase 14 — Add bounded Empty Trash/Junk and exact-folder purge

#### Files to read before starting

- Sweep (location mutation and folder-role sections; provider Junk retention fact).
- Phase 9 role resolver, Phase 11 bulk exact delete, and Phase 13 selection model.
- `internal/store/mail.go` location enumeration/indexes for one folder and presence/deleted eligibility.
- `internal/transport/icloud/locations.go` exact UID EXPUNGE behavior.
- `internal/app/mail_operations.go` bounded resumable-operation patterns and progress summaries.
- `internal/cli/locations.go` destructive confirmation/dry-run conventions.

#### What to do

Add `mail empty trash`, `mail empty junk`, and an explicitly selected custom-folder purge form only when the user deliberately names the folder. Each invocation enumerates a bounded page of exact current provider locations, creates/uses a selection-like continuation scope, and executes per-location permanent deletion through the existing exact mutation path.

Expose `more`/`next_cursor` or an opaque purge ID until the mailbox is empty. Dry-run reports exact current count/page and retention warning without provider writes. The workflow never marks a whole mailbox `\\Deleted` and issues generic EXPUNGE.

#### Validation strategy

Populate a folder with more than one page plus an unrelated location already marked Deleted. Prove purge deletes only enumerated exact UIDs, resumes after cancellation/failure, reaches later pages, and never expunges the unrelated message. Exercise Trash/Junk semantic bindings and custom-folder explicit authorization.

#### What must not break

- Logical message/content/thread survival when another location remains.
- Provider ambiguity remains unresolved, not replayed.
- Existing exact single-location delete remains unchanged.

### Phase 15 — Make one-folder remote search resumable

#### Files to read before starting

- Sweep ("Remote provider search and targeted hydration", Landmines 13–15).
- `internal/mail/remote.go` remote references/page types.
- `internal/transport/icloud/remote.go` (`searchRemoteSummaries`, UID window/candidate bounds, exact date filter).
- `internal/app/mail_remote.go` validation/result shaping.
- `internal/cli/search.go` remote/local option split.
- Remote transport/app tests including sparse same-day date-window regressions from Round 2.

#### What to do

Define `RemoteSearchCursor` version 1 for one folder. The cursor must bind account, stable folder ID, UIDVALIDITY, complete normalized remote filter/query signature, and the **next unscanned UID boundary**, not merely the last returned hit. Transport search accepts a before/start UID frontier so a sparse-filter continuation cannot repeat or skip bounded windows.

Return `next_cursor` whenever `more` or `partial` indicates older provider space remains. Cursor resumption uses the same per-call candidate budget and result limit. A changed query/folder/account or UIDVALIDITY rollover rejects the cursor and tells the caller to restart from current provider state.

#### Validation strategy

Use deterministic mailboxes where the first page has few/no matches across several UID windows, then place matches beyond the candidate bound. Page to completion and prove every match appears once, candidate counts remain bounded per call, exact RFC3339 boundaries remain correct, and stale/cross-query cursors fail before fetching unrelated content.

#### What must not break

- `rem_...` targeted message references and hydration.
- Default remote search stays newest-first and bounded.
- Local search cursors remain a distinct type/contract.

### Phase 16 — Continue remote search across arbitrary selectable folders

#### Files to read before starting

- Sweep (remote app fanout and schema-19 folder paging; Landmine 14).
- Phase 15 `RemoteSearchCursor` contract.
- `internal/app/mail_remote.go` (`resolveRemoteSearchFolders`, merge/sort, `maxRemoteFolderFanout`).
- `internal/store/folders.go` (`ListSelectablePage`) — persisted current-folder traversal.
- `internal/transport/icloud/remote.go` one-folder bounded primitive.
- Multi-folder sync partial-result/JSONL tests as a pattern for ordered independent folder work.

#### What to do

Replace the “more than ten folders is usage error” shape with bounded paged folder traversal. One remote-search invocation processes at most a documented number of folder scans/candidate UIDs and records the remaining folder cursor plus per-folder UID frontiers in `RemoteSearchCursor`.

Because global newest-first ordering across independently paged provider folders is non-trivial, maintain a bounded merge frontier sufficient to guarantee deterministic page order without loading every folder result. The cursor must encode enough frontier data to avoid returning older results from one folder before a known newer unconsumed result from another. If this requires a small bounded pending-hit buffer in the cursor, size and validate it explicitly; do not persist server-side session state.

#### Validation strategy

Create >10 selectable folders with interleaved timestamps, sparse hits, and equal-time ties. Consume all remote pages and prove global deterministic ordering, no duplicates/omissions, bounded provider calls per invocation, and cursor rejection after selected-folder membership/UIDVALIDITY invalidates its assumptions.

#### What must not break

- No single invocation becomes a whole-account provider scan.
- Folder inventory scheduling remains keyset-paged.
- Provider LIST >1,000 snapshot guard is explicitly out of scope.

### Phase 17 — Add bounded “drain until current/complete” convenience workflows

#### Files to read before starting

- Sweep (sync/backfill current paths and automation/output contracts).
- `internal/app/mail_sync.go` (`mailSync`, per-folder page commit, `MoreAvailable`).
- `internal/app/mail_operations.go` historical backfill/rebaseline/frontier code.
- `internal/mail/operations.go` backfill summaries/frontiers.
- `internal/cli/mail.go` sync/backfill JSONL/partial rendering.
- Existing checkpoint/frontier cancellation and partial sync tests.

#### What to do

Add explicit convenience modes `mail sync --until-current` and `mail sync backfill --until-complete`. They are loops over the existing bounded page transactions, not new unbounded provider functions. Require explicit global ceilings such as maximum pages/messages and an overall command deadline; defaults must be conservative and reported when reached.

Each committed page/folder remains independently visible in compact/JSONL output. Failure leaves existing checkpoint/frontier state as the only resume authority; the helper must never maintain a contradictory in-memory cursor after a committed transaction.

#### Validation strategy

Use a mailbox needing multiple forward and historical pages, inject cancellation/failure on an intermediate page, and prove committed frontiers resume exactly on the next command. Prove max-pages/max-messages stops cleanly with an actionable `more` state and no hidden history crawl.

#### What must not break

- Ordinary `mail sync` and `backfill` remain one bounded page per selected folder/request.
- Forward/history frontiers remain independent.
- Partial multi-folder work remains truthful.

### Phase 18 — Scale foreground watch beyond ten configured accounts

#### Files to read before starting

- Sweep ("Foreground watch", Landmine 19–20).
- `internal/mail/watch.go` (`MaxWatchAccounts`, event types/options).
- `internal/app/mail_watch.go` account enumeration, watch-owner locks, `syncGate`, reconnect/backoff.
- Phase 1 account cursor paging.
- `internal/transport/icloud/watch.go` session ownership/cancellation.
- `internal/cli/watch.go` all-account flags/output.
- Watch race/concurrency/cancellation tests.

#### What to do

Remove the arbitrary total-configuration dependence on `MaxWatchAccounts=10`. `--all-accounts` should page configured accounts locally and permit up to a documented process safety maximum aligned with the CLI’s general bounded automation ceiling (100 unless implementation evidence justifies a lower explicit cap). Accounts beyond the cap produce a clear bounded-operation error with selection guidance; they are never silently omitted.

Preserve one worker/watch-owner lock per account and the existing provider session model. Re-evaluate the process-local shared sync gate under the larger worker count: keep serialized durable commits if required by the current global account-state lock, but make queued synchronization fair/cancellation-aware so one noisy account cannot permanently starve others.

Do not add a daemon or durable event log.

#### Validation strategy

Deterministically configure >10 accounts, use fake watch sessions, and prove every selected account through the supported maximum starts, emits independently ordered events, reconnects/cancels cleanly, and gets bounded sync opportunities under one noisy account. Race tests must cover cancellation while workers wait for the shared gate.

#### What must not break

- One-account watch behavior and compact noise suppression.
- Account credential reread on reconnect.
- No provider session or goroutine survives cancellation/closed connection.

### Phase 19 — Add inline/CID outbound attachments across every compose path

#### Files to read before starting

- Sweep ("MIME, message bodies, and attachments", Landmines 21 and 23).
- `internal/mail/content.go` (`MIMEPart`, `Attachment`, Content-ID/Inline parsing) — inbound canonical representation.
- `internal/mail/send.go` (`OutboundAttachment`, normalization/hash) and `send_compose.go` MIME construction.
- `internal/app/mail_send_attachments.go` reviewed source staging/hashing.
- `internal/app/mail_drafts.go` attachment reconstruction, `mail_forward.go` original attachment conversion.
- Compose CLI/JSON request structs and schema generator.
- Existing multi-megabyte streaming, BCC, draft, reply, forward attachment tests.

#### What to do

Expand the single outbound attachment model with validated disposition (`attachment`/`inline`) and optional canonical Content-ID. Ordinary attachments retain today’s defaults. Inline attachments require an explicit ID or a deterministic generated CID that is part of the normalized request/idempotency hash.

Teach the MIME composer to emit a structurally correct multipart shape for HTML/text alternatives plus inline related resources plus ordinary attachments without buffering attachment bytes. Send, draft, reply, and forward must all use the same model. Draft persistence/reconstruction must retain disposition/CID through provider round trips.

#### Validation strategy

Compose plain attachments, HTML + one inline image, mixed inline + ordinary files, and multi-megabyte sources; parse the exact output through the canonical MIME parser and prove disposition/CID/bytes survive. Independent recipient/live evidence should verify a CID-referenced image when practical. Exact idempotent replay and changed-CID conflict behavior must be covered.

#### What must not break

- 20 MiB composed-message provider bound and source-size guards.
- Streaming memory behavior.
- BCC omission from RFC headers.
- Existing draft/forward attachment hashes and stable inbound attachment IDs.

### Phase 20 — Add safe provider attachment removal with `MessageRewrite`

#### Files to read before starting

- Sweep (content rewrite integration risk and Landmine 11).
- Phase 19 outbound MIME model and `internal/mail/content.go` canonical parser.
- `internal/app/mail_attachments.go` exact attachment lookup/blob reads.
- `internal/transport/icloud/append.go` generic APPEND/APPENDUID and `locations.go` exact UID delete.
- `internal/store/mail.go` canonical message/content/source binding and identity resolution.
- `internal/store/location_mutations.go` only as a contrast: location-only mutation invariants.
- Draft replacement recovery in `internal/app/mail_drafts.go` / `internal/store/drafts.go` — closest append-new-before-delete recovery pattern.

#### What to do

Introduce `MessageRewrite` specifically for content-changing provider replacements and implement `mail attachments remove <message> --attachment att_...` (repeatable) through it. Resolve exactly one current provider source location; dry-run lists the stable attachment IDs/metadata and resulting message-size/attachment count without rewriting.

Reconstruct the message while preserving safe original headers, bodies, non-selected MIME content, and message/thread identity evidence; selected attachment parts are omitted. Stage and parse the new raw message locally, persist rewrite intent/evidence, append the new copy first, reconcile exact new provider UID/raw identity, then exact-delete the old source. Only after provider replacement is proven should canonical representative content switch to the new raw/MIME model. Ambiguous APPEND/delete outcomes remain recoverable and non-retryable until reconciliation.

Do not promise byte-identical preservation of MIME boundaries/Date when a rewrite necessarily recomposes content; document exactly which semantic headers/content are preserved and which transport headers are regenerated.

#### Validation strategy

Test messages with duplicate filenames/hashes, inline CID parts, nested message/rfc822, multiple provider locations, APPEND without UIDPLUS, disconnect after APPEND, failure before old delete, failure after new append/before local canonical switch, and cancellation. Prove unrelated locations/content are not deleted and FTS/thread/message stable IDs remain coherent after success.

#### What must not break

- Never delete the original provider copy before replacement identity is proven.
- No local-only rewrite that disagrees with iCloud.
- Existing attachment export and content-addressed blob verification.

### Phase 21 — Import one `.eml` through canonical provider ingestion

#### Files to read before starting

- Sweep ("Mailbox import/export", canonical MIME/blob paths).
- `internal/transport/icloud/append.go` generic streaming APPEND and APPENDUID.
- `internal/app/mail_ingest.go` fetched-record preparation/canonical parse.
- `internal/store/mail.go` provider-location identity/reconciliation transaction.
- `internal/blob/store.go` staged raw limits/content addressing.
- Outbound Sent/Draft APPEND ambiguity recovery patterns.
- `internal/cli/message.go` file/raw export safety and `automation_requests.go` file input conventions.

#### What to do

Add `mail message import <file.eml> --to <folder>` as an explicit provider write. Validate a regular file, raw-size bound, RFC/MIME parseability, destination current/selectable state, and safe internal date/flags options before provider contact. Stage exact source raw, allocate an import idempotency identity, APPEND it, and reconcile the returned or subsequently discovered provider copy through the canonical ingestion transaction so local message/location/content/thread/search state is exactly the same shape as synchronized mail.

A lost APPEND response must not lead to automatic duplicate upload. Persist enough request/raw/provider evidence to search/reconcile the destination for the exact imported source before another APPEND is permitted.

#### Validation strategy

Exercise valid/invalid EML, Unicode/multipart/attachments, max-size/cancellation, APPENDUID and no-APPENDUID, disconnect after provider acceptance, idempotent replay, and import into custom Unicode folders. Live evidence should import a synthetic EML and read/export the exact content through the public CLI.

#### What must not break

- Normal sync checkpoints do not advance merely because targeted import hydrated a provider copy.
- Provider message identity/dedupe/thread logic remains canonical.
- Imported raw bytes/private paths do not enter ordinary output.

### Phase 22 — Export synchronized mailboxes as streaming mboxrd

#### Files to read before starting

- Sweep (mailbox export absence, raw message source behavior).
- `internal/store/search.go` / message folder paging for bounded ordered local traversal.
- `internal/app/mail_message.go` exact verified raw source selection.
- `internal/blob/store.go` context-aware verified streaming.
- Message `raw`/`export` file-safety code in `internal/cli/message.go`.
- mbox format primary specification/reference selected during implementation; confirm escaping rules before coding.

#### What to do

Add `mail mailbox export <folder> --format mboxrd --output <file|->`. Traverse synchronized logical/provider messages in a deterministic documented order using keyset paging; stream each verified exact raw RFC source directly to the mbox writer. Implement mboxrd `From ` separator/body escaping correctly and include a stable export summary/count/cursor or completion state without loading the mailbox into memory.

An export is local-only and never causes remote sync implicitly. If raw content for a selected message is unavailable/corrupt, fail truthfully or support an explicit continue mode that records skipped stable IDs; do not silently emit an incomplete “successful” backup.

#### Validation strategy

Export >one page including bodies beginning with `From `, CRLF variants, large attachments, Unicode headers, and a corrupt/missing raw fixture. Parse the resulting mbox with an independent/reference parser and prove message count/raw semantic round trip. Measure bounded memory on a representative large mailbox.

#### What must not break

- No network contact without an explicit preceding sync.
- Raw/BCC bytes appear only because the user explicitly requested mailbox export.
- Output file creation/replacement follows existing safe atomic file patterns.

### Phase 23 — Add resumable streaming mboxrd import

#### Files to read before starting

- Phase 21 EML import contract and Phase 22 mboxrd writer/parser evidence.
- `internal/store/migrations/00017_backfill_frontiers.sql` / operations repositories — existing resumable-frontier shape.
- `internal/app/mail_operations.go` interruption/resume patterns.
- `internal/transport/icloud/append.go` one-message APPEND boundary.
- Strict bulk/partial-result output from Phase 11.

#### What to do

Add `mail mailbox import <file.mbox> --to <folder>` using mboxrd streaming. Persist an import job keyed to account, destination stable folder ID, canonical source file fingerprint/size, byte/message frontier, and current item state. Parse and submit one message at a time through the Phase 21 EML import primitive; advance the frontier only after provider/local reconciliation proves that item.

Reject continuation if the source file changes. Support fail-fast and explicit continue-on-error only when the job can retain exact failed-item accounting without skipping the resume frontier incorrectly. Never hold a whole mbox or multiple attachment payloads in memory.

#### Validation strategy

Import a multi-page mbox with large attachments, `From ` escaping, one malformed item, provider disconnect after APPEND, cancellation, process reopen, and changed source file. Prove exact resume, no duplicate provider copies, bounded memory, and final local/provider message counts.

#### What must not break

- One-message EML import idempotency/reconciliation.
- Sync/backfill frontiers remain independent of import jobs.
- Source paths and message content remain private in ordinary progress/status output.

### Phase 24 — Add standards-based mailing-list unsubscribe

#### Files to read before starting

- Sweep (decoded headers/privacy, Landmine 26).
- `internal/mail/content.go` normalized `HeaderField` storage/parser.
- `internal/cli/message.go` public header redaction/explicit expansion.
- `internal/app/mail_message.go` local complete-message access.
- `internal/app/mail_send.go` safe/idempotent outbound path for `mailto:` methods.
- `internal/transport` failure/stage vocabulary and existing bounded HTTP-download pattern in package installer only as timeout inspiration, not as mail coupling.
- RFC 2369 / RFC 8058 primary specifications during implementation for List-Unsubscribe and one-click POST semantics.

#### What to do

Parse stored `List-Unsubscribe` and `List-Unsubscribe-Post` into a sanitized semantic capability on message reads. Add `mail unsubscribe <msg_...> --dry-run|--yes` that selects a supported method deliberately. Prefer a standards-compliant one-click HTTPS POST when the message declares it; otherwise support a `mailto:` action through the existing safe send path. Multiple alternatives require explicit selection rather than arbitrary URL order.

HTTPS unsubscribe is a new narrow standards transport: HTTPS only by default, bounded redirects/response size/deadline, no cookies/Apple Account credentials, no ambient browser session, and no arbitrary custom headers from the message beyond the standard one-click request. Ordinary compact output reports method and host, not full tokenized URLs.

#### Validation strategy

Fixtures must cover valid one-click, multiple URL/mailto alternatives, malformed/control-character headers, HTTP/downgrade/redirect attacks, oversized responses, cancellation, idempotent mailto send, and privacy redaction. A local deterministic HTTPS server proves exact one-click request semantics.

#### What must not break

- Header/body/BCC privacy.
- No generic browser automation or private web-session facility is introduced.
- Unsubscribe cannot be triggered implicitly by reading a message.

### Phase 25 — Add local VIP and blocked sender preferences

#### Files to read before starting

- Sweep ("Threads and local client preferences", local search section).
- `internal/mail/message.go` address normalization/public message model.
- `internal/store/search.go` structured sender/participant filters and projection indexes.
- `internal/store/accounts.go` account-isolated repository patterns.
- `internal/cli/search.go` filter rendering and schema generation.
- `internal/app/mail_watch.go` only the notification/display seam; do not change it in this phase.

#### What to do

Introduce account-owned `SenderPreference` keyed by normalized exact email address with independent `vip` and `blocked` booleans plus timestamps. Add `mail senders list|show|vip|unvip|block|unblock` as local-only state operations and expose sender preference on message/thread/search results where it helps triage.

Extend local search/messages/thread list filters with `--vip` / `--blocked` semantics that join/index preference state efficiently rather than loading preference lists into memory per page. “Blocked” in this phase is a local preference label only: it does not delete, move, hide, or configure Apple Contacts/iCloud blocking.

#### Validation strategy

Prove case-normalized exact-address behavior, account isolation, pagination beyond one page, local search filters, thread participant aggregation display, and no provider calls or message mutations when preference state changes. Large mailbox query-plan tests should show the new filters remain indexed/bounded.

#### What must not break

- Existing sender/participant search semantics.
- No automatic filtering/rule behavior.
- No claim that local VIP/block state syncs to Apple Mail/Contacts.

### Phase 26 — Add local thread mute preferences that survive graph changes

#### Files to read before starting

- Sweep (thread preference absence, Landmine 25, watch event limitation).
- `internal/store/threads.go` canonical resolution, aliases, merge/split/finalize paths.
- `internal/mail/thread.go` public aggregate model.
- `internal/app/mail_threads.go` canonical read boundary.
- `internal/app/mail_watch.go` compact/verbose event rendering trigger points.
- Thread merge/split/alias and deferred-projection tests.

#### What to do

Add `ThreadPreference` with mute/unmute state owned by the logical conversation, but define its canonicalization rule at the thread repository boundary so late merges and alias redirects cannot orphan preferences. On a merge, muted state follows the canonical resulting conversation. On a split caused by stronger evidence, preserve mute only on the component containing the original preference anchor; do not silently mute unrelated separated mail.

Add `mail threads mute|unmute` and expose `muted` in local thread/message context. Watch still synchronizes every message. When it can confidently resolve a change to a muted thread after sync, suppress the user-facing compact attention event while retaining verbose/JSONL synchronization truth; if exact changed thread identity is unavailable, do not skip synchronization or invent a durable event log.

#### Validation strategy

Exercise alias redirect, late merge, later split, >10k deferred projection, muted and unmuted threads under watch sync, and process restart. Prove all mail remains synchronized/searchable and only local presentation preference changes.

#### What must not break

- Thread graph identity/projection invariants.
- Watch JSONL sync-result truth and reconnect behavior.
- No rule-driven archive/trash side effect is added.

### Phase 27 — Add reusable per-identity signatures

#### Files to read before starting

- Sweep (send/draft identity + local preference seams).
- Phase 4–6 `MailIdentity` model and selected-sender resolution.
- `internal/mail/send.go` request normalization/idempotency and body limits.
- `internal/mail/send_compose.go` text/HTML body generation.
- `internal/app/mail_send.go`, `mail_reply.go`, `mail_forward.go`, `mail_drafts.go` preview/final request boundaries.
- CLI compose request parsing and dry-run renderers.

#### What to do

Add account-owned `Signature` records containing text and optional HTML, with stable IDs and optional `MailIdentity` binding. Support default signature choices by operation family (new message, reply/reply-all, forward) with an explicit “none” option. Add `mail signatures list|show|create|update|delete|default` plus compose-time override.

Apply the selected signature **before** send/draft request hashing and preview so idempotency and user review represent the exact delivered content. Define deterministic insertion semantics for text and HTML bodies, including empty body and HTML-only compose, and enforce the existing body-size ceiling after expansion. Saved drafts persist the already expanded content for that revision; later signature edits do not retroactively mutate a saved draft unless the user explicitly re-applies/updates it.

#### Validation strategy

Cover identity-specific defaults, operation-specific defaults, text+HTML insertion, no-signature override, body-size overflow, dry-run exact content, draft revision stability after signature edit, and idempotency conflict when final expanded content changes.

#### What must not break

- Explicit user body content/HTML alternative structure.
- Ordinary sent outcome remains payload-free.
- BCC and selected MailIdentity privacy/semantics.

### Phase 28 — Probe and, only if proven, support Apple/iCloud colored flags

#### Files to read before starting

- Sweep (flag model, unknown flags, provider factual gaps, Landmine 6).
- `internal/mail/message.go` canonical/unknown flag parser.
- `internal/transport/icloud/flags.go` exact STORE/re-read path.
- `internal/app/mail_flags.go` provider mutation and aggregate persistence.
- `internal/store/mail.go` unknown-flag round trip.
- Generic flag CLI/bulk implementation and JSON schemas.
- Apple Mail flag-color documentation plus live disposable harness policy.

#### What to do

Start with compatibility research inside the implementation phase: create synthetic messages, apply distinct Apple/iCloud flag colors where the harness can do so, synchronize exact provider flag sets, and determine whether the color/label representation is observable and writable over the documented IMAP path.

If a stable representation is proven, add a typed color/label layer that maps only those verified keywords/metadata through the existing exact flag mutation path. Generic `mail flag` remains the portable `\\Flagged` operation. If the representation is not exposed or cannot be safely written, finish the phase by publishing an explicit `colored_flags=unsupported` capability and documentation; do not create local colors that masquerade as Apple provider state.

#### Validation strategy

The acceptance evidence must be one of two valid outcomes: verified provider round-trip for multiple colors plus unknown-flag preservation, or a documented negative compatibility probe that proves the CLI reports unsupported without altering generic flag behavior. Never accept fixture-only guessed keyword names as provider proof.

#### What must not break

- Generic `\\Flagged` mutation and bulk commands.
- Unknown provider flags.
- No local category/rules feature is introduced through flag labels.

### Phase 29 — Represent Hide My Email as an identity without inventing its management API

#### Files to read before starting

- Phase 4–6 MailIdentity/self/send boundaries.
- Sweep external provider facts and provider-settings absence.
- `internal/app/mail_reply.go` exact parent/identity selection.
- `internal/app/mail_send.go` SMTP auth/envelope split.
- Identity CLI/public schemas/capabilities.
- Apple Hide My Email documentation named in Planning Basis.

#### What to do

Add a `hide_my_email` `MailIdentity` kind for manually configured/pre-existing HME addresses. Store only the random address, optional user-visible label, and local verification state; do not persist or expose its underlying forwarding destination as ordinary mail identity data.

Use the same self-exclusion/reply sender mechanics as other identities. When a received message was delivered to a known HME identity and a reply is requested without explicit From override, prefer that identity when doing so is unambiguous and provider compatibility has proven it can send through current SMTP auth.

Run a live compatibility probe if the harness has HME entitlement/address. If iCloud SMTP rejects HME From or no safe provider proof is available, keep the identity usable for self/delivery recognition but mark outbound support unavailable. Do not implement HME creation, deactivation, label sync, or Apple Account web authentication.

#### Validation strategy

Deterministic tests cover self exclusion, recipient-to-identity matching, ambiguous multiple-recipient cases, explicit override, privacy output, and provider accept/reject capability states. Live evidence is required before enabling HME outbound sending in capabilities.

#### What must not break

- Alias/custom-domain identity behavior.
- No automatic generation of random HME addresses.
- No forwarding destination leakage.

### Phase 30 — Consolidate truthful Mail settings and capability discovery

#### Files to read before starting

- Sweep ("Automation, schemas, and output", provider-settings factual gaps, Landmine 27).
- `internal/cli/root.go` `capabilities --json` shape and compact golden.
- `internal/cli/schemas.go` machine contract discovery.
- MailIdentity, FolderRoleBinding, Signature, Sender/ThreadPreference and HME repositories from prior phases.
- Apple current docs for default Send From, forwarding, automatic reply, aliases/custom domain, Hide My Email, and categories/rules — these are product capability references, not provider APIs.

#### What to do

Add `mail settings show` as a concise account-level capability/preference view. It should expose local default identity/signature, semantic folder bindings, optional local sender/thread preference counts, Outbox policy once available, and provider compatibility states such as alternate From, answered, forwarded, colored flags, S/MIME.

For Apple-documented iCloud.com features that current app-specific-password IMAP/SMTP cannot configure—automatic forwarding, automatic reply, server rules, alias/custom-domain provisioning, HME management, category views—report `unsupported_current_auth` with a short next action in verbose/help/docs. Do not add setters that pretend to work locally or scrape the web.

Keep compact `capabilities` stable and small; richer detail belongs in `capabilities --json` / `mail settings show --json`.

#### Validation strategy

Build accounts with different local/provider verification states and prove output distinguishes `supported`, `unsupported`, and `unverified` without network work unless explicitly refreshed. Schema/capability golden tests must reject stale advertised features.

#### What must not break

- No provider network call for a local settings read by default.
- Existing compact capabilities byte/line budget.
- Excluded rules/categories remain absent as commands.

### Phase 31 — Add an explicit safe-retry Outbox without Send Later or a daemon

#### Files to read before starting

- Sweep ("Outbound attempts", Landmines 22–23 and integration risk 7).
- `internal/mail/send.go` normalized request/idempotency/attachment hashes.
- `internal/store/outbound.go` pending/failed/unknown/sent states and exact raw source.
- `internal/app/mail_send.go` SMTP outcome classification and durable outcome context.
- `internal/transport/icloud/send.go` DATA-stage failure boundary.
- `internal/blob/store.go` private verified raw reads.
- Existing unknown-send/explicit retry tests and CLI status privacy tests.

#### What to do

Add `OutboxItem` only for outbound failures that the transport proves occurred before SMTP DATA could have been accepted. The item stores private exact raw blob identity plus the private SMTP envelope required to replay the reviewed bytes, source attempt/operation/identity metadata, failure reason, and lifecycle. It is not created for unknown/post-DATA/accepted/reconciliation failures.

Expose `mail outbox list|show|retry|cancel`. Retry is always an explicit command in this track—no scheduler, Send Later, background daemon, or automatic periodic retry. Retrying streams the exact reviewed raw bytes/envelope through SMTP, records a new linked attempt, and transitions/removes the Outbox item only according to the new proven outcome. If the retry becomes unknown, the Outbox must stop being retryable automatically and surface the normal duplicate-risk recovery path.

#### Validation strategy

Inject DNS/connect/auth/pre-DATA failures versus DATA-started disconnect/final-accept cases and prove only the definitely safe classes create Outbox items. Verify exact byte/envelope replay including BCC, process restart, explicit cancel, retry ancestry/idempotency, and complete redaction of recipients/body/private blob keys from ordinary list/status output.

#### What must not break

- Unknown sends are never automatically/repeatedly submitted.
- No queued item for already accepted mail.
- No timer/schedule/daemon behavior enters this phase.

### Phase 32 — Parse and verify inbound S/MIME signatures with explicit trust

#### Files to read before starting

- Sweep (S/MIME absence, integration risk 9, Landmine 28).
- `internal/mail/content.go` MIME parser/part recursion and content limits.
- `internal/mail/message.go` public message/content metadata.
- `internal/store/mail.go` content/part persistence and `internal/cli/message.go` explicit verbose/header output.
- `internal/secret` only to understand current private-material policy; no private key needed yet.
- `go.mod` dependency boundary and cross-platform packaging tests.
- `github.com/smallstep/pkcs7` primary package documentation, especially `VerifyWithChain`; Apple S/MIME certificate/email requirements from Planning Basis.

#### What to do

Add optional S/MIME inspection to canonical MIME parsing for common signed forms (`multipart/signed` with PKCS#7 signature and opaque signed-data where applicable). Keep the original raw/ordinary MIME representation authoritative; S/MIME parse failure is diagnostics, not a reason compact message metadata disappears.

Persist/publicly expose a bounded `SMIMEStatus` containing container kind, signed/encrypted flags, cryptographic signature validity, signer certificate fingerprint/subject/email binding, validity interval/key usage, and a **separate trust result**. Trust verification uses explicit system/configured roots with `VerifyWithChain` or equivalent; never use a helper that verifies signature math with an empty trust store and labels it trusted.

Add certificate import/list/remove for public recipient/signer certificates only, scoped by account/email, so later encryption has a durable public-key source. Keep certificate DER bounded and public; no private keys in this phase.

#### Validation strategy

Use generated CA chains and representative Apple-compatible signed fixtures: valid trusted, valid untrusted, expired, wrong email, wrong key usage, tampered content, malformed CMS, multiple signer, and large ordinary attachment coexistence. Ordinary raw/text/HTML reads must remain available when signature verification fails.

#### What must not break

- Existing MIME recursion/attachment limits and streaming raw access.
- No signature is labeled trusted without explicit chain/email policy success.
- CGO-free build portability.

### Phase 33 — Add S/MIME private-key identity references and inbound decryption

#### Files to read before starting

- Phase 32 `SMIMEStatus` / public certificate store.
- Phase 4 `MailIdentity` and selected self-address logic.
- `internal/secret/secret.go` keyring/env/file reference handling, zeroization and size/permission checks.
- `internal/mail/content.go` encrypted-container boundary and canonical nested MIME parse.
- `internal/blob/store.go` content/privacy lifecycle.
- Account credential tests as the closest external-secret-reference pattern.

#### What to do

Introduce `SMIMEIdentity` linked to one `MailIdentity`: public certificate metadata plus external private-key reference using the established keyring/env/file philosophy. Do not store PEM/PKCS#12 private bytes in SQLite. Define secure import/configuration workflows that can read a protected key/certificate source without placing secret material on argv; wipe transient bytes.

Implement explicit local decryption for supported S/MIME enveloped-data. After decrypting, run the plaintext MIME through the canonical parser with the same limits, BCC/content privacy, attachment blob storage rules, and explicit output modes. Decide and document whether decrypted derived content is cached encrypted-at-rest only by OS filesystem permissions or reparsed on demand; prefer the simplest design that does not persist new plaintext copies unnecessarily. If persisted for search/read parity, it must use the existing private blob root and clear availability/trust metadata.

#### Validation strategy

Cover matching/wrong/missing key, password-protected key input if supported, malformed ciphertext, cancellation, oversized decrypted content, nested signed+encrypted messages, process restart with keyring/file refs, and secret redaction scans. Prove ordinary metadata/raw encrypted MIME remains readable without the key.

#### What must not break

- Existing account credential references remain independent from S/MIME keys.
- Private key material never appears in schemas/logs/compact output/database.
- Message stable IDs/provider locations do not change because decryption becomes available.

### Phase 34 — Sign and encrypt outbound mail through the normal send/draft path

#### Files to read before starting

- Phases 32–33 S/MIME model and MailIdentity linkage.
- `internal/mail/send.go` final normalized request, idempotency, body/attachment bounds.
- `internal/mail/send_compose.go` current MIME writer and Phase 19 inline/CID structure.
- `internal/app/mail_send.go` exact staged raw/idempotency/SMTP boundary.
- `internal/app/mail_drafts.go`, `mail_reply.go`, `mail_forward.go` shared compose paths.
- Recipient certificate lookup from Phase 32.
- Existing streaming/large attachment, BCC, unknown-send and draft replacement tests.

#### What to do

Add explicit compose controls to sign, encrypt, or sign-then-encrypt using the selected `MailIdentity`/`SMIMEIdentity`. Resolve every recipient certificate before SMTP; encryption cannot partially encrypt or silently downgrade. BCC recipients require certificates for envelope delivery but must remain absent from visible MIME headers.

Compose the ordinary message first through the existing body/inline/attachment writer, then apply S/MIME wrapping/signing in a bounded streaming/staging pipeline. The final cryptographic MIME bytes—not the pre-S/MIME draft—are the raw request hashed/staged/sent/idempotently replayed. Draft behavior must be explicit: a saved draft may retain ordinary editable content and apply S/MIME at send time, while dry-run can show intended certificate coverage without exposing private key material.

Provider/Sent reconciliation continues to use generated Message-ID/strong identity evidence even when the outer MIME is encrypted; ensure the selected design keeps enough identity evidence in the provider copy to reconcile safely or adapt reconciliation with cryptographic/raw evidence without weakening duplicate prevention.

#### Validation strategy

Independent fixtures must verify signed mail, encrypted mail, sign+encrypt, multiple To/CC/BCC recipients, alternate From identity, inline CID/attachments, missing/untrusted recipient cert failure before SMTP, unknown send behavior, idempotent replay, and Apple-Mail-compatible decode/verify where available. Large-message memory and 20 MiB final composed-provider bound remain enforced on the final wrapped bytes.

#### What must not break

- No silent plaintext fallback when encryption was requested.
- BCC privacy.
- Unknown-send duplicate protection and Sent reconciliation.
- Ordinary non-S/MIME send/draft paths remain byte/behavior compatible except for shared refactors.

### Phase 35 — Consolidate, harden, document, and independently accept the expansion

#### Files to read before starting

- This plan in full, including every Amendment and all 134 acceptance criteria.
- `gg/progress.md` active parity-track entries in full and Git history/diffs for every expansion phase.
- Sweep Landmines and Coverage Statement.
- Current command tree, schemas/capabilities, migrations, package/release/docs contracts, and all changed domain/store/app/transport tests.
- Prior Round 1/2 regression tests for row iteration, SMTP cancellation, >10k threads, RFC-ID reuse, folder paging, bulk ordering, and precise remote dates.

#### What to do

Perform an independent terminal review of the expansion rather than trusting the phase ledger’s completion claims. Build a numbered 134-row acceptance matrix mapping each criterion to concrete code/deterministic/live/package evidence or a truthful unsupported capability result where the plan explicitly allows one (provider forwarded/colored/HME cases).

Close any missing defect-class tests, migration/backfill bounds, cursor pagination gaps, privacy leaks, race/cancellation hazards, query-plan/scale regressions, schema/reference drift, stale compatibility paths, or misleading docs/capability claims. Ensure canonical command aliases share one implementation and remove any temporary migration/shim path the earlier phases left behind.

Run the full authorized synthetic provider lifecycle that is safe and supported on the disposable harness: alternate sender where available, reply Answered state, semantic role/Junk round trip, bulk/thread mutation, remote cursor continuation, inline attachment, import/export/rewrite, unsubscribe against a controlled endpoint, local preferences/signatures, safe Outbox failure/retry, and S/MIME fixtures/provider delivery where credentials/entitlements permit. Clean up all live fixtures.

Consolidate reader docs/navigation/reference/package metadata. This phase is not permission to add excluded features.

#### Validation strategy

Final evidence must include the full ordinary test/check scope, authoritative race coverage with production semantics unchanged, schema migration from current schema 19 and intermediate expansion schemas, active fuzz/property tests at MIME/cursor/identity/import/S-MIME boundaries, representative large-state/query-plan tests, docs check/build, deterministic reference/schema regeneration, all six CGO-disabled native builds, npm pack/global-install smoke, dependency/license verification for new libraries, and privacy scans for live/private material.

The acceptance matrix is evidence to be independently reviewed later; it must not declare PASS where the provider compatibility phase truthfully concluded unsupported.

#### What must not break

- No excluded rules/categories/scheduler/reminder/event-log feature appears in production code or public docs.
- No second executable/store/transport architecture remains.
- No credentials, private live mail, databases, raw fixtures, S/MIME private keys, or blob artifacts are tracked or packaged.
- Every earlier Crucible defect-class regression remains green.

## Amendments

None yet.
