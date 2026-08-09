# iCloud Mail Agent Parity Expansion — Zodex Implementation Prompts

These prompts are for `amxv/icloud-cli` at `/workspace/repos/icloud-cli` and the expansion track planned against `main` at `1c3c9372af516564695e5daed6a766a277062141` on 2026-08-10.

Authoritative execution artifacts:

- `gg/icloud-mail-agent-parity-implementation-plan-2026-08-10.md`
- `gg/icloud-mail-agent-parity-sweep-2026-08-10.md`
- `gg/progress.md`

The earlier `gg/icloud-mail-implementation-plan-2026-08-05.md` and historical sweep/acceptance artifacts describe the already completed original implementation track. They are not the plan for this expansion unless the new plan explicitly points at a historical defect or contract.

## Prompt 1 — First implementation agent

```text
You are implementing Phase 1 of the iCloud Mail Agent Parity Expansion in `amxv/icloud-cli` using the connected Zodex remote-machine tools.

Repository and branch contract
- Work in the existing checkout `/workspace/repos/icloud-cli`.
- Work directly on the current `main`.
- Fetch first and pull with fast-forward-only semantics.
- Preserve any newer remote work. Do not reset, force-push, rewrite history, or create a replacement branch.
- The planning baseline was `1c3c9372af516564695e5daed6a766a277062141`, but the current remote `main` is authoritative. Inspect every intervening commit before editing.

Read/reconstruct in this order
1. `gg/icloud-mail-agent-parity-implementation-plan-2026-08-10.md`: read Planning Basis, State of Ideal System, Decisions and Assumptions, the global execution protocol, then Phase 1 in full. Do not load every later phase into context.
2. `gg/progress.md`: read the active Agent Parity track, current handoff, ledger rules, and the authorized disposable live-harness policy. Do not copy private harness values into output, Git, docs, or logs.
3. `gg/icloud-mail-agent-parity-sweep-2026-08-10.md`: read only the sections Phase 1 names, especially “Pagination and bounded-list contract” and the referenced Landmines.
4. Inspect the exact current source/tests/symbols in Phase 1’s “Files to read before starting”, plus Git history for any region whose behavior is surprising.

Do not trust planning/progress blindly. Reconcile the phase against the current code and tests. If an earlier change already completed part of Phase 1, verify it and finish the real remaining defect class rather than duplicating it. If implementation reality invalidates a load-bearing plan assumption or selected architecture, add an Amendment to the plan immediately and name every later phase affected; never weaken the user’s acceptance criteria in an Amendment.

Implement Phase 1 only: “Make every in-scope bounded local inventory traversable.” Carry it end to end through repository APIs, public CLI output, structured JSON/schema/reference surfaces, tests, and relevant reader-facing docs. The observable requirement is that every in-scope list that can report `more=true` also returns a usable opaque `next_cursor`, and callers can enumerate all records without duplication/omission or cross-scope cursor reuse.

Architecture and safety constraints
- Preserve the existing one `icloud` executable, one versioned SQLite canonical store, one content-addressed blob root, and narrow IMAP/SMTP provider interfaces.
- Preserve compact plain text as the universal default; JSON/JSONL and full/raw/body/header modes remain explicit.
- Preserve BCC/private-content redaction, native keyring/file/env credential references, exact provider-write safety, cancellation, idempotency, and unknown-send behavior.
- Fix the whole pagination defect class for the Phase 1 surfaces; do not raise hidden limits as a substitute for continuation.
- Dynamic shell completion’s >100 limitation and the provider LIST snapshot >1,000 guard are explicitly out of scope for this track.
- Do not implement or scaffold excluded features: rules/category classifiers, Primary/Updates/Transactions/Promotions, Send Later, Undo Send, reminders/follow-up, saved searches/Smart Mailboxes, Mail Drop, durable event logging, or a local spam classifier.

Evidence and docs
- Add phase-specific positive evidence whose outcome fails/does not exist on the pre-phase branch and passes afterward.
- Include cursor tie/order, changed-scope rejection, >one-page scale, and all Round 1 row-iteration error invariants relevant to the changed stores.
- Run the repository’s appropriate normal/race/generated-reference/schema/package checks; do not claim a check you did not run.
- Update command-local help/examples and the relevant task-focused docs if the public traversal contract changes. Regenerate schemas/reference/man/completion assets from their sources rather than editing generated artifacts by hand.
- Use the authorized disposable live harness only if Phase 1 actually needs provider evidence; it should normally be local-only. Never use a personal mailbox or commit credentials, live raw mail, databases, blobs, private addresses, private paths, or fixture payloads.

Handoff
- Update the active Phase 1 row and Current handoff in `gg/progress.md` in the same final commit/slice. Append a full progress entry with starting SHA, pushed ending SHA(s), observable outcome, files/areas, positive evidence, regression evidence, docs evidence or a concrete not-applicable reason, local decisions, Amendments, known risks, and exact Phase 2 handoff.
- Commit coherently and push normally to `origin/main`.
- Fetch after push and verify local HEAD equals `origin/main` and the worktree is clean.
- Return the exact full pushed SHA(s), validation performed, any Amendment, and the exact next phase/boundary.
```

## Prompt 2 — Subsequent implementation agents

