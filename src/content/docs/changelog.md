---
title: "Changelog"
description: "User-visible release notes for icloud."
order: 99
category: Reference
summary: "What changed for people and agents using the CLI."
---

## 0.2.0 — 2026-08-11

- Expanded iCloud Mail into a comprehensive command family covering account setup and health checks, local state, folder discovery, sync and history, local and remote search, conversations, attachments, drafts, send/reply/forward, sender identities and signatures, folder management, message organization, imports/exports, mailing-list unsubscribe, S/MIME, and recovery tools.
- Added reviewed bulk selections and paged permanent-deletion workflows so agents and scripts can freeze the exact mail they intend to change before applying an action.
- Added local VIP, blocked-sender, and conversation-mute preferences for lightweight triage.
- Added a safe-retry Outbox for the send failures the CLI can prove are safe to replay, plus reconciliation tools for uncertain outbound results.
- Added compact text by default, explicit JSON/JSONL, strict JSON request schemas, request IDs, stable exit classes, shell completion, and generated man pages for automation.

## 0.1.0 — 2026-08-05

- Published the initial `icloud` package, native CLI wrapper, release workflow, and documentation site.
