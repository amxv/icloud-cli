---
title: "Supported features"
description: "A plain-language snapshot of what the current icloud CLI can do, what is local-only, and which broader iCloud services are not implemented yet."
order: 60
category: Reference
summary: "Use this page for orientation and `icloud capabilities` for the exact installed-build contract."
---

This page describes the current project at a human level. For machine-readable truth about the installed binary, run:

```bash
icloud capabilities
icloud capabilities --json
```

## iCloud Mail

### Accounts

Supported:

- multiple Mail accounts;
- app-specific passwords through the system keyring, environment references, protected files, or stdin setup;
- account validation and diagnostics;
- a default account plus per-command selection.

### Reading and search

Supported:

- current Inbox and folder synchronization;
- foreground watching for new mail;
- deliberate historical backfill;
- local message and conversation browsing;
- local full-text and metadata search;
- bounded fresh provider search and selective caching;
- complete text and HTML bodies on request;
- exact raw EML access;
- attachments.

### Compose and send

Supported:

- new mail;
- reply and reply-all;
- forward;
- CC, BCC, Reply-To, custom safe headers;
- text and HTML bodies;
- file and inline attachments;
- drafts;
- aliases / sender identities;
- reusable signatures;
- idempotent outbound operations;
- review-only dry runs.

### Organizing mail

Supported:

- read/unread;
- flag/unflag;
- move and copy;
- archive/unarchive;
- trash/restore;
- junk/not-junk;
- permanent deletion;
- reviewed bulk selections;
- bounded empty Trash/Junk/custom-folder workflows;
- local VIP and blocked-sender preferences;
- local conversation mute preferences.

### Folders

Supported:

- discovery and counts;
- choosing routine sync folders;
- create, rename, and delete custom folders;
- subscribe and unsubscribe;
- local role bindings for Sent, Drafts, Archive, Trash, and Junk when needed.

### Import, export, and recovery

Supported:

- single-message EML export/import;
- mboxrd mailbox export/import;
- local sync checks and repair;
- operation history;
- safe-retry Outbox for eligible pre-send failures;
- outbound reconciliation against Sent.

### S/MIME

Supported:

- signed-message inspection;
- public certificate storage and explicit trust;
- external private-key references;
- local decryption;
- outbound signing, encryption, and sign+encrypt.

### Automation

Supported:

- compact human/agent output;
- JSON responses;
- JSONL streams and bulk results;
- strict JSON request files for supported commands;
- published schemas;
- stable exit classes and request IDs;
- shell completion and generated man pages.

## Current limitations worth knowing

The current Mail surface does not expose Apple Mail colored flags as provider state. Provider-forwarded state is also not treated as a reliable supported state. Some Apple Mail UI preferences remain local to Apple clients and are not claimed as iCloud CLI features.

Use:

```bash
icloud mail settings show --verbose
```

for the current account's compatibility notes.

## Broader iCloud services

Contacts, Reminders, Calendar, Drive, Notes, Photos, Find My, and other non-Mail iCloud services are **not implemented in the current CLI** unless they appear in your installed `icloud --help` / `icloud capabilities` output.

The project is intentionally structured so more iCloud service families can be added over time. See [The iCloud ecosystem](/docs/icloud-ecosystem).
