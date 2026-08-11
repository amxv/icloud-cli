---
title: "Conversations and threads"
description: "Browse related messages as conversations, read them chronologically, mute local conversations, or apply deliberate thread-wide actions."
order: 13
category: Mail
summary: "Work with `thr_…` conversation IDs without needing to understand how the grouping is built."
---

`icloud` groups related synchronized messages into conversations and gives each conversation a stable-looking `thr_…` selector for normal use.

You do not need to understand the grouping algorithm to use the feature.

## List recent conversations

```bash
icloud mail threads list
```

Filter them like normal local mail:

```bash
icloud mail threads list --query launch --unread
icloud mail threads list --participant teammate@example.com
```

The older `icloud mail latest` command is a compatibility alias for the list view.

## Read a conversation

```bash
icloud mail threads show thr_…
```

You can start from a message ID too:

```bash
icloud mail threads show msg_…
```

The default shows a concise chronological conversation. Ask for complete bodies when needed:

```bash
icloud mail threads show thr_… --full
icloud mail threads show thr_… --format html
```

For a body-only stream:

```bash
icloud mail threads show thr_… --full --body-only
```

## Continue a long conversation

If the result contains a `next_cursor`:

```bash
icloud mail threads show thr_… --limit 20 --cursor cur_…
```

Use the cursor only with the same conversation.

## Mute a conversation locally

```bash
icloud mail threads mute thr_…
icloud mail threads unmute thr_…
```

Mute is a local CLI preference. It does not move, delete, archive, or mark the mail read in iCloud.

## Change a conversation

Thread actions apply the normal message actions to the selected conversation while keeping the provider folder scope explicit.

Examples:

```bash
icloud mail threads mark-read thr_… --folder inbox
icloud mail threads flag thr_… --folder inbox
icloud mail threads archive thr_… --from inbox --dry-run
icloud mail threads trash thr_… --from inbox --dry-run
```

For a real location-changing action, review first and then use the confirmation/idempotency options shown by that command's help.

If a conversation is very large, the CLI may require explicit paging rather than silently acting on only part of it.

## If grouping looks surprising

First synchronize the folders that may contain the rest of the conversation:

```bash
icloud mail sync
icloud mail threads show thr_…
```

If you need diagnostic detail:

```bash
icloud mail threads show thr_… --metadata-only --verbose --json
```

Old conversation selectors can sometimes redirect to a newer canonical thread after more mail arrives. Existing `msg_…` message IDs remain the safest exact selectors for one-message actions.

For bulk actions over a reviewed set of messages or conversations, see [Reviewed bulk actions](/docs/review-and-apply-mail-selections).
