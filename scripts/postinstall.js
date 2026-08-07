#!/usr/bin/env node

const fs = require("node:fs");
const path = require("node:path");
const https = require("node:https");
const crypto = require("node:crypto");
const { spawnSync } = require("node:child_process");

const pkg = require("../package.json");
const cliName = pkg.config?.cliName || Object.keys(pkg.bin || {})[0] || "icloud";
const binaryBaseName = pkg.config?.cliBinaryName || cliName;

const repoURL = pkg.repository?.url || "";
const repoMatch = repoURL.match(/github\.com[:/](.+?)\/(.+?)(?:\.git)?$/);
if (!repoMatch) {
  console.error("package.json repository.url must point to a GitHub repo.");
  process.exit(1);
}

const repoOwner = repoMatch[1];
const repoName = repoMatch[2];

const goosMap = {
  darwin: "darwin",
  linux: "linux",
  win32: "windows"
};

const goarchMap = {
  x64: "amd64",
  arm64: "arm64"
};

const rootDir = path.resolve(__dirname, "..");
const binDir = path.join(rootDir, "bin");

function buildLdflags(version) {
  const resolvedVersion = `${version || ""}`.trim() || "dev";
  const modulePath = pkg.config?.goModule || "github.com/amxv/icloud-cli";
  return `-s -w -X ${modulePath}/internal/buildinfo.Version=${resolvedVersion}`;
}

function createInstallPlan(platform = process.platform, architecture = process.arch) {
  const resolvedGoos = goosMap[platform];
  const resolvedGoarch = goarchMap[architecture];
  const installedBinaryName = platform === "win32" ? `${binaryBaseName}.exe` : `${binaryBaseName}-bin`;
  const destinationPath = path.join(binDir, installedBinaryName);
  const assetName = resolvedGoos && resolvedGoarch
    ? `${binaryBaseName}_${resolvedGoos}_${resolvedGoarch}${resolvedGoos === "windows" ? ".exe" : ""}`
    : "";

  return {
    goos: resolvedGoos,
    goarch: resolvedGoarch,
    binaryName: installedBinaryName,
    destination: destinationPath,
    assetName,
    sourcePath: path.join(rootDir, "cmd", cliName, "main.go")
  };
}

async function main() {
  const plan = createInstallPlan();
  if (!plan.goos || !plan.goarch) {
    console.warn(`Unsupported platform/arch: ${process.platform}/${process.arch}`);
    fallbackBuildOrExit(plan);
    return;
  }

  fs.mkdirSync(binDir, { recursive: true });

  const version = pkg.version;
  const assetURL = `https://github.com/${repoOwner}/${repoName}/releases/download/v${version}/${plan.assetName}`;
  const checksumsURL = `https://github.com/${repoOwner}/${repoName}/releases/download/v${version}/checksums.txt`;
  const temporaryPath = plan.goos === "windows"
    ? `${plan.destination}.download-${process.pid}.exe`
    : `${plan.destination}.download-${process.pid}`;

  try {
    if (process.env.ICLOUD_CLI_FORCE_BUILD === "1") {
      throw new Error("forced local build");
    }
    fs.rmSync(temporaryPath, { force: true });
    await downloadToFile(assetURL, temporaryPath);
    const checksums = parseChecksums(await downloadText(checksumsURL));
    const expected = checksums.get(plan.assetName);
    if (!expected) {
      throw new Error(`release checksum is missing for ${plan.assetName}`);
    }
    const actual = await sha256File(temporaryPath);
    if (actual !== expected) {
      throw new Error(`release checksum mismatch for ${plan.assetName}`);
    }
    if (plan.goos !== "windows") {
      fs.chmodSync(temporaryPath, 0o755);
    }
    verifyBinaryVersion(temporaryPath, version);
    fs.rmSync(plan.destination, { force: true });
    fs.renameSync(temporaryPath, plan.destination);
    console.log(`Installed verified ${cliName} binary from ${assetURL}`);
  } catch (err) {
    fs.rmSync(temporaryPath, { force: true });
    console.warn(`Failed to download prebuilt binary: ${err.message}`);
    fallbackBuildOrExit(plan);
  }
}

