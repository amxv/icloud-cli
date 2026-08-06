---
title: Changelog
description: Release notes and unreleased user-visible changes for icloud.
order: 99
category: Reference
summary: Public command, output, installation, and documentation changes.
---

## Unreleased

- Added exact provider-location `move`, `copy`, `archive`, `unarchive`, `trash`, `restore`, and permanent `delete` commands with local dry-run resolution, non-interactive `--yes`, and stable idempotency keys.
- Added durable `mut_…` intents and bounded `mail status --mutations` output for pending, confirmed, failed, and unresolved provider outcomes.
- Added atomic MOVE preference, safe COPY plus exact UID EXPUNGE fallback, provider UID mapping capture, refusal of mailbox-wide EXPUNGE, and cancellation-safe IMAP cleanup.
- Preserved stable logical message, content, attachment, search, and thread identity across location changes; deleting one provider copy no longer risks purging another location.
- Added the [Move, copy, archive, and delete mail](/docs/manage-mail-locations) workflow plus Quickstart, automation, troubleshooting, reference, capability, schema, and landing-page updates.
- Added `icloud mail forward <msg_…>` as an exact-source, new-conversation workflow with normalized `Fwd:` subjects and no reply relationship headers.
- Added explicit `--original quoted|eml|none` source representation, verified attached EML, selective `--include-attachment att_…` original attachments, and ordinary new filesystem attachments through the existing streamed send path.
- Added schema version 14 forward-source provenance on outbound attempts, payload-free forward outcomes/status, idempotent replay, independent local thread creation, and source-message/thread immutability proofs.
- Expanded the send, Quickstart, output/automation, troubleshooting, command-reference, README, capabilities, landing, completion, man, migration, privacy, and compact-output journeys for forwarding.
- Added repeated filesystem `--attach` inputs with optional filename/media-type overrides, pre-confirmation SHA-256 review, changed-file detection, durable local attachment bytes, streamed multipart composition, and a 20 MiB composed-message guard before SMTP.
- Added schema version 12 outbound attachment records, linked failed/unknown retries, bounded payload-free `mail status --outbound`, focused `mail sync --reconcile-outbound`, exact generated Message-ID confirmation, and ambiguity-preserving unknown outcomes.
- Added streamed provider Sent-copy creation after SMTP acceptance, complete local attachment access before provider reconciliation, and live small/9 MiB two-way attachment round-trip evidence with one logical Inbox/Sent message.
- Expanded the send, sync, output/automation, troubleshooting, command-reference, README, help, migration, privacy, and compact-output journeys for attachments, Sent reconciliation, and explicit duplicate-risk recovery.
- Added `icloud mail send` for deliberate text/HTML messages with account display-name `From`, display-name recipients, Reply-To, safe custom headers, generated Message-ID, envelope-only BCC, and mandatory STARTTLS submission.
- Added no-network/no-attempt dry runs, interactive confirmation, non-TTY `--yes`, bounded body/file/stdin input, explicit text/HTML/raw/header preview expansions, and payload-free compact/JSON success output.
- Added schema version 11 pending/sent/failed/unknown outbound attempts, normalized-request idempotency, pre-SMTP durability, atomic accepted-message/search/thread creation, local raw-message authority, and non-retryable post-DATA ambiguity.
- Added the Send mail safely guide and updated Quickstart, output/automation, troubleshooting, command reference, README, capabilities, landing, completion, man, migration, privacy, and compact-output contracts.
- Added explicit bounded iCloud search with stale-safe `rem_…` provider references, one-folder default, opt-in all-folder fan-out, current envelope/flag summaries, decoded bounded previews, visible `more`/`partial` bounds, and honest unsupported-filter errors.
- Added targeted remote hydration through `mail sync --hydrate` and `mail message show --remote --cache`, reusing the canonical parser/blob/identity/search/attachment/thread path without advancing ordinary synchronization checkpoints.
- Added `mail_remote_search` and `mail_hydration` capability flags and expanded the search, sync, output, troubleshooting, quickstart, README, and reference journeys.
- Added schema version 10 indexed local mail search across subject, sender, To/CC participants, decoded text, attachment metadata, and RFC Message-ID, with BCC excluded.
- Added `icloud mail search`, filterable `mail inbox` and `mail latest`, message or thread results, bounded relevance/newest paging, and cursors bound to the complete account and query.
- Added transactional search updates during sync, content reparse, rethreading, and deletion, plus bounded `mail search repair` for incomplete legacy or interrupted local body indexing.
- Added the Search synchronized mail guide and updated Quickstart, sync, conversations, output, troubleshooting, command reference, README, capabilities, and landing journeys.
- Added schema version 9 local RFC conversation reconstruction with stable `thr_…` IDs, immutable message membership, durable redirect aliases, ordered edge evidence, bounded aggregates, and one-time schema-8 backfill.
- Added offline `icloud mail latest` and `icloud mail thread show <thread-id|message-id>` with compact chronological previews, explicit full text/HTML/headers/raw JSON, metadata-only graph evidence, and multi-message raw-stream protection.
- Added transactional late-root and bridge merges, conservative subject-and-participant fallback only without RFC evidence, visible unresolved/malformed/self/cycle states, and aggregate refresh after sync or location-state changes.
- Added the Reconstructed conversations guide and updated Quickstart, output, troubleshooting, command reference, README, capabilities, and landing journeys for local RFC threads and aliases.
- Added schema version 8 identity provenance and dedupe conflict records for logical messages observed in several provider locations.
- Added bounded synchronization for every enabled folder, `--folder sent` and stable-folder selectors across sync, status, and local listing, plus one compact or JSON result per selected folder.
- Added history-free Sent setup: the first run records the current Sent tail without downloading old messages, while later runs ingest only newer Sent copies.
- Added strong RFC/generated-ID reconciliation, guarded location-less content matching, independent per-location UIDs and flags, outbound direction from confirmed folder/account evidence, and conflict-preserving behavior for ambiguous provider observations.
- Updated the sync, quickstart, folder-policy, output, troubleshooting, command-reference, landing-page, and README journeys for enabled folders, multi-location messages, and safe Sent expectations.
- Added structured per-location Seen, Flagged, Answered, Draft, Deleted, unknown-flag, mod-sequence, presence, and last-checked state through schema version 7.
- Added bounded rotating Inbox flag reconciliation so repeated syncs detect remote state changes and missing locations without re-downloading message bodies or starving older local messages.
- Added location-aware `mail mark-read`, `mark-unread`, `flag`, and `unflag` commands with explicit folder selection for ambiguous messages, server-returned state persistence, conditional-race detection, targeted verification, and cancellation-safe IMAP STORE timeouts.
- Corrected unread and flagged summaries to derive from every eligible current location rather than mutable counters or the last location read, while excluding deleted or absent copies and preserving unknown provider flags.
- Added first-class inbound attachment records with stable IDs, MIME-part links, decoded sizes and hashes, media parameters, inline/content-ID metadata, and explicit availability state through schema version 6.
- Added `icloud mail attachments list`, verified single-file save, binary stdout opt-in, and atomic `save-all` with safe Unicode filenames, deterministic collision suffixes, no-clobber defaults, and rollback on batch failure.
- Preserved exact raw RFC822 authority and stable message, location, MIME-part, and attachment identities while existing schema-5 messages hydrate decoded attachments once from trusted local content.
- Added compact attachment-output budgets, content-integrity and missing-blob failures, zero/large/inline/unnamed/duplicate/Unicode/nested-message coverage, migration safety, and the Save mail attachments guide.
- Added `icloud mail message show/raw/export` for bounded message metadata, complete decoded text, HTML, decoded headers, metadata-only and MIME-part views, exact raw RFC822 streaming, and private atomic export.
- Added charset-aware nested MIME normalization, ordered headers and parts, inline and embedded-message metadata, partial parse diagnostics, stable UUIDv7 MIME-part identities, malformed-Date stability, and schema-5 upgrades from existing synchronized mail.
- Kept message JSON preview-only by default; complete text/HTML/raw requires an explicit content mode, raw JSON is bounded to 8 MiB, and BCC/raw storage coordinates remain excluded from ordinary output.
- Changed compact text to readable vertical blocks: result type first, stable identity plus the first field next when present, one remaining field per line, and a blank line between blocks.
- Added bounded `icloud mail sync` for current-tail Inbox bootstrap and incremental UID paging, with streamed raw-message staging and atomic page/checkpoint commits.
- Added local bounded `icloud mail inbox`, compact decoded-text previews, explicit JSON metadata, and stable message/location/checkpoint identities without exposing BCC or raw storage paths.
- Added `mail sync status` and `mail sync checkpoints`, visible failed/repair-required states, UIDVALIDITY mismatch protection, empty-gap scanning, and retry-safe frontier handling.
- Added deterministic fetch/parse/blob/transaction failure coverage, malformed-Date stability, content-addressed blob limits, compact-output budgets, and live iCloud bootstrap plus exactly-once AgentMail incremental evidence.
- Added the Sync and read the Inbox guide and simplified nearby folder, quickstart, automation, troubleshooting, command-reference, landing-page, and README journeys.
- Added `icloud mail folders status` to discover every remote mailbox and read provider counts without fetching messages or mutating mail.
- Added durable schema-version-3 folder records containing decoded and modified-UTF-7 wire names, delimiter, attributes, special-use classification, selectable/subscription knowledge, provider counts, UID metadata, and discovery/status timestamps.
- Added local bounded `mail folders list/show`, explicit `enable/disable` future-sync policy, Inbox/Sent defaults, policy preservation across rediscovery, and retained missing/stale folders.
- Added nested, Unicode, non-selectable, localized-special-use, Sent-fallback, OLDNAME rename, stale/reappearance, timeout, redaction, and live iCloud folder evidence.
- Added the folder discovery and sync policy guide and updated quickstart, state, automation, troubleshooting, command reference, landing page, README, capabilities, completion, and man contracts.
- Added read-only `icloud account test` for one or all accounts with direct-TLS IMAP, mandatory SMTP STARTTLS, separate authentication results, bounded capability summaries, and no send or mailbox mutation commands.
- Added full-email/short-name IMAP authentication fallback and persistence of the exact successful username.
- Added durable connected/degraded health, last-tested/last-healthy timestamps, redacted failure stages, and sanitized IMAP/SMTP capabilities through schema version 2.
- Added local and opt-in live `icloud doctor` diagnostics, stage-specific typed exits, per-stage deadlines, prompt cancellation, deterministic fake TLS protocol servers, and a disposable-account live harness that skips unless explicitly enabled.
- Added the account validation and doctor guide and updated the current setup, state, output, troubleshooting, command-reference, landing-page, and README contracts.
- Added offline multi-account setup with stable IDs, case-insensitive name/email uniqueness, first-account defaulting, deterministic selection, rename/default/remove commands, and `pending_validation` status.
- Added native keyring credential storage and rotation plus first-class environment and file references; no plaintext credential is stored in SQLite or returned in compact/JSON output.
- Added secure terminal and `--password-stdin` input without a public `--password` flag, explicit non-TTY behavior, bounded secret input, and redacted keyring/reference failures.
- Added credential metadata, replace, and delete commands; destructive account/credential operations require `--yes`, and default-account removal is protected while alternatives remain.
- Added cross-process account mutation locking and keyring rollback/cleanup around failed database transactions.
- Added the account setup and credentials guide and updated quickstart, installation, local-state, automation, troubleshooting, command reference, landing page, and README for the secure account workflow.
- Added durable local application paths with `--home`, `ICLOUD_HOME`, and platform-default resolution.
- Added private first-run state creation, automatic versioned upgrades, cross-process locking, and a reported data-schema version.
- Added `icloud account list` and `icloud account show` with compact defaults, explicit JSON envelopes, typed not-found/conflict exits, and credential-reference redaction.
- Added stable prefixed account identifiers and the initial account/credential-reference state contract without storing plaintext credentials.
- Added the local-state and account-inspection guide plus updated installation, quickstart, automation, troubleshooting, command reference, and homepage content.
- Replaced the placeholder command with the public `icloud` executable across Go builds, npm installation, project metadata, and release automation.
- Added grouped Cobra help, compact `version` and `capabilities` commands, explicit JSON envelopes, request IDs, stable error classes, and deterministic stdout/stderr behavior.
- Added Bash, Zsh, Fish, and PowerShell completion generation.
- Added root and full-tree man-page generation from the live command tree.
- Added bounded compact-output golden tests and packaging checks for all supported release targets.
- Replaced the starter documentation with installation, quickstart, automation, troubleshooting, and current command-reference guides.

## 0.1.0 — 2026-08-05

- Published the initial repository scaffold, npm wrapper, release workflow, and documentation site before iCloud-specific command implementation began.
