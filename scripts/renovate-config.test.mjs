import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

const ROOT = path.resolve(import.meta.dirname, "..");

function readConfig() {
  return JSON.parse(fs.readFileSync(path.join(ROOT, "renovate.json"), "utf8"));
}

function readPackage() {
  return JSON.parse(fs.readFileSync(path.join(ROOT, "package.json"), "utf8"));
}

function readLock() {
  return JSON.parse(fs.readFileSync(path.join(ROOT, "package-lock.json"), "utf8"));
}

function assertScheduledLockMaintenanceDisabled(config) {
  assert.deepEqual(config.lockFileMaintenance, { enabled: false });
}

function assertPackageVersion(lock, name, version) {
  assert.ok(lock.packages[name], `${name} is missing`);
  assert.equal(lock.packages[name].version, version, name);
}

function versionParts(version) {
  return version.split(".").map(Number);
}

function assertAtLeast(version, floor, label) {
  const [actual, minimum] = [versionParts(version), versionParts(floor)];
  const order = actual.reduce((diff, part, index) => diff || part - minimum[index], 0);
  assert.ok(order >= 0, `${label} ${version} is below the declared floor ${floor}`);
}

// sharp's own optionalDependencies name the platform package versions it was
// published against, so the lock is checked against that, not a literal.
function assertSharpPackagesCurrent(lock, packageJson) {
  const sharp = lock.packages["node_modules/sharp"];
  assert.ok(sharp, "node_modules/sharp is missing");
  assertAtLeast(sharp.version, packageJson.overrides.next.sharp.replace(/^[\^~]/u, ""), "sharp");
  const sharpPackages = Object.entries(lock.packages).filter(
    ([name]) =>
      name === "node_modules/sharp" ||
      name.endsWith("/node_modules/sharp") ||
      name.includes("/@img/sharp-"),
  );
  assert.ok(sharpPackages.length > 0);
  for (const [name, packageData] of sharpPackages) {
    const platform = name.slice(name.indexOf("@img/"));
    const expected = name.includes("sharp-libvips")
      ? sharp.optionalDependencies[platform]
      : sharp.version;
    assert.ok(expected, `${name} is not an optional dependency of sharp`);
    assert.equal(packageData.version, expected, name);
  }
}

function assertSharpPlatformPackage(lock, name) {
  const sharp = lock.packages["node_modules/sharp"];
  assertPackageVersion(lock, name, sharp.optionalDependencies[name.slice(name.indexOf("@img/"))]);
}

test("Renovate disables scheduled lock maintenance while retaining dependency updates", () => {
  const config = readConfig();
  assertScheduledLockMaintenanceDisabled(config);

  const oldConfig = { ...config };
  delete oldConfig.lockFileMaintenance;
  assert.throws(() => assertScheduledLockMaintenanceDisabled(oldConfig));
  assert.ok(config.packageRules.length > 0);
});

test("the refreshed Next dependency keeps Sharp and every optional platform package current", () => {
  const packageJson = readPackage();
  const lock = readLock();
  assert.match(packageJson.overrides.next.sharp, /^\^\d+\.\d+\.\d+$/u);
  assertSharpPackagesCurrent(lock, packageJson);

  for (const name of [
    "node_modules/@img/sharp-darwin-arm64",
    "node_modules/@img/sharp-linux-x64",
    "node_modules/@img/sharp-libvips-darwin-arm64",
    "node_modules/@img/sharp-libvips-linux-x64",
  ]) {
    assertSharpPlatformPackage(lock, name);
  }
  const lightningcss = lock.packages["node_modules/lightningcss"].version;
  for (const name of [
    "node_modules/lightningcss-darwin-arm64",
    "node_modules/lightningcss-linux-x64-gnu",
  ]) {
    assertPackageVersion(lock, name, lightningcss);
  }

  const lockWithoutDarwin = { packages: { ...lock.packages } };
  delete lockWithoutDarwin.packages["node_modules/@img/sharp-darwin-arm64"];
  assert.throws(() =>
    assertSharpPlatformPackage(lockWithoutDarwin, "node_modules/@img/sharp-darwin-arm64"),
  );

  const lockWithNestedStaleSharp = { packages: { ...lock.packages } };
  lockWithNestedStaleSharp.packages["node_modules/next/node_modules/sharp"] = { version: "0.0.1" };
  assert.throws(() => assertSharpPackagesCurrent(lockWithNestedStaleSharp, packageJson));

  const belowFloor = { packages: { ...lock.packages } };
  belowFloor.packages["node_modules/sharp"] = {
    ...lock.packages["node_modules/sharp"],
    version: "0.0.1",
  };
  assert.throws(
    () => assertSharpPackagesCurrent(belowFloor, packageJson),
    /below the declared floor/u,
  );

  const oldConfig = {
    ...packageJson,
    overrides: {
      ...packageJson.overrides,
      next: { ...packageJson.overrides.next, sharp: "^0.35.3" },
    },
  };
  assert.notEqual(oldConfig.overrides.next.sharp, packageJson.overrides.next.sharp);
});
