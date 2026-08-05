# AGENTS.md

Guidance for coding agents working on `amxv/icloud-cli`.

## Product contract

`icloud` is one local-first executable for iCloud services. The default output of every command is compact, bounded, undecorated plain text for both TTY and non-TTY callers. JSON, JSONL, complete bodies, HTML, raw MIME, headers, body-only views, and verbose diagnostics are explicit command-supported modes, never automatic defaults.

Follow the authoritative implementation track in:

- `gg/icloud-mail-implementation-plan-2026-08-05.md`
- `gg/progress.md`
- phase-scoped evidence in `gg/icloud-mail-sweep-2026-08-05.md`

Work directly on the current `main`, pull with fast-forward-only semantics, complete the earliest pending phase, update the ledger, commit coherent work, and push.

## Current architecture

- `cmd/icloud/main.go`: process startup and final `os.Exit`.
- `internal/app`: dependency composition and invocation of the CLI boundary.
- `internal/cli`: Cobra command definitions, global controls, typed errors, compact renderer, structured envelopes, completions, and man pages.
- `internal/buildinfo`: linker-injected version metadata.
- `bin/icloud.js`: npm shim for the packaged native binary.
- `scripts/postinstall.js`: release download and local Go-build fallback.
- `.github/workflows/release.yml`: CGO-disabled release matrix and npm publication.
- `src/content/docs`: progressive user-facing documentation.

Do not add storage, credentials, network behavior, or later-phase commands ahead of the plan boundary.

## Local commands

```bash
make fmt
make test
make vet
make lint
make check
make build
make build-all
make install-local
bun run docs:check
bun run docs:build
```

Run docs check and build serially.

## Output requirements

- Add a positive behavior test for every command change.
- Keep representative default output under explicit line and byte budgets.
- Reject repeated labels, duplicated request or body payloads, decorative tables, ANSI, unbounded excerpts, and implicit JSON/HTML/raw output.
- Success output goes to stdout; warnings and failures go to stderr.
- Every non-streaming command that supports JSON returns one `OutputEnvelope`.
- Streaming or bulk JSONL is opt-in and emits one typed object per line.
- Unsupported mode combinations return a typed usage error rather than being ignored.

## Documentation cadence

After a substantial public command subset or workflow is usable, update or create a page under `src/content/docs`. Write for a first-time user or agent: goal, prerequisites, safe setup, shortest workflow, representative compact output, supported expansion flags, common errors, and next actions.

Do not turn public docs into package, schema, migration, phase, or implementation notes. Run `bun run docs:check` and then `bun run docs:build`, and record both in `gg/progress.md`.

## Release contract

- public command: `icloud`
- Go entrypoint: `./cmd/icloud`
- local binary: `dist/icloud`
- release assets: `icloud_<goos>_<goarch>[.exe]`
- npm-installed native binary: `bin/icloud-bin` or `bin/icloud.exe`
- npm package: `@amxv/icloud-cli`

Preserve build-time `internal/buildinfo.Version` injection and `CGO_ENABLED=0` cross-platform builds.

## Safety and privacy

Never commit credentials, app-specific passwords, live mailbox content, raw MIME, recipient data, local databases, blobs, protocol captures, or private absolute paths. Use fake servers and sanitized fixtures. Preserve noninteractive authorization requirements for future send and destructive operations.
