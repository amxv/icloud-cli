---
title: Changelog
description: "Release notes for this CLI."
order: 99
category: Distribution
summary: Placeholder changelog for projects created from this template.
---

This page is a starter changelog for projects created from this template. Replace the placeholder entries with real release notes when you cut versions.

## 0.1.0 - 2026-08-05

- Initial scaffold release, bootstrapped from `amxv/go-cli-template`.
- No iCloud-specific CLI behavior yet; the binary still exposes the
  template's placeholder `hello` command.
- Establishes the npm wrapper package, GitHub release workflow, and docs site.

## Maintainer notes

When adding future entries:

- Keep the newest version at the top.
- Add sections only for version tags that exist.
- Summarize code and product changes from `main` since the previous version tag.
- This is an OSS template, so internal code changes may be included when useful.
- Do not include docs-site-only changes such as site styling, docs package bumps, deploy plumbing, footer/layout changes, or documentation navigation changes.
- Rewrite commit subjects into clear release notes instead of pasting raw commit messages.
- If a release contains only tagging/release metadata, write: `Maintenance release. No direct code behavior changes beyond release preparation.`
