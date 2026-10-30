const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const root = path.resolve(__dirname, "..");
const docsDir = path.join(root, "docs", "src", "content", "docs");

function titleSlug(title) {
  return title
    .normalize("NFKD")
    .replace(/[\u0300-\u036f]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "");
}

function read(name) {
  return fs.readFileSync(path.join(docsDir, name), "utf8");
}

function narrativeFiles() {
  return fs.readdirSync(docsDir)
    .filter((name) => name.endsWith(".md") && name !== "command-reference.md")
    .sort();
}

test("documentation filenames match their public page titles", () => {
  for (const name of fs.readdirSync(docsDir).filter((value) => value.endsWith(".md"))) {
    const source = read(name);
    const match = source.match(/^title:\s*(.+)$/m);
    assert.ok(match, `${name} is missing a title`);
    const title = match[1].trim().replace(/^['"]|['"]$/g, "");
    assert.equal(path.basename(name, ".md"), titleSlug(title), `${name} should match title ${title}`);
  }
});

test("documentation is organized around discoverable user and agent tasks", () => {
  const coverage = new Map([
    ["orientation", ["what-is-icloud.md", ["What is icloud?", "Mail is the comprehensive"]]],
    ["broader iCloud direction", ["the-icloud-ecosystem.md", ["Available today: Mail", "icloud contacts", "icloud reminders"]]],
    ["quickstart", ["quickstart.md", ["account add", "mail sync", "messages list", "mail send"]]],
    ["Mail overview", ["mail.md", ["Pick a task", "mail sync", "--dry-run"]]],
    ["accounts", ["accounts-and-credentials.md", ["app-specific password", "credential replace", "account default"]]],
    ["reading", ["read-mail.md", ["messages list", "message show", "--full"]]],
    ["search", ["search-mail.md", ["mail search", "--remote", "--cache"]]],
    ["conversations", ["conversations-and-threads.md", ["threads list", "threads show", "threads mute"]]],
    ["attachments", ["attachments.md", ["attachments list", "attachments save", "save-all"]]],
    ["compose", ["send-reply-and-forward.md", ["mail send", "mail reply", "mail forward", "--dry-run"]]],
    ["drafts", ["drafts.md", ["draft create", "draft sync", "draft send"]]],
    ["identities", ["sender-identities.md", ["identities add", "identities default", "--from"]]],
    ["signatures", ["signatures.md", ["signatures create", "signatures default", "--signature"]]],
    ["folders", ["folders.md", ["folders refresh", "folders enable", "folders create"]]],
    ["organization", ["organize-mail.md", ["mail move", "mail archive", "mail trash", "mail delete"]]],
    ["reviewed bulk actions", ["reviewed-bulk-actions.md", ["selection create", "selection apply", "request_hash"]]],
    ["permanent deletion", ["empty-trash-junk-or-a-folder.md", ["empty trash", "purge_id", "request_hash"]]],
    ["import and export", ["import-and-export-mail.md", ["message export", "message import", "mailbox export", "mailbox import"]]],
    ["mailing lists and senders", ["mailing-lists-and-sender-preferences.md", ["mail unsubscribe", "senders vip", "senders block"]]],
    ["S/MIME", ["s-mime.md", ["smime certificates", "--decrypt", "sign-encrypt"]]],
    ["outbound recovery", ["outbox-and-send-recovery.md", ["operations outbound", "outbox retry", "reconcile-outbound"]]],
    ["agent guidance", ["agent-guide.md", ["capabilities --json", "--no-input", "idempotency"]]],
    ["automation recipes", ["automation-recipes.md", ["mail watch --jsonl", "selection create", "--request-id"]]],
    ["automation reference", ["output-and-automation.md", ["--input-json", "--jsonl", "Exit classes"]]],
    ["feature coverage", ["supported-features.md", ["iCloud Mail", "Broader iCloud services", "not implemented"]]],
    ["diagnostics", ["account-checks-and-doctor.md", ["account test", "icloud doctor", "--live"]]],
    ["local data", ["local-data-and-backups.md", ["ICLOUD_HOME", "Back up", "mail sync repair --check"]]],
    ["troubleshooting", ["troubleshooting.md", ["Authentication fails", "Inbox looks stale", "send failed"]]]
  ]);

  for (const [label, [file, needles]] of coverage) {
    const source = read(file);
    for (const needle of needles) {
      assert.ok(source.toLowerCase().includes(needle.toLowerCase()), `${label} page ${file} is missing ${needle}`);
    }
  }
});

test("narrative docs stay small, user-facing, and internally linked", () => {
  const names = narrativeFiles();
  const knownSlugs = new Set([
    ...names.map((name) => name.replace(/\.md$/, "")),
    "command-reference"
  ]);

  for (const name of names) {
    const source = read(name);
    const lines = source.split("\n").length;
    assert.ok(lines <= 400, `${name} grew to ${lines} lines; split user tasks into smaller pages`);

    for (const forbidden of [
      /\bPhase \d+\b/i,
      /go test -run/i,
      /\binternal\//i,
      /UIDVALIDITY/i,
      /content-addressed blob/i,
      /canonical ingestion/i,
      /RFC graph evidence/i
    ]) {
      assert.doesNotMatch(source, forbidden, `${name} exposes implementation-oriented language`);
    }

    for (const match of source.matchAll(/\]\(\/docs\/([a-z0-9-]+)(?:#[^)]+)?\)/g)) {
      assert.ok(knownSlugs.has(match[1]), `${name} links to missing docs slug ${match[1]}`);
    }
  }
});

test("project positioning says Mail is available today without presenting future iCloud services as implemented", () => {
  const overview = read("what-is-icloud.md");
  const ecosystem = read("the-icloud-ecosystem.md");
  const supported = read("supported-features.md");
  const readme = fs.readFileSync(path.join(root, "README.md"), "utf8");
  const siteData = fs.readFileSync(path.join(root, "docs", "src", "data", "docs.ts"), "utf8");

  for (const source of [overview, ecosystem, supported, readme, siteData]) {
    assert.match(source, /Mail/i);
  }
  assert.match(ecosystem, /Available today: Mail/i);
  assert.match(ecosystem, /icloud contacts \.\.\./i);
  assert.match(ecosystem, /icloud reminders \.\.\./i);
  assert.match(supported, /not implemented in the current CLI/i);
  assert.doesNotMatch(readme, /scope is iCloud Mail only/i);
});

test("generated command reference and docs deployment boundary stay explicit", () => {
  const reference = read("command-reference.md");
  assert.match(reference, /generated from the same command tree as `icloud --help`/);
  assert.match(reference, /order: 90/);
  for (const command of [
    "icloud account add",
    "icloud mail sync repair",
    "icloud mail draft send",
    "icloud mail attachments save-all",
    "icloud mail delete",
    "icloud capabilities"
  ]) {
    assert.ok(reference.includes(`## \`${command}\``), `generated reference missing ${command}`);
  }

  const workflow = fs.readFileSync(path.join(root, ".github/workflows/docs-deploy.yml"), "utf8");
  for (const docsPath of ['"docs/**"', '"vercel.json"', '".github/workflows/docs-deploy.yml"']) {
    assert.ok(workflow.includes(docsPath), `GitHub docs deployment filter missing ${docsPath}`);
  }
  for (const unrelated of ['"internal/**"', '"cmd/**"', '"gg/**"']) {
    assert.ok(!workflow.includes(unrelated), `Docs would deploy for unrelated ${unrelated}`);
  }

  assert.ok(fs.existsSync(path.join(root, "docs", "src", "pages", "docs.md.ts")));
  assert.ok(fs.existsSync(path.join(root, "docs", "src", "pages", "docs", "[...slug].md.ts")));
  assert.match(fs.readFileSync(path.join(root, "docs", "astro.config.mjs"), "utf8"), /https:\/\/icloud\.ashray\.xyz/);
});

test("docs deployment filtering happens before Vercel receives a build", () => {
  const config = JSON.parse(fs.readFileSync(path.join(root, "vercel.json"), "utf8"));
  assert.equal(config.git.deploymentEnabled, false);
  assert.equal(Object.hasOwn(config, "ignoreCommand"), false);
  const workflow = fs.readFileSync(path.join(root, ".github/workflows/docs-deploy.yml"), "utf8");
  assert.match(workflow, /paths:\s*\n\s*- "docs\/\*\*"/);
  assert.match(workflow, /vercel@latest deploy --prebuilt --prod/);
});
