#!/usr/bin/env node

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");

const pkg = require("../package.json");
const repoRoot = path.resolve(__dirname, "..");

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: repoRoot,
    encoding: "utf8",
    windowsHide: true,
    ...options
  });
  if (result.error || result.status !== 0) {
    throw new Error(
      `${command} ${args.join(" ")} failed: ${result.error?.message || result.stderr || `exit ${result.status}`}`
    );
  }
  return result.stdout;
}

function main() {
  const prefix = fs.mkdtempSync(path.join(os.tmpdir(), "icloud-npm-smoke-"));
  let tarball = "";
  try {
    const packed = JSON.parse(run("npm", ["pack", "--ignore-scripts", "--json"]));
    tarball = path.join(repoRoot, packed[0].filename);
    run("npm", ["install", "--global", "--prefix", prefix, tarball], {
      env: { ...process.env, ICLOUD_CLI_FORCE_BUILD: "1" }
    });

    const wrapper = process.platform === "win32"
      ? path.join(prefix, "icloud.cmd")
      : path.join(prefix, "bin", "icloud");
    const spawnOptions = process.platform === "win32" ? { shell: true } : {};
    assert.equal(run(wrapper, ["version"], spawnOptions), `version\n${pkg.version}\n`);

    for (const shell of ["bash", "zsh", "fish", "powershell"]) {
      const completion = run(wrapper, ["completion", shell], spawnOptions);
      assert.ok(completion.length > 200 && completion.toLowerCase().includes("icloud"), `${shell} completion`);
    }
    const man = run(wrapper, ["man"], spawnOptions);
    assert.match(man, /\.TH "ICLOUD" "1"/);

    const npmRoot = run("npm", ["root", "--global", "--prefix", prefix]).trim();
    const packageRoot = path.join(npmRoot, "@amxv", "icloud-cli");
    for (const relative of [
      "man/icloud.1",
      "completions/_icloud",
      "completions/icloud.bash",
      "completions/icloud.fish",
      "completions/icloud.ps1",
      "schemas/v1/message.schema.json"
    ]) {
      assert.ok(fs.statSync(path.join(packageRoot, relative)).isFile(), `packed ${relative}`);
    }
    console.log(`npm install smoke passed on ${process.platform}/${process.arch}`);
  } finally {
    if (tarball) fs.rmSync(tarball, { force: true });
    fs.rmSync(prefix, { recursive: true, force: true });
  }
}

if (require.main === module) {
  try {
    main();
  } catch (error) {
    console.error(error.stack || error.message);
    process.exit(1);
  }
}
