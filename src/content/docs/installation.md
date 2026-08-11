---
title: Installation
description: Install or update icloud through npm or build it from source, verify the native binary and release checksum path, and enable reference assets.
order: 2
category: Start
summary: npm install/update, verified native release assets, source fallback, version checks, completions, and man pages.
---

## Prerequisites

Choose one installation path:

- npm installation: Node.js 18 or newer;
- source build: Go 1.26.5 or newer;
- documentation development: Bun 1 or newer in addition to the source-build tools.

No credential is required to install the binary, inspect capabilities, run local doctor diagnostics, or create an empty state. Account setup uses an app-specific password or an environment/file reference; it never requires the primary password for your Apple Account. Live contact occurs only when you explicitly run `account test` or `doctor --live`.

## Install through npm

```bash
npm install --global @amxv/icloud-cli
icloud version
```

The npm executable is named `icloud`. The package installer selects a release asset with this shape:

```text
icloud_<operating-system>_<architecture>[.exe]
```

On macOS and Linux, the installed native binary lives beside the JavaScript shim as `icloud-bin`. On Windows it is `icloud.exe`. Before replacing that binary, the installer downloads `checksums.txt` from the same tagged GitHub release and verifies the asset's SHA-256 digest. A missing or mismatched checksum is treated exactly like an unavailable release asset; an unverified download is never installed.

Each release also publishes `release-metadata.json`, a machine-readable list of the six supported native assets, target operating system/architecture, byte size, and SHA-256 digest.

If downloading the release asset fails, the installer checks for Go and the packaged `cmd/icloud` source, then performs a local fallback build. A failure in both paths stops installation rather than leaving a non-working command.

## Build from source

From the repository root:

```bash
make check
make build
./dist/icloud version
```

Install that build into `~/.local/bin`:

```bash
make install-local
icloud --help
```

The Makefile injects the package version into the same build-time version field used by release binaries.

## Update an npm installation

Install the current tagged npm release and then ask the binary what actually runs:

```bash
npm install --global @amxv/icloud-cli@latest
icloud version
```

The npm package version selects the GitHub release tag and native asset version. Do not infer the running CLI version from a package-manager cache or directory name; `icloud version` is the authoritative check for the executable that is on `PATH`.

## Enable completion and man pages

The CLI can always generate completion from its installed command tree:

```bash
icloud completion bash > icloud.bash
icloud completion zsh > _icloud
icloud completion fish > icloud.fish
icloud completion powershell > icloud.ps1
```

The npm tarball contains the same four deterministic files under `completions/`. Install or source the file using your shell's normal completion location.

The npm package declares the generated root `man/icloud.1` page and contains the complete subcommand man tree. You can also generate a fresh tree from the running executable:

```bash
icloud man --output-dir ./man
man ./man/icloud.1
```

## Verify identity and capabilities

```bash
icloud version
icloud capabilities
```

Both commands use one compact line by default. `capabilities` includes the installed local-state schema version. Use `--json` only when a consumer needs structured fields.

## Verify local state

Choose an isolated directory for a first read:

```bash
ICLOUD_HOME=./icloud-state icloud account list
```

A new installation prints `accounts count=0` and creates the state root. Omitting both `--home` and `ICLOUD_HOME` uses the platform data directory described in [Local state and account inspection](/docs/local-state-and-accounts).

## Verify account setup

For a safe first profile, read [Account setup and credentials](/docs/account-setup-and-credentials), then choose native keyring, environment, or file-backed setup. A successful add returns `pending_validation`; follow it with the read-only workflow in [Account validation and doctor](/docs/account-validation-and-doctor).

## Repair an npm installation

When the JavaScript shim exists but its native binary does not:

```bash
npm rebuild --global @amxv/icloud-cli
```

If that still fails, check that the installed package version has a matching GitHub release asset **and** checksum entry, or that Go is available for the fallback build. A checksum mismatch should be treated as a failed native download, not bypassed.

## Uninstall

For a global npm installation:

```bash
npm uninstall --global @amxv/icloud-cli
```

Package removal does not delete local application state, native keyring entries, environment variables, or external credential files. Remove accounts through `icloud account remove <selector> --yes` before uninstalling when you want keyring cleanup. Confirm the selected root and preserve anything needed before deleting it manually. `version`, `capabilities`, completion, man, and help do not create state.
