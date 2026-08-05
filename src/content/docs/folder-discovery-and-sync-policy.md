---
title: Folder discovery and sync policy
description: Discover every iCloud Mail folder, inspect remote counts and provider metadata, understand system-folder classification, and choose which folders later synchronization may read.
order: 5
category: Guides
summary: Live folder discovery, decoded and wire names, special-use roles, status counts, stale folders, and explicit sync enablement.
---

## Discover the current mailbox tree

Run a live, read-only folder refresh after the account passes validation:

```bash
icloud mail folders status
icloud mail folders status --limit 50
```

The command:

1. resolves the selected account and app-specific password;
2. connects through direct-TLS IMAP;
3. authenticates with the proven full/short username fallback;
4. issues `LIST "" "*"` once;
5. issues `STATUS` for every selectable mailbox;
6. logs out;
7. transactionally reconciles the observations into local state.

It does **not** select folders, search for UIDs, fetch headers or bodies, download attachments, change flags, move messages, or send mail.

Use the global account selector when several profiles exist:

```bash
icloud --account Personal mail folders status
```

## Read compact folder results

A compact record has this shape:

```text
folder fld_… name=INBOX kind=inbox selectable=yes subscription=unknown present=yes sync=enabled messages=15 unseen=2
folder fld_… name=Projects/Alpha kind=other parent=Projects selectable=yes subscription=unknown present=yes sync=disabled messages=1
folder-status account=acct_… count=8 present=8 stale=0 enabled=2
```

The default record includes the decoded display name, logical kind, parent, selectable state, subscription knowledge, provider presence, local sync policy, and current message/unseen counts when STATUS succeeded.

The refresh always reconciles every discovered folder. Output is independently bounded to 20 records by default and 100 with `--limit`; the summary reports `total` and `more=true` when additional persisted records were not printed.

Request provider-level detail explicitly:

```bash
icloud mail folders status --verbose
```

Verbose output adds the canonical modified UTF-7 wire name, delimiter, LIST attributes, classification source, UIDNEXT, UIDVALIDITY, discovery time, status time, and missing-since time.

## Understand decoded names and wire names

IMAP4rev1 encodes non-ASCII mailbox names using modified UTF-7. The CLI stores both forms:

```text
name=Unicode-日本語
wire_name=Unicode-&ZeVnLIqe-
```

Use the decoded `name` for normal display and selection. The `wire_name` is provider identity data used for reconciliation; it is not a filesystem path and the CLI never joins it to a local directory.

Nested mailboxes retain their provider delimiter and parent:

```text
name=Projects/Alpha parent=Projects delimiter=/
```

## How system-folder kinds are assigned

Folder names are provider data. The CLI does not label a mailbox as Archive, Drafts, Trash, or Junk merely because its visible English name resembles one.

Classification rules are:

- `INBOX` is intrinsically `kind=inbox` under IMAP rules.
- `\Sent`, `\Drafts`, `\Archive`, `\Trash`, `\Junk`, `\All`, `\Flagged`, and `\Important` LIST attributes map to their corresponding kinds.
- When **no** mailbox carries `\Sent`, the common names `Sent`, `Sent Messages`, and `Sent Mail` are considered in that order as a compatibility fallback.
- No name-only fallback is used for Drafts, Archive, Trash, or Junk.
- Everything else is `kind=other`.

Use `--verbose` to see `kind_source=attribute`, `kind_source=fallback`, or the intrinsic Inbox source.

## Subscription can be unknown

iCloud currently rejects the LIST-EXTENDED subscribed query used by the selected IMAP library and does not include `\Subscribed` on ordinary LIST responses. The CLI therefore reports:

```text
subscription=unknown
```

It reports `subscribed` only when the provider actually supplies that attribute. Unknown is not equivalent to unsubscribed.

## Inspect locally without contacting Apple

After one live refresh, read the persisted inventory locally:

```bash
icloud mail folders list
icloud mail folders list --limit 50
icloud mail folders show INBOX
icloud mail folders show 'Unicode-日本語' --verbose
```

`list` and `show` do not resolve credentials or make network requests. A folder selector may be a stable folder ID, decoded name, exact wire name, or an unambiguous kind. Duplicate display names return a conflict rather than guessing.

## Choose future synchronization policy

Discovery and synchronization are separate decisions. Newly discovered Inbox and Sent folders default to enabled; every other folder defaults to disabled:

```bash
icloud mail folders enable Receipts
icloud mail folders disable Receipts
```

These commands update local policy only. They do not fetch or mutate messages. A policy choice survives later status refreshes, renames, disappearance, and reappearance.

Non-selectable container mailboxes cannot be enabled because they cannot contain selectable message state. Disable remains safe for any retained folder.

Phase 6 uses this policy when message synchronization begins. Enabling a folder now grants only future eligibility; it does not trigger sync in this build.

## Renamed and missing folders remain truthful

Rediscovery preserves a stable local folder ID when identity can be reconciled through:

- an exact wire name;
- provider `OLDNAME` metadata;
- a unique special-use role such as the sole Sent mailbox.

A previously known folder that is absent from a successful refresh is retained as:

```text
present=no stale=true missing_since=...
```

Live `status` includes these stale records so disappearance is explicit. Normal local `list` shows current folders only; opt into retained records with:

```bash
icloud mail folders list --include-missing
```

If the same provider folder reappears, its stable ID and sync policy are restored.

## Request JSON

```bash
icloud --request-id folders-01 mail folders status --json
icloud mail folders list --include-missing --json
```

JSON includes the complete folder record and schema version. It never includes the credential reference or value. Redirecting output does not turn JSON on automatically.

## What is still intentionally absent

The current folder commands do not synchronize messages, expose a local Inbox, create/rename/delete remote mailboxes, or change provider subscription state. Continue with the [command reference](/docs/command-reference) for exact flags and [Output and automation](/docs/output-and-automation) for error and envelope contracts.
