const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const pkg = require("../package.json");
const project = require("../project.config.json");
const { createInstallPlan, parseChecksums } = require("./postinstall");
const { assetName, writeReleaseMetadata } = require("./release-metadata");

test("npm exposes one public icloud command", () => {
  assert.deepEqual(pkg.bin, { icloud: "bin/icloud.js" });
  assert.equal(pkg.config.cliName, "icloud");
  assert.equal(pkg.config.cliBinaryName, "icloud");
  assert.ok(fs.existsSync(path.join(__dirname, "..", pkg.bin.icloud)));
  assert.ok(pkg.files.includes("go.sum"));
  assert.ok(pkg.files.includes("schemas"));
  assert.ok(pkg.files.includes("man"));
  assert.ok(pkg.files.includes("completions"));
  assert.deepEqual(pkg.man, ["man/icloud.1"]);
  assert.ok(fs.existsSync(path.join(__dirname, "..", "schemas", "v1", "message.schema.json")));
  assert.ok(fs.existsSync(path.join(__dirname, "..", "man", "icloud.1")));
  for (const completion of ["_icloud", "icloud.bash", "icloud.fish", "icloud.ps1"]) {
    assert.ok(fs.existsSync(path.join(__dirname, "..", "completions", completion)));
  }
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
  assert.match(workflow, /release-metadata\.js --version/);
  assert.match(workflow, /make references VERSION=/);
  assert.match(workflow, /Validate release tag/);
  assert.match(workflow, /Verify runnable release version/);
  assert.match(workflow, /package-install:/);
  assert.match(workflow, /files: dist\/\*/);
});

test("Makefile release output keeps both OS and architecture segments", () => {
  const makefile = fs.readFileSync(path.join(__dirname, "..", "Makefile"), "utf8");
  assert.match(makefile, /\$\$\{GOOS\}_\$\$\{GOARCH\}\$\$\{EXT\}/);
});

test("release metadata covers exactly the six native assets with matching checksums", () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "icloud-release-metadata-"));
  try {
    const targets = [
      ["darwin", "amd64"],
      ["darwin", "arm64"],
      ["linux", "amd64"],
      ["linux", "arm64"],
      ["windows", "amd64"],
      ["windows", "arm64"]
    ];
    for (const [goos, goarch] of targets) {
      fs.writeFileSync(path.join(directory, assetName(goos, goarch)), `${goos}/${goarch}\n`);
    }
    const metadata = writeReleaseMetadata(directory, "1.2.3");
    assert.equal(metadata.schema_version, 1);
    assert.equal(metadata.product, "icloud");
    assert.equal(metadata.version, "1.2.3");
    assert.equal(metadata.assets.length, 6);
    const checksums = parseChecksums(fs.readFileSync(path.join(directory, "checksums.txt"), "utf8"));
    assert.equal(checksums.size, 6);
    for (const asset of metadata.assets) {
      const file = fs.readFileSync(path.join(directory, asset.name));
      const digest = crypto.createHash("sha256").update(file).digest("hex");
      assert.equal(asset.sha256, digest);
      assert.equal(checksums.get(asset.name), digest);
    }
    const onDisk = JSON.parse(fs.readFileSync(path.join(directory, "release-metadata.json"), "utf8"));
    assert.deepEqual(onDisk, metadata);
    assert.deepEqual(writeReleaseMetadata(directory, "1.2.3"), metadata);
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});

test("checksum parser rejects malformed or duplicate release entries", () => {
  assert.throws(() => parseChecksums("not-a-checksum  icloud_linux_amd64\n"), /invalid/);
  const digest = "a".repeat(64);
  assert.throws(
    () => parseChecksums(`${digest}  icloud_linux_amd64\n${digest}  icloud_linux_amd64\n`),
    /duplicate/
  );
});
