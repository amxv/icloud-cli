# Contributing

## Prerequisites

- Go 1.26+
- Node.js 18+
- Bun 1+
- npm publish rights for `@amxv/icloud-cli` when preparing a release

## Local validation

```bash
make check
make build
./dist/icloud --help
./dist/icloud capabilities
bun run docs:check
bun run docs:build
```

Run the documentation commands serially. Use `make build-all` when changing command paths, linker flags, packaging, or release behavior.

## Release process

1. Ensure `main` is clean, current, and fully validated.
2. Update `src/content/docs/changelog.md` with user-visible changes.
3. Commit and push the changelog and any release metadata.
4. Create and push a `v*` tag:

```bash
make release-tag VERSION=0.2.0
```

The GitHub Actions release workflow runs Go and Node checks, builds CGO-disabled native binaries, publishes GitHub release assets, and publishes npm.

## Required secret

`NPM_TOKEN` must exist in the repository Actions secrets with permission to publish `@amxv/icloud-cli`. Never print or synchronize it through repository tooling.

## Public identity

Keep these surfaces aligned in one change:

- executable and help name: `icloud`
- Go entrypoint: `cmd/icloud`
- npm shim: `bin/icloud.js`
- native asset base: `icloud`
- package: `@amxv/icloud-cli`
- docs: `https://icloud.ashray.xyz`

When command behavior changes, update command-local help, tests, progressive user docs, and `gg/progress.md` before marking the phase complete.
