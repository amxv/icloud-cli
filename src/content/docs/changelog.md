---
title: Changelog
description: Release notes and unreleased user-visible changes for icloud.
order: 99
category: Reference
summary: Public command, output, installation, and documentation changes.
---

## Unreleased

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