```text
You are continuing the iCloud Mail Agent Parity Expansion in `amxv/icloud-cli` using the connected Zodex remote-machine tools.

Repository and branch contract
- Use the existing checkout `/workspace/repos/icloud-cli` on `main`.
- Fetch first and pull fast-forward-only. Preserve newer remote work; never reset, force-push, rewrite shared history, or replace the branch.
- The planning baseline was `1c3c9372af516564695e5daed6a766a277062141`; it is historical context only. The current `origin/main` is authoritative.

Reconstruct the real handoff before changing code
1. Read `gg/icloud-mail-agent-parity-implementation-plan-2026-08-10.md` Planning Basis, State of Ideal System, Decisions and Assumptions, global execution protocol, and the earliest genuinely incomplete/in-progress phase. Read a later phase only when it is the next phase you will actually execute or the current phase explicitly names its cross-phase contract.
2. Read the active Agent Parity section, Current handoff, ledger rules, and latest relevant entries in `gg/progress.md`. Treat progress claims as evidence to verify, never as proof.
3. Read only the phase-specific sections/Landmines named from `gg/icloud-mail-agent-parity-sweep-2026-08-10.md`.
4. Inspect Git history/diffs since the last recorded handoff and the current code/tests/help/docs around the phase’s exact symbols.
5. If a supposedly complete earlier phase is materially incomplete or contradicts its acceptance contract, reopen and fix it before proceeding. Finish an in-progress phase before starting another.

Execution policy
- Start at the earliest genuinely incomplete phase and complete only as many contiguous phases as you can finish thoroughly. A phase is not complete until its observable capability, positive evidence, regressions, applicable help/docs/reference assets, progress entry, commit, push, and verified remote handoff are all complete.
- Keep every phase vertically exercisable through the real public path. Do not build a broad unused backend that only becomes usable several phases later.
- Preserve the plan’s hard vocabulary and boundaries: `MailIdentity`, `FolderRoleBinding`, `MailSelection`, `RemoteSearchCursor`, `SenderPreference`, `ThreadPreference`, `Signature`, `OutboxItem`, `MessageRewrite`, and `SMIMEIdentity` mean what State of Ideal System says.
- When implementation reality invalidates a load-bearing assumption, provider fact, dependency, or selected architecture, append an Amendment immediately with evidence, decision, rationale, and all affected later phases. Do not silently reinterpret or weaken an acceptance criterion.
- Fix defect classes rather than local symptoms and end migrations with one coherent path, not permanent shims/parallel implementations.

Permanent product constraints
- One `icloud` executable; one SQLite canonical store; one private content-addressed blob store; narrow provider interfaces. No hosted service, daemon, MCP server, or generic HTTP/RPC service.
- Compact plain text remains the universal default for humans and agents. JSON is explicit for bounded results; JSONL is explicit for streaming/bulk. Full body, HTML, raw MIME, headers, verbose diagnostics, and binary bytes never appear implicitly.
- Preserve strict JSON input validation, bounded pagination, stable IDs, cancellation/timeouts, idempotency, non-TTY authorization, BCC privacy, file permissions, ambiguous-provider-outcome handling, and Round 1/2 Crucible regressions.
- Apple/provider-specific state is claimed only when deterministic/live evidence supports it. A phase that permits an explicit `unsupported` result (forwarded provider keyword, colored flags, Hide My Email sending) must not invent a local provider representation to force a PASS.
- Junk uses Apple/iCloud’s Junk folder semantics only. Do not add spam scoring/classification.
- Do not implement excluded scope anywhere in this track: local/server rules engine, Primary/Updates/Transactions/Promotions categories, custom category classifier, saved searches/Smart Mailboxes, Send Later, Undo Send, reminders/follow-up, Mail Drop, durable watch event log, provider >1,000 LIST-snapshot work, dynamic-completion >100 work, Contacts, or Apple-client GUI features.
- Do not reverse-engineer private iCloud.com/Apple Account endpoints. `mail settings` must report unsupported-current-auth truthfully for web-only settings until the user explicitly authorizes a new supported integration track.

Provider/live testing
- The authorized disposable iCloud/AgentMail harness and standing destructive-test authorization are documented in `gg/progress.md`. Discover any connected AgentMail tool schema fresh in the session.
- Use the iCloud side through the CLI/transport under test and the independent disposable counterpart for delivery/threading verification when provider behavior can be exercised safely.
- Deterministic fakes remain required for races, malformed protocol/MIME/CMS, provider disconnect timing, ambiguity, large state, cursor windows, partial writes, and offline CI.
- Never operate on a personal mailbox. Never print, paste, commit, attach, or document app-specific passwords, private live addresses/content, raw MIME, local databases/blobs, S/MIME private keys, or private filesystem paths.

Docs/contracts per public slice
- Update command-local help/examples and the smallest coherent reader journey under `src/content/docs`; write for a first-time human/agent, not an implementer. Keep implementation tables/migrations/package names/test diary out of narrative docs unless required for safe troubleshooting.
- Regenerate command reference, man pages, completions, and JSON schemas from their source generators. Keep `capabilities --json` truthful and compact capabilities bounded.
- Run the repository’s docs validation/build for every applicable public checkpoint and record the exact evidence in progress.

Progress/push discipline
- Update `gg/progress.md` with the active phase status and append one full entry after each completed or blocked phase. Record starting SHA, ending pushed SHA(s), observable result, files/areas, positive evidence, regressions, docs, decisions, Amendments, known defects/risks, and exact next handoff.
- Commit coherent phase work directly to main and push normally. If `origin/main` advances, fetch, inspect, and integrate it without dropping either agent’s work; no force push.
- After the final push of the session, fetch again, verify local HEAD equals `origin/main`, verify a clean worktree, and return exact commit coordinates plus the earliest remaining phase/boundary.
```
