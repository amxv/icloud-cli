---
title: Changelog
description: Release notes and unreleased user-visible changes for icloud.
order: 99
category: Reference
summary: Public command, output, installation, and documentation changes.
---

## Unreleased

- Added durable local application paths with `--home`, `ICLOUD_HOME`, and platform-default resolution.
- Added private first-run state creation, automatic versioned upgrades, cross-process locking, and a reported data-schema version.
- Added `icloud account list` and `icloud account show` with compact defaults, explicit JSON envelopes, typed not-found/conflict exits, and credential-reference redaction.
- Added stable prefixed account identifiers and the initial account/credential-reference state contract without storing plaintext credentials.
- Added the local-state and account-inspection guide plus updated installation, quickstart, automation, troubleshooting, command reference, and homepage content.
- Replaced the placeholder command with the public `icloud` executable across Go builds, npm installation, project metadata, and release automation.
- Added grouped Cobra help, compact `version` and `capabilities` commands, explicit JSON envelopes, request IDs, stable error classes, and deterministic stdout/stderr behavior.
- Added Bash, Zsh, Fish, and PowerShell completion generation.
- Added root and full-tree man-page generation from the live command tree.
- Added bounded compact-output golden tests and packaging checks for all supported release targets.
- Replaced the starter documentation with installation, quickstart, automation, troubleshooting, and current command-reference guides.

## 0.1.0 — 2026-08-05

- Published the initial repository scaffold, npm wrapper, release workflow, and documentation site before iCloud-specific command implementation began.
