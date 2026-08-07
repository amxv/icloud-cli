# iCloud Mail CLI final acceptance matrix — 2026-08-07

Phase 25 closes the standalone iCloud Mail implementation against all 90 numbered acceptance criteria in `gg/icloud-mail-implementation-plan-2026-08-05.md`. Every gating item below is **PASS**. Evidence combines deterministic unit/integration/property/race/scale tests, generated-reference/package checks, and the authorized disposable two-provider live harness. Live credentials, recipient lists, message IDs, subjects/bodies, raw MIME, and local mailbox databases are intentionally omitted from this record.

## Terminal validation gates

- `make check` — PASS.
- `CGO_ENABLED=0 go test -count=1 ./...` — PASS.
- `go test -race -count=1 -timeout=30m -p 1 ./...` — PASS with no race reports; race-only test-harness timeout calibration leaves production and explicit timeout behavior unchanged.
- Active fuzz mutation runs — PASS for public IDs, RFC822/MIME parsing, remote references, bounded reply references, addresses, identity/dedupe inputs, thread components, and Unicode mailbox names.
- Every released local schema 1–17 upgrades to schema 18 with stable account IDs; corrupt SQLite is rejected without replacement — PASS.
- Large-mailbox query-plan, bounded repair, large MIME/body/attachment streaming, blob cancellation/corruption, remote-search bounds, contiguous UID paging, mandatory STARTTLS, and ambiguous SMTP-outcome regressions — PASS.
- `go mod verify` and `bun install --frozen-lockfile --ignore-scripts` — PASS; the shipped Go build closure has recognized permissive license files and the npm package has no runtime JavaScript dependency graph.
- `make references`, `make reference-check`, `make docs-check`, and `make docs-build` — PASS with zero Astro errors; existing non-gating deprecation hints and bundle-size advisory remain.
- Six CGO-free release binaries, release metadata/checksums, Linux native invocation, npm pack contents, and isolated global npm install smoke — PASS.
- Authorized live harness — PASS for IMAP/SMTP validation, folder discovery, IDLE-triggered receive, sync, compact/full/HTML/header/raw reads, Unicode attachment export, server-backed flags, send/idempotent replay, exact-parent reply, independent forward, draft create/sync/update/send/cleanup, copy/move fallback/idempotent replay, Sent reconciliation, exact-location permanent cleanup, and folder cleanup. A post-cleanup live `doctor --live` encountered repeated typed retryable provider timeouts after earlier IMAP+SMTP validation and the direct live compatibility test had passed; deterministic diagnostic-stage tests remain green. Repair reports no integrity/search/blob/UID/frontier problems and intentionally preserves one older harness `unknown` send because no exact Sent evidence exists.

## Numbered acceptance matrix

