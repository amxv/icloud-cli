---
title: Installation
description: Install icloud through npm or build it from source, then verify the native binary and version.
order: 2
category: Start
summary: npm installation, native fallback behavior, local builds, and verification.
---

## Prerequisites

Choose one installation path:

- npm installation: Node.js 18 or newer;
- source build: Go 1.26 or newer;
- documentation development: Bun 1 or newer in addition to the source-build tools.

No iCloud credentials are required by the current command set.

## Install through npm

```bash
npm install --global @amxv/icloud-cli
icloud version
```

The npm executable is named `icloud`. The package installer selects a release asset with this shape:

```text
icloud_<operating-system>_<architecture>[.exe]
```

On macOS and Linux, the installed native binary lives beside the JavaScript shim as `icloud-bin`. On Windows it is `icloud.exe`.

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

## Verify identity and capabilities

```bash
icloud version
icloud capabilities
```

Both commands use one compact line by default. Use `--json` only when a consumer needs structured fields.

## Repair an npm installation

When the JavaScript shim exists but its native binary does not:

```bash
npm rebuild --global @amxv/icloud-cli
```

If that still fails, check that the installed package version has a matching GitHub release asset or that Go is available for the fallback build.

## Uninstall

For a global npm installation:

```bash
npm uninstall --global @amxv/icloud-cli
```

The current build creates no mail database, blob cache, or credential record, so there is no application data to remove yet.
