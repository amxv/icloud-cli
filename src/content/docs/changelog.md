---
title: Changelog
description: Release notes and unreleased user-visible changes for icloud.
order: 99
category: Reference
summary: Public command, output, installation, and documentation changes.
---

## Unreleased

- Replaced the placeholder command with the public `icloud` executable across Go builds, npm installation, project metadata, and release automation.
- Added grouped Cobra help, compact `version` and `capabilities` commands, explicit JSON envelopes, request IDs, stable error classes, and deterministic stdout/stderr behavior.
- Added Bash, Zsh, Fish, and PowerShell completion generation.
- Added root and full-tree man-page generation from the live command tree.
- Added bounded compact-output golden tests and packaging checks for all supported release targets.
- Replaced the starter documentation with installation, quickstart, automation, troubleshooting, and current command-reference guides.

## 0.1.0 — 2026-08-05

- Published the initial repository scaffold, npm wrapper, release workflow, and documentation site before iCloud-specific command implementation began.
