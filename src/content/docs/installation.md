---
title: "Installation"
description: "Install or update the icloud CLI, verify it works, and optionally enable shell completion and man pages."
order: 2
category: Start
summary: "Install with npm, update later with the same command, or build from source."
---

## Install with npm

The easiest installation is:

```bash
npm install --global @amxv/icloud-cli@latest
```

Then verify the command is available:

```bash
icloud version
icloud --help
```

The package installs the `icloud` command. You do not run `icloud-cli`.

## Update

Use the same install command whenever you want the latest published release:

```bash
npm install --global @amxv/icloud-cli@latest
icloud version
```

## Build from source

If you are working from a repository checkout:

```bash
make build
./dist/icloud version
```

For a full development verification:

```bash
make check
make docs-check
```

## Shell completion

The CLI can generate completion for Bash, Zsh, Fish, and PowerShell.

For example:

```bash
icloud completion zsh > /tmp/_icloud
icloud completion bash > /tmp/icloud.bash
```

Where you install that file depends on your shell configuration. You can always regenerate it from the installed version.

## Man pages

Generate the main man page to stdout:

```bash
icloud man > icloud.1
man ./icloud.1
```

Or generate the complete tree:

```bash
icloud man --output-dir ./man
man ./man/icloud-mail-search.1
```

## Verify the installed feature set

`capabilities` is useful when a script or agent needs to know what this build supports:

```bash
icloud capabilities
icloud capabilities --json
```

The output is tied to the installed binary, so it is a better source than assuming a particular release has a feature.

## Uninstall

If you installed with npm:

```bash
npm uninstall --global @amxv/icloud-cli
```

Uninstalling the package does **not** automatically remove the CLI's local account and mail data. See [Local data and backups](/docs/local-data-and-backups) before deleting that state.

Next: [Quickstart](/docs/quickstart).
