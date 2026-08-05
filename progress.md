# iCloud Mail Implementation Progress

> **Authoritative plan:** [`gg/icloud-mail-implementation-plan-2026-08-05.md`](./gg/icloud-mail-implementation-plan-2026-08-05.md)
> **Current-state research:** [`gg/icloud-mail-sweep-2026-08-05.md`](./gg/icloud-mail-sweep-2026-08-05.md)
> **Reusable agent prompts:** [`gg/icloud-mail-agent-prompts.md`](./gg/icloud-mail-agent-prompts.md)

## Purpose

This is the durable handoff ledger for the multi-agent implementation track. It does not replace the plan or Git history. It records which plan phase is actually complete, what evidence exists, what the branch contains now, and what the next agent must know before continuing.

The source reference is `amxv/gg-ama` at planning commit `d5c15aff9b7cab98ef6e4ad9b360eab96b20420b`. Treat that repository as read-only context and never run its Rust builds or mutate it.

The destination research baseline was `amxv/icloud-cli` `main` at `2bf55d1a4f68c8d49391fa32bd59f2c1447120c8`. Before the planning artifacts were published, existing commits `bcc0ac4bc5d2d791f8882c3431479f430aaae7cb` and `69e77c711a96d6ba8cd97d882aa010f0b2752687` advanced the canonical docs URL and Vercel docs-only build filter; those changes were preserved. Implementation agents work directly on the current `main`, never on a replacement branch.

## Status values

- `pending` — no accepted implementation work has started.
- `in_progress` — a phase has uncommitted or committed work but lacks its complete validation evidence.
- `blocked` — the phase cannot satisfy the plan without a user decision or a documented plan amendment.
- `complete` — the phase's observable outcome, positive evidence, regressions, help/docs, progress update, commit, and push are complete.

A phase is not complete because files exist or tests from before the phase still pass. Record the phase-specific behavior whose result changed.

## Phase ledger

| Phase | Capability | Status | Completion commit | Evidence / next boundary |
| ---: | --- | --- | --- | --- |
| 1 | Public `icloud` command and CLI contract | pending | — | Start here. |
| 2 | Durable local state and zero-account system | pending | — | — |
| 3 | Secure multi-account profiles and credentials | pending | — | — |
| 4 | Live iCloud transport proof and account health | pending | — | Compatibility gate for later network phases. |
| 5 | Folder discovery, status, and sync policy | pending | — | — |
| 6 | Correct bounded recent Inbox sync and local inbox | pending | — | Must close the skipped-UID class. |
| 7 | Complete bodies, headers, and raw MIME | pending | — | — |
| 8 | Inbound attachment indexing and export | pending | — | — |
| 9 | Flags, unread state, and read/flag mutations | pending | — | — |
| 10 | Sent sync and multi-location identity | pending | — | — |
| 11 | Stable RFC reference-graph threads | pending | — | — |
| 12 | Indexed local search | pending | — | — |
| 13 | Explicit remote search and hydration | pending | — | — |
| 14 | Safe idempotent text/HTML send | pending | — | — |
| 15 | Outbound attachments and Sent/unknown reconciliation | pending | — | — |
| 16 | Reply and reply-all | pending | — | — |
| 17 | Forward | pending | — | — |
| 18 | Location-aware move/copy/archive/trash/delete | pending | — | — |
| 19 | Mailbox administration and arbitrary-folder sync | pending | — | — |
| 20 | Backfill, rebaseline, and repair | pending | — | — |
| 21 | IDLE watch and reconnect | pending | — | — |
| 22 | iCloud-backed drafts | pending | — | — |
| 23 | Agent JSON input, schemas, and bulk contract | pending | — | — |
| 24 | Docs, man/completion assets, and release packaging | pending | — | — |
| 25 | Exhaustive hardening and final cutover | pending | — | Final acceptance matrix required. |

## Current handoff

- **Last completed phase:** none; planning only.
- **Current implementation state:** unchanged Go CLI template plus planning artifacts.
- **Next phase:** Phase 1 — Cut over the public command and establish the CLI contract.
- **Known gating assumptions:** no existing users depend on the template `icloud-cli hello` command; no standalone mail state exists; Phase 4 must prove the selected IMAP client against a disposable iCloud account before later network phases proceed.
- **Open blockers:** none.

## Required update format

Append one entry after every completed or blocked implementation session. Update the phase table and Current handoff at the same time.

### YYYY-MM-DD — Phase N — `<status>`

- **Agent/session:** identify the agent or execution context.
- **Starting commit:** full SHA actually inspected before work.
- **Ending commit(s):** full SHA(s) pushed for this phase or slice.
- **Outcome:** what became observably true; do not summarize only files edited.
- **Files/areas changed:** concise paths and major symbols/migrations.
- **Positive evidence:** tests or exercised behavior that failed/was absent before and now passes.
- **Regression evidence:** existing behavior/check scope demonstrated after the change.
- **Decisions made:** local implementation judgments that do not amend the plan.
- **Amendments:** link the plan amendment entry when an assumption/approach changed; write `none` otherwise.
- **Known defects/risks:** exact remaining problems, including partial work that prevents phase completion.
- **Next handoff:** earliest pending phase or exact unfinished boundary, with the files/symbols the next agent should inspect first.

## Ledger rules

1. Inspect the branch and tests before trusting this file; correct stale progress immediately and explain the correction.
2. One phase may use several commits, but its status stays `in_progress` until all phase validation and documentation are complete.
3. Do not mark a later phase complete while an earlier dependency is incomplete unless the plan explicitly says the phases are independent; record any intentional reordering as an Amendment.
4. Never paste passwords, app-specific-password values, private email bodies, raw MIME, recipient lists from live mail, or private filesystem paths into this file.
5. Keep temporary databases, blobs, protocol captures, generated live-mail fixtures, and credentials outside Git.
6. When a plan assumption proves false, amend the plan immediately; do not hide the divergence in this ledger alone.
7. Push this update with the code it describes. A local-only progress update is not a handoff.

## Progress entries

No implementation entries yet.
