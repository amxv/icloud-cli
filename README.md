# icloud

`icloud` is a command-line interface for Apple's iCloud ecosystem. **iCloud Mail is the comprehensive service today**: read, search, synchronize, draft, send, reply, organize, import, export, secure, and automate your mailbox from one predictable command tree.

The project is designed to grow beyond Mail over time. Contacts, Reminders, Calendar, and other iCloud services can live under the same `icloud` command as they become implemented and reliable.

## Install

```bash
npm install --global @amxv/icloud-cli@latest
icloud version
```

## Get started

Use an Apple **app-specific password**, not your normal Apple Account password:

```bash
icloud account add --name Personal --email you@icloud.com
icloud account test Personal
icloud mail folders refresh
icloud mail sync
icloud mail messages list
```

Search locally:

```bash
icloud mail search invoice
```

Read one selected message:

```bash
icloud mail message show msg_… --full
```

Preview a reply before sending it:

```bash
icloud mail reply msg_… --body-file reply.txt --dry-run
```

Then send the reviewed reply with a stable idempotency key:

```bash
icloud mail reply msg_… \
  --body-file reply.txt \
  --idempotency-key reply-01 \
  --yes
```

The [Quickstart](https://icloud.ashray.xyz/docs/quickstart) walks through the full first-use flow.

## Mail today

The current CLI includes:

- multiple iCloud Mail accounts and safe credential sources;
- folder discovery, current sync, foreground watch, and historical backfill;
- local search plus explicit fresh provider search;
- messages, conversations, attachments, and exact EML access;
- drafts, send, reply, reply-all, forward, aliases, and signatures;
- read/flag state, move/copy, archive, trash, junk, restore, and delete;
- reviewed bulk actions and bounded empty-folder workflows;
- mailbox and EML import/export;
- mailing-list unsubscribe and local VIP/blocked sender preferences;
- S/MIME inspection, decryption, signing, and encryption;
- outbound operation history and safe-retry Outbox recovery;
- compact text, JSON, JSONL, strict request schemas, completion, and man pages.

Check the exact feature set of the binary you installed with:

```bash
icloud capabilities
icloud capabilities --json
```

## Built for agents too

Normal output is compact so an agent can explore without pulling full message bodies into context. Stable IDs such as `msg_…`, `thr_…`, `att_…`, and `drf_…` make multi-step workflows easy to compose.

```bash
icloud mail search invoice
icloud mail message show msg_… --full
icloud mail attachments list msg_…
icloud mail reply msg_… --body-file reply.txt --dry-run
```

Use `--json` when you need structured parsing, `--jsonl` for event streams or per-item results, and `--no-input` to prevent implicit prompts in automation.

See the [Agent guide](https://icloud.ashray.xyz/docs/agent-guide) and [Automation recipes](https://icloud.ashray.xyz/docs/automation-recipes).

## Documentation

The documentation is organized around user tasks rather than implementation details:

- [What is icloud?](https://icloud.ashray.xyz/docs/overview)
- [Quickstart](https://icloud.ashray.xyz/docs/quickstart)
- [Mail guides](https://icloud.ashray.xyz/docs)
- [Supported features](https://icloud.ashray.xyz/docs/supported-features)
- [Command reference](https://icloud.ashray.xyz/docs/command-reference)
- [Troubleshooting](https://icloud.ashray.xyz/docs/troubleshooting)

Raw Markdown is available from `/docs.md` and each guide's `.md` route for agent-friendly retrieval.

## Project direction

Mail is the first complete command family, not the intended limit of the project. The top-level `icloud` command leaves room for additional iCloud services without pretending they already exist. The installed command tree and `icloud capabilities` remain the source of truth for what is available in a particular release.

## Development

Prerequisites: Go 1.26.5+, Node.js 18+, and Bun 1+ for the docs site.

```bash
make references
make check
make build-all
make docs-check
make docs-build
```

`make references` regenerates schemas, the exhaustive command reference, man pages, and shell completion from the current CLI sources.

## License

Apache 2.0
