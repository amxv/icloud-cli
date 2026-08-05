# iCloud Mail Implementation Agent Prompts

These prompts are designed for ChatGPT agents with access to the connected Zodex remote-machine tools. The implementation track works directly on `amxv/icloud-cli` `main`, phase by phase, using `progress.md` as the durable handoff ledger.

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
2. `progress.md`
3. The phase-specific sections of `gg/icloud-mail-sweep-2026-08-05.md` named by Phase 1

The implementation plan is the acceptance contract and architecture. `progress.md` is the handoff ledger. The sweep is current-state evidence, not a second plan.

## Reference repository

Clone or update `amxv/gg-ama` on the Zodex machine only as read-only context. The planning reference commit was:

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
- reconcile `progress.md` against the code and correct it if necessary.

During implementation:

- optimize for the final architecture, not a minimal template patch;
- keep the branch working at the phase boundary;
- add positive tests whose result changes because of Phase 1, plus the regression scope named in the plan;
- update command-local help and any documentation directly affected by the phase;
- do not begin Phase 2 until every Phase 1 outcome and validation item is complete.

You may continue into later contiguous phases only when the current phase is fully complete, pushed, and recorded, and you still have enough context to execute the next phase without compressing or skipping its research/validation. Prefer one thoroughly completed phase over several partial phases.

## Handoff requirements

For every phase or coherent partial slice:

- use coherent commits with descriptive messages;
- update the phase table, Current handoff, and append a full entry in `progress.md`;
- if reality invalidates the plan, append an Amendment to the implementation plan immediately and identify affected later phases;
- never mark a phase complete without phase-specific positive evidence;
- push the code and progress update to `main`;
- finish with the pushed commit SHAs, validation evidence, any amendment, known risks, and the exact next phase/boundary.

## Safety and privacy

- Never commit iCloud credentials, app-specific passwords, live mailbox content, raw MIME, recipient data, local databases, blobs, protocol captures, or private absolute paths.
- Use fake servers/fixtures for normal tests. Any live iCloud test must be explicit, opt-in, disposable-account based, and scrubbed.
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
2. `progress.md`
3. recent Git history and diffs for every commit since the previous recorded handoff
4. the current code/tests/migrations/help for the last completed and earliest incomplete phases
5. only the phase-specific sweep sections and source regions listed for the phase you will execute

Do not trust `progress.md` blindly. Reconcile it against code, tests, Git history, and the plan. If it is stale or overclaims completion, correct it before continuing and record the evidence.

## Reference repository

Use `amxv/gg-ama` only as read-only implementation evidence. The planning reference was `d5c15aff9b7cab98ef6e4ad9b360eab96b20420b`, primarily under `apps/amaos`.

Do not modify it or run Rust/Tauri/workspace builds. Port useful behavior deliberately, but do not copy AMA/case/document-request/event/projection/Gmail/remote-server coupling or any defect identified in the sweep.

## Work selection

Find the earliest phase in `progress.md` that is not genuinely complete. Start there.

- If it is `in_progress`, finish its exact remaining outcome and validation before starting another phase.
- If it is `blocked`, verify the blocker. Resolve it only when the plan already provides the decision; otherwise report the user-decision blocker instead of weakening scope.
- If a previous phase is marked complete but its required evidence or contract is missing, reopen it and fix the class of defect before moving forward.
- Do not skip an earlier dependency merely because a later phase looks easier.

Implement as many contiguous complete phases as context safely permits, but never leave several half-built phases. Each completed phase must leave the branch buildable, tested, documented for its new commands, pushed, and independently observable.

## Architecture and quality rules

- The implementation plan is the acceptance contract. Preserve its selected decisions and hard cross-phase vocabulary.
- Inspect the branch as it exists now; earlier phases may have moved files. Follow current architecture while maintaining the planned responsibilities.
- Fix defect classes, not only symptoms. Do not copy the source’s unsafe UID cursor, metadata-only locations, unread reset, immediate-parent thread identity, thread-wide Sent mutation, conditional TLS, whole-mailbox search, or ambiguous-send retry behavior.
- Keep one path after cutover. Do not add fallback stores, duplicate transports, permanent compatibility shims, or a second public executable.
- Keep work vertically exercisable. A phase is not complete because an unused backend/module exists.
- Add phase-specific positive tests and the named regression scope. Use fake protocol servers/fixtures by default.
- Maintain bounded pages, streaming blobs, transaction boundaries, locks, cancellation, secret/BCC/body redaction, and non-TTY safety.

## Progress, amendments, commits, and push

After every coherent implementation session:

- update the phase table and Current handoff in `progress.md`;
- append the required detailed progress entry with starting/ending SHAs, outcome, files, positive/regression evidence, decisions, risks, and next boundary;
- append a dated plan Amendment immediately if an assumption, path, dependency, or approach changed in a way later agents must know;
- commit the progress/amendment with the code it describes;
- push to `main` and verify the remote contains the commits.

Never mark a phase complete without its full observable outcome, positive evidence, regressions, command help/docs, and push.

## Safety and privacy

Never commit credentials, app-specific-password values, private mailbox contents, raw live MIME, live recipient lists, local SQLite databases, blobs, protocol captures, or private absolute paths. Live tests must be opt-in, disposable-account based, and scrubbed.

Do not ask the user to repeat prior decisions or wait for confirmation. Reconstruct the branch, continue the earliest real incomplete phase, validate, document, commit, push, and return the exact implementation coordinates and next handoff.
```
