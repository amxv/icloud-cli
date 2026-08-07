const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");
const test = require("node:test");

const root = path.resolve(__dirname, "..");
const docsDir = path.join(root, "src", "content", "docs");

function read(name) {
  return fs.readFileSync(path.join(docsDir, name), "utf8");
}

test("every completed Mail workflow has a discoverable task-focused guide", () => {
  const coverage = new Map([
    ["account lifecycle", ["account-setup-and-credentials.md", ["account add", "credential replace", "account remove"]]],
    ["validation and doctor", ["account-validation-and-doctor.md", ["account test", "--live"]]],
    ["folders", ["folder-discovery-and-sync-policy.md", ["folders status", "folders create", "folders rename", "folders delete"]]],
    ["sync operations", ["sync-and-read-inbox.md", ["mail sync", "mail watch", "sync backfill", "sync rebaseline", "sync repair"]]],
    ["search and hydration", ["search-synchronized-mail.md", ["mail search", "--remote", "--cache"]]],
    ["conversation reads", ["read-reconstructed-conversations.md", ["mail latest", "thread show", "--full"]]],
    ["attachments", ["save-mail-attachments.md", ["attachments list", "attachments save", "save-all"]]],
    ["draft and outbound", ["send-mail-safely.md", ["draft create", "draft send", "mail send", "mail reply", "mail forward"]]],
    ["location changes", ["manage-mail-locations.md", ["mail move", "mail copy", "mail archive", "mail trash", "mail delete"]]],
    ["automation", ["output-and-automation.md", ["--input-json", "--jsonl", "capabilities --json"]]],
    ["installation and update", ["installation.md", ["@latest", "checksum", "completion", "man/icloud.1"]]],
    ["local backup and removal", ["local-state-and-accounts.md", ["Back up", "restore", "Removing an account"]]]
  ]);
  for (const [label, [file, needles]] of coverage) {
    const source = read(file);
    for (const needle of needles) {
      assert.ok(source.includes(needle), `${label} guide ${file} is missing ${needle}`);
    }
  }
});

test("narrative docs explain every retained output expansion without implementation diary copy", () => {
  const narrativeFiles = fs.readdirSync(docsDir)
    .filter((name) => name.endsWith(".md") && name !== "command-reference.md");
  const narrative = narrativeFiles.map(read).join("\n");
  for (const needle of [
    "compact", "--json", "--jsonl", "--full", "--format", "HTML", "raw MIME",
    "--headers", "--body-only", "--verbose", "--cursor", "--limit"
  ]) {
    assert.ok(narrative.includes(needle), `narrative docs are missing output expansion ${needle}`);
  }
  for (const forbidden of [
    /iCloud services/i,
    /\bAMAOS?\b/i,
    /\bPhase \d+\b/i,
    /go test -run/i,
    /internal\//i
  ]) {
    assert.doesNotMatch(narrative, forbidden);
  }
});

test("generated command reference and docs deployment boundary stay explicit", () => {
  const reference = read("command-reference.md");
  assert.match(reference, /generated from the same command tree as `icloud --help`/);
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

  const filter = fs.readFileSync(path.join(root, "scripts", "should-build-docs.mjs"), "utf8");
  for (const docsPath of ['"src"', '"public"', '"package.json"']) {
    assert.ok(filter.includes(docsPath), `Vercel docs filter missing ${docsPath}`);
  }
  for (const unrelated of ['"internal"', '"cmd"', '"gg"']) {
    assert.ok(!filter.includes(unrelated), `Vercel docs filter would rebuild for unrelated ${unrelated}`);
  }

  assert.ok(fs.existsSync(path.join(root, "src", "pages", "docs.md.ts")));
  assert.ok(fs.existsSync(path.join(root, "src", "pages", "docs", "[...slug].md.ts")));
  assert.match(fs.readFileSync(path.join(root, "astro.config.mjs"), "utf8"), /https:\/\/icloud\.ashray\.xyz/);
});

test("Vercel docs filter skips unrelated Go-only commits and builds real docs commits", () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "icloud-docs-filter-"));
  const git = (args) => spawnSync("git", args, { cwd: directory, encoding: "utf8" });
  try {
    assert.equal(git(["init", "-q"]).status, 0);
    assert.equal(git(["config", "user.email", "test@example.invalid"]).status, 0);
    assert.equal(git(["config", "user.name", "Docs Filter Test"]).status, 0);
    fs.mkdirSync(path.join(directory, "internal"), { recursive: true });
    fs.mkdirSync(path.join(directory, "src"), { recursive: true });
    fs.writeFileSync(path.join(directory, "internal", "state.go"), "package internal\n");
    fs.writeFileSync(path.join(directory, "src", "page.md"), "docs\n");
    assert.equal(git(["add", "."]).status, 0);
    assert.equal(git(["commit", "-qm", "base"]).status, 0);
    const base = git(["rev-parse", "HEAD"]).stdout.trim();

    fs.appendFileSync(path.join(directory, "internal", "state.go"), "// go-only\n");
    assert.equal(git(["add", "."]).status, 0);
    assert.equal(git(["commit", "-qm", "go only"]).status, 0);
    const goHead = git(["rev-parse", "HEAD"]).stdout.trim();
    const script = path.join(root, "scripts", "should-build-docs.mjs");
    const goOnly = spawnSync(process.execPath, [script], {
      cwd: directory,
      env: { ...process.env, SITE_DEPLOY_DIFF_BASE: base, VERCEL_GIT_COMMIT_SHA: goHead }
    });
    assert.equal(goOnly.status, 0, "Go-only commit should be skipped by Vercel ignoreCommand");

    fs.appendFileSync(path.join(directory, "src", "page.md"), "changed\n");
    assert.equal(git(["add", "."]).status, 0);
    assert.equal(git(["commit", "-qm", "docs"]).status, 0);
    const docsHead = git(["rev-parse", "HEAD"]).stdout.trim();
    const docsChange = spawnSync(process.execPath, [script], {
      cwd: directory,
      env: { ...process.env, SITE_DEPLOY_DIFF_BASE: goHead, VERCEL_GIT_COMMIT_SHA: docsHead }
    });
    assert.equal(docsChange.status, 1, "docs commit should force a Vercel build");
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});
