#!/usr/bin/env node

const fs = require("node:fs");
const path = require("node:path");
const https = require("node:https");
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

  try {
    if (process.env.ICLOUD_CLI_FORCE_BUILD === "1") {
      throw new Error("forced local build");
    }
    await downloadToFile(assetURL, plan.destination);
    if (plan.goos !== "windows") {
      fs.chmodSync(plan.destination, 0o755);
    }
    console.log(`Installed ${cliName} binary from ${assetURL}`);
  } catch (err) {
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

  console.log("Installed CLI binary by building from source.");
}

function downloadToFile(url, destinationPath) {
  return new Promise((resolve, reject) => {
    const request = https.get(url, (response) => {
      if (response.statusCode >= 300 && response.statusCode < 400 && response.headers.location) {
        response.resume();
        downloadToFile(response.headers.location, destinationPath).then(resolve).catch(reject);
        return;
      }

      if (response.statusCode !== 200) {
        response.resume();
        reject(new Error(`HTTP ${response.statusCode}`));
        return;
      }

      const file = fs.createWriteStream(destinationPath);
      response.pipe(file);

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

if (require.main === module) {
  main().catch((err) => {
    console.error(err.message);
    process.exit(1);
  });
}

module.exports = {
  buildLdflags,
  createInstallPlan,
  fallbackBuildOrExit
};
