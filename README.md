# icloud

`icloud` is a local-first command-line client for **iCloud Mail**. Compact plain text is the universal default for humans and agents; JSON, JSONL streams, complete bodies, HTML, headers, raw MIME, and verbose diagnostics are explicit expansions.

It supports secure multi-account setup, IMAP/SMTP validation, folder discovery and policy, bounded forward sync and historical backfill, foreground IDLE watch, local and explicit remote search, reconstructed RFC conversations, complete message and attachment access, drafts, send/reply/reply-all/forward, read/flag state, exact-location move/copy/archive/trash/restore/delete, repair/rebaseline/reconciliation workflows, strict JSON write input, bounded JSONL bulk state changes, versioned schemas, local-only completion, and generated man pages.

## Install or update

```bash
npm install --global @amxv/icloud-cli@latest
icloud version
icloud capabilities
```

The npm installer downloads the native `icloud_<os>_<arch>[.exe]` asset for that exact package version and verifies it against the release checksum before installation. If the release asset is unavailable and Go is installed, the package can build the same version from its packaged source as a fallback.

To update later, run the same `npm install --global @amxv/icloud-cli@latest` command and confirm the result with `icloud version`.

Build from source instead:

```bash
make check
make build
./dist/icloud version
```

## First account

Use an Apple **app-specific password**, never the primary Apple Account password. Interactive setup stores the secret in the operating-system keyring by default:

```bash
icloud account add --name Personal --email owner@icloud.com
icloud account test Personal
icloud mail folders status
icloud mail sync
icloud mail inbox
```

For non-interactive setup, use `--password-stdin`, `--credential-env`, or `--credential-file`; there is no plaintext password argv flag. `account test` validates direct-TLS IMAP and mandatory-STARTTLS SMTP without sending mail.

## Safe outbound mail

Review a request before allowing a provider write:

```bash
icloud mail send \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt \
  --dry-run

icloud mail send \
  --to client@example.com \
  --subject 'Project update' \
  --body-file message.txt \
  --idempotency-key project-update-01 \
  --yes
```

The same confirmation, idempotency, BCC/privacy, and unknown-outcome rules apply to replies, forwards, draft sends, folder administration, and location-changing operations. Non-TTY writes that require authorization never infer consent.

## Automation

Redirected execution keeps compact text unless you explicitly request a structured mode. Discover the installed automation contract and schema IDs with:

```bash
icloud capabilities --json
```

Commands in that published write contract accept one strict `--input-json <path|->` request instead of equivalent write flags. Read/flag state commands also accept bounded JSONL input and can emit typed `--jsonl` item/completion events. Generated JSON Schemas ship in `schemas/v1/`.

Use content-expansion flags deliberately. `mail message show` and thread/draft reads keep complete text, HTML, decoded headers, body-only streams, verbose evidence, and raw MIME behind explicit flags; attachment bytes require an explicit output destination.

## Completion, man pages, and reference

The installed command tree can generate completion for Bash, Zsh, Fish, and PowerShell and a complete man-page tree:

```bash
icloud completion bash > icloud.bash
icloud man --output-dir ./man
man ./man/icloud.1
```

The npm package also contains deterministic `completions/`, `man/`, and `schemas/v1/` assets generated from the same command/schema sources as the executable. The exhaustive [command reference](https://icloud.ashray.xyz/docs/command-reference) is generated from Cobra; the narrative guides stay task-focused.

## Documentation

Start with the [Quickstart](https://icloud.ashray.xyz/docs/quickstart). The hosted docs also cover credentials, validation and doctor, folder policy, synchronization/backfill/watch/repair, search, conversations, attachment export, sending and drafts, location changes, local-state backup/removal, compact and structured output, schemas, installation/update, and troubleshooting.

Raw Markdown is available from `/docs.md` and each guide's `.md` route for agent-friendly retrieval.

## Development

Prerequisites: Go 1.26+, Node.js 18+, and Bun 1+ for the docs site.

```bash
make references
make check
make build-all
make docs-check
make docs-build
```

`make references` regenerates the versioned schemas, exhaustive command reference, man pages, and shell-completion assets. `make check` and `make docs-check` fail when generated references drift from the current command/schema sources.

## License

Apache 2.0
