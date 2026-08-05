# icloud

`icloud` is a local-first command-line client for iCloud services. The project is being built for agent workflows without making the normal terminal experience noisy: compact plain text is the default, and structured or expanded output is always explicit.

The current implementation provides the public executable and CLI contract plus durable local account-state inspection. It creates isolated local state, lists stored accounts, shows one account by stable selector, reports the data-schema version, and retains version, capability, completion, man, JSON, exit, and stdout/stderr contracts. It does not add credentials or contact iCloud yet.

## Install

```bash
npm install --global @amxv/icloud-cli
icloud version
icloud capabilities
```

Build from source:

```bash
make check
make build
./dist/icloud --help
```

## Current commands

```bash
icloud account list [--json]
icloud account show <name|email|account-id> [--json]
icloud version [--json]
icloud capabilities [--json]
icloud completion bash|zsh|fish|powershell
icloud man [--output-dir <directory>]
icloud help [command]
```

Representative default output:

```text
accounts count=0
version 0.1.0
capabilities command=icloud version=0.1.0 schema=1 default_output=compact json=true completion=bash,zsh,fish,powershell man=true
```

Redirected and non-interactive execution uses the same compact default. Use `--json` only on commands that advertise it. For isolated state, set `ICLOUD_HOME=./icloud-state` or pass `--home ./icloud-state`.

## Development

Prerequisites: Go 1.26+, Node.js 18+, and Bun 1+ for the docs site.

```bash
make fmt
make test
make vet
make lint
make check
make build-all
bun run docs:check
bun run docs:build
```

Run the docs commands serially.

## Project layout

- `cmd/icloud`: process startup and final exit behavior
- `internal/app`: dependency composition and execution entry
- `internal/account`, `internal/config`, `internal/id`, and `internal/store`: local account state, paths, identifiers, and persistence
- `internal/cli`: Cobra tree, compact rendering, JSON/JSONL primitives, help, errors, completion, and man generation
- `internal/buildinfo`: linker-injected version metadata
- `bin/icloud.js`: npm executable shim
- `scripts/postinstall.js`: release-asset installer with local Go fallback
- `.github/workflows/release.yml`: cross-platform release and npm publication
- `src/content/docs`: user-facing guides
- `gg`: implementation plan, sweep, and progress ledger

## Documentation

The hosted documentation is at `https://icloud.ashray.xyz`. Local docs development:

```bash
bun install
bun run docs:dev
```

## License

Apache 2.0