| # | Verdict | Evidence |
|---:|:---:|---|
| 1 | PASS | `TestGeneratedReferenceAssetsAreDeterministicAndComplete`, packaging tests, six native artifacts, npm install smoke, and native `version` all use `icloud`. |
| 2 | PASS | `TestRootHelpUsesICloudIdentityAndGroups` proves grouped account/mail/diagnostic/docs help and rejects template/old executable wording; stale-name sweep is clean. |
| 3 | PASS | Cobra tree/reference generation plus CLI usage tests cover local help/args/examples; package install smoke runs outside the repository and succeeds. |
| 4 | PASS | `TestCompletionGenerationForEveryShell` plus installed-package smoke validates Bash, zsh, fish, and PowerShell completion. |
| 5 | PASS | `TestManGenerationToStdoutAndDirectory`, generated-reference drift check, npm pack inspection, and installed-package smoke validate deterministic man pages. |
| 6 | PASS | `TestCapabilitiesPublishesVersionedAutomationAndSchemaContracts`, compact budget, cached-provider capability test, and JSON redaction coverage. |
| 7 | PASS | `TestAccountListCreatesAndReopensPrivateZeroState` and private-path mode tests validate safe first-run empty state/migrations. |
| 8 | PASS | Account lifecycle, ambiguity/default, multi-home isolation, validation, credential replace/delete, and removal tests exercise the complete multi-account flow. |
| 9 | PASS | Keyring/password-stdin tests verify non-argv secret entry and keyring storage/rollback behavior. |
| 10 | PASS | Env/file credential lifecycle and missing-secret tests prove references are persisted and resolved only for provider work. |
| 11 | PASS | Secret/schema/output/error tests, Git/content sweep, and package inspection find no plaintext credential persistence or publication. |
| 12 | PASS | Account validation tests distinguish IMAP and SMTP stages with typed redacted errors; live account validation proved both paths in this terminal pass. |
| 13 | PASS | Account lifecycle starts pending; connected health is written only after successful validation in account-test/health repository tests. |
| 14 | PASS | Validation failure/missing-credential tests preserve synchronized state while degrading health with typed auth/secret errors. |
| 15 | PASS | Folder transport/repository/app tests cover decoded names, role, delimiter, selectability/subscription, counts, and sync policy; live discovery passed. |
| 16 | PASS | Special-use precedence and role-only resolver regressions cover Inbox/Sent/Drafts/Archive/Trash/Junk without English-name inference. |
| 17 | PASS | Modified UTF-7/Unicode folder tests plus live Unicode folder/attachment history and terminal mailbox-name fuzzing cover round-trip selection/administration. |
| 18 | PASS | Folder policy workflow tests prove Inbox/Sent defaults and per-folder enable/disable behavior. |
| 19 | PASS | Folder-administration safety tests cover invalid names, protected roles, preview/authorization, rename recovery, and delete conflicts. |
| 20 | PASS | `TestMailSyncIncrementalWorkflowAndCompactBudgets` and Sent-tail bootstrap tests prove bounded recent Inbox bootstrap and zero-history Sent tail establishment. |
| 21 | PASS | `TestAdvanceIncrementalWindowCommitsOnlyFetchedContiguousPage` plus atomic mail-page commit test is the old skipped-UID regression: only fetched contiguous UIDs advance. |
| 22 | PASS | Sync fetch/parse/blob/transaction failure tests prove frontier non-advancement and idempotent retry. |
| 23 | PASS | UIDVALIDITY mismatch, rebaseline, targeted repair, and historical-frontier tests preserve cache while requiring explicit repair. |
| 24 | PASS | Sent identity merge/provider-location tests preserve one logical message with independent locations and strong-identity conflict safety. |
| 25 | PASS | Location flag reconciliation and live mark-read/flag/unflag/unread mutation prove server-backed seen/flagged/answered/draft/deleted handling. |
| 26 | PASS | `TestMailLocationFlagsReconcileAndDeriveSummariesAcrossEligibleLocations` and Sent merge tests are the old unread-reset regression. |
| 27 | PASS | Page bounds, large-mailbox query tests, bounded remote search, streaming MIME/blob tests, and repair bounds prevent whole-mailbox/materialized work. |
| 28 | PASS | Migration/account/write/watch/draft lock tests plus subprocess open tests return deterministic conflicts for competing operations. |
| 29 | PASS | Blob staging cancellation/cleanup, transaction rollback, interrupted draft replacement, backfill resume, and repair tests preserve valid retryable state. |
| 30 | PASS | Status/checkpoint/rebaseline/backfill/repair app/CLI tests prove bounded plans/results and cache preservation rather than cursor-driven deletion. |
| 31 | PASS | Phase-20 reverse-frontier tests prove folder/date/count/page-bounded resumable history independent from forward checkpoints. |
| 32 | PASS | Watch fake-server tests cover IDLE/fallback/reconnect/backoff/ordered compact/JSONL; live IDLE emitted a change and synchronized fresh fixtures. |
| 33 | PASS | Inbox/search compact budget/cursor tests and query-plan tests prove indexed keyset pagination with bounded one-record summaries. |
| 34 | PASS | Message compact/full/text/HTML/header/body/raw/export tests plus terminal live reads validate compact default and explicit complete representations. |
| 35 | PASS | MIME parser charset/MIME-word/filename tests and Unicode live attachment prove normalization while raw bytes remain exportable. |
| 36 | PASS | MIME content tests cover alternative/mixed/nested/inline/embedded `message/rfc822` structure and metadata. |
| 37 | PASS | Large raw/body/attachment tests and blob-store streaming/cancellation/hash tests keep bytes out of SQLite and bounded in memory. |
| 38 | PASS | Attachment list/save/save-all/stdout, filename traversal/collision, rollback, symlink/directory, and binary-intent tests; live Unicode save was byte-exact and mode 0600. |
| 39 | PASS | `TestParsePreviewMalformedDateNeverUsesWallClock` and normalized malformed-Date tests prove stable fallback identity/order. |
| 40 | PASS | Search repository/app tests cover text, participants, subject, RFC ID, folder, flags, dates, attachment metadata, account, limit, keyset cursors, and compact defaults. |
| 41 | PASS | Transactional search refresh/reparse/rethread/delete/body-source tests and repair tests prove FTS consistency. |
| 42 | PASS | Remote-search app/transport tests prove bounded unsynchronized results, opaque remote identity, optional hydration, and checkpoint isolation. |
| 43 | PASS | `TestThreadGraphMissingParentLaterRootAndLongReferences` is the old immediate-parent-fragmentation regression; reference-chain component reconstruction keeps A→B→C together. |
| 44 | PASS | Late-bridge/rethread tests keep stable message IDs/locations/attachments while deterministically merging components. |
| 45 | PASS | Thread graph/app/compact tests prove deterministic order, participants/unread/folders/attachments, and locally reconstructed identity. |
| 46 | PASS | Explicit-location flag mutation/reconciliation tests plus live state mutations validate mark-read/unread/flag/unflag. |
| 47 | PASS | `TestMailArchiveNeverTouchesSentAndAmbiguityRequiresFrom` is the old Sent-to-Inbox/archive regression; role-only destination/source rules prevent thread-wide mutation. |
| 48 | PASS | Location convenience tests prove unarchive requires Archive role and moves that exact location to Inbox without unexplained duplicates. |
| 49 | PASS | Location mutation store/app/transport tests expose source/destination, mapping, provider method, and reconciliation state; live copy/move confirmed COPYUID/fallback behavior. |
| 50 | PASS | Destructive location/folder CLI tests enforce `--yes`, exact message/location selection, dry-run, and ambiguity errors. |
| 51 | PASS | `TestMailSendDryRunAndNonTTYGuardCreateNoAttemptOrSMTPCall` and compact preview budget prove review-only dry-run with richer modes explicit. |
| 52 | PASS | Non-TTY guard and interactive cancel/confirm tests prove real sends cannot proceed implicitly. |
| 53 | PASS | Send normalization/composer/app tests cover display names, To/CC/BCC, Reply-To, safe custom headers, text/HTML, stdin/files, and attachments. |
| 54 | PASS | SMTP/composer/privacy tests prove BCC is envelope-only and absent from RFC headers/default output. |
| 55 | PASS | `TestSendSMTPRequiresSTARTTLSAndStreamsEnvelopeWithHiddenBcc` plus no-STARTTLS rejection is the plaintext-SMTP regression. |
| 56 | PASS | Send idempotency/conflict tests and live exact replay prove one normalized request/key cannot send twice. |
| 57 | PASS | SMTP outcome/app tests map acceptance→sent, rejection→failed, post-DATA uncertainty→unknown; `TestMailSendUnknownOutcomeBlocksAutomaticRetry` is the duplicate-unknown-send regression. |
| 58 | PASS | Outbound reconciliation repository/app tests require exact generated Message-ID evidence; terminal live Sent sync identity-merged and confirmed four fresh outbounds with zero ambiguity. |
| 59 | PASS | Reply plan/composer/app tests and live exact-parent round trip prove Reply-To preference, bounded References/In-Reply-To, subject normalization, and selected-parent targeting. |
| 60 | PASS | `TestBuildReplyAllExcludesSelfDeduplicatesAndHandlesOwnSentParent` and BCC regressions prove participant dedupe/self exclusion/no invented BCC. |
| 61 | PASS | Forward plan/composer/app tests and live forward prove a new thread, selected attachments, quoted/EML modes, and absence of reply headers. |
| 62 | PASS | Draft app/CLI/store tests cover create/list/show/update/sync/send/delete, exact Drafts selection fallback, interruption recovery; terminal live lifecycle completed. |
| 63 | PASS | Draft send tests and terminal live lifecycle prove one outbound attempt, provider draft cleanup, and post-send local draft removal. |
| 64 | PASS | Shared renderer/output tests and command-level budgets enforce undecorated bounded plain text by default; expansion modes never activate implicitly. |
| 65 | PASS | Account/folder/inbox/search/sync/status/doctor compact-budget tests enforce summary-only defaults and verbose opt-in detail. |
| 66 | PASS | Message/thread budget tests plus full/text/html/raw/header/body expansion tests enforce essential fields/excerpts by default. |
| 67 | PASS | Send/reply/move compact outcome ceilings and write-output privacy tests expose IDs/outcomes/next actions without submitted payloads. |
| 68 | PASS | Watch and bulk compact-output tests plus typed JSONL tests prove concise line defaults and documented JSONL expansion. |
| 69 | PASS | JSON envelope/error tests, schemas, message content bounds, and command matrix validate opt-in structured non-streaming output and redaction/size limits. |
| 70 | PASS | Golden/explicit budgets cover capabilities, account/folder, inbox/search/message/thread, sync/watch, send, reply, move, doctor, attachments, and bulk outputs. |
| 71 | PASS | Error normalization/exit-class tests, backend redaction tests, credential tests, and provider error mapping preserve stable code/retryability without secrets/bodies. |
| 72 | PASS | Non-TTY account/send/location/folder/draft guards fail immediately rather than prompting. |
| 73 | PASS | Account resolution/default/ambiguity tests prove explicit selector → default → sole-account behavior and typed no-account/ambiguity. |
| 74 | PASS | Parser/blob/watch/backfill/send/draft cancellation tests plus terminal-outcome persistence prove deadlines are honored without converting unknown into safe retry. |
| 75 | PASS | Doctor/account-test/provider/store/blob/checkpoint/lock/send error tests distinguish the named health stages with bounded default diagnostics; live timeout remained typed/retryable. |
| 76 | PASS | `TestNoColorDoesNotChangeCompactOutput`, redirected output tests, and JSON/JSONL parsing tests prove decoration-free machine output. |
| 77 | PASS | Deterministic protocol/storage/app tests cover paging gaps, injected failures, flags, special-use, IDLE, MOVE fallback, UIDVALIDITY, MIME, archive flows, scale, races, and ambiguous SMTP. |
| 78 | PASS | Active fuzz/property targets cover malformed RFC822/MIME, mailbox names, remote/reply references, addresses, public IDs, content/provider identity, and thread/dedupe invariants. |
| 79 | PASS | Phase ledger plus this terminal disposable two-provider pass records live evidence for safe provider-facing behavior; failure/ambiguity/race/scale remain deterministic-only where induction would be unsafe or nondeterministic. |
| 80 | PASS | `TestMailSearchQueryPlansAndLargeMailboxStayBounded` meets its production 5-second budget; large MIME/body/attachment/blob tests prove streaming bounds. |
| 81 | PASS | `CGO_ENABLED=0` full suite and `make build-all` produce six macOS/Linux/Windows amd64/arm64 assets; native Linux and npm-installed wrapper invoke `icloud`. |
| 82 | PASS | Release metadata/checksum verification covers all six artifacts; npm pack/install smoke verifies man/completion/schema assets. |
| 83 | PASS | Every prior visible slice has a task-focused guide in the progress ledger; Phase 25 improves existing README/docs index scope wording and changelog instead of adding a phase-shaped page. |
| 84 | PASS | Docs-contract tests require discoverable task-focused guides; current guides center setup/workflows/recovery/next actions rather than repository knowledge. |
| 85 | PASS | Docs-contract tests and narrative sweep reject implementation-diary/package/schema/ancestry prose; exhaustive syntax is generated. |
| 86 | PASS | Current branch behavior is validated, examples are sanitized, docs check/build pass, and the Phase 25 progress entry records the reader-facing improvement/evidence. |
| 87 | PASS | Docs-contract coverage plus built site includes installation/credential safety, output modes, command reference, agent automation/JSON schemas, sync, troubleshooting, privacy/local state, and changelog. |
| 88 | PASS | Production/help/docs vocabulary sweep finds no AMA/AMAOS/cases/immigration/document-request/Gmail coupling. |
| 89 | PASS | Architecture/entrypoint sweep confirms one `cmd/icloud`, one SQLite store, one blob store, one iCloud transport path, no JSON whole-state store/template `hello`/dual executable. |
| 90 | PASS | README and docs landing page explicitly state iCloud Mail-only scope and exclude Contacts, Calendar, Drive, Notes, Photos, Find My, and hosted mail. |

## Named historical landmine verdicts

The seven terminal regressions explicitly required by Phase 25 are all covered by failing-against-old/passing-now tests: skipped incremental UID paging (`TestAdvanceIncrementalWindowCommitsOnlyFetchedContiguousPage`), unread reset (`TestMailLocationFlagsReconcileAndDeriveSummariesAcrossEligibleLocations`), immediate-parent thread fragmentation (`TestThreadGraphMissingParentLaterRootAndLongReferences`), Sent-inclusive archive/unarchive mutation (`TestMailArchiveNeverTouchesSentAndAmbiguityRequiresFrom`), plaintext/conditional TLS (`TestSendSMTPRequiresSTARTTLSAndStreamsEnvelopeWithHiddenBcc` plus no-STARTTLS rejection), whole-mailbox remote search (`TestRemoteSearchCandidateScanIsBoundedAndVisible` plus the large-mailbox query-plan test), and automatic retry after ambiguous SMTP acceptance (`TestMailSendUnknownOutcomeBlocksAutomaticRetry` plus post-DATA transport classification).
