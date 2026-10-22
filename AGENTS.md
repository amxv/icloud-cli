# AGENTS.md

Guidance for coding agents working on `amxv/icloud-cli`.

## What this project is

`icloud` is a command-line interface for Apple's iCloud ecosystem. iCloud Mail is the comprehensive service today, with support for account setup, synchronization, search, reading, attachments, drafts, sending, replies, forwarding, organization, import/export, S/MIME, recovery workflows, and automation.

The project is intended to grow beyond Mail over time. Future services such as Contacts, Reminders, Calendar, and other parts of iCloud should fit naturally under the same `icloud` command rather than becoming unrelated tools.

The CLI is designed for both people and agents:

- compact plain text is the normal output;
- JSON and JSONL are explicit structured modes;
- complete bodies, HTML, headers, raw content, and verbose diagnostics are explicit expansions;
- stable IDs make multi-step workflows composable;
- writes favor dry-run review, deliberate authorization, and idempotency where appropriate.

When behavior matters, treat the current executable, tests, and user-facing documentation as the source of truth rather than assuming an old implementation note or planning document is still current.

## How to orient yourself

Start from the public surface before diving into implementation details:

```bash
icloud --help
icloud capabilities
icloud mail --help
```

For a specific task, read that command's `--help` and the matching page in the documentation. The docs are organized around user workflows and are usually the fastest way to understand intended behavior.

At a high level, the repository contains:

- **CLI and application code** — the Go implementation of commands, Mail behavior, local state, provider integration, and supporting domain logic;
- **tests** — unit, integration, behavioral, and regression coverage close to the code they exercise;
- **documentation** — the user-facing Astro/ZueDocs site and generated command reference;
- **automation contracts** — generated schemas and structured-input/output definitions used by agents and scripts;
- **packaging and release support** — npm packaging, native binaries, shell completion, man pages, and release tooling;
- **project support files** — build configuration, contributor guidance, and task-specific working material when present.

Do not treat particular implementation files or directories as permanent architecture. Search the current tree, follow the code that owns the behavior you are changing, and use nearby tests and conventions as your guide.

## Useful commands

The Makefile is the best overview of supported development tasks. Common commands are:

```bash
make fmt
make test
make vet
make lint
make check
make build
make build-all
make references
make reference-check
make docs-check
```

For docs-only work, use `make docs-check`; production Astro builds run in GitHub Actions.

For command/reference changes, regenerate derived assets with:

```bash
make references
```

Before finishing a meaningful code change, run the narrow tests that cover the behavior you touched and then the broadest relevant repository checks that are practical for the task.

## Working principles

- Understand the user-visible behavior before changing implementation details.
- Prefer existing public concepts and command patterns over introducing parallel mechanisms.
- Keep defaults concise and predictable for both humans and agents.
- Preserve structured output compatibility deliberately; do not make incidental JSON or JSONL changes.
- Keep network and provider writes explicit. Dry runs should remain genuinely non-mutating when a command promises that behavior.
- Preserve stable IDs and recovery/idempotency semantics when changing multi-step workflows.
- Add or update tests for user-visible behavior and regressions introduced by a change.
- Update user-facing docs when a command, workflow, default, or capability changes materially.
- Keep generated references, schemas, completion, and man pages synchronized with the command surface when applicable.
- Prefer the current code and tests over stale assumptions. Historical plans or progress notes can provide context, but they are not standing instructions unless a task explicitly makes them authoritative.

## Documentation style

Write documentation for someone trying to use the CLI, including agents using it as a tool. Lead with the goal, the shortest useful command sequence, examples, important behavior, and likely next actions.

Avoid exposing implementation machinery unless it changes what a user needs to know. Detailed protocol, storage, migration, package, or internal architecture notes belong in code, tests, design material, or a narrowly technical reference—not in ordinary task guides.

The exhaustive generated command reference exists for exact syntax and every flag. Narrative docs should explain how to accomplish tasks without duplicating that reference.

## Safety and privacy

Treat mailbox data and credentials as private. Never commit app-specific passwords, live credentials, private keys, real mailbox content, raw private messages, local account databases, private blobs, or other user secrets.

Use deliberate test data and preserve the CLI's existing safety boundaries around credentials, outbound mail, destructive actions, raw/binary output, and non-interactive execution. If a change affects one of those boundaries, inspect the current behavior and tests carefully rather than assuming how it works.

## Release and compatibility mindset

The public command is `icloud`, distributed through `@amxv/icloud-cli` and native release binaries. The project supports macOS, Linux, and Windows builds.

Treat the installed command tree, `icloud capabilities`, generated schemas, completion, man pages, and hosted docs as different views of the same public product. When one view changes intentionally, check whether the others need to change with it.

### Docs deployment

The Docs GitHub Actions workflow deploys production only for the watched site paths; Vercel Git auto-deployments and `ignoreCommand` stay disabled. Keep the project-scoped `VERCEL_TOKEN` Actions secret and ID variables configured. Check Astro locally; builds run in CI.
