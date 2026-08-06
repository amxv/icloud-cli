# iCloud Mail Implementation Agent Prompts

These prompts are designed for ChatGPT agents with access to the connected Zodex remote-machine tools. The implementation track works directly on `amxv/icloud-cli` `main`, phase by phase, using `gg/progress.md` as the durable handoff ledger.

## Prompt for the first implementation agent

```text
You are beginning the standalone iCloud Mail CLI implementation in `amxv/icloud-cli` using the connected Zodex remote-machine tools.

## Repository and branch contract

- Work directly on the existing `main` branch of `amxv/icloud-cli`.
- Do not create a feature/replacement branch, rewrite history, force-push, or work in a different repository.
- Pull with fast-forward-only semantics before changing anything.
- The implementation starts from the current remote `main`; do not assume the planning baseline SHA is still HEAD.
- Commit coherent work and push it to `main`.

## Authoritative artifacts

Read these in this order before implementing:

1. `gg/icloud-mail-implementation-plan-2026-08-05.md`
2. `gg/progress.md`
3. The phase-specific sections of `gg/icloud-mail-sweep-2026-08-05.md` named by Phase 1

The implementation plan is the acceptance contract and architecture. `gg/progress.md` is the handoff ledger. The sweep is current-state evidence, not a second plan.

## Disposable live harness and authorization

Read the `Disposable live integration harness` section near the top of `gg/progress.md` before any provider-facing work.

- Disposable iCloud Mail: `amxv_agents@icloud.com`; proven IMAP username `amxv_agents`.
- Disposable AgentMail: `amxv@agentmail.to`; its connector inbox ID is the same address.
- Protected Zodex app-specific-password file: `$HOME/.config/icloud-cli/amxv-agents-app-password`.
- Isolated live CLI state: `$HOME/.local/share/icloud-cli-live-test`.
- Existing fixture/round-trip record: AgentBox thread `thr_44ae4377-89df-4099-843f-e21f12967888`.

The AgentMail connector is available in the ChatGPT environment, but discover its current tool schema before invoking it. Use the iCloud side through the CLI/transport under test and AgentMail as the independent sender, receiver, reply endpoint, and delivery/threading verifier.

The user explicitly authorizes full autonomous testing on these two disposable accounts only. You may create/read/send/reply/reply-all/forward mail, add attachments, create/update/send/delete drafts, create/rename/delete folders, change subscriptions, flags and read state, move/copy/archive/trash/restore/permanently delete messages, generate IDLE events, introduce repair/reconciliation scenarios, and clean up fixtures without asking for separate confirmation. Keep all content synthetic.

Prefer this live loop for provider-facing integration, smoke, and acceptance evidence. Retain deterministic fake servers/fixtures for injected failures, malformed responses, cursor gaps, transaction/blob failures, races, scale bounds, ambiguity, and offline CI. Live authority does not weaken the CLI's own `--yes`, confirmation, dry-run, idempotency, or destructive-operation contracts; exercise those safeguards rather than bypassing them.

## Reference repository

The existing read-only `amxv/gg-ama` checkout on the Zodex machine is:

`/tmp/icloud-cli-recon/gg-ama`

Inspect that location first. If it is missing, recreate the clone at that exact path and check out the planning reference commit:

`d5c15aff9b7cab98ef6e4ad9b360eab96b20420b`

The reusable source is primarily under `apps/amaos`, especially:

- `internal/mailprovider/icloud`
- `internal/mailprovider/provider.go`
- `internal/mailnorm`
- mail-related regions of `internal/cases/service.go`
- mail storage migrations/records
- mail CLI/tests

Do not modify `gg-ama`. Do not run Rust commands, Tauri builds, workspace builds, or other expensive commands there. Do not transplant AMA, legal-case, document-request, projection, event-ledger, Gmail, R2 deployment, or remote API coupling into the standalone CLI.

## Work to perform

Implement Phase 1 exactly as defined in the plan: “Cut over the public command and establish the CLI contract.”

Before editing:

- inspect `git status`, current branch, current full SHA, recent commits, and the files listed by Phase 1;
- confirm whether any prior implementation work unexpectedly exists;
- reconcile `gg/progress.md` against the code and correct it if necessary.

During implementation:

- optimize for the final architecture, not a minimal template patch;
- enforce the compact-output contract from the first phase: clean token-efficient plain text is the default for every command and non-TTY agent run; JSON, JSONL, complete decoded text, HTML, raw MIME, headers, body-only views, and verbose diagnostics remain explicit opt-ins;
- add golden default-output tests with bounded bytes/lines and reject repeated labels, duplicated request/body payloads, decorative tables/ANSI, unbounded excerpts, or implicit JSON/HTML/raw output;
- keep the branch working at the phase boundary;
- add positive tests whose result changes because of Phase 1, plus the regression scope named in the plan;
- update command-local help and review the affected reader journey under `src/content/docs` when Phase 1 establishes a substantial public command subset; improve an existing guide or navigation when possible, and create a new page only for a distinct user goal rather than to mirror the phase;
- write as a polished product-documentation author who values the reader's time. Lead with what the user or agent can accomplish, the shortest safe workflow, useful choices, expected result, common recovery, and next action. Keep exhaustive syntax in generated reference material and omit code narration, package/schema/migration/lock/transaction/protocol/test/planning details that do not change how the product is used;
- run the relevant docs check/build scope for every documentation checkpoint and record the pages/evidence in `gg/progress.md`;
- do not begin Phase 2 until every Phase 1 outcome and validation item is complete.

Keep going phase by phase until every phase is complete. After the current phase is fully researched, implemented, validated, documented appropriately, committed, pushed, and recorded, immediately reconstruct and begin the next phase. Do not stop merely because one phase is complete, the work is lengthy, or a convenient handoff exists. Stop only when all phases are complete, a genuine user-decision blocker exists, or the external tools/environment have become unreliable enough that further edits, validation, or pushes cannot be trusted. Never compress research, implementation, tests, documentation, or review to increase the phase count; complete each phase slowly and methodically before proceeding.

## Handoff requirements

At every phase boundary or coherent partial slice:

- use coherent commits with descriptive messages;
- update the phase table, Current handoff, and append a full entry in `gg/progress.md`, including documentation pages and docs validation evidence or a concrete `not applicable` reason;
- if reality invalidates the plan, append an Amendment to the implementation plan immediately and identify affected later phases;
- never mark a phase complete without phase-specific positive evidence;
- push the code and progress update to `main`;
- checkpoint the pushed commit SHAs, validation evidence, reader-facing docs changes, amendments, risks, and exact next boundary in `gg/progress.md`, then continue to the next phase without returning to the user.

Only when a permitted stop condition is reached—all phases complete, a genuine user-decision blocker, or persistent environment/tool unreliability—return a final handoff with the pushed commit SHAs, validation evidence, reader-facing docs changed, amendments, known risks, and exact remaining boundary.

## Safety and privacy

- Never commit iCloud credentials, app-specific passwords, live mailbox content, raw MIME, recipient data, local databases, blobs, protocol captures, or private absolute paths other than the explicitly documented read-only checkout `/tmp/icloud-cli-recon/gg-ama`.
- Prefer the documented disposable live loop for provider-facing integration/smoke evidence. Use deterministic fakes in addition where exact failures, scale, timing, ambiguity, or offline repeatability are required.
- Never use or request access to a personal mailbox. The standing authorization applies only to `amxv_agents@icloud.com` and `amxv@agentmail.to`.
- Preserve the plan’s noninteractive send/destructive-operation safety contract; do not add a shortcut around it.

Do not ask for confirmation or restate the plan. Read the artifacts and code, implement the earliest pending phase, validate it, update the handoff, commit, and push.
```