function fallbackBuildOrExit(plan = createInstallPlan()) {
  const hasGo = spawnSync("go", ["version"], { stdio: "ignore" }).status === 0;
  const hasSource = fs.existsSync(plan.sourcePath);

  if (!hasGo || !hasSource) {
    console.error("Unable to install CLI binary. Missing release asset and local Go build fallback.");
    process.exit(1);
  }

  const result = spawnSync(
    "go",
    ["build", "-trimpath", `-ldflags=${buildLdflags(pkg.version)}`, "-o", plan.destination, `./cmd/${cliName}`],
    { cwd: rootDir, stdio: "inherit" }
  );

  if (result.status !== 0) {
    process.exit(result.status || 1);
  }

  if (plan.goos !== "windows") {
    fs.chmodSync(plan.destination, 0o755);
  }

  verifyBinaryVersion(plan.destination, pkg.version);

  console.log("Installed CLI binary by building from source.");
}

function verifyBinaryVersion(binaryPath, expectedVersion) {
  const result = spawnSync(binaryPath, ["version"], {
    encoding: "utf8",
    timeout: 10000,
    windowsHide: true
  });
  if (result.error) {
    throw new Error(`installed binary version check failed: ${result.error.message}`);
  }
  if (result.status !== 0) {
    throw new Error(`installed binary version check exited ${result.status}`);
  }
  const expected = `version\n${expectedVersion}\n`;
  if (result.stdout !== expected) {
    throw new Error(`installed binary version does not match package ${expectedVersion}`);
  }
}

function downloadToFile(url, destinationPath, redirectsRemaining = 5) {
  return new Promise((resolve, reject) => {
    const request = https.get(url, (response) => {
      if (response.statusCode >= 300 && response.statusCode < 400 && response.headers.location) {
        response.resume();
        if (redirectsRemaining <= 0) {
          reject(new Error("too many release download redirects"));
          return;
        }
        downloadToFile(response.headers.location, destinationPath, redirectsRemaining - 1).then(resolve).catch(reject);
        return;
      }

      if (response.statusCode !== 200) {
        response.resume();
        reject(new Error(`HTTP ${response.statusCode}`));
        return;
      }

      const file = fs.createWriteStream(destinationPath);
      response.pipe(file);
      response.on("error", (err) => file.destroy(err));

      file.on("finish", () => {
        file.close((err) => {
          if (err) {
            reject(err);
            return;
          }
          resolve();
        });
      });

      file.on("error", (err) => {
        fs.rmSync(destinationPath, { force: true });
        reject(err);
      });
    });

    request.on("error", reject);
  });
}

function downloadText(url, redirectsRemaining = 5) {
  return new Promise((resolve, reject) => {
    const request = https.get(url, (response) => {
      if (response.statusCode >= 300 && response.statusCode < 400 && response.headers.location) {
        response.resume();
        if (redirectsRemaining <= 0) {
          reject(new Error("too many release checksum redirects"));
          return;
        }
        downloadText(response.headers.location, redirectsRemaining - 1).then(resolve).catch(reject);
        return;
      }
      if (response.statusCode !== 200) {
        response.resume();
        reject(new Error(`HTTP ${response.statusCode}`));
        return;
      }
      const chunks = [];
      let size = 0;
      response.on("data", (chunk) => {
        size += chunk.length;
        if (size > 1024 * 1024) {
          request.destroy(new Error("release checksum response is too large"));
          return;
        }
        chunks.push(chunk);
      });
      response.on("end", () => resolve(Buffer.concat(chunks).toString("utf8")));
      response.on("error", reject);
    });
    request.on("error", reject);
  });
}

function parseChecksums(value) {
  const result = new Map();
  for (const line of `${value || ""}`.split(/\r?\n/)) {
    if (!line.trim()) continue;
    const match = line.match(/^([0-9a-f]{64})  ([A-Za-z0-9._-]+)$/);
    if (!match) {
      throw new Error("release checksum file is invalid");
    }
    if (result.has(match[2])) {
      throw new Error(`duplicate release checksum for ${match[2]}`);
    }
    result.set(match[2], match[1]);
  }
  return result;
}

function sha256File(filePath) {
  return new Promise((resolve, reject) => {
    const hash = crypto.createHash("sha256");
    const stream = fs.createReadStream(filePath);
    stream.on("data", (chunk) => hash.update(chunk));
    stream.on("error", reject);
    stream.on("end", () => resolve(hash.digest("hex")));
  });
}

if (require.main === module) {
  main().catch((err) => {
    console.error(err.message);
    process.exit(1);
  });
}

module.exports = {
  buildLdflags,
  createInstallPlan,
  fallbackBuildOrExit,
  parseChecksums,
  sha256File,
  verifyBinaryVersion
};
