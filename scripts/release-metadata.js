#!/usr/bin/env node

const crypto = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");

const targets = [
  ["darwin", "amd64"],
  ["darwin", "arm64"],
  ["linux", "amd64"],
  ["linux", "arm64"],
  ["windows", "amd64"],
  ["windows", "arm64"]
];

function assetName(goos, goarch) {
  return `icloud_${goos}_${goarch}${goos === "windows" ? ".exe" : ""}`;
}

function sha256File(filePath) {
  return crypto.createHash("sha256").update(fs.readFileSync(filePath)).digest("hex");
}

function buildReleaseMetadata(distDir, version) {
  const resolvedVersion = `${version || ""}`.trim();
  if (!resolvedVersion) {
    throw new Error("release version is required");
  }
  const expectedNames = targets.map(([goos, goarch]) => assetName(goos, goarch)).sort();
  const presentNames = fs.readdirSync(distDir)
    .filter((name) => !["checksums.txt", "release-metadata.json"].includes(name))
    .filter((name) => fs.statSync(path.join(distDir, name)).isFile())
    .sort();
  if (JSON.stringify(presentNames) !== JSON.stringify(expectedNames)) {
    throw new Error(`release directory must contain exactly: ${expectedNames.join(", ")}`);
  }
  const assets = targets.map(([goos, goarch]) => {
    const name = assetName(goos, goarch);
    const filePath = path.join(distDir, name);
    const stat = fs.statSync(filePath);
    if (!stat.isFile()) {
      throw new Error(`release asset is not a file: ${name}`);
    }
    return {
      name,
      goos,
      goarch,
      bytes: stat.size,
      sha256: sha256File(filePath)
    };
  });
  return {
    schema_version: 1,
    product: "icloud",
    version: resolvedVersion,
    npm_package: "@amxv/icloud-cli",
    docs: "https://icloud.ashray.xyz",
    assets
  };
}

function writeReleaseMetadata(distDir, version) {
  const metadata = buildReleaseMetadata(distDir, version);
  const checksums = metadata.assets.map((asset) => `${asset.sha256}  ${asset.name}`).join("\n") + "\n";
  fs.writeFileSync(path.join(distDir, "checksums.txt"), checksums);
  fs.writeFileSync(path.join(distDir, "release-metadata.json"), `${JSON.stringify(metadata, null, 2)}\n`);
  return metadata;
}

function parseArgs(argv) {
  let version = "";
  let distDir = "dist";
  for (let index = 0; index < argv.length; index += 1) {
    switch (argv[index]) {
      case "--version":
        version = argv[++index] || "";
        break;
      case "--dist":
        distDir = argv[++index] || "";
        break;
      default:
        throw new Error(`unknown argument: ${argv[index]}`);
    }
  }
  return { version, distDir };
}

if (require.main === module) {
  try {
    const { version, distDir } = parseArgs(process.argv.slice(2));
    writeReleaseMetadata(distDir, version);
  } catch (error) {
    console.error(error.message);
    process.exit(1);
  }
}

module.exports = {
  assetName,
  buildReleaseMetadata,
  writeReleaseMetadata
};