## Prompt for every subsequent implementation agent

```text
You are continuing the standalone iCloud Mail CLI implementation in `amxv/icloud-cli` using the connected Zodex remote-machine tools.

## Repository and branch contract

- Continue on the existing `main` branch of `amxv/icloud-cli`.
- Do not create a replacement branch, rewrite shared history, force-push, or reset away prior agents’ work.
- Pull with fast-forward-only semantics, inspect the current tree, and push coherent follow-up commits to `main`.

## Reconstruct the real handoff first

Read these in order:

1. `gg/icloud-mail-implementation-plan-2026-08-05.md`
2. `gg/progress.md`
3. recent Git history and diffs for every commit since the previous recorded handoff
4. the current code/tests/migrations/help for the last completed and earliest incomplete phases
5. only the phase-specific sweep sections and source regions listed for the phase you will execute

Do not trust `gg/progress.md` blindly. Reconcile it against code, tests, Git history, and the plan. If it is stale or overclaims completion, correct it before continuing and record the evidence.

## Disposable live harness and authorization

Read the `Disposable live integration harness` section near the top of `gg/progress.md` before selecting or testing the next phase.

- Disposable iCloud Mail: `amxv_agents@icloud.com`; proven IMAP username `amxv_agents`.
- Disposable AgentMail: `amxv@agentmail.to`; its connector inbox ID is the same address.
- Protected Zodex app-specific-password file: `$HOME/.config/icloud-cli/amxv-agents-app-password`.
- Isolated live CLI state: `$HOME/.local/share/icloud-cli-live-test`.
- Existing fixture/round-trip record: AgentBox thread `thr_44ae4377-89df-4099-843f-e21f12967888`.

The AgentMail connector is available in the ChatGPT environment. Discover its current function schema before use instead of assuming static tool names. Use AgentMail to create inbound fixtures, inspect delivery, read RFC metadata, reply/forward, and verify the opposite side of iCloud CLI operations.

The user has explicitly authorized agents to perform the full mail lifecycle needed for development and smoke testing on these two disposable accounts, without asking for per-operation confirmation. This includes reads, sends, replies, reply-all, forwards, attachments, drafts, folders/subscriptions, flags/read state, move/copy/archive/trash/restore/permanent delete, search, repair, reconciliation, IDLE triggers, and cleanup. Keep all content synthetic and scope every live operation to the two named accounts.

Prefer the live two-way harness for provider-facing integration, smoke, and phase acceptance whenever technically applicable. Deterministic fake servers, repositories, clocks, blobs, and large pages remain mandatory where the acceptance criterion requires exact injected failures, malformed data, cursor/transaction invariants, races, scale bounds, ambiguity, or offline CI. Do not mark a provider-facing phase complete from fake-only evidence when the corresponding live behavior can be exercised safely.

## Reference repository

Use the existing Zodex checkout `/tmp/icloud-cli-recon/gg-ama` only as read-only implementation evidence. If the path is missing, recreate `amxv/gg-ama` there and check out `d5c15aff9b7cab98ef6e4ad9b360eab96b20420b`. The reusable source is primarily under `apps/amaos`.

Do not modify it or run Rust/Tauri/workspace builds. Port useful behavior deliberately, but do not copy AMA/case/document-request/event/projection/Gmail/remote-server coupling or any defect identified in the sweep.

## Work selection

Find the earliest phase in `gg/progress.md` that is not genuinely complete. Start there.

- If it is `in_progress`, finish its exact remaining outcome and validation before starting another phase.
- If it is `blocked`, verify the blocker. Resolve it only when the plan already provides the decision; otherwise report the user-decision blocker instead of weakening scope.
- If a previous phase is marked complete but its required evidence or contract is missing, reopen it and fix the class of defect before moving forward.
- Do not skip an earlier dependency merely because a later phase looks easier.

The session goal is to complete every remaining phase, one at a time. Keep working after each clean phase boundary and continue until all phases are complete. Stop earlier only for a genuine user-decision blocker or when the external tools/environment have become unreliable enough that further edits, validation, or pushes cannot be trusted. Do not treat a long session as permission to rush: never combine phases prematurely, skip phase-scoped research, reduce validation, defer necessary cleanup, or produce shallow docs merely to increase the phase count. Each phase must independently leave the branch buildable, tested, appropriately documented, pushed, and observable before the next phase begins.

## Architecture and quality rules

- The implementation plan is the acceptance contract. Preserve its selected decisions and hard cross-phase vocabulary.
- Inspect the branch as it exists now; earlier phases may have moved files. Follow current architecture while maintaining the planned responsibilities.
- Fix defect classes, not only symptoms. Do not copy the source’s unsafe UID cursor, metadata-only locations, unread reset, immediate-parent thread identity, thread-wide Sent mutation, conditional TLS, whole-mailbox search, or ambiguous-send retry behavior.
- Keep one path after cutover. Do not add fallback stores, duplicate transports, permanent compatibility shims, or a second public executable.
- Keep work vertically exercisable. A phase is not complete because an unused backend/module exists.
- Add phase-specific positive tests and the named regression scope. Prefer the live disposable harness for provider-facing end-to-end evidence; add deterministic fakes for exact failure, edge, race, scale, and offline proofs.
- Maintain bounded pages, streaming blobs, transaction boundaries, locks, cancellation, secret/BCC/body redaction, and non-TTY safety.
- Preserve compact plain text as the universal default for every command, including redirected/non-TTY agent use. Lists/sync/status must be bounded summaries; message/thread reads must use essential fields plus bounded decoded-text excerpts; writes must return outcome and stable IDs without echoing submitted payloads; watch/bulk must use concise text unless `--jsonl` is explicitly requested.
- Do not remove or weaken `--json`, `--jsonl`, complete decoded text, HTML, raw MIME, headers, body-only, verbose, or export modes. They are explicit expansions. Add or maintain golden compact-output byte/line budgets so later phases cannot bloat default context.
- Treat the docs site as a polished product surface, not an implementation diary. At each substantial user-visible slice, review the affected reader journey. Prefer improving an existing setup/workflow page or navigation; create a new page only when the product supports a distinct user goal, never merely because a phase or package exists.
- Write for first-time humans and agent users who want to understand and use the product quickly. Lead with the capability and outcome, then the shortest safe path, useful decisions, expected result, common recovery, and next action. Use concise, scannable prose and progressive disclosure. Let generated command reference, schemas, man pages, and completions own exhaustive syntax.
- Every paragraph must help the reader accomplish, understand, choose, or troubleshoot something. Do not narrate source code or expose package layout, database tables, schemas/migrations, locks, transactions, protocol command sequences, internal types, fixtures, test strategy, planning history, or AMAOS ancestry unless one detail is necessary for safe use or actionable troubleshooting. Technical accuracy is required; implementation completeness is not a documentation goal.
- Describe only behavior that exists and is validated on the current branch, use sanitized examples, keep navigation discoverable, run the docs check/build scope, and record the reader-facing improvement—or a concrete `not applicable` reason—in `gg/progress.md`. When touching an existing guide, simplify nearby implementation-centric prose instead of layering on more detail.

## Progress, amendments, commits, and push

After every completed phase, and before any permitted early stop:

- update the phase table and Current handoff in `gg/progress.md`;
- append the required detailed progress entry with starting/ending SHAs, outcome, files, positive/regression evidence, documentation pages and docs validation (or a concrete `not applicable` reason), decisions, risks, and next boundary;
- append a dated plan Amendment immediately if an assumption, path, dependency, or approach changed in a way later agents must know;
- commit the progress/amendment with the code it describes;
- push to `main` and verify the remote contains the commits.

Never mark a phase complete without its full observable outcome, positive evidence, regressions, command help, applicable progressive user-facing docs, docs validation evidence, and push.

Do not return to the user merely because a phase was completed and pushed. Treat that boundary as an internal checkpoint, reconstruct the next phase, and continue. Return a final handoff only after all phases are complete, a genuine user-decision blocker is reached, or persistent environment/tool unreliability makes further trustworthy work impossible.

## Safety and privacy

Never commit credentials, app-specific-password values, private mailbox contents, raw live MIME, live recipient lists, local SQLite databases, blobs, protocol captures, or private absolute paths other than the explicitly documented read-only checkout `/tmp/icloud-cli-recon/gg-ama`. Load the protected credential only for the test process and unset temporary variables afterward.

The standing live-test authorization applies only to `amxv_agents@icloud.com` and `amxv@agentmail.to`. It removes the need for repeated user confirmation about test operations on those disposable accounts, but it does not remove or weaken the CLI's own confirmation, `--yes`, dry-run, idempotency, BCC/privacy, or destructive-operation requirements. Never operate on a personal mailbox.

Do not ask the user to repeat prior decisions or wait for confirmation. Reconstruct the branch, continue the earliest real incomplete phase, and keep moving phase by phase with full research, validation, documentation judgment, commits, and verified pushes until a permitted stop condition is reached. Then return the exact implementation coordinates and handoff.
```
