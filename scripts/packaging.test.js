const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const pkg = require("../package.json");
const project = require("../project.config.json");
const { createInstallPlan } = require("./postinstall");

test("npm exposes one public icloud command", () => {
  assert.deepEqual(pkg.bin, { icloud: "bin/icloud.js" });
  assert.equal(pkg.config.cliName, "icloud");
  assert.equal(pkg.config.cliBinaryName, "icloud");
  assert.ok(fs.existsSync(path.join(__dirname, "..", pkg.bin.icloud)));
  assert.ok(pkg.files.includes("go.sum"));
  assert.ok(pkg.files.includes("schemas"));
  assert.ok(fs.existsSync(path.join(__dirname, "..", "schemas", "v1", "message.schema.json")));
});

test("project identity and fallback source use cmd/icloud", () => {
  assert.equal(project.cliName, "icloud");
  assert.equal(project.binaryName, "icloud");
  assert.equal(project.paths.command, "cmd/icloud");
  assert.equal(project.paths.wrapper, "bin/icloud.js");

  const linux = createInstallPlan("linux", "x64");
  assert.equal(linux.assetName, "icloud_linux_amd64");
  assert.equal(path.basename(linux.destination), "icloud-bin");
  assert.match(linux.sourcePath, /cmd[\\/]icloud[\\/]main\.go$/);

  const windows = createInstallPlan("win32", "arm64");
  assert.equal(windows.assetName, "icloud_windows_arm64.exe");
  assert.equal(path.basename(windows.destination), "icloud.exe");
});

test("release workflow preserves every native asset target", () => {
  const workflow = fs.readFileSync(path.join(__dirname, "..", ".github", "workflows", "release.yml"), "utf8");
  assert.match(workflow, /CLI_BINARY: "icloud"/);
  assert.match(workflow, /CLI_ENTRYPOINT: "icloud"/);
  for (const target of [
    "darwin amd64",
    "darwin arm64",
    "linux amd64",
    "linux arm64",
    "windows amd64",
    "windows arm64"
  ]) {
    const [goos, goarch] = target.split(" ");
    assert.match(workflow, new RegExp(`goos: ${goos}\\n\\s+goarch: ${goarch}`));
  }
});

test("Makefile release output keeps both OS and architecture segments", () => {
  const makefile = fs.readFileSync(path.join(__dirname, "..", "Makefile"), "utf8");
  assert.match(makefile, /\$\$\{GOOS\}_\$\$\{GOARCH\}\$\$\{EXT\}/);
});
