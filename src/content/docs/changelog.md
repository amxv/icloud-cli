---
title: Changelog
description: Release notes and unreleased user-visible changes for icloud.
order: 99
category: Reference
summary: Public Mail workflows, output contracts, installation, and documentation changes.
---

## Unreleased

- Completed the standalone iCloud Mail workflow: multi-account credentials and validation; folder discovery/administration; bounded forward sync, historical backfill, foreground IDLE watch, rebaseline and repair; local and bounded remote search; complete message/thread/attachment reads; drafts; send/reply/reply-all/forward; and exact-location read/flag/move/copy/archive/trash/restore/delete operations.
- Added local-only streaming mboxrd mailbox export over verified canonical provider copies, with bounded deterministic paging, reversible `From ` quoting, native stdout streaming, and atomic private file output that fails closed on missing or corrupt raw mail.
- Added bounded resumable mboxrd mailbox import with an immutable source fingerprint, exact byte/message frontier, per-record recovery evidence, single-message APPEND reconciliation reuse, source-change refusal, and explicit fail-fast or continue-on-error partial-work reporting.
- Added standards-based mailing-list unsubscribe with token-redacted message capability, strict RFC 8058 DKIM-covered one-click HTTPS POST, explicit multi-alternative selection, and durable idempotent mailto fallback through the normal send journal.
- Kept compact plain text as the universal default. Complete decoded text, HTML, headers, body-only streams, raw MIME, verbose diagnostics, JSON envelopes, and JSONL streams remain explicit expansions with bounded defaults and privacy-aware output.
- Added strict `--input-json <path|->` requests for the published automation write contract and bounded JSONL bulk read/flag updates with explicit fail-fast or continue-on-error behavior. `capabilities --json` reports the automation contract, write-command schemas, bulk bounds, and cached provider capabilities when available locally.
- Added versioned generated JSON Schemas for public resources, errors/events, output envelopes, and every JSON write request. Schemas ship with the npm package and are served from the docs site.
- Added local-only account and folder completion. Completion suggestions never authenticate, resolve a credential, or contact iCloud.
- Added deterministic generated command-reference, man-page, and Bash/Zsh/Fish/PowerShell completion assets derived from the same Cobra tree as the executable. Docs and repository checks fail when generated references drift.
- Hardened npm installation so a downloaded native release binary is verified against the tagged release's SHA-256 checksums before installation. Releases also publish machine-readable metadata for all six supported OS/architecture assets; source fallback remains available when Go is installed.
- Hardened deadline and retry behavior so a send or draft-send that reaches a provider outcome remains durably idempotent even if the caller's deadline expires, while indeterminate sends still require explicit reconciliation instead of automatic retry.
- Consolidated the docs around task-focused journeys for installation/update, app-specific-password safety, account health, folders, synchronization/backfill/watch/repair, search, conversations, attachments, drafts/outbound mail, location changes, local-state backup/removal, automation, and troubleshooting. Exhaustive syntax now belongs to the generated command reference instead of being maintained by hand.

## 0.1.0 — 2026-08-05

- Published the initial repository scaffold, npm wrapper, release workflow, and documentation site before the standalone Mail implementation was completed.
