---
title: "The iCloud ecosystem"
description: "See what the icloud CLI covers today and how the command structure can expand to more Apple iCloud services over time."
order: 5
category: Start
summary: "Mail is comprehensive today; the project is designed to grow into a broader iCloud CLI."
---

`icloud` is intended to become a general command-line home for useful iCloud workflows.

## Available today: Mail

Mail is the mature service in the current CLI. It includes:

- account and credential setup;
- folder discovery and synchronization;
- local and remote search;
- message and conversation reading;
- attachments;
- drafts;
- send, reply, reply-all, and forward;
- sender identities and signatures;
- read, flag, archive, move, trash, junk, restore, and delete actions;
- bulk reviewed actions;
- mailbox and EML import/export;
- mailing-list unsubscribe;
- Outbox and operation recovery;
- S/MIME inspection, signing, encryption, and decryption;
- JSON/JSONL automation for agents and scripts.

Start with [Quickstart](/docs/quickstart) or browse the [Mail guides](/docs).

## The broader direction

The top-level command is intentionally `icloud`, not `icloud-mail`. That leaves a clean home for more iCloud services as they become implemented and reliable.

Potential future command families can include areas such as:

```text
icloud contacts ...
icloud reminders ...
icloud calendar ...
```

Other iCloud areas may fit over time too. The important rule is that documentation should describe a service as available only once the CLI actually ships it.

## How to check what your build supports

Use the binary itself:

```bash
icloud --help
icloud capabilities
icloud capabilities --json
```

If a service is not present in `icloud --help`, do not assume the installed version can use it.

## Documentation follows capabilities

As new services arrive, they should receive their own small, task-focused documentation sections instead of making the Mail guides more complicated. Shared topics—installation, accounts, output, agents, and troubleshooting—can remain common across the CLI.
